//! Ports of the Go `internal/messenger` tests.

use swlsp_indexer::{Indexer, ParsedFile, PreparingIndexer};
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};

use crate::{Index, Message, Occurrence, ReferenceRole, handler_method_reference_at, reference_at};

fn file(path: &str, source: &str) -> ParsedFile {
    ParsedFile::new(path, source.as_bytes().to_vec())
}

fn require_message(index: &Index, name: &str) -> Message {
    index
        .message(name)
        .expect("message lookup")
        .unwrap_or_else(|| panic!("message {name} not found"))
}

fn assert_handler(message: &Message, class_name: &str, method_name: &str) -> Occurrence {
    let handlers = message.handlers();
    handlers
        .iter()
        .find(|handler| handler.class == class_name && handler.method == method_name)
        .cloned()
        .unwrap_or_else(|| {
            panic!(
                "handler {class_name}::{method_name} for {} not found; got {handlers:#?}",
                message.name
            )
        })
}

fn open_indexes() -> (tempfile::TempDir, PhpIndex, Index) {
    let directory = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(directory.path().join("php"), None).unwrap();
    let index = Index::new(directory.path().join("messenger"), None).unwrap();
    index.set_php_index(php_index.clone());
    (directory, php_index, index)
}

#[test]
fn index_modern_handlers_and_typed_dispatches() {
    let (_directory, php_index, index) = open_indexes();
    php_index
        .index(&file(
            "/project/vendor/MessageBusInterface.php",
            r"<?php
namespace Symfony\Component\Messenger;
interface MessageBusInterface {
    public function dispatch(object $message, array $stamps = []): object;
}",
        ))
        .unwrap();
    let source = r"<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
use Symfony\Component\Messenger\MessageBusInterface;

class FirstMessage {}
class SecondMessage {}
class ThirdMessage {}

#[AsMessageHandler]
final class UnionHandler {
    public function __invoke(FirstMessage|SecondMessage $message): void {}
}

#[AsMessageHandler(handles: ThirdMessage::class, method: 'handle')]
final class ExplicitHandler {
    public function handle(ThirdMessage $message): void {}
}

final class MethodHandler {
    #[AsMessageHandler]
    public function consume(ThirdMessage $message): void {}
}

function publish(MessageBusInterface $messageBus, FirstMessage $message): void {
    $messageBus->dispatch(new FirstMessage());
    $messageBus->dispatch($message);
}";
    let parsed = file("/project/src/Messenger.php", source);
    php_index.index(&parsed).unwrap();
    index.index(&parsed).unwrap();

    let first = require_message(&index, "App\\FirstMessage");
    assert_handler(&first, "App\\UnionHandler", "__invoke");
    assert_eq!(first.dispatches().len(), 2);

    let second = require_message(&index, "App\\SecondMessage");
    assert_handler(&second, "App\\UnionHandler", "__invoke");

    let third = require_message(&index, "App\\ThirdMessage");
    assert_handler(&third, "App\\ExplicitHandler", "handle");
    assert_handler(&third, "App\\MethodHandler", "consume");
}

#[test]
fn index_service_handler_tags_across_formats() {
    let (_directory, php_index, index) = open_indexes();
    let handler = file(
        "/project/src/InferredHandler.php",
        r"<?php
namespace App;
class InferredMessage {}
class InferredHandler {
    public function __invoke(InferredMessage $message): void {}
}",
    );
    php_index.index(&handler).unwrap();
    index.index(&handler).unwrap();

    for parsed in [
        file(
            "/project/config/services.php",
            r"<?php
use App\PhpHandler;
use App\PhpMessage;
$services->set(PhpHandler::class)
    ->tag('messenger.message_handler', [
        'handles' => PhpMessage::class,
        'method' => 'process',
        'priority' => 10,
    ]);",
        ),
        file(
            "/project/config/services.yaml",
            r"services:
  App\YamlHandler:
    tags:
      - { name: messenger.message_handler, handles: App\YamlMessage, method: consume }
  App\InferredHandler:
    tags: [messenger.message_handler]
",
        ),
        file(
            "/project/config/services.xml",
            r#"<container><services>
  <service id="App\XmlHandler">
    <tag name="messenger.message_handler" handles="App\XmlMessage" method="run"/>
  </service>
</services></container>"#,
        ),
    ] {
        index.index(&parsed).unwrap();
    }

    let php = assert_handler(
        &require_message(&index, "App\\PhpMessage"),
        "App\\PhpHandler",
        "process",
    );
    assert_eq!(php.priority, "10");
    assert_handler(
        &require_message(&index, "App\\YamlMessage"),
        "App\\YamlHandler",
        "consume",
    );
    assert_handler(
        &require_message(&index, "App\\XmlMessage"),
        "App\\XmlHandler",
        "run",
    );
    assert_handler(
        &require_message(&index, "App\\InferredMessage"),
        "App\\InferredHandler",
        "__invoke",
    );
}

#[test]
fn index_restores_and_removes_stale_messenger_data() {
    let directory = tempfile::tempdir().unwrap();
    let path = "/project/src/Handler.php";
    let source = r"<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
class Message {}
#[AsMessageHandler]
class Handler {
    public function __invoke(Message $message): void {}
}";
    let index = Index::new(directory.path(), None).unwrap();
    index.index(&file(path, source)).unwrap();
    assert!(
        index
            .message_names()
            .unwrap()
            .contains(&"App\\Message".to_owned())
    );
    index.close().unwrap();
    drop(index);

    let restored = Index::new(directory.path(), None).unwrap();
    assert!(
        restored
            .message_names()
            .unwrap()
            .contains(&"App\\Message".to_owned())
    );
    restored
        .index(&file(path, "<?php namespace App; class Handler {}"))
        .unwrap();
    assert!(restored.message_names().unwrap().is_empty());
    restored.close().unwrap();
}

#[test]
fn prepared_indexing_matches_direct_indexing() {
    let directory = tempfile::tempdir().unwrap();
    let path = "/project/src/Handler.php";
    let source = r"<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
class Message {}
#[AsMessageHandler(handles: Message::class, method: 'run')]
class Handler {
    public function run(Message $message): void {}
}";
    let direct = Index::new(directory.path().join("direct"), None).unwrap();
    direct.index(&file(path, source)).unwrap();
    let prepared = Index::new(directory.path().join("prepared"), None).unwrap();
    let parsed = file(path, source);
    let data = prepared.prepare(&parsed).unwrap();
    prepared.index_prepared(&parsed, data.as_ref()).unwrap();

    let messages = direct.messages().unwrap();
    assert!(!messages.is_empty());
    assert_eq!(prepared.messages().unwrap(), messages);

    let cleared = file(path, "<?php namespace App; class Handler {}");
    let data = prepared.prepare(&cleared).unwrap();
    prepared.index_prepared(&cleared, data.as_ref()).unwrap();
    assert!(prepared.messages().unwrap().is_empty());
    direct.close().unwrap();
    prepared.close().unwrap();
}

fn node_at(root: &SyntaxNode, offset: usize) -> SyntaxNode {
    root.node_at(TextSize::from(offset as u32)).unwrap()
}

#[test]
fn handler_method_reference_at_supports_return_and_yield_forms() {
    let source = r"<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface
{
    public static function getHandledMessages(): iterable
    {
        yield FirstMessage::class => ['method' => 'handleFirst'];
        yield SecondMessage::class => 'handleSecond';
        return [
            ThirdMessage::class => 'handleThird',
            FourthMessage::class => ['method' => 'handleFourth'],
        ];
    }

    public function handleFirst(): void {}
    public function handleSecond(): void {}
    public function handleThird(): void {}
    public function handleFourth(): void {}
}";
    let root = swlsp_parser_php::parse(source).tree.root();
    for name in ["handleFirst", "handleSecond", "handleThird", "handleFourth"] {
        let offset = source.find(&format!("'{name}'")).unwrap() + 2;
        let reference = handler_method_reference_at(None, &root, &node_at(&root, offset))
            .unwrap_or_else(|| panic!("{name}"));
        assert_eq!(reference.name, name);
        assert_eq!(reference.class, "App\\Subscriber");
    }
}

#[test]
fn handler_method_reference_at_rejects_messages_and_nested_closures() {
    let source = r"<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface
{
    public static function getHandledMessages(): iterable
    {
        yield 'App\Message' => ['method' => 'handle'];
        $factory = static function (): array {
            return ['App\Message' => 'nested'];
        };
    }

    public function handle(): void {}
}";
    let root = swlsp_parser_php::parse(source).tree.root();
    for value in ["App\\Message", "nested"] {
        let offset = source.rfind(&format!("'{value}'")).unwrap() + 2;
        assert!(
            handler_method_reference_at(None, &root, &node_at(&root, offset)).is_none(),
            "{value}"
        );
    }
}

#[test]
fn public_handler_methods_filters_non_public_and_special_methods() {
    // The provider-level tests exercise the semantic index. Keep this unit
    // test focused on the reference detector's class shape.
    let source = r"<?php
class Other {
    public static function getHandledMessages(): iterable {
        return [Message::class => 'handle'];
    }
}";
    let root = swlsp_parser_php::parse(source).tree.root();
    let literals = swlsp_parser_php::query::nodes(&root, &[swlsp_parser_php::syntax::PHP_STRING]);
    assert_eq!(literals.len(), 1);
    assert!(handler_method_reference_at(None, &root, &literals[0]).is_none());
}

#[test]
fn reference_at_recognizes_modern_handler_configuration() {
    struct Case {
        path: &'static str,
        source: &'static str,
        marker: &'static str,
        role: ReferenceRole,
        name: &'static str,
        class_name: &'static str,
    }
    let cases = [
        Case {
            path: "/project/src/Handler.php",
            source: r"<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
#[AsMessageHandler(handles: Message::class, method: 'consume')]
class Handler { public function consume(Message $message): void {} }",
            marker: "Message::class",
            role: ReferenceRole::Message,
            name: "App\\Message",
            class_name: "App\\Handler",
        },
        Case {
            path: "/project/config/services.yaml",
            source: r"services:
  App\Handler:
    tags:
      - { name: messenger.message_handler, handles: App\Message, method: consume }
",
            marker: "App\\Message",
            role: ReferenceRole::Message,
            name: "App\\Message",
            class_name: "App\\Handler",
        },
        Case {
            path: "/project/config/services.xml",
            source: r#"<container><services>
  <service id="App\Handler">
    <tag name="messenger.message_handler" handles="App\Message" method="consume"/>
  </service>
</services></container>"#,
            marker: "consume",
            role: ReferenceRole::HandlerMethod,
            name: "consume",
            class_name: "App\\Handler",
        },
    ];
    for case in cases {
        let root = if case.path.ends_with(".php") {
            swlsp_parser_php::parse(case.source).tree.root()
        } else if case.path.ends_with(".yaml") {
            swlsp_parser_yaml::parse(case.source).tree.root()
        } else {
            swlsp_parser_xml::parse(case.source).tree.root()
        };
        let offset = case.source.find(case.marker).unwrap() + 1;
        let reference = reference_at(None, case.path, &root, &node_at(&root, offset))
            .unwrap_or_else(|| panic!("{}", case.path));
        assert_eq!(reference.role, case.role, "{}", case.path);
        assert_eq!(reference.name, case.name, "{}", case.path);
        assert_eq!(reference.class, case.class_name, "{}", case.path);
    }
}
