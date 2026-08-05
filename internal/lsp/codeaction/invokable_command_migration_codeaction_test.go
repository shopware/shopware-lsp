package codeaction

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokableCommandMigrationConvertsArgumentsOptionsAndBody(
	t *testing.T,
) {
	provider := invokableCommandMigrationFixture(t)
	source := `<?php
namespace App\Command;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;

#[AsCommand(name: 'app:create-user')]
class CreateUserCommand extends Command
{
    protected function configure(): void
    {
        $this
            ->addArgument('username', InputArgument::REQUIRED, 'The username')
            ->addOption('admin', 'a', InputOption::VALUE_NONE, 'Make user admin');
    }

    protected function execute(
        InputInterface $input,
        OutputInterface $output,
    ): int {
        $username = $input->getArgument('username');
        $admin = $input->getOption('admin');
        $output->writeln('Creating user: ' . $username);

        return Command::SUCCESS;
    }
}`
	request := commandInvokeParameterRequest(
		source,
		"CreateUserCommand",
	)

	actions := provider.GetCodeActions(context.Background(), request)

	require.Len(t, actions, 1)
	assert.Equal(
		t,
		"Symfony: Migrate to invokable command",
		actions[0].Title,
	)
	assert.Equal(t, protocol.CodeActionRefactorRewrite, actions[0].Kind)
	updated := applyCodeActionEdit(
		t,
		source,
		actions[0],
		request.TextDocument.URI,
		request.Document,
	)
	assert.NotContains(t, updated, "extends Command")
	assert.NotContains(t, updated, "function configure")
	assert.NotContains(t, updated, "function execute")
	assert.Contains(t, updated, "public function __invoke(")
	assert.Contains(
		t,
		updated,
		"#[Argument(description: 'The username')] string $username",
	)
	assert.Contains(
		t,
		updated,
		"#[Option(shortcut: 'a', description: 'Make user admin')] "+
			"bool $admin = false",
	)
	assert.Contains(t, updated, "OutputInterface $output")
	assert.NotContains(t, updated, "InputInterface $input")
	assert.NotContains(t, updated, "$input->getArgument")
	assert.NotContains(t, updated, "$input->getOption")
	assert.NotContains(t, updated, "$username = $username")
	assert.NotContains(t, updated, "$admin = $admin")
	assert.Contains(t, updated, "return Command::SUCCESS;")
	assert.Contains(
		t,
		updated,
		"use Symfony\\Component\\Console\\Attribute\\Argument;",
	)
	assert.Contains(
		t,
		updated,
		"use Symfony\\Component\\Console\\Attribute\\Option;",
	)
	assert.NotContains(
		t,
		updated,
		"use Symfony\\Component\\Console\\Input\\InputArgument;",
	)
	assert.NotContains(
		t,
		updated,
		"use Symfony\\Component\\Console\\Input\\InputOption;",
	)
	assert.NotContains(
		t,
		updated,
		"use Symfony\\Component\\Console\\Input\\InputInterface;",
	)
	require.Empty(
		t,
		requestDocumentWithSource(request, updated).ParseErrors,
	)
}

func TestInvokableCommandMigrationConvertsNamesCastsAndExitConstants(
	t *testing.T,
) {
	provider := invokableCommandMigrationFixture(t)
	source := `<?php
namespace App\Command;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;

class ImportCommand extends Command
{
    public function __construct()
    {
        parent::__construct('app:import');
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addArgument(
            'user-name',
            InputArgument::REQUIRED,
            'User name'
        );
        $this->addArgument(
            'age',
            InputArgument::OPTIONAL,
            'Age',
            '18'
        );
        $this->addOption(
            'dry-run',
            'd',
            InputOption::VALUE_NONE,
            'Dry run'
        );
        $this->addOption(
            'formats',
            null,
            InputOption::VALUE_OPTIONAL | InputOption::VALUE_IS_ARRAY,
            'Formats',
            []
        );
    }

    protected function execute(
        InputInterface $input,
        OutputInterface $output,
    ): int {
        $userName = $input->getArgument('user-name');
        $age = (int) $input->getArgument('age');
        $dryRun = $input->getOption('dry-run');
        $formats = (array) $input->getOption('formats');

        if ($dryRun) {
            $output->writeln($userName . $age . count($formats));
        }

        return self::SUCCESS;
    }
}`
	request := commandInvokeParameterRequest(source, "ImportCommand")

	actions := provider.GetCodeActions(context.Background(), request)

	require.Len(t, actions, 1)
	updated := applyCodeActionEdit(
		t,
		source,
		actions[0],
		request.TextDocument.URI,
		request.Document,
	)
	assert.Contains(
		t,
		updated,
		"#[Argument(name: 'user-name', description: 'User name')] "+
			"string $userName",
	)
	assert.Contains(
		t,
		updated,
		"#[Argument(description: 'Age')] ?string $age = '18'",
	)
	assert.Contains(
		t,
		updated,
		"#[Option(name: 'dry-run', shortcut: 'd', "+
			"description: 'Dry run')] bool $dryRun = false",
	)
	assert.Contains(
		t,
		updated,
		"#[Option(description: 'Formats')] ?array $formats = []",
	)
	assert.NotContains(t, updated, "parent::__construct")
	assert.Contains(t, updated, "public function __construct()")
	assert.NotContains(t, updated, "(int) $age")
	assert.NotContains(t, updated, "(array) $formats")
	assert.Contains(t, updated, "return Command::SUCCESS;")
	require.Empty(
		t,
		requestDocumentWithSource(request, updated).ParseErrors,
	)
}

func TestInvokableCommandMigrationKeepsOnlyStillUsedFrameworkParameters(
	t *testing.T,
) {
	provider := invokableCommandMigrationFixture(t)
	source := `<?php
namespace App\Command;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

class InspectCommand extends Command
{
    protected function configure(): void
    {
        $this->addArgument('name', InputArgument::REQUIRED);
    }

    protected function execute(
        InputInterface $input,
        OutputInterface $output,
    ): int {
        $name = $input->getArgument('name');
        $allArguments = $input->getArguments();

        return count($allArguments) + strlen($name);
    }
}`
	request := commandInvokeParameterRequest(source, "InspectCommand")

	actions := provider.GetCodeActions(context.Background(), request)

	require.Len(t, actions, 1)
	updated := applyCodeActionEdit(
		t,
		source,
		actions[0],
		request.TextDocument.URI,
		request.Document,
	)
	assert.Contains(t, updated, "InputInterface $input")
	assert.NotContains(t, updated, "OutputInterface $output")
	assert.Contains(t, updated, "#[Argument] string $name")
	assert.NotContains(t, updated, "$input->getArgument('name')")
	assert.Contains(t, updated, "$input->getArguments()")
}

func TestInvokableCommandMigrationReusesAttributeAliasAndConflict(
	t *testing.T,
) {
	provider := invokableCommandMigrationFixture(t)
	source := `<?php
namespace App\Command;
use App\Metadata\Option;
use Symfony\Component\Console\Attribute\Argument as ConsoleArgument;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;

class AliasCommand extends Command
{
    protected function configure(): void
    {
        $this->addArgument('name', InputArgument::REQUIRED);
        $this->addOption('force', null, InputOption::VALUE_NONE);
    }

    protected function execute(InputInterface $input): int
    {
        return 0;
    }
}`
	request := commandInvokeParameterRequest(source, "AliasCommand")

	actions := provider.GetCodeActions(context.Background(), request)

	require.Len(t, actions, 1)
	updated := applyCodeActionEdit(
		t,
		source,
		actions[0],
		request.TextDocument.URI,
		request.Document,
	)
	assert.Contains(t, updated, "#[ConsoleArgument] string $name")
	assert.Contains(
		t,
		updated,
		"#[\\Symfony\\Component\\Console\\Attribute\\Option] "+
			"bool $force = false",
	)
	assert.NotContains(
		t,
		updated,
		"use Symfony\\Component\\Console\\Attribute\\Option;",
	)
}

func TestInvokableCommandMigrationImportsCommandForInheritedExitConstant(
	t *testing.T,
) {
	provider := invokableCommandMigrationFixture(t)
	source := `<?php
class ExitCommand extends \Symfony\Component\Console\Command\Command
{
    protected function execute(): int
    {
        return self::SUCCESS;
    }
}`
	request := commandInvokeParameterRequest(source, "ExitCommand")

	actions := provider.GetCodeActions(context.Background(), request)

	require.Len(t, actions, 1)
	updated := applyCodeActionEdit(
		t,
		source,
		actions[0],
		request.TextDocument.URI,
		request.Document,
	)
	assert.Contains(
		t,
		updated,
		"use Symfony\\Component\\Console\\Command\\Command;",
	)
	assert.Contains(t, updated, "return Command::SUCCESS;")
	require.Empty(
		t,
		requestDocumentWithSource(request, updated).ParseErrors,
	)
}

func TestInvokableCommandMigrationRejectsUnsafeClasses(t *testing.T) {
	provider := invokableCommandMigrationFixture(t)
	for _, test := range []struct {
		name   string
		source string
		needle string
	}{
		{
			name: "non command",
			source: `<?php
class Service { protected function execute(): int { return 0; } }`,
			needle: "Service",
		},
		{
			name: "indirect command",
			source: `<?php
use App\BaseCommand;
class TestCommand extends BaseCommand {
    protected function execute(): int { return 0; }
}`,
			needle: "TestCommand",
		},
		{
			name: "already invokable",
			source: `<?php
use Symfony\Component\Console\Command\Command;
class TestCommand extends Command {
    public function __invoke(): int { return 0; }
    protected function execute(): int { return 0; }
}`,
			needle: "TestCommand",
		},
		{
			name: "other configure call",
			source: `<?php
use Symfony\Component\Console\Command\Command;
class TestCommand extends Command {
    protected function configure(): void {
        $this->setDescription('Unsafe');
    }
    protected function execute(): int { return 0; }
}`,
			needle: "TestCommand",
		},
		{
			name: "dynamic input name",
			source: `<?php
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
class TestCommand extends Command {
    protected function configure(): void {
        $this->addArgument(self::INPUT, InputArgument::REQUIRED);
    }
    protected function execute(): int { return 0; }
}`,
			needle: "TestCommand",
		},
		{
			name: "runtime default",
			source: `<?php
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
class TestCommand extends Command {
    protected function configure(): void {
        $this->addArgument(
            'date',
            InputArgument::OPTIONAL,
            '',
            new \DateTimeImmutable()
        );
    }
    protected function execute(): int { return 0; }
}`,
			needle: "TestCommand",
		},
		{
			name: "readonly command",
			source: `<?php
use Symfony\Component\Console\Command\Command;
readonly class TestCommand extends Command {
    protected function execute(): int { return 0; }
}`,
			needle: "TestCommand",
		},
		{
			name: "generated parameter collision",
			source: `<?php
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
class TestCommand extends Command {
    protected function configure(): void {
        $this->addArgument('input', InputArgument::REQUIRED);
    }
    protected function execute(InputInterface $input): int {
        return count($input->getArguments());
    }
}`,
			needle: "TestCommand",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := commandInvokeParameterRequest(
				test.source,
				test.needle,
			)
			assert.Empty(t, provider.GetCodeActions(
				context.Background(),
				request,
			))
		})
	}
}

func invokableCommandMigrationFixture(
	t *testing.T,
) *InvokableCommandMigrationCodeActionProvider {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/vendor/SymfonyConsole73.php",
		[]byte(`<?php
namespace Symfony\Component\Console\Command;
abstract class Command
{
    public const SUCCESS = 0;
    public const FAILURE = 1;
    public const INVALID = 2;
}
final class InvokableCommand {}
namespace Symfony\Component\Console\Attribute;
#[\Attribute(\Attribute::TARGET_PARAMETER)]
final class Argument {}
#[\Attribute(\Attribute::TARGET_PARAMETER)]
final class Option {}
#[\Attribute(\Attribute::TARGET_CLASS)]
final class AsCommand {}
namespace Symfony\Component\Console\Input;
interface InputInterface {}
final class InputArgument {}
final class InputOption {}
namespace Symfony\Component\Console\Output;
interface OutputInterface {}
namespace App;
abstract class BaseCommand extends \Symfony\Component\Console\Command\Command {}
`),
	)))
	return NewInvokableCommandMigrationCodeActionProvider(phpIndex)
}

func requestDocumentWithSource(
	request *lsp.CodeActionRequest,
	source string,
) *lsp.TextDocument {
	return lsp.NewTextDocument(
		request.Document.URI,
		source,
		request.Document.Version+1,
	)
}
