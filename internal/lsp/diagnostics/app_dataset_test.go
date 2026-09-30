package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func newAppDatasetTestIndexes(
	t *testing.T,
	manifest string,
) (*extension.ExtensionIndexer, *shopwaredal.Index, string) {
	t.Helper()
	cache := t.TempDir()
	extensions, err := extension.NewExtensionIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, extensions.Close()) })
	dalIndex, err := shopwaredal.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })

	for path, source := range map[string]string{
		"/project/src/ProductDefinition.php": `<?php
class ProductDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        new StringField('name', 'name'),
        new FkField('manufacturer_id', 'manufacturerId', ManufacturerDefinition::class),
        new ManyToOneAssociationField('manufacturer', 'manufacturer_id', ManufacturerDefinition::class),
        new OneToManyAssociationField('variants', ProductDefinition::class, 'parent_id'),
    ]); }
}`,
		"/project/src/ManufacturerDefinition.php": `<?php
class ManufacturerDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'product_manufacturer'; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        new StringField('name', 'name'),
    ]); }
}`,
		"/project/src/OrderDefinition.php": `<?php
class OrderDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'order'; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        new FkField('language_id', 'languageId', LanguageDefinition::class),
        new ManyToOneAssociationField('language', 'language_id', LanguageDefinition::class),
    ]); }
}`,
		"/project/src/LanguageDefinition.php": `<?php
class LanguageDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'language'; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        new StringField('name', 'name'),
    ]); }
}`,
		"/project/src/SalesChannelDefinition.php": `<?php
class SalesChannelDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'sales_channel'; }
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        new StringField('name', 'name'),
    ]); }
}`,
	} {
		require.NoError(t, dalIndex.Index(indexer.NewParsedFile(path, []byte(source))))
	}

	appRoot := filepath.Join(cache, "AcmeApp")
	manifestPath := filepath.Join(appRoot, "manifest.xml")
	require.NoError(t, extensions.Index(indexer.NewParsedFile(manifestPath, []byte(manifest))))
	return extensions, dalIndex, appRoot
}

func appDatasetDocument(appRoot, name, source string) *lsp.TextDocument {
	scriptPath := filepath.Join(appRoot, "Resources", "app", "administration", "src", name)
	return lsp.NewTextDocument(uriutil.FileURI(scriptPath), source, 1)
}

func TestAppDatasetAnalyzerReportsMissingManufacturerPermission(t *testing.T) {
	manifest := `<manifest><meta><name>AcmeApp</name></meta><permissions><read>product</read></permissions></manifest>`
	extensions, dalIndex, appRoot := newAppDatasetTestIndexes(t, manifest)
	source := `import { data } from '@shopware-ag/meteor-admin-sdk';
data.subscribe('sw-product-detail__product', () => {}, { selectors: ['name', 'manufacturer.name'] });`
	document := appDatasetDocument(appRoot, "main.js", source)
	problems, err := NewAppDatasetAnalyzer(extensions, dalIndex).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, lsp.DiagnosticID("app_dataset.permission-missing"), problems[0].ID)
	require.Contains(t, problems[0].Message, "product_manufacturer")
	require.Contains(t, problems[0].Message, "sw-product-detail__product")
	require.Equal(t, "product_manufacturer", problems[0].Payload.(map[string]any)["entity"])
}

func TestAppDatasetAnalyzerPassesWhenAllPermissionsPresent(t *testing.T) {
	manifest := `<manifest><meta><name>AcmeApp</name></meta><permissions><read>product</read><read>product_manufacturer</read></permissions></manifest>`
	extensions, dalIndex, appRoot := newAppDatasetTestIndexes(t, manifest)
	source := `data.subscribe('sw-product-detail__product', () => {}, { selectors: ['name', 'manufacturer.name'] });`
	document := appDatasetDocument(appRoot, "main.js", source)
	problems, err := NewAppDatasetAnalyzer(extensions, dalIndex).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Empty(t, problems)
}

func TestAppDatasetAnalyzerWarnsWithoutSelectors(t *testing.T) {
	manifest := `<manifest><meta><name>AcmeApp</name></meta><permissions><read>product</read></permissions></manifest>`
	extensions, dalIndex, appRoot := newAppDatasetTestIndexes(t, manifest)
	source := `data.subscribe('sw-product-detail__product', () => {});`
	document := appDatasetDocument(appRoot, "main.js", source)
	problems, err := NewAppDatasetAnalyzer(extensions, dalIndex).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, lsp.DiagnosticID("app_dataset.subscribe-without-selectors"), problems[0].ID)
}

func TestAppDatasetAnalyzerSkipsDynamicCalls(t *testing.T) {
	manifest := `<manifest><meta><name>AcmeApp</name></meta><permissions><read>product</read></permissions></manifest>`
	extensions, dalIndex, appRoot := newAppDatasetTestIndexes(t, manifest)
	source := `data.subscribe(datasetId, () => {}, { selectors: ['name'] });
data.subscribe('sw-product-detail__product', () => {}, { selectors: dynamicSelectors });
data.subscribe('sw-product-detail__product', () => {}, { selectors: ['manufacturer.' + field] });`
	document := appDatasetDocument(appRoot, "main.js", source)
	problems, err := NewAppDatasetAnalyzer(extensions, dalIndex).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Empty(t, problems)
}

func TestAppDatasetAnalyzerSupportsDataGetObjectForm(t *testing.T) {
	manifest := `<manifest><meta><name>AcmeApp</name></meta><permissions><read>order</read></permissions></manifest>`
	extensions, dalIndex, appRoot := newAppDatasetTestIndexes(t, manifest)
	source := `const order = await data.get({ id: 'sw-order-detail-base__order', selectors: ['language.name'] });`
	document := appDatasetDocument(appRoot, "main.js", source)
	problems, err := NewAppDatasetAnalyzer(extensions, dalIndex).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, lsp.DiagnosticID("app_dataset.permission-missing"), problems[0].ID)
	require.Equal(t, "language", problems[0].Payload.(map[string]any)["entity"])
}

func TestAppDatasetAnalyzerSkipsUnknownDatasets(t *testing.T) {
	manifest := `<manifest><meta><name>AcmeApp</name></meta><permissions><read>product</read></permissions></manifest>`
	extensions, dalIndex, appRoot := newAppDatasetTestIndexes(t, manifest)
	for _, source := range []string{
		`data.subscribe('product-detail', () => {}, { selectors: ['name'] });`,
		`data.subscribe('sw-unknown__missing_entity', () => {}, { selectors: ['name'] });`,
		`data.subscribe('sw-product-detail__product', () => {}, { selectors: ['unknownField.name'] });`,
		`data.subscribe('sw-product-detail__product', () => {}, { selectors: ['variants.*.name'] });`,
	} {
		document := appDatasetDocument(appRoot, "main.js", source)
		problems, err := NewAppDatasetAnalyzer(extensions, dalIndex).Analyze(context.Background(), document)
		require.NoError(t, err)
		require.Empty(t, problems, "source: %s", source)
	}
}

func TestAppDatasetAnalyzerResolvesCamelCaseDatasetSuffix(t *testing.T) {
	manifest := `<manifest><meta><name>AcmeApp</name></meta><permissions><read>order</read></permissions></manifest>`
	extensions, dalIndex, appRoot := newAppDatasetTestIndexes(t, manifest)
	source := `data.subscribe('sw-sales-channel-detail__salesChannel', () => {}, { selectors: ['name'] });`
	document := appDatasetDocument(appRoot, "main.js", source)
	problems, err := NewAppDatasetAnalyzer(extensions, dalIndex).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, "sales_channel", problems[0].Payload.(map[string]any)["entity"])
}

func TestDatasetRootEntityNormalization(t *testing.T) {
	for dataset, expected := range map[string]string{
		"sw-product-detail__product":              "product",
		"sw-order-detail-base__order":             "order",
		"sw-sales-channel-detail__salesChannel":   "sales_channel",
		"sw-product-detail__product_manufacturer": "product_manufacturer",
	} {
		entity, ok := datasetRootEntity(dataset)
		require.True(t, ok, dataset)
		require.Equal(t, expected, entity, dataset)
	}
	_, ok := datasetRootEntity("product-detail")
	require.False(t, ok)
}
