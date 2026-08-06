package inspections

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/shopware"
	"github.com/stretchr/testify/require"
)

func TestEntitySearchResultMigrationQuickFixes(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/EntitySearchResult.php",
		[]byte(`<?php
namespace Shopware\Core\Framework\DataAbstractionLayer\Search;
class EntitySearchResult {}`),
	)))
	version := shopware.ResolvedVersion{
		Version: project.Version{Major: 6, Minor: 8},
		Source:  shopware.VersionSourceExplicit,
		Known:   true,
	}
	inspection := NewShopwareMigration(phpIndex, version)

	for _, test := range []struct {
		name     string
		body     string
		expected string
	}{
		{
			name:     "method call",
			body:     `$result->first();`,
			expected: `$result->getEntities()->first();`,
		},
		{
			name:     "nullsafe method call",
			body:     `$result?->first();`,
			expected: `$result?->getEntities()->first();`,
		},
		{
			name:     "count function",
			body:     `count($result);`,
			expected: `count($result->getEntities());`,
		},
		{
			name:     "foreach expression",
			body:     `foreach ($result as $entity) {}`,
			expected: `foreach ($result->getEntities() as $entity) {}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `<?php
use Shopware\Core\Framework\DataAbstractionLayer\Search\EntitySearchResult;
function migrate(EntitySearchResult $result): void { ` + test.body + ` }
`
			document := lsp.NewTextDocument(
				"file:///project/src/Migrate.php",
				source,
				1,
			)
			collector := &problemCollector{}
			require.NoError(t, inspection.Inspect(
				context.Background(),
				document,
				collector,
			))
			require.Len(t, collector.problems, 1)
			problem := collector.problems[0]
			require.Len(t, problem.Fixes, 1)
			bound := problem.Fixes[0]
			require.Equal(t, entitySearchResultGetEntitiesFixID, bound.ID)
			fix := quickFixWithID(t, inspection, bound.ID)
			presentation, visible, presentErr := fix.Present(
				context.Background(),
				fixContext(t, document, problem, bound, nil),
			)
			require.NoError(t, presentErr)
			require.True(t, visible)
			require.True(t, presentation.Preferred)
			plan, buildErr := fix.Build(
				context.Background(),
				fixContext(t, document, problem, bound, nil),
			)
			require.NoError(t, buildErr)
			require.Len(t, plan.Documents, 1)
			updated, applyErr := plan.Documents[0].Apply()
			require.NoError(t, applyErr)
			require.Contains(t, updated, test.expected)
			require.Empty(t, lsp.NewTextDocument(
				document.URI,
				updated,
				2,
			).ParseErrors)
		})
	}
}

func TestEntityExtensionMigrationQuickFix(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/EntityExtension.php",
		[]byte(`<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
abstract class EntityExtension {}`),
	)))
	for _, test := range []struct {
		name              string
		version           project.Version
		includeEntityName bool
		keepDefinition    bool
	}{
		{name: "Shopware 6.6 adds compatible method", version: project.Version{Major: 6, Minor: 6}, keepDefinition: true},
		{name: "Shopware 6.7 replaces definition method", version: project.Version{Major: 6, Minor: 7}},
		{name: "Shopware 6.7 removes remaining legacy method", version: project.Version{Major: 6, Minor: 7}, includeEntityName: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			entityName := ""
			if test.includeEntityName {
				entityName = `
    public function getEntityName(): string { return ProductDefinition::ENTITY_NAME; }`
			}
			source := `<?php
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
class ProductExtension extends EntityExtension
{
    public function getDefinitionClass(): string { return ProductDefinition::class; }` + entityName + `
}
`
			document := lsp.NewTextDocument("file:///project/src/Extension.php", source, 1)
			inspection := NewShopwareMigration(phpIndex, shopware.ResolvedVersion{
				Version: test.version,
				Source:  shopware.VersionSourceExplicit,
				Known:   true,
			})
			updated := applyOnlyMigrationFix(t, inspection, document, entityExtensionEntityNameFixID)
			require.Contains(t, updated, "public function getEntityName(): string")
			require.Contains(t, updated, "return ProductDefinition::ENTITY_NAME;")
			if test.keepDefinition {
				require.Contains(t, updated, "getDefinitionClass")
			} else {
				require.NotContains(t, updated, "getDefinitionClass")
			}
		})
	}
}

func TestReverseProxyBanAllMigrationQuickFix(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/AbstractReverseProxyGateway.php",
		[]byte(`<?php
namespace Shopware\Storefront\Framework\Cache\ReverseProxy;
abstract class AbstractReverseProxyGateway {}`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/Proxy.php",
		`<?php
use Shopware\Storefront\Framework\Cache\ReverseProxy\AbstractReverseProxyGateway;
class Proxy extends AbstractReverseProxyGateway {}
`,
		1,
	)
	inspection := NewShopwareMigration(phpIndex, shopware.ResolvedVersion{
		Version: project.Version{Major: 6, Minor: 5},
		Source:  shopware.VersionSourceExplicit,
		Known:   true,
	})
	updated := applyOnlyMigrationFix(t, inspection, document, reverseProxyBanAllFixID)
	require.Contains(t, updated, "public function banAll(): void")
	require.Contains(t, updated, "$this->ban([]);")
}

func TestScheduledTaskLoggerMigrationQuickFix(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/ScheduledTaskHandler.php",
		[]byte(`<?php
namespace Shopware\Core\Framework\MessageQueue\ScheduledTask;
abstract class ScheduledTaskHandler {}`),
	)))
	version := shopware.ResolvedVersion{
		Version: project.Version{Major: 6, Minor: 7},
		Source:  shopware.VersionSourceExplicit,
		Known:   true,
	}
	for _, test := range []struct {
		name         string
		parameters   string
		expected     string
		expectImport bool
	}{
		{
			name:         "adds logger before optional parameters",
			parameters:   "object $repository, bool $retry = false",
			expected:     "object $repository, LoggerInterface $exceptionLogger, bool $retry = false",
			expectImport: true,
		},
		{
			name:       "reuses existing exception logger parameter",
			parameters: "object $repository, \\Psr\\Log\\LoggerInterface $exceptionLogger",
			expected:   "object $repository, \\Psr\\Log\\LoggerInterface $exceptionLogger",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `<?php
use Shopware\Core\Framework\MessageQueue\ScheduledTask\ScheduledTaskHandler;
class Handler extends ScheduledTaskHandler
{
    public function __construct(` + test.parameters + `)
    {
        parent::__construct($repository);
    }
}
`
			document := lsp.NewTextDocument("file:///project/src/Handler.php", source, 1)
			inspection := NewShopwareMigration(phpIndex, version)
			updated := applyOnlyMigrationFix(t, inspection, document, scheduledTaskLoggerFixID)
			require.Contains(t, updated, test.expected)
			require.Contains(t, updated, "parent::__construct($repository, $exceptionLogger);")
			if test.expectImport {
				require.Contains(t, updated, "use Psr\\Log\\LoggerInterface;")
			}
		})
	}
}

func migrationInspectionPHPIndex(t *testing.T) *php.PHPIndex {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	return phpIndex
}

func applyOnlyMigrationFix(
	t *testing.T,
	inspection lsp.Inspection,
	document *lsp.TextDocument,
	fixID lsp.FixID,
) string {
	t.Helper()
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))
	require.Len(t, collector.problems, 1)
	problem := collector.problems[0]
	require.Len(t, problem.Fixes, 1)
	bound := problem.Fixes[0]
	require.Equal(t, fixID, bound.ID)
	fix := quickFixWithID(t, inspection, bound.ID)
	plan, err := fix.Build(
		context.Background(),
		fixContext(t, document, problem, bound, nil),
	)
	require.NoError(t, err)
	require.Len(t, plan.Documents, 1)
	updated, err := plan.Documents[0].Apply()
	require.NoError(t, err)
	require.Empty(t, lsp.NewTextDocument(document.URI, updated, document.Version+1).ParseErrors)
	return updated
}
