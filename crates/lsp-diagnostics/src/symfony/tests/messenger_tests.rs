use super::{analyze, index_file, open_php, suggestions};
use crate::symfony::{
    MISSING_MESSENGER_HANDLER_METHOD_CODE, MISSING_MESSENGER_MESSAGE_CODE, MessengerAnalyzer,
};
use crate::test_support::diagnostics_document;

#[test]
fn messenger_subscriber_diagnostics_suggest_handler_method() {
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/vendor/MessageSubscriberInterface.php",
        r"<?php
namespace Symfony\Component\Messenger\Handler;
interface MessageSubscriberInterface {}",
    );
    let source = r"<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface {
    public static function getHandledMessages(): iterable {
        return [Message::class => 'handel'];
    }
    public function handle(): void {}
}";
    let path = "/project/src/Subscriber.php";
    index_file(&php, path, source);
    let result = analyze(
        &MessengerAnalyzer::new(Some(php.clone())),
        &diagnostics_document(&format!("file://{path}"), source),
    );
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].id.0, MISSING_MESSENGER_HANDLER_METHOD_CODE);
    assert!(result[0].message.contains("App\\Subscriber::handel"));
    assert_eq!(suggestions(&result[0]), vec!["handle".to_owned()]);
}

#[test]
fn messenger_configuration_diagnostics_report_message_and_method_typos() {
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/src/Handler.php",
        r"<?php
namespace App;
class Message {}
class Handler {
    public function consume(Message $message): void {}
}",
    );
    let source = r"services:
  App\Handler:
    tags:
      - { name: messenger.message_handler, handles: App\Mesage, method: consum }
";
    let result = analyze(
        &MessengerAnalyzer::new(Some(php.clone())),
        &diagnostics_document("file:///project/config/services.yaml", source),
    );
    assert_eq!(result.len(), 2);
    let mut codes: Vec<&str> = result.iter().map(|problem| problem.id.0.as_str()).collect();
    codes.sort_unstable();
    assert_eq!(
        codes,
        vec![
            MISSING_MESSENGER_HANDLER_METHOD_CODE,
            MISSING_MESSENGER_MESSAGE_CODE
        ]
    );
    for problem in &result {
        assert!(problem.payload.is_some());
    }
}
