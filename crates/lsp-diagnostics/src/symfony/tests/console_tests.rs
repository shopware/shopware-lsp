use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};

use super::{analyze, index_file, open_php, tempdir};
use crate::symfony::{
    ConsoleAnalyzer, MISSING_CONSOLE_ARGUMENT_CODE, MISSING_CONSOLE_COMMAND_CODE,
    MISSING_CONSOLE_OPTION_CODE,
};
use crate::test_support::diagnostics_document;

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
    let source = r"<?php
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
}";
    let path = "/project/src/Command/CreateUserCommand.php";
    let parsed = ParsedFile::new(path, source.as_bytes().to_vec());
    php.index(&parsed).unwrap();
    console.index(&parsed).unwrap();

    let result = analyze(
        &ConsoleAnalyzer::new(Some(console), Some(php.clone())),
        &diagnostics_document(&format!("file://{path}"), source),
    );
    assert_eq!(result.len(), 3);
    let mut codes: Vec<&str> = result.iter().map(|problem| problem.id.0.as_str()).collect();
    codes.sort_unstable();
    assert_eq!(
        codes,
        vec![
            MISSING_CONSOLE_ARGUMENT_CODE,
            MISSING_CONSOLE_COMMAND_CODE,
            MISSING_CONSOLE_OPTION_CODE
        ]
    );
    for problem in &result {
        assert!(problem.payload.is_some());
    }
}
