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

func TestTwigTypesTagDefinitionNavigatesToPHPClass(t *testing.T) {
	root := t.TempDir()
	phpPath := filepath.Join(root, "src", "User.php")
	phpSource := `<?php
namespace App\Entity;
class User {}`
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o700))
	require.NoError(t, os.WriteFile(phpPath, []byte(phpSource), 0o600))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	provider := NewTwigDefinitionProvider(
		root,
		twigIndex,
		nil,
		phpIndex,
	)

	sourceWithCaret := `{% types {
    user?: 'App\\Ent<caret>ity\\User[]',
} %}`
	offset := strings.Index(sourceWithCaret, "<caret>")
	require.NotEqual(t, -1, offset)
	source := strings.Replace(sourceWithCaret, "<caret>", "", 1)
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"templates",
			"page.html.twig",
		)),
		source,
		1,
	)
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := provider.GetDefinition(
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
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(phpPath), locations[0].URI)
}
