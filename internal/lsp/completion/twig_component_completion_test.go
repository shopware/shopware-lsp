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
)

func TestTwigComponentCompletionNamesAndProps(t *testing.T) {
	root := t.TempDir()
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := twigcomponent.NewIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(nil, nil, twigIndex)

	templatePath := filepath.Join(
		root,
		"templates/components/Alert.html.twig",
	)
	template := []byte(`{# @prop variant string Visual style #}
{% props message, variant = 'info' %}
{% block content %}{% endblock %}`)
	require.NoError(t, twigIndex.Index(
		indexer.NewParsedFile(templatePath, template),
	))
	require.NoError(t, componentIndex.Index(
		indexer.NewParsedFile(templatePath, template),
	))
	provider := NewTwigComponentCompletionProvider(componentIndex)

	for _, source := range []string{
		`{{ component('Al`,
		`<twig:Al`,
		`{% component 'Al`,
	} {
		items := componentCompletions(t, provider, source)
		requireCompletion(t, items, "Alert")
	}

	items := componentCompletions(
		t,
		provider,
		`<twig:Alert message="Hello" va`,
	)
	requireCompletion(t, items, "variant")
	for _, item := range items {
		require.NotEqual(t, "message", item.Label)
	}
	items = componentCompletions(t, provider, `<twig:Alert :va`)
	requireCompletion(t, items, ":variant")
	items = componentCompletions(
		t,
		provider,
		`<twig:Alert><twig:block name="co`,
	)
	requireCompletion(t, items, "content")
}

func TestTwigLiveComponentCompletionShowsMappedWritableProps(t *testing.T) {
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

	classPath := filepath.Join(root, "src/Twig/Components/Search.php")
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveProp;
use Symfony\UX\TwigComponent\Attribute\ExposeInTemplate;
#[AsLiveComponent]
final class Search {
    #[ExposeInTemplate(name: 'headline')]
    private string $title = '';

    #[LiveProp(writable: true)]
    public string $query = '';
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	provider := NewTwigComponentCompletionProvider(componentIndex)

	items := componentCompletions(t, provider, `<twig:Se`)
	search := requireCompletion(t, items, "Search")
	require.Contains(t, search.Detail, "live component")

	items = componentCompletions(t, provider, `<twig:Search `)
	headline := requireCompletion(t, items, "headline")
	require.Equal(t, "string", headline.Detail)
	query := requireCompletion(t, items, "query")
	require.Contains(t, query.Detail, "writable live prop")
	for _, item := range items {
		require.NotEqual(t, "title", item.Label)
	}
}

func TestTwigLiveActionCompletionInHelperAndDataAttribute(t *testing.T) {
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
    public function save(
        #[LiveArg('itemId')] int $id,
        ?string $note = null,
    ): void {}

    #[LiveAction]
    public function removeItem(): void {}
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, class)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	templatePath := filepath.Join(
		root,
		"templates/components/Cart.html.twig",
	)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		[]byte("Cart"),
	)))
	provider := NewTwigComponentCompletionProvider(componentIndex)

	for _, source := range []string{
		`<button data-live-action-param="debounce(300)|sa`,
		`{{ live_action('rem`,
	} {
		items := componentCompletionsAtPath(
			t,
			provider,
			templatePath,
			source,
		)
		save := requireCompletion(t, items, "save")
		require.Contains(t, save.Detail, "int $id")
		requireCompletion(t, items, "removeItem")
	}

	items := componentCompletionsAtPath(
		t,
		provider,
		templatePath,
		`<button data-live-action-param="save" data-live-`,
	)
	itemID := requireCompletion(t, items, "data-live-item-id-param")
	require.Contains(t, itemID.Detail, "LiveArg(itemId)")
	edit, ok := itemID.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, `data-live-item-id-param="$0"`, edit.NewText)
	requireCompletion(t, items, "data-live-note-param")

	items = componentCompletionsAtPath(
		t,
		provider,
		templatePath,
		`<button data-live-action-param="save" data-live-note-param="" data-live-`,
	)
	for _, item := range items {
		require.NotEqual(t, "data-live-note-param", item.Label)
	}

	items = componentCompletionsAtPath(
		t,
		provider,
		templatePath,
		`{{ live_action('save', { it`,
	)
	itemID = requireCompletion(t, items, "itemId")
	require.Equal(t, "itemId: $0", itemID.InsertText)
	requireCompletion(t, items, "note")
}

func componentCompletions(
	t *testing.T,
	provider *TwigComponentCompletionProvider,
	source string,
) []protocol.CompletionItem {
	return componentCompletionsAtPath(
		t,
		provider,
		"/project/templates/page.html.twig",
		source,
	)
}

func componentCompletionsAtPath(
	t *testing.T,
	provider *TwigComponentCompletionProvider,
	path,
	source string,
) []protocol.CompletionItem {
	t.Helper()
	document := lsp.NewTextDocument(
		"file://"+path,
		source,
		1,
	)
	offset := uint32(len(source))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	node := document.SyntaxTree.Root.NodeAtOffset(
		uint32(strings.LastIndex(source, source[len(source)-1:])),
	)
	return provider.GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
}
