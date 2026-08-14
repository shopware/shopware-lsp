package entityschema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapshotCanonicalRoundTripAndGraph(t *testing.T) {
	first, err := Snapshot{
		Kind:   SnapshotMigration,
		Plugin: PluginIdentity{ComposerName: "acme/example"},
		Schema: EmptySchema(),
	}.Seal()
	require.NoError(t, err)
	second, err := Snapshot{
		Kind: SnapshotMigration, Parents: []string{first.ID},
		Plugin: first.Plugin, Schema: EmptySchema(),
		Decisions: []Decision{{Kind: "columnCreate", Entity: "example", To: "name"}},
	}.Seal()
	require.NoError(t, err)

	encoded, err := MarshalSnapshot(second)
	require.NoError(t, err)
	require.Equal(t, byte('\n'), encoded[len(encoded)-1])
	parsed, err := ParseSnapshot(encoded)
	require.NoError(t, err)
	require.Equal(t, second.ID, parsed.ID)

	graph, err := BuildSnapshotGraph([]SnapshotFile{
		{Path: "first.json", Snapshot: first},
		{Path: "second.json", Snapshot: second},
	})
	require.NoError(t, err)
	require.Len(t, graph.Leaves, 1)
	require.Equal(t, second.ID, graph.Leaves[0].Snapshot.ID)
}

func TestSnapshotRejectsContentWithStaleID(t *testing.T) {
	snapshot, err := Snapshot{Kind: SnapshotBaseline, Schema: EmptySchema()}.Seal()
	require.NoError(t, err)
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	encoded = append([]byte(nil), encoded...)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(encoded, &payload))
	payload["kind"] = "merge"
	encoded, err = json.Marshal(payload)
	require.NoError(t, err)
	_, err = ParseSnapshot(encoded)
	require.ErrorContains(t, err, "does not match")
}

func TestSchemaFromSpecIgnoresPHPNamespaceMoves(t *testing.T) {
	beforeSpec := CompleteSpec(EntitySpec{
		Mode:       "new",
		Namespace:  `Acme\Old\Content\Blog`,
		ClassName:  "Blog",
		EntityName: "acme_blog",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{
				ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name",
				Required: true, APIAware: true, SearchRanking: 500,
				PreservedFlags: []string{"new AllowHtml()"}, Editable: true,
			},
			{
				ID: "products", Kind: FieldOneToMany, PropertyName: "products",
				TargetDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
				TargetCollectionClass: `Shopware\Core\Content\Product\ProductCollection`,
				TargetEntityName:      "product", ReferenceStorageName: "blog_id", Editable: true,
			},
			{ID: "custom", Kind: FieldLocked, Raw: "customFieldFactory()"},
		},
	})
	afterSpec := beforeSpec
	afterSpec.Namespace = `Acme\New\Domain\Blog`
	afterSpec.DefinitionClass = ""
	afterSpec.EntityClass = ""
	afterSpec.CollectionClass = ""
	afterSpec = CompleteSpec(afterSpec)

	beforeEntity, err := SchemaFromSpec(beforeSpec)
	require.NoError(t, err)
	afterEntity, err := SchemaFromSpec(afterSpec)
	require.NoError(t, err)
	require.Equal(t, beforeEntity, afterEntity)

	before := EmptySchema()
	before.Entities[beforeEntity.Name] = beforeEntity
	after := EmptySchema()
	after.Entities[afterEntity.Name] = afterEntity
	require.False(t, DiffSchemas(before, after).DatabaseChanged())
}

func TestSnapshotSerializesDatabaseMetadataOnly(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode:       "new",
		Namespace:  `Acme\Example\Content\Blog`,
		ClassName:  "Blog",
		EntityName: "acme_blog",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{
				ID: "name", Kind: FieldString, PropertyName: "displayName", StorageName: "name",
				APIAware: true, SearchRanking: 500, PreservedFlags: []string{"new AllowHtml()"}, Editable: true,
			},
			{ID: "custom", Kind: FieldLocked, Raw: "customFieldFactory()"},
		},
	})
	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	schema := EmptySchema()
	schema.Entities[entity.Name] = entity
	snapshot, err := Snapshot{
		Kind:   SnapshotMigration,
		Plugin: PluginIdentity{ComposerName: "acme/example"},
		Migrations: []MigrationReference{{
			Path: "src/Migration/Migration1.php", Timestamp: 1, SHA256: strings.Repeat("a", 64),
		}},
		Schema: schema,
	}.Seal()
	require.NoError(t, err)
	encoded, err := MarshalSnapshot(snapshot)
	require.NoError(t, err)

	serialized := string(encoded)
	require.Contains(t, serialized, `"formatVersion": 2`)
	require.Contains(t, serialized, `"composerName": "acme/example"`)
	require.Contains(t, serialized, `"sqlType": "VARCHAR(255)"`)
	for _, key := range []string{
		`"pluginClass"`, `"class"`, `"definitionClass"`, `"entityClass"`, `"collectionClass"`,
		`"propertyName"`, `"kind": "string"`, `"apiAware"`, `"searchRanking"`,
		`"flags"`, `"associations"`, `"opaqueFields"`, "customFieldFactory", `Acme\\Example`,
	} {
		require.NotContains(t, serialized, key)
	}
}

func TestSnapshotRejectsPreviousFormat(t *testing.T) {
	snapshot, err := Snapshot{Kind: SnapshotBaseline, Schema: EmptySchema()}.Seal()
	require.NoError(t, err)
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(encoded, &payload))
	payload["formatVersion"] = float64(1)
	encoded, err = json.Marshal(payload)
	require.NoError(t, err)

	_, err = ParseSnapshot(encoded)
	require.ErrorContains(t, err, "unsupported entity snapshot format 1")
}

func TestSchemaDiffRequiresExplicitRenameResolution(t *testing.T) {
	before := EmptySchema()
	before.Entities["acme"] = Entity{
		Name: "acme",
		Columns: map[string]Column{"old_name": {
			Name: "old_name", SQLType: "VARCHAR(255)", NotNull: true,
		}},
	}
	after := EmptySchema()
	after.Entities["acme"] = Entity{
		Name: "acme",
		Columns: map[string]Column{"new_name": {
			Name: "new_name", SQLType: "VARCHAR(500)", NotNull: true,
		}},
	}
	diff := DiffSchemas(before, after)
	require.Len(t, diff.RenameQuestions, 1)
	_, _, err := ResolveRenameQuestions(diff, nil)
	require.ErrorContains(t, err, "unresolved")
	_, decisions, err := ResolveRenameQuestions(diff, []Decision{{
		Kind: "columnRename", Entity: "acme", From: "old_name", To: "new_name",
	}})
	require.NoError(t, err)
	require.Len(t, decisions, 1)
}

func TestSchemaDiffRequiresExplicitEntityRenameResolution(t *testing.T) {
	before := EmptySchema()
	before.Entities["old_table"] = Entity{
		Name: "old_table",
		Columns: map[string]Column{
			"id": {Name: "id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true},
		},
	}
	after := EmptySchema()
	after.Entities["new_table"] = Entity{
		Name: "new_table",
		Columns: map[string]Column{
			"id": {Name: "id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true},
		},
	}

	diff := DiffSchemas(before, after)
	require.Len(t, diff.EntityRenameQuestions, 1)
	require.Equal(t, "new_table", diff.EntityRenameQuestions[0].Added)
	require.Equal(t, "old_table", diff.EntityRenameQuestions[0].Candidates[0].From)
	_, _, _, err := ResolveSchemaDiff(before, after, nil)
	require.ErrorContains(t, err, "unresolved entity change")

	resolvedPrevious, resolved, decisions, err := ResolveSchemaDiff(before, after, []Decision{{
		Kind: "entityRename", Entity: "new_table", From: "old_table", To: "new_table",
	}})
	require.NoError(t, err)
	require.Contains(t, resolvedPrevious.Entities, "new_table")
	require.NotContains(t, resolvedPrevious.Entities, "old_table")
	require.Empty(t, resolved.CreatedEntities)
	require.Empty(t, resolved.RemovedEntities)
	require.Len(t, decisions, 1)
}

func TestSnapshotGraphDetectsMissingParentsAndCycles(t *testing.T) {
	orphan, err := Snapshot{Kind: SnapshotMigration, Parents: []string{"missing"}, Schema: EmptySchema()}.Seal()
	require.NoError(t, err)
	graph, err := BuildSnapshotGraph([]SnapshotFile{{Path: "orphan", Snapshot: orphan}})
	require.NoError(t, err)
	require.Equal(t, []string{"missing"}, graph.Missing[orphan.ID])

	a := Snapshot{ID: "a", FormatVersion: SnapshotFormatVersion, Kind: SnapshotMerge, Parents: []string{"b"}, Schema: EmptySchema()}
	b := Snapshot{ID: "b", FormatVersion: SnapshotFormatVersion, Kind: SnapshotMerge, Parents: []string{"a"}, Schema: EmptySchema()}
	_, err = BuildSnapshotGraph([]SnapshotFile{{Path: "a", Snapshot: a}, {Path: "b", Snapshot: b}})
	require.ErrorContains(t, err, "cycle")
}
