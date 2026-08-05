package reference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestTwigComponentReferencesOverlayCurrentDocument(t *testing.T) {
	root := t.TempDir()
	templatePath := filepath.Join(
		root,
		"templates/components/Alert.html.twig",
	)
	currentPath := filepath.Join(root, "templates/page.html.twig")
	otherPath := filepath.Join(root, "templates/other.html.twig")
	for _, path := range []string{templatePath, currentPath, otherPath} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	}
	for path, source := range map[string]string{
		templatePath: `<div></div>`,
		currentPath:  `{{ component('Alert') }}`,
		otherPath:    `<twig:Alert />`,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := twigcomponent.NewIndex(
		filepath.Join(root, ".cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(nil, nil, twigIndex)
	for path, source := range map[string]string{
		templatePath: `<div></div>`,
		currentPath:  `{{ component('Alert') }}`,
		otherPath:    `<twig:Alert />`,
	} {
		file := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, twigIndex.Index(file))
		require.NoError(t, componentIndex.Index(file))
	}

	unsaved := `<twig:Alert /> {{ component('Alert') }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(currentPath),
		unsaved,
		2,
	)
	offset := uint32(strings.Index(unsaved, "Alert") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewTwigComponentReferenceProvider(
		componentIndex,
		nil,
	).GetReferences(context.Background(), &lsp.ReferenceRequest{
		ReferenceParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
		},
	})
	require.NoError(t, err)
	require.Len(t, locations, 4)
	require.Equal(t, 2, countComponentURI(
		locations,
		uriutil.FileURI(currentPath),
	))
	require.Equal(t, 1, countComponentURI(
		locations,
		uriutil.FileURI(otherPath),
	))
	require.Equal(t, 1, countComponentURI(
		locations,
		uriutil.FileURI(templatePath),
	))
}

func TestTwigLiveActionReferencesIncludeEveryUseAndDeclaration(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := twigcomponent.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, nil, twigIndex)

	classPath := filepath.Join(root, "src/Twig/Components/Cart.php")
	templatePath := filepath.Join(
		root,
		"templates/components/Cart.html.twig",
	)
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveAction;
#[AsLiveComponent]
final class Cart {
    #[LiveAction]
    public function save(): void {}
}`)
	template := []byte(`{{ live_action('save') }}
<button data-live-action-param="debounce(300)|save">Save</button>`)
	for path, source := range map[string][]byte{
		classPath:    class,
		templatePath: template,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, source, 0o644))
		require.NoError(t, componentIndex.Index(
			indexer.NewParsedFile(path, source),
		))
	}
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, class)))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		template,
	)))
	// Reindex the component after the PHP dependency has the method symbols.
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))

	document := lsp.NewTextDocument(
		uriutil.FileURI(templatePath),
		string(template),
		1,
	)
	offset := uint32(strings.Index(string(template), "save") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewTwigComponentReferenceProvider(
		componentIndex,
		phpIndex,
	).GetReferences(context.Background(), &lsp.ReferenceRequest{
		ReferenceParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
		},
	})
	require.NoError(t, err)
	require.Len(t, locations, 3)
	require.Equal(t, 2, countComponentURI(
		locations,
		uriutil.FileURI(templatePath),
	))
	require.Equal(t, 1, countComponentURI(
		locations,
		uriutil.FileURI(classPath),
	))
}

func countComponentURI(
	locations []protocol.Location,
	uri string,
) int {
	count := 0
	for _, location := range locations {
		if location.URI == uri {
			count++
		}
	}
	return count
}
