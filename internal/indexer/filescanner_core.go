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
	"strings"
	"sync"
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
	".run":         true,
	".idea":        true,
	".vscode":      true,
	"tests":        true,
	"public":       true,
	".devenv":      true,
	".direnv":      true,
}

// FileScanner scans the project for files and tracks changes
type FileScanner struct {
	platformWatcherState
	projectRoot  string
	pharCache    string
	db           *sql.DB
	store        *Store
	symbols      *WorkspaceSymbolCatalog
	indexer      []Indexer
	watcherCtx   context.Context
	cancel       context.CancelFunc
	watcherWg    sync.WaitGroup
	watcherMu    sync.Mutex
	nativeEvents chan fileSystemEvent
	onUpdate     func()
	workerCount  int
	operationMu  sync.Mutex
	closeOnce    sync.Once
	closeErr     error
}

type fileSystemEvent struct {
	path  string
	flags uint32
}

type FileScannerStats struct {
	TrackedFiles int   `json:"trackedFiles"`
	TrackedBytes int64 `json:"trackedBytes"`
	Indexers     int   `json:"indexers"`
	Workers      int   `json:"workers"`
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
		watcherCtx:  ctx,
		cancel:      cancel,
	}
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
	stats.Indexers = len(fs.indexer)
	stats.Workers = defaultIndexWorkerCount(runtime.NumCPU())
	fs.operationMu.Lock()
	if fs.workerCount > 0 {
		stats.Workers = fs.workerCount
	}
	fs.operationMu.Unlock()
	return stats, nil
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

		if isPHARArchivePath(path) {
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
) (bool, []byte, os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return false, nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, nil, nil, err
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
			return false, nil, info, fmt.Errorf("query file state: %w", err)
		}
	}

	if exists &&
		stored.size == info.Size() &&
		stored.mtime == info.ModTime().UnixNano() {
		return false, nil, info, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return false, nil, info, err
	}

	return true, content, info, nil
}

// RemoveFiles removes multiple files from the index
func (fs *FileScanner) RemoveFiles(ctx context.Context, paths []string) error {
	fs.operationMu.Lock()
	defer fs.operationMu.Unlock()

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
