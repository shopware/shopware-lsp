package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStimulusDiagnosticsReportsMissingControllerWithSuggestion(t *testing.T) {
	index, err := stimulus.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/assets/controllers/hello_controller.js",
		[]byte(`import { Controller } from '@hotwired/stimulus';
export default class extends Controller {}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		`<div data-controller="helo"></div>`,
		1,
	)
	result, diagnosticsErr := NewStimulusAnalyzer(
		index,
	).Analyze(context.Background(), document)
	require.NoError(t, diagnosticsErr)
	require.Len(t, result, 1)
	assert.Equal(t, missingStimulusControllerCode, result[0].ID)
	assert.Equal(t, "hello", result[0].Payload.(map[string]any)["suggestions"].([]string)[0])
}
