package dal

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/require"
)

func TestIndexEntityDefinitionFieldsAndAssociations(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
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

    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new VersionField(),
            new FkField('manufacturer_id', 'manufacturerId', ManufacturerDefinition::class),
            new ManyToOneAssociationField('manufacturer', 'manufacturer_id', ManufacturerDefinition::class),
        ]);
    }
}`
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))

	definitions, err := idx.Definition("product")
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "ProductDefinition", definitions[0].Class)
	require.True(t, definitions[0].VersionAware)
	require.Len(t, definitions[0].Fields, 4)
	require.True(t, definitions[0].Fields[0].Primary)
	require.Equal(t, "versionId", definitions[0].Fields[1].Name)
	require.Equal(t, "version_id", definitions[0].Fields[1].StorageName)
	require.True(t, definitions[0].Fields[1].Primary)
	require.Equal(t, "manufacturerId", definitions[0].Fields[2].Name)
	require.Equal(t, "manufacturer_id", definitions[0].Fields[2].StorageName)
	require.Equal(t, "manufacturer", definitions[0].Fields[3].Name)
	require.True(t, definitions[0].Fields[3].Association)
	fields, err := idx.FieldDefinitions("manufacturer", true)
	require.NoError(t, err)
	require.Len(t, fields, 1)
	require.Equal(t, "product", fields[0].Entity)
	require.Equal(t, "ManyToOneAssociationField", fields[0].Field.Type)
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
