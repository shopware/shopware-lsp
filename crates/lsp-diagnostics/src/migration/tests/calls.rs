use super::*;

#[test]
fn shopware65_call_migrations_are_typed_and_safe() {
    let index = migration_test_php_index();
    index.add(
        "/project/vendor/Context.php",
        "<?php\nnamespace Shopware\\Core\\Framework;\nclass Context {}",
    );
    index.add(
        "/project/vendor/Generator.php",
        "<?php\nnamespace Faker;\nclass Generator {}",
    );
    let document = document(
        "file:///project/src/Migrate.php",
        r#"<?php
use Faker\Generator;
use Shopware\Core\Framework\Context;

function migrate(Generator $faker, Context $context, object $other): void
{
    $context->addExtension(EntityIndexerRegistry::USE_INDEXING_QUEUE, new ArrayEntity());
    $context->addExtension(EntityIndexerRegistry::DISABLE_INDEXING, new ArrayEntity());
    $context->addExtension(EntityIndexerRegistry::OTHER, new ArrayEntity());
    $other->addExtension(EntityIndexerRegistry::USE_INDEXING_QUEUE, new ArrayEntity());

    $faker->randomDigit;
    $faker->name = 'changed';
    $faker->randomDigit();
    $other->randomDigit;
}
"#,
    );
    assert!(document.parse_errors().is_empty());
    let problems = analyze(&index, version(6, 5, 0), &document);
    assert_eq!(problems.len(), 4);
    assert_eq!(
        problems[0].id.0,
        "shopware.migration.context_metadata.state"
    );
    assert_eq!(range_text(&document, problems[0].range), "addExtension");
    assert!(payload(&problems[0]).safe);
    assert_eq!(
        problems[1].id.0,
        "shopware.migration.context_metadata.state"
    );
    assert!(payload(&problems[1]).safe);
    assert_eq!(problems[2].id.0, "shopware.migration.faker.property_call");
    assert_eq!(range_text(&document, problems[2].range), "randomDigit");
    assert!(payload(&problems[2]).safe);
    assert_eq!(problems[3].id.0, "shopware.migration.faker.property_call");
    assert_eq!(range_text(&document, problems[3].range), "name");
    assert!(!payload(&problems[3]).safe);

    assert!(analyze(&index, version(6, 4, 20), &document).is_empty());
}

#[test]
fn product_stream_migration_recognizes_safe_patterns() {
    let index = migration_test_php_index();
    index.add(
        "/project/vendor/ProductStreamBuilderInterface.php",
        "<?php\nnamespace Shopware\\Core\\Content\\ProductStream\\Service;\ninterface ProductStreamBuilderInterface {}",
    );
    let document = document(
        "file:///project/src/ProductStream.php",
        r#"<?php
use Shopware\Core\Content\ProductStream\Service\ProductStreamBuilderInterface;

function migrate(
    ProductStreamBuilderInterface $builder,
    object $criteria,
    string $streamId,
    object $context,
): array {
    $filters = $builder->buildFilters($streamId, $context);
    $criteria->addFilter(...$filters);

    $criteria->addFilter(...$builder->buildFilters($streamId, $context));

    return $builder->buildFilters($streamId, $context);
}
"#,
    );
    assert!(document.parse_errors().is_empty());
    let problems = analyze(&index, version(6, 8, 0), &document);
    let product_stream: Vec<&Problem> = problems
        .iter()
        .filter(|problem| problem.id.0 == "shopware.migration.product_stream.enrich_criteria")
        .collect();
    assert_eq!(product_stream.len(), 3);

    let assignment = payload(product_stream[0]);
    assert_eq!(assignment.kind, "assignment");
    assert!(assignment.safe);
    assert!(assignment.start < assignment.end);
    assert_eq!(
        assignment.replacement,
        "$builder->enrichCriteria($criteria, $streamId, $context);"
    );

    let inline = payload(product_stream[1]);
    assert_eq!(inline.kind, "inline");
    assert!(inline.safe);
    assert_eq!(
        inline.replacement,
        "$builder->enrichCriteria($criteria, $streamId, $context)"
    );

    let manual = payload(product_stream[2]);
    assert_eq!(manual.kind, "manual");
    assert!(manual.safe);
    assert_ne!(manual.start, 0);
    assert!(manual.replacement.contains("TODO: Replace buildFilters()"));
    assert!(
        product_stream[2]
            .message
            .contains("manual migration required")
    );

    assert!(analyze(&index, version(6, 7, 10), &document).is_empty());
}
