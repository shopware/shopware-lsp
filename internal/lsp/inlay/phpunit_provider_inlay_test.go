package inlay

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPUnitProviderInlayHintsSupportDocblocksAndAttributes(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/phpunit/TestCase.php",
		[]byte(`<?php namespace PHPUnit\Framework; abstract class TestCase {}`),
	)))
	source := `<?php
namespace App\Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

final class ProductTest extends TestCase
{
    /** @dataProvider productCases */
    public function testProduct(string $name, int $stock): void {}

    #[DataProvider('priceCases')]
    public function testPrice(int $gross, int $net): void {}

    public static function productCases(): iterable
    {
        yield ['Hat', 4];
        yield 'empty' => ['Shoe', 0];
    }

    public static function priceCases(): iterable
    {
        yield [119, 100];
    }
}
`
	document := lsp.NewTextDocument(
		"file:///project/tests/ProductTest.php",
		source,
		1,
	)
	hints, err := NewPHPUnitProviderProvider(phpIndex).GetInlayHints(
		context.Background(),
		inlayHintRequest(document),
	)
	require.NoError(t, err)
	require.Len(t, hints, 6)
	labels := make([]string, 0, len(hints))
	for _, hint := range hints {
		parts, ok := hint.Label.([]protocol.InlayHintLabelPart)
		require.True(t, ok)
		require.Len(t, parts, 1)
		labels = append(labels, parts[0].Value)
		assert.NotNil(t, parts[0].Location)
		assert.Equal(t, protocol.InlayHintKindParameter, hint.Kind)
	}
	assert.Equal(t, []string{
		"$name:", "$stock:", "$name:", "$stock:", "$gross:", "$net:",
	}, labels)
}
