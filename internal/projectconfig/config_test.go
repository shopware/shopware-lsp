package projectconfig

import (
	"encoding/json"
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
	assertCatalog("domainMap", DomainCatalog)
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
