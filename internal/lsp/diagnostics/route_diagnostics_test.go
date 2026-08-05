package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteDiagnosticsForPHPAndTwig(t *testing.T) {
	provider := routeDiagnosticsFixture(t)
	tests := []struct {
		name    string
		uri     string
		source  string
		missing bool
	}{
		{
			name:   "existing PHP route",
			uri:    "file:///project/Controller.php",
			source: `<?php $this->redirectToRoute('product.show');`,
		},
		{
			name:    "missing PHP route",
			uri:     "file:///project/Controller.php",
			source:  `<?php $this->generateUrl('product.missing');`,
			missing: true,
		},
		{
			name:   "existing Twig route",
			uri:    "file:///project/view.twig",
			source: `{{ path('product.show') }}`,
		},
		{
			name:    "missing Twig route",
			uri:     "file:///project/view.twig",
			source:  `{{ url('product.missing') }}`,
			missing: true,
		},
		{
			name:   "existing Twig route comparison",
			uri:    "file:///project/view.twig",
			source: `{% if app.request.attributes.get('_route') == 'product.show' %}{% endif %}`,
		},
		{
			name:    "missing Twig route comparison",
			uri:     "file:///project/view.twig",
			source:  `{% if app.request.attributes.get('_route') in ['product.missing'] %}{% endif %}`,
			missing: true,
		},
		{
			name:   "Twig route prefix comparison is ignored",
			uri:    "file:///project/view.twig",
			source: `{% if app.request.attributes.get('_route') starts with 'product.missing' %}{% endif %}`,
		},
		{
			name:   "dynamic Twig route is ignored",
			uri:    "file:///project/view.twig",
			source: `{{ path("product.#{kind}") }}`,
		},
		{
			name:   "controller FQCN shortcut is ignored",
			uri:    "file:///project/view.twig",
			source: `{{ path('App\\Controller\\ProductController::show') }}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(test.uri, test.source, 1)
			result, err := provider.Analyze(context.Background(), document)
			require.NoError(t, err)
			if !test.missing {
				assert.Empty(t, result)
				return
			}
			require.Len(t, result, 1)
			assert.Equal(t, lsp.DiagnosticID("symfony.route.missing"), result[0].ID)
			assert.Equal(t, "Route 'product.missing' not found", result[0].Message)
			assert.Equal(t, "symfony", result[0].Source)
		})
	}
}

func TestRouteDiagnosticsIncludeTypoSuggestionsAndPreciseRange(t *testing.T) {
	provider := routeDiagnosticsFixture(t)
	source := `<?php $this->generateUrl('product.sho');`
	document := lsp.NewTextDocument(
		"file:///project/Controller.php",
		source,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Contains(t, problemSuggestionStrings(result[0]), "product.show")
	assert.Equal(t, "product.sho", problemRangeText(document, result[0].Range))
}

func TestRouteDiagnosticsValidateInheritedPHPDocAssistantReferences(
	t *testing.T,
) {
	provider := routeDiagnosticsFixture(t)
	source := `<?php
namespace App\Controller;
function open(RouteConsumer $consumer): void {
    $consumer->open('product.sho');
}`
	document := lsp.NewTextDocument(
		"file:///project/src/Usage.php",
		source,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, lsp.DiagnosticID("symfony.route.missing"), result[0].ID)
	assert.Contains(t, problemSuggestionStrings(result[0]), "product.show")
	assert.Equal(
		t,
		"product.sho",
		problemRangeText(document, result[0].Range),
	)
}

func TestTwigRouteComparisonDiagnosticsIncludeTypoSuggestion(t *testing.T) {
	provider := routeDiagnosticsFixture(t)
	source := `{% if app.request.attributes.get('_route') is same as('product.sho') %}{% endif %}`
	document := lsp.NewTextDocument(
		"file:///project/view.twig",
		source,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, lsp.DiagnosticID("symfony.route.missing"), result[0].ID)
	assert.Contains(t, problemSuggestionStrings(result[0]), "product.show")
	assert.Equal(t, "product.sho", problemRangeText(document, result[0].Range))
}

func TestRouteDiagnosticsMarkDeprecatedControllerActions(t *testing.T) {
	provider := routeDiagnosticsFixture(t)
	tests := []struct {
		name   string
		uri    string
		source string
		route  string
	}{
		{
			name:   "deprecated method from PHP",
			uri:    "file:///project/Consumer.php",
			source: `<?php $this->generateUrl('product.legacy_method');`,
			route:  "product.legacy_method",
		},
		{
			name:   "deprecated class from Twig",
			uri:    "file:///project/view.twig",
			source: `{{ path('product.legacy_class') }}`,
			route:  "product.legacy_class",
		},
		{
			name:   "deprecated service class from Twig",
			uri:    "file:///project/view.twig",
			source: `{{ path('product.legacy_service') }}`,
			route:  "product.legacy_service",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(test.uri, test.source, 1)
			result, err := provider.Analyze(
				context.Background(),
				document,
			)
			require.NoError(t, err)
			require.Len(t, result, 1)
			assert.Equal(t, deprecatedControllerCode, result[0].ID)
			assert.Equal(
				t,
				protocol.DiagnosticSeverityHint,
				result[0].Severity,
			)
			assert.Equal(
				t,
				[]protocol.DiagnosticTag{
					protocol.DiagnosticTagDeprecated,
				},
				result[0].Tags,
			)
			assert.Equal(
				t,
				test.route,
				problemRangeText(document, result[0].Range),
			)
		})
	}
}

func routeDiagnosticsFixture(t *testing.T) *RouteAnalyzer {
	t.Helper()
	routeIndex, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	require.NoError(t, routeIndex.Index(indexer.NewParsedFile(
		"/project/config/routes.yaml",
		[]byte(`product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
product.legacy_method:
  path: /products/legacy-method
  controller: App\Controller\ProductController::legacy
product.legacy_class:
  path: /products/legacy-class
  controller: App\Controller\LegacyController::show
product.legacy_service:
  path: /products/legacy-service
  controller: app.legacy_controller:show
`),
	)))

	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	classPath := filepath.Join(t.TempDir(), "Controllers.php")
	classSource := []byte(`<?php
namespace App\Controller;
class ProductController {
    public function show(): void {}
    /** @deprecated Use show instead. */
    public function legacy(): void {}
}
/** @deprecated Use ProductController instead. */
class LegacyController {
    public function show(): void {}
}
interface RouteAware {
    /** @param string $route #Route */
    public function open(string $route): void;
}
class RouteConsumer implements RouteAware {
    public function open(string $route): void {}
}
`)
	require.NoError(t, os.WriteFile(classPath, classSource, 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		classSource,
	)))

	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/config/services.yaml",
		[]byte(`services:
  app.legacy_controller:
    class: App\Controller\LegacyController
`),
	)))
	return NewRouteAnalyzer(
		routeIndex,
		serviceIndex,
		phpIndex,
	)
}
