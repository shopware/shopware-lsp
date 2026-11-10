use super::*;

#[test]
fn shopware_argument_migrations_are_typed_and_versioned() {
    let index = migration_test_php_index();
    index.add(
        "/project/vendor/DuplicateWishlistProductException.php",
        "<?php\nnamespace Shopware\\Core\\Checkout\\Customer\\Exception;\nclass DuplicateWishlistProductException {}",
    );
    let (_, problems) = check(
        &index,
        version(6, 5, 0),
        "file:///project/src/Arguments.php",
        r#"<?php
use Shopware\Core\Checkout\Cart\Tax\Struct\CalculatedTaxCollection;
use Shopware\Core\Checkout\Customer\Exception\DuplicateWishlistProductException;
use Shopware\Core\Content\Media\Thumbnail\ThumbnailService;

class CustomDuplicateException extends DuplicateWishlistProductException
{
    public function {|shopware.migration.arguments.remove:__construct|}(string $productId, int $code) {}
}

function migrate(
    CalculatedTaxCollection $taxes,
    ThumbnailService $thumbnail,
    object $other,
    object $media,
    object $context,
): void {
    $taxes->{|shopware.migration.arguments.remove:merge|}($other, true);
    new {|shopware.migration.arguments.remove:DuplicateWishlistProductException|}('obsolete');
    $thumbnail->{|shopware.migration.arguments.add_default:updateThumbnails|}($media, $context);
    $thumbnail->{|shopware.migration.thumbnail.generate:generateThumbnails|}($media, $context);
}
"#,
    );
    let [constructor, merge, exception, update, generate] = problems.as_slice() else {
        unreachable!()
    };
    assert_eq!(payload(merge).argument_index, 1);
    assert_eq!(payload(exception).kind, "call-argument");
    assert_eq!(payload(constructor).kind, "constructor-parameter");
    assert_eq!(payload(update).value, "false");
    let thumbnail = payload(generate);
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
    check(
        &index,
        version(6, 8, 0),
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
}
