package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/projectconfig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestConfigurationLayersProjectAndEditorValues(t *testing.T) {
	root := t.TempDir()
	writeProjectConfiguration(t, root, `{
        "version": 1,
        "features": {"hover": false},
        "php": {"extensions": ["redis"]}
    }`)
	enabled := true
	empty := []string{}
	server := NewServer(nil, "", "test")
	_, err := server.initialize(context.Background(), &protocol.InitializeParams{
		RootURI: uriutil.FileURI(root),
		InitializationOptions: protocol.InitializationOptions{
			Configuration: &projectconfig.Partial{
				Features: map[string]bool{"hover": enabled},
				PHP:      &projectconfig.PHPConfig{Extensions: &empty},
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	effective := server.EffectiveConfiguration()
	require.True(t, effective.Features["hover"])
	require.Empty(t, effective.PHP.Extensions)
	require.Equal(t, "editor", effective.Origins["features.hover"])
}

func TestDiagnosticConfigurationDisablesAndOverridesRules(t *testing.T) {
	for _, test := range []struct {
		name        string
		diagnostics projectconfig.DiagnosticsConfig
		count       int
		severity    protocol.DiagnosticSeverity
	}{
		{name: "global", diagnostics: projectconfig.DiagnosticsConfig{Enabled: boolPointer(false)}},
		{name: "inspection", diagnostics: projectconfig.DiagnosticsConfig{Inspections: map[string]bool{"test.invalid-value": false}}},
		{name: "rule", diagnostics: projectconfig.DiagnosticsConfig{Rules: map[string]projectconfig.Severity{"test.invalid-value": projectconfig.SeverityOff}}},
		{name: "severity", diagnostics: projectconfig.DiagnosticsConfig{Rules: map[string]projectconfig.Severity{"test.invalid-value": projectconfig.SeverityError}}, count: 1, severity: protocol.DiagnosticSeverityError},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(nil, "", "test")
			server.RegisterInspection(testInspection{})
			_, err := server.initialize(context.Background(), &protocol.InitializeParams{
				RootURI: uriutil.FileURI(t.TempDir()),
				InitializationOptions: protocol.InitializationOptions{Configuration: &projectconfig.Partial{
					Diagnostics: &test.diagnostics,
				}},
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
			const uri = "file:///project/test.yaml"
			server.documentManager.OpenDocument(uri, "value: bad\n", 1)
			result := server.diagnostic(context.Background(), &protocol.DiagnosticParams{
				TextDocument: struct {
					URI string `json:"uri"`
				}{URI: uri},
			}).(protocol.DiagnosticResult)
			require.Len(t, result.Items, test.count)
			if test.count > 0 {
				require.Equal(t, test.severity, result.Items[0].Severity)
			}
		})
	}
}

func TestInvalidProjectConfigurationIsStrictForCLIAndSafeForEditor(t *testing.T) {
	root := t.TempDir()
	writeProjectConfiguration(t, root, `{"version":1,"features":{"unknown":false}}`)

	cliServer := NewServer(nil, "", "test")
	_, err := cliServer.initialize(context.Background(), &protocol.InitializeParams{
		RootURI:               uriutil.FileURI(root),
		InitializationOptions: protocol.InitializationOptions{CLIMode: true},
	})
	require.ErrorContains(t, err, "unknown feature")
	require.NoError(t, cliServer.CloseAll())

	editorServer := NewServer(nil, "", "test")
	_, err = editorServer.initialize(context.Background(), &protocol.InitializeParams{
		RootURI: uriutil.FileURI(root),
	})
	require.NoError(t, err)
	require.NotEmpty(t, editorServer.configurationCatalog().Error)
	require.True(t, editorServer.EffectiveConfiguration().Features["hover"])
	require.NoError(t, editorServer.CloseAll())
}

func TestReloadAppliesLiveSettingsAndRequestsRestartForStructuralSettings(t *testing.T) {
	root := t.TempDir()
	server := NewServer(nil, "", "test")
	_, err := server.initialize(context.Background(), &protocol.InitializeParams{RootURI: uriutil.FileURI(root)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })

	writeProjectConfiguration(t, root, `{"version":1,"features":{"hover":false}}`)
	result := server.reloadProjectConfiguration(context.Background())
	require.True(t, result.Applied)
	require.False(t, result.RestartRequired)
	require.False(t, server.EffectiveConfiguration().Features["hover"])

	writeProjectConfiguration(t, root, `{"version":1,"domains":{"scss":false}}`)
	result = server.reloadProjectConfiguration(context.Background())
	require.True(t, result.Applied)
	require.True(t, result.RestartRequired)
	// The running workspace retains its structural configuration until restart.
	require.True(t, server.EffectiveConfiguration().Domains["scss"])
}

func boolPointer(value bool) *bool { return &value }

func writeProjectConfiguration(t *testing.T, root, source string) {
	t.Helper()
	path := projectconfig.Path(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
}
