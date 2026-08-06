package inspections

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/require"
)

func TestShopwareAPIClassRenameQuickFixUpdatesImportAndUsages(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/ClassRename.php",
		`<?php
use Shopware\Core\Framework\Adapter\Console\ShopwareStyle;
function migrate(ShopwareStyle $style): ShopwareStyle
{
    return new ShopwareStyle();
}
`,
		1,
	)
	updated := applyOnlyMigrationFix(
		t,
		NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
		document,
		apiRenameFixID,
	)
	require.Contains(t, updated, "use Symfony\\Component\\Console\\Style\\SymfonyStyle;")
	require.Contains(t, updated, "function migrate(SymfonyStyle $style): SymfonyStyle")
	require.Contains(t, updated, "return new SymfonyStyle();")
	require.NotContains(t, updated, "ShopwareStyle")
}

func TestShopwareAPIClassRenameAvoidsImportAliasConflict(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/ClassRenameConflict.php",
		`<?php
use App\Console\SymfonyStyle;
use Shopware\Core\Framework\Adapter\Console\ShopwareStyle;
function migrate(ShopwareStyle $style): void {}
`,
		1,
	)
	updated := applyOnlyMigrationFix(
		t,
		NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
		document,
		apiRenameFixID,
	)
	require.Contains(t, updated, "use Symfony\\Component\\Console\\Style\\SymfonyStyle as ShopwareStyle;")
	require.Contains(t, updated, "function migrate(ShopwareStyle $style)")
}

func TestShopwareAPIMemberRenameQuickFixes(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	for _, test := range []struct {
		name     string
		version  int
		source   string
		expected string
		removed  string
	}{
		{
			name:    "instance method",
			version: 5,
			source: `<?php
use Shopware\Core\Content\ImportExport\Processing\Mapping\Mapping;
function migrate(Mapping $mapping): mixed { return $mapping->getMappedDefault(); }
`,
			expected: "$mapping->getDefaultValue()",
			removed:  "getMappedDefault",
		},
		{
			name:    "static method owner",
			version: 6,
			source: `<?php
use Shopware\Core\Framework\DataAbstractionLayer\FieldSerializer\JsonFieldSerializer;
function migrate(): string { return JsonFieldSerializer::encodeJson([]); }
`,
			expected: "\\Shopware\\Core\\Framework\\Util\\Json::encode([])",
			removed:  "encodeJson",
		},
		{
			name:    "class constant owner",
			version: 6,
			source: `<?php
use Shopware\Core\Checkout\Cart;
function migrate(): string { return Cart::CHECKOUT_ORDER_PLACED; }
`,
			expected: "\\Shopware\\Core\\Framework\\Event\\BusinessEvents::CHECKOUT_ORDER_PLACED",
			removed:  "Cart::CHECKOUT_ORDER_PLACED",
		},
		{
			name:    "property getter",
			version: 5,
			source: `<?php
use Shopware\Core\Content\Flow\Dispatching\FlowState;
function migrate(FlowState $state): string { return $state->sequenceId; }
`,
			expected: "$state->getSequenceId()",
			removed:  "$state->sequenceId",
		},
		{
			name:    "exception factory",
			version: 6,
			source: `<?php
use Shopware\Core\Framework\Routing\Exception\InvalidRequestParameterException;
function migrate(): void { throw new InvalidRequestParameterException('field'); }
`,
			expected: "\\Shopware\\Core\\Framework\\Routing\\RoutingException::invalidRequestParameter('field')",
			removed:  "new InvalidRequestParameterException",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/src/APIRename.php",
				test.source,
				1,
			)
			updated := applyOnlyMigrationFix(
				t,
				NewShopwareMigration(phpIndex, migrationInspectionVersion(6, test.version)),
				document,
				apiRenameFixID,
			)
			require.Contains(t, updated, test.expected)
			require.NotContains(t, updated, test.removed)
		})
	}
}
