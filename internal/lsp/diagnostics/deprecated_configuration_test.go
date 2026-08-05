package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyConfigurationDiagnosticsForYAML(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/config.yaml",
		`services:
  app.legacy:
    factory_class: App\Factory
    factory_method: create
    factory_service: app.factory
product.show:
  pattern: /products/{id}
  requirements:
    _method: GET
    '_scheme': https
framework:
  analyzer:
    pattern: '[a-z]+'
other:
  factory_method: unrelated
`,
		1,
	)
	result, err := NewLegacyConfigurationAnalyzer().
		Analyze(context.Background(), document)
	require.NoError(t, err)
	assertLegacyConfigurationDiagnostics(t, document, result, map[lsp.DiagnosticID]int{
		deprecatedFactorySettingCode: 3,
		deprecatedRoutePatternCode:   1,
		deprecatedRequirementCode:    2,
	})
}

func TestLegacyConfigurationDiagnosticsForXML(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/config.xml",
		`<container>
<services>
  <service id="app.legacy" factory-class="App\Factory"
    factory-method="create" factory-service="app.factory"/>
</services>
<routes>
  <route id="product.show" pattern="/products/{id}">
    <requirement key="_method">GET</requirement>
    <requirement key="_scheme">https</requirement>
  </route>
</routes>
<other pattern="not-a-route" factory-method="unrelated"/>
</container>`,
		1,
	)
	result, err := NewLegacyConfigurationAnalyzer().
		Analyze(context.Background(), document)
	require.NoError(t, err)
	assertLegacyConfigurationDiagnostics(t, document, result, map[lsp.DiagnosticID]int{
		deprecatedFactorySettingCode: 3,
		deprecatedRoutePatternCode:   1,
		deprecatedRequirementCode:    2,
	})
}

func TestLegacyConfigurationDiagnosticsIgnoreUnrelatedYAMLPattern(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/config/packages/search.yaml",
		`analyzer:
  pattern: '[a-z]+'
requirements:
  _method: metadata
`,
		1,
	)
	result, err := NewLegacyConfigurationAnalyzer().
		Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func assertLegacyConfigurationDiagnostics(
	t *testing.T,
	document *lsp.TextDocument,
	diagnostics []lsp.Problem,
	expected map[lsp.DiagnosticID]int,
) {
	t.Helper()
	actual := make(map[lsp.DiagnosticID]int)
	for _, diagnostic := range diagnostics {
		actual[diagnostic.ID]++
		assert.Equal(
			t,
			protocol.DiagnosticSeverityHint,
			diagnostic.Severity,
		)
		assert.Equal(
			t,
			[]protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
			diagnostic.Tags,
		)
		assert.NotEmpty(t, problemRangeText(document, diagnostic.Range))
	}
	assert.Equal(t, expected, actual)
}
