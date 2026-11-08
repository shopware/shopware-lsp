//! Port of `inference_test.go` (second slice: lines 1153..2409).

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_parser_php::syntax::PHP_MEMBER_CALL;
use swlsp_php_binder::link;
use swlsp_php_semantic::{Document, NameKind, Snapshot};
use swlsp_php_types::{Type, substitute};

use super::{
    Parsed, analyze_source, assert_node_type, assert_text_type, bind, builtins, parse, snapshot,
    symbol_named,
};
use crate::{Extension, link_members};

fn exts(with_builtins: bool) -> Vec<Arc<dyn Extension>> {
    if with_builtins {
        builtins()
    } else {
        Vec::new()
    }
}

fn analyze(
    path: &str,
    source: &str,
    with_builtins: bool,
    with_stubs: bool,
) -> (Parsed, Document, Arc<Snapshot>) {
    analyze_source(path, source, with_stubs, exts(with_builtins))
}

fn analyze_and_link(path: &str, source: &str, with_builtins: bool) -> Document {
    let (parsed, analyzed, snapshot) = analyze(path, source, with_builtins, false);
    link_members(&analyzed, &snapshot, &parsed.root)
}

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

#[track_caller]
fn assert_resolved(document: &Document, names: &[&str]) -> usize {
    let mut count = 0;
    for reference in &document.references {
        if names.contains(&&*reference.name) {
            assert!(
                !reference.resolved.is_empty(),
                "{} unresolved",
                reference.name
            );
            count += 1;
        }
    }
    count
}

#[test]
fn instanceof_narrowed_property_survives_leading_comments() {
    let linked = analyze_and_link(
        "/property-comment-flow.php",
        r#"<?php
interface TranslatorInterface {}
class SymfonyTranslator implements TranslatorInterface {
    public function setFallbackLocales(array $locales): void {}
}
class Translator {
    public function __construct(private TranslatorInterface $translator) {}
    public function reset(): void {
        if ($this->translator instanceof SymfonyTranslator) {
            // Explain why this implementation-specific reset is required.
            $this->translator->setFallbackLocales([]);
        }
    }
}
"#,
        false,
    );
    assert_resolved(&linked, &["setFallbackLocales"]);
}

#[test]
fn terminating_negated_instanceof_narrows_to_intersection() {
    let (parsed, analyzed, snapshot) = analyze(
        "/instanceof-intersection.php",
        r#"<?php
/**
 * @template TKey
 * @template TValue
 */
interface IteratorAggregate {}
/**
 * @template T
 * @implements IteratorAggregate<array-key, T>
 */
abstract class Collection implements IteratorAggregate {}
interface StorageAware {
    public function getStorageName(): string;
}
abstract class Field {
    public function getPropertyName(): string {}
}
/** @extends Collection<Field> */
class FieldCollection extends Collection {}
function storageNames(FieldCollection $fields): \Generator {
    foreach ($fields as $field) {
        if (!$field instanceof StorageAware) {
            continue;
        }

        yield $field->getStorageName() => $field->getPropertyName();
    }
}
function storageMap(Field $field): array {
    $mapped = [];
    if ($field instanceof StorageAware && $field->getStorageName() !== '') {
        $mapped[$field->getStorageName()] = $field->getPropertyName();
    }
    return $mapped;
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.undefined", "php.returnType"]);
    let linked = link_members(&analyzed, &snapshot, &parsed.root);
    assert_eq!(
        assert_resolved(&linked, &["getStorageName", "getPropertyName"]),
        5
    );
}

#[test]
fn foreach_intersection_survives_member_call_inside_array_index_argument() {
    let linked = analyze_and_link(
        "/array-index-flow.php",
        r#"<?php
interface StorageAware {
    public function getStorageName(): string;
}
class Field {}
class Query {
    public function setParameter(string $name, mixed $value): void {}
}
/** @param list<Field> $fields */
function write(array $fields, array $primaryKey, Query $query): void {
    foreach ($fields as $field) {
        if (!$field instanceof StorageAware) {
            continue;
        }
        $param = 'param';
		$primaryKey[$field->getStorageName()] = 'value';
        $query->setParameter($param, $primaryKey[$field->getStorageName()]);
    }
}
"#,
        false,
    );
    assert_resolved(&linked, &["getStorageName"]);
}

#[test]
fn array_shift_preserves_shape_element_for_interface_narrowing() {
    let linked = analyze_and_link(
        "/array-shift-shape.php",
        r#"<?php
interface SalesChannelDefinitionInterface {
    public function processCriteria(): void;
}
abstract class EntityDefinition {
    abstract public function getEntityName(): string;
}
function process(EntityDefinition $initial): void {
    if (!$initial instanceof SalesChannelDefinitionInterface) {
        return;
    }
    $queue = [['definition' => $initial]];
    while ($queue !== []) {
        $current = array_shift($queue);
        $definition = $current['definition'];
        if ($definition instanceof SalesChannelDefinitionInterface) {
            $definition->processCriteria();
            $definition->getEntityName();
        }
    }
}
"#,
        true,
    );
    assert_resolved(&linked, &["getEntityName"]);
}

#[test]
fn array_shift_inside_non_empty_loop_does_not_add_null() {
    let (_, analyzed, _) = analyze(
        "/array-shift-non-empty.php",
        r#"<?php
class Entity {
    public function get(string $property): mixed {}
}
function resolve(Entity $entity, string $path): void {
    $parts = explode('.', $path);
    while ($parts !== []) {
        $part = array_shift($parts);
        $entity->get($part);
    }
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn array_pop_from_explode_does_not_add_null() {
    let (_, analyzed, _) = analyze(
        "/array-pop-explode.php",
        r#"<?php
function shortName(string $class): string {
    $parts = explode('\\', $class);
    return array_pop($parts);
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn array_pop_and_shift_respect_non_empty_list_php_doc() {
    let (_, analyzed, _) = analyze(
        "/non-empty-list.php",
        r#"<?php
/** @param non-empty-list<string> $values */
function first(array $values): string {
    return array_shift($values);
}
/** @param non-empty-list<array{name: string}> $values */
function lastName(array $values): string {
    return array_pop($values)['name'];
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType", "php.arguments"]);
}

#[test]
fn strict_empty_array_comparison_narrows_array_to_non_empty() {
    let (_, analyzed, _) = analyze(
        "/non-empty-array-comparison.php",
        r#"<?php
class Tags {
    /** @var array<string, array{id: string}> */
    private array $values = [];

    /** @return non-empty-array<string, array{id: string}> */
    public function all(): array {
        if ($this->values !== []) {
            return $this->values;
        }

        /** @var non-empty-array<string, array{id: string}> $result */
        $result = loadTags();
        return $this->values = $result;
    }
}
function loadTags(): array {}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn appending_to_non_empty_tuple_keeps_non_empty_list() {
    let (_, analyzed, _) = analyze(
        "/non-empty-tuple-append.php",
        r#"<?php
/**
 * @param list<string> $parts
 * @return non-empty-list<string>
 */
function path(array $parts): array {
    $values = ['root'];
    foreach ($parts as $part) {
        $values[] = $part;
    }
    return $values;
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn array_shift_in_positive_count_ternary_is_non_null() {
    let (_, analyzed, _) = analyze(
        "/count-array-shift.php",
        r#"<?php
/** @param array<string, string> $values */
function identifier(array $values): array|string {
    return count($values) === 1 ? array_shift($values) : $values;
}
"#,
        true,
        true,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn preg_replace_callback_knows_whole_match_is_string() {
    let (_, analyzed, _) = analyze(
        "/preg-callback.php",
        r#"<?php
function acceptsString(string $value): string { return $value; }
function replace(string $content): string {
    return (string) preg_replace_callback('/x/', static function (array $match): string {
        return acceptsString(str_replace('x', 'y', $match[0]));
    }, $content);
}
"#,
        true,
        true,
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn foreach_supports_value_only_traversable_php_doc() {
    let (_, analyzed, _) = analyze(
        "/traversable-value.php",
        r#"<?php
class Item {}
class Items {
    /** @return Traversable<Item> */
    public function getIterator(): Traversable {}
}
function acceptsItem(Item $item): void {}
function consume(Items $items): void {
    foreach ($items->getIterator() as $item) {
        acceptsItem($item);
    }
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn isset_narrowing_survives_terminating_branch() {
    let (_, analyzed, _) = analyze(
        "/isset-property.php",
        r#"<?php
class Entity {
    private ?string $id = null;
    public function id(): string {
        if (!isset($this->id)) {
            return '';
        }
        return $this->id;
    }
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn var_export_return_flag_produces_string() {
    let (_, analyzed, _) = analyze(
        "/var-export.php",
        r#"<?php
function exportKey(string $key): string {
    return var_export($key, true);
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn multiple_isset_operands_narrow_every_shape_offset() {
    let (_, analyzed, _) = analyze(
        "/multiple-isset.php",
        r#"<?php
function acceptsString(string $value): void {}
/** @param array{from?: string, to?: string|null} $range */
function validateRange(array $range): void {
    if (!isset($range['from'], $range['to'])) {
        return;
    }
    acceptsString($range['from']);
    acceptsString($range['to']);
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn predicate_on_null_coalesce_narrows_underlying_array_offset() {
    let (_, analyzed, _) = analyze(
        "/coalesce-predicate.php",
        r#"<?php
/** @param array<string, bool> $permissions */
function acceptsPermissions(array $permissions): void {}
/** @param array{permissions?: array<string, bool>|string|null} $options */
function configure(array $options): void {
    if (is_array($options['permissions'] ?? null)) {
        acceptsPermissions($options['permissions']);
    }
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn boolean_alias_carries_object_refinement_into_true_branch() {
    let (_, analyzed, _) = analyze(
        "/boolean-alias.php",
        r#"<?php
class WriteCommand {}
class UpdateCommand extends WriteCommand {}
function updateOnly(UpdateCommand $command): void {}
function process(WriteCommand $command, bool $changed): void {
    $needsUpdate = $command instanceof UpdateCommand && $changed;
    if ($needsUpdate) {
        updateOnly($command);
    }
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn has_accessor_convention_narrows_repeated_getter() {
    let (_, analyzed, _) = analyze(
        "/has-accessor.php",
        r#"<?php
class Field {}
class Fields {
    public function has(string $name): bool { return false; }
    public function get(string $name): ?Field { return null; }
}
class EntityExistence {
    public function hasEntityName(): bool { return false; }
    public function getEntityName(): ?string { return null; }
}
function acceptsField(Field $field): void {}
function acceptsName(string $name): void {}
function inspect(Fields $fields, EntityExistence $existence, string $key): void {
    if ($fields->has($key)) {
        acceptsField($fields->get($key));
    }
    if ($existence->hasEntityName()) {
        acceptsName($existence->getEntityName());
    }
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn is_subclass_of_narrows_class_string_without_turning_it_into_object() {
    let (_, analyzed, _) = analyze(
        "/is-subclass-class-string.php",
        r#"<?php
class Command {}
/** @param class-string $class */
function packageName(string $class): void {}
/** @param array{class?: class-string} $trace */
function inspect(array $trace): void {
    if (isset($trace['class']) && is_subclass_of($trace['class'], Command::class)) {
        packageName($trace['class']);
    }
}
"#,
        true,
        true,
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn date_time_modify_with_structured_relative_time_does_not_return_false() {
    let (_, analyzed, _) = analyze(
        "/date-modify.php",
        r#"<?php
function retry(\DateTimeImmutable $now, int $seconds): \DateTimeImmutable {
    return $now->modify(sprintf('+%d seconds', $seconds));
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn array_map_preserves_list_and_callback_result() {
    let (_, analyzed, _) = analyze(
        "/array-map.php",
        r#"<?php
/** @param array<string, list<string>> $queries */
function total(array $queries): int {
    return array_sum(array_map('count', $queries));
}
/** @param array<string, mixed> $properties
 * @return list<string>
 */
function names(array $properties): array {
    return array_map(
        static fn (string|int $name): string => strtolower((string) $name),
        array_keys($properties),
    );
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn iterator_to_array_preserves_iterable_key_and_value_types() {
    let (_, analyzed, _) = analyze(
        "/iterator-to-array.php",
        r#"<?php
class Item {}
/** @param iterable<string, Item> $items
 *  @return array<string, Item>
 */
function indexed(iterable $items): array {
    return iterator_to_array($items);
}
/** @param iterable<string, Item> $items
 *  @return list<Item>
 */
function values(iterable $items): array {
    return iterator_to_array($items, false);
}
/** @param iterable<string, Item> $items
 *  @return array<string, Item>
 */
function normalized(iterable $items): array {
    if ($items instanceof Traversable) {
        return iterator_to_array($items);
    }
    return $items;
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn negative_date_time_immutable_check_narrows_engine_interface() {
    let (_, analyzed, _) = analyze(
        "/datetime-interface.php",
        r#"<?php
function mutableDate(DateTimeInterface $value): DateTime {
    if ($value instanceof DateTimeImmutable) {
        return DateTime::createFromImmutable($value);
    }
    return $value;
}
"#,
        true,
        true,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn conditional_return_uses_callable_parameter_argument() {
    let (_, analyzed, _) = analyze(
        "/conditional-parameter.php",
        r#"<?php
class Groups {
    /**
     * @template TResult
     * @param callable(array<string, string>): TResult|null $mapper
     * @return ($mapper is callable ? array<string, list<TResult>> : array<string, list<array<string, string>>>)
     */
    public static function group(?callable $mapper = null): array {}
}
/** @return array<string, list<string>> */
function names(): array {
    return Groups::group(static fn (array $row): string => $row['name']);
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn unresolved_template_return_does_not_produce_diagnostic() {
    let (_, analyzed, _) = analyze(
        "/template-return.php",
        r#"<?php
/**
 * @template T of object
 * @return T
 */
function create(string $class): object {
    return new $class();
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn unconditional_assertions_narrow_following_statements() {
    let (_, analyzed, _) = analyze(
        "/unconditional-assertions.php",
        r#"<?php
class Assert {
    /** @phpstan-assert !null $actual */
    public static function assertNotNull(mixed $actual): void {}
    /**
     * @template ExpectedType of object
     * @param class-string<ExpectedType> $expected
     * @phpstan-assert =ExpectedType $actual
     */
    public static function assertInstanceOf(string $expected, mixed $actual): void {}
}
class Entity {}
class TestCase extends Assert {
    public function notNull(?Entity $entity): Entity {
        static::assertNotNull($entity);
        return $entity;
    }
    public function instance(object $entity): Entity {
        static::assertInstanceOf(Entity::class, $entity);
        return $entity;
    }
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn php_unit_assertions_narrow_inside_traits() {
    let (_, analyzed, _) = analyze(
        "/trait-assertions.php",
        r#"<?php
namespace PHPUnit\Framework;
class Assert {
    /** @phpstan-assert !null $actual */
    public static function assertNotNull(mixed $actual): void {}
    /**
     * @template ExpectedType of object
     * @param class-string<ExpectedType> $expected
     * @phpstan-assert =ExpectedType $actual
     */
    public static function assertInstanceOf(string $expected, mixed $actual): void {}
}
namespace App;
class Entity {}
trait TestHelper {
    public function notNull(?Entity $entity): Entity {
        static::assertNotNull($entity);
        return $entity;
    }
    public function instance(object $entity): Entity {
        static::assertInstanceOf(Entity::class, $entity);
        return $entity;
    }
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn never_returning_call_terminates_flow() {
    let (_, analyzed, _) = analyze(
        "/never-flow.php",
        r#"<?php
class Consent {
    public ?string $acceptedUntil = null;
}
class Handler {
    private function fail(): never {
        throw new RuntimeException();
    }
    public function acceptedUntil(Consent $consent): string {
        if ($consent->acceptedUntil === null) {
            $this->fail();
        }
        return $consent->acceptedUntil;
    }
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn foreach_preserves_explicit_generic_across_concrete_collection_subclasses() {
    let (_, analyzed, _) = analyze(
        "/concrete-collection.php",
        r#"<?php
/**
 * @template TKey
 * @template TValue
 */
interface IteratorAggregate {}
/**
 * @template T
 * @implements IteratorAggregate<array-key, T>
 */
abstract class Collection implements IteratorAggregate {}
class Field {}
class AssociationField extends Field {
    public function getMappingDefinition(): string {}
}
/** @extends Collection<Field> */
class FieldCollection extends Collection {}
class CompiledFieldCollection extends FieldCollection {}
function mappings(CompiledFieldCollection $fields): void {
    /** @var CompiledFieldCollection<AssociationField> $associations */
    $associations = $fields;
    foreach ($associations as $association) {
        $association->getMappingDefinition();
    }
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.undefined"]);
}

#[test]
fn switch_true_narrows_consecutive_instanceof_cases() {
    let linked = analyze_and_link(
        "/switch-true.php",
        r#"<?php
abstract class Field {}
class ParentField extends Field {
    public function getStorageName(): string {}
}
class AssociationField extends Field {
    public function getStorageName(): string {}
}
function storageName(Field $field): string {
    switch (true) {
        case $field instanceof ParentField:
        case $field instanceof AssociationField:
            return $field->getStorageName();
        default:
            return '';
    }
}
"#,
        false,
    );
    for reference in &linked.references {
        if reference.kind == NameKind::Member && &*reference.name == "getStorageName" {
            assert!(!reference.candidate_ids().is_empty());
            assert_eq!(
                reference.receiver.to_string(),
                "AssociationField|ParentField"
            );
        }
    }
}

#[test]
fn exact_class_identity_narrows_object_receiver() {
    let linked = analyze_and_link(
        "/class-identity.php",
        r#"<?php
abstract class Field {}
class JsonField extends Field {
    public function getPropertyMapping(): array {}
}
function mapping(Field $field): array {
    if ($field::class === JsonField::class) {
        return $field->getPropertyMapping();
    }
    return [];
}
"#,
        false,
    );
    assert_resolved(&linked, &["getPropertyMapping"]);
}

#[test]
fn conditional_method_assertion_narrows_receiver() {
    let linked = analyze_and_link(
        "/conditional-assertion.php",
        r#"<?php
abstract class Test {
    /** @phpstan-assert-if-true TestMethod $this */
    public function isTestMethod(): bool { return false; }
}
class TestMethod extends Test {
    public function className(): string { return ''; }
}
function prepare(Test $test): string {
    if (!$test->isTestMethod()) {
        return '';
    }
    return $test->className();
}
"#,
        false,
    );
    for reference in &linked.references {
        if &*reference.name == "className" {
            assert!(!reference.resolved.is_empty());
            assert_eq!(reference.receiver.to_string(), "TestMethod");
        }
    }
}

#[test]
fn conditional_method_assertion_narrows_repeated_receiver_method() {
    let (parsed, analyzed, _) = analyze(
        "/method-result-assertion.php",
        r#"<?php
class ClassReflection {}
interface Scope {
    /** @phpstan-assert-if-true !null $this->getClassReflection() */
    public function isInClass(): bool;
    public function getClassReflection(): ?ClassReflection;
}
function acceptsClass(ClassReflection $class): void {}
function inspect(Scope $scope): void {
    if (!$scope->isInClass()) {
        return;
    }
    acceptsClass($scope->getClassReflection());
}
"#,
        false,
        false,
    );
    assert_text_type(
        &analyzed,
        &parsed.root,
        "$scope->getClassReflection()",
        "ClassReflection",
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn array_destructuring_uses_tuple_element_types() {
    let (_, analyzed, _) = analyze(
        "/array-destructuring.php",
        r#"<?php
/** @return array{0: list<string>, 1: string} */
function splitIds(string $ids): array { return [[], '']; }
/** @param list<string> $ids */
function acceptsIds(array $ids): void {}
function migrate(string $source): void {
    [$ids, $cacheKey] = splitIds($source);
    acceptsIds($ids);
    strlen($cacheKey);
}
"#,
        true,
        true,
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn match_class_arm_narrows_selector_object() {
    let (_, analyzed, _) = analyze(
        "/match-class-arm.php",
        r#"<?php
abstract class Aggregation {}
class FilterAggregation extends Aggregation {}
function restrict(FilterAggregation $aggregation): Aggregation { return $aggregation; }
function sanitize(Aggregation $aggregation): Aggregation {
    return match ($aggregation::class) {
        FilterAggregation::class => restrict($aggregation),
        default => $aggregation,
    };
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.arguments"]);
}

#[test]
fn null_coalescing_array_assignment_preserves_existing_row_shape() {
    let (_, analyzed, _) = analyze(
        "/coalescing-array-assignment.php",
        r#"<?php
/**
 * @param list<array{salesChannelId?: string|null, routeName: string, foreignKey: string, isModified?: bool}> $rows
 */
function firstRoute(array $rows): string {
    $grouped = [];
    foreach ($rows as $row) {
        $id = $row['salesChannelId'] ?? null;
        $row['isModified'] ??= true;
        $grouped[$id][] = $row;
    }
    foreach ($grouped as $group) {
        return $group[0]['routeName'];
    }
    return '';
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn array_filter_arrow_instanceof_narrows_element_type() {
    let linked = analyze_and_link(
        "/array-filter-arrow.php",
        r#"<?php
abstract class Field {}
class AssociationField extends Field {
    public function getReferenceDefinition(): string {}
}
/** @param list<Field> $fields */
function reference(array $fields): string {
    $associations = array_filter(
        $fields,
        static fn (Field $field) => $field instanceof AssociationField,
    );
    $last = $associations[count($associations) - 1];
    return $last->getReferenceDefinition();
}
"#,
        true,
    );
    assert_resolved(&linked, &["getReferenceDefinition"]);
}

#[test]
fn collection_filter_arrow_narrows_generic_element_type() {
    let (_, analyzed, _) = analyze(
        "/collection-filter.php",
        r#"<?php
namespace Shopware\Core\Framework\Struct;
/** @template T */
class Collection {
    /**
     * @param callable(T): bool $predicate
     * @return static
     */
    public function filter(callable $predicate): static {}
}
namespace App;
use Shopware\Core\Framework\Struct\Collection;
abstract class Command {}
class Login extends Command {}
class Register extends Command {}
/**
 * @template T of Command = Command
 * @extends Collection<T>
 */
class CommandCollection extends Collection {
    /** @return self<Login|Register> */
    public function tokenCommands(): self {
        return $this->filter(
            static fn (Command $command): bool =>
                $command instanceof Login || $command instanceof Register,
        );
    }
}
"#,
        true,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn inherited_method_template_does_not_collide_with_child_template() {
    let (_, analyzed, _) = analyze(
        "/method-template-shadow.php",
        r#"<?php
/** @template TElement */
class Collection {
    /**
     * @template T
     * @param callable(TElement): T $callback
     * @return array<array-key, T>
     */
    public function map(callable $callback): array {}
}

abstract class Command {
    public static function name(): string {}
}
/**
 * @template T of Command = Command
 * @extends Collection<T>
 */
class Commands extends Collection {
    /** @return array<string> */
    public function names(): array {
        return $this->map(static fn (Command $command): string => $command::name());
    }
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn member_call_on_union_generic_collections_returns_union_elements() {
    let (parsed, analyzed, _) = analyze(
        "/union-collections.php",
        r#"<?php
class A {}
class B extends A {}
/**
 * @template T
 * @template TKey of array-key = array-key
 */
class Items {
	/**
	 * @param TKey $key
	 * @return T|null
	 */
    public function get(mixed $key): mixed {}
}
/**
 * @template T
 * @extends Items<T, string>
 */
class GenericItems extends Items {}
/** @extends GenericItems<A> */
class AItems extends GenericItems {}
/** @param GenericItems<B> $b
 * @param list<string> $ids
 * @return array<string, A|B>
 */
function find(bool $flag, AItems $a, Items $b, array $ids): array {
    if ($flag) {
        $items = $a;
    } else {
        $items = $b;
    }
    $result = [];
    foreach ($ids as $id) {
        if ($items->get($id) === null) {
            continue;
        }
        $result[$id] = $items->get($id);
    }
    return $result;
}
"#,
        false,
        false,
    );
    assert_node_type(&analyzed, &parsed.root, PHP_MEMBER_CALL, "A|B");
    assert_no_issue(&analyzed, &["php.returnType"]);
}

#[test]
fn deep_generic_builder_returns_to_parent() {
    let source = format!(
        "{}{}{}{}",
        r#"<?php
interface NodeParentInterface {}
/** @template-covariant TParent of NodeParentInterface|null = null */
abstract class NodeDefinition implements NodeParentInterface {
    /** @return TParent */
    public function end(): ?NodeParentInterface {}
}
/**
 * @template TParent of NodeParentInterface|null = null
 * @extends NodeDefinition<TParent>
 */
class ScalarNodeDefinition extends NodeDefinition {}
/**
 * @template TParent of NodeParentInterface|null = null
 * @extends NodeDefinition<TParent>
 */
class ArrayNodeDefinition extends NodeDefinition {
    /** @return NodeBuilder<static> */
    public function children(): NodeBuilder {}
}
/** @template TParent of NodeParentInterface|null = null */
class NodeBuilder implements NodeParentInterface {
    /** @return ArrayNodeDefinition<$this> */
    public function arrayNode(string $name): ArrayNodeDefinition {}
    /** @return ScalarNodeDefinition<$this> */
    public function scalarNode(string $name): ScalarNodeDefinition {}
    /** @return TParent */
    public function end(): ?NodeParentInterface {}
}
function build(NodeBuilder $root): void {
	$root
"#,
        "        ->arrayNode('nested')->children()\n".repeat(40),
        "        ->end()->end()\n".repeat(40),
        r#"
        ->scalarNode('sibling');
}
"#,
    );
    let linked = analyze_and_link("/deep-builder.php", &source, false);
    let target = source.rfind("scalarNode").unwrap() as u32;
    let mut sibling_resolved = false;
    let mut sibling_receiver = Type::unknown();
    for reference in &linked.references {
        if &*reference.name == "scalarNode" && u32::from(reference.range.start()) == target {
            sibling_resolved =
                !reference.resolved.is_empty() || !reference.candidate_ids().is_empty();
            sibling_receiver = reference.receiver.clone();
        }
    }
    assert!(sibling_resolved, "{sibling_receiver}");
}

#[test]
fn logical_right_assignment_preserves_call_arguments() {
    let (_, analyzed, _) = analyze(
        "/logical-assignment.php",
        r#"<?php
class Context {}
class Media {
    public function getHash(): ?string {}
}
class Service {
    private function find(string $hash, Context $context): ?string {}

    public function upload(Media $media, Context $context): string {
        if ($media->getHash() && $existing = $this->find($media->getHash(), $context)) {
            return $existing;
        }

        return '';
    }
}
"#,
        false,
        false,
    );
    assert_no_issue(&analyzed, &["php.arguments", "php.undefinedVariable"]);
}

#[test]
fn long_logical_chain_does_not_reanalyze_prefixes() {
    let source = format!(
        "{}{}{}",
        r#"<?php
function allTruthy(?string $value): bool {
    return "#,
        "$value && ".repeat(24),
        r#"$value;
}
"#,
    );
    let (_, analyzed, _) = analyze("/logical-chain.php", &source, false, false);
    assert!(
        analyzed.issues.is_empty(),
        "{:?}",
        super::all_messages(&analyzed)
    );
}

#[test]
fn generic_constructor_uses_class_template_default() {
    let parsed = parse(
        r#"<?php
/**
 * @template T of string|array<string, string> = string
 */
class Criteria {
    /** @param array<T>|null $ids */
    public function __construct(?array $ids = null) {}
}
class Defaults {
    public const string CURRENCY = 'currency';
}
function criteria(): Criteria {
    return new Criteria([Defaults::CURRENCY]);
}
function compoundCriteria(): Criteria {
    return new Criteria([['id' => 'first'], 'second']);
}
"#,
    );
    let bound = bind("/generic-constructor.php", &parsed);
    let templates: HashMap<&str, Type> = HashMap::from([("T", Type::string())]);
    let constructor = symbol_named(&bound, "__construct");
    assert_eq!(
        substitute(&constructor.parameters[0].ty, &templates).to_string(),
        "array<array-key,string>|null"
    );
    let snapshot = snapshot(std::slice::from_ref(&bound));
    let linked = link(&bound, &snapshot);
    let linked_constructor = symbol_named(&linked, "__construct");
    assert_eq!(
        substitute(&linked_constructor.parameters[0].ty, &templates).to_string(),
        "array<array-key,string>|null"
    );
    let analyzed = super::analyzer(&snapshot, Vec::new()).analyze(&linked, &parsed.root);
    assert_no_issue(&analyzed, &["php.arguments"]);
}
