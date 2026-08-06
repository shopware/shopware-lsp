package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShopwareArgumentMigrationsAreTypedAndVersioned(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/DuplicateWishlistProductException.php",
		[]byte(`<?php
namespace Shopware\Core\Checkout\Customer\Exception;
class DuplicateWishlistProductException {}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/Arguments.php",
		`<?php
use Shopware\Core\Checkout\Cart\Tax\Struct\CalculatedTaxCollection;
use Shopware\Core\Checkout\Customer\Exception\DuplicateWishlistProductException;
use Shopware\Core\Content\Media\Thumbnail\ThumbnailService;

class CustomDuplicateException extends DuplicateWishlistProductException
{
    public function __construct(string $productId, int $code) {}
}

function migrate(
    CalculatedTaxCollection $taxes,
    ThumbnailService $thumbnail,
    object $other,
    object $media,
    object $context,
): void {
    $taxes->merge($other, true);
    new DuplicateWishlistProductException('obsolete');
    $thumbnail->updateThumbnails($media, $context);
    $thumbnail->generateThumbnails($media, $context);
}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 5, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 5)

	assert.Equal(t, removeArgumentMigrationCode, problems[0].ID)
	assert.Equal(t, 1, problems[0].Payload.(ShopwareMigrationPayload).ArgumentIndex)
	assert.Equal(t, removeArgumentMigrationCode, problems[1].ID)
	assert.Equal(t, "call-argument", problems[1].Payload.(ShopwareMigrationPayload).Kind)
	assert.Equal(t, removeArgumentMigrationCode, problems[2].ID)
	assert.Equal(t, "constructor-parameter", problems[2].Payload.(ShopwareMigrationPayload).Kind)
	assert.Equal(t, addDefaultArgumentCode, problems[3].ID)
	assert.Equal(t, "false", problems[3].Payload.(ShopwareMigrationPayload).Value)
	assert.Equal(t, thumbnailGenerateCode, problems[4].ID)
	thumbnail := problems[4].Payload.(ShopwareMigrationPayload)
	assert.True(t, thumbnail.Safe)
	assert.Contains(t, thumbnail.Replacement, "new \\Shopware\\Core\\Content\\Media\\MediaCollection([$media])")
}

func TestShopwareArgumentMigrationsIgnoreUnrelatedTypes(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Unrelated.php",
		`<?php
function migrate(object $collection, object $thumbnail): void
{
    $collection->merge('first', 'second');
    $thumbnail->updateThumbnails('media', 'context');
    $thumbnail->generateThumbnails('media', 'context');
}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 8, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, problems)
}
