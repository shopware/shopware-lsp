package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigRenderBlockDiagnosticsReportTyposOnlyOnAbstractController(
	t *testing.T,
) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "AbstractController.php"),
		[]byte(`<?php
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {}`),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "templates", "base.html.twig"),
		[]byte(`{% block content %}{% endblock %}
{% block sidebar %}{% endblock %}`),
	)))

	source := `<?php
namespace App;
class Controller extends \Symfony\Bundle\FrameworkBundle\Controller\AbstractController {
    public function page(): void {
        $this->renderBlock('base.html.twig', 'content');
        $this->renderBlock('base.html.twig', 'contnet');
    }
}
class Unrelated {
    public function page(): void {
        $this->renderBlock('base.html.twig', 'also_missing');
    }
}`
	path := filepath.Join(root, "src", "Controller.php")
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	result, err := NewTwigRenderBlockAnalyzer(
		twigIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument(uriutil.FileURI(path), []byte(source)),
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, missingTwigRenderBlockCode, result[0].ID)
	assert.Contains(t, result[0].Message, "contnet")
	data, ok := result[0].Payload.(map[string]any)
	require.True(t, ok)
	assert.Contains(t, data["suggestions"], "content")
}
