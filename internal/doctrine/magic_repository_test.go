package doctrine

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMagicRepositoryMethodsResolveMappedFields(t *testing.T) {
	idx, phpIndex := queryBuilderFixture(t)
	source := `<?php
namespace App\Repository;
class ProductRepository {
    public function load(): void {
        $this->findOneByNameAndCategory('name', 1);
    }
}`
	path := "/project/src/Repository/ProductRepository.php"
	root := phpparser.Parse(source).Tree.Root
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	offset := uint32(strings.Index(source, "findOneByName") + 3)
	node := root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		root,
	)
	method, found := idx.MagicMethodAt(ctx, root, node)
	require.True(t, found)
	assert.Equal(t, "App\\Entity\\Product|null", method.ReturnType)
	require.Len(t, method.Fields, 2)
	assert.Equal(t, "name", method.Fields[0].Name)
	assert.Equal(t, "category", method.Fields[1].Name)
	assert.Empty(t, method.Unknown)

	completions := idx.MagicMethodCompletionsAt(ctx, root, node)
	var names []string
	for _, completion := range completions {
		names = append(names, completion.Name)
	}
	assert.Contains(t, names, "findOneByName")
	assert.Contains(t, names, "findByCategory")
	assert.Contains(t, names, "countById")
}

func TestMagicRepositoryMethodReportsUnknownCriteria(t *testing.T) {
	idx, phpIndex := queryBuilderFixture(t)
	source := `<?php
namespace App\Repository;
class ProductRepository {
    public function load(): void {
        $this->findByMissing('value');
    }
}`
	path := "/project/src/Repository/ProductRepository.php"
	root := phpparser.Parse(source).Tree.Root
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	offset := uint32(strings.Index(source, "findByMissing") + 3)
	node := root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		root,
	)
	method, found := idx.MagicMethodAt(ctx, root, node)
	require.True(t, found)
	assert.Equal(t, []string{"Missing"}, method.Unknown)
}
