package completion

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestLiveComponentEventCompletionInPHPAndTwig(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	componentIndex, err := twigcomponent.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, nil, nil)

	classPath := filepath.Join(root, "src/Twig/Components/Cart.php")
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveArg;
use Symfony\UX\LiveComponent\Attribute\LiveListener;
#[AsLiveComponent]
final class Cart {
    #[LiveListener('productAdded')]
    public function refresh(
        #[LiveArg('productId')] int $id,
        #[LiveArg] string $source,
    ): void {}
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, class)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	provider := NewLiveComponentEventCompletionProvider(componentIndex)

	items := liveEventCompletions(
		t,
		provider,
		filepath.Join(root, "src/Emitter.php"),
		`<?php class Emitter { function save() { $this->emit('prod`,
	)
	event := requireCompletion(t, items, "productAdded")
	require.Contains(t, event.Detail, "Cart::refresh")

	items = liveEventCompletions(
		t,
		provider,
		filepath.Join(root, "src/Emitter.php"),
		`<?php class Emitter { function save() { $this->emit('productAdded', ['pro`,
	)
	product := requireCompletion(t, items, "productId")
	require.Equal(t, "'productId' => $0", product.InsertText)
	require.Contains(t, product.Detail, "LiveArg(productId)")
	requireCompletion(t, items, "source")

	templatePath := filepath.Join(root, "templates/Cart.html.twig")
	items = liveEventCompletions(
		t,
		provider,
		templatePath,
		`<button data-action="live#emit" data-live-event-param="prod`,
	)
	requireCompletion(t, items, "productAdded")

	items = liveEventCompletions(
		t,
		provider,
		templatePath,
		`<button data-action="live#emit" data-live-event-param="productAdded" data-live-`,
	)
	attribute := requireCompletion(
		t,
		items,
		"data-live-product-id-param",
	)
	edit, ok := attribute.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, `data-live-product-id-param="$0"`, edit.NewText)
	requireCompletion(t, items, "data-live-source-param")
}

func liveEventCompletions(
	t *testing.T,
	provider *LiveComponentEventCompletionProvider,
	path,
	source string,
) []protocol.CompletionItem {
	t.Helper()
	document := lsp.NewTextDocument(
		uriutil.FileURI(path),
		source,
		1,
	)
	offset := uint32(len(source))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	nodeOffset := offset
	if nodeOffset > 0 {
		nodeOffset--
	}
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
				Node: document.SyntaxTree.Root.NodeAtOffset(
					nodeOffset,
				),
			},
		},
	)
}
