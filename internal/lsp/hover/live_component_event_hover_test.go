package hover

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestLiveComponentEventHoverShowsListenerAndAliasedPayload(t *testing.T) {
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
    ): void {}
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, class)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	provider := NewLiveComponentEventHoverProvider(componentIndex)
	path := filepath.Join(root, "templates/Cart.html.twig")
	source := `<button data-action="live#emit" data-live-event-param="productAdded" data-live-product-id-param="42"></button>`

	event := liveEventHoverAt(
		t,
		provider,
		path,
		source,
		"productAdded",
	)
	require.Contains(t, event.Contents.Value, "Symfony UX Live event")
	require.Contains(t, event.Contents.Value, "Cart::refresh")
	require.Contains(t, event.Contents.Value, "int $id")

	argument := liveEventHoverAt(
		t,
		provider,
		path,
		source,
		"product-id",
	)
	require.Contains(t, argument.Contents.Value, "Live event argument")
	require.Contains(t, argument.Contents.Value, "PHP type: `int`")
	require.Contains(t, argument.Contents.Value, "$id")
	require.Contains(t, argument.Contents.Value, "#[LiveArg]")
}

func liveEventHoverAt(
	t *testing.T,
	provider *LiveComponentEventHoverProvider,
	path,
	source,
	needle string,
) *protocol.Hover {
	t.Helper()
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, needle) + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := provider.GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
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
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}
