package indexer

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreIsolatesRepositoryNamespaces(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "indexes.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()

	numbers, err := NewDataIndexerInStore[int](store, "numbers")
	require.NoError(t, err)
	words, err := NewDataIndexerInStore[string](store, "words")
	require.NoError(t, err)

	require.NoError(t, numbers.SaveItem("/a", "same-key", 42))
	require.NoError(t, words.SaveItem("/b", "same-key", "forty-two"))

	numberValues, err := numbers.GetValues("same-key")
	require.NoError(t, err)
	require.Equal(t, []int{42}, numberValues)

	wordValues, err := words.GetValues("same-key")
	require.NoError(t, err)
	require.Equal(t, []string{"forty-two"}, wordValues)

	// A repository using a shared store does not own the connection.
	require.NoError(t, numbers.Close())
	require.NoError(t, words.SaveItem("/c", "another-key", "value"))
}

func TestStoreMutationWaitHonorsContext(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "indexes.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()

	active, err := store.BeginMutation(context.Background())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.BeginMutation(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, active.Rollback())
}

func TestStoreAllowsCommittedRepositoryReadsDuringMutation(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "indexes.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	repository, err := NewDataIndexerInStore[string](store, "services")
	require.NoError(t, err)
	require.NoError(t, repository.SaveItem("/old.php", "old", "committed"))

	mutation, err := store.BeginMutation(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mutation.Rollback()) })
	require.NoError(t, repository.BatchSaveItemsIn(
		mutation,
		map[string]map[string]string{
			"/new.php": {"new": "pending"},
		},
	))

	type result struct {
		values []string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		values, readErr := repository.GetValues("old")
		done <- result{values: values, err: readErr}
	}()

	select {
	case read := <-done:
		require.NoError(t, read.err)
		require.Equal(t, []string{"committed"}, read.values)
	case <-time.After(time.Second):
		t.Fatal("repository read deadlocked behind its caller's write transaction")
	}
}

func TestStoreMutationSharesPreparedStatementsAcrossRepositories(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "indexes.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	first, err := NewDataIndexerInStore[string](store, "first")
	require.NoError(t, err)
	second, err := NewDataIndexerInStore[string](store, "second")
	require.NoError(t, err)

	mutation, err := store.BeginMutation(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mutation.Rollback()) })
	require.NoError(t, first.BatchSaveItemsIn(
		mutation,
		map[string]map[string]string{"/a.php": {"a": "first"}},
	))
	require.NoError(t, second.BatchSaveItemsIn(
		mutation,
		map[string]map[string]string{"/b.php": {"b": "second"}},
	))
	require.Len(t, mutation.statements, 2)
	require.NoError(t, mutation.Commit())
	require.Empty(t, mutation.statements)

	firstValues, err := first.GetValues("a")
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, firstValues)
	secondValues, err := second.GetValues("b")
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, secondValues)
}

func TestStoreMutationDeleteOnlyReplacementSkipsInsertStatement(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "indexes.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	repository, err := NewDataIndexerInStore[string](store, "services")
	require.NoError(t, err)
	require.NoError(t, repository.SaveItem("/removed.php", "service", "value"))

	mutation, err := store.BeginMutation(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mutation.Rollback()) })
	require.NoError(t, repository.BatchSaveItemsIn(
		mutation,
		map[string]map[string]string{"/removed.php": {}},
	))
	require.Len(t, mutation.statements, 1)
	require.NoError(t, mutation.Commit())

	values, err := repository.GetValuesByPath("/removed.php")
	require.NoError(t, err)
	require.Empty(t, values)
}

func TestStoreMutationBatchesFileRemovalsAcrossRepositories(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "indexes.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	first, err := NewDataIndexerInStore[int](store, "first")
	require.NoError(t, err)
	second, err := NewDataIndexerInStore[int](store, "second")
	require.NoError(t, err)

	const fileCount = 257
	paths := make([]string, fileCount)
	firstItems := make(map[string]map[string]int, fileCount)
	secondItems := make(map[string]map[string]int, fileCount)
	for index := range fileCount {
		filePath := fmt.Sprintf("/file-%03d.php", index)
		paths[index] = filePath
		firstItems[filePath] = map[string]int{"value": index}
		secondItems[filePath] = map[string]int{"value": index}
	}
	require.NoError(t, first.BatchSaveItems(firstItems))
	require.NoError(t, second.BatchSaveItems(secondItems))

	mutation, err := store.BeginMutation(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mutation.Rollback()) })
	require.NoError(t, first.BatchDeleteByFilePathsIn(mutation, paths))
	require.NoError(t, second.BatchDeleteByFilePathsIn(mutation, paths))
	// The 128-path statement and one-path tail are shared by both namespaces.
	require.Len(t, mutation.statements, 2)
	require.NoError(t, mutation.Commit())

	firstValues, err := first.GetAllValues()
	require.NoError(t, err)
	require.Empty(t, firstValues)
	secondValues, err := second.GetAllValues()
	require.NoError(t, err)
	require.Empty(t, secondValues)
}

func TestStoreRebuildsLegacyFileAssociationSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "indexes.db")
	legacy, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = legacy.Exec(`
		CREATE TABLE data (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			namespace TEXT NOT NULL,
			key TEXT NOT NULL,
			value BLOB NOT NULL
		);
		CREATE TABLE files (
			namespace TEXT NOT NULL,
			file_path TEXT NOT NULL,
			data_id INTEGER NOT NULL
		);
		INSERT INTO data(namespace, key, value)
			VALUES ('legacy', 'key', X'01');
		INSERT INTO files(namespace, file_path, data_id)
			VALUES ('legacy', '/old.php', 1);
	`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	store, err := NewStore(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()

	var legacyTables int
	require.NoError(t, store.db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'files'",
	).Scan(&legacyTables))
	require.Zero(t, legacyTables)

	var rows int
	require.NoError(t, store.db.QueryRow("SELECT COUNT(*) FROM data").Scan(&rows))
	require.Zero(t, rows)

	repository, err := NewDataIndexerInStore[string](store, "current")
	require.NoError(t, err)
	require.NoError(t, repository.SaveItem("/new.php", "key", "value"))
	values, err := repository.GetValuesByPath("/new.php")
	require.NoError(t, err)
	require.Equal(t, []string{"value"}, values)
}

func BenchmarkStoreMutationBatchDeleteByFilePaths(b *testing.B) {
	store, repositories, paths := setupBatchDeleteBenchmark(b)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		mutation, mutationErr := store.BeginMutation(context.Background())
		require.NoError(b, mutationErr)
		for _, repository := range repositories {
			require.NoError(
				b,
				repository.BatchDeleteByFilePathsIn(mutation, paths),
			)
		}
		require.NoError(b, mutation.Rollback())
	}
}

func BenchmarkStoreMutationUnbatchedDeleteByFilePaths(b *testing.B) {
	store, repositories, paths := setupBatchDeleteBenchmark(b)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		mutation, mutationErr := store.BeginMutation(context.Background())
		require.NoError(b, mutationErr)
		for _, repository := range repositories {
			for _, filePath := range paths {
				_, mutationErr = mutation.tx.Exec(
					"DELETE FROM data WHERE namespace = ? AND file_path = ?",
					repository.namespace,
					filePath,
				)
				require.NoError(b, mutationErr)
			}
		}
		require.NoError(b, mutation.Rollback())
	}
}

func setupBatchDeleteBenchmark(
	b *testing.B,
) (*Store, []*DataIndexer[int], []string) {
	b.Helper()
	store, err := NewStore(filepath.Join(b.TempDir(), "indexes.db"))
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, store.Close()) })

	const repositoryCount = 46
	repositories := make([]*DataIndexer[int], repositoryCount)
	for index := range repositoryCount {
		repositories[index], err = NewDataIndexerInStore[int](
			store,
			fmt.Sprintf("repository-%02d", index),
		)
		require.NoError(b, err)
	}
	const fileCount = 512
	paths := make([]string, fileCount)
	for index := range fileCount {
		paths[index] = fmt.Sprintf("/removed-%03d.php", index)
	}
	return store, repositories, paths
}
