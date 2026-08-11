package projectconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveLayersAndExplicitEmptyArrays(t *testing.T) {
	t.Parallel()
	project, err := Decode([]byte(`{
        "version": 1,
        "php": {"extensions": ["redis"]},
        "features": {"hover": false},
        "diagnostics": {"rules": {"php.arguments": "error"}},
        "check": {"failOn": "warning"}
    }`))
	require.NoError(t, err)
	empty := []string{}
	enabled := true
	editor := Partial{
		PHP:      &PHPConfig{Extensions: &empty},
		Features: map[string]bool{"hover": true},
		Diagnostics: &DiagnosticsConfig{
			Enabled: &enabled,
			Rules:   map[string]Severity{"php.arguments": SeverityOff},
		},
	}
	effective := Resolve(project, editor)
	require.Empty(t, effective.PHP.Extensions)
	require.True(t, effective.Features["hover"])
	require.Equal(t, SeverityOff, effective.Diagnostics.Rules["php.arguments"])
	require.Equal(t, SeverityWarning, effective.Check.FailOn)
	require.Equal(t, "editor", effective.Origins["php.extensions"])
}

func TestSchemaCatalogsStayInSync(t *testing.T) {
	t.Parallel()
	var schema struct {
		Definitions map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(Schema, &schema))
	assertCatalog := func(definition string, catalog []CatalogEntry) {
		actual := make([]string, 0, len(schema.Definitions[definition].Properties))
		for id := range schema.Definitions[definition].Properties {
			actual = append(actual, id)
		}
		expected := make([]string, 0, len(catalog))
		for _, entry := range catalog {
			expected = append(expected, entry.ID)
		}
		sort.Strings(actual)
		sort.Strings(expected)
		require.Equal(t, expected, actual)
	}
	assertCatalog("featureMap", FeatureCatalog)
	assertCatalog("mcpToolMap", MCPToolCatalog)
	assertCatalog("domainMap", DomainCatalog)
}

func TestLoadUsesShopwareLSPConfigurationPath(t *testing.T) {
	root := t.TempDir()
	require.Equal(t, filepath.Join(root, ".config", "shopware", "lsp.json"), Path(root))
	require.NoError(t, os.MkdirAll(filepath.Dir(Path(root)), 0o755))
	require.NoError(t, os.WriteFile(Path(root), []byte(`{"version":1}`), 0o644))
	_, found, err := Load(root)
	require.NoError(t, err)
	require.True(t, found)
}

func TestResolveCascadesDisabledDependencies(t *testing.T) {
	t.Parallel()
	project, err := Decode([]byte(`{"version":1,"domains":{"php":false}}`))
	require.NoError(t, err)
	effective := Resolve(project, Partial{})
	require.False(t, effective.Domains["symfony.services"])
	require.False(t, effective.Domains["twig"])
	require.Contains(t, effective.DisabledReason["twig"], "requires")
}

func TestDecodeRejectsUnknownAndInvalidValues(t *testing.T) {
	t.Parallel()
	_, err := Decode([]byte(`{"version":1,"features":{"future":false}}`))
	require.ErrorContains(t, err, "unknown feature")
	_, err = Decode([]byte(`{"version":1,"diagnostics":{"rules":{"php.arguments":"loud"}}}`))
	require.ErrorContains(t, err, "invalid severity")
	_, err = Decode([]byte(`{"version":1,"unknown":true}`))
	require.ErrorContains(t, err, "unknown field")
	_, err = Decode([]byte(`{"version":1,"shopware":{"targetVersion":"next"}}`))
	require.ErrorContains(t, err, "invalid Shopware target version")
	_, err = Decode([]byte(`{"version":1,"mcp":{"tools":{"future_tool":false}}}`))
	require.ErrorContains(t, err, "unknown MCP tool")
	_, err = Decode([]byte(`{"version":1,"diagnostics":{"overrides":[{"files":[],"enabled":false}]}}`))
	require.ErrorContains(t, err, "files must not be empty")
	_, err = Decode([]byte(`{"version":1,"diagnostics":{"overrides":[{"files":["../outside/**"],"enabled":false}]}}`))
	require.ErrorContains(t, err, "must not escape")
}

func TestResolveMCPToolOverrides(t *testing.T) {
	t.Parallel()
	project, err := Decode([]byte(`{
        "version": 1,
        "mcp": {"tools": {"shopware_scaffold": false}}
    }`))
	require.NoError(t, err)
	editor := Partial{MCP: &MCPConfig{Tools: map[string]bool{
		"shopware_scaffold": true,
		"shopware_hover":    false,
	}}}
	effective := Resolve(project, editor)
	require.True(t, effective.MCPToolEnabled("shopware_scaffold"))
	require.False(t, effective.MCPToolEnabled("shopware_hover"))
	require.True(t, effective.MCPToolEnabled("shopware_diagnostics"))
	require.Equal(t, "editor", effective.Origins["mcp.tools.shopware_scaffold"])
}

func TestApplyDiagnosticsUsesOrderedPathOverrides(t *testing.T) {
	t.Parallel()
	disabled := false
	enabled := true
	configuration := &DiagnosticsConfig{
		Rules: map[string]Severity{"php.arguments": SeverityWarning},
		Overrides: []DiagnosticOverride{
			{Files: []string{"src/**"}, Enabled: &disabled},
			{Files: []string{`src\Generated\Keep.php`}, Enabled: &enabled,
				Rules: map[string]Severity{"php.arguments": SeverityOff}},
		},
	}
	policy := DefaultDiagnosticPolicy()
	ApplyDiagnostics(&policy, configuration, "src/Generated/Keep.php")
	require.True(t, policy.Enabled)
	require.Equal(t, SeverityOff, policy.Rules["php.arguments"])

	other := DefaultDiagnosticPolicy()
	ApplyDiagnostics(&other, configuration, "tests/Example.php")
	require.True(t, other.Enabled)
	require.Equal(t, SeverityWarning, other.Rules["php.arguments"])
}

func TestLoadScopesDiscoversNestedDiagnosticsConfigurations(t *testing.T) {
	root := t.TempDir()
	plugin := filepath.Join(root, "custom", "plugins", "Example")
	configPath := Path(plugin)
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(configPath, []byte(
		`{"version":1,"diagnostics":{"rules":{"php.arguments":"off"}}}`,
	), 0o644))
	invalid := filepath.Join(root, "custom", "plugins", "Invalid")
	require.NoError(t, os.MkdirAll(filepath.Dir(Path(invalid)), 0o755))
	require.NoError(t, os.WriteFile(Path(invalid), []byte(
		`{"version":1,"domains":{"php":false}}`,
	), 0o644))

	scopes, err := LoadScopes(root)
	require.NoError(t, err)
	require.Len(t, scopes, 2)
	require.Equal(t, plugin, scopes[0].Root)
	require.Empty(t, scopes[0].Error)
	require.Equal(t, SeverityOff, scopes[0].Configuration.Diagnostics.Rules["php.arguments"])
	require.ErrorContains(t, ScopeErrors(scopes), "nested configuration may only contain diagnostics")
}

func TestStructuralFingerprintIgnoresLiveSettings(t *testing.T) {
	t.Parallel()
	first := Default()
	second := Default()
	second.Features["hover"] = false
	second.Diagnostics.Enabled = false
	require.Equal(t, first.StructuralFingerprint(), second.StructuralFingerprint())
	second.Domains["scss"] = false
	require.NotEqual(t, first.StructuralFingerprint(), second.StructuralFingerprint())
}
