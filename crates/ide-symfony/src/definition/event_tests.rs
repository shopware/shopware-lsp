use std::sync::Arc;

use swlsp_uriutil::file_uri;

use super::*;
use crate::definition::php_common::test_support::*;

#[test]
fn event_definitions_navigate_to_event_class_and_listener_method() {
    let root = tempfile::tempdir().unwrap();
    let path = p(&root.path().join("src/Events.php"));
    let source = r"<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
class DomainEvent {}
class Subscriber implements EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return ['app.ready' => 'onReady'];
    }
    public function onReady(DomainEvent $event): void {}
}
function publish(EventDispatcherInterface $dispatcher): void {
    $dispatcher->dispatch(new DomainEvent(), 'app.ready');
}";
    write(std::path::Path::new(&path), source);
    let php = php_index(&root.path().join("php"));
    index(
        &php,
        &p(&root.path().join("vendor/EventDispatcher.php")),
        r"<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}
namespace Symfony\Contracts\EventDispatcher;
interface EventDispatcherInterface {
    public function dispatch(object $event, ?string $eventName = null): object;
}",
    );
    let events = Arc::new(swlsp_event::Index::new(root.path().join("events"), None).unwrap());
    events.set_php_index(php.clone());
    index(&php, &path, source);
    index(events.as_ref(), &path, source);

    let document = document(&file_uri(&path), source);
    let provider = EventDefinitionProvider::new(Some(events), Some(php), None);

    let method_offset = index_of(source, "'onReady'") + 2;
    let method_locations = definitions(&provider, &request_at(&document, method_offset));
    assert_eq!(method_locations.len(), 1);
    assert_eq!(method_locations[0].range.start.line, 9);

    let event_offset = last_index_of(source, "'app.ready'") + 2;
    let event_locations = definitions(&provider, &request_at(&document, event_offset));
    assert!(event_locations.len() >= 2, "{event_locations:?}");
    let lines: Vec<u32> = event_locations
        .iter()
        .map(|location| {
            assert_eq!(location.uri, file_uri(&path));
            location.range.start.line
        })
        .collect();
    assert!(lines.contains(&4), "{lines:?}");
    assert!(lines.contains(&9), "{lines:?}");
}
