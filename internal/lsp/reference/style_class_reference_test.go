package reference

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/style"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStyleClassReferencesConnectSCSSAndTwig(t *testing.T) {
	root := t.TempDir()
	stylePath := filepath.Join(root, "component.scss")
	firstPath := filepath.Join(root, "first.html.twig")
	secondPath := filepath.Join(root, "second.html.twig")
	files := map[string]string{
		stylePath:  `.sw-card { color: red; }`,
		firstPath:  `<div class="sw-card"></div>`,
		secondPath: `<section class="layout sw-card"></section>`,
	}
	index, err := style.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range files {
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
	offset := uint32(strings.Index(files[firstPath], "sw-card") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	request := styleClassReferenceRequest(document, node, offset, true)
	locations, referenceErr := NewStyleClassReferenceProvider(index).GetReferences(
		context.Background(),
		request,
	)

	require.NoError(t, referenceErr)
	require.Len(t, locations, 3)
	var uris []string
	for _, location := range locations {
		uris = append(uris, location.URI)
	}
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(stylePath),
		uriutil.FileURI(firstPath),
		uriutil.FileURI(secondPath),
	}, uris)

	request.Context.IncludeDeclaration = false
	locations, referenceErr = NewStyleClassReferenceProvider(index).GetReferences(
		context.Background(),
		request,
	)
	require.NoError(t, referenceErr)
	require.Len(t, locations, 2)
	assert.NotEqual(t, uriutil.FileURI(stylePath), locations[0].URI)
}

func styleClassReferenceRequest(
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
	includeDeclaration bool,
) *lsp.ReferenceRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = includeDeclaration
	return &lsp.ReferenceRequest{
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
	}
}
