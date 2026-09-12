package git

import (
	"fmt"
	"karte/internal/canonical"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// VCS wraps Git operations
type VCS struct {
	repo   *git.Repository
	path   string
	ctx    interface{}
	logger func(msg string)
}

// NewVCS creates a new VCS instance
func NewVCS(ctx interface{}, repoPath string, logger func(msg string)) (*VCS, error) {
	vcs := &VCS{
		path:   repoPath,
		ctx:    ctx,
		logger: logger,
	}

	// Try to open existing repository
	repo, err := git.PlainOpen(repoPath)
	if err == git.ErrRepositoryNotExists {
		// Initialize new repository
		repo, err = git.PlainInit(repoPath, false)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize git repository: %v", err)
		}
		vcs.logger("Initialized new git repository")
	} else if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %v", err)
	}

	vcs.repo = repo

	// Initialize git config if needed
	if err := vcs.ensureGitConfig(); err != nil {
		vcs.logger(fmt.Sprintf("Warning: failed to set git config: %v", err))
	}

	// Ensure .gitignore exists
	if err := vcs.ensureGitignore(); err != nil {
		vcs.logger(fmt.Sprintf("Warning: failed to create .gitignore: %v", err))
	}

	return vcs, nil
}

// ensureGitConfig sets up basic git config
func (v *VCS) ensureGitConfig() error {
	cfg, err := v.repo.Config()
	if err != nil {
		return err
	}

	// Set user config if not set
	if cfg.User.Name == "" {
		cfg.User.Name = "Karte User"
	}
	if cfg.User.Email == "" {
		cfg.User.Email = "karte@localhost"
	}

	return v.repo.SetConfig(cfg)
}

// ensureGitignore creates .gitignore if it doesn't exist
func (v *VCS) ensureGitignore() error {
	return canonical.WithWriter(v.path, func(w *canonical.Writer) error {
		data, err := w.Read(".gitignore")
		if os.IsNotExist(err) {
			data = []byte("# Karte generated files\npublic/\nlog/\n.mdsys/\n*.log\nbuild/\n.DS_Store\nThumbs.db\n.backups/\n")
		} else if err != nil {
			return err
		}
		text := string(data)
		rules := []string{"content/**/ephy-v2-*.md", ".mdsys/ephy/outbox/v2/", ".mdsys/ephy/records/v2/", ".mdsys/context/v2/", ".mdsys/write.lock"}
		lines := map[string]bool{}
		for _, line := range strings.Split(text, "\n") {
			lines[line] = true
		}
		for _, rule := range rules {
			if !lines[rule] {
				if !strings.HasSuffix(text, "\n") {
					text += "\n"
				}
				text += rule + "\n"
			}
		}
		existing, err := w.CurrentHash(".gitignore")
		if err != nil {
			return err
		}
		if existing != nil && *existing == canonical.Hash([]byte(text)) {
			return nil
		}
		return w.WriteCAS(".gitignore", existing, []byte(text), 0644)
	})
}

// CommitFile commits a single file
func (v *VCS) CommitFile(relativePath, message string) error {
	if v.repo == nil {
		return fmt.Errorf("repository not initialized")
	}

	worktree, err := v.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %v", err)
	}

	if err := v.CheckAutomaticCommit(relativePath); err != nil {
		return err
	}
	// Add file to staging
	if _, err := worktree.Add(relativePath); err != nil {
		return fmt.Errorf("failed to stage file: %v", err)
	}

	// Check if there are changes
	status, err := worktree.Status()
	if err != nil {
		return fmt.Errorf("failed to get status: %v", err)
	}

	// Only commit if there are changes
	fileStatus, hasChanges := status[relativePath]
	if !hasChanges || fileStatus.Staging == git.Unmodified {
		return nil // No changes to commit
	}

	// Commit
	commitHash, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Karte User",
			Email: "karte@localhost",
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to commit: %v", err)
	}

	v.logger(fmt.Sprintf("Committed file: %s (commit: %s)", relativePath, commitHash.String()[:7]))
	return nil
}

// GetFileHash returns the hash of a file at a specific commit
func (v *VCS) GetFileHash(relativePath string, commitHash string) (string, error) {
	if v.repo == nil {
		return "", fmt.Errorf("repository not initialized")
	}

	var commit *object.Commit
	var err error

	if commitHash == "" {
		// Get HEAD commit
		ref, err := v.repo.Head()
		if err != nil {
			return "", fmt.Errorf("failed to get HEAD: %v", err)
		}
		commit, err = v.repo.CommitObject(ref.Hash())
		if err != nil {
			return "", fmt.Errorf("failed to get commit: %v", err)
		}
	} else {
		hash := plumbing.NewHash(commitHash)
		commit, err = v.repo.CommitObject(hash)
		if err != nil {
			return "", fmt.Errorf("failed to get commit: %v", err)
		}
	}

	// Get file from commit
	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("failed to get tree: %v", err)
	}

	file, err := tree.File(relativePath)
	if err != nil {
		return "", fmt.Errorf("file not found in commit: %v", err)
	}

	content, err := file.Contents()
	if err != nil {
		return "", fmt.Errorf("failed to read file contents: %v", err)
	}

	return CalculateHash(content), nil
}

// GetStatus returns the git status
func (v *VCS) GetStatus() (map[string]git.StatusCode, error) {
	if v.repo == nil {
		return nil, fmt.Errorf("repository not initialized")
	}

	worktree, err := v.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %v", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %v", err)
	}

	result := make(map[string]git.StatusCode)
	for path, fileStatus := range status {
		result[path] = fileStatus.Staging
	}

	return result, nil
}

// Repository returns the underlying git repository
func (v *VCS) Repository() *git.Repository {
	return v.repo
}

// GetLatestCommitHash returns the hash of the latest commit
func (v *VCS) GetLatestCommitHash() (string, error) {
	if v.repo == nil {
		return "", fmt.Errorf("repository not initialized")
	}

	ref, err := v.repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD: %v", err)
	}

	return ref.Hash().String(), nil
}

// GetFileByContentHash finds and returns the file content that matches the given content hash
// It searches through the commit history to find the version of the file with the matching hash
func (v *VCS) GetFileByContentHash(relativePath, targetHash string) (string, error) {
	if v.repo == nil {
		return "", fmt.Errorf("repository not initialized")
	}

	if targetHash == "" {
		return "", fmt.Errorf("target hash is empty")
	}

	// Get HEAD commit to start searching from
	ref, err := v.repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD: %v", err)
	}

	// Iterate through commit history
	cIter, err := v.repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return "", fmt.Errorf("failed to get commit log: %v", err)
	}
	defer cIter.Close()

	// Search through commits (from newest to oldest)
	for {
		c, err := cIter.Next()
		if err != nil {
			// End of iteration or error
			break
		}

		// Get file from this commit
		tree, err := c.Tree()
		if err != nil {
			continue // Skip this commit if tree can't be read
		}

		file, err := tree.File(relativePath)
		if err != nil {
			continue // File doesn't exist in this commit, continue
		}

		content, err := file.Contents()
		if err != nil {
			continue // Can't read content, continue
		}

		// Calculate hash of this version
		contentHash := CalculateHash(content)
		if contentHash == targetHash {
			// Found matching version, return it
			return content, nil
		}
	}

	// No match found
	return "", fmt.Errorf("file version with hash %s not found in commit history", targetHash)
}

// CheckAutomaticCommit also protects the initial Add-all commit．
func (v *VCS) CheckAutomaticCommit(relativePath string) error {
	if v.repo == nil {
		return fmt.Errorf("repository not initialized")
	}
	worktree, err := v.repo.Worktree()
	if err != nil {
		return err
	}
	return canonical.WithWriter(v.path, func(w *canonical.Writer) error {
		paths, err := canonical.ManagedPaths(w)
		if err != nil {
			return err
		}
		protected := func(path string) bool {
			slash := filepath.ToSlash(path)
			if strings.HasPrefix(slash, ".mdsys/ephy/records/v2/") || strings.HasPrefix(slash, ".mdsys/ephy/outbox/v2/") || strings.HasPrefix(slash, ".mdsys/context/v2/") {
				return true
			}
			if canonical.IsRecordPath(path, nil, paths) {
				return true
			}
			if !strings.EqualFold(filepath.Ext(path), ".md") {
				return false
			}
			data, _ := w.ReadLimit(path, 1<<20)
			return canonical.IsRecordPath(path, data, paths)
		}
		if protected(relativePath) {
			return fmt.Errorf("v2 storage-only record cannot be committed automatically")
		}
		status, err := worktree.Status()
		if err != nil {
			return err
		}
		for path, state := range status {
			if state.Staging != git.Unmodified && state.Staging != git.Untracked && protected(path) {
				return fmt.Errorf("staged v2 storage-only data blocks automatic commit")
			}
		}
		return nil
	})
}
