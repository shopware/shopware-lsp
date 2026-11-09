use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile, PreparingIndexer};
use swlsp_lsp::{Context, TextDocument};
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNodeExt, TextSize};

use super::*;

fn open(dir: &Path) -> Index {
    Index::new(dir, None).unwrap()
}

fn index_source(index: &Index, path: &str, source: &str) {
    index
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
}

fn names(inputs: &[Input]) -> Vec<&str> {
    let mut names: Vec<&str> = inputs.iter().map(|input| input.name.as_str()).collect();
    names.sort_unstable();
    names
}

#[test]
fn index_php_console_commands_and_inputs() {
    let dir = tempfile::tempdir().unwrap();
    let index = open(dir.path());
    let source = r"<?php
namespace App\Command;

#[AsCommand(
    name: 'app:user:create',
    description: 'Creates a user',
    aliases: ['app:user:add'],
)]
class CreateUserCommand
{
    protected function configure(): void
    {
        $this
            ->addArgument('role', InputArgument::OPTIONAL, 'Initial role', 'user')
            ->addOption(
                name: 'admin',
                shortcut: 'a',
                mode: InputOption::VALUE_NONE,
                description: 'Grant admin access',
            )
            ->setDefinition([
                new InputArgument('tenant', InputArgument::REQUIRED, 'Tenant'),
                new InputOption('dry-run', 'd', InputOption::VALUE_NONE, 'Dry run'),
            ]);
    }

    public function __invoke(
        #[Argument(description: 'User name')] string $username,
        #[Option(name: 'notify', shortcut: 'n')] bool $sendNotification = true,
    ): void {}
}

class UserCommands
{
    #[AsCommand('app:user:delete')]
    public function delete(
        #[Argument(name: 'user-id')] string $id,
        #[Option(name: 'force')] bool $force = false,
    ): void {}
}";
    index_source(&index, "/project/src/Command/CreateUserCommand.php", source);

    let commands = index.get_command("app:user:create").unwrap();
    assert_eq!(commands.len(), 1);
    let command = &commands[0];
    assert_eq!(command.class, "App\\Command\\CreateUserCommand");
    assert_eq!(command.description, "Creates a user");
    assert_eq!(names(&command.arguments), ["role", "tenant", "username"]);
    assert_eq!(names(&command.options), ["admin", "dry-run", "notify"]);
    let admin = command
        .options
        .iter()
        .find(|input| input.name == "admin")
        .unwrap();
    assert_eq!(admin.shortcut, "a");
    assert_eq!(admin.mode, "InputOption::VALUE_NONE");
    let role = command
        .arguments
        .iter()
        .find(|input| input.name == "role")
        .unwrap();
    assert_eq!(role.default, "'user'");
    assert_eq!(role.description, "Initial role");
    let notify = command
        .options
        .iter()
        .find(|input| input.name == "notify")
        .unwrap();
    assert_eq!(notify.default, "true");
    assert_eq!(notify.shortcut, "n");

    let aliases = index.get_command("app:user:add").unwrap();
    assert_eq!(aliases.len(), 1);
    assert_eq!(aliases[0].canonical, "app:user:create");

    let method_commands = index.get_command("app:user:delete").unwrap();
    assert_eq!(method_commands.len(), 1);
    assert_eq!(method_commands[0].method, "delete");
    assert_eq!(names(&method_commands[0].arguments), ["user-id"]);
    assert_eq!(names(&method_commands[0].options), ["force"]);

    let inputs = index
        .inputs_for_target("\\App\\Command\\CreateUserCommand", "", InputKind::Option)
        .unwrap();
    assert_eq!(names(&inputs), ["admin", "dry-run", "notify"]);
    index.close().unwrap();
}

#[test]
fn index_legacy_and_compiled_console_commands() {
    let dir = tempfile::tempdir().unwrap();
    let index = open(dir.path());
    index_source(
        &index,
        "/project/src/LegacyCommand.php",
        r"<?php
namespace App;
class LegacyCommand extends Command {
    protected static $defaultName = 'app:legacy|app:old';
}
class ConfiguredCommand extends Command {
    protected function configure(): void {
        $this->setName('app:configured');
    }
}",
    );
    for name in ["app:legacy", "app:old", "app:configured"] {
        assert_eq!(index.get_command(name).unwrap().len(), 1, "{name}");
    }
    assert_eq!(
        index.get_command("app:old").unwrap()[0].canonical,
        "app:legacy"
    );

    index_source(
        &index,
        "/project/var/cache/container.xml",
        r#"<container><services>
  <service id="App\Command\CompiledCommand" class="App\Command\CompiledCommand">
    <tag name="console.command" command="app:compiled"/>
  </service>
</services></container>"#,
    );
    let compiled = index.get_command("app:compiled").unwrap();
    assert_eq!(compiled.len(), 1);
    assert_eq!(compiled[0].class, "App\\Command\\CompiledCommand");
    index.close().unwrap();
}

#[test]
fn index_as_command_positional_and_local_constant_aliases() {
    let dir = tempfile::tempdir().unwrap();
    let index = open(dir.path());
    let source = r"<?php
namespace App\Command;

class UserCommands
{
    private const CREATE = 'app:user:create';
    private const CREATE_ALIAS = 'app:user:add';
    private const DELETE_ALIAS = 'app:user:remove';
    private const DYNAMIC = self::CREATE . ':dynamic';

    #[AsCommand(self::CREATE, 'Creates a user', [
        static::CREATE_ALIAS,
        'app:user:new',
        self::DYNAMIC,
        External::ALIAS,
    ])]
    public function create(): int { return 0; }

    #[AsCommand(name: 'app:user:delete', aliases: [self::DELETE_ALIAS])]
    public function delete(): int { return 0; }
}";
    index_source(&index, "/project/src/Command/UserCommands.php", source);
    for name in ["app:user:create", "app:user:add", "app:user:new"] {
        assert!(!index.get_command(name).unwrap().is_empty(), "{name}");
    }
    let create = index.get_command("app:user:create").unwrap();
    assert_eq!(create.len(), 1);
    assert_eq!(create[0].canonical, "app:user:create");
    assert_eq!(create[0].description, "Creates a user");
    assert_eq!(create[0].method, "create");

    let delete_alias = index.get_command("app:user:remove").unwrap();
    assert_eq!(delete_alias.len(), 1);
    assert_eq!(delete_alias[0].canonical, "app:user:delete");

    for unsupported in ["app:user:create:dynamic", "External::ALIAS"] {
        assert!(
            index.get_command(unsupported).unwrap().is_empty(),
            "{unsupported}"
        );
    }
    index.close().unwrap();
}

#[test]
fn as_command_without_static_name_falls_back_to_traditional_name() {
    let dir = tempfile::tempdir().unwrap();
    let index = open(dir.path());
    index_source(
        &index,
        "/project/src/Command/LegacyCommand.php",
        r"<?php
#[AsCommand(name: CommandNames::DYNAMIC)]
class LegacyCommand {
    protected static $defaultName = 'app:legacy|app:old';
}",
    );
    for name in ["app:legacy", "app:old"] {
        assert_eq!(index.get_command(name).unwrap().len(), 1, "{name}");
    }
    index.close().unwrap();
}

#[test]
fn restored_command_path_clears_stale_candidate() {
    let dir = tempfile::tempdir().unwrap();
    let path = "/project/src/Command/RestoredCommand.php";
    let first = open(dir.path());
    index_source(
        &first,
        path,
        "<?php\n#[AsCommand(name: 'app:restored')]\nclass RestoredCommand {}\n",
    );
    first.close().unwrap();

    let restored = open(dir.path());
    index_source(&restored, path, "<?php return 1;");
    assert!(restored.get_command("app:restored").unwrap().is_empty());
    restored.close().unwrap();
}

#[test]
fn legacy_command_inputs_preserve_source_order() {
    let file = ParsedFile::new(
        "/project/src/OrderedCommand.php",
        br"<?php
class OrderedCommand {
    protected function configure(): void {
        $this->addArgument('zebra', InputArgument::REQUIRED);
        $this->addArgument('alpha', InputArgument::OPTIONAL);
        $this->addOption('verbose', 'v', InputOption::VALUE_NONE);
    }
}
"
        .to_vec(),
    );
    let classes = swlsp_parser_php::query::classes(&file.syntax_tree().unwrap().root());
    assert_eq!(classes.len(), 1);
    let (arguments, options) = legacy_command_inputs(&classes[0], file.path());
    assert_eq!(
        arguments
            .iter()
            .map(|input| input.name.as_str())
            .collect::<Vec<_>>(),
        ["zebra", "alpha"]
    );
    assert_eq!(options.len(), 1);
    assert_eq!(options[0].name, "verbose");
    assert_eq!(options[0].shortcut, "v");
}

#[test]
fn indexing_publishes_command_workspace_symbols() {
    let dir = tempfile::tempdir().unwrap();
    let index = open(dir.path());
    let file = ParsedFile::new(
        "/project/src/Command.php",
        b"<?php\n#[AsCommand(name: 'app:sym', description: 'Does things')]\nclass Sym {}\n"
            .to_vec(),
    );
    index.index(&file).unwrap();
    let commands = index.get_command("app:sym").unwrap();
    assert_eq!(commands[0].description, "Does things");
    index.close().unwrap();
}

#[test]
fn prepared_indexing_matches_direct_indexing() {
    let dir = tempfile::tempdir().unwrap();
    let path = "/project/src/Command.php";
    let source = "<?php\n#[AsCommand(name: 'app:dup', description: 'First')]\nclass First {}\n#[AsCommand(name: 'app:dup', description: 'Second')]\nclass Second {}\n#[AsCommand(name: 'app:other')]\nclass Other {}\n";
    let direct = open(&dir.path().join("direct"));
    index_source(&direct, path, source);
    let prepared = open(&dir.path().join("prepared"));
    let file = ParsedFile::new(path, source.as_bytes().to_vec());
    let data = prepared.prepare(&file).unwrap();
    prepared.index_prepared(&file, data.as_ref()).unwrap();

    let commands = direct.get_commands().unwrap();
    assert_eq!(commands.len(), 2);
    assert_eq!(prepared.get_commands().unwrap(), commands);
    assert_eq!(
        prepared.get_command("app:dup").unwrap()[0].description,
        "Second"
    );

    let cleared = ParsedFile::new(path, b"<?php return 1;".to_vec());
    let data = prepared.prepare(&cleared).unwrap();
    prepared.index_prepared(&cleared, data.as_ref()).unwrap();
    assert!(prepared.get_commands().unwrap().is_empty());
    direct.close().unwrap();
    prepared.close().unwrap();
}

#[test]
fn catalog_merges_and_filters_commands() {
    let dir = tempfile::tempdir().unwrap();
    let index = Arc::new(open(dir.path()));
    index_source(
        &index,
        "/project/src/Command/A.php",
        "<?php\nnamespace App;\n#[AsCommand(name: 'app:alpha', description: 'First')]\nclass A { protected function configure(): void { $this->addArgument('x'); } }\n",
    );
    index_source(
        &index,
        "/project/var/cache/container.xml",
        r#"<container><services><service id="app.alpha"><tag name="console.command" command="app:alpha"/></service>
<service class="App\B"><tag name="console.command" command="app:beta"/></service></services></container>"#,
    );
    index_source(
        &index,
        "/outside/C.php",
        "<?php\n#[AsCommand(name: 'APP:gamma')]\nclass C {}\n",
    );
    let provider = CatalogProvider::new(index.clone(), Some("/project/"));
    let context = Context::background();
    let entries = provider.catalog(&context, "").unwrap();
    let names: Vec<&str> = entries.iter().map(|entry| entry.name.as_str()).collect();
    assert_eq!(names, ["app:alpha", "app:beta", "APP:gamma"]);
    assert_eq!(entries[0].class, "App\\A");
    assert_eq!(entries[0].file_path, "src/Command/A.php");
    assert_eq!(entries[0].file_uri, "file:///project/src/Command/A.php");
    assert_eq!(entries[0].arguments.len(), 1);
    assert_eq!(entries[2].file_path, "");

    let json = serde_json::to_value(&entries[1]).unwrap();
    assert_eq!(
        json,
        serde_json::json!({
            "name": "app:beta",
            "canonical": "app:beta",
            "class": "App\\B",
            "fileUri": "file:///project/var/cache/container.xml",
            "filePath": "var/cache/container.xml",
        })
    );

    let filtered = provider.catalog(&context, "FIRST").unwrap();
    assert_eq!(filtered.len(), 1);
    let globbed = provider
        .catalog_with_request(
            &context,
            &CatalogRequest {
                file_glob: "var/**".to_owned(),
                ..CatalogRequest::default()
            },
        )
        .unwrap();
    assert_eq!(
        globbed
            .iter()
            .map(|entry| entry.name.as_str())
            .collect::<Vec<_>>(),
        ["app:alpha", "app:beta"]
    );
    let request: CatalogRequest =
        serde_json::from_str(r#"{"query":"x","fileGlob":"src/**"}"#).unwrap();
    assert_eq!(request.file_glob, "src/**");

    let cancelled = Context::background().child();
    cancelled.token().cancel();
    assert!(provider.catalog(&cancelled, "").is_err());
    index.close().unwrap();
}

fn helper_php_index(root: &Path, cache: &Path) -> PhpIndex {
    let index = PhpIndex::open(cache, None).unwrap();
    let add = |path: std::path::PathBuf, source: &str| {
        index
            .index(&ParsedFile::new(
                path.to_str().unwrap(),
                source.as_bytes().to_vec(),
            ))
            .unwrap();
    };
    add(
        root.join("vendor/Console.php"),
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
    for (name, source) in [
        (
            "QuestionHelper.php",
            r"<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
class QuestionHelper extends Helper {
    public function getName(): string { return 'question'; }
}",
        ),
        (
            "FormatterHelper.php",
            r#"<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
class FormatterHelper extends Helper {
    public function getName(): string {
        if (random_int(0, 1)) { return "formatter"; }
        return "formatter";
    }
}"#,
        ),
        (
            "AbstractHelper.php",
            r"<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
abstract class AbstractHelper extends Helper {
    public function getName(): string { return 'ignored'; }
}",
        ),
        (
            "ComputedHelper.php",
            r"<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
class ComputedHelper extends Helper {
    public function getName(): string { return self::NAME; }
}",
        ),
    ] {
        add(root.join("src").join(name), source);
    }
    index
}

#[test]
fn helpers_resolve_literal_names_from_concrete_implementations() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let index = helper_php_index(root.path(), cache.path());
    let helpers = helpers(&index);
    assert_eq!(helpers.len(), 2, "{helpers:?}");
    assert_eq!(helpers[0].name, "formatter");
    assert_eq!(helpers[0].class, "App\\FormatterHelper");
    assert_eq!(helpers[1].name, "question");
    assert_eq!(helpers[1].class, "App\\QuestionHelper");
    assert!(helpers[1].file.ends_with("QuestionHelper.php"));
    assert!(!helpers[1].range.is_empty());
    index.close().unwrap();
}

#[test]
fn helper_reference_requires_typed_console_receiver() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let index = helper_php_index(root.path(), cache.path());
    let cases = [
        (
            "helper_set_get",
            "<?php\nuse Symfony\\Component\\Console\\Helper\\HelperSet;\nfunction inspect(HelperSet $helpers): void { $helpers->get('question'); }",
            true,
        ),
        (
            "helper_set_has",
            "<?php\nuse Symfony\\Component\\Console\\Helper\\HelperSet;\nfunction inspect(HelperSet $helpers): void { $helpers->has('question'); }",
            true,
        ),
        (
            "command_helper",
            "<?php\nuse Symfony\\Component\\Console\\Command\\Command;\nfunction inspect(Command $command): void { $command->getHelper('question'); }",
            true,
        ),
        (
            "unrelated_get",
            "<?php\nclass Repository { public function get(string $name): object {} }\nfunction inspect(Repository $repository): void { $repository->get('question'); }",
            false,
        ),
    ];
    for (name, source, valid) in cases {
        let path = root.path().join(format!("{name}.php"));
        let path = path.to_str().unwrap();
        let document = TextDocument::new(swlsp_uriutil::file_uri(path), source, 1);
        let document_root = document.root().unwrap();
        let offset = source.rfind("question").unwrap() + 1;
        let node = document_root
            .node_at(TextSize::from(offset as u32))
            .unwrap();
        let reference = helper_reference_at(&node).expect(name);
        assert_eq!(reference.name, "question");
        assert_eq!(&source[reference.range], "question");
        let context = index.document_context(path, 1, Some(&node), &document_root, source);
        assert_eq!(
            validate_helper_reference(&index, Some(&context), &reference),
            valid,
            "{name}"
        );
    }
    index.close().unwrap();
}

#[test]
fn helper_catalog_refreshes_after_semantic_revision() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let index = helper_php_index(root.path(), cache.path());
    let catalog = HelperCatalog::new(index.clone());
    assert_eq!(catalog.helpers().len(), 2);
    index
        .index(&ParsedFile::new(
            root.path()
                .join("src/DescriptorHelper.php")
                .to_str()
                .unwrap(),
            br"<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
class DescriptorHelper extends Helper {
    public function getName(): string { return 'descriptor'; }
}"
            .to_vec(),
        ))
        .unwrap();
    let helpers = catalog.helpers();
    assert_eq!(helpers.len(), 3);
    assert_eq!(helpers[0].name, "descriptor");
    index.close().unwrap();
}

#[test]
fn console_references_validate_receivers_and_resolve_inputs() {
    let cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(cache.path(), None).unwrap();
    php.index(&ParsedFile::new(
        "/vendor/Console.php",
        br"<?php
namespace Symfony\Component\Console\Input;
interface InputInterface { public function getArgument(string $name): mixed; public function getOption(string $name): mixed; }
namespace Symfony\Component\Console;
class Application { public function find(string $name): object {} }
"
        .to_vec(),
    ))
    .unwrap();
    let commands_dir = tempfile::tempdir().unwrap();
    let commands = open(commands_dir.path());
    let source = r"<?php
namespace App;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Application;
class Cmd {
    protected function configure(): void { $this->addArgument('target')->addOption('force'); }
    protected function execute(InputInterface $input, Application $app): int {
        $input->getArgument('target');
        $input->getOption('force');
        $app->find('app:cmd');
        $other->getArgument('target');
        return 0;
    }
}";
    let path = "/project/src/Cmd.php";
    index_source(
        &commands,
        path,
        "<?php\nnamespace App;\n#[AsCommand(name: 'app:cmd')]\nclass Cmd { protected function configure(): void { $this->addArgument('target')->addOption('force'); } }",
    );
    let document = TextDocument::new(swlsp_uriutil::file_uri(path), source, 1);
    let document_root = document.root().unwrap();
    let at = |needle: &str, nth: usize| {
        let offset = source.match_indices(needle).nth(nth).unwrap().0 + 1;
        document_root
            .node_at(TextSize::from(offset as u32))
            .unwrap()
    };
    let check = |node: swlsp_syntax::SyntaxNode, role: ReferenceRole, valid: bool| {
        let reference = reference_at(&node).unwrap();
        assert_eq!(reference.role, role);
        let context = php.document_context(path, 1, Some(&node), &document_root, source);
        assert_eq!(
            validate_reference(Some(&context), &reference),
            valid,
            "{}",
            reference.name
        );
        (reference, context)
    };
    let (argument, context) = check(at("'target'", 1), ReferenceRole::Argument, true);
    let inputs = inputs_for_reference(Some(&context), &commands, &argument).unwrap();
    assert_eq!(names(&inputs), ["target"]);
    let (option, context) = check(at("'force'", 1), ReferenceRole::Option, true);
    assert_eq!(
        names(&inputs_for_reference(Some(&context), &commands, &option).unwrap()),
        ["force"]
    );
    check(at("'app:cmd'", 0), ReferenceRole::Command, true);
    check(at("'target'", 2), ReferenceRole::Argument, false);
    assert!(reference_at(&at("'target'", 0)).is_none());
    php.close().unwrap();
    commands.close().unwrap();
}
