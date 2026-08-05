package diagnostics

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDALEntityAnalyzerReportsOnlyCloseStaticMisspellings(t *testing.T) {
	dalIndex := newDALEntityDiagnosticIndex(t)
	source := []byte(`
Shopware.Service('repositoryFactory').create('prodcut');
Shopware.EntityDefinition.get('runtime_custom_entity');
Shopware.EntityDefinition.has('prodcut');
Shopware.EntityDefinition.get(dynamicName);`)
	problems, err := NewDALEntityAnalyzer(dalIndex).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/src/Resources/app/administration/consumer.ts",
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	problem := problems[0]
	assert.Equal(t, "shopware.dal.entity-not-found", string(problem.ID))
	assert.Equal(t, "prodcut", string(source[problem.Range.Start:problem.Range.End]))
	assert.Equal(
		t,
		[]string{"product"},
		problem.Payload.(map[string]any)["suggestions"],
	)
}

func TestDALEntityAnalyzerHandlesEmbeddedVueScript(t *testing.T) {
	dalIndex := newDALEntityDiagnosticIndex(t)
	source := `<template><div title="prodcut" /></template>
<script setup lang="ts">
const definition = Shopware.EntityDefinition.get('prodcut');
</script>`
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/Card.vue", source, 1,
	)
	problems, err := NewDALEntityAnalyzer(dalIndex).Analyze(
		context.Background(), document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(
		t,
		strings.LastIndex(source, "prodcut"),
		int(problems[0].Range.Start),
	)
}

func newDALEntityDiagnosticIndex(t *testing.T) *dal.Index {
	t.Helper()
	dalIndex, err := dal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	require.NoError(t, dalIndex.Index(indexer.NewParsedFile(
		"/project/src/Core/Content/Product/ProductDefinition.php",
		[]byte(`<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([new IdField('id', 'id')]);
    }
}`),
	)))
	return dalIndex
}
