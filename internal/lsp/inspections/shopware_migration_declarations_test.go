package inspections

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/require"
)

func TestInterfaceToAbstractClassMigrationQuickFixes(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	for _, test := range []struct {
		name     string
		source   string
		expected string
	}{
		{
			name: "class inheritance",
			source: `<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;
class Persister implements CartPersisterInterface {}
`,
			expected: "class Persister extends AbstractCartPersister",
		},
		{
			name: "parameter type",
			source: `<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;
class Service
{
    public function __construct(CartPersisterInterface $persister) {}
}
`,
			expected: "__construct(AbstractCartPersister $persister)",
		},
		{
			name: "property type",
			source: `<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;
class Service
{
    private CartPersisterInterface $persister;
}
`,
			expected: "private AbstractCartPersister $persister;",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/src/InterfaceMigration.php",
				test.source,
				1,
			)
			updated := applyOnlyMigrationFix(
				t,
				NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
				document,
				declarationMigrationFixID,
			)
			require.Contains(t, updated, test.expected)
			require.Contains(t, updated, "use Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister;")
		})
	}
}

func TestShopwareMethodDeclarationMigrationQuickFixes(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	for path, source := range map[string]string{
		"/project/vendor/AbstractCaptcha.php": `<?php
namespace Shopware\Storefront\Framework\Captcha;
abstract class AbstractCaptcha {}`,
		"/project/vendor/EntityIndexer.php": `<?php
namespace Shopware\Core\Framework\DataAbstractionLayer\Indexing;
abstract class EntityIndexer {}`,
		"/project/vendor/TemplateIterator.php": `<?php
namespace Shopware\Core\Framework\Adapter\Twig;
abstract class TemplateIterator {}`,
		"/project/vendor/AbstractElasticsearchDefinition.php": `<?php
namespace Shopware\Elasticsearch\Framework;
abstract class AbstractElasticsearchDefinition {}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	for _, test := range []struct {
		name     string
		version  int
		source   string
		expected string
	}{
		{
			name:    "required parameter",
			version: 5,
			source: `<?php
use Shopware\Storefront\Framework\Captcha\AbstractCaptcha;
class Captcha extends AbstractCaptcha
{
    public function supports(string $type): bool { return true; }
}
`,
			expected: "supports(string $type, array $captchaConfig)",
		},
		{
			name:    "parameter type",
			version: 5,
			source: `<?php
use Shopware\Core\Framework\DataAbstractionLayer\Indexing\EntityIndexer;
class Indexer extends EntityIndexer
{
    public function iterate($offset): void {}
}
`,
			expected: "iterate(?array $offset)",
		},
		{
			name:    "return type",
			version: 5,
			source: `<?php
use Shopware\Core\Framework\Adapter\Twig\TemplateIterator;
class Iterator extends TemplateIterator
{
    public function getIterator(): array { return []; }
}
`,
			expected: "getIterator(): Traversable",
		},
		{
			name:    "Shopware 6.7 return type",
			version: 7,
			source: `<?php
use Shopware\Elasticsearch\Framework\AbstractElasticsearchDefinition;
class Definition extends AbstractElasticsearchDefinition
{
    public function buildTermQuery(): BoolQuery {}
}
`,
			expected: "buildTermQuery(): BuilderInterface",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/src/DeclarationMigration.php",
				test.source,
				1,
			)
			updated := applyOnlyMigrationFix(
				t,
				NewShopwareMigration(phpIndex, migrationInspectionVersion(6, test.version)),
				document,
				declarationMigrationFixID,
			)
			require.Contains(t, updated, test.expected)
		})
	}
}

func TestInterfaceToAbstractClassMigrationQuickFixesFreeFunctionParameter(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Function.php",
		`<?php
namespace App;

use Shopware\Core\Checkout\Cart\CartPersisterInterface;

function persist(CartPersisterInterface $persister): void {}
`,
		1,
	)
	updated := applyOnlyMigrationFix(
		t,
		NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
		document,
		declarationMigrationFixID,
	)
	require.Contains(t, updated, "use Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister;")
	require.Contains(t, updated, "function persist(AbstractCartPersister $persister): void")
}
