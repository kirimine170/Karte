package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultMaxCSVBytes          = int64(64 * 1024 * 1024)
	csvMaxRecords               = 1_000_000
	csvMaxRecordBytes           = 1 * 1024 * 1024
	csvMaxCellBytes             = 256 * 1024
	csvMaxFields                = 1_024
	csvDefaultPageLimit         = 50
	csvMaxPageLimit             = 200
	csvMaxPageDecodedBytes      = 4 * 1024 * 1024
	csvPageJSONMetadataReserve  = 4 * 1024
	csvLegacyMaxRecords         = 200
	csvStoreCopyBufferBytes     = 128 * 1024
	csvStoreTemporaryNamePrefix = ".karte-csv-"
	csvStoreRecoveryScanEntries = 4_096
)

var errCSVRevisionConflict = errors.New("csv revision conflict")
var errCSVCommitUncertain = errors.New("csv commit completed but durability is unconfirmed")

// CSVItem is retained as a bounded compatibility result．New callers use
// SearchResources with kind=csv．
type CSVItem struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

type CsvPageRequest struct {
	Path  string `json:"path"`
	Page  int    `json:"page"`
	Limit int    `json:"limit"`
}

type CsvPageResult struct {
	Path      string     `json:"path"`
	Header    []string   `json:"header"`
	Rows      [][]string `json:"rows"`
	Page      int        `json:"page"`
	Limit     int        `json:"limit"`
	TotalRows int        `json:"totalRows"`
	HasMore   bool       `json:"hasMore"`
	Revision  string     `json:"revision"`
}

type CsvSavePageRequest struct {
	Path     string     `json:"path"`
	Revision string     `json:"revision"`
	Page     int        `json:"page"`
	Limit    int        `json:"limit"`
	Header   []string   `json:"header"`
	Rows     [][]string `json:"rows"`
}

type CsvSaveResult struct {
	Path      string `json:"path"`
	Revision  string `json:"revision"`
	TotalRows int    `json:"totalRows"`
}

type csvStoreHooks struct {
	openRoot                func(string) (*os.Root, error)
	randomID                func() (string, error)
	afterDataRootOpen       func(*os.Root, os.FileInfo) error
	afterDirectoryOpen      func(*os.Root, string, *os.Root) error
	afterFileLstat          func(*os.Root, string, os.FileInfo) error
	beforePublishRevalidate func(*os.Root, string) error
	write                   func(*os.File, []byte) (int, error)
	chmod                   func(*os.File, os.FileMode) error
	sync                    func(*os.File) error
	close                   func(*os.File) error
	replace                 func(*os.Root, string, string) error
	link                    func(*os.Root, string, string) error
	remove                  func(*os.Root, string) error
	syncRoot                func(*os.Root) error
}

type appCSVStoreState struct {
	mu    sync.Mutex
	hooks *csvStoreHooks
}

type csvFileSnapshot struct {
	header    []string
	rows      [][]string
	totalRows int
	revision  string
	identity  os.FileInfo
	mode      os.FileMode
}

type csvOutput struct {
	file    *os.File
	hooks   csvStoreHooks
	hasher  hash.Hash
	written int64
}

func defaultCSVStoreHooks() csvStoreHooks {
	return csvStoreHooks{
		openRoot: os.OpenRoot,
		randomID: randomMediaImportID,
		write: func(file *os.File, data []byte) (int, error) {
			return file.Write(data)
		},
		chmod: func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) },
		sync:  func(file *os.File) error { return file.Sync() },
		close: func(file *os.File) error { return file.Close() },
		// os.Root.Rename is descriptor-relative on supported Unix and Windows
		// targets．On Windows it uses replace-if-exists rename information，and
		// the following directory Sync supplies the write-through boundary．
		replace: func(root *os.Root, temporaryName, destinationName string) error {
			return root.Rename(temporaryName, destinationName)
		},
		link:   func(root *os.Root, oldName, newName string) error { return root.Link(oldName, newName) },
		remove: func(root *os.Root, name string) error { return root.Remove(name) },
		syncRoot: func(root *os.Root) error {
			directory, err := root.Open(".")
			if err != nil {
				return err
			}
			defer directory.Close()
			return syncMediaImportDirectory(directory)
		},
	}
}

func (hooks csvStoreHooks) normalized() csvStoreHooks {
	defaults := defaultCSVStoreHooks()
	if hooks.openRoot == nil {
		hooks.openRoot = defaults.openRoot
	}
	if hooks.randomID == nil {
		hooks.randomID = defaults.randomID
	}
	if hooks.write == nil {
		hooks.write = defaults.write
	}
	if hooks.chmod == nil {
		hooks.chmod = defaults.chmod
	}
	if hooks.sync == nil {
		hooks.sync = defaults.sync
	}
	if hooks.close == nil {
		hooks.close = defaults.close
	}
	if hooks.replace == nil {
		hooks.replace = defaults.replace
	}
	if hooks.link == nil {
		hooks.link = defaults.link
	}
	if hooks.remove == nil {
		hooks.remove = defaults.remove
	}
	if hooks.syncRoot == nil {
		hooks.syncRoot = defaults.syncRoot
	}
	return hooks
}

func (a *App) csvStoreHooks() csvStoreHooks {
	if a.csvStore.hooks == nil {
		return defaultCSVStoreHooks()
	}
	return a.csvStore.hooks.normalized()
}

// ImportCsvFile uses the same opened-handle staged importer as other dropped
// resources．The staged bytes are parsed strictly before durable publication．
func (a *App) ImportCsvFile(sourcePath string) (string, error) {
	return a.importMediaPath(mediaImportKindCSV, sourcePath)
}

// ImportCsvBase64 remains only for old WebViews．Decoding is streamed and
// bounded by both the CSV and legacy one-shot byte limits．
func (a *App) ImportCsvBase64(filename, encoded string) (string, error) {
	return a.importLegacyMediaBase64(mediaImportKindCSV, filename, encoded)
}

// GetCsvPage validates and parses the entire file with bounded working memory，
// but returns only one decoded page across the Wails boundary．
func (a *App) GetCsvPage(request CsvPageRequest) (CsvPageResult, error) {
	path, relativeName, err := normalizeCSVPath(request.Path)
	page, limit := normalizeCSVPage(request.Page, request.Limit)
	result := CsvPageResult{
		Path:   path,
		Header: []string{},
		Rows:   [][]string{},
		Page:   page,
		Limit:  limit,
	}
	if err != nil {
		return result, err
	}

	a.csvStore.mu.Lock()
	defer a.csvStore.mu.Unlock()
	hooks := a.csvStoreHooks()
	root, err := openStableCSVRoot(a.dataDir, false, hooks)
	if err != nil {
		return result, err
	}
	defer root.Close()

	snapshot, err := readCSVSnapshot(root, relativeName, page, limit, hooks)
	if err != nil {
		return result, err
	}
	if page > csvLastPage(snapshot.totalRows, limit) {
		return result, fmt.Errorf("csv page %d is outside the last page %d", page, csvLastPage(snapshot.totalRows, limit))
	}
	result.Header = snapshot.header
	result.Rows = snapshot.rows
	result.TotalRows = snapshot.totalRows
	result.HasMore = csvPageHasMore(page, limit, snapshot.totalRows)
	result.Revision = snapshot.revision
	return result, nil
}

// SaveCsvPage performs optimistic page replacement．Existing files require an
// exact raw SHA-256 revision．Only the final page may change its row count．
func (a *App) SaveCsvPage(request CsvSavePageRequest) (CsvSaveResult, error) {
	path, relativeName, err := normalizeCSVPath(request.Path)
	page, limit := normalizeCSVPage(request.Page, request.Limit)
	result := CsvSaveResult{Path: path}
	if err != nil {
		return result, err
	}
	if request.Page != page || request.Limit != limit {
		return result, errors.New("csv save page and limit must already be normalized")
	}
	if err := validateCSVEditRecords(request.Header, request.Rows); err != nil {
		return result, err
	}
	if len(request.Rows) > limit {
		return result, fmt.Errorf("csv edit contains %d rows，page limit is %d", len(request.Rows), limit)
	}
	if csvDecodedSize(request.Header, request.Rows) > csvMaxPageDecodedBytes {
		return result, fmt.Errorf("csv edit exceeds the %d-byte decoded page limit", csvMaxPageDecodedBytes)
	}
	if csvJSONTransferSize(request.Header, request.Rows) > csvMaxPageDecodedBytes {
		return result, fmt.Errorf("csv edit exceeds the %d-byte JSON transfer limit", csvMaxPageDecodedBytes)
	}

	a.csvStore.mu.Lock()
	defer a.csvStore.mu.Unlock()
	hooks := a.csvStoreHooks()
	root, err := openStableCSVRoot(a.dataDir, true, hooks)
	if err != nil {
		return result, err
	}
	defer root.Close()

	current, currentErr := readCSVSnapshot(root, relativeName, 1, 1, hooks)
	if errors.Is(currentErr, os.ErrNotExist) {
		if request.Revision != "" {
			return result, fmt.Errorf("%w: csv file no longer exists", errCSVRevisionConflict)
		}
		if page != 1 {
			return result, errors.New("new csv files must be created on page 1")
		}
		return a.saveNewCSVFile(root, relativeName, path, request.Header, request.Rows, hooks)
	}
	if currentErr != nil {
		return result, currentErr
	}
	if request.Revision == "" || request.Revision != current.revision {
		return result, fmt.Errorf("%w: expected %q，current %q", errCSVRevisionConflict, request.Revision, current.revision)
	}
	if len(request.Header) != len(current.header) {
		return result, fmt.Errorf("csv header has %d fields，expected %d", len(request.Header), len(current.header))
	}
	lastPage := csvLastPage(current.totalRows, limit)
	if page > lastPage {
		return result, fmt.Errorf("csv page %d is outside the last page %d", page, lastPage)
	}
	targetRows := csvRowsOnPage(page, limit, current.totalRows)
	if page < lastPage && len(request.Rows) != targetRows {
		return result, fmt.Errorf("non-final csv page %d must retain %d rows", page, targetRows)
	}

	temporary, temporaryName, err := createCSVTemporary(root, current.mode, hooks)
	if err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = temporary.Close()
			_ = hooks.remove(root, temporaryName)
		}
	}()

	outputRevision, totalRows, sourceIdentity, err := rewriteCSVPage(
		root,
		relativeName,
		temporary,
		current,
		page,
		limit,
		request.Header,
		request.Rows,
		hooks,
	)
	if err != nil {
		return result, err
	}
	if err := finalizeCSVTemporary(temporary, hooks); err != nil {
		return result, err
	}
	if hooks.beforePublishRevalidate != nil {
		if err := hooks.beforePublishRevalidate(root, relativeName); err != nil {
			return result, err
		}
	}
	identity, revision, err := readCSVRevision(root, relativeName, hooks)
	if err != nil || !os.SameFile(sourceIdentity, identity) || revision != current.revision {
		return result, fmt.Errorf("%w: csv changed before publish", errCSVRevisionConflict)
	}
	if err := hooks.replace(root, temporaryName, relativeName); err != nil {
		return result, fmt.Errorf("replace csv atomically: %w", err)
	}
	committed = true
	if err := hooks.syncRoot(root); err != nil {
		a.logError(fmt.Sprintf("CSV replacement committed but directory Sync failed: %v", err))
		return CsvSaveResult{Path: path, Revision: outputRevision, TotalRows: totalRows},
			fmt.Errorf("%w: replacement directory Sync: %v", errCSVCommitUncertain, err)
	}
	return CsvSaveResult{Path: path, Revision: outputRevision, TotalRows: totalRows}, nil
}

// GetCsvList is a bounded adapter for older frontends．It no longer performs a
// recursive filepath walk or returns an unbounded metadata payload．
func (a *App) GetCsvList() []CSVItem {
	result, err := a.SearchResources(ResourceSearchRequest{
		Kinds: []ResourceKind{ResourceKindCSV},
		Page:  1,
		Limit: resourceSearchMaxLimit,
	})
	if err != nil {
		a.logError(fmt.Sprintf("GetCsvList failed: %v", err))
		return []CSVItem{}
	}
	items := make([]CSVItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, CSVItem{
			Path:    item.Path,
			Name:    item.Metadata.Name,
			Size:    item.Metadata.Size,
			ModTime: item.Metadata.ModTime,
		})
	}
	return items
}

// GetCsvFile is a bounded compatibility wrapper．Large tables must use
// GetCsvPage so they never cross IPC as a full [][]string payload．
func (a *App) GetCsvFile(path string) ([][]string, error) {
	page, err := a.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: csvMaxPageLimit})
	if err != nil {
		return nil, err
	}
	if page.TotalRows+1 > csvLegacyMaxRecords || page.HasMore {
		return nil, fmt.Errorf("legacy csv read is limited to %d records", csvLegacyMaxRecords)
	}
	records := make([][]string, 0, 1+len(page.Rows))
	if len(page.Header) > 0 {
		records = append(records, page.Header)
	}
	records = append(records, page.Rows...)
	return records, nil
}

// SaveCsvFile is a bounded compatibility wrapper．It snapshots a small current
// file and delegates to the optimistic atomic page writer．
func (a *App) SaveCsvFile(path string, data [][]string) error {
	if len(data) == 0 {
		return errors.New("legacy csv save requires a header row")
	}
	if len(data) > csvLegacyMaxRecords {
		return fmt.Errorf("legacy csv save is limited to %d records", csvLegacyMaxRecords)
	}
	page, err := a.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: csvMaxPageLimit})
	revision := ""
	if err == nil {
		if page.TotalRows+1 > csvLegacyMaxRecords || page.HasMore {
			return fmt.Errorf("legacy csv save cannot replace a file larger than %d records", csvLegacyMaxRecords)
		}
		revision = page.Revision
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err = a.SaveCsvPage(CsvSavePageRequest{
		Path:     path,
		Revision: revision,
		Page:     1,
		Limit:    csvMaxPageLimit,
		Header:   data[0],
		Rows:     data[1:],
	})
	return err
}

func normalizeCSVPath(rawPath string) (string, string, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" || path != rawPath || strings.ContainsRune(path, '\x00') || strings.Contains(path, "\\") || strings.Contains(path, ":") ||
		pathpkg.IsAbs(path) || filepath.IsAbs(filepath.FromSlash(path)) || pathpkg.Clean(path) != path ||
		!strings.HasPrefix(path, "data/csv/") {
		return "", "", fmt.Errorf("invalid csv path %q", rawPath)
	}
	// CSV storage is intentionally flat．This keeps temporary files，the
	// destination，and the directory durability barrier under one opened Root．
	relativeName := strings.TrimPrefix(path, "data/csv/")
	if relativeName == "" || relativeName == "." || relativeName == ".." || strings.HasPrefix(relativeName, "../") ||
		strings.Contains(relativeName, "/") || !strings.EqualFold(pathpkg.Ext(relativeName), ".csv") {
		return "", "", fmt.Errorf("invalid csv path %q", rawPath)
	}
	return path, filepath.FromSlash(relativeName), nil
}

func normalizeCSVPage(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = csvDefaultPageLimit
	}
	if limit > csvMaxPageLimit {
		limit = csvMaxPageLimit
	}
	return page, limit
}

func csvPageStart(page, limit int) int {
	maximum := int(^uint(0) >> 1)
	if page <= 1 {
		return 0
	}
	if page-1 > maximum/limit {
		return maximum
	}
	return (page - 1) * limit
}

func csvLastPage(totalRows, limit int) int {
	if totalRows <= 0 {
		return 1
	}
	return 1 + (totalRows-1)/limit
}

func csvRowsOnPage(page, limit, totalRows int) int {
	start := csvPageStart(page, limit)
	if start >= totalRows {
		return 0
	}
	remaining := totalRows - start
	if remaining > limit {
		return limit
	}
	return remaining
}

func csvPageHasMore(page, limit, totalRows int) bool {
	start := csvPageStart(page, limit)
	return start < totalRows && totalRows-start > limit
}

func openStableCSVRoot(dataDirectory string, create bool, hooks csvStoreHooks) (*os.Root, error) {
	before, err := os.Lstat(dataDirectory)
	if err != nil {
		return nil, fmt.Errorf("inspect csv data root: %w", err)
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("csv data root is not a non-symlink directory")
	}
	dataRoot, err := hooks.openRoot(dataDirectory)
	if err != nil {
		return nil, fmt.Errorf("open csv data root: %w", err)
	}
	defer dataRoot.Close()
	opened, err := dataRoot.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("csv data root changed while it was opened")
	}
	if hooks.afterDataRootOpen != nil {
		if err := hooks.afterDataRootOpen(dataRoot, opened); err != nil {
			return nil, err
		}
	}
	after, err := os.Lstat(dataDirectory)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		return nil, errors.New("csv data root changed while it was opened")
	}

	current := ""
	for _, component := range []string{"data", "csv"} {
		current = filepath.Join(current, component)
		info, inspectErr := dataRoot.Lstat(current)
		if errors.Is(inspectErr, os.ErrNotExist) && create {
			if err := dataRoot.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return nil, fmt.Errorf("prepare csv directory: %w", err)
			}
			info, inspectErr = dataRoot.Lstat(current)
		}
		if inspectErr != nil {
			return nil, inspectErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("csv directory contains a symlink or non-directory component")
		}
	}

	root, err := dataRoot.OpenRoot(filepath.Join("data", "csv"))
	if err != nil {
		return nil, err
	}
	directoryInfo, err := root.Stat(".")
	if err != nil || !directoryInfo.IsDir() {
		root.Close()
		return nil, errors.New("csv root is not a directory")
	}
	if hooks.afterDirectoryOpen != nil {
		if err := hooks.afterDirectoryOpen(dataRoot, filepath.Join("data", "csv"), root); err != nil {
			root.Close()
			return nil, err
		}
	}
	pathInfo, err := dataRoot.Lstat(filepath.Join("data", "csv"))
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(directoryInfo, pathInfo) {
		root.Close()
		return nil, errors.New("csv root changed while it was opened")
	}
	after, err = os.Lstat(dataDirectory)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		root.Close()
		return nil, errors.New("csv data root changed while csv root was opened")
	}
	return root, nil
}

func validateCSVPathComponents(root *os.Root, relativeName string, allowFinalMissing bool) error {
	components := strings.Split(filepath.ToSlash(relativeName), "/")
	current := "."
	for index, component := range components {
		if component == "" || component == "." || component == ".." {
			return errors.New("invalid csv path component")
		}
		directory, err := root.Open(current)
		if err != nil {
			return err
		}
		exact := false
		caseAlias := false
		for {
			entries, readErr := directory.ReadDir(128)
			for _, entry := range entries {
				if entry.Name() == component {
					exact = true
					break
				}
				if strings.EqualFold(entry.Name(), component) {
					caseAlias = true
				}
			}
			if exact || errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				directory.Close()
				return readErr
			}
		}
		if closeErr := directory.Close(); closeErr != nil {
			return closeErr
		}
		final := index == len(components)-1
		if !exact {
			if final && allowFinalMissing && !caseAlias {
				return nil
			}
			if caseAlias {
				return errors.New("csv path casing does not match the directory entry")
			}
			return os.ErrNotExist
		}
		path := filepath.Join(current, component)
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if final {
			return nil
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("csv path contains a symlink or non-directory ancestor")
		}
		current = path
	}
	return nil
}

func openStableCSVFile(root *os.Root, relativeName string, hooks csvStoreHooks) (*os.File, os.FileInfo, error) {
	return openStableCSVFileAttempt(root, relativeName, hooks, true)
}

func openStableCSVFileAttempt(root *os.Root, relativeName string, hooks csvStoreHooks, allowRecovery bool) (*os.File, os.FileInfo, error) {
	if err := validateCSVPathComponents(root, relativeName, false); err != nil {
		return nil, nil, err
	}
	before, err := root.Lstat(relativeName)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("csv target is not a regular non-symlink file")
	}
	if csvFileHasMultipleLinks(before) {
		if allowRecovery {
			recovered, recoverErr := recoverCSVTemporaryLinks(root, relativeName, before, hooks)
			if recoverErr != nil {
				return nil, nil, recoverErr
			}
			if recovered {
				return openStableCSVFileAttempt(root, relativeName, hooks, false)
			}
		}
		return nil, nil, errors.New("csv target has a hard-link alias")
	}
	if hooks.afterFileLstat != nil {
		if err := hooks.afterFileLstat(root, relativeName, before); err != nil {
			return nil, nil, err
		}
	}
	file, err := root.Open(relativeName)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	after, err := root.Lstat(relativeName)
	multipleLinks, linkErr := csvOpenedFileHasMultipleLinks(file, opened)
	if linkErr != nil {
		file.Close()
		return nil, nil, linkErr
	}
	if err != nil || !after.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(before, opened) || !os.SameFile(opened, after) {
		file.Close()
		return nil, nil, errors.New("csv target changed while it was opened")
	}
	if csvFileHasMultipleLinks(after) || multipleLinks {
		file.Close()
		if allowRecovery {
			recovered, recoverErr := recoverCSVTemporaryLinks(root, relativeName, after, hooks)
			if recoverErr != nil {
				return nil, nil, recoverErr
			}
			if recovered {
				return openStableCSVFileAttempt(root, relativeName, hooks, false)
			}
		}
		return nil, nil, errors.New("csv target has a hard-link alias")
	}
	return file, opened, nil
}

// recoverCSVTemporaryLinks only removes names from the store's reserved
// temporary namespace that still identify the published destination．Any
// ordinary hard-link alias remains fail-closed．The entry cap prevents a large
// directory from turning recovery into an unbounded scan．
func recoverCSVTemporaryLinks(root *os.Root, relativeName string, destination os.FileInfo, hooks csvStoreHooks) (bool, error) {
	directory, err := root.Open(".")
	if err != nil {
		return false, err
	}
	defer directory.Close()

	candidates := make([]string, 0, 1)
	scanned := 0
	for scanned < csvStoreRecoveryScanEntries {
		remaining := csvStoreRecoveryScanEntries - scanned
		batchSize := 128
		if remaining < batchSize {
			batchSize = remaining
		}
		entries, readErr := directory.ReadDir(batchSize)
		scanned += len(entries)
		for _, entry := range entries {
			name := entry.Name()
			if !isCSVStoreTemporaryName(name) {
				continue
			}
			info, inspectErr := root.Lstat(name)
			if errors.Is(inspectErr, os.ErrNotExist) {
				continue
			}
			if inspectErr != nil {
				return false, fmt.Errorf("inspect csv recovery link: %w", inspectErr)
			}
			if info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && os.SameFile(destination, info) {
				candidates = append(candidates, name)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return false, fmt.Errorf("scan csv recovery links: %w", readErr)
		}
	}
	if len(candidates) == 0 {
		return false, nil
	}

	removed := false
	for _, name := range candidates {
		current, inspectErr := root.Lstat(name)
		if errors.Is(inspectErr, os.ErrNotExist) {
			continue
		}
		if inspectErr != nil {
			return true, fmt.Errorf("revalidate csv recovery link: %w", inspectErr)
		}
		if !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(destination, current) {
			continue
		}
		if err := hooks.remove(root, name); err != nil {
			return true, fmt.Errorf("remove csv recovery link %q: %w", name, err)
		}
		removed = true
	}
	if removed {
		if err := hooks.syncRoot(root); err != nil {
			return true, fmt.Errorf("%w: csv recovery directory Sync: %v", errCSVCommitUncertain, err)
		}
	}
	after, err := root.Lstat(relativeName)
	if err != nil || !after.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(destination, after) {
		return true, errors.New("csv target changed while temporary links were recovered")
	}
	return true, nil
}

func isCSVStoreTemporaryName(name string) bool {
	const randomIDBytes = 16
	prefixLength := len(csvStoreTemporaryNamePrefix)
	if len(name) != prefixLength+randomIDBytes*2+len(".tmp") || !strings.HasPrefix(name, csvStoreTemporaryNamePrefix) || !strings.HasSuffix(name, ".tmp") {
		return false
	}
	for index := prefixLength; index < len(name)-len(".tmp"); index++ {
		character := name[index]
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func csvFileHasMultipleLinks(info os.FileInfo) bool {
	if info == nil || info.Sys() == nil {
		return false
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	field := value.FieldByName("Nlink")
	if !field.IsValid() {
		return false
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return field.Uint() > 1
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return field.Int() > 1
	default:
		return false
	}
}

func readCSVSnapshot(root *os.Root, relativeName string, page, limit int, hooks csvStoreHooks) (csvFileSnapshot, error) {
	file, identity, err := openStableCSVFile(root, relativeName, hooks)
	if err != nil {
		return csvFileSnapshot{}, err
	}
	defer file.Close()
	hasher := sha256.New()
	parser := newStrictCSVParser(io.TeeReader(file, hasher))
	start := csvPageStart(page, limit)
	end := start
	if start <= int(^uint(0)>>1)-limit {
		end = start + limit
	}
	snapshot := csvFileSnapshot{
		header:   []string{},
		rows:     [][]string{},
		identity: identity,
		mode:     identity.Mode().Perm(),
	}
	decodedBytes := 0
	jsonBytes := csvPageJSONMetadataReserve + 2
	recordIndex := 0
	for {
		record, readErr := parser.ReadRecord()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return csvFileSnapshot{}, readErr
		}
		if recordIndex == 0 {
			snapshot.header = record
			decodedBytes += csvDecodedRecordSize(record)
			jsonBytes += csvJSONEncodedRecordSize(record)
		} else {
			rowIndex := recordIndex - 1
			if rowIndex >= start && rowIndex < end {
				decodedBytes += csvDecodedRecordSize(record)
				if decodedBytes > csvMaxPageDecodedBytes {
					return csvFileSnapshot{}, fmt.Errorf("csv page exceeds the %d-byte decoded transfer limit", csvMaxPageDecodedBytes)
				}
				jsonBytes += csvJSONEncodedRecordSize(record) + 1
				if jsonBytes > csvMaxPageDecodedBytes {
					return csvFileSnapshot{}, fmt.Errorf("csv page exceeds the %d-byte JSON transfer limit", csvMaxPageDecodedBytes)
				}
				snapshot.rows = append(snapshot.rows, record)
			}
			snapshot.totalRows++
		}
		recordIndex++
	}
	if recordIndex == 0 {
		return csvFileSnapshot{}, errors.New("csv file has no header record")
	}
	if decodedBytes > csvMaxPageDecodedBytes {
		return csvFileSnapshot{}, fmt.Errorf("csv page exceeds the %d-byte decoded transfer limit", csvMaxPageDecodedBytes)
	}
	if jsonBytes > csvMaxPageDecodedBytes {
		return csvFileSnapshot{}, fmt.Errorf("csv page exceeds the %d-byte JSON transfer limit", csvMaxPageDecodedBytes)
	}
	if err := validateOpenCSVIdentity(root, relativeName, file, identity); err != nil {
		return csvFileSnapshot{}, err
	}
	parsedRevision := formatCSVRevision(hasher.Sum(nil))
	stableIdentity, stableRevision, err := readCSVRevision(root, relativeName, hooks)
	if err != nil {
		return csvFileSnapshot{}, err
	}
	if !os.SameFile(identity, stableIdentity) || parsedRevision != stableRevision {
		return csvFileSnapshot{}, fmt.Errorf("%w: csv changed while its page was read", errCSVRevisionConflict)
	}
	snapshot.revision = stableRevision
	return snapshot, nil
}

func readCSVRevision(root *os.Root, relativeName string, hooks csvStoreHooks) (os.FileInfo, string, error) {
	file, identity, err := openStableCSVFile(root, relativeName, hooks)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	hasher := sha256.New()
	limited := &csvByteLimitReader{reader: file, remaining: defaultMaxCSVBytes}
	buffer := make([]byte, csvStoreCopyBufferBytes)
	if _, err := io.CopyBuffer(hasher, limited, buffer); err != nil {
		return nil, "", err
	}
	if err := validateOpenCSVIdentity(root, relativeName, file, identity); err != nil {
		return nil, "", err
	}
	return identity, formatCSVRevision(hasher.Sum(nil)), nil
}

func validateOpenCSVIdentity(root *os.Root, relativeName string, file *os.File, identity os.FileInfo) error {
	openedAfter, err := file.Stat()
	if err != nil {
		return err
	}
	pathAfter, err := root.Lstat(relativeName)
	multipleLinks, linkErr := csvOpenedFileHasMultipleLinks(file, openedAfter)
	if linkErr != nil {
		return linkErr
	}
	if err != nil || !pathAfter.Mode().IsRegular() || pathAfter.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(identity, openedAfter) || !os.SameFile(openedAfter, pathAfter) || csvFileHasMultipleLinks(pathAfter) || multipleLinks {
		return errors.New("csv target changed while it was read")
	}
	return nil
}

func formatCSVRevision(sum []byte) string {
	return "sha256:" + hex.EncodeToString(sum)
}

func createCSVTemporary(root *os.Root, mode os.FileMode, hooks csvStoreHooks) (*os.File, string, error) {
	if mode.Perm() == 0 {
		mode = 0o644
	}
	for attempt := 0; attempt < 16; attempt++ {
		id, err := hooks.randomID()
		if err != nil {
			return nil, "", err
		}
		name := csvStoreTemporaryNamePrefix + id + ".tmp"
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("create csv temporary file: %w", err)
		}
		if err := hooks.chmod(file, mode.Perm()); err != nil {
			file.Close()
			_ = hooks.remove(root, name)
			return nil, "", fmt.Errorf("set csv temporary permissions: %w", err)
		}
		return file, name, nil
	}
	return nil, "", errors.New("could not allocate a csv temporary file")
}

func finalizeCSVTemporary(file *os.File, hooks csvStoreHooks) error {
	if err := hooks.sync(file); err != nil {
		return fmt.Errorf("sync csv temporary file: %w", err)
	}
	if err := hooks.close(file); err != nil {
		_ = file.Close()
		return fmt.Errorf("close csv temporary file: %w", err)
	}
	return nil
}

func (a *App) saveNewCSVFile(root *os.Root, relativeName, path string, header []string, rows [][]string, hooks csvStoreHooks) (CsvSaveResult, error) {
	if err := validateCSVPathComponents(root, relativeName, true); err != nil {
		return CsvSaveResult{Path: path}, err
	}
	if _, err := root.Lstat(relativeName); err == nil {
		return CsvSaveResult{Path: path}, fmt.Errorf("%w: csv file already exists", errCSVRevisionConflict)
	} else if !errors.Is(err, os.ErrNotExist) {
		return CsvSaveResult{Path: path}, err
	}
	temporary, temporaryName, err := createCSVTemporary(root, 0o644, hooks)
	if err != nil {
		return CsvSaveResult{Path: path}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = temporary.Close()
			_ = hooks.remove(root, temporaryName)
		}
	}()
	hasher := sha256.New()
	output := &csvOutput{file: temporary, hooks: hooks, hasher: hasher}
	if err := output.writeRecord(header); err != nil {
		return CsvSaveResult{Path: path}, err
	}
	for _, row := range rows {
		if err := output.writeRecord(row); err != nil {
			return CsvSaveResult{Path: path}, err
		}
	}
	if err := finalizeCSVTemporary(temporary, hooks); err != nil {
		return CsvSaveResult{Path: path}, err
	}
	if hooks.beforePublishRevalidate != nil {
		if err := hooks.beforePublishRevalidate(root, relativeName); err != nil {
			return CsvSaveResult{Path: path}, err
		}
	}
	if err := validateCSVPathComponents(root, relativeName, true); err != nil {
		return CsvSaveResult{Path: path}, err
	}
	if _, err := root.Lstat(relativeName); err == nil {
		return CsvSaveResult{Path: path}, fmt.Errorf("%w: csv file appeared before publish", errCSVRevisionConflict)
	} else if !errors.Is(err, os.ErrNotExist) {
		return CsvSaveResult{Path: path}, err
	}
	if err := hooks.link(root, temporaryName, relativeName); err != nil {
		if errors.Is(err, os.ErrExist) {
			return CsvSaveResult{Path: path}, fmt.Errorf("%w: csv file appeared before publish", errCSVRevisionConflict)
		}
		return CsvSaveResult{Path: path}, fmt.Errorf("publish csv without replacement: %w", err)
	}
	committed = true
	result := CsvSaveResult{Path: path, Revision: formatCSVRevision(hasher.Sum(nil)), TotalRows: len(rows)}
	postCommitErrors := make([]error, 0, 3)
	if err := hooks.syncRoot(root); err != nil {
		a.logError(fmt.Sprintf("CSV creation committed but directory Sync failed: %v", err))
		postCommitErrors = append(postCommitErrors, fmt.Errorf("publish directory Sync: %w", err))
	}
	if err := hooks.remove(root, temporaryName); err != nil {
		a.logError(fmt.Sprintf("CSV creation committed but temporary link cleanup failed: %v", err))
		postCommitErrors = append(postCommitErrors, fmt.Errorf("temporary link cleanup: %w", err))
	} else if err := hooks.syncRoot(root); err != nil {
		a.logError(fmt.Sprintf("CSV creation committed but temporary cleanup Sync failed: %v", err))
		postCommitErrors = append(postCommitErrors, fmt.Errorf("cleanup directory Sync: %w", err))
	}
	if len(postCommitErrors) > 0 {
		return result, fmt.Errorf("%w: %v", errCSVCommitUncertain, errors.Join(postCommitErrors...))
	}
	return result, nil
}

func rewriteCSVPage(
	root *os.Root,
	relativeName string,
	temporary *os.File,
	current csvFileSnapshot,
	page int,
	limit int,
	header []string,
	rows [][]string,
	hooks csvStoreHooks,
) (string, int, os.FileInfo, error) {
	source, identity, err := openStableCSVFile(root, relativeName, hooks)
	if err != nil {
		return "", 0, nil, err
	}
	defer source.Close()
	if !os.SameFile(current.identity, identity) {
		return "", 0, nil, fmt.Errorf("%w: csv file identity changed", errCSVRevisionConflict)
	}
	sourceHash := sha256.New()
	parser := newStrictCSVParser(io.TeeReader(source, sourceHash))
	outputHash := sha256.New()
	output := &csvOutput{file: temporary, hooks: hooks, hasher: outputHash}
	start := csvPageStart(page, limit)
	end := start + csvRowsOnPage(page, limit, current.totalRows)
	recordIndex := 0
	replacementWritten := false
	totalRows := 0
	for {
		record, readErr := parser.ReadRecord()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", 0, nil, readErr
		}
		if recordIndex == 0 {
			if err := output.writeRecord(header); err != nil {
				return "", 0, nil, err
			}
			recordIndex++
			continue
		}
		rowIndex := recordIndex - 1
		if rowIndex == start && !replacementWritten {
			for _, replacement := range rows {
				if err := output.writeRecord(replacement); err != nil {
					return "", 0, nil, err
				}
			}
			totalRows += len(rows)
			replacementWritten = true
		}
		if rowIndex < start || rowIndex >= end {
			if err := output.writeRecord(record); err != nil {
				return "", 0, nil, err
			}
			totalRows++
		}
		recordIndex++
	}
	if recordIndex == 0 {
		return "", 0, nil, errors.New("csv file has no header record")
	}
	if !replacementWritten {
		for _, replacement := range rows {
			if err := output.writeRecord(replacement); err != nil {
				return "", 0, nil, err
			}
		}
		totalRows += len(rows)
	}
	if err := validateOpenCSVIdentity(root, relativeName, source, identity); err != nil {
		return "", 0, nil, err
	}
	if revision := formatCSVRevision(sourceHash.Sum(nil)); revision != current.revision {
		return "", 0, nil, fmt.Errorf("%w: csv changed while edit was staged", errCSVRevisionConflict)
	}
	return formatCSVRevision(outputHash.Sum(nil)), totalRows, identity, nil
}

func validateCSVEditRecords(header []string, rows [][]string) error {
	if err := validateCSVHeader(header); err != nil {
		return err
	}
	for index, row := range rows {
		if len(row) != len(header) {
			return fmt.Errorf("csv row %d has %d fields，expected %d", index+1, len(row), len(header))
		}
		if err := validateCSVDecodedRecord(row, false); err != nil {
			return fmt.Errorf("invalid csv row %d: %w", index+1, err)
		}
	}
	return nil
}

func validateCSVHeader(header []string) error {
	if len(header) == 0 {
		return errors.New("csv header must contain at least one field")
	}
	if len(header) > csvMaxFields {
		return fmt.Errorf("csv header exceeds the %d-field limit", csvMaxFields)
	}
	if err := validateCSVDecodedRecord(header, false); err != nil {
		return fmt.Errorf("invalid csv header: %w", err)
	}
	for _, cell := range header {
		if cell != "" {
			return nil
		}
	}
	return errors.New("csv header must contain a non-empty field")
}

func validateCSVDecodedRecord(record []string, allowLeadingBOM bool) error {
	rowBytes := 0
	for index, cell := range record {
		if !utf8.ValidString(cell) {
			return errors.New("csv cell is not valid UTF-8")
		}
		if strings.ContainsRune(cell, '\ufeff') && !(allowLeadingBOM && index == 0 && strings.HasPrefix(cell, "\ufeff") && !strings.ContainsRune(strings.TrimPrefix(cell, "\ufeff"), '\ufeff')) {
			return errors.New("UTF-8 BOM is only allowed at the start of the file")
		}
		if len(cell) > csvMaxCellBytes {
			return fmt.Errorf("csv cell exceeds the %d-byte limit", csvMaxCellBytes)
		}
		rowBytes += len(cell)
		if index > 0 {
			rowBytes++
		}
	}
	if rowBytes > csvMaxRecordBytes {
		return fmt.Errorf("csv record exceeds the %d-byte limit", csvMaxRecordBytes)
	}
	return nil
}

func csvDecodedRecordSize(record []string) int {
	size := 2
	for _, cell := range record {
		size += len(cell) + 3
	}
	return size
}

func csvDecodedSize(header []string, rows [][]string) int {
	size := csvDecodedRecordSize(header)
	for _, row := range rows {
		size += csvDecodedRecordSize(row)
		if size > csvMaxPageDecodedBytes {
			return size
		}
	}
	return size
}

func csvJSONTransferSize(header []string, rows [][]string) int {
	size := csvPageJSONMetadataReserve + csvJSONEncodedRecordSize(header) + 2
	for _, row := range rows {
		size += csvJSONEncodedRecordSize(row) + 1
		if size > csvMaxPageDecodedBytes {
			return size
		}
	}
	return size
}

func csvJSONEncodedRecordSize(record []string) int {
	size := 2
	for index, cell := range record {
		if index > 0 {
			size++
		}
		size += csvJSONEncodedStringSize(cell)
	}
	return size
}

func csvJSONEncodedStringSize(value string) int {
	size := 2
	for offset := 0; offset < len(value); {
		character := value[offset]
		if character < utf8.RuneSelf {
			switch {
			case character < 0x20 || character == '<' || character == '>' || character == '&':
				size += 6
			case character == '\\' || character == '"':
				size += 2
			default:
				size++
			}
			offset++
			continue
		}
		runeValue, width := utf8.DecodeRuneInString(value[offset:])
		if runeValue == '\u2028' || runeValue == '\u2029' {
			size += 6
		} else {
			size += width
		}
		offset += width
	}
	return size
}

func (output *csvOutput) writeRecord(record []string) error {
	if err := validateCSVDecodedRecord(record, false); err != nil {
		return err
	}
	encodedBytes := 2
	for index, cell := range record {
		if index > 0 {
			encodedBytes++
		}
		if csvCellNeedsQuotes(cell) {
			encodedBytes += 2 + len(cell) + strings.Count(cell, "\"")
		} else {
			encodedBytes += len(cell)
		}
	}
	if encodedBytes > csvMaxRecordBytes {
		return fmt.Errorf("encoded csv record exceeds the %d-byte limit", csvMaxRecordBytes)
	}
	for index, cell := range record {
		if index > 0 {
			if err := output.writeBytes([]byte{','}); err != nil {
				return err
			}
		}
		if csvCellNeedsQuotes(cell) {
			if err := output.writeBytes([]byte{'"'}); err != nil {
				return err
			}
			if err := output.writeBytes([]byte(strings.ReplaceAll(cell, "\"", "\"\""))); err != nil {
				return err
			}
			if err := output.writeBytes([]byte{'"'}); err != nil {
				return err
			}
		} else if err := output.writeBytes([]byte(cell)); err != nil {
			return err
		}
	}
	return output.writeBytes([]byte{'\r', '\n'})
}

func csvCellNeedsQuotes(cell string) bool {
	return strings.ContainsAny(cell, ",\"\r\n")
}

func (output *csvOutput) writeBytes(data []byte) error {
	if output.written+int64(len(data)) > defaultMaxCSVBytes {
		return fmt.Errorf("csv output exceeds the %d-byte file limit", defaultMaxCSVBytes)
	}
	for len(data) > 0 {
		written, err := output.hooks.write(output.file, data)
		if written < 0 || written > len(data) {
			return errors.New("invalid csv temporary write count")
		}
		if written > 0 {
			_, _ = output.hasher.Write(data[:written])
			output.written += int64(written)
			data = data[written:]
		}
		if err != nil {
			return fmt.Errorf("write csv temporary file: %w", err)
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

type csvByteLimitReader struct {
	reader    io.Reader
	remaining int64
}

func (reader *csvByteLimitReader) Read(data []byte) (int, error) {
	if reader.remaining < 0 {
		return 0, fmt.Errorf("csv file exceeds the %d-byte limit", defaultMaxCSVBytes)
	}
	maximum := int64(len(data))
	if maximum > reader.remaining+1 {
		maximum = reader.remaining + 1
	}
	read, err := reader.reader.Read(data[:maximum])
	reader.remaining -= int64(read)
	if reader.remaining < 0 {
		return read, fmt.Errorf("csv file exceeds the %d-byte limit", defaultMaxCSVBytes)
	}
	return read, err
}

type strictCSVParser struct {
	reader         *bufio.Reader
	bytesRead      int64
	recordsRead    int
	expectedFields int
}

func newStrictCSVParser(reader io.Reader) *strictCSVParser {
	return &strictCSVParser{reader: bufio.NewReaderSize(reader, 64*1024)}
}

func (parser *strictCSVParser) readByte(recordBytes *int) (byte, error) {
	value, err := parser.reader.ReadByte()
	if err != nil {
		return 0, err
	}
	parser.bytesRead++
	*recordBytes++
	if parser.bytesRead > defaultMaxCSVBytes {
		return 0, fmt.Errorf("csv file exceeds the %d-byte limit", defaultMaxCSVBytes)
	}
	if *recordBytes > csvMaxRecordBytes {
		return 0, fmt.Errorf("csv record exceeds the %d-byte limit", csvMaxRecordBytes)
	}
	return value, nil
}

func (parser *strictCSVParser) ReadRecord() ([]string, error) {
	const (
		csvFieldStart = iota
		csvUnquotedField
		csvQuotedField
		csvAfterQuote
	)
	state := csvFieldStart
	record := make([]string, 0, minInt(csvMaxFields, 16))
	field := make([]byte, 0, 64)
	recordBytes := 0
	started := false
	if err := parser.consumeLeadingBOM(&recordBytes); err != nil {
		return nil, err
	}

	appendByte := func(value byte) error {
		if len(field) >= csvMaxCellBytes {
			return fmt.Errorf("csv cell exceeds the %d-byte limit", csvMaxCellBytes)
		}
		field = append(field, value)
		return nil
	}
	appendField := func() error {
		if len(record) >= csvMaxFields {
			return fmt.Errorf("csv record exceeds the %d-field limit", csvMaxFields)
		}
		record = append(record, string(field))
		field = field[:0]
		return nil
	}
	finish := func() ([]string, error) {
		if err := appendField(); err != nil {
			return nil, err
		}
		if err := validateCSVDecodedRecord(record, false); err != nil {
			return nil, err
		}
		if parser.recordsRead == 0 {
			if err := validateCSVHeader(record); err != nil {
				return nil, err
			}
			parser.expectedFields = len(record)
		} else if len(record) != parser.expectedFields {
			return nil, fmt.Errorf("csv record %d has %d fields，expected %d", parser.recordsRead+1, len(record), parser.expectedFields)
		}
		parser.recordsRead++
		if parser.recordsRead > csvMaxRecords {
			return nil, fmt.Errorf("csv exceeds the %d-record limit", csvMaxRecords)
		}
		return record, nil
	}

	for {
		value, err := parser.readByte(&recordBytes)
		if errors.Is(err, io.EOF) {
			if !started {
				return nil, io.EOF
			}
			if state == csvQuotedField {
				return nil, errors.New("malformed csv: unterminated quoted field")
			}
			return finish()
		}
		if err != nil {
			return nil, err
		}
		started = true

		switch state {
		case csvFieldStart:
			switch value {
			case '"':
				state = csvQuotedField
			case ',':
				if err := appendField(); err != nil {
					return nil, err
				}
			case '\n':
				return finish()
			case '\r':
				next, nextErr := parser.readByte(&recordBytes)
				if nextErr != nil || next != '\n' {
					return nil, errors.New("malformed csv: bare carriage return outside a quoted field")
				}
				return finish()
			default:
				if err := appendByte(value); err != nil {
					return nil, err
				}
				state = csvUnquotedField
			}
		case csvUnquotedField:
			switch value {
			case '"':
				return nil, errors.New("malformed csv: quote in an unquoted field")
			case ',':
				if err := appendField(); err != nil {
					return nil, err
				}
				state = csvFieldStart
			case '\n':
				return finish()
			case '\r':
				next, nextErr := parser.readByte(&recordBytes)
				if nextErr != nil || next != '\n' {
					return nil, errors.New("malformed csv: bare carriage return outside a quoted field")
				}
				return finish()
			default:
				if err := appendByte(value); err != nil {
					return nil, err
				}
			}
		case csvQuotedField:
			if value == '"' {
				state = csvAfterQuote
			} else if err := appendByte(value); err != nil {
				return nil, err
			}
		case csvAfterQuote:
			switch value {
			case '"':
				if err := appendByte('"'); err != nil {
					return nil, err
				}
				state = csvQuotedField
			case ',':
				if err := appendField(); err != nil {
					return nil, err
				}
				state = csvFieldStart
			case '\n':
				return finish()
			case '\r':
				next, nextErr := parser.readByte(&recordBytes)
				if nextErr != nil || next != '\n' {
					return nil, errors.New("malformed csv: bare carriage return after a quoted field")
				}
				return finish()
			default:
				return nil, errors.New("malformed csv: unexpected data after a closing quote")
			}
		}
	}
}

func (parser *strictCSVParser) consumeLeadingBOM(recordBytes *int) error {
	if parser.recordsRead != 0 || parser.bytesRead != 0 {
		return nil
	}
	prefix, _ := parser.reader.Peek(3)
	if len(prefix) != 3 || prefix[0] != 0xef || prefix[1] != 0xbb || prefix[2] != 0xbf {
		return nil
	}
	discarded, err := parser.reader.Discard(3)
	if err != nil {
		return err
	}
	parser.bytesRead += int64(discarded)
	*recordBytes += discarded
	if parser.bytesRead > defaultMaxCSVBytes {
		return fmt.Errorf("csv file exceeds the %d-byte limit", defaultMaxCSVBytes)
	}
	if *recordBytes > csvMaxRecordBytes {
		return fmt.Errorf("csv record exceeds the %d-byte limit", csvMaxRecordBytes)
	}
	return nil
}

func validateStrictCSVImport(file *os.File) error {
	if file == nil {
		return errors.New("csv import file is unavailable")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	parser := newStrictCSVParser(file)
	records := 0
	for {
		_, err := parser.ReadRecord()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		records++
	}
	if records == 0 {
		return errors.New("csv data is empty")
	}
	_, err := file.Seek(0, io.SeekStart)
	return err
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
