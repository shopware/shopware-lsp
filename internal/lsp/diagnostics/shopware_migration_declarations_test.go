package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShopwareDeclarationMigrationTablesCoverRectorConfiguration(t *testing.T) {
	assert.Len(t, interfaceAbstractClassMigrations, 4)
	assert.Len(t, addedMethodParameterMigrations, 2)
	assert.Len(t, parameterTypeMigrations, 10)
	assert.Len(t, returnTypeMigrations, 5)
}

func TestShopwareDeclarationMigrationsAreVersioned(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
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
	document := lsp.NewTextDocument(
		"file:///project/src/Declarations.php",
		`<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;
use Shopware\Core\Framework\Adapter\Twig\TemplateIterator;
use Shopware\Core\Framework\DataAbstractionLayer\Indexing\EntityIndexer;
use Shopware\Elasticsearch\Framework\AbstractElasticsearchDefinition;
use Shopware\Storefront\Framework\Captcha\AbstractCaptcha;

class Persister implements CartPersisterInterface
{
    private CartPersisterInterface $inner;
    public function __construct(CartPersisterInterface $inner) {}
}
class Captcha extends AbstractCaptcha
{
    public function supports(string $type): bool { return true; }
}
class Indexer extends EntityIndexer
{
    public function iterate($offset): void {}
}
class Iterator extends TemplateIterator
{
    public function getIterator(): array { return []; }
}
class ElasticsearchDefinition extends AbstractElasticsearchDefinition
{
    public function buildTermQuery(): BoolQuery {}
}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 7, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 7)

	assert.Equal(t, interfaceAbstractClassCode, problems[0].ID)
	assert.Equal(t, "interface-class", problems[0].Payload.(ShopwareMigrationPayload).Kind)
	assert.Equal(t, interfaceAbstractClassCode, problems[1].ID)
	assert.Equal(t, "interface-parameter", problems[1].Payload.(ShopwareMigrationPayload).Kind)
	assert.Equal(t, interfaceAbstractClassCode, problems[2].ID)
	assert.Equal(t, "interface-property", problems[2].Payload.(ShopwareMigrationPayload).Kind)
	assert.Equal(t, addMethodParameterCode, problems[3].ID)
	assert.Equal(t, 1, problems[3].Payload.(ShopwareMigrationPayload).ArgumentIndex)
	assert.Equal(t, nativeTypeMigrationCode, problems[4].ID)
	assert.Equal(t, "?array", problems[4].Payload.(ShopwareMigrationPayload).Replacement)
	assert.Equal(t, nativeTypeMigrationCode, problems[5].ID)
	assert.Equal(t, "\\Traversable", problems[5].Payload.(ShopwareMigrationPayload).Replacement)
	assert.Equal(t, nativeTypeMigrationCode, problems[6].ID)
	assert.Equal(t, "\\OpenSearchDSL\\BuilderInterface", problems[6].Payload.(ShopwareMigrationPayload).Replacement)

	problems, err = NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 6, 10),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 6)
}

func TestInterfaceToAbstractMigrationAvoidsReplacingExistingParent(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/UnsafeParent.php",
		`<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;
class Persister extends ExistingParent implements CartPersisterInterface {}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 5, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.False(t, problems[0].Payload.(ShopwareMigrationPayload).Safe)
}
