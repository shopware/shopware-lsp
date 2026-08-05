package reference

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
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestTwigMacroReferencesConnectDeclarationImportsAndCalls(t *testing.T) {
	root := t.TempDir()
	macroPath := filepath.Join(root, "templates", "macros", "forms.html.twig")
	pagePath := filepath.Join(root, "templates", "page.html.twig")
	otherPath := filepath.Join(root, "templates", "other.html.twig")
	for _, path := range []string{macroPath, pagePath, otherPath} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	}
	macroSource := `{% macro input(name) %}{% endmacro %}`
	pageSource := `{% import 'macros/forms.html.twig' as forms %}
{{ forms.input('email') }}
`
	otherSource := `{% from 'macros/forms.html.twig' import input as field %}
{{ field('name') }}
`
	for path, source := range map[string]string{
		macroPath: macroSource,
		pagePath:  pageSource,
		otherPath: otherSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		macroPath: macroSource,
		pagePath:  pageSource,
		otherPath: otherSource,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	document := lsp.NewTextDocument(
		uriutil.FileURI(pagePath),
		pageSource,
		1,
	)
	offset := uint32(strings.Index(pageSource, "forms.input") + len("forms.") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewTwigMacroReferenceProvider(index).GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, locations, 4)
	var uris []string
	for _, location := range locations {
		uris = append(uris, location.URI)
	}
	assert.Equal(t, 1, countMacroURI(uris, uriutil.FileURI(macroPath)))
	assert.Equal(t, 1, countMacroURI(uris, uriutil.FileURI(pagePath)))
	assert.Equal(t, 2, countMacroURI(uris, uriutil.FileURI(otherPath)))
}

func countMacroURI(values []string, candidate string) int {
	count := 0
	for _, value := range values {
		if value == candidate {
			count++
		}
	}
	return count
}
