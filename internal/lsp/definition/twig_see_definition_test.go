package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigSeeDefinitionResolvesPHPTemplateAndRelativeFiles(
	t *testing.T,
) {
	root := t.TempDir()
	phpPath := filepath.Join(root, "src", "ProductController.php")
	phpSource := []byte(`<?php
namespace App\Controller;
class ProductController {
    public function show(): void {}
}`)
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o700))
	require.NoError(t, os.WriteFile(phpPath, phpSource, 0o600))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		phpSource,
	)))

	templatePath := filepath.Join(
		root,
		"templates",
		"product",
		"show.html.twig",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(templatePath), 0o700))
	require.NoError(t, os.WriteFile(templatePath, []byte("product"), 0o600))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		[]byte("product"),
	)))

	currentPath := filepath.Join(root, "templates", "docs", "page.html.twig")
	notesPath := filepath.Join(root, "templates", "docs", "notes.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(currentPath), 0o700))
	require.NoError(t, os.WriteFile(notesPath, []byte("notes"), 0o600))
	provider := NewTwigDefinitionProvider(
		root,
		twigIndex,
		nil,
		phpIndex,
	)
	tests := []struct {
		source string
		target string
		line   int
	}{
		{
			source: `{# @see App\Controller\ProductController #}`,
			target: phpPath,
			line:   2,
		},
		{
			source: `{# @see App\Controller\ProductController::show #}`,
			target: phpPath,
			line:   3,
		},
		{
			source: `{# @see product/show.html.twig #}`,
			target: templatePath,
		},
		{
			source: `{# @see notes.md #}`,
			target: notesPath,
		},
		{
			source: `{# App\Controller\ProductController:show #}`,
			target: phpPath,
			line:   3,
		},
	}
	for _, test := range tests {
		locations := twigSeeDefinitionLocations(
			t,
			provider,
			currentPath,
			test.source,
		)
		require.NotEmpty(t, locations, test.source)
		assert.Equal(t, uriutil.FileURI(test.target), locations[0].URI)
		assert.Equal(t, test.line, locations[0].Range.Start.Line)
	}
}

func twigSeeDefinitionLocations(
	t *testing.T,
	provider *TwigDefinitionProvider,
	path,
	sourceWithCaret string,
) []protocol.Location {
	t.Helper()
	targetStart := strings.Index(sourceWithCaret, "@see ")
	if targetStart >= 0 {
		targetStart += len("@see ")
	} else {
		targetStart = strings.Index(sourceWithCaret, "{# ") + len("{# ")
	}
	require.GreaterOrEqual(t, targetStart, 0)
	offset := targetStart + 1
	document := lsp.NewTextDocument(
		uriutil.FileURI(path),
		sourceWithCaret,
		1,
	)
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return provider.GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					uint32(offset),
				),
			},
		},
	)
}
