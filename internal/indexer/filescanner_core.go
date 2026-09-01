package indexer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	iofs "io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charlievieth/fastwalk"
	_ "github.com/mattn/go-sqlite3"
)

var defaultSkipDirs = map[string]bool{
	"node_modules": true,
	"var":          true,
	"vendor-bin":   true,
	"bin":          true,
	"cache":        true,
	"dist":         true,
	".tmp":         true,
	".git":         true,
	".github":      true,
	".gitlab":      true,
	".claude":      true,
	".codex":       true,
	".continue":    true,
	".cursor":      true,
	".delta":       true,
	".windsurf":    true,
	".run":         true,
	".idea":        true,
	".vscode":      true,
	".fleet":       true,
	".zed":         true,
	"tests":        true,
	"public":       true,
	".ddev":        true,
	".devbox":      true,
	".devenv":      true,
	".direnv":      true,
}

const (
	DefaultMaxFileSizeBytes = int64(8 << 20)
	MaximumSourceFileBytes  = int64(^uint32(0))
	fileStatsLimit          = 10

	skippedConfiguredLimit = "configured file size limit"
	skippedParserRange     = "32-bit parser source range"
)

// FileScanner scans the project for files and tracks changes
type FileScanner struct {
	platformWatcherState
	projectRoot string
	pharCache   string
	db          *sql.DB
	store       *Store
	symbols     *WorkspaceSymbolCatalog
	indexer     []Indexer
	watcherCtx  context.Context
	cancel      context.CancelFunc
	watcherWg   sync.WaitGroup
	onUpdate    func()
	workerCount int
	exclusions  PathExclusions
	maxFileSize atomic.Int64
	statsMu     sync.RWMutex
	skipped     map[string]SkippedFileStats
	operationMu sync.Mutex
	closeOnce   sync.Once
	closeErr    error
}

type FileScannerStats struct {
	TrackedFiles          int                `json:"trackedFiles"`
	TrackedBytes          int64              `json:"trackedBytes"`
	Indexers              int                `json:"indexers"`
	Workers               int                `json:"workers"`
	MaxFileSizeBytes      int64              `json:"maxFileSizeBytes"`
	SkippedOversizedCount int                `json:"skippedOversizedCount"`
	SkippedOversizedBytes int64              `json:"skippedOversizedBytes"`
	LargestIndexedFiles   []FileSizeStats    `json:"largestIndexedFiles,omitempty"`
	LargestSkippedFiles   []SkippedFileStats `json:"largestSkippedFiles,omitempty"`
}

type FileSizeStats struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

type SkippedFileStats struct {
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	LimitBytes int64  `json:"limitBytes"`
	Reason     string `json:"reason"`
}

// NewFileScanner creates a new file scanner
func NewFileScanner(projectRoot string, dbPath string, stores ...*Store) (*FileScanner, error) {
	// Ensure parent directory exists for the DB file
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	// Open the database with WAL mode for concurrent access
	// Using _txlock=immediate to acquire locks early and avoid SQLITE_BUSY
	db, err := sql.Open("sqlite3", dbPath+"?_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Enable WAL mode and set pragmas for concurrent access and optimization
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		// SQLite interprets a negative cache size as KiB. File hash scans are
		// sequential and do not benefit from retaining a large page cache.
		"PRAGMA cache_size=-8192",
		"PRAGMA auto_vacuum=INCREMENTAL",
		"PRAGMA wal_autocheckpoint=1000",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to set pragma %s: %w", pragma, err)
		}
	}

	// Create the table if it doesn't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS file_hashes (
			path TEXT PRIMARY KEY,
			size INTEGER NOT NULL,
			mtime INTEGER NOT NULL
		)
	`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to initialize tables: %w", err)
	}

	// Create a new context for the watcher
	ctx, cancel := context.WithCancel(context.Background())

	scanner := &FileScanner{
		projectRoot: projectRoot,
		pharCache:   filepath.Join(filepath.Dir(dbPath), "phar-sources"),
		db:          db,
		indexer:     []Indexer{},
		skipped:     make(map[string]SkippedFileStats),
		watcherCtx:  ctx,
		cancel:      cancel,
	}
	scanner.maxFileSize.Store(DefaultMaxFileSizeBytes)
	if len(stores) > 0 {
		scanner.store = stores[0]
	}
	return scanner, nil
}

func (fs *FileScanner) SetOnUpdate(onUpdate func()) {
	fs.onUpdate = onUpdate
}

func (fs *FileScanner) SetWorkspaceSymbolCatalog(
	catalog *WorkspaceSymbolCatalog,
) {
	fs.symbols = catalog
}

func (fs *FileScanner) Stats(ctx context.Context) (FileScannerStats, error) {
	if fs == nil || fs.db == nil {
		return FileScannerStats{}, fmt.Errorf("file scanner is closed")
	}
	var stats FileScannerStats
	if err := fs.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*), COALESCE(SUM(size), 0) FROM file_hashes",
	).Scan(&stats.TrackedFiles, &stats.TrackedBytes); err != nil {
		return FileScannerStats{}, fmt.Errorf("query tracked file statistics: %w", err)
	}
	rows, err := fs.db.QueryContext(
		ctx,
		"SELECT path, size FROM file_hashes ORDER BY size DESC, path LIMIT ?",
		fileStatsLimit,
	)
	if err != nil {
		return FileScannerStats{}, fmt.Errorf("query largest tracked files: %w", err)
	}
	for rows.Next() {
		var file FileSizeStats
		if err := rows.Scan(&file.Path, &file.Bytes); err != nil {
			_ = rows.Close()
			return FileScannerStats{}, fmt.Errorf("scan largest tracked file: %w", err)
		}
		stats.LargestIndexedFiles = append(stats.LargestIndexedFiles, file)
	}
	if err := rows.Close(); err != nil {
		return FileScannerStats{}, fmt.Errorf("close largest tracked files: %w", err)
	}
	if err := rows.Err(); err != nil {
		return FileScannerStats{}, fmt.Errorf("iterate largest tracked files: %w", err)
	}
	stats.MaxFileSizeBytes = fs.maxFileSize.Load()
	stats.SkippedOversizedCount, stats.SkippedOversizedBytes,
		stats.LargestSkippedFiles = fs.skippedFileStats()
	stats.Indexers = len(fs.indexer)
	stats.Workers = defaultIndexWorkerCount(runtime.NumCPU())
	fs.operationMu.Lock()
	if fs.workerCount > 0 {
		stats.Workers = fs.workerCount
	}
	fs.operationMu.Unlock()
	return stats, nil
}

// SetMaxFileSizeBytes sets the configurable source-file indexing limit. Zero
// disables the configurable ceiling; the parser's 32-bit range remains a hard
// limit. It must be called before indexing or starting the watcher.
func (fs *FileScanner) SetMaxFileSizeBytes(size int64) {
	if size < 0 {
		size = 0
	}
	fs.maxFileSize.Store(size)
}

func (fs *FileScanner) sourceSizeRejection(size int64) (string, int64, bool) {
	if size > MaximumSourceFileBytes {
		return skippedParserRange, MaximumSourceFileBytes, true
	}
	limit := fs.maxFileSize.Load()
	if limit > 0 && size > limit {
		return skippedConfiguredLimit, limit, true
	}
	return "", 0, false
}

func (fs *FileScanner) skippedFileStats() (int, int64, []SkippedFileStats) {
	fs.statsMu.RLock()
	files := make([]SkippedFileStats, 0, len(fs.skipped))
	var bytes int64
	for _, file := range fs.skipped {
		files = append(files, file)
		bytes += file.Bytes
	}
	fs.statsMu.RUnlock()
	sort.Slice(files, func(left, right int) bool {
		if files[left].Bytes == files[right].Bytes {
			return files[left].Path < files[right].Path
		}
		return files[left].Bytes > files[right].Bytes
	})
	return len(files), bytes, files[:min(len(files), fileStatsLimit)]
}

func (fs *FileScanner) replaceSkippedFiles(files map[string]SkippedFileStats) {
	fs.statsMu.Lock()
	fs.skipped = files
	fs.statsMu.Unlock()
}

func (fs *FileScanner) recordSkippedFiles(files []SkippedFileStats) {
	if len(files) == 0 {
		return
	}
	fs.statsMu.Lock()
	if fs.skipped == nil {
		fs.skipped = make(map[string]SkippedFileStats)
	}
	for _, file := range files {
		fs.skipped[file.Path] = file
	}
	fs.statsMu.Unlock()
}

func (fs *FileScanner) clearSkippedFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	fs.statsMu.Lock()
	for _, path := range paths {
		delete(fs.skipped, path)
	}
	fs.statsMu.Unlock()
}

func (fs *FileScanner) refreshSkippedFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	fs.statsMu.Lock()
	defer fs.statsMu.Unlock()
	if fs.skipped == nil {
		fs.skipped = make(map[string]SkippedFileStats)
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || !fs.shouldIndexPath(path) {
			delete(fs.skipped, path)
			continue
		}
		reason, limit, rejected := fs.sourceSizeRejection(info.Size())
		if !rejected {
			delete(fs.skipped, path)
			continue
		}
		fs.skipped[path] = SkippedFileStats{
			Path: path, Bytes: info.Size(), LimitBytes: limit, Reason: reason,
		}
	}
}

// SetWorkerCount overrides the automatic indexing concurrency. A value of
// zero restores the default. The setting takes effect on the next operation.
func (fs *FileScanner) SetWorkerCount(count int) {
	if count < 0 {
		count = 0
	}
	fs.operationMu.Lock()
	fs.workerCount = count
	fs.operationMu.Unlock()
}

// SetExcludedPaths installs the immutable workspace-relative exclusion policy.
// It must be called before indexing or starting the watcher.
func (fs *FileScanner) SetExcludedPaths(patterns []string) error {
	exclusions, err := NewPathExclusions(patterns)
	if err != nil {
		return fmt.Errorf("configure excluded paths: %w", err)
	}
	fs.exclusions = exclusions
	return nil
}

func (fs *FileScanner) AddIndexer(indexer Indexer) {
	fs.indexer = append(fs.indexer, indexer)
}

func shouldSkipRelPath(relPath string) bool {
	if relPath == "" || relPath == "." {
		return false
	}

	separator := byte(os.PathSeparator)
	insideVendor := false
	for {
		part := relPath
		if position := strings.IndexByte(relPath, separator); position >= 0 {
			part = relPath[:position]
			relPath = relPath[position+1:]
		} else {
			relPath = ""
		}
		if part == "vendor" {
			insideVendor = true
		}
		// Composer package names and source trees can legitimately contain
		// directories such as symfony/cache. Root/application cache-like
		// directories remain excluded, while dependency source stays visible.
		skipInsideVendor := part == "cache" ||
			part == "bin" ||
			part == "dist" ||
			part == "public" ||
			part == "var"
		if defaultSkipDirs[part] && (!insideVendor || !skipInsideVendor) {
			return true
		}
		if relPath == "" {
			return false
		}
	}
}

// ShouldSkipRelativePath reports whether a path relative to a scan root is in
// a generated, cache, tooling, or otherwise excluded directory. It is exposed
// for commands that need to discover the same source-file set as the scanner.
func ShouldSkipRelativePath(relPath string) bool {
	return shouldSkipRelPath(relPath)
}

// Close closes the database and stops the file watcher
func (fs *FileScanner) Close() error {
	fs.closeOnce.Do(func() {
		fs.StopWatcher()
		fs.operationMu.Lock()
		defer fs.operationMu.Unlock()
		if fs.db == nil {
			return
		}
		_, _ = fs.db.Exec("PRAGMA optimize")
		_, _ = fs.db.Exec("PRAGMA incremental_vacuum")
		_, _ = fs.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		fs.closeErr = fs.db.Close()
	})
	return fs.closeErr
}

func (fs *FileScanner) IndexAll(ctx context.Context) error {
	// A previously warm cache must not advertise a complete generation while
	// the filesystem reconciliation that may invalidate it is still running.
	// Request-time consumers use this marker to reject destructive previews
	// against partial or stale indexes.
	if fs.symbols != nil {
		if err := fs.symbols.SetReady(ctx, false); err != nil {
			return fmt.Errorf("mark workspace indexes rebuilding: %w", err)
		}
	}
	files, err := fs.discoverFiles(ctx)
	if err != nil {
		return err
	}
	storedStates, stale, err := fs.scanFileStates(ctx, files, true)
	if err != nil {
		return fmt.Errorf("load tracked file states: %w", err)
	}
	if len(stale) > 0 {
		if err := fs.RemoveFiles(ctx, stale); err != nil {
			return fmt.Errorf("remove stale index entries: %w", err)
		}
	}

	log.Printf("Found %d files to index", len(files))

	startTime := time.Now()

	if fs.symbols != nil {
		fs.symbols.BeginBulkPopulation()
	}
	indexErr := fs.indexFiles(ctx, files, false, storedStates)
	var symbolErr error
	if fs.symbols != nil {
		symbolErr = fs.symbols.EndBulkPopulation(ctx)
	}
	if err := errors.Join(indexErr, symbolErr); err != nil {
		return fmt.Errorf("failed to index files: %w", err)
	}
	if fs.symbols != nil {
		if err := fs.symbols.SetReady(ctx, true); err != nil {
			return fmt.Errorf("mark workspace symbol catalog ready: %w", err)
		}
	}

	log.Printf("Indexing took %s", time.Since(startTime))

	return nil
}

func (fs *FileScanner) discoverFiles(ctx context.Context) ([]string, error) {
	var capacity int
	if err := fs.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM file_hashes",
	).Scan(&capacity); err != nil {
		return nil, fmt.Errorf("count tracked files: %w", err)
	}
	files := make([]string, 0, capacity)
	var archives []string
	var filesMu sync.Mutex
	config := &fastwalk.Config{
		NumWorkers: defaultIndexWorkerCount(runtime.NumCPU()),
	}
	err := fastwalk.Walk(config, fs.projectRoot, func(
		path string,
		entry iofs.DirEntry,
		err error,
	) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return fmt.Errorf("access %s: %w", path, err)
		}

		if entry.IsDir() {
			if !fs.shouldEnterDirectory(path) {
				return fastwalk.SkipDir
			}
			return nil
		}

		if isPHARArchivePath(path) && !fs.isExplicitlyExcluded(path, false) {
			filesMu.Lock()
			archives = append(archives, path)
			filesMu.Unlock()
		} else if fs.shouldIndexPath(path) {
			filesMu.Lock()
			files = append(files, path)
			filesMu.Unlock()
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk project directory: %w", err)
	}
	archiveFiles, err := fs.discoverPHARSources(ctx, archives)
	if err != nil {
		return nil, err
	}
	files = append(files, archiveFiles...)
	// A complete scan rebuilds skipped-file telemetry from the same Stat calls
	// workers already need, avoiding another metadata syscall for every source.
	fs.replaceSkippedFiles(make(map[string]SkippedFileStats))
	slices.Sort(files)
	return files, nil
}

type storedFileState struct {
	size  int64
	mtime int64
	found bool
}

func (fs *FileScanner) loadFileStates(
	ctx context.Context,
	files []string,
) ([]storedFileState, error) {
	states, _, err := fs.scanFileStates(ctx, files, false)
	return states, err
}

// scanFileStates aligns the ordered SQLite state with the ordered discovery
// result in one pass. IndexAll also collects stored paths absent from discovery
// so it does not scan the file_hashes table a second time to find deletions.
func (fs *FileScanner) scanFileStates(
	ctx context.Context,
	files []string,
	collectStale bool,
) ([]storedFileState, []string, error) {
	rows, err := fs.db.QueryContext(
		ctx,
		"SELECT path, size, mtime FROM file_hashes ORDER BY path",
	)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	states := make([]storedFileState, len(files))
	var stale []string
	fileIndex := 0
	for rows.Next() {
		var path sql.RawBytes
		var state storedFileState
		if err := rows.Scan(&path, &state.size, &state.mtime); err != nil {
			return nil, nil, err
		}
		matched := false
		for fileIndex < len(files) {
			compared := compareRawPath(path, files[fileIndex])
			if compared < 0 {
				break
			}
			if compared == 0 {
				state.found = true
				states[fileIndex] = state
				matched = true
				break
			}
			fileIndex++
		}
		if collectStale && !matched {
			// RawBytes is only valid until the next Rows call.
			stale = append(stale, string(path))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return states, stale, nil
}

func compareRawPath(left []byte, right string) int {
	length := min(len(left), len(right))
	for index := 0; index < length; index++ {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
}

// fileNeedsIndexing checks if a file needs to be indexed. A non-nil state
// snapshot avoids one SQLite query per file during large workspace scans.
func (fs *FileScanner) fileNeedsIndexing(
	ctx context.Context,
	path string,
	state *storedFileState,
) (bool, []byte, os.FileInfo, *SkippedFileStats, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, nil, nil, nil, false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, nil, nil, nil, false, err
	}

	var stored storedFileState
	var exists bool
	if state != nil {
		stored = *state
		exists = stored.found
	} else {
		err = fs.db.QueryRowContext(
			ctx,
			"SELECT size, mtime FROM file_hashes WHERE path = ?",
			path,
		).Scan(&stored.size, &stored.mtime)
		switch err {
		case nil:
			exists = true
		case sql.ErrNoRows:
		default:
			return false, nil, info, nil, false,
				fmt.Errorf("query file state: %w", err)
		}
	}
	if reason, limit, rejected := fs.sourceSizeRejection(info.Size()); rejected {
		return false, nil, info, &SkippedFileStats{
			Path: path, Bytes: info.Size(), LimitBytes: limit, Reason: reason,
		}, exists, nil
	}

	if exists &&
		stored.size == info.Size() &&
		stored.mtime == info.ModTime().UnixNano() {
		return false, nil, info, nil, false, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return false, nil, info, nil, false, err
	}
	// The file may have grown between Stat and ReadFile. Check the actual
	// allocation before handing it to a parser.
	if reason, limit, rejected := fs.sourceSizeRejection(int64(len(content))); rejected {
		return false, nil, info, &SkippedFileStats{
			Path: path, Bytes: int64(len(content)), LimitBytes: limit, Reason: reason,
		}, exists, nil
	}

	return true, content, info, nil, false, nil
}

// RemoveFiles removes multiple files from the index
func (fs *FileScanner) RemoveFiles(ctx context.Context, paths []string) error {
	fs.operationMu.Lock()
	defer fs.operationMu.Unlock()
	if err := fs.removeFilesLocked(ctx, paths); err != nil {
		return err
	}
	fs.refreshSkippedFiles(paths)
	return nil
}

func (fs *FileScanner) removeFilesLocked(ctx context.Context, paths []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}

	var mutation *Mutation
	if fs.store != nil {
		transactional := true
		for _, idx := range fs.indexer {
			if _, ok := idx.(TransactionalRemover); !ok {
				transactional = false
				break
			}
		}
		if transactional {
			var err error
			mutation, err = fs.store.BeginMutation(ctx)
			if err != nil {
				return err
			}
		}
	}

	var removeErrors []error
	for _, idx := range fs.indexer {
		if err := ctx.Err(); err != nil {
			removeErrors = append(removeErrors, err)
			break
		}
		var err error
		if remover, ok := idx.(TransactionalRemover); mutation != nil && ok {
			err = remover.RemovedFilesIn(paths, mutation)
		} else {
			err = idx.RemovedFiles(paths)
		}
		if err != nil {
			removeErrors = append(removeErrors, fmt.Errorf("%s: %w", idx.ID(), err))
		}
	}
	if err := errors.Join(removeErrors...); err != nil {
		if mutation != nil {
			err = errors.Join(err, mutation.Rollback())
		}
		return fmt.Errorf("remove index entries: %w", err)
	}
	if mutation != nil {
		if fs.symbols != nil {
			if err := fs.symbols.DeleteFilesIn(mutation, paths); err != nil {
				return errors.Join(err, mutation.Rollback())
			}
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(err, mutation.Rollback())
		}
		if err := mutation.Commit(); err != nil {
			return err
		}
	} else if fs.symbols != nil {
		if err := fs.symbols.DeleteFiles(ctx, paths); err != nil {
			return err
		}
	}

	tx, err := fs.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const batchSize = 128
	firstBatchCount := min(len(paths), batchSize)
	stmt, err := tx.PrepareContext(
		ctx,
		inClauseSQL("DELETE FROM file_hashes WHERE path IN (", firstBatchCount),
	)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	var tailStmt *sql.Stmt
	defer func() {
		if tailStmt != nil {
			_ = tailStmt.Close()
		}
	}()
	args := make([]any, firstBatchCount)
	for start := 0; start < len(paths); start += batchSize {
		end := min(start+batchSize, len(paths))
		batch := paths[start:end]
		currentStmt := stmt
		if len(batch) != firstBatchCount {
			tailStmt, err = tx.PrepareContext(
				ctx,
				inClauseSQL("DELETE FROM file_hashes WHERE path IN (", len(batch)),
			)
			if err != nil {
				return err
			}
			currentStmt = tailStmt
		}
		currentArgs := args[:len(batch)]
		for index, path := range batch {
			currentArgs[index] = path
		}
		if _, err := currentStmt.ExecContext(ctx, currentArgs...); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if fs.onUpdate != nil {
		fs.onUpdate()
	}

	return nil
}

func logSkippedFile(file SkippedFileStats) {
	log.Printf(
		"Skipping oversized source file %s (%d bytes): exceeds %s (%d bytes)",
		file.Path,
		file.Bytes,
		file.Reason,
		file.LimitBytes,
	)
}

func logSkippedFileCount(count int) {
	log.Printf(
		"Skipped %d oversized source files; use stats for the largest files",
		count,
	)
}

func (fs *FileScanner) updateFileStates(ctx context.Context, files []fileState) error {
	if len(files) == 0 {
		return nil
	}
	tx, err := fs.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const batchSize = 128
	firstBatchCount := min(len(files), batchSize)
	stmt, err := tx.PrepareContext(ctx, fileStateUpsertSQL(firstBatchCount))
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	var tailStmt *sql.Stmt
	defer func() {
		if tailStmt != nil {
			_ = tailStmt.Close()
		}
	}()
	args := make([]any, firstBatchCount*3)
	for start := 0; start < len(files); start += batchSize {
		end := min(start+batchSize, len(files))
		batch := files[start:end]
		currentStmt := stmt
		if len(batch) != firstBatchCount {
			tailStmt, err = tx.PrepareContext(
				ctx,
				fileStateUpsertSQL(len(batch)),
			)
			if err != nil {
				return err
			}
			currentStmt = tailStmt
		}
		currentArgs := args[:len(batch)*3]
		for index, file := range batch {
			offset := index * 3
			currentArgs[offset] = file.path
			currentArgs[offset+1] = file.info.Size()
			currentArgs[offset+2] = file.info.ModTime().UnixNano()
		}
		if _, err := currentStmt.ExecContext(ctx, currentArgs...); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func fileStateUpsertSQL(count int) string {
	const prefix = "INSERT OR REPLACE INTO file_hashes (path, size, mtime) VALUES "
	const tuple = "(?, ?, ?)"
	var query strings.Builder
	query.Grow(len(prefix) + count*(len(tuple)+1))
	query.WriteString(prefix)
	for index := 0; index < count; index++ {
		if index != 0 {
			query.WriteByte(',')
		}
		query.WriteString(tuple)
	}
	return query.String()
}

type fileState struct {
	path string
	info os.FileInfo
}

type fileWork struct {
	path    string
	content []byte
	info    os.FileInfo
}

type preparedFileWork struct {
	file     *ParsedFile
	info     os.FileInfo
	prepared []any
}

const (
	// Bound preparation by source weight as well as file count. Tiny files can
	// still share an efficient transaction, while one generated container file
	// no longer keeps dozens of syntax trees alive until a 50-file batch fills.
	maxPreparationBatchFiles = 50
	maxPreparationBatchBytes = 128 << 10
)

func preparationBatchReady(fileCount, sourceBytes int) bool {
	return fileCount >= maxPreparationBatchFiles ||
		sourceBytes >= maxPreparationBatchBytes
}

func preparationBatchWouldOverflow(
	fileCount,
	sourceBytes,
	nextSourceBytes int,
) bool {
	return fileCount > 0 &&
		(fileCount+1 > maxPreparationBatchFiles ||
			sourceBytes+nextSourceBytes > maxPreparationBatchBytes)
}

// IndexFiles processes multiple files in parallel
func (fs *FileScanner) IndexFiles(
	ctx context.Context,
	files []string,
) error {
	return fs.indexFiles(ctx, files, true, nil)
}

func defaultIndexWorkerCount(cpuCount int) int {
	return min(max(cpuCount, 1), 4)
}

func (fs *FileScanner) shouldEnterDirectory(path string) bool {
	if pathWithin(fs.pharCache, path) {
		return false
	}
	relative, within := relativePathWithin(fs.projectRoot, path)
	if within && fs.exclusions.ExcludesDirectory(relative) {
		return false
	}
	if !within || !shouldSkipRelPath(relative) {
		return true
	}
	for _, idx := range fs.indexer {
		if selector, ok := idx.(SupplementalPathIndexer); ok &&
			selector.ShouldEnterDirectory(path) {
			return true
		}
	}
	return false
}

func (fs *FileScanner) shouldIndexPath(path string) bool {
	if pathWithin(fs.pharCache, path) && isScannedPath(path) {
		return true
	}
	relative, within := relativePathWithin(fs.projectRoot, path)
	if within && fs.exclusions.Excludes(relative) {
		return false
	}
	if within &&
		!shouldSkipRelPath(relative) &&
		isScannedPath(path) {
		return true
	}
	for _, idx := range fs.indexer {
		if selector, ok := idx.(SupplementalPathIndexer); ok &&
			selector.ShouldIndexPath(path) {
			return true
		}
	}
	return false
}

func (fs *FileScanner) shouldPreparsePath(path string) bool {
	if pathWithin(fs.pharCache, path) && isScannedPath(path) {
		return true
	}
	relative, within := relativePathWithin(fs.projectRoot, path)
	if within && fs.exclusions.Excludes(relative) {
		return false
	}
	if within &&
		!shouldSkipRelPath(relative) &&
		isScannedPath(path) {
		return true
	}
	for _, idx := range fs.indexer {
		selector, selected := idx.(SupplementalPathIndexer)
		if !selected || !selector.ShouldIndexPath(path) {
			continue
		}
		if syntaxIndexer, ok := idx.(SupplementalSyntaxIndexer); ok &&
			syntaxIndexer.ShouldPreparsePath(path) {
			return true
		}
	}
	return false
}

func (fs *FileScanner) isExplicitlyExcluded(path string, directory bool) bool {
	relative, within := relativePathWithin(fs.projectRoot, path)
	if !within {
		return false
	}
	if directory {
		return fs.exclusions.ExcludesDirectory(relative)
	}
	return fs.exclusions.Excludes(relative)
}

// ClearHashes clears all file hashes, forcing reindexing
func (fs *FileScanner) ClearHashes() error {
	fs.operationMu.Lock()
	defer fs.operationMu.Unlock()

	var mutation *Mutation
	if fs.store != nil {
		transactional := true
		for _, idx := range fs.indexer {
			if _, ok := idx.(TransactionalClearer); !ok {
				transactional = false
				break
			}
		}
		if transactional {
			var err error
			mutation, err = fs.store.BeginMutation(context.Background())
			if err != nil {
				return err
			}
		}
	}

	var clearErrors []error
	for _, idx := range fs.indexer {
		var err error
		if clearer, ok := idx.(TransactionalClearer); mutation != nil && ok {
			err = clearer.ClearIn(mutation)
		} else {
			err = idx.Clear()
		}
		if err != nil {
			clearErrors = append(clearErrors, fmt.Errorf("%s: %w", idx.ID(), err))
		}
	}
	if err := errors.Join(clearErrors...); err != nil {
		if mutation != nil {
			err = errors.Join(err, mutation.Rollback())
		}
		return fmt.Errorf("clear indexes: %w", err)
	}
	if mutation != nil {
		if fs.symbols != nil {
			if err := fs.symbols.ClearIn(mutation); err != nil {
				return errors.Join(err, mutation.Rollback())
			}
		}
		if err := mutation.Commit(); err != nil {
			return err
		}
	} else if fs.symbols != nil {
		if err := fs.symbols.Clear(context.Background()); err != nil {
			return err
		}
	}

	_, err := fs.db.Exec("DELETE FROM file_hashes")
	if err != nil {
		return err
	}

	// Reclaim space after clearing all data
	_, err = fs.db.Exec("PRAGMA incremental_vacuum")
	return err
}
