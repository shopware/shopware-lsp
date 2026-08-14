package entityschema

import (
	"reflect"
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestEntityExtensionFieldModificationsCoverEveryTypedFlag(t *testing.T) {
	falseValue := false
	trueValue := true
	strict := true
	flags := []FieldFlagSpec{
		{Kind: FlagRequired},
		{Kind: FlagPrimaryKey},
		{Kind: FlagAPIAware, APISources: []string{`Shopware\Core\Framework\Api\Context\AdminApiSource`}},
		{Kind: FlagSearchRanking, SearchRanking: 500, SearchTokenize: &falseValue},
		{Kind: FlagCascadeDelete, CloneRelevant: &falseValue},
		{Kind: FlagSetNullOnDelete, EnforcedByConstraint: &trueValue},
		{Kind: FlagRestrictDelete},
		{Kind: FlagRuntime, RuntimeDependencies: []string{"name", "updatedAt"}},
		{Kind: FlagComputed},
		{Kind: FlagNoConstraint},
		{Kind: FlagInherited, InheritedForeignKey: "parent_id"},
		{Kind: FlagReverseInherited, ReverseProperty: "children"},
		{Kind: FlagWriteProtected, WriteScopes: []string{"system", "user"}},
		{Kind: FlagAllowHTML, AllowHTMLSanitized: &falseValue},
		{Kind: FlagAllowEmptyString},
		{Kind: FlagAsArray},
		{Kind: FlagImmutable},
		{Kind: FlagSince, Since: "6.7.0.0"},
		{Kind: FlagDeprecated, Deprecated: &Deprecation{DeprecatedSince: "6.7.0.0", WillBeRemovedIn: "6.8.0.0", ReplacedBy: "replacement"}},
		{Kind: FlagIgnoreInOpenAPISchema},
		{Kind: FlagIgnoreInUnusedMediaSearch},
		{Kind: FlagAPICriteriaAware},
		{Kind: FlagRuleAreas, RuleAreas: []string{"product", "order"}},
		{Kind: FlagChoice, Choice: &ChoiceSpec{Values: []string{"'one'", "Example::TWO"}, Strict: &strict}},
		{Kind: FlagDoNotUseContext},
		{Kind: FlagExtension},
	}
	remove := fieldFlagKinds()
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionExtension,
		Namespace: `Acme\Example`, ClassName: "Product", EntityName: "product",
		ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		FieldModifications: []FieldModificationSpec{
			{ID: "name-flags", PropertyName: "name", AddFlags: flags},
			{ID: "description-flags", PropertyName: "description", RemoveFlags: remove},
		},
	})
	require.Empty(t, ValidateSpec(spec))

	source, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(source).Errors, source)
	require.Contains(t, source, "$collection->get('name')?->addFlags(")
	require.Equal(t, len(remove), strings.Count(source, "$collection->get('description')?->removeFlag("))

	lookup := func(class string) (RelationTarget, bool) {
		return RelationTarget{DefinitionClass: class, EntityName: "product"}, true
	}
	imported, err := ImportExtension(source, lookup)
	require.NoError(t, err)
	require.Empty(t, imported.ModifyFieldsMethodRaw)
	for index := range spec.FieldModifications {
		spec.FieldModifications[index].ID = ""
		imported.FieldModifications[index].ID = ""
	}
	require.True(t, reflect.DeepEqual(spec.FieldModifications, imported.FieldModifications), "before: %#v\nafter: %#v", spec.FieldModifications, imported.FieldModifications)
}

func TestEntityExtensionCustomModifyFieldsIsLockedAndPreserved(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
final class ProductExtension extends EntityExtension
{
    public function modifyFields(FieldCollection $collection): void
    {
        foreach ($collection as $field) {
            $this->modify($field);
        }
    }

    public function getEntityName(): string
    {
        return ProductDefinition::ENTITY_NAME;
    }
}`
	lookup := func(class string) (RelationTarget, bool) {
		return RelationTarget{DefinitionClass: class, EntityName: "product"}, true
	}
	spec, err := ImportExtension(source, lookup)
	require.NoError(t, err)
	require.Contains(t, spec.ModifyFieldsMethodRaw, "$this->modify($field)")
	spec.Fields = append(spec.Fields, FieldSpec{ID: "runtime", Kind: FieldString, PropertyName: "runtime", StorageName: "runtime", Behavior: &FieldBehavior{Runtime: true}, Editable: true})
	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.Contains(t, rewritten, "$this->modify($field)")
}

func TestEntityExtensionFieldModificationValidation(t *testing.T) {
	base := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionExtension,
		Namespace: `Acme\Example`, ClassName: "Product", EntityName: "product",
		ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
	})
	base.FieldModifications = []FieldModificationSpec{{
		ID: "invalid", PropertyName: "bad-name",
		AddFlags:    []FieldFlagSpec{{Kind: FlagReverseInherited}},
		RemoveFlags: []FieldFlagKind{FlagReverseInherited},
	}}
	codes := validationCodes(ValidateSpec(base))
	require.Contains(t, codes, "entity.extension.modifyFields.property.invalid")
	require.Contains(t, codes, "entity.extension.modifyFields.reverseInherited.invalid")
	require.Contains(t, codes, "entity.extension.modifyFields.flag.conflict")
}
