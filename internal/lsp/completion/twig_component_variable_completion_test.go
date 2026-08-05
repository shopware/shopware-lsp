package completion

import (
	"context"
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

func TestTwigComponentVariableCompletionUsesClassAndUnsavedProps(
	t *testing.T,
) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.ConfigureProject(root))
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := twigcomponent.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, nil, twigIndex)

	classPath := filepath.Join(root, "src/Twig/Components/Alert.php")
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent]
final class Alert {
    public string $title = '';
    public function getTotal(): int {}
    public function withArgument(string $value): string {}
}`)
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
		"templates/components/Alert.html.twig",
	)
	persisted := []byte(`{% props old %}{{ old }}`)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		persisted,
	)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		templatePath,
		persisted,
	)))

	source := `{% props variant = 'info' %}{{ va }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(templatePath),
		source,
		2,
	)
	offset := uint32(strings.LastIndex(source, "va") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	items := NewTwigComponentVariableCompletionProvider(
		componentIndex,
		phpIndex,
	).GetCompletions(context.Background(), &lsp.CompletionRequest{
		CompletionParams: params,
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
	requireCompletion(t, items, "variant")
	requireCompletion(t, items, "title")
	requireCompletion(t, items, "this")
	requireCompletion(t, items, "computed")
	for _, item := range items {
		require.NotEqual(t, "old", item.Label)
	}

	computedSource := `{{ computed.to }}`
	computedDocument := lsp.NewTextDocument(
		uriutil.FileURI(templatePath),
		computedSource,
		3,
	)
	computedOffset := uint32(strings.Index(computedSource, "to") + 1)
	computedLine, computedCharacter :=
		computedDocument.LineIndex.PositionUTF16(computedOffset)
	computedParams := &protocol.CompletionParams{}
	computedParams.TextDocument.URI = computedDocument.URI
	computedParams.Position.Line = int(computedLine)
	computedParams.Position.Character = int(computedCharacter)
	computedItems := NewTwigComponentVariableCompletionProvider(
		componentIndex,
		phpIndex,
	).GetCompletions(context.Background(), &lsp.CompletionRequest{
		CompletionParams: computedParams,
		SyntaxContext: lsp.SyntaxContext{
			Document:        computedDocument,
			Language:        computedDocument.SyntaxLanguage,
			DocumentContent: computedDocument.Text,
			DocumentTree:    computedDocument.SyntaxTree,
			LineIndex:       computedDocument.LineIndex,
			Root:            computedDocument.SyntaxTree.Root,
			Node: computedDocument.SyntaxTree.Root.NodeAtOffset(
				computedOffset,
			),
		},
	})
	requireCompletion(t, computedItems, "total")
	for _, item := range computedItems {
		require.NotEqual(t, "withArgument", item.Label)
	}
}
