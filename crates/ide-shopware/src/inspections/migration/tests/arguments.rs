use super::super::arguments::{
    ADD_DEFAULT_ARGUMENT_FIX_ID, REMOVE_ARGUMENT_MIGRATION_FIX_ID, THUMBNAIL_GENERATE_FIX_ID,
};
use super::*;

const URI: &str = "file:///project/src/ArgumentMigration.php";

#[test]
fn shopware_call_argument_migration_quick_fixes() {
    let index = migration_inspection_php_index();
    for (before, fix_id, after) in [
        (
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\Tax\\Struct\\CalculatedTaxCollection;\nfunction migrate(CalculatedTaxCollection $taxes, object $other): void\n{\n    $taxes->{|shopware.migration.arguments.remove:merge|}($other, true);\n}\n",
            REMOVE_ARGUMENT_MIGRATION_FIX_ID,
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\Tax\\Struct\\CalculatedTaxCollection;\nfunction migrate(CalculatedTaxCollection $taxes, object $other): void\n{\n    $taxes->merge($other);\n}\n",
        ),
        (
            "<?php\nuse Shopware\\Core\\Checkout\\Customer\\Exception\\DuplicateWishlistProductException;\nfunction migrate(): object\n{\n    return new {|shopware.migration.arguments.remove:DuplicateWishlistProductException|}('obsolete');\n}\n",
            REMOVE_ARGUMENT_MIGRATION_FIX_ID,
            "<?php\nuse Shopware\\Core\\Checkout\\Customer\\Exception\\DuplicateWishlistProductException;\nfunction migrate(): object\n{\n    return new DuplicateWishlistProductException();\n}\n",
        ),
        (
            "<?php\nuse Shopware\\Core\\Content\\Media\\Thumbnail\\ThumbnailService;\nfunction migrate(ThumbnailService $thumbnail, object $media, object $context): void\n{\n    $thumbnail->{|shopware.migration.arguments.add_default:updateThumbnails|}($media, $context);\n}\n",
            ADD_DEFAULT_ARGUMENT_FIX_ID,
            "<?php\nuse Shopware\\Core\\Content\\Media\\Thumbnail\\ThumbnailService;\nfunction migrate(ThumbnailService $thumbnail, object $media, object $context): void\n{\n    $thumbnail->updateThumbnails($media, $context, false);\n}\n",
        ),
        (
            "<?php\nuse Shopware\\Core\\Content\\Media\\Thumbnail\\ThumbnailService;\nfunction migrate(ThumbnailService $thumbnail, object $media, object $context): void\n{\n    $thumbnail->{|shopware.migration.thumbnail.generate:generateThumbnails|}($media, $context);\n}\n",
            THUMBNAIL_GENERATE_FIX_ID,
            "<?php\nuse Shopware\\Core\\Content\\Media\\Thumbnail\\ThumbnailService;\nfunction migrate(ThumbnailService $thumbnail, object $media, object $context): void\n{\n    $thumbnail->generate(new \\Shopware\\Core\\Content\\Media\\MediaCollection([$media]), $context);\n}\n",
        ),
    ] {
        check_only_migration_fix(&migration(&index, 5), URI, before, fix_id, after);
    }
}

#[test]
fn shopware_constructor_parameter_removal_quick_fix() {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/DuplicateWishlistProductException.php",
        "<?php\nnamespace Shopware\\Core\\Checkout\\Customer\\Exception;\nclass DuplicateWishlistProductException {}",
    );
    check_only_migration_fix(
        &migration(&index, 5),
        "file:///project/src/CustomException.php",
        r#"<?php
use Shopware\Core\Checkout\Customer\Exception\DuplicateWishlistProductException;
class CustomException extends DuplicateWishlistProductException
{
    public function {|shopware.migration.arguments.remove:__construct|}(string $obsolete, int $code) {}
}
"#,
        REMOVE_ARGUMENT_MIGRATION_FIX_ID,
        r#"<?php
use Shopware\Core\Checkout\Customer\Exception\DuplicateWishlistProductException;
class CustomException extends DuplicateWishlistProductException
{
    public function __construct(int $code) {}
}
"#,
    );
}
