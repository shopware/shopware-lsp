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

func TestTwigRenderBlockDefinitionNavigatesOverridesAndParents(t *testing.T) {
	root := t.TempDir()
	twigIndex, phpIndex := renderBlockDefinitionFixture(t, root)
	source := `<?php
namespace App;
class Controller extends \Symfony\Bundle\FrameworkBundle\Controller\AbstractController {
    public function page(): void {
        $this->renderBlockView(
            block: 'content',
            view: 'child.html.twig',
        );
    }
}`
	path := filepath.Join(root, "src", "Controller.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := strings.Index(source, "'content'") + 2
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	locations := NewTwigRenderBlockDefinitionProvider(
		twigIndex,
		phpIndex,
	).GetDefinition(ctx, &lsp.DefinitionRequest{
		DefinitionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			DocumentContent: document.Text,
			Root:            document.SyntaxTree.Root,
			Node:            node,
			LineIndex:       document.LineIndex,
		},
	})
	require.Len(t, locations, 2)
	require.ElementsMatch(t, []string{
		uriutil.FileURI(filepath.Join(root, "templates", "base.html.twig")),
		uriutil.FileURI(filepath.Join(root, "templates", "child.html.twig")),
	}, []string{locations[0].URI, locations[1].URI})
}

func renderBlockDefinitionFixture(
	t *testing.T,
	root string,
) (*twig.TwigIndexer, *php.PHPIndex) {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	frameworkPath := filepath.Join(root, "vendor", "AbstractController.php")
	framework := `<?php
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {}`
	require.NoError(t, os.MkdirAll(filepath.Dir(frameworkPath), 0o755))
	require.NoError(t, os.WriteFile(frameworkPath, []byte(framework), 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		frameworkPath,
		[]byte(framework),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	for relative, source := range map[string]string{
		"base.html.twig": `{% block content %}{% endblock %}`,
		"child.html.twig": `{% extends 'base.html.twig' %}
{% block content %}{% endblock %}`,
	} {
		path := filepath.Join(root, "templates", relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	return twigIndex, phpIndex
}
