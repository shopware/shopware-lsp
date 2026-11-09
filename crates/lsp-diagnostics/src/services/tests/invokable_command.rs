use super::*;
use crate::services::{
    INVOKABLE_COMMAND_RETURN_TYPE_MESSAGE, INVOKABLE_COMMAND_RETURN_VALUE_MESSAGE,
    InvokableCommandAnalyzer,
};
use crate::test_support::check_problems;

const RETURN_TYPE: &str = "symfony.console.invoke.return_type";

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
    public function {|symfony.console.invoke.return_type:__invoke|}()
    {
        $exitCode = 0;
        if (rand(0, 1)) {
            return $exitCode;
        }
        if (rand(0, 1)) {
            return Command::SUCCESS;
        }
        return {|symfony.console.invoke.return_value:'error'|};
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
    public function {|symfony.console.invoke.return_type:__invoke|}(): string
    {
        return {|symfony.console.invoke.return_value:null|};
    }
}

class TraditionalCommand extends Command
{
    public function __invoke(): void {}
}
";
    let (_, problems) = check_problems(
        &InvokableCommandAnalyzer::new(Some(php)),
        "file:///project/src/Command/Commands.php",
        source,
    );
    for problem in &problems {
        let expected = match problem.id.as_str() {
            RETURN_TYPE => INVOKABLE_COMMAND_RETURN_TYPE_MESSAGE,
            _ => INVOKABLE_COMMAND_RETURN_VALUE_MESSAGE,
        };
        assert_eq!(problem.message, expected);
    }
    assert!(problems[0].payload.is_none());
}

#[test]
fn invokable_command_diagnostics_report_bare_return() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(dir.path(), &[]);
    let (_, problems) = check_problems(
        &InvokableCommandAnalyzer::new(Some(php)),
        "file:///project/BareCommand.php",
        r"<?php
use Symfony\Component\Console\Attribute\AsCommand;

#[AsCommand]
class BareCommand {
    public function {|symfony.console.invoke.return_type:__invoke|}(): void {
        {|symfony.console.invoke.return_value:return;|}
    }
}",
    );
    assert!(problems[0].payload.is_none());
}
