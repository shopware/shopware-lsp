use swlsp_php_types::{Type, must_parse};

use super::*;

#[test]
fn parse_rich_phpdoc() {
    let document = parse(
        r"/**
 * Product repository contract.
 *
 * @template-covariant TEntity of Entity
 * @extends Repository<TEntity>
 * @implements IteratorAggregate<int, TEntity>
 * @property-read list<TEntity> $entities
 * @method static TEntity|null find(string $id #Route, bool $strict = false)
 * @param array<string, TEntity> $items items to save #Route #Service #route
 * @return list<TEntity>
 * @throws RuntimeException
 * @phpstan-type EntityMap array<string, TEntity>
 * @deprecated
 * @final
 */",
    );

    assert_eq!(document.summary, "Product repository contract.");
    assert_eq!(document.params["$items"].text(), "array<string,TEntity>");
    assert_eq!(document.return_type.text(), "list<TEntity>");
    assert_eq!(document.templates.len(), 1);
    assert!(document.templates[0].covariant);
    assert_eq!(document.templates[0].bound.text(), "Entity");
    assert_eq!(document.extends[0].text(), "Repository<TEntity>");
    assert_eq!(
        document.implements[0].text(),
        "IteratorAggregate<int,TEntity>"
    );
    assert_eq!(document.throws[0].text(), "RuntimeException");
    assert_eq!(document.properties[0].ty.text(), "list<TEntity>");
    assert_eq!(document.properties[0].name, "entities");
    assert!(document.properties[0].read_only);
    assert_eq!(document.methods[0].name, "find");
    assert_eq!(document.methods[0].return_type.text(), "TEntity|null");
    assert!(document.methods[0].is_static);
    assert_eq!(document.methods[0].parameters.len(), 2);
    assert_eq!(document.methods[0].parameters[0].tags, ["Route"]);
    assert!(document.methods[0].parameters[1].optional);
    assert_eq!(document.param_tags["$items"], ["Route", "Service"]);
    assert_eq!(
        document.aliases["EntityMap"].text(),
        "array<string,TEntity>"
    );
    assert!(document.deprecated);
    assert!(document.is_final);
    assert!(!document.internal);
}

#[test]
fn untyped_magic_method_parameter_is_mixed() {
    let document = parse(
        r"/**
 * @method mixed randomElement($array = ['a', 'b', 'c'])
 */",
    );

    assert_eq!(document.methods.len(), 1);
    assert_eq!(document.methods[0].parameters.len(), 1);
    assert_eq!(document.methods[0].parameters[0].ty.text(), "mixed");
    assert!(document.methods[0].parameters[0].optional);
}

#[test]
fn parse_imported_type_aliases() {
    let document = parse(
        r"/**
 * @phpstan-import-type Payload from Source
 * @psalm-import-type Configuration from \Vendor\Definition as LocalConfig
 */",
    );

    assert_eq!(
        document.imports["Payload"],
        TypeImport {
            name: "Payload".to_owned(),
            from: "Source".to_owned(),
        }
    );
    assert_eq!(
        document.imports["LocalConfig"],
        TypeImport {
            name: "Configuration".to_owned(),
            from: r"\Vendor\Definition".to_owned(),
        }
    );
}

#[test]
fn parse_conditional_assertions() {
    let document = parse(
        r"/**
 * @phpstan-assert-if-true TestMethod $this
 * @psalm-assert-if-false null $value
 * @phpstan-assert !null $result
 * @phpstan-assert =ExpectedType $actual
 */",
    );

    assert_eq!(
        document.assertions,
        vec![
            Assertion {
                target: "$this".to_owned(),
                ty: must_parse("TestMethod"),
                when_true: true,
                conditional: true,
                negated: false,
            },
            Assertion {
                target: "$value".to_owned(),
                ty: must_parse("null"),
                when_true: false,
                conditional: true,
                negated: false,
            },
            Assertion {
                target: "$result".to_owned(),
                ty: must_parse("null"),
                negated: true,
                ..Assertion::default()
            },
            Assertion {
                target: "$actual".to_owned(),
                ty: must_parse("ExpectedType"),
                ..Assertion::default()
            },
        ]
    );
}

#[test]
fn variadic_and_reference_param_tags_keep_their_types() {
    let document = parse(
        r"/**
 * @param Entity ...$events
 * @param array<string, mixed> &$metadata
 */",
    );
    assert_eq!(document.params["$events"].text(), "Entity");
    assert_eq!(document.params["$metadata"].text(), "array<string,mixed>");
}

#[test]
fn multiline_shape_tag() {
    let document = parse(
        r"/**
 * @return array{
 *   id: string,
 *   nested?: array<int, string>
 * }
 */",
    );
    assert_eq!(
        document.return_type.text(),
        "array{id:string,nested?:array<int,string>}"
    );
}

#[test]
fn multiline_conditional_return() {
    let document = parse(
        r"/**
 * @return (
 *    T is 'array' ? ArrayNodeDefinition<$this>
 *    : (T is 'variable' ? VariableNodeDefinition<$this>
 *    : (T is 'scalar' ? ScalarNodeDefinition<$this>
 *    : NodeDefinition<$this>))
 * )
 */",
    );
    assert_eq!(
        document.return_type.text(),
        concat!(
            r#"(T is "array" ? ArrayNodeDefinition<static> : "#,
            r#"(T is "variable" ? VariableNodeDefinition<static> : "#,
            r#"(T is "scalar" ? ScalarNodeDefinition<static> : "#,
            "NodeDefinition<static>)))",
        )
    );
}

#[test]
fn streaming_logical_lines_preserve_summary_and_tag_continuation() {
    let document = parse(
        r"/**
 * First summary line.
 * Second summary line.
 * @param string $value ordinary
 *   continued description
 * @return int
 */",
    );

    assert_eq!(document.summary, "First summary line. Second summary line.");
    assert_eq!(document.params["$value"].text(), "string");
    assert_eq!(document.return_type.text(), "int");
    assert!(document.param_tags.is_empty());
}

#[test]
fn templates_with_bounds_and_defaults() {
    let document = parse(
        r"/**
 * @template T
 * @template TKey of array-key = int
 * @phpstan-template TValue as object description
 * @template-contravariant TIn = string
 */",
    );
    let templates = &document.templates;
    assert_eq!(templates.len(), 4);
    assert_eq!(templates[0].name, "T");
    assert_eq!(templates[0].bound, Type::MIXED);
    assert!(templates[0].default.is_unknown());
    assert_eq!(templates[1].bound, Type::ARRAY_KEY);
    assert_eq!(templates[1].default, Type::INT);
    assert_eq!(templates[2].bound, Type::OBJECT);
    assert!(!templates[2].covariant);
    assert!(templates[3].contravariant);
    assert!(!templates[3].covariant);
    assert_eq!(templates[3].bound, Type::MIXED);
    assert_eq!(templates[3].default, Type::STRING);
}

#[test]
fn malformed_types_become_error_and_vars_keep_unnamed_entries() {
    let document = parse(
        r"/**
 * @var array<string
 * @return
 * @internal
 */",
    );
    assert_eq!(document.vars[""], Type::ERROR);
    assert!(document.return_type.is_unknown());
    assert!(document.internal);
}

#[test]
fn summary_stops_after_first_type_tag() {
    let document = parse("/** Summary\n * @return int\n * trailing text */");
    assert_eq!(document.summary, "Summary");
    assert_eq!(document.return_type.text(), "int");
}

#[test]
fn assistant_tags_skip_character_after_empty_marker() {
    assert_eq!(assistant_tags("##Route #Service"), ["Service"]);
    assert_eq!(assistant_tags("#a #A #b_1"), ["a", "b_1"]);
    assert!(assistant_tags("no tags").is_empty());
}

#[test]
fn visit_reference_names_collects_type_and_annotation_names() {
    let mut names = Vec::new();
    visit_reference_names(
        r#"/**
 * Summary mentions Ignored.
 * @param Collection<int, Product>|null $items described by Other
 * @return \App\Result description Text
 * @ORM\Entity(repositoryClass="App\Repository", name='x')
 * @Route("/path", methods={"GET"})
 * @custom-tag Value $var 123abc
 */"#,
        |name| names.push(name.to_owned()),
    );
    assert_eq!(
        names,
        [
            "param",
            "Collection",
            "int",
            "Product",
            "null",
            "return",
            r"\App\Result",
            r"orm\entity",
            "repositoryclass",
            r"app\repository",
            "name",
            "x",
            "route",
            "path",
            "methods",
            "GET",
            "custom",
            "tag",
            "Value",
            "abc",
        ]
    );
}

#[test]
fn visit_reference_names_collects_inline_see_and_link_targets() {
    let mut names = Vec::new();
    visit_reference_names(
        r#"/**
 * Telemetry collaborator for {@see CartCalculator}: times a cart.
 * Decoded as {@see UnknownConditionRule::match()}, see {@link Docs\Page}
 * and {@link https://example.com/docs}; {@see self::run()} is local.
 * {@inheritDoc}
 */"#,
        |name| names.push(name.to_owned()),
    );
    assert_eq!(
        names,
        [
            "CartCalculator",
            "UnknownConditionRule",
            "Docs\\Page",
            "self"
        ]
    );
}
