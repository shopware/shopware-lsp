package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestTwigMacroDefinitionNavigatesToDeclaration(t *testing.T) {
	root := t.TempDir()
	macroPath := filepath.Join(
		root,
		"templates",
		"macros",
		"forms.html.twig",
	)
	pagePath := filepath.Join(root, "templates", "page.html.twig")
	require.NoError(t, os.MkdirAll(filepath.Dir(macroPath), 0o755))
	macroSource := `{% macro input(name) %}{% endmacro %}`
	pageSource := `{% import 'macros/forms.html.twig' as forms %}
{{ forms.input('email') }}
`
	require.NoError(t, os.WriteFile(macroPath, []byte(macroSource), 0o644))
	require.NoError(t, os.WriteFile(pagePath, []byte(pageSource), 0o644))
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		macroPath,
		[]byte(macroSource),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		pagePath,
		[]byte(pageSource),
	)))

	document := lsp.NewTextDocument(uriutil.FileURI(pagePath), pageSource, 1)
	offset := uint32(strings.Index(pageSource, "forms.input") + len("forms.") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewTwigMacroDefinitionProvider(index).GetDefinition(
		context.Background(),
		twigMacroDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(macroPath), locations[0].URI)
	assert.Equal(t, 0, locations[0].Range.Start.Line)
	assert.Equal(t, strings.Index(macroSource, "input"), locations[0].Range.Start.Character)
}

func twigMacroDefinitionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
) *lsp.DefinitionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.DefinitionRequest{
		DefinitionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}
