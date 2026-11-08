use std::sync::Arc;

use swlsp_lsp::CompletionProvider;

use super::*;
use crate::MessengerCompletionProvider;

#[test]
fn messenger_subscriber_completion_includes_public_handler_methods() {
    let cache = tempdir();
    let php = php_index(&cache);
    index_file(
        &php,
        "/project/vendor/MessageSubscriberInterface.php",
        r"<?php
namespace Symfony\Component\Messenger\Handler;
interface MessageSubscriberInterface {
    public static function getHandledMessages(): iterable;
}",
    );
    let source = r"<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface {
    public static function getHandledMessages(): iterable {
        yield Message::class => ['method' => ''];
    }
    public function handleFirst(): void {}
    public function handleSecond(): void {}
    protected function hidden(): void {}
    private function secret(): void {}
    public function __toString(): string { return ''; }
}";
    let path = "/project/src/Subscriber.php";
    index_file(&php, path, source);
    let document = document(&format!("file://{path}"), source);
    let offset = source.find("'method' => ''").unwrap() + "'method' => '".len();
    let items = MessengerCompletionProvider::new(Some(php), None)
        .get_completions(&ctx(), &node_request(&document, offset));
    require_completion(&items, "handleFirst");
    require_completion(&items, "handleSecond");
    let labels = labels(&items);
    for absent in ["hidden", "secret", "__toString", "getHandledMessages"] {
        assert!(!labels.contains(&absent.to_string()), "{absent}");
    }
}

#[test]
fn messenger_configuration_completion_includes_messages_and_methods() {
    let (php_cache, messenger_cache) = (tempdir(), tempdir());
    let php = php_index(&php_cache);
    let messenger = Arc::new(swlsp_messenger::Index::new(messenger_cache.path(), None).unwrap());
    messenger.set_php_index(php.clone());
    let handler_source = r"<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
class Message {}
#[AsMessageHandler]
class Handler {
    public function __invoke(Message $message): void {}
    public function consume(Message $message): void {}
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
      - { name: messenger.message_handler, handles: '', method: '' }
";
    let document = document("file:///project/config/services.yaml", source);
    let provider = MessengerCompletionProvider::new(Some(php), Some(messenger));
    for (marker, label) in [("handles: ''", "App\\Message"), ("method: ''", "consume")] {
        let offset = source.find(marker).unwrap() + marker.rfind("''").unwrap() + 1;
        let items = provider.get_completions(&ctx(), &node_request(&document, offset));
        require_completion(&items, label);
    }
}
