package dal

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/require"
)

func TestIndexEntityDefinitionFieldsAndAssociations(t *testing.T) {
	directory := t.TempDir()
	idx, err := NewIndex(directory)
	require.NoError(t, err)
	defer func() { _ = idx.Close() }()

	path := "/project/src/Core/Content/Product/ProductDefinition.php"
	source := `<?php
class ProductDefinition extends EntityDefinition
{
    public const ENTITY_NAME = 'product';

    public function getEntityName(): string
    {
        return self::ENTITY_NAME;
    }

    public function isInheritanceAware(): bool
    {
        return true;
    }

    protected function defineFields(): FieldCollection
    {
		return new FieldCollection([
			new IdField('id', 'id'),
			new VersionField(),
			new FkField('manufacturer_id', 'manufacturerId', ManufacturerDefinition::class),
			new ManyToOneAssociationField('manufacturer', 'manufacturer_id', ManufacturerDefinition::class),
			new TranslatedField('name'),
			(new StringField('runtime_label', 'runtimeLabel'))->addFlags(new Runtime()),
		]);
    }
}`
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))

	definitions, err := idx.Definition("product")
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "ProductDefinition", definitions[0].Class)
	require.Equal(t, "entity", definitions[0].Kind)
	require.True(t, definitions[0].VersionAware)
	require.True(t, definitions[0].InheritanceAware)
	require.Len(t, definitions[0].Fields, 6)
	require.True(t, definitions[0].Fields[0].Primary)
	require.True(t, definitions[0].Fields[0].Stored)
	require.Equal(t, "versionId", definitions[0].Fields[1].Name)
	require.Equal(t, "version_id", definitions[0].Fields[1].StorageName)
	require.True(t, definitions[0].Fields[1].Primary)
	require.Equal(t, "manufacturerId", definitions[0].Fields[2].Name)
	require.Equal(t, "manufacturer_id", definitions[0].Fields[2].StorageName)
	require.Equal(t, "manufacturer", definitions[0].Fields[3].Name)
	require.True(t, definitions[0].Fields[3].Association)
	require.False(t, definitions[0].Fields[3].Stored)
	require.False(t, definitions[0].Fields[4].Stored)
	require.False(t, definitions[0].Fields[5].Stored)
	fields, err := idx.FieldDefinitions("manufacturer", true)
	require.NoError(t, err)
	require.Len(t, fields, 1)
	require.Equal(t, "product", fields[0].Entity)
	require.Equal(t, "ManyToOneAssociationField", fields[0].Field.Type)

	require.NoError(t, idx.Close())
	idx, err = NewIndex(directory)
	require.NoError(t, err)
	restarted, err := idx.Definition("product")
	require.NoError(t, err)
	require.Len(t, restarted, 1)
	require.Equal(t, "entity", restarted[0].Kind)
	require.True(t, restarted[0].InheritanceAware)
}

func TestIndexLiteralEntityName(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = idx.Close() }()

	source := `<?php
final class AcmeDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'acme'; }
    protected function defineFields(): FieldCollection { return new FieldCollection([]); }
}`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/src/AcmeDefinition.php",
		[]byte(source),
	)))
	definitions, err := idx.Definition("acme")
	require.NoError(t, err)
	require.Len(t, definitions, 1)
}

func TestIndexExplicitVersionAwarenessOverridesFieldInference(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	for path, source := range map[string]string{
		"/project/src/VersionDisabledDefinition.php": `<?php
class VersionDisabledDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'version_disabled';
    public function isVersionAware(): bool { return false; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new VersionField()]); }
}`,
		"/project/src/MappingDefinition.php": `<?php
class MappingDefinition extends MappingEntityDefinition {
    public const ENTITY_NAME = 'version_mapping';
    public function isVersionAware(): bool { return true; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new ReferenceVersionField(ProductDefinition::class)]); }
}`,
	} {
		require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	disabled, err := idx.Definition("version_disabled")
	require.NoError(t, err)
	require.Len(t, disabled, 1)
	require.False(t, disabled[0].VersionAware)
	mapping, err := idx.Definition("version_mapping")
	require.NoError(t, err)
	require.Len(t, mapping, 1)
	require.True(t, mapping[0].VersionAware)
}

func TestIndexKeepsCustomBaseCandidateOutOfOrdinaryDefinitions(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	source := `<?php
namespace Acme;
class CatalogModel extends AbstractRecord {
    public const ENTITY_NAME = 'acme_catalog';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`
	require.NoError(t, idx.Index(indexer.NewParsedFile("/project/src/CatalogModel.php", []byte(source))))
	definitions, err := idx.Definitions()
	require.NoError(t, err)
	require.Empty(t, definitions)
	candidates, err := idx.UnresolvedDefinitions()
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, DefinitionKindUnresolved, candidates[0].Kind)
	require.Equal(t, `Acme\CatalogModel`, candidates[0].FullyQualifiedClass)
}

func TestIndexEntityHierarchyFields(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = idx.Close() }()

	source := `<?php
namespace Acme\Content\Node;
class NodeDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_node';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        new ParentFkField(self::class),
        new ParentAssociationField(self::class, 'id'),
        new ChildrenAssociationField(self::class, 'queries'),
    ]); }
}`
	require.NoError(t, idx.Index(indexer.NewParsedFile("/project/src/NodeDefinition.php", []byte(source))))
	definitions, err := idx.Definition("acme_node")
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Len(t, definitions[0].Fields, 4)
	require.Equal(t, "parentId", definitions[0].Fields[1].Name)
	require.Equal(t, "parent_id", definitions[0].Fields[1].StorageName)
	require.Equal(t, `Acme\Content\Node\NodeDefinition`, definitions[0].Fields[1].TargetClass)
	require.Equal(t, "parent", definitions[0].Fields[2].Name)
	require.True(t, definitions[0].Fields[2].Association)
	require.Equal(t, "queries", definitions[0].Fields[3].Name)
	require.True(t, definitions[0].Fields[3].Association)
}
