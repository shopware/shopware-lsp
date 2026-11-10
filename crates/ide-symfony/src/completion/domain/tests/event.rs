use std::sync::Arc;

use swlsp_lsp::CompletionProvider;

use super::*;
use crate::completion::EventCompletionProvider;

const EVENT_DISPATCHER_STUBS: &str = r"<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}
namespace Symfony\Contracts\EventDispatcher;
interface EventDispatcherInterface {
    public function dispatch(object $event, ?string $eventName = null): object;
}";

#[test]
fn event_and_subscriber_method_completion_is_type_aware() {
    let (php_cache, event_cache) = (tempdir(), tempdir());
    let php = php_index(&php_cache);
    index_file(
        &php,
        "/project/vendor/EventDispatcher.php",
        EVENT_DISPATCHER_STUBS,
    );
    let events = Arc::new(swlsp_event::Index::new(event_cache.path(), None).unwrap());
    events.set_php_index(php.clone());
    let source = r"<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
class DomainEvent {}
class Subscriber implements EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return ['app.ready' => ''];
    }
    public function onReady(DomainEvent $event): void {}
}
function publish(EventDispatcherInterface $dispatcher): void {
    $dispatcher->dispatch(new DomainEvent(), '');
}";
    let path = "/project/src/EventFeature.php";
    index_file(&php, path, source);
    index_file(events.as_ref(), path, source);
    let document = document(&format!("file://{path}"), source);
    let provider = EventCompletionProvider::new(events.clone(), Some(php.clone()), None);
    for (needle, label) in [
        ("'app.ready' => ''", "onReady"),
        ("dispatch(new DomainEvent(), '')", "app.ready"),
    ] {
        let offset = source.find(needle).unwrap() + needle.rfind("''").unwrap() + 1;
        let items = provider.get_completions(&ctx(), &node_request(&document, offset));
        require_completion(&items, label);
    }

    let untyped = r"<?php
function publish($messageBus): void {
    $messageBus->dispatch(new \stdClass(), '');
}";
    let untyped_document = super::document("file:///project/src/Message.php", untyped);
    assert!(
        provider
            .get_completions(
                &ctx(),
                &node_request(&untyped_document, untyped.rfind("''").unwrap() + 1)
            )
            .is_empty()
    );
}

#[test]
fn event_configuration_completion_for_yaml_and_xml() {
    let (php_cache, event_cache) = (tempdir(), tempdir());
    let php = php_index(&php_cache);
    let events = Arc::new(swlsp_event::Index::new(event_cache.path(), None).unwrap());
    events.set_php_index(php.clone());
    let listener_source = r"<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
class ConfigListener {
    public function onConfig(): void {}
}
class Subscriber implements EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return ['app.ready' => 'handle'];
    }
    public function handle(): void {}
}";
    index_file(
        &php,
        "/project/vendor/EventSubscriber.php",
        r"<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}",
    );
    let listener_path = "/project/src/ConfigListener.php";
    index_file(&php, listener_path, listener_source);
    index_file(events.as_ref(), listener_path, listener_source);

    for (path, source, method_mark, event_mark) in [
        (
            "/project/config/services.yaml",
            r"services:
    App\ConfigListener:
        tags:
            - { name: kernel.event_listener, event: '', method: '' }
",
            "method: ''",
            "event: ''",
        ),
        (
            "/project/config/services.xml",
            r#"<container><services>
  <service id="App\ConfigListener">
    <tag name="kernel.event_listener" event="" method=""/>
  </service>
</services></container>"#,
            r#"method="""#,
            r#"event="""#,
        ),
    ] {
        index_file(events.as_ref(), path, source);
        let document = document(&format!("file://{path}"), source);
        let provider = EventCompletionProvider::new(events.clone(), Some(php.clone()), None);
        let quote = |mark: &str| mark.find(['\'', '"']).unwrap();
        let method_offset = source.find(method_mark).unwrap() + quote(method_mark) + 1;
        let items = provider.get_completions(&ctx(), &node_request(&document, method_offset));
        require_completion(&items, "onConfig");
        let event_offset = source.find(event_mark).unwrap() + quote(event_mark) + 1;
        let items = provider.get_completions(&ctx(), &node_request(&document, event_offset));
        require_completion(&items, "app.ready");
    }
}
