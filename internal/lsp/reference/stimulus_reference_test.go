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
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStimulusReferencesConnectDeclarationAndTwigUsages(t *testing.T) {
	root := t.TempDir()
	controllerPath := filepath.Join(
		root,
		"assets",
		"controllers",
		"hello_controller.js",
	)
	firstPath := filepath.Join(root, "templates", "first.html.twig")
	secondPath := filepath.Join(root, "templates", "second.html.twig")
	files := map[string]string{
		controllerPath: `import { Controller } from '@hotwired/stimulus';
export default class extends Controller {}`,
		firstPath:  `{{ stimulus_controller('hello') }}`,
		secondPath: `<div data-controller="hello"></div>`,
	}
	index, err := stimulus.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(firstPath),
		files[firstPath],
		1,
	)
	offset := uint32(strings.Index(files[firstPath], "hello") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, referenceErr := NewStimulusReferenceProvider(
		index,
	).GetReferences(context.Background(), &lsp.ReferenceRequest{
		ReferenceParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	})
	require.NoError(t, referenceErr)
	require.Len(t, locations, 3)
	assert.Equal(t, uriutil.FileURI(controllerPath), locations[0].URI)
	assert.ElementsMatch(
		t,
		[]string{uriutil.FileURI(firstPath), uriutil.FileURI(secondPath)},
		[]string{locations[1].URI, locations[2].URI},
	)
}
