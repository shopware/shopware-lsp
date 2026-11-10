use super::super::api::API_RENAME_FIX_ID;
use super::*;

#[test]
fn shopware_api_class_rename_quick_fix_updates_import_and_usages() {
    let index = migration_inspection_php_index();
    check_only_migration_fix(
        &migration(&index, 5),
        "file:///project/src/ClassRename.php",
        r#"<?php
use {|shopware.migration.api.class.rename:Shopware\Core\Framework\Adapter\Console\ShopwareStyle|};
function migrate(ShopwareStyle $style): ShopwareStyle
{
    return new ShopwareStyle();
}
"#,
        API_RENAME_FIX_ID,
        r#"<?php
use Symfony\Component\Console\Style\SymfonyStyle;
function migrate(SymfonyStyle $style): SymfonyStyle
{
    return new SymfonyStyle();
}
"#,
    );
}

#[test]
fn shopware_api_class_rename_avoids_import_alias_conflict() {
    let index = migration_inspection_php_index();
    check_only_migration_fix(
        &migration(&index, 5),
        "file:///project/src/ClassRenameConflict.php",
        r#"<?php
use App\Console\SymfonyStyle;
use {|shopware.migration.api.class.rename:Shopware\Core\Framework\Adapter\Console\ShopwareStyle|};
function migrate(ShopwareStyle $style): void {}
"#,
        API_RENAME_FIX_ID,
        r#"<?php
use App\Console\SymfonyStyle;
use Symfony\Component\Console\Style\SymfonyStyle as ShopwareStyle;
function migrate(ShopwareStyle $style): void {}
"#,
    );
}

#[test]
fn shopware_api_member_rename_quick_fixes() {
    let index = migration_inspection_php_index();
    for (minor, before, after) in [
        (
            5,
            "<?php\nuse Shopware\\Core\\Content\\ImportExport\\Processing\\Mapping\\Mapping;\nfunction migrate(Mapping $mapping): mixed { return $mapping->{|shopware.migration.api.method.rename:getMappedDefault|}(); }\n",
            "<?php\nuse Shopware\\Core\\Content\\ImportExport\\Processing\\Mapping\\Mapping;\nfunction migrate(Mapping $mapping): mixed { return $mapping->getDefaultValue(); }\n",
        ),
        (
            6,
            "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\FieldSerializer\\JsonFieldSerializer;\nfunction migrate(): string { return {|shopware.migration.api.static_method.rename:JsonFieldSerializer::encodeJson([])|}; }\n",
            "<?php\nfunction migrate(): string { return \\Shopware\\Core\\Framework\\Util\\Json::encode([]); }\n",
        ),
        (
            6,
            "<?php\nuse Shopware\\Core\\Checkout\\Cart;\nfunction migrate(): string { return {|shopware.migration.api.constant.rename:Cart::CHECKOUT_ORDER_PLACED|}; }\n",
            "<?php\nfunction migrate(): string { return \\Shopware\\Core\\Framework\\Event\\BusinessEvents::CHECKOUT_ORDER_PLACED; }\n",
        ),
        (
            5,
            "<?php\nuse Shopware\\Core\\Content\\Flow\\Dispatching\\FlowState;\nfunction migrate(FlowState $state): string { return $state->{|shopware.migration.api.property:sequenceId|}; }\n",
            "<?php\nuse Shopware\\Core\\Content\\Flow\\Dispatching\\FlowState;\nfunction migrate(FlowState $state): string { return $state->getSequenceId(); }\n",
        ),
        (
            6,
            "<?php\nuse Shopware\\Core\\Framework\\Routing\\Exception\\InvalidRequestParameterException;\nfunction migrate(): void { throw {|shopware.migration.api.exception_factory:new InvalidRequestParameterException('field')|}; }\n",
            "<?php\nfunction migrate(): void { throw \\Shopware\\Core\\Framework\\Routing\\RoutingException::invalidRequestParameter('field'); }\n",
        ),
    ] {
        check_only_migration_fix(
            &migration(&index, minor),
            "file:///project/src/APIRename.php",
            before,
            API_RENAME_FIX_ID,
            after,
        );
    }
}
