//! Ports of the Go `internal/event` tests.

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile, PreparingIndexer};
use swlsp_parser_php::query as phpquery;
use swlsp_php::PhpIndex;
use swlsp_php_types::equal_fold;

use crate::{Event, Index, Occurrence, PhpTypeExtension};

fn file(path: &str, source: &str) -> ParsedFile {
    ParsedFile::new(path, source.as_bytes().to_vec())
}

fn require_event(index: &Index, name: &str) -> Event {
    index
        .event(name)
        .expect("event lookup")
        .unwrap_or_else(|| panic!("event {name} not found"))
}

fn assert_listener(event: &Event, class: &str, method: &str) -> Occurrence {
    let listeners = event.listeners();
    listeners
        .iter()
        .find(|listener| listener.class == class && listener.method == method)
        .cloned()
        .unwrap_or_else(|| panic!("listener {class}::{method} missing from {listeners:#?}"))
}

#[test]
fn index_php_subscribers_attributes_constants_and_dispatches() {
    let directory = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(directory.path().join("php"), None).unwrap();
    let index = Index::new(directory.path().join("event"), None).unwrap();
    index.set_php_index(php_index.clone());

    let subscriber = file(
        "/project/src/Subscriber.php",
        r"<?php
namespace App;

use App\Event\OrderPlaced;
use Symfony\Component\EventDispatcher\Attribute\AsEventListener;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Component\HttpKernel\KernelEvents;

class Subscriber implements EventSubscriberInterface
{
    public static function getSubscribedEvents(): array
    {
        return [
            KernelEvents::REQUEST => [
                ['onRequest', 40],
                ['auditRequest'],
            ],
            OrderPlaced::class => 'onOrder',
            'app.manual' => ['onManual', -10],
        ];
    }

    public function onRequest(\Symfony\Component\HttpKernel\Event\RequestEvent $event): void {}
    public function auditRequest(): void {}
    public function onOrder(OrderPlaced $event): void {}
    public function onManual(OrderPlaced $event): void {}
}

#[AsEventListener(event: 'app.attribute', method: 'handleAttribute', priority: 20)]
class AttributeListener
{
    public function handleAttribute(OrderPlaced $event): void {}
}

class MethodListener
{
    #[AsEventListener]
    public function onOrderPlaced(OrderPlaced $event): void {}
}

function publish($eventDispatcher): void
{
    $eventDispatcher->dispatch(new OrderPlaced());
    $eventDispatcher->dispatch(new OrderPlaced(), 'app.named');
    $eventDispatcher->dispatch('app.legacy', new OrderPlaced());
}",
    );
    php_index.index(&subscriber).unwrap();
    index.index(&subscriber).unwrap();

    let placed = require_event(&index, "App\\Event\\OrderPlaced");
    assert_listener(&placed, "App\\Subscriber", "onOrder");
    assert_listener(&placed, "App\\MethodListener", "onOrderPlaced");
    assert!(!placed.dispatches().is_empty());

    let manual = require_event(&index, "app.manual");
    let listener = assert_listener(&manual, "App\\Subscriber", "onManual");
    assert_eq!(listener.priority, "-10");
    assert_eq!(listener.event_type, "App\\Event\\OrderPlaced");

    let attribute = require_event(&index, "app.attribute");
    let listener = assert_listener(&attribute, "App\\AttributeListener", "handleAttribute");
    assert_eq!(listener.priority, "20");

    assert_eq!(
        require_event(&index, "app.named").event_type,
        "App\\Event\\OrderPlaced"
    );
    assert_eq!(
        require_event(&index, "app.legacy").event_type,
        "App\\Event\\OrderPlaced"
    );

    assert!(
        index.event("kernel.request").unwrap().is_none(),
        "constant must remain resolvable after later files"
    );

    let constant_root = tempfile::tempdir().unwrap();
    let constant_path = constant_root
        .path()
        .join("vendor/symfony/http-kernel/KernelEvents.php");
    std::fs::create_dir_all(constant_path.parent().unwrap()).unwrap();
    let constant_source = r"<?php
namespace Symfony\Component\HttpKernel;
final class KernelEvents
{
    public const REQUEST = 'kernel.request';
}";
    std::fs::write(&constant_path, constant_source).unwrap();
    php_index
        .index(&file(constant_path.to_str().unwrap(), constant_source))
        .unwrap();
    let request = require_event(&index, "kernel.request");
    assert_listener(&request, "App\\Subscriber", "onRequest");
    assert_listener(&request, "App\\Subscriber", "auditRequest");
    assert_eq!(
        request.event_type,
        "Symfony\\Component\\HttpKernel\\Event\\RequestEvent"
    );
}

#[test]
fn index_service_listener_tags() {
    let directory = tempfile::tempdir().unwrap();
    let index = Index::new(directory.path(), None).unwrap();
    index
        .index(&file(
            "/project/config/services.php",
            r"<?php
use App\Listener\ConfigListener;
$services->set(ConfigListener::class)
    ->tag('kernel.event_listener', [
        'event' => 'app.php',
        'method' => 'onPHP',
        'priority' => 100,
    ]);",
        ))
        .unwrap();
    index
        .index(&file(
            "/project/config/services.yaml",
            r"services:
    App\Listener\YamlListener:
        tags:
            - { name: kernel.event_listener, event: app.yaml, method: onYaml, priority: 10 }
",
        ))
        .unwrap();
    index
        .index(&file(
            "/project/config/services.xml",
            r#"<container><services>
  <service id="app.xml_listener" class="App\Listener\XmlListener">
    <tag name="kernel.event_listener" event="app.xml" method="onXml" priority="-5"/>
  </service>
</services></container>"#,
        ))
        .unwrap();

    let listener = assert_listener(
        &require_event(&index, "app.php"),
        "App\\Listener\\ConfigListener",
        "onPHP",
    );
    assert_eq!(listener.priority, "100");
    let listener = assert_listener(
        &require_event(&index, "app.yaml"),
        "App\\Listener\\YamlListener",
        "onYaml",
    );
    assert_eq!(listener.priority, "10");
    let listener = assert_listener(
        &require_event(&index, "app.xml"),
        "App\\Listener\\XmlListener",
        "onXml",
    );
    assert_eq!(listener.priority, "-5");
}

#[test]
fn event_dispatcher_call_returns_concrete_event_type() {
    let directory = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(directory.path(), None).unwrap();
    php_index.register_type_extension(Arc::new(PhpTypeExtension::new()));
    php_index
        .index(&file(
            "/project/vendor/EventDispatcher.php",
            r"<?php
namespace Symfony\Contracts\EventDispatcher;
interface EventDispatcherInterface {
    public function dispatch(object $event, ?string $eventName = null): object;
}",
        ))
        .unwrap();
    let source = r"<?php
namespace App;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
class DomainEvent {}
function publish(EventDispatcherInterface $dispatcher): void {
    $modern = $dispatcher->dispatch(new DomainEvent(), 'app.modern');
    $legacy = $dispatcher->dispatch('app.legacy', new DomainEvent());
}";
    let parsed = file("/project/src/Publisher.php", source);
    let root = parsed.syntax_tree().unwrap().root();
    let document = php_index.analyze_document(parsed.path(), 1, &root, source);
    let mut dispatches = 0;
    for call in phpquery::calls(&root, &[]) {
        if !equal_fold(&phpquery::call_method_name(&call), "dispatch") {
            continue;
        }
        dispatches += 1;
        assert_eq!(document.type_of(&call).ty.to_string(), "App\\DomainEvent");
    }
    assert_eq!(dispatches, 2);
}

#[test]
fn index_skips_messenger_dispatches() {
    let directory = tempfile::tempdir().unwrap();
    let index = Index::new(directory.path(), None).unwrap();
    index
        .index(&file(
            "/project/src/Publisher.php",
            r"<?php
namespace App;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
use Symfony\Component\Messenger\MessageBusInterface;
class DomainEvent {}
class Command {}
function publish(
    EventDispatcherInterface $eventDispatcher,
    MessageBusInterface $dispatcher,
): void {
    $eventDispatcher->dispatch (new DomainEvent());
    $dispatcher->dispatch(new Command());
}",
        ))
        .unwrap();
    let events = index.events().unwrap();
    assert_eq!(events.len(), 1);
    assert_eq!(events[0].name, "App\\DomainEvent");
}

#[test]
fn index_clears_persisted_path_after_candidate_removal() {
    let directory = tempfile::tempdir().unwrap();
    let path = "/project/src/Listener.php";
    let index = Index::new(directory.path(), None).unwrap();
    index
        .index(&file(
            path,
            r"<?php
namespace App;
#[\Symfony\Component\EventDispatcher\Attribute\AsEventListener(event: 'app.ready')]
class Listener { public function __invoke(): void {} }",
        ))
        .unwrap();
    assert!(index.event("app.ready").unwrap().is_some());
    index.close().unwrap();
    drop(index);

    let reopened = Index::new(directory.path(), None).unwrap();
    reopened
        .index(&file(path, "<?php namespace App; class Listener {}"))
        .unwrap();
    assert!(reopened.event("app.ready").unwrap().is_none());
}

#[test]
fn prepared_indexing_matches_direct_indexing() {
    let directory = tempfile::tempdir().unwrap();
    let path = "/project/config/services.yaml";
    let source = r"services:
    App\Listener\YamlListener:
        tags:
            - { name: kernel.event_listener, event: app.yaml, method: onYaml, priority: 10 }
";
    let direct = Index::new(directory.path().join("direct"), None).unwrap();
    direct.index(&file(path, source)).unwrap();
    let prepared = Index::new(directory.path().join("prepared"), None).unwrap();
    let parsed = file(path, source);
    let data = prepared.prepare(&parsed).unwrap();
    prepared.index_prepared(&parsed, data.as_ref()).unwrap();

    let events = direct.events().unwrap();
    assert!(!events.is_empty());
    assert_eq!(prepared.events().unwrap(), events);

    let cleared = file(path, "services: {}\n");
    let data = prepared.prepare(&cleared).unwrap();
    prepared.index_prepared(&cleared, data.as_ref()).unwrap();
    assert!(prepared.events().unwrap().is_empty());
    direct.close().unwrap();
    prepared.close().unwrap();
}

#[test]
fn event_catalog_is_shared_until_records_change() {
    let directory = tempfile::tempdir().unwrap();
    let index = Index::new(directory.path(), None).unwrap();
    let listener = |event: &str| {
        format!(
            "<?php\nnamespace App;\n#[\\Symfony\\Component\\EventDispatcher\\Attribute\\AsEventListener(event: '{event}')]\nclass Listener {{ public function __invoke(): void {{}} }}"
        )
    };
    index
        .index(&file("/project/src/Listener.php", &listener("app.first")))
        .unwrap();
    let first = index.events_view().unwrap();
    assert!(Arc::ptr_eq(&first, &index.events_view().unwrap()));
    assert!(index.event("app.first").unwrap().is_some());

    index
        .index(&file("/project/src/Listener.php", &listener("app.second")))
        .unwrap();
    let second = index.events_view().unwrap();
    assert!(!Arc::ptr_eq(&first, &second));
    assert!(index.event("app.first").unwrap().is_none());
    assert!(index.event("app.second").unwrap().is_some());
    index.close().unwrap();
}
