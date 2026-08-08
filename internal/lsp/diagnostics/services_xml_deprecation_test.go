package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestServicesXMLDeprecationDiagnostic(t *testing.T) {
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			t.TempDir(), "src/Resources/config/services.xml",
		)),
		`<?xml version="1.0"?>
<container xmlns="http://symfony.com/schema/dic/services">
    <services>
        <service id="App\Example" class="App\Example"/>
    </services>
</container>`,
		1,
	)

	problems, err := NewServicesXMLDeprecationAnalyzer().Analyze(
		context.Background(), document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, ServicesXMLDeprecatedCode, problems[0].ID)
	require.Equal(t, protocol.DiagnosticSeverityWarning, problems[0].Severity)
	require.Equal(t, []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated}, problems[0].Tags)
	require.Equal(t, "container", problemRangeText(document, problems[0].Range))
	payload, ok := problems[0].Payload.(ServicesXMLDeprecationPayload)
	require.True(t, ok)
	require.True(t, payload.Convertible)
}

func TestServicesXMLDeprecationDiagnosticHasNoAutomaticConversionForUnsupportedXML(t *testing.T) {
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			t.TempDir(), "Resources/config/services.xml",
		)),
		`<container><services><stack id="app"/></services></container>`,
		1,
	)

	problems, err := NewServicesXMLDeprecationAnalyzer().Analyze(
		context.Background(), document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	payload, ok := problems[0].Payload.(ServicesXMLDeprecationPayload)
	require.True(t, ok)
	require.False(t, payload.Convertible)
}

func TestServicesXMLDeprecationDiagnosticIgnoresExplicitlyLoadedXML(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/config/services.xml",
		`<container/>`,
		1,
	)

	problems, err := NewServicesXMLDeprecationAnalyzer().Analyze(
		context.Background(), document,
	)
	require.NoError(t, err)
	require.Empty(t, problems)
}
