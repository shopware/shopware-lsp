package inspections

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/require"
)

func TestShopwareCallArgumentMigrationQuickFixes(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	for _, test := range []struct {
		name     string
		source   string
		fixID    lsp.FixID
		expected string
		removed  string
	}{
		{
			name: "remove method argument",
			source: `<?php
use Shopware\Core\Checkout\Cart\Tax\Struct\CalculatedTaxCollection;
function migrate(CalculatedTaxCollection $taxes, object $other): void
{
    $taxes->merge($other, true);
}
`,
			fixID:    removeArgumentMigrationFixID,
			expected: "$taxes->merge($other);",
			removed:  "true",
		},
		{
			name: "remove exception argument",
			source: `<?php
use Shopware\Core\Checkout\Customer\Exception\DuplicateWishlistProductException;
function migrate(): object
{
    return new DuplicateWishlistProductException('obsolete');
}
`,
			fixID:    removeArgumentMigrationFixID,
			expected: "new DuplicateWishlistProductException()",
			removed:  "'obsolete'",
		},
		{
			name: "add thumbnail strict default",
			source: `<?php
use Shopware\Core\Content\Media\Thumbnail\ThumbnailService;
function migrate(ThumbnailService $thumbnail, object $media, object $context): void
{
    $thumbnail->updateThumbnails($media, $context);
}
`,
			fixID:    addDefaultArgumentFixID,
			expected: "$thumbnail->updateThumbnails($media, $context, false);",
		},
		{
			name: "batch thumbnail generation",
			source: `<?php
use Shopware\Core\Content\Media\Thumbnail\ThumbnailService;
function migrate(ThumbnailService $thumbnail, object $media, object $context): void
{
    $thumbnail->generateThumbnails($media, $context);
}
`,
			fixID:    thumbnailGenerateFixID,
			expected: "$thumbnail->generate(new \\Shopware\\Core\\Content\\Media\\MediaCollection([$media]), $context);",
			removed:  "generateThumbnails",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/src/ArgumentMigration.php",
				test.source,
				1,
			)
			updated := applyOnlyMigrationFix(
				t,
				NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
				document,
				test.fixID,
			)
			require.Contains(t, updated, test.expected)
			if test.removed != "" {
				require.NotContains(t, updated, test.removed)
			}
		})
	}
}

func TestShopwareConstructorParameterRemovalQuickFix(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/DuplicateWishlistProductException.php",
		[]byte(`<?php
namespace Shopware\Core\Checkout\Customer\Exception;
class DuplicateWishlistProductException {}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/CustomException.php",
		`<?php
use Shopware\Core\Checkout\Customer\Exception\DuplicateWishlistProductException;
class CustomException extends DuplicateWishlistProductException
{
    public function __construct(string $obsolete, int $code) {}
}
`,
		1,
	)
	updated := applyOnlyMigrationFix(
		t,
		NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
		document,
		removeArgumentMigrationFixID,
	)
	require.Contains(t, updated, "public function __construct(int $code)")
	require.NotContains(t, updated, "$obsolete")
}
