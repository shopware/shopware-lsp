package definition

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

func TestTwigComponentDefinitionNavigatesToAnonymousTemplate(t *testing.T) {
	root := t.TempDir()
	templatePath := filepath.Join(
		root,
		"templates/components/Alert.html.twig",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(templatePath), 0o755))
	require.NoError(t, os.WriteFile(
		templatePath,
		[]byte("<div>Alert</div>"),
		0o644,
	))
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		[]byte("<div>Alert</div>"),
	)))
	componentIndex, err := twigcomponent.NewIndex(
		filepath.Join(root, ".cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(nil, nil, twigIndex)

	source := `<twig:Alert />`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates/page.html.twig")),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "Alert") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewTwigComponentDefinitionProvider(
		componentIndex,
		nil,
	).GetDefinition(context.Background(), &lsp.DefinitionRequest{
		DefinitionParams: params,
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
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(templatePath), locations[0].URI)
	require.Equal(t, protocol.Position{}, locations[0].Range.Start)
}

func TestTwigComponentDefinitionNavigatesComputedGetter(t *testing.T) {
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

	classPath := filepath.Join(root, "src/Twig/Components/Card.php")
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent]
class Card {
    public function getTotal(): int {}
}`)
	require.NoError(t, os.MkdirAll(filepath.Dir(classPath), 0o755))
	require.NoError(t, os.WriteFile(classPath, class, 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	templatePath := filepath.Join(
		root,
		"templates/components/Card.html.twig",
	)
	template := []byte(`{{ computed.total }}`)
	require.NoError(t, os.MkdirAll(filepath.Dir(templatePath), 0o755))
	require.NoError(t, os.WriteFile(templatePath, template, 0o644))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		template,
	)))

	document := lsp.NewTextDocument(
		uriutil.FileURI(templatePath),
		string(template),
		1,
	)
	offset := uint32(strings.Index(string(template), "total") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewTwigComponentDefinitionProvider(
		componentIndex,
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
			Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
		},
	})
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(classPath), locations[0].URI)
	require.Equal(t, 5, locations[0].Range.Start.Line)
}

func TestTwigComponentDefinitionNavigatesLiveAction(t *testing.T) {
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
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveAction;
use Symfony\UX\LiveComponent\Attribute\LiveArg;
#[AsLiveComponent]
final class Cart {
    #[LiveAction]
    public function save(#[LiveArg('itemId')] int $id): void {}
}`)
	require.NoError(t, os.MkdirAll(filepath.Dir(classPath), 0o755))
	require.NoError(t, os.WriteFile(classPath, class, 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, class)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	templatePath := filepath.Join(
		root,
		"templates/components/Cart.html.twig",
	)
	template := []byte(
		`<button data-live-action-param="debounce(300)|save" data-live-item-id-param="42">Save</button>`,
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(templatePath), 0o755))
	require.NoError(t, os.WriteFile(templatePath, template, 0o644))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		template,
	)))

	document := lsp.NewTextDocument(
		uriutil.FileURI(templatePath),
		string(template),
		1,
	)
	offset := uint32(strings.Index(string(template), "save") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewTwigComponentDefinitionProvider(
		componentIndex,
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
			Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
		},
	})
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(classPath), locations[0].URI)
	require.Equal(t, 8, locations[0].Range.Start.Line)
	require.Equal(t, 20, locations[0].Range.Start.Character)

	argumentOffset := uint32(strings.Index(string(template), "item-id") + 2)
	argumentLine, argumentCharacter := document.LineIndex.PositionUTF16(
		argumentOffset,
	)
	argumentParams := &protocol.DefinitionParams{}
	argumentParams.TextDocument.URI = document.URI
	argumentParams.Position.Line = int(argumentLine)
	argumentParams.Position.Character = int(argumentCharacter)
	locations = NewTwigComponentDefinitionProvider(
		componentIndex,
		phpIndex,
	).GetDefinition(context.Background(), &lsp.DefinitionRequest{
		DefinitionParams: argumentParams,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				argumentOffset,
			),
		},
	})
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(classPath), locations[0].URI)
	require.Equal(t, 8, locations[0].Range.Start.Line)
	require.Greater(t, locations[0].Range.Start.Character, 20)
}
