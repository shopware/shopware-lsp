package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsoleDiagnosticsReportUnknownCommandArgumentsAndOptions(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Console.php",
		[]byte(`<?php
namespace Symfony\Component\Console\Input;
interface InputInterface {
    public function getArgument(string $name): mixed;
    public function getOption(string $name): mixed;
}
namespace Symfony\Component\Console;
class Application {
    public function find(string $name): object {}
}`),
	)))
	consoleIndex, err := console.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, consoleIndex.Close()) })

	source := `<?php
namespace App\Command;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Input\InputInterface;

#[AsCommand('app:user:create')]
class CreateUserCommand {
    protected function configure(): void {
        $this->addArgument('username');
        $this->addOption('admin');
    }
    public function execute(InputInterface $input): void {
        $input->getArgument('usernme');
        $input->getOption('admn');
    }
}

function run(Application $application): void {
    $application->find('app:user:creat');
}`
	path := "/project/src/Command/CreateUserCommand.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, consoleIndex.Index(parsed))

	diagnostics, err := NewConsoleAnalyzer(
		consoleIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument("file://"+path, []byte(source)),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 3)
	codes := make([]any, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes = append(codes, diagnostic.ID)
		assert.NotEmpty(t, diagnostic.Payload)
	}
	assert.ElementsMatch(t, []any{
		missingConsoleArgumentCode,
		missingConsoleOptionCode,
		missingConsoleCommandCode,
	}, codes)
}
