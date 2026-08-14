package scaffold

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	php "github.com/shopware/shopware-lsp/internal/parser/php"
	phpindex "github.com/shopware/shopware-lsp/internal/php"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
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
	require.Contains(t, bootstrap.DefinitionKinds, entityschema.DefinitionBulkExtension)
	require.Len(t, bootstrap.Spec.Fields, 1)
	require.Equal(t, entityschema.FieldID, bootstrap.Spec.Fields[0].Kind)
	var mappingTimestampTypes int
	for _, fieldType := range bootstrap.FieldTypes {
		if fieldType.Kind == "created-at" || fieldType.Kind == "updated-at" {
			require.Equal(t, []entityschema.DefinitionKind{entityschema.DefinitionEntity, entityschema.DefinitionMapping}, fieldType.DefinitionKinds)
			require.True(t, fieldType.RequiresDefaultFieldsOverride)
			mappingTimestampTypes++
		}
	}
	require.Equal(t, 2, mappingTimestampTypes)

	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", PluginRootURI: bootstrap.Plugin.RootURI, DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\Example\Entity\Example`, ClassName: "Example", EntityName: "acme_example",
		CreateMigration: true,
		DefinitionBehavior: &entityschema.DefinitionBehaviorSpec{
			ParentDefinitionClass:        `Shopware\Core\Content\Product\ProductDefinition`,
			VersionAware:                 testBoolPointer(false),
			OverrideDefaultFields:        true,
			RestrictDeleteMetaProperties: []string{"id"},
		},
		DefinitionMetadata: &entityschema.DefinitionMetadataSpec{
			Since: "6.7.1.0", Defaults: []entityschema.DefinitionDefaultSpec{{PropertyName: "name", ValueExpression: "'default'"}},
			ChildDefaults: []entityschema.DefinitionDefaultSpec{{PropertyName: "name", ValueExpression: "'child'"}},
			HydratorClass: `Acme\Example\Entity\Example\ExampleHydrator`,
		},
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "name", Kind: entityschema.FieldString, PropertyName: "name", StorageName: "name", Required: true, Editable: true},
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
	var definitionFile string
	for _, file := range preview.Files {
		require.NotContains(t, file.After, "updateDestructive")
		if strings.HasSuffix(file.URI, "/Resources/config/services.yaml") {
			serviceFile = file.URI
			require.Contains(t, file.After, "shopware.entity.definition")
		}
		if strings.HasSuffix(file.URI, "/ExampleDefinition.php") {
			definitionFile = file.After
		}
		require.False(t, strings.HasSuffix(file.URI, "/Resources/config/services.xml"))
		if filepath.Ext(file.URI) == ".json" {
			snapshotFiles++
		}
	}
	require.NotEmpty(t, serviceFile)
	require.Contains(t, definitionFile, "return ProductDefinition::class")
	require.Contains(t, definitionFile, "public function isVersionAware(): bool")
	require.Contains(t, definitionFile, "protected function defaultFields(): array")
	require.Contains(t, definitionFile, "getRestrictDeleteMetaFields")
	require.Contains(t, definitionFile, "public function since(): ?string")
	require.Contains(t, definitionFile, "public function getDefaults(): array")
	require.Contains(t, definitionFile, "public function getChildDefaults(): array")
	require.Contains(t, definitionFile, "return ExampleHydrator::class")
	require.Equal(t, 2, snapshotFiles)

	applyRaw := rawJSON(t, EntitySchemaApplyRequest{EntitySchemaPreviewRequest: previewRequest, Revision: preview.Revision})
	value, err = provider.entitySchemaApply(context.Background(), &applyRaw)
	require.NoError(t, err)
	apply := value.(EntitySchemaApplyResponse)
	require.NotNil(t, apply.Edit)
	require.NotEmpty(t, apply.Edit.DocumentChanges)
}

func testBoolPointer(value bool) *bool {
	return &value
}

func TestEntitySchemaTechnicalNameChangeRequiresTableRenameDecision(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Entity", "Example")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/rename","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\Rename\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Rename\\Plugin"}
}`), 0o644))
	provider := NewProvider(root, nil, nil)
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", PluginRootURI: uriutil.FileURI(root), DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\Rename\Entity\Example`, ClassName: "Example", EntityName: "acme_old",
		CreateMigration: true,
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "payload", Kind: entityschema.FieldJSON, PropertyName: "payload", StorageName: "payload", Editable: true},
		},
	})
	createRequest := EntitySchemaPreviewRequest{Spec: spec}
	createRaw := rawJSON(t, createRequest)
	value, err := provider.entitySchemaPreview(context.Background(), &createRaw)
	require.NoError(t, err)
	createPreview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, createPreview.Issues)
	createRequest.Spec.MigrationTimestamp = createPreview.MigrationTimestamp
	applyRaw := rawJSON(t, EntitySchemaApplyRequest{EntitySchemaPreviewRequest: createRequest, Revision: createPreview.Revision})
	value, err = provider.entitySchemaApply(context.Background(), &applyRaw)
	require.NoError(t, err)
	applyEntityWorkspaceEdit(t, value.(EntitySchemaApplyResponse).Edit)

	definitionPath := filepath.Join(directory, "ExampleDefinition.php")
	loadRaw := rawJSON(t, EntitySchemaLoadRequest{FileURI: uriutil.FileURI(definitionPath)})
	value, err = provider.entitySchemaLoad(context.Background(), &loadRaw)
	require.NoError(t, err)
	loaded := value.(entityschema.EntitySpec)
	loaded.EntityName = "acme_new"
	loaded.MigrationTimestamp = 0

	renameRequest := EntitySchemaPreviewRequest{Spec: loaded}
	renameRaw := rawJSON(t, renameRequest)
	value, err = provider.entitySchemaPreview(context.Background(), &renameRaw)
	require.NoError(t, err)
	unresolved := value.(EntitySchemaPreviewResponse)
	require.Contains(t, unresolved.Issues, entityschema.ValidationIssue{
		Code: "entity.table.rename.decision", Message: "unresolved entity change for acme_new", Severity: "error",
	})
	require.Len(t, unresolved.Diff.EntityRenameQuestions, 1)
	require.Equal(t, "acme_old", unresolved.Diff.EntityRenameQuestions[0].Candidates[0].From)

	renameRequest.Spec.MigrationTimestamp = unresolved.MigrationTimestamp
	renameRequest.Decisions = []entityschema.Decision{{Kind: "entityRename", Entity: "acme_new", From: "acme_old", To: "acme_new"}}
	renameRequest.Spec.CreateMigration = false
	renameRaw = rawJSON(t, renameRequest)
	value, err = provider.entitySchemaPreview(context.Background(), &renameRaw)
	require.NoError(t, err)
	require.Contains(t, value.(EntitySchemaPreviewResponse).Issues, entityschema.ValidationIssue{
		Code: "entity.migration.required", Message: "Database changes require a migration", Severity: "error",
	})
	renameRequest.Spec.CreateMigration = true
	renameRaw = rawJSON(t, renameRequest)
	value, err = provider.entitySchemaPreview(context.Background(), &renameRaw)
	require.NoError(t, err)
	resolved := value.(EntitySchemaPreviewResponse)
	require.Empty(t, resolved.Issues)
	require.False(t, resolved.Destructive)
	var migration, renamedDefinition, renamedCollection string
	for _, file := range resolved.Files {
		if strings.Contains(file.URI, "/Migration/Migration") {
			migration = file.After
		}
		if strings.HasSuffix(file.URI, "/ExampleDefinition.php") {
			renamedDefinition = file.After
		}
		if strings.HasSuffix(file.URI, "/ExampleCollection.php") {
			renamedCollection = file.After
		}
	}
	require.Contains(t, migration, "RENAME TABLE `acme_old` TO `acme_new`;")
	require.NotContains(t, migration, "DROP TABLE")
	require.NotContains(t, migration, "CREATE TABLE")
	require.Contains(t, renamedDefinition, "const ENTITY_NAME = 'acme_new';")
	require.NotContains(t, renamedDefinition, "const ENTITY_NAME = 'acme_old';")
	require.Contains(t, renamedCollection, "return 'acme_new_collection';")
	require.NotContains(t, renamedCollection, "return 'acme_old_collection';")
}

func TestEntitySchemaMappingDefinitionPreviewCreatesOnlyDefinitionAndTable(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Content", "ProductTag")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/mapping","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\Mapping\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Mapping\\Plugin"}
}`), 0o644))
	provider := NewProvider(root, nil, nil)
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", DefinitionKind: entityschema.DefinitionMapping,
		PluginRootURI: uriutil.FileURI(root), DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\Mapping\Content\ProductTag`, ClassName: "ProductTag", EntityName: "acme_product_tag", CreateMigration: true,
		Fields: []entityschema.FieldSpec{
			{ID: "product", Kind: entityschema.FieldForeignKey, PropertyName: "productId", StorageName: "product_id", TargetDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`, TargetEntityName: "product", Required: true, Primary: true, Editable: true},
			{ID: "tag", Kind: entityschema.FieldForeignKey, PropertyName: "tagId", StorageName: "tag_id", TargetDefinitionClass: `Shopware\Core\System\Tag\TagDefinition`, TargetEntityName: "tag", Required: true, Primary: true, Editable: true},
		},
	})
	raw := rawJSON(t, EntitySchemaPreviewRequest{Spec: spec})
	value, err := provider.entitySchemaPreview(context.Background(), &raw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, preview.Issues)
	require.Len(t, preview.Files, 5)
	var definition, migration string
	for _, file := range preview.Files {
		require.NotContains(t, file.URI, "ProductTagEntity.php")
		require.NotContains(t, file.URI, "ProductTagCollection.php")
		if strings.HasSuffix(file.URI, "/ProductTagDefinition.php") {
			definition = file.After
		}
		if strings.Contains(file.URI, "/Migration/Migration") {
			migration = file.After
		}
	}
	require.Contains(t, definition, "extends MappingEntityDefinition")
	require.NotContains(t, definition, "getEntityClass")
	require.Contains(t, migration, "PRIMARY KEY (`product_id`, `tag_id`)")
	require.NotContains(t, migration, "`created_at`")
	require.NotContains(t, migration, "`updated_at`")

	applyRaw := rawJSON(t, EntitySchemaApplyRequest{
		EntitySchemaPreviewRequest: EntitySchemaPreviewRequest{Spec: spec},
		Revision:                   preview.Revision,
	})
	value, err = provider.entitySchemaApply(context.Background(), &applyRaw)
	require.NoError(t, err)
	require.NotEmpty(t, value.(EntitySchemaApplyResponse).Edit.DocumentChanges)
}

func TestEntitySchemaVersionAwareHierarchyPreview(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Content", "Node")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/hierarchy","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\Hierarchy\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Hierarchy\\Plugin"}
}`), 0o644))
	provider := NewProvider(root, nil, nil)
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", PluginRootURI: uriutil.FileURI(root), DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\Hierarchy\Content\Node`, ClassName: "Node", EntityName: "acme_node", CreateMigration: true,
		InheritanceAware: true,
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "version", Kind: entityschema.FieldVersion, Editable: true},
			{ID: "hierarchy", Kind: entityschema.FieldHierarchy, PropertyName: "children", Editable: true},
			{ID: "label", Kind: entityschema.FieldString, PropertyName: "label", StorageName: "label", Inherited: true, Editable: true},
		},
	})
	raw := rawJSON(t, EntitySchemaPreviewRequest{Spec: spec})
	value, err := provider.entitySchemaPreview(context.Background(), &raw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, preview.Issues)
	var definition, entity, migration string
	for _, file := range preview.Files {
		switch {
		case strings.HasSuffix(file.URI, "/NodeDefinition.php"):
			definition = file.After
		case strings.HasSuffix(file.URI, "/NodeEntity.php"):
			entity = file.After
		case strings.Contains(file.URI, "/Migration/Migration"):
			migration = file.After
		}
	}
	require.Contains(t, definition, "new ParentFkField(NodeDefinition::class)")
	require.Contains(t, definition, "new ReferenceVersionField(NodeDefinition::class, 'parent_version_id')")
	require.Contains(t, definition, "new ParentAssociationField(NodeDefinition::class, 'id')")
	require.Contains(t, definition, "new ChildrenAssociationField(NodeDefinition::class)")
	require.Contains(t, definition, "public function isInheritanceAware(): bool")
	require.Contains(t, definition, "new StringField('label', 'label'))->addFlags(new Inherited())")
	require.Contains(t, entity, "protected ?NodeEntity $parent = null;")
	require.Contains(t, entity, "protected ?NodeCollection $children = null;")
	require.Contains(t, migration, "`parent_id` BINARY(16) NULL")
	require.Contains(t, migration, "`parent_version_id` BINARY(16) NULL")
	require.Contains(t, migration, "FOREIGN KEY (`parent_id`, `parent_version_id`) REFERENCES `acme_node` (`id`, `version_id`) ON DELETE CASCADE")
}

func TestEntitySchemaExtensionPreviewApplyAndLoad(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Extension", "Content", "Product")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/product-extension","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\ProductExtension\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\ProductExtension\\Plugin"}
}`), 0o644))
	dalIndex, err := shopwaredal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	productDefinitionPath := filepath.Join(root, "vendor", "shopware", "core", "Content", "Product", "ProductDefinition.php")
	productDefinition := `<?php declare(strict_types=1);
namespace Shopware\Core\Content\Product;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ProductDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'product';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`
	require.NoError(t, dalIndex.Index(indexer.NewParsedFile(productDefinitionPath, []byte(productDefinition))))
	categoryDefinitionPath := filepath.Join(root, "vendor", "shopware", "core", "Content", "Category", "CategoryDefinition.php")
	categoryDefinition := `<?php declare(strict_types=1);
namespace Shopware\Core\Content\Category;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CategoryDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'category';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`
	require.NoError(t, dalIndex.Index(indexer.NewParsedFile(categoryDefinitionPath, []byte(categoryDefinition))))
	provider := NewProvider(root, nil, nil, dalIndex)

	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", DefinitionKind: entityschema.DefinitionExtension,
		PluginRootURI: uriutil.FileURI(root), DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\ProductExtension\Extension\Content\Product`, ClassName: "ProductCustom", EntityName: "product",
		ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`, CreateMigration: true,
		ReadProtected: true, ReadProtectionScopes: []string{"system"},
		FieldModifications: []entityschema.FieldModificationSpec{{
			ID: "modify-id", PropertyName: "id",
			AddFlags:    []entityschema.FieldFlagSpec{{Kind: entityschema.FlagAPIAware}},
			RemoveFlags: []entityschema.FieldFlagKind{entityschema.FlagRequired},
		}},
		Fields: []entityschema.FieldSpec{
			{
				ID: "category", Kind: entityschema.FieldManyToOne, PropertyName: "category", ForeignKeyPropertyName: "categoryId",
				StorageName: "acme_category_id", TargetDefinitionClass: `Shopware\Core\Content\Category\CategoryDefinition`,
				TargetEntityClass: `Shopware\Core\Content\Category\CategoryEntity`, TargetCollectionClass: `Shopware\Core\Content\Category\CategoryCollection`,
				TargetEntityName: "category", ReferenceField: "id", ReferenceStorageName: "id", DeleteBehavior: entityschema.DeleteSetNull, Editable: true,
			},
			{ID: "children", Kind: entityschema.FieldOneToMany, PropertyName: "children", ReferenceStorageName: "parent_id", SourceColumn: "id", TargetDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`, TargetEntityClass: `Shopware\Core\Content\Product\ProductEntity`, TargetCollectionClass: `Shopware\Core\Content\Product\ProductCollection`, TargetEntityName: "product", Editable: true},
		},
		Indexes: []entityschema.IndexSpec{{
			Name: "uniq.product.acme_category_id_id", Kind: entityschema.IndexUnique,
			Columns: []string{"acme_category_id", "id"},
		}},
	})
	request := EntitySchemaPreviewRequest{Spec: spec}
	raw := rawJSON(t, request)
	value, err := provider.entitySchemaPreview(context.Background(), &raw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, preview.Issues)
	require.Len(t, preview.Files, 5)
	var extension, service, migration string
	for _, file := range preview.Files {
		switch {
		case strings.HasSuffix(file.URI, "/ProductCustomExtension.php"):
			extension = file.After
		case strings.HasSuffix(file.URI, "/services.yaml"):
			service = file.After
		case strings.Contains(file.URI, "/Migration/Migration"):
			migration = file.After
		}
		require.NotContains(t, file.URI, "ProductCustomEntity.php")
		require.NotContains(t, file.URI, "ProductCustomCollection.php")
	}
	require.Contains(t, extension, "extends EntityExtension")
	require.Contains(t, extension, "return ProductDefinition::ENTITY_NAME")
	require.Contains(t, extension, "function extendProtections(EntityProtectionCollection $protections): void")
	require.Contains(t, extension, "$protections->add(new ReadProtection('system'))")
	require.Contains(t, extension, "$collection->get('id')?->addFlags(new ApiAware())")
	require.Contains(t, extension, "$collection->get('id')?->removeFlag(Required::class)")
	require.NotContains(t, extension, "getDefinitionClass")
	require.Contains(t, service, "shopware.entity.extension")
	require.Contains(t, migration, "ALTER TABLE `product` ADD COLUMN `acme_category_id` BINARY(16) NULL")
	require.Contains(t, migration, "ADD UNIQUE INDEX `uniq.product.acme_category_id_id` (`acme_category_id`, `id`)")
	require.Contains(t, migration, "REFERENCES `category` (`id`) ON DELETE SET NULL")
	require.NotContains(t, migration, "CREATE TABLE")
	require.Empty(t, preview.Diff.CreatedEntities)
	require.Len(t, preview.Diff.AddedColumns, 1)
	require.Len(t, preview.Diff.AddedIndexes, 2, "the DAL relation owns its foreign-key index in addition to the custom index")

	request.Spec.MigrationTimestamp = preview.MigrationTimestamp
	applyRaw := rawJSON(t, EntitySchemaApplyRequest{EntitySchemaPreviewRequest: request, Revision: preview.Revision})
	value, err = provider.entitySchemaApply(context.Background(), &applyRaw)
	require.NoError(t, err)
	applyEntityWorkspaceEdit(t, value.(EntitySchemaApplyResponse).Edit)

	extensionPath := filepath.Join(directory, "ProductCustomExtension.php")
	loadRaw := rawJSON(t, EntitySchemaLoadRequest{FileURI: uriutil.FileURI(extensionPath)})
	value, err = provider.entitySchemaLoad(context.Background(), &loadRaw)
	require.NoError(t, err)
	loaded := value.(entityschema.EntitySpec)
	require.Equal(t, entityschema.DefinitionExtension, loaded.DefinitionKind)
	require.Equal(t, `Shopware\Core\Content\Product\ProductDefinition`, loaded.ExtendedDefinitionClass)
	require.Equal(t, "product", loaded.EntityName)
	require.Len(t, loaded.Fields, 2)
	require.Contains(t, loaded.ExtendedFields, entityschema.RelationTargetField{PropertyName: "id", StorageName: "id", Primary: true})
	require.Contains(t, loaded.ExtendedFields, entityschema.RelationTargetField{PropertyName: "createdAt", StorageName: "created_at"})
	require.Equal(t, spec.Indexes, loaded.Indexes)
	require.True(t, loaded.ReadProtected)
	require.Equal(t, []string{"system"}, loaded.ReadProtectionScopes)
	require.Len(t, loaded.FieldModifications, 1)
	require.Equal(t, "id", loaded.FieldModifications[0].PropertyName)
	require.Equal(t, []entityschema.FieldFlagKind{entityschema.FlagRequired}, loaded.FieldModifications[0].RemoveFlags)

	loaded.EntityName = "category"
	loaded.ExtendedDefinitionClass = `Shopware\Core\Content\Category\CategoryDefinition`
	loaded.MigrationTimestamp = 0
	retargetRequest := EntitySchemaPreviewRequest{Spec: loaded}
	retargetRaw := rawJSON(t, retargetRequest)
	value, err = provider.entitySchemaPreview(context.Background(), &retargetRaw)
	require.NoError(t, err)
	retarget := value.(EntitySchemaPreviewResponse)
	require.Empty(t, retarget.Issues)
	require.True(t, retarget.Destructive)
	require.Len(t, retarget.Diff.RemovedColumns, 1)
	require.Equal(t, "product", retarget.Diff.RemovedColumns[0].Entity)
	require.Len(t, retarget.Diff.AddedColumns, 1)
	require.Equal(t, "category", retarget.Diff.AddedColumns[0].Entity)
	require.Len(t, retarget.Diff.RemovedIndexes, 2)
	for _, removed := range retarget.Diff.RemovedIndexes {
		require.Equal(t, "product", removed.Entity)
	}
	require.Len(t, retarget.Diff.AddedIndexes, 2)
	for _, added := range retarget.Diff.AddedIndexes {
		require.Equal(t, "category", added.Entity)
	}
	var retargetDefinition, retargetMigration string
	for _, file := range retarget.Files {
		switch {
		case strings.HasSuffix(file.URI, "/ProductCustomExtension.php"):
			retargetDefinition = file.After
		case strings.Contains(file.URI, "/Migration/Migration"):
			retargetMigration = file.After
		}
	}
	require.Contains(t, retargetDefinition, "return CategoryDefinition::ENTITY_NAME")
	require.Contains(t, retargetMigration, "ALTER TABLE `product` DROP COLUMN `acme_category_id`")
	require.Contains(t, retargetMigration, "ALTER TABLE `product` DROP INDEX `uniq.product.acme_category_id_id`")
	require.Contains(t, retargetMigration, "ALTER TABLE `category` ADD COLUMN `acme_category_id`")
	require.Contains(t, retargetMigration, "ALTER TABLE `category` ADD UNIQUE INDEX `uniq.product.acme_category_id_id` (`acme_category_id`, `id`)")

	retargetRequest.Spec.MigrationTimestamp = retarget.MigrationTimestamp
	retargetApplyRaw := rawJSON(t, EntitySchemaApplyRequest{
		EntitySchemaPreviewRequest: retargetRequest,
		Revision:                   retarget.Revision,
		AllowDestructive:           true,
	})
	value, err = provider.entitySchemaApply(context.Background(), &retargetApplyRaw)
	require.NoError(t, err)
	applyEntityWorkspaceEdit(t, value.(EntitySchemaApplyResponse).Edit)
	loadRaw = rawJSON(t, EntitySchemaLoadRequest{FileURI: uriutil.FileURI(extensionPath)})
	value, err = provider.entitySchemaLoad(context.Background(), &loadRaw)
	require.NoError(t, err)
	retargeted := value.(entityschema.EntitySpec)
	require.Equal(t, `Shopware\Core\Content\Category\CategoryDefinition`, retargeted.ExtendedDefinitionClass)
	require.Equal(t, "category", retargeted.EntityName)
}

func TestEntitySchemaExtensionTargetCanChangeDuringEdit(t *testing.T) {
	previous := entityschema.EntitySpec{
		Mode: "edit", DefinitionKind: entityschema.DefinitionExtension,
		EntityName: "product", ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
	}
	requested := previous
	requested.EntityName = "category"
	requested.ExtendedDefinitionClass = `Shopware\Core\Content\Category\CategoryDefinition`
	issues, err := mergePreviousEntitySpec(requested, &previous, nil, &entityschema.Schema{})
	require.NoError(t, err)
	require.Empty(t, issues)
}

func TestEntitySchemaBulkExtensionPreviewApplyAndLoad(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Extension", "Catalog")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/bulk-extension","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\BulkExtension\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\BulkExtension\\Plugin"}
}`), 0o644))
	dalIndex, err := shopwaredal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	indexDefinition := func(path, namespace, className, entityName string) {
		t.Helper()
		source := `<?php declare(strict_types=1);
namespace ` + namespace + `;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ` + className + ` extends EntityDefinition {
    public const ENTITY_NAME = '` + entityName + `';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`
		require.NoError(t, dalIndex.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	productDefinition := `Shopware\Core\Content\Product\ProductDefinition`
	categoryDefinition := `Shopware\Core\Content\Category\CategoryDefinition`
	indexDefinition(filepath.Join(root, "vendor/shopware/core/Content/Product/ProductDefinition.php"), `Shopware\Core\Content\Product`, "ProductDefinition", "product")
	indexDefinition(filepath.Join(root, "vendor/shopware/core/Content/Category/CategoryDefinition.php"), `Shopware\Core\Content\Category`, "CategoryDefinition", "category")
	provider := NewProvider(root, nil, nil, dalIndex)
	toOne := func(id, property, storage, targetClass, targetEntity string) entityschema.FieldSpec {
		base := strings.TrimSuffix(targetClass, "Definition")
		return entityschema.FieldSpec{
			ID: id, Kind: entityschema.FieldManyToOne, PropertyName: property,
			ForeignKeyPropertyName: property + "Id", StorageName: storage,
			TargetDefinitionClass: targetClass, TargetEntityClass: base + "Entity",
			TargetCollectionClass: base + "Collection", TargetEntityName: targetEntity,
			ReferenceField: "id", ReferenceStorageName: "id",
			DeleteBehavior: entityschema.DeleteSetNull, Editable: true,
		}
	}
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", DefinitionKind: entityschema.DefinitionBulkExtension,
		PluginRootURI: uriutil.FileURI(root), DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\BulkExtension\Extension\Catalog`, ClassName: "Catalog", CreateMigration: true,
		BulkExtensions: []entityschema.BulkExtensionTargetSpec{
			{
				ID: "product", EntityName: "product", ExtendedDefinitionClass: productDefinition,
				Fields:  []entityschema.FieldSpec{toOne("category", "acmeCategory", "acme_category_id", categoryDefinition, "category")},
				Indexes: []entityschema.IndexSpec{{Name: "uniq.product.acme_category_id_id", Kind: entityschema.IndexUnique, Columns: []string{"acme_category_id", "id"}}},
			},
			{
				ID: "category", EntityName: "category", ExtendedDefinitionClass: categoryDefinition,
				Fields:  []entityschema.FieldSpec{toOne("product", "acmeProduct", "acme_product_id", productDefinition, "product")},
				Indexes: []entityschema.IndexSpec{{Name: "uniq.category.acme_product_id_id", Kind: entityschema.IndexUnique, Columns: []string{"acme_product_id", "id"}}},
			},
		},
	})
	request := EntitySchemaPreviewRequest{Spec: spec}
	raw := rawJSON(t, request)
	value, err := provider.entitySchemaPreview(context.Background(), &raw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, preview.Issues)
	require.Len(t, preview.Diff.AddedColumns, 2)
	var definition, service, migration string
	for _, file := range preview.Files {
		switch {
		case strings.HasSuffix(file.URI, "/CatalogBulkEntityExtension.php"):
			definition = file.After
		case strings.HasSuffix(file.URI, "/services.yaml"):
			service = file.After
		case strings.Contains(file.URI, "/Migration/Migration"):
			migration = file.After
		}
		require.NotContains(t, file.URI, "CatalogEntity.php")
		require.NotContains(t, file.URI, "CatalogCollection.php")
	}
	require.Contains(t, definition, "extends BulkEntityExtension")
	require.Contains(t, definition, "yield ProductDefinition::ENTITY_NAME")
	require.Contains(t, definition, "yield CategoryDefinition::ENTITY_NAME")
	require.Contains(t, service, "shopware.bulk.entity.extension")
	require.NotContains(t, service, "shopware.entity.definition")
	require.Contains(t, migration, "ALTER TABLE `product` ADD COLUMN `acme_category_id`")
	require.Contains(t, migration, "ALTER TABLE `category` ADD COLUMN `acme_product_id`")

	request.Spec.MigrationTimestamp = preview.MigrationTimestamp
	applyRaw := rawJSON(t, EntitySchemaApplyRequest{EntitySchemaPreviewRequest: request, Revision: preview.Revision})
	value, err = provider.entitySchemaApply(context.Background(), &applyRaw)
	require.NoError(t, err)
	applyEntityWorkspaceEdit(t, value.(EntitySchemaApplyResponse).Edit)

	path := filepath.Join(directory, "CatalogBulkEntityExtension.php")
	loadRaw := rawJSON(t, EntitySchemaLoadRequest{FileURI: uriutil.FileURI(path)})
	value, err = provider.entitySchemaLoad(context.Background(), &loadRaw)
	require.NoError(t, err)
	loaded := value.(entityschema.EntitySpec)
	require.Equal(t, entityschema.DefinitionBulkExtension, loaded.DefinitionKind)
	require.Equal(t, "Catalog", loaded.ClassName)
	require.Len(t, loaded.BulkExtensions, 2)
	require.Equal(t, spec.BulkExtensions[0].Indexes, loaded.BulkExtensions[0].Indexes)
	require.Equal(t, spec.BulkExtensions[1].Indexes, loaded.BulkExtensions[1].Indexes)
	require.Contains(t, loaded.BulkExtensions[0].ExtendedFields, entityschema.RelationTargetField{PropertyName: "id", StorageName: "id", Primary: true})
}

func TestEntitySchemaPreservesCustomBulkCollectAndRejectsStructuredEdits(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Extension")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/dynamic","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\Dynamic\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Dynamic\\Plugin"}
}`), 0o644))
	definitionPath := filepath.Join(directory, "DynamicBulkEntityExtension.php")
	source := `<?php declare(strict_types=1);

namespace Acme\Dynamic\Extension;

use Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension;

class DynamicBulkEntityExtension extends BulkEntityExtension
{
    public function collect(): \Generator
    {
        yield from $this->computedTargets();
    }

    private function computedTargets(): \Generator
    {
        yield 'product' => [];
    }
}
`
	require.NoError(t, os.WriteFile(definitionPath, []byte(source), 0o644))
	provider := NewProvider(root, nil, nil)
	loadRaw := rawJSON(t, EntitySchemaLoadRequest{FileURI: uriutil.FileURI(definitionPath)})
	value, err := provider.entitySchemaLoad(context.Background(), &loadRaw)
	require.NoError(t, err)
	loaded := value.(entityschema.EntitySpec)
	require.NotEmpty(t, loaded.CollectMethodRaw)
	require.Empty(t, loaded.BulkExtensions)

	previewRaw := rawJSON(t, EntitySchemaPreviewRequest{Spec: loaded})
	value, err = provider.entitySchemaPreview(context.Background(), &previewRaw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, preview.Issues)
	for _, file := range preview.Files {
		if file.URI == uriutil.FileURI(definitionPath) {
			require.Equal(t, source, file.After)
		}
	}

	mutated := loaded
	mutated.CollectMethodRaw += "\n// changed"
	mutated.BulkExtensions = []entityschema.BulkExtensionTargetSpec{{ID: "product", EntityName: "product"}}
	previewRaw = rawJSON(t, EntitySchemaPreviewRequest{Spec: mutated})
	value, err = provider.entitySchemaPreview(context.Background(), &previewRaw)
	require.NoError(t, err)
	issues := value.(EntitySchemaPreviewResponse).Issues
	codes := make([]string, 0, len(issues))
	for _, issue := range issues {
		codes = append(codes, issue.Code)
	}
	require.Contains(t, codes, "entity.bulkExtension.collectRaw.locked")
}

func TestEntitySchemaPreservesAndLocksCustomDefinitionBehavior(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Content", "Catalog")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/behavior","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\Behavior\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Behavior\\Plugin"}
}`), 0o644))
	definitionPath := filepath.Join(directory, "CatalogDefinition.php")
	source := `<?php declare(strict_types=1);
namespace Acme\Behavior\Content\Catalog;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_catalog';
    public function getEntityClass(): string { return CatalogEntity::class; }
    public function getCollectionClass(): string { return CatalogCollection::class; }
    public function since(): ?string { return $this->configuredSince(); }
    public function isVersionAware(): bool { return $this->configuredVersioning(); }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`
	require.NoError(t, os.WriteFile(definitionPath, []byte(source), 0o644))
	provider := NewProvider(root, nil, nil)
	loadRaw := rawJSON(t, EntitySchemaLoadRequest{FileURI: uriutil.FileURI(definitionPath), DefinitionClass: `Acme\Behavior\Content\Catalog\CatalogDefinition`, DefinitionKind: entityschema.DefinitionEntity})
	value, err := provider.entitySchemaLoad(context.Background(), &loadRaw)
	require.NoError(t, err)
	loaded := value.(entityschema.EntitySpec)
	require.NotEmpty(t, loaded.DefinitionMetadata.SinceMethodRaw)
	require.NotEmpty(t, loaded.DefinitionBehavior.VersionAwareMethodRaw)

	previewRaw := rawJSON(t, EntitySchemaPreviewRequest{Spec: loaded})
	value, err = provider.entitySchemaPreview(context.Background(), &previewRaw)
	require.NoError(t, err)
	require.Empty(t, value.(EntitySchemaPreviewResponse).Issues)

	mutated := loaded
	metadata := *loaded.DefinitionMetadata
	metadata.SinceMethodRaw += "\n// changed"
	mutated.DefinitionMetadata = &metadata
	previewRaw = rawJSON(t, EntitySchemaPreviewRequest{Spec: mutated})
	value, err = provider.entitySchemaPreview(context.Background(), &previewRaw)
	require.NoError(t, err)
	require.Contains(t, entityIssueCodes(value.(EntitySchemaPreviewResponse).Issues), "entity.definition.raw.locked")
}

func TestEntitySchemaAppliesEveryClassBasedDefinitionKindTransition(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Content", "Transition")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/transition","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\Transition\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Transition\\Plugin"}
}`), 0o644))
	dalIndex, err := shopwaredal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	productPath := filepath.Join(root, "vendor", "shopware", "core", "Content", "Product", "ProductDefinition.php")
	productSource := `<?php declare(strict_types=1);
namespace Shopware\Core\Content\Product;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ProductDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'product';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`
	require.NoError(t, dalIndex.Index(indexer.NewParsedFile(productPath, []byte(productSource))))
	provider := NewProvider(root, nil, nil, dalIndex)
	definitionPath := filepath.Join(directory, "TransitionDefinition.php")
	entityPath := filepath.Join(directory, "TransitionEntity.php")
	collectionPath := filepath.Join(directory, "TransitionCollection.php")
	servicePath := filepath.Join(root, "src", "Resources", "config", "services.yaml")

	apply := func(spec entityschema.EntitySpec, allowDestructive bool) EntitySchemaPreviewResponse {
		t.Helper()
		spec.MigrationTimestamp = 0
		request := EntitySchemaPreviewRequest{Spec: spec}
		raw := rawJSON(t, request)
		value, previewErr := provider.entitySchemaPreview(context.Background(), &raw)
		require.NoError(t, previewErr)
		preview := value.(EntitySchemaPreviewResponse)
		require.Empty(t, preview.Issues, "diff: %#v", preview.Diff)
		request.Spec.MigrationTimestamp = preview.MigrationTimestamp
		stableRaw := rawJSON(t, request)
		stableValue, stableErr := provider.entitySchemaPreview(context.Background(), &stableRaw)
		require.NoError(t, stableErr)
		require.Equal(t, preview.Revision, stableValue.(EntitySchemaPreviewResponse).Revision)
		applyRaw := rawJSON(t, EntitySchemaApplyRequest{
			EntitySchemaPreviewRequest: request,
			Revision:                   preview.Revision,
			AllowDestructive:           allowDestructive,
		})
		value, applyErr := provider.entitySchemaApply(context.Background(), &applyRaw)
		require.NoError(t, applyErr)
		applyEntityWorkspaceEdit(t, value.(EntitySchemaApplyResponse).Edit)
		return preview
	}
	load := func() entityschema.EntitySpec {
		t.Helper()
		raw := rawJSON(t, EntitySchemaLoadRequest{FileURI: uriutil.FileURI(definitionPath)})
		value, loadErr := provider.entitySchemaLoad(context.Background(), &raw)
		require.NoError(t, loadErr)
		return value.(entityschema.EntitySpec)
	}
	convert := func(kind entityschema.DefinitionKind) EntitySchemaPreviewResponse {
		t.Helper()
		spec := load()
		spec.DefinitionKind = kind
		spec.Translation = nil
		spec.InheritanceAware = false
		spec.ReadProtected = false
		spec.WriteProtected = false
		spec.PreservedProtections = nil
		spec.ProtectionMethodRaw = ""
		spec.FieldModifications = nil
		spec.ModifyFieldsMethodRaw = ""
		spec.ExtendedDefinitionClass = ""
		spec.ExtendedFields = nil
		spec.BulkExtensions = nil
		spec.EntityName = "acme_transition"
		spec.Fields = []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, PropertyName: "id", StorageName: "id", Required: true, Primary: true, Editable: true},
			{ID: "note", Kind: entityschema.FieldString, PropertyName: "note", StorageName: "note", Editable: true},
		}
		switch kind {
		case entityschema.DefinitionExtension:
			spec.EntityName = "product"
			spec.ExtendedDefinitionClass = `Shopware\Core\Content\Product\ProductDefinition`
			spec.Fields = spec.Fields[1:]
			spec.Fields[0].Behavior = &entityschema.FieldBehavior{Runtime: true}
		case entityschema.DefinitionBulkExtension:
			spec.EntityName = ""
			spec.Fields = nil
			spec.Indexes = nil
			spec.BulkExtensions = []entityschema.BulkExtensionTargetSpec{{
				ID: "product", EntityName: "product",
				ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
				Fields: []entityschema.FieldSpec{{
					ID: "note", Kind: entityschema.FieldString, PropertyName: "note", StorageName: "note",
					Behavior: &entityschema.FieldBehavior{Runtime: true}, Editable: true,
				}},
			}}
		}
		return apply(spec, true)
	}

	created := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", PluginRootURI: uriutil.FileURI(root), DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\Transition\Content\Transition`, ClassName: "Transition", EntityName: "acme_transition",
		CreateMigration: true,
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, PropertyName: "id", StorageName: "id", Required: true, Primary: true, Editable: true},
			{ID: "note", Kind: entityschema.FieldString, PropertyName: "note", StorageName: "note", Editable: true},
		},
	})
	apply(created, false)
	require.FileExists(t, entityPath)
	require.FileExists(t, collectionPath)

	for _, transition := range []struct {
		kind        entityschema.DefinitionKind
		destructive bool
	}{
		{entityschema.DefinitionMapping, true},
		{entityschema.DefinitionEntity, false},
		{entityschema.DefinitionExtension, true},
		{entityschema.DefinitionBulkExtension, false},
		{entityschema.DefinitionMapping, false},
		{entityschema.DefinitionBulkExtension, true},
		{entityschema.DefinitionExtension, false},
		{entityschema.DefinitionEntity, false},
	} {
		kind := transition.kind
		preview := convert(kind)
		require.Equal(t, transition.destructive, preview.Destructive)
		definition, readErr := os.ReadFile(definitionPath)
		require.NoError(t, readErr)
		service, readErr := os.ReadFile(servicePath)
		require.NoError(t, readErr)
		switch kind {
		case entityschema.DefinitionEntity:
			require.Contains(t, string(definition), "extends EntityDefinition")
			require.NotContains(t, string(definition), "new Extension()")
			require.FileExists(t, entityPath)
			require.FileExists(t, collectionPath)
			require.Contains(t, string(service), "shopware.entity.definition")
			require.NotContains(t, string(service), "shopware.entity.extension")
		case entityschema.DefinitionMapping:
			require.Contains(t, string(definition), "extends MappingEntityDefinition")
			require.NotContains(t, string(definition), "function getEntityClass")
			require.NotContains(t, string(definition), "function getCollectionClass")
			require.NotContains(t, string(definition), "new Extension()")
			require.NoFileExists(t, entityPath)
			require.NoFileExists(t, collectionPath)
			require.Contains(t, string(service), "shopware.entity.definition")
			require.NotContains(t, string(service), "shopware.entity.extension")
		case entityschema.DefinitionExtension:
			require.Contains(t, string(definition), "extends EntityExtension")
			require.Contains(t, string(definition), "return ProductDefinition::ENTITY_NAME")
			require.NoFileExists(t, entityPath)
			require.NoFileExists(t, collectionPath)
			require.Contains(t, string(service), "shopware.entity.extension")
			require.NotContains(t, string(service), "shopware.entity.definition")
		case entityschema.DefinitionBulkExtension:
			require.Contains(t, string(definition), "extends BulkEntityExtension")
			require.Contains(t, string(definition), "yield ProductDefinition::ENTITY_NAME")
			require.NoFileExists(t, entityPath)
			require.NoFileExists(t, collectionPath)
			require.Contains(t, string(service), "shopware.bulk.entity.extension")
			require.NotContains(t, string(service), "shopware.entity.extension")
			require.NotContains(t, string(service), "shopware.entity.definition")
		}
		require.Equal(t, kind, load().DefinitionKind)
	}
}

func TestNormalizeDefinitionTransitionPreservesOnlyCurrentMappingCompanions(t *testing.T) {
	previousEntity := entityschema.EntitySpec{
		Mode: "edit", DefinitionKind: entityschema.DefinitionEntity,
		EntityClass: `Acme\Example\ExampleEntity`, CollectionClass: `Acme\Example\ExampleCollection`,
		EntityURI: "file:///plugin/ExampleEntity.php", CollectionURI: "file:///plugin/ExampleCollection.php",
	}
	next := previousEntity
	next.DefinitionKind = entityschema.DefinitionMapping
	normalized := normalizeDefinitionTransition(next, &previousEntity)
	require.Empty(t, normalized.EntityClass)
	require.Empty(t, normalized.CollectionClass)
	require.Equal(t, previousEntity.EntityURI, normalized.EntityURI)
	require.Equal(t, previousEntity.CollectionURI, normalized.CollectionURI)

	previousMapping := next
	previousMapping.EntityClass = `Acme\Example\CustomMappingEntity`
	previousMapping.CollectionClass = `Acme\Example\CustomMappingCollection`
	normalized = normalizeDefinitionTransition(previousMapping, &previousMapping)
	require.Equal(t, previousMapping.EntityClass, normalized.EntityClass)
	require.Equal(t, previousMapping.CollectionClass, normalized.CollectionClass)

	previousExtension := entityschema.EntitySpec{Mode: "edit", DefinitionKind: entityschema.DefinitionExtension}
	toEntity := previousExtension
	toEntity.DefinitionKind = entityschema.DefinitionEntity
	toEntity.Fields = []entityschema.FieldSpec{{
		ID: "note", Kind: entityschema.FieldString, PropertyName: "note", StorageName: "note",
		Metadata:            &entityschema.FieldMetadata{Extension: true},
		AssociationMetadata: &entityschema.FieldMetadata{Extension: true, Immutable: true},
		Editable:            true,
	}}
	normalized = normalizeDefinitionTransition(toEntity, &previousExtension)
	require.Nil(t, normalized.Fields[0].Metadata)
	require.NotNil(t, normalized.Fields[0].AssociationMetadata)
	require.False(t, normalized.Fields[0].AssociationMetadata.Extension)
	require.True(t, normalized.Fields[0].AssociationMetadata.Immutable)
}

func TestEntitySchemaTranslationBundlePreviewApplyAndLoad(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Content", "Blog")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/blog","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\Blog\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\Blog\\Plugin"}
}`), 0o644))
	provider := NewProvider(root, nil, nil)
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", PluginRootURI: uriutil.FileURI(root), DirectoryURI: uriutil.FileURI(directory),
		Namespace: `Acme\Blog\Content\Blog`, ClassName: "Blog", EntityName: "acme_blog", CreateMigration: true,
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "version", Kind: entityschema.FieldVersion, Editable: true},
			{ID: "name", Kind: entityschema.FieldString, PropertyName: "name", StorageName: "name", Required: true, APIAware: true, Translated: true, Editable: true},
		},
		Indexes: []entityschema.IndexSpec{{
			Name: "uniq.acme_blog_translation.name_language", Kind: entityschema.IndexUnique,
			Columns: []string{"name", "language_id"}, Translation: true,
		}},
	})
	request := EntitySchemaPreviewRequest{Spec: spec}
	raw := rawJSON(t, request)
	value, err := provider.entitySchemaPreview(context.Background(), &raw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, preview.Issues)
	require.Len(t, preview.Files, 10)
	files := make(map[string]string)
	for _, file := range preview.Files {
		files[file.URI] = file.After
	}
	translationDirectory := filepath.Join(directory, "Aggregate", "BlogTranslation")
	require.Contains(t, files[uriutil.FileURI(filepath.Join(directory, "BlogDefinition.php"))], "new TranslatedField('name')")
	require.Contains(t, files[uriutil.FileURI(filepath.Join(translationDirectory, "BlogTranslationDefinition.php"))], "extends EntityTranslationDefinition")
	require.Contains(t, files[uriutil.FileURI(filepath.Join(translationDirectory, "BlogTranslationEntity.php"))], "protected string $acmeBlogVersionId;")
	service := files[uriutil.FileURI(filepath.Join(root, "src", "Resources", "config", "services.yaml"))]
	require.Contains(t, service, `Acme\Blog\Content\Blog\BlogDefinition`)
	require.Contains(t, service, `Acme\Blog\Content\Blog\Aggregate\BlogTranslation\BlogTranslationDefinition`)
	var migration string
	for uri, source := range files {
		if strings.Contains(uri, "/Migration/Migration") {
			migration = source
		}
	}
	require.Contains(t, migration, "CREATE TABLE IF NOT EXISTS `acme_blog_translation`")
	require.Contains(t, migration, "FOREIGN KEY (`acme_blog_id`, `acme_blog_version_id`)")
	require.Contains(t, migration, "UNIQUE KEY `uniq.acme_blog_translation.name_language` (`name`, `language_id`)")

	request.Spec.MigrationTimestamp = preview.MigrationTimestamp
	applyRaw := rawJSON(t, EntitySchemaApplyRequest{EntitySchemaPreviewRequest: request, Revision: preview.Revision})
	value, err = provider.entitySchemaApply(context.Background(), &applyRaw)
	require.NoError(t, err)
	applyEntityWorkspaceEdit(t, value.(EntitySchemaApplyResponse).Edit)

	loadRaw := rawJSON(t, EntitySchemaLoadRequest{FileURI: uriutil.FileURI(filepath.Join(directory, "BlogDefinition.php"))})
	value, err = provider.entitySchemaLoad(context.Background(), &loadRaw)
	require.NoError(t, err)
	loaded := value.(entityschema.EntitySpec)
	require.NotNil(t, loaded.Translation)
	require.Equal(t, uriutil.FileURI(filepath.Join(translationDirectory, "BlogTranslationDefinition.php")), loaded.Translation.DefinitionURI)
	require.Equal(t, []entityschema.IndexSpec{{
		Name: "uniq.acme_blog_translation.name_language", Kind: entityschema.IndexUnique,
		Columns: []string{"name", "language_id"}, Translation: true,
	}}, loaded.Indexes)
	var translated entityschema.FieldSpec
	for _, field := range loaded.Fields {
		if field.PropertyName == "name" {
			translated = field
		}
	}
	require.True(t, translated.Translated)
	require.Equal(t, entityschema.FieldString, translated.Kind)
	loaded.Fields = append(loaded.Fields, entityschema.FieldSpec{
		ID: "description", Kind: entityschema.FieldLongText, PropertyName: "description", StorageName: "description", Translated: true, Editable: true,
	})
	editRaw := rawJSON(t, EntitySchemaPreviewRequest{Spec: loaded})
	value, err = provider.entitySchemaPreview(context.Background(), &editRaw)
	require.NoError(t, err)
	editPreview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, editPreview.Issues)
	var translationDefinitionAfter string
	for _, file := range editPreview.Files {
		if strings.HasSuffix(file.URI, "/BlogTranslationDefinition.php") {
			translationDefinitionAfter = file.After
		}
	}
	require.Contains(t, translationDefinitionAfter, "new LongTextField('description', 'description')")

	loaded.Fields = loaded.Fields[:len(loaded.Fields)-1]
	for index := range loaded.Fields {
		if loaded.Fields[index].PropertyName == "name" {
			loaded.Fields[index].Translated = false
			loaded.Fields[index].MigrationDefault = "'migrated'"
		}
	}
	require.NotNil(t, loaded.Translation)
	loaded.Indexes = nil
	loaded.Translation.Enabled = false
	removeRequest := EntitySchemaPreviewRequest{Spec: loaded}
	removeRaw := rawJSON(t, removeRequest)
	value, err = provider.entitySchemaPreview(context.Background(), &removeRaw)
	require.NoError(t, err)
	removePreview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, removePreview.Issues)
	require.True(t, removePreview.Destructive)
	deleted := make(map[string]bool)
	for _, file := range removePreview.Files {
		if file.Action == "delete" {
			deleted[file.URI] = true
		}
		if strings.HasSuffix(file.URI, "/BlogDefinition.php") {
			require.Contains(t, file.After, "new StringField('name', 'name')")
			require.NotContains(t, file.After, "TranslationsAssociationField")
		}
		if strings.HasSuffix(file.URI, "/services.yaml") {
			require.NotContains(t, file.After, `BlogTranslation\BlogTranslationDefinition`)
		}
	}
	for _, name := range []string{"BlogTranslationDefinition.php", "BlogTranslationEntity.php", "BlogTranslationCollection.php"} {
		require.True(t, deleted[uriutil.FileURI(filepath.Join(translationDirectory, name))], name)
	}
	removeApplyRaw := rawJSON(t, EntitySchemaApplyRequest{
		EntitySchemaPreviewRequest: removeRequest,
		Revision:                   removePreview.Revision,
		AllowDestructive:           true,
	})
	value, err = provider.entitySchemaApply(context.Background(), &removeApplyRaw)
	require.NoError(t, err)
	applyEntityWorkspaceEdit(t, value.(EntitySchemaApplyResponse).Edit)
	require.NoFileExists(t, filepath.Join(translationDirectory, "BlogTranslationDefinition.php"))
	require.NoFileExists(t, filepath.Join(translationDirectory, "BlogTranslationEntity.php"))
	require.NoFileExists(t, filepath.Join(translationDirectory, "BlogTranslationCollection.php"))
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

	spec.Fields = spec.Fields[:1]
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
		if change.Kind == protocol.DeleteFileOperation {
			path, err := uriutil.Path(change.URI)
			require.NoError(t, err)
			require.NoError(t, os.Remove(path))
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

func TestEntitySchemaFieldCatalogIncludesSpecializedCreationTemplates(t *testing.T) {
	types := entitySchemaFieldTypes("", nil)
	var customFields, stateMachine *EntitySchemaFieldType
	for index := range types {
		switch types[index].ID {
		case "specialized:CustomFields":
			customFields = &types[index]
		case "specialized:StateMachineStateField":
			stateMachine = &types[index]
		}
	}
	require.NotNil(t, customFields)
	require.NotNil(t, customFields.Template)
	require.Equal(t, "custom_fields", customFields.Template.StorageName)
	require.NotNil(t, stateMachine)
	require.Equal(t, 1, stateMachine.Template.Implementation.MinimumAdditionalArguments)
}

func TestEntitySchemaEnumValidationUsesPHPEnumSemantics(t *testing.T) {
	phpIndex, err := phpindex.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	path := filepath.Join(t.TempDir(), "Status.php")
	source := `<?php
namespace Acme\Example;
enum Status: int { case Active = 1; }
`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, []byte(source))))
	provider := NewProvider(t.TempDir(), phpIndex, nil)
	spec := entityschema.CompleteSpec(entityschema.EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example", ShopwareVersion: "6.7.0",
		Fields: []entityschema.FieldSpec{
			{ID: "id", Kind: entityschema.FieldID, Editable: true},
			{ID: "status", Kind: entityschema.FieldEnum, PropertyName: "status", StorageName: "status", EnumClass: `Acme\Example\Status`, EnumCase: "Active", Editable: true},
		},
	})
	provider.entities.EnrichSpec(&spec)
	require.Equal(t, "int", spec.Fields[1].EnumBackingType)
	require.Empty(t, validateEntitySchemaSpec(provider, spec))

	spec.Fields[1].EnumCase = "Missing"
	issues := validateEntitySchemaSpec(provider, spec)
	require.Contains(t, issues, entityschema.ValidationIssue{
		Code: "entity.field.enum.case.unavailable", Message: `Enum case Acme\Example\Status::Missing was not found`, FieldID: "status", Severity: "error",
	})
	spec.Fields[1].EnumCase = "Active"
	spec.Fields[1].EnumBackingType = "string"
	issues = validateEntitySchemaSpec(provider, spec)
	require.Contains(t, issues, entityschema.ValidationIssue{
		Code: "entity.field.enum.backing.mismatch", Message: `Enum Acme\Example\Status is int-backed, not string-backed`, FieldID: "status", Severity: "error",
	})
}

func TestEntitySchemaFieldCatalogFiltersTargetVersionAndInstalledClasses(t *testing.T) {
	classes := map[string]bool{
		`Shopware\Core\Framework\DataAbstractionLayer\Field\CustomFields`:     true,
		`Shopware\Core\Framework\DataAbstractionLayer\Field\EnumField`:        true,
		`Shopware\Core\Content\MeasurementSystem\Field\MeasurementUnitsField`: true,
	}
	types := entitySchemaFieldTypes("6.7.0", func(class string) bool { return classes[class] })
	ids := make(map[string]bool, len(types))
	enumAvailable := false
	for _, fieldType := range types {
		ids[fieldType.ID] = true
		enumAvailable = enumAvailable || fieldType.Kind == "enum"
	}
	require.True(t, ids["specialized:CustomFields"])
	require.True(t, enumAvailable)
	require.False(t, ids["specialized:MeasurementUnitsField"], "field was introduced in 6.7.1")
	require.False(t, ids["specialized:PriceField"], "installed-class filter must be honored")

	types = entitySchemaFieldTypes("6.7.1", func(class string) bool { return classes[class] })
	ids = make(map[string]bool, len(types))
	for _, fieldType := range types {
		ids[fieldType.ID] = true
	}
	require.True(t, ids["specialized:MeasurementUnitsField"])

	types = entitySchemaFieldTypes("6.6.9", func(class string) bool { return classes[class] })
	for _, fieldType := range types {
		require.NotEqual(t, "enum", fieldType.Kind)
	}
}

func TestEntitySchemaBootstrapHidesBulkExtensionBeforeShopware6610(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src", "Entity"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name": "acme/legacy",
  "type": "shopware-platform-plugin",
  "require": {"shopware/core": "~6.6.9"},
  "autoload": {"psr-4": {"Acme\\Legacy\\": "src/"}},
  "extra": {"shopware-plugin-class": "Acme\\Legacy\\AcmeLegacy"}
}`), 0o644))
	provider := NewProvider(root, nil, nil)
	raw := rawJSON(t, EntitySchemaBootstrapRequest{DirectoryURI: uriutil.FileURI(filepath.Join(root, "src", "Entity"))})
	value, err := provider.entitySchemaBootstrap(context.Background(), &raw)
	require.NoError(t, err)
	bootstrap := value.(EntitySchemaBootstrapResponse)
	require.NotContains(t, bootstrap.DefinitionKinds, entityschema.DefinitionBulkExtension)
	for _, fieldType := range bootstrap.FieldTypes {
		require.NotContains(t, fieldType.DefinitionKinds, entityschema.DefinitionBulkExtension)
	}
}

func TestEntitySchemaLoadsAndRewritesDefinitionUsingCustomAbstractBase(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "src", "Catalog")
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name":"acme/custom-base","type":"shopware-platform-plugin","require":{"shopware/core":"~6.7"},
  "autoload":{"psr-4":{"Acme\\CustomBase\\":"src/"}},
  "extra":{"shopware-plugin-class":"Acme\\CustomBase\\Plugin"}
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "AbstractRecord.php"), []byte(`<?php declare(strict_types=1);
namespace Acme\CustomBase;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
abstract class AbstractRecord extends EntityDefinition {}
`), 0o644))
	definitionPath := filepath.Join(directory, "CatalogModel.php")
	require.NoError(t, os.WriteFile(definitionPath, []byte(`<?php declare(strict_types=1);
namespace Acme\CustomBase\Catalog;
use Acme\CustomBase\AbstractRecord;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogModel extends AbstractRecord {
    public const ENTITY_NAME = 'acme_catalog';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}
`), 0o644))
	provider := NewProvider(root, nil, nil)
	bootstrapRaw := rawJSON(t, EntitySchemaBootstrapRequest{DirectoryURI: uriutil.FileURI(directory)})
	value, err := provider.entitySchemaBootstrap(context.Background(), &bootstrapRaw)
	require.NoError(t, err)
	bootstrap := value.(EntitySchemaBootstrapResponse)
	var target *EntitySchemaEditableTarget
	for index := range bootstrap.Editable {
		if bootstrap.Editable[index].DefinitionClass == `Acme\CustomBase\Catalog\CatalogModel` {
			target = &bootstrap.Editable[index]
			break
		}
	}
	require.NotNil(t, target)
	require.Equal(t, entityschema.DefinitionEntity, target.DefinitionKind)

	loadRaw := rawJSON(t, EntitySchemaLoadRequest{
		FileURI: target.FileURI, DefinitionClass: target.DefinitionClass, DefinitionKind: target.DefinitionKind,
	})
	value, err = provider.entitySchemaLoad(context.Background(), &loadRaw)
	require.NoError(t, err)
	loaded := value.(entityschema.EntitySpec)
	require.Equal(t, "acme_catalog", loaded.EntityName)
	loaded.Fields = append(loaded.Fields, entityschema.FieldSpec{
		ID: "label", Kind: entityschema.FieldString, PropertyName: "label", StorageName: "label", Editable: true,
	})
	previewRaw := rawJSON(t, EntitySchemaPreviewRequest{Spec: loaded})
	value, err = provider.entitySchemaPreview(context.Background(), &previewRaw)
	require.NoError(t, err)
	preview := value.(EntitySchemaPreviewResponse)
	require.Empty(t, preview.Issues)
	for _, file := range preview.Files {
		if file.URI == target.FileURI {
			require.Contains(t, file.After, "extends AbstractRecord")
			require.Contains(t, file.After, "new StringField('label', 'label')")
			return
		}
	}
	t.Fatal("rewritten custom-base definition was not previewed")
}

func TestEntitySchemaSearchResolvesIndexedCustomDALBaseSemantically(t *testing.T) {
	dalIndex, err := shopwaredal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	phpIndex, err := phpindex.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	base := []byte(`<?php
namespace Acme;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
abstract class AbstractRecord extends EntityDefinition {}
`)
	entity := []byte(`<?php
namespace Acme;
class CatalogModel extends AbstractRecord {
    public const ENTITY_NAME = 'acme_catalog';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`)
	framework := []byte(`<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
abstract class EntityDefinition {}
`)
	for path, source := range map[string][]byte{
		"/project/vendor/shopware/EntityDefinition.php": framework,
		"/project/src/AbstractRecord.php":               base,
		"/project/src/CatalogModel.php":                 entity,
	} {
		file := indexer.NewParsedFile(path, source)
		require.NoError(t, phpIndex.Index(file))
		require.NoError(t, dalIndex.Index(file))
	}
	provider := NewProvider("/project", phpIndex, nil, dalIndex)
	results, err := provider.relationTargets("acme_catalog")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, entityschema.DefinitionEntity, results[0].DefinitionKind)
	require.Equal(t, `Acme\CatalogModel`, results[0].DefinitionClass)
	require.Contains(t, results[0].Fields, entityschema.RelationTargetField{PropertyName: "id", StorageName: "id", Primary: true})
}
