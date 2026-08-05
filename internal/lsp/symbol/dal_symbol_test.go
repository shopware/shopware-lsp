package symbol

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDALWorkspaceSymbolsExposeTechnicalEntityAndFieldNames(t *testing.T) {
	dalIndex, err := dal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	path := "/project/src/Core/Content/Product/ProductDefinition.php"
	require.NoError(t, dalIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(`<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new StringField('product_number', 'productNumber'),
        ]);
    }
}`),
	)))
	provider := NewDALWorkspaceSymbolProvider(dalIndex)
	entity, err := provider.WorkspaceSymbols(context.Background(), "productdef")
	require.NoError(t, err)
	require.Len(t, entity, 1)
	assert.Equal(t, "product", entity[0].Name)
	assert.Equal(t, protocol.SymbolClass, entity[0].Kind)
	assert.Contains(t, entity[0].ContainerName, "ProductDefinition")

	fields, err := provider.WorkspaceSymbols(context.Background(), "productNumber")
	require.NoError(t, err)
	require.Len(t, fields, 1)
	assert.Equal(t, "productNumber", fields[0].Name)
	assert.Equal(t, protocol.SymbolField, fields[0].Kind)
	assert.Contains(t, fields[0].ContainerName, "product")
}
