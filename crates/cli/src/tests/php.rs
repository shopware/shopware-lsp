//! PHP refactor commands: extract method, extract variable and imports.

use serde_json::json;

use super::common::{McpClient, action_titles, path, temp_dir, write};

#[test]
fn mcp_php_extract_method_uses_production_edits() {
    let root = temp_dir();
    let file = path(root.path(), "Live.php");
    let source = "<?php\nclass Demo { public function run($a) {\nreturn $a;\n} }\n";
    write(&file, source);
    let mut client = McpClient::connect(root.path());
    let mut args = json!({"path": "Live.php", "line": 3, "column": 1, "kind": "refactor.extract"});
    let listed = client.call_ok("shopware_code_actions", args.clone());
    let title = "Extract method 'extractedMethod'";
    assert!(
        action_titles(&listed).contains(&title.to_owned()),
        "{listed}"
    );
    assert_eq!(std::fs::read_to_string(&file).unwrap(), source);
    args["title"] = json!(title);
    client.call_ok("shopware_apply_code_action", args);
    let after = std::fs::read_to_string(&file).unwrap();
    assert!(
        after.contains("return $this->extractedMethod($a);"),
        "{after}"
    );
    assert!(
        after.contains("private function extractedMethod($a)"),
        "{after}"
    );
}

#[test]
fn mcp_php_extract_variable_uses_production_edits() {
    let root = temp_dir();
    let file = path(root.path(), "Live.php");
    let source = "<?php\nreturn build();\n";
    write(&file, source);
    let mut client = McpClient::connect(root.path());
    let mut args = json!({"path": "Live.php", "line": 2, "column": 9, "kind": "refactor.extract"});
    let listed = client.call_ok("shopware_code_actions", args.clone());
    let title = "Extract variable '$extracted'";
    assert!(
        action_titles(&listed).contains(&title.to_owned()),
        "{listed}"
    );
    assert_eq!(std::fs::read_to_string(&file).unwrap(), source);
    args["title"] = json!(title);
    client.call_ok("shopware_apply_code_action", args);
    assert_eq!(
        std::fs::read_to_string(&file).unwrap(),
        "<?php\n$extracted = build();\nreturn $extracted;\n"
    );
}

#[test]
fn mcp_php_import_cleanup_uses_production_analysis_and_edits() {
    for kind in ["quickfix", "source.organizeImports"] {
        let root = temp_dir();
        let file = path(root.path(), "Live.php");
        let source = "<?php\nuse Vendor\\Unused;\nclass Live {}\n";
        write(&file, source);
        let mut client = McpClient::connect(root.path());
        let diagnostics = client.call_ok(
            "shopware_diagnostics",
            json!({"path": "Live.php", "severity": "hint"}),
        );
        let items = diagnostics["diagnostics"].as_array().unwrap();
        assert_eq!(items.len(), 1, "{diagnostics}");
        assert_eq!(items[0]["code"], "php.unusedImport");
        assert_eq!(items[0]["range"]["start"]["line"], 2);
        let mut args = json!({"path": "Live.php", "line": 2, "column": 5, "kind": kind});
        let listed = client.call_ok("shopware_code_actions", args.clone());
        let title = if kind == "quickfix" {
            "Remove unused import 'Unused'"
        } else {
            "Organize Imports"
        };
        assert!(
            action_titles(&listed).contains(&title.to_owned()),
            "{kind}: {listed}"
        );
        assert_eq!(std::fs::read_to_string(&file).unwrap(), source);
        args["title"] = json!(title);
        client.call_ok("shopware_apply_code_action", args);
        assert_eq!(
            std::fs::read_to_string(&file).unwrap(),
            "<?php\nclass Live {}\n"
        );
    }
}
