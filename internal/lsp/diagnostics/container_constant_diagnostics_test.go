package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerConstantDiagnosticsForYAMLAndXML(t *testing.T) {
	phpIndex := containerConstantDiagnosticsPHPIndex(t)
	provider := NewContainerConstantAnalyzer(phpIndex)
	tests := []struct {
		name   string
		uri    string
		source string
	}{
		{
			name: "yaml",
			uri:  "file:///project/config/services.yaml",
			source: `parameters:
  active: !php/const App\Mode::ACTIVE
  global: !php/const APP_LIMIT
  legacy_missing: !php/const:App\Mode::MISSING
  modern_missing: !php/const App\Missing::VALUE
`,
		},
		{
			name: "xml",
			uri:  "file:///project/config/services.xml",
			source: `<container><services><service id="app">
  <argument type="constant">App\Mode::ACTIVE</argument>
  <argument type="constant">App\Mode::MISSING</argument>
  <argument type="string">App\Mode::IGNORED</argument>
</service></services></container>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := diagnosticsDocument(test.uri, []byte(test.source))
			result, err := provider.Analyze(
				context.Background(),
				document,
			)
			require.NoError(t, err)
			if test.name == "yaml" {
				require.Len(t, result, 2)
				assert.Equal(
					t,
					"App\\Mode::MISSING",
					problemRangeText(document, result[0].Range),
				)
				assert.Equal(
					t,
					"App\\Missing::VALUE",
					problemRangeText(document, result[1].Range),
				)
			} else {
				require.Len(t, result, 1)
				assert.Equal(
					t,
					"App\\Mode::MISSING",
					problemRangeText(document, result[0].Range),
				)
			}
			for _, diagnostic := range result {
				assert.Equal(
					t,
					missingContainerConstantCode,
					diagnostic.ID,
				)
				assert.Equal(
					t,
					protocol.DiagnosticSeverityError,
					diagnostic.Severity,
				)
				assert.Equal(
					t,
					"Symfony: constant not found",
					diagnostic.Message,
				)
			}
		})
	}
}

func containerConstantDiagnosticsPHPIndex(t *testing.T) *php.PHPIndex {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Mode.php",
		[]byte(`<?php
namespace {
    const APP_LIMIT = 10;
}
namespace App {
    class Mode {
        public const ACTIVE = 'active';
    }
}
`),
	)))
	return phpIndex
}
