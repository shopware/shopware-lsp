package completion

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceCompletionsMarkDeprecatedServicesAndClasses(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/Types.php",
		[]byte(`<?php
namespace App;
class Modern {}
/** @deprecated Use Modern instead. */
class Legacy {}
`),
	)))

	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/services.yaml",
		[]byte(`services:
  app.modern:
    class: App\Modern
  app.legacy:
    class: App\Legacy
    deprecated: 'The %service_id% service is deprecated; use app.modern.'
`),
	)))

	provider := NewServiceCompletionProvider(serviceIndex, phpIndex)
	services := provider.serviceCompletionItems("@", "")
	legacyService := completionItemByLabel(t, services, "app.legacy")
	assert.True(t, legacyService.Deprecated)
	assert.Equal(t, "@app.legacy", legacyService.InsertText)
	assert.Equal(t, "Deprecated Symfony service", legacyService.Detail)
	assert.Contains(t, legacyService.Documentation.Value, "app.legacy")
	assert.NotContains(t, legacyService.Documentation.Value, "%service_id%")
	assert.False(
		t,
		completionItemByLabel(t, services, "app.modern").Deprecated,
	)

	classes := provider.classCompletionItems()
	legacyClass := completionItemByLabel(t, classes, "App\\Legacy")
	assert.True(t, legacyClass.Deprecated)
	assert.Equal(t, "Deprecated PHP type", legacyClass.Detail)
	assert.False(
		t,
		completionItemByLabel(t, classes, "App\\Modern").Deprecated,
	)
}

func completionItemByLabel(
	t *testing.T,
	items []protocol.CompletionItem,
	label string,
) protocol.CompletionItem {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("completion item %q not found in %#v", label, items)
	return protocol.CompletionItem{}
}
