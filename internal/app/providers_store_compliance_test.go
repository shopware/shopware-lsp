package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestStoreComplianceOverlayRefreshesComposerDiagnostics(t *testing.T) {
	for _, location := range extension.ConfigLocations {
		t.Run(location, func(t *testing.T) { testExtensionConfigOverlay(t, location) })
	}
}

func testExtensionConfigOverlay(t *testing.T, location string) {
	root := t.TempDir()
	compliance, err := extension.NewConfigIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, compliance.Close()) })
	server := lsp.NewServer(nil, root, "test")
	registerExtensionConfigDocumentObserver(server, compliance)
	registerProblemInspection(server, "shopware.store_composer", []language.ID{language.JSON}, "shopware-lsp", []string{
		"shopware.store.label", "shopware.store.description", "shopware.store.manufacturer-link", "shopware.store.support-link", "shopware.store.require-core",
	}, diagnostics.NewShopwareStoreComposerAnalyzer(compliance))
	client, published := startAdministrationLifecycleClient(t, server)
	// A standalone plugin at the workspace root must support the same opt-in.
	composerURI := uriutil.FileURI(filepath.Join(root, "composer.json"))
	configPath := filepath.Join(root, location)
	configURI := uriutil.FileURI(configPath)
	notifyAdministrationDocumentOpen(t, client, composerURI, 1, `{"type":"shopware-platform-plugin"}`)
	initial := waitForAdministrationDiagnostics(t, published, composerURI)
	require.Empty(t, initial[composerURI].Diagnostics)
	config := "validation:\n  store_compliance: true\n"
	notifyAdministrationDocumentOpen(t, client, configURI, 1, config)
	opened := waitForAdministrationDiagnostics(t, published, composerURI, configURI)
	require.NotEmpty(t, opened[composerURI].Diagnostics)
	require.NoError(t, client.Notify(context.Background(), "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": configURI, "version": 2},
		"contentChanges": []map[string]any{{"text": "validation:\n  store_compliance: false\n"}},
	}))
	changed := waitForAdministrationDiagnostics(t, published, composerURI, configURI)
	require.Empty(t, changed[composerURI].Diagnostics)
	require.NoError(t, client.Notify(context.Background(), "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": configURI, "version": 3},
		"contentChanges": []map[string]any{{"text": config}},
	}))
	changed = waitForAdministrationDiagnostics(t, published, composerURI, configURI)
	require.NotEmpty(t, changed[composerURI].Diagnostics)

	require.NoError(t, compliance.Index(indexer.NewParsedFile(configPath, []byte("validation: {}"))))
	require.NoError(t, client.Notify(context.Background(), "textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": configURI}}))
	closed := waitForAdministrationDiagnostics(t, published, composerURI, configURI)
	require.Empty(t, closed[composerURI].Diagnostics)
	require.NoError(t, compliance.Index(indexer.NewParsedFile(configPath, []byte(config))))
	disk := waitForAdministrationDiagnostics(t, published, composerURI)
	require.NotEmpty(t, disk[composerURI].Diagnostics)
	require.NoError(t, compliance.RemovedFiles([]string{configPath}))
	removed := waitForAdministrationDiagnostics(t, published, composerURI)
	require.Empty(t, removed[composerURI].Diagnostics)
}
