package entityschema

import (
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestImportDefinitionPromotesBehaviorFlagsWithoutChangingSemantics(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\CascadeDelete;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Computed;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\NoConstraint;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Runtime;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\SearchRanking;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FkField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\OneToManyAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        (new StringField('url', 'url'))->addFlags(new Runtime(['path', 'updatedAt']), new SearchRanking(SearchRanking::HIGH_SEARCH_RANKING, false)),
        (new StringField('hash', 'hash'))->addFlags(new Computed()),
        (new StringField('dynamic', 'dynamic'))->addFlags(new Runtime(\array_merge(self::BASE, ['name']))),
        (new FkField('product_id', 'productId', ProductDefinition::class))->addFlags(new NoConstraint()),
        (new OneToManyAssociationField('products', ProductDefinition::class, 'parent_id'))->addFlags(new CascadeDelete(false)),
    ]); }
}`

	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.Len(t, spec.Fields, 6)
	byProperty := make(map[string]FieldSpec, len(spec.Fields))
	for _, field := range spec.Fields {
		byProperty[field.PropertyName] = field
	}

	runtimeField := byProperty["url"]
	require.Equal(t, float64(500), runtimeField.SearchRanking)
	require.NotNil(t, runtimeField.SearchRankingTokenize)
	require.False(t, *runtimeField.SearchRankingTokenize)
	require.Equal(t, &FieldBehavior{Runtime: true, RuntimeDependencies: []string{"path", "updatedAt"}}, runtimeField.Behavior)
	require.Empty(t, runtimeField.PreservedFlags)
	require.Equal(t, &FieldBehavior{Computed: true}, byProperty["hash"].Behavior)
	require.Equal(t, `\array_merge(self::BASE, ['name'])`, byProperty["dynamic"].Behavior.RuntimeDependenciesExpression)
	require.True(t, byProperty["productId"].Behavior.NoConstraint)
	products := byProperty["products"]
	require.Equal(t, DeleteCascade, products.DeleteBehavior)
	require.NotNil(t, products.DeleteCloneRelevant)
	require.False(t, *products.DeleteCloneRelevant)

	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.NotContains(t, entity.Columns, "url")
	require.NotContains(t, entity.Columns, "dynamic")
	require.Contains(t, entity.Columns, "hash")
	require.Contains(t, entity.Columns, "product_id")
	require.Empty(t, entity.ForeignKeys, "NoConstraint keeps the column but omits the physical constraint")

	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rewritten).Errors, rewritten)
	require.Contains(t, rewritten, "new Runtime(['path', 'updatedAt'])")
	require.Contains(t, rewritten, "new SearchRanking(500, false)")
	require.Contains(t, rewritten, `new Runtime(\array_merge(self::BASE, ['name']))`)
	require.Contains(t, rewritten, "new NoConstraint()")
	require.Contains(t, rewritten, "new CascadeDelete(false)")

	entitySource, err := RenderEntity(spec)
	require.NoError(t, err)
	require.Contains(t, entitySource, "function getUrl()")
	require.NotContains(t, entitySource, "function setUrl(")
	require.Contains(t, entitySource, "function getHash()")
	require.NotContains(t, entitySource, "function setHash(")
}

func TestDeleteFlagOptionsRoundTripOnAllAssociationKinds(t *testing.T) {
	falseValue := false
	target := RelationTarget{
		DefinitionClass: `Acme\Target\TargetDefinition`, EntityClass: `Acme\Target\TargetEntity`,
		CollectionClass: `Acme\Target\TargetCollection`, EntityName: "acme_target",
	}
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Example", EntityName: "acme_example",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "one-many", Kind: FieldOneToMany, PropertyName: "targets", TargetDefinitionClass: target.DefinitionClass, TargetEntityClass: target.EntityClass, TargetCollectionClass: target.CollectionClass, TargetEntityName: target.EntityName, ReferenceStorageName: "example_id", DeleteBehavior: DeleteSetNull, DeleteEnforcedByConstraint: &falseValue, Editable: true},
			{ID: "many-many", Kind: FieldManyToMany, PropertyName: "tags", TargetDefinitionClass: target.DefinitionClass, TargetEntityClass: target.EntityClass, TargetCollectionClass: target.CollectionClass, TargetEntityName: target.EntityName, MappingDefinitionClass: `Acme\Example\ExampleTagDefinition`, MappingLocalColumn: "example_id", MappingReferenceColumn: "target_id", DeleteBehavior: DeleteCascade, DeleteCloneRelevant: &falseValue, Editable: true},
		},
	})

	definition, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(definition).Errors, definition)
	require.Contains(t, definition, "new SetNullOnDelete(false)")
	require.Contains(t, definition, "new CascadeDelete(false)")
	require.Equal(t, 1, strings.Count(definition, "new CascadeDelete(false)"))

	lookup := func(class string) (RelationTarget, bool) {
		return target, class == target.DefinitionClass
	}
	imported, err := ImportDefinition(definition, lookup)
	require.NoError(t, err)
	require.Equal(t, DeleteSetNull, imported.Fields[1].DeleteBehavior)
	require.False(t, *imported.Fields[1].DeleteEnforcedByConstraint)
	require.Equal(t, DeleteCascade, imported.Fields[2].DeleteBehavior)
	require.False(t, *imported.Fields[2].DeleteCloneRelevant)
}

func TestAssociationOnlyManyToOneDoesNotInventStorage(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Runtime;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
use Shopware\Core\System\User\UserDefinition;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        (new ManyToOneAssociationField('activeUser', 'active_user_id', UserDefinition::class, 'id', false))->addFlags(new Runtime()),
    ]); }
}`

	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.Len(t, spec.Fields, 2)
	association := spec.Fields[1]
	require.Equal(t, FieldManyToOne, association.Kind)
	require.True(t, association.UsesExistingColumn)
	require.True(t, association.AssociationBehavior.Runtime)
	require.Empty(t, ValidateSpec(spec))

	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.NotContains(t, entity.Columns, "active_user_id")
	require.Empty(t, entity.ForeignKeys)

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.NotContains(t, rendered, "new FkField('active_user_id'")
	require.Contains(t, rendered, "new ManyToOneAssociationField('activeUser', 'active_user_id'")
}
