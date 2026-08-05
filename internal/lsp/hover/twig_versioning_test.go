package hover

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
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
