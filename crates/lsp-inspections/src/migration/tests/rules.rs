use super::super::{
    ENTITY_EXTENSION_ENTITY_NAME_FIX_ID, ENTITY_SEARCH_RESULT_GET_ENTITIES_FIX_ID,
    REVERSE_PROXY_BAN_ALL_FIX_ID, SCHEDULED_TASK_LOGGER_FIX_ID,
};
use super::*;

#[test]
fn entity_search_result_migration_quick_fixes() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/EntitySearchResult.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer\\Search;\nclass EntitySearchResult {}",
    );
    let inspection = migration(&index, 8);
    for (name, body, expected) in [
        (
            "method call",
            "$result->first();",
            "$result->getEntities()->first();",
        ),
        (
            "nullsafe method call",
            "$result?->first();",
            "$result?->getEntities()->first();",
        ),
        (
            "count function",
            "count($result);",
            "count($result->getEntities());",
        ),
        (
            "foreach expression",
            "foreach ($result as $entity) {}",
            "foreach ($result->getEntities() as $entity) {}",
        ),
    ] {
        let source = format!(
            "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\EntitySearchResult;\nfunction migrate(EntitySearchResult $result): void {{ {body} }}\n"
        );
        let document = document("file:///project/src/Migrate.php", &source);
        let problems = inspect(inspection.as_ref(), &document);
        assert_eq!(problems.len(), 1, "{name}");
        let problem = &problems[0];
        assert_eq!(problem.fixes.len(), 1, "{name}");
        let bound = &problem.fixes[0];
        assert_eq!(bound.id.0, ENTITY_SEARCH_RESULT_GET_ENTITIES_FIX_ID);
        let fix = quick_fix_with_id(inspection.as_ref(), &bound.id.0);
        let context = fix_context(&document, problem, bound, None);
        let presentation = fix.present(&ctx(), &context).unwrap().expect("visible");
        assert!(presentation.preferred);
        let updated = apply_single(&fix, &context);
        assert!(updated.contains(expected), "{name}: {updated}");
        assert!(
            TextDocument::new(document.uri.clone(), &updated, 2)
                .parse_errors()
                .is_empty()
        );
    }
}

#[test]
fn entity_extension_migration_quick_fix() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/EntityExtension.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer;\nabstract class EntityExtension {}",
    );
    for (name, minor, include_entity_name, keep_definition) in [
        ("Shopware 6.6 adds compatible method", 6, false, true),
        ("Shopware 6.7 replaces definition method", 7, false, false),
        (
            "Shopware 6.7 removes remaining legacy method",
            7,
            true,
            false,
        ),
    ] {
        let entity_name = if include_entity_name {
            "\n    public function getEntityName(): string { return ProductDefinition::ENTITY_NAME; }"
        } else {
            ""
        };
        let source = format!(
            "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityExtension;\nclass ProductExtension extends EntityExtension\n{{\n    public function getDefinitionClass(): string {{ return ProductDefinition::class; }}{entity_name}\n}}\n"
        );
        let document = document("file:///project/src/Extension.php", &source);
        let updated = apply_only_migration_fix(
            &migration(&index, minor),
            &document,
            ENTITY_EXTENSION_ENTITY_NAME_FIX_ID,
        );
        assert!(
            updated.contains("public function getEntityName(): string"),
            "{name}"
        );
        assert!(
            updated.contains("return ProductDefinition::ENTITY_NAME;"),
            "{name}"
        );
        assert_eq!(
            updated.contains("getDefinitionClass"),
            keep_definition,
            "{name}: {updated}"
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
    let document = document(
        "file:///project/src/Proxy.php",
        "<?php\nuse Shopware\\Storefront\\Framework\\Cache\\ReverseProxy\\AbstractReverseProxyGateway;\nclass Proxy extends AbstractReverseProxyGateway {}\n",
    );
    let updated = apply_only_migration_fix(
        &migration(&index, 5),
        &document,
        REVERSE_PROXY_BAN_ALL_FIX_ID,
    );
    assert!(updated.contains("public function banAll(): void"));
    assert!(updated.contains("$this->ban([]);"));
}

#[test]
fn scheduled_task_logger_migration_quick_fix() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/ScheduledTaskHandler.php",
        "<?php\nnamespace Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask;\nabstract class ScheduledTaskHandler {}",
    );
    for (name, parameters, expected, expect_import) in [
        (
            "adds logger before optional parameters",
            "object $repository, bool $retry = false",
            "object $repository, LoggerInterface $exceptionLogger, bool $retry = false",
            true,
        ),
        (
            "reuses existing exception logger parameter",
            "object $repository, \\Psr\\Log\\LoggerInterface $exceptionLogger",
            "object $repository, \\Psr\\Log\\LoggerInterface $exceptionLogger",
            false,
        ),
    ] {
        let source = format!(
            "<?php\nuse Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask\\ScheduledTaskHandler;\nclass Handler extends ScheduledTaskHandler\n{{\n    public function __construct({parameters})\n    {{\n        parent::__construct($repository);\n    }}\n}}\n"
        );
        let document = document("file:///project/src/Handler.php", &source);
        let updated = apply_only_migration_fix(
            &migration(&index, 7),
            &document,
            SCHEDULED_TASK_LOGGER_FIX_ID,
        );
        assert!(updated.contains(expected), "{name}: {updated}");
        assert!(
            updated.contains("parent::__construct($repository, $exceptionLogger);"),
            "{name}"
        );
        if expect_import {
            assert!(
                updated.contains("use Psr\\Log\\LoggerInterface;"),
                "{name}: {updated}"
            );
        }
    }
}

#[test]
fn message_handler_subscriber_migration_quick_fix() {
    let index = migration_inspection_php_index();
    let document = document(
        "file:///project/src/Handler.php",
        r#"<?php
namespace App;

use Shopware\Core\Framework\MessageQueue\Handler\AbstractMessageHandler;

class Handler extends AbstractMessageHandler
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
    );
    let updated = apply_only_migration_fix(
        &migration(&index, 5),
        &document,
        super::super::message_handler::MESSAGE_HANDLER_SUBSCRIBER_FIX_ID,
    );
    assert!(updated.contains("use Symfony\\Component\\Messenger\\Attribute\\AsMessageHandler;"));
    assert!(
        updated.contains("use Symfony\\Component\\Messenger\\Handler\\MessageSubscriberInterface;")
    );
    assert!(updated.contains("#[AsMessageHandler]"));
    assert!(updated.contains("class Handler implements MessageSubscriberInterface"));
    assert!(updated.contains("public function __invoke(Message $message): void"));
    assert!(!updated.contains("extends AbstractMessageHandler"));
    assert!(!updated.contains("getHandledMessages"));
    assert!(!updated.contains("function handle"));
}

#[test]
fn message_handler_subscriber_migration_quick_fix_without_handler_method() {
    let index = migration_inspection_php_index();
    let document = document(
        "file:///project/src/AbstractHandler.php",
        r#"<?php
namespace App;

use Shopware\Core\Framework\MessageQueue\Handler\AbstractMessageHandler;

abstract class Handler extends AbstractMessageHandler
{
}
"#,
    );
    let updated = apply_only_migration_fix(
        &migration(&index, 5),
        &document,
        super::super::message_handler::MESSAGE_HANDLER_SUBSCRIBER_FIX_ID,
    );
    assert!(updated.contains("#[AsMessageHandler]"));
    assert!(updated.contains("abstract class Handler implements MessageSubscriberInterface"));
    assert!(!updated.contains("extends AbstractMessageHandler"));
}

#[test]
fn shopware_route_annotation_migration_quick_fixes() {
    let index = migration_inspection_php_index();
    for (name, docblock, expected, removed) in [
        (
            "captcha",
            "    /**\n     * @Route(\"/contact\", defaults={\"XmlHttpRequest\"=true})\n     * @Captcha\n     */",
            r#"@Route("/contact", defaults={"XmlHttpRequest"=true, "_captcha"=true})"#,
            "@Captcha",
        ),
        (
            "login required",
            "    /**\n     * @LoginRequired\n     * @Route(\"/account\")\n     */",
            r#"@Route("/account", defaults={"_loginRequired"=true})"#,
            "@LoginRequired",
        ),
        (
            "route scope without route",
            "    /**\n     * @RouteScope(scopes={\"storefront\", \"api\"})\n     */",
            r#"@Route(defaults={"_routeScope"={"storefront", "api"}})"#,
            "@RouteScope",
        ),
        (
            "route scope with route",
            "    /**\n     * @Route(\"/storefront\", methods={\"GET\"})\n     * @RouteScope(scopes={\"storefront\"})\n     */",
            r#"@Route("/storefront", methods={"GET"}, defaults={"_routeScope"={"storefront"}})"#,
            "@RouteScope",
        ),
    ] {
        let source = format!(
            "<?php\nclass Controller\n{{\n{docblock}\n    public function action(): void {{}}\n}}\n"
        );
        let document = document("file:///project/src/Controller.php", &source);
        let updated = apply_only_migration_fix(
            &migration(&index, 5),
            &document,
            super::super::annotations::ROUTE_ANNOTATION_MIGRATION_FIX_ID,
        );
        assert!(updated.contains(expected), "{name}: {updated}");
        assert!(!updated.contains(removed), "{name}: {updated}");
    }
}

#[test]
fn shopware_criteria_inspection_moves_id_filter_to_constructor() {
    let inspection = super::super::new_shopware_criteria();
    let document = document(
        "file:///Criteria.php",
        "<?php\nfunction load(string $id): void {\n    $criteria = new Criteria();\n    $criteria->addFilter(new EqualsFilter('id', $id));\n}\n",
    );
    let problems = inspect(inspection.as_ref(), &document);
    assert_eq!(problems.len(), 1);
    let bound = &problems[0].fixes[0];
    let fix = quick_fix_with_id(inspection.as_ref(), &bound.id.0);
    let context = fix_context(&document, &problems[0], bound, None);
    let presentation = fix.present(&ctx(), &context).unwrap().expect("visible");
    assert_eq!(
        presentation.title,
        "Move ID constraint to Criteria constructor"
    );
    let updated = apply_single(&fix, &context);
    assert!(
        updated.contains("$criteria = new Criteria($id);"),
        "{updated}"
    );
    assert!(!updated.contains("EqualsFilter"), "{updated}");
}
