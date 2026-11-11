use super::*;

/// A slice of Shopware announcing changes that calls, property accesses
/// and subclasses cannot follow mechanically.
fn announcing_index() -> TestIndex {
    let index = migration_inspection_php_index();
    index.add(
        "/project/vendor/Announced.php",
        r#"<?php
namespace Shopware\Core\Checkout\Cart;

use Shopware\Core\Framework\Deprecation\BCChange\BecomesAbstract;
use Shopware\Core\Framework\Deprecation\BCChange\BecomesFinal;
use Shopware\Core\Framework\Deprecation\BCChange\BecomesInternal;
use Shopware\Core\Framework\Deprecation\BCChange\BecomesReadonly;
use Shopware\Core\Framework\Deprecation\BCChange\NewOptionalParameter;
use Shopware\Core\Framework\Deprecation\BCChange\NewRequiredParameter;
use Shopware\Core\Framework\Deprecation\BCChange\ParameterNameChange;
use Shopware\Core\Framework\Deprecation\BCChange\ParameterRemoval;
use Shopware\Core\Framework\Deprecation\BCChange\ParameterTypeNarrowing;
use Shopware\Core\Framework\Deprecation\BCChange\PropertyTypeNarrowing;
use Shopware\Core\Framework\Deprecation\BCChange\PropertyTypeWidening;
use Shopware\Core\Framework\Deprecation\BCChange\ReturnTypeWidening;
use Shopware\Core\Framework\Deprecation\BCChange\VisibilityChange;

class Cart
{
    public string $token = '';

    public function getToken(): string { return ''; }
}

class Labels
{
    #[PropertyTypeWidening(version: 'v6.8.0', newType: 'int|string')]
    public int $count = 0;

    #[ReturnTypeWidening(version: 'v6.8.0', newType: '?string')]
    public function getLabel(): string { return ''; }

    #[ReturnTypeWidening(version: 'v6.8.0', newType: '?' . Cart::class)]
    public function getCart(): Cart { return new Cart(); }

    public function __construct(string $label = '') {}

    public static function of(string $label): self { return new self($label); }
}

class LegacyExtension
{
    /**
     * @abstract
     */
    public function getEntityName(): string { return ''; }
}

class LineItem
{
    #[BecomesReadonly(version: 'v6.8.0')]
    public string $id = '';

    #[PropertyTypeNarrowing(version: 'v6.8.0', newType: 'int')]
    public int|string $position = 0;

    #[VisibilityChange(version: 'v6.8.0', newVisibility: 'protected')]
    public array $payload = [];

    #[VisibilityChange(version: 'v6.8.0', newVisibility: 'private')]
    protected array $states = [];
}

class CartService
{
    #[BecomesInternal(version: 'v6.8.0')]
    public function recalculate(): void {}

    #[VisibilityChange(version: 'v6.8.0', newVisibility: 'protected')]
    public function persist(): void {}

    #[ParameterRemoval(version: 'v6.8.0', parameterName: 'legacy')]
    #[ParameterNameChange(version: 'v6.8.0', parameterName: 'token', newName: 'cartToken')]
    public function load(string $token, bool $legacy = false): void {}

    #[NewRequiredParameter(version: 'v6.8.0', parameterName: 'locale', parameterType: 'string')]
    #[NewOptionalParameter(version: 'v6.8.0', parameterName: 'cart', parameterType: '?' . Cart::class)]
    public function order(string $id): void {}

    #[ParameterTypeNarrowing(version: 'v6.8.0', parameterName: 'quantity', newType: 'int')]
    public function add(int|string $quantity): void {}

    #[BecomesAbstract(version: 'v6.8.0')]
    public function getDecorated(): self { return $this; }

    public function plain(): void {}
}

#[BecomesFinal(version: 'v6.8.0')]
class Calculator
{
    #[NewRequiredParameter(version: 'v6.8.0', parameterName: 'precision', parameterType: 'int')]
    public function __construct() {}
}

#[BecomesInternal(version: 'v6.9.0')]
class Processor
{
    public static function create(): self { return new self(); }

    public function process(): void {}
}
"#,
    );
    index
}

fn check(uri: &str, fixture: &str) -> Vec<String> {
    let index = announcing_index();
    let (document, problems) = check_diagnostics(migration(&index, 7).as_ref(), uri, fixture);
    assert!(document.parse_errors().is_empty());
    problems
        .iter()
        .map(|problem| {
            assert!(problem.fixes.is_empty(), "{problem:?}");
            problem.message.clone()
        })
        .collect()
}

#[test]
fn calls_report_the_changes_they_do_not_survive() {
    let messages = check(
        "file:///project/src/Calls.php",
        r"<?php
namespace Acme\Plugin;

use Shopware\Core\Checkout\Cart\Calculator;
use Shopware\Core\Checkout\Cart\Cart;
use Shopware\Core\Checkout\Cart\CartService;
use Shopware\Core\Checkout\Cart\Processor;

function run(CartService $service, Processor $processor, int|string $maybe): void
{
    $service->{|shopware.migration.announced.internal:recalculate|}();
    $service->{|shopware.migration.announced.visibility:persist|}();
    $service->load('token', {|shopware.migration.announced.parameter.removed:true|});
    $service->load({|shopware.migration.announced.parameter.renamed:token|}: 'token');
    $service->load('token');
    $service->{|shopware.migration.announced.parameter.required:order|}('id');
    $service->order('id', 'en-GB', new Cart());
    $service->order('id', {|shopware.migration.announced.parameter.type:12|}, {|shopware.migration.announced.parameter.type:'cart'|});
    $service->add({|shopware.migration.announced.parameter.type:'three'|});
    $service->add(3);
    $service->add($maybe);
    $service->order(...['id']);
    $service->plain();
    new {|shopware.migration.announced.parameter.required:Calculator|}();
    new Calculator(2);
    $processor->{|shopware.migration.announced.internal:process|}();
    Processor::{|shopware.migration.announced.internal:create|}();
    new {|shopware.migration.announced.internal:Processor|}();
}
",
    );
    assert_eq!(
        messages,
        [
            "Shopware 6.8: Shopware\\Core\\Checkout\\Cart\\CartService::recalculate() becomes internal; stop calling it",
            "Shopware 6.8: Shopware\\Core\\Checkout\\Cart\\CartService::persist() becomes protected; this call will no longer be allowed",
            "Shopware 6.8: $legacy of Shopware\\Core\\Checkout\\Cart\\CartService::load() is removed; stop passing it",
            "Shopware 6.8: $token of Shopware\\Core\\Checkout\\Cart\\CartService::load() is renamed to $cartToken; pass it by position",
            "Shopware 6.8: Shopware\\Core\\Checkout\\Cart\\CartService::order() requires $locale; pass it by position now",
            "Shopware 6.8: $locale of Shopware\\Core\\Checkout\\Cart\\CartService::order() requires string, but 12 is passed",
            "Shopware 6.8: $cart of Shopware\\Core\\Checkout\\Cart\\CartService::order() requires ?\\Shopware\\Core\\Checkout\\Cart\\Cart, but \"cart\" is passed",
            "Shopware 6.8: $quantity of Shopware\\Core\\Checkout\\Cart\\CartService::add() is narrowed to int, but \"three\" is passed",
            "Shopware 6.8: Shopware\\Core\\Checkout\\Cart\\Calculator::__construct() requires $precision; pass it by position now",
            "Shopware 6.9: Shopware\\Core\\Checkout\\Cart\\Processor becomes internal; stop using it",
            "Shopware 6.9: Shopware\\Core\\Checkout\\Cart\\Processor becomes internal; stop using it",
            "Shopware 6.9: Shopware\\Core\\Checkout\\Cart\\Processor becomes internal; stop using it",
        ]
    );
}

#[test]
fn property_accesses_report_the_changes_they_do_not_survive() {
    check(
        "file:///project/src/Properties.php",
        r"<?php
namespace Acme\Plugin;

use Shopware\Core\Checkout\Cart\LineItem;

function run(LineItem $item): void
{
    $item->{|shopware.migration.announced.property.readonly:id|} = 'other';
    echo $item->id;
    $item->position = {|shopware.migration.announced.property.type:'first'|};
    $item->position = 1;
    echo $item->{|shopware.migration.announced.visibility:payload|};
}

class Mine extends LineItem
{
    {|shopware.migration.announced.property.redeclared:public string $id = 'mine';|}

    {|shopware.migration.announced.property.redeclared:protected array $states = ['mine'];|}

    public function payload(): array
    {
        return $this->payload;
    }
}
",
    );
}

#[test]
fn subclasses_report_the_changes_they_do_not_survive() {
    let messages = check(
        "file:///project/src/Subclasses.php",
        r"<?php
namespace Acme\Plugin;

use Shopware\Core\Checkout\Cart\Calculator;
use Shopware\Core\Checkout\Cart\CartService;
use Shopware\Core\Checkout\Cart\Processor;

class MyCalculator extends {|shopware.migration.announced.final:Calculator|} {}

class MyProcessor extends {|shopware.migration.announced.internal:Processor|} {}

class {|shopware.migration.announced.abstract:Decorator|} extends CartService {}

class Implemented extends CartService
{
    public function getDecorated(): CartService { return $this; }
}

class Inherits extends Implemented {}

abstract class StillAbstract extends CartService {}
",
    );
    assert_eq!(
        messages,
        [
            "Shopware 6.8: Shopware\\Core\\Checkout\\Cart\\Calculator becomes final; MyCalculator can no longer extend it",
            "Shopware 6.9: Shopware\\Core\\Checkout\\Cart\\Processor becomes internal; stop extending it",
            "Shopware 6.8: Shopware\\Core\\Checkout\\Cart\\CartService::getDecorated() becomes abstract; implement it in Decorator",
        ]
    );
}

#[test]
fn shopware_itself_and_code_removed_with_the_change_are_left_alone() {
    check(
        "file:///project/vendor/shopware/core/Checkout/Cart/Own.php",
        r"<?php
namespace Shopware\Core\Checkout\Cart;

function run(CartService $service): void
{
    $service->recalculate();
    new Processor();
}

class Own extends Processor {}
",
    );
    check(
        "file:///project/src/Staged.php",
        r"<?php
namespace Acme\Plugin;

use Shopware\Core\Checkout\Cart\Calculator;
use Shopware\Core\Checkout\Cart\CartService;

/**
 * @deprecated tag:v6.8.0 - will be removed
 */
class Legacy extends Calculator
{
    public function run(CartService $service): void
    {
        $service->recalculate();
    }
}

class Current
{
    /**
     * @deprecated tag:v6.8.0 - will be removed
     */
    public function run(CartService $service): void
    {
        $service->recalculate();
    }
}
",
    );
}

#[test]
fn uses_of_widened_types_report_what_they_do_not_accept() {
    let messages = check(
        "file:///project/src/Widened.php",
        r"<?php
namespace Acme\Plugin;

use Shopware\Core\Checkout\Cart\Labels;

function takesString(string $value): void {}
function takesNullable(?string $value): void {}
function takesInt(int $value): void {}
function takesAny(int|string $value): void {}

function widened(Labels $labels): string
{
    takesString($labels->{|shopware.migration.announced.type.widened:getLabel|}());
    takesString(value: ($labels->{|shopware.migration.announced.type.widened:getLabel|}()));
    takesNullable($labels->getLabel());
    Labels::of($labels->{|shopware.migration.announced.type.widened:getLabel|}());
    new Labels($labels->{|shopware.migration.announced.type.widened:getLabel|}());
    $labels->{|shopware.migration.announced.type.widened:getCart|}()->getToken();
    $labels->{|shopware.migration.announced.type.widened:getCart|}()->token;
    $labels->getCart()?->getToken();
    takesInt($labels->{|shopware.migration.announced.type.widened:count|});
    takesAny($labels->count);
    $labels->count = 2;
    $label = $labels->getLabel();

    return $labels->{|shopware.migration.announced.type.widened:getLabel|}();
}

function nullable(Labels $labels): ?string
{
    return $labels->getLabel();
}
",
    );
    assert_eq!(
        [&messages[0], &messages[4], &messages[6]],
        [
            "Shopware 6.8: Shopware\\Core\\Checkout\\Cart\\Labels::getLabel() returns ?string; string is expected here",
            "Shopware 6.8: Shopware\\Core\\Checkout\\Cart\\Labels::getCart() returns ?\\Shopware\\Core\\Checkout\\Cart\\Cart; it may be null here",
            "Shopware 6.8: Shopware\\Core\\Checkout\\Cart\\Labels::$count becomes int|string; int is expected here",
        ]
    );
}

#[test]
fn methods_marked_abstract_in_their_phpdoc_need_an_implementation() {
    let messages = check(
        "file:///project/src/Extensions.php",
        r"<?php
namespace Acme\Plugin;

use Shopware\Core\Checkout\Cart\LegacyExtension;

class {|shopware.migration.announced.abstract:Missing|} extends LegacyExtension {}

class Present extends LegacyExtension
{
    public function getEntityName(): string { return 'acme'; }
}

abstract class {|shopware.migration.announced.abstract:Deferred|} extends LegacyExtension {}
",
    );
    assert_eq!(
        messages,
        [
            "Shopware: Shopware\\Core\\Checkout\\Cart\\LegacyExtension::getEntityName() is marked @abstract and becomes abstract; implement it in Missing",
            "Shopware: Shopware\\Core\\Checkout\\Cart\\LegacyExtension::getEntityName() is marked @abstract and becomes abstract; implement it in Deferred",
        ]
    );
}

#[test]
fn widened_types_are_followed_through_variables() {
    let messages = check(
        "file:///project/src/Followed.php",
        r"<?php
namespace Acme\Plugin;

use Shopware\Core\Checkout\Cart\Labels;

function takesString(string $value): void {}
function takesNullable(?string $value): void {}

function followed(Labels $labels): string
{
    $label = $labels->getLabel();
    takesNullable($label);
    {|shopware.migration.announced.type.widened:takesString($label)|};
    if ($label !== null) {
        takesString($label);
    }

    $cart = $labels->getCart();
    {|shopware.migration.announced.type.widened:$cart|}->getToken();
    $cart?->getToken();
    if ($cart !== null) {
        $cart->getToken();
    }

    {|shopware.migration.announced.type.widened:return $label;|}
}
",
    );
    // Two widened declarations are used, so the message names both.
    assert_eq!(
        messages,
        [
            "Shopware announces wider types (Shopware\\Core\\Checkout\\Cart\\Labels::getLabel, Shopware\\Core\\Checkout\\Cart\\Labels::getCart); then no matching signature for takesString(): parameter #1 ($value) expects string, null|string given",
            "Shopware announces wider types (Shopware\\Core\\Checkout\\Cart\\Labels::getLabel, Shopware\\Core\\Checkout\\Cart\\Labels::getCart); it may be null here",
            "Shopware announces wider types (Shopware\\Core\\Checkout\\Cart\\Labels::getLabel, Shopware\\Core\\Checkout\\Cart\\Labels::getCart); then expected return type string, got null|string",
        ]
    );
}
