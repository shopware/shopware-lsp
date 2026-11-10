use super::{index_file, open_php, suggestions};
use crate::diagnostics::symfony::MessengerAnalyzer;
use crate::diagnostics::test_support::check_problems;

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
    let fixture = r"<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface {
    public static function getHandledMessages(): iterable {
        return [Message::class => '{|symfony.messenger.handler_method.missing:handel|}'];
    }
    public function handle(): void {}
}";
    let path = "/project/src/Subscriber.php";
    let (source, _) = swlsp_lsp::fixture::parse(fixture);
    index_file(&php, path, &source);
    let (_, result) = check_problems(
        &MessengerAnalyzer::new(Some(php.clone())),
        &format!("file://{path}"),
        fixture,
    );
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
    let (_, result) = check_problems(
        &MessengerAnalyzer::new(Some(php.clone())),
        "file:///project/config/services.yaml",
        r"services:
  App\Handler:
    tags:
      - { name: messenger.message_handler, handles: {|symfony.messenger.message.missing:App\Mesage|}, method: {|symfony.messenger.handler_method.missing:consum|} }
",
    );
    for problem in &result {
        assert!(problem.payload.is_some());
    }
}
