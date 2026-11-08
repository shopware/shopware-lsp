use super::super::api::API_RENAME_FIX_ID;
use super::*;

#[test]
fn shopware_api_class_rename_quick_fix_updates_import_and_usages() {
    let index = migration_inspection_php_index();
    let document = document(
        "file:///project/src/ClassRename.php",
        r#"<?php
use Shopware\Core\Framework\Adapter\Console\ShopwareStyle;
function migrate(ShopwareStyle $style): ShopwareStyle
{
    return new ShopwareStyle();
}
"#,
    );
    let updated = apply_only_migration_fix(&migration(&index, 5), &document, API_RENAME_FIX_ID);
    assert!(
        updated.contains("use Symfony\\Component\\Console\\Style\\SymfonyStyle;"),
        "{updated}"
    );
    assert!(updated.contains("function migrate(SymfonyStyle $style): SymfonyStyle"));
    assert!(updated.contains("return new SymfonyStyle();"));
    assert!(!updated.contains("ShopwareStyle"));
}

#[test]
fn shopware_api_class_rename_avoids_import_alias_conflict() {
    let index = migration_inspection_php_index();
    let document = document(
        "file:///project/src/ClassRenameConflict.php",
        r#"<?php
use App\Console\SymfonyStyle;
use Shopware\Core\Framework\Adapter\Console\ShopwareStyle;
function migrate(ShopwareStyle $style): void {}
"#,
    );
    let updated = apply_only_migration_fix(&migration(&index, 5), &document, API_RENAME_FIX_ID);
    assert!(
        updated.contains("use Symfony\\Component\\Console\\Style\\SymfonyStyle as ShopwareStyle;")
    );
    assert!(updated.contains("function migrate(ShopwareStyle $style)"));
}

#[test]
fn shopware_api_member_rename_quick_fixes() {
    let index = migration_inspection_php_index();
    for (name, minor, source, expected, removed) in [
        (
            "instance method",
            5,
            "<?php\nuse Shopware\\Core\\Content\\ImportExport\\Processing\\Mapping\\Mapping;\nfunction migrate(Mapping $mapping): mixed { return $mapping->getMappedDefault(); }\n",
            "$mapping->getDefaultValue()",
            "getMappedDefault",
        ),
        (
            "static method owner",
            6,
            "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\FieldSerializer\\JsonFieldSerializer;\nfunction migrate(): string { return JsonFieldSerializer::encodeJson([]); }\n",
            "\\Shopware\\Core\\Framework\\Util\\Json::encode([])",
            "encodeJson",
        ),
        (
            "class constant owner",
            6,
            "<?php\nuse Shopware\\Core\\Checkout\\Cart;\nfunction migrate(): string { return Cart::CHECKOUT_ORDER_PLACED; }\n",
            "\\Shopware\\Core\\Framework\\Event\\BusinessEvents::CHECKOUT_ORDER_PLACED",
            "Cart::CHECKOUT_ORDER_PLACED",
        ),
        (
            "property getter",
            5,
            "<?php\nuse Shopware\\Core\\Content\\Flow\\Dispatching\\FlowState;\nfunction migrate(FlowState $state): string { return $state->sequenceId; }\n",
            "$state->getSequenceId()",
            "$state->sequenceId",
        ),
        (
            "exception factory",
            6,
            "<?php\nuse Shopware\\Core\\Framework\\Routing\\Exception\\InvalidRequestParameterException;\nfunction migrate(): void { throw new InvalidRequestParameterException('field'); }\n",
            "\\Shopware\\Core\\Framework\\Routing\\RoutingException::invalidRequestParameter('field')",
            "new InvalidRequestParameterException",
        ),
    ] {
        let document = document("file:///project/src/APIRename.php", source);
        let updated =
            apply_only_migration_fix(&migration(&index, minor), &document, API_RENAME_FIX_ID);
        assert!(updated.contains(expected), "{name}: {updated}");
        assert!(!updated.contains(removed), "{name}: {updated}");
    }
}
