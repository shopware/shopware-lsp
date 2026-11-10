//! Selection handling of the extraction refactors.

use serde_json::{Value, json};
use swlsp_lsp_protocol::Position;

use super::common::{McpClient, action_titles, path, run, temp_dir, write};
use crate::code_action_selection;

#[test]
fn cli_extract_function_selection_preview() {
    let root = temp_dir();
    let file = path(root.path(), "Live.php");
    let source = "<?php\nfunction run($a,$b) {\n$x=$a+1;\n$y=$b+2;\nreturn [$x,$y];\n}\n";
    write(&file, source);
    let target = format!("{file}:3:1");
    let run = run(&[
        "--root",
        root.path().to_str().unwrap(),
        "--allow-unsupported-project",
        "lsp",
        "codeaction",
        "--end-line",
        "4",
        "--end-column",
        "9",
        "--kind",
        "refactor.extract",
        "--title",
        "^Extract function 'extractedFunction'$",
        "--exec",
        "-d",
        &target,
    ]);
    assert!(run.result.is_ok(), "{:?} {}", run.result, run.err);
    assert!(
        run.out.contains("[$x, $y] = extractedFunction($a, $b);"),
        "{}",
        run.out
    );
    assert_eq!(std::fs::read_to_string(&file).unwrap(), source);
}

#[test]
fn mcp_extract_function_selection_and_all_occurrences() {
    let cases: [(&str, &str, &str, Value); 2] = [
        (
            "<?php\nfunction run($a,$b) {\n$x=$a+1;\n$y=$b+2;\nreturn [$x,$y];\n}\n",
            "Extract function 'extractedFunction'",
            "[$x, $y] = extractedFunction($a, $b);",
            json!({"line": 3, "column": 1, "endLine": 4, "endColumn": 9}),
        ),
        (
            "<?php\n$a=1;\n$x=$a+1;\n$y=$a+1;\n",
            "Extract variable '$extracted' (all 2 occurrences)",
            "$y=$extracted;",
            json!({"line": 3, "column": 4}),
        ),
    ];
    for (source, title, expected, mut args) in cases {
        let root = temp_dir();
        let file = path(root.path(), "Live.php");
        write(&file, source);
        let mut client = McpClient::connect(root.path());
        args["path"] = json!("Live.php");
        args["kind"] = json!("refactor.extract");
        let listed = client.call_ok("shopware_code_actions", args.clone());
        assert!(
            action_titles(&listed).contains(&title.to_owned()),
            "{listed}"
        );
        args["title"] = json!(title);
        client.call_ok("shopware_apply_code_action", args);
        let got = std::fs::read_to_string(&file).unwrap();
        assert!(got.contains(expected), "{title}: {got}");
    }
}

#[test]
fn code_action_selection_boundaries() {
    let start = Position {
        line: 2,
        character: 4,
    };
    assert_eq!(code_action_selection(start, 0, 0).unwrap().end, start);
    assert_eq!(
        code_action_selection(start, 4, 1).unwrap().end,
        Position {
            line: 3,
            character: 0
        }
    );
    for (line, column) in [(-1, 2), (2, 1), (3, 2), (0, -1)] {
        assert!(
            code_action_selection(start, line, column).is_err(),
            "{line}:{column}"
        );
    }
}
