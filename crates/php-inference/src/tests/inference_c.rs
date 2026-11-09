use std::sync::Arc;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_project::Version;
use swlsp_php_resolver::{Argument, MemberResolver, resolve_signature};
use swlsp_php_semantic::{
    Document, Flags, Parameter, Symbol, SymbolKind, TypeSource, new_symbol_id, node_identity,
};
use swlsp_php_types::{Kind, Relations, Type};

use super::{
    analyze_linked, analyze_source, analyzer, assert_node_type, assert_text_type, bind, builtins,
    parse, snapshot, symbol_named,
};
use crate::Extension;
use crate::env::{Env, join_environments};
use crate::types_util::{
    MAX_SPECIAL_TYPE_DEPTH, MAX_SPECIAL_TYPE_NODES, exceeds_special_type_nodes,
    resolve_special_type,
};

#[track_caller]
fn assert_no_issue(document: &Document, codes: &[&str]) {
    for issue in &document.issues {
        assert!(
            !codes.contains(&&*issue.code),
            "unexpected {}: {}",
            issue.code,
            issue.message
        );
    }
}

fn stubs85() -> Document {
    swlsp_php_stubs::document(Version::new(8, 5, 0))
}

/// Bind `source`, snapshot it with `extra` documents, link and analyze.
fn analyze_with(
    path: &str,
    source: &str,
    extra: Vec<Document>,
    extensions: Vec<Arc<dyn Extension>>,
) -> (super::Parsed, Document) {
    let parsed = parse(source);
    let bound = bind(path, &parsed);
    let mut documents = vec![bound.clone()];
    documents.extend(extra);
    let snapshot = snapshot(&documents);
    let analyzed = analyze_linked(&snapshot, extensions, &bound, &parsed);
    (parsed, analyzed)
}

#[test]
fn class_template_refines_native_method_return() {
    let (parsed, analyzed, _) = analyze_source(
        "/generic-method-return.php",
        r#"<?php
class EntityCollection {}
class ProductCollection extends EntityCollection {}
/**
 * @template TCollection of EntityCollection
 */
class SearchResult {
    /** @return TCollection */
    public function getEntities(): EntityCollection {}
}
/** @return SearchResult<ProductCollection> */
function search(): SearchResult {}
function products(): ProductCollection {
    return search()->getEntities();
}
"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
    assert_text_type(
        &analyzed,
        &parsed.root,
        "search()->getEntities()",
        "ProductCollection",
    );
}

#[test]
fn dynamic_class_string_object_creation_returns_object_type() {
    let (parsed, analyzed, _) = analyze_source(
        "/dynamic-object.php",
        r#"<?php
class Product {}
/** @return class-string<Product> */
function productClass(): string {}
function createProduct(): Product {
    $class = productClass();
    return new $class();
}
"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
    assert_text_type(&analyzed, &parsed.root, "new $class()", "Product");
}

#[test]
fn first_class_callable_uses_declared_signature() {
    let (parsed, analyzed, _) = analyze_source(
        "/first-class-callable.php",
        r#"<?php
function predicate(mixed $value): bool {
    return true;
}
function consume(callable $callback): void {}
function run(): void {
    consume(predicate(...));
}
"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
    assert_text_type(
        &analyzed,
        &parsed.root,
        "predicate(...)",
        "callable(mixed $value):bool",
    );
}

#[test]
fn first_class_callable_detection_does_not_allocate() {
    let parsed = parse(
        r#"<?php
predicate(...);
predicate($value);
predicate(...$values);
"#,
    );
    let calls = query::calls(&parsed.root, &[]);
    assert_eq!(calls.len(), 3);
    assert!(crate::expr::is_first_class_callable(&calls[0]));
    assert!(!crate::expr::is_first_class_callable(&calls[1]));
    assert!(!crate::expr::is_first_class_callable(&calls[2]));
}

#[test]
fn array_filter_first_class_predicate_narrows_elements() {
    let (_, analyzed, _) = analyze_source(
        "/array-filter-predicate.php",
        r#"<?php
/** @param mixed[] $values
 *  @return list<string>
 */
function stringsOnly(array $values): array {
    return array_values(array_filter($values, is_string(...)));
}
"#,
        false,
        builtins(),
    );
    assert_no_issue(&analyzed, &["php.returnType", "php.arguments"]);
}

#[test]
fn array_filter_null_predicate_preserves_shape() {
    let (parsed, analyzed, _) = analyze_source(
        "/array-filter-shape.php",
        r#"<?php
/** @return array{request: string, response?: string} */
function response(string $request, ?string $response): array {
    return array_filter([
        'request' => $request,
        'response' => $response,
    ], static fn ($value) => $value !== null);
}
"#,
        false,
        builtins(),
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
    assert_text_type(
        &analyzed,
        &parsed.root,
        "array_filter([\n        'request' => $request,\n        'response' => $response,\n    ], static fn ($value) => $value !== null)",
        "array{request:string,response?:string}",
    );
}

#[test]
fn environment_arena_preserves_fork_isolation_across_blocks() {
    let original = Env::new(1);
    original.set("$value", Type::INT);

    let forks: Vec<Env> = (0..20).map(|_| original.fork()).collect();
    for (index, fork) in forks.iter().enumerate() {
        assert!(!fork.same_handle(&original));
        for other in &forks[..index] {
            assert!(!fork.same_handle(other));
        }
    }
    for (index, fork) in forks.iter().enumerate() {
        fork.set("$value", Type::literal_int(&index.to_string()));
    }
    assert_eq!(original.get("$value"), Some(Type::INT));
    for (index, fork) in forks.iter().enumerate() {
        let value = fork.get("$value").expect("fork value");
        assert_eq!(value.to_string(), index.to_string());
    }
}

#[test]
fn dynamic_array_assignment_preserves_string_key_type() {
    let (parsed, analyzed, _) = analyze_source(
        "/array-key-assignment.php",
        r#"<?php
/** @param list<string> $values
 *  @return list<string>
 */
function normalize(array $values): array {
    $normalized = [];
    foreach ($values as $value) {
        $normalized[$value] = true;
    }
    return array_keys($normalized);
}
/** @param list<string> $values
 *  @return list<string>
 */
function collect(array $values): array {
    $result = [];
    foreach ($values as $value) {
        $result[] = $value;
    }
    return $result;
}
"#,
        false,
        builtins(),
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
    assert_text_type(
        &analyzed,
        &parsed.root,
        "array_keys($normalized)",
        "list<string>",
    );
}

#[test]
fn json_encode_throw_flag_removes_false_return() {
    let (parsed, analyzed, _) = analyze_source(
        "/json-encode.php",
        r#"<?php
function encode(mixed $value): string {
    return json_encode($value, \JSON_THROW_ON_ERROR);
}
"#,
        false,
        builtins(),
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
    assert_text_type(
        &analyzed,
        &parsed.root,
        r"json_encode($value, \JSON_THROW_ON_ERROR)",
        "string",
    );
}

#[test]
fn string_position_guard_flows_into_substring_offset() {
    let (parsed, analyzed, _) = analyze_source(
        "/string-functions.php",
        r#"<?php
function suffix(string $value): string {
    $position = strpos($value, ':');
    if ($position === false) {
        return '';
    }

    return substr($value, $position + 1);
}
"#,
        true,
        builtins(),
    );
    assert_text_type(
        &analyzed,
        &parsed.root,
        "substr($value, $position + 1)",
        "string",
    );
    assert_no_issue(&analyzed, &["php.arguments", "php.returnType"]);
}

#[test]
fn string_replacement_return_follows_subject() {
    let (parsed, analyzed) = analyze_with(
        "/string-replacement.php",
        r#"<?php
function normalized(string $value): string {
    return str_replace('_', '-', $value);
}
function normalizedMany(array $values): array {
    return str_ireplace('_', '-', $values);
}
function replacedRange(string $value): string {
    return substr_replace($value, '-', 1, 1);
}
function normalizedNullable(?string $value): string {
    return str_replace('_', '-', $value);
}
/** @param array-key $value */
function normalizedKey($value): string {
    return str_replace('_', '-', $value);
}
function normalizedUnknown(mixed $input): mixed {
    return str_replace('_', '-', $input);
}
/** @param list<string> $values */
function lastValue(array $values): ?string {
    return array_last($values);
}
function regexNormalized(string $value): string {
    $result = preg_replace('/_/', '-', $value);
    if ($result === null) {
        return '';
    }
    return $result;
}
"#,
        vec![stubs85()],
        builtins(),
    );
    let root = &parsed.root;
    assert_text_type(&analyzed, root, "str_replace('_', '-', $value)", "string");
    assert_text_type(
        &analyzed,
        root,
        "str_ireplace('_', '-', $values)",
        "array<mixed,string>",
    );
    assert_text_type(
        &analyzed,
        root,
        "substr_replace($value, '-', 1, 1)",
        "string",
    );
    assert_text_type(&analyzed, root, "str_replace('_', '-', $value)", "string");
    assert_text_type(&analyzed, root, "array_last($values)", "null|string");
    assert_text_type(&analyzed, root, "str_replace('_', '-', $input)", "mixed");
    assert_text_type(
        &analyzed,
        root,
        "preg_replace('/_/', '-', $value)",
        "null|string",
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn array_assignment_preserves_aliases_and_union_shapes() {
    let (_, analyzed, _) = analyze_source(
        "/assigned-alias-shape.php",
        r#"<?php
/** @phpstan-type Module array{name: string, source?: string|null} */
class App {
    /** @return Module */
    public function module(): array {}
}
/** @return array{name: string, source: string} */
function module(App $app): array {
    $module = $app->module();
    $module['source'] = 'url';
    return $module;
}
/** @return array{height: int, id: string, width: int} */
function size(bool $large): array {
    $size = $large
        ? ['height' => 800, 'width' => 800]
        : ['height' => 400, 'width' => 400];
    $size['id'] = 'id';
    return $size;
}
"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn array_spread_preserves_string_key_shape() {
    let (parsed, analyzed, _) = analyze_source(
        "/spread-shape.php",
        r#"<?php
/** @return array{id: string, email: string} */
function base(): array {}
/** @return array{id: string, email: string, active: bool} */
function row(): array {
    return [...base(), 'active' => true];
}
"#,
        false,
        Vec::new(),
    );
    assert_text_type(
        &analyzed,
        &parsed.root,
        "[...base(), 'active' => true]",
        "array{active:true,email:string,id:string}",
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn conditional_literal_key_assignments_produce_optional_shape_fields() {
    let (_, analyzed, _) = analyze_source(
        "/optional-shape.php",
        r#"<?php
/**
 * @return array{languageId?: string, currencyId?: string}
 */
function parameters(bool $language, bool $currency): array {
    $result = [];
    if ($language) {
        $result['languageId'] = 'language';
    }
    if ($currency) {
        $result['currencyId'] = 'currency';
    }
    return $result;
}
"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn builtin_dependent_return_types() {
    let (parsed, analyzed) = analyze_with(
        "/dependent-builtins.php",
        r#"<?php
/** @param list<string> $values */
function guardedLast(array $values): string {
    if (!$values) {
        throw new RuntimeException();
    }
    return array_last($values);
}
/** @param list<string> $values */
function guardedShift(array $values): string {
    if ($values !== []) {
        return array_shift($values);
    }
    return '';
}
function compares(string $left, string $right): bool {
    return version_compare($left, $right, '>');
}
function loads(): object {
    $loader = require __DIR__ . '/autoload.php';
    return $loader;
}
function printed(mixed $value): string {
    return print_r($value, true);
}
function expires(DateTimeImmutable $now): DateTimeImmutable {
    return $now->modify('+120 minutes');
}
function expiresDynamic(DateTimeImmutable $now, int $seconds): DateTimeImmutable {
    return $now->modify('+' . $seconds . ' seconds');
}
/** @param list<string> $values */
function reduced(array $values): int {
    return array_reduce($values, static fn (int $count, string $value): int => $count + 1, 0);
}
/** @param array<string, string> $values */
function flipped(array $values, string $id): string {
    return array_flip($values)[$id];
}
"#,
        vec![stubs85()],
        builtins(),
    );
    let root = &parsed.root;
    assert_text_type(&analyzed, root, "array_last($values)", "string");
    assert_text_type(&analyzed, root, "array_shift($values)", "string");
    assert_text_type(
        &analyzed,
        root,
        "version_compare($left, $right, '>')",
        "bool",
    );
    assert_text_type(
        &analyzed,
        root,
        "require __DIR__ . '/autoload.php'",
        "mixed",
    );
    assert_text_type(&analyzed, root, "print_r($value, true)", "string");
    assert_text_type(
        &analyzed,
        root,
        "$now->modify('+120 minutes')",
        "DateTimeImmutable",
    );
    assert_text_type(
        &analyzed,
        root,
        "$now->modify('+' . $seconds . ' seconds')",
        "DateTimeImmutable",
    );
    assert_text_type(
        &analyzed,
        root,
        "array_flip($values)",
        "array<string,string>",
    );
    assert_text_type(
        &analyzed,
        root,
        "array_reduce($values, static fn (int $count, string $value): int => $count + 1, 0)",
        "int",
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn builtin_configuration_and_url_return_types() {
    let (parsed, analyzed) = analyze_with(
        "/builtin-config-url.php",
        r#"<?php
function acceptsString(string $value): void {}
function memoryLimit(): void {
    acceptsString(ini_get('memory_limit'));
}
function unknownIniOption(string $option): string|false {
    return ini_get($option);
}
function missingIniOption(): string|false {
    return ini_get('shopware_lsp.missing');
}
function parseQuery(string $dsn): void {
    $query = parse_url($dsn, PHP_URL_QUERY);
    if ($query === false || $query === null) {
        return;
    }
    $result = [];
    parse_str($query, $result);
}
function queryComponent(string $dsn): string|false|null {
    return parse_url($dsn, PHP_URL_QUERY);
}
function portComponent(string $dsn): int|false|null {
    return parse_url($dsn, PHP_URL_PORT);
}
function dynamicComponent(string $dsn, int $component): array|int|string|false|null {
    return parse_url($dsn, $component);
}
"#,
        vec![stubs85()],
        builtins(),
    );
    let root = &parsed.root;
    assert_text_type(&analyzed, root, "ini_get('memory_limit')", "string");
    assert_text_type(&analyzed, root, "ini_get($option)", "false|string");
    assert_text_type(
        &analyzed,
        root,
        "ini_get('shopware_lsp.missing')",
        "false|string",
    );
    assert_text_type(
        &analyzed,
        root,
        "parse_url($dsn, PHP_URL_QUERY)",
        "false|null|string",
    );
    assert_text_type(
        &analyzed,
        root,
        "parse_url($dsn, PHP_URL_PORT)",
        "false|int|null",
    );
    assert_text_type(
        &analyzed,
        root,
        "parse_url($dsn, $component)",
        "array{fragment:string,host:string,pass:string,path:string,port:int,query:string,scheme:string,user:string}|false|int|null|string",
    );
    assert_no_issue(&analyzed, &["php.arguments", "php.returnType"]);
}

#[test]
fn empty_guard_narrows_optional_array_field() {
    let (_, analyzed) = analyze_with(
        "/empty-guard.php",
        r#"<?php
/** @param array{security_eol?: string} $support */
function securityEol(array $support): DateTime {
    if (empty($support['security_eol'])) {
        throw new RuntimeException();
    }
    return new DateTime($support['security_eol']);
}
"#,
        vec![stubs85()],
        builtins(),
    );
    assert_no_issue(&analyzed, &["php.arguments", "php.returnType"]);
}

#[test]
fn builtin_string_collection_return_types() {
    let (parsed, analyzed, _) = analyze_source(
        "/builtin-string-collections.php",
        r#"<?php
/** @return list<string> */
function splitIds(string $ids): array {
    return explode(',', $ids);
}
function fileExtension(string $path): string {
    return pathinfo($path, PATHINFO_EXTENSION);
}
function allPathParts(string $path): array|string {
    return pathinfo($path, PATHINFO_ALL);
}
"#,
        true,
        builtins(),
    );
    let root = &parsed.root;
    assert_text_type(&analyzed, root, "explode(',', $ids)", "list<string>");
    assert_text_type(
        &analyzed,
        root,
        "pathinfo($path, PATHINFO_EXTENSION)",
        "string",
    );
    assert_text_type(
        &analyzed,
        root,
        "pathinfo($path, PATHINFO_ALL)",
        "array{basename:string,dirname:string,extension:string,filename:string}|string",
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn shorthand_ternary_excludes_falsy_condition_type() {
    let (parsed, analyzed) = analyze_with(
        "/shorthand-ternary.php",
        r#"<?php
function pieces(string $value): array {
    return preg_split('/\s+/', $value, -1, PREG_SPLIT_NO_EMPTY) ?: [$value];
}
function lastPiece(string $value): ?string {
    return array_last(preg_split('/\s+/', $value) ?: [$value]);
}
"#,
        vec![stubs85()],
        builtins(),
    );
    let root = &parsed.root;
    assert_text_type(
        &analyzed,
        root,
        r"preg_split('/\s+/', $value, -1, PREG_SPLIT_NO_EMPTY) ?: [$value]",
        "list<string>",
    );
    assert_text_type(
        &analyzed,
        root,
        r"array_last(preg_split('/\s+/', $value) ?: [$value])",
        "null|string",
    );
    assert_no_issue(&analyzed, &["php.arguments", "php.returnType"]);
}

#[test]
fn override_body_uses_specialized_inherited_parameter() {
    let (parsed, analyzed, _) = analyze_source(
        "/specialized-override.php",
        r#"<?php
class Command {}
class ConcreteCommand extends Command {
    public string $iso = 'EUR';
}
/** @template TCommand of Command */
abstract class Handler {
    /** @param TCommand $command */
    abstract public function handle(Command $command): void;
}
/** @extends Handler<ConcreteCommand> */
class ConcreteHandler extends Handler {
    public function handle(Command $command): void {
        strlen($command->iso);
    }
}
"#,
        true,
        builtins(),
    );
    assert_text_type(&analyzed, &parsed.root, "$command->iso", "string");
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn generated_stub_mismatch_falls_back_without_diagnostic() {
    let parsed = parse(
        r#"<?php
function consume(): string {
    return generated_builtin(1);
}
"#,
    );
    let bound = bind("/generated-stub.php", &parsed);
    let path = "phpstub://test/generated";
    let stub = Document {
        path: path.into(),
        symbols: vec![Symbol {
            id: new_symbol_id(SymbolKind::Function, "generated_builtin", path, 0),
            kind: SymbolKind::Function,
            name: "generated_builtin".into(),
            fully_qualified: "generated_builtin".into(),
            path: path.into(),
            flags: Flags::INTERNAL | Flags::GENERATED_STUB,
            return_type: Type::STRING,
            parameters: vec![Parameter {
                name: "$value".into(),
                ty: Type::STRING,
                ..Parameter::default()
            }],
            ..Symbol::default()
        }],
        ..Document::default()
    };
    let snapshot = snapshot(&[bound.clone(), stub]);
    let analyzed = analyze_linked(&snapshot, Vec::new(), &bound, &parsed);
    assert_text_type(&analyzed, &parsed.root, "generated_builtin(1)", "string");
    assert_no_issue(&analyzed, &["php.arguments", "php.returnType"]);
}

#[test]
fn unpacked_list_uses_element_type_for_variadic_parameter() {
    let (parsed, analyzed, _) = analyze_source(
        "/unpacked-variadic.php",
        r#"<?php
class Constraint {}
class Required extends Constraint {}
class Field {
    /** @return static */
    public function addFlags(Constraint ...$constraints): static {}
}
function add(string $name, Constraint ...$constraints): void {}
function configure(): Field {
    $constraints = [new Required()];
    add('name', ...$constraints);
    return (new Field())->addFlags(...$constraints);
}
"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.arguments", "php.returnType"]);
    assert_text_type(
        &analyzed,
        &parsed.root,
        "(new Field())->addFlags(...$constraints)",
        "Field",
    );
}

#[test]
fn array_filter_without_callback_narrows_falsy_elements() {
    let (parsed, analyzed, _) = analyze_source(
        "/array-filter-falsy.php",
        r#"<?php
class Flag {}
class Required extends Flag {}
class ApiAware extends Flag {}
class Field {
    public function addFlags(Flag ...$flags): void {}
}
function configure(Field $field, ?Required $required, ?ApiAware $apiAware): void {
    $flags = array_filter([$required, $apiAware]);
    $field->addFlags(...$flags);
}
"#,
        false,
        builtins(),
    );
    assert_text_type(
        &analyzed,
        &parsed.root,
        "array_filter([$required, $apiAware])",
        "array{0?:Required,1?:ApiAware}",
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn predicate_narrows_repeated_member_expressions() {
    let (_, analyzed, _) = analyze_source(
        "/member-predicate.php",
        r#"<?php
class Value {}
function consumeString(string $value): void {}
function consumeValue(?Value $value): void {}
class Consumer {
    public Value|string|null $left;
    public Value|string|null $right;

    public function run(): void {
        if (is_string($this->left)) {
            consumeString($this->left);
        }
        if (is_string($this->left) || is_string($this->right)) {
            return;
        }
        consumeValue($this->left);
        consumeValue($this->right);
    }
}
"#,
        false,
        builtins(),
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn match_true_narrows_each_arm() {
    let (_, analyzed, _) = analyze_source(
        "/match-true.php",
        r#"<?php
abstract class Filter {}
class EqualsFilter extends Filter {
    public function getField(): string {}
}
class RangeFilter extends Filter {
    public function getParameters(): array {}
}
function serializeFilter(Filter $filter): mixed {
    return match (true) {
        $filter instanceof EqualsFilter => $filter->getField(),
        $filter instanceof RangeFilter => $filter->getParameters(),
        default => null,
    };
}
"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.undefined"]);
}

/// The analyzer state has no test constructor, so only the document-level
/// derivation is asserted.
#[test]
fn literal_inference_derives_without_recorded_fact() {
    let parsed = parse(r#"<?php "stable literal";"#);
    let document = bind("/literal.php", &parsed);
    let literals = query::nodes(&parsed.root, &[PHP_STRING]);
    assert_eq!(literals.len(), 1);
    assert!(document.type_fact(node_identity(&literals[0])).is_none());
    let fact = document.type_of(&literals[0]);
    assert_eq!(fact.ty.to_string(), r#""stable literal""#);
    assert_eq!(fact.source, TypeSource::Literal);
}

#[test]
fn join_environments_grows_for_disjoint_branches() {
    let left = Env::new(5);
    let right = Env::new(5);
    for index in 0..5 {
        left.set(&format!("$left{index}"), Type::INT);
        right.set(&format!("$right{index}"), Type::STRING);
    }
    left.set("$shared", Type::INT);
    right.set("$shared", Type::STRING);

    let joined = join_environments(Relations::default(), &left, &right);
    assert_eq!(joined.len(), 11);
    assert!(joined.is_map());
    let shared = joined.get("$shared").expect("shared binding");
    assert_eq!(shared.to_string(), "int|string");
}

#[test]
fn expression_and_flow_inference() {
    let (parsed, analyzed, _) = analyze_source(
        "/service.php",
        r#"<?php
namespace App;

class Repository {
    public function find(string $id): Product|null {}
}
class Product {
    public string $name;
}
class Service {
    public function run(Repository $repository, string|null $id): string {
        if ($id === null) {
            return 'missing';
        }
        $product = $repository->find($id);
        if (!$product instanceof Product) {
            return 'unknown';
        }
        $names = [$product->name, 'fallback'];
        return $names[0];
    }
}"#,
        false,
        builtins(),
    );
    let root = &parsed.root;
    assert_eq!(
        symbol_named(&analyzed, "run").return_type.to_string(),
        "string"
    );
    assert_node_type(&analyzed, root, PHP_MEMBER_CALL, "App\\Product|null");
    assert_text_type(&analyzed, root, "$product->name", "string");
    assert_text_type(&analyzed, root, "$names[0]", "string");
}

#[test]
fn generic_call_inference() {
    let parsed = parse(
        r#"<?php
function identity(T $value): T { return $value; }
function useIt(): string { return identity('value'); }
"#,
    );
    let mut bound = bind("/generic.php", &parsed);
    for symbol in &mut bound.symbols {
        if &*symbol.name == "identity" {
            symbol.parameters[0].ty = Type::template("T");
            symbol.return_type = Type::template("T");
        }
    }
    let snapshot = snapshot(std::slice::from_ref(&bound));
    let analyzed = analyzer(&snapshot, Vec::new()).analyze(&bound, &parsed.root);
    assert_text_type(&analyzed, &parsed.root, "identity('value')", r#""value""#);
}

#[test]
fn constructor_validation_and_generic_inference() {
    let source = r#"<?php
/**
 * @template T of object
 */
class Box {
    /** @param T $value */
    public function __construct($value) {}
    /** @return T */
    public function value(): object { return $this->value; }
}
class Product {}
function valid(): Box { return new Box(new Product()); }
function invalid(): Box { return new Box('wrong'); }
"#;
    let parsed = parse(source);
    let bound = bind("/constructor.php", &parsed);
    let snapshot = snapshot(std::slice::from_ref(&bound));
    let constructors =
        MemberResolver::new(&snapshot).methods(&Type::named("Box", vec![]), "__construct");
    assert_eq!(constructors.len(), 1);
    let constructor = &constructors[0].symbol;
    assert_eq!(constructor.parameters[0].ty.to_string(), "T");
    assert_eq!(constructor.templates().len(), 1);
    assert_eq!(constructor.templates()[0].bound.to_string(), "object");
    assert!(
        !resolve_signature(
            snapshot.relations(),
            constructor.clone(),
            &[Argument::of(Type::literal_string("wrong"))],
        )
        .compatible
    );
    let analyzed = analyze_linked(&snapshot, Vec::new(), &bound, &parsed);
    assert_text_type(
        &analyzed,
        &parsed.root,
        "new Box(new Product())",
        "Box<Product>",
    );
    let argument_issues = analyzed
        .issues
        .iter()
        .filter(|issue| &*issue.code == "php.arguments")
        .count();
    assert_eq!(argument_issues, 1);
}

#[test]
fn tuple_and_unknown_length_argument_unpacking() {
    let (_, analyzed, _) = analyze_source(
        "/argument-unpack.php",
        r#"<?php
class DomainException {
    public function __construct(int $status, string $code, string $message, array $parameters) {}
}
function exception(string $label, ?string $code): DomainException {
    if ($code === null) {
        $messages = ['Invalid {{ label }}', ['label' => $label]];
    } else {
        $messages = ['Invalid {{ code }}', ['code' => $code]];
    }
    return new DomainException(400, 'invalid', ...$messages);
}
abstract class Command {
    /** @param array<array-key, mixed> $payload */
    public static function create(array $payload): static {
        return new static(...$payload);
    }
}
"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn defaulted_method_template_flows_through_chained_generic_return() {
    let (parsed, analyzed, _) = analyze_source(
        "/defaulted-method-template.php",
        r#"<?php
/**
 * @template IDStructure of string|array<string, string> = string
 */
class Criteria {}

/**
 * @template IDStructure of string|array<string, string> = string
 */
class IdSearchResult {
    /** @return list<IDStructure> */
    public function getIds(): array {}
}

class Repository {
    /**
     * @template IDStructure of string|array<string, string> = string
     * @param Criteria<IDStructure> $criteria
     * @return IdSearchResult<IDStructure>
     */
    public function searchIds(Criteria $criteria): IdSearchResult {}
}

class Consumer {
    /** @return list<string> */
    public function getIds(Repository $repository): array {
        $criteria = new Criteria();
        return $repository->searchIds($criteria)->getIds();
    }
}"#,
        false,
        Vec::new(),
    );
    assert_text_type(
        &analyzed,
        &parsed.root,
        "$repository->searchIds($criteria)->getIds()",
        "list<string>",
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn generic_self_return_preserves_receiver_arguments() {
    let (parsed, analyzed, _) = analyze_source(
        "/generic-self.php",
        r#"<?php
/** @template T = string */
class Collection {
    /** @return self<T> */
    public function filtered(): self {}
    /** @return list<T> */
    public function values(): array {}
}
/** @template T = string */
class Owner {
    /** @return Collection<T> */
    public function collection(): Collection {}
    /** @return list<T> */
    public function values(): array {
        return $this->collection()->filtered()->values();
    }
}"#,
        false,
        Vec::new(),
    );
    assert_text_type(
        &analyzed,
        &parsed.root,
        "$this->collection()->filtered()",
        "Collection<T>",
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn generic_self_factory_and_fluent_methods_refine_receiver() {
    let (parsed, analyzed, _) = analyze_source(
        "/generic-fluent-self.php",
        r#"<?php
interface RuleError {}
interface IdentifierRuleError extends RuleError {}
interface LineRuleError extends RuleError {}

/** @template-covariant T of RuleError */
final class RuleErrorBuilder {
    /** @return self<RuleError> */
    public static function message(string $message): self {}

    /** @return self<T&IdentifierRuleError> */
    public function identifier(string $identifier): self {}

    /** @return self<T&LineRuleError> */
    public function line(int $line): self {}

    /** @return T */
    public function build(): RuleError {}
}

/** @return list<RuleError> */
function errors(): array {
    return [
        RuleErrorBuilder::message('failure')
            ->identifier('app.failure')
            ->line(1)
            ->build(),
    ];
}"#,
        false,
        Vec::new(),
    );
    let root = &parsed.root;
    assert_text_type(
        &analyzed,
        root,
        "RuleErrorBuilder::message('failure')",
        "RuleErrorBuilder<RuleError>",
    );
    assert_text_type(
        &analyzed,
        root,
        "RuleErrorBuilder::message('failure')\n            ->identifier('app.failure')",
        "RuleErrorBuilder<IdentifierRuleError&RuleError>",
    );
    assert_text_type(
        &analyzed,
        root,
        "RuleErrorBuilder::message('failure')\n            ->identifier('app.failure')\n            ->line(1)\n            ->build()",
        "IdentifierRuleError&LineRuleError&RuleError",
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn resolve_special_type_bounds_adversarial_nesting() {
    let mut value = Type::STRING;
    for _ in 0..MAX_SPECIAL_TYPE_DEPTH * 2 {
        value = Type::named("Nested", vec![value]);
    }
    let mut resolved = resolve_special_type(
        &value,
        &Type::named("Receiver", vec![]),
        &Type::named("Current", vec![]),
    );
    for _ in 0..MAX_SPECIAL_TYPE_DEPTH {
        if resolved.is_unknown() {
            return;
        }
        assert_eq!(resolved.kind(), Kind::Object);
        resolved = resolved.argument(0);
    }
    assert!(resolved.is_unknown());
}

#[test]
fn resolve_special_type_bounds_fluent_receiver_growth() {
    let branches: Vec<Type> = (0..9)
        .map(|index| {
            Type::named(
                &format!("NodeDefinition{index}"),
                vec![Type::static_type(vec![])],
            )
        })
        .collect();
    let declared = Type::union(branches);

    let mut receiver = Type::named("ArrayNodeDefinition", vec![]);
    for _ in 0..40 {
        receiver = resolve_special_type(&declared, &receiver, &receiver);
        assert!(
            !exceeds_special_type_nodes(&receiver, 16 * MAX_SPECIAL_TYPE_NODES),
            "one fluent link must not grow the receiver without bound"
        );
    }
}

#[test]
fn resolve_special_type_substitutes_modest_receivers() {
    let receiver = Type::named("Receiver", vec![]);
    let resolved = resolve_special_type(
        &Type::named("Builder", vec![Type::static_type(vec![])]),
        &receiver,
        &Type::named("Current", vec![]),
    );
    assert_eq!(Type::named("Builder", vec![receiver]), resolved);
}

#[test]
fn untyped_magic_method_parameter_accepts_typed_array() {
    let (_, analyzed, _) = analyze_source(
        "/magic-method.php",
        r#"<?php
/**
 * @method mixed randomElement($array = ['a', 'b', 'c'])
 */
class Generator {}

class Consumer {
    /** @param list<string> $ids */
    public function getRandomId(Generator $generator, array $ids): ?string {
        return $generator->randomElement($ids);
    }
}"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn php_doc_scalar_pseudo_type_accepts_scalar_shape_values() {
    let (_, analyzed, _) = analyze_source(
        "/scalar-pseudo-type.php",
        r#"<?php
namespace App;

final class Event {
    /** @return array<string, scalar|array<mixed>|null> */
    public function values(): array {
        return [
            'active' => true,
            'count' => 1,
            'name' => 'shopware',
            'ratio' => 1.5,
        ];
    }
}"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.returnType", "php.undefined"]);
}

#[test]
fn faker_random_element_preserves_input_element_type() {
    let (parsed, analyzed, _) = analyze_source(
        "/faker.php",
        r#"<?php
namespace Faker {
    /**
     * @method mixed randomElement($array = ['a', 'b', 'c'])
     */
    class Generator {}
}

namespace App {
    use Faker\Generator;

    class Consumer {
        /** @param list<string> $ids */
        public function getRandomId(Generator $generator, array $ids): ?string {
            return $generator->randomElement($ids);
        }
    }
}"#,
        false,
        vec![Arc::new(crate::FakerTypes)],
    );
    assert_text_type(
        &analyzed,
        &parsed.root,
        "$generator->randomElement($ids)",
        "null|string",
    );
    assert_no_issue(&analyzed, &["php.arguments", "php.returnType"]);
}

#[test]
fn new_self_uses_enclosing_class_type() {
    let (parsed, analyzed, _) = analyze_source(
        "/new-self.php",
        r#"<?php
namespace App;
class DomainError {
    public static function create(): self {
        return new self();
    }
}"#,
        false,
        Vec::new(),
    );
    assert_text_type(&analyzed, &parsed.root, "new self()", "App\\DomainError");
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn new_self_preserves_and_can_replace_enclosing_class_template() {
    let (parsed, analyzed, _) = analyze_source(
        "/generic-new-self.php",
        r#"<?php
namespace App;
/**
 * @template T = string
 */
class Box {
    /** @param T $value */
    public function __construct(mixed $value) {}

    /**
     * @param T $value
     * @return self<T>
     */
    public function copy(mixed $value): self {
        return new self($value);
    }

    /**
     * @template U
     * @param U $replacement
     * @return self<U>
     */
    public function replace(mixed $replacement): self {
        return new self($replacement);
    }
}"#,
        false,
        Vec::new(),
    );
    assert_text_type(&analyzed, &parsed.root, "new self($value)", "App\\Box<T>");
    assert_text_type(
        &analyzed,
        &parsed.root,
        "new self($replacement)",
        "App\\Box<U>",
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn nested_closure_return_uses_closure_declaration() {
    let (_, analyzed, _) = analyze_source(
        "/nested-closure.php",
        r#"<?php
function consume(callable $callback): void {}
function run(): void {
    consume(function (): bool {
        return true;
    });
}"#,
        false,
        Vec::new(),
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}
