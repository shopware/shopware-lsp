//! Port of `inference_test.go` (lines 1-1152).

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_parser_php::{query, syntax::PHP_VARIABLE};
use swlsp_php_semantic::{Document, NameKind, Symbol, SymbolKind};
use swlsp_php_types::{CallableParameter, ShapeField, Type};

use super::{analyze_source, assert_node_type, assert_text_type, builtins, parse};
use crate::Extension;
use crate::expr::{SHAPE_FIELD_LINEAR_LIMIT, is_implicit_list_array, upsert_shape_field};
use crate::types_util::{callable_result, resolve_special_type};

fn extensions(with_builtins: bool) -> Vec<Arc<dyn Extension>> {
    if with_builtins {
        builtins()
    } else {
        Vec::new()
    }
}

#[track_caller]
fn assert_no_issues(document: &Document, codes: &[&str]) {
    for issue in &document.issues {
        for code in codes {
            assert_ne!(&*issue.code, *code, "{}", issue.message);
        }
    }
}

#[track_caller]
fn check_no_issues(path: &str, source: &str, stubs: bool, with_builtins: bool, codes: &[&str]) {
    let (_, analyzed, _) = analyze_source(path, source, stubs, extensions(with_builtins));
    assert_no_issues(&analyzed, codes);
}

#[test]
fn resolve_ordinary_composite_type_does_not_allocate() {
    let value = Type::array(
        Type::string(),
        Type::list(Type::named("Shopware\\Core\\Content\\Product", vec![])),
    );
    let receiver = Type::named("Shopware\\Core\\Content\\Product", vec![]);
    let resolved = resolve_special_type(&value, &receiver, &receiver);
    assert_eq!(resolved, value);
}

#[test]
fn resolve_special_type_rebuilds_context_dependent_children() {
    let stable = Type::named("Stable", vec![]);
    let current = Type::named("Current", vec![]);
    let receiver = Type::named("Receiver", vec![]);
    let tests = vec![
        (
            Type::array(stable.clone(), Type::list(Type::self_type(vec![]))),
            Type::array(stable.clone(), Type::list(current.clone())),
        ),
        (
            Type::callable(
                vec![CallableParameter::of(stable.clone())],
                Type::static_type(vec![]),
            ),
            Type::callable(
                vec![CallableParameter::of(stable.clone())],
                receiver.clone(),
            ),
        ),
        (
            Type::array_shape(
                vec![
                    ShapeField::new("stable", stable.clone()),
                    ShapeField::new("current", Type::self_type(vec![])),
                ],
                false,
            ),
            Type::array_shape(
                vec![
                    ShapeField::new("stable", stable.clone()),
                    ShapeField::new("current", current.clone()),
                ],
                false,
            ),
        ),
    ];
    for (value, expected) in tests {
        let actual = resolve_special_type(&value, &receiver, &current);
        assert_eq!(expected, actual);
    }
}

#[test]
fn runtime_exception_provides_concrete_throwable_methods() {
    let source = r#"<?php
class ApplicationException extends RuntimeException {}
"#;
    let (_, analyzed, _) = analyze_source("/exception.php", source, true, Vec::new());
    assert_no_issues(&analyzed, &["php.abstract"]);
}

#[test]
fn shape_field_index_stays_lazy_and_preserves_last_duplicate() {
    let mut fields: Vec<ShapeField> = Vec::new();
    let mut indices: Option<HashMap<String, usize>> = None;
    for index in 0..SHAPE_FIELD_LINEAR_LIMIT {
        upsert_shape_field(
            &mut fields,
            &mut indices,
            ShapeField::new(&index.to_string(), Type::int()),
        );
    }
    assert!(indices.is_none());

    upsert_shape_field(
        &mut fields,
        &mut indices,
        ShapeField::new("3", Type::string()),
    );
    assert!(indices.is_none());
    assert_eq!(fields.len(), SHAPE_FIELD_LINEAR_LIMIT);
    assert_eq!(fields[3].ty, Type::string());

    upsert_shape_field(
        &mut fields,
        &mut indices,
        ShapeField::new("16", Type::bool()),
    );
    assert!(indices.is_some());
    assert_eq!(fields.len(), SHAPE_FIELD_LINEAR_LIMIT + 1);

    upsert_shape_field(
        &mut fields,
        &mut indices,
        ShapeField::new("16", Type::float()),
    );
    assert_eq!(indices.as_ref().unwrap().get("16"), Some(&16));
    assert_eq!(fields.len(), SHAPE_FIELD_LINEAR_LIMIT + 1);
    assert_eq!(fields[16].ty, Type::float());
}

#[test]
fn implicit_list_array_classification() {
    let parsed = parse(
        r#"<?php
[];
[1, 2];
[1, 'key' => 2];
[...$values];
"#,
    );
    let arrays = query::arrays(&parsed.root);
    assert_eq!(arrays.len(), 4);
    assert!(is_implicit_list_array(&arrays[0]));
    assert!(is_implicit_list_array(&arrays[1]));
    assert!(!is_implicit_list_array(&arrays[2]));
    assert!(!is_implicit_list_array(&arrays[3]));
}

#[test]
fn small_implicit_array_literal_preserves_tuple_positions() {
    let source = r#"<?php
/** @return array{0: string, 1: bool, 2: bool} */
function tuple(): array {
    return ['value', false, true];
}
$value = tuple()[0];
"#;
    let (parsed, analyzed, _) = analyze_source("/tuple.php", source, false, Vec::new());
    assert_text_type(
        &analyzed,
        &parsed.root,
        "['value', false, true]",
        r#"array{0:"value",1:false,2:true}"#,
    );
    assert_text_type(&analyzed, &parsed.root, "tuple()[0]", "string");
    assert_no_issues(&analyzed, &["php.returnType"]);
}

#[test]
fn literal_key_assignments_preserve_array_shape() {
    let source = r#"<?php
/**
 * @return array{id: string, count: int, optional?: bool}
 */
function payload(): array {
    $result = [];
    $result['id'] = 'product';
    $result['count'] = 1;
    return $result;
}
"#;
    let (parsed, analyzed, _) = analyze_source("/assigned-shape.php", source, false, Vec::new());
    assert_node_type(
        &analyzed,
        &parsed.root,
        PHP_VARIABLE,
        r#"array{count:1,id:"product"}"#,
    );
    assert_no_issues(&analyzed, &["php.returnType"]);
}

#[test]
fn nullable_callable_retains_invocation_result() {
    let result = callable_result(&Type::nullable(Type::callable(Vec::new(), Type::string())));
    assert_eq!(result, Some(Type::string()));
}

#[test]
fn by_reference_call_declares_output_variable() {
    let source = r#"<?php
function fill(string $input, array &$matches): bool {
    return true;
}
function run(string $input): mixed {
    fill($input, $matches);

    return $matches[0];
}
function runCast(string $input): mixed {
    $count = (int) fill($input, $castMatches);

    return $castMatches[0];
}
"#;
    let (_, analyzed, _) = analyze_source("/by-reference.php", source, false, Vec::new());
    let mut outputs: HashMap<String, Symbol> = HashMap::new();
    for symbol in &analyzed.symbols {
        if symbol.kind == SymbolKind::Local
            && (&*symbol.name == "$matches" || &*symbol.name == "$castMatches")
        {
            outputs.insert(symbol.name.to_string(), symbol.clone());
        }
    }
    assert!(outputs.contains_key("$matches"));
    assert!(outputs.contains_key("$castMatches"));
    assert_eq!(outputs["$matches"].ty.to_string(), "array");
    assert_eq!(outputs["$castMatches"].ty.to_string(), "array");
    for reference in &analyzed.references {
        if reference.kind != NameKind::Variable {
            continue;
        }
        if let Some(output) = outputs.get(&*reference.name) {
            assert_eq!(output.id, reference.resolved);
        }
    }
}

#[test]
fn ternary_narrows_repeated_predicate_expression() {
    check_no_issues(
        "/ternary-predicate.php",
        r#"<?php
class Response {
    public function getContent(): string|false {}
}
function content(Response $response): string {
    $value = is_string($response->getContent())
        ? $response->getContent()
        : '';

    return $value;
}
"#,
        false,
        true,
        &["php.returnType"],
    );
}

#[test]
fn unset_after_null_check_removes_array_shape_field() {
    check_no_issues(
        "/unset-array-field.php",
        r#"<?php
class Required {}
class CascadeDelete {}
/** @return array<string, array{class: string}> */
function flags(bool $keep): array {
	$flags = [];
	$flags[Required::class] = ['class' => Required::class];
	$flags['cascade'] = $keep ? ['class' => CascadeDelete::class] : null;
    if ($flags['cascade'] === null) {
        unset($flags['cascade']);
    }

    return $flags;
}
"#,
        false,
        false,
        &["php.returnType"],
    );
}

#[test]
fn nested_array_writes_and_unset_update_element_shape() {
    check_no_issues(
        "/nested-array-write.php",
        r#"<?php
/**
 * @param array<string, array{id: string, translations: string}> $options
 * @return array<string, array{id: string, group: array{id: string}}>
 */
function enrich(array $options): array {
    foreach ($options as $optionId => $option) {
        $options[$optionId]['group'] = ['id' => $options[$optionId]['id']];
        unset($options[$optionId]['translations']);
    }

    return $options;
}
"#,
        false,
        false,
        &["php.returnType"],
    );
}

#[test]
fn nested_array_append_keeps_outer_key_type() {
    check_no_issues(
        "/nested-array-append.php",
        r#"<?php
class EntityDefinition {}
/**
 * @param class-string<EntityDefinition> $definition
 * @return array<class-string<EntityDefinition>, list<string>>
 */
function collect(string $definition): array {
    $result = [];
    $result[$definition][] = 'error';

    return $result;
}
"#,
        false,
        false,
        &["php.returnType"],
    );
}

#[test]
fn terminating_falsy_guard_removes_nullable_return() {
    check_no_issues(
        "/truthiness.php",
        r#"<?php
function requireValue(?string $value): string {
    if (!$value) {
        throw new RuntimeException();
    }

    return $value;
}
"#,
        false,
        false,
        &["php.returnType"],
    );
}

#[test]
fn negative_is_numeric_branch_excludes_float() {
    check_no_issues(
        "/numeric-guard.php",
        r#"<?php
function convert(float|string|null $value): ?string {
    if (!is_numeric($value)) {
        return $value === null ? null : $value;
    }

    return (string) $value;
}
"#,
        false,
        true,
        &["php.returnType"],
    );
}

#[test]
fn class_constant_equality_narrows_nullable_argument() {
    check_no_issues(
        "/constant-equality.php",
        r#"<?php
class ShippingMethod {
    public const TAX_TYPE_FIXED = 'fixed';
}
function consume(string $taxType): void {}
function validate(?string $taxType, ?string $taxId): void {
    if ($taxType === ShippingMethod::TAX_TYPE_FIXED && $taxId === null) {
        consume($taxType);
    }
}
"#,
        false,
        false,
        &["php.arguments"],
    );
}

#[test]
fn terminating_falsy_guard_preserves_concrete_generic_object() {
    check_no_issues(
        "/generic-truthiness.php",
        r#"<?php
class Product {}
/**
 * @template T
 */
class Collection {
    /** @return T|null */
    public function first() {}
}
/** @extends Collection<Product> */
class Products extends Collection {}
function consume(Product $product): void {}
function run(Products $products): void {
    $product = $products->first();
    if (!$product) {
        return;
    }
    consume($product);
}
"#,
        false,
        false,
        &["php.arguments"],
    );
}

#[test]
fn inline_var_annotations_refine_assignments_and_foreach_values() {
    check_no_issues(
        "/inline-var.php",
        r#"<?php
/** @param array<string, string> $row */
function consumeRow(array $row): void {}
function consumeString(string $value): void {}
function annotatedValue(): string {
    /** @var string $annotated */
    // @phpstan-ignore varTag.type
    $annotated = 42;

    return $annotated;
}

/** @param list<array<string, mixed>> $rows */
function run(array $rows): void {
    /** @var array<string, string> $row */
    foreach ($rows as $row) {
        consumeRow($row);
    }

    /** @var string $value */
    $value = $rows[0]['value'];
    consumeString($value);
}
"#,
        false,
        false,
        &["php.arguments", "php.returnType"],
    );
}

#[test]
fn private_readonly_property_uses_constructor_assignment_type() {
    check_no_issues(
        "/readonly-constructor-type.php",
        r#"<?php
final class Scopes {
    /** @var array<string> */
    private readonly array $values;

    /** @return list<string> */
    public function values(): array {
        return $this->values;
    }

    /** @param list<string> $values */
    public function __construct(array $values) {
        $this->values = $values;
    }
}
"#,
        false,
        false,
        &["php.returnType"],
    );
}

#[test]
fn symfony_finder_real_path_is_benevolently_string() {
    check_no_issues(
        "/finder-real-path.php",
        r#"<?php
namespace Symfony\Component\Finder {
    class SplFileInfo {
        public function getRealPath(): string|false {}
    }
}

namespace App {
    /** @return list<string> */
    function paths(iterable $files): array {
        $paths = [];
        foreach ($files as $file) {
            if ($file instanceof \Symfony\Component\Finder\SplFileInfo) {
                $paths[] = $file->getRealPath();
            }
        }
        return $paths;
    }
}
"#,
        false,
        true,
        &["php.returnType"],
    );
}

#[test]
fn internal_false_unions_are_benevolent_at_use_sites() {
    check_no_issues(
        "/benevolent-internal-unions.php",
        r#"<?php
function path(): string {
    return tempnam('/tmp', 'prefix');
}
function expiration(DateTimeImmutable $now, string $modifier): DateTimeInterface {
    return $now->modify($modifier);
}
"#,
        true,
        true,
        &["php.returnType"],
    );
}

#[test]
fn isset_initialization_drops_impossible_empty_array_branch() {
    check_no_issues(
        "/isset-array-initialization.php",
        r#"<?php
class SalesChannel {}
/**
 * @param list<string> $foreignKeys
 * @param non-empty-list<array{foreignKey: string}> $urls
 */
function consume(array $foreignKeys, array $urls, SalesChannel $channel): void {}
/** @param iterable<string, SalesChannel> $channels */
function build(iterable $channels): void {
    $configs = [];
    foreach ($channels as $id => $channel) {
        if (!isset($configs[$id])) {
            $configs[$id] = ['urls' => [], 'channel' => $channel];
        }
        $configs[$id]['urls'][] = ['foreignKey' => $id];
    }
    foreach ($configs as $config) {
        consume(array_column($config['urls'], 'foreignKey'), $config['urls'], $config['channel']);
    }
}
"#,
        false,
        true,
        &["php.arguments"],
    );
}

#[test]
fn nested_isset_initialization_does_not_create_partial_shape() {
    check_no_issues(
        "/nested-isset-array-initialization.php",
        r#"<?php
/**
 * @param iterable<array{package: string, version: string, normalized: string|null, source: string}> $rows
 * @return array<string, array<string, array{pretty: string, normalized: string|null, sources: list<string>}>>
 */
function collect(iterable $rows): array {
    $packages = [];
    foreach ($rows as $row) {
        $package = $row['package'];
        $version = $row['version'];
        if (!isset($packages[$package][$version])) {
            $packages[$package][$version] = [
                'pretty' => $version,
                'normalized' => $row['normalized'],
                'sources' => [],
            ];
        }
        if (!in_array($row['source'], $packages[$package][$version]['sources'], true)) {
            $packages[$package][$version]['sources'][] = $row['source'];
        }
    }
    return $packages;
}
"#,
        false,
        true,
        &["php.returnType"],
    );
}

#[test]
fn conditional_assertion_narrows_opposite_branch() {
    check_no_issues(
        "/conditional-opposite-assertion.php",
        r#"<?php
class Result {
    public ?Throwable $exception = null;
    /** @phpstan-assert-if-true null $this->exception */
    public function successful(): bool {}
}
function consumeThrowable(Throwable $exception): void {}
function handle(Result $result): void {
    if ($result->successful()) {
        return;
    }
    consumeThrowable($result->exception);
}
"#,
        false,
        false,
        &["php.arguments"],
    );
}

#[test]
fn array_search_preserves_haystack_key_type() {
    check_no_issues(
        "/array-search.php",
        r#"<?php
/** @param array<string, string> $values */
function findKey(array $values): string {
    $updates = [];
    foreach (['needle'] as $needle) {
        if ($key = array_search($needle, $values, true)) {
            $updates[$key] = true;
            continue;
        }
    }
    foreach ($updates as $key => $_) {
        return $key;
    }
    return '';
}
"#,
        false,
        true,
        &["php.returnType"],
    );
}

#[test]
fn inherited_method_self_parameter_uses_declaring_class() {
    check_no_issues(
        "/inherited-self.php",
        r#"<?php
abstract class Aggregation {
    public function add(self $aggregation): self {
        return $this;
    }
}
class NestedAggregation extends Aggregation {}
function compose(
    NestedAggregation $nested,
    Aggregation $aggregation
): Aggregation {
    return $nested->add($aggregation);
}
"#,
        false,
        false,
        &["php.arguments", "php.returnType"],
    );
}

#[test]
fn dynamic_plain_string_construction_remains_uncertain() {
    check_no_issues(
        "/dynamic-class.php",
        r#"<?php
class Event {}
function consume(Event $event): void {}
function create(string $class): void {
    consume(new $class());
}
"#,
        false,
        false,
        &["php.arguments"],
    );
}

#[test]
fn invokable_object_is_assignable_to_callable() {
    let source = r#"<?php
class Middleware {
    public function __invoke(string $request): int {
        return 1;
    }
}
function middleware(): callable {
    return new Middleware();
}
"#;
    let (_, analyzed, snapshot) = analyze_source("/invokable.php", source, false, Vec::new());
    assert!(snapshot.relations().is_subtype(
        &Type::named("Middleware", vec![]),
        &Type::callable(Vec::new(), Type::mixed())
    ));
    assert_no_issues(&analyzed, &["php.returnType"]);
}

#[test]
fn assert_predicate_narrows_nullable_return() {
    check_no_issues(
        "/assert-predicate.php",
        r#"<?php
function requireString(?string $value): string {
    \assert(\is_string($value));
    return $value;
}
function requireNamedString(?string $value): string {
    \assert(assertion: \is_string($value));
    return $value;
}
"#,
        false,
        true,
        &["php.returnType"],
    );
}

#[test]
fn assert_is_resource_narrows_fopen_result() {
    check_no_issues(
        "/assert-resource.php",
        r#"<?php
/** @param resource $stream */
function acceptsStream($stream): void {}
function write(): void {
    $stream = fopen('php://temp', 'rwb');
    assert(is_resource($stream));
    acceptsStream($stream);
}
"#,
        true,
        true,
        &["php.arguments"],
    );
}

#[test]
fn class_existence_and_is_a_narrow_string_to_bound_class_string() {
    check_no_issues(
        "/class-string-flow.php",
        r#"<?php
class Kernel {}
/**
 * @return class-string<Kernel>
 */
function validatedClass(string $class): string {
    if (!class_exists($class)) {
        throw new RuntimeException();
    }
    if (!is_a($class, Kernel::class, true)) {
        throw new RuntimeException();
    }

    return $class;
}
/** @return class-string<Kernel> */
function validatedSubclass(string $class): string {
    if (!class_exists($class)) {
        throw new RuntimeException();
    }
    if (!is_subclass_of($class, Kernel::class)) {
        throw new RuntimeException();
    }
    return $class;
}
"#,
        true,
        true,
        &["php.returnType"],
    );
}

#[test]
fn terminating_repeated_getter_guard_narrows_return() {
    check_no_issues(
        "/getter-truthiness.php",
        r#"<?php
class Customer {
    public function getId(): ?string {}
}
function requireId(?Customer $customer): string {
    if (!$customer?->getId()) {
        throw new RuntimeException();
    }

    return $customer->getId();
}
"#,
        false,
        false,
        &["php.returnType"],
    );
}

#[test]
fn truthy_nullsafe_call_narrows_receiver() {
    check_no_issues(
        "/nullsafe-receiver.php",
        r#"<?php
class Item {
    public function enabled(): bool {}
}
function consume(Item $item): void {}
function maybeConsume(?Item $item): void {
    if ($item?->enabled()) {
        consume($item);
    }
}
"#,
        false,
        false,
        &["php.arguments"],
    );
}

#[test]
fn property_assignment_updates_branch_flow() {
    check_no_issues(
        "/property-assignment.php",
        r#"<?php
class Recipient {}
class Event {
    private ?Recipient $recipient = null;

    public function recipient(): Recipient {
        if (!$this->recipient instanceof Recipient) {
            $this->recipient = new Recipient();
        }

        return $this->recipient;
    }
}
"#,
        false,
        false,
        &["php.returnType"],
    );
}
