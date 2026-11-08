use std::sync::Arc;

use swlsp_lsp::CompletionProvider;
use swlsp_lsp_protocol::CompletionItemKind;

use super::*;
use crate::{ConsoleCompletionProvider, ConsoleHelperCompletionProvider};

fn console_fixture(
    dirs: &(tempfile::TempDir, tempfile::TempDir),
) -> (ConsoleCompletionProvider, &'static str, &'static str) {
    let php = php_index(&dirs.0);
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
    let console = Arc::new(swlsp_console::Index::new(dirs.1.path(), None).unwrap());
    let source = r"<?php
namespace App\Command;
use Symfony\Component\Console\Input\InputInterface;

#[AsCommand(name: 'app:user:create', description: 'Create user')]
class CreateUserCommand
{
    protected function configure(): void
    {
        $this->addArgument('username', description: 'User name');
        $this->addOption('admin', 'a', description: 'Grant admin');
    }

    public function execute(InputInterface $input): void
    {
        $input->getArgument('');
        $input->getOption('');
    }
}";
    let path = "/project/src/Command/CreateUserCommand.php";
    index_file(&php, path, source);
    index_file(console.as_ref(), path, source);
    (
        ConsoleCompletionProvider::new(console, Some(php)),
        source,
        path,
    )
}

#[test]
fn console_input_argument_and_option_completion() {
    let dirs = (tempdir(), tempdir());
    let (provider, source, path) = console_fixture(&dirs);
    let document = document(&format!("file://{path}"), source);
    for (needle, label) in [("getArgument('')", "username"), ("getOption('')", "admin")] {
        let offset = source.find(needle).unwrap() + needle.find("''").unwrap() + 1;
        let items = provider.get_completions(&ctx(), &node_request(&document, offset));
        let item = require_completion(&items, label);
        assert_eq!(item.kind, Some(CompletionItemKind::FIELD));
    }
}

#[test]
fn console_command_name_completion_requires_application_type() {
    let dirs = (tempdir(), tempdir());
    let (provider, _, _) = console_fixture(&dirs);
    let source = r"<?php
namespace App;
use Symfony\Component\Console\Application;
function findCommand(Application $application) {
    return $application->find('');
}";
    let document = document("file:///project/src/Runner.php", source);
    let items = provider.get_completions(
        &ctx(),
        &node_request(&document, source.find("''").unwrap() + 1),
    );
    let command = require_completion(&items, "app:user:create");
    assert_eq!(command.kind, Some(CompletionItemKind::REFERENCE));

    let untyped = "<?php $repository->find('');";
    let untyped_document = document_untyped(untyped);
    assert!(
        provider
            .get_completions(
                &ctx(),
                &node_request(&untyped_document, untyped.find("''").unwrap() + 1)
            )
            .is_empty()
    );
}

fn document_untyped(source: &str) -> Arc<swlsp_lsp::TextDocument> {
    super::document("file:///project/src/Repository.php", source)
}

fn helper_php_index(cache: &tempfile::TempDir, root: &str) -> swlsp_php::PhpIndex {
    let php = php_index(cache);
    index_file(
        &php,
        &format!("{root}/vendor/Console.php"),
        r"<?php
namespace Symfony\Component\Console\Helper;
interface HelperInterface { public function getName(): string; }
abstract class Helper implements HelperInterface {}
class HelperSet {
    public function get(string $name): HelperInterface {}
    public function has(string $name): bool {}
}
namespace Symfony\Component\Console\Command;
class Command { public function getHelper(string $name): object {} }
",
    );
    index_file(
        &php,
        &format!("{root}/src/QuestionHelper.php"),
        r"<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
/** Helps ask interactive questions. */
class QuestionHelper extends Helper {
    public function getName(): string { return 'question'; }
}",
    );
    php
}

#[test]
fn console_helper_completion_uses_typed_helper_set_and_command() {
    let (cache, root) = (tempdir(), tempdir());
    let root = root.path().to_string_lossy().into_owned();
    let php = helper_php_index(&cache, &root);
    let provider = ConsoleHelperCompletionProvider::new(Some(php));
    for (name, source) in [
        (
            "helper set",
            r"<?php
use Symfony\Component\Console\Helper\HelperSet;
function inspect(HelperSet $helpers): void { $helpers->get('que'); }",
        ),
        (
            "command",
            r"<?php
use Symfony\Component\Console\Command\Command;
function inspect(Command $command): void { $command->getHelper('que'); }",
        ),
    ] {
        let path = format!("{root}/src/{name}.php");
        let document = document(&swlsp_uriutil::file_uri(&path), source);
        let offset = source.rfind("que").unwrap() + 1;
        let items = provider.get_completions(&ctx(), &node_request(&document, offset));
        let item = require_completion(&items, "question");
        assert_eq!(item.kind, Some(CompletionItemKind::REFERENCE), "{name}");
        assert_eq!(item.detail, "App\\QuestionHelper");
        let edit = text_edit(item);
        assert_eq!(range_text(&document, edit.range), "que");
        assert_eq!(edit.new_text, "question");
    }
}

#[test]
fn console_helper_completion_rejects_unrelated_get() {
    let (cache, root) = (tempdir(), tempdir());
    let root = root.path().to_string_lossy().into_owned();
    let php = helper_php_index(&cache, &root);
    let source = r"<?php
class Repository { public function get(string $name): object {} }
function inspect(Repository $repository): void { $repository->get('que'); }";
    let path = format!("{root}/src/Repository.php");
    let document = document(&swlsp_uriutil::file_uri(&path), source);
    let offset = source.rfind("que").unwrap() + 1;
    assert!(
        ConsoleHelperCompletionProvider::new(Some(php))
            .get_completions(&ctx(), &node_request(&document, offset))
            .is_empty()
    );
}
