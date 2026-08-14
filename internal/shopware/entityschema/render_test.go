package entityschema

import (
	"reflect"
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestMappingDefinitionRoundTripsCompositeForeignKeysWithoutImplicitTimestamps(t *testing.T) {
	product := RelationTarget{
		DefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		EntityClass:     `Shopware\Core\Content\Product\ProductEntity`,
		CollectionClass: `Shopware\Core\Content\Product\ProductCollection`,
		EntityName:      "product",
		Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
		VersionAware:    true,
	}
	tag := RelationTarget{
		DefinitionClass: `Shopware\Core\System\Tag\TagDefinition`,
		EntityClass:     `Shopware\Core\System\Tag\TagEntity`,
		CollectionClass: `Shopware\Core\System\Tag\TagCollection`,
		EntityName:      "tag",
		Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
	}
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionMapping,
		Namespace: `Acme\Example\Content\ProductTag`, ClassName: "ProductTag", EntityName: "acme_product_tag",
		Fields: []FieldSpec{
			{ID: "product", Kind: FieldManyToOne, PropertyName: "product", ForeignKeyPropertyName: "productId", StorageName: "product_id", TargetDefinitionClass: product.DefinitionClass, TargetEntityClass: product.EntityClass, TargetCollectionClass: product.CollectionClass, TargetEntityName: product.EntityName, ReferenceField: "id", ReferenceStorageName: "id", Required: true, Primary: true, DeleteBehavior: DeleteRestrict, Editable: true},
			{ID: "product-version", Kind: FieldReferenceVersion, PropertyName: "productVersionId", StorageName: "product_version_id", TargetDefinitionClass: product.DefinitionClass, TargetEntityName: product.EntityName, Required: true, Primary: true, Editable: true},
			{ID: "tag", Kind: FieldManyToOne, PropertyName: "tag", ForeignKeyPropertyName: "tagId", StorageName: "tag_id", TargetDefinitionClass: tag.DefinitionClass, TargetEntityClass: tag.EntityClass, TargetCollectionClass: tag.CollectionClass, TargetEntityName: tag.EntityName, ReferenceField: "id", ReferenceStorageName: "id", Required: true, Primary: true, DeleteBehavior: DeleteRestrict, Editable: true},
		},
	})
	require.Empty(t, spec.EntityClass)
	require.Empty(t, spec.CollectionClass)

	definition, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(definition).Errors, definition)
	require.Contains(t, definition, "extends MappingEntityDefinition")
	require.NotContains(t, definition, "getEntityClass")
	require.NotContains(t, definition, "getCollectionClass")
	require.NotContains(t, definition, "CreatedAtField")
	require.NotContains(t, definition, "UpdatedAtField")
	require.Contains(t, definition, "new ReferenceVersionField(ProductDefinition::class, 'product_version_id')")

	lookup := func(class string) (RelationTarget, bool) {
		switch class {
		case product.DefinitionClass:
			return product, true
		case tag.DefinitionClass:
			return tag, true
		default:
			return RelationTarget{}, false
		}
	}
	imported, err := ImportDefinition(definition, lookup)
	require.NoError(t, err)
	require.Equal(t, DefinitionMapping, imported.DefinitionKind)
	require.Empty(t, imported.EntityClass)
	require.Empty(t, imported.CollectionClass)

	before, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	after, err := SchemaFromSpec(imported)
	require.NoError(t, err)
	before.BackfillFree()
	after.BackfillFree()
	require.True(t, reflect.DeepEqual(before, after), "schema mismatch\nbefore: %#v\nafter: %#v", before, after)
	require.NotContains(t, before.Columns, "created_at")
	require.NotContains(t, before.Columns, "updated_at")
	require.Len(t, before.Columns, 3)
	require.True(t, before.Columns["product_id"].PrimaryKey)
	require.True(t, before.Columns["product_version_id"].PrimaryKey)
	require.True(t, before.Columns["tag_id"].PrimaryKey)
}

func TestRenderDefinitionImportsStandaloneForeignKeyTarget(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Namespace: "Acme\\Example", ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{Kind: FieldID, APIAware: true, SearchRanking: 100, Editable: true},
			{
				Kind: FieldForeignKey, StorageName: "product_id", PropertyName: "productId",
				TargetDefinitionClass: "Shopware\\Core\\Content\\Product\\ProductDefinition",
				Editable:              true,
			},
		},
	})

	source, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Contains(t, source, "use Shopware\\Core\\Content\\Product\\ProductDefinition;")
	require.Contains(t, source, "new ApiAware()")
	require.Contains(t, source, "new SearchRanking(100)")

	roundTripped, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.True(t, roundTripped.Fields[0].APIAware)
	require.Equal(t, float64(100), roundTripped.Fields[0].SearchRanking)
	require.Equal(t, "Shopware\\Core\\Content\\Product\\ProductDefinition", roundTripped.Fields[1].TargetDefinitionClass)
}

func TestMappingDefinitionSupportsStandaloneForeignKeyAndExplicitTimestamps(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionMapping,
		Namespace: `Acme\Example\Mapping`, ClassName: "ExampleMapping", EntityName: "acme_example_mapping",
		Fields: []FieldSpec{
			{ID: "source", Kind: FieldForeignKey, PropertyName: "sourceId", StorageName: "source_id", TargetDefinitionClass: `Acme\Example\SourceDefinition`, TargetEntityName: "acme_source", Required: true, Primary: true, Editable: true},
			{ID: "created", Kind: FieldCreatedAt, Editable: true},
			{ID: "updated", Kind: FieldUpdatedAt, Editable: true},
		},
	})
	definition, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Contains(t, definition, "new FkField('source_id', 'sourceId', SourceDefinition::class, 'id')")
	require.Contains(t, definition, "new CreatedAtField()")
	require.Contains(t, definition, "new UpdatedAtField()")
	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.Contains(t, entity.Columns, "created_at")
	require.Contains(t, entity.Columns, "updated_at")
	require.Equal(t, DeleteRestrict, entity.ForeignKeys["fk.acme_example_mapping.source_id"].OnDelete)
}

func TestImportedMappingDefinitionPreservesOptionalEntityAndCollectionClasses(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\Framework\DataAbstractionLayer\MappingEntityDefinition;
class ExampleMappingDefinition extends MappingEntityDefinition {
    public const ENTITY_NAME = 'acme_mapping';
    public function getEntityClass(): string { return ExampleMappingEntity::class; }
    public function getCollectionClass(): string { return ExampleMappingCollection::class; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        (new IdField('id', 'id'))->addFlags(new Required(), new PrimaryKey()),
    ]); }
}`

	imported, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.Equal(t, `Acme\Example\ExampleMappingEntity`, imported.EntityClass)
	require.Equal(t, `Acme\Example\ExampleMappingCollection`, imported.CollectionClass)

	rendered, err := RenderDefinition(imported)
	require.NoError(t, err)
	require.Contains(t, rendered, "public function getEntityClass(): string")
	require.Contains(t, rendered, "public function getCollectionClass(): string")

	roundTripped, err := ImportDefinition(rendered, nil)
	require.NoError(t, err)
	require.Equal(t, imported.EntityClass, roundTripped.EntityClass)
	require.Equal(t, imported.CollectionClass, roundTripped.CollectionClass)
}

func TestMappingDefinitionRejectsTranslationSchemaGeneration(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionMapping,
		Namespace: `Acme\Example\Mapping`, ClassName: "ExampleMapping", EntityName: "acme_example_mapping",
		Translation: &TranslationSpec{Enabled: true},
		Fields:      []FieldSpec{{ID: "id", Kind: FieldID, Editable: true}},
	})
	_, err := SchemaEntitiesFromSpec(spec)
	require.EqualError(t, err, "mapping definitions cannot own translation bundles")
}

func exampleSpec() EntitySpec {
	return CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example\Entity\Catalog`,
		ClassName: "CatalogItem", EntityName: "acme_catalog_item",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name", Required: true, APIAware: true, MaxLength: 500, Editable: true},
			{ID: "active", Kind: FieldBool, PropertyName: "active", StorageName: "active", Required: true, Editable: true},
			{ID: "payload", Kind: FieldJSON, PropertyName: "payload", StorageName: "payload", Editable: true},
			{
				ID: "manufacturer", Kind: FieldManyToOne, PropertyName: "manufacturer",
				ForeignKeyPropertyName: "manufacturerId", StorageName: "manufacturer_id",
				TargetDefinitionClass: `Shopware\Core\Content\Product\Aggregate\ProductManufacturer\ProductManufacturerDefinition`,
				TargetEntityClass:     `Shopware\Core\Content\Product\Aggregate\ProductManufacturer\ProductManufacturerEntity`,
				TargetEntityName:      "product_manufacturer", ReferenceField: "id", ReferenceStorageName: "id",
				DeleteBehavior: DeleteSetNull, Editable: true,
			},
		},
		Indexes: []IndexSpec{{Name: "uniq.acme_catalog_item.name", Kind: IndexUnique, Columns: []string{"name"}}},
	})
}

func TestRenderEntityBundleAndMigrationUseOnlyUpdate(t *testing.T) {
	spec := exampleSpec()
	definition, err := RenderDefinition(spec)
	require.NoError(t, err)
	entity, err := RenderEntity(spec)
	require.NoError(t, err)
	collection, err := RenderCollection(spec)
	require.NoError(t, err)
	for _, source := range []string{definition, entity, collection} {
		require.Empty(t, php.Parse(source).Errors)
	}
	require.Contains(t, definition, "new FkField('manufacturer_id', 'manufacturerId'")
	require.Contains(t, definition, "addFlags(new SetNullOnDelete())")
	require.NotContains(t, definition, "CreatedAtField")
	require.NotContains(t, definition, "UpdatedAtField")
	require.Contains(t, entity, "protected ?ProductManufacturerEntity $manufacturer = null;")
	require.NotContains(t, entity, "$createdAt")
	require.NotContains(t, entity, "$updatedAt")

	entitySchema, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.Equal(t, Column{Name: "created_at", SQLType: "DATETIME(3)", NotNull: true, BackfillSQL: "CURRENT_TIMESTAMP(3)"}, entitySchema.Columns["created_at"])
	require.Equal(t, Column{Name: "updated_at", SQLType: "DATETIME(3)"}, entitySchema.Columns["updated_at"])
	next := EmptySchema()
	next.Entities[spec.EntityName] = entitySchema
	statements, diff, err := MigrationStatements(EmptySchema(), next, nil)
	require.NoError(t, err)
	require.True(t, diff.DatabaseChanged())
	migration := RenderMigration(`Acme\Example`, "Migration1700000000CreateCatalogItem", 1700000000, statements)
	require.Empty(t, php.Parse(migration).Errors)
	require.Contains(t, migration, "public function update(Connection $connection): void")
	require.NotContains(t, migration, "updateDestructive")
	require.Contains(t, migration, "CREATE TABLE IF NOT EXISTS `acme_catalog_item`")
	require.Contains(t, migration, "UNIQUE KEY `uniq.acme_catalog_item.name`")
}

func TestRenderTranslationBundleAndVersionAwareSchema(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example\Content\Blog`, ClassName: "Blog", EntityName: "acme_blog",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "version", Kind: FieldVersion, Editable: true},
			{ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name", Required: true, APIAware: true, SearchRanking: 500, Translated: true, Editable: true},
			{ID: "description", Kind: FieldLongText, PropertyName: "description", StorageName: "description", Translated: true, Editable: true},
		},
	})
	require.NotNil(t, spec.Translation)
	require.True(t, spec.Translation.AssociationRequired)

	parentDefinition, err := RenderDefinition(spec)
	require.NoError(t, err)
	parentEntity, err := RenderEntity(spec)
	require.NoError(t, err)
	translationDefinition, err := RenderTranslationDefinition(spec)
	require.NoError(t, err)
	translationEntity, err := RenderTranslationEntity(spec)
	require.NoError(t, err)
	translationCollection, err := RenderTranslationCollection(spec)
	require.NoError(t, err)
	for _, source := range []string{parentDefinition, parentEntity, translationDefinition, translationEntity, translationCollection} {
		require.Empty(t, php.Parse(source).Errors, source)
	}
	require.Contains(t, parentDefinition, "new TranslatedField('name')")
	require.Contains(t, parentDefinition, "new TranslationsAssociationField(BlogTranslationDefinition::class, 'acme_blog_id')")
	require.NotContains(t, parentDefinition, "new StringField('name'")
	require.Contains(t, parentEntity, "protected ?string $name = null;")
	require.Contains(t, parentEntity, "protected ?BlogTranslationCollection $translations = null;")
	require.Contains(t, translationDefinition, "extends EntityTranslationDefinition")
	require.Contains(t, translationDefinition, "return BlogDefinition::class;")
	require.Contains(t, translationDefinition, "new StringField('name', 'name')")
	require.Contains(t, translationEntity, "extends TranslationEntity")
	require.Contains(t, translationEntity, "protected string $acmeBlogId;")
	require.Contains(t, translationEntity, "protected string $acmeBlogVersionId;")

	entities, err := SchemaEntitiesFromSpec(spec)
	require.NoError(t, err)
	require.Len(t, entities, 2)
	parentSchema := entities[0]
	translationSchema := entities[1]
	require.NotContains(t, parentSchema.Columns, "name")
	require.Contains(t, translationSchema.Columns, "name")
	require.True(t, translationSchema.Columns["acme_blog_id"].PrimaryKey)
	require.True(t, translationSchema.Columns["acme_blog_version_id"].PrimaryKey)
	require.True(t, translationSchema.Columns["language_id"].PrimaryKey)
	parentFK := translationSchema.ForeignKeys["fk.acme_blog_translation.acme_blog_id"]
	require.Equal(t, []string{"acme_blog_id", "acme_blog_version_id"}, parentFK.Columns)
	require.Equal(t, []string{"id", "version_id"}, parentFK.ReferenceColumns)

	next := EmptySchema()
	for _, entity := range entities {
		next.Entities[entity.Name] = entity
	}
	statements, _, err := MigrationStatements(EmptySchema(), next, nil)
	require.NoError(t, err)
	joined := strings.Join(statements, "\n")
	require.Contains(t, joined, "CREATE TABLE IF NOT EXISTS `acme_blog_translation`")
	require.Contains(t, joined, "FOREIGN KEY (`acme_blog_id`, `acme_blog_version_id`) REFERENCES `acme_blog` (`id`, `version_id`)")
	require.Contains(t, joined, "FOREIGN KEY (`language_id`) REFERENCES `language` (`id`)")
}

func TestTranslationTableIndexesValidatePersistAndRoundTrip(t *testing.T) {
	spec := exampleSpec()
	spec.Indexes = nil
	spec.Fields[1].Translated = true
	spec.Fields = append(spec.Fields, FieldSpec{ID: "version", Kind: FieldVersion, Editable: true})
	spec.Translation = &TranslationSpec{Enabled: true, AssociationRequired: true}
	spec = CompleteSpec(spec)
	spec.Indexes = []IndexSpec{
		{Name: "idx.same_name", Kind: IndexNormal, Columns: []string{"active"}},
		{Name: "idx.same_name", Kind: IndexUnique, Columns: []string{"name", "language_id"}, Translation: true},
		{Name: "idx.translation_parent", Kind: IndexNormal, Columns: []string{"acme_catalog_item_id", "acme_catalog_item_version_id"}, Translation: true},
	}
	require.Empty(t, ValidateSpec(spec))

	entities, err := SchemaEntitiesFromSpec(spec)
	require.NoError(t, err)
	require.Len(t, entities, 2)
	schema := EmptySchema()
	for _, entity := range entities {
		schema.Entities[entity.Name] = entity
	}
	require.False(t, schema.Entities[spec.EntityName].Indexes["idx.same_name"].Unique)
	require.True(t, schema.Entities[spec.Translation.EntityName].Indexes["idx.same_name"].Unique)
	require.Equal(t, []string{"name", "language_id"}, schema.Entities[spec.Translation.EntityName].Indexes["idx.same_name"].Columns)

	statements, _, err := MigrationStatements(EmptySchema(), schema, nil)
	require.NoError(t, err)
	joined := strings.Join(statements, "\n")
	require.Contains(t, joined, "UNIQUE KEY `idx.same_name` (`name`, `language_id`)")
	require.Contains(t, joined, "KEY `idx.translation_parent` (`acme_catalog_item_id`, `acme_catalog_item_version_id`)")

	restored := IndexSpecsFromEntities(spec, schema)
	require.Equal(t, spec.Indexes, restored)

	disabled := spec
	disabled.Fields = append([]FieldSpec(nil), spec.Fields...)
	for index := range disabled.Fields {
		disabled.Fields[index].Translated = false
	}
	disabled.Translation = nil
	requireIssueCode(t, ValidateSpec(disabled), "entity.index.translation.missing")
}

func TestMigrationColumnRenameRestatesNewDefinitionInUpdate(t *testing.T) {
	before := EmptySchema()
	before.Entities["example"] = Entity{Name: "example", Columns: map[string]Column{
		"old": {Name: "old", SQLType: "VARCHAR(255)", NotNull: false},
	}}
	after := EmptySchema()
	after.Entities["example"] = Entity{Name: "example", Columns: map[string]Column{
		"new": {Name: "new", SQLType: "VARCHAR(500)", NotNull: true, BackfillSQL: "'renamed'"},
	}}
	statements, _, err := MigrationStatements(before, after, []Decision{{Kind: "columnRename", Entity: "example", From: "old", To: "new"}})
	require.NoError(t, err)
	joined := strings.Join(statements, "\n")
	require.Contains(t, joined, "CHANGE COLUMN `old` `new` VARCHAR(500) NULL")
	require.Contains(t, joined, "UPDATE `example` SET `new` = 'renamed' WHERE `new` IS NULL")
	require.Contains(t, joined, "MODIFY COLUMN `new` VARCHAR(500) NOT NULL")
}

func TestMigrationEntityRenamePreservesTableAndRebuildsGeneratedObjects(t *testing.T) {
	before := EmptySchema()
	before.Entities["old_table"] = Entity{
		Name: "old_table",
		Columns: map[string]Column{
			"id":        {Name: "id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true},
			"payload":   {Name: "payload", SQLType: "JSON"},
			"target_id": {Name: "target_id", SQLType: "BINARY(16)"},
		},
		Indexes: map[string]Index{
			"idx.old_table.target_id": {Name: "idx.old_table.target_id", Columns: []string{"target_id"}},
		},
		ForeignKeys: map[string]ForeignKey{
			"fk.old_table.target_id": {Name: "fk.old_table.target_id", Column: "target_id", ReferenceEntity: "target", ReferenceColumn: "id"},
		},
	}
	before.Entities["target"] = Entity{Name: "target", Columns: map[string]Column{"id": {Name: "id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true}}}
	after := before.Clone()
	entity := after.Entities["old_table"]
	delete(after.Entities, "old_table")
	entity.Name = "new_table"
	entity.Indexes = map[string]Index{
		"idx.new_table.target_id": {Name: "idx.new_table.target_id", Columns: []string{"target_id"}},
	}
	entity.ForeignKeys = map[string]ForeignKey{
		"fk.new_table.target_id": {Name: "fk.new_table.target_id", Column: "target_id", ReferenceEntity: "target", ReferenceColumn: "id"},
	}
	after.Entities["new_table"] = entity

	statements, diff, err := MigrationStatements(before, after, []Decision{{
		Kind: "entityRename", Entity: "new_table", From: "old_table", To: "new_table",
	}})
	require.NoError(t, err)
	joined := strings.Join(statements, "\n")
	require.Contains(t, joined, "RENAME TABLE `old_table` TO `new_table`;")
	require.Contains(t, joined, "ALTER TABLE `new_table` DROP CHECK `json.old_table.payload`;")
	require.Contains(t, joined, "ALTER TABLE `new_table` ADD CONSTRAINT `json.new_table.payload` CHECK")
	require.Contains(t, joined, "ALTER TABLE `new_table` DROP INDEX `idx.old_table.target_id`;")
	require.Contains(t, joined, "ALTER TABLE `new_table` ADD INDEX `idx.new_table.target_id`")
	require.NotContains(t, joined, "CREATE TABLE IF NOT EXISTS `new_table`")
	require.NotContains(t, joined, "DROP TABLE IF EXISTS `old_table`")
	require.Empty(t, diff.CreatedEntities)
	require.Empty(t, diff.RemovedEntities)
}

func TestMigrationEntityAndJSONColumnRenameDropsOriginalConstraintOnce(t *testing.T) {
	before := EmptySchema()
	before.Entities["old_table"] = Entity{Name: "old_table", Columns: map[string]Column{
		"id":      {Name: "id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true},
		"payload": {Name: "payload", SQLType: "JSON"},
	}}
	after := EmptySchema()
	after.Entities["new_table"] = Entity{Name: "new_table", Columns: map[string]Column{
		"id":      {Name: "id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true},
		"content": {Name: "content", SQLType: "JSON"},
	}}
	decisions := []Decision{
		{Kind: "entityRename", Entity: "new_table", From: "old_table", To: "new_table"},
		{Kind: "columnRename", Entity: "new_table", From: "payload", To: "content"},
	}
	statements, _, err := MigrationStatements(before, after, decisions)
	require.NoError(t, err)
	joined := strings.Join(statements, "\n")
	require.Equal(t, 1, strings.Count(joined, "DROP CHECK `json.old_table.payload`"))
	require.NotContains(t, joined, "DROP CHECK `json.new_table.payload`")
	require.Contains(t, joined, "CHANGE COLUMN `payload` `content` JSON NULL")
	require.Contains(t, joined, "ADD CONSTRAINT `json.new_table.content` CHECK")
}

func TestMigrationRequiredColumnUsesNullableBackfillSequence(t *testing.T) {
	before := EmptySchema()
	before.Entities["example"] = Entity{Name: "example", Columns: map[string]Column{
		"id": {Name: "id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true},
	}}
	after := before.Clone()
	after.Entities["example"].Columns["active"] = Column{Name: "active", SQLType: "TINYINT(1)", NotNull: true, BackfillSQL: "0"}
	statements, diff, err := MigrationStatements(before, after, nil)
	require.NoError(t, err)
	require.Empty(t, ValidateMigration(before, after, diff, nil))
	joined := strings.Join(statements, "\n")
	require.Contains(t, joined, "ADD COLUMN `active` TINYINT(1) NULL")
	require.Contains(t, joined, "UPDATE `example` SET `active` = 0 WHERE `active` IS NULL")
	require.Contains(t, joined, "MODIFY COLUMN `active` TINYINT(1) NOT NULL")
}

func TestMigrationRequiredColumnRejectsMissingBackfill(t *testing.T) {
	before := EmptySchema()
	before.Entities["example"] = Entity{Name: "example", Columns: map[string]Column{}}
	after := before.Clone()
	after.Entities["example"].Columns["name"] = Column{Name: "name", SQLType: "VARCHAR(255)", NotNull: true}
	diff := DiffSchemas(before, after)
	require.NotEmpty(t, ValidateMigration(before, after, diff, nil))
	_, _, err := MigrationStatements(before, after, nil)
	require.ErrorContains(t, err, "backfill")
}
