package definition

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
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/serializer"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestSerializerDefinitionNavigatesStringTarget(t *testing.T) {
	root := t.TempDir()
	modelPath := filepath.Join(root, "src", "Model.php")
	handlerPath := filepath.Join(root, "src", "Handler.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(modelPath), 0o755))
	modelSource := "<?php\nnamespace App;\nclass Model {}\n"
	handlerSource := `<?php
namespace App;
function load($serializer): void {
    $serializer->deserialize($data, 'App\Model[]', 'json');
}
`
	require.NoError(t, os.WriteFile(modelPath, []byte(modelSource), 0o644))
	require.NoError(t, os.WriteFile(handlerPath, []byte(handlerSource), 0o644))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serializerIndex, err := serializer.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serializerIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		modelPath,
		[]byte(modelSource),
	)))
	require.NoError(t, serializerIndex.Index(indexer.NewParsedFile(
		handlerPath,
		[]byte(handlerSource),
	)))

	document := lsp.NewTextDocument(
		uriutil.FileURI(handlerPath),
		handlerSource,
		1,
	)
	offset := uint32(strings.Index(handlerSource, "App\\Model") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewSerializerDefinitionProvider(
		serializerIndex,
		phpIndex,
	).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(modelPath), locations[0].URI)
}
