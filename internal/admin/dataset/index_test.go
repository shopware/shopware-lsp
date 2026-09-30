package dataset

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexPublishDataPathsAndRemoval(t *testing.T) {
	directory := t.TempDir()
	idx := openDatasetIndex(t, directory)
	path := "/project/src/Administration/Resources/app/administration/src/module/sw-order/index.js"
	source := `Shopware.ExtensionAPI.publishData({
    id: 'sw-order-detail-base__order',
    path: 'order',
    scope: this,
});
Shopware.ExtensionAPI.publishData({ id: 'sw-product-detail__product', path: 'product.manufacturer' });
Shopware.ExtensionAPI.publishData({ id: ` + "`sw-${name}`" + `, path: 'product' });
`
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))
	propertyPath, err := idx.PropertyPath("sw-order-detail-base__order")
	require.NoError(t, err)
	assert.Equal(t, "order", propertyPath)
	propertyPath, err = idx.PropertyPath("sw-product-detail__product")
	require.NoError(t, err)
	assert.Equal(t, "product.manufacturer", propertyPath)
	propertyPath, err = idx.PropertyPath("sw-missing")
	require.NoError(t, err)
	assert.Empty(t, propertyPath)

	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(
		`Shopware.ExtensionAPI.publishData({ id: 'sw-order-detail-base__order', path: 'order' });`,
	))))
	propertyPath, err = idx.PropertyPath("sw-product-detail__product")
	require.NoError(t, err)
	assert.Empty(t, propertyPath)

	require.NoError(t, idx.Close())
	reopened := openDatasetIndex(t, directory)
	propertyPath, err = reopened.PropertyPath("sw-order-detail-base__order")
	require.NoError(t, err)
	assert.Equal(t, "order", propertyPath)
}

func TestIndexVuePublishData(t *testing.T) {
	idx := openDatasetIndex(t, t.TempDir())
	source := `<script>
Shopware.ExtensionAPI.publishData({ id: 'sw-mail-template-detail__mailTemplate', path: 'mailTemplate' });
</script>`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/src/module/sw-mail-template/index.vue",
		[]byte(source),
	)))
	propertyPath, err := idx.PropertyPath("sw-mail-template-detail__mailTemplate")
	require.NoError(t, err)
	assert.Equal(t, "mailTemplate", propertyPath)
}

func openDatasetIndex(t *testing.T, directory string) *Index {
	t.Helper()
	idx, err := NewIndex(directory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	return idx
}
