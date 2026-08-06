package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/shopware/entityschema"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestEntitySnapshotAnalyzerReportsChangedMigration(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, filepath.FromSlash(entityschema.SnapshotRelativeDirectory))
	require.NoError(t, os.MkdirAll(directory, 0o755))
	migrationPath := filepath.Join(root, "src", "Migration", "Migration1.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(migrationPath), 0o755))
	require.NoError(t, os.WriteFile(migrationPath, []byte("changed"), 0o644))
	snapshot, err := (entityschema.Snapshot{
		Kind: entityschema.SnapshotMigration, Schema: entityschema.EmptySchema(),
		Migrations: []entityschema.MigrationReference{{Path: "src/Migration/Migration1.php", SHA256: entityschema.FileSHA256([]byte("original"))}},
	}).Seal()
	require.NoError(t, err)
	content, err := entityschema.MarshalSnapshot(snapshot)
	require.NoError(t, err)
	path := filepath.Join(directory, "1.snapshot.json")
	require.NoError(t, os.WriteFile(path, content, 0o644))
	document := lsp.NewTextDocument(uriutil.FileURI(path), string(content), 1)
	problems, err := NewEntitySnapshotAnalyzer().Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Contains(t, problemIDs(problems), "shopware.entity_snapshot.migration_changed")
}

func TestEntitySnapshotAnalyzerAcceptsExternalRelationsAndSnapshotOnlyIndexes(t *testing.T) {
	root := t.TempDir()
	definitionDirectory := filepath.Join(root, "src", "Entity", "Example")
	require.NoError(t, os.MkdirAll(definitionDirectory, 0o755))
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", Namespace: `Acme\Example\Entity\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "language", Kind: entityschema.FieldManyToOne, PropertyName: "language", ForeignKeyPropertyName: "languageId", StorageName: "language_id", TargetDefinitionClass: `Shopware\Core\System\Language\LanguageDefinition`, TargetEntityClass: `Shopware\Core\System\Language\LanguageEntity`, TargetCollectionClass: `Shopware\Core\System\Language\LanguageCollection`, TargetEntityName: "language", ReferenceField: "id", ReferenceStorageName: "id", Editable: true},
		},
		Indexes: []entityschema.IndexSpec{{Name: "uniq.acme_example.language_id", Kind: entityschema.IndexUnique, Columns: []string{"language_id"}}},
	})
	definition, err := entityschema.RenderDefinition(spec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(definitionDirectory, "ExampleDefinition.php"), []byte(definition), 0o644))
	entity, err := entityschema.SchemaFromSpec(spec)
	require.NoError(t, err)
	schema := entityschema.EmptySchema()
	schema.Entities[entity.Name] = entity
	snapshot, err := (entityschema.Snapshot{Kind: entityschema.SnapshotMigration, Schema: schema}).Seal()
	require.NoError(t, err)
	content, err := entityschema.MarshalSnapshot(snapshot)
	require.NoError(t, err)
	directory := filepath.Join(root, filepath.FromSlash(entityschema.SnapshotRelativeDirectory))
	require.NoError(t, os.MkdirAll(directory, 0o755))
	path := filepath.Join(directory, "1.snapshot.json")
	require.NoError(t, os.WriteFile(path, content, 0o644))

	document := lsp.NewTextDocument(uriutil.FileURI(path), string(content), 1)
	problems, err := NewEntitySnapshotAnalyzer().Analyze(context.Background(), document)
	require.NoError(t, err)
	require.NotContains(t, problemIDs(problems), "shopware.entity_snapshot.schema_drift")
}

func problemIDs(problems []lsp.Problem) []string {
	result := make([]string, 0, len(problems))
	for _, problem := range problems {
		result = append(result, string(problem.ID))
	}
	return result
}
