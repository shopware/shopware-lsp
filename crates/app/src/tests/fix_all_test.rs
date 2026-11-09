//! "Fix all in file" applies one quick fix to every problem of a code,
//! sequentially, through the production wiring.

use serde_json::{Value, json};

use super::support::{TestClient, file_uri, workspace_server};

/// Applies the single text edit of `edit` (ASCII sources only).
fn apply(source: &str, edit: &Value) -> String {
    let edits: Vec<&Value> = if let Some(changes) = edit["changes"].as_object() {
        changes
            .values()
            .flat_map(|v| v.as_array().unwrap())
            .collect()
    } else {
        edit["documentChanges"]
            .as_array()
            .unwrap()
            .iter()
            .flat_map(|change| change["edits"].as_array().unwrap())
            .collect()
    };
    assert_eq!(edits.len(), 1, "{edit}");
    let offset = |position: &Value| {
        let line = position["line"].as_u64().unwrap() as usize;
        let character = position["character"].as_u64().unwrap() as usize;
        source
            .split_inclusive('\n')
            .take(line)
            .map(str::len)
            .sum::<usize>()
            + character
    };
    let range = &edits[0]["range"];
    let (start, end) = (offset(&range["start"]), offset(&range["end"]));
    format!(
        "{}{}{}",
        &source[..start],
        edits[0]["newText"].as_str().unwrap(),
        &source[end..]
    )
}

#[test]
fn fix_all_removes_every_unused_import_in_one_edit() {
    for resolve in [true, false] {
        let cache = tempfile::tempdir().unwrap();
        let project = tempfile::tempdir().unwrap();
        let root = project.path();
        let server = workspace_server(root, cache.path());
        let client = TestClient::start(&server);
        let capabilities = if resolve {
            json!({"textDocument": {"codeAction": {
                "dataSupport": true,
                "resolveSupport": {"properties": ["edit"]},
            }}})
        } else {
            json!({})
        };
        client.call(
            "initialize",
            json!({"rootUri": file_uri(root), "initializationOptions": {}, "capabilities": capabilities}),
        );
        let uri = file_uri(&root.join("Live.php"));
        let source = "<?php\nuse Vendor\\{First, Used, Second};\nuse Vendor\\Third;\nnew Used();\n";
        client.open(&uri, 1, source);
        let publication = client.wait_diagnostics(&[&uri]).remove(&uri).unwrap();
        let diagnostics: Vec<Value> = publication["diagnostics"]
            .as_array()
            .unwrap()
            .iter()
            .filter(|item| item["code"] == json!("php.unusedImport"))
            .cloned()
            .collect();
        assert_eq!(diagnostics.len(), 3, "{publication}");
        let actions = client.call(
            "textDocument/codeAction",
            json!({
                "textDocument": {"uri": uri},
                "range": diagnostics[0]["range"],
                "context": {"diagnostics": [diagnostics[0]]},
            }),
        );
        let mut action = actions
            .as_array()
            .unwrap()
            .iter()
            .find(|action| {
                action["title"] == json!("Fix all 3 'php.unusedImport' problems in this file")
            })
            .unwrap_or_else(|| panic!("no fix-all action: {actions}"))
            .clone();
        if action["edit"].is_null() {
            action = client.call("codeAction/resolve", action);
        }
        assert_eq!(
            apply(source, &action["edit"]),
            "<?php\nuse Vendor\\{Used};\nnew Used();\n",
            "resolve: {resolve}"
        );
        client.shutdown().unwrap();
    }
}
