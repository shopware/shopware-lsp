package inlay

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnippetPreviewInlayHintsPreferEnglishAndNavigate(t *testing.T) {
	index, err := snippet.NewSnippetIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		"/project/src/Resources/snippet/de-DE/messages.json":                      `{"checkout":{"finish":"Bestellung abschliessen"}}`,
		"/project/src/Resources/snippet/en-GB/messages.json":                      `{"checkout":{"finish":"Complete order"}}`,
		"/project/src/Resources/app/administration/src/module/snippet/en-GB.json": `{"admin":{"title":"Administration title"}}`,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	provider := NewSnippetPreviewProvider(index)
	for name, document := range map[string]*lsp.TextDocument{
		"twig frontend": lsp.NewTextDocument(
			"file:///project/templates/checkout.html.twig",
			`{{ 'checkout.finish'|trans }}`,
			1,
		),
		"twig admin": lsp.NewTextDocument(
			"file:///project/src/Resources/app/administration/card.html.twig",
			`{{ $tc('admin.title') }}`,
			1,
		),
		"twig admin bound attribute": lsp.NewTextDocument(
			"file:///project/src/Resources/app/administration/card.html.twig",
			`<mt-card :title="$t('admin.title')" />`,
			1,
		),
		"javascript admin": lsp.NewTextDocument(
			"file:///project/src/Resources/app/administration/card.js",
			`this.$tc('admin.title');`,
			1,
		),
		"javascript injected translator": lsp.NewTextDocument(
			"file:///project/src/Resources/app/administration/service.ts",
			`translator.$t('admin.title');`,
			1,
		),
		"javascript snippet service": lsp.NewTextDocument(
			"file:///project/src/Resources/app/administration/service.js",
			`Shopware.Snippet.tc('admin.title');`,
			1,
		),
		"javascript module metadata": lsp.NewTextDocument(
			"file:///project/src/Resources/app/administration/module.js",
			`Module.register('demo', { title: 'admin.title' });`,
			1,
		),
	} {
		t.Run(name, func(t *testing.T) {
			hints, hintErr := provider.GetInlayHints(
				context.Background(),
				inlayHintRequest(document),
			)
			require.NoError(t, hintErr)
			require.Len(t, hints, 1)
			parts, ok := hints[0].Label.([]protocol.InlayHintLabelPart)
			require.True(t, ok)
			require.Len(t, parts, 1)
			if name == "twig frontend" {
				assert.Equal(t, "→ Complete order", parts[0].Value)
				assert.Equal(t, filepath.Base("messages.json"), filepath.Base(parts[0].Location.URI))
			} else {
				assert.Equal(t, "→ Administration title", parts[0].Value)
			}
			assert.NotNil(t, parts[0].Location)
			assert.True(t, hints[0].PaddingLeft)
		})
	}
}
