package entityschema

import (
	"reflect"
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestInheritanceAwareDefinitionRendersImportsAndRoundTrips(t *testing.T) {
	target := RelationTarget{
		DefinitionClass: `Acme\Catalog\ManufacturerDefinition`,
		EntityClass:     `Acme\Catalog\ManufacturerEntity`,
		CollectionClass: `Acme\Catalog\ManufacturerCollection`,
		EntityName:      "acme_manufacturer",
		Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
	}
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Catalog`, ClassName: "Product", EntityName: "acme_product",
		InheritanceAware: true,
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "hierarchy", Kind: FieldHierarchy, PropertyName: "variants", Editable: true},
			{ID: "number", Kind: FieldString, PropertyName: "number", StorageName: "number", Inherited: true, Editable: true},
			{
				ID: "manufacturer", Kind: FieldManyToOne, PropertyName: "manufacturer",
				ForeignKeyPropertyName: "manufacturerId", StorageName: "manufacturer_id",
				TargetDefinitionClass: target.DefinitionClass, TargetEntityClass: target.EntityClass,
				TargetCollectionClass: target.CollectionClass, TargetEntityName: target.EntityName,
				ReferenceField: "id", ReferenceStorageName: "id", DeleteBehavior: DeleteSetNull,
				Inherited: true, InheritedForeignKey: "manufacturer_id",
				AssociationInherited: true, ReverseInheritedProperty: "manufacturer",
				Editable: true,
			},
		},
	})
	require.Empty(t, ValidateSpec(spec))

	definition, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(definition).Errors, definition)
	require.Contains(t, definition, "public function isInheritanceAware(): bool")
	require.Contains(t, definition, "return true;")
	require.Contains(t, definition, "new StringField('number', 'number'))->addFlags(new Inherited())")
	require.Contains(t, definition, "new FkField('manufacturer_id', 'manufacturerId', ManufacturerDefinition::class))->addFlags(new Inherited('manufacturer_id'))")
	require.Contains(t, definition, "new Inherited(), new ReverseInherited('manufacturer')")
	require.Equal(t, 1, strings.Count(definition, "use Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\Flag\\Inherited;"))
	require.Equal(t, 1, strings.Count(definition, "use Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\Flag\\ReverseInherited;"))

	lookup := func(class string) (RelationTarget, bool) {
		if class == target.DefinitionClass {
			return target, true
		}
		if class == spec.DefinitionClass {
			return RelationTarget{
				DefinitionClass: spec.DefinitionClass, EntityClass: spec.EntityClass,
				CollectionClass: spec.CollectionClass, EntityName: spec.EntityName, InheritanceAware: true,
			}, true
		}
		return RelationTarget{}, false
	}
	imported, err := ImportDefinition(definition, lookup)
	require.NoError(t, err)
	require.True(t, imported.InheritanceAware)
	number := fieldByProperty(t, imported.Fields, "number")
	require.True(t, number.Inherited)
	require.NotContains(t, number.PreservedFlags, "new Inherited()")
	manufacturer := fieldByProperty(t, imported.Fields, "manufacturer")
	require.True(t, manufacturer.Inherited)
	require.Equal(t, "manufacturer_id", manufacturer.InheritedForeignKey)
	require.True(t, manufacturer.AssociationInherited)
	require.Equal(t, "manufacturer", manufacturer.ReverseInheritedProperty)
	require.NotContains(t, manufacturer.PreservedFlags, "new Inherited()")
	require.NotContains(t, manufacturer.AssociationFlags, "new ReverseInherited('manufacturer')")

	before, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	after, err := SchemaFromSpec(imported)
	require.NoError(t, err)
	before.BackfillFree()
	after.BackfillFree()
	require.True(t, reflect.DeepEqual(before, after), "inheritance metadata changed the database schema")
}

func TestTranslatedInheritanceKeepsFacadeAndStorageFlagsSeparate(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Catalog`, ClassName: "Product", EntityName: "acme_product",
		InheritanceAware: true,
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "hierarchy", Kind: FieldHierarchy, Editable: true},
			{
				ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name",
				Required: true, Translated: true, Inherited: true, TranslationInherited: true, Editable: true,
			},
		},
	})
	parent, err := RenderDefinition(spec)
	require.NoError(t, err)
	translation, err := RenderTranslationDefinition(spec)
	require.NoError(t, err)
	require.Contains(t, parent, "new TranslatedField('name'))->addFlags(new Inherited())")
	require.Contains(t, translation, "new StringField('name', 'name'))->addFlags(new Required(), new Inherited())")

	lookup := func(class string) (RelationTarget, bool) {
		if class == spec.DefinitionClass {
			return RelationTarget{
				DefinitionClass: spec.DefinitionClass, EntityClass: spec.EntityClass,
				CollectionClass: spec.CollectionClass, EntityName: spec.EntityName, InheritanceAware: true,
			}, true
		}
		return RelationTarget{}, false
	}
	importedParent, err := ImportDefinition(parent, lookup)
	require.NoError(t, err)
	importedTranslation, err := ImportTranslationDefinition(translation, lookup)
	require.NoError(t, err)
	attached := AttachTranslation(importedParent, importedTranslation)
	name := fieldByProperty(t, attached.Fields, "name")
	require.True(t, name.Inherited, "translation-table storage flag")
	require.True(t, name.TranslationInherited, "parent TranslatedField facade flag")
}

func TestRewriteInheritanceAwareMethodIsSafeAndReversible(t *testing.T) {
	base := CompleteSpec(EntitySpec{
		Mode: "edit", Namespace: `Acme\Catalog`, ClassName: "Product", EntityName: "acme_product",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "hierarchy", Kind: FieldHierarchy, Editable: true},
		},
	})
	source, err := RenderDefinition(base)
	require.NoError(t, err)
	require.NotContains(t, source, "isInheritanceAware")

	enabled := base
	enabled.InheritanceAware = true
	rewritten, err := RewriteDefinition(source, enabled)
	require.NoError(t, err)
	require.Contains(t, rewritten, "public function isInheritanceAware(): bool")
	require.Equal(t, 1, strings.Count(rewritten, "function isInheritanceAware"))

	disabled, err := RewriteDefinition(rewritten, base)
	require.NoError(t, err)
	require.NotContains(t, disabled, "isInheritanceAware")

	customized := strings.Replace(rewritten, "    public function isInheritanceAware(): bool", "    /** Keep this contract documented. */\n    public function isInheritanceAware(): bool", 1)
	_, err = RewriteDefinition(customized, base)
	require.ErrorContains(t, err, "cannot remove customized isInheritanceAware method")
}

func TestInheritanceValidationRequiresEntityHierarchy(t *testing.T) {
	withoutHierarchy := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Catalog`, ClassName: "Product", EntityName: "acme_product",
		InheritanceAware: true,
		Fields:           []FieldSpec{{ID: "id", Kind: FieldID, Editable: true}},
	})
	requireIssueCode(t, ValidateSpec(withoutHierarchy), "entity.inheritance.hierarchy.required")

	mapping := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionMapping,
		Namespace: `Acme\Catalog`, ClassName: "ProductTag", EntityName: "acme_product_tag",
		InheritanceAware: true,
		Fields: []FieldSpec{{
			ID: "product", Kind: FieldForeignKey, PropertyName: "productId", StorageName: "product_id",
			TargetDefinitionClass: `Acme\Catalog\ProductDefinition`, TargetEntityName: "acme_product",
			Required: true, Primary: true, Editable: true,
		}},
	})
	requireIssueCode(t, ValidateSpec(mapping), "entity.inheritance.owner.unsupported")
}

func fieldByProperty(t *testing.T, fields []FieldSpec, property string) FieldSpec {
	t.Helper()
	for _, field := range fields {
		if field.PropertyName == property {
			return field
		}
	}
	require.FailNow(t, "field not found", property)
	return FieldSpec{}
}

func requireIssueCode(t *testing.T, issues []ValidationIssue, code string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Code == code {
			return
		}
	}
	require.FailNow(t, "validation issue not found", code)
}
