package symbol

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSymfonyWorkspaceSymbolsCoverPluginNavigationDomains(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	routeIndex, err := symfony.NewRouteIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	commandIndex, err := console.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, commandIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	doctrineIndex, err := doctrine.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	componentIndex, err := twigcomponent.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(nil, serviceIndex, twigIndex)
	translationIndex, err := translation.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, translationIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	servicePath := filepath.Join(root, "config", "services.xml")
	routePath := filepath.Join(root, "config", "routes.yaml")
	commandPath := filepath.Join(root, "src", "Command", "CreateUser.php")
	templatePath := filepath.Join(
		root,
		"templates",
		"layout",
		"base.html.twig",
	)
	extensionPath := filepath.Join(root, "src", "Twig", "ShopExtension.php")
	entityPath := filepath.Join(root, "src", "Entity", "Product.php")
	componentPath := filepath.Join(
		root,
		"src",
		"Twig",
		"Components",
		"Alert.php",
	)
	controllerPath := filepath.Join(
		root,
		"src",
		"Controller",
		"CatalogController.php",
	)
	translationPath := filepath.Join(root, "translations", "messages.en.yaml")
	files := map[string]string{
		servicePath: `<container><services>
<service id="app.mailer" class="App\Mailer"/>
</services><parameters>
<parameter key="kernel.environment">test</parameter>
</parameters></container>`,
		routePath: `catalog.show:
  path: /catalog/{id}
  methods: [GET]
  controller: App\Controller\CatalogController::show
`,
		commandPath: `<?php
namespace App\Command;
#[AsCommand(name: 'app:user:create', description: 'Creates a user')]
final class CreateUser {}
`,
		templatePath: `{% block page_content %}{% endblock %}
{% macro render_card(title) %}{{ title }}{% endmacro %}
`,
		extensionPath: `<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
final class ShopExtension extends AbstractExtension {
    public function getFunctions(): array {
        return [new TwigFunction('shop_currency', [$this, 'currency'])];
    }
    public function currency(string $iso): string {}
}
`,
		entityPath: `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
#[ORM\Table(name: 'products')]
final class Product {}
`,
		componentPath: `<?php
namespace App\Twig\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent(name: 'Alert')]
final class Alert {}
`,
		controllerPath: `<?php
namespace App\Controller;
final class CatalogController {
    public function show(): void {}
    private function helper(): void {}
}
`,
		translationPath: `checkout.complete: Order completed
`,
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, serviceIndex.Index(parsed))
		require.NoError(t, routeIndex.Index(parsed))
		require.NoError(t, commandIndex.Index(parsed))
		require.NoError(t, twigIndex.Index(parsed))
		require.NoError(t, doctrineIndex.Index(parsed))
		require.NoError(t, componentIndex.Index(parsed))
		require.NoError(t, translationIndex.Index(parsed))
		if filepath.Ext(path) == ".php" {
			require.NoError(t, phpIndex.Index(parsed))
		}
	}

	provider := NewSymfonyWorkspaceSymbolProvider(
		serviceIndex,
		routeIndex,
		commandIndex,
		twigIndex,
		doctrineIndex,
		componentIndex,
		translationIndex,
		phpIndex,
	)
	tests := []struct {
		query      string
		name       string
		container  string
		path       string
		kind       protocol.SymbolKind
		exactRange bool
	}{
		{
			query: "app.mailer", name: "app.mailer",
			container: "Symfony service", path: servicePath,
			kind: protocol.SymbolObject,
		},
		{
			query: "kernel.environment", name: "kernel.environment",
			container: "Symfony parameter", path: servicePath,
			kind: protocol.SymbolConstant,
		},
		{
			query: "catalog.show", name: "catalog.show",
			container: "Symfony route · GET /catalog/{id} · CatalogController:show",
			path:      routePath,
			kind:      protocol.SymbolMethod,
		},
		{
			query: "CatalogController::show",
			name:  "App\\Controller\\CatalogController::show",
			container: "Symfony controller · " +
				"App\\Controller\\CatalogController",
			path: controllerPath, kind: protocol.SymbolMethod,
			exactRange: true,
		},
		{
			query: "/catalog/42", name: "/catalog/42",
			container: "Symfony route URL", path: routePath,
			kind: protocol.SymbolMethod,
		},
		{
			query:     "https://shop.example/catalog/foo.bar?preview=1#details",
			name:      "https://shop.example/catalog/foo.bar?preview=1#details",
			container: "Symfony route URL", path: routePath,
			kind: protocol.SymbolMethod,
		},
		{
			query: "app:user:create", name: "app:user:create",
			container: "Symfony command", path: commandPath,
			kind: protocol.SymbolFunction, exactRange: true,
		},
		{
			query: "layout/base.html.twig", name: "layout/base.html.twig",
			container: "Twig template", path: templatePath,
			kind: protocol.SymbolFile,
		},
		{
			query: "page_content", name: "page_content",
			container: "Twig block", path: templatePath,
			kind: protocol.SymbolField, exactRange: true,
		},
		{
			query: "render_card", name: "render_card",
			container: "Twig macro", path: templatePath,
			kind: protocol.SymbolFunction, exactRange: true,
		},
		{
			query: "shop_currency", name: "shop_currency",
			container: "Twig function", path: extensionPath,
			kind: protocol.SymbolFunction,
		},
		{
			query: "Product", name: "Product",
			container: "Doctrine entity", path: entityPath,
			kind: protocol.SymbolClass, exactRange: true,
		},
		{
			query: "products", name: "products",
			container: "Doctrine table", path: entityPath,
			kind: protocol.SymbolStruct, exactRange: true,
		},
		{
			query: "Alert", name: "Alert",
			container: "Twig component", path: componentPath,
			kind: protocol.SymbolClass, exactRange: true,
		},
		{
			query: "checkout.complete", name: "checkout.complete",
			container: "Translation", path: translationPath,
			kind: protocol.SymbolString, exactRange: true,
		},
		{
			query: "messages", name: "messages",
			container: "Translation domain", path: translationPath,
			kind: protocol.SymbolNamespace, exactRange: true,
		},
	}
	for _, test := range tests {
		symbols, symbolErr := provider.WorkspaceSymbols(
			context.Background(),
			test.query,
		)
		require.NoError(t, symbolErr, test.query)
		current := requireSymbol(t, symbols, test.name, test.container)
		assert.Equal(t, test.kind, current.Kind)
		assert.Equal(t, uriutil.FileURI(test.path), current.Location.URI)
		if test.exactRange {
			assert.NotEqual(
				t,
				current.Location.Range.Start,
				current.Location.Range.End,
			)
		}
	}
}

func requireSymbol(
	t *testing.T,
	symbols []protocol.SymbolInformation,
	name,
	container string,
) protocol.SymbolInformation {
	t.Helper()
	for _, current := range symbols {
		if current.Name == name &&
			strings.Contains(current.ContainerName, container) {
			return current
		}
	}
	t.Fatalf(
		"symbol %q in %q not found in %#v",
		name,
		container,
		symbols,
	)
	return protocol.SymbolInformation{}
}
