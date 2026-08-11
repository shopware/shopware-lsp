package entityschema

import (
	"reflect"
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestSupportedFieldMatrixRendersImportsAndRoundTrips(t *testing.T) {
	target := RelationTarget{
		DefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		EntityClass:     `Shopware\Core\Content\Product\ProductEntity`,
		CollectionClass: `Shopware\Core\Content\Product\ProductCollection`,
		EntityName:      "product",
		Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
		VersionAware:    true,
	}
	fields := []FieldSpec{
		{ID: "id", Kind: FieldID, Editable: true},
		{ID: "auto", Kind: FieldAutoIncrement, Editable: true},
		{ID: "version", Kind: FieldVersion, APIAware: true, Editable: true},
		{ID: "reference-version", Kind: FieldReferenceVersion, PropertyName: "productVersionId", StorageName: "product_version_id", TargetDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`, TargetEntityClass: `Shopware\Core\Content\Product\ProductEntity`, TargetEntityName: "product", Required: true, Primary: true, Editable: true},
		{ID: "string", Kind: FieldString, PropertyName: "title", StorageName: "title", MaxLength: 500, Required: true, SearchRanking: 0.5, MigrationDefault: "'existing'", Editable: true},
		{ID: "long", Kind: FieldLongText, PropertyName: "description", StorageName: "description", Editable: true},
		{ID: "blob", Kind: FieldBlob, PropertyName: "payloadRaw", StorageName: "payload_raw", Editable: true},
		{ID: "int", Kind: FieldInt, PropertyName: "position", StorageName: "position", Editable: true},
		{ID: "float", Kind: FieldFloat, PropertyName: "factor", StorageName: "factor", Editable: true},
		{ID: "bool", Kind: FieldBool, PropertyName: "active", StorageName: "active", Editable: true},
		{ID: "date", Kind: FieldDate, PropertyName: "validFrom", StorageName: "valid_from", Editable: true},
		{ID: "datetime", Kind: FieldDateTime, PropertyName: "publishedAt", StorageName: "published_at", Editable: true},
		{ID: "json", Kind: FieldJSON, PropertyName: "config", StorageName: "config", Editable: true},
		{ID: "list", Kind: FieldList, PropertyName: "tags", StorageName: "tags", ElementTypeClass: `Shopware\Core\Framework\DataAbstractionLayer\Field\StringField`, Editable: true},
		{ID: "object", Kind: FieldObject, PropertyName: "metadata", StorageName: "metadata", Editable: true},
		{ID: "created", Kind: FieldCreatedAt, Editable: true},
		{ID: "updated", Kind: FieldUpdatedAt, Editable: true},
		{ID: "many-one", Kind: FieldManyToOne, PropertyName: "product", ForeignKeyPropertyName: "productId", StorageName: "product_id", TargetDefinitionClass: target.DefinitionClass, TargetEntityClass: target.EntityClass, TargetCollectionClass: target.CollectionClass, TargetEntityName: target.EntityName, AssociationAPIAware: true, Editable: true},
		{ID: "one-one", Kind: FieldOneToOne, PropertyName: "featuredProduct", ForeignKeyPropertyName: "featuredProductId", StorageName: "featured_product_id", TargetDefinitionClass: target.DefinitionClass, TargetEntityClass: target.EntityClass, TargetCollectionClass: target.CollectionClass, TargetEntityName: target.EntityName, Editable: true},
		{ID: "inverse-one", Kind: FieldOneToOne, PropertyName: "inverseProduct", StorageName: "id", ReferenceField: "extension_id", ReferenceStorageName: "extension_id", TargetDefinitionClass: target.DefinitionClass, TargetEntityClass: target.EntityClass, TargetCollectionClass: target.CollectionClass, TargetEntityName: target.EntityName, UsesExistingColumn: true, AssociationSearchRank: 0.5, Editable: true},
		{ID: "one-many", Kind: FieldOneToMany, PropertyName: "products", ReferenceStorageName: "extension_id", TargetDefinitionClass: target.DefinitionClass, TargetEntityClass: target.EntityClass, TargetCollectionClass: target.CollectionClass, TargetEntityName: target.EntityName, DeleteBehavior: DeleteCascade, Editable: true},
		{ID: "many-many", Kind: FieldManyToMany, PropertyName: "relatedProducts", TargetDefinitionClass: target.DefinitionClass, TargetEntityClass: target.EntityClass, TargetCollectionClass: target.CollectionClass, TargetEntityName: target.EntityName, MappingDefinitionClass: `Acme\Example\ExampleProductDefinition`, MappingLocalColumn: "example_id", MappingReferenceColumn: "product_id", SourceColumn: "id", ReferenceField: "id", AssociationAPIAware: true, Editable: true},
	}
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: fields, CreateMigration: true,
	})
	require.Empty(t, ValidateSpec(spec))

	definition, err := RenderDefinition(spec)
	require.NoError(t, err)
	entitySource, err := RenderEntity(spec)
	require.NoError(t, err)
	for _, source := range []string{definition, entitySource} {
		require.Empty(t, php.Parse(source).Errors, source)
	}
	require.Contains(t, definition, "new AutoIncrementField()")
	require.Contains(t, definition, "new VersionField()")
	require.Contains(t, definition, "new ReferenceVersionField(ProductDefinition::class, 'product_version_id')")
	require.Contains(t, definition, "ReferenceVersionField(ProductDefinition::class, 'product_version_id'))->addFlags(new Required(), new PrimaryKey())")
	require.Contains(t, definition, "new ListField('tags', 'tags', StringField::class)")
	require.Contains(t, definition, "new OneToOneAssociationField('inverseProduct', 'id', 'extension_id'")
	require.Contains(t, definition, "new ManyToManyAssociationField(")
	require.Equal(t, 2, strings.Count(definition, "new FkField("), "the inverse one-to-one must not create an FK field")
	require.Contains(t, entitySource, "protected ?object $payloadRaw = null;")
	require.NotContains(t, entitySource, "$versionId")
	require.NotContains(t, entitySource, "$productVersionId")

	lookup := func(class string) (RelationTarget, bool) {
		if class == target.DefinitionClass {
			return target, true
		}
		return RelationTarget{}, false
	}
	imported, err := ImportDefinition(definition, lookup)
	require.NoError(t, err)
	require.Len(t, imported.Fields, len(spec.Fields))
	for _, field := range imported.Fields {
		require.NotEqual(t, FieldLocked, field.Kind, "unexpected locked field: %s", field.Raw)
	}
	before, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	after, err := SchemaFromSpec(imported)
	require.NoError(t, err)
	before.BackfillFree()
	after.BackfillFree()
	require.True(t, reflect.DeepEqual(before, after), "schema mismatch\nbefore: %#v\nafter: %#v", before, after)
}

func TestVersionAwareRelationUsesCompositeForeignKeyAndIndex(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "product-version", Kind: FieldReferenceVersion, PropertyName: "productVersionId", StorageName: "product_version_id", TargetDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`, TargetEntityName: "product", Editable: true},
			{ID: "product", Kind: FieldManyToOne, PropertyName: "product", ForeignKeyPropertyName: "productId", StorageName: "product_id", TargetDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`, TargetEntityClass: `Shopware\Core\Content\Product\ProductEntity`, TargetEntityName: "product", Editable: true},
		},
	})
	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	fk := entity.ForeignKeys["fk.acme_example.product_id"]
	require.Equal(t, []string{"product_id", "product_version_id"}, foreignKeyColumns(fk))
	require.Equal(t, []string{"id", "version_id"}, foreignKeyReferenceColumns(fk))
	require.Equal(t, []string{"product_id", "product_version_id"}, entity.Indexes["idx.acme_example.product_id"].Columns)

	next := EmptySchema()
	next.Entities[entity.Name] = entity
	statements, _, err := MigrationStatements(EmptySchema(), next, nil)
	require.NoError(t, err)
	joined := strings.Join(statements, "\n")
	require.Contains(t, joined, "FOREIGN KEY (`product_id`, `product_version_id`) REFERENCES `product` (`id`, `version_id`)")
}

func TestMigrationMatrixCoversColumnsIndexesForeignKeysAndJSONConstraints(t *testing.T) {
	before := EmptySchema()
	before.Entities["example"] = Entity{
		Name: "example",
		Columns: map[string]Column{
			"id":      {Name: "id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true},
			"payload": {Name: "payload", SQLType: "JSON"},
			"name":    {Name: "name", SQLType: "VARCHAR(255)"},
		},
		Indexes:     map[string]Index{"idx.example.name": {Name: "idx.example.name", Columns: []string{"name"}}},
		ForeignKeys: map[string]ForeignKey{},
	}
	after := before.Clone()
	entity := after.Entities["example"]
	entity.Columns["payload"] = Column{Name: "payload", SQLType: "LONGTEXT"}
	entity.Columns["name"] = Column{Name: "name", SQLType: "VARCHAR(500)", NotNull: true, BackfillSQL: "'unknown'"}
	entity.Columns["settings"] = Column{Name: "settings", SQLType: "JSON"}
	entity.Columns["active"] = Column{Name: "active", SQLType: "TINYINT(1)", NotNull: true, BackfillSQL: "0"}
	entity.Columns["auto_increment"] = Column{Name: "auto_increment", SQLType: "BIGINT UNSIGNED", NotNull: true, AutoIncrement: true}
	entity.Columns["product_id"] = Column{Name: "product_id", SQLType: "BINARY(16)"}
	delete(entity.Indexes, "idx.example.name")
	entity.Indexes["uniq.example.name"] = Index{Name: "uniq.example.name", Unique: true, Columns: []string{"name"}}
	entity.Indexes["idx.example.product_id"] = Index{Name: "idx.example.product_id", Columns: []string{"product_id"}}
	entity.ForeignKeys["fk.example.product_id"] = ForeignKey{Name: "fk.example.product_id", Column: "product_id", ReferenceEntity: "product", ReferenceColumn: "id", OnDelete: DeleteSetNull, OnUpdate: "cascade"}
	after.Entities["example"] = entity

	statements, diff, err := MigrationStatements(before, after, nil)
	require.NoError(t, err)
	require.Empty(t, ValidateMigration(before, after, diff, nil))
	joined := strings.Join(statements, "\n")
	require.Contains(t, joined, "DROP CHECK `json.example.payload`")
	require.Contains(t, joined, "MODIFY COLUMN `payload` LONGTEXT NULL")
	require.Contains(t, joined, "UPDATE `example` SET `name` = 'unknown'")
	require.Contains(t, joined, "ADD COLUMN `settings` JSON NULL")
	require.Contains(t, joined, "ADD CONSTRAINT `json.example.settings` CHECK")
	require.Contains(t, joined, "ADD COLUMN `active` TINYINT(1) NULL")
	require.Contains(t, joined, "ADD COLUMN `auto_increment` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT UNIQUE")
	require.Contains(t, joined, "DROP INDEX `idx.example.name`")
	require.Contains(t, joined, "ADD UNIQUE INDEX `uniq.example.name`")
	require.Contains(t, joined, "ADD CONSTRAINT `fk.example.product_id` FOREIGN KEY")
	migration := RenderMigration(`Acme\Example`, "Migration1UpdateExample", 1, statements)
	require.NotContains(t, migration, "updateDestructive")
	require.Empty(t, php.Parse(migration).Errors)
}

func TestJSONColumnRenameReplacesNamedConstraint(t *testing.T) {
	before := EmptySchema()
	before.Entities["example"] = Entity{Name: "example", Columns: map[string]Column{
		"old_payload": {Name: "old_payload", SQLType: "JSON"},
	}}
	after := EmptySchema()
	after.Entities["example"] = Entity{Name: "example", Columns: map[string]Column{
		"payload": {Name: "payload", SQLType: "JSON"},
	}}
	statements, _, err := MigrationStatements(before, after, []Decision{{Kind: "columnRename", Entity: "example", From: "old_payload", To: "payload"}})
	require.NoError(t, err)
	joined := strings.Join(statements, "\n")
	require.Contains(t, joined, "DROP CHECK `json.example.old_payload`")
	require.Contains(t, joined, "CHANGE COLUMN `old_payload` `payload` JSON NULL")
	require.Contains(t, joined, "ADD CONSTRAINT `json.example.payload` CHECK")
}

func TestAddingVersionFieldBackfillsAndRebuildsCompositePrimaryKey(t *testing.T) {
	before := EmptySchema()
	before.Entities["example"] = Entity{Name: "example", Columns: map[string]Column{
		"id": {Name: "id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true},
	}}
	after := before.Clone()
	entity := after.Entities["example"]
	entity.Columns["version_id"] = Column{
		Name: "version_id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true,
		BackfillSQL: "UNHEX('0fa91ce3e96a4bc2be4bd9ce752c3425')",
	}
	after.Entities["example"] = entity

	statements, diff, err := MigrationStatements(before, after, nil)
	require.NoError(t, err)
	require.Len(t, diff.ChangedPrimaryKeys, 1)
	joined := strings.Join(statements, "\n")
	require.Contains(t, joined, "DROP PRIMARY KEY")
	require.Contains(t, joined, "ADD COLUMN `version_id` BINARY(16) NULL")
	require.Contains(t, joined, "UPDATE `example` SET `version_id` = UNHEX('0fa91ce3e96a4bc2be4bd9ce752c3425')")
	require.Contains(t, joined, "ADD PRIMARY KEY (`id`, `version_id`)")
	require.Less(t, strings.Index(joined, "DROP PRIMARY KEY"), strings.Index(joined, "ADD COLUMN `version_id`"))
	require.Less(t, strings.Index(joined, "ADD COLUMN `version_id`"), strings.Index(joined, "ADD PRIMARY KEY"))
}

// BackfillFree clears transient migration-only data before comparing the
// committed schema shape produced by a rendered/imported definition.
func (e *Entity) BackfillFree() {
	for name, column := range e.Columns {
		column.BackfillSQL = ""
		e.Columns[name] = column
	}
}
