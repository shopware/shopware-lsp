package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStimulusDefinitionNavigatesToControllerFiles(t *testing.T) {
	root := t.TempDir()
	controllerPath := filepath.Join(
		root,
		"assets",
		"controllers",
		"hello_controller.js",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	controllerSource := `import { Controller } from '@hotwired/stimulus';
export default class extends Controller {}`
	require.NoError(t, os.WriteFile(
		controllerPath,
		[]byte(controllerSource),
		0o644,
	))
	index, err := stimulus.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		controllerPath,
		[]byte(controllerSource),
	)))
	source := `{{ stimulus_controller('hello') }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates", "page.html.twig")),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "hello") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewStimulusDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(controllerPath), locations[0].URI)
}

func TestStimulusDefinitionNavigatesToControllersJSON(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "assets", "controllers.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	configSource := `{
  "controllers": {
    "@symfony/ux-chartjs": {
      "chart": {"enabled": true}
    }
  }
}`
	require.NoError(t, os.WriteFile(
		configPath,
		[]byte(configSource),
		0o644,
	))
	index, err := stimulus.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		configPath,
		[]byte(configSource),
	)))
	source := `{{ stimulus_controller('@symfony/ux-chartjs/chart') }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates", "page.html.twig")),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "ux-chartjs") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewStimulusDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(configPath), locations[0].URI)
	assert.Equal(t, 3, locations[0].Range.Start.Line)
}
