use super::super::api::API_RENAME_FIX_ID;
use super::super::arguments::ADD_DEFAULT_ARGUMENT_FIX_ID;
use super::super::declarations::DECLARATION_MIGRATION_FIX_ID;
use super::*;

/// A slice of Shopware that announces its changes the way the real source
/// does.
fn announcing_index() -> TestIndex {
    let index = migration_inspection_php_index();
    for (path, source) in [
        (
            "/project/vendor/CartException.php",
            r#"<?php
namespace Shopware\Core\Checkout\Cart;

use Shopware\Core\Framework\Deprecation\BCChange\NewOptionalParameter;
use Shopware\Core\Framework\Deprecation\BCChange\ParameterTypeWidening;
use Shopware\Core\Framework\Deprecation\BCChange\ReturnTypeNarrowing;

class Address {}

class CartException extends \Exception
{
    #[ReturnTypeNarrowing(version: 'v6.8.0', newType: 'self')]
    public static function invalid(): self|\RuntimeException {}

    #[ReturnTypeNarrowing(version: 'v6.8.0', newType: 'string')]
    public function label(): ?string {}

    #[ParameterTypeWidening(version: 'v6.8.0', parameterName: 'address', newType: '?' . Address::class)]
    public function setAddress(Address $address): void {}

    #[NewOptionalParameter(version: 'v6.8.0', parameterName: 'cart', parameterType: '?' . Cart::class)]
    #[NewOptionalParameter(version: 'v6.8.0', parameterName: 'locale', parameterType: 'string', defaultValue: 'en_GB')]
    public function load(string $token): void {}
}

class Cart {}
"#,
        ),
        (
            "/project/vendor/CreatedByField.php",
            r#"<?php
namespace Shopware\Core\Framework\DataAbstractionLayer\Field;

use Shopware\Core\Framework\Context;
use Shopware\Core\Framework\Deprecation\BCChange\ParameterDefaultValueChange;

class CreatedByField
{
    #[ParameterDefaultValueChange(version: 'v6.8.0', parameterName: 'allowedWriteScopes', newDefaultValue: [Context::SYSTEM_SCOPE, Context::CRUD_API_SCOPE])]
    public function __construct(array $allowedWriteScopes = [Context::SYSTEM_SCOPE]) {}
}
"#,
        ),
        (
            "/project/vendor/NotificationEntity.php",
            r#"<?php
namespace Shopware\Core\Framework\Notification;

use Shopware\Core\Framework\Deprecation\BCChange\ClassMoved;

#[ClassMoved(version: 'v6.9.0', previousClassName: 'Shopware\Administration\Notification\NotificationEntity')]
class NotificationEntity {}

class_alias(NotificationEntity::class, 'Shopware\Administration\Notification\NotificationEntity');
"#,
        ),
    ] {
        index.add(path, source);
    }
    index
}

#[test]
fn overrides_follow_the_changes_shopware_announces() {
    let index = announcing_index();
    for (before, after) in [
        (
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartException;\nclass Mine extends CartException\n{\n    {|shopware.migration.declaration.type:public static function invalid(): self|\\RuntimeException {}|}\n}\n",
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartException;\nclass Mine extends CartException\n{\n    public static function invalid(): self {}\n}\n",
        ),
        (
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartException;\nclass Mine extends CartException\n{\n    {|shopware.migration.declaration.type:public function label(): ?string {}|}\n}\n",
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartException;\nclass Mine extends CartException\n{\n    public function label(): string {}\n}\n",
        ),
        (
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\Address;\nuse Shopware\\Core\\Checkout\\Cart\\CartException;\nclass Mine extends CartException\n{\n    public function setAddress({|shopware.migration.declaration.type:Address $address|}): void {}\n}\n",
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\Address;\nuse Shopware\\Core\\Checkout\\Cart\\CartException;\nclass Mine extends CartException\n{\n    public function setAddress(?Address $address): void {}\n}\n",
        ),
    ] {
        check_only_migration_fix(
            &migration(&index, 7),
            "file:///project/src/Mine.php",
            before,
            DECLARATION_MIGRATION_FIX_ID,
            after,
        );
    }
}

#[test]
fn overrides_add_announced_optional_parameters() {
    let index = announcing_index();
    let before = "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartException;\nclass Mine extends CartException\n{\n    {|shopware.migration.declaration.parameter.add:{|shopware.migration.declaration.parameter.add:public function load(string $token): void {}|}|}\n}\n";
    let (_, problems) = check_diagnostics(
        migration(&index, 7).as_ref(),
        "file:///project/src/Mine.php",
        before,
    );
    let messages: Vec<&str> = problems
        .iter()
        .map(|problem| problem.message.as_str())
        .collect();
    assert_eq!(
        messages,
        [
            "Shopware 6.8: add $cart to load()",
            "Shopware 6.8: add $locale to load()"
        ]
    );
    let replacements: Vec<String> = problems
        .iter()
        .map(|problem| {
            problem.fixes[0].payload.as_ref().unwrap()["replacement"]
                .as_str()
                .unwrap()
                .to_owned()
        })
        .collect();
    assert_eq!(
        replacements,
        [
            "?\\Shopware\\Core\\Checkout\\Cart\\Cart $cart = null",
            "string $locale = 'en_GB'"
        ]
    );
}

#[test]
fn settled_overrides_and_unrelated_classes_are_not_reported() {
    let index = announcing_index();
    for source in [
        // Already the announced types and parameters.
        "<?php\nuse Shopware\\Core\\Checkout\\Cart\\Address;\nuse Shopware\\Core\\Checkout\\Cart\\Cart;\nuse Shopware\\Core\\Checkout\\Cart\\CartException;\nclass Mine extends CartException\n{\n    public static function invalid(): self {}\n    public function label(): string {}\n    public function setAddress(?Address $address): void {}\n    public function load(string $token, ?Cart $cart = null, string $locale = 'en_GB'): void {}\n}\n",
        // Another type than the inherited one: not ours to rewrite.
        "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartException;\nclass Mine extends CartException\n{\n    public static function invalid(): static {}\n}\n",
        // The override announces the change itself.
        "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartException;\nuse Shopware\\Core\\Framework\\Deprecation\\BCChange\\ReturnTypeNarrowing;\nclass Mine extends CartException\n{\n    #[ReturnTypeNarrowing(version: 'v6.8.0', newType: 'string')]\n    public function label(): ?string {}\n}\n",
        // Same method names without the Shopware parent.
        "<?php\nclass Mine\n{\n    public function label(): ?string {}\n    public function load(string $token): void {}\n}\n",
    ] {
        check_diagnostics(
            migration(&index, 7).as_ref(),
            "file:///project/src/Mine.php",
            source,
        );
    }
}

#[test]
fn constructor_calls_keep_a_default_that_is_about_to_change() {
    let index = announcing_index();
    check_only_migration_fix(
        &migration(&index, 7),
        "file:///project/src/Definition.php",
        "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\CreatedByField;\n$field = new {|shopware.migration.arguments.add_default:CreatedByField|}();\n$other = new CreatedByField(['user']);\n$named = new CreatedByField(allowedWriteScopes: []);\n",
        ADD_DEFAULT_ARGUMENT_FIX_ID,
        "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\CreatedByField;\n$field = new CreatedByField(allowedWriteScopes: [\\Shopware\\Core\\Framework\\Context::SYSTEM_SCOPE]);\n$other = new CreatedByField(['user']);\n$named = new CreatedByField(allowedWriteScopes: []);\n",
    );
}

#[test]
fn announced_class_moves_are_renamed() {
    let index = announcing_index();
    check_only_migration_fix(
        &migration(&index, 7),
        "file:///project/src/Listener.php",
        "<?php\nfunction notify({|shopware.migration.api.class.rename:\\Shopware\\Administration\\Notification\\NotificationEntity|} $entity): void {}\n",
        API_RENAME_FIX_ID,
        "<?php\nfunction notify(\\Shopware\\Core\\Framework\\Notification\\NotificationEntity $entity): void {}\n",
    );
}
