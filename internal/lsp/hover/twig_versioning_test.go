package hover

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigVersioningHoverProvider_nilIndexerNoPanic(t *testing.T) {
	ctx := context.Background()
	provider := NewTwigVersioningHoverProvider(nil)
	require.NotNil(t, provider)

	content := []byte(`{% block foo %}{% endblock %}`)
	parsed := twigparser.Parse(string(content))

	params := &protocol.HoverParams{
		TextDocument: struct {
			URI string `json:"uri"`
		}{URI: "file:///tmp/foo.twig"},
		Position: protocol.Position{Line: 0, Character: 10},
	}
	request := &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			DocumentContent: content,
			DocumentTree:    parsed.Tree,
			LineIndex:       cst.NewLineIndex(string(content)),
			Root:            parsed.Tree.Root,
			Node:            parsed.Tree.Root,
		},
	}

	hover, err := provider.GetHover(ctx, request)
	require.NoError(t, err)
	assert.Nil(t, hover)
}

func TestTwigVersioningHoverShowsThirdPartyUpstreamAndStatus(t *testing.T) {
	root := t.TempDir()
	idx, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	themeRoot := filepath.Join(root, "custom", "plugins", "Theme")
	require.NoError(t, os.MkdirAll(themeRoot, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(themeRoot, "composer.json"),
		[]byte(`{"version":"2.5.0"}`),
		0o644,
	))
	upstreamPath := filepath.Join(
		themeRoot, "src", "Resources", "views", "storefront", "card.html.twig",
	)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		upstreamPath,
		[]byte(`{% block card %}upstream{% endblock %}`),
	)))
	hashes, err := idx.GetTwigBlockHashes("card")
	require.NoError(t, err)
	require.Len(t, hashes, 1)
	currentPath := filepath.Join(
		root, "custom", "plugins", "Plugin", "src", "Resources", "views",
		"storefront", "card.html.twig",
	)
	source := `{% sw_extends '@Theme/storefront/card.html.twig' %}
{# shopware-block: ` + hashes[0].Hash + `@2.5.0 #}
{% block card %}local{% endblock %}`
	document := lsp.NewTextDocument(uriutil.FileURI(currentPath), source, 1)
	offset := strings.LastIndex(source, "{% block card") + len("{% block ") + 1
	request := &lsp.HoverRequest{
		HoverParams: &protocol.HoverParams{
			TextDocument: struct {
				URI string `json:"uri"`
			}{URI: document.URI},
		},
		SyntaxContext: lsp.SyntaxContext{
			Document: document, DocumentContent: document.Text,
			DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
			Root:  document.SyntaxTree.Root,
			Token: document.SyntaxTree.Root.TokenAtOffset(uint32(offset)),
			Node:  document.SyntaxTree.Root.NodeAtOffset(uint32(offset)),
		},
	}
	hover, err := NewTwigVersioningHoverProvider(
		twig.NewVersioningService(root, idx, ""),
	).GetHover(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, upstreamPath)
	assert.Contains(t, hover.Contents.Value, "2.5.0")
	assert.Contains(t, hover.Contents.Value, "up to date")
}
