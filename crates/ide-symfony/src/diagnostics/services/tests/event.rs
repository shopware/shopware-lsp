use serde_json::json;
use swlsp_event::Index;
use swlsp_lsp::fixture;
use swlsp_uriutil::file_uri;

use super::*;
use crate::diagnostics::services::EventAnalyzer;
use crate::diagnostics::test_support::check_problems;

fn event_index(dir: &Path, php: &PhpIndex) -> Arc<Index> {
    let index = Index::new(dir, None).unwrap();
    index.set_php_index(php.clone());
    Arc::new(index)
}

#[test]
fn event_diagnostics_report_missing_events_and_listener_methods() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        &dir.path().join("php"),
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
    let events = event_index(&dir.path().join("events"), &php);
    let fixture = r"<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
class DomainEvent {}
class Subscriber implements EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return ['app.ready' => '{|symfony.event.listener_method.missing:onRady|}'];
    }
    public function onReady(DomainEvent $event): void {}
}
function publish(EventDispatcherInterface $dispatcher): void {
    $dispatcher->dispatch(new DomainEvent(), 'app.ready');
    $dispatcher->dispatch(new DomainEvent(), '{|symfony.event.missing:app.raedy|}');
}";
    let (source, _) = fixture::parse(fixture);
    let path = p(&dir.path().join("Events.php"));
    std::fs::write(&path, &source).unwrap();
    let parsed = ParsedFile::new(path.as_str(), source.as_bytes().to_vec());
    php.index(&parsed).unwrap();
    events.index(&parsed).unwrap();

    let analyzer = EventAnalyzer::new(Some(events), Some(php), None);
    let (_, problems) = check_problems(&analyzer, &format!("file://{path}"), fixture);
    for problem in &problems {
        assert!(!payload(problem).is_empty());
    }
    let data = payload(&problems[0]);
    assert_eq!(data["methodName"], "onRady");
    assert!(!data.contains_key("classURI"));
    assert!(!data.contains_key("insertLine"));
    assert_eq!(data["eventTypes"], json!(["App\\DomainEvent"]));
}

#[test]
fn event_diagnostics_configured_listener_methods_and_create_data() {
    let fixtures = [
        (
            ".yaml",
            r"services:
  app.listener:
    class: App\Listener
    tags:
      - { name: kernel.event_listener, event: App\DomainEvent, method: {|symfony.event.listener_method.missing:onMissing|} }
",
        ),
        (
            ".xml",
            r#"<container><services>
  <service id="app.listener" class="App\Listener">
    <tag name="kernel.event_listener" event="App\DomainEvent" method="{|symfony.event.listener_method.missing:onMissing|}"/>
  </service>
</services></container>"#,
        ),
    ];
    for (extension, fixture) in fixtures {
        let (source, _) = fixture::parse(fixture);
        let root = tempfile::tempdir().unwrap();
        let class_path = p(&root.path().join("Listener.php"));
        let class_source = r"<?php
namespace App;
class DomainEvent {}
class Listener
{
    public function onExisting(DomainEvent $event): void {}
}
";
        std::fs::write(&class_path, class_source).unwrap();
        let php = php_index(&root.path().join("php"), &[(&class_path, class_source)]);
        let events = event_index(&root.path().join("events"), &php);
        let config_path = p(&root.path().join(format!("services{extension}")));
        events
            .index(&ParsedFile::new(
                config_path.as_str(),
                source.as_bytes().to_vec(),
            ))
            .unwrap();
        let (_, problems) = check_problems(
            &EventAnalyzer::new(Some(events), Some(php), None),
            &file_uri(&config_path),
            fixture,
        );
        let data = payload(&problems[0]);
        assert_eq!(data["eventTypes"], json!(["App\\DomainEvent"]));
        assert!(!data.contains_key("classURI"));
    }
}

#[test]
fn event_diagnostics_class_level_attribute_method() {
    let root = tempfile::tempdir().unwrap();
    let php = php_index(&root.path().join("php"), &[]);
    let events = event_index(&root.path().join("events"), &php);
    let path = p(&root.path().join("AttributedListener.php"));
    let fixture = r"<?php
namespace App;
use Symfony\Component\EventDispatcher\Attribute\AsEventListener;
class DomainEvent {}
#[AsEventListener(event: DomainEvent::class, method: '{|symfony.event.listener_method.missing:onMissing|}')]
class AttributedListener
{
}
";
    let (source, _) = fixture::parse(fixture);
    std::fs::write(&path, &source).unwrap();
    let parsed = ParsedFile::new(path.as_str(), source.as_bytes().to_vec());
    php.index(&parsed).unwrap();
    events.index(&parsed).unwrap();
    let (_, problems) = check_problems(
        &EventAnalyzer::new(Some(events), Some(php), None),
        &file_uri(&path),
        fixture,
    );
    let data = payload(&problems[0]);
    assert_eq!(data["methodName"], "onMissing");
    assert_eq!(data["eventTypes"], json!(["App\\DomainEvent"]));
    assert!(!data.contains_key("classURI"));
}
