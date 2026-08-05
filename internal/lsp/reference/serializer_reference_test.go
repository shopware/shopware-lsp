package reference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/serializer"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestSerializerReferencesConnectClassAndDeserializeTargets(t *testing.T) {
	root := t.TempDir()
	modelPath := filepath.Join(root, "src", "Model.php")
	handlerPath := filepath.Join(root, "src", "Handler.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(modelPath), 0o755))
	modelSource := "<?php\nnamespace App;\nclass Model {}\n"
	handlerSource := `<?php
namespace App;
function load($serializer): void {
    $serializer->deserialize($data, Model::class, 'json');
    $serializer->deserialize($data, 'App\Model[]', 'json');
}
`
	for path, source := range map[string]string{
		modelPath:   modelSource,
		handlerPath: handlerSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serializerIndex, err := serializer.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serializerIndex.Close()) })
	for path, source := range map[string]string{
		modelPath:   modelSource,
		handlerPath: handlerSource,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
		require.NoError(t, serializerIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	document := lsp.NewTextDocument(
		uriutil.FileURI(modelPath),
		modelSource,
		1,
	)
	offset := uint32(strings.Index(modelSource, "Model") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		modelPath,
		1,
		node,
		document.SyntaxTree.Root,
	)
	locations, err := NewSerializerReferenceProvider(
		serializerIndex,
		phpIndex,
	).GetReferences(ctx, &lsp.ReferenceRequest{
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
	require.NoError(t, err)
	require.Len(t, locations, 3)
	assert.Equal(t, uriutil.FileURI(modelPath), locations[0].URI)
	assert.Equal(t, uriutil.FileURI(handlerPath), locations[1].URI)
	assert.Equal(t, uriutil.FileURI(handlerPath), locations[2].URI)
}
