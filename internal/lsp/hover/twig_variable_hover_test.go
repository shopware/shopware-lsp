package hover

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigControllerVariableHover(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/ProductController.php",
		[]byte(`<?php
namespace App;
class Product {}
class ProductController {
    public function show(Product $product) {
        return $this->render('product/show.html.twig', [
            'product' => $product,
        ]);
    }
}`),
	)))

	source := `{{ product }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/product/show.html.twig",
		source,
		1,
	)
	offset := strings.Index(source, "product") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	request := &lsp.HoverRequest{
		HoverParams: params,
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

	result, err := NewTwigHoverProvider(
		"/project",
		nil,
		phpIndex,
		nil,
	).GetHover(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Twig variable")
	assert.Contains(t, result.Contents.Value, "App\\Product")
	assert.Contains(t, result.Contents.Value, "src/ProductController.php")
}

func TestTwigTypesVariableHoverIncludesDocumentationAndOptionality(
	t *testing.T,
) {
	source := `{% types {
    ## User shown in the profile card.
    user?: 'App\\User',
} %}
{{ user }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/profile.html.twig",
		source,
		1,
	)
	offset := strings.LastIndex(source, "user") + 1
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewTwigHoverProvider(
		"/project",
		nil,
		nil,
		nil,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Declared type: `App\\User`")
	assert.Contains(t, result.Contents.Value, "Optional in the Twig `types` tag")
	assert.Contains(t, result.Contents.Value, "User shown in the profile card.")
}

func TestTwigConstructHoverShowsDocumentationComment(t *testing.T) {
	source := `{## Main page content. ##}
{% block content %}{% endblock %}`
	document := lsp.NewTextDocument(
		"file:///project/templates/profile.html.twig",
		source,
		1,
	)
	offset := strings.Index(source, "block") + 1
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewTwigHoverProvider(
		"/project",
		nil,
		nil,
		nil,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "Main page content.", result.Contents.Value)
}

func TestTwigOutputDocumentationEnrichesVariableHover(t *testing.T) {
	source := `{# @var user App\User #}
{## User rendered by this expression. #}
{{ user }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/profile.html.twig",
		source,
		1,
	)
	offset := strings.LastIndex(source, "user") + 1
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewTwigHoverProvider(
		"/project",
		nil,
		nil,
		nil,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Declared type: `App\\User`")
	assert.Contains(
		t,
		result.Contents.Value,
		"User rendered by this expression.",
	)
}

func TestTwigGlobalVariableHover(t *testing.T) {
	root := t.TempDir()
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	configPath := filepath.Join(root, "config/packages/twig.yaml")
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		configPath,
		[]byte(`twig:
  globals:
    site_name: 'Store'
`),
	)))

	source := `{{ site_name }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	offset := strings.Index(source, "site_name") + 1
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Character = offset
	result, err := NewTwigHoverProvider(
		root,
		nil,
		nil,
		twigIndex,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Twig global")
	assert.Contains(t, result.Contents.Value, "PHP type: `string`")
	assert.Contains(t, result.Contents.Value, "Twig configuration")
	assert.Contains(t, result.Contents.Value, "config/packages/twig.yaml")
}
