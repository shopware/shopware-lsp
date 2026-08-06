package diagnostics

import (
	"context"
	"fmt"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShopwareAPIMigrationTablesCoverRectorConfiguration(t *testing.T) {
	assert.Len(t, apiClassMigrations, 19)
	assert.Len(t, apiMethodMigrations, 53)
	assert.Len(t, apiStaticMethodMigrations, 1)
	assert.Len(t, apiConstantMigrations, 35)
	assert.Len(t, apiPropertyMigrations, 1)
	assert.Len(t, apiFactoryMigrations, 12)
}

func TestEveryShopwareAPIMigrationMappingProducesDiagnostic(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	for _, rule := range apiClassMigrations {
		assertShopwareAPIMigrationDiagnostic(
			t,
			phpIndex,
			rule.since,
			fmt.Sprintf("<?php function migrate(): void { new \\%s(); }", rule.from),
			apiClassRenameCode,
		)
	}
	for _, rule := range apiMethodMigrations {
		assertShopwareAPIMigrationDiagnostic(
			t,
			phpIndex,
			rule.since,
			fmt.Sprintf(
				"<?php function migrate(\\%s $subject): void { $subject->%s(); }",
				rule.owner,
				rule.from,
			),
			apiMethodRenameCode,
		)
	}
	for _, rule := range apiStaticMethodMigrations {
		assertShopwareAPIMigrationDiagnostic(
			t,
			phpIndex,
			rule.since,
			fmt.Sprintf("<?php \\%s::%s();", rule.owner, rule.from),
			apiStaticMethodRenameCode,
		)
	}
	for _, rule := range apiConstantMigrations {
		assertShopwareAPIMigrationDiagnostic(
			t,
			phpIndex,
			rule.since,
			fmt.Sprintf("<?php $value = \\%s::%s;", rule.owner, rule.from),
			apiConstantRenameCode,
		)
	}
	for _, rule := range apiPropertyMigrations {
		assertShopwareAPIMigrationDiagnostic(
			t,
			phpIndex,
			rule.since,
			fmt.Sprintf(
				"<?php function migrate(\\%s $subject): void { echo $subject->%s; }",
				rule.owner,
				rule.from,
			),
			apiPropertyMigrationCode,
		)
	}
	for _, rule := range apiFactoryMigrations {
		assertShopwareAPIMigrationDiagnostic(
			t,
			phpIndex,
			rule.since,
			fmt.Sprintf("<?php throw new \\%s('value');", rule.from),
			apiExceptionFactoryCode,
		)
	}
}

func assertShopwareAPIMigrationDiagnostic(
	t *testing.T,
	phpIndex *php.PHPIndex,
	since shopwareMigrationSince,
	source string,
	expected lsp.DiagnosticID,
) {
	t.Helper()
	document := lsp.NewTextDocument("file:///project/src/Mapping.php", source, 1)
	require.Empty(t, document.ParseErrors)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, since.minor, since.patch),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	for _, problem := range problems {
		if problem.ID == expected {
			return
		}
	}
	require.Failf(t, "missing migration diagnostic", "%s did not produce %s", source, expected)
}

func TestShopwareAPIPropertyAndExceptionFactoryMigrations(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/APITransforms.php",
		`<?php
use Shopware\Core\Content\Flow\Dispatching\FlowState;
use Shopware\Core\Framework\Routing\Exception\InvalidRequestParameterException;

function migrate(FlowState $state): void
{
    echo $state->sequenceId;
    $state->sequenceId = 'changed';
    throw new InvalidRequestParameterException('field');
}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 6, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 3)

	assert.Equal(t, apiPropertyMigrationCode, problems[0].ID)
	assert.True(t, problems[0].Payload.(ShopwareMigrationPayload).Safe)
	assert.Equal(t, "getSequenceId()", problems[0].Payload.(ShopwareMigrationPayload).Replacement)
	assert.Equal(t, apiPropertyMigrationCode, problems[1].ID)
	assert.False(t, problems[1].Payload.(ShopwareMigrationPayload).Safe)
	assert.Equal(t, apiExceptionFactoryCode, problems[2].ID)
	assert.Equal(
		t,
		"\\Shopware\\Core\\Framework\\Routing\\RoutingException::invalidRequestParameter('field')",
		problems[2].Payload.(ShopwareMigrationPayload).Replacement,
	)

	problems, err = NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 5, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 2)
}

func TestShopwareAPIMigrationsUseSemanticReferences(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/APIMigration.php",
		`<?php
use Shopware\Core\Checkout\Cart;
use Shopware\Core\Content\ImportExport\Processing\Mapping\Mapping;
use Shopware\Core\Framework\Adapter\Console\ShopwareStyle;
use Shopware\Core\Framework\DataAbstractionLayer\FieldSerializer\JsonFieldSerializer;

function migrate(Mapping $mapping): void
{
    $style = new ShopwareStyle();
    $mapping->getDefault();
    JsonFieldSerializer::encodeJson([]);
    echo Cart::CHECKOUT_ORDER_PLACED;
}
`,
		1,
	)
	require.Empty(t, document.ParseErrors)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 6, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 4)

	classProblem := problems[0]
	assert.Equal(t, apiClassRenameCode, classProblem.ID)
	classPayload := classProblem.Payload.(ShopwareMigrationPayload)
	assert.True(t, classPayload.Safe)
	require.Len(t, classPayload.Edits, 2)
	assert.Equal(t, "Shopware\\Core\\Framework\\Adapter\\Console\\ShopwareStyle", classPayload.Edits[0].Original)
	assert.Equal(t, "Symfony\\Component\\Console\\Style\\SymfonyStyle", classPayload.Edits[0].Replacement)
	assert.Equal(t, "ShopwareStyle", classPayload.Edits[1].Original)
	assert.Equal(t, "SymfonyStyle", classPayload.Edits[1].Replacement)

	methodProblem := problems[1]
	assert.Equal(t, apiMethodRenameCode, methodProblem.ID)
	methodPayload := methodProblem.Payload.(ShopwareMigrationPayload)
	assert.Equal(t, "getDefaultValue", methodPayload.Replacement)
	assert.Equal(t, "getDefault", problemRangeText(document, methodProblem.Range))

	staticProblem := problems[2]
	assert.Equal(t, apiStaticMethodRenameCode, staticProblem.ID)
	staticPayload := staticProblem.Payload.(ShopwareMigrationPayload)
	assert.Equal(t, "\\Shopware\\Core\\Framework\\Util\\Json::encode([])", staticPayload.Replacement)

	constantProblem := problems[3]
	assert.Equal(t, apiConstantRenameCode, constantProblem.ID)
	constantPayload := constantProblem.Payload.(ShopwareMigrationPayload)
	assert.Equal(t, "\\Shopware\\Core\\Framework\\Event\\BusinessEvents::CHECKOUT_ORDER_PLACED", constantPayload.Replacement)

	problems, err = NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 5, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 2)
	assert.Equal(t, apiClassRenameCode, problems[0].ID)
	assert.Equal(t, apiMethodRenameCode, problems[1].ID)
}

func TestShopwareAPIClassRenamePreservesExplicitAlias(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Aliased.php",
		`<?php
use Shopware\Core\Framework\Adapter\Console\ShopwareStyle as ConsoleStyle;
function migrate(ConsoleStyle $style): void {}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 5, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	payload := problems[0].Payload.(ShopwareMigrationPayload)
	require.Len(t, payload.Edits, 1)
	assert.Equal(t, "Symfony\\Component\\Console\\Style\\SymfonyStyle", payload.Edits[0].Replacement)
}
