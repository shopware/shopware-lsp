package indexer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// Store owns the single SQLite connection used by all repositories in a
// workspace. Repository data is isolated by namespace.
type Store struct {
	db           *sql.DB
	mutationGate chan struct{}
	closeOnce    sync.Once
	closeErr     error
}

type mutationCache interface {
	invalidateMutationCache()
}

// Mutation is a file-scoped transaction shared by every namespaced
// repository in one workspace store.
type Mutation struct {
	store       *Store
	tx          *sql.Tx
	statements  map[string]*sql.Stmt
	touched     map[mutationCache]struct{}
	afterCommit []func()
	deletePaths []string
	deleteArgs  [][]any
	deleteBatch int
	done        bool
}

func NewStore(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite3", dbPath+"?_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open index database: %w", err)
	}
	// The mutation gate below still permits exactly one writer. A small
	// connection pool is required because index preparation may consult an
	// already-committed repository (for example Symfony service types) while
	// the same goroutine owns the current WAL write transaction.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		// A negative cache size is expressed in KiB. Eight MiB keeps the
		// resident native SQLite cache bounded without constraining the Go
		// object indexes that serve interactive reads after indexing.
		"PRAGMA cache_size=-8192",
		"PRAGMA foreign_keys=ON",
		"PRAGMA auto_vacuum=INCREMENTAL",
		"PRAGMA wal_autocheckpoint=1000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set pragma %s: %w", pragma, err)
		}
	}

	legacySchema, err := storeUsesLegacyFileTable(db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("inspect index database schema: %w", err)
	}
	if legacySchema {
		if _, err := db.Exec(`
			DROP TABLE IF EXISTS files;
			DROP TABLE IF EXISTS data;
		`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("reset legacy index database schema: %w", err)
		}
	}

	if _, err := db.Exec(`
		DROP TABLE IF EXISTS files;

		CREATE TABLE IF NOT EXISTS data (
			namespace TEXT NOT NULL,
			file_path TEXT NOT NULL,
			key TEXT NOT NULL,
			value BLOB NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_data_namespace_key ON data(namespace, key);
		CREATE INDEX IF NOT EXISTS idx_data_namespace_path ON data(namespace, file_path);
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize index database: %w", err)
	}
	store := &Store{
		db:           db,
		mutationGate: make(chan struct{}, 1),
	}
	store.mutationGate <- struct{}{}
	return store, nil
}

func storeUsesLegacyFileTable(db *sql.DB) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(data)")
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()

	hasColumns := false
	hasFilePath := false
	for rows.Next() {
		var (
			columnIndex int
			name        string
			columnType  string
			notNull     int
			defaultSQL  sql.NullString
			primaryKey  int
		)
		if err := rows.Scan(
			&columnIndex,
			&name,
			&columnType,
			&notNull,
			&defaultSQL,
			&primaryKey,
		); err != nil {
			return false, err
		}
		hasColumns = true
		if name == "file_path" {
			hasFilePath = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return hasColumns && !hasFilePath, nil
}

func (s *Store) BeginMutation(ctx context.Context) (*Mutation, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("index store is required")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.mutationGate:
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.mutationGate <- struct{}{}
		return nil, fmt.Errorf("begin index mutation: %w", err)
	}
	return &Mutation{
		store: s,
		tx:    tx,
	}, nil
}

// Prepare returns a transaction-scoped cached statement. Repository
// replacements share a handful of SQL shapes across every namespace and file
// in a mutation batch, so preparing them once avoids repeated database/sql and
// SQLite statement construction.
func (m *Mutation) Prepare(query string) (*sql.Stmt, error) {
	if m == nil || m.done || m.tx == nil {
		return nil, fmt.Errorf("index mutation is not active")
	}
	if statement := m.statements[query]; statement != nil {
		return statement, nil
	}
	if m.statements == nil {
		m.statements = make(map[string]*sql.Stmt)
	}
	statement, err := m.tx.Prepare(query)
	if err != nil {
		return nil, err
	}
	m.statements[query] = statement
	return statement, nil
}

// cachedStatements reports that callers must leave statements open until the
// mutation commits or rolls back.
func (m *Mutation) cachedStatements() bool {
	return true
}

func (m *Mutation) closeStatements() {
	for query, statement := range m.statements {
		_ = statement.Close()
		delete(m.statements, query)
	}
}

func (m *Mutation) addCache(cache mutationCache) error {
	if m == nil || m.done || m.tx == nil {
		return fmt.Errorf("index mutation is not active")
	}
	if m.touched == nil {
		m.touched = make(map[mutationCache]struct{})
	}
	m.touched[cache] = struct{}{}
	return nil
}

func (m *Mutation) deleteArguments(
	filePaths []string,
	batchSize int,
) [][]any {
	if m.deleteBatch == batchSize &&
		slices.Equal(m.deletePaths, filePaths) &&
		len(m.deleteArgs) > 0 {
		return m.deleteArgs
	}

	m.deletePaths = append(m.deletePaths[:0], filePaths...)
	m.deleteBatch = batchSize
	batchCount := (len(filePaths) + batchSize - 1) / batchSize
	m.deleteArgs = make([][]any, 0, batchCount)
	for start := 0; start < len(filePaths); start += batchSize {
		end := min(start+batchSize, len(filePaths))
		args := make([]any, end-start+1)
		for index, filePath := range filePaths[start:end] {
			args[index+1] = filePath
		}
		m.deleteArgs = append(m.deleteArgs, args)
	}
	return m.deleteArgs
}

// AfterCommit registers an in-memory publication callback. It is only invoked
// after the SQLite transaction and all repository cache invalidations succeed.
// Rollback discards callbacks.
func (m *Mutation) AfterCommit(callback func()) error {
	if m == nil || m.done || m.tx == nil {
		return fmt.Errorf("index mutation is not active")
	}
	if callback != nil {
		m.afterCommit = append(m.afterCommit, callback)
	}
	return nil
}

func (m *Mutation) Commit() error {
	if m == nil || m.done {
		return fmt.Errorf("index mutation is not active")
	}
	m.done = true
	m.closeStatements()
	m.deletePaths = nil
	m.deleteArgs = nil
	m.deleteBatch = 0
	err := m.tx.Commit()
	if err == nil {
		for cache := range m.touched {
			cache.invalidateMutationCache()
		}
		for _, callback := range m.afterCommit {
			callback()
		}
	}
	m.store.mutationGate <- struct{}{}
	if err != nil {
		return fmt.Errorf("commit index mutation: %w", err)
	}
	return nil
}

func (m *Mutation) Rollback() error {
	if m == nil || m.done {
		return nil
	}
	m.done = true
	m.closeStatements()
	m.deletePaths = nil
	m.deleteArgs = nil
	m.deleteBatch = 0
	err := m.tx.Rollback()
	m.store.mutationGate <- struct{}{}
	if err != nil && err != sql.ErrTxDone {
		return fmt.Errorf("rollback index mutation: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		<-s.mutationGate
		defer func() { s.mutationGate <- struct{}{} }()
		_, _ = s.db.Exec("PRAGMA optimize")
		_, _ = s.db.Exec("PRAGMA incremental_vacuum")
		_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}
