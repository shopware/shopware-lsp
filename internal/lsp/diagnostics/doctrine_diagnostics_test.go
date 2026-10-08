package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctrineDiagnosticsValidateShopwareDALTablesAndColumns(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	dalIndex, err := shopwaredal.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dalIndex.Close()) })
	require.NoError(t, dalIndex.Index(indexer.NewParsedFile(
		"/project/src/ScheduledTaskDefinition.php",
		[]byte(`<?php
class ScheduledTaskDefinition extends EntityDefinition
{
    public const ENTITY_NAME = 'scheduled_task';
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new StringField('scheduled_task_class', 'scheduledTaskClass'),
        ]);
    }
}`),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine-dbal.php",
		[]byte(`<?php
namespace Doctrine\DBAL;
class Connection { public function insert(string $table, array $data): void {} }
`),
	)))
	source := []byte(`<?php
use Doctrine\DBAL\Connection;
function write(Connection $connection): void {
    $connection->insert('scheduled_task', [
        'scheduled_task_class' => 'Task',
        'scheduled_task_clas' => 'Task',
    ]);
    $connection->insert('migration_audit_log', ['payload' => 'value']);
}`)

	result, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
		dalIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument("file:///project/src/Database.php", source),
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, missingDoctrineColumnCode, result[0].ID)
	assert.Contains(t, result[0].Message, "scheduled_task_clas")
	assert.Contains(
		t,
		result[0].Payload.(map[string]any)["suggestions"],
		"scheduled_task_class",
	)
}

func TestDoctrineDiagnosticsValidateTypeRegistrationClasses(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/project/vendor/Type.php": `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}`,
		"/project/src/Types.php": `<?php
namespace App\Doctrine;
class MoneyType extends \Doctrine\DBAL\Types\Type {}
class NotAType {}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	diagnostics, err := NewDoctrineAnalyzer(
		doctrineIndex,
		phpIndex,
		nil,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/config/packages/doctrine.php",
			[]byte(`<?php
use Doctrine\DBAL\Types\Type;

Type::addType('valid', \App\Doctrine\MoneyType::class);
Type::addType('missing', \App\Doctrine\MonyType::class);
Type::overrideType('invalid', \App\Doctrine\NotAType::class);`),
		),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 2)
	codes := make(map[any]lsp.Problem)
	for _, diagnostic := range diagnostics {
		codes[diagnostic.ID] = diagnostic
	}
	require.Contains(t, codes, missingDoctrineTypeClassCode)
	require.Contains(t, codes, invalidDoctrineTypeClassCode)
	require.Contains(
		t,
		codes[missingDoctrineTypeClassCode].
			Payload.(map[string]any)["suggestions"],
		"App\\Doctrine\\MoneyType",
	)
}
