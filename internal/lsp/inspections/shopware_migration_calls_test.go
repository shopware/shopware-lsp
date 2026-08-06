package inspections

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/shopware"
	"github.com/stretchr/testify/require"
)

func TestContextMetadataStateMigrationQuickFix(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Context.php",
		[]byte(`<?php
namespace Shopware\Core\Framework;
class Context {}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/ContextMigration.php",
		`<?php
use Shopware\Core\Framework\Context;
function migrate(Context $context): void
{
    $context->addExtension(EntityIndexerRegistry::USE_INDEXING_QUEUE, new ArrayEntity());
}
`,
		1,
	)
	updated := applyOnlyMigrationFix(
		t,
		NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
		document,
		contextMetadataStateFixID,
	)
	require.Contains(t, updated, "$context->addState(EntityIndexerRegistry::USE_INDEXING_QUEUE);")
	require.NotContains(t, updated, "new ArrayEntity()")
}

func TestFakerPropertyMigrationQuickFix(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Generator.php",
		[]byte(`<?php
namespace Faker;
class Generator {}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/FakerMigration.php",
		`<?php
use Faker\Generator;
function migrate(Generator $faker): int
{
    return $faker->randomDigit;
}
`,
		1,
	)
	updated := applyOnlyMigrationFix(
		t,
		NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
		document,
		fakerPropertyCallFixID,
	)
	require.Contains(t, updated, "return $faker->randomDigit();")
}

func TestProductStreamMigrationQuickFixes(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/ProductStreamBuilderInterface.php",
		[]byte(`<?php
namespace Shopware\Core\Content\ProductStream\Service;
interface ProductStreamBuilderInterface {}`),
	)))
	for _, test := range []struct {
		name     string
		body     string
		expected string
		removed  string
	}{
		{
			name: "assignment followed by addFilter",
			body: `$filters = $builder->buildFilters($streamId, $context);
    $criteria->addFilter(...$filters);`,
			expected: "$builder->enrichCriteria($criteria, $streamId, $context);",
			removed:  "$filters =",
		},
		{
			name:     "inline addFilter",
			body:     `$criteria->addFilter(...$builder->buildFilters($streamId, $context));`,
			expected: "$builder->enrichCriteria($criteria, $streamId, $context);",
			removed:  "addFilter",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/src/ProductStreamMigration.php",
				`<?php
use Shopware\Core\Content\ProductStream\Service\ProductStreamBuilderInterface;
function migrate(ProductStreamBuilderInterface $builder, object $criteria, string $streamId, object $context): void
{
    `+test.body+`
}
`,
				1,
			)
			updated := applyOnlyMigrationFix(
				t,
				NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 8)),
				document,
				productStreamEnrichCriteriaFixID,
			)
			require.Contains(t, updated, test.expected)
			require.NotContains(t, updated, test.removed)
		})
	}
}

func TestProductStreamManualMigrationHasNoQuickFix(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/ProductStreamBuilderInterface.php",
		[]byte(`<?php
namespace Shopware\Core\Content\ProductStream\Service;
interface ProductStreamBuilderInterface {}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/ProductStreamMigration.php",
		`<?php
use Shopware\Core\Content\ProductStream\Service\ProductStreamBuilderInterface;
function migrate(ProductStreamBuilderInterface $builder, string $streamId, object $context): array
{
    return $builder->buildFilters($streamId, $context);
}
`,
		1,
	)
	collector := &problemCollector{}
	require.NoError(t, NewShopwareMigration(
		phpIndex,
		migrationInspectionVersion(6, 8),
	).Inspect(context.Background(), document, collector))
	require.Len(t, collector.problems, 1)
	require.Empty(t, collector.problems[0].Fixes)
}

func migrationInspectionVersion(major, minor int) shopware.ResolvedVersion {
	return shopware.ResolvedVersion{
		Version: project.Version{Major: major, Minor: minor},
		Source:  shopware.VersionSourceExplicit,
		Known:   true,
	}
}
