package entityschema

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateSpecRejectsInvalidAdvancedFieldSettings(t *testing.T) {
	minimum, maximum := 10, 5
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name", MaxLength: 20000, SearchRanking: -1, Editable: true},
			{ID: "position", Kind: FieldInt, PropertyName: "position", StorageName: "position", Min: &minimum, Max: &maximum, Editable: true},
			{ID: "children", Kind: FieldOneToMany, PropertyName: "children", TargetDefinitionClass: `Acme\Example\ChildDefinition`, TargetCollectionClass: `Acme\Example\ChildCollection`, ReferenceStorageName: "parent_id", SourceColumn: "id", DeleteBehavior: "invalid", Editable: true},
			{ID: "related", Kind: FieldManyToMany, PropertyName: "related", TargetDefinitionClass: `Acme\Example\ChildDefinition`, TargetCollectionClass: `Acme\Example\ChildCollection`, MappingDefinitionClass: `Acme\Example\MappingDefinition`, MappingLocalColumn: "example_id", MappingReferenceColumn: "child_id", SourceColumn: "id", ReferenceField: "id", AssociationSearchRank: math.Inf(1), Editable: true},
		},
	})

	codes := validationCodes(ValidateSpec(spec))
	require.Contains(t, codes, "entity.field.length.invalid")
	require.Contains(t, codes, "entity.field.searchRanking.invalid")
	require.Contains(t, codes, "entity.field.range.invalid")
	require.Contains(t, codes, "entity.relation.delete.invalid")
	require.Contains(t, codes, "entity.association.searchRanking.invalid")
}

func TestValidateSpecRequiresReferenceVersionForRequiredRelation(t *testing.T) {
	target := `Shopware\Core\Content\Product\ProductDefinition`
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "product-version", Kind: FieldReferenceVersion, PropertyName: "productVersionId", StorageName: "product_version_id", TargetDefinitionClass: target, TargetEntityName: "product", Editable: true},
			{ID: "product", Kind: FieldManyToOne, PropertyName: "product", ForeignKeyPropertyName: "productId", StorageName: "product_id", Required: true, DeleteBehavior: DeleteRestrict, TargetDefinitionClass: target, TargetEntityClass: `Shopware\Core\Content\Product\ProductEntity`, TargetEntityName: "product", ReferenceField: "id", ReferenceStorageName: "id", Editable: true},
		},
	})

	require.Contains(t, validationCodes(ValidateSpec(spec)), "entity.referenceVersion.required")
	spec.Fields[1].Required = true
	require.NotContains(t, validationCodes(ValidateSpec(spec)), "entity.referenceVersion.required")
}

func TestValidateSpecKeepsTranslationColumnsOutOfParentIndexes(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name", Translated: true, Editable: true},
		},
		Indexes: []IndexSpec{{Name: "idx.acme_example.name", Kind: IndexNormal, Columns: []string{"name"}}},
	})
	require.Contains(t, validationCodes(ValidateSpec(spec)), "entity.index.column.translated")
	spec.Indexes = nil
	require.Empty(t, ValidateSpec(spec))

	spec.Fields[1].Kind = FieldManyToOne
	require.Contains(t, validationCodes(ValidateSpec(spec)), "entity.translation.kind.unsupported")
}

func TestValidateSpecAcceptsLegalCoreAliasesAndAssociationShapes(t *testing.T) {
	target := `Acme\Example\TargetDefinition`
	spec := CompleteSpec(EntitySpec{
		Mode: "edit", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "icon-raw", Kind: FieldBlob, PropertyName: "iconRaw", StorageName: "icon", Raw: "new BlobField('icon', 'iconRaw')", Editable: true},
			{ID: "icon-runtime", Kind: FieldString, PropertyName: "icon", StorageName: "icon", Behavior: &FieldBehavior{Runtime: true}, Raw: "runtime icon", Editable: true},
			{ID: "runtime-config", Kind: FieldJSON, PropertyName: "fieldConfig", StorageName: "fieldConfig", Behavior: &FieldBehavior{Runtime: true}, Raw: "runtime config", Editable: true},
			{ID: "first-reference", Kind: FieldReferenceVersion, PropertyName: "firstVersionId", StorageName: "first_version_id", TargetDefinitionClass: target, TargetEntityName: "target", Raw: "first reference", Editable: true},
			{ID: "second-reference", Kind: FieldReferenceVersion, PropertyName: "secondVersionId", StorageName: "second_version_id", TargetDefinitionClass: target, TargetEntityName: "target", Raw: "second reference", Editable: true},
			{ID: "qualified-relation", Kind: FieldManyToOne, PropertyName: "qualified", StorageName: "target.id", UsesExistingColumn: true, TargetDefinitionClass: target, TargetEntityClass: `Acme\Example\TargetEntity`, TargetEntityName: "target", ReferenceField: "id", ReferenceStorageName: "id", Raw: "qualified relation", Editable: true},
			{ID: "persona-one-to-many", Kind: FieldOneToMany, PropertyName: "personaPromotions", TargetDefinitionClass: target, TargetCollectionClass: `Acme\Example\TargetCollection`, ReferenceStorageName: "rule_id", SourceColumn: "id", Raw: "one to many", Editable: true},
			{ID: "persona-many-to-many", Kind: FieldManyToMany, PropertyName: "personaPromotions", TargetDefinitionClass: target, TargetCollectionClass: `Acme\Example\TargetCollection`, MappingDefinitionClass: `Acme\Example\TargetMappingDefinition`, MappingLocalColumn: "rule_id", MappingReferenceColumn: "target_id", SourceColumn: "id", ReferenceField: "id", Raw: "many to many", Editable: true},
		},
	})

	require.Empty(t, ValidateSpec(spec))
}

func TestValidateSpecStillRejectsNewDuplicatePropertiesAndColumns(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "first", Kind: FieldBool, PropertyName: "active", StorageName: "active", Editable: true},
			{ID: "second", Kind: FieldBool, PropertyName: "active", StorageName: "active", Editable: true},
		},
	})

	codes := validationCodes(ValidateSpec(spec))
	require.Contains(t, codes, "entity.field.property.duplicate")
	require.Contains(t, codes, "entity.field.storage.duplicate")
}

func TestValidateSpecAllowsInheritedTranslationAssociationWithoutEntityHierarchy(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "edit", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name", Translated: true, Editable: true},
		},
	})
	spec.Translation.AssociationInherited = true

	require.Empty(t, ValidateSpec(spec))
}

func TestValidateSpecAllowsLeadingUnderscoresInDALStorageIdentities(t *testing.T) {
	spec := exampleSpec()
	spec.EntityName = "_acme_entity"
	spec.Fields[0].StorageName = "_id"
	spec.Translation = &TranslationSpec{
		Enabled: true, EntityName: "_acme_entity_translation",
		DefinitionClass:       `Acme\Example\AcmeEntityTranslationDefinition`,
		EntityClass:           `Acme\Example\AcmeEntityTranslationEntity`,
		CollectionClass:       `Acme\Example\AcmeEntityTranslationCollection`,
		ParentDefinitionClass: spec.DefinitionClass,
		ParentStorageName:     "_acme_entity_id",
		ParentPropertyName:    "acmeEntity",
		AssociationProperty:   "translations",
		AssociationLocalField: "_id",
	}

	issues := ValidateSpec(spec)
	require.NotContains(t, validationCodes(issues), "entity.name.invalid")
	require.NotContains(t, validationCodes(issues), "entity.field.storage.invalid")
	require.NotContains(t, validationCodes(issues), "entity.translation.name.invalid")
	require.NotContains(t, validationCodes(issues), "entity.translation.parentStorage.invalid")
	require.NotContains(t, validationCodes(issues), "entity.translation.localField.invalid")
}

func validationCodes(issues []ValidationIssue) []string {
	result := make([]string, 0, len(issues))
	for _, issue := range issues {
		result = append(result, issue.Code)
	}
	return result
}
