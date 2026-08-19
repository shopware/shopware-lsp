package lsp

import (
	"context"
	"errors"
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

func TestSCSSInspectionUsesSCSSDomain(t *testing.T) {
	require.Equal(t, "scss", inspectionDomain("scss.variable"))
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

func TestNestedConfigurationAppliesOnlyInsideItsExtensionScope(t *testing.T) {
	root := t.TempDir()
	writeProjectConfiguration(t, root, `{
        "version":1,
        "diagnostics":{"rules":{"test.invalid-value":"error"}}
    }`)
	plugin := filepath.Join(root, "custom", "plugins", "Example")
	writeProjectConfiguration(t, plugin, `{
        "version":1,
        "diagnostics":{
            "rules":{"test.invalid-value":"off"},
            "overrides":[{
                "files":["src/Keep.yaml"],
                "rules":{"test.invalid-value":"warning"}
            }]
        }
    }`)

	server := NewServer(nil, "", "test")
	server.RegisterInspection(testInspection{})
	_, err := server.initialize(context.Background(), &protocol.InitializeParams{
		RootURI: uriutil.FileURI(root),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })

	require.Empty(t, configuredDiagnosticsForPath(
		t, server, filepath.Join(plugin, "src", "Ignored.yaml"),
	))
	kept := configuredDiagnosticsForPath(
		t, server, filepath.Join(plugin, "src", "Keep.yaml"),
	)
	require.Len(t, kept, 1)
	require.Equal(t, protocol.DiagnosticSeverityWarning, kept[0].Severity)
	outside := configuredDiagnosticsForPath(
		t, server, filepath.Join(root, "src", "Outside.yaml"),
	)
	require.Len(t, outside, 1)
	require.Equal(t, protocol.DiagnosticSeverityError, outside[0].Severity)
}

func TestEditorPathOverrideWinsNestedExtensionConfiguration(t *testing.T) {
	root := t.TempDir()
	plugin := filepath.Join(root, "custom", "plugins", "Example")
	writeProjectConfiguration(t, plugin,
		`{"version":1,"diagnostics":{"rules":{"test.invalid-value":"off"}}}`,
	)
	warning := projectconfig.SeverityWarning
	server := NewServer(nil, "", "test")
	server.RegisterInspection(testInspection{})
	_, err := server.initialize(context.Background(), &protocol.InitializeParams{
		RootURI: uriutil.FileURI(root),
		InitializationOptions: protocol.InitializationOptions{Configuration: &projectconfig.Partial{
			Diagnostics: &projectconfig.DiagnosticsConfig{Overrides: []projectconfig.DiagnosticOverride{{
				Files: []string{"custom/plugins/Example/src/Keep.yaml"},
				Rules: map[string]projectconfig.Severity{"test.invalid-value": warning},
			}}},
		}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })

	diagnostics := configuredDiagnosticsForPath(
		t, server, filepath.Join(plugin, "src", "Keep.yaml"),
	)
	require.Len(t, diagnostics, 1)
	require.Equal(t, protocol.DiagnosticSeverityWarning, diagnostics[0].Severity)
}

func TestInvalidNestedReloadKeepsLastKnownGoodDiagnostics(t *testing.T) {
	root := t.TempDir()
	plugin := filepath.Join(root, "custom", "plugins", "Example")
	writeProjectConfiguration(t, plugin,
		`{"version":1,"diagnostics":{"rules":{"test.invalid-value":"off"}}}`,
	)
	server := NewServer(nil, "", "test")
	server.RegisterInspection(testInspection{})
	_, err := server.initialize(context.Background(), &protocol.InitializeParams{
		RootURI: uriutil.FileURI(root),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	path := filepath.Join(plugin, "src", "Ignored.yaml")
	require.Empty(t, configuredDiagnosticsForPath(t, server, path))

	writeProjectConfiguration(t, plugin, `{"version":1,"domains":{"php":false}}`)
	result := server.reloadProjectConfiguration(context.Background())
	require.True(t, result.Applied)
	require.ErrorContains(t, errors.New(result.Error), "nested configuration may only contain diagnostics")
	require.Empty(t, configuredDiagnosticsForPath(t, server, path))
}

func configuredDiagnosticsForPath(
	t *testing.T,
	server *Server,
	path string,
) []protocol.Diagnostic {
	t.Helper()
	uri := uriutil.FileURI(path)
	server.documentManager.OpenDocument(uri, "value: bad\n", 1)
	result := server.diagnostic(context.Background(), &protocol.DiagnosticParams{
		TextDocument: struct {
			URI string `json:"uri"`
		}{URI: uri},
	}).(protocol.DiagnosticResult)
	return result.Items
}

func boolPointer(value bool) *bool { return &value }

func writeProjectConfiguration(t *testing.T, root, source string) {
	t.Helper()
	path := projectconfig.Path(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
}
