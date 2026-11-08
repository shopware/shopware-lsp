use super::*;

const ENTITY_SEARCH_RESULT_GET_ENTITIES_CODE: &str =
    "shopware.migration.entity_search_result.get_entities";

#[test]
fn shopware_migration_analyzer_delegates_entity_search_results() {
    let index = migration_test_php_index();
    let document = document(
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
    $result->first();
    $nullable?->last();
    count($result);
    iterator_to_array($listing);
    foreach ($result as $entity) {}

    $result->getTotal();
    $collection->first();
    count($collection);
    foreach ($collection as $entity) {}
}
"#,
    );
    assert!(document.parse_errors().is_empty());
    let problems = analyze(&index, version(6, 8, 0), &document);
    assert_eq!(problems.len(), 5);
    assert_eq!(range_text(&document, problems[0].range), "first");
    assert_eq!(range_text(&document, problems[1].range), "last");
    assert_eq!(range_text(&document, problems[2].range), "$result");
    assert_eq!(range_text(&document, problems[3].range), "$listing");
    assert_eq!(range_text(&document, problems[4].range), "$result");
    for problem in &problems {
        assert_eq!(problem.id.0, ENTITY_SEARCH_RESULT_GET_ENTITIES_CODE);
        assert_eq!(problem.source, "shopware-rector");
        assert!(payload(problem).safe);
    }
}

#[test]
fn shopware_migration_analyzer_requires_known_target_version() {
    let index = migration_test_php_index();
    let document = document(
        "file:///project/src/Migrate.php",
        r#"<?php
use Shopware\Core\Framework\DataAbstractionLayer\Search\EntitySearchResult;
function migrate(EntitySearchResult $result): void { $result->first(); }
"#,
    );
    for resolved in [ResolvedVersion::default(), version(6, 7, 9)] {
        assert!(analyze(&index, resolved, &document).is_empty());
    }
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
    let document = document(
        "file:///project/src/Extensions.php",
        r#"<?php
namespace App;

use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Storefront\Framework\Cache\ReverseProxy\AbstractReverseProxyGateway;

class ProductExtension extends EntityExtension
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

class LegacyExtension extends EntityExtension
{
    public function getDefinitionClass(): string { return LegacyDefinition::class; }
    public function getEntityName(): string { return 'legacy'; }
}

class Proxy extends AbstractReverseProxyGateway {}
class CompleteProxy extends AbstractReverseProxyGateway
{
    public function banAll(): void {}
}
"#,
    );
    let problems = analyze(&index, version(6, 7, 0), &document);
    assert_eq!(problems.len(), 3);
    assert_eq!(
        problems[0].id.0,
        "shopware.migration.entity_extension.entity_name"
    );
    assert_eq!(range_text(&document, problems[0].range), "ProductExtension");
    let product = payload(&problems[0]);
    assert_eq!(product.replacement, "ProductDefinition::ENTITY_NAME");
    assert!(product.remove_legacy);
    assert!(product.safe);
    assert_eq!(
        problems[1].id.0,
        "shopware.migration.entity_extension.entity_name"
    );
    let legacy = payload(&problems[1]);
    assert_eq!(legacy.replacement, "LegacyDefinition::ENTITY_NAME");
    assert!(legacy.remove_legacy);
    assert!(legacy.safe);
    assert_eq!(problems[2].id.0, "shopware.migration.reverse_proxy.ban_all");
    assert_eq!(range_text(&document, problems[2].range), "Proxy");
}

#[test]
fn entity_extension_migration_keeps_legacy_method_before_shopware67() {
    let index = migration_test_php_index();
    index.add(
        "/project/vendor/EntityExtension.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer;\nabstract class EntityExtension {}",
    );
    let document = document(
        "file:///project/src/Extension.php",
        r#"<?php
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
class ProductExtension extends EntityExtension
{
    public function getDefinitionClass(): string { return ProductDefinition::class; }
}
"#,
    );
    let problems = analyze(&index, version(6, 6, 0), &document);
    assert_eq!(problems.len(), 1);
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
    let document = document(
        "file:///project/src/Tasks.php",
        r#"<?php
namespace App;
use Psr\Log\LoggerInterface;
use Shopware\Core\Framework\MessageQueue\ScheduledTask\ScheduledTaskHandler;

class MissingLogger extends ScheduledTaskHandler
{
    public function __construct(object $repository) { parent::__construct($repository); }
}
class ExistingLogger extends ScheduledTaskHandler
{
    public function __construct(object $repository, LoggerInterface $exceptionLogger) { parent::__construct($repository); }
}
class WrongLogger extends ScheduledTaskHandler
{
    public function __construct(object $repository, string $exceptionLogger) { parent::__construct($repository); }
}
class Complete extends ScheduledTaskHandler
{
    public function __construct(object $repository, LoggerInterface $exceptionLogger) { parent::__construct($repository, $exceptionLogger); }
}
"#,
    );
    let problems = analyze(&index, version(6, 7, 0), &document);
    assert_eq!(problems.len(), 3);
    assert_eq!(
        problems[0].id.0,
        "shopware.migration.scheduled_task.exception_logger"
    );
    let missing = payload(&problems[0]);
    assert!(missing.safe);
    assert!(missing.add_parameter);
    let existing = payload(&problems[1]);
    assert!(existing.safe);
    assert!(!existing.add_parameter);
    let wrong = payload(&problems[2]);
    assert!(!wrong.safe);
    assert!(!wrong.add_parameter);

    assert!(analyze(&index, version(6, 6, 10), &document).is_empty());
}

#[test]
fn message_handler_subscriber_migration_detects_safe_classes() {
    let index = migration_test_php_index();
    let document = document(
        "file:///project/src/Handlers.php",
        r#"<?php
use Shopware\Core\Framework\MessageQueue\Handler\AbstractMessageHandler;

class Handler extends AbstractMessageHandler
{
    public static function getHandledMessages(): iterable { return [Message::class]; }
    public function handle(Message $message): void {}
}
class ConflictingHandler extends AbstractMessageHandler
{
    public function handle(Message $message): void {}
    public function __invoke(Message $message): void {}
}
class IncompleteHandler extends AbstractMessageHandler {}
class UnrelatedHandler extends OtherHandler
{
    public function handle(Message $message): void {}
}
"#,
    );
    let problems = analyze(&index, version(6, 5, 0), &document);
    assert_eq!(problems.len(), 3);
    assert_eq!(
        problems[0].id.0,
        "shopware.migration.message_handler.subscriber"
    );
    assert!(payload(&problems[0]).safe);
    assert!(!payload(&problems[1]).safe);
    assert!(payload(&problems[2]).safe);

    assert!(analyze(&index, version(6, 4, 20), &document).is_empty());
}

#[test]
fn shopware_route_annotation_migrations() {
    let index = migration_test_php_index();
    let document = document(
        "file:///project/src/Controller.php",
        r#"<?php
/**
 * @RouteScope(scopes={"storefront", "api"})
 */
class Controller
{
    /**
     * @Route("/contact", defaults={"XmlHttpRequest"=true})
     * @Captcha
     */
    public function contact(): void {}

    /**
     * @LoginRequired
     * @Route("/account")
     */
    public function account(): void {}

    /**
     * @Route("/complete", defaults={"_captcha"=true})
     * @Captcha
     */
    public function complete(): void {}
}
"#,
    );
    let problems = analyze(&index, version(6, 5, 0), &document);
    assert_eq!(problems.len(), 3);
    assert_eq!(
        problems[0].id.0,
        "shopware.migration.annotation.route_defaults"
    );
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

    assert!(analyze(&index, version(6, 4, 20), &document).is_empty());
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
fn migration_payload_omits_empty_fields_like_go() {
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
