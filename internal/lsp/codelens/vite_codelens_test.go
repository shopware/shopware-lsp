package codelens

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViteCodeLensLinksEntryTargetToConfigAndTwigUsages(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "assets", "app.js")
	configPath := filepath.Join(root, "vite.config.js")
	templatePath := filepath.Join(root, "templates", "base.html.twig")
	files := map[string]string{
		targetPath: "console.log('app');",
		configPath: `export default defineConfig({
  build: {rollupOptions: {input: {app: './assets/app.js'}}}
});`,
		templatePath: `{{ vite_entry_script_tags('app') }}`,
	}
	index, err := asset.NewIndex(root, t.TempDir())
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
		uriutil.FileURI(targetPath),
		files[targetPath],
		1,
	)
	params := &protocol.CodeLensParams{}
	params.TextDocument.URI = document.URI
	lenses, lensErr := NewViteCodeLensProvider(index).GetCodeLenses(
		context.Background(),
		&lsp.CodeLensRequest{
			CodeLensParams: params,
			Document:       document,
		},
	)
	require.NoError(t, lensErr)
	require.Len(t, lenses, 1)
	require.NotNil(t, lenses[0].Command)
	assert.Equal(t, "Vite entry 'app' · 2 related", lenses[0].Command.Title)
	assert.Equal(t, "shopware.openReferences", lenses[0].Command.Command)
	targets := lenses[0].Command.Arguments[0].([]string)
	require.Len(t, targets, 2)
	assert.Contains(t, targets[0]+targets[1], uriutil.FileURI(configPath))
	assert.Contains(t, targets[0]+targets[1], uriutil.FileURI(templatePath))
}
