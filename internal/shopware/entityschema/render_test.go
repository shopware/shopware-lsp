package entityschema

import (
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func exampleSpec() EntitySpec {
	return CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example\Entity\Catalog`,
		ClassName: "CatalogItem", EntityName: "acme_catalog_item",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name", Required: true, APIAware: true, MaxLength: 500, Editable: true},
			{ID: "active", Kind: FieldBool, PropertyName: "active", StorageName: "active", Required: true, Editable: true},
			{ID: "payload", Kind: FieldJSON, PropertyName: "payload", StorageName: "payload", Editable: true},
			{ID: "created", Kind: FieldCreatedAt, Editable: true},
			{ID: "updated", Kind: FieldUpdatedAt, Editable: true},
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
	require.Contains(t, definition, "new CreatedAtField()")
	require.Contains(t, definition, "new UpdatedAtField()")
	require.Contains(t, entity, "protected ?ProductManufacturerEntity $manufacturer = null;")
	require.NotContains(t, entity, "$createdAt")
	require.NotContains(t, entity, "$updatedAt")

	entitySchema, err := SchemaFromSpec(spec)
	require.NoError(t, err)
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
