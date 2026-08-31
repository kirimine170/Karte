package ephyoutbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxProposalBytes = 2 * 1024 * 1024

type Store struct {
	dataRoot        string
	outboxRoot      string
	pendingDir      string
	acceptedDir     string
	rejectedDir     string
	receiptsDir     string
	transactionsDir string
}

func NewStore(dataDir string) (*Store, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve KARTE_DATA_DIR: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve KARTE_DATA_DIR symlinks: %w", err)
	}
	store := &Store{
		dataRoot:        realRoot,
		outboxRoot:      filepath.Join(realRoot, ".mdsys", "ephy", "outbox"),
		pendingDir:      filepath.Join(realRoot, ".mdsys", "ephy", "outbox", "pending"),
		acceptedDir:     filepath.Join(realRoot, ".mdsys", "ephy", "outbox", "accepted"),
		rejectedDir:     filepath.Join(realRoot, ".mdsys", "ephy", "outbox", "rejected"),
		receiptsDir:     filepath.Join(realRoot, ".mdsys", "ephy", "outbox", "receipts"),
		transactionsDir: filepath.Join(realRoot, ".mdsys", "ephy", "outbox", "transactions"),
	}
	for _, candidate := range []string{store.outboxRoot, store.pendingDir, store.acceptedDir, store.rejectedDir, store.receiptsDir, store.transactionsDir} {
		if err := store.assertNoSymlinkEscape(candidate); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (store *Store) EnsureLayout() error {
	for _, dir := range []string{store.pendingDir, store.acceptedDir, store.rejectedDir, store.receiptsDir, store.transactionsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create outbox directory: %w", err)
		}
		if err := store.assertExistingWithinDataRoot(dir); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) ListPending() ([]Proposal, []ProposalError, error) {
	if _, err := os.Stat(store.pendingDir); errors.Is(err, os.ErrNotExist) {
		return []Proposal{}, []ProposalError{}, nil
	} else if err != nil {
		return nil, nil, err
	}
	if err := store.assertExistingWithinDataRoot(store.pendingDir); err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(store.pendingDir)
	if err != nil {
		return nil, nil, fmt.Errorf("list pending proposals: %w", err)
	}
	var proposals []Proposal
	var proposalErrors []ProposalError
	seen := map[string]string{}
	duplicates := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".json" {
			continue
		}
		filePath := filepath.Join(store.pendingDir, name)
		proposal, readErr := store.readProposalFile(filePath)
		if readErr != nil {
			proposalErrors = append(proposalErrors, ProposalError{Filename: name, Code: "invalid_proposal", Message: readErr.Error()})
			continue
		}
		if previous, ok := seen[proposal.CandidateID]; ok {
			duplicates[proposal.CandidateID] = true
			proposalErrors = append(proposalErrors, ProposalError{Filename: name, CandidateID: proposal.CandidateID, Code: "duplicate_candidate_id", Message: fmt.Sprintf("candidate_id also appears in %s", previous)})
			continue
		}
		seen[proposal.CandidateID] = name
		if strings.TrimSuffix(name, ".json") != proposal.CandidateID {
			proposalErrors = append(proposalErrors, ProposalError{Filename: name, CandidateID: proposal.CandidateID, Code: "filename_mismatch", Message: "proposal filename must equal candidate_id.json"})
			continue
		}
		proposals = append(proposals, proposal)
	}
	if len(duplicates) > 0 {
		filtered := proposals[:0]
		for _, proposal := range proposals {
			if duplicates[proposal.CandidateID] {
				proposalErrors = append(proposalErrors, ProposalError{Filename: seen[proposal.CandidateID], CandidateID: proposal.CandidateID, Code: "duplicate_candidate_id", Message: "candidate_id appears more than once"})
				continue
			}
			filtered = append(filtered, proposal)
		}
		proposals = filtered
	}
	sort.Slice(proposals, func(i, j int) bool { return proposals[i].CreatedAt < proposals[j].CreatedAt })
	sort.Slice(proposalErrors, func(i, j int) bool { return proposalErrors[i].Filename < proposalErrors[j].Filename })
	return proposals, proposalErrors, nil
}

func (store *Store) ReadPending(candidateID string) (Proposal, error) {
	if !candidateIDPattern.MatchString(candidateID) {
		return Proposal{}, fmt.Errorf("invalid candidate_id")
	}
	return store.readProposalFile(filepath.Join(store.pendingDir, candidateID+".json"))
}

func (store *Store) ReadReceipt(candidateID string) (*Receipt, error) {
	if !candidateIDPattern.MatchString(candidateID) {
		return nil, fmt.Errorf("invalid candidate_id")
	}
	filePath := filepath.Join(store.receiptsDir, candidateID+".json")
	info, err := os.Lstat(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect receipt: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("receipt must be a regular file")
	}
	if err := store.assertExistingWithinDataRoot(filePath); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read receipt: %w", err)
	}
	var receipt Receipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return nil, fmt.Errorf("decode receipt: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("decode receipt: trailing JSON value")
	}
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (store *Store) WriteReceipt(receipt Receipt) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	if err := store.EnsureLayout(); err != nil {
		return err
	}
	destination := filepath.Join(store.receiptsDir, receipt.CandidateID+".json")
	existing, err := store.ReadReceipt(receipt.CandidateID)
	if err != nil {
		return err
	}
	if existing != nil {
		left, _ := json.Marshal(existing)
		right, _ := json.Marshal(receipt)
		if bytes.Equal(left, right) {
			return nil
		}
		return fmt.Errorf("receipt already exists with different content")
	}
	return atomicWriteJSON(destination, receipt)
}

func (store *Store) MoveProposal(candidateID, result string) error {
	var destinationDir string
	switch result {
	case "accepted":
		destinationDir = store.acceptedDir
	case "rejected":
		destinationDir = store.rejectedDir
	default:
		return nil
	}
	if err := store.EnsureLayout(); err != nil {
		return err
	}
	source := filepath.Join(store.pendingDir, candidateID+".json")
	destination := filepath.Join(destinationDir, candidateID+".json")
	if destinationInfo, err := os.Lstat(destination); err == nil {
		if destinationInfo.Mode()&os.ModeSymlink != 0 || !destinationInfo.Mode().IsRegular() {
			return fmt.Errorf("processed proposal must be a regular file")
		}
		if _, sourceErr := os.Stat(source); errors.Is(sourceErr, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("processed proposal exists while pending proposal remains")
	}
	if err := store.assertExistingWithinDataRoot(source); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("archive proposal: %w", err)
	}
	if err := syncDirectory(destinationDir); err != nil {
		return err
	}
	return syncDirectory(store.pendingDir)
}

func (store *Store) ReadTransaction(candidateID string) (*Transaction, error) {
	filePath := filepath.Join(store.transactionsDir, candidateID+".json")
	info, err := os.Lstat(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("transaction must be a regular file")
	}
	if err := store.assertExistingWithinDataRoot(filePath); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var transaction Transaction
	if err := json.Unmarshal(data, &transaction); err != nil {
		return nil, fmt.Errorf("decode transaction: %w", err)
	}
	if transaction.CandidateID != candidateID {
		return nil, fmt.Errorf("transaction candidate_id does not match filename")
	}
	if err := transaction.Validate(); err != nil {
		return nil, err
	}
	return &transaction, nil
}

func (store *Store) WriteTransaction(transaction Transaction) error {
	if err := transaction.Validate(); err != nil {
		return err
	}
	if err := store.EnsureLayout(); err != nil {
		return err
	}
	return atomicWriteJSON(filepath.Join(store.transactionsDir, transaction.CandidateID+".json"), transaction)
}

func (store *Store) RemoveTransaction(candidateID string) error {
	filePath := filepath.Join(store.transactionsDir, candidateID+".json")
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(store.transactionsDir)
}

func (store *Store) readProposalFile(filePath string) (Proposal, error) {
	if err := store.assertExistingWithinDataRoot(filePath); err != nil {
		return Proposal{}, err
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return Proposal{}, fmt.Errorf("inspect proposal: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Proposal{}, fmt.Errorf("proposal must be a regular file")
	}
	if info.Size() > maxProposalBytes {
		return Proposal{}, fmt.Errorf("proposal exceeds size limit")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return Proposal{}, fmt.Errorf("open proposal: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxProposalBytes+1))
	if err != nil {
		return Proposal{}, fmt.Errorf("read proposal: %w", err)
	}
	return DecodeProposal(data)
}

func (store *Store) assertWithinDataRoot(candidate string) error {
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(store.dataRoot, abs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("outbox path escapes KARTE_DATA_DIR")
	}
	return nil
}

func (store *Store) assertNoSymlinkEscape(candidate string) error {
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return err
	}
	existing := abs
	for {
		if _, statErr := os.Lstat(existing); statErr == nil {
			break
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return fmt.Errorf("cannot resolve outbox path ancestor")
		}
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	remainder, err := filepath.Rel(existing, abs)
	if err != nil {
		return err
	}
	return store.assertWithinDataRoot(filepath.Join(resolved, remainder))
}

func (store *Store) assertExistingWithinDataRoot(candidate string) error {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return fmt.Errorf("resolve outbox path: %w", err)
	}
	return store.assertWithinDataRoot(resolved)
}

func atomicWriteJSON(destination string, value any) error {
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(destination)+".*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, destination); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}
