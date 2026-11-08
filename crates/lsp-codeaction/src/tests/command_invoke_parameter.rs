//! Go `command_invoke_parameter_codeaction_test.go`.

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_php::PhpIndex;

use super::CommandInvokeParameterCodeActionProvider;
use crate::test_support::request_at;

const URI: &str = "file:///project/src/TestCommand.php";

fn fixture() -> (tempfile::TempDir, CommandInvokeParameterCodeActionProvider) {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/vendor/ConsoleCommandStubs.php".to_owned(),
            br"<?php
namespace Symfony\Component\Console\Command;
abstract class Command {}
namespace App;
abstract class BaseLegacyCommand extends \Symfony\Component\Console\Command\Command {}
"
            .to_vec(),
        ))
        .unwrap();
    (
        dir,
        CommandInvokeParameterCodeActionProvider::new(Some(index)),
    )
}

fn request(source: &str, needle: &str) -> CodeActionRequest {
    let offset = source.find(needle).unwrap() as u32;
    request_at(URI, source, offset, offset + needle.len() as u32)
}

fn actions(source: &str, needle: &str) -> (CodeActionRequest, Vec<protocol::CodeAction>) {
    let (_dir, provider) = fixture();
    let request = request(source, needle);
    let actions = provider.get_code_actions(&Context::background(), &request);
    (request, actions)
}

fn action<'a>(actions: &'a [protocol::CodeAction], type_name: &str) -> &'a protocol::CodeAction {
    actions
        .iter()
        .find(|action| action.title.contains(&format!("Add {type_name} parameter")))
        .unwrap_or_else(|| panic!("missing parameter action {type_name}"))
}

fn edits(action: &protocol::CodeAction) -> &Vec<protocol::TextEdit> {
    &action.edit.as_ref().unwrap().changes[URI]
}

#[test]
fn offers_available_types() {
    let source = r"<?php
namespace App\Command;
use Symfony\Component\Console\Attribute\AsCommand as ConsoleCommand;
use Symfony\Component\Console\Style\SymfonyStyle as Style;

#[ConsoleCommand(name: 'app:test')]
class TestCommand
{
    public function __invoke(Style $io): int
    {
        return 0;
    }
}";
    let (_, actions) = actions(source, "TestCommand");
    assert_eq!(actions.len(), 4);
    let titles: Vec<&str> = actions.iter().map(|action| action.title.as_str()).collect();
    for action in &actions {
        assert_eq!(action.kind, Some(CodeActionKind::REFACTOR_REWRITE));
    }
    assert!(titles.contains(&"Symfony: Add InputInterface parameter to __invoke"));
    assert!(titles.contains(&"Symfony: Add OutputInterface parameter to __invoke"));
    assert!(!titles.contains(&"Symfony: Add SymfonyStyle parameter to __invoke"));
}

#[test]
fn rejects_invalid_classes() {
    let all_parameters = "InputInterface $input,
        OutputInterface $output,
        Cursor $cursor,
        SymfonyStyle $io,
        Application $application";
    let all = format!(
        r"<?php
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Cursor;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\Console\Application;
#[AsCommand]
class TestCommand
{{
    public function __invoke(
        {all_parameters}
    ): int {{}}
}}"
    );
    let fixtures = [
        (
            "without AsCommand",
            r"<?php
class TestCommand
{
    public function __invoke(): int {}
}"
            .to_owned(),
        ),
        (
            "without invoke",
            r"<?php
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand
{
    public function execute(): int {}
}"
            .to_owned(),
        ),
        (
            "direct legacy Command subclass",
            r"<?php
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
#[AsCommand]
class TestCommand extends Command
{
    public function __invoke(): int {}
}"
            .to_owned(),
        ),
        (
            "indirect legacy Command subclass",
            r"<?php
namespace App;
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand extends BaseLegacyCommand
{
    public function __invoke(): int {}
}"
            .to_owned(),
        ),
        ("all parameters exist", all),
    ];
    for (name, source) in fixtures {
        let (_, actions) = actions(&source, "TestCommand");
        assert!(actions.is_empty(), "{name}");
    }
}

#[test]
fn inserts_before_optional_parameter() {
    let source = r"<?php
namespace App\Command;
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand
{
    public function __invoke(?string $format = null): int {}
}";
    let (_, actions) = actions(source, "__invoke");
    let edits = edits(action(&actions, "SymfonyStyle"));
    assert_eq!(edits.len(), 2);
    assert_eq!(edits[0].new_text, "SymfonyStyle $io, ");
    assert!(
        edits[1]
            .new_text
            .contains("use Symfony\\Component\\Console\\Style\\SymfonyStyle;")
    );
}

#[test]
fn preserves_multiline_style() {
    for (name, parameters, type_name, new_text) in [
        (
            "empty",
            "\n    ",
            "InputInterface",
            "\n        InputInterface $input",
        ),
        (
            "trailing comma",
            "\n        string $name,\n    ",
            "OutputInterface",
            "\n        OutputInterface $output,",
        ),
        (
            "without trailing comma",
            "\n        string $name\n    ",
            "Cursor",
            ",\n        Cursor $cursor",
        ),
    ] {
        let source = format!(
            r"<?php
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand
{{
    public function __invoke({parameters}): int {{}}
}}"
        );
        let (_, actions) = actions(&source, "__invoke");
        let edits = edits(action(&actions, type_name));
        assert!(!edits.is_empty(), "{name}");
        assert_eq!(edits[0].new_text, new_text, "{name}");
    }
}

#[test]
fn avoids_import_conflict() {
    let source = r"<?php
use App\Model\Cursor;
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand
{
    public function __invoke(): int {}
}";
    let (_, actions) = actions(source, "__invoke");
    let edits = edits(action(&actions, "Cursor"));
    assert_eq!(edits.len(), 1);
    assert_eq!(
        edits[0].new_text,
        r"\Symfony\Component\Console\Cursor $cursor"
    );
}

#[test]
fn inserts_before_variadic() {
    let source = r"<?php
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand
{
    public function __invoke(string ...$values): int {}
}";
    let (_, actions) = actions(source, "__invoke");
    let edits = edits(action(&actions, "InputInterface"));
    assert_eq!(edits.len(), 2);
    assert_eq!(edits[0].new_text, "InputInterface $input, ");
}
