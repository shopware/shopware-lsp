use swlsp_parser_php::query;
use swlsp_syntax::{SyntaxNode, TextRange};

use super::*;

const NEVER: Cancelled<'static> = &|| false;
const ALWAYS: Cancelled<'static> = &|| true;

fn parse(source: &str) -> swlsp_syntax::Parse {
    swlsp_parser_php::parse(source)
}

fn range(start: usize, end: usize) -> TextRange {
    TextRange::new((start as u32).into(), (end as u32).into())
}

fn apply(source: &str, extraction: &Extraction) -> String {
    swlsp_rewrite::apply(source, &extraction.edits).unwrap()
}

fn method_fixture(body: &str) -> String {
    format!("<?php\nclass Demo\n{{\n    public function run($a, $b)\n    {{\n{body}\n    }}\n}}\n")
}

fn extract_method_test(source: &str, selected: &str) -> (Option<Extraction>, String) {
    let parsed = parse(source);
    assert_eq!(parsed.tree.source(), source);
    assert!(parsed.errors.is_empty(), "{:?}", parsed.errors);
    let start = source.find(selected).expect("selection present");
    let result = extract_method(
        &parsed.root(),
        source,
        range(start, start + selected.len()),
        None,
        NEVER,
    )
    .unwrap();
    let Some(result) = result else {
        return (None, String::new());
    };
    let got = apply(source, &result);
    assert!(parse(&got).errors.is_empty(), "{got}");
    (Some(result), got)
}

fn returns_value(_: &SyntaxNode) -> bool {
    true
}

// -- extract_variable_test.go ----------------------------------------------------

#[test]
fn extract_variable_cases() {
    let cases = [
        (
            "assignment",
            "<?php\n$x = build();",
            "build()",
            "<?php\n$extracted = build();\n$x = $extracted;",
        ),
        (
            "return and indent",
            "<?php function f() {\n    return build();\n}",
            "build()",
            "<?php function f() {\n    $extracted = build();\n    return $extracted;\n}",
        ),
        (
            "first operand",
            "<?php\nreturn build() + other();",
            "build()",
            "<?php\n$extracted = build();\nreturn $extracted + other();",
        ),
        (
            "whole expression",
            "<?php\nreturn first() + second();",
            "first() + second()",
            "<?php\n$extracted = first() + second();\nreturn $extracted;",
        ),
        (
            "collision",
            "<?php function f($extracted) { $extracted2 = 1; return build(); }",
            "build()",
            "<?php function f($extracted) { $extracted2 = 1; $extracted3 = build();\nreturn $extracted3; }",
        ),
        (
            "CRLF",
            "<?php\r\n$x = build();",
            "build()",
            "<?php\r\n$extracted = build();\r\n$x = $extracted;",
        ),
        (
            "comments",
            "<?php\n// explanation\nreturn build(/* keep */);",
            "build(/* keep */)",
            "<?php\n// explanation\n$extracted = build(/* keep */);\nreturn $extracted;",
        ),
        (
            "standalone",
            "<?php\nbuild();",
            "build()",
            "<?php\n$extracted = build();\n$extracted;",
        ),
    ];
    for (name, source, selected, want) in cases {
        let tree = parse(source);
        let start = source.find(selected).unwrap();
        let result = extract_variable(
            &tree.root(),
            source,
            range(start, start + selected.len()),
            VariableOptions::default(),
            NEVER,
        )
        .unwrap()
        .unwrap_or_else(|| panic!("{name}: unavailable"));
        let got = apply(source, &result);
        assert_eq!(got, want, "{name}");
        assert!(parse(&got).errors.is_empty(), "{name}");
    }
}

#[test]
fn extract_variable_rejects_unsafe_contexts() {
    for source in [
        "<?php return SELECTED() ?? 1;",
        "<?php consume(SELECTED());",
        "<?php $x =& SELECTED();",
        "<?php function &f() { return SELECTED(); }",
        "<?php class C { public mixed $p { &get { return SELECTED(); } } }",
        "<?php return (yield SELECTED());",
        "<?php $x = get_defined_vars(); return SELECTED();",
        "<?php $$name = 1; return SELECTED();",
    ] {
        let tree = parse(source);
        let start = source.find("SELECTED()").unwrap();
        let result = extract_variable(
            &tree.root(),
            source,
            range(start, start + 10),
            VariableOptions::default(),
            NEVER,
        )
        .unwrap();
        assert!(result.is_none(), "{source}");
    }
}

#[test]
fn extract_variable_caret_and_cancellation() {
    let source = "<?php return build();";
    let tree = parse(source);
    let result = extract_variable(
        &tree.root(),
        source,
        range(15, 15),
        VariableOptions::default(),
        NEVER,
    )
    .unwrap();
    assert!(result.is_some());
    let error = extract_variable(
        &tree.root(),
        source,
        range(15, 15),
        VariableOptions::default(),
        ALWAYS,
    )
    .unwrap_err();
    assert_eq!(error, RefactorError::Cancelled);
}

// -- extraction_expanded_test.go -------------------------------------------------

#[test]
fn extract_variable_preserves_evaluation_point() {
    for source in [
        "<?php return first() + SELECTED();",
        "<?php return $a && SELECTED();",
        "<?php return $a ? SELECTED() : 1;",
        "<?php return $a ?? SELECTED();",
        "<?php while (SELECTED()) {}",
        "<?php for (;SELECTED();) {}",
        "<?php if ($a) return SELECTED();",
        "<?php $a[other()] = SELECTED();",
        "<?php $f = fn() => SELECTED();",
        "<?php consume(first(), SELECTED(), last());",
    ] {
        let tree = parse(source);
        let start = source.find("SELECTED()").unwrap();
        let options = VariableOptions {
            call_returns_value: Some(&returns_value),
            all_occurrences: false,
        };
        let result = extract_variable(
            &tree.root(),
            source,
            range(start, start + 10),
            options,
            NEVER,
        )
        .unwrap()
        .unwrap_or_else(|| panic!("{source}: unavailable"));
        let got = apply(source, &result);
        assert_eq!(
            got,
            source.replacen("SELECTED()", "($extracted = SELECTED())", 1),
            "{source}"
        );
        assert!(parse(&got).errors.is_empty(), "{source}");
    }
}

#[test]
fn extract_variable_all_occurrences_and_mutation_barriers() {
    let cases = [
        ("<?php\n$x = $a + 1;\n$y = $a+1;\nreturn $a + 1;", 3),
        ("<?php\n$x = $a + 1;\n$a = 2;\n$y = $a + 1;", 0),
        ("<?php\n$x = $a + 1;\nmutate($a);\n$y = $a + 1;", 0),
        ("<?php\n$x = $a + 1;\nif ($a) { $y = $a + 1; }", 0),
        ("<?php\n$x = ($a + 1) + ($a + 1);", 2),
        ("<?php consume($a + 1, $a+1);", 2),
        ("<?php consume($a && ($a + 1), $a+1);", 0),
        ("<?php $x = $a + 1; $y = $a /* keep */ + 1;", 0),
    ];
    for (source, count) in cases {
        let tree = parse(source);
        let start = source.find("$a + 1").unwrap();
        let options = VariableOptions {
            call_returns_value: None,
            all_occurrences: true,
        };
        let result = extract_variable(
            &tree.root(),
            source,
            range(start, start + 6),
            options,
            NEVER,
        )
        .unwrap();
        if count == 0 {
            assert!(result.is_none(), "{source}");
            continue;
        }
        let result = result.unwrap_or_else(|| panic!("{source}: unavailable"));
        assert_eq!(result.occurrences, count, "{source}");
        let got = apply(source, &result);
        let mut expected_mentions = count + 1;
        if source.contains("consume") {
            expected_mentions -= 1;
        }
        assert_eq!(
            got.matches("$extracted").count(),
            expected_mentions,
            "{source}"
        );
        assert!(parse(&got).errors.is_empty(), "{source}");
    }
}

#[test]
fn extract_method_structured_flow_and_multiple_outputs() {
    let cases = [
        (
            "if ($a) { echo $b; }",
            "if ($a) { echo $b; }",
            "$this->extractedMethod($a, $b);",
        ),
        (
            "if ($a) { echo $b; }",
            "echo $b;",
            "$this->extractedMethod($b);",
        ),
        (
            "$x = $a; $y = $b; echo $x; echo $y;",
            "$x = $a; $y = $b;",
            "[$x, $y] = $this->extractedMethod($a, $b);",
        ),
        (
            "if ($a) { $x = 1; } else { $x = 2; } echo $x;",
            "if ($a) { $x = 1; } else { $x = 2; }",
            "$x = $this->extractedMethod($a);",
        ),
        (
            "if ($a) { $b = 1; } echo $b;",
            "if ($a) { $b = 1; }",
            "$b = $this->extractedMethod($a, $b);",
        ),
        (
            "$sum = 0; foreach ($a as $item) { $sum += $item; } echo $sum;",
            "foreach ($a as $item) { $sum += $item; }",
            "$sum = $this->extractedMethod($a, $sum);",
        ),
        (
            "foreach ($a as $item) { echo $item; }",
            "echo $item;",
            "$this->extractedMethod($item);",
        ),
        (
            "$sum = 0; for ($i = 0; $i < $a; $i++) { $sum += $i; } echo $sum;",
            "for ($i = 0; $i < $a; $i++) { $sum += $i; }",
            "$sum = $this->extractedMethod($a, $sum);",
        ),
        (
            "while ($a) { $a--; if ($a == 1) break; } echo $a;",
            "while ($a) { $a--; if ($a == 1) break; }",
            "$a = $this->extractedMethod($a);",
        ),
        (
            "if ($a) { return 1; } else { return 2; }",
            "if ($a) { return 1; } else { return 2; }",
            "return $this->extractedMethod($a);",
        ),
        (
            "echo \"Hello $a\";",
            "echo \"Hello $a\";",
            "$this->extractedMethod($a);",
        ),
        (
            "$a[0] = 1;",
            "$a[0] = 1;",
            "$a = $this->extractedMethod($a);",
        ),
        (
            "$this->value = $a;",
            "$this->value = $a;",
            "$this->extractedMethod($a);",
        ),
    ];
    for (body, selected, call) in cases {
        let (result, got) =
            extract_method_test(&method_fixture(&format!("        {body}")), selected);
        assert!(result.is_some(), "{selected}");
        assert!(got.contains(call), "{selected}: {got}");
    }
}

#[test]
fn extract_method_keeps_unsafe_boundary_unavailable() {
    for (body, selected) in [
        ("if ($a) { $x = 1; } echo $x;", "if ($a) { $x = 1; }"),
        ("while ($a) { break; }", "break;"),
        ("while ($a) { continue 2; }", "while ($a) { continue 2; }"),
        ("$fn = fn() => $a; echo $b;", "$fn = fn() => $a;"),
        (
            "$x = new Trace; if ($a) return; echo $b;",
            "$x = new Trace; if ($a) return;",
        ),
    ] {
        let (result, _) =
            extract_method_test(&method_fixture(&format!("        {body}")), selected);
        assert!(result.is_none(), "{selected}");
    }
}

#[test]
fn extract_function_references_and_literal_contents() {
    let source = "<?php namespace Demo;\nfunction run(&$a) {\n    $a++;\n    echo \"first\n  second $a\";\n}\n";
    let selected = "$a++;\n    echo \"first\n  second $a\";";
    let (result, got) = extract_method_test(source, selected);
    let result = result.unwrap();
    assert_eq!(result.name, "extractedFunction");
    assert!(got.contains("function extractedFunction(&$a)"), "{got}");
    assert!(got.contains("\"first\n  second $a\""), "{got}");
    assert!(got.contains("$a = extractedFunction($a);"), "{got}");
    assert!(!got.contains("private function"), "{got}");
}

#[test]
fn extract_method_early_returns_many() {
    let body = "        if ($a) { return 1; }\n".repeat(100) + "        echo $b;";
    let source = method_fixture(&format!("{body}\n        return 0;"));
    let tree = parse(&source);
    let start = source.find("if ($a)").unwrap();
    let end = source.find("echo $b;").unwrap() + "echo $b;".len();
    let result = extract_method(&tree.root(), &source, range(start, end), None, NEVER)
        .unwrap()
        .unwrap();
    let got = apply(&source, &result);
    assert!(parse(&got).errors.is_empty(), "{got}");
    assert_eq!(got.matches(" [1, 1]").count(), 100, "{got}");
}

// -- extract_method_test.go ------------------------------------------------------

#[test]
fn extract_method_inputs_and_live_output() {
    let selected = "$sum = $a + $b;\n        $result = $sum * 2;";
    let source = method_fixture(&format!("        {selected}\n        return $result;"));
    let (result, got) = extract_method_test(&source, selected);
    assert!(result.is_some());
    let caller = method_fixture(
        "        $result = $this->extractedMethod($a, $b);\n        return $result;",
    );
    let expected = format!(
        "{}\n\n    private function extractedMethod($a, $b)\n    {{\n        $sum = $a + $b;\n        $result = $sum * 2;\n        return $result;\n    }}\n}}\n",
        &caller[..caller.rfind("\n}").unwrap()]
    );
    assert_eq!(got, expected);
}

#[test]
fn extract_method_returns_static_and_names() {
    let cases = [
        (
            "return",
            method_fixture("        return $a + $b;"),
            "return $a + $b;",
            "return $this->extractedMethod($a, $b);",
            "private function extractedMethod($a, $b)",
        ),
        (
            "static",
            method_fixture("        return $a + 1;").replacen(
                "public function",
                "public static function",
                1,
            ),
            "return $a + 1;",
            "return self::extractedMethod($a);",
            "private static function extractedMethod($a)",
        ),
        (
            "dynamic call input",
            method_fixture("        return $this->$a();"),
            "return $this->$a();",
            "return $this->extractedMethod($a);",
            "private function extractedMethod($a)",
        ),
        (
            "this",
            method_fixture("        return $this->calculate();"),
            "return $this->calculate();",
            "return $this->extractedMethod();",
            "private function extractedMethod()",
        ),
        (
            "bare return",
            method_fixture("        echo $a;\n        return;"),
            "echo $a;\n        return;",
            "$this->extractedMethod($a);\n        return;",
            "private function extractedMethod($a)",
        ),
        (
            "existing name",
            method_fixture("        echo $a;").replacen(
                "class Demo",
                "/* extractedMETHOD */ class Demo",
                1,
            ),
            "echo $a;",
            "$this->extractedMethod2($a);",
            "private function extractedMethod2($a)",
        ),
        (
            "prefix local",
            method_fixture("        $local = 1;\n        echo $local;"),
            "echo $local;",
            "$this->extractedMethod($local);",
            "private function extractedMethod($local)",
        ),
        (
            "reference-call output",
            method_fixture("        mutate($a);\n        return $a;"),
            "mutate($a);",
            "$a = $this->extractedMethod($a);",
            "private function extractedMethod(&$a)",
        ),
    ];
    for (name, source, selected, call, signature) in cases {
        let (result, got) = extract_method_test(&source, selected);
        assert!(result.is_some(), "{name}");
        assert!(got.contains(call), "{name}: {got}");
        assert!(got.contains(signature), "{name}: {got}");
    }
}

#[test]
fn extract_method_rejects_unsafe_selections() {
    for (body, selected) in [
        ("return $a; echo $b;", "return $a; echo $b;"),
        ("echo $unknown;", "echo $unknown;"),
        ("$x = $a + $b;", "$a + $b"),
        ("echo __METHOD__;", "echo __METHOD__;"),
        ("global $a; echo $a;", "echo $a;"),
        ("echo $a; eval($b);", "echo $a;"),
    ] {
        let (result, _) =
            extract_method_test(&method_fixture(&format!("        {body}")), selected);
        assert!(result.is_none(), "{body}");
    }
}

#[test]
fn extract_method_caret_comments_crlf_and_cancellation() {
    let source = method_fixture("        $x = /* keep */ $a + $b;\n        return $x;")
        .replace('\n', "\r\n");
    let parsed = parse(&source);
    let start = source.find("$x =").unwrap() + 3;
    let available = |_: Option<&SyntaxNode>, name: &str| name != "extractedMethod";
    let result = extract_method(
        &parsed.root(),
        &source,
        range(start, start),
        Some(&available),
        NEVER,
    )
    .unwrap()
    .unwrap();
    assert_eq!(result.name, "extractedMethod2");
    let got = apply(&source, &result);
    assert!(got.contains("/* keep */"));
    assert!(!got.replace("\r\n", "").contains('\n'));
    let reparsed = parse(&got);
    assert_eq!(
        query::methods(&query::classes(&reparsed.root())[0]).len(),
        2
    );
    let error =
        extract_method(&parsed.root(), &source, TextRange::default(), None, ALWAYS).unwrap_err();
    assert_eq!(error, RefactorError::Cancelled);
}

#[test]
fn extract_method_caret_on_large_body() {
    let source =
        method_fixture(&("        $local = 1;\n".repeat(1000) + "        return $a + $b;"));
    let parsed = parse(&source);
    let start = source.rfind("return").unwrap();
    let result = extract_method(&parsed.root(), &source, range(start, start), None, NEVER).unwrap();
    assert!(result.is_some());
}

// -- extraction_runtime_test.go --------------------------------------------------

/// Optional execution checks supplement native-CST tests without making PHP
/// a build prerequisite.
#[test]
fn extraction_preserves_php_execution() {
    use std::io::Write;
    use std::process::{Command, Stdio};

    if Command::new("php").arg("--version").output().is_err() {
        eprintln!("PHP executable unavailable");
        return;
    }
    let run = |source: &str| -> String {
        let mut child = Command::new("php")
            .args(["-n", "-d", "display_errors=1", "-d", "error_reporting=-1"])
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        child
            .stdin
            .take()
            .unwrap()
            .write_all(source.as_bytes())
            .unwrap();
        let output = child.wait_with_output().unwrap();
        let mut text = String::from_utf8_lossy(&output.stdout).into_owned();
        text.push_str(&String::from_utf8_lossy(&output.stderr));
        assert!(output.status.success(), "{text}");
        text
    };
    let cases: &[(&str, &str, &str, bool)] = &[
        (
            "argument order",
            "<?php $log=[]; function step($n){global $log;$log[]=$n;return $n;} function useValues($a,$b,$c){global $log;echo json_encode($log);} useValues(step(1), step(2), step(3));",
            "step(2)",
            true,
        ),
        (
            "short circuit",
            "<?php $n=0; function step(){global $n;$n++;return true;} $x=false && step(); echo $n;",
            "step()",
            true,
        ),
        (
            "loop frequency",
            "<?php $n=0; function step(){global $n;return $n++ < 3;} while(step()) {} echo $n;",
            "step()",
            true,
        ),
        (
            "retained object",
            "<?php class Trace { function __destruct(){echo 'd';} } function run(){ $x=new Trace; echo 'a'; echo 'b'; } run();",
            "$x=new Trace; echo 'a';",
            false,
        ),
        (
            "reassigned input",
            "<?php class Trace { function __destruct(){echo 'd';} } function run($a){ $a=1; echo 'a'; echo 'b'; } run(new Trace);",
            "$a=1; echo 'a';",
            false,
        ),
        (
            "multiple outputs",
            "<?php function run($a,$b){ $x=$a+1; $y=$b+2; return [$x,$y]; } echo json_encode(run(2,3));",
            "$x=$a+1; $y=$b+2;",
            false,
        ),
        (
            "condition assignment",
            "<?php function run($a){ if($x=$a){echo $x;}else{echo $x;} return $x; } echo run(0),run(2);",
            "if($x=$a){echo $x;}else{echo $x;}",
            false,
        ),
        (
            "nested branch",
            "<?php function run($a){ if($a){$x=1;}else{$x=2;} return $x; } echo run(true), run(false);",
            "if($a){$x=1;}else{$x=2;}",
            false,
        ),
        (
            "foreach",
            "<?php function run($items){ $sum=0; foreach($items as $item){$sum += $item;} return $sum; } echo run([]), run([1,2,3]);",
            "foreach($items as $item){$sum += $item;}",
            false,
        ),
        (
            "for",
            "<?php function run($n){ $sum=0; for($i=0;$i<$n;$i++){$sum += $i;} return $sum; } echo run(0), run(4);",
            "for($i=0;$i<$n;$i++){$sum += $i;}",
            false,
        ),
        (
            "references",
            "<?php function run(&$a,&$b){ $a=7; echo $b; } $v=1; run($v,$v); echo $v;",
            "$a=7; echo $b;",
            false,
        ),
        (
            "literal",
            "<?php function run($a){ echo \"first\n  second $a\"; } run(3);",
            "echo \"first\n  second $a\";",
            false,
        ),
        (
            "early return value",
            "<?php function run($a){ if($a) return null; $x=3; return $x; } echo json_encode([run(true),run(false)]);",
            "if($a) return null; $x=3;",
            false,
        ),
        (
            "early return void",
            "<?php function run($a):void { if($a) return; echo 'a'; echo 'b'; } run(true);run(false);",
            "if($a) return; echo 'a';",
            false,
        ),
        (
            "early loop return",
            "<?php function run($a){ foreach($a as $v){if($v>1)return $v;} return 0; } echo run([]),run([0,2]);",
            "foreach($a as $v){if($v>1)return $v;}",
            false,
        ),
        (
            "switch",
            "<?php function run($a){ switch($a){case 1:$x=1;break;default:$x=2;} return $x; } echo run(1),run(2);",
            "switch($a){case 1:$x=1;break;default:$x=2;}",
            false,
        ),
    ];
    for (name, source, selected, variable) in cases {
        let tree = parse(source);
        let start = source.rfind(selected).unwrap();
        let selection = range(start, start + selected.len());
        let result = if *variable {
            let options = VariableOptions {
                call_returns_value: Some(&returns_value),
                all_occurrences: false,
            };
            extract_variable(&tree.root(), source, selection, options, NEVER)
        } else {
            extract_method(&tree.root(), source, selection, None, NEVER)
        }
        .unwrap()
        .unwrap_or_else(|| panic!("{name}: unavailable"));
        let changed = apply(source, &result);
        assert_eq!(run(source), run(&changed), "{name}: {changed}");
    }
}
