use super::*;

#[test]
fn shopware_migration_analyzer_delegates_entity_search_results() {
    let index = migration_test_php_index();
    let (document, problems) = check(
        &index,
        version(6, 8, 0),
        "file:///project/src/Migrate.php",
        r#"<?php
namespace App;

use Shopware\Core\Content\Product\SalesChannel\Listing\ProductListingResult;
use Shopware\Core\Framework\DataAbstractionLayer\EntityCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Search\EntitySearchResult;

function migrate(
    EntitySearchResult $result,
    ?EntitySearchResult $nullable,
    ProductListingResult $listing,
    EntityCollection $collection,
): void {
    $result->{|shopware.migration.entity_search_result.get_entities:first|}();
    $nullable?->{|shopware.migration.entity_search_result.get_entities:last|}();
    count({|shopware.migration.entity_search_result.get_entities:$result|});
    iterator_to_array({|shopware.migration.entity_search_result.get_entities:$listing|});
    foreach ({|shopware.migration.entity_search_result.get_entities:$result|} as $entity) {}

    $result->getTotal();
    $collection->first();
    count($collection);
    foreach ($collection as $entity) {}
}
"#,
    );
    assert!(document.parse_errors().is_empty());
    for problem in &problems {
        assert_eq!(problem.source, "shopware-rector");
        assert!(payload(problem).safe);
    }
}

#[test]
fn shopware_migration_analyzer_requires_known_target_version() {
    let index = migration_test_php_index();
    let fixture = r#"<?php
use Shopware\Core\Framework\DataAbstractionLayer\Search\EntitySearchResult;
function migrate(EntitySearchResult $result): void { $result->{|shopware.migration.entity_search_result.get_entities:first|}(); }
"#;
    let uri = "file:///project/src/Migrate.php";
    for resolved in [ResolvedVersion::default(), version(6, 7, 9)] {
        check(&index, resolved, uri, &only(fixture, &[]));
    }
    check(&index, version(6, 8, 0), uri, fixture);
}

#[test]
fn shopware_migration_analyzer_class_level_rules() {
    let index = migration_test_php_index();
    index.add(
        "/project/vendor/EntityExtension.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer;\nabstract class EntityExtension {}",
    );
    index.add(
        "/project/vendor/AbstractReverseProxyGateway.php",
        "<?php\nnamespace Shopware\\Storefront\\Framework\\Cache\\ReverseProxy;\nabstract class AbstractReverseProxyGateway {}",
    );
    let (_, problems) = check(
        &index,
        version(6, 7, 0),
        "file:///project/src/Extensions.php",
        r#"<?php
namespace App;

use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Storefront\Framework\Cache\ReverseProxy\AbstractReverseProxyGateway;

class {|shopware.migration.entity_extension.entity_name:ProductExtension|} extends EntityExtension
{
    public function getDefinitionClass(): string
    {
        return ProductDefinition::class;
    }
}

class CompleteExtension extends EntityExtension
{
    public function getEntityName(): string { return 'complete'; }
}

class {|shopware.migration.entity_extension.entity_name:LegacyExtension|} extends EntityExtension
{
    public function getDefinitionClass(): string { return LegacyDefinition::class; }
    public function getEntityName(): string { return 'legacy'; }
}

class {|shopware.migration.reverse_proxy.ban_all:Proxy|} extends AbstractReverseProxyGateway {}
class CompleteProxy extends AbstractReverseProxyGateway
{
    public function banAll(): void {}
}
"#,
    );
    let product = payload(&problems[0]);
    assert_eq!(product.replacement, "ProductDefinition::ENTITY_NAME");
    assert!(product.remove_legacy);
    assert!(product.safe);
    let legacy = payload(&problems[1]);
    assert_eq!(legacy.replacement, "LegacyDefinition::ENTITY_NAME");
    assert!(legacy.remove_legacy);
    assert!(legacy.safe);
}

#[test]
fn entity_extension_migration_keeps_legacy_method_before_shopware67() {
    let index = migration_test_php_index();
    index.add(
        "/project/vendor/EntityExtension.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer;\nabstract class EntityExtension {}",
    );
    let (_, problems) = check(
        &index,
        version(6, 6, 0),
        "file:///project/src/Extension.php",
        r#"<?php
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
class {|shopware.migration.entity_extension.entity_name:ProductExtension|} extends EntityExtension
{
    public function getDefinitionClass(): string { return ProductDefinition::class; }
}
"#,
    );
    let payload = payload(&problems[0]);
    assert!(!payload.remove_legacy);
    assert_eq!(payload.replacement, "ProductDefinition::ENTITY_NAME");
}

#[test]
fn scheduled_task_logger_migration_is_versioned_and_type_safe() {
    let index = migration_test_php_index();
    index.add(
        "/project/vendor/ScheduledTaskHandler.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask;\nabstract class ScheduledTaskHandler {}",
    );
    let fixture = r#"<?php
namespace App;
use Psr\Log\LoggerInterface;
use Shopware\Core\Framework\MessageQueue\ScheduledTask\ScheduledTaskHandler;

class {|shopware.migration.scheduled_task.exception_logger:MissingLogger|} extends ScheduledTaskHandler
{
    public function __construct(object $repository) { parent::__construct($repository); }
}
class {|shopware.migration.scheduled_task.exception_logger:ExistingLogger|} extends ScheduledTaskHandler
{
    public function __construct(object $repository, LoggerInterface $exceptionLogger) { parent::__construct($repository); }
}
class {|shopware.migration.scheduled_task.exception_logger:WrongLogger|} extends ScheduledTaskHandler
{
    public function __construct(object $repository, string $exceptionLogger) { parent::__construct($repository); }
}
class Complete extends ScheduledTaskHandler
{
    public function __construct(object $repository, LoggerInterface $exceptionLogger) { parent::__construct($repository, $exceptionLogger); }
}
"#;
    let uri = "file:///project/src/Tasks.php";
    let (_, problems) = check(&index, version(6, 7, 0), uri, fixture);
    let missing = payload(&problems[0]);
    assert!(missing.safe);
    assert!(missing.add_parameter);
    let existing = payload(&problems[1]);
    assert!(existing.safe);
    assert!(!existing.add_parameter);
    let wrong = payload(&problems[2]);
    assert!(!wrong.safe);
    assert!(!wrong.add_parameter);

    check(&index, version(6, 6, 10), uri, &only(fixture, &[]));
}

#[test]
fn message_handler_subscriber_migration_detects_safe_classes() {
    let index = migration_test_php_index();
    let fixture = r#"<?php
use Shopware\Core\Framework\MessageQueue\Handler\AbstractMessageHandler;

class {|shopware.migration.message_handler.subscriber:Handler|} extends AbstractMessageHandler
{
    public static function getHandledMessages(): iterable { return [Message::class]; }
    public function handle(Message $message): void {}
}
class {|shopware.migration.message_handler.subscriber:ConflictingHandler|} extends AbstractMessageHandler
{
    public function handle(Message $message): void {}
    public function __invoke(Message $message): void {}
}
class {|shopware.migration.message_handler.subscriber:IncompleteHandler|} extends AbstractMessageHandler {}
class UnrelatedHandler extends OtherHandler
{
    public function handle(Message $message): void {}
}
"#;
    let uri = "file:///project/src/Handlers.php";
    let (_, problems) = check(&index, version(6, 5, 0), uri, fixture);
    assert!(payload(&problems[0]).safe);
    assert!(!payload(&problems[1]).safe);
    assert!(payload(&problems[2]).safe);

    check(&index, version(6, 4, 20), uri, &only(fixture, &[]));
}

#[test]
fn shopware_route_annotation_migrations() {
    let index = migration_test_php_index();
    let fixture = r#"<?php
/**
 * {|shopware.migration.annotation.route_defaults:@RouteScope(scopes={"storefront", "api"})|}
 */
class Controller
{
    /**
     * @Route("/contact", defaults={"XmlHttpRequest"=true})
     * {|shopware.migration.annotation.route_defaults:@Captcha|}
     */
    public function contact(): void {}

    /**
     * {|shopware.migration.annotation.route_defaults:@LoginRequired|}
     * @Route("/account")
     */
    public function account(): void {}

    /**
     * @Route("/complete", defaults={"_captcha"=true})
     * @Captcha
     */
    public function complete(): void {}
}
"#;
    let uri = "file:///project/src/Controller.php";
    let (_, problems) = check(&index, version(6, 5, 0), uri, fixture);
    let route_scope = payload(&problems[0]);
    assert_eq!(route_scope.kind, "route-scope");
    assert_eq!(
        route_scope.replacement,
        r#"@Route(defaults={"_routeScope"={"storefront", "api"}})"#
    );
    let captcha = payload(&problems[1]);
    assert_eq!(captcha.kind, "captcha");
    assert_eq!(
        captcha.replacement,
        r#"@Route("/contact", defaults={"XmlHttpRequest"=true, "_captcha"=true})"#
    );
    let login = payload(&problems[2]);
    assert_eq!(login.kind, "login-required");
    assert_eq!(
        login.replacement,
        r#"@Route("/account", defaults={"_loginRequired"=true})"#
    );

    check(&index, version(6, 4, 20), uri, &only(fixture, &[]));
}

#[test]
fn doctrine_route_default_rewrite_handles_nested_values() {
    let (updated, changed, safe) = super::super::annotations::add_doctrine_route_default(
        r#"@Route("/demo", methods={"GET", "POST"}, defaults={"nested"={"a", "b"}})"#,
        "_loginRequired",
        "true",
    );
    assert!(safe);
    assert!(changed);
    assert_eq!(
        updated,
        r#"@Route("/demo", methods={"GET", "POST"}, defaults={"nested"={"a", "b"}, "_loginRequired"=true})"#
    );
}

#[test]
fn migration_payload_omits_empty_fields() {
    let value = serde_json::to_value(ShopwareMigrationPayload {
        rule: "r".into(),
        kind: "k".into(),
        argument_index: -1,
        ..ShopwareMigrationPayload::default()
    })
    .unwrap();
    assert_eq!(
        value,
        serde_json::json!({"rule": "r", "kind": "k", "safe": false, "argumentIndex": -1})
    );
}
