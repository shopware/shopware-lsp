package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin/dataset"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminDatasetAnalyzerReportsMissingSelectorPrivileges(t *testing.T) {
	analyzer, appRoot := newAdminDatasetAnalyzer(t, `
    <read>currency</read>
    <crud>product</crud>`)
	script := filepath.Join(appRoot, "Resources", "app", "administration", "src", "main.js")
	source := `
import { data as sdkData } from '@shopware-ag/meteor-admin-sdk';
data.subscribe('sw-order-detail-base__order', (order) => order, {
    selectors: ['language.name'],
});
data.subscribe('sw-order-detail-base__order', (order) => order);
data.get({ id: 'sw-order-detail-base__order' });
sdkData.get({
    id: 'sw-order-detail-base__order',
    selectors: ['orderNumber'],
});
data.subscribe('sw-product-detail__product', (product) => product, {
    selectors: ['name'],
});
data.subscribe('sw-product-detail__manufacturer', (manufacturer) => manufacturer, {
    selectors: ['name'],
});
data.subscribe('sw-sales-channel-detail__salesChannel', (channel) => channel, {
    selectors: ['name'],
});
data.subscribe('sw-order-detail-base__order', (order) => order, {
    selectors: ['deliveries.*.shippingMethod.name'],
});
data.subscribe('sw-order-detail-base__order', (order) => order, {
    selectors: ['customFields.foo'],
});
data.subscribe('sw-order-detail-base__order', (order) => order, { selectors: [] });
data.subscribe(datasetId, (value) => value, { selectors: ['name'] });
data.subscribe('sw-order-detail-base__order', (order) => order, { selectors });
data.subscribe('sw-dashboard-detail__todayOrderData', (value) => value, {
    selectors: ['value'],
});
response.data.get('id');
`
	problems := analyzeDataset(t, analyzer, script, source)
	require.Len(t, problems, 9)
	assert.Equal(t, lsp.DiagnosticID(adminDatasetPermissionMissing), problems[0].ID)
	assert.Equal(t, []string{"language", "order"}, problems[0].Payload.(map[string]any)["entities"])
	assert.Contains(t, problems[0].Message, "read:language, read:order")
	assert.Equal(t, "sw-order-detail-base__order", source[problems[0].Range.Start:problems[0].Range.End])

	assert.Equal(t, lsp.DiagnosticID(adminDatasetUnscoped), problems[1].ID)
	assert.Equal(t, lsp.DiagnosticID(adminDatasetPermissionMissing), problems[2].ID)
	assert.Equal(t, []string{"order"}, problems[2].Payload.(map[string]any)["entities"])
	assert.Equal(t, lsp.DiagnosticID(adminDatasetPermissionMissing), problems[3].ID)
	assert.Equal(t, []string{"product_manufacturer"}, problems[3].Payload.(map[string]any)["entities"])
	assert.Equal(t, []string{"sales_channel"}, problems[4].Payload.(map[string]any)["entities"])
	assert.Equal(t, []string{"order", "order_delivery", "shipping_method"}, problems[5].Payload.(map[string]any)["entities"])
	assert.Equal(t, lsp.DiagnosticID(adminDatasetUnresolved), problems[6].ID)
	assert.Equal(t, "dynamic-id", problems[6].Payload.(map[string]any)["reason"])
	assert.Equal(t, "dynamic-selectors", problems[7].Payload.(map[string]any)["reason"])
	assert.Equal(t, "unknown-dataset", problems[8].Payload.(map[string]any)["reason"])
}

func TestAdminDatasetAnalyzerSkipsGrantedWildcardAndNonAppSources(t *testing.T) {
	analyzer, appRoot := newAdminDatasetAnalyzer(t, `<read>*</read>`)
	script := filepath.Join(appRoot, "Resources", "app", "administration", "src", "main.js")
	problems := analyzeDataset(t, analyzer, script, `
data.subscribe('sw-order-detail-base__order', (order) => order, {
    selectors: ['language.name'],
});`)
	assert.Empty(t, problems)

	storefront := filepath.Join(appRoot, "Resources", "views", "storefront", "main.js")
	problems = analyzeDataset(t, analyzer, storefront, `
data.subscribe('sw-order-detail-base__order', (order) => order);`)
	assert.Empty(t, problems)

	outside := filepath.Join(t.TempDir(), "plugin", "src", "Resources", "app", "administration", "src", "main.js")
	problems = analyzeDataset(t, analyzer, outside, `
data.subscribe('sw-order-detail-base__order', (order) => order, {
    selectors: ['language.name'],
});`)
	assert.Empty(t, problems)
}

func TestAdminDatasetAnalyzerReadsVueScript(t *testing.T) {
	analyzer, appRoot := newAdminDatasetAnalyzer(t, `<read>currency</read>`)
	script := filepath.Join(appRoot, "Resources", "app", "administration", "src", "order.vue")
	problems := analyzeDataset(t, analyzer, script, `<template><div /></template>
<script>
data.get({
    id: 'sw-order-detail-base__order',
    selectors: ['language.name'],
});
</script>`)
	require.Len(t, problems, 1)
	assert.Equal(t, lsp.DiagnosticID(adminDatasetPermissionMissing), problems[0].ID)
	assert.Equal(t, "data.get", problems[0].Payload.(map[string]any)["call"])
}

func newAdminDatasetAnalyzer(
	t *testing.T,
	permissions string,
) (*AdminDatasetAnalyzer, string) {
	t.Helper()
	cache := t.TempDir()
	extensions, err := extension.NewExtensionIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, extensions.Close()) })
	definitions, err := dal.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, definitions.Close()) })
	datasets, err := dataset.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, datasets.Close()) })

	require.NoError(t, definitions.Index(indexer.NewParsedFile(
		"/project/src/OrderDefinition.php",
		[]byte(datasetDefinitionSource),
	)))
	require.NoError(t, datasets.Index(indexer.NewParsedFile(
		"/project/src/Administration/order-detail.js",
		[]byte(`Shopware.ExtensionAPI.publishData({
    id: 'sw-order-detail-base__order',
    path: 'order',
});
Shopware.ExtensionAPI.publishData({
    id: 'sw-product-detail__product',
    path: 'product',
});
Shopware.ExtensionAPI.publishData({
    id: 'sw-product-detail__manufacturer',
    path: 'product.manufacturer',
});`),
	)))

	appRoot := filepath.Join(cache, "DemoApp")
	manifest := `<manifest>
    <meta><name>DemoApp</name></meta>
    <permissions>` + permissions + `</permissions>
</manifest>`
	require.NoError(t, extensions.Index(indexer.NewParsedFile(
		filepath.Join(appRoot, "manifest.xml"),
		[]byte(manifest),
	)))
	return NewAdminDatasetAnalyzer(datasets, definitions, extensions), appRoot
}

func analyzeDataset(
	t *testing.T,
	analyzer *AdminDatasetAnalyzer,
	path, source string,
) []lsp.Problem {
	t.Helper()
	problems, err := analyzer.Analyze(
		context.Background(),
		lsp.NewTextDocument(uriutil.FileURI(path), source, 1),
	)
	require.NoError(t, err)
	return problems
}

const datasetDefinitionSource = `<?php
class OrderDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'order'; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([
            new StringField('order_number', 'orderNumber'),
            new JsonField('custom_fields', 'customFields'),
            new ManyToOneAssociationField('language', 'language_id', LanguageDefinition::class),
            new OneToManyAssociationField('deliveries', OrderDeliveryDefinition::class, 'order_id'),
        ]);
    }
}
class LanguageDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'language'; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([new StringField('name', 'name')]);
    }
}
class OrderDeliveryDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'order_delivery'; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([
            new ManyToOneAssociationField('shippingMethod', 'shipping_method_id', ShippingMethodDefinition::class),
        ]);
    }
}
class ShippingMethodDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'shipping_method'; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([new StringField('name', 'name')]);
    }
}
class ProductDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([
            new StringField('name', 'name'),
            new ManyToOneAssociationField('manufacturer', 'product_manufacturer_id', ProductManufacturerDefinition::class),
        ]);
    }
}
class ProductManufacturerDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'product_manufacturer'; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([new StringField('name', 'name')]);
    }
}
class SalesChannelDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'sales_channel'; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([new StringField('name', 'name')]);
    }
}
`
