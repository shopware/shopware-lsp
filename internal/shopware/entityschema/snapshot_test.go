package entityschema

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapshotCanonicalRoundTripAndGraph(t *testing.T) {
	first, err := Snapshot{
		Kind:   SnapshotMigration,
		Plugin: PluginIdentity{ComposerName: "acme/example", PluginClass: `Acme\Example`},
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

func TestSchemaDiffRequiresExplicitRenameResolution(t *testing.T) {
	before := EmptySchema()
	before.Entities["acme"] = Entity{
		Name: "acme",
		Columns: map[string]Column{"old_name": {
			Name: "old_name", PropertyName: "name", Kind: FieldString,
			SQLType: "VARCHAR(255)", NotNull: true,
		}},
	}
	after := EmptySchema()
	after.Entities["acme"] = Entity{
		Name: "acme",
		Columns: map[string]Column{"new_name": {
			Name: "new_name", PropertyName: "name", Kind: FieldString,
			SQLType: "VARCHAR(500)", NotNull: true,
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
