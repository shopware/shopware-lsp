package entityschema

import (
	"reflect"
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestVersionAwareHierarchyRendersImportsAndRoundTrips(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example\Content\Node`, ClassName: "Node", EntityName: "acme_node",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "version", Kind: FieldVersion, APIAware: true, Editable: true},
			{
				ID: "hierarchy", Kind: FieldHierarchy, PropertyName: "children",
				APIAware: true, AssociationAPIAware: true, HierarchyChildrenAPIAware: true,
				HierarchyVersionAPIAware: true, Editable: true,
			},
		},
	})
	require.Empty(t, ValidateSpec(spec))
	require.Len(t, spec.Fields, 3, "the parent version is owned by the hierarchy row")
	hierarchy := spec.Fields[2]
	require.True(t, hierarchy.HierarchyVersionAware)
	require.Equal(t, spec.DefinitionClass, hierarchy.TargetDefinitionClass)
	require.Equal(t, "parent_id", hierarchy.StorageName)
	require.Equal(t, "parent", hierarchy.HierarchyParentProperty)

	definition, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(definition).Errors, definition)
	require.Contains(t, definition, "new ParentFkField(NodeDefinition::class)")
	require.Contains(t, definition, "new ReferenceVersionField(NodeDefinition::class, 'parent_version_id')")
	require.Contains(t, definition, "new ParentAssociationField(NodeDefinition::class, 'id')")
	require.Contains(t, definition, "new ChildrenAssociationField(NodeDefinition::class)")
	require.Equal(t, 5, strings.Count(definition, "new ApiAware()"))

	entitySource, err := RenderEntity(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(entitySource).Errors, entitySource)
	require.Contains(t, entitySource, "protected ?string $parentId = null;")
	require.Contains(t, entitySource, "protected ?NodeEntity $parent = null;")
	require.Contains(t, entitySource, "protected ?NodeCollection $children = null;")

	lookup := func(class string) (RelationTarget, bool) {
		if class != spec.DefinitionClass {
			return RelationTarget{}, false
		}
		return RelationTarget{
			DefinitionClass: spec.DefinitionClass, EntityClass: spec.EntityClass,
			CollectionClass: spec.CollectionClass, EntityName: spec.EntityName, VersionAware: true,
		}, true
	}
	imported, err := ImportDefinition(definition, lookup)
	require.NoError(t, err)
	require.Len(t, imported.Fields, 3)
	require.Equal(t, FieldHierarchy, imported.Fields[2].Kind)
	require.True(t, imported.Fields[2].HierarchyVersionAware)
	require.True(t, imported.Fields[2].APIAware)
	require.True(t, imported.Fields[2].AssociationAPIAware)
	require.True(t, imported.Fields[2].HierarchyChildrenAPIAware)
	require.True(t, imported.Fields[2].HierarchyVersionAPIAware)

	before, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	after, err := SchemaFromSpec(imported)
	require.NoError(t, err)
	before.BackfillFree()
	after.BackfillFree()
	require.True(t, reflect.DeepEqual(before, after), "schema mismatch\nbefore: %#v\nafter: %#v", before, after)
	require.Equal(t, Column{Name: "parent_id", SQLType: "BINARY(16)"}, before.Columns["parent_id"])
	require.Equal(t, Column{Name: "parent_version_id", SQLType: "BINARY(16)"}, before.Columns["parent_version_id"])
	fk := before.ForeignKeys["fk.acme_node.parent_id"]
	require.Equal(t, []string{"parent_id", "parent_version_id"}, fk.Columns)
	require.Equal(t, []string{"id", "version_id"}, fk.ReferenceColumns)
	require.Equal(t, DeleteCascade, fk.OnDelete)
	require.Equal(t, []string{"parent_id", "parent_version_id"}, before.Indexes["idx.acme_node.parent_id"].Columns)
	require.Empty(t, IndexSpecsFromEntity(spec, before))
}

func TestUnversionedHierarchyOmitsParentVersion(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", Namespace: `Acme\Example`, ClassName: "Tree", EntityName: "acme_tree",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "hierarchy", Kind: FieldHierarchy, PropertyName: "queries", Editable: true},
		},
	})
	definition, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Contains(t, definition, "new ChildrenAssociationField(TreeDefinition::class, 'queries')")
	require.NotContains(t, definition, "parent_version_id")
	entity, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.NotContains(t, entity.Columns, "parent_version_id")
	require.Equal(t, []string(nil), entity.ForeignKeys["fk.acme_tree.parent_id"].Columns)
}

func TestHierarchyValidationRejectsDuplicateAndNonEntityOwnership(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionMapping,
		Namespace: `Acme\Example`, ClassName: "Tree", EntityName: "acme_tree",
		Fields: []FieldSpec{
			{ID: "first", Kind: FieldHierarchy, Editable: true},
			{ID: "second", Kind: FieldHierarchy, Editable: true},
		},
	})
	codes := make(map[string]bool)
	for _, issue := range ValidateSpec(spec) {
		codes[issue.Code] = true
	}
	require.True(t, codes["entity.hierarchy.owner.unsupported"])
	require.True(t, codes["entity.hierarchy.duplicate"])
}
