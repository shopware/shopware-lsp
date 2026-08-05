package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestLiveComponentEventDiagnosticsReportUnknownEventsAndPayload(t *testing.T) {
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
	provider := NewLiveComponentEventAnalyzer(componentIndex)

	phpPath := filepath.Join(root, "src/Emitter.php")
	phpSource := []byte(`<?php class Emitter {
    public function save(string $dynamic): void {
        $this->emit('prodcutAdded');
        $this->emit('productAdded', ['prodcutId' => 42]);
        $this->emit($dynamic);
    }
}`)
	phpDocument := diagnosticsDocument(
		uriutil.FileURI(phpPath),
		phpSource,
	)
	values, err := provider.Analyze(
		context.Background(),
		phpDocument,
	)
	require.NoError(t, err)
	require.Len(t, values, 2)
	require.Equal(t, missingLiveEventCode, values[0].ID)
	require.Equal(
		t,
		"prodcutAdded",
		problemRangeText(phpDocument, values[0].Range),
	)
	require.Contains(
		t,
		values[0].Payload.(map[string]any)["suggestions"],
		"productAdded",
	)
	require.Equal(t, missingLiveEventArgumentCode, values[1].ID)
	require.Equal(
		t,
		"prodcutId",
		problemRangeText(phpDocument, values[1].Range),
	)
	require.Contains(
		t,
		values[1].Payload.(map[string]any)["suggestions"],
		"productId",
	)

	twigPath := filepath.Join(root, "templates/Cart.html.twig")
	twigSource := []byte(`<button
    data-action="click->live#emit"
    data-live-event-param="productAdded"
    data-live-prodcut-id-param="42"
></button>`)
	twigDocument := diagnosticsDocument(
		uriutil.FileURI(twigPath),
		twigSource,
	)
	values, err = provider.Analyze(
		context.Background(),
		twigDocument,
	)
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, missingLiveEventArgumentCode, values[0].ID)
	require.Equal(
		t,
		"prodcut-id",
		problemRangeText(twigDocument, values[0].Range),
	)
	require.Contains(
		t,
		values[0].Payload.(map[string]any)["suggestions"],
		"product-id",
	)
}
