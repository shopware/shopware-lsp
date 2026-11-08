use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;

use swlsp_admin::AdminComponentIndexer;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    CallHierarchyCallsRequest, CallHierarchyPrepareRequest, CallHierarchyProvider, Context,
    SyntaxContext, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;

use super::AdminCallHierarchyProvider;

const PERSISTED_BASE: &str = "import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    methods: {
        save(id: string): void {},
        submit(id: string): void {
            this.save(id);
            Shopware.Store.get('session').login(id);
        },
    },
});";

const STORE_SOURCE: &str = "Shopware.Store.register('session', {
    actions: {
        login(user: string): void {},
        refresh(user: string): void {
            Shopware.Store.get('session').login(user);
        },
    },
});";

struct Fixture {
    _root: tempfile::TempDir,
    index: Arc<AdminComponentIndexer>,
    base_path: String,
    child_template: String,
    store_path: String,
    live_base: String,
}

impl Drop for Fixture {
    fn drop(&mut self) {
        self.index.close().expect("close index");
    }
}

fn path_string(path: &Path) -> String {
    path.to_str().expect("utf-8 path").to_owned()
}

fn fixture() -> Fixture {
    let root = tempfile::tempdir().unwrap();
    let cache = path_string(&root.path().join("cache"));
    let index = Arc::new(AdminComponentIndexer::new(&cache, None).unwrap());
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let base_path = admin_root.join("component/sw-base/index.ts");
    let base_template = admin_root.join("component/sw-base/sw-base.html.twig");
    let child_path = admin_root.join("component/sw-child/index.ts");
    let child_template = admin_root.join("component/sw-child/sw-child.html.twig");
    let store_path = admin_root.join("store/session.ts");
    let live_base = PERSISTED_BASE.replacen(
        "            this.save(id);",
        "            this.save(id);\n            this.save(id);",
        1,
    );
    for (path, source) in [
        (&base_path, PERSISTED_BASE),
        (&base_template, "{{ save(product.id) }}"),
        (
            &child_path,
            "import template from './sw-child.html.twig'; Component.extend('sw-child', 'sw-base', { template });",
        ),
        (
            &child_template,
            "<button @click=\"save(product.id)\">Save</button>",
        ),
        (&store_path, STORE_SOURCE),
    ] {
        index
            .index(&ParsedFile::new(
                path_string(path),
                source.as_bytes().to_vec(),
            ))
            .unwrap();
    }
    Fixture {
        index,
        base_path: path_string(&base_path),
        child_template: path_string(&child_template),
        store_path: path_string(&store_path),
        live_base,
        _root: root,
    }
}

fn prepare(
    provider: &AdminCallHierarchyProvider,
    path: &str,
    source: &str,
    needle: &str,
) -> (Vec<protocol::CallHierarchyItem>, Arc<TextDocument>) {
    let document = Arc::new(TextDocument::new(file_uri(path), source, 2));
    let offset = (source.find(needle).expect("needle") + 1) as u32;
    let (line, character) = document.line_index().position_utf16(offset);
    let root = document.root().expect("root");
    let node = root.node_at(TextSize::from(offset));
    let request = CallHierarchyPrepareRequest {
        params: protocol::CallHierarchyPrepareParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: document.uri.clone(),
            },
            position: protocol::Position { line, character },
        },
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document.clone()),
            root: Some(root),
            token: None,
            node,
        },
    };
    let items = provider
        .prepare_call_hierarchy(&Context::background(), &request)
        .expect("prepare");
    (items, document)
}

fn calls_request(
    item: protocol::CallHierarchyItem,
    documents: Vec<Arc<TextDocument>>,
) -> CallHierarchyCallsRequest {
    CallHierarchyCallsRequest { item, documents }
}

fn round_trip(item: protocol::CallHierarchyItem) -> protocol::CallHierarchyItem {
    serde_json::from_str(&serde_json::to_string(&item).unwrap()).unwrap()
}

#[test]
fn connects_inherited_methods_across_javascript_and_twig() {
    let fixture = fixture();
    let provider = AdminCallHierarchyProvider::new(fixture.index.clone());

    let (save_items, live_base) = prepare(
        &provider,
        &fixture.base_path,
        &fixture.live_base,
        "save(id:",
    );
    assert_eq!(save_items.len(), 1);
    let save = round_trip(save_items[0].clone());
    assert_eq!(save.name, "save");
    assert!(save.detail.contains("sw-base"), "{}", save.detail);
    assert!(save.detail.contains("sw-child"), "{}", save.detail);

    let live_template = Arc::new(TextDocument::new(
        file_uri(&fixture.child_template),
        "<button @click=\"save(product.id); save(other.id)\">Save</button>",
        2,
    ));
    let incoming = provider
        .incoming_calls(
            &Context::background(),
            &calls_request(save, vec![live_base.clone(), live_template]),
        )
        .unwrap();
    assert_eq!(incoming.len(), 2, "{incoming:#?}");
    let callers: HashMap<_, _> = incoming
        .iter()
        .map(|call| (call.from.name.as_str(), call))
        .collect();
    assert_eq!(callers["submit"].from_ranges.len(), 2);
    assert_eq!(callers["sw-child template"].from_ranges.len(), 2);

    let (submit_items, _) = prepare(
        &provider,
        &fixture.base_path,
        &fixture.live_base,
        "submit(id:",
    );
    assert_eq!(submit_items.len(), 1);
    let outgoing = provider
        .outgoing_calls(
            &Context::background(),
            &calls_request(submit_items[0].clone(), vec![live_base]),
        )
        .unwrap();
    assert_eq!(outgoing.len(), 2, "{outgoing:#?}");
    let targets: HashMap<_, _> = outgoing
        .iter()
        .map(|call| (call.to.name.as_str(), call))
        .collect();
    assert_eq!(targets["save"].from_ranges.len(), 2);
    assert_eq!(targets["login"].from_ranges.len(), 1);
}

#[test]
fn supports_pinia_action_declarations_and_callers() {
    let fixture = fixture();
    let provider = AdminCallHierarchyProvider::new(fixture.index.clone());
    let (login_items, live_store) =
        prepare(&provider, &fixture.store_path, STORE_SOURCE, "login(user:");
    assert_eq!(login_items.len(), 1);
    let login = login_items[0].clone();
    assert_eq!(login.name, "login");
    assert!(login.detail.contains("session"), "{}", login.detail);

    let live_base = Arc::new(TextDocument::new(
        file_uri(&fixture.base_path),
        &fixture.live_base,
        2,
    ));
    let incoming = provider
        .incoming_calls(
            &Context::background(),
            &calls_request(login, vec![live_store.clone(), live_base]),
        )
        .unwrap();
    let callers: HashMap<_, _> = incoming
        .iter()
        .map(|call| (call.from.name.as_str(), call))
        .collect();
    assert_eq!(callers["submit"].from_ranges.len(), 1, "{incoming:#?}");
    assert_eq!(callers["refresh"].from_ranges.len(), 1, "{incoming:#?}");

    let (refresh_items, _) = prepare(
        &provider,
        &fixture.store_path,
        STORE_SOURCE,
        "refresh(user:",
    );
    assert_eq!(refresh_items.len(), 1);
    let outgoing = provider
        .outgoing_calls(
            &Context::background(),
            &calls_request(refresh_items[0].clone(), vec![live_store]),
        )
        .unwrap();
    assert_eq!(outgoing.len(), 1);
    assert_eq!(outgoing[0].to.name, "login");
    assert_eq!(outgoing[0].from_ranges.len(), 1);
}
