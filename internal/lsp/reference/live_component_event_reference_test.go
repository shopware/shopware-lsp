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
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestLiveComponentEventReferencesIncludePHPAndTwigEmissions(t *testing.T) {
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
	phpEmitterPath := filepath.Join(root, "src/Emitter.php")
	twigEmitterPath := filepath.Join(root, "templates/Cart.html.twig")
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveListener;
#[AsLiveComponent]
final class Cart {
    #[LiveListener('productAdded')]
    public function refresh(): void {}
}`)
	phpEmitter := []byte(`<?php class Emitter {
    public function save(): void { $this->emit('productAdded'); }
}`)
	twigEmitter := []byte(
		`<button data-action="live#emit" data-live-event-param="productAdded"></button>`,
	)
	for path, source := range map[string][]byte{
		classPath:       class,
		phpEmitterPath:  phpEmitter,
		twigEmitterPath: twigEmitter,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, source, 0o644))
		require.NoError(t, componentIndex.Index(
			indexer.NewParsedFile(path, source),
		))
	}
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, class)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))

	document := lsp.NewTextDocument(
		uriutil.FileURI(twigEmitterPath),
		string(twigEmitter),
		1,
	)
	offset := uint32(strings.Index(string(twigEmitter), "productAdded") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewLiveComponentEventReferenceProvider(
		componentIndex,
	).GetReferences(context.Background(), &lsp.ReferenceRequest{
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
	})
	require.NoError(t, err)
	require.Len(t, locations, 3)
	require.Equal(t, 1, countComponentURI(
		locations,
		uriutil.FileURI(twigEmitterPath),
	))
	require.Equal(t, 1, countComponentURI(
		locations,
		uriutil.FileURI(phpEmitterPath),
	))
	require.Equal(t, 1, countComponentURI(
		locations,
		uriutil.FileURI(classPath),
	))
}
