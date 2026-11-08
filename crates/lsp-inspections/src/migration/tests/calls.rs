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
    let document = document(
        "file:///project/src/ContextMigration.php",
        r#"<?php
use Shopware\Core\Framework\Context;
function migrate(Context $context): void
{
    $context->addExtension(EntityIndexerRegistry::USE_INDEXING_QUEUE, new ArrayEntity());
}
"#,
    );
    let updated = apply_only_migration_fix(
        &migration(&index, 5),
        &document,
        CONTEXT_METADATA_STATE_FIX_ID,
    );
    assert!(
        updated.contains("$context->addState(EntityIndexerRegistry::USE_INDEXING_QUEUE);"),
        "{updated}"
    );
    assert!(!updated.contains("new ArrayEntity()"));
}

#[test]
fn faker_property_migration_quick_fix() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/Generator.php",
        "<?php\nnamespace Faker;\nclass Generator {}",
    );
    let document = document(
        "file:///project/src/FakerMigration.php",
        "<?php\nuse Faker\\Generator;\nfunction migrate(Generator $faker): int\n{\n    return $faker->randomDigit;\n}\n",
    );
    let updated =
        apply_only_migration_fix(&migration(&index, 5), &document, FAKER_PROPERTY_CALL_FIX_ID);
    assert!(updated.contains("return $faker->randomDigit();"));
}

#[test]
fn product_stream_migration_quick_fixes() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/ProductStreamBuilderInterface.php",
        PRODUCT_STREAM_INTERFACE,
    );
    for (name, body, expected, removed) in [
        (
            "assignment followed by addFilter",
            "$filters = $builder->buildFilters($streamId, $context);\n    $criteria->addFilter(...$filters);",
            "$builder->enrichCriteria($criteria, $streamId, $context);",
            "$filters =",
        ),
        (
            "inline addFilter",
            "$criteria->addFilter(...$builder->buildFilters($streamId, $context));",
            "$builder->enrichCriteria($criteria, $streamId, $context);",
            "addFilter",
        ),
    ] {
        let source = format!(
            "<?php\nuse Shopware\\Core\\Content\\ProductStream\\Service\\ProductStreamBuilderInterface;\nfunction migrate(ProductStreamBuilderInterface $builder, object $criteria, string $streamId, object $context): void\n{{\n    {body}\n}}\n"
        );
        let document = document("file:///project/src/ProductStreamMigration.php", &source);
        let updated = apply_migration_fix_by_id(
            &migration(&index, 8),
            &document,
            PRODUCT_STREAM_ENRICH_CRITERIA_FIX_ID,
        );
        assert!(updated.contains(expected), "{name}: {updated}");
        assert!(!updated.contains(removed), "{name}: {updated}");
    }
}

#[test]
fn product_stream_manual_migration_adds_rector_todo() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/ProductStreamBuilderInterface.php",
        PRODUCT_STREAM_INTERFACE,
    );
    let document = document(
        "file:///project/src/ProductStreamMigration.php",
        "<?php\nuse Shopware\\Core\\Content\\ProductStream\\Service\\ProductStreamBuilderInterface;\nfunction migrate(ProductStreamBuilderInterface $builder, string $streamId, object $context): array\n{\n    return $builder->buildFilters($streamId, $context);\n}\n",
    );
    let inspection = migration(&index, 8);
    let updated = apply_migration_fix_by_id(
        &inspection,
        &document,
        PRODUCT_STREAM_ENRICH_CRITERIA_FIX_ID,
    );
    assert!(updated.contains("    // TODO: Replace buildFilters() call with AbstractProductStreamBuilder::enrichCriteria() - please check manually\n    return $builder->buildFilters"), "{updated}");

    let updated_document = Arc::new(TextDocument::new(
        document.uri.clone(),
        &updated,
        document.version + 1,
    ));
    let problems = inspect(inspection.as_ref(), &updated_document);
    let product_stream: Vec<_> = problems
        .iter()
        .filter(|problem| problem.id.0 == "shopware.migration.product_stream.enrich_criteria")
        .collect();
    assert_eq!(product_stream.len(), 1);
    assert!(product_stream[0].fixes.is_empty());
}
