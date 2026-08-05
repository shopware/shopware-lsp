package definition

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestDALDefinitionNavigatesJavaScriptCriteriaPathSegments(t *testing.T) {
	index, productPath, manufacturerPath := newDALDefinitionTestIndex(t)
	provider := NewDALDefinitionProvider(index)
	source := `Criteria.equals('manufacturer.name', value)`
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/index.js",
		source,
		1,
	)
	for _, test := range []struct {
		segment string
		path    string
	}{
		{"manufacturer", productPath},
		{"name", manufacturerPath},
	} {
		t.Run(test.segment, func(t *testing.T) {
			offset := uint32(strings.Index(source, test.segment) + 1)
			locations := provider.GetDefinition(
				context.Background(),
				dalDefinitionRequest(document, offset),
			)
			require.Len(t, locations, 1)
			require.Equal(t, uriutil.FileURI(test.path), locations[0].URI)
		})
	}
}

func TestDALDefinitionRestrictsAssociationReferences(t *testing.T) {
	index, productPath, _ := newDALDefinitionTestIndex(t)
	provider := NewDALDefinitionProvider(index)
	source := `criteria.addAssociation('manufacturer')`
	document := lsp.NewTextDocument(
		"file:///project/src/Resources/app/administration/index.ts",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "manufacturer") + 1)
	locations := provider.GetDefinition(
		context.Background(),
		dalDefinitionRequest(document, offset),
	)
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(productPath), locations[0].URI)
}

func TestDALDefinitionNavigatesEntityDefinitionLookup(t *testing.T) {
	index, productPath, _ := newDALDefinitionTestIndex(t)
	for _, source := range []string{
		`Shopware.EntityDefinition.get('product')`,
		`const { EntityDefinition } = Shopware; EntityDefinition.has('product')`,
		`Shopware.Service('repositoryFactory').create('product')`,
	} {
		document := lsp.NewTextDocument(
			"file:///project/src/Resources/app/administration/index.ts",
			source,
			1,
		)
		offset := uint32(strings.LastIndex(source, "product") + 1)
		locations := NewDALDefinitionProvider(index).GetDefinition(
			context.Background(), dalDefinitionRequest(document, offset),
		)
		require.Len(t, locations, 1, source)
		require.Equal(t, uriutil.FileURI(productPath), locations[0].URI)
	}
}

func dalDefinitionRequest(
	document *lsp.TextDocument,
	offset uint32,
) *lsp.DefinitionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.DefinitionRequest{
		DefinitionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document, DocumentContent: document.Text,
			DocumentTree: document.SyntaxTree,
			LineIndex:    document.LineIndex,
			Root:         document.SyntaxTree.Root,
			Node:         document.SyntaxTree.Root.NodeAtOffset(offset),
		},
	}
}

func newDALDefinitionTestIndex(
	t *testing.T,
) (*dal.Index, string, string) {
	t.Helper()
	dalIndex, err := dal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	productPath := "/project/src/Core/Content/Product/ProductDefinition.php"
	manufacturerPath := "/project/src/Core/Content/Product/ManufacturerDefinition.php"
	for path, source := range map[string]string{
		productPath: `<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new ManyToOneAssociationField('manufacturer', 'manufacturer_id', ManufacturerDefinition::class),
        ]);
    }
}`,
		manufacturerPath: `<?php
class ManufacturerDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product_manufacturer'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new StringField('name', 'name'),
        ]);
    }
}`,
	} {
		require.NoError(t, dalIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	return dalIndex, productPath, manufacturerPath
}
