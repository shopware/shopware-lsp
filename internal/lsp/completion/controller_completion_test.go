package completion

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControllerCompletionIncludesClassAndRouteServiceActions(t *testing.T) {
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	classPath := filepath.Join(t.TempDir(), "NavController.php")
	classSource := `<?php
namespace App\Controller;
class NavController {
    public function menu(string $section): void {}
    public function __invoke(): void {}
    protected function hidden(): void {}
}
namespace Acme\DemoBundle\Controller\Admin;
class LegacyController {
    public function showAction(): void {}
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		[]byte(classSource),
	)))
	services, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, services.Close()) })
	require.NoError(t, services.Index(indexer.NewParsedFile(
		"/project/services.yaml",
		[]byte(`services:
  app.navigation:
    class: App\Controller\NavController
`),
	)))
	routes, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routes.Close()) })
	require.NoError(t, routes.Index(indexer.NewParsedFile(
		"/project/routes.yaml",
		[]byte(`navigation:
  path: /navigation
  controller: app.navigation:menu
`),
	)))

	document := lsp.NewTextDocument(
		"file:///project/navigation.html.twig",
		`{{ render(controller('')) }}`,
		1,
	)
	literals := twigquery.Nodes(
		document.SyntaxTree.Root,
		twigsyntax.TwigLiteralString,
	)
	require.Len(t, literals, 1)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	provider := NewControllerCompletionProvider(phpIndex, services, routes)
	items := provider.GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:  document,
				Root:      document.SyntaxTree.Root,
				Node:      literals[0],
				LineIndex: document.LineIndex,
			},
		},
	)
	byLabel := make(map[string]protocol.CompletionItem)
	for _, item := range items {
		byLabel[item.Label] = item
	}
	for _, label := range []string{
		`App\Controller\NavController::menu`,
		`App\Controller\NavController`,
		"app.navigation:menu",
		"app.navigation::menu",
		"app.navigation",
		`DemoBundle:Admin\Legacy:show`,
	} {
		assert.Contains(t, byLabel, label)
	}
	assert.NotContains(t, byLabel, `App\Controller\NavController::hidden`)
	edit, ok := byLabel[`App\Controller\NavController::menu`].
		TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(
		t,
		`App\\Controller\\NavController::menu`,
		edit.NewText,
	)
}
