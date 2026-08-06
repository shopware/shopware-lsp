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

func validationCodes(issues []ValidationIssue) []string {
	result := make([]string, 0, len(issues))
	for _, issue := range issues {
		result = append(result, issue.Code)
	}
	return result
}
