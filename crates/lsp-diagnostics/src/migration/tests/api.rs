use super::super::api::{
    API_CLASS_MIGRATIONS, API_CONSTANT_MIGRATIONS, API_FACTORY_MIGRATIONS, API_METHOD_MIGRATIONS,
    API_PROPERTY_MIGRATIONS, API_STATIC_METHOD_MIGRATIONS, Since,
};
use super::*;

#[test]
fn shopware_api_migration_tables_cover_rector_configuration() {
    assert_eq!(API_CLASS_MIGRATIONS.len(), 19);
    assert_eq!(API_METHOD_MIGRATIONS.len(), 53);
    assert_eq!(API_STATIC_METHOD_MIGRATIONS.len(), 1);
    assert_eq!(API_CONSTANT_MIGRATIONS.len(), 35);
    assert_eq!(API_PROPERTY_MIGRATIONS.len(), 1);
    assert_eq!(API_FACTORY_MIGRATIONS.len(), 12);
}

fn assert_api_migration_diagnostic(index: &TestIndex, since: Since, source: &str, expected: &str) {
    let document = document("file:///project/src/Mapping.php", source);
    assert!(document.parse_errors().is_empty(), "{source}");
    let problems = analyze(index, version(6, since.minor, since.patch), &document);
    assert!(
        problems.iter().any(|problem| problem.id.0 == expected),
        "{source} did not produce {expected}"
    );
}

#[test]
fn every_shopware_api_migration_mapping_produces_diagnostic() {
    let index = migration_test_php_index();
    for rule in API_CLASS_MIGRATIONS {
        assert_api_migration_diagnostic(
            &index,
            rule.since,
            &format!(
                "<?php function migrate(): void {{ new \\{}(); }}",
                rule.from
            ),
            "shopware.migration.api.class.rename",
        );
    }
    for rule in API_METHOD_MIGRATIONS.iter() {
        assert_api_migration_diagnostic(
            &index,
            rule.since,
            &format!(
                "<?php function migrate(\\{} $subject): void {{ $subject->{}(); }}",
                rule.owner, rule.from
            ),
            "shopware.migration.api.method.rename",
        );
    }
    for rule in API_STATIC_METHOD_MIGRATIONS {
        assert_api_migration_diagnostic(
            &index,
            rule.since,
            &format!("<?php \\{}::{}();", rule.owner, rule.from),
            "shopware.migration.api.static_method.rename",
        );
    }
    for rule in API_CONSTANT_MIGRATIONS.iter() {
        assert_api_migration_diagnostic(
            &index,
            rule.since,
            &format!("<?php $value = \\{}::{};", rule.owner, rule.from),
            "shopware.migration.api.constant.rename",
        );
    }
    for rule in API_PROPERTY_MIGRATIONS {
        assert_api_migration_diagnostic(
            &index,
            rule.since,
            &format!(
                "<?php function migrate(\\{} $subject): void {{ echo $subject->{}; }}",
                rule.owner, rule.from
            ),
            "shopware.migration.api.property",
        );
    }
    for rule in API_FACTORY_MIGRATIONS {
        assert_api_migration_diagnostic(
            &index,
            rule.since,
            &format!("<?php throw new \\{}('value');", rule.from),
            "shopware.migration.api.exception_factory",
        );
    }
}

#[test]
fn shopware_api_property_and_exception_factory_migrations() {
    let index = migration_test_php_index();
    let document = document(
        "file:///project/src/APITransforms.php",
        r#"<?php
use Shopware\Core\Content\Flow\Dispatching\FlowState;
use Shopware\Core\Framework\Routing\Exception\InvalidRequestParameterException;

function migrate(FlowState $state): void
{
    echo $state->sequenceId;
    $state->sequenceId = 'changed';
    throw new InvalidRequestParameterException('field');
}
"#,
    );
    let problems = analyze(&index, version(6, 6, 0), &document);
    assert_eq!(problems.len(), 3);
    assert_eq!(problems[0].id.0, "shopware.migration.api.property");
    assert!(payload(&problems[0]).safe);
    assert_eq!(payload(&problems[0]).replacement, "getSequenceId()");
    assert_eq!(problems[1].id.0, "shopware.migration.api.property");
    assert!(!payload(&problems[1]).safe);
    assert_eq!(problems[2].id.0, "shopware.migration.api.exception_factory");
    assert_eq!(
        payload(&problems[2]).replacement,
        "\\Shopware\\Core\\Framework\\Routing\\RoutingException::invalidRequestParameter('field')"
    );

    assert_eq!(analyze(&index, version(6, 5, 0), &document).len(), 2);
}

#[test]
fn shopware_api_migrations_use_semantic_references() {
    let index = migration_test_php_index();
    let document = document(
        "file:///project/src/APIMigration.php",
        r#"<?php
use Shopware\Core\Checkout\Cart;
use Shopware\Core\Content\ImportExport\Processing\Mapping\Mapping;
use Shopware\Core\Framework\Adapter\Console\ShopwareStyle;
use Shopware\Core\Framework\DataAbstractionLayer\FieldSerializer\JsonFieldSerializer;

function migrate(Mapping $mapping): void
{
    $style = new ShopwareStyle();
    $mapping->getDefault();
    JsonFieldSerializer::encodeJson([]);
    echo Cart::CHECKOUT_ORDER_PLACED;
}
"#,
    );
    assert!(document.parse_errors().is_empty());
    let problems = analyze(&index, version(6, 6, 0), &document);
    assert_eq!(problems.len(), 4);

    assert_eq!(problems[0].id.0, "shopware.migration.api.class.rename");
    let class_payload = payload(&problems[0]);
    assert!(class_payload.safe);
    assert_eq!(class_payload.edits.len(), 2);
    assert_eq!(
        class_payload.edits[0].original,
        "Shopware\\Core\\Framework\\Adapter\\Console\\ShopwareStyle"
    );
    assert_eq!(
        class_payload.edits[0].replacement,
        "Symfony\\Component\\Console\\Style\\SymfonyStyle"
    );
    assert_eq!(class_payload.edits[1].original, "ShopwareStyle");
    assert_eq!(class_payload.edits[1].replacement, "SymfonyStyle");

    assert_eq!(problems[1].id.0, "shopware.migration.api.method.rename");
    assert_eq!(payload(&problems[1]).replacement, "getDefaultValue");
    assert_eq!(range_text(&document, problems[1].range), "getDefault");

    assert_eq!(
        problems[2].id.0,
        "shopware.migration.api.static_method.rename"
    );
    assert_eq!(
        payload(&problems[2]).replacement,
        "\\Shopware\\Core\\Framework\\Util\\Json::encode([])"
    );

    assert_eq!(problems[3].id.0, "shopware.migration.api.constant.rename");
    assert_eq!(
        payload(&problems[3]).replacement,
        "\\Shopware\\Core\\Framework\\Event\\BusinessEvents::CHECKOUT_ORDER_PLACED"
    );

    let problems = analyze(&index, version(6, 5, 0), &document);
    assert_eq!(problems.len(), 2);
    assert_eq!(problems[0].id.0, "shopware.migration.api.class.rename");
    assert_eq!(problems[1].id.0, "shopware.migration.api.method.rename");
}

#[test]
fn shopware_api_class_rename_preserves_explicit_alias() {
    let index = migration_test_php_index();
    let document = document(
        "file:///project/src/Aliased.php",
        r#"<?php
use Shopware\Core\Framework\Adapter\Console\ShopwareStyle as ConsoleStyle;
function migrate(ConsoleStyle $style): void {}
"#,
    );
    let problems = analyze(&index, version(6, 5, 0), &document);
    assert_eq!(problems.len(), 1);
    let payload = payload(&problems[0]);
    assert_eq!(payload.edits.len(), 1);
    assert_eq!(
        payload.edits[0].replacement,
        "Symfony\\Component\\Console\\Style\\SymfonyStyle"
    );
}
