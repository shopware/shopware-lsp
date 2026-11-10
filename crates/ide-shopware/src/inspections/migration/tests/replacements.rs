use super::super::replacements::{
    CART_BEHAVIOR_PERMISSION_FIX_ID, FILTERABLE_PRICE_DEFINITION_FIX_ID, REFLECTION_HELPER_FIX_ID,
};
use super::*;

fn replacement_index() -> TestIndex {
    let index = migration_inspection_php_index();
    for (path, source) in [
        (
            "/project/vendor/CartBehavior.php",
            "<?php\nnamespace Shopware\\Core\\Checkout\\Cart;\nclass CartBehavior { public function isRecalculation(): bool {} }",
        ),
        (
            "/project/vendor/PriceDefinitionInterface.php",
            "<?php\nnamespace Shopware\\Core\\Checkout\\Cart\\Price\\Struct;\ninterface PriceDefinitionInterface {}\ninterface FilterableInterface {}",
        ),
    ] {
        index.add(path, source);
    }
    index
}

#[test]
fn cart_behavior_recalculation_becomes_a_permission_check() {
    let index = replacement_index();
    let before = "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartBehavior;\nfunction skip(CartBehavior $behavior, ?CartBehavior $other): bool\n{\n    return $behavior->{|shopware.migration.cart_behavior.permission:isRecalculation|}();\n}\n";
    check_only_migration_fix(
        &migration(&index, 8),
        "file:///project/src/Cart.php",
        before,
        CART_BEHAVIOR_PERMISSION_FIX_ID,
        "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartBehavior;\nuse Shopware\\Core\\Checkout\\CheckoutPermissions;\nfunction skip(CartBehavior $behavior, ?CartBehavior $other): bool\n{\n    return $behavior->hasPermission(CheckoutPermissions::SKIP_CART_PERSISTENCE);\n}\n",
    );
    // Not before Shopware 6.8, and not on another class.
    let plain = before
        .replace("{|shopware.migration.cart_behavior.permission:", "")
        .replace("|}", "");
    check_diagnostics(
        migration(&index, 7).as_ref(),
        "file:///project/src/Cart.php",
        &plain,
    );
    check_diagnostics(
        migration(&index, 8).as_ref(),
        "file:///project/src/Cart.php",
        "<?php\nclass Other { public function isRecalculation(): bool {} }\nfunction skip(Other $behavior): bool { return $behavior->isRecalculation(); }\n",
    );
}

#[test]
fn filterable_price_definitions_declare_the_interface() {
    let index = replacement_index();
    check_only_migration_fix(
        &migration(&index, 7),
        "file:///project/src/Price.php",
        "<?php\nuse Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\PriceDefinitionInterface;\nuse Shopware\\Core\\Framework\\Rule\\Rule;\nclass {|shopware.migration.price_definition.filterable:Price|} implements PriceDefinitionInterface\n{\n    public function getFilter(): ?Rule { return null; }\n}\n",
        FILTERABLE_PRICE_DEFINITION_FIX_ID,
        "<?php\nuse Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\PriceDefinitionInterface;\nuse Shopware\\Core\\Framework\\Rule\\Rule;\nuse Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\FilterableInterface;\nclass Price implements PriceDefinitionInterface, FilterableInterface\n{\n    public function getFilter(): ?Rule { return null; }\n}\n",
    );
    for source in [
        // Already declared.
        "<?php\nuse Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\FilterableInterface;\nuse Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\PriceDefinitionInterface;\nuse Shopware\\Core\\Framework\\Rule\\Rule;\nclass Price implements PriceDefinitionInterface, FilterableInterface\n{\n    public function getFilter(): ?Rule { return null; }\n}\n",
        // Another getFilter().
        "<?php\nuse Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\PriceDefinitionInterface;\nclass Price implements PriceDefinitionInterface\n{\n    public function getFilter(): ?string { return null; }\n}\n",
        // No price definition.
        "<?php\nuse Shopware\\Core\\Framework\\Rule\\Rule;\nclass Price\n{\n    public function getFilter(): ?Rule { return null; }\n}\n",
    ] {
        check_diagnostics(
            migration(&index, 7).as_ref(),
            "file:///project/src/Price.php",
            source,
        );
    }
}

#[test]
fn reflection_helper_calls_become_native_reflection() {
    let index = replacement_index();
    let import = "<?php\nuse Shopware\\Core\\Framework\\Test\\TestCaseHelper\\ReflectionHelper;\n";
    for (before, after) in [
        (
            "$m = ReflectionHelper::{|shopware.migration.reflection_helper.native:getMethod|}(Foo::class, 'run');\n",
            "$m = new \\ReflectionMethod(Foo::class, 'run');\n",
        ),
        (
            "ReflectionHelper::{|shopware.migration.reflection_helper.native:getProperty|}(Foo::class, 'name')->setValue($foo, 1);\n",
            "(new \\ReflectionProperty(Foo::class, 'name'))->setValue($foo, 1);\n",
        ),
        (
            "$v = ReflectionHelper::{|shopware.migration.reflection_helper.native:getPropertyValue|}($foo, 'name');\n",
            "$v = (new \\ReflectionProperty($foo, 'name'))->getValue($foo);\n",
        ),
        (
            "$f = ReflectionHelper::{|shopware.migration.reflection_helper.native:getFileName|}(Foo::class);\n",
            "$f = (new \\ReflectionClass(Foo::class))->getFileName();\n",
        ),
    ] {
        check_only_migration_fix(
            &migration(&index, 7),
            "file:///project/tests/FooTest.php",
            &format!("{import}{before}"),
            REFLECTION_HELPER_FIX_ID,
            &format!("<?php\n{after}"),
        );
    }
    // Reported without a fix: the object expression would run twice, and
    // named arguments have other names in native reflection.
    for source in [
        "$v = ReflectionHelper::{|shopware.migration.reflection_helper.native:getPropertyValue|}(make(), 'name');\n",
        "$m = ReflectionHelper::{|shopware.migration.reflection_helper.native:getMethod|}(methodName: 'run', className: Foo::class);\n",
    ] {
        let (_, problems) = check_diagnostics(
            migration(&index, 7).as_ref(),
            "file:///project/tests/FooTest.php",
            &format!("{import}{source}"),
        );
        assert!(problems[0].fixes.is_empty());
    }
}
