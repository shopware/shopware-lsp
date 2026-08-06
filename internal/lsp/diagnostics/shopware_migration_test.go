package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/shopware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShopwareMigrationAnalyzerDelegatesEntitySearchResults(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Migrate.php",
		`<?php
namespace App;

use Shopware\Core\Content\Product\SalesChannel\Listing\ProductListingResult;
use Shopware\Core\Framework\DataAbstractionLayer\EntityCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Search\EntitySearchResult;

function migrate(
    EntitySearchResult $result,
    ?EntitySearchResult $nullable,
    ProductListingResult $listing,
    EntityCollection $collection,
): void {
    $result->first();
    $nullable?->last();
    count($result);
    iterator_to_array($listing);
    foreach ($result as $entity) {}

    $result->getTotal();
    $collection->first();
    count($collection);
    foreach ($collection as $entity) {}
}
`,
		1,
	)
	require.Empty(t, document.ParseErrors)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 8, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 5)

	assert.Equal(t, "first", problemRangeText(document, problems[0].Range))
	assert.Equal(t, "last", problemRangeText(document, problems[1].Range))
	assert.Equal(t, "$result", problemRangeText(document, problems[2].Range))
	assert.Equal(t, "$listing", problemRangeText(document, problems[3].Range))
	assert.Equal(t, "$result", problemRangeText(document, problems[4].Range))
	for _, problem := range problems {
		assert.Equal(t, entitySearchResultGetEntitiesCode, problem.ID)
		assert.Equal(t, "shopware-rector", problem.Source)
		payload, ok := problem.Payload.(ShopwareMigrationPayload)
		require.True(t, ok)
		assert.True(t, payload.Safe)
	}
}

func TestShopwareMigrationAnalyzerRequiresKnownTargetVersion(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Migrate.php",
		`<?php
use Shopware\Core\Framework\DataAbstractionLayer\Search\EntitySearchResult;
function migrate(EntitySearchResult $result): void { $result->first(); }
`,
		1,
	)
	for _, version := range []shopware.ResolvedVersion{
		{},
		resolvedShopwareMigrationVersion(6, 7, 9),
	} {
		problems, err := NewShopwareMigrationAnalyzer(phpIndex, version).Analyze(
			context.Background(),
			document,
		)
		require.NoError(t, err)
		assert.Empty(t, problems)
	}
}

func TestShopwareMigrationAnalyzerClassLevelRules(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	for path, source := range map[string]string{
		"/project/vendor/EntityExtension.php": `<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
abstract class EntityExtension {}`,
		"/project/vendor/AbstractReverseProxyGateway.php": `<?php
namespace Shopware\Storefront\Framework\Cache\ReverseProxy;
abstract class AbstractReverseProxyGateway {}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	document := lsp.NewTextDocument(
		"file:///project/src/Extensions.php",
		`<?php
namespace App;

use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Storefront\Framework\Cache\ReverseProxy\AbstractReverseProxyGateway;

class ProductExtension extends EntityExtension
{
    public function getDefinitionClass(): string
    {
        return ProductDefinition::class;
    }
}

class CompleteExtension extends EntityExtension
{
    public function getEntityName(): string { return 'complete'; }
}

class LegacyExtension extends EntityExtension
{
    public function getDefinitionClass(): string { return LegacyDefinition::class; }
    public function getEntityName(): string { return 'legacy'; }
}

class Proxy extends AbstractReverseProxyGateway {}
class CompleteProxy extends AbstractReverseProxyGateway
{
    public function banAll(): void {}
}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 7, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 3)
	assert.Equal(t, entityExtensionEntityNameCode, problems[0].ID)
	assert.Equal(t, "ProductExtension", problemRangeText(document, problems[0].Range))
	productPayload := problems[0].Payload.(ShopwareMigrationPayload)
	assert.Equal(t, "ProductDefinition::ENTITY_NAME", productPayload.Replacement)
	assert.True(t, productPayload.RemoveLegacy)
	assert.True(t, productPayload.Safe)
	assert.Equal(t, entityExtensionEntityNameCode, problems[1].ID)
	legacyPayload := problems[1].Payload.(ShopwareMigrationPayload)
	assert.Equal(t, "LegacyDefinition::ENTITY_NAME", legacyPayload.Replacement)
	assert.True(t, legacyPayload.RemoveLegacy)
	assert.True(t, legacyPayload.Safe)
	assert.Equal(t, reverseProxyBanAllCode, problems[2].ID)
	assert.Equal(t, "Proxy", problemRangeText(document, problems[2].Range))
}

func TestEntityExtensionMigrationKeepsLegacyMethodBeforeShopware67(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/EntityExtension.php",
		[]byte(`<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
abstract class EntityExtension {}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/Extension.php",
		`<?php
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
class ProductExtension extends EntityExtension
{
    public function getDefinitionClass(): string { return ProductDefinition::class; }
}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 6, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	payload := problems[0].Payload.(ShopwareMigrationPayload)
	assert.False(t, payload.RemoveLegacy)
	assert.Equal(t, "ProductDefinition::ENTITY_NAME", payload.Replacement)
}

func TestScheduledTaskLoggerMigrationIsVersionedAndTypeSafe(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/ScheduledTaskHandler.php",
		[]byte(`<?php
namespace Shopware\Core\Framework\MessageQueue\ScheduledTask;
abstract class ScheduledTaskHandler {}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/Tasks.php",
		`<?php
namespace App;
use Psr\Log\LoggerInterface;
use Shopware\Core\Framework\MessageQueue\ScheduledTask\ScheduledTaskHandler;

class MissingLogger extends ScheduledTaskHandler
{
    public function __construct(object $repository) { parent::__construct($repository); }
}
class ExistingLogger extends ScheduledTaskHandler
{
    public function __construct(object $repository, LoggerInterface $exceptionLogger) { parent::__construct($repository); }
}
class WrongLogger extends ScheduledTaskHandler
{
    public function __construct(object $repository, string $exceptionLogger) { parent::__construct($repository); }
}
class Complete extends ScheduledTaskHandler
{
    public function __construct(object $repository, LoggerInterface $exceptionLogger) { parent::__construct($repository, $exceptionLogger); }
}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 7, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 3)

	missing := problems[0].Payload.(ShopwareMigrationPayload)
	assert.Equal(t, scheduledTaskLoggerCode, problems[0].ID)
	assert.True(t, missing.Safe)
	assert.True(t, missing.AddParameter)
	existing := problems[1].Payload.(ShopwareMigrationPayload)
	assert.True(t, existing.Safe)
	assert.False(t, existing.AddParameter)
	wrong := problems[2].Payload.(ShopwareMigrationPayload)
	assert.False(t, wrong.Safe)
	assert.False(t, wrong.AddParameter)

	problems, err = NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 6, 10),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func migrationTestPHPIndex(t *testing.T) *php.PHPIndex {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	fixtures := map[string]string{
		"/project/vendor/EntitySearchResult.php": `<?php
namespace Shopware\Core\Framework\DataAbstractionLayer\Search;
class EntitySearchResult {}`,
		"/project/vendor/ProductListingResult.php": `<?php
namespace Shopware\Core\Content\Product\SalesChannel\Listing;
use Shopware\Core\Framework\DataAbstractionLayer\Search\EntitySearchResult;
class ProductListingResult extends EntitySearchResult {}`,
		"/project/vendor/EntityCollection.php": `<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
class EntityCollection {}`,
	}
	for path, source := range fixtures {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	return phpIndex
}

func resolvedShopwareMigrationVersion(
	major int,
	minor int,
	patch int,
) shopware.ResolvedVersion {
	return shopware.ResolvedVersion{
		Version: project.Version{Major: major, Minor: minor, Patch: patch},
		Source:  shopware.VersionSourceExplicit,
		Known:   true,
	}
}
