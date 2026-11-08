use super::*;
use crate::services::{
    INVOKABLE_COMMAND_RETURN_TYPE_MESSAGE, INVOKABLE_COMMAND_RETURN_VALUE_MESSAGE,
    InvokableCommandAnalyzer,
};

const RETURN_TYPE: &str = "symfony.console.invoke.return_type";
const RETURN_VALUE: &str = "symfony.console.invoke.return_value";

#[test]
fn invokable_command_diagnostics() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        dir.path(),
        &[(
            "/project/vendor/Command.php",
            r"<?php
namespace Symfony\Component\Console\Command;
class Command {
    public const SUCCESS = 0;
}",
        )],
    );
    let source = r"<?php
namespace App\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;

#[AsCommand(name: 'app:invalid')]
class InvalidCommand
{
    public function __invoke()
    {
        $exitCode = 0;
        if (rand(0, 1)) {
            return $exitCode;
        }
        if (rand(0, 1)) {
            return Command::SUCCESS;
        }
        return 'error';
    }
}

#[AsCommand(name: 'app:typed')]
class TypedCommand
{
    public function __invoke(): int
    {
        return 'the PHP type checker owns this mismatch';
    }
}

#[AsCommand(name: 'app:wrong')]
class WrongTypeCommand
{
    public function __invoke(): string
    {
        return null;
    }
}

class TraditionalCommand extends Command
{
    public function __invoke(): void {}
}
";
    let document = document("file:///project/src/Command/Commands.php", source);
    let result = analyze(&InvokableCommandAnalyzer::new(Some(php)), &document);
    assert_eq!(result.len(), 4, "{result:?}");
    let mut types = 0;
    let mut values = 0;
    for problem in &result {
        match problem.id.as_str() {
            RETURN_TYPE => {
                types += 1;
                assert_eq!(problem.message, INVOKABLE_COMMAND_RETURN_TYPE_MESSAGE);
            }
            RETURN_VALUE => {
                values += 1;
                assert_eq!(problem.message, INVOKABLE_COMMAND_RETURN_VALUE_MESSAGE);
            }
            _ => {}
        }
    }
    assert_eq!(types, 2);
    assert_eq!(values, 2);
    assert_eq!(range_text(&document, result[0].range), "__invoke");
    assert_eq!(range_text(&document, result[1].range), "'error'");
    assert!(result[0].payload.is_none());
}

#[test]
fn invokable_command_diagnostics_report_bare_return() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(dir.path(), &[]);
    let document = document(
        "file:///project/BareCommand.php",
        r"<?php
use Symfony\Component\Console\Attribute\AsCommand;

#[AsCommand]
class BareCommand {
    public function __invoke(): void {
        return;
    }
}",
    );
    let result = analyze(&InvokableCommandAnalyzer::new(Some(php)), &document);
    assert_eq!(result.len(), 2);
    assert_eq!(result[0].id.as_str(), RETURN_TYPE);
    assert!(result[0].payload.is_none());
    assert_eq!(result[1].id.as_str(), RETURN_VALUE);
    assert_eq!(range_text(&document, result[1].range), "return;");
}
