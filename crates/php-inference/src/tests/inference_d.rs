use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{PHP_MEMBER_CALL, PHP_NUMBER, PHP_VARIABLE};
use swlsp_php_binder::link;
use swlsp_php_semantic::{
    CallReturnContract, CallReturnKind, CallValueKind, Document, Snapshot, SymbolId, SymbolKind,
    node_identity,
};
use swlsp_php_types::{Kind as TypeKind, Type};

use super::{
    Parsed, all_messages, analyze_linked, analyze_source, analyzer, assert_node_type,
    assert_text_type, bind, builtins, contains_message, parse, snapshot, symbol_named,
};
use crate::{AttributeContracts, CallArgument, CallContext, CallContracts, Extension};

#[track_caller]
fn assert_no_issue(document: &Document, code: &str) {
    for issue in &document.issues {
        assert_ne!(&*issue.code, code, "{}", issue.message);
    }
}

fn analyze_plain(path: &str, source: &str) -> (Parsed, Document) {
    let (parsed, document, _) = analyze_source(path, source, false, Vec::new());
    (parsed, document)
}

fn call_contracts() -> Vec<Arc<dyn Extension>> {
    vec![Arc::new(CallContracts)]
}

/// A metadata document holding the contracts parsed from `meta`.
fn meta_document(meta: &str) -> Document {
    let parsed = parse(meta);
    Document {
        path: "/.phpstorm.meta.php".into(),
        call_contracts: swlsp_php_phpstormmeta::parse(&parsed.root),
        ..Document::default()
    }
}

/// Mirrors `extensions::call_contracts::evaluate_call_return_contract`
/// for a context without a call node.
fn evaluate_call_return_contract(
    context: &CallContext<'_>,
    contract: &CallReturnContract,
) -> Option<Type> {
    let snapshot = context.snapshot?;
    swlsp_php_resolver::effective_call_return_type(
        snapshot.relations(),
        context.arguments,
        std::slice::from_ref(contract),
    )
}

#[test]
fn generator_bare_return_is_not_validated_as_object_return() {
    let source = r#"<?php
class Generator {}
function values(bool $enabled): Generator {
    if (!$enabled) {
        return;
    }
    yield 'value';
}"#;
    let (_, analyzed) = analyze_plain("/generator.php", source);
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn empty_array_satisfies_typed_array_and_list_returns() {
    let source = r#"<?php
/** @return list<string> */
function names(): array {
    return [];
}
/** @return array<string, mixed> */
function values(): array {
    return [];
}"#;
    let (_, analyzed) = analyze_plain("/empty-array.php", source);
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn associative_array_literal_preserves_shape() {
    let source = r#"<?php
/**
 * @return array{uri: string, mimeType: string, text: string}
 */
function resource(): array {
    return [
        'uri' => 'shopware://entities',
        'mimeType' => 'application/json',
        'text' => '{}',
    ];
}

/**
 * @param array<string, string> $defaults
 * @return array<string, string>
 */
function merged(array $defaults): array {
    return [...$defaults, ...['field' => 'value']];
}
"#;
    let (parsed, analyzed) = analyze_plain("/array-shape-literal.php", source);
    assert_text_type(
        &analyzed,
        &parsed.root,
        "[
        'uri' => 'shopware://entities',
        'mimeType' => 'application/json',
        'text' => '{}',
    ]",
        r#"array{mimeType:"application/json",text:"{}",uri:"shopware://entities"}"#,
    );
    assert_text_type(
        &analyzed,
        &parsed.root,
        "[...$defaults, ...['field' => 'value']]",
        "non-empty-array<string,string>",
    );
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn integer_key_array_spread_produces_list() {
    let source = r#"<?php
/** @return list<string> */
function names(): array {}
/** @return array<int, string> */
function indexedNames(): array {}
$list = [...names(), 'tail'];
$reindexed = [...indexedNames()];
"#;
    let (parsed, analyzed) = analyze_plain("/list-spread.php", source);
    assert_text_type(
        &analyzed,
        &parsed.root,
        "[...names(), 'tail']",
        "non-empty-list<string>",
    );
    assert_text_type(
        &analyzed,
        &parsed.root,
        "[...indexedNames()]",
        "list<string>",
    );
}

#[test]
fn concrete_trait_method_satisfies_abstract_trait_requirement() {
    let source = r#"<?php
interface Contract {
    public function value(): string;
}
trait RequiresValue {
    abstract public function value(): string;
}
trait ProvidesValue {
    public function value(): string {
        return 'value';
    }
}
class Service implements Contract {
    use RequiresValue;
    use ProvidesValue;
}
"#;
    let (_, analyzed) = analyze_plain("/traits.php", source);
    assert_no_issue(&analyzed, "php.abstract");
}

#[test]
fn backed_enum_runtime_methods_do_not_produce_override_diagnostics() {
    let source = r#"<?php
enum Status: string {
    case Active = 'active';
}
"#;
    let (_, analyzed, _) = analyze_source("/status.php", source, true, Vec::new());
    assert_no_issue(&analyzed, "php.override");
}

#[test]
fn redis_extension_signatures_accept_compatible_test_doubles() {
    let source = r#"<?php
class RedisStub extends Redis {
    public function get(string $key): mixed {}
    public function set(string $key, mixed $value, mixed $options = null): Redis|string|bool {}
    public function del(array|string $key, string ...$other_keys): Redis|int|false {}
    public function keys(string $pattern): Redis|array|false {}
    public function sAdd(string $key, mixed $value, mixed ...$other_values): Redis|int|false {}
    public function sMembers(string $key): Redis|array|false {}
    public function multi(int $value = Redis::MULTI): Redis|bool {}
    public function exec(): array {}
}
"#;
    let (_, analyzed, _) = analyze_source("/redis-stub.php", source, true, Vec::new());
    assert_no_issue(&analyzed, "php.override");
}

#[test]
fn inherited_generic_constructor_accepts_subclass_elements() {
    let source = r#"<?php
/** @template TElement */
abstract class Collection {
    /** @param iterable<TElement> $elements */
    public function __construct(iterable $elements = []) {}
    /** @param TElement $element */
    public function add($element): void {}
}
class Field {}
class IdField extends Field {
    /** @return static */
    public function addFlags(): self {
        return $this;
    }
}
/** @extends Collection<Field> */
class FieldCollection extends Collection {}
function fields(): FieldCollection {
    $fields = new FieldCollection([(new IdField())->addFlags()]);
    $fields->add(new IdField());
    return $fields;
}"#;
    let (_, analyzed) = analyze_plain("/inherited-constructor.php", source);
    assert_no_issue(&analyzed, "php.arguments");
}

#[test]
fn untyped_method_parameter_accepts_known_argument() {
    let source = r#"<?php
class Collection {
    public function add($element): void {}
}
function append(Collection $items): void {
    $items->add('value');
}"#;
    let (_, analyzed) = analyze_plain("/untyped-method.php", source);
    assert_no_issue(&analyzed, "php.arguments");
}

#[test]
fn mixed_value_does_not_produce_unprovable_return_mismatch() {
    let source = r#"<?php
function value(mixed $input): string {
    return $input;
}"#;
    let (_, analyzed) = analyze_plain("/mixed-return.php", source);
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn variadic_parameter_is_a_list_inside_function() {
    let source = r#"<?php
function values(?string ...$values): array {
    return $values;
}"#;
    let (parsed, analyzed) = analyze_plain("/variadic.php", source);
    assert_node_type(&analyzed, &parsed.root, PHP_VARIABLE, "list<null|string>");
}

#[test]
fn object_creation_inside_function_argument_does_not_recurse() {
    let source = r#"<?php
class Product {
    public function __construct(string $name) {}
}
function consume(Product $product): void {}
function run(): void {
    consume(new Product('demo'));
}"#;
    let (parsed, analyzed) = analyze_plain("/nested-constructor.php", source);
    assert_text_type(&analyzed, &parsed.root, "new Product('demo')", "Product");
}

#[test]
fn large_literal_array_widens_instead_of_growing_unbounded_union() {
    let mut source = String::from("<?php $messages = [");
    for index in 0..1_000 {
        if index > 0 {
            source.push(',');
        }
        source.push_str("'message-");
        source.push_str(&index.to_string());
        source.push('\'');
    }
    source.push_str("];");

    let (parsed, analyzed) = analyze_plain("/locale.php", &source);
    let arrays = query::arrays(&parsed.root);
    assert_eq!(arrays.len(), 1);
    assert_eq!(
        analyzed.type_of(&arrays[0]).ty.to_string(),
        "non-empty-list<string>"
    );
}

#[test]
fn numeric_literal_inference_excludes_leading_comments() {
    let source = r#"<?php
$descriptors = [
    // stdin
    1,
    // stdout
    2,
];"#;
    let (parsed, analyzed) = analyze_plain("/descriptors.php", source);
    let numbers = query::nodes(&parsed.root, &[PHP_NUMBER]);
    assert_eq!(numbers.len(), 2);
    assert_eq!(analyzed.type_of(&numbers[0]).ty.to_string(), "1");
    assert_eq!(analyzed.type_of(&numbers[1]).ty.to_string(), "2");
}

#[test]
fn unknown_expressions_do_not_create_redundant_type_facts() {
    let source = r#"<?php
function run($service) {
    return $service->missing();
}"#;
    let (parsed, analyzed) = analyze_plain("/unknown.php", source);
    let calls = query::nodes(&parsed.root, &[PHP_MEMBER_CALL]);
    assert_eq!(calls.len(), 1);
    assert!(analyzed.type_of(&calls[0]).ty.is_unknown());
    assert!(!analyzed.type_facts.contains_key(&node_identity(&calls[0])));
}

#[test]
fn assignment_updates_local_completion_type() {
    let source = r#"<?php
function run(): void {
    $value = 'first';
    $value = 42;
}"#;
    let (_, analyzed) = analyze_plain("/locals.php", source);
    let symbol = analyzed
        .symbols
        .iter()
        .find(|symbol| symbol.kind == SymbolKind::Local && &*symbol.name == "$value")
        .expect("missing local symbol");
    assert_eq!(symbol.ty.to_string(), r#""first"|42"#);
}

#[test]
fn declaration_compatibility_diagnostics() {
    let source = r#"<?php
class Product {}
interface Contract {
    public function transform(object $value): object;
    public function missing(): void;
}
class Base {
    final protected function sealed(): void {}
    public function transform(Product $value): object { return $value; }
    protected string $name;
}
class Child extends Base implements Contract {
    private function sealed(): void {}
    public function transform(Product $value): string { return 'wrong'; }
    public int $name;
    #[Override]
    public function stray(): void {}
}
final class Closed {}
class Invalid extends Closed {}
"#;
    let (_, analyzed) = analyze_plain("/declarations.php", source);
    let messages: Vec<String> = analyzed
        .issues
        .iter()
        .map(|issue| issue.message.to_string())
        .collect();
    for needle in [
        "cannot override final member",
        "incompatible signature",
        "must keep the invariant type string",
        "marked #[Override]",
        "must implement abstract method",
        "cannot extend final class Closed",
    ] {
        assert!(
            contains_message(&messages, needle),
            "missing {needle:?} in {messages:?}"
        );
    }
}

#[test]
fn class_aliases_of_final_and_abstract_classes_are_not_subclasses() {
    let source = r#"<?php
final class Closed {}
abstract class Route { abstract public function load(): void; }
class_alias(Closed::class, 'Legacy\\Closed');
class_alias(Route::class, 'Legacy\\Route');
"#;
    let (_, analyzed) = analyze_plain("/aliases.php", source);
    let messages: Vec<String> = analyzed
        .issues
        .iter()
        .map(|issue| issue.message.to_string())
        .collect();
    assert!(messages.is_empty(), "{messages:?}");
}

#[test]
fn declaration_compatibility_accepts_variance() {
    let source = r#"<?php
class Entity {}
class Product extends Entity {}
interface Mapper {
    public function map(Product $value): Entity;
}
class ProductMapper implements Mapper {
    public function map(object $value): Product { return new Product(); }
}"#;
    let (_, analyzed) = analyze_plain("/variance.php", source);
    assert_no_issue(&analyzed, "php.override");
    assert_no_issue(&analyzed, "php.abstract");
}

#[test]
fn declaration_compatibility_honors_synthetic_and_staged_signatures() {
    let source = r#"<?php
namespace App;

use Shopware\Core\Framework\Deprecation\BCChange\NewOptionalParameter;

/** @method string future(string $value) */
interface FutureInterface {}
class FutureImplementation implements FutureInterface {
    public function future(int $value): int { return $value; }
}

interface TentativeReturn {
    public function current(): string;
}
class LegacyReturn implements TentativeReturn {
    #[\ReturnTypeWillChange]
    public function current() { return 'value'; }
}

trait Assigns {
    public function assign(array $values): static { return $this; }
}
class Values {
    use Assigns;
    public function assign(array $values): static { return $this; }
}

class StagedBase {
    #[NewOptionalParameter(
        version: 'v2',
        parameterName: 'silent',
        parameterType: 'bool',
        defaultValue: true,
    )]
    public function save(string $value): void {}
}
class StagedChild extends StagedBase {
    public function save(string $value): void {}
}
"#;
    let (_, analyzed) = analyze_plain("/compatibility-mechanisms.php", source);
    assert_no_issue(&analyzed, "php.override");
}

#[test]
fn late_static_return_from_parent_call_uses_current_class() {
    let source = r#"<?php
class Base {
    /** @return static */
    public function fluent(): self { return $this; }
}
class Child extends Base {
    public function child(): self {
        return parent::fluent();
    }
}
"#;
    let (parsed, analyzed) = analyze_plain("/late-static.php", source);
    assert_text_type(&analyzed, &parsed.root, "parent::fluent()", "Child");
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn strict_false_comparison_narrows_following_flow() {
    let source = r#"<?php
function locate(): int|false {}
function position(): int {
    $position = locate();
    if ($position === false) {
        return 0;
    }
    return $position;
}
"#;
    let (_, analyzed) = analyze_plain("/false-comparison.php", source);
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn spaceship_operator_returns_int() {
    let source = r#"<?php
function compare(string $left, string $right): int {
    return $left <=> $right;
}
"#;
    let (parsed, analyzed) = analyze_plain("/spaceship.php", source);
    assert_text_type(&analyzed, &parsed.root, "$left <=> $right", "int");
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn top_level_script_inference() {
    let source = r#"<?php
$name = 'shopware';
$length = strlen($name);
return $length;
"#;
    let (parsed, analyzed, _) = analyze_source("/config.php", source, false, builtins());
    assert_text_type(&analyzed, &parsed.root, "$length", "int");
    let mut found_local = false;
    for symbol in &analyzed.symbols {
        if symbol.kind == SymbolKind::Local && &*symbol.name == "$name" {
            assert_eq!(symbol.ty.to_string(), r#""shopware""#);
            found_local = true;
        }
    }
    assert!(found_local);
}

#[test]
fn assignment_guard_narrows_assigned_variable() {
    let source = r#"<?php
class Source {
    public function value(): ?string { return null; }
}
/** @param array<array-key, string> $values */
function accept(array $values): void {}
function run(Source $source): void {
    if (($value = $source->value()) !== null) {
        accept([$value]);
    }
}"#;
    let (parsed, analyzed) = analyze_plain("/assignment-guard.php", source);
    assert_text_type(&analyzed, &parsed.root, "[$value]", "array{0:string}");
    assert_no_issue(&analyzed, "php.arguments");
}

#[test]
fn same_document_return_inference_reaches_fixed_point() {
    let source = r#"<?php
class Values {
    public function raw() { return 'value'; }
    public function forwarded() { return $this->raw(); }
    public function finalValue(): string { return $this->forwarded(); }
    public function stable(): string { return 'stable'; }
}"#;
    let parsed = parse(source);
    let bound = bind("/fixed-point.php", &parsed);
    let snapshot: Arc<Snapshot> = snapshot(std::slice::from_ref(&bound));
    let mut analyzer = analyzer(&snapshot, Vec::new());
    let counts: Arc<Mutex<HashMap<SymbolId, usize>>> = Arc::default();
    let observed = counts.clone();
    analyzer.observe_function = Some(Arc::new(move |id: &SymbolId| {
        *observed.lock().unwrap().entry(id.clone()).or_default() += 1;
    }));
    let analyzed = analyzer.analyze(&link(&bound, &snapshot), &parsed.root);

    assert_eq!(
        symbol_named(&analyzed, "raw").return_type.to_string(),
        r#""value""#
    );
    assert_eq!(
        symbol_named(&analyzed, "forwarded").return_type.to_string(),
        r#""value""#
    );
    assert_text_type(&analyzed, &parsed.root, "$this->forwarded()", r#""value""#);
    let counts = counts.lock().unwrap();
    let count = |name: &str| {
        counts
            .get(&symbol_named(&analyzed, name).id)
            .copied()
            .unwrap_or(0)
    };
    assert_eq!(count("raw"), 1);
    assert_eq!(count("forwarded"), 2);
    assert_eq!(count("finalValue"), 2);
    assert_eq!(count("stable"), 1);
}

#[test]
fn foreach_value_does_not_replace_iterable_type() {
    let source = r#"<?php
/** @param list<string> $items */
function preserve($items) {
    foreach ($items as $item) {}
    return $items;
}"#;
    let (_, analyzed) = analyze_plain("/foreach.php", source);
    assert_eq!(
        symbol_named(&analyzed, "preserve").return_type.to_string(),
        "list<string>"
    );
}

#[test]
fn non_empty_array_and_list_operations_preserve_cardinality() {
    let source = r#"<?php
/** @param non-empty-array<string> $values
 *  @return non-empty-list<string>
 */
function reindex(array $values): array {
    return array_values($values);
}
/** @param list<string> $parents
 *  @return non-empty-list<string>
 */
function appendRequired(array $parents, string $current): array {
    return [...$parents, $current];
}
/** @return array<string, non-empty-list<string>> */
function grouped(): array {
    $result = [];
    $result['language'][] = 'id';
    return $result;
}
/** @return list<string> */
function tokens(string $value): array {
    return preg_split('/_+/', $value, -1, PREG_SPLIT_NO_EMPTY) ?: [];
}
"#;
    let (_, analyzed, _) =
        analyze_source("/non-empty-array-operations.php", source, true, builtins());
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn array_map_contextually_types_callback_parameters() {
    let source = r#"<?php
/** @template T */
class Result {
    /** @return T */
    public function get(): mixed {}
}
/**
 * @param list<Result<string|array<string, string>>> $results
 * @return list<string|array<string, string>>
 */
function values(array $results): array {
    return array_map(static fn (Result $result) => $result->get(), $results);
}
"#;
    let (_, analyzed, _) = analyze_source("/array-map-context.php", source, false, builtins());
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn optional_non_null_shape_field_stays_non_null_after_isset_branch() {
    let source = r#"<?php
/** @param array<string, string> $values
 *  @return array<string, string>
 */
function resolve(array $values): array { return $values; }
/**
 * @param array{imports: array<string, string>, scopes?: array<string, string>} $map
 * @return array{imports: array<string, string>, scopes?: array<string, string>}
 */
function importMap(array $map): array {
    if (isset($map['scopes'])) {
        $map['scopes'] = resolve($map['scopes']);
    }
    return $map;
}
"#;
    let (_, analyzed, _) = analyze_source("/isset-optional-shape.php", source, false, builtins());
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn php_stan_ignore_next_line_suppresses_diagnostic() {
    let source = r#"<?php
/** @return list<string> */
function values(): array {
    /** @phpstan-ignore-next-line return.type */
    return array_combine(['key'], ['value']);
}
"#;
    let (_, analyzed, _) = analyze_source("/phpstan-ignore.php", source, true, builtins());
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn php_stan_targeted_ignore_suppresses_cascading_diagnostic() {
    let source = r#"<?php
/** @return class-string|null */
function className(): ?string {
    /** @phpstan-ignore cast.string */
    return (string) arbitraryValue();
}
function arbitraryValue(): mixed {}
"#;
    let (_, analyzed) = analyze_plain("/phpstan-targeted-ignore.php", source);
    assert_no_issue(&analyzed, "php.returnType");
}

#[test]
fn php_stan_targeted_ignore_leaves_unrelated_diagnostic_on_same_line() {
    let source = r#"<?php
function needsArgument(string $value): int { return 1; }
function value(): string {
    /** @phpstan-ignore return.type */ return needsArgument();
}
"#;
    let (_, analyzed) = analyze_plain("/phpstan-targeted.php", source);
    let return_types = analyzed
        .issues
        .iter()
        .filter(|i| &*i.code == "php.returnType")
        .count();
    let arguments = analyzed
        .issues
        .iter()
        .filter(|i| &*i.code == "php.arguments")
        .count();
    assert_eq!(return_types, 0);
    assert!(arguments > 0);
}

#[test]
fn php_storm_meta_function_return_contracts() {
    let meta = meta_document(
        r#"<?php
namespace PHPSTORM_META;
override(\identity(0), type(0));
override(\first(0), elementType(0));
"#,
    );
    let parsed = parse(
        r#"<?php
class Product {}
function run(): void {
    $identity = identity(new Product());
    $first = first([new Product()]);
}
"#,
    );
    let bound = bind("/contracts.php", &parsed);
    let snapshot = snapshot(&[meta, bound.clone()]);
    let analyzed = analyze_linked(&snapshot, call_contracts(), &bound, &parsed);
    assert_text_type(
        &analyzed,
        &parsed.root,
        "identity(new Product())",
        "Product",
    );
    assert_text_type(&analyzed, &parsed.root, "first([new Product()])", "Product");
}

#[test]
fn no_return_attribute_conditional_exit_point() {
    let source = r#"<?php
use JetBrains\PhpStorm\NoReturn;

#[NoReturn(1)]
function stopOnOne(int $mode): void {}

#[NoReturn(NoReturn::ANY_ARGUMENT, true)]
function stopOnFlag(string $message, bool $exit): void {}

function runOne(): void {
    $continued = stopOnOne(2);
    $stopped = stopOnOne(1);
}
function runFlag(): void {
    $notStopped = stopOnFlag('message', false);
    $wildcard = stopOnFlag('message', true);
}
"#;
    let (parsed, analyzed, _) = analyze_source(
        "/no-return-attribute.php",
        source,
        false,
        vec![Arc::new(AttributeContracts)],
    );
    assert_text_type(&analyzed, &parsed.root, "stopOnOne(1)", "never");
    assert_text_type(&analyzed, &parsed.root, "stopOnOne(2)", "void");
    assert_text_type(
        &analyzed,
        &parsed.root,
        "stopOnFlag('message', true)",
        "never",
    );
    assert_text_type(
        &analyzed,
        &parsed.root,
        "stopOnFlag('message', false)",
        "void",
    );
}

#[test]
fn php_storm_meta_method_contract_applies_to_subtype() {
    let meta = meta_document(
        r#"<?php
namespace PHPSTORM_META;
override(\BaseContainer::get(0), type(0));
"#,
    );
    let parsed = parse(
        r#"<?php
class Product {}
class BaseContainer { public function get(mixed $value): mixed {} }
class Container extends BaseContainer {}
function run(Container $container): void {
    $product = $container->get(new Product());
}
"#,
    );
    let bound = bind("/method-contract.php", &parsed);
    let snapshot = snapshot(&[meta, bound.clone()]);
    let analyzed = analyze_linked(&snapshot, call_contracts(), &bound, &parsed);
    assert_text_type(
        &analyzed,
        &parsed.root,
        "$container->get(new Product())",
        "Product",
    );
}

#[test]
fn php_storm_meta_map_and_exit_point_contracts() {
    let meta = meta_document(
        r#"<?php
namespace PHPSTORM_META;
override(\make(0), map(['product' => \Product::class, '' => '@']));
override(\mock(0), map(['' => '$0']));
exitPoint(\dd());
exitPoint(\trigger_error(ANY_ARGUMENT, \E_USER_ERROR));
"#,
    );
    let parsed = parse(
        r#"<?php
class Product {}
function run(): void {
    $mapped = make('product');
    $fallback = make('GeneratedProduct');
    $mock = mock(Product::class);
}
function warning(): void {
    trigger_error('warning', \E_USER_WARNING);
}
function failure(): void {
    trigger_error('failure', \E_USER_ERROR);
}
function dump(): void {
    dd();
}
"#,
    );
    let bound = bind("/map-contract.php", &parsed);
    let snapshot = snapshot(&[meta.clone(), bound.clone()]);
    assert_eq!(meta.call_contracts.len(), 4);
    let mut seen_map_contract = false;
    snapshot.visit_function_call_contracts("make", |contract| {
        seen_map_contract = contract.return_contract.kind == CallReturnKind::ArgumentMap;
        true
    });
    assert!(seen_map_contract);
    assert_eq!(
        meta.call_contracts[0].return_contract.map[0].result.kind,
        CallValueKind::ClassConstant,
        "{:?}",
        meta.call_contracts[0].return_contract.map[0].result
    );
    let arguments = [CallArgument::of(Type::literal_string("product"))];
    let value = evaluate_call_return_contract(
        &CallContext {
            snapshot: Some(&snapshot),
            arguments: &arguments,
            ..CallContext::default()
        },
        &meta.call_contracts[0].return_contract,
    )
    .expect("contract matched");
    assert_eq!(value.to_string(), "Product");
    let analyzed = analyze_linked(&snapshot, call_contracts(), &bound, &parsed);

    assert_text_type(&analyzed, &parsed.root, "make('product')", "Product");
    assert_text_type(
        &analyzed,
        &parsed.root,
        "make('GeneratedProduct')",
        "GeneratedProduct",
    );
    assert_text_type(&analyzed, &parsed.root, "mock(Product::class)", "Product");
    assert_text_type(
        &analyzed,
        &parsed.root,
        "trigger_error('failure', \\E_USER_ERROR)",
        "never",
    );
    for call in query::calls(&parsed.root, &[]) {
        if call.text().to_string().trim() != "trigger_error('warning', \\E_USER_WARNING)" {
            continue;
        }
        assert_ne!(analyzed.type_of(&call).ty.kind(), TypeKind::Never);
    }
    assert_text_type(&analyzed, &parsed.root, "dd()", "never");
}

#[test]
fn php_storm_meta_framework_return_contracts() {
    let meta = meta_document(
        r#"<?php
namespace PHPSTORM_META;
override(\Psr\Container\ContainerInterface::get(0), map([
    'product.repository' => \App\Repository\ProductRepository::class,
]));
override(\Doctrine\Persistence\ObjectManager::getRepository(0), map([
    \App\Entity\Product::class => \App\Repository\ProductRepository::class,
]));
override(\PHPUnit\Framework\TestCase::createMock(0), map(['' => '$0']));
"#,
    );
    let parsed = parse(
        r#"<?php
namespace Psr\Container {
    interface ContainerInterface { public function get(string $id): object; }
}
namespace Doctrine\Persistence {
    interface ObjectManager { public function getRepository(string $class): object; }
}
namespace PHPUnit\Framework {
    abstract class TestCase { protected function createMock(string $class): object {} }
}
namespace App\Entity { final class Product {} }
namespace App\Repository { final class ProductRepository {} }
namespace App\Infrastructure {
    final class Container implements \Psr\Container\ContainerInterface {
        public function get(string $id): object {}
    }
    final class EntityManager implements \Doctrine\Persistence\ObjectManager {
        public function getRepository(string $class): object {}
    }
}
namespace App\Tests {
    use App\Entity\Product;
    use App\Infrastructure\Container;
    use App\Infrastructure\EntityManager;
    final class ProductTest extends \PHPUnit\Framework\TestCase {
        public function run(Container $container, EntityManager $manager): void {
            $service = $container->get('product.repository');
            $repository = $manager->getRepository(Product::class);
            $mock = $this->createMock(Product::class);
        }
    }
}
"#,
    );
    let bound = bind("/framework-contracts.php", &parsed);
    let snapshot = snapshot(&[meta, bound.clone()]);
    let analyzed = analyze_linked(&snapshot, call_contracts(), &bound, &parsed);
    for expression in [
        "$container->get('product.repository')",
        "$manager->getRepository(Product::class)",
    ] {
        assert_text_type(
            &analyzed,
            &parsed.root,
            expression,
            "App\\Repository\\ProductRepository",
        );
    }
    assert_text_type(
        &analyzed,
        &parsed.root,
        "$this->createMock(Product::class)",
        "App\\Entity\\Product",
    );
}

#[test]
fn no_matching_signature_explains_argument_mismatch() {
    let source = r#"<?php
namespace App;
function decode(string $json, bool $associative): void {}
function run(float $price): void {
    \App\decode($price, true);
}
"#;
    let (_, analyzed) = analyze_plain("/decode.php", source);
    let messages = super::messages(&analyzed, "php.arguments");
    assert_eq!(
        messages,
        vec![
            "No matching signature for App\\decode(): parameter #1 ($json) expects string, float given"
                .to_owned()
        ],
        "{:?}",
        all_messages(&analyzed)
    );
}

#[test]
fn no_matching_signature_explains_arity_mismatch() {
    let source = r#"<?php
function consume(string $value): void {}
function run(): void {
    consume();
    consume('a', value: 'b');
}
"#;
    let (_, analyzed) = analyze_plain("/arity.php", source);
    let mut messages = super::messages(&analyzed, "php.arguments");
    messages.sort();
    let mut expected = vec![
        "No matching signature for consume(): missing argument for parameter #1 ($value)"
            .to_owned(),
        "No matching signature for consume(): parameter #1 ($value) is passed more than once"
            .to_owned(),
    ];
    expected.sort();
    assert_eq!(messages, expected);
}

#[test]
fn declared_polyfill_return_types_outweigh_inferred_ones() {
    // Polyfills declare one function several times behind `function_exists`;
    // only some declare their return type.
    let polyfills = r#"<?php
if (!function_exists('poly_trim')) {
    function poly_trim(?string $value) { return $value === '' ? false : (string) $value; }
}
if (!function_exists('poly_trim')) {
    function poly_trim(?string $value): string { return (string) $value; }
}
"#;
    let parsed_polyfills = parse(polyfills);
    let bound_polyfills = bind("/polyfills.php", &parsed_polyfills);
    let base = snapshot(std::slice::from_ref(&bound_polyfills));
    let analyzed_polyfills = analyze_linked(&base, builtins(), &bound_polyfills, &parsed_polyfills);

    let caller = r#"<?php
function take(?string $value): void {}
$trimmed = poly_trim('x');
take($trimmed);
"#;
    let parsed_caller = parse(caller);
    let bound_caller = bind("/caller.php", &parsed_caller);
    let workspace = snapshot(&[analyzed_polyfills, bound_caller.clone()]);
    let analyzed = analyze_linked(&workspace, builtins(), &bound_caller, &parsed_caller);
    assert_text_type(&analyzed, &parsed_caller.root, "$trimmed", "string");
    assert!(
        all_messages(&analyzed).is_empty(),
        "{:?}",
        all_messages(&analyzed)
    );
}

/// Analyzes `source` with the PHP 8.3 runtime stubs and the builtin
/// extensions, returning its issue messages.
fn issues_with_builtins(path: &str, source: &str) -> (Parsed, Document) {
    let (parsed, document, _) = analyze_source(path, source, true, builtins());
    (parsed, document)
}

#[test]
fn for_loop_conditions_narrow_the_body() {
    let source = r#"<?php
class Node { public function parent(): ?Node { return null; } }
function visit(Node $node): void {}
function walk(Node $start): void {
    for ($current = $start->parent(); $current !== null; $current = $current->parent()) {
        visit($current);
    }
}
"#;
    let (_, analyzed) = issues_with_builtins("/for.php", source);
    assert!(
        all_messages(&analyzed).is_empty(),
        "{:?}",
        all_messages(&analyzed)
    );
}

#[test]
fn strict_in_array_and_array_key_exists_narrow_their_needle() {
    let source = r#"<?php
function take(string $value): void {}
/** @return array{tools: int, prompts: int} */
function versions(?string $mode, string $list, int $version): array {
    if (\in_array($mode, ['a', 'b'], true)) {
        take($mode);
    }
    $versions = ['tools' => 0, 'prompts' => 0];
    if (\array_key_exists($list, $versions)) {
        $versions[$list] = $version;
    }
    return $versions;
}
"#;
    let (_, analyzed) = issues_with_builtins("/narrow.php", source);
    assert!(
        all_messages(&analyzed).is_empty(),
        "{:?}",
        all_messages(&analyzed)
    );
}

#[test]
fn assigning_one_of_several_shape_keys_keeps_the_shape() {
    let source = r#"<?php
/**
 * @param array{m: bool, x: bool} $flags
 * @return array{m: bool, x: bool}
 */
function inline_flags(array $flags, string $on): array {
    foreach (['m', 'x'] as $flag) {
        if (str_contains($on, $flag)) {
            $flags[$flag] = true;
        }
    }
    return $flags;
}
"#;
    let (_, analyzed) = issues_with_builtins("/shape.php", source);
    assert!(
        all_messages(&analyzed).is_empty(),
        "{:?}",
        all_messages(&analyzed)
    );
}

#[test]
fn parse_url_and_hrtime_results_depend_on_their_arguments() {
    let source = r#"<?php
function port(int $port): void {}
function timer(int|float $started): void {}
/** @param array<string, mixed> $parts */
function build(array $parts): string { return ''; }
function run(string $url): void {
    $parts = parse_url($url);
    if ($parts === false) {
        return;
    }
    port($parts['port'] ?? 80);
    build($parts);
    timer(hrtime(true));
    $pair = hrtime();
}
"#;
    let (parsed, analyzed) = issues_with_builtins("/builtins.php", source);
    assert!(
        all_messages(&analyzed).is_empty(),
        "{:?}",
        all_messages(&analyzed)
    );
    assert_text_type(&analyzed, &parsed.root, "$pair", "array{0:int,1:int}");
}

#[test]
fn declared_callback_parameters_win_over_the_iterated_array() {
    let source = r#"<?php
function permits(string $name): bool { return true; }
/** @param array<string, array{privileges: list<string>}> $events */
function filter(array $events): array {
    return array_filter($events, fn (string $name) => permits($name), \ARRAY_FILTER_USE_KEY);
}
"#;
    let (_, analyzed) = issues_with_builtins("/callback.php", source);
    assert!(
        all_messages(&analyzed).is_empty(),
        "{:?}",
        all_messages(&analyzed)
    );
}

#[test]
fn a_ternary_with_a_null_branch_ties_the_result_to_its_condition() {
    let source = r#"<?php
class Method {}
function source_of(Method $method): string { return ''; }
function created(?Method $setUp, bool $expects): ?string {
    $assign = $setUp !== null && !$expects ? 'assign' : null;
    if ($assign !== null) {
        return source_of($setUp);
    }
    $reverse = $setUp === null ? null : 'assign';
    if (null !== $reverse) {
        return source_of($setUp);
    }
    $assign = 'reassigned';
    if ($assign !== null) {
        return source_of($setUp);
    }
    return null;
}
"#;
    let (_, analyzed) = issues_with_builtins("/alias.php", source);
    let messages = all_messages(&analyzed);
    // Only the call after the reassignment lost the tie to the condition.
    assert_eq!(messages.len(), 1, "{messages:?}");
}

#[test]
fn a_collection_is_an_iterable_of_what_it_binds_to_traversable() {
    let source = r#"<?php
class Entity {}
class AppEntity extends Entity {}
class Other {}
/**
 * @template TElement
 * @template TKey of array-key = array-key
 * @implements \IteratorAggregate<TKey, TElement>
 */
abstract class Collection implements \IteratorAggregate {
    public function getIterator(): \Traversable { yield from []; }
}
/**
 * @template TElement of Entity
 * @extends Collection<TElement, string>
 */
class EntityCollection extends Collection {}
/** @extends EntityCollection<AppEntity> */
class AppCollection extends EntityCollection {}

/** @return iterable<AppEntity> */
function apps(AppCollection $apps): iterable { return $apps; }
/** @return iterable<string, Entity> */
function entities(AppCollection $apps): iterable { return $apps; }
/** @return iterable<Other> */
function others(AppCollection $apps): iterable { return $apps; }
/** @return iterable<int, AppEntity> */
function byPosition(AppCollection $apps): iterable { return $apps; }
"#;
    let (_, analyzed, _) = analyze_source("/collection-iterable.php", source, true, builtins());
    let messages: Vec<&str> = analyzed
        .issues
        .iter()
        .filter(|issue| &*issue.code == "php.returnType")
        .map(|issue| &*issue.message)
        .collect();
    // Only the wrong value type and the wrong key type are reported.
    assert_eq!(
        messages,
        [
            "Expected return type iterable<array-key,Other>, got AppCollection",
            "Expected return type iterable<int,AppEntity>, got AppCollection",
        ]
    );
}

#[test]
fn clone_call_form_has_the_type_of_its_first_argument() {
    let source = r#"<?php
class Listing {
    public function withText(string $text): self {
        return clone($this, ['text' => $text]);
    }
    public function copy(): static {
        return clone $this;
    }
}"#;
    let (parsed, analyzed, _) = analyze_source("/listing.php", source, false, Vec::new());
    assert_text_type(
        &analyzed,
        &parsed.root,
        "clone($this, ['text' => $text])",
        "Listing",
    );
    assert_text_type(&analyzed, &parsed.root, "clone $this", "Listing");
    assert_no_issue(&analyzed, "php.returnType");
}
