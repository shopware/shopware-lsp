package completion

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigEnumCompletionListsPHPEnumsWithTwigEscaping(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "Status.php"),
		[]byte(`<?php
namespace App\Domain;
enum OrderStatus { case Open; }
class NotAnEnum {}`),
	)))

	source := `{{ enum('App\\Domain\\OrderSta') }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates", "order.html.twig")),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "OrderSta") + len("OrderSta"))
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	items := NewTwigEnumCompletionProvider(phpIndex).GetCompletions(
		context.Background(),
		twigEnumCompletionRequest(document, node, offset),
	)
	require.Len(t, items, 1)
	assert.Equal(t, "App\\Domain\\OrderStatus", items[0].Label)
	assert.Equal(t, `App\\Domain\\OrderStatus`, items[0].InsertText)
}

func twigEnumCompletionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
) *lsp.CompletionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.CompletionRequest{
		CompletionParams: params,
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
