package completion

import (
	"context"
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

func TestTwigConstantCompletionSupportsPHPConstantForms(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "Constants.php"),
		[]byte(`<?php
const CONST_FOO = 'foo';
namespace App\Bike;
class FooConst {
    public const CAR = 'car';
    protected const HIDDEN = 'hidden';
}
enum FooEnum { case FOOBAR; }
namespace BugDemo;
const NAMESPACED_CONST = 'value';
class CardSuite {
    public const CLUBS = 'clubs';
    public const SPADES = 'spades';
}`),
	)))
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)
	provider := NewTwigConstantCompletionProvider(phpIndex, twigIndex)

	general := twigConstantCompletions(
		t,
		provider,
		root,
		`{{ constant('App\\Bike\\Foo') }}`,
		"Foo",
	)
	assert.ElementsMatch(
		t,
		[]string{"FooConst::CAR", "FooEnum::FOOBAR"},
		completionLabels(general),
	)
	generalEdit, ok := completionByLabel(
		t,
		general,
		"FooEnum::FOOBAR",
	).TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, `App\\Bike\\FooEnum::FOOBAR`, generalEdit.NewText)

	explicit := twigConstantCompletions(
		t,
		provider,
		root,
		`{{ constant('App\\Bike\\FooConst::C') }}`,
		"::C",
	)
	require.Equal(t, []string{"CAR"}, completionLabels(explicit))
	explicitEdit, ok := explicit[0].TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, `App\\Bike\\FooConst::CAR`, explicitEdit.NewText)

	object := twigConstantCompletions(
		t,
		provider,
		root,
		`{# @var suite \BugDemo\CardSuite #}
{{ constant('CL', suite) }}`,
		"'CL",
	)
	require.Equal(t, []string{"CLUBS"}, completionLabels(object))
	objectEdit, ok := object[0].TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "CLUBS", objectEdit.NewText)

	global := twigConstantCompletions(
		t,
		provider,
		root,
		`{{ constant('BugDemo\\N') }}`,
		"\\N",
	)
	require.Equal(t, []string{"NAMESPACED_CONST"}, completionLabels(global))

	assert.Empty(t, twigConstantCompletions(
		t,
		provider,
		root,
		`{{ constant('CL', factory()) }}`,
		"'CL",
	))
	for _, item := range general {
		assert.NotEqual(t, "HIDDEN", item.Label)
	}
}

func twigConstantCompletions(
	t *testing.T,
	provider *TwigConstantCompletionProvider,
	root,
	source,
	needle string,
) []protocol.CompletionItem {
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
	offset := uint32(strings.LastIndex(source, needle) + len(needle))
	nodeOffset := offset
	if nodeOffset > 0 {
		nodeOffset--
	}
	return provider.GetCompletions(
		context.Background(),
		twigEnumCompletionRequest(
			document,
			document.SyntaxTree.Root.NodeAtOffset(nodeOffset),
			offset,
		),
	)
}
