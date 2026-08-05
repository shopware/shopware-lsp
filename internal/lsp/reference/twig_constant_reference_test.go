package reference

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

func TestTwigConstantReferencesBridgePHPAndTwigUsages(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	constantsPath := filepath.Join(root, "src", "CardSuite.php")
	constants := `<?php
namespace App;
class CardSuite {
    public const CLUBS = 'clubs';
    public const SPADES = 'spades';
}
const GLOBAL_SUIT = 'global';`
	writeTwigConstantFixture(t, constantsPath, constants)

	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		constantsPath,
		[]byte(constants),
	)))
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)

	directPath := filepath.Join(root, "templates", "direct.html.twig")
	direct := `{{ constant('App\\CardSuite::CLUBS') }}`
	objectPath := filepath.Join(root, "templates", "object.html.twig")
	object := `{# @var suite \App\CardSuite #}
{{ constant('CLUBS', suite) }}`
	otherPath := filepath.Join(root, "templates", "other.html.twig")
	other := `{{ constant('App\\CardSuite::SPADES') }}`
	for path, source := range map[string]string{
		directPath: direct,
		objectPath: object,
		otherPath:  other,
	} {
		writeTwigConstantFixture(t, path, source)
		require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	provider := NewTwigConstantReferenceProvider(twigIndex, phpIndex)

	fromTwig, err := provider.GetReferences(
		context.Background(),
		twigConstantReferenceRequest(
			t,
			directPath,
			direct,
			"CLUBS",
			true,
		),
	)
	require.NoError(t, err)
	require.Len(t, fromTwig, 3)
	require.ElementsMatch(t, []string{
		uriutil.FileURI(constantsPath),
		uriutil.FileURI(directPath),
		uriutil.FileURI(objectPath),
	}, constantLocationURIs(fromTwig))

	fromPHP, err := provider.GetReferences(
		context.Background(),
		twigConstantReferenceRequest(
			t,
			constantsPath,
			constants,
			"CLUBS",
			true,
		),
	)
	require.NoError(t, err)
	require.Len(t, fromPHP, 2)
	require.ElementsMatch(t, []string{
		uriutil.FileURI(directPath),
		uriutil.FileURI(objectPath),
	}, constantLocationURIs(fromPHP))
}

func TestTwigGlobalConstantReferencesNormalizeLeadingSlash(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	constantsPath := filepath.Join(root, "src", "Global.php")
	constants := `<?php namespace BugDemo;
const NAMESPACED_CONST = 'value';
const OTHER_CONST = 'other';`
	writeTwigConstantFixture(t, constantsPath, constants)
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		constantsPath,
		[]byte(constants),
	)))
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)
	for name, source := range map[string]string{
		"plain":   `{{ constant('BugDemo\\NAMESPACED_CONST') }}`,
		"leading": `{{ constant('\\BugDemo\\NAMESPACED_CONST') }}`,
		"other":   `{{ constant('BugDemo\\OTHER_CONST') }}`,
	} {
		path := filepath.Join(root, "templates", name+".html.twig")
		writeTwigConstantFixture(t, path, source)
		require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	provider := NewTwigConstantReferenceProvider(twigIndex, phpIndex)
	source := `{{ constant('BugDemo\\NAMESPACED_CONST') }}`
	path := filepath.Join(root, "templates", "plain.html.twig")
	locations, err := provider.GetReferences(
		context.Background(),
		twigConstantReferenceRequest(
			t,
			path,
			source,
			"NAMESPACED_CONST",
			false,
		),
	)
	require.NoError(t, err)
	require.Len(t, locations, 2)
}

func twigConstantReferenceRequest(
	t *testing.T,
	path,
	source,
	needle string,
	includeDeclaration bool,
) *lsp.ReferenceRequest {
	t.Helper()
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, needle) + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.Context.IncludeDeclaration = includeDeclaration
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.ReferenceRequest{
		ReferenceParams: params,
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
	}
}

func writeTwigConstantFixture(
	t *testing.T,
	path,
	source string,
) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
}

func constantLocationURIs(
	locations []protocol.Location,
) []string {
	result := make([]string, 0, len(locations))
	for _, location := range locations {
		result = append(result, location.URI)
	}
	return result
}
