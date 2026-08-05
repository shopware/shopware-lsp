package codelens

import (
	"context"
	"os"
	"path/filepath"
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

func TestSerializerCodeLensLinksClassToDeserializeUses(t *testing.T) {
	root := t.TempDir()
	modelPath := filepath.Join(root, "src", "Model.php")
	handlerPath := filepath.Join(root, "src", "Handler.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(modelPath), 0o755))
	modelSource := "<?php\nnamespace App;\nclass Model {}\n"
	handlerSource := `<?php
$serializer->deserialize($data, 'App\Model', 'json');
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
		uriutil.FileURI(modelPath),
		modelSource,
		1,
	)
	params := &protocol.CodeLensParams{}
	params.TextDocument.URI = document.URI
	lenses, err := NewSerializerCodeLensProvider(
		serializerIndex,
		phpIndex,
	).GetCodeLenses(context.Background(), &lsp.CodeLensRequest{
		CodeLensParams: params,
		Document:       document,
	})
	require.NoError(t, err)
	require.Len(t, lenses, 1)
	require.NotNil(t, lenses[0].Command)
	assert.Equal(t, "1 serializer use(s)", lenses[0].Command.Title)
	assert.Equal(t, "shopware.openReferences", lenses[0].Command.Command)
}
