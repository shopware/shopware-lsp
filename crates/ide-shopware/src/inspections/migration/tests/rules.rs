use super::super::{
    ENTITY_EXTENSION_ENTITY_NAME_FIX_ID, ENTITY_SEARCH_RESULT_GET_ENTITIES_FIX_ID,
    REVERSE_PROXY_BAN_ALL_FIX_ID, SCHEDULED_TASK_LOGGER_FIX_ID,
};
use super::*;

/// An entity search result migration source with `body` as the function
/// body.
fn entity_search_result_source(body: &str) -> String {
    "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\EntitySearchResult;\nfunction migrate(EntitySearchResult $result): void { BODY }\n"
        .replace("BODY", body)
}

#[test]
fn entity_search_result_migration_quick_fixes() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/EntitySearchResult.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer\\Search;\nclass EntitySearchResult {}",
    );
    let inspection = migration(&index, 8);
    for (before, after) in [
        (
            "$result->{|shopware.migration.entity_search_result.get_entities:first|}();",
            "$result->getEntities()->first();",
        ),
        (
            "$result?->{|shopware.migration.entity_search_result.get_entities:first|}();",
            "$result?->getEntities()->first();",
        ),
        (
            "count({|shopware.migration.entity_search_result.get_entities:$result|});",
            "count($result->getEntities());",
        ),
        (
            "foreach ({|shopware.migration.entity_search_result.get_entities:$result|} as $entity) {}",
            "foreach ($result->getEntities() as $entity) {}",
        ),
    ] {
        let presentation = check_only_migration_fix(
            &inspection,
            "file:///project/src/Migrate.php",
            &entity_search_result_source(before),
            ENTITY_SEARCH_RESULT_GET_ENTITIES_FIX_ID,
            &entity_search_result_source(after),
        );
        assert!(presentation.preferred);
    }
}

#[test]
fn entity_search_result_migration_follows_the_installed_deprecation() {
    // On 6.7 the methods the installed class deprecates are reported: the
    // delegation through getEntities() already works there.
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/EntitySearchResult.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer\\Search;\nclass EntitySearchResult {\n    /** @deprecated tag:v6.8.0 - Use getEntities()->first() instead. */\n    public function first() {}\n    /** @deprecated tag:v6.8.0 */\n    public function getIterator() {}\n    public function last() {}\n    public function count() {}\n}",
    );
    let inspection = migration(&index, 7);
    for (before, after) in [
        (
            "$result->{|shopware.migration.entity_search_result.get_entities:first|}();",
            "$result->getEntities()->first();",
        ),
        (
            "foreach ({|shopware.migration.entity_search_result.get_entities:$result|} as $entity) {}",
            "foreach ($result->getEntities() as $entity) {}",
        ),
    ] {
        check_only_migration_fix(
            &inspection,
            "file:///project/src/Migrate.php",
            &entity_search_result_source(before),
            ENTITY_SEARCH_RESULT_GET_ENTITIES_FIX_ID,
            &entity_search_result_source(after),
        );
    }
    // What the installed class does not deprecate stays unreported.
    let (_, problems) = check_diagnostics(
        inspection.as_ref(),
        "file:///project/src/Migrate.php",
        &entity_search_result_source("$result->last(); count($result);"),
    );
    assert!(problems.is_empty(), "{problems:?}");
}

/// An entity extension source with `members` as the class body.
fn entity_extension_source(class: &str, members: &str) -> String {
    "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityExtension;\nclass PRODUCT_EXTENSION extends EntityExtension\n{\nMEMBERS\n}\n"
        .replace("MEMBERS", members)
        .replace("PRODUCT_EXTENSION", class)
}

#[test]
fn entity_extension_migration_quick_fix() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/EntityExtension.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer;\nabstract class EntityExtension {}",
    );
    for (minor, before, after) in [
        // Shopware 6.6 adds a compatible method.
        (
            6,
            "    public function getDefinitionClass(): string { return ProductDefinition::class; }",
            "    public function getDefinitionClass(): string { return ProductDefinition::class; }\n    public function getEntityName(): string\n    {\n        return ProductDefinition::ENTITY_NAME;\n    }",
        ),
        // Shopware 6.7 replaces the definition method.
        (
            7,
            "    public function getDefinitionClass(): string { return ProductDefinition::class; }",
            "    public function getEntityName(): string\n    {\n        return ProductDefinition::ENTITY_NAME;\n    }",
        ),
        // Shopware 6.7 removes the remaining legacy method.
        (
            7,
            "    public function getDefinitionClass(): string { return ProductDefinition::class; }\n    public function getEntityName(): string { return ProductDefinition::ENTITY_NAME; }",
            "    public function getEntityName(): string { return ProductDefinition::ENTITY_NAME; }",
        ),
    ] {
        check_only_migration_fix(
            &migration(&index, minor),
            "file:///project/src/Extension.php",
            &entity_extension_source(
                "{|shopware.migration.entity_extension.entity_name:ProductExtension|}",
                before,
            ),
            ENTITY_EXTENSION_ENTITY_NAME_FIX_ID,
            &entity_extension_source("ProductExtension", after),
        );
    }
}

#[test]
fn reverse_proxy_ban_all_migration_quick_fix() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/AbstractReverseProxyGateway.php",
        "<?php\nnamespace Shopware\\Storefront\\Framework\\Cache\\ReverseProxy;\nabstract class AbstractReverseProxyGateway {}",
    );
    check_only_migration_fix(
        &migration(&index, 5),
        "file:///project/src/Proxy.php",
        "<?php\nuse Shopware\\Storefront\\Framework\\Cache\\ReverseProxy\\AbstractReverseProxyGateway;\nclass {|shopware.migration.reverse_proxy.ban_all:Proxy|} extends AbstractReverseProxyGateway {}\n",
        REVERSE_PROXY_BAN_ALL_FIX_ID,
        "<?php\nuse Shopware\\Storefront\\Framework\\Cache\\ReverseProxy\\AbstractReverseProxyGateway;\nclass Proxy extends AbstractReverseProxyGateway {\n    public function banAll(): void\n    {\n        $this->ban([]);\n    }\n}\n",
    );
}

/// A scheduled task handler source named `class`, with `parameters` as the
/// constructor parameters and `parent` as the parent constructor call.
fn scheduled_task_source(class: &str, parameters: &str, parent: &str) -> String {
    "<?php\nuse Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask\\ScheduledTaskHandler;\nclass CLASS extends ScheduledTaskHandler\n{\n    public function __construct(PARAMETERS)\n    {\n        PARENT;\n    }\n}\n"
        .replace("CLASS", class)
        .replace("PARAMETERS", parameters)
        .replace("PARENT", parent)
}

#[test]
fn scheduled_task_logger_migration_quick_fix() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/ScheduledTaskHandler.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask;\nabstract class ScheduledTaskHandler {}",
    );
    for (parameters, after_parameters, import) in [
        // Adds the logger before optional parameters.
        (
            "object $repository, bool $retry = false",
            "object $repository, LoggerInterface $exceptionLogger, bool $retry = false",
            "use Psr\\Log\\LoggerInterface;\n",
        ),
        // Reuses an existing exception logger parameter.
        (
            "object $repository, \\Psr\\Log\\LoggerInterface $exceptionLogger",
            "object $repository, \\Psr\\Log\\LoggerInterface $exceptionLogger",
            "",
        ),
    ] {
        let after = scheduled_task_source(
            "Handler",
            after_parameters,
            "parent::__construct($repository, $exceptionLogger)",
        )
        .replacen("\nclass ", &format!("\n{import}class "), 1);
        check_only_migration_fix(
            &migration(&index, 7),
            "file:///project/src/Handler.php",
            &scheduled_task_source(
                "{|shopware.migration.scheduled_task.exception_logger:Handler|}",
                parameters,
                "parent::__construct($repository)",
            ),
            SCHEDULED_TASK_LOGGER_FIX_ID,
            &after,
        );
    }
}

#[test]
fn message_handler_subscriber_migration_quick_fix() {
    let index = migration_inspection_php_index();
    check_only_migration_fix(
        &migration(&index, 5),
        "file:///project/src/Handler.php",
        r#"<?php
namespace App;

use Shopware\Core\Framework\MessageQueue\Handler\AbstractMessageHandler;

class {|shopware.migration.message_handler.subscriber:Handler|} extends AbstractMessageHandler
{
    public static function getHandledMessages(): iterable
    {
        return [Message::class];
    }

    public function handle(Message $message): void
    {
    }
}
"#,
        super::super::message_handler::MESSAGE_HANDLER_SUBSCRIBER_FIX_ID,
        r#"<?php
namespace App;

use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;

#[AsMessageHandler]
class Handler implements MessageSubscriberInterface
{
    public function __invoke(Message $message): void
    {
    }
}
"#,
    );
}

#[test]
fn message_handler_subscriber_migration_quick_fix_without_handler_method() {
    let index = migration_inspection_php_index();
    check_only_migration_fix(
        &migration(&index, 5),
        "file:///project/src/AbstractHandler.php",
        r#"<?php
namespace App;

use Shopware\Core\Framework\MessageQueue\Handler\AbstractMessageHandler;

abstract class {|shopware.migration.message_handler.subscriber:Handler|} extends AbstractMessageHandler
{
}
"#,
        super::super::message_handler::MESSAGE_HANDLER_SUBSCRIBER_FIX_ID,
        r#"<?php
namespace App;

use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;

#[AsMessageHandler]
abstract class Handler implements MessageSubscriberInterface
{
}
"#,
    );
}

/// A controller source with `docblock` above its action.
fn controller_source(docblock: &str) -> String {
    "<?php\nclass Controller\n{\nDOCBLOCK\n    public function action(): void {}\n}\n"
        .replace("DOCBLOCK", docblock)
}

#[test]
fn shopware_route_annotation_migration_quick_fixes() {
    let index = migration_inspection_php_index();
    for (before, after) in [
        (
            "    /**\n     * @Route(\"/contact\", defaults={\"XmlHttpRequest\"=true})\n     * {|shopware.migration.annotation.route_defaults:@Captcha|}\n     */",
            "    /**\n     * @Route(\"/contact\", defaults={\"XmlHttpRequest\"=true, \"_captcha\"=true})\n     */",
        ),
        (
            "    /**\n     * {|shopware.migration.annotation.route_defaults:@LoginRequired|}\n     * @Route(\"/account\")\n     */",
            "    /**\n     * @Route(\"/account\", defaults={\"_loginRequired\"=true})\n     */",
        ),
        (
            "    /**\n     * {|shopware.migration.annotation.route_defaults:@RouteScope(scopes={\"storefront\", \"api\"})|}\n     */",
            "    /**\n     * @Route(defaults={\"_routeScope\"={\"storefront\", \"api\"}})\n     */",
        ),
        (
            "    /**\n     * @Route(\"/storefront\", methods={\"GET\"})\n     * {|shopware.migration.annotation.route_defaults:@RouteScope(scopes={\"storefront\"})|}\n     */",
            "    /**\n     * @Route(\"/storefront\", methods={\"GET\"}, defaults={\"_routeScope\"={\"storefront\"}})\n     */",
        ),
    ] {
        check_only_migration_fix(
            &migration(&index, 5),
            "file:///project/src/Controller.php",
            &controller_source(before),
            super::super::annotations::ROUTE_ANNOTATION_MIGRATION_FIX_ID,
            &controller_source(after),
        );
    }
}

#[test]
fn shopware_criteria_inspection_moves_id_filter_to_constructor() {
    let inspection = super::super::new_shopware_criteria();
    let uri = "file:///Criteria.php";
    let before = "<?php\nfunction load(string $id): void {\n    $criteria = new Criteria();\n    $criteria->addFilter(new EqualsFilter('{|shopware.criteria.id-filter:id|}', $id));\n}\n";
    let (_, problems) = check_diagnostics(inspection.as_ref(), uri, before);
    let presentation = check_fix(
        inspection.as_ref(),
        uri,
        before,
        problems[0].fixes[0].id.as_str(),
        "<?php\nfunction load(string $id): void {\n    $criteria = new Criteria($id);\n}\n",
    );
    assert_eq!(
        presentation.title,
        "Move ID constraint to Criteria constructor"
    );
}
