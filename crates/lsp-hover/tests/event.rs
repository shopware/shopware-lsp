mod common;

use std::sync::Arc;

use common::*;
use swlsp_event::Index;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::EventHoverProvider;

#[test]
fn event_hover_shows_type_listeners_and_dispatches() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(
        &cache.path().join("php"),
        &[(
            "/project/vendor/EventDispatcher.php",
            r"<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}
namespace Symfony\Contracts\EventDispatcher;
interface EventDispatcherInterface {
    public function dispatch(object $event, ?string $eventName = null): object;
}",
        )],
    );
    let events = Arc::new(Index::new(cache.path().join("events"), None).unwrap());
    events.set_php_index(php.clone());
    let source = r"<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
class DomainEvent {}
class Subscriber implements EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return ['app.ready' => ['onReady', 20]];
    }
    public function onReady(DomainEvent $event): void {}
}
function publish(EventDispatcherInterface $dispatcher): void {
    $dispatcher->dispatch(new DomainEvent(), 'app.ready');
}";
    let path = "/project/src/Events.php";
    index_file(&php, path, source);
    index_file(events.as_ref(), path, source);
    let document = TextDocument::new(format!("file://{path}"), source, 1);
    let request = request_at(document, source.rfind("'app.ready'").unwrap() + 2);
    let result = hover(
        &EventHoverProvider::new("/project", Some(events), Some(php), None),
        &request,
    )
    .expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Symfony event"));
    assert!(value.contains("App\\DomainEvent"));
    assert!(value.contains("App\\Subscriber::onReady()"));
    assert!(value.contains("priority `20`"));
    assert!(value.contains("1 indexed dispatch site"));
}
