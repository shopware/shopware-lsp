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
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestLiveComponentEventDefinitionNavigatesListenerAndArgument(t *testing.T) {
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
	require.NoError(t, os.MkdirAll(filepath.Dir(classPath), 0o755))
	require.NoError(t, os.WriteFile(classPath, class, 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, class)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	provider := NewLiveComponentEventDefinitionProvider(componentIndex)

	templatePath := filepath.Join(root, "templates/Cart.html.twig")
	template := `<button data-action="click->live#emit" data-live-event-param="productAdded" data-live-product-id-param="42"></button>`
	eventLocations := liveEventDefinitionsAt(
		t,
		provider,
		templatePath,
		template,
		"productAdded",
	)
	require.Len(t, eventLocations, 1)
	require.Equal(t, uriutil.FileURI(classPath), eventLocations[0].URI)
	require.Equal(t, 7, eventLocations[0].Range.Start.Line)

	argumentLocations := liveEventDefinitionsAt(
		t,
		provider,
		templatePath,
		template,
		"product-id",
	)
	require.Len(t, argumentLocations, 1)
	require.Equal(t, uriutil.FileURI(classPath), argumentLocations[0].URI)
	require.Equal(t, 9, argumentLocations[0].Range.Start.Line)

	emitterPath := filepath.Join(root, "src/Emitter.php")
	emitter := `<?php class Emitter { public function save(): void {
    $this->emit('productAdded', ['productId' => 42]);
} }`
	phpLocations := liveEventDefinitionsAt(
		t,
		provider,
		emitterPath,
		emitter,
		"productAdded",
	)
	require.Len(t, phpLocations, 1)
	require.Equal(t, uriutil.FileURI(classPath), phpLocations[0].URI)
}

func liveEventDefinitionsAt(
	t *testing.T,
	provider *LiveComponentEventDefinitionProvider,
	path,
	source,
	needle string,
) []protocol.Location {
	t.Helper()
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, needle) + 1)
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
