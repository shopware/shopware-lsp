use super::super::api::{
    API_CLASS_MIGRATIONS, API_CONSTANT_MIGRATIONS, API_FACTORY_MIGRATIONS, API_METHOD_MIGRATIONS,
    API_PROPERTY_MIGRATIONS, API_STATIC_METHOD_MIGRATIONS, Since,
};
use swlsp_lsp::Context;

use super::*;
use crate::diagnostics::ProblemAnalyzer;

#[test]
fn shopware_api_migration_tables_cover_rector_configuration() {
    assert_eq!(API_CLASS_MIGRATIONS.len(), 25);
    assert_eq!(API_METHOD_MIGRATIONS.len(), 53);
    assert_eq!(API_STATIC_METHOD_MIGRATIONS.len(), 1);
    assert_eq!(API_CONSTANT_MIGRATIONS.len(), 35);
    assert_eq!(API_PROPERTY_MIGRATIONS.len(), 1);
    assert_eq!(API_FACTORY_MIGRATIONS.len(), 12);
}

// Table coverage: each generated source must produce its rule's code. The
// sources also trigger incidental migrations that vary per row (a renamed
// owner type, for example), so these rows assert membership instead of exact
// markup.
fn assert_api_migration_diagnostic(index: &TestIndex, since: Since, source: &str, expected: &str) {
    let document = Arc::new(TextDocument::new(
        "file:///project/src/Mapping.php",
        source,
        1,
    ));
    assert!(document.parse_errors().is_empty(), "{source}");
    let problems = ShopwareMigrationAnalyzer::new(
        Some(index.index.clone()),
        version(6, since.minor, since.patch),
    )
    .analyze(&Context::background(), &document)
    .expect("analyze");
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
    let fixture = r#"<?php
use Shopware\Core\Content\Flow\Dispatching\FlowState;
use Shopware\Core\Framework\Routing\Exception\InvalidRequestParameterException;

function migrate(FlowState $state): void
{
    echo $state->{|shopware.migration.api.property:sequenceId|};
    $state->{|shopware.migration.api.property:sequenceId|} = 'changed';
    throw {|shopware.migration.api.exception_factory:new InvalidRequestParameterException('field')|};
}
"#;
    let uri = "file:///project/src/APITransforms.php";
    let (_, problems) = check(&index, version(6, 6, 0), uri, fixture);
    assert!(payload(&problems[0]).safe);
    assert_eq!(payload(&problems[0]).replacement, "getSequenceId()");
    assert!(!payload(&problems[1]).safe);
    assert_eq!(
        payload(&problems[2]).replacement,
        "\\Shopware\\Core\\Framework\\Routing\\RoutingException::invalidRequestParameter('field')"
    );

    check(
        &index,
        version(6, 5, 0),
        uri,
        &only(fixture, &["shopware.migration.api.property"]),
    );
}

#[test]
fn shopware_api_migrations_use_semantic_references() {
    let index = migration_test_php_index();
    let fixture = r#"<?php
use Shopware\Core\Checkout\Cart;
use Shopware\Core\Content\ImportExport\Processing\Mapping\Mapping;
use {|shopware.migration.api.class.rename:Shopware\Core\Framework\Adapter\Console\ShopwareStyle|};
use Shopware\Core\Framework\DataAbstractionLayer\FieldSerializer\JsonFieldSerializer;

function migrate(Mapping $mapping): void
{
    $style = new ShopwareStyle();
    $mapping->{|shopware.migration.api.method.rename:getDefault|}();
    {|shopware.migration.api.static_method.rename:JsonFieldSerializer::encodeJson([])|};
    echo {|shopware.migration.api.constant.rename:Cart::CHECKOUT_ORDER_PLACED|};
}
"#;
    let uri = "file:///project/src/APIMigration.php";
    let (document, problems) = check(&index, version(6, 6, 0), uri, fixture);
    assert!(document.parse_errors().is_empty());

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

    assert_eq!(payload(&problems[1]).replacement, "getDefaultValue");

    assert_eq!(
        payload(&problems[2]).replacement,
        "\\Shopware\\Core\\Framework\\Util\\Json::encode([])"
    );

    assert_eq!(
        payload(&problems[3]).replacement,
        "\\Shopware\\Core\\Framework\\Event\\BusinessEvents::CHECKOUT_ORDER_PLACED"
    );

    check(
        &index,
        version(6, 5, 0),
        uri,
        &only(
            fixture,
            &[
                "shopware.migration.api.class.rename",
                "shopware.migration.api.method.rename",
            ],
        ),
    );
}

#[test]
fn shopware_api_class_rename_preserves_explicit_alias() {
    let index = migration_test_php_index();
    let (_, problems) = check(
        &index,
        version(6, 5, 0),
        "file:///project/src/Aliased.php",
        r#"<?php
use {|shopware.migration.api.class.rename:Shopware\Core\Framework\Adapter\Console\ShopwareStyle|} as ConsoleStyle;
function migrate(ConsoleStyle $style): void {}
"#,
    );
    let payload = payload(&problems[0]);
    assert_eq!(payload.edits.len(), 1);
    assert_eq!(
        payload.edits[0].replacement,
        "Symfony\\Component\\Console\\Style\\SymfonyStyle"
    );
}
