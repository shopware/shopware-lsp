//! Symfony service and compiler-pass generator tests.

use std::sync::Arc;

use serde_json::Value;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, CodeActionRequest, CommandProvider, Context};
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;
use swlsp_uriutil::file_uri;

use super::*;
use swlsp_ide::codeaction::testing::{path_string, request_at};

fn php_index(files: &[(&str, &str)]) -> (tempfile::TempDir, PhpIndex) {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    for (path, source) in files {
        index
            .index(&ParsedFile::new(
                (*path).to_owned(),
                source.as_bytes().to_vec(),
            ))
            .unwrap();
    }
    (dir, index)
}

fn code_action_request(uri: &str, source: &str, needle: &str) -> CodeActionRequest {
    let offset = source.find(needle).unwrap_or(0) as u32;
    request_at(uri, source, offset, offset)
}

const BUNDLE_INTERFACE: &str =
    "<?php\nnamespace Symfony\\Component\\HttpKernel\\Bundle;\ninterface BundleInterface {}\n";

#[test]
fn code_actions_expose_interactive_generators() {
    let bundle = "<?php\nnamespace App;\nclass AppBundle implements \\Symfony\\Component\\HttpKernel\\Bundle\\BundleInterface\n{\n    public function boot(): void {}\n}\n";
    let (_dir, index) = php_index(&[
        ("/vendor/BundleInterface.php", BUNDLE_INTERFACE),
        ("/project/src/AppBundle.php", bundle),
    ]);
    let provider = SymfonyGeneratorProvider::new(Some(index), None);
    let uri = "file:///project/src/AppBundle.php";
    let actions = provider.get_code_actions(
        &Context::background(),
        &code_action_request(uri, bundle, "boot"),
    );
    assert_eq!(actions.len(), 2);
    assert_eq!(actions[0].title, "Generate Symfony service");
    let command = actions[0].command.as_ref().unwrap();
    assert_eq!(command.command, GENERATE_SYMFONY_SERVICE_ACTION);
    assert_eq!(
        command.arguments,
        vec![Value::from(uri), Value::from("App\\AppBundle")]
    );
    assert_eq!(actions[1].title, "Symfony: Create CompilerPass");
    assert_eq!(
        actions[1].command.as_ref().unwrap().command,
        CREATE_COMPILER_PASS_ACTION
    );

    let class_actions = provider.get_code_actions(
        &Context::background(),
        &code_action_request(uri, bundle, "AppBundle"),
    );
    assert_eq!(class_actions.len(), 1);
    assert_eq!(class_actions[0].title, "Symfony: Create CompilerPass");
}

struct Fixture {
    _dirs: Vec<tempfile::TempDir>,
    provider: SymfonyGeneratorProvider,
}

fn fixture() -> Fixture {
    let (php_dir, index) = php_index(&[
        (
            "/vendor/EventSubscriberInterface.php",
            "<?php\nnamespace Symfony\\Component\\EventDispatcher;\ninterface EventSubscriberInterface {}\n",
        ),
        (
            "/project/src/LoggerInterface.php",
            "<?php\nnamespace App;\ninterface LoggerInterface {}\n",
        ),
        (
            "/project/src/Logger.php",
            "<?php\nnamespace App;\nclass Logger implements LoggerInterface {}\n",
        ),
        (
            "/project/src/Consumer.php",
            "<?php\nnamespace App;\nclass Consumer implements \\Symfony\\Component\\EventDispatcher\\EventSubscriberInterface\n{\n    public function __construct(LoggerInterface $logger, string $channel) {}\n    public function setLogger(LoggerInterface $logger): void {}\n}\n",
        ),
    ]);
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root_path = path_string(root.path());
    let services = Arc::new(ServiceIndex::new(&root_path, cache.path(), None).unwrap());
    services.set_php_index(Some(index.clone()));
    services
        .index(&ParsedFile::new(
            format!("{root_path}/services.yaml"),
            b"services:\n  app.logger:\n    class: App\\Logger\n  app.secondary_logger:\n    class: App\\Logger\n".to_vec(),
        ))
        .unwrap();
    Fixture {
        _dirs: vec![php_dir, root, cache],
        provider: SymfonyGeneratorProvider::new(Some(index), Some(services)),
    }
}

fn run_command(provider: &SymfonyGeneratorProvider, name: &str, args: Value) -> Value {
    let ctx = Context::background();
    let commands = provider.commands(&ctx);
    (commands[name])(&ctx, Some(&args)).unwrap()
}

#[test]
fn service_generator_infers_arguments_calls_and_tags() {
    let fixture = fixture();
    let result: SymfonyServiceGenerationResponse = serde_json::from_value(run_command(
        &fixture.provider,
        GENERATE_SYMFONY_SERVICE_COMMAND,
        serde_json::json!({"className": "App\\Consumer", "output": "yaml", "classAsId": true}),
    ))
    .unwrap();
    assert_eq!(result.language, "yaml");
    assert!(result.content.contains("App\\Consumer:"));
    assert!(
        result.content.contains("arguments: ['@app.logger', '@?']"),
        "{}",
        result.content
    );
    assert!(result.content.contains("- [setLogger, ['@app.logger']]"));
    assert!(
        result
            .content
            .contains("- { name: kernel.event_subscriber }")
    );

    for output in ["xml", "fluent", "php-array"] {
        let generated = fixture
            .provider
            .generate_service(
                &Context::background(),
                &SymfonyServiceGenerationRequest {
                    class_name: "App\\Consumer".to_owned(),
                    output: output.to_owned(),
                    class_as_id: false,
                    service_id: "app.consumer".to_owned(),
                    ..SymfonyServiceGenerationRequest::default()
                },
            )
            .unwrap();
        let content = &generated.content;
        assert!(!content.is_empty());
        match output {
            "xml" => {
                assert!(content.contains("<service id=\"app.consumer\" class=\"App\\Consumer\">"));
                assert!(content.contains("<argument type=\"service\" id=\"app.logger\"/>"));
            }
            "fluent" => {
                assert!(content.contains("$services->set('app.consumer', \\App\\Consumer::class)"));
                assert!(content.contains("->tag('kernel.event_subscriber');"));
            }
            _ => {
                assert!(content.contains("'app.consumer' => ["));
                assert!(content.contains("'class' => \\App\\Consumer::class"));
            }
        }
    }
}

#[test]
fn service_generator_uses_unsaved_php_overlay() {
    let fixture = fixture();
    let source = "<?php\nnamespace App;\nclass UnsavedConsumer\n{\n    public function __construct(LoggerInterface $logger) {}\n}\n";
    let result = fixture
        .provider
        .generate_service(
            &Context::background(),
            &SymfonyServiceGenerationRequest {
                class_name: "App\\UnsavedConsumer".to_owned(),
                output: "yaml".to_owned(),
                class_as_id: true,
                file_uri: "file:///project/src/UnsavedConsumer.php".to_owned(),
                source: source.to_owned(),
                version: 7,
                ..SymfonyServiceGenerationRequest::default()
            },
        )
        .unwrap();
    assert!(result.content.contains("App\\UnsavedConsumer:"));
    assert!(
        result.content.contains("arguments: ['@app.logger']"),
        "{}",
        result.content
    );
}

#[test]
fn definition_collector_generates_multiple_classes_and_choices() {
    let fixture = fixture();
    let result = fixture
        .provider
        .collect_service_definitions(
            &Context::background(),
            &SymfonyServiceDefinitionCollectionRequest {
                class_names: "App\\Consumer, App\\Logger, App\\Missing".to_owned(),
                output: "yaml".to_owned(),
                class_as_id: true,
            },
        )
        .unwrap();
    assert_eq!(result.definitions.len(), 3);
    let consumer = &result.definitions[0];
    assert_eq!(consumer.class_name, "App\\Consumer");
    assert_eq!(consumer.language, "yaml");
    assert!(consumer.error.is_empty());
    assert_eq!(consumer.suggestions.len(), 2);
    assert_eq!(consumer.suggestions[0].method, "__construct");
    assert_eq!(consumer.suggestions[0].parameter, "logger");
    assert_eq!(consumer.suggestions[0].ty, "App\\LoggerInterface");
    assert_eq!(
        consumer.suggestions[0].services,
        vec!["app.logger".to_owned(), "app.secondary_logger".to_owned()]
    );
    assert!(consumer.content.contains("App\\Consumer:"));
    assert!(
        consumer
            .content
            .contains("arguments: ['@app.logger', '@?']")
    );
    assert!(
        consumer
            .content
            .contains("# Possible services per parameter:")
    );
    assert!(
        consumer
            .content
            .contains("$logger [App\\LoggerInterface] => app.logger, app.secondary_logger")
    );
    assert!(
        consumer
            .content
            .contains("$logger (setLogger) [App\\LoggerInterface]")
    );

    let logger = &result.definitions[1];
    assert_eq!(logger.class_name, "App\\Logger");
    assert_eq!(logger.content, "App\\Logger: ~");
    assert!(logger.suggestions.is_empty());

    let missing = &result.definitions[2];
    assert_eq!(missing.class_name, "App\\Missing");
    assert!(missing.error.contains("was not found"));
    assert!(missing.content.is_empty());

    // The command response omits empty fields.
    let encoded = run_command(
        &fixture.provider,
        COLLECT_SYMFONY_SERVICES_COMMAND,
        serde_json::json!({"classNames": "App\\Missing", "output": "xml"}),
    );
    assert_eq!(
        encoded,
        serde_json::json!({"definitions": [{
            "className": "App\\Missing",
            "language": "xml",
            "error": "PHP class \"App\\\\Missing\" was not found"
        }]})
    );
}

#[test]
fn compiler_pass_generator_creates_file_and_registers_bundle() {
    let bundle = "<?php\n\nnamespace Acme\\Demo;\n\nuse Symfony\\Component\\HttpKernel\\Bundle\\Bundle;\n\nclass DemoBundle extends Bundle\n{\n}\n";
    let (_dir, index) = php_index(&[
        ("/vendor/BundleInterface.php", BUNDLE_INTERFACE),
        (
            "/vendor/Bundle.php",
            "<?php\nnamespace Symfony\\Component\\HttpKernel\\Bundle;\nclass Bundle implements BundleInterface {}\n",
        ),
        ("/project/DemoBundle.php", bundle),
    ]);
    let root = tempfile::tempdir().unwrap();
    let root_path = path_string(root.path());
    let bundle_path = format!("{root_path}/DemoBundle.php");
    let provider = SymfonyGeneratorProvider::new(Some(index), None);
    let value = run_command(
        &provider,
        CREATE_COMPILER_PASS_COMMAND,
        serde_json::to_value(CompilerPassCreationRequest {
            bundle_uri: file_uri(&bundle_path),
            bundle_class: "Acme\\Demo\\DemoBundle".to_owned(),
            class_name: "CollectWidgetsPass".to_owned(),
            source: bundle.to_owned(),
            version: 4,
        })
        .unwrap(),
    );
    let result: CompilerPassCreationResponse = serde_json::from_value(value).unwrap();
    assert_eq!(
        result.file_uri,
        file_uri(&format!(
            "{root_path}/DependencyInjection/Compiler/CollectWidgetsPass.php"
        ))
    );
    assert!(
        result
            .file_content
            .contains("namespace Acme\\Demo\\DependencyInjection\\Compiler;")
    );
    assert!(
        result
            .file_content
            .contains("class CollectWidgetsPass implements CompilerPassInterface")
    );
    let content = &result.bundle_content;
    assert!(content.contains("use Acme\\Demo\\DependencyInjection\\Compiler\\CollectWidgetsPass;"));
    assert!(content.contains("use Symfony\\Component\\DependencyInjection\\ContainerBuilder;"));
    assert!(content.contains("public function build(ContainerBuilder $container): void"));
    assert!(content.contains("parent::build($container);"));
    assert!(content.contains("$container->addCompilerPass(new CollectWidgetsPass());"));
    assert!(swlsp_parser_php::parse(content).errors.is_empty());
    assert!(
        swlsp_parser_php::parse(&result.file_content)
            .errors
            .is_empty()
    );
}

#[test]
fn compiler_pass_generator_uses_existing_build_parameter() {
    let source = "<?php\n\nnamespace App;\n\nuse Symfony\\Component\\DependencyInjection\\ContainerBuilder;\n\nclass AppBundle\n{\n    public function build(ContainerBuilder $builder): void\n    {\n        parent::build($builder);\n    }\n}\n";
    let updated = add_compiler_pass_to_bundle(
        "file:///project/src/AppBundle.php",
        source,
        1,
        "App\\AppBundle",
        "App\\DependencyInjection\\Compiler\\CollectPass",
        "CollectPass",
    )
    .unwrap();
    assert!(updated.contains("use App\\DependencyInjection\\Compiler\\CollectPass;"));
    assert!(updated.contains("$builder->addCompilerPass(new CollectPass());"));
    assert_eq!(
        updated
            .matches("use Symfony\\Component\\DependencyInjection\\ContainerBuilder;")
            .count(),
        1
    );
    assert!(swlsp_parser_php::parse(&updated).errors.is_empty());
}
