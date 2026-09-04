package indexer

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/vmihailenco/msgpack/v5"
)

const (
	maxPooledMessagePackBuffer = 1 << 20
	messagePackBufferPoolSize  = 8
)

var messagePackEncoders = make(
	chan *messagePackEncoder,
	messagePackBufferPoolSize,
)

type messagePackEncoder struct {
	buffer  *bytes.Buffer
	encoder *msgpack.Encoder
}

func acquireMessagePackEncoder() *messagePackEncoder {
	select {
	case encoder := <-messagePackEncoders:
		return encoder
	default:
		return &messagePackEncoder{
			buffer:  &bytes.Buffer{},
			encoder: msgpack.GetEncoder(),
		}
	}
}

func (encoder *messagePackEncoder) encode(value any) ([]byte, error) {
	encoder.buffer.Reset()
	encoder.encoder.Reset(encoder.buffer)
	if err := encoder.encoder.Encode(value); err != nil {
		return nil, err
	}
	return encoder.buffer.Bytes(), nil
}

func (encoder *messagePackEncoder) release() {
	if encoder == nil || encoder.buffer == nil || encoder.encoder == nil {
		return
	}
	if encoder.buffer.Cap() <= maxPooledMessagePackBuffer {
		encoder.buffer.Reset()
		select {
		case messagePackEncoders <- encoder:
			return
		default:
		}
	}
	discardMessagePackEncoder(encoder)
}

func discardMessagePackEncoder(encoder *messagePackEncoder) {
	if encoder == nil {
		return
	}
	if encoder.encoder != nil {
		msgpack.PutEncoder(encoder.encoder)
		encoder.encoder = nil
	}
	encoder.buffer = nil
}

func clearMessagePackBuffers() {
	for {
		select {
		case encoder := <-messagePackEncoders:
			discardMessagePackEncoder(encoder)
		default:
			return
		}
	}
}

// DataIndexer is a generic indexer that can store any type of data in a SQLite database
type DataIndexer[T any] struct {
	db              *sql.DB
	mu              sync.RWMutex
	store           *Store
	namespace       string
	ownsStore       bool
	closeOnce       sync.Once
	closeErr        error
	valuesCache     map[string][]T
	allCache        []T
	allCacheValid   bool
	keysCache       []string
	keysCacheValid  bool
	pathKeysCache   map[string][]string
	pathValuesCache map[string][]T
}

func cloneSlice[T any](items []T) []T {
	return append([]T(nil), items...)
}

func (idx *DataIndexer[T]) invalidateCache() {
	idx.valuesCache = nil
	idx.allCache = nil
	idx.allCacheValid = false
	idx.keysCache = nil
	idx.keysCacheValid = false
	idx.pathKeysCache = nil
	idx.pathValuesCache = nil
}

func (idx *DataIndexer[T]) invalidateMutationCache() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.invalidateCache()
}

// NewDataIndexer creates a new generic data indexer
func NewDataIndexer[T any](dbPath string) (*DataIndexer[T], error) {
	store, err := NewStore(dbPath)
	if err != nil {
		return nil, err
	}
	index, err := NewDataIndexerInStore[T](store, "default")
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	index.ownsStore = true
	return index, nil
}

func NewDataIndexerInStore[T any](store *Store, namespace string) (*DataIndexer[T], error) {
	if store == nil || store.db == nil {
		return nil, fmt.Errorf("index store is required")
	}
	if namespace == "" {
		return nil, fmt.Errorf("index namespace is required")
	}
	return &DataIndexer[T]{
		db:        store.db,
		store:     store,
		namespace: namespace,
	}, nil
}

// NewRepository creates a namespaced repository in a workspace store when one
// is provided, otherwise it creates an isolated compatibility database.
func NewRepository[T any](dbPath, namespace string, stores ...*Store) (*DataIndexer[T], error) {
	if len(stores) > 0 && stores[0] != nil {
		return NewDataIndexerInStore[T](stores[0], namespace)
	}
	store, err := NewStore(dbPath)
	if err != nil {
		return nil, err
	}
	index, err := NewDataIndexerInStore[T](store, namespace)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	index.ownsStore = true
	return index, nil
}

// SaveItem saves an item to the database with the given key and associates it with a file path
func (idx *DataIndexer[T]) SaveItem(filePath, key string, item T) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	encoder := acquireMessagePackEncoder()
	defer encoder.release()
	data, err := encoder.encode(item)
	if err != nil {
		return fmt.Errorf("failed to marshal item: %w", err)
	}

	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(
		"INSERT INTO data (namespace, file_path, key, value) VALUES (?, ?, ?, ?)",
		idx.namespace, filePath, key, data,
	)
	if err != nil {
		return fmt.Errorf("failed to save item: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	idx.invalidateCache()
	return nil
}

// BatchSaveItems saves multiple items in a single transaction
// It first deletes any existing entries for the given file paths to avoid duplicates
func (idx *DataIndexer[T]) BatchSaveItems(items map[string]map[string]T) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := idx.batchSaveItems(tx, items); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	idx.invalidateCache()
	return nil
}

// BatchSaveItemsIn applies a replacement inside the file-scoped workspace
// mutation. Passing nil preserves the standalone repository behavior used by
// unit tests and direct callers.
func (idx *DataIndexer[T]) BatchSaveItemsIn(mutation *Mutation, items map[string]map[string]T) error {
	if mutation == nil {
		return idx.BatchSaveItems(items)
	}
	if mutation.store != idx.store {
		return fmt.Errorf("index mutation belongs to a different store")
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if err := idx.batchSaveItems(mutation, items); err != nil {
		return err
	}
	return mutation.addCache(idx)
}

type statementPreparer interface {
	Prepare(query string) (*sql.Stmt, error)
}

type cachedStatementPreparer interface {
	cachedStatements() bool
}

func (idx *DataIndexer[T]) batchSaveItems(executor statementPreparer, items map[string]map[string]T) error {
	// First, delete existing entries for these file paths to avoid duplicates.
	deleteDataStmt, err := executor.Prepare(
		"DELETE FROM data WHERE namespace = ? AND file_path = ?",
	)
	if err != nil {
		return fmt.Errorf("failed to prepare delete data statement: %w", err)
	}
	if !usesCachedStatements(executor) {
		defer func() { _ = deleteDataStmt.Close() }()
	}

	for filePath := range items {
		if _, err := deleteDataStmt.Exec(idx.namespace, filePath); err != nil {
			return fmt.Errorf("failed to delete existing data: %w", err)
		}
	}

	// A replacement with empty item maps is a deliberate delete-only update.
	// Defer both insertion resources until an item actually needs encoding.
	var dataStmt *sql.Stmt
	var encoder *messagePackEncoder
	defer func() {
		if encoder != nil {
			encoder.release()
		}
		if dataStmt != nil && !usesCachedStatements(executor) {
			_ = dataStmt.Close()
		}
	}()
	for filePath, keyItems := range items {
		for key, item := range keyItems {
			if dataStmt == nil {
				dataStmt, err = executor.Prepare(
					"INSERT INTO data (namespace, file_path, key, value) VALUES (?, ?, ?, ?)",
				)
				if err != nil {
					return fmt.Errorf("failed to prepare data statement: %w", err)
				}
				encoder = acquireMessagePackEncoder()
			}
			data, err := encoder.encode(item)
			if err != nil {
				return fmt.Errorf("failed to marshal item: %w", err)
			}

			_, err = dataStmt.Exec(
				idx.namespace,
				filePath,
				key,
				data,
			)
			if err != nil {
				return fmt.Errorf("failed to save item: %w", err)
			}
		}
	}

	return nil
}

func usesCachedStatements(executor statementPreparer) bool {
	cached, ok := executor.(cachedStatementPreparer)
	return ok && cached.cachedStatements()
}

// GetValues returns all items with the given key
func (idx *DataIndexer[T]) GetValues(key string) ([]T, error) {
	items, err := idx.GetValuesView(key)
	if err != nil {
		return nil, err
	}
	return cloneSlice(items), nil
}

// GetValuesView returns the immutable cached values for key. Callers must not
// modify the returned slice or values reachable through it. Read-heavy
// workspace services use this view to avoid cloning large catalogs for every
// request.
func (idx *DataIndexer[T]) GetValuesView(key string) ([]T, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if items, ok := idx.valuesCache[key]; ok {
		return items, nil
	}

	rows, err := idx.db.Query("SELECT value FROM data WHERE namespace = ? AND key = ?", idx.namespace, key)
	if err != nil {
		return nil, fmt.Errorf("failed to query data: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var items []T
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		var item T
		if err := msgpack.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("failed to unmarshal item: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	if idx.valuesCache == nil {
		idx.valuesCache = make(map[string][]T)
	}
	idx.valuesCache[key] = items
	return items, nil
}

// GetAllValues returns all items stored in the data table
func (idx *DataIndexer[T]) GetAllValues() ([]T, error) {
	items, err := idx.GetAllValuesView()
	if err != nil {
		return nil, err
	}
	return cloneSlice(items), nil
}

// GetAllValuesView returns the immutable cached repository catalog. Callers
// must not modify the returned slice or values reachable through it.
func (idx *DataIndexer[T]) GetAllValuesView() ([]T, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.allCacheValid {
		return idx.allCache, nil
	}

	rows, err := idx.db.Query("SELECT value FROM data WHERE namespace = ?", idx.namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to query data: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var items []T
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		var item T
		if len(data) == 0 {
			continue
		}
		if err := msgpack.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("failed to unmarshal item: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	idx.allCache = items
	idx.allCacheValid = true
	return items, nil
}

// VisitAllValues decodes repository values without retaining the complete
// result in the repository cache. It is intended for startup restoration where
// callers immediately project large persisted values into a compact in-memory
// representation.
func (idx *DataIndexer[T]) VisitAllValues(visitor func(T) error) error {
	if visitor == nil {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	rows, err := idx.db.Query("SELECT value FROM data WHERE namespace = ?", idx.namespace)
	if err != nil {
		return fmt.Errorf("failed to query data: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}
		if len(data) == 0 {
			continue
		}
		var item T
		if err := msgpack.Unmarshal(data, &item); err != nil {
			return fmt.Errorf("failed to unmarshal item: %w", err)
		}
		if err := visitor(item); err != nil {
			return err
		}
	}
	return rows.Err()
}

// VisitAllEncodedValues visits each MessagePack repository value without
// decoding it or copying the SQLite row through database/sql. The supplied
// slice is borrowed from sql.Rows and is valid only until visitor returns;
// callers must finish reading it and must not retain it.
func (idx *DataIndexer[T]) VisitAllEncodedValues(
	visitor func([]byte) error,
) error {
	if visitor == nil {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	rows, err := idx.db.Query(
		"SELECT file_path, key, value FROM data WHERE namespace = ?",
		idx.namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to query data: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var filePath, key string
		var data sql.RawBytes
		if err := rows.Scan(&filePath, &key, &data); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}
		if len(data) == 0 {
			continue
		}
		if err := visitor(data); err != nil {
			return fmt.Errorf(
				"visit encoded value %s (%s): %w",
				filePath,
				key,
				err,
			)
		}
	}
	return rows.Err()
}

// VisitEncodedValuesByPath visits the encoded repository values for one source
// path without populating the typed path cache. The supplied slice is borrowed
// from sql.Rows and is valid only until visitor returns.
func (idx *DataIndexer[T]) VisitEncodedValuesByPath(
	filePath string,
	visitor func([]byte) error,
) error {
	if visitor == nil {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	rows, err := idx.db.Query(`
		SELECT key, value FROM data
		WHERE namespace = ? AND file_path = ?
	`, idx.namespace, filePath)
	if err != nil {
		return fmt.Errorf("failed to query encoded values by path: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var key string
		var data sql.RawBytes
		if err := rows.Scan(&key, &data); err != nil {
			return fmt.Errorf("failed to scan encoded value by path: %w", err)
		}
		if len(data) == 0 {
			continue
		}
		if err := visitor(data); err != nil {
			return fmt.Errorf(
				"visit encoded value %s (%s): %w",
				filePath,
				key,
				err,
			)
		}
	}
	return rows.Err()
}

// CountAllValues reports the number of values in this repository namespace.
// Startup loaders can use it to reserve their final in-memory representation
// before streaming values without materializing repository data.
func (idx *DataIndexer[T]) CountAllValues() (int, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var count int
	if err := idx.db.QueryRow(
		"SELECT COUNT(*) FROM data WHERE namespace = ?",
		idx.namespace,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count data: %w", err)
	}
	return count, nil
}

// GetAllKeys returns all unique keys in the database
func (idx *DataIndexer[T]) GetAllKeys() ([]string, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.keysCacheValid {
		return append([]string(nil), idx.keysCache...), nil
	}

	rows, err := idx.db.Query("SELECT DISTINCT key FROM data WHERE namespace = ?", idx.namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to query keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("failed to scan key: %w", err)
		}
		keys = append(keys, key)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	idx.keysCache = append([]string(nil), keys...)
	idx.keysCacheValid = true
	return append([]string(nil), keys...), nil
}

// HasAnyKeyExceptFold reports whether the repository contains a key other
// than the supplied case-insensitive exclusions. Unlike GetAllKeys it does
// not materialize and clone the complete key catalog when the caller only
// needs an existence check.
func (idx *DataIndexer[T]) HasAnyKeyExceptFold(
	excluded ...string,
) (bool, error) {
	if idx == nil {
		return false, nil
	}
	normalized := make(map[string]struct{}, len(excluded))
	for _, key := range excluded {
		key = strings.ToLower(strings.TrimSpace(key))
		if key != "" {
			normalized[key] = struct{}{}
		}
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.keysCacheValid {
		for _, key := range idx.keysCache {
			if _, skip := normalized[strings.ToLower(key)]; !skip {
				return true, nil
			}
		}
		return false, nil
	}

	query := "SELECT 1 FROM data WHERE namespace = ?"
	arguments := []any{idx.namespace}
	if len(normalized) != 0 {
		placeholders := make([]string, 0, len(normalized))
		for key := range normalized {
			placeholders = append(placeholders, "?")
			arguments = append(arguments, key)
		}
		query += " AND LOWER(key) NOT IN (" +
			strings.Join(placeholders, ",") + ")"
	}
	query += " LIMIT 1"
	var present int
	if err := idx.db.QueryRow(query, arguments...).Scan(&present); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("failed to query key existence: %w", err)
	}
	return true, nil
}

// GetAllFilePaths returns every source path currently represented in the
// repository. Indexers can load this once to avoid a SQLite lookup for every
// file rejected by a cheap content candidate check.
func (idx *DataIndexer[T]) GetAllFilePaths() ([]string, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	rows, err := idx.db.Query(
		"SELECT DISTINCT file_path FROM data WHERE namespace = ?",
		idx.namespace,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query file paths: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("failed to scan file path: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

// DeleteByFilePath deletes all items associated with the given file path
func (idx *DataIndexer[T]) DeleteByFilePath(filePath string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(
		"DELETE FROM data WHERE namespace = ? AND file_path = ?",
		idx.namespace,
		filePath,
	)
	if err != nil {
		return fmt.Errorf("failed to delete data: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	idx.invalidateCache()
	return nil
}

// GetAllKeysByPath returns all unique keys associated with a specific file path
func (idx *DataIndexer[T]) GetAllKeysByPath(filePath string) ([]string, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if keys, ok := idx.pathKeysCache[filePath]; ok {
		return append([]string(nil), keys...), nil
	}

	rows, err := idx.db.Query(`
		SELECT DISTINCT key FROM data
		WHERE namespace = ? AND file_path = ?
	`, idx.namespace, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to query keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("failed to scan key: %w", err)
		}
		keys = append(keys, key)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	if idx.pathKeysCache == nil {
		idx.pathKeysCache = make(map[string][]string)
	}
	idx.pathKeysCache[filePath] = append([]string(nil), keys...)
	return append([]string(nil), keys...), nil
}

// GetValuesByPath returns the immutable repository snapshot for one source file.
func (idx *DataIndexer[T]) GetValuesByPath(filePath string) ([]T, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if values, ok := idx.pathValuesCache[filePath]; ok {
		return cloneSlice(values), nil
	}

	rows, err := idx.db.Query(`
		SELECT value FROM data
		WHERE namespace = ? AND file_path = ?
	`, idx.namespace, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to query values by path: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var values []T
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("failed to scan value by path: %w", err)
		}
		var value T
		if err := msgpack.Unmarshal(data, &value); err != nil {
			return nil, fmt.Errorf("failed to unmarshal value by path: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if idx.pathValuesCache == nil {
		idx.pathValuesCache = make(map[string][]T)
	}
	idx.pathValuesCache[filePath] = cloneSlice(values)
	return cloneSlice(values), nil
}

// HasFilePathIn reports whether this repository currently contains any item
// associated with filePath. When a workspace mutation is active, the lookup
// uses that transaction so callers do not deadlock on the store's single
// connection and see preceding writes from the same indexing pass.
func (idx *DataIndexer[T]) HasFilePathIn(mutation *Mutation, filePath string) (bool, error) {
	query := "SELECT 1 FROM data WHERE namespace = ? AND file_path = ? LIMIT 1"
	var exists int
	if mutation == nil {
		err := idx.db.QueryRow(query, idx.namespace, filePath).Scan(&exists)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("failed to query file association: %w", err)
		}
		return true, nil
	}
	if mutation.store != idx.store || mutation.done || mutation.tx == nil {
		return false, fmt.Errorf("index mutation belongs to a different store")
	}
	err := mutation.tx.QueryRow(query, idx.namespace, filePath).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to query file association: %w", err)
	}
	return true, nil
}

// BatchDeleteByFilePaths deletes all items associated with the given file paths in a single transaction
func (idx *DataIndexer[T]) BatchDeleteByFilePaths(filePaths []string) error {
	if len(filePaths) == 0 {
		return nil
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := idx.batchDeleteByFilePaths(tx, filePaths); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	idx.invalidateCache()
	return nil
}

func (idx *DataIndexer[T]) BatchDeleteByFilePathsIn(mutation *Mutation, filePaths []string) error {
	if mutation == nil {
		return idx.BatchDeleteByFilePaths(filePaths)
	}
	if mutation.store != idx.store {
		return fmt.Errorf("index mutation belongs to a different store")
	}
	if len(filePaths) == 0 {
		return nil
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if err := idx.batchDeleteByFilePaths(mutation, filePaths); err != nil {
		return err
	}
	return mutation.addCache(idx)
}

func (idx *DataIndexer[T]) batchDeleteByFilePaths(
	executor statementPreparer,
	filePaths []string,
) error {
	if len(filePaths) == 0 {
		return nil
	}
	const batchSize = 128
	firstBatchCount := min(len(filePaths), batchSize)
	statement, err := executor.Prepare(deleteDataByPathsSQL(firstBatchCount))
	if err != nil {
		return fmt.Errorf("failed to prepare data deletion: %w", err)
	}
	if !usesCachedStatements(executor) {
		defer func() { _ = statement.Close() }()
	}

	var tailStatement *sql.Stmt
	defer func() {
		if tailStatement != nil && !usesCachedStatements(executor) {
			_ = tailStatement.Close()
		}
	}()
	var cachedArgs [][]any
	if mutation, ok := executor.(*Mutation); ok {
		cachedArgs = mutation.deleteArguments(filePaths, batchSize)
	}
	var args []any
	if cachedArgs == nil {
		args = make([]any, firstBatchCount+1)
	}
	for start := 0; start < len(filePaths); start += batchSize {
		end := min(start+batchSize, len(filePaths))
		batch := filePaths[start:end]
		currentStatement := statement
		if len(batch) != firstBatchCount {
			tailStatement, err = executor.Prepare(deleteDataByPathsSQL(len(batch)))
			if err != nil {
				return fmt.Errorf("failed to prepare data deletion: %w", err)
			}
			currentStatement = tailStatement
		}
		var currentArgs []any
		if cachedArgs != nil {
			currentArgs = cachedArgs[start/batchSize]
		} else {
			currentArgs = args[:len(batch)+1]
			for index, filePath := range batch {
				currentArgs[index+1] = filePath
			}
		}
		currentArgs[0] = idx.namespace
		if _, err := currentStatement.Exec(currentArgs...); err != nil {
			return fmt.Errorf("failed to delete data: %w", err)
		}
	}

	return nil
}

func deleteDataByPathsSQL(count int) string {
	return inClauseSQL(
		"DELETE FROM data WHERE namespace = ? AND file_path IN (",
		count,
	)
}

func inClauseSQL(prefix string, count int) string {
	var query strings.Builder
	query.Grow(len(prefix) + count*2)
	query.WriteString(prefix)
	for index := 0; index < count; index++ {
		if index != 0 {
			query.WriteByte(',')
		}
		query.WriteByte('?')
	}
	query.WriteByte(')')
	return query.String()
}

func (idx *DataIndexer[T]) Clear() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	_, err := idx.db.Exec(
		"DELETE FROM data WHERE namespace = ?",
		idx.namespace,
	)
	if err != nil {
		return err
	}
	idx.invalidateCache()

	// Reclaim space after clearing all data
	_, err = idx.db.Exec("PRAGMA incremental_vacuum")
	return err
}

func (idx *DataIndexer[T]) ClearIn(mutation *Mutation) error {
	if mutation == nil {
		return idx.Clear()
	}
	if mutation.store != idx.store {
		return fmt.Errorf("index mutation belongs to a different store")
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if _, err := mutation.tx.Exec(
		"DELETE FROM data WHERE namespace = ?",
		idx.namespace,
	); err != nil {
		return err
	}
	return mutation.addCache(idx)
}

// Close closes the database with optimization
func (idx *DataIndexer[T]) Close() error {
	idx.closeOnce.Do(func() {
		idx.mu.Lock()
		idx.invalidateCache()
		idx.mu.Unlock()
		if idx.ownsStore {
			idx.closeErr = idx.store.Close()
		}
	})
	return idx.closeErr
}
