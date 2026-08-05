package definition

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

func TestTwigEnumDefinitionNavigatesToPHPEnum(t *testing.T) {
	root := t.TempDir()
	enumPath := filepath.Join(root, "src", "Status.php")
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		enumPath,
		[]byte("<?php namespace App; enum Status { case Open; }"),
	)))
	source := `{{ enum_cases('App\\Status') }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates", "status.html.twig")),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "Status") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewTwigEnumDefinitionProvider(phpIndex).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
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
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(enumPath), locations[0].URI)
}
