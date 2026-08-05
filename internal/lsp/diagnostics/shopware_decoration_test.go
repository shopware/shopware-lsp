package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShopwareDecorationAnalyzerRequiresDecorationAbstraction(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/project/src/AbstractProductRoute.php": `<?php
namespace Shopware\Core\Content\Product\SalesChannel;
abstract class AbstractProductRoute
{
    abstract public function getDecorated(): AbstractProductRoute;
}`,
		"/project/src/ProductRoute.php": `<?php
namespace Shopware\Core\Content\Product\SalesChannel;
class ProductRoute extends AbstractProductRoute
{
    public function getDecorated(): AbstractProductRoute { return $this; }
}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	document := lsp.NewTextDocument(
		"file:///project/src/Consumer.php",
		`<?php
namespace App;
use Shopware\Core\Content\Product\SalesChannel\ProductRoute;
class Consumer
{
    public function __construct(private ProductRoute $route) {}
}`,
		1,
	)
	problems, err := NewShopwareDecorationAnalyzer(phpIndex).Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, lsp.DiagnosticID("shopware.decoration.abstraction"), problems[0].ID)
	assert.Contains(t, problems[0].Message, "AbstractProductRoute")
}
