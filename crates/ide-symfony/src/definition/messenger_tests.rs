use swlsp_uriutil::file_uri;

use super::*;
use crate::definition::php_common::test_support::*;

#[test]
fn messenger_subscriber_definition_navigates_handler_method() {
    let root = tempfile::tempdir().unwrap();
    let path = p(&root.path().join("src/Subscriber.php"));
    let source = r"<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface {
    public static function getHandledMessages(): iterable {
        return [Message::class => 'handle'];
    }
    public function handle(): void {}
}";
    write(std::path::Path::new(&path), source);
    let php = php_index(&root.path().join("cache"));
    index(
        &php,
        &p(&root.path().join("vendor/MessageSubscriberInterface.php")),
        r"<?php
namespace Symfony\Component\Messenger\Handler;
interface MessageSubscriberInterface {
    public static function getHandledMessages(): iterable;
}",
    );
    index(&php, &path, source);
    let document = document(&file_uri(&path), source);
    let offset = index_of(source, "'handle'") + 2;
    let locations = definitions(
        &MessengerDefinitionProvider::new(Some(php)),
        &request_at(&document, offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&path));
    assert_eq!(locations[0].range.start.line, 7);
}

#[test]
fn messenger_configuration_definition_navigates_message_and_method() {
    let root = tempfile::tempdir().unwrap();
    let handler_path = root.path().join("src/Handler.php");
    let config_path = root.path().join("config/services.yaml");
    let handler_source = r"<?php
namespace App;
class Message {}
class Handler {
    public function consume(Message $message): void {}
}";
    let config_source = r"services:
  App\Handler:
    tags:
      - { name: messenger.message_handler, handles: App\Message, method: consume }
";
    write(&handler_path, handler_source);
    write(&config_path, config_source);
    let php = php_index(&root.path().join("cache"));
    index(&php, &p(&handler_path), handler_source);
    let document = document(&file_uri(&p(&config_path)), config_source);
    let provider = MessengerDefinitionProvider::new(Some(php));
    for (marker, line) in [("App\\Message", 2), ("consume", 4)] {
        let offset = index_of(config_source, marker) + 1;
        let locations = definitions(&provider, &request_at(&document, offset));
        assert_eq!(locations.len(), 1, "{marker}");
        assert_eq!(locations[0].range.start.line, line);
    }
}
