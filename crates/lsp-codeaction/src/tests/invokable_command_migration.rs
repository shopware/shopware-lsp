//! Go `invokable_command_migration_codeaction_test.go`.

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, Context, TextDocument};
use swlsp_lsp_protocol::CodeActionKind;
use swlsp_php::PhpIndex;

use super::InvokableCommandMigrationCodeActionProvider;
use crate::test_support::{apply_code_action_edit, request_at};

const URI: &str = "file:///project/src/TestCommand.php";

const STUBS: &str = r"<?php
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
";

fn fixture() -> (
    tempfile::TempDir,
    InvokableCommandMigrationCodeActionProvider,
) {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/vendor/SymfonyConsole73.php",
            STUBS.as_bytes().to_vec(),
        ))
        .unwrap();
    (
        dir,
        InvokableCommandMigrationCodeActionProvider::new(Some(index)),
    )
}

fn actions(source: &str, needle: &str) -> Vec<swlsp_lsp_protocol::CodeAction> {
    let (_dir, provider) = fixture();
    let offset = source.find(needle).unwrap() as u32;
    let request = request_at(URI, source, offset, offset + needle.len() as u32);
    provider.get_code_actions(&Context::background(), &request)
}

fn migrate(source: &str, needle: &str) -> String {
    let actions = actions(source, needle);
    assert_eq!(actions.len(), 1);
    apply_code_action_edit(source, &actions[0], URI)
}

fn assert_parses(updated: &str) {
    assert!(
        TextDocument::new(URI, updated, 2).parse_errors().is_empty(),
        "{updated}"
    );
}

#[test]
fn converts_arguments_options_and_body() {
    let source = r"<?php
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
}";
    let actions = actions(source, "CreateUserCommand");
    assert_eq!(actions.len(), 1);
    assert_eq!(actions[0].title, "Symfony: Migrate to invokable command");
    assert_eq!(actions[0].kind, Some(CodeActionKind::REFACTOR_REWRITE));
    let updated = apply_code_action_edit(source, &actions[0], URI);
    for absent in [
        "extends Command",
        "function configure",
        "function execute",
        "InputInterface $input",
        "$input->getArgument",
        "$input->getOption",
        "$username = $username",
        "$admin = $admin",
        "use Symfony\\Component\\Console\\Input\\InputArgument;",
        "use Symfony\\Component\\Console\\Input\\InputOption;",
        "use Symfony\\Component\\Console\\Input\\InputInterface;",
    ] {
        assert!(!updated.contains(absent), "{absent}\n{updated}");
    }
    for present in [
        "public function __invoke(",
        "#[Argument(description: 'The username')] string $username",
        "#[Option(shortcut: 'a', description: 'Make user admin')] bool $admin = false",
        "OutputInterface $output",
        "return Command::SUCCESS;",
        "use Symfony\\Component\\Console\\Attribute\\Argument;",
        "use Symfony\\Component\\Console\\Attribute\\Option;",
    ] {
        assert!(updated.contains(present), "{present}\n{updated}");
    }
    assert_parses(&updated);
}

#[test]
fn converts_names_casts_and_exit_constants() {
    let source = r"<?php
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
}";
    let updated = migrate(source, "ImportCommand");
    for present in [
        "#[Argument(name: 'user-name', description: 'User name')] string $userName",
        "#[Argument(description: 'Age')] ?string $age = '18'",
        "#[Option(name: 'dry-run', shortcut: 'd', description: 'Dry run')] bool $dryRun = false",
        "#[Option(description: 'Formats')] ?array $formats = []",
        "public function __construct()",
        "return Command::SUCCESS;",
    ] {
        assert!(updated.contains(present), "{present}\n{updated}");
    }
    for absent in ["parent::__construct", "(int) $age", "(array) $formats"] {
        assert!(!updated.contains(absent), "{absent}\n{updated}");
    }
    assert_parses(&updated);
}

#[test]
fn keeps_only_still_used_framework_parameters() {
    let source = r"<?php
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
}";
    let updated = migrate(source, "InspectCommand");
    assert!(updated.contains("InputInterface $input"));
    assert!(!updated.contains("OutputInterface $output"));
    assert!(updated.contains("#[Argument] string $name"));
    assert!(!updated.contains("$input->getArgument('name')"));
    assert!(updated.contains("$input->getArguments()"));
}

#[test]
fn reuses_attribute_alias_and_conflict() {
    let source = r"<?php
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
}";
    let updated = migrate(source, "AliasCommand");
    assert!(updated.contains("#[ConsoleArgument] string $name"));
    assert!(
        updated.contains("#[\\Symfony\\Component\\Console\\Attribute\\Option] bool $force = false")
    );
    assert!(!updated.contains("use Symfony\\Component\\Console\\Attribute\\Option;"));
}

#[test]
fn imports_command_for_inherited_exit_constant() {
    let source = r"<?php
class ExitCommand extends \Symfony\Component\Console\Command\Command
{
    protected function execute(): int
    {
        return self::SUCCESS;
    }
}";
    let updated = migrate(source, "ExitCommand");
    assert!(updated.contains("use Symfony\\Component\\Console\\Command\\Command;"));
    assert!(updated.contains("return Command::SUCCESS;"));
    assert_parses(&updated);
}

#[test]
fn rejects_unsafe_classes() {
    for (name, source, needle) in [
        (
            "non command",
            "<?php\nclass Service { protected function execute(): int { return 0; } }",
            "Service",
        ),
        (
            "indirect command",
            r"<?php
use App\BaseCommand;
class TestCommand extends BaseCommand {
    protected function execute(): int { return 0; }
}",
            "TestCommand",
        ),
        (
            "already invokable",
            r"<?php
use Symfony\Component\Console\Command\Command;
class TestCommand extends Command {
    public function __invoke(): int { return 0; }
    protected function execute(): int { return 0; }
}",
            "TestCommand",
        ),
        (
            "other configure call",
            r"<?php
use Symfony\Component\Console\Command\Command;
class TestCommand extends Command {
    protected function configure(): void {
        $this->setDescription('Unsafe');
    }
    protected function execute(): int { return 0; }
}",
            "TestCommand",
        ),
        (
            "dynamic input name",
            r"<?php
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
class TestCommand extends Command {
    protected function configure(): void {
        $this->addArgument(self::INPUT, InputArgument::REQUIRED);
    }
    protected function execute(): int { return 0; }
}",
            "TestCommand",
        ),
        (
            "runtime default",
            r"<?php
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
}",
            "TestCommand",
        ),
        (
            "readonly command",
            r"<?php
use Symfony\Component\Console\Command\Command;
readonly class TestCommand extends Command {
    protected function execute(): int { return 0; }
}",
            "TestCommand",
        ),
        (
            "generated parameter collision",
            r"<?php
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
}",
            "TestCommand",
        ),
    ] {
        assert!(actions(source, needle).is_empty(), "{name}");
    }
}
