package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDALHoverDescribesJavaScriptCriteriaPathSegment(t *testing.T) {
	dalIndex, err := dal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	require.NoError(t, dalIndex.Index(indexer.NewParsedFile(
		"/project/src/Core/Content/Product/ManufacturerDefinition.php",
		[]byte(`<?php
class ManufacturerDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product_manufacturer'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new StringField('name', 'name'),
        ]);
    }
}`),
	)))
	source := `Criteria.equals('manufacturer.name', value)`
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/index.js",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "name") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewDALHoverProvider(dalIndex).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree,
				LineIndex:    document.LineIndex,
				Root:         document.SyntaxTree.Root,
				Node:         document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Shopware DAL field")
	assert.Contains(t, result.Contents.Value, "product_manufacturer")
	assert.Contains(t, result.Contents.Value, "StringField")
}

func TestDALHoverDescribesAdministrationEntityReference(t *testing.T) {
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
	source := `Shopware.EntityDefinition.get('product')`
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/index.ts", source, 1,
	)
	offset := uint32(strings.Index(source, "product") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, hoverErr := NewDALHoverProvider(dalIndex).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Shopware DAL entity")
	assert.Contains(t, result.Contents.Value, "ProductDefinition")
	assert.Contains(t, result.Contents.Value, "Indexed fields: 1")
}
