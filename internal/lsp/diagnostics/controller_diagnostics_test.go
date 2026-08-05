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

func TestControllerDiagnosticsForYAMLTargetsAndMethods(t *testing.T) {
	provider, _ := controllerDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/routes.yaml",
		`product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
product.edit:
  path: /products/{id}/edit
  controller: App\Controller\ProductController::edit
product.service:
  path: /products/{id}
  controller: app.product_controller:show
product.unknown:
  path: /unknown
  controller: App\Controller\MissingController::show
product.legacy:
  path: /legacy
  controller: Bundle:Controller:action
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)

	byCode := make(map[lsp.DiagnosticID]protocolDiagnosticView)
	for _, diagnostic := range result {
		byCode[diagnostic.ID] = protocolDiagnosticView{
			message: diagnostic.Message,
			text:    problemRangeText(document, diagnostic.Range),
			data:    diagnostic.Payload,
		}
	}
	method := byCode[missingControllerMethodCode]
	assert.Equal(t, "edit", method.text)
	assert.Contains(t, method.message, "ProductController::edit")
	methodData := method.data.(map[string]any)
	assert.Equal(t, "edit", methodData["methodName"])
	assert.Equal(t, []string{"id"}, methodData["routeParameters"])
	assert.NotContains(t, methodData, "classURI")

	target := byCode[missingControllerTargetCode]
	assert.Equal(t, `App\Controller\MissingController`, target.text)
	assert.Contains(t, target.message, "Controller target")
}

func TestControllerDiagnosticsForXMLController(t *testing.T) {
	provider, _ := controllerDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/routes.xml",
		`<routes>
  <route id="product.edit" path="/products/{id}" controller="App\Controller\ProductController::edit"/>
</routes>`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, missingControllerMethodCode, result[0].ID)
	assert.Equal(t, "edit", problemRangeText(document, result[0].Range))
}

func TestControllerDiagnosticsForTwigController(t *testing.T) {
	provider, _ := controllerDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/navigation.html.twig",
		`{{ controller('App\\Controller\\ProductController::edit') }}
{{ render(controller('\\App\\Controller\\MissingController::show')) }}
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	byCode := make(map[lsp.DiagnosticID]protocolDiagnosticView)
	for _, diagnostic := range result {
		byCode[diagnostic.ID] = protocolDiagnosticView{
			message: diagnostic.Message,
			text:    problemRangeText(document, diagnostic.Range),
			data:    diagnostic.Payload,
		}
	}
	assert.Equal(t, "edit", byCode[missingControllerMethodCode].text)
	assert.Equal(
		t,
		`\\App\\Controller\\MissingController::show`,
		byCode[missingControllerTargetCode].text,
	)
}

func TestControllerDiagnosticsMarkDeprecatedActions(t *testing.T) {
	provider, _ := controllerDiagnosticsFixture(t)
	tests := []struct {
		name   string
		uri    string
		source string
		value  string
	}{
		{
			name: "YAML method",
			uri:  "file:///project/routes.yaml",
			source: `product.legacy:
  path: /legacy
  controller: App\Controller\ProductController::legacy
`,
			value: `App\Controller\ProductController::legacy`,
		},
		{
			name: "XML class",
			uri:  "file:///project/routes.xml",
			source: `<routes>
<route id="product.legacy" path="/legacy" controller="App\Controller\LegacyController::show"/>
</routes>`,
			value: `App\Controller\LegacyController::show`,
		},
		{
			name:   "Twig service",
			uri:    "file:///project/view.twig",
			source: `{{ controller('app.legacy_controller:show') }}`,
			value:  `app.legacy_controller:show`,
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
				test.value,
				problemRangeText(document, result[0].Range),
			)
		})
	}
}

type protocolDiagnosticView struct {
	message string
	text    string
	data    any
}

func controllerDiagnosticsFixture(
	t *testing.T,
) (*ControllerAnalyzer, string) {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	classPath := filepath.Join(t.TempDir(), "ProductController.php")
	content := []byte(`<?php
namespace App\Controller;
class ProductController
{
    public function show(int $id): void {}
    /** @deprecated Use show instead. */
    public function legacy(): void {}
}
/** @deprecated Use ProductController instead. */
class LegacyController
{
    public function show(): void {}
}
`)
	require.NoError(t, os.WriteFile(classPath, content, 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, content)))

	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/services.yaml",
		[]byte(`services:
  app.product_controller:
    class: App\Controller\ProductController
  app.legacy_controller:
    class: App\Controller\LegacyController
`),
	)))
	return NewControllerAnalyzer(serviceIndex, phpIndex), classPath
}
