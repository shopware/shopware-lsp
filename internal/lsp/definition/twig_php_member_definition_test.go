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
	"github.com/stretchr/testify/require"
)

func TestTwigPHPMemberDefinition(t *testing.T) {
	root := t.TempDir()
	phpPath := filepath.Join(root, "src", "Bar.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o755))
	phpSource := []byte(`<?php
namespace Foo;
class Bar {
    public const KIND = 'bar';
    /** @deprecated */
    public function getDeprecated(): static { return $this; }
}`)
	require.NoError(t, os.WriteFile(phpPath, phpSource, 0o644))
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		phpSource,
	)))
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".twig-cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })

	source := `{# @var bar \Foo\Bar #} {{ bar.deprecated }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates/page.html.twig")),
		source,
		1,
	)
	offset := strings.LastIndex(source, "deprecated") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Character = offset
	locations := NewTwigDefinitionProvider(
		root,
		twigIndex,
		nil,
		phpIndex,
	).GetDefinition(context.Background(), &lsp.DefinitionRequest{
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
	})
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(phpPath), locations[0].URI)
	require.Equal(t, 5, locations[0].Range.Start.Line)
	require.Equal(t, 20, locations[0].Range.Start.Character)
}

func TestTwigPHPClassConstantDefinition(t *testing.T) {
	root := t.TempDir()
	phpPath := filepath.Join(root, "src", "Bar.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o755))
	phpSource := []byte(`<?php
namespace Foo;
class Bar {
    public const KIND = 'bar';
}`)
	require.NoError(t, os.WriteFile(phpPath, phpSource, 0o644))
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		phpSource,
	)))
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".twig-cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })

	source := `{# @var bar \Foo\Bar #} {{ bar.KIND }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates/page.html.twig")),
		source,
		1,
	)
	offset := strings.LastIndex(source, "KIND") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Character = offset
	locations := NewTwigDefinitionProvider(
		root,
		twigIndex,
		nil,
		phpIndex,
	).GetDefinition(context.Background(), &lsp.DefinitionRequest{
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
	})
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(phpPath), locations[0].URI)
	require.Equal(t, 3, locations[0].Range.Start.Line)
	require.Equal(t, 17, locations[0].Range.Start.Character)
}
