//! Ports of the Go `internal/php/rewrite` tests.

use swlsp_parser_php::query::{classes, functions, methods, nodes, parameters, properties};
use swlsp_parser_php::syntax::PHP_FUNCTION_CALL;
use swlsp_syntax::SyntaxNode;

use crate::{Editor, find_phpdoc_annotation};

fn parse(source: &str) -> SyntaxNode {
    let parsed = swlsp_parser_php::parse(source);
    assert!(parsed.errors.is_empty(), "{:?}", parsed.errors);
    parsed.root()
}

fn apply(source: &str, editor: &Editor<'_>) -> String {
    let edits = editor.finish().unwrap();
    let updated = swlsp_rewrite::apply(source, &edits).unwrap();
    assert!(
        swlsp_parser_php::parse(&updated).errors.is_empty(),
        "{updated}"
    );
    updated
}

// ----- editor -----

#[test]
fn class_reference_reuses_existing_aliased_and_grouped_imports() {
    let source = "<?php\n\nnamespace App;\n\nuse Vendor\\Package\\{Existing as Current, Other};\n\nfinal class Demo {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    assert_eq!(
        editor.class_reference(r"Vendor\Package\Existing").unwrap(),
        "Current"
    );
    assert_eq!(
        editor.class_reference(r"Vendor\Package\Other").unwrap(),
        "Other"
    );
    assert_eq!(apply(source, &editor), source);
}

#[test]
fn class_reference_adds_multiple_imports_once() {
    let source = "<?php\n\nnamespace App;\n\nfinal class Demo {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    assert_eq!(editor.class_reference(r"Vendor\One").unwrap(), "One");
    assert_eq!(editor.class_reference(r"Other\Two").unwrap(), "Two");
    assert_eq!(editor.class_reference(r"\Vendor\One").unwrap(), "One");
    assert_eq!(
        apply(source, &editor),
        "<?php\n\nnamespace App;\n\nuse Vendor\\One;\nuse Other\\Two;\n\nfinal class Demo {}"
    );
}

#[test]
fn class_reference_falls_back_for_alias_and_local_class_conflicts() {
    let source = "<?php\n\nnamespace App;\n\nuse Existing\\Service;\n\nfinal class Result {}\nfinal class Demo {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    assert_eq!(
        editor.class_reference(r"Vendor\Service").unwrap(),
        r"\Vendor\Service"
    );
    assert_eq!(
        editor.class_reference(r"Vendor\Result").unwrap(),
        r"\Vendor\Result"
    );
    assert_eq!(apply(source, &editor), source);
}

#[test]
fn class_reference_uses_same_namespace_without_import() {
    let source = r"<?php namespace App\Domain; final class Demo {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    assert_eq!(
        editor.class_reference(r"App\Domain\Service").unwrap(),
        "Service"
    );
    assert_eq!(apply(source, &editor), source);
}

#[test]
fn class_reference_without_namespace_and_root() {
    let source = "<?php\nfinal class Demo {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    assert_eq!(editor.class_reference(r"Vendor\One").unwrap(), "One");
    assert_eq!(
        apply(source, &editor),
        "<?php\n\nuse Vendor\\One;\nfinal class Demo {}"
    );
    let mut detached = Editor::new(source, None);
    assert_eq!(
        detached.class_reference(r"Vendor\One").unwrap(),
        r"\Vendor\One"
    );
    assert!(detached.class_reference("  ").is_err());
}

// ----- types -----

#[test]
fn native_type_edits_compose() {
    let source = "<?php\nclass Demo\n{\n    private $value;\n\n    public function convert($input): string {}\n}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let class = &classes(&root)[0];
    let method = &methods(class)[0];
    let parameter = parameters(method).next().unwrap();
    editor.set_parameter_type(&parameter, "array").unwrap();
    editor
        .set_property_type(&properties(class)[0], "mixed")
        .unwrap();
    editor.set_return_type(method, "\\Traversable").unwrap();
    assert_eq!(
        apply(source, &editor),
        "<?php\nclass Demo\n{\n    private mixed $value;\n\n    public function convert(array $input): \\Traversable {}\n}"
    );
}

#[test]
fn native_type_edits_replace_composite_types() {
    let source = "<?php\nfunction convert(string|int $input): ?string {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let function = &functions(&root)[0];
    let parameter = parameters(function).next().unwrap();
    editor.set_parameter_type(&parameter, "?array").unwrap();
    editor.set_return_type(function, "bool").unwrap();
    assert_eq!(
        apply(source, &editor),
        "<?php\nfunction convert(?array $input): bool {}"
    );
}

#[test]
fn native_type_edits_add_missing_return_and_reject_invalid_input() {
    let source = "<?php\nfunction convert($input) {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let function = &functions(&root)[0];
    assert!(editor.set_return_type(function, " ").is_err());
    assert!(editor.set_parameter_type(function, "int").is_err());
    editor.set_return_type(function, "void").unwrap();
    assert_eq!(
        apply(source, &editor),
        "<?php\nfunction convert($input): void {}"
    );
}

// ----- phpdoc -----

#[test]
fn phpdoc_annotation_replace_and_remove() {
    let source = r#"<?php
final class Controller
{
    /**
     * Description.
     * @Route(
     *     "/demo",
     *     defaults={"value"=")"}
     * )
     * @Captcha
     */
    public function demo(): void {}
}"#;
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let method = &methods(&classes(&root)[0])[0];
    assert!(
        editor
            .replace_phpdoc_annotation(
                method,
                "Route",
                r#"@Route("/demo", defaults={"_captcha"=true})"#
            )
            .unwrap()
    );
    assert!(editor.remove_phpdoc_annotation(method, "Captcha").unwrap());
    assert_eq!(
        apply(source, &editor),
        r#"<?php
final class Controller
{
    /**
     * Description.
     * @Route("/demo", defaults={"_captcha"=true})
     */
    public function demo(): void {}
}"#
    );
}

#[test]
fn phpdoc_annotation_supports_qualified_names() {
    let source = r#"<?php
/** @not-an-annotation */
final class Controller
{
    /**
     * @Shopware\Storefront\Framework\Routing\Annotation\RouteScope(scopes={"storefront"})
     */
    public function demo(): void {}
}"#;
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let method = &methods(&classes(&root)[0])[0];
    let annotation = find_phpdoc_annotation(method, "RouteScope").unwrap();
    assert_eq!(
        annotation.text,
        r#"@Shopware\Storefront\Framework\Routing\Annotation\RouteScope(scopes={"storefront"})"#
    );
    assert_eq!(&source[annotation.range], annotation.text);
    assert!(find_phpdoc_annotation(method, r"Other\RouteScope").is_none());
    assert!(
        editor
            .remove_phpdoc_annotation(method, "RouteScope")
            .unwrap()
    );
    assert_eq!(
        apply(source, &editor),
        "<?php\n/** @not-an-annotation */\nfinal class Controller\n{\n    /**\n     */\n    public function demo(): void {}\n}"
    );
}

#[test]
fn phpdoc_annotation_rejects_malformed_and_inline_annotations() {
    for source in [
        "<?php /**\n * @Route(defaults={\"broken\"=true}\n */\nfunction demo(): void {}",
        "<?php /**\n * @Route(\"/demo\") trailing text\n */\nfunction demo(): void {}",
    ] {
        let root = parse(source);
        let mut editor = Editor::new(source, Some(&root));
        let function = &functions(&root)[0];
        assert!(
            !editor
                .replace_phpdoc_annotation(function, "Route", r#"@Route("/new")"#)
                .unwrap()
        );
        assert_eq!(apply(source, &editor), source);
    }
}

// ----- lists -----

type ListEdit = fn(&mut Editor<'_>, &SyntaxNode) -> crate::editor::Result<()>;

#[test]
fn argument_list_insert_append_and_remove() {
    let cases: &[(&str, &str, ListEdit, &str)] = &[
        (
            "insert middle",
            "<?php foo($first, named: $last);",
            |e, c| e.insert_argument(c, 1, "$middle"),
            "<?php foo($first, $middle, named: $last);",
        ),
        (
            "append after trailing comma",
            "<?php foo($first,);",
            |e, c| e.append_argument(c, "$last"),
            "<?php foo($first, $last);",
        ),
        (
            "comment comma is not a separator",
            "<?php foo($first /* comma, in comment */);",
            |e, c| e.append_argument(c, "$last"),
            "<?php foo($first /* comma, in comment */, $last);",
        ),
        (
            "append empty",
            "<?php foo();",
            |e, c| e.append_argument(c, "$only"),
            "<?php foo($only);",
        ),
        (
            "append multiline",
            "<?php\nfoo(\n    $first\n);",
            |e, c| e.append_argument(c, "$last"),
            "<?php\nfoo(\n    $first,\n    $last,\n);",
        ),
        (
            "insert multiline first",
            "<?php\nfoo(\n    $last,\n);",
            |e, c| e.insert_argument(c, 0, "$first"),
            "<?php\nfoo(\n    $first,\n    $last,\n);",
        ),
        (
            "remove middle",
            "<?php foo($first, $middle, $last);",
            |e, c| e.remove_argument(c, 1),
            "<?php foo($first, $last);",
        ),
        (
            "remove last",
            "<?php foo($first, $last);",
            |e, c| e.remove_argument(c, 1),
            "<?php foo($first);",
        ),
        (
            "remove multiline last",
            "<?php\nfoo(\n    $first,\n    $last,\n);",
            |e, c| e.remove_argument(c, 1),
            "<?php\nfoo(\n    $first,\n);",
        ),
        (
            "remove only",
            "<?php foo($only);",
            |e, c| e.remove_argument(c, 0),
            "<?php foo();",
        ),
    ];
    for (name, source, edit, expected) in cases {
        let root = parse(source);
        let mut editor = Editor::new(source, Some(&root));
        let calls = nodes(&root, &[PHP_FUNCTION_CALL]);
        assert_eq!(calls.len(), 1, "{name}");
        edit(&mut editor, &calls[0]).unwrap();
        assert_eq!(&apply(source, &editor), expected, "{name}");
    }
}

#[test]
fn parameter_list_insert_append_and_remove() {
    let source = "<?php\nfinal class Handler\n{\n    public function __construct(\n        private Repository $repository,\n        bool $strict = false,\n    ) {}\n}";
    let cases: &[(&str, ListEdit, &str)] = &[
        (
            "insert",
            |e, m| e.insert_parameter(m, 1, "LoggerInterface $logger"),
            "<?php\nfinal class Handler\n{\n    public function __construct(\n        private Repository $repository,\n        LoggerInterface $logger,\n        bool $strict = false,\n    ) {}\n}",
        ),
        (
            "remove",
            |e, m| e.remove_parameter(m, 1),
            "<?php\nfinal class Handler\n{\n    public function __construct(\n        private Repository $repository,\n    ) {}\n}",
        ),
        (
            "append",
            |e, m| e.append_parameter(m, "array $options = []"),
            "<?php\nfinal class Handler\n{\n    public function __construct(\n        private Repository $repository,\n        bool $strict = false,\n        array $options = [],\n    ) {}\n}",
        ),
    ];
    for (name, edit, expected) in cases {
        let root = parse(source);
        let mut editor = Editor::new(source, Some(&root));
        let method = &methods(&classes(&root)[0])[0];
        edit(&mut editor, method).unwrap();
        assert_eq!(&apply(source, &editor), expected, "{name}");
    }
}

#[test]
fn comma_list_edits_reject_invalid_input() {
    let source = "<?php foo($value);";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let call = &nodes(&root, &[PHP_FUNCTION_CALL])[0];
    assert!(editor.insert_argument(call, 2, "$other").is_err());
    assert!(editor.append_argument(call, " ").is_err());
    assert!(editor.remove_argument(call, 1).is_err());

    let commented = "<?php foo($first, /* belongs to last */ $last);";
    let root = parse(commented);
    let mut editor = Editor::new(commented, Some(&root));
    let call = &nodes(&root, &[PHP_FUNCTION_CALL])[0];
    assert!(editor.insert_argument(call, 1, "$middle").is_err());
    assert!(editor.remove_argument(call, 0).is_err());
}

// ----- classes -----

#[test]
fn class_inheritance_edits_compose() {
    let source = "<?php\n\nfinal class Demo implements OldContract, KeepContract {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let class = &classes(&root)[0];
    editor.set_extends(class, "AbstractDemo").unwrap();
    assert!(editor.remove_implements(class, "OldContract").unwrap());
    editor.add_implements(class, "ExtraContract").unwrap();
    editor.add_implements(class, "\\KeepContract").unwrap();
    assert_eq!(
        apply(source, &editor),
        "<?php\n\nfinal class Demo extends AbstractDemo implements KeepContract, ExtraContract {}"
    );
}

#[test]
fn set_extends_replaces_existing_parent() {
    let source = "<?php class Demo extends Legacy {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    editor.set_extends(&classes(&root)[0], "Modern").unwrap();
    assert_eq!(apply(source, &editor), "<?php class Demo extends Modern {}");
}

#[test]
fn remove_extends_and_add_attribute() {
    let source = "<?php\n/** Handler. */\nclass Demo extends Legacy implements Existing {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let class = &classes(&root)[0];
    assert!(editor.remove_extends(class).unwrap());
    editor.add_attribute(class, "AsHandler").unwrap();
    assert_eq!(
        apply(source, &editor),
        "<?php\n/** Handler. */\n#[AsHandler]\nclass Demo implements Existing {}"
    );
}

#[test]
fn remove_only_implemented_interface() {
    let source = "<?php class Demo implements Contract {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    assert!(
        editor
            .remove_implements(&classes(&root)[0], "Contract")
            .unwrap()
    );
    assert!(
        !editor
            .remove_implements(&classes(&root)[0], "Missing")
            .unwrap()
    );
    assert_eq!(apply(source, &editor), "<?php class Demo {}");
}

#[test]
fn inheritance_edit_rejects_commented_clause() {
    let source = "<?php class Demo implements /* keep */ Contract {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let class = &classes(&root)[0];
    assert!(editor.add_implements(class, "Other").is_err());
    assert!(editor.remove_implements(class, "Contract").is_err());
}

#[test]
fn insert_class_member_formats_empty_and_populated_classes() {
    let source = "<?php final class Demo {}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    editor
        .insert_class_member(
            &classes(&root)[0],
            "\npublic function value(): string\n{\n    return 'value';\n}",
        )
        .unwrap();
    assert_eq!(
        apply(source, &editor),
        "<?php final class Demo {\n    public function value(): string\n    {\n        return 'value';\n    }\n}"
    );

    let source = "<?php\nfinal class Demo\n{\n\tprivate string $name;\n}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    editor
        .insert_class_member(
            &classes(&root)[0],
            "public function name(): string\n{\n    return $this->name;\n}",
        )
        .unwrap();
    assert_eq!(
        apply(source, &editor),
        "<?php\nfinal class Demo\n{\n\tprivate string $name;\n\tpublic function name(): string\n\t{\n\t    return $this->name;\n\t}\n}"
    );
}

#[test]
fn remove_class_member_includes_owned_phpdoc() {
    let source = "<?php\nfinal class Demo\n{\n    private string $keep;\n\n    /** Remove this method. */\n    public function legacy(): void\n    {\n    }\n\n    public function keep(): void\n    {\n    }\n}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let methods = methods(&classes(&root)[0]);
    assert_eq!(methods.len(), 2);
    editor.remove_class_member(&methods[0]).unwrap();
    assert_eq!(
        apply(source, &editor),
        "<?php\nfinal class Demo\n{\n    private string $keep;\n\n    public function keep(): void\n    {\n    }\n}"
    );
}

#[test]
fn remove_last_class_member_up_to_closing_brace() {
    let source = "<?php\nfinal class Demo\n{\n    private string $keep;\n\n    public function legacy(): void {}\n}";
    let root = parse(source);
    let mut editor = Editor::new(source, Some(&root));
    let method = &methods(&classes(&root)[0])[0];
    editor.remove_class_member(method).unwrap();
    assert_eq!(
        apply(source, &editor),
        "<?php\nfinal class Demo\n{\n    private string $keep;\n\n}"
    );
    assert!(editor.remove_class_member(&classes(&root)[0]).is_err());
}
