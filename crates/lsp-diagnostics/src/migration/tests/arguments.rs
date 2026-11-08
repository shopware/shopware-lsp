use super::*;

#[test]
fn shopware_argument_migrations_are_typed_and_versioned() {
    let index = migration_test_php_index();
    index.add(
        "/project/vendor/DuplicateWishlistProductException.php",
        "<?php\nnamespace Shopware\\Core\\Checkout\\Customer\\Exception;\nclass DuplicateWishlistProductException {}",
    );
    let document = document(
        "file:///project/src/Arguments.php",
        r#"<?php
use Shopware\Core\Checkout\Cart\Tax\Struct\CalculatedTaxCollection;
use Shopware\Core\Checkout\Customer\Exception\DuplicateWishlistProductException;
use Shopware\Core\Content\Media\Thumbnail\ThumbnailService;

class CustomDuplicateException extends DuplicateWishlistProductException
{
    public function __construct(string $productId, int $code) {}
}

function migrate(
    CalculatedTaxCollection $taxes,
    ThumbnailService $thumbnail,
    object $other,
    object $media,
    object $context,
): void {
    $taxes->merge($other, true);
    new DuplicateWishlistProductException('obsolete');
    $thumbnail->updateThumbnails($media, $context);
    $thumbnail->generateThumbnails($media, $context);
}
"#,
    );
    let problems = analyze(&index, version(6, 5, 0), &document);
    assert_eq!(problems.len(), 5);
    assert_eq!(problems[0].id.0, "shopware.migration.arguments.remove");
    assert_eq!(payload(&problems[0]).argument_index, 1);
    assert_eq!(problems[1].id.0, "shopware.migration.arguments.remove");
    assert_eq!(payload(&problems[1]).kind, "call-argument");
    assert_eq!(problems[2].id.0, "shopware.migration.arguments.remove");
    assert_eq!(payload(&problems[2]).kind, "constructor-parameter");
    assert_eq!(problems[3].id.0, "shopware.migration.arguments.add_default");
    assert_eq!(payload(&problems[3]).value, "false");
    assert_eq!(problems[4].id.0, "shopware.migration.thumbnail.generate");
    let thumbnail = payload(&problems[4]);
    assert!(thumbnail.safe);
    assert!(
        thumbnail
            .replacement
            .contains("new \\Shopware\\Core\\Content\\Media\\MediaCollection([$media])")
    );
}

#[test]
fn shopware_argument_migrations_ignore_unrelated_types() {
    let index = migration_test_php_index();
    let document = document(
        "file:///project/src/Unrelated.php",
        r#"<?php
function migrate(object $collection, object $thumbnail): void
{
    $collection->merge('first', 'second');
    $thumbnail->updateThumbnails('media', 'context');
    $thumbnail->generateThumbnails('media', 'context');
}
"#,
    );
    assert!(analyze(&index, version(6, 8, 0), &document).is_empty());
}
