package hover

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/extension"
	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The prefixes below put a two-byte (ä) and a four-byte, two-UTF-16-unit
// (😀) character before the hovered name, so byte and UTF-16 columns differ.
// The cursor sits inside the name; the range must still cover exactly the
// name, in UTF-16 units.

func utf16HoverRequestAt(document *lsp.TextDocument, offset uint32) *lsp.HoverRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document, DocumentContent: document.Text,
			DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
			Root:  document.SyntaxTree.Root,
			Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
			Token: document.SyntaxTree.Root.TokenAtOffset(offset),
		},
	}
}

func assertUTF16HoverRange(
	t *testing.T,
	hover *protocol.Hover,
	document *lsp.TextDocument,
	name string,
	start, end int,
) {
	t.Helper()
	require.NotNil(t, hover)
	require.NotNil(t, hover.Range)
	assert.Equal(t, protocol.Range{
		Start: protocol.Position{Line: 0, Character: start},
		End:   protocol.Position{Line: 0, Character: end},
	}, *hover.Range)
	assert.Equal(t, name, hoverRangeText(document, *hover.Range))
}

func TestSnippetHoverRangeUsesUTF16OnNonASCIILines(t *testing.T) {
	root := t.TempDir()
	snippetIndex, err := snippet.NewSnippetIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snippetIndex.Close()) })
	require.NoError(t, snippetIndex.Index(indexerpkg.NewParsedFile(
		filepath.Join(root, "src/Resources/app/administration/src/module/demo/snippet/en-GB.json"),
		[]byte(`{"admin":{"settings":{"title":"Settings"}}}`),
	)))
	require.NoError(t, snippetIndex.Index(indexerpkg.NewParsedFile(
		filepath.Join(root, "src/Resources/snippet/en_GB/storefront.en-GB.json"),
		[]byte(`{"product":{"title":"Title"}}`),
	)))
	provider := NewSnippetHoverProvider(root, snippetIndex)

	for _, test := range []struct {
		name, uri, source, key string
		start, end             int
	}{
		{
			"storefront twig trans", "file:///project/src/Resources/views/page.html.twig",
			`{# ä😀 #} {{ 'product.title'|trans }}`, "product.title", 14, 27,
		},
		{
			"administration twig $tc", "file:///project/src/Resources/app/administration/view.html.twig",
			`{# ä😀 #} {{ $tc('admin.settings.title') }}`, "admin.settings.title", 18, 38,
		},
		{
			"php trans", "file:///project/src/Controller/PageController.php",
			`<?php /* ä😀 */ $translator->trans('product.title');`, "product.title", 36, 49,
		},
		{
			"administration javascript", "file:///project/src/Resources/app/administration/index.js",
			`/* ä😀 */ this.$root.$t('admin.settings.title')`, "admin.settings.title", 25, 45,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(test.uri, test.source, 1)
			offset := uint32(strings.Index(test.source, test.key) + 3)
			hover, err := provider.GetHover(context.Background(), utf16HoverRequestAt(document, offset))
			require.NoError(t, err)
			assertUTF16HoverRange(t, hover, document, test.key, test.start, test.end)
		})
	}
}

func TestTwigIconHoverRangeUsesUTF16OnNonASCIILines(t *testing.T) {
	root := t.TempDir()
	iconPath := filepath.Join(
		root, "src/Storefront/Resources/app/storefront/dist/assets/icon/default/arrow-down.svg",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(iconPath), 0o755))
	require.NoError(t, os.WriteFile(iconPath, []byte("<svg/>"), 0o600))
	extensionIndex, err := extension.NewExtensionIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, extensionIndex.Close()) })

	source := `{# ä😀 #} {% sw_icon 'arrow-down' %}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "src/Resources/views/page.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, "arrow-down") + 3)
	hover, err := NewTwigHoverProvider(root, extensionIndex, nil, nil).GetHover(
		context.Background(), utf16HoverRequestAt(document, offset),
	)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "**Icon**: `arrow-down`")
	assertUTF16HoverRange(t, hover, document, "arrow-down", 22, 32)
}

func TestAdminComponentHoverRangeUsesUTF16OnNonASCIILines(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(root, "src/Administration/Resources/app/administration/src")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "main.ts"),
		[]byte(`Shopware.Component.register('sw-card', { props: { title: String } });`),
	)))
	provider := NewAdminHoverProvider(root, index)

	for _, test := range []struct {
		name, file, source string
		start, end         int
	}{
		{"twig tag", "consumer.html.twig", `{# ä😀 #} <sw-card></sw-card>`, 11, 18},
		{
			"javascript registry", "consumer.ts",
			`/* ä😀 */ Shopware.Component.getComponentRegistry().get('sw-card')`, 57, 64,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, test.file)), test.source, 1,
			)
			offset := uint32(strings.Index(test.source, "sw-card") + 3)
			hover, err := provider.GetHover(context.Background(), utf16HoverRequestAt(document, offset))
			require.NoError(t, err)
			require.NotNil(t, hover)
			assert.Contains(t, hover.Contents.Value, "`sw-card`")
			assertUTF16HoverRange(t, hover, document, "sw-card", test.start, test.end)
		})
	}
}
