package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/stretchr/testify/require"
)

func TestDALCompletionProvidesJavaScriptCriteriaFields(t *testing.T) {
	index := newDALCompletionTestIndex(t)
	provider := NewDALCompletionProvider(index)
	for name, test := range map[string]struct {
		source  string
		present []string
		absent  []string
	}{
		"filter field": {
			source:  `Criteria.equals('act', true)`,
			present: []string{"active", "id", "manufacturer"},
		},
		"association": {
			source:  `criteria.addAssociation('man')`,
			present: []string{"manufacturer"},
			absent:  []string{"active", "id"},
		},
		"aggregation field": {
			source:  `Criteria.max('latest', 'created')`,
			present: []string{"createdAt"},
		},
		"entity definition": {
			source:  `Shopware.EntityDefinition.get('pro')`,
			present: []string{"product"},
			absent:  []string{"active", "manufacturer"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			offset := uint32(strings.LastIndex(test.source, "'") - 1)
			if name == "filter field" || name == "association" {
				offset = uint32(strings.Index(test.source, "'") + 2)
			}
			document := lsp.NewTextDocument(
				"file:///project/src/Resources/app/administration/index.js",
				test.source,
				1,
			)
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.CompletionParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			items := provider.GetCompletions(
				context.Background(),
				&lsp.CompletionRequest{
					CompletionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree,
						LineIndex:    document.LineIndex,
						Root:         document.SyntaxTree.Root,
						Node:         document.SyntaxTree.Root.NodeAtOffset(offset),
					},
				},
			)
			labels := make([]string, 0, len(items))
			for _, item := range items {
				labels = append(labels, item.Label)
			}
			for _, expected := range test.present {
				require.Contains(t, labels, expected)
			}
			for _, unexpected := range test.absent {
				require.NotContains(t, labels, unexpected)
			}
		})
	}
}

func TestDALCompletionProvidesEntitiesInVueScript(t *testing.T) {
	index := newDALCompletionTestIndex(t)
	source := `<template><div /></template>
<script setup lang="ts">
const definition = Shopware.EntityDefinition.get('pro');
</script>`
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/Card.vue", source, 1,
	)
	offset := uint32(strings.Index(source, "'pro'") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	items := NewDALCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	require.Contains(t, labels, "product")
}

func newDALCompletionTestIndex(t *testing.T) *dal.Index {
	t.Helper()
	dalIndex, err := dal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	source := `<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new BoolField('active', 'active'),
            new DateTimeField('created_at', 'createdAt'),
            new ManyToOneAssociationField('manufacturer', 'manufacturer_id', ManufacturerDefinition::class),
        ]);
    }
}`
	require.NoError(t, dalIndex.Index(indexer.NewParsedFile(
		"/project/src/Core/Content/Product/ProductDefinition.php",
		[]byte(source),
	)))
	return dalIndex
}
