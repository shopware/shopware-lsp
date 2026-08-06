//go:build integration

package entityschema

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

// TestRealWorldPluginEntityRoundTrip exercises the designer importer against a
// real plugin checkout without modifying it. Override the default fixture with
// SHOPWARE_LSP_ENTITY_PLUGIN_ROOT.
func TestRealWorldPluginEntityRoundTrip(t *testing.T) {
	pluginRoot := os.Getenv("SHOPWARE_LSP_ENTITY_PLUGIN_ROOT")
	if pluginRoot == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		pluginRoot = filepath.Join(home, "Developer", "sw-trunk", "custom", "plugins", "FroshMySQLSearch")
	}
	if info, err := os.Stat(pluginRoot); err != nil || !info.IsDir() {
		t.Skipf("real-world entity plugin is unavailable: %s", pluginRoot)
	}

	schema, definitions, err := ScanPluginSchema(pluginRoot)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(definitions), 4)
	require.Len(t, schema.Entities, len(definitions))
	lookup := relationLookupFromDefinitions(definitions)
	for _, definition := range definitions {
		t.Run(definition.Spec.EntityName, func(t *testing.T) {
			require.Empty(t, ValidateSpec(definition.Spec))
			source, renderErr := RenderDefinition(definition.Spec)
			require.NoError(t, renderErr)
			require.Empty(t, php.Parse(source).Errors)
			roundTripped, importErr := ImportDefinition(source, lookup)
			require.NoError(t, importErr)
			before, schemaErr := SchemaFromSpec(definition.Spec)
			require.NoError(t, schemaErr)
			after, schemaErr := SchemaFromSpec(roundTripped)
			require.NoError(t, schemaErr)
			before.BackfillFree()
			after.BackfillFree()
			require.True(t, reflect.DeepEqual(before.NormalizeForTest(), after.NormalizeForTest()), "schema changed after render/import round trip")
		})
	}
}

func relationLookupFromDefinitions(definitions []ScannedDefinition) RelationLookup {
	targets := make(map[string]RelationTarget, len(definitions))
	for _, definition := range definitions {
		target := RelationTarget{DefinitionClass: definition.Spec.DefinitionClass, EntityClass: definition.Spec.EntityClass, CollectionClass: definition.Spec.CollectionClass, EntityName: definition.Spec.EntityName}
		for _, field := range definition.Spec.Fields {
			if field.Kind == FieldVersion {
				target.VersionAware = true
			}
			if field.StorageName != "" && field.Kind != FieldLocked && !isNonStoredForTest(field) {
				target.Fields = append(target.Fields, RelationTargetField{PropertyName: field.PropertyName, StorageName: field.StorageName, Primary: field.Primary || field.Kind == FieldID || field.Kind == FieldVersion})
			}
		}
		targets[target.DefinitionClass] = target
	}
	return func(class string) (RelationTarget, bool) { target, found := targets[class]; return target, found }
}

func isNonStoredForTest(field FieldSpec) bool {
	return field.Kind == FieldOneToMany || field.Kind == FieldManyToMany || field.UsesExistingColumn
}

func (e Entity) NormalizeForTest() Entity {
	schema := EmptySchema()
	schema.Entities[e.Name] = e
	return schema.Normalize().Entities[e.Name]
}
