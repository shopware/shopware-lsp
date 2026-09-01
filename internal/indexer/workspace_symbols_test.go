package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type workspaceSymbolTestIndexer struct{}

func (*workspaceSymbolTestIndexer) ID() string                  { return "test.symbols" }
func (*workspaceSymbolTestIndexer) Index(*ParsedFile) error     { return nil }
func (*workspaceSymbolTestIndexer) RemovedFiles([]string) error { return nil }
func (*workspaceSymbolTestIndexer) Close() error                { return nil }
func (*workspaceSymbolTestIndexer) Clear() error                { return nil }
func (*workspaceSymbolTestIndexer) RemovedFilesIn([]string, *Mutation) error {
	return nil
}
func (*workspaceSymbolTestIndexer) ClearIn(*Mutation) error { return nil }
func (*workspaceSymbolTestIndexer) WorkspaceSymbols(
	file *ParsedFile,
	_ any,
) ([]WorkspaceSymbol, error) {
	return []WorkspaceSymbol{{
		Name:     file.Source,
		Kind:     WorkspaceSymbolClass,
		Priority: WorkspaceSymbolPriorityPHPType,
	}}, nil
}

func TestWorkspaceSymbolCatalogQueryAndPriority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "indexes.db")
	store, err := NewStore(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()

	catalog, err := NewWorkspaceSymbolCatalog(store)
	require.NoError(t, err)
	require.NoError(t, catalog.ReplaceFiles(
		context.Background(),
		[]WorkspaceSymbolDocument{
			{
				Path: "/project/SystemConfigService.php",
				Symbols: []WorkspaceSymbol{
					{
						Name:     "SystemConfigService",
						Aliases:  []string{`Shopware\Core\SystemConfig\SystemConfigService`},
						Kind:     WorkspaceSymbolClass,
						Priority: WorkspaceSymbolPriorityPHPType,
					},
					{
						Name:          "SystemConfigService",
						ContainerName: "Fixture",
						Kind:          WorkspaceSymbolMethod,
						Priority:      WorkspaceSymbolPriorityPHPMember,
					},
				},
			},
			{
				Path: "/project/services.xml",
				Symbols: []WorkspaceSymbol{
					{
						Name:     "Shopware_SystemConfigService",
						Aliases:  []string{"SystemConfigService"},
						Kind:     WorkspaceSymbolObject,
						Priority: WorkspaceSymbolPriorityFramework,
					},
				},
			},
			{
				Path: "/project/Unrelated.php",
				Symbols: []WorkspaceSymbol{{
					Name:          "testSystem",
					ContainerName: "Config Service",
					Kind:          WorkspaceSymbolMethod,
					Priority:      WorkspaceSymbolPriorityPHPMember,
				}},
			},
		},
	))

	results, err := catalog.Query(context.Background(), "SystemConfigService", 20)
	require.NoError(t, err)
	require.Len(t, results, 3)
	require.Equal(t, WorkspaceSymbolClass, results[0].Kind)
	require.Equal(t, WorkspaceSymbolObject, results[1].Kind)
	require.Equal(t, WorkspaceSymbolMethod, results[2].Kind)

	results, err = catalog.Query(context.Background(), "ConfigService", 20)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, "SystemConfigService", results[0].Name)
}

func TestWorkspaceSymbolCatalogExcludesDomainsInSQLQuery(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "indexes.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	catalog, err := NewWorkspaceSymbolCatalog(store)
	require.NoError(t, err)
	require.NoError(t, catalog.ReplaceFiles(
		context.Background(),
		[]WorkspaceSymbolDocument{{
			Path: "/project/Symbols.php",
			Symbols: []WorkspaceSymbol{
				{Name: "ProductRepository", Domain: "php", Kind: WorkspaceSymbolClass},
				{Name: "ProductRepository", Domain: "symfony.service", Kind: WorkspaceSymbolObject},
			},
		}},
	))

	for _, query := range []string{"", "ProductRepository"} {
		results, queryErr := catalog.QueryWithOptions(
			context.Background(), query, 20,
			WorkspaceSymbolQueryOptions{
				ExcludedDomains: []string{"php", "php", " "},
			},
		)
		require.NoError(t, queryErr)
		require.Len(t, results, 1)
		require.Equal(t, "symfony.service", results[0].Domain)
	}
}

func TestWorkspaceSymbolCatalogReplaceDeleteAndReadOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "indexes.db")
	store, err := NewStore(path)
	require.NoError(t, err)
	catalog, err := NewWorkspaceSymbolCatalog(store)
	require.NoError(t, err)

	require.NoError(t, catalog.ReplaceFiles(ctx, []WorkspaceSymbolDocument{{
		Path: "/project/Fixture.php",
		Symbols: []WorkspaceSymbol{{
			Name:     "BeforeReplacement",
			Kind:     WorkspaceSymbolClass,
			Priority: WorkspaceSymbolPriorityPHPType,
		}},
	}}))
	require.NoError(t, catalog.ReplaceFiles(ctx, []WorkspaceSymbolDocument{{
		Path: "/project/Fixture.php",
		Symbols: []WorkspaceSymbol{{
			Name:     "AfterReplacement",
			Kind:     WorkspaceSymbolClass,
			Priority: WorkspaceSymbolPriorityPHPType,
		}},
	}}))
	require.NoError(t, catalog.SetReady(ctx, true))

	before, err := catalog.Query(ctx, "Before", 20)
	require.NoError(t, err)
	require.Empty(t, before)
	after, err := catalog.Query(ctx, "After", 20)
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.NoError(t, store.Close())

	readOnly, err := OpenWorkspaceSymbolCatalog(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, readOnly.Close()) }()
	ready, err := readOnly.Ready(ctx)
	require.NoError(t, err)
	require.True(t, ready)
	after, err = readOnly.Query(ctx, "After", 20)
	require.NoError(t, err)
	require.Len(t, after, 1)
}

func TestWorkspaceSymbolCatalogBulkInsertPreservesFTSDocIDs(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "indexes.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	catalog, err := NewWorkspaceSymbolCatalog(store)
	require.NoError(t, err)

	symbols := make([]WorkspaceSymbol, 0, 161)
	for index := 0; index < 161; index++ {
		symbols = append(symbols, WorkspaceSymbol{
			Name:     fmt.Sprintf("BulkSymbol%03d", index),
			Kind:     WorkspaceSymbolClass,
			Priority: WorkspaceSymbolPriorityPHPType,
		})
	}
	require.NoError(t, catalog.ReplaceFiles(ctx, []WorkspaceSymbolDocument{{
		Path: "/project/Bulk.php", Symbols: symbols,
	}}))
	result, err := catalog.Query(ctx, "BulkSymbol160", 20)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "BulkSymbol160", result[0].Name)
}

func TestWorkspaceSymbolIndexTextNormalization(t *testing.T) {
	symbol := WorkspaceSymbol{
		Name:          "SystemConfigServiceFactory42",
		ContainerName: `Shopware\\Core\\System\\SystemConfigService`,
		Aliases: []string{
			`Shopware\\Core\\System\\SystemConfig\\SystemConfigServiceFactory42`,
			"system-config-service-factory-42",
		},
	}
	require.Equal(
		t,
		"system config service factory 42 "+
			"systemconfigservicefactory42 configservicefactory42 "+
			"servicefactory42 factory42 systemconfigservice configservice",
		workspaceSymbolIndexText(symbol),
	)
	require.Equal(t, "RésuméÜber", workspaceSymbolLeafName("Vendor·RésuméÜber"))
	require.Equal(t, "RésuméÜber", workspaceSymbolLeafName("Vendor·RésuméÜber/"))
	require.Equal(t, `\\`, workspaceSymbolLeafName(`\\`))
	require.Equal(
		t,
		"xml http request 2 xmlhttprequest2 httprequest2 request2 résumé über résuméüber",
		workspaceSymbolSearchText("XMLHttpRequest2", "RésuméÜber"),
	)
}

func TestFileScannerMaintainsWorkspaceSymbolCatalogLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cache := t.TempDir()
	path := filepath.Join(root, "Fixture.php")
	require.NoError(t, os.WriteFile(path, []byte("Before"), 0o644))

	store, err := NewStore(filepath.Join(cache, "indexes.db"))
	require.NoError(t, err)
	catalog, err := NewWorkspaceSymbolCatalog(store)
	require.NoError(t, err)
	scanner, err := NewFileScanner(
		root,
		filepath.Join(cache, "files.db"),
		store,
	)
	require.NoError(t, err)
	scanner.SetWorkspaceSymbolCatalog(catalog)
	scanner.AddIndexer(&workspaceSymbolTestIndexer{})
	defer func() {
		require.NoError(t, scanner.Close())
		require.NoError(t, store.Close())
	}()

	require.NoError(t, scanner.IndexAll(ctx))
	ready, err := catalog.Ready(ctx)
	require.NoError(t, err)
	require.True(t, ready)
	before, err := catalog.Query(ctx, "Before", 20)
	require.NoError(t, err)
	require.Len(t, before, 1)

	require.NoError(t, os.WriteFile(path, []byte("After"), 0o644))
	require.NoError(t, scanner.IndexFiles(ctx, []string{path}))
	before, err = catalog.Query(ctx, "Before", 20)
	require.NoError(t, err)
	require.Empty(t, before)
	after, err := catalog.Query(ctx, "After", 20)
	require.NoError(t, err)
	require.Len(t, after, 1)

	require.NoError(t, scanner.RemoveFiles(ctx, []string{path}))
	after, err = catalog.Query(ctx, "After", 20)
	require.NoError(t, err)
	require.Empty(t, after)

	require.NoError(t, scanner.ClearHashes())
	ready, err = catalog.Ready(ctx)
	require.NoError(t, err)
	require.False(t, ready)
}

func BenchmarkWorkspaceSymbolIndexText(b *testing.B) {
	symbol := WorkspaceSymbol{
		Name:          "SystemConfigServiceFactory42",
		ContainerName: `Shopware\\Core\\System\\SystemConfigService`,
		Aliases: []string{
			`Shopware\\Core\\System\\SystemConfig\\SystemConfigServiceFactory42`,
			"system-config-service-factory-42",
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	var result string
	for range b.N {
		result = workspaceSymbolIndexText(symbol)
	}
	if result == "" {
		b.Fatal("expected workspace symbol search text")
	}
}

func BenchmarkWorkspaceSymbolCatalogReplaceFilesBulk(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(b.TempDir(), "indexes.db"))
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, store.Close()) })
	catalog, err := NewWorkspaceSymbolCatalog(store)
	require.NoError(b, err)
	catalog.BeginBulkPopulation()

	files := benchmarkWorkspaceSymbolDocuments(50, 20)
	require.NoError(b, catalog.ReplaceFiles(ctx, files))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		require.NoError(b, catalog.ReplaceFiles(ctx, files))
	}
}

func benchmarkWorkspaceSymbolDocuments(
	fileCount int,
	symbolsPerFile int,
) []WorkspaceSymbolDocument {
	files := make([]WorkspaceSymbolDocument, 0, fileCount)
	for fileIndex := range fileCount {
		path := fmt.Sprintf("/project/src/Domain%03d.php", fileIndex)
		symbols := make([]WorkspaceSymbol, 0, symbolsPerFile)
		for symbolIndex := range symbolsPerFile {
			name := fmt.Sprintf(
				"Domain%03dProductConfigurationService%03d",
				fileIndex,
				symbolIndex,
			)
			symbols = append(symbols, WorkspaceSymbol{
				Name:          name,
				ContainerName: `Shopware\\Core\\Content\\Product`,
				Aliases: []string{
					`Shopware\\Core\\Content\\Product\\` + name,
					"product-configuration-" + name,
				},
				Path:     path,
				Domain:   "php",
				Kind:     WorkspaceSymbolMethod,
				Priority: WorkspaceSymbolPriorityPHPMember,
				Range: WorkspaceSymbolRange{
					Start: WorkspaceSymbolPosition{Line: symbolIndex},
					End: WorkspaceSymbolPosition{
						Line: symbolIndex, Character: len(name),
					},
				},
			})
		}
		files = append(files, WorkspaceSymbolDocument{
			Path: path, Symbols: symbols,
		})
	}
	return files
}
