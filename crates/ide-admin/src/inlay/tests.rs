use super::*;
use std::path::Path;
use std::sync::Arc;
use swlsp_admin::AdminComponentIndexer;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, TextDocument};
use swlsp_lsp_protocol::{self as protocol, InlayHintKind};
use swlsp_uriutil::file_uri;

fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn request(document: TextDocument) -> InlayHintRequest {
    let mut params = protocol::InlayHintParams::default();
    params.text_document.uri = document.uri.clone();
    let (line, character) = document
        .line_index()
        .position_utf16(document.source().len() as u32);
    params.range.end = protocol::Position { line, character };
    InlayHintRequest {
        params,
        document: Arc::new(document),
    }
}

fn hints(provider: &dyn InlayHintProvider, request: &InlayHintRequest) -> Vec<protocol::InlayHint> {
    provider
        .get_inlay_hints(&Context::background(), request)
        .unwrap()
}

fn labels(hints: &[protocol::InlayHint]) -> Vec<String> {
    hints
        .iter()
        .map(|hint| match &hint.label {
            protocol::InlayHintLabel::String(label) => label.clone(),
            protocol::InlayHintLabel::Parts(parts) => {
                parts.iter().map(|part| part.value.as_str()).collect()
            }
        })
        .collect()
}

// -- admin parameters ---------------------------------------------------------

fn admin_index(root: &Path) -> Arc<AdminComponentIndexer> {
    Arc::new(AdminComponentIndexer::new(&p(&root.join("cache")), None).unwrap())
}

#[test]
fn admin_parameter_hints_reuse_component_signatures_in_javascript_and_twig() {
    let root = tempfile::tempdir().unwrap();
    let index = admin_index(root.path());
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let definition_path = admin_root.join("component/sw-card/index.ts");
    let template_path = admin_root.join("component/sw-card/sw-card.html.twig");
    let definition_source = "import template from './sw-card.html.twig';
Component.register('sw-card', {
    template,
    methods: {
        save(id: string, options?: { force: boolean }): Promise<void> {
            return Promise.resolve();
        },
        submit(id: string, options: { force: boolean }) {
            this.save(id, options);
            this.save(this.product.id, { force: true });
        },
    },
});";
    let template_source = "<button @click=\"save(product.id, { force: true }); $emit('saved', product.id); value | default('fallback')\">Save</button>";
    for (path, source) in [
        (&definition_path, definition_source),
        (&template_path, template_source),
    ] {
        index
            .index(&ParsedFile::new(p(path), source.as_bytes().to_vec()))
            .unwrap();
    }
    let provider = AdminParameterProvider::new(index);
    let javascript = hints(
        &provider,
        &request(TextDocument::new(
            file_uri(&p(&definition_path)),
            definition_source,
            1,
        )),
    );
    assert_eq!(labels(&javascript), vec!["id:", "options:"]);
    assert!(javascript[0].tooltip.contains("save(id: string"));
    assert!(javascript[0].padding_right);

    let twig = hints(
        &provider,
        &request(TextDocument::new(
            file_uri(&p(&template_path)),
            template_source,
            1,
        )),
    );
    assert_eq!(labels(&twig), vec!["id:", "options:", "event:", "args:"]);
    for hint in &twig {
        assert_eq!(hint.kind, Some(InlayHintKind::PARAMETER));
    }

    let mut ranged = request(TextDocument::new(
        file_uri(&p(&template_path)),
        template_source,
        1,
    ));
    let start = template_source.find("{ force").unwrap() as u32;
    let end = start + "{ force: true }".len() as u32;
    let lines = ranged.document.line_index();
    let (start_line, start_character) = lines.position_utf16(start);
    let (end_line, end_character) = lines.position_utf16(end);
    ranged.params.range = protocol::Range {
        start: protocol::Position {
            line: start_line,
            character: start_character,
        },
        end: protocol::Position {
            line: end_line,
            character: end_character,
        },
    };
    assert_eq!(labels(&hints(&provider, &ranged)), vec!["options:"]);
}

#[test]
fn admin_parameter_hints_reuse_pinia_action_signatures() {
    let root = tempfile::tempdir().unwrap();
    let index = admin_index(root.path());
    let path = root
        .path()
        .join("src/Administration/Resources/app/administration/src/store/session.ts");
    let source = "Shopware.Store.register('session', {
    actions: {
        login(user: string, remember?: boolean): Promise<boolean> {
            return Promise.resolve(true);
        },
    },
});
Shopware.Store.get('session').login('admin', true);";
    index
        .index(&ParsedFile::new(p(&path), source.as_bytes().to_vec()))
        .unwrap();
    let result = hints(
        &AdminParameterProvider::new(index),
        &request(TextDocument::new(file_uri(&p(&path)), source, 1)),
    );
    assert_eq!(labels(&result), vec!["user:", "remember:"]);
}

#[test]
fn admin_parameter_hint_skips_ambiguous_overload_names() {
    let parameter = |label: &str| protocol::ParameterInformation {
        label: label.to_string(),
        documentation: None,
    };
    let help = protocol::SignatureHelp {
        signatures: vec![
            protocol::SignatureInformation {
                parameters: vec![parameter("value: string")],
                ..Default::default()
            },
            protocol::SignatureInformation {
                parameters: vec![parameter("item: number")],
                ..Default::default()
            },
        ],
        ..Default::default()
    };
    assert!(crate::inlay::admin_parameter::admin_parameter_hint(Some(&help), 0).is_none());
}
