package dataset

import (
	"testing"

	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallsReadsLiteralDatasetSelectors(t *testing.T) {
	source := `import { data as sdkData } from '@shopware-ag/meteor-admin-sdk';
import * as SDK from '@shopware-ag/meteor-admin-sdk';
data.subscribe('sw-order-detail-base__order', (order) => order, {
    selectors: ['language.name', 'deliveries.*.shippingMethod.name'],
});
data.subscribe('sw-product-detail__product', (product) => product);
data.subscribe(datasetId, (value) => value, { selectors });
sdkData.get({ id: 'sw-order-detail-base__order', selectors: ['orderNumber'] });
SDK.data.get({ id: 'sw-product-detail__product' });
response.data.get('id');
data.get({ selectors: ['name', extra] });
`
	calls := Calls(javascriptparser.Parse(source).Tree.Root)
	require.Len(t, calls, 6)

	assert.Equal(t, "subscribe", calls[0].Method)
	assert.Equal(t, "sw-order-detail-base__order", calls[0].ID)
	assert.Equal(t, SelectorsLiteral, calls[0].SelectorsMode)
	assert.Equal(t, []string{
		"language.name",
		"deliveries.*.shippingMethod.name",
	}, calls[0].Selectors)
	assert.Equal(t, "sw-order-detail-base__order", source[calls[0].IDRange.Start:calls[0].IDRange.End])

	assert.Equal(t, SelectorsAbsent, calls[1].SelectorsMode)
	assert.Equal(t, "sw-product-detail__product", calls[1].ID)

	assert.Empty(t, calls[2].ID)
	assert.Equal(t, SelectorsDynamic, calls[2].SelectorsMode)

	assert.Equal(t, "get", calls[3].Method)
	assert.Equal(t, SelectorsLiteral, calls[3].SelectorsMode)
	assert.Equal(t, []string{"orderNumber"}, calls[3].Selectors)

	assert.Equal(t, "get", calls[4].Method)
	assert.Equal(t, "sw-product-detail__product", calls[4].ID)
	assert.Equal(t, SelectorsAbsent, calls[4].SelectorsMode)

	assert.Equal(t, "get", calls[5].Method)
	assert.Empty(t, calls[5].ID)
	assert.Equal(t, SelectorsDynamic, calls[5].SelectorsMode)
}

func TestCallsIgnoresDynamicDatasetIds(t *testing.T) {
	source := "data.subscribe(`sw-${page}__order`, (value) => value, { selectors: ['name'] });"
	calls := Calls(javascriptparser.Parse(source).Tree.Root)
	require.Len(t, calls, 1)
	assert.Empty(t, calls[0].ID)
	assert.Equal(t, SelectorsLiteral, calls[0].SelectorsMode)
}
