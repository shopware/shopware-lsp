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
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigEnumHoverShowsEnumCases(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "Status.php"),
		[]byte(`<?php
namespace App;
enum Status { case Open; case Closed; }`),
	)))
	source := `{{ enum('App\\Status') }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates", "status.html.twig")),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "Status") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewTwigEnumHoverProvider(phpIndex).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				DocumentContent: document.Text,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "PHP enum")
	assert.Contains(t, result.Contents.Value, "App\\Status")
	assert.Contains(t, result.Contents.Value, "Closed")
	assert.Contains(t, result.Contents.Value, "Open")
}
