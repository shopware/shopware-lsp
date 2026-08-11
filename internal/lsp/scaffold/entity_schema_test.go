package scaffold

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/shopware/entityschema"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestEntitySchemaCreatePreviewAndApply(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src", "Entity", "Example"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name": "acme/example",
  "type": "shopware-platform-plugin",
  "require": {"shopware/core": "~6.7"},
  "autoload": {"psr-4": {"Acme\\Example\\": "src/"}},
  "extra": {"shopware-plugin-class": "Acme\\Example\\AcmeExample"}
}`), 0o644))
	provider := NewProvider(root, nil, nil)
	directory := filepath.Join(root, "src", "Entity", "Example")
	bootstrapRaw := rawJSON(t, EntitySchemaBootstrapRequest{DirectoryURI: uriutil.FileURI(directory)})
	value, err := provider.entitySchemaBootstrap(context.Background(), &bootstrapRaw)
	require.NoError(t, err)
	bootstrap := value.(EntitySchemaBootstrapResponse)
	require.Equal(t, `Acme\Example\Entity\Example`, bootstrap.Plugin.Namespace)

	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", PluginRootURI: bootstrap.Plugin.RootURI, DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\Example\Entity\Example`, ClassName: "Example", EntityName: "acme_example",
		CreateMigration: true,
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "name", Kind: entityschema.FieldString, PropertyName: "name", StorageName: "name", Required: true, Editable: true},
			{ID: "created", Kind: entityschema.FieldCreatedAt, Editable: true},
		},
	})
	previewRequest := EntitySchemaPreviewRequest{Spec: spec}
	previewRaw := rawJSON(t, previewRequest)
	value, err = provider.entitySchemaPreview(context.Background(), &previewRaw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, preview.Issues)
	require.NotEmpty(t, preview.Revision)
	require.Contains(t, preview.Revision, ":")
	require.NotEmpty(t, preview.SnapshotID)
	require.Positive(t, preview.MigrationTimestamp)
	require.GreaterOrEqual(t, len(preview.Files), 7) // bundle, service, migration and committed snapshots
	var snapshotFiles int
	var serviceFile string
	for _, file := range preview.Files {
		require.NotContains(t, file.After, "updateDestructive")
		if strings.HasSuffix(file.URI, "/Resources/config/services.yaml") {
			serviceFile = file.URI
			require.Contains(t, file.After, "shopware.entity.definition")
		}
		require.False(t, strings.HasSuffix(file.URI, "/Resources/config/services.xml"))
		if filepath.Ext(file.URI) == ".json" {
			snapshotFiles++
		}
	}
	require.NotEmpty(t, serviceFile)
	require.Equal(t, 2, snapshotFiles)

	applyRaw := rawJSON(t, EntitySchemaApplyRequest{EntitySchemaPreviewRequest: previewRequest, Revision: preview.Revision})
	value, err = provider.entitySchemaApply(context.Background(), &applyRaw)
	require.NoError(t, err)
	apply := value.(EntitySchemaApplyResponse)
	require.NotNil(t, apply.Edit)
	require.NotEmpty(t, apply.Edit.DocumentChanges)
}

func TestEntitySchemaBootstrapUsesComposerSourceRootForPluginRoot(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(sourceRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/example","type":"shopware-platform-plugin",
  "autoload":{"psr-4":{"Acme\\Example\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Example\\Plugin"}
}`), 0o644))

	provider := NewProvider(root, nil, nil)
	bootstrapRaw := rawJSON(t, EntitySchemaBootstrapRequest{DirectoryURI: uriutil.FileURI(root)})
	value, err := provider.entitySchemaBootstrap(context.Background(), &bootstrapRaw)
	require.NoError(t, err)
	bootstrap := value.(EntitySchemaBootstrapResponse)
	require.Equal(t, uriutil.FileURI(sourceRoot), bootstrap.Spec.DirectoryURI)
	require.Equal(t, `Acme\Example`, bootstrap.Spec.Namespace)
	require.Equal(t, "Example", bootstrap.Spec.ClassName)

	previewRaw := rawJSON(t, EntitySchemaPreviewRequest{Spec: bootstrap.Spec})
	value, err = provider.entitySchemaPreview(context.Background(), &previewRaw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, preview.Issues)
	for _, file := range preview.Files {
		path, pathErr := uriutil.Path(file.URI)
		require.NoError(t, pathErr)
		if strings.HasSuffix(path, "Definition.php") || strings.HasSuffix(path, "Entity.php") || strings.HasSuffix(path, "Collection.php") {
			require.True(t, safePluginTarget(sourceRoot, path), path)
		}
	}
}

func TestEntitySchemaPreviewRejectsDirectoryOutsideComposerSourceRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/example","type":"shopware-platform-plugin",
  "autoload":{"psr-4":{"Acme\\Example\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Example\\Plugin"}
}`), 0o644))
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", PluginRootURI: uriutil.FileURI(root), DirectoryURI: uriutil.FileURI(root),
		Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []entityschema.FieldSpec{{ID: "id", Kind: entityschema.FieldID, Editable: true}},
	})
	previewRaw := rawJSON(t, EntitySchemaPreviewRequest{Spec: spec})
	_, err := NewProvider(root, nil, nil).entitySchemaPreview(context.Background(), &previewRaw)
	require.ErrorContains(t, err, "outside the Composer PSR-4 source root")
}

func TestEntitySchemaRevisionCarriesGeneratedTimestamp(t *testing.T) {
	hash := sha256.Sum256([]byte("entity preview"))
	revision := entitySchemaRevision(1700000000, hash)

	timestamp, ok := entitySchemaRevisionTimestamp(revision)
	require.True(t, ok)
	require.Equal(t, int64(1700000000), timestamp)
	_, ok = entitySchemaRevisionTimestamp("stale")
	require.False(t, ok)
}

func TestEntitySchemaDriftReturnsConcreteDiffAndRecoveryChoices(t *testing.T) {
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []entityschema.FieldSpec{{ID: "id", Kind: entityschema.FieldID, Editable: true}},
	})
	entity, err := entityschema.SchemaFromSpec(spec)
	require.NoError(t, err)
	committed := entityschema.EmptySchema()
	committed.Entities[entity.Name] = entity
	response := EntitySchemaPreviewResponse{}

	history, err := reconcileEntitySchemaDrift(
		entityschema.PluginContext{},
		entityschema.Snapshot{ID: "leaf", Schema: committed},
		spec,
		"",
		&response,
		entitySchemaHistory{scanned: entityschema.EmptySchema()},
	)
	require.NoError(t, err)
	require.True(t, history.stop)
	require.True(t, response.Drift)
	require.Len(t, response.Diff.RemovedEntities, 1)
	require.Contains(t, response.DriftMessage, "driftDecision=adopt")
	require.Contains(t, response.DriftMessage, "driftDecision=migrate")
}

func TestEntitySchemaProductionRoundTrip(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Entity", "CatalogItem")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/catalog","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\Catalog\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Catalog\\AcmeCatalog"}
}`), 0o644))
	provider := NewProvider(root, nil, nil)
	bootstrapRaw := rawJSON(t, EntitySchemaBootstrapRequest{DirectoryURI: uriutil.FileURI(directory)})
	value, err := provider.entitySchemaBootstrap(context.Background(), &bootstrapRaw)
	require.NoError(t, err)
	bootstrap := value.(EntitySchemaBootstrapResponse)
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", PluginRootURI: bootstrap.Plugin.RootURI, DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\Catalog\Entity\CatalogItem`, ClassName: "CatalogItem", EntityName: "acme_catalog_item",
		CreateMigration: true, Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "name", Kind: entityschema.FieldString, PropertyName: "name", StorageName: "name", Required: true, MaxLength: 500, APIAware: true, Editable: true},
			{ID: "position", Kind: entityschema.FieldInt, PropertyName: "position", StorageName: "position", Editable: true},
			{ID: "config", Kind: entityschema.FieldJSON, PropertyName: "config", StorageName: "config", Editable: true},
			{ID: "created", Kind: entityschema.FieldCreatedAt, Editable: true},
			{ID: "updated", Kind: entityschema.FieldUpdatedAt, Editable: true},
		},
		Indexes: []entityschema.IndexSpec{{Name: "uniq.acme_catalog_item.name", Kind: entityschema.IndexUnique, Columns: []string{"name"}}},
	})
	previewRequest := EntitySchemaPreviewRequest{Spec: spec}
	previewRaw := rawJSON(t, previewRequest)
	value, err = provider.entitySchemaPreview(context.Background(), &previewRaw)
	require.NoError(t, err)
	createPreview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, createPreview.Issues)
	previewRequest.Spec.MigrationTimestamp = createPreview.MigrationTimestamp
	applyRaw := rawJSON(t, EntitySchemaApplyRequest{EntitySchemaPreviewRequest: previewRequest, Revision: createPreview.Revision})
	value, err = provider.entitySchemaApply(context.Background(), &applyRaw)
	require.NoError(t, err)
	applyEntityWorkspaceEdit(t, value.(EntitySchemaApplyResponse).Edit)

	definitionPath := filepath.Join(directory, "CatalogItemDefinition.php")
	entityPath := filepath.Join(directory, "CatalogItemEntity.php")
	entitySource, err := os.ReadFile(entityPath)
	require.NoError(t, err)
	entitySource = []byte(strings.TrimSuffix(string(entitySource), "}\n") + "    public function customLabel(): string { return 'custom'; }\n}\n")
	require.NoError(t, os.WriteFile(entityPath, entitySource, 0o644))

	loadRaw := rawJSON(t, EntitySchemaLoadRequest{FileURI: uriutil.FileURI(definitionPath)})
	value, err = provider.entitySchemaLoad(context.Background(), &loadRaw)
	require.NoError(t, err)
	loaded := value.(entityschema.EntitySpec)
	require.Equal(t, "edit", loaded.Mode)
	require.NotEmpty(t, loaded.BaseSnapshotIDs)
	require.Equal(t, []entityschema.IndexSpec{{Name: "uniq.acme_catalog_item.name", Kind: entityschema.IndexUnique, Columns: []string{"name"}}}, loaded.Indexes)
	for index := range loaded.Fields {
		if loaded.Fields[index].ID == "field-2" || loaded.Fields[index].PropertyName == "name" {
			loaded.Fields[index].PropertyName = "displayName"
			loaded.Fields[index].StorageName = "display_name"
		}
	}
	loaded.Indexes[0].Name = "uniq.acme_catalog_item.display_name"
	loaded.Indexes[0].Columns = []string{"display_name"}
	editRequest := EntitySchemaPreviewRequest{Spec: loaded}
	editRaw := rawJSON(t, editRequest)
	value, err = provider.entitySchemaPreview(context.Background(), &editRaw)
	require.NoError(t, err)
	renamePreview := value.(EntitySchemaPreviewResponse)
	require.Contains(t, entityIssueCodes(renamePreview.Issues), "entity.column.rename.decision")
	require.Len(t, renamePreview.Diff.RenameQuestions, 1)
	editRequest.Spec.MigrationTimestamp = renamePreview.MigrationTimestamp
	editRequest.Decisions = []entityschema.Decision{{Kind: "columnRename", Entity: "acme_catalog_item", From: "name", To: "display_name"}}
	editRaw = rawJSON(t, editRequest)
	value, err = provider.entitySchemaPreview(context.Background(), &editRaw)
	require.NoError(t, err)
	editPreview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, editPreview.Issues)
	require.True(t, editPreview.Destructive)
	applyRaw = rawJSON(t, EntitySchemaApplyRequest{EntitySchemaPreviewRequest: editRequest, Revision: editPreview.Revision, AllowDestructive: true})
	value, err = provider.entitySchemaApply(context.Background(), &applyRaw)
	require.NoError(t, err)
	applyEntityWorkspaceEdit(t, value.(EntitySchemaApplyResponse).Edit)

	definitionSource, err := os.ReadFile(definitionPath)
	require.NoError(t, err)
	entitySource, err = os.ReadFile(entityPath)
	require.NoError(t, err)
	require.Contains(t, string(definitionSource), "'display_name', 'displayName', 500")
	require.NotContains(t, string(definitionSource), "'name', 'name'")
	require.Contains(t, string(entitySource), "function customLabel()")
	require.Contains(t, string(entitySource), "$displayName")
	require.NotContains(t, string(entitySource), "$name")
	require.Empty(t, php.Parse(string(definitionSource)).Errors)
	require.Empty(t, php.Parse(string(entitySource)).Errors)
	snapshots, err := entityschema.ReadSnapshots(root)
	require.NoError(t, err)
	require.Len(t, snapshots, 3)
	migrations, err := filepath.Glob(filepath.Join(root, "src", "Migration", "*.php"))
	require.NoError(t, err)
	require.Len(t, migrations, 2)
}

func TestEntitySchemaApplyRejectsStalePreview(t *testing.T) {
	provider := NewProvider(t.TempDir(), nil, nil)
	raw := rawJSON(t, EntitySchemaApplyRequest{Revision: "stale"})
	_, err := provider.entitySchemaApply(context.Background(), &raw)
	require.Error(t, err)
}

func TestEntitySchemaLoadUsesOpenDefinitionDocument(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Entity", "Example")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/example","type":"shopware-platform-plugin",
  "autoload":{"psr-4":{"Acme\\Example\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Example\\Plugin"}
}`), 0o644))
	diskSpec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", Namespace: `Acme\Example\Entity\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []entityschema.FieldSpec{{ID: "id", Kind: entityschema.FieldID, Editable: true}, {ID: "name", Kind: entityschema.FieldString, PropertyName: "name", StorageName: "name", Editable: true}},
	})
	diskSource, err := entityschema.RenderDefinition(diskSpec)
	require.NoError(t, err)
	definitionPath := filepath.Join(directory, "ExampleDefinition.php")
	require.NoError(t, os.WriteFile(definitionPath, []byte(diskSource), 0o644))
	overlaySpec := diskSpec
	overlaySpec.Fields = append([]entityschema.FieldSpec(nil), diskSpec.Fields...)
	overlaySpec.Fields[1].PropertyName = "displayName"
	overlaySpec.Fields[1].StorageName = "display_name"
	overlaySource, err := entityschema.RenderDefinition(overlaySpec)
	require.NoError(t, err)
	version := 7
	uri := uriutil.FileURI(definitionPath)
	request := EntitySchemaLoadRequest{FileURI: uri, Documents: map[string]EntitySchemaDocument{uri: {Text: overlaySource, Version: &version}}}
	raw := rawJSON(t, request)
	value, err := NewProvider(root, nil, nil).entitySchemaLoad(context.Background(), &raw)
	require.NoError(t, err)
	loaded := value.(entityschema.EntitySpec)
	require.Equal(t, "displayName", loaded.Fields[1].PropertyName)
	require.Equal(t, "display_name", loaded.Fields[1].StorageName)
}

func TestEntitySchemaPreviewRejectsChangedSnapshotHead(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Entity", "Example")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/example","type":"shopware-platform-plugin",
  "autoload":{"psr-4":{"Acme\\Example\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Example\\Plugin"}
}`), 0o644))
	snapshotDirectory := filepath.Join(root, filepath.FromSlash(entityschema.SnapshotRelativeDirectory))
	require.NoError(t, os.MkdirAll(snapshotDirectory, 0o755))
	baseline, err := (entityschema.Snapshot{Kind: entityschema.SnapshotBaseline, Schema: entityschema.EmptySchema()}).Seal()
	require.NoError(t, err)
	baselineContent, err := entityschema.MarshalSnapshot(baseline)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(snapshotDirectory, "baseline.snapshot.json"), baselineContent, 0o644))

	provider := NewProvider(root, nil, nil)
	bootstrapRaw := rawJSON(t, EntitySchemaBootstrapRequest{DirectoryURI: uriutil.FileURI(directory)})
	value, err := provider.entitySchemaBootstrap(context.Background(), &bootstrapRaw)
	require.NoError(t, err)
	bootstrap := value.(EntitySchemaBootstrapResponse)
	require.Equal(t, []string{baseline.ID}, bootstrap.Spec.BaseSnapshotIDs)

	newHead, err := (entityschema.Snapshot{Parents: []string{baseline.ID}, Kind: entityschema.SnapshotMigration, Schema: entityschema.EmptySchema()}).Seal()
	require.NoError(t, err)
	newHeadContent, err := entityschema.MarshalSnapshot(newHead)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(snapshotDirectory, "new-head.snapshot.json"), newHeadContent, 0o644))
	previewRaw := rawJSON(t, EntitySchemaPreviewRequest{Spec: bootstrap.Spec})
	value, err = provider.entitySchemaPreview(context.Background(), &previewRaw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Contains(t, entityIssueCodes(preview.Issues), "entity.snapshot.stale")
	require.Empty(t, preview.Revision)
}

func TestRestoreSnapshotOnlyIndexesDoesNotRestoreObsoleteRelationIndex(t *testing.T) {
	snapshot := entityschema.EmptySchema()
	snapshot.Entities["example"] = entityschema.Entity{
		Name: "example",
		Indexes: map[string]entityschema.Index{
			"idx.example.old_id": {Name: "idx.example.old_id", Columns: []string{"old_id"}},
			"uniq.example.name":  {Name: "uniq.example.name", Unique: true, Columns: []string{"name"}},
		},
		ForeignKeys: map[string]entityschema.ForeignKey{
			"fk.example.old_id": {Name: "fk.example.old_id", Column: "old_id", Columns: []string{"old_id"}},
		},
	}
	scanned := entityschema.EmptySchema()
	scanned.Entities["example"] = entityschema.Entity{
		Name: "example",
		Indexes: map[string]entityschema.Index{
			"idx.example.new_id": {Name: "idx.example.new_id", Columns: []string{"new_id"}},
		},
		ForeignKeys: map[string]entityschema.ForeignKey{
			"fk.example.new_id": {Name: "fk.example.new_id", Column: "new_id", Columns: []string{"new_id"}},
		},
	}

	restored := entityschema.RestoreSnapshotOnlyIndexes(scanned, snapshot)
	require.Contains(t, restored.Entities["example"].Indexes, "uniq.example.name")
	require.Contains(t, restored.Entities["example"].Indexes, "idx.example.new_id")
	require.NotContains(t, restored.Entities["example"].Indexes, "idx.example.old_id")
}

func TestEntitySchemaNamespaceMoveDoesNotCauseDrift(t *testing.T) {
	beforeSpec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "edit", Namespace: `Acme\Old\Content\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "name", Kind: entityschema.FieldString, PropertyName: "name", StorageName: "name", Editable: true},
		},
	})
	afterSpec := beforeSpec
	afterSpec.Namespace = `Acme\New\Domain\Example`
	afterSpec.DefinitionClass = ""
	afterSpec.EntityClass = ""
	afterSpec.CollectionClass = ""
	afterSpec = entityschema.CompleteSpec(afterSpec)
	beforeEntity, err := entityschema.SchemaFromSpec(beforeSpec)
	require.NoError(t, err)
	afterEntity, err := entityschema.SchemaFromSpec(afterSpec)
	require.NoError(t, err)
	before := entityschema.EmptySchema()
	before.Entities[beforeEntity.Name] = beforeEntity
	after := entityschema.EmptySchema()
	after.Entities[afterEntity.Name] = afterEntity
	leaf, err := (entityschema.Snapshot{Kind: entityschema.SnapshotBaseline, Schema: before}).Seal()
	require.NoError(t, err)
	response := EntitySchemaPreviewResponse{}

	history, err := reconcileEntitySchemaDrift(
		entityschema.PluginContext{},
		leaf,
		afterSpec,
		"",
		&response,
		entitySchemaHistory{scanned: after, parents: []string{leaf.ID}},
	)
	require.NoError(t, err)
	require.False(t, response.Drift)
	require.False(t, history.stop)
	require.Equal(t, before, history.previous)
}

func TestEntitySchemaEditPreservesCustomCodeAndConfirmsDestructiveDDL(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Entity", "Example")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/example","type":"shopware-platform-plugin",
  "autoload":{"psr-4":{"Acme\\Example\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Example\\Plugin"}
}`), 0o644))
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "edit", PluginRootURI: uriutil.FileURI(root), DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\Example\Entity\Example`, ClassName: "Example", EntityName: "acme_example",
		CreateMigration: true, MigrationTimestamp: 1700000001,
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "name", Kind: entityschema.FieldString, PropertyName: "name", StorageName: "name", Required: true, Editable: true},
			{ID: "created", Kind: entityschema.FieldCreatedAt, Editable: true},
			{ID: "updated", Kind: entityschema.FieldUpdatedAt, Editable: true},
		},
	})
	definition, err := entityschema.RenderDefinition(spec)
	require.NoError(t, err)
	definition = definition[:len(definition)-2] + "\n    public function custom(): string { return 'preserved'; }\n}\n"
	entity, err := entityschema.RenderEntity(spec)
	require.NoError(t, err)
	entity = entity[:len(entity)-2] + "\n    public function custom(): string { return 'preserved'; }\n}\n"
	collection, err := entityschema.RenderCollection(spec)
	require.NoError(t, err)
	for name, source := range map[string]string{"ExampleDefinition.php": definition, "ExampleEntity.php": entity, "ExampleCollection.php": collection} {
		require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte(source), 0o644))
	}
	servicesPath := filepath.Join(root, "src", "Resources", "config", "services.xml")
	require.NoError(t, os.MkdirAll(filepath.Dir(servicesPath), 0o755))
	services, err := entityschema.PatchServiceConfiguration(servicesPath, "", spec.DefinitionClass)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(servicesPath, []byte(services), 0o644))
	currentEntity, err := entityschema.SchemaFromSpec(spec)
	require.NoError(t, err)
	currentSchema := entityschema.EmptySchema()
	currentSchema.Entities[spec.EntityName] = currentEntity
	snapshot, err := (entityschema.Snapshot{Kind: entityschema.SnapshotBaseline, Schema: currentSchema}).Seal()
	require.NoError(t, err)
	snapshotContent, err := entityschema.MarshalSnapshot(snapshot)
	require.NoError(t, err)
	snapshotDir := filepath.Join(root, filepath.FromSlash(entityschema.SnapshotRelativeDirectory))
	require.NoError(t, os.MkdirAll(snapshotDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotDir, "baseline.snapshot.json"), snapshotContent, 0o644))

	spec.Fields = []entityschema.FieldSpec{spec.Fields[0], spec.Fields[2], spec.Fields[3]}
	spec.ServiceURI = uriutil.FileURI(servicesPath)
	provider := NewProvider(root, nil, nil)
	previewRequest := EntitySchemaPreviewRequest{Spec: spec}
	previewRaw := rawJSON(t, previewRequest)
	value, err := provider.entitySchemaPreview(context.Background(), &previewRaw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, preview.Issues)
	require.True(t, preview.Destructive)
	var preserved bool
	for _, file := range preview.Files {
		if file.URI == uriutil.FileURI(filepath.Join(directory, "ExampleEntity.php")) {
			preserved = true
			require.Contains(t, file.After, "function custom")
			require.NotContains(t, file.After, "$name")
		}
	}
	require.True(t, preserved)
	applyRaw := rawJSON(t, EntitySchemaApplyRequest{EntitySchemaPreviewRequest: previewRequest, Revision: preview.Revision})
	_, err = provider.entitySchemaApply(context.Background(), &applyRaw)
	require.ErrorContains(t, err, "destructive")
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func entityIssueCodes(issues []entityschema.ValidationIssue) []string {
	result := make([]string, 0, len(issues))
	for _, issue := range issues {
		result = append(result, issue.Code)
	}
	return result
}

func applyEntityWorkspaceEdit(t *testing.T, edit *protocol.WorkspaceEdit) {
	t.Helper()
	require.NotNil(t, edit)
	for _, change := range edit.DocumentChanges {
		if change.Kind == protocol.CreateFileOperation {
			path, err := uriutil.Path(change.URI)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			require.NoError(t, err)
			require.NoError(t, file.Close())
			continue
		}
		require.NotNil(t, change.TextDocument)
		require.Len(t, change.Edits, 1)
		path, err := uriutil.Path(change.TextDocument.URI)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, []byte(change.Edits[0].NewText), 0o644))
	}
}

func TestSafePluginTargetResolvesExistingSymlinkAncestors(t *testing.T) {
	plugin := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(plugin, "src"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(plugin, "src", "Migration")))
	require.False(t, safePluginTarget(plugin, filepath.Join(plugin, "src", "Migration", "Nested", "Migration1.php")))
	require.True(t, safePluginTarget(plugin, filepath.Join(plugin, "src", "Entity", "Example.php")))
}
