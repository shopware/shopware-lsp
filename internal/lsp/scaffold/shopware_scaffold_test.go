package scaffold

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestShopwareScaffoldKindsProduceValidatedAtomicEdits(t *testing.T) {
	root := t.TempDir()
	provider := &Provider{root: root}
	kinds := []string{
		"plugin", "system-config", "scheduled-task", "migration",
		"app", "app-custom-entities", "app-cms", "app-script",
		"admin-component", "admin-module", "cms-block", "cms-element",
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			directory := filepath.Join(root, kind)
			require.NoError(t, os.MkdirAll(directory, 0o755))
			if kind == "admin-component" || kind == "admin-module" {
				require.NoError(t, os.WriteFile(
					filepath.Join(directory, "main.js"),
					[]byte("// entry\n"),
					0o644,
				))
			}
			payload, err := json.Marshal(ShopwareRequest{
				Kind:         kind,
				DirectoryURI: uriutil.FileURI(directory),
				Name:         "acme-example",
				Options: map[string]any{
					"namespace": "Acme\\Example",
					"timestamp": "1700000000",
					"hook":      "product-page-loaded",
				},
			})
			require.NoError(t, err)
			raw := json.RawMessage(payload)
			result, err := provider.createShopware(context.Background(), &raw)
			require.NoError(t, err)
			response, ok := result.(ShopwareResponse)
			require.True(t, ok)
			require.NotNil(t, response.Edit)
			require.NotEmpty(t, response.Edit.DocumentChanges)
			require.NotEmpty(t, response.PrimaryFileURI)
		})
	}
}

func TestShopwareScaffoldRejectsCollisionsBeforeReturningEdit(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "manifest.xml"),
		[]byte("existing"),
		0o644,
	))
	provider := &Provider{root: root}
	payload, err := json.Marshal(ShopwareRequest{
		Kind:         "app-custom-entities",
		DirectoryURI: uriutil.FileURI(root),
		Name:         "existing",
	})
	require.NoError(t, err)
	raw := json.RawMessage(payload)
	_, err = provider.createShopware(context.Background(), &raw)
	// The custom entities target differs from manifest.xml and remains valid.
	require.NoError(t, err)

	configPath := filepath.Join(root, "Resources", "config", "config.xml")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(configPath, []byte("existing"), 0o644))
	payload, err = json.Marshal(ShopwareRequest{
		Kind:         "system-config",
		DirectoryURI: uriutil.FileURI(root),
		Name:         "existing",
	})
	require.NoError(t, err)
	raw = json.RawMessage(payload)
	_, err = provider.createShopware(context.Background(), &raw)
	require.Error(t, err)
}

func TestShopwareScaffoldCreatesAdminOverrideAndEntrypoint(t *testing.T) {
	root := t.TempDir()
	provider := &Provider{root: root}
	payload, err := json.Marshal(ShopwareRequest{
		Kind:         "admin-component",
		DirectoryURI: uriutil.FileURI(root),
		Name:         "sw-product-list-custom",
		Options: map[string]any{
			"mode":        "extend",
			"target":      "sw-product-list",
			"method":      "loadProducts",
			"methodGroup": "methods",
			"parameters":  "criteria, context",
		},
	})
	require.NoError(t, err)
	raw := json.RawMessage(payload)
	result, err := provider.createShopware(context.Background(), &raw)
	require.NoError(t, err)
	response := result.(ShopwareResponse)
	var generated string
	var createdMain bool
	for _, change := range response.Edit.DocumentChanges {
		if filepath.Base(change.URI) == "main.js" {
			createdMain = true
		}
		for _, edit := range change.Edits {
			generated += edit.NewText
		}
	}
	require.True(t, createdMain)
	require.Contains(t, generated, "Component.extend('sw-product-list-custom', 'sw-product-list'")
	require.Contains(t, generated, "loadProducts(criteria, context)")
	require.Contains(t, generated, "this.$super('loadProducts', criteria, context)")
}

func TestShopwareScaffoldCreatesEventListener(t *testing.T) {
	root := t.TempDir()
	provider := &Provider{root: root}
	payload, err := json.Marshal(ShopwareRequest{
		Kind:         "event-listener",
		DirectoryURI: uriutil.FileURI(root),
		Name:         "ProductLoadedEventListener",
		Options: map[string]any{
			"namespace": "Acme\\Example\\EventListener",
			"event":     "Shopware\\Core\\Content\\Product\\ProductLoadedEvent",
		},
	})
	require.NoError(t, err)
	raw := json.RawMessage(payload)
	result, err := provider.createShopware(context.Background(), &raw)
	require.NoError(t, err)
	response := result.(ShopwareResponse)
	require.Contains(t, response.PrimaryFileURI, "ProductLoadedEventListener.php")
	var generated string
	for _, change := range response.Edit.DocumentChanges {
		for _, edit := range change.Edits {
			generated += edit.NewText
		}
	}
	require.Contains(t, generated, "#[AsEventListener]")
	require.Contains(t, generated, "ProductLoadedEvent $event")
}
