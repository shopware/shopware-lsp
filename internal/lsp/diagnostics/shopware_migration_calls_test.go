package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShopware65CallMigrationsAreTypedAndSafe(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	for path, source := range map[string]string{
		"/project/vendor/Context.php": `<?php
namespace Shopware\Core\Framework;
class Context {}`,
		"/project/vendor/Generator.php": `<?php
namespace Faker;
class Generator {}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	document := lsp.NewTextDocument(
		"file:///project/src/Migrate.php",
		`<?php
use Faker\Generator;
use Shopware\Core\Framework\Context;

function migrate(Generator $faker, Context $context, object $other): void
{
    $context->addExtension(EntityIndexerRegistry::USE_INDEXING_QUEUE, new ArrayEntity());
    $context->addExtension(EntityIndexerRegistry::DISABLE_INDEXING, new ArrayEntity());
    $context->addExtension(EntityIndexerRegistry::OTHER, new ArrayEntity());
    $other->addExtension(EntityIndexerRegistry::USE_INDEXING_QUEUE, new ArrayEntity());

    $faker->randomDigit;
    $faker->name = 'changed';
    $faker->randomDigit();
    $other->randomDigit;
}
`,
		1,
	)
	require.Empty(t, document.ParseErrors)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 5, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 4)

	assert.Equal(t, contextMetadataStateCode, problems[0].ID)
	assert.Equal(t, "addExtension", problemRangeText(document, problems[0].Range))
	assert.True(t, problems[0].Payload.(ShopwareMigrationPayload).Safe)
	assert.Equal(t, contextMetadataStateCode, problems[1].ID)
	assert.True(t, problems[1].Payload.(ShopwareMigrationPayload).Safe)

	assert.Equal(t, fakerPropertyCallCode, problems[2].ID)
	assert.Equal(t, "randomDigit", problemRangeText(document, problems[2].Range))
	assert.True(t, problems[2].Payload.(ShopwareMigrationPayload).Safe)
	assert.Equal(t, fakerPropertyCallCode, problems[3].ID)
	assert.Equal(t, "name", problemRangeText(document, problems[3].Range))
	assert.False(t, problems[3].Payload.(ShopwareMigrationPayload).Safe)

	problems, err = NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 4, 20),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func TestProductStreamMigrationRecognizesSafePatterns(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/ProductStreamBuilderInterface.php",
		[]byte(`<?php
namespace Shopware\Core\Content\ProductStream\Service;
interface ProductStreamBuilderInterface {}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/ProductStream.php",
		`<?php
use Shopware\Core\Content\ProductStream\Service\ProductStreamBuilderInterface;

function migrate(
    ProductStreamBuilderInterface $builder,
    object $criteria,
    string $streamId,
    object $context,
): array {
    $filters = $builder->buildFilters($streamId, $context);
    $criteria->addFilter(...$filters);

    $criteria->addFilter(...$builder->buildFilters($streamId, $context));

    return $builder->buildFilters($streamId, $context);
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
	require.Len(t, problems, 3)

	assignment := problems[0].Payload.(ShopwareMigrationPayload)
	assert.Equal(t, productStreamBuilderCode, problems[0].ID)
	assert.Equal(t, "assignment", assignment.Kind)
	assert.True(t, assignment.Safe)
	assert.Less(t, assignment.Start, assignment.End)
	assert.Equal(t, "$builder->enrichCriteria($criteria, $streamId, $context);", assignment.Replacement)

	inline := problems[1].Payload.(ShopwareMigrationPayload)
	assert.Equal(t, "inline", inline.Kind)
	assert.True(t, inline.Safe)
	assert.Equal(t, "$builder->enrichCriteria($criteria, $streamId, $context)", inline.Replacement)

	manual := problems[2].Payload.(ShopwareMigrationPayload)
	assert.Equal(t, "manual", manual.Kind)
	assert.False(t, manual.Safe)
	assert.Contains(t, problems[2].Message, "manual migration required")

	problems, err = NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 7, 10),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, problems)
}
