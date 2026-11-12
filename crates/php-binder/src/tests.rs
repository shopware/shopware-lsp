use std::collections::BTreeMap;

use swlsp_php_semantic::{
    AttributeValueKind, ConstantReturn, Document, Flags, NameKind, Snapshot, Str, Symbol,
    SymbolKind, TraitAlias, Visibility,
};
use swlsp_php_types::{Kind as TypeKind, ShapeField, Type, phpdoc_alias};
use swlsp_syntax::TextRange;

use crate::capacity::*;
use crate::declaration_references::non_class_native_type_name;
use crate::{Binder, effective_type, link};

fn bind(path: &str, source: &str) -> Document {
    let parse = swlsp_parser_php::parse(source);
    Binder::new().bind(path, 1, &parse.tree)
}

fn find<'a>(document: &'a Document, kind: SymbolKind, name: &str) -> &'a Symbol {
    document
        .symbols
        .iter()
        .find(|symbol| symbol.kind == kind && &*symbol.name == name)
        .unwrap_or_else(|| panic!("missing {kind:?} symbol {name:?}"))
}

fn slice(source: &str, range: TextRange) -> &str {
    &source[range]
}

fn strs(values: &[Str]) -> Vec<&str> {
    values.iter().map(|value| value.as_str()).collect()
}

#[test]
fn estimated_symbol_capacity_is_bounded() {
    assert_eq!(estimated_symbol_capacity(0), 0);
    assert_eq!(estimated_symbol_capacity(1), 1);
    assert_eq!(estimated_symbol_capacity(1024), 1);
    assert_eq!(estimated_symbol_capacity(u32::MAX), 512);
}

#[test]
fn effective_type_preserves_documented_object_narrowing() {
    let value = effective_type(
        &Type::named("AbstractCommand", vec![]),
        &Type::named("ConcreteCommand", vec![]),
    );
    assert_eq!(value.to_string(), "ConcreteCommand");
}

#[test]
fn effective_type_preserves_native_nullability() {
    let value = effective_type(
        &Type::nullable(Type::array(Type::array_key(), Type::mixed())),
        &Type::array(Type::string(), Type::string()),
    );
    assert_eq!(value.to_string(), "array<string,string>|null");
}

#[test]
fn effective_type_preserves_documented_static_object_narrowing() {
    let value = effective_type(
        &Type::named("MessageInterface", vec![]),
        &Type::static_type(vec![]),
    );
    assert_eq!(value.to_string(), "static");
}

#[test]
fn binder_finds_conditional_polyfill_class() {
    let document = bind(
        "/polyfill.php",
        "<?php\nif (PHP_VERSION_ID < 80300) {\n    final class Override {}\n}\n",
    );
    assert_eq!(
        &*find(&document, SymbolKind::Class, "Override").fully_qualified,
        "Override"
    );
}

#[test]
fn binder_scopes_anonymous_class_methods() {
    let document = bind(
        "/anonymous.php",
        r#"<?php
function listen(object $dispatcher): object {
    $prefix = 'event';
    return new class($prefix) {
        public function __construct(private string $prefix) {}
        public function handle(object $event): string {
            return $this->prefix . $event::class;
        }
    };
}
"#,
    );
    let anonymous = document
        .symbols
        .iter()
        .find(|symbol| symbol.kind == SymbolKind::Class && symbol.flags.has(Flags::SYNTHETIC))
        .expect("anonymous class");
    assert!(
        anonymous
            .fully_qualified
            .contains("{anonymous@/anonymous.php:")
    );
    let handle = find(&document, SymbolKind::Method, "handle");
    assert_eq!(handle.container, anonymous.id);
    for reference in &document.references {
        if reference.kind == NameKind::Variable && &*reference.name == "$event" {
            assert_ne!(reference.scope, document.scopes[1].id);
        }
    }
}

#[test]
fn binder_declares_top_level_foreach_locals() {
    let document = bind(
        "/top-level.php",
        "<?php\n$items = ['one'];\nforeach ($items as $item) {\n    echo $item;\n}\n",
    );
    assert!(document.scopes[0].has_symbol(&document.symbols, "$item"));
    for reference in &document.references {
        if reference.kind == NameKind::Variable && &*reference.name == "$item" {
            assert_eq!(reference.scope, 0);
        }
    }
}

#[test]
fn non_class_native_type_name_matches_builtin_type_vocabulary() {
    let builtins = [
        "never",
        "mixed",
        "void",
        "null",
        "bool",
        "boolean",
        "true",
        "false",
        "int",
        "integer",
        "positive-int",
        "negative-int",
        "non-negative-int",
        "float",
        "double",
        "string",
        "non-empty-string",
        "numeric-string",
        "literal-string",
        "object",
        "resource",
        "array-key",
        "array",
        "non-empty-array",
        "list",
        "non-empty-list",
        "iterable",
        "callable",
        "class-string",
        "interface-string",
        "trait-string",
        "enum-string",
    ];
    for name in builtins {
        assert!(swlsp_php_types::parse_native(name).is_ok(), "{name}");
        assert!(non_class_native_type_name(name), "{name}");
        assert!(non_class_native_type_name(&name.to_uppercase()), "{name}");
        assert!(non_class_native_type_name(&format!("\\{name}")), "{name}");
    }
    for name in [
        // Ordinary class names, although the type parser reads them as types.
        "Closure",
        "Error",
        "Number",
        "Unknown",
        "App\\Product",
        "Product",
        "self",
        "static",
        "parent",
        "$this",
    ] {
        assert!(!non_class_native_type_name(name), "{name}");
    }
}

#[test]
fn estimated_document_symbol_capacity_covers_structural_declarations() {
    let source = r#"<?php
class Product {
    public const FIRST = 1, SECOND = 2;
    private string $name, $label;
    public function __construct(public string $id, int $count) {
        $callback = static fn (string $value): string => $value;
    }
}
"#;
    let parse = swlsp_parser_php::parse(source);
    let document = Binder::new().bind("/capacity.php", 1, &parse.tree);
    assert_eq!(structural_symbol_count(&parse.root()), 12);
    assert!(estimated_document_symbol_capacity(&parse.root()) >= document.symbols.len() - 1);
}

#[test]
fn estimated_document_scope_capacity_covers_structural_scopes() {
    let source = r#"<?php
namespace App;
class Product {
    public function map(): void {
        $callback = static fn (string $value): string => $value;
    }
}
"#;
    let parse = swlsp_parser_php::parse(source);
    let document = Binder::new().bind("/capacity.php", 1, &parse.tree);
    assert_eq!(
        document.scopes.len(),
        estimated_document_capacities(&parse.root()).scopes
    );
    assert_eq!(document.scopes.len(), document.scopes.capacity());
}

#[test]
fn structural_symbol_count_uses_exact_foreach_locals() {
    for (foreach, expected) in [
        ("foreach ($items as $value) {}", 3),
        ("foreach ($items as $key => $value) {}", 4),
        ("foreach ($items as [$first, $second]) {}", 4),
    ] {
        let source = format!("<?php function walk($items) {{ {foreach} }}");
        let parse = swlsp_parser_php::parse(&source);
        let document = Binder::new().bind("/foreach.php", 1, &parse.tree);
        assert_eq!(
            structural_symbol_count(&parse.root()),
            expected,
            "{foreach}"
        );
        assert_eq!(document.symbols.len(), expected, "{foreach}");
    }
}

#[test]
fn estimated_type_fact_capacity_is_bounded() {
    assert_eq!(estimated_type_fact_capacity(39), 0);
    assert_eq!(estimated_type_fact_capacity(40), 1);
    assert_eq!(estimated_type_fact_capacity(u32::MAX), 4096);
    assert_eq!(estimated_non_literal_type_fact_capacity(39, 0), 0);
    assert_eq!(estimated_non_literal_type_fact_capacity(120, 0), 3);
    assert_eq!(estimated_non_literal_type_fact_capacity(120, 3), 2);
    assert_eq!(
        estimated_non_literal_type_fact_capacity(u32::MAX, 4096),
        1366
    );
}

#[test]
fn estimated_reference_capacity_is_bounded() {
    let huge = u32::MAX as usize;
    assert_eq!(estimated_reference_capacity(0), 0);
    assert_eq!(estimated_reference_capacity(192), 144);
    assert_eq!(estimated_reference_capacity(huge), 4096);
    assert_eq!(estimated_linked_reference_capacity(192, 10), 154);
    assert_eq!(estimated_linked_reference_capacity(huge, 10), 4096);
    assert_eq!(estimated_document_reference_capacity(192, 10), 139);
    assert_eq!(estimated_document_reference_capacity(huge, 10), 4096);
}

#[test]
fn estimated_scope_capacity_is_bounded() {
    assert_eq!(estimated_scope_capacity(0), 1);
    assert_eq!(estimated_scope_capacity(10), 11);
    assert_eq!(estimated_scope_capacity(u32::MAX as usize), 4096);
}

#[test]
fn bind_rich_declarations_and_references() {
    let source = r#"<?php
namespace App\Domain;

use Vendor\Contracts\BaseEntity;
use Vendor\Service as ImportedService;
use function Vendor\build;

#[Entity]
final class Product extends BaseEntity implements \JsonSerializable
{
    use TracksChanges;

    public const KIND = 'product';
    private readonly ImportedService $service;

    public function __construct(
        public readonly string $id,
        ImportedService $service = null,
    ) {
        $this->service = $service;
    }

    public function load(): ?Result
    {
        $local = new Result();
        return build($local);
    }

    public function absolute(): \Vendor\Result
    {
        return new \Vendor\Result();
    }
}

function helper(Product $product): Product
{
    return $product;
}"#;
    let parse = swlsp_parser_php::parse(source);
    let document = Binder::new().bind("/project/Product.php", 7, &parse.tree);
    assert_eq!(&*document.namespace, "App\\Domain");
    assert!(!document.scopes.is_empty());
    let class = find(&document, SymbolKind::Class, "Product");
    assert_eq!(strs(class.extends()), ["Vendor\\Contracts\\BaseEntity"]);
    assert_eq!(strs(class.implements()), ["JsonSerializable"]);
    assert_eq!(strs(class.traits()), ["App\\Domain\\TracksChanges"]);
    assert!(class.flags.has(Flags::FINAL));
    let constructor = find(&document, SymbolKind::Method, "__construct");
    assert_eq!(constructor.parameters.len(), 2);
    assert_eq!(constructor.parameters[0].ty.to_string(), "string");
    assert_eq!(constructor.parameters[1].ty.to_string(), "Vendor\\Service");
    assert!(constructor.parameters[0].flags.has(Flags::READONLY));
    assert_eq!(
        find(&document, SymbolKind::Method, "load")
            .return_type
            .to_string(),
        "App\\Domain\\Result|null"
    );
    assert_eq!(
        find(&document, SymbolKind::Method, "absolute")
            .return_type
            .to_string(),
        "Vendor\\Result"
    );
    assert_eq!(constructor.return_type.to_string(), "void");
    find(&document, SymbolKind::Property, "service");
    assert!(
        find(&document, SymbolKind::Property, "id")
            .flags
            .has(Flags::PROMOTED)
    );
    assert_eq!(
        find(&document, SymbolKind::ClassConstant, "KIND")
            .ty
            .to_string(),
        "\"product\""
    );
    find(&document, SymbolKind::Function, "helper");
    find(&document, SymbolKind::Local, "$local");
    assert!(!document.references.is_empty());
}

fn class_reference_names(document: &Document) -> Vec<String> {
    document
        .references
        .iter()
        .filter(|reference| reference.kind == NameKind::Class)
        .flat_map(|reference| reference.qualified_names().iter().map(ToString::to_string))
        .collect()
}

#[test]
fn bind_does_not_record_broad_object_as_class_reference() {
    let document = bind(
        "/project/ObjectTypes.php",
        "<?php\nnamespace App;\nfunction transform(object $input, Product $product): object {}\n",
    );
    let names = class_reference_names(&document);
    assert!(names.contains(&"App\\Product".to_owned()));
    assert!(!names.contains(&"App\\object".to_owned()));
}

#[test]
fn bind_does_not_treat_dynamic_class_constant_name_as_class_reference() {
    let document = bind(
        "/project/DynamicClass.php",
        "<?php\nnamespace App;\nfunction className(object $value): string {\n    return $value::class;\n}\n",
    );
    assert!(!class_reference_names(&document).contains(&"App\\class".to_owned()));
}

#[test]
fn bind_untyped_parameter_as_mixed() {
    let document = bind(
        "/project/Untyped.php",
        "<?php\nfunction consume($value): void {}\n",
    );
    let function = find(&document, SymbolKind::Function, "consume");
    assert_eq!(function.parameters.len(), 1);
    assert_eq!(function.parameters[0].ty.to_string(), "mixed");
}

#[test]
fn bind_records_each_native_class_type_reference_once() {
    let document = bind(
        "/project/NativeTypes.php",
        r#"<?php
function transform(
    Simple $simple,
    Left&Right $intersection,
    (GroupedOne&GroupedTwo)|null $grouped,
): Result|false {}
"#,
    );
    let mut counts = BTreeMap::new();
    for name in class_reference_names(&document) {
        *counts.entry(name).or_insert(0) += 1;
    }
    let expected: BTreeMap<String, i32> = [
        "GroupedOne",
        "GroupedTwo",
        "Left",
        "Result",
        "Right",
        "Simple",
    ]
    .iter()
    .map(|name| (name.to_string(), 1))
    .collect();
    assert_eq!(counts, expected);
}

#[test]
fn bind_uses_exact_parameter_storage() {
    let document = bind(
        "/project/Parameters.php",
        r#"<?php
/** @method void virtual(int $first, string $second, bool $third) */
class Parameters {
    public function actual(int $first, string $second, bool $third): void {
        $closure = function (int $first, string $second, bool $third): void {};
    }
}
"#,
    );
    for symbol in [
        find(&document, SymbolKind::Method, "virtual"),
        find(&document, SymbolKind::Method, "actual"),
        find(&document, SymbolKind::Closure, "{closure}"),
    ] {
        assert_eq!(symbol.parameters.len(), 3, "{}", symbol.name);
        assert_eq!(
            symbol.parameters.len(),
            symbol.parameters.capacity(),
            "{}",
            symbol.name
        );
    }
}

#[test]
fn bind_shares_complete_namespace_imports_across_scopes() {
    let document = bind(
        "/project/Imports.php",
        r#"<?php
namespace App;

class BeforeImport {}

use Vendor\Service;
use function Vendor\run;
use const Vendor\VERSION;

function afterImport(Service $service): void {}
"#,
    );
    let mut namespace_scopes = 0;
    for scope in &document.scopes {
        if &*scope.namespace != "App" {
            continue;
        }
        namespace_scopes += 1;
        assert_eq!(
            scope.imports.classes.get("service").map(|v| v.as_str()),
            Some("Vendor\\Service"),
            "scope {}",
            scope.id
        );
        assert_eq!(
            scope.imports.functions.get("run").map(|v| v.as_str()),
            Some("Vendor\\run")
        );
        assert_eq!(
            scope.imports.constants.get("VERSION").map(|v| v.as_str()),
            Some("Vendor\\VERSION")
        );
    }
    assert!(namespace_scopes >= 3);
}

#[test]
fn bind_keeps_sibling_namespace_import_tables_isolated() {
    let document = bind(
        "/project/Imports.php",
        r#"<?php
namespace App {
    use Vendor\First;
    class One {}
}
namespace App {
    class Two {}
}
"#,
    );
    let one = find(&document, SymbolKind::Class, "One").id.clone();
    let two = find(&document, SymbolKind::Class, "Two").id.clone();
    for scope in &document.scopes {
        if scope.owner == one {
            assert_eq!(
                scope.imports.classes.get("first").map(|v| v.as_str()),
                Some("Vendor\\First")
            );
        } else if scope.owner == two {
            assert!(scope.imports.classes.is_empty());
        }
    }
}

#[test]
fn bind_phpdoc_assistant_tags_on_parameters() {
    let document = bind(
        "/project/Assistant.php",
        r#"<?php
class Assistant
{
    /**
     * @param string $route #Route #Service
     */
    public function open(string $route): void {}
}
"#,
    );
    let method = find(&document, SymbolKind::Method, "open");
    assert_eq!(method.parameters.len(), 1);
    assert_eq!(
        strs(&method.parameters[0].assistant_tags),
        ["Route", "Service"]
    );
}

#[test]
fn bind_allocates_type_aliases_only_when_declared() {
    let without = bind("/project/Plain.php", "<?php class Plain {}");
    assert!(without.type_aliases.is_empty());
    let with = bind(
        "/project/Aliased.php",
        "<?php\n/**\n * @phpstan-type EntityMap array<string, int>\n */\nclass Aliased {}\n",
    );
    assert_eq!(
        with.type_aliases["EntityMap"].to_string(),
        "array<string,int>"
    );
    let alias = find(&with, SymbolKind::TypeAlias, "EntityMap");
    assert_eq!(alias.ty.to_string(), "array<string,int>");
    assert!(alias.flags.has(Flags::SYNTHETIC));
}

#[test]
fn bind_imported_type_alias_uses_declaring_class_identity() {
    let document = bind(
        "/project/Consumer.php",
        r#"<?php
namespace App;
use Vendor\Definition;
/**
 * @phpstan-import-type Payload from Definition
 */
class Consumer {
    /** @param Payload $payload */
    public function consume(array $payload): void {}
}
"#,
    );
    let alias = phpdoc_alias("Vendor\\Definition", "Payload");
    assert_eq!(document.type_aliases["Payload"], alias);
    assert_eq!(
        find(&document, SymbolKind::Method, "consume").parameters[0].doc_type,
        alias
    );
}

#[test]
fn indexed_type_alias_expands_for_compatibility() {
    let document = bind(
        "/project/Definition.php",
        "<?php\nnamespace Vendor;\n/**\n * @phpstan-type Payload array{id: string}\n */\nclass Definition {}\n",
    );
    let snapshot = Snapshot::new(1, &[document]);
    let actual = Type::array_shape(vec![ShapeField::new("id", Type::string())], false);
    assert!(
        snapshot
            .relations()
            .is_assignable_to(&actual, &phpdoc_alias("Vendor\\Definition", "Payload"))
    );
}

#[test]
fn link_resolves_cross_file_and_local_references() {
    let base = bind(
        "/base.php",
        "<?php\nnamespace App;\nclass Target {}\nfunction build(Target $target): Target { return $target; }\n",
    );
    let consumer = bind(
        "/consumer.php",
        "<?php\nnamespace App;\nfunction consume(): Target {\n    $value = new Target();\n    return build($value);\n}\n",
    );
    let snapshot = Snapshot::new(1, &[base, consumer.clone()]);
    let linked = link(&consumer, &snapshot);
    let mut resolved = 0;
    for reference in &linked.references {
        if !reference.resolved.is_empty() {
            resolved += 1;
            assert!(reference.candidate_ids().is_empty());
        }
    }
    assert!(resolved >= 3);
}

#[test]
fn bind_enum_runtime_members() {
    let document = bind(
        "/status.php",
        "<?php\nenum Status: string {\n    case Active = 'active';\n}\n",
    );
    let status = find(&document, SymbolKind::Enum, "Status");
    assert!(strs(status.implements()).contains(&"UnitEnum"));
    assert!(strs(status.implements()).contains(&"BackedEnum"));
    let cases = find(&document, SymbolKind::Method, "cases");
    assert!(cases.flags.has(Flags::STATIC));
    assert!(cases.flags.has(Flags::SYNTHETIC));
    assert_eq!(cases.return_type.to_string(), "list<Status>");
    let from = find(&document, SymbolKind::Method, "from");
    assert!(from.flags.has(Flags::STATIC));
    assert_eq!(from.return_type.to_string(), "Status");
    assert_eq!(from.parameters[0].ty.to_string(), "string");
    assert_eq!(
        find(&document, SymbolKind::Method, "tryFrom")
            .return_type
            .to_string(),
        "Status|null"
    );
    assert_eq!(
        find(&document, SymbolKind::Property, "name").ty.to_string(),
        "string"
    );
    assert_eq!(
        find(&document, SymbolKind::Property, "value")
            .ty
            .to_string(),
        "string"
    );
}

#[test]
fn bind_phpdoc_generics_and_synthetic_members() {
    let document = bind(
        "/repository.php",
        r#"<?php
namespace App;
/**
 * @template TEntity of Entity
 * @extends Repository<TEntity>
 * @property-read list<TEntity> $entities
 * @method TEntity|null find(string $id)
 */
class ProductRepository extends Repository {
    /** @return list<TEntity> */
    public function all(): array {}

    /**
     * @template Item of Entity
     * @param Item $entity
     * @return Item
     */
    public function identity($entity) {}
}
"#,
    );
    let class = find(&document, SymbolKind::Class, "ProductRepository");
    assert_eq!(class.templates().len(), 1);
    assert_eq!(class.templates()[0].bound.to_string(), "App\\Entity");
    assert_eq!(
        class.extends_types()[0].to_string(),
        "App\\Repository<TEntity>"
    );
    let entities = find(&document, SymbolKind::Property, "entities");
    assert_eq!(entities.ty.to_string(), "list<TEntity>");
    assert!(entities.flags.has(Flags::SYNTHETIC));
    let found = find(&document, SymbolKind::Method, "find");
    assert_eq!(found.return_type.to_string(), "TEntity|null");
    assert!(found.flags.has(Flags::SYNTHETIC));
    assert_eq!(
        find(&document, SymbolKind::Method, "all")
            .return_type
            .to_string(),
        "list<TEntity>"
    );
    let identity = find(&document, SymbolKind::Method, "identity");
    assert_eq!(identity.templates().len(), 1);
    assert_eq!(&*identity.templates()[0].name, "Item");
    assert_eq!(identity.templates()[0].bound.to_string(), "App\\Entity");
    assert_eq!(identity.parameters[0].ty.to_string(), "Item");
    assert_eq!(identity.return_type.to_string(), "Item");
}

#[test]
fn bind_typed_constants_and_asymmetric_properties() {
    let document = bind(
        "/modern.php",
        "<?php\nnamespace App;\nclass Modern {\n    public const string KIND = 'modern';\n    public private(set) string $name {\n        get => $this->name;\n    }\n}",
    );
    // The literal is kept: a route name written as `self::KIND` resolves.
    assert_eq!(
        find(&document, SymbolKind::ClassConstant, "KIND")
            .ty
            .to_string(),
        "\"modern\""
    );
    let property = find(&document, SymbolKind::Property, "name");
    assert_eq!(property.visibility, Visibility::Public);
    assert!(property.has_write_visibility);
    assert_eq!(property.write_visibility, Visibility::Private);
    assert_eq!(property.ty.to_string(), "string");
}

#[test]
fn bind_infers_untyped_constant_literal_types() {
    let document = bind(
        "/constants.php",
        "<?php\nnamespace App;\nclass ExitCode {\n    public const SUCCESS = 0, FAILURE = -1;\n    public const HEX_MASK = 0xDEAD;\n    public const LABEL = 'done';\n    public const ENABLED = true;\n}",
    );
    for (name, expected) in [
        ("SUCCESS", "0"),
        ("FAILURE", "-1"),
        ("HEX_MASK", "0xDEAD"),
        ("LABEL", "\"done\""),
        ("ENABLED", "true"),
    ] {
        assert_eq!(
            find(&document, SymbolKind::ClassConstant, name)
                .ty
                .to_string(),
            expected
        );
    }
}

#[test]
fn bind_preserves_typed_constant_array_entries() {
    let source = r#"<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
        'headers' => [],
        'verify_peer' => true,
        'max_redirects' => 20,
        'bindto' => '0',
    ];
}"#;
    let document = bind("/HttpClientInterface.php", source);
    let constant = find(&document, SymbolKind::ClassConstant, "OPTIONS_DEFAULTS");
    let items = constant.constant_array();
    assert_eq!(items.len(), 5);
    assert_eq!(&*items[0].key, "timeout");
    assert_eq!(items[0].ty.to_string(), "null");
    assert_eq!(&*items[0].value, "null");
    assert_eq!(slice(source, items[0].key_range), "'timeout'");
    assert_eq!(items[1].ty.to_string(), "array");
    assert_eq!(items[2].ty.to_string(), "bool");
    assert_eq!(items[3].ty.to_string(), "int");
    assert_eq!(items[4].ty.to_string(), "string");
}

#[test]
fn bind_preserves_direct_literal_method_returns() {
    let source = r#"<?php
namespace App;
class Helper {
    public function getName(bool $debug): string {
        $nested = static function (): string {
            return 'nested';
        };
        if ($debug) {
            return 'debug_formatter';
        }
        return 'formatter';
    }
}"#;
    let document = bind("/Helper.php", source);
    let method = find(&document, SymbolKind::Method, "getName");
    let literals = method.literal_returns();
    assert_eq!(literals.len(), 2);
    assert_eq!(&*literals[0].value, "debug_formatter");
    assert_eq!(literals[0].ty.to_string(), "\"debug_formatter\"");
    assert_eq!(&*literals[1].value, "formatter");
    for literal in literals {
        assert_ne!(&*literal.value, "nested");
        assert_eq!(slice(source, literal.range), format!("'{}'", literal.value));
    }
}

#[test]
fn bind_preserves_class_constant_method_returns() {
    let source = r#"<?php
namespace App;
use Shared\TypeNames;
class Helper {
    public function getName(bool $nested): string {
        $callback = static function (): string {
            return self::NESTED;
        };
        if ($nested) {
            return self::LOCAL;
        }
        return TypeNames::SHARED;
    }
}"#;
    let document = bind("/Helper.php", source);
    let method = find(&document, SymbolKind::Method, "getName");
    let returns = method.constant_returns();
    assert_eq!(
        returns,
        [
            ConstantReturn {
                receiver: Str::from("self"),
                name: Str::from("LOCAL"),
                range: returns[0].range,
            },
            ConstantReturn {
                receiver: Str::from("Shared\\TypeNames"),
                name: Str::from("SHARED"),
                range: returns[1].range,
            },
        ]
    );
    for returned in returns {
        assert!(!slice(source, returned.range).contains("NESTED"));
    }
}

#[test]
fn bind_deprecated_phpdoc_and_attributes() {
    let document = bind(
        "/legacy.php",
        r#"<?php
namespace App;
class Legacy {
    /** @deprecated */
    public string $documented;

    #[\Deprecated(message: 'Use active instead')]
    public string $attributed;

    /** @deprecated */
    public function documentedMethod(): void {}

    #[\Deprecated(message: 'Use active instead')]
    public function attributedMethod(): void {}

    #[\Deprecated]
    public const LEGACY = 'legacy';

    public function __construct(
        #[\Deprecated]
        public string $promoted = '',
    ) {}
}"#,
    );
    for name in ["documented", "attributed", "promoted"] {
        assert!(
            find(&document, SymbolKind::Property, name)
                .flags
                .has(Flags::DEPRECATED),
            "{name}"
        );
    }
    for name in ["documentedMethod", "attributedMethod"] {
        assert!(
            find(&document, SymbolKind::Method, name)
                .flags
                .has(Flags::DEPRECATED),
            "{name}"
        );
    }
    assert!(
        find(&document, SymbolKind::ClassConstant, "LEGACY")
            .flags
            .has(Flags::DEPRECATED)
    );
}

#[test]
fn bind_phpdoc_final_separately_from_native_final() {
    let document = bind(
        "/classes.php",
        "<?php\nnamespace App;\n/** @final */\nclass ExtensionPoint {}\nfinal class ClosedClass {}\n",
    );
    let extension_point = find(&document, SymbolKind::Class, "ExtensionPoint");
    assert!(extension_point.flags.has(Flags::SOFT_FINAL));
    assert!(!extension_point.flags.has(Flags::FINAL));
    let closed = find(&document, SymbolKind::Class, "ClosedClass");
    assert!(closed.flags.has(Flags::FINAL));
    assert!(!closed.flags.has(Flags::SOFT_FINAL));
}

#[test]
fn bind_constant_attribute_arguments_on_symbols_and_parameters() {
    let document = bind(
        "/attributes.php",
        r#"<?php
namespace App;
use JetBrains\PhpStorm\ExpectedValues;

final class Demo {
    #[\JetBrains\PhpStorm\ArrayShape([
        'id' => 'positive-int',
        'name' => 'string',
    ])]
    #[\JetBrains\PhpStorm\Deprecated(
        reason: 'Legacy API',
        replacement: 'readNew()',
        since: '2.0',
    )]
    public function read(
        #[ExpectedValues(values: [Mode::ACTIVE, 'all', 7], flags: [FLAG_A | FLAG_B])]
        string $mode,
    ): array {}
}
"#,
    );
    let method = find(&document, SymbolKind::Method, "read");
    assert_eq!(method.attributes().len(), 2);
    assert_eq!(method.return_type.kind(), TypeKind::ArrayShape);
    assert_eq!(method.return_type.field(0).unwrap().ty.to_string(), "int");
    assert_eq!(
        method.return_type.field(1).unwrap().ty.to_string(),
        "string"
    );

    let shape = &method.attributes()[0];
    assert_eq!(&*shape.name, "JetBrains\\PhpStorm\\ArrayShape");
    assert_eq!(shape.arguments.len(), 1);
    assert_eq!(shape.arguments[0].value.kind, AttributeValueKind::Array);
    assert_eq!(shape.arguments[0].value.items.len(), 2);
    assert!(shape.arguments[0].value.items[0].has_key);
    assert_eq!(&*shape.arguments[0].value.items[0].key.value, "id");
    assert_eq!(
        &*shape.arguments[0].value.items[0].value.value,
        "positive-int"
    );

    let deprecated = &method.attributes()[1];
    assert_eq!(&*deprecated.name, "JetBrains\\PhpStorm\\Deprecated");
    let names: Vec<&str> = deprecated
        .arguments
        .iter()
        .map(|a| a.name.as_str())
        .collect();
    assert_eq!(names, ["reason", "replacement", "since"]);
    assert_eq!(&*deprecated.arguments[0].value.value, "Legacy API");

    assert_eq!(method.parameters.len(), 1);
    assert_eq!(method.parameters[0].attributes.len(), 1);
    let expected = &method.parameters[0].attributes[0];
    assert_eq!(&*expected.name, "JetBrains\\PhpStorm\\ExpectedValues");
    assert_eq!(expected.arguments.len(), 2);
    assert_eq!(&*expected.arguments[0].name, "values");
    assert_eq!(expected.arguments[0].value.items.len(), 3);
    assert_eq!(
        &*expected.arguments[0].value.items[0].value.value,
        "App\\Mode::ACTIVE"
    );
    assert_eq!(
        expected.arguments[1].value.items[0].value.kind,
        AttributeValueKind::Expression
    );
    assert_eq!(
        &*expected.arguments[1].value.items[0].value.expression,
        "FLAG_A | FLAG_B"
    );
    let parameter = find(&document, SymbolKind::Parameter, "$mode");
    assert_eq!(&parameter.attributes()[0], expected);
}

#[test]
fn bind_shape_and_no_return_attribute_types() {
    let document = bind(
        "/attribute-types.php",
        r#"<?php
namespace App;
use JetBrains\PhpStorm\{ArrayShape, NoReturn, ObjectShape};

#[NoReturn]
function stop(): void {}

#[NoReturn(1)]
function stopConditionally(int $mode): void {}

#[ObjectShape(['id' => 'positive-int', 'owner' => User::class])]
function record(): object {}

function accept(
    #[ArrayShape(['id' => 'positive-int', 'label?' => 'string'])]
    array $input,
): void {}
"#,
    );
    assert_eq!(
        find(&document, SymbolKind::Function, "stop")
            .return_type
            .kind(),
        TypeKind::Never
    );
    assert_eq!(
        find(&document, SymbolKind::Function, "stopConditionally")
            .return_type
            .kind(),
        TypeKind::Void
    );
    let record = find(&document, SymbolKind::Function, "record");
    assert_eq!(record.return_type.kind(), TypeKind::ObjectShape);
    // Invalid non-string shape values degrade only that field to omission.
    assert_eq!(record.return_type.field_count(), 1);
    assert_eq!(record.return_type.field(0).unwrap().ty.to_string(), "int");
    let accept = find(&document, SymbolKind::Function, "accept");
    assert_eq!(accept.parameters.len(), 1);
    assert_eq!(accept.parameters[0].ty.kind(), TypeKind::ArrayShape);
    assert!(accept.parameters[0].ty.field(1).unwrap().optional);
    assert_eq!(
        accept.parameters[0].ty,
        find(&document, SymbolKind::Parameter, "$input").ty
    );
}

#[test]
fn bind_inline_promoted_property_var_type() {
    let document = bind(
        "/promoted-property-doc.php",
        r#"<?php
class StoreClient {
    public function __construct(
        /** @var array<string, string> */
        protected readonly array $endpoints,
    ) {}
}
"#,
    );
    let constructor = find(&document, SymbolKind::Method, "__construct");
    assert_eq!(constructor.parameters.len(), 1);
    assert_eq!(
        constructor.parameters[0].ty.to_string(),
        "array<string,string>"
    );
    assert_eq!(
        constructor.parameters[0].doc_type.to_string(),
        "array<string,string>"
    );
    let property = find(&document, SymbolKind::Property, "endpoints");
    assert_eq!(property.ty.to_string(), "array<string,string>");
    assert_eq!(property.doc_type.to_string(), "array<string,string>");
}

#[test]
fn bind_shopware_planned_method_parameters() {
    let document = bind(
        "/planned-parameters.php",
        r#"<?php
namespace App;

use Shopware\Core\Framework\Deprecation\BCChange\NewOptionalParameter;
use Shopware\Core\Framework\Deprecation\BCChange\NewRequiredParameter;

class Collection {}
class Service {
    #[NewOptionalParameter(
        version: 'v6.8.0',
        parameterName: 'collection',
        parameterType: '?' . Collection::class,
        defaultValue: null,
    )]
    #[NewRequiredParameter(
        version: 'v6.8.0',
        parameterName: 'enabled',
        parameterType: 'bool',
    )]
    public function load(string $name): void {}
}"#,
    );
    let method = find(&document, SymbolKind::Method, "load");
    assert_eq!(method.parameters.len(), 3);
    assert_eq!(&*method.parameters[0].name, "$name");
    assert_eq!(method.parameters[0].ty.to_string(), "string");
    assert!(!method.parameters[0].optional);
    assert_eq!(&*method.parameters[1].name, "$collection");
    assert_eq!(method.parameters[1].ty.to_string(), "App\\Collection|null");
    assert!(method.parameters[1].flags.has(Flags::SYNTHETIC));
    assert!(method.parameters[1].optional);
    assert_eq!(&*method.parameters[2].name, "$enabled");
    assert_eq!(method.parameters[2].ty.to_string(), "bool");
    assert!(method.parameters[2].flags.has(Flags::SYNTHETIC));
    assert!(!method.parameters[2].optional);
}

#[test]
fn bind_local_forms_closures_and_declaration_references() {
    let document = bind(
        "/locals.php",
        r#"<?php
namespace App;
#[Attribute]
class Service {}
class Consumer {
    public function run(Service $service): Service {
        $items = [$service];
        foreach ($items as $key => $item) {
            $format = function () use ($item): Service {
                return new Service();
            };
        }
        try {
            return $service;
        } catch (\RuntimeException $error) {
            throw $error;
        }
    }
}"#,
    );
    let snapshot = Snapshot::new(1, std::slice::from_ref(&document));
    let linked = link(&document, &snapshot);
    find(&linked, SymbolKind::Closure, "{closure}");
    for name in ["$items", "$key", "$item", "$format", "$error"] {
        find(&linked, SymbolKind::Local, name);
    }
    let mut service_references = 0;
    let mut resolved_variables = 0;
    for reference in &linked.references {
        if reference.kind == NameKind::Class
            && reference
                .qualified_names()
                .first()
                .is_some_and(|name| name.as_str() == "App\\Service")
        {
            service_references += 1;
            assert!(!reference.resolved.is_empty());
        }
        if reference.kind == NameKind::Variable && !reference.resolved.is_empty() {
            resolved_variables += 1;
        }
    }
    assert!(service_references >= 3);
    assert!(resolved_variables >= 5);
}

#[test]
fn bind_trait_method_aliases() {
    let document = bind(
        "/trait-alias.php",
        r#"<?php
namespace App;
use Shared\Reusable;
class Consumer {
    use Reusable {
        Reusable::value as private aliasedValue;
        Reusable::hidden as public;
    }
}"#,
    );
    let class = find(&document, SymbolKind::Class, "Consumer");
    assert_eq!(
        class.trait_aliases(),
        [
            TraitAlias {
                trait_name: Str::from("Shared\\Reusable"),
                method: Str::from("value"),
                alias: Str::from("aliasedValue"),
                visibility: Visibility::Private,
                has_visibility: true,
            },
            TraitAlias {
                trait_name: Str::from("Shared\\Reusable"),
                method: Str::from("hidden"),
                alias: Str::from("hidden"),
                visibility: Visibility::Public,
                has_visibility: true,
            },
        ]
    );
}

#[test]
fn array_element_assignment_declares_local() {
    let document = bind(
        "/array-assignment.php",
        "<?php\nfunction collect(string $key): array {\n    $items[$key] = 'first';\n    $items[$key] = 'second';\n    return $items;\n}",
    );
    let snapshot = Snapshot::new(1, std::slice::from_ref(&document));
    let linked = link(&document, &snapshot);
    find(&linked, SymbolKind::Local, "$items");
    let resolved = linked
        .references
        .iter()
        .filter(|r| r.kind == NameKind::Variable && &*r.name == "$items" && !r.resolved.is_empty())
        .count();
    assert_eq!(resolved, 2);
}

#[test]
fn bind_class_alias_declarations_and_targets() {
    let source = r#"<?php
namespace App;
use Vendor\Original;
class_alias(Original::class, \Legacy\AliasName::class);
class_alias('Vendor\Other', 'Legacy\OtherAlias');
class_alias($dynamic, 'Legacy\DynamicAlias');
"#;
    let parse = swlsp_parser_php::parse(source);
    assert!(parse.errors.is_empty());
    let document = Binder::new().bind("/class-alias.php", 1, &parse.tree);
    let aliases: BTreeMap<&str, &Symbol> = document
        .symbols
        .iter()
        .filter(|symbol| symbol.flags.has(Flags::CLASS_ALIAS))
        .map(|symbol| (symbol.fully_qualified.as_str(), symbol))
        .collect();
    assert_eq!(aliases.len(), 2);
    assert_eq!(
        strs(aliases["Legacy\\AliasName"].extends()),
        ["Vendor\\Original"]
    );
    assert_eq!(
        strs(aliases["Legacy\\OtherAlias"].extends()),
        ["Vendor\\Other"]
    );
    assert!(aliases["Legacy\\AliasName"].flags.has(Flags::SYNTHETIC));
    assert_eq!(
        slice(source, aliases["Legacy\\AliasName"].selection_range),
        "\\Legacy\\AliasName"
    );
    assert!(
        document
            .references
            .iter()
            .any(|reference| reference.kind == NameKind::Class
                && &*reference.name == "Vendor\\Other"
                && reference.qualified_names().len() == 1
                && reference.qualified_names()[0].as_str() == "Vendor\\Other")
    );
}

#[test]
fn declaration_set_tracks_identities_and_deduplicates() {
    // A node is a declaration exactly when its identity was marked.
    let source = "<?php\nfunction transform($input) {\n    $first = $input;\n    $second = $first;\n    return [$first, $second];\n}\n";
    let document = bind("/declarations.php", source);
    let locals: Vec<_> = document
        .symbols
        .iter()
        .filter(|symbol| symbol.kind == SymbolKind::Local)
        .map(|symbol| symbol.name.to_string())
        .collect();
    assert_eq!(locals, ["$first", "$second"]);
    // Declared variables are not references; later uses are.
    let first_refs = document
        .references
        .iter()
        .filter(|reference| &*reference.name == "$first")
        .count();
    assert_eq!(first_refs, 2);
}

#[test]
fn constant_expressions_and_commented_names_reference_their_classes() {
    let source = r#"<?php
namespace App;

use Lib\{Acl, Service, Field, Types, Flag, Size, Fallback, Result, Closure, Error, Rule};

#[Route(defaults: ['_acl' => [Acl::READ]])]
enum Example: string
{
    case One = Flag::ONE;
    private const TYPES = [Field::class => Types::JSON];
    public static array $sizes = [Size::SMALL];

    public function __construct(
        #[Autowire(service: Service::class)]
        private object $service = new Fallback(),
    ) {}

    public function run(Closure $callback): void
    {
        try {
            $callback(
                // a comment before the name
                Result::info('x'),
            );
        } catch (Error) {
        }
        array_map(static fn (array $tax) => new Rule($tax), []);
    }
}
"#;
    let document = bind("/example.php", source);
    let mut classes: Vec<&str> = document
        .references
        .iter()
        .filter(|reference| reference.kind == NameKind::Class)
        .map(|reference| &*reference.name)
        .collect();
    classes.sort_unstable();
    classes.dedup();
    assert_eq!(
        classes,
        [
            "Acl", "Autowire", "Closure", "Error", "Fallback", "Field", "Flag", "Result", "Route",
            "Rule", "Service", "Size", "Types",
        ]
    );
    // The reference starts at the name, not at the comment before it.
    let result = document
        .references
        .iter()
        .find(|reference| &*reference.name == "Result")
        .unwrap();
    assert_eq!(&source[result.range], "Result");
}

#[test]
fn bc_change_attributes_keep_their_arguments() {
    let document = bind(
        "/project/vendor/Entity.php",
        r#"<?php
namespace Shopware\Core\Checkout;

use Shopware\Core\Framework\Context;
use Shopware\Core\Framework\Deprecation\BCChange\ParameterDefaultValueChange;
use Shopware\Core\Framework\Deprecation\BCChange\ParameterTypeWidening;

class Address {}

class Entity
{
    #[ParameterTypeWidening(version: 'v6.8.0', parameterName: 'address', newType: '?' . Address::class)]
    #[ParameterDefaultValueChange(version: 'v6.8.0', parameterName: 'scopes', newDefaultValue: [])]
    public function assign(Address $address, array $scopes = [Context::SYSTEM_SCOPE], int $limit = 5): void {}
}
"#,
    );
    let method = find(&document, SymbolKind::Method, "assign");
    let widening = &method.attributes()[0];
    assert_eq!(
        widening.argument("version", 0).unwrap().value.as_str(),
        "v6.8.0"
    );
    // A concatenation of strings and class names is a string.
    let new_type = widening.argument("newType", 2).unwrap();
    assert_eq!(new_type.kind, AttributeValueKind::String);
    assert_eq!(
        new_type.value.as_str(),
        "?Shopware\\Core\\Checkout\\Address"
    );
    // Only the default that is announced to change is kept.
    let scopes = method.parameters[1].default_value.as_deref().unwrap();
    assert_eq!(
        scopes.items[0].value.value.as_str(),
        "Shopware\\Core\\Framework\\Context::SYSTEM_SCOPE"
    );
    assert!(method.parameters[2].default_value.is_none());
}
