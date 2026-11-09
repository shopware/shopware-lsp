use std::alloc::{GlobalAlloc, Layout, System};
use std::cell::Cell;
use std::collections::{HashMap, HashSet};

use crate::relations::{MAX_PRECISE_LITERAL_UNION_MEMBERS, MAX_PRECISE_UNION_MEMBERS};
use crate::render::{rendered_length_hint, write_rendered};
use crate::*;

// ----- allocation counting -----

struct CountingAllocator;

thread_local! {
    static ALLOCATIONS: Cell<usize> = const { Cell::new(0) };
}

// SAFETY: delegates to the system allocator; the counter is a const-initialized
// thread local without destructor, so touching it never allocates.
unsafe impl GlobalAlloc for CountingAllocator {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        let _ = ALLOCATIONS.try_with(|count| count.set(count.get() + 1));
        // SAFETY: forwarded unchanged.
        unsafe { System.alloc(layout) }
    }

    unsafe fn dealloc(&self, pointer: *mut u8, layout: Layout) {
        // SAFETY: forwarded unchanged.
        unsafe { System.dealloc(pointer, layout) }
    }

    unsafe fn realloc(&self, pointer: *mut u8, layout: Layout, size: usize) -> *mut u8 {
        let _ = ALLOCATIONS.try_with(|count| count.set(count.get() + 1));
        // SAFETY: forwarded unchanged.
        unsafe { System.realloc(pointer, layout, size) }
    }
}

#[global_allocator]
static GLOBAL: CountingAllocator = CountingAllocator;

fn allocations_per_run(runs: usize, mut body: impl FnMut()) -> usize {
    body();
    let before = ALLOCATIONS.with(Cell::get);
    for _ in 0..runs {
        body();
    }
    (ALLOCATIONS.with(Cell::get) - before) / runs
}

// ----- helpers -----

fn named(name: &str) -> Type {
    Type::named(name, Vec::new())
}

fn named_of(name: &str, arguments: Vec<Type>) -> Type {
    Type::named(name, arguments)
}

fn field(name: &str, ty: Type) -> ShapeField {
    ShapeField::new(name, ty)
}

fn optional(name: &str, ty: Type) -> ShapeField {
    ShapeField::optional(name, ty)
}

fn param(ty: Type) -> CallableParameter {
    CallableParameter::of(ty)
}

fn templates(entries: &[(&str, Type)]) -> HashMap<String, Type> {
    entries
        .iter()
        .map(|(name, ty)| ((*name).to_owned(), ty.clone()))
        .collect()
}

#[derive(Default)]
struct TestHierarchy {
    parents: HashMap<&'static str, Vec<&'static str>>,
    variances: HashMap<&'static str, Vec<Variance>>,
}

impl TestHierarchy {
    fn with(parents: &[(&'static str, &'static str)]) -> Self {
        let mut hierarchy = TestHierarchy::default();
        for (candidate, target) in parents {
            hierarchy.parents.entry(candidate).or_default().push(target);
        }
        hierarchy
    }
}

impl Hierarchy for TestHierarchy {
    fn is_subtype_of(&self, candidate: &str, target: &str) -> bool {
        self.parents
            .get(candidate)
            .is_some_and(|targets| targets.contains(&target))
    }

    fn template_variance(&self, name: &str, index: usize) -> Variance {
        self.variances
            .get(name)
            .and_then(|values| values.get(index).copied())
            .unwrap_or_default()
    }
}

// ----- tests -----

#[test]
fn builtin_type_parsing_does_not_allocate() {
    let allocations = allocations_per_run(1000, || {
        let value = parse_native("int").unwrap();
        std::hint::black_box(value);
    });
    assert_eq!(allocations, 0);
}

#[test]
fn type_stays_one_word() {
    assert_eq!(std::mem::size_of::<Type>(), std::mem::size_of::<usize>());
    assert_eq!(
        std::mem::size_of::<Option<Type>>(),
        std::mem::size_of::<usize>()
    );
}

#[test]
fn rendered_type_writer_matches_canonical_text() {
    let values = [
        Type::literal_string("line\n\"quoted\""),
        named_of("Collection", vec![Type::literal_string("ä")]),
        Type::self_type(vec![Type::template("T")]),
        Type::static_type(vec![named("Entity")]),
        Type::array(Type::literal_string("key"), Type::list(named("Product"))),
        Type::conditional(
            Type::template("T"),
            named("Entity"),
            Type::literal_string("yes"),
            Type::literal_string(&"x".repeat(80)),
        ),
    ];
    for value in values {
        let mut out = String::with_capacity(rendered_length_hint(&value));
        write_rendered(&mut out, &value);
        assert_eq!(value.text(), out);
    }
}

#[test]
fn parse_canonical_types() {
    let tests = [
        ("?string", "null|string"),
        ("string|null|string", "null|string"),
        (
            "(Countable&Traversable)|null",
            "(Countable&Traversable)|null",
        ),
        (
            "App\\Entity\\ProductEntity[]",
            "array<array-key,App\\Entity\\ProductEntity>",
        ),
        (
            "array<string, ProductEntity>",
            "array<string,ProductEntity>",
        ),
        (
            "iterable<int, ProductEntity>",
            "iterable<int,ProductEntity>",
        ),
        (
            "non-empty-list<ProductEntity>",
            "non-empty-list<ProductEntity>",
        ),
        ("class-string<ProductEntity>", "class-string<ProductEntity>"),
        (
            "Repository<covariant Collection<covariant Entity>>",
            "Repository<Collection<Entity>>",
        ),
        ("Consumer<contravariant Entity>", "Consumer<Entity>"),
        ("Collection<*>", "Collection<mixed>"),
        ("self<RuleError>", "self<RuleError>"),
        ("static<T&Identified>", "static<Identified&T>"),
        ("callable(string, int=): bool", "callable(string,int=):bool"),
        ("\\Closure(string): int", "callable(string):int"),
        (
            "array{id:int,name?:string,...}",
            "array{id:int,name?:string,...}",
        ),
        (
            "object{name:string,active?:bool}",
            "object{active?:bool,name:string}",
        ),
        ("true|false", "false|true"),
        ("bool|true|false", "bool"),
        (
            "array<array-key,list<string|null>>",
            "array<array-key,list<null|string>>",
        ),
        ("scalar", "bool|float|int|string"),
        (
            "(T is 'array' ? ArrayNode<T> : Node<T>)",
            "(T is \"array\" ? ArrayNode<T> : Node<T>)",
        ),
        (
            "($mapper is callable ? string : int)",
            "($mapper is callable ? string : int)",
        ),
    ];
    for (source, expected) in tests {
        let actual = parse(source).unwrap_or_else(|error| panic!("{source}: {error}"));
        assert_eq!(actual.text(), expected, "{source}");
    }
}

#[test]
fn non_empty_list_relationships() {
    let relations = Relations::default();
    let non_empty = Type::non_empty_list(Type::STRING);

    assert_eq!(non_empty.text(), "non-empty-list<string>");
    assert!(relations.is_subtype(&non_empty, &Type::list(Type::STRING)));
    assert!(relations.is_subtype(&non_empty, &Type::array(Type::INT, Type::STRING)));
    assert!(relations.is_assignable_to(&non_empty, &Type::iterable(Type::INT, Type::STRING)));
    assert!(!relations.is_subtype(&Type::list(Type::STRING), &non_empty));
    assert!(!relations.is_assignable_to(&Type::list(Type::STRING), &non_empty));

    let restored = parse_persisted(&non_empty.persisted_string()).unwrap();
    assert_eq!(non_empty, restored);
}

#[test]
fn non_empty_array_relationships() {
    let relations = Relations::default();
    let non_empty = must_parse("non-empty-array<string, int>");

    assert_eq!(Type::non_empty_array(Type::STRING, Type::INT), non_empty);
    assert!(relations.is_subtype(&non_empty, &Type::array(Type::STRING, Type::INT)));
    assert!(relations.is_assignable_to(&non_empty, &Type::iterable(Type::STRING, Type::INT)));
    assert!(!relations.is_subtype(&Type::array(Type::STRING, Type::INT), &non_empty));
    assert!(relations.is_subtype(
        &Type::array_shape(vec![field("id", Type::INT)], false),
        &non_empty,
    ));

    let restored = parse_persisted(&non_empty.persisted_string()).unwrap();
    assert_eq!(non_empty, restored);
}

#[test]
fn special_class_types_preserve_generic_arguments() {
    let value = Type::self_type(vec![Type::template("T")]);
    assert_eq!(value.text(), "self<T>");
    assert_eq!(
        substitute(&value, &templates(&[("T", named("Product"))])).text(),
        "self<Product>"
    );
    let restored = parse_persisted(&value.persisted_string()).unwrap();
    assert_eq!(value, restored);
    assert_eq!(restored.argument(0).kind(), Kind::Template);
}

#[test]
fn unresolved_template_is_uncertain() {
    assert!(contains_uncertain(&Type::template("T")));
    assert!(contains_uncertain(&Type::list(Type::template("T"))));
}

#[test]
fn builtin_type_classification_does_not_allocate() {
    let mut result = "";
    let allocations = allocations_per_run(100, || {
        result = crate::parser::builtin_type_name("INTERFACE-STRING");
    });
    assert_eq!(allocations, 0);
    assert_eq!(result, "interface-string");
    assert_eq!(crate::parser::builtin_type_name("App\\Product"), "");
}

#[test]
fn conditional_type_substitution_selects_branch() {
    let value = must_parse("(T is 'array' ? ArrayNode<T> : Node<T>)");
    assert_eq!(
        substitute(&value, &templates(&[("T", Type::literal_string("array"))])).text(),
        "ArrayNode<\"array\">"
    );
    assert_eq!(
        substitute(&value, &templates(&[("T", Type::literal_string("string"))])).text(),
        "Node<\"string\">"
    );
}

#[test]
fn composite_type_string_is_cached() {
    let value = Type::array(Type::STRING, named("Product"));
    assert_eq!(value.text(), "array<string,Product>");
    let allocations = allocations_per_run(100, || {
        std::hint::black_box(value.key());
    });
    assert_eq!(allocations, 0);
}

#[test]
fn joiner_caches_only_final_composite_type() {
    let relations = Relations::default();
    let mut joiner = Joiner::new(relations, Type::NEVER);
    joiner.add(Type::literal_string("red"));
    joiner.add(Type::literal_string("green"));
    joiner.add(Type::literal_string("blue"));

    assert_eq!(joiner.member_count(), 3);
    assert!(joiner.pending_value().is_unknown());

    let value = joiner.value();
    assert_eq!(value.text(), "\"blue\"|\"green\"|\"red\"");
    assert_eq!(value.cached_text(), Some(&*value.text()));
    assert_eq!(
        value,
        relations.join(
            &relations.join(&Type::NEVER, &Type::literal_string("red")),
            &relations.join(
                &Type::literal_string("green"),
                &Type::literal_string("blue"),
            ),
        )
    );
}

#[test]
fn joiner_matches_pairwise_join() {
    let mut many_literals = vec![Type::NEVER];
    for index in 0..MAX_PRECISE_LITERAL_UNION_MEMBERS + 2 {
        many_literals.push(Type::literal_string(&index.to_string()));
    }
    let mut many_objects = vec![Type::NEVER];
    for index in 0..MAX_PRECISE_UNION_MEMBERS + 2 {
        many_objects.push(named(&format!("DomainType{index}")));
    }
    let cases: Vec<(&str, Vec<Type>)> = vec![
        ("single", vec![Type::NEVER]),
        (
            "ordinary union",
            vec![Type::NEVER, Type::INT, Type::STRING, Type::FLOAT],
        ),
        (
            "nested union",
            vec![
                Type::NEVER,
                Type::union([Type::INT, Type::STRING]),
                Type::FLOAT,
            ],
        ),
        (
            "bool normalized",
            vec![Type::NEVER, Type::TRUE, Type::FALSE, Type::BOOL],
        ),
        (
            "unknown absorbs",
            vec![Type::NEVER, Type::INT, Type::UNKNOWN, Type::STRING],
        ),
        ("literal widening", many_literals),
        ("member ceiling", many_objects),
    ];
    let relations = Relations::default();
    for (name, values) in cases {
        let mut expected = values[0].clone();
        for value in &values[1..] {
            expected = relations.join(&expected, value);
        }
        let mut joiner = Joiner::new(relations, values[0].clone());
        for value in &values[1..] {
            joiner.add(value.clone());
        }
        let actual = joiner.value();
        assert_eq!(expected, actual, "{name}");
        assert_eq!(expected.text(), actual.text(), "{name}");
    }
}

#[test]
fn semantic_names_and_composite_text_share_one_string_slot() {
    let literal = Type::literal_string("storefront");
    assert_eq!(literal.name(), "storefront");
    assert_eq!(literal.text(), "\"storefront\"");

    let union = Type::union([literal, Type::NULL]);
    assert_eq!(union.name(), "");
    assert_eq!(union.text(), "\"storefront\"|null");
    assert_eq!(union.cached_text(), Some("\"storefront\"|null"));
}

#[test]
fn array_shape_sorts_fields_and_keeps_caller_storage() {
    let fields = vec![field("z", Type::STRING), field("a", Type::INT)];
    let value = Type::array_shape(fields.clone(), false);
    assert_eq!(value.text(), "array{a:int,z:string}");
    assert_eq!(&*fields[0].name, "z");
    assert_eq!(&*value.fields()[0].name, "a");
}

#[test]
fn type_collection_accessors_do_not_allocate() {
    let shape = Type::array_shape(
        vec![field("id", Type::INT), field("name", Type::STRING)],
        false,
    );
    let callable = Type::callable(
        vec![
            CallableParameter::new("$id", Type::INT),
            CallableParameter {
                optional: true,
                ..CallableParameter::new("$name", Type::STRING)
            },
        ],
        Type::BOOL,
    );

    assert_eq!(shape.field_count(), 2);
    assert_eq!(&*shape.field(0).unwrap().name, "id");
    assert!(shape.field(2).is_none());
    assert_eq!(callable.parameter_count(), 2);
    assert_eq!(&*callable.parameter(1).unwrap().name, "$name");
    assert!(callable.parameter(2).is_none());

    let allocations = allocations_per_run(1000, || {
        std::hint::black_box(shape.field(1).cloned());
        std::hint::black_box(callable.parameter(1).cloned());
    });
    assert_eq!(allocations, 0);
}

#[test]
fn common_literal_types_are_canonical() {
    assert_eq!(Type::literal_string("").text(), "\"\"");
    assert_eq!(Type::literal_int("0").text(), "0");
    assert_eq!(Type::literal_int("1").text(), "1");

    let allocations = allocations_per_run(1000, || {
        std::hint::black_box(Type::literal_string(""));
        std::hint::black_box(Type::literal_int("0"));
        std::hint::black_box(Type::literal_int("1"));
    });
    assert_eq!(allocations, 0);
}

#[test]
fn compact_type_storage_keeps_arguments_and_text_alive() {
    let name = "LongTypeName".repeat(32);
    let value = named_of(&name, vec![Type::array(Type::STRING, named("Product"))]);
    let expected = value.text().into_owned();
    drop(name);

    assert_eq!(value.text(), expected);
    assert_eq!(value.argument_count(), 1);
    assert_eq!(value.argument(0).text(), "array<string,Product>");
}

#[test]
fn compact_type_storage_keeps_detailed_payloads_alive() {
    let field_name = "field".repeat(32);
    let parameter_name = format!("${}", "parameter".repeat(16));
    let shape = Type::object_shape(vec![field(&field_name, Type::list(named("Product")))], true);
    let value = Type::callable(
        vec![CallableParameter {
            optional: true,
            ..CallableParameter::new(&parameter_name, shape.clone())
        }],
        named("Result"),
    );
    let expected = value.text().into_owned();

    assert_eq!(value.text(), expected);
    assert_eq!(*value.result(), named("Result"));
    assert_eq!(&*value.parameters()[0].name, parameter_name);
    assert!(value.parameters()[0].optional);
    assert!(shape.is_open_shape());
    assert_eq!(&*shape.fields()[0].name, field_name);
    assert_eq!(shape.fields()[0].ty.text(), "list<Product>");
}

#[test]
fn type_msgpack_encoding_remains_backward_compatible() {
    let expected = named_of(
        "Collection",
        vec![Type::array(Type::STRING, named("Product"))],
    );

    let encoded = rmp_serde::to_vec(&expected).unwrap();
    let current: Type = rmp_serde::from_slice(&encoded).unwrap();
    assert_eq!(expected, current);

    let legacy = rmp_serde::to_vec(&serde_bytes_wrapper(expected.text().as_bytes())).unwrap();
    let restored: Type = rmp_serde::from_slice(&legacy).unwrap();
    assert_eq!(expected, restored);
}

/// Serializes as a msgpack `bin`, the legacy encoding of persisted type text.
fn serde_bytes_wrapper(bytes: &[u8]) -> impl serde::Serialize + '_ {
    struct Bytes<'a>(&'a [u8]);
    impl serde::Serialize for Bytes<'_> {
        fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
            serializer.serialize_bytes(self.0)
        }
    }
    Bytes(bytes)
}

#[test]
fn structural_type_equality_preserves_canonical_semantics() {
    let left = named_of(
        "Collection",
        vec![Type::union([
            Type::literal_string("storefront"),
            Type::NULL,
        ])],
    );
    let right = named_of(
        "Collection",
        vec![Type::union([
            Type::NULL,
            Type::literal_string("storefront"),
        ])],
    );
    assert_eq!(left, right);
    assert_eq!(left.key(), right.key());
    assert_eq!(left.key(), "Collection<\"storefront\"|null>");
}

#[test]
fn parse_rejects_malformed_types() {
    for source in ["array<string", "callable(string", "array{id int}", "Foo|"] {
        assert!(parse(source).is_err(), "{source}");
    }
}

#[test]
fn parse_php_integer_literal_formats() {
    for source in [
        "0o755", "0O644", "0xDEAD", "0b1010", "0755", "-0xF", "1_000",
    ] {
        let parsed = parse(source).unwrap_or_else(|error| panic!("{source}: {error}"));
        assert_eq!(parsed, Type::literal_int(source), "{source}");
        assert_eq!(parsed.text(), source);
    }
}

#[test]
fn invalid_named_types_collapse_to_unknown() {
    for source in [
        "App\\",
        "App\\\\Product",
        "App\\Broken//comment",
        "App\\Settings.Result",
        "App\\Type,description",
        "pseudo-type-with-dashes",
    ] {
        assert_eq!(named(source), Type::UNKNOWN, "{source}");
    }
    assert_eq!(Type::template("T,value"), Type::UNKNOWN);
    assert_eq!(Type::literal_int("// stdin\n1"), Type::INT);
    assert_eq!(Type::literal_float("1.0 // description"), Type::FLOAT);
}

#[test]
fn literal_float_validation_accepts_decimal_hex_and_special_values() {
    for valid in [
        "1.5",
        "1.",
        ".5",
        "1e5",
        "1E-5",
        "+1.5",
        "-1.5",
        "1_000.5",
        "inf",
        "+Inf",
        "-infinity",
        "NaN",
        "0x1p-2",
        "0x1.8p1",
        "0x.8p1",
        "1e-400",
        "0x1p1023",
        "0x0p99999",
    ] {
        assert_eq!(
            Type::literal_float(valid).kind(),
            Kind::LiteralFloat,
            "{valid}"
        );
    }
    for invalid in [
        "",
        ".",
        "e5",
        "1e",
        "1e+",
        "+-1",
        "-nan",
        "+nan",
        "infin",
        "0x1",
        "0xp1",
        "0x1p",
        "1e400",
        "0x1p1024",
        "0x10p1021",
        "1.0f",
    ] {
        assert_eq!(Type::literal_float(invalid), Type::FLOAT, "{invalid}");
    }
}

#[test]
fn qualified_name_validation_does_not_allocate() {
    let allocations = allocations_per_run(1000, || {
        assert!(crate::ty::valid_qualified_name(
            "Shopware\\Core\\Content\\Product"
        ));
    });
    assert_eq!(allocations, 0);
}

#[test]
fn relations() {
    let hierarchy = TestHierarchy::with(&[("Product", "Entity")]);
    let relations = Relations::new(&hierarchy);

    assert!(relations.is_subtype(&Type::literal_int("1"), &Type::INT));
    assert!(relations.is_subtype(&Type::TRUE, &Type::BOOL));
    assert!(relations.is_subtype(&named("Product"), &named("Entity")));
    assert!(relations.is_subtype(
        &named("SHOPWARE\\Core\\Product"),
        &named("shopware\\core\\product"),
    ));
    assert!(relations.is_subtype(
        &Type::list(Type::STRING),
        &Type::iterable(Type::INT, Type::STRING),
    ));
    let iterable_hierarchy = TestHierarchy::with(&[("Generator", "Traversable")]);
    let iterable_relations = Relations::new(&iterable_hierarchy);
    assert!(iterable_relations.is_subtype(
        &named("Generator"),
        &Type::iterable(Type::MIXED, Type::MIXED),
    ));
    assert!(iterable_relations.is_subtype(
        &named("Generator"),
        &Type::iterable(Type::ARRAY_KEY, Type::MIXED),
    ));
    assert!(!iterable_relations.is_subtype(
        &named("Generator"),
        &Type::iterable(Type::INT, Type::STRING),
    ));
    assert!(!iterable_relations.is_subtype(
        &named("Generator"),
        &Type::iterable(Type::STRING, Type::MIXED),
    ));
    let empty_array = Type::array(Type::ARRAY_KEY, Type::NEVER);
    assert!(relations.is_subtype(&empty_array, &Type::list(Type::STRING)));
    assert!(relations.is_subtype(&empty_array, &Type::array(Type::STRING, Type::MIXED)));
    assert!(relations.is_subtype(&empty_array, &Type::iterable(Type::INT, Type::OBJECT)));
    assert!(relations.is_subtype(
        &Type::array_shape(
            vec![
                field("0", Type::literal_string("first")),
                field("1", Type::literal_string("second")),
            ],
            false,
        ),
        &Type::list(Type::STRING),
    ));
    assert!(relations.is_subtype(
        &Type::array_shape(
            vec![field("0", Type::STRING), optional("1", Type::STRING)],
            false,
        ),
        &Type::list(Type::STRING),
    ));
    assert!(!relations.is_subtype(
        &Type::array_shape(
            vec![optional("0", Type::STRING), field("1", Type::STRING)],
            false,
        ),
        &Type::list(Type::STRING),
    ));
    assert!(!relations.is_subtype(
        &Type::array_shape(
            vec![field("0", Type::STRING), field("2", Type::STRING)],
            false,
        ),
        &Type::list(Type::STRING),
    ));
    assert!(relations.is_subtype(
        &Type::array_shape(vec![field("email", Type::STRING)], false),
        &Type::array_shape(vec![field("\"email\"", Type::STRING)], false),
    ));
    assert!(relations.is_subtype(
        &Type::array(Type::ARRAY_KEY, Type::NEVER),
        &Type::array_shape(vec![optional("optional", Type::STRING)], false),
    ));
    assert!(relations.is_assignable_to(&Type::INT, &Type::FLOAT));
    assert!(relations.is_assignable_to(
        &Type::list(Type::array_shape(
            vec![
                field("factor", Type::literal_int("1")),
                field("key", Type::STRING),
            ],
            false,
        )),
        &Type::list(Type::array(
            Type::STRING,
            Type::union([Type::FLOAT, Type::STRING]),
        )),
    ));
    assert!(relations.is_assignable_to(&Type::INT, &Type::union([Type::FLOAT, Type::NULL])));
    assert!(relations.is_assignable_to(
        &Type::union([Type::literal_int("0"), Type::FLOAT]),
        &Type::FLOAT,
    ));
    assert!(relations.is_assignable_to(
        &Type::array(Type::ARRAY_KEY, Type::NEVER),
        &Type::conditional(
            Type::UNKNOWN,
            Type::callable(Vec::new(), Type::MIXED),
            Type::array(Type::STRING, Type::list(Type::STRING)),
            Type::array(
                Type::STRING,
                Type::list(Type::array(Type::STRING, Type::STRING)),
            ),
        ),
    ));
    assert!(relations.is_subtype(&Type::ARRAY_KEY, &Type::union([Type::INT, Type::STRING])));
    assert!(relations.is_subtype(&Type::BOOL, &Type::union([Type::TRUE, Type::FALSE])));
    assert!(relations.is_subtype(
        &Type::callable(Vec::new(), Type::VOID),
        &Type::callable(vec![param(named("Connection"))], Type::VOID),
    ));
    assert!(!relations.is_subtype(
        &Type::callable(vec![param(named("Connection"))], Type::VOID),
        &Type::callable(Vec::new(), Type::VOID),
    ));
    assert!(relations.is_assignable_to(
        &Type::callable(vec![param(named("Symfony\\ItemInterface"))], Type::STRING),
        &Type::union([
            named_of("CallbackInterface", vec![Type::STRING]),
            Type::callable(
                vec![param(named("Psr\\CacheItemInterface")), param(Type::BOOL)],
                Type::STRING,
            ),
            Type::callable(
                vec![param(named("Symfony\\ItemInterface")), param(Type::BOOL)],
                Type::STRING,
            ),
        ]),
    ));
    let scalar_list_or_map = Type::union([
        Type::list(Type::union([
            Type::STRING,
            Type::INT,
            Type::FLOAT,
            Type::NULL,
        ])),
        Type::array(Type::STRING, Type::STRING),
    ]);
    assert!(relations.is_assignable_to(
        &Type::array(Type::ARRAY_KEY, Type::STRING),
        &scalar_list_or_map,
    ));
    assert!(relations.is_assignable_to(
        &Type::array(Type::ARRAY_KEY, Type::union([Type::NULL, Type::STRING])),
        &scalar_list_or_map,
    ));
    let string_list_or_map = Type::union([
        Type::list(Type::STRING),
        Type::array(Type::STRING, Type::STRING),
    ]);
    assert!(!relations.is_assignable_to(
        &Type::array(Type::ARRAY_KEY, Type::INT),
        &string_list_or_map,
    ));
    assert!(!relations.is_assignable_to(
        &Type::array(Type::ARRAY_KEY, Type::OBJECT),
        &string_list_or_map,
    ));
    assert!(!relations.is_subtype(&Type::MIXED, &Type::STRING));
    assert!(!relations.is_subtype(&Type::UNKNOWN, &Type::MIXED));
    assert_eq!(
        relations.join(&Type::INT, &Type::STRING).text(),
        "int|string"
    );
    assert_eq!(
        relations
            .narrow(&Type::union([Type::STRING, Type::NULL]), &Type::STRING)
            .text(),
        "string"
    );
    assert_eq!(
        relations
            .without(&Type::union([Type::STRING, Type::NULL]), &Type::NULL)
            .text(),
        "string"
    );
}

#[test]
fn join_array_shape_branches_keeps_optional_fields() {
    let relations = Relations::default();
    let empty = Type::array(Type::ARRAY_KEY, Type::NEVER);
    let with_language = Type::array_shape(vec![field("languageId", Type::STRING)], false);
    let with_currency = Type::array_shape(vec![field("currencyId", Type::STRING)], false);

    assert_eq!(
        relations.join(&empty, &with_language).text(),
        "array{languageId?:string}"
    );
    assert_eq!(
        relations.join(&with_language, &with_currency).text(),
        "array{currencyId?:string,languageId?:string}"
    );
    assert_eq!(
        relations
            .join(
                &with_language,
                &Type::array_shape(
                    vec![
                        field("currencyId", Type::STRING),
                        field("languageId", Type::STRING),
                    ],
                    false,
                ),
            )
            .text(),
        "array{currencyId?:string,languageId:string}"
    );
    assert_eq!(
        relations
            .join(
                &empty,
                &Type::array_shape(vec![field("0", Type::STRING)], false),
            )
            .text(),
        "list<string>"
    );
}

#[test]
fn array_shape_assignment_carries_scalar_widening() {
    let relations = Relations::default();
    assert!(relations.is_assignable_to(
        &Type::array_shape(
            vec![
                field("standardRate", Type::literal_int("20")),
                field("reduced", Type::literal_int("7")),
            ],
            false,
        ),
        &Type::array_shape(
            vec![
                field("standardRate", Type::FLOAT),
                optional("reduced", Type::FLOAT),
            ],
            false,
        ),
    ));
}

#[test]
fn generic_object_variance() {
    let mut hierarchy = TestHierarchy::with(&[("Product", "Entity")]);
    hierarchy
        .variances
        .insert("Producer", vec![Variance::Covariant]);
    hierarchy
        .variances
        .insert("Consumer", vec![Variance::Contravariant]);
    let relations = Relations::new(&hierarchy);
    let entity = named("Entity");
    let product = named("Product");
    assert!(relations.is_subtype(
        &named_of("Producer", vec![product.clone()]),
        &named_of("Producer", vec![entity.clone()]),
    ));
    assert!(relations.is_subtype(
        &named_of("Consumer", vec![entity.clone()]),
        &named_of("Consumer", vec![product.clone()]),
    ));
    assert!(!relations.is_subtype(
        &named_of("Box", vec![product.clone()]),
        &named_of("Box", vec![entity.clone()]),
    ));
    assert!(relations.is_subtype(
        &named_of("Box", vec![product.clone()]),
        &named_of("Box", vec![product.clone()]),
    ));
    assert!(relations.is_subtype(
        &named_of("Box", vec![entity.clone()]),
        &named_of("Box", vec![Type::union([product.clone(), entity.clone()])]),
    ));
    assert!(relations.is_assignable_to(&named("Box"), &named_of("Box", vec![product.clone()]),));
    assert!(relations.is_assignable_to(
        &Type::class_string(named("Box")),
        &Type::class_string(named_of("Box", vec![product.clone()])),
    ));
    assert!(relations.is_subtype(
        &named_of("Box", vec![named_of("Collection", vec![product])]),
        &named_of("Box", vec![named("Collection")]),
    ));
}

#[test]
fn structural_and_callable_relations() {
    let hierarchy = TestHierarchy::with(&[("Product", "Entity")]);
    let relations = Relations::new(&hierarchy);
    assert!(relations.is_subtype(
        &Type::array(Type::STRING, named("Product")),
        &Type::array(Type::STRING, named("Entity")),
    ));
    assert!(relations.is_subtype(
        &Type::array_shape(
            vec![
                field("id", Type::STRING),
                field("product", named("Product")),
            ],
            false,
        ),
        &Type::array_shape(vec![field("product", named("Entity"))], false),
    ));
    assert!(!relations.is_subtype(
        &Type::array_shape(vec![optional("id", Type::STRING)], false),
        &Type::array_shape(vec![field("id", Type::STRING)], false),
    ));
    assert!(relations.is_subtype(
        &Type::callable(vec![param(Type::OBJECT)], named("Product")),
        &Type::callable(vec![param(named("Product"))], named("Entity")),
    ));
    assert!(relations.is_subtype(
        &Type::iterable(Type::ARRAY_KEY, named("Product")),
        &Type::union([
            Type::array(Type::ARRAY_KEY, named("Entity")),
            named("Traversable"),
        ]),
    ));
}

#[test]
fn php_callable_representations_are_assignable() {
    let relations = Relations::default();
    let callable = Type::callable(Vec::new(), Type::MIXED);
    for value in [
        Type::STRING,
        Type::literal_string("handler"),
        Type::array(Type::ARRAY_KEY, Type::MIXED),
        Type::list(Type::MIXED),
        Type::OBJECT,
        Type::array_shape(
            vec![
                field("0", Type::class_string(named("Handler"))),
                field("1", Type::literal_string("run")),
            ],
            false,
        ),
    ] {
        assert!(relations.is_assignable_to(&value, &callable), "{value}");
    }
    assert!(!relations.is_assignable_to(
        &Type::array_shape(vec![field("name", Type::STRING)], false),
        &callable,
    ));
}

#[test]
fn relations_widen_large_literal_unions() {
    let relations = Relations::default();
    let mut value = Type::NEVER;
    for index in 0..1000 {
        value = relations.join(&value, &Type::literal_string(&index.to_string()));
    }
    assert_eq!(value, Type::STRING);
}

#[test]
fn relations_widen_literal_unions_before_structural_unions() {
    let relations = Relations::default();
    let mut literals = Type::NEVER;
    for index in 0..=MAX_PRECISE_LITERAL_UNION_MEMBERS {
        literals = relations.join(&literals, &Type::literal_string(&index.to_string()));
    }
    assert_eq!(literals, Type::STRING);

    let mut objects = Type::NEVER;
    for index in 0..=MAX_PRECISE_LITERAL_UNION_MEMBERS {
        objects = relations.join(&objects, &named(&format!("Example{index}")));
    }
    assert_eq!(objects.kind(), Kind::Union);
    assert_eq!(
        objects.argument_count(),
        MAX_PRECISE_LITERAL_UNION_MEMBERS + 1
    );
}

#[test]
fn types_are_immutable_at_api_boundary() {
    let original = named_of("Collection", vec![Type::STRING]);
    assert_eq!(original.argument_count(), 1);
    assert_eq!(original.argument(0), Type::STRING);
    assert_eq!(original.argument(1), Type::UNKNOWN);
    let mut arguments = original.arguments().to_vec();
    arguments[0] = Type::INT;
    assert_eq!(original.text(), "Collection<string>");

    let callable = Type::callable(
        vec![CallableParameter::new("$value", Type::STRING)],
        Type::BOOL,
    );
    let mut parameters = callable.parameters().to_vec();
    parameters[0].ty = Type::INT;
    assert_eq!(callable.text(), "callable(string $value):bool");
}

#[test]
fn detacher_preserves_canonical_types_and_sharing() {
    let named_type = named("App\\Product");
    let value = Type::array(Type::STRING, Type::list(named_type.clone()));
    let mut detacher = Detacher::new();

    let detached = detacher.detach(&value);
    assert_eq!(value, detached);
    assert!(!value.ptr_eq(&detached));
    assert!(
        detacher
            .detach(&named_type)
            .ptr_eq(&detacher.detach(&named_type))
    );
    assert!(detacher.detach(&Type::STRING).ptr_eq(&Type::STRING));
    // Nested nodes are shared with separately detached copies of the same
    // source node.
    let nested = detacher.detach(&value.argument(1).argument(0));
    assert!(nested.ptr_eq(&detached.argument(1).argument(0)));
}

#[test]
fn detacher_interns_names() {
    let mut interner = Interner::new();
    let first = named("App\\Product");
    let second = named("App\\Product");
    let (left, right) = {
        let mut detacher = Detacher::with_interner(|value| interner.intern(value));
        (detacher.detach(&first), detacher.detach(&second))
    };
    assert_eq!(left, right);
    assert!(!left.ptr_eq(&right));
    assert_eq!(interner.len(), 1);
}

#[test]
fn substitute_templates() {
    let value = must_parse("array<TKey,list<TValue|null>>");
    let actual = substitute(
        &value,
        &templates(&[("TKey", Type::STRING), ("TValue", named("Product"))]),
    );
    assert_eq!(actual.text(), "array<string,list<Product|null>>");
}

#[test]
fn persisted_type_preserves_non_conventional_template_names() {
    let original = Type::callable(
        vec![CallableParameter::new(
            "$ids",
            Type::array(Type::ARRAY_KEY, Type::template("IDStructure")),
        )],
        Type::array_shape(vec![field("value", Type::template("IDStructure"))], false),
    );

    let encoded = original.persisted_string();
    assert!(encoded.contains(&format!(
        "{}IDStructure",
        crate::transform::PERSISTED_TEMPLATE_PREFIX
    )));
    let restored = parse_persisted(&encoded).unwrap();
    assert_eq!(original, restored);
    assert_eq!(
        restored.parameters()[0].ty.arguments()[1].kind(),
        Kind::Template
    );
    assert_eq!(restored.result().fields()[0].ty.kind(), Kind::Template);
}

#[test]
fn persisted_union_keeps_callable_arms_grouped() {
    let template = Type::template("T");
    let original = Type::union([
        named_of("CallbackInterface", vec![template.clone()]),
        Type::callable(
            vec![param(named("CacheItemInterface")), param(Type::BOOL)],
            template.clone(),
        ),
        Type::callable(
            vec![param(named("ItemInterface")), param(Type::BOOL)],
            template,
        ),
    ]);
    let encoded = original.persisted_string();
    assert!(encoded.contains("(callable("));
    let restored = parse_persisted(&encoded).unwrap();
    assert_eq!(original, restored);
}

#[test]
fn parse_persisted_accepts_legacy_canonical_type() {
    let restored = parse_persisted("array<string,Product>").unwrap();
    assert_eq!(restored.text(), "array<string,Product>");
}

#[test]
fn parse_persisted_reports_missing_template_separator() {
    let error = parse_persisted("\0shopware-lsp-templates:T").unwrap_err();
    assert_eq!(error, PersistedError::MissingTemplateSeparator);
}

#[test]
fn parse_persisted_round_trips_nullable_callable_shape_field() {
    let original = Type::array_shape(
        vec![
            optional(
                "clientCertSource",
                Type::nullable(Type::callable(Vec::new(), Type::MIXED)),
            ),
            field("credentials", Type::NULL),
        ],
        false,
    );
    let restored = parse_persisted(&original.persisted_string()).unwrap();
    assert_eq!(original, restored);
}

#[test]
fn inferred_shape_quotes_non_identifier_keys() {
    let original = Type::array_shape(
        vec![field(
            "grpc.service_config_disable_resolution",
            Type::literal_int("1"),
        )],
        false,
    );
    assert_eq!(
        original.text(),
        "array{\"grpc.service_config_disable_resolution\":1}"
    );
    let restored = parse_persisted(&original.persisted_string()).unwrap();
    assert_eq!(original, restored);
}

#[test]
fn parse_persisted_loads_inferred_client_options_shape() {
    parse_persisted(
        r"array|array{apiEndpoint:unknown,clientCertSource:callable,credentials:null,credentialsConfig:array-shape,disableRetries:false,gapicVersion:unknown,libName:null,libVersion:null,logger?:Psr\Log\LoggerInterface|false|null,transport:null,transportConfig:array<mixed,array{stubOpts?:array{grpc.service_config_disable_resolution:1}}>,universeDomain:unknown}|array{apiEndpoint:unknown,clientCertSource?:callable&null,credentials:null,credentialsConfig:array-shape,disableRetries:false,gapicVersion:unknown,libName:null,libVersion:null,logger?:Psr\Log\LoggerInterface|false|null,transport:null,transportConfig:array<mixed,array{stubOpts?:array{grpc.service_config_disable_resolution:1}}>,universeDomain:unknown}",
    )
    .unwrap();
}

#[test]
fn parse_persisted_accepts_non_ascii_class_name() {
    let original = named("\u{FFFD}");
    assert!(!original.is_unknown());
    let restored = parse_persisted(&original.persisted_string()).unwrap();
    assert_eq!(original, restored);
}

#[test]
fn substitute_preserves_bare_object() {
    assert_eq!(
        substitute(&Type::OBJECT, &templates(&[("T", Type::STRING)])),
        Type::OBJECT
    );
}

#[test]
fn restore_templates_turns_unqualified_names_into_templates() {
    let names: HashSet<&str> = ["T"].into_iter().collect();
    let restored = restore_templates(&must_parse("array<string,T|App\\T>"), &names);
    assert_eq!(restored.argument(1).arguments()[0].kind(), Kind::Object);
    assert!(contains_template(&restored));
}

#[test]
fn class_string_is_a_valid_array_key() {
    let relations = Relations::default();
    assert!(relations.is_subtype(&Type::class_string(named("Plugin")), &Type::ARRAY_KEY));
    assert!(relations.is_subtype(
        &Type::array(Type::class_string(named("Plugin")), named("Plugin")),
        &Type::array(Type::ARRAY_KEY, Type::MIXED),
    ));
}

#[test]
fn array_key_is_benevolently_assignable_to_either_scalar_key_type() {
    let relations = Relations::default();
    assert!(relations.is_assignable_to(&Type::ARRAY_KEY, &Type::INT));
    assert!(relations.is_assignable_to(&Type::ARRAY_KEY, &Type::STRING));
    assert!(!relations.is_subtype(&Type::ARRAY_KEY, &Type::INT));
    assert!(!relations.is_subtype(&Type::ARRAY_KEY, &Type::STRING));
    assert!(!relations.is_assignable_to(&Type::union([Type::INT, Type::STRING]), &Type::STRING));
}

#[test]
fn parse_native_does_not_guess_templates() {
    let value = parse_native("T").unwrap();
    assert_eq!(value.kind(), Kind::Object);
    assert_eq!(value.name(), "T");
}

#[test]
fn this_template_check_uses_unicode_case_folding() {
    // Case folding treats U+017F (long s) as 's', so "$thiſ" is
    // not a template variable and collapses to an invalid class name.
    assert_eq!(parse("$thiſ").unwrap(), Type::UNKNOWN);
    assert_eq!(parse("$THIS").unwrap(), Type::STATIC);
    assert_eq!(parse("$value").unwrap().kind(), Kind::Template);
}

#[test]
fn phpdoc_alias_round_trips() {
    let alias = phpdoc_alias(" \\App\\Product\\ ", " Payload ");
    assert_eq!(
        alias.name(),
        "__ShopwareLSPPHPDocAlias\\App\\Product\\Payload"
    );
    assert_eq!(
        phpdoc_alias_parts(&alias),
        Some(("App\\Product", "Payload"))
    );
    assert_eq!(phpdoc_alias("App\\\\Bad", "Payload"), Type::UNKNOWN);
    assert_eq!(phpdoc_alias("App", "Pay-load"), Type::UNKNOWN);
    assert_eq!(phpdoc_alias_parts(&named("App\\Product")), None);
}

#[test]
fn persisted_nested_uncached_composites_round_trip() {
    let template = Type::template("T");
    let cases = vec![
        Type::list(Type::callable(Vec::new(), template.clone())),
        Type::list(Type::callable(
            vec![param(Type::INT)],
            Type::list(template.clone()),
        )),
        Type::nullable(Type::callable(Vec::new(), Type::INT)),
        Type::list(Type::array_shape(Vec::new(), false)),
        Type::list(Type::array_shape(Vec::new(), true)),
        Type::list(Type::object_shape(Vec::new(), false)),
        Type::nullable(Type::array_shape(Vec::new(), false)),
        Type::array_shape(
            vec![
                field("a", Type::INT),
                optional("b", Type::array_shape(Vec::new(), false)),
                field("c", Type::callable(Vec::new(), template.clone())),
            ],
            false,
        ),
        Type::union([
            Type::array_shape(
                vec![field("a", Type::INT), optional("b", Type::STRING)],
                false,
            ),
            Type::callable(Vec::new(), Type::STRING),
            Type::NULL,
        ]),
        named_of(
            "Collection",
            vec![
                Type::array_shape(Vec::new(), false),
                Type::callable(Vec::new(), Type::VOID),
            ],
        ),
    ];
    for original in cases {
        let encoded = original.persisted_string();
        assert!(!encoded.contains("array-shape"), "{encoded:?}");
        assert!(!encoded.contains("object-shape"), "{encoded:?}");
        let restored = parse_persisted(&encoded).unwrap();
        assert_eq!(original, restored, "{encoded:?}");
        assert_eq!(original.text(), restored.text());
    }
}

#[test]
fn nested_uncached_composites_render_fully() {
    assert_eq!(
        Type::list(Type::callable(Vec::new(), Type::INT)).text(),
        "list<callable():int>"
    );
    assert_eq!(
        Type::list(Type::array_shape(Vec::new(), false)).text(),
        "list<array{}>"
    );
    assert_eq!(
        Type::list(Type::object_shape(Vec::new(), true)).text(),
        "list<object{...}>"
    );
    assert_ne!(
        Type::list(Type::callable(Vec::new(), Type::INT)),
        Type::list(Type::callable(Vec::new(), Type::STRING)).clone()
    );
}
