package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDuplicateDiagnosticsForYAMLDefinitions(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/config.yaml",
		`parameters:
  app.mode: one
  app.mode: two
services:
  app.handler:
    class: App\First
  app.handler:
    class: App\Second
product.show:
  path: /first
product.show:
  path: /second
`,
		1,
	)
	result, err := NewDuplicateAnalyzer().Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	assertDuplicateCodes(t, result, map[lsp.DiagnosticID]int{
		duplicateRouteCode:     2,
		duplicateServiceCode:   2,
		duplicateParameterCode: 2,
	})
}

func TestDuplicateDiagnosticsForXMLDefinitions(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		expected map[lsp.DiagnosticID]int
	}{
		{
			name: "services and parameters",
			source: `<container>
<parameters>
  <parameter key="app.mode">one</parameter>
  <parameter key="app.mode">two</parameter>
</parameters>
<services>
  <service id="app.handler" class="App\First"/>
  <alias id="app.handler" service="app.first"/>
</services>
</container>`,
			expected: map[lsp.DiagnosticID]int{
				duplicateServiceCode:   2,
				duplicateParameterCode: 2,
			},
		},
		{
			name: "routes",
			source: `<routes>
  <route id="product.show" path="/first"/>
  <route id="product.show" path="/second"/>
</routes>`,
			expected: map[lsp.DiagnosticID]int{duplicateRouteCode: 2},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/config.xml",
				test.source,
				1,
			)
			result, err := NewDuplicateAnalyzer().Analyze(
				context.Background(),
				document,
			)
			require.NoError(t, err)
			assertDuplicateCodes(t, result, test.expected)
		})
	}
}

func TestDuplicateDiagnosticsForPHPRouteAttributes(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/Controller.php",
		`<?php
class Controller {
    #[Route('/first', name: 'product.show')]
    public function first(): void {}

    #[Route('/second', 'product.show')]
    public function second(): void {}
}`,
		1,
	)
	result, err := NewDuplicateAnalyzer().Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	assertDuplicateCodes(t, result, map[lsp.DiagnosticID]int{duplicateRouteCode: 2})
}

func assertDuplicateCodes(
	t *testing.T,
	diagnostics []lsp.Problem,
	expected map[lsp.DiagnosticID]int,
) {
	t.Helper()
	actual := make(map[lsp.DiagnosticID]int)
	for _, diagnostic := range diagnostics {
		actual[diagnostic.ID]++
		assert.Equal(t, protocol.DiagnosticSeverityWarning, diagnostic.Severity)
	}
	assert.Equal(t, expected, actual)
}
