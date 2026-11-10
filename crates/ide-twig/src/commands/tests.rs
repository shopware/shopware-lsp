use super::*;
use serde_json::json;
use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use swlsp_extension::ExtensionIndexer;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    BoxError, Context, DocumentError, DocumentResolver, DocumentSnapshot, TextDocument,
    WorkspaceEditHost,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::WorkspacePlan;
use swlsp_uriutil::file_uri;

fn path_string(path: &std::path::Path) -> String {
    path.to_str().expect("utf-8 path").to_owned()
}

// --- edits -----------------------------------------------------------------

#[derive(Default)]
struct EditTestHost {
    snapshots: HashMap<String, DocumentSnapshot>,
    plan: Mutex<Option<WorkspacePlan>>,
    reject: String,
    resources: Vec<String>,
}

impl DocumentResolver for EditTestHost {
    fn resolve_document(&self, _ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError> {
        if uri == self.reject {
            return Err(Box::new(DocumentError::OutsideWorkspace(uri.to_owned())));
        }
        self.snapshots.get(uri).cloned().ok_or_else(|| {
            Box::new(DocumentError::Read {
                uri: uri.to_owned(),
                source: std::io::Error::from(std::io::ErrorKind::NotFound),
            }) as BoxError
        })
    }
}

impl WorkspaceEditHost for EditTestHost {
    fn workspace_edit(
        &self,
        _ctx: &Context,
        plan: &WorkspacePlan,
    ) -> Result<protocol::WorkspaceEdit, BoxError> {
        *self.plan.lock().unwrap() = Some(plan.clone());
        Ok(plan.workspace_edit()?)
    }
}

impl EditHost for EditTestHost {
    fn resource_paths(
        &self,
        _ctx: &Context,
        directory: &str,
        _limit: usize,
    ) -> Result<Vec<String>, BoxError> {
        Ok(self
            .resources
            .iter()
            .filter(|path| path.starts_with(directory))
            .cloned()
            .collect())
    }
}

fn snapshot(uri: &str, source: &str, version: i32) -> DocumentSnapshot {
    DocumentSnapshot {
        document: Arc::new(TextDocument::new(uri, source, version)),
        version: Some(version),
    }
}

#[test]
fn twig_block_plan_preserves_unsaved_template() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let extensions = Arc::new(ExtensionIndexer::new(cache.path(), None).unwrap());
    let plugin = path_string(&root.path().join("src/DemoPlugin.php"));
    extensions
        .index(&ParsedFile::new(
            plugin,
            b"<?php\nuse Shopware\\Core\\Framework\\Plugin;\nclass DemoPlugin extends Plugin {}\n"
                .to_vec(),
        ))
        .unwrap();
    let target = path_string(
        &root
            .path()
            .join("src/Resources/views/storefront/page.html.twig"),
    );
    let uri = file_uri(&target);
    let host = Arc::new(EditTestHost {
        snapshots: HashMap::from([(uri.clone(), snapshot(&uri, "{# unsaved ä #}\n", 3))]),
        ..EditTestHost::default()
    });
    let provider = TwigCommandProvider::new(
        &path_string(root.path()),
        Some(extensions.clone()),
        None,
        host.clone(),
    );
    let text_uri = file_uri(&path_string(
        &root
            .path()
            .join("vendor/Resources/views/storefront/page.html.twig"),
    ));
    let args = json!({"textUri": text_uri, "blockName": "page_content", "extension": "DemoPlugin"});
    let result = provider
        .extend_block(&Context::background(), Some(&args))
        .unwrap();
    assert!(!result["edit"].is_null());
    assert_eq!(result["uri"], json!(uri));
    let plan = host.plan.lock().unwrap().clone().unwrap();
    assert_eq!(plan.documents.len(), 1);
    assert_eq!(plan.documents[0].version, Some(3));
    let content = plan.documents[0].apply().unwrap();
    assert!(content.contains("{# unsaved ä #}"));
    assert!(content.contains("{% block page_content %}"));
    assert!(!std::path::Path::new(&target).exists());

    // A missing extension and an existing block report Shopware errors.
    let missing = json!({"textUri": text_uri, "blockName": "x", "extension": "Nope"});
    assert_eq!(
        provider
            .extend_block(&Context::background(), Some(&missing))
            .unwrap(),
        json!({"code": "extension.not_found", "message": "Extension not found"})
    );
    extensions.close().unwrap();
}
