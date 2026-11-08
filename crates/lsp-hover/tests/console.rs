mod common;

use std::sync::Arc;

use common::*;
use swlsp_console::Index;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::ConsoleHoverProvider;

#[test]
fn console_command_hover() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(
        &cache.path().join("php"),
        &[(
            "/project/vendor/Application.php",
            r"<?php
namespace Symfony\Component\Console;
class Application { public function find(string $name): object {} }",
        )],
    );
    let console = Arc::new(Index::new(cache.path().join("console"), None).unwrap());
    index_file(
        console.as_ref(),
        "/project/src/CreateUserCommand.php",
        r"<?php
namespace App;
#[AsCommand(name: 'app:user:create', description: 'Creates a user')]
class CreateUserCommand {
    protected function configure(): void {
        $this->addArgument('username');
        $this->addOption('admin');
    }
}",
    );
    let source = r"<?php
use Symfony\Component\Console\Application;
function run(Application $application): void {
    $application->find('app:user:create');
}";
    let document = TextDocument::new("file:///project/src/Runner.php", source, 1);
    let request = request_at(document, source.find("app:user:create").unwrap() + 1);
    let result = hover(
        &ConsoleHoverProvider::new("/project", Some(console), Some(php)),
        &request,
    )
    .expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Symfony command"));
    assert!(value.contains("Creates a user"));
    assert!(value.contains("1 arguments · 1 options"));
}
