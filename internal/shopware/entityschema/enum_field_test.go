package entityschema

import (
	"strings"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestClassBasedEnumFieldRendersImportsAndRoundTrips(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Catalog`, ClassName: "Catalog", EntityName: "acme_catalog",
		ShopwareVersion: "^6.6.10",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{
				ID: "status", Kind: FieldEnum, PropertyName: "status", StorageName: "status",
				EnumClass: `Acme\Catalog\Status`, EnumCase: "Active", EnumBackingType: "string",
				Required: true, MigrationDefault: "'active'", Editable: true,
			},
		},
	})
	require.Empty(t, ValidateSpec(spec))

	definition, err := RenderDefinition(spec)
	require.NoError(t, err)
	entity, err := RenderEntity(spec)
	require.NoError(t, err)
	require.Empty(t, phpparser.Parse(definition).Errors, definition)
	require.Empty(t, phpparser.Parse(entity).Errors, entity)
	require.Contains(t, definition, `use Shopware\Core\Framework\DataAbstractionLayer\Field\EnumField;`)
	require.Contains(t, definition, `use Acme\Catalog\Status;`)
	require.Contains(t, definition, `new EnumField('status', 'status', Status::Active)`)
	require.Contains(t, entity, `protected Status $status;`)
	require.Contains(t, entity, `public function getStatus(): Status`)

	imported, err := ImportDefinition(definition, nil)
	require.NoError(t, err)
	status := fieldByProperty(t, imported.Fields, "status")
	require.NotNil(t, status)
	require.Equal(t, FieldEnum, status.Kind)
	require.Equal(t, `Acme\Catalog\Status`, status.EnumClass)
	require.Equal(t, "Active", status.EnumCase)
	require.Empty(t, status.EnumBackingType, "the constructor does not encode the enum backing type")
	for index := range imported.Fields {
		if imported.Fields[index].PropertyName == "status" {
			imported.Fields[index].EnumBackingType = "string"
		}
	}

	before, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	after, err := SchemaFromSpec(imported)
	require.NoError(t, err)
	before.BackfillFree()
	after.BackfillFree()
	require.Equal(t, before, after)
}

func TestEnumFieldSQLTypesAndValidation(t *testing.T) {
	stringType, err := SQLType(FieldSpec{Kind: FieldEnum, PropertyName: "state", EnumBackingType: "string"})
	require.NoError(t, err)
	require.Equal(t, "VARCHAR(255)", stringType)
	intType, err := SQLType(FieldSpec{Kind: FieldEnum, PropertyName: "state", EnumBackingType: "int"})
	require.NoError(t, err)
	require.Equal(t, "INT", intType)
	_, err = SQLType(FieldSpec{Kind: FieldEnum, PropertyName: "state"})
	require.ErrorContains(t, err, "has no backing type")

	base := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Catalog`, ClassName: "Catalog", EntityName: "acme_catalog",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "state", Kind: FieldEnum, PropertyName: "state", StorageName: "state", EnumClass: `Acme\Catalog\State`, EnumCase: "Ready", EnumBackingType: "int", Editable: true},
		},
	})
	base.ShopwareVersion = "6.6.9"
	requireIssueCode(t, ValidateSpec(base), "entity.field.enum.version.unsupported")
	base.ShopwareVersion = "6.6.10"
	require.NotContains(t, strings.Join(validationCodes(ValidateSpec(base)), ","), "entity.field.enum")

	base.Fields[1].EnumCase = ""
	requireIssueCode(t, ValidateSpec(base), "entity.field.enum.case.invalid")
	base.Fields[1].EnumCase = "Ready"
	base.Fields[1].EnumBackingType = ""
	requireIssueCode(t, ValidateSpec(base), "entity.field.enum.backing.invalid")
}
