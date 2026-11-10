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
    let fixture = r#"<?php
use Faker\Generator;
use Shopware\Core\Framework\Context;

function migrate(Generator $faker, Context $context, object $other): void
{
    $context->{|shopware.migration.context_metadata.state:addExtension|}(EntityIndexerRegistry::USE_INDEXING_QUEUE, new ArrayEntity());
    $context->{|shopware.migration.context_metadata.state:addExtension|}(EntityIndexerRegistry::DISABLE_INDEXING, new ArrayEntity());
    $context->addExtension(EntityIndexerRegistry::OTHER, new ArrayEntity());
    $other->addExtension(EntityIndexerRegistry::USE_INDEXING_QUEUE, new ArrayEntity());

    $faker->{|shopware.migration.faker.property_call:randomDigit|};
    $faker->{|shopware.migration.faker.property_call:name|} = 'changed';
    $faker->randomDigit();
    $other->randomDigit;
}
"#;
    let uri = "file:///project/src/Migrate.php";
    let (document, problems) = check(&index, version(6, 5, 0), uri, fixture);
    assert!(document.parse_errors().is_empty());
    assert!(payload(&problems[0]).safe);
    assert!(payload(&problems[1]).safe);
    assert!(payload(&problems[2]).safe);
    assert!(!payload(&problems[3]).safe);

    check(&index, version(6, 4, 20), uri, &only(fixture, &[]));
}

#[test]
fn product_stream_migration_recognizes_safe_patterns() {
    let index = migration_test_php_index();
    index.add(
        "/project/vendor/ProductStreamBuilderInterface.php",
        "<?php\nnamespace Shopware\\Core\\Content\\ProductStream\\Service;\ninterface ProductStreamBuilderInterface {}",
    );
    // The builder parameter type also migrates; the old test filtered that out.
    let fixture = r#"<?php
use Shopware\Core\Content\ProductStream\Service\ProductStreamBuilderInterface;

function migrate(
    {|shopware.migration.declaration.interface_to_abstract:ProductStreamBuilderInterface $builder|},
    object $criteria,
    string $streamId,
    object $context,
): array {
    $filters = $builder->{|shopware.migration.product_stream.enrich_criteria:buildFilters|}($streamId, $context);
    $criteria->addFilter(...$filters);

    $criteria->addFilter(...$builder->{|shopware.migration.product_stream.enrich_criteria:buildFilters|}($streamId, $context));

    return $builder->{|shopware.migration.product_stream.enrich_criteria:buildFilters|}($streamId, $context);
}
"#;
    let uri = "file:///project/src/ProductStream.php";
    let (document, problems) = check(&index, version(6, 8, 0), uri, fixture);
    assert!(document.parse_errors().is_empty());
    let product_stream = &problems[1..];

    let assignment = payload(&product_stream[0]);
    assert_eq!(assignment.kind, "assignment");
    assert!(assignment.safe);
    assert!(assignment.start < assignment.end);
    assert_eq!(
        assignment.replacement,
        "$builder->enrichCriteria($criteria, $streamId, $context);"
    );

    let inline = payload(&product_stream[1]);
    assert_eq!(inline.kind, "inline");
    assert!(inline.safe);
    assert_eq!(
        inline.replacement,
        "$builder->enrichCriteria($criteria, $streamId, $context)"
    );

    let manual = payload(&product_stream[2]);
    assert_eq!(manual.kind, "manual");
    assert!(manual.safe);
    assert_ne!(manual.start, 0);
    assert!(manual.replacement.contains("TODO: Replace buildFilters()"));
    assert!(
        product_stream[2]
            .message
            .contains("manual migration required")
    );

    check(&index, version(6, 7, 10), uri, &only(fixture, &[]));
}
