use std::sync::Arc;

use swlsp_uriutil::file_uri;

use super::*;
use crate::php_common::test_support::*;

#[test]
fn console_definition_navigates_commands_and_inputs() {
    let root = tempfile::tempdir().unwrap();
    let command_path = p(&root.path().join("src/CreateUserCommand.php"));
    let source = r"<?php
namespace App\Command;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Input\InputInterface;

#[AsCommand('app:user:create', aliases: [self::CREATE_ALIAS])]
class CreateUserCommand {
    private const CREATE_ALIAS = 'app:user:add';

    protected function configure(): void {
        $this->addOption('admin', 'a');
    }
    public function execute(InputInterface $input): void {
        $input->getOption('admin');
    }
}

function run(Application $application): void {
    $application->find('app:user:create');
    $application->find('app:user:add');
}";
    write(std::path::Path::new(&command_path), source);
    let php = php_index(&root.path().join("php-cache"));
    index(
        &php,
        "/project/vendor/Console.php",
        r"<?php
namespace Symfony\Component\Console\Input;
interface InputInterface { public function getOption(string $name): mixed; }
namespace Symfony\Component\Console;
class Application { public function find(string $name): object {} }",
    );
    let console = Arc::new(Index::new(root.path().join("console-cache"), None).unwrap());
    index(&php, &command_path, source);
    index(console.as_ref(), &command_path, source);

    let document = document(&file_uri(&command_path), source);
    let provider = ConsoleDefinitionProvider::new(Some(console), Some(php));
    let alias_line = source.split('\n').nth(5).unwrap();
    for (needle, line, character) in [
        ("getOption('admin')", 10, None),
        ("find('app:user:create')", 5, None),
        (
            "find('app:user:add')",
            5,
            Some(alias_line.find("self::CREATE_ALIAS").unwrap() as u32),
        ),
    ] {
        let offset = index_of(source, needle) + needle.find('\'').unwrap() + 1;
        // The request leaves the position at 0:0; only the node matters.
        let mut request = request_at(&document, offset);
        request.params.position = Default::default();
        let locations = definitions(&provider, &request);
        assert_eq!(locations.len(), 1, "{needle}");
        assert_eq!(locations[0].uri, file_uri(&command_path));
        assert_eq!(locations[0].range.start.line, line);
        if let Some(character) = character {
            assert_eq!(locations[0].range.start.character, character);
        }
    }
}
