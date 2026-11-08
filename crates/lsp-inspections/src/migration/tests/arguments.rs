use super::super::arguments::{
    ADD_DEFAULT_ARGUMENT_FIX_ID, REMOVE_ARGUMENT_MIGRATION_FIX_ID, THUMBNAIL_GENERATE_FIX_ID,
};
use super::*;

#[test]
fn shopware_call_argument_migration_quick_fixes() {
    let index = migration_inspection_php_index();
    for (name, source, fix_id, expected, removed) in [
        (
            "remove method argument",
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\Tax\\Struct\\CalculatedTaxCollection;\nfunction migrate(CalculatedTaxCollection $taxes, object $other): void\n{\n    $taxes->merge($other, true);\n}\n",
            REMOVE_ARGUMENT_MIGRATION_FIX_ID,
            "$taxes->merge($other);",
            "true",
        ),
        (
            "remove exception argument",
            "<?php\nuse Shopware\\Core\\Checkout\\Customer\\Exception\\DuplicateWishlistProductException;\nfunction migrate(): object\n{\n    return new DuplicateWishlistProductException('obsolete');\n}\n",
            REMOVE_ARGUMENT_MIGRATION_FIX_ID,
            "new DuplicateWishlistProductException()",
            "'obsolete'",
        ),
        (
            "add thumbnail strict default",
            "<?php\nuse Shopware\\Core\\Content\\Media\\Thumbnail\\ThumbnailService;\nfunction migrate(ThumbnailService $thumbnail, object $media, object $context): void\n{\n    $thumbnail->updateThumbnails($media, $context);\n}\n",
            ADD_DEFAULT_ARGUMENT_FIX_ID,
            "$thumbnail->updateThumbnails($media, $context, false);",
            "",
        ),
        (
            "batch thumbnail generation",
            "<?php\nuse Shopware\\Core\\Content\\Media\\Thumbnail\\ThumbnailService;\nfunction migrate(ThumbnailService $thumbnail, object $media, object $context): void\n{\n    $thumbnail->generateThumbnails($media, $context);\n}\n",
            THUMBNAIL_GENERATE_FIX_ID,
            "$thumbnail->generate(new \\Shopware\\Core\\Content\\Media\\MediaCollection([$media]), $context);",
            "generateThumbnails",
        ),
    ] {
        let document = document("file:///project/src/ArgumentMigration.php", source);
        let updated = apply_only_migration_fix(&migration(&index, 5), &document, fix_id);
        assert!(updated.contains(expected), "{name}: {updated}");
        if !removed.is_empty() {
            assert!(!updated.contains(removed), "{name}: {updated}");
        }
    }
}

#[test]
fn shopware_constructor_parameter_removal_quick_fix() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/DuplicateWishlistProductException.php",
        "<?php\nnamespace Shopware\\Core\\Checkout\\Customer\\Exception;\nclass DuplicateWishlistProductException {}",
    );
    let document = document(
        "file:///project/src/CustomException.php",
        r#"<?php
use Shopware\Core\Checkout\Customer\Exception\DuplicateWishlistProductException;
class CustomException extends DuplicateWishlistProductException
{
    public function __construct(string $obsolete, int $code) {}
}
"#,
    );
    let updated = apply_only_migration_fix(
        &migration(&index, 5),
        &document,
        REMOVE_ARGUMENT_MIGRATION_FIX_ID,
    );
    assert!(
        updated.contains("public function __construct(int $code)"),
        "{updated}"
    );
    assert!(!updated.contains("$obsolete"));
}
