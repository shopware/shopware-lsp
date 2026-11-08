//! Ports of the Go `internal/php/imports` tests.
//!
//! Like Go, each source is bound with the PHP binder.

use swlsp_php_binder::Binder;
use swlsp_php_semantic::Document;

use crate::{Analysis, analyze, organize, remove};

fn never() -> bool {
    false
}

fn analyze_test(source: &str) -> Analysis {
    let parse = swlsp_parser_php::parse(source);
    let root = parse.root();
    assert_eq!(root.text().to_string(), source);
    let document = Binder::new().bind("/live.php", 1, &parse.tree);
    analyze(&never, &root, &document).unwrap()
}

fn unused(analysis: &Analysis) -> Vec<String> {
    analysis
        .scopes
        .iter()
        .flat_map(|scope| &scope.declarations)
        .flat_map(|declaration| &declaration.items)
        .filter(|item| !item.used)
        .map(|item| item.import.alias.clone())
        .collect()
}

#[test]
fn import_usage() {
    let cases: &[(&str, &str, &[&str])] = &[
        (
            "classes and aliases",
            r"<?php use Vendor\Thing as Alias; use Vendor\Unused; new Alias();",
            &["Unused"],
        ),
        (
            "unresolved references",
            r"<?php use Missing\Thing; function run(Thing $a): Thing { return new Thing; }",
            &[],
        ),
        (
            "fully qualified",
            r"<?php use Vendor\Thing; new \Vendor\Thing();",
            &["Thing"],
        ),
        (
            "namespace relative",
            r"<?php namespace App; use App\Thing; new namespace\Thing();",
            &["Thing"],
        ),
        (
            "prefix aliases",
            r"<?php use Vendor\Package as P; P\run(); echo P\VALUE;",
            &[],
        ),
        (
            "function and const",
            r"<?php use function Vendor\same; use const Vendor\same; same();",
            &["same"],
        ),
        (
            "constant usage",
            r"<?php use function Vendor\same; use const Vendor\same; echo same;",
            &["same"],
        ),
        (
            "case sensitivity",
            r"<?php use Vendor\THING; use function Vendor\RUN; use const Vendor\VALUE; new thing; run(); echo value;",
            &["VALUE"],
        ),
        (
            "grouped mixed",
            r"<?php use Vendor\{Thing as T, Other, function run, const FLAG}; new T; run(); echo FLAG;",
            &["Other"],
        ),
        (
            "type attribute trait",
            r"<?php use Vendor\A; use Vendor\B; use Vendor\C; #[A] class T extends B { use C; }",
            &[],
        ),
        (
            "phpdoc",
            r"<?php use Vendor\Type; use Vendor\Collection; /** @return Collection<Type> */ function get() {}",
            &[],
        ),
        (
            "phpdoc annotation",
            r"<?php use Doctrine\ORM\Mapping as ORM; /** @ORM\Entity */ class Entity {}",
            &[],
        ),
        (
            "phpdoc only fully qualified",
            r"<?php use Vendor\Type; /** @return \Vendor\Type */ function get() {}",
            &["Type"],
        ),
        (
            "annotation class string",
            r#"<?php use Vendor\Entity; use Doctrine\ORM\Mapping as ORM; /** @ORM\ManyToOne(targetEntity="Entity") */ class C {}"#,
            &[],
        ),
        (
            "catch and instanceof",
            r"<?php use Vendor\Problem; use Vendor\Thing; try {} catch (Problem $e) {} $is = $e instanceof Thing;",
            &[],
        ),
        (
            "constant defaults",
            r"<?php use const Vendor\VALUE; function f($x = VALUE) { return [VALUE]; }",
            &[],
        ),
        (
            "constant conditions",
            r"<?php use const Vendor\VALUE; if (VALUE) {} while (VALUE) {} switch (VALUE) {} $x = match(VALUE) { default => 1 }; for (;VALUE;) {}",
            &[],
        ),
        (
            "constant label not use",
            r"<?php use const Vendor\label; f(label: 1);",
            &["label"],
        ),
        (
            "plain string",
            r"<?php use Vendor\Thing; echo 'Thing';",
            &["Thing"],
        ),
        (
            "plain comment",
            "<?php use Vendor\\Thing; // Thing\n",
            &["Thing"],
        ),
        (
            "separate namespaces",
            r"<?php namespace A; use Vendor\Thing; new Thing; namespace B; use Vendor\Thing;",
            &["Thing"],
        ),
        (
            "braced namespaces",
            r"<?php namespace A { use Vendor\Thing; } namespace B { use Vendor\Thing; new Thing; }",
            &["Thing"],
        ),
        (
            "closure captures and trait uses",
            r"<?php class T { use MyTrait; } $x=1; $c=function() use ($x) {};",
            &[],
        ),
    ];
    for (name, source, expected) in cases {
        assert_eq!(unused(&analyze_test(source)), *expected, "{name}");
    }
}

#[test]
fn organize_imports() {
    let cases: &[(&str, &str, &str)] = &[
        (
            "sort remove and kinds",
            "<?php\nuse Z\\Zed;\nuse A\\Unused;\nuse const A\\FLAG;\nuse A\\Alpha as A;\nuse function A\\run;\nnew Zed; new A; run(); echo FLAG;\n",
            "<?php\nuse A\\Alpha as A;\nuse Z\\Zed;\nuse function A\\run;\nuse const A\\FLAG;\nnew Zed; new A; run(); echo FLAG;\n",
        ),
        (
            "expand mixed group",
            r"<?php use Vendor\{Other, Thing as T, function run, const FLAG}; new T; run(); echo FLAG;",
            "<?php use Vendor\\Thing as T;\nuse function Vendor\\run;\nuse const Vendor\\FLAG; new T; run(); echo FLAG;",
        ),
        (
            "remove line CRLF",
            "<?php\r\nuse Vendor\\Unused;\r\nclass Live {}\r\n",
            "<?php\r\nclass Live {}\r\n",
        ),
        (
            "comment barrier",
            "<?php\nuse Z\\Zed;\n// keep explanation\nuse A\\Alpha;\nnew Zed; new Alpha;",
            "<?php\nuse Z\\Zed;\n// keep explanation\nuse A\\Alpha;\nnew Zed; new Alpha;",
        ),
        (
            "internal line comment",
            "<?php use Vendor\\{Unused, // keep\n Thing}; new Thing;",
            "<?php use Vendor\\{// keep\nThing}; new Thing;",
        ),
        (
            "internal comments",
            r"<?php use Vendor\{Unused /* keep */, Thing}; new Thing;",
            r"<?php use Vendor\{/* keep */Thing}; new Thing;",
        ),
        (
            "all unused preserve comments",
            r"<?php use /* keep */ Vendor\Unused; echo 1;",
            r"<?php /* keep */ echo 1;",
        ),
        (
            "scoped ordering",
            "<?php namespace A {\n use Z\\Zed;\n use A\\Alpha;\n new Zed; new Alpha;\n}\nnamespace B {\n use Z\\Zed;\n}\n",
            "<?php namespace A {\n use A\\Alpha;\n use Z\\Zed;\n new Zed; new Alpha;\n}\nnamespace B {\n}\n",
        ),
    ];
    for (name, source, want) in cases {
        let analysis = analyze_test(source);
        let edits = organize(&never, source, &analysis).unwrap();
        let result = swlsp_rewrite::apply(source, &edits).unwrap();
        assert_eq!(&result, want, "{name}");
        assert!(swlsp_parser_php::parse(&result).errors.is_empty(), "{name}");
        let again = organize(&never, &result, &analyze_test(&result)).unwrap();
        assert!(again.is_empty(), "{name}: {again:?}");
    }
}

#[test]
fn remove_grouped_import() {
    let source = r"<?php use Vendor\{A, B as Alias, C};";
    for (alias, want) in [
        ("A", r"<?php use Vendor\{B as Alias, C};"),
        ("Alias", r"<?php use Vendor\{A, C};"),
        ("C", r"<?php use Vendor\{A, B as Alias};"),
    ] {
        let analysis = analyze_test(source);
        let edits = remove(source, &analysis.scopes[0].declarations[0], |item| {
            item.import.alias == alias
        })
        .unwrap();
        assert_eq!(
            swlsp_rewrite::apply(source, &edits).unwrap(),
            want,
            "{alias}"
        );
    }
}

#[test]
fn unsupported_declaration_marks_scope_unsafe() {
    let source = r"<?php use Vendor\{A, ; new A;";
    let analysis = analyze_test(source);
    assert!(analysis.scopes[0].unsafe_scope);
    assert!(organize(&never, source, &analysis).unwrap().is_empty());
}

#[test]
fn cancellation_stops_analysis() {
    let source = r"<?php use Vendor\A; new A;";
    let parse = swlsp_parser_php::parse(source);
    let cancelled = || true;
    assert!(analyze(&cancelled, &parse.root(), &Document::default()).is_err());
}

#[test]
fn constant_import_expression_contexts() {
    for expression in [
        "if (VALUE) {}",
        "while (VALUE) {}",
        "switch (VALUE) {}",
        "$x = match(VALUE) { default => 1 };",
        "for (;VALUE;) {}",
        "foreach (VALUE as $v) {}",
        "do {} while (VALUE);",
        "$x = VALUE;",
        "f(VALUE);",
        "class C { const X = VALUE; }",
        "enum C: int { case X = VALUE; }",
        "class C { public $x = VALUE; }",
        "$x = VALUE ? 1 : 0;",
        "$x = [VALUE => 1];",
        "yield VALUE;",
        "$f = fn() => VALUE;",
        "throw VALUE;",
        "$x->{VALUE};",
        "C::{VALUE}();",
    ] {
        let source = format!("<?php use const Vendor\\VALUE; {expression}");
        assert!(unused(&analyze_test(&source)).is_empty(), "{expression}");
    }
}
