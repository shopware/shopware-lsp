use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};

use super::{index_file, open_php, tempdir};
use crate::diagnostics::symfony::ConsoleAnalyzer;
use crate::diagnostics::test_support::check_problems;

#[test]
fn console_diagnostics_report_unknown_command_arguments_and_options() {
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/vendor/Console.php",
        r"<?php
namespace Symfony\Component\Console\Input;
interface InputInterface {
    public function getArgument(string $name): mixed;
    public function getOption(string $name): mixed;
}
namespace Symfony\Component\Console;
class Application {
    public function find(string $name): object {}
}",
    );
    let console_dir = tempdir();
    let console = Arc::new(swlsp_console::Index::new(console_dir.path(), None).unwrap());
    let fixture = r"<?php
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
        $input->getArgument('{|symfony.console.argument.missing:usernme|}');
        $input->getOption('{|symfony.console.option.missing:admn|}');
    }
}

function run(Application $application): void {
    $application->find('{|symfony.console.command.missing:app:user:creat|}');
}";
    let path = "/project/src/Command/CreateUserCommand.php";
    let (source, _) = swlsp_lsp::fixture::parse(fixture);
    let parsed = ParsedFile::new(path, source.as_bytes().to_vec());
    php.index(&parsed).unwrap();
    console.index(&parsed).unwrap();

    let (_, result) = check_problems(
        &ConsoleAnalyzer::new(Some(console), Some(php.clone())),
        &format!("file://{path}"),
        fixture,
    );
    for problem in &result {
        assert!(problem.payload.is_some());
    }
}
