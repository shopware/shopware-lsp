package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func TestTwigMacroDiagnosticsReportUnknownImportedMacro(t *testing.T) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/macros/forms.html.twig",
		[]byte(`{% macro input(name) %}{% endmacro %}`),
	)))
	source := []byte(`{% import 'macros/forms.html.twig' as forms %}
{{ forms.inpt('email') }}
{{ forms.input('name') }}
`)
	diagnostics, err := NewTwigMacroAnalyzer(
		index,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/templates/page.html.twig",
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, missingTwigMacroCode, diagnostics[0].ID)
	assert.Contains(t, diagnostics[0].Message, "inpt")
	assert.Contains(
		t,
		diagnostics[0].Payload.(map[string]any)["suggestions"],
		"input",
	)
}

func TestTwigMacroDiagnosticsOverlayUnsavedSelfMacros(t *testing.T) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	source := []byte(`{% import _self as local %}
{{ local.helper() }}
{% macro helper() %}{% endmacro %}
`)
	diagnostics, err := NewTwigMacroAnalyzer(
		index,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/templates/page.html.twig",
			source,
		),
	)
	require.NoError(t, err)
	assert.Empty(t, diagnostics)
}
