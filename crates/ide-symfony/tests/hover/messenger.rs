use std::sync::Arc;

use crate::common::*;
use swlsp_ide_symfony::hover::MessengerHoverProvider;
use swlsp_lsp::TextDocument;
use swlsp_messenger::Index;

#[test]
fn messenger_subscriber_hover_shows_resolved_handler() {
    let cache = tempfile::tempdir().unwrap();
    let source = r"<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface {
    public static function getHandledMessages(): iterable {
        yield Message::class => ['method' => 'handle'];
    }
    /** Handles the domain message. */
    public function handle(): void {}
}";
    let path = "/project/src/Subscriber.php";
    let php = php_index(
        cache.path(),
        &[
            (
                "/project/vendor/MessageSubscriberInterface.php",
                r"<?php
namespace Symfony\Component\Messenger\Handler;
interface MessageSubscriberInterface {}",
            ),
            (path, source),
        ],
    );
    let document = TextDocument::new(format!("file://{path}"), source, 1);
    let request = request_at(document, source.find("'handle'").unwrap() + 2);
    let result = hover(&MessengerHoverProvider::new(Some(php), None), &request).expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Symfony Messenger handler method"));
    assert!(value.contains("App\\Subscriber::handle()"));
    assert!(value.contains("Handles the domain message."));
}

#[test]
fn messenger_message_hover_shows_graph_counts() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(&cache.path().join("php"), &[]);
    let messenger = Arc::new(Index::new(cache.path().join("messenger"), None).unwrap());
    messenger.set_php_index(php.clone());
    let handler_source = r"<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
class Message {}
#[AsMessageHandler]
class Handler {
    public function __invoke(Message $message): void {}
}";
    index_file(&php, "/project/src/Handler.php", handler_source);
    index_file(
        messenger.as_ref(),
        "/project/src/Handler.php",
        handler_source,
    );
    let source = r"services:
  App\Handler:
    tags:
      - { name: messenger.message_handler, handles: App\Message }
";
    let document = TextDocument::new("file:///project/config/services.yaml", source, 1);
    let request = request_at(document, source.find("App\\Message").unwrap() + 1);
    let result = hover(
        &MessengerHoverProvider::new(Some(php), Some(messenger)),
        &request,
    )
    .expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Symfony Messenger message"));
    assert!(value.contains("App\\Message"));
    assert!(value.contains("1 handler(s)"));
}
