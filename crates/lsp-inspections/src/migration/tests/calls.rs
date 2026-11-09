use super::super::calls::{
    CONTEXT_METADATA_STATE_FIX_ID, FAKER_PROPERTY_CALL_FIX_ID,
    PRODUCT_STREAM_ENRICH_CRITERIA_FIX_ID,
};
use super::*;

const PRODUCT_STREAM_INTERFACE: &str = "<?php\nnamespace Shopware\\Core\\Content\\ProductStream\\Service;\ninterface ProductStreamBuilderInterface {}";

#[test]
fn context_metadata_state_migration_quick_fix() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/Context.php",
        "<?php\nnamespace Shopware\\Core\\Framework;\nclass Context {}",
    );
    check_only_migration_fix(
        &migration(&index, 5),
        "file:///project/src/ContextMigration.php",
        r#"<?php
use Shopware\Core\Framework\Context;
function migrate(Context $context): void
{
    $context->{|shopware.migration.context_metadata.state:addExtension|}(EntityIndexerRegistry::USE_INDEXING_QUEUE, new ArrayEntity());
}
"#,
        CONTEXT_METADATA_STATE_FIX_ID,
        r#"<?php
use Shopware\Core\Framework\Context;
function migrate(Context $context): void
{
    $context->addState(EntityIndexerRegistry::USE_INDEXING_QUEUE);
}
"#,
    );
}

#[test]
fn faker_property_migration_quick_fix() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/Generator.php",
        "<?php\nnamespace Faker;\nclass Generator {}",
    );
    check_only_migration_fix(
        &migration(&index, 5),
        "file:///project/src/FakerMigration.php",
        "<?php\nuse Faker\\Generator;\nfunction migrate(Generator $faker): int\n{\n    return $faker->{|shopware.migration.faker.property_call:randomDigit|};\n}\n",
        FAKER_PROPERTY_CALL_FIX_ID,
        "<?php\nuse Faker\\Generator;\nfunction migrate(Generator $faker): int\n{\n    return $faker->randomDigit();\n}\n",
    );
}

/// A product stream migration source with `builder` as the builder
/// parameter and `body` as the function body.
fn product_stream_source(builder: &str, body: &str) -> String {
    "<?php\nuse Shopware\\Core\\Content\\ProductStream\\Service\\ProductStreamBuilderInterface;\nfunction migrate(BUILDER, object $criteria, string $streamId, object $context): void\n{\n    BODY\n}\n"
        .replace("BUILDER", builder)
        .replace("BODY", body)
}

#[test]
fn product_stream_migration_quick_fixes() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/ProductStreamBuilderInterface.php",
        PRODUCT_STREAM_INTERFACE,
    );
    for (before, after) in [
        (
            "$filters = $builder->{|shopware.migration.product_stream.enrich_criteria:buildFilters|}($streamId, $context);\n    $criteria->addFilter(...$filters);",
            "$builder->enrichCriteria($criteria, $streamId, $context);",
        ),
        (
            "$criteria->addFilter(...$builder->{|shopware.migration.product_stream.enrich_criteria:buildFilters|}($streamId, $context));",
            "$builder->enrichCriteria($criteria, $streamId, $context);",
        ),
    ] {
        check_migration_fix_by_id(
            &migration(&index, 8),
            "file:///project/src/ProductStreamMigration.php",
            &product_stream_source(
                "{|shopware.migration.declaration.interface_to_abstract:ProductStreamBuilderInterface $builder|}",
                before,
            ),
            PRODUCT_STREAM_ENRICH_CRITERIA_FIX_ID,
            &product_stream_source("ProductStreamBuilderInterface $builder", after),
        );
    }
}

#[test]
fn product_stream_manual_migration_adds_rector_todo() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/ProductStreamBuilderInterface.php",
        PRODUCT_STREAM_INTERFACE,
    );
    let uri = "file:///project/src/ProductStreamMigration.php";
    let inspection = migration(&index, 8);
    check_migration_fix_by_id(
        &inspection,
        uri,
        "<?php\nuse Shopware\\Core\\Content\\ProductStream\\Service\\ProductStreamBuilderInterface;\nfunction migrate({|shopware.migration.declaration.interface_to_abstract:ProductStreamBuilderInterface $builder|}, string $streamId, object $context): array\n{\n    return $builder->{|shopware.migration.product_stream.enrich_criteria:buildFilters|}($streamId, $context);\n}\n",
        PRODUCT_STREAM_ENRICH_CRITERIA_FIX_ID,
        "<?php\nuse Shopware\\Core\\Content\\ProductStream\\Service\\ProductStreamBuilderInterface;\nfunction migrate(ProductStreamBuilderInterface $builder, string $streamId, object $context): array\n{\n    // TODO: Replace buildFilters() call with AbstractProductStreamBuilder::enrichCriteria() - please check manually\n    return $builder->buildFilters($streamId, $context);\n}\n",
    );

    // The TODO keeps the problem but no longer offers the fix.
    let (_, problems) = check_diagnostics(
        inspection.as_ref(),
        uri,
        "<?php\nuse Shopware\\Core\\Content\\ProductStream\\Service\\ProductStreamBuilderInterface;\nfunction migrate({|shopware.migration.declaration.interface_to_abstract:ProductStreamBuilderInterface $builder|}, string $streamId, object $context): array\n{\n    // TODO: Replace buildFilters() call with AbstractProductStreamBuilder::enrichCriteria() - please check manually\n    return $builder->{|shopware.migration.product_stream.enrich_criteria:buildFilters|}($streamId, $context);\n}\n",
    );
    assert!(problems[1].fixes.is_empty());
}
