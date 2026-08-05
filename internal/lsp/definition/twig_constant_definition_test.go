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

func TestTwigConstantDefinitionNavigatesAllStaticForms(t *testing.T) {
	root := t.TempDir()
	constantsPath := filepath.Join(root, "src", "Constants.php")
	cache := t.TempDir()
	constants := []byte(`<?php
namespace App {
    class CardSuite { public const CLUBS = 'clubs'; }
}
namespace BugDemo {
    const NAMESPACED_CONST = 'value';
}`)
	require.NoError(t, os.MkdirAll(filepath.Dir(constantsPath), 0o755))
	require.NoError(t, os.WriteFile(constantsPath, constants, 0o644))
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		constantsPath,
		constants,
	)))
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)
	provider := NewTwigConstantDefinitionProvider(phpIndex, twigIndex)

	for _, test := range []struct {
		source string
		needle string
	}{
		{
			source: `{{ constant('App\\CardSuite::CLUBS') }}`,
			needle: "CLUBS",
		},
		{
			source: `{{ constant('\\BugDemo\\NAMESPACED_CONST') }}`,
			needle: "NAMESPACED_CONST",
		},
		{
			source: `{# @var suite \App\CardSuite #}
{{ constant('CLUBS', suite) }}`,
			needle: "'CLUBS",
		},
	} {
		locations := twigConstantDefinitions(
			t,
			provider,
			root,
			test.source,
			test.needle,
		)
		require.Len(t, locations, 1, test.source)
		require.Equal(t, uriutil.FileURI(constantsPath), locations[0].URI)
		require.NotEqual(t, protocol.Range{}, locations[0].Range)
	}
}

func twigConstantDefinitions(
	t *testing.T,
	provider *TwigConstantDefinitionProvider,
	root,
	source,
	needle string,
) []protocol.Location {
	t.Helper()
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"templates",
			"constant.html.twig",
		)),
		source,
		1,
	)
	offset := uint32(strings.LastIndex(source, needle) + len(needle)/2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return provider.GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
}
