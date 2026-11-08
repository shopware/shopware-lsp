//! Go `admin_twig_override_codeaction_test.go`.

use std::path::Path;
use std::sync::Arc;

use serde_json::json;
use swlsp_admin::{AdminComponentIndexer, TwigBlock, VueComponent};
use swlsp_extension::ExtensionIndexer;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, Context, TextDocument, WorkspaceEditHost};
use swlsp_uriutil::{file_uri, filepath};

use super::*;
use crate::test_support::{
    GenerationTestHost, apply_generated_workspace_edit, path_string, request_for_document,
};

struct Fixture {
    _root: tempfile::TempDir,
    provider: AdminTwigOverrideProvider,
    template_path: String,
    plugin_source: String,
}

fn fixture() -> Fixture {
    let root = tempfile::tempdir().unwrap();
    let base = path_string(root.path());
    let admin_index = Arc::new(
        AdminComponentIndexer::new(&filepath::join(&[base.as_str(), "admin-cache"]), None).unwrap(),
    );
    let extension_index =
        Arc::new(ExtensionIndexer::new(root.path().join("extension-cache"), None).unwrap());
    let template_path = filepath::join(&[
        base.as_str(),
        "platform/src/Administration/Resources/app/administration/src/component/sw-card/sw-card.html.twig",
    ]);
    let index_js = filepath::join(&[dir(&template_path).as_str(), "index.js"]);
    admin_index
        .save_component(&{
            let mut component = VueComponent::default();
            component.name = "sw-card".to_owned();
            component.file_path = index_js.clone();
            component.definition_path = index_js;
            component.template_path = template_path.clone();
            component.blocks = vec![TwigBlock {
                name: "sw_card_header".to_owned(),
                line: 1,
                file_path: template_path.clone(),
                ..TwigBlock::default()
            }];
            component
        })
        .unwrap();
    let plugin_source = filepath::join(&[base.as_str(), "custom/plugins/DemoPlugin/src"]);
    let plugin_class = filepath::join(&[plugin_source.as_str(), "DemoPlugin.php"]);
    extension_index
        .index(&ParsedFile::new(
            plugin_class,
            b"<?php declare(strict_types=1);

namespace Demo;

use Shopware\\Core\\Framework\\Plugin;

final class DemoPlugin extends Plugin
{
}
"
            .to_vec(),
        ))
        .unwrap();
    let host: Arc<dyn WorkspaceEditHost> = Arc::new(GenerationTestHost::default());
    Fixture {
        _root: root,
        provider: AdminTwigOverrideProvider::new(
            Some(admin_index),
            Some(extension_index),
            Some(host),
        ),
        template_path,
        plugin_source,
    }
}

fn override_request(fixture: &Fixture, block: &str) -> serde_json::Value {
    json!({
        "textUri": file_uri(&fixture.template_path),
        "blockName": block,
        "extension": "DemoPlugin",
    })
}

fn generate(fixture: &Fixture, block: &str) -> AdminTwigOverrideResponse {
    fixture
        .provider
        .generate_admin_twig_override(
            &Context::background(),
            Some(&override_request(fixture, block)),
        )
        .unwrap()
        .unwrap()
}

#[test]
fn action_resolves_owning_component() {
    let fixture = fixture();
    let source = "{% block sw_card_header %}<h1>Title</h1>{% endblock %}";
    let document = Arc::new(TextDocument::new(
        file_uri(&fixture.template_path),
        source,
        1,
    ));
    let offset = source.find("sw_card_header").unwrap() as u32 + 2;
    let request = request_for_document(document.clone(), offset, offset);
    let actions = fixture
        .provider
        .get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    assert_eq!(
        actions[0].title,
        "Override sw_card_header for Administration component sw-card in a plugin"
    );
    let command = actions[0].command.as_ref().unwrap();
    assert_eq!(command.command, ADMIN_TWIG_OVERRIDE_ACTION);
    assert_eq!(
        command.arguments,
        vec![json!(document.uri), json!("sw_card_header")]
    );
}

#[test]
fn action_is_not_offered_for_storefront_twig() {
    let fixture = fixture();
    let source = "{% block sw_card_header %}{% endblock %}";
    let document = Arc::new(TextDocument::new(
        "file:///project/Resources/views/storefront/card.html.twig",
        source,
        1,
    ));
    let offset = source.find("sw_card_header").unwrap() as u32 + 1;
    let request = request_for_document(document, offset, offset);
    assert!(
        fixture
            .provider
            .get_code_actions(&Context::background(), &request)
            .is_empty()
    );
}

#[test]
fn generator_creates_loadable_override() {
    let fixture = fixture();
    let administration_source = filepath::join(&[
        fixture.plugin_source.as_str(),
        "Resources",
        "app",
        "administration",
        "src",
    ]);
    std::fs::create_dir_all(&administration_source).unwrap();
    std::fs::write(
        filepath::join(&[administration_source.as_str(), "main.js"]),
        "import './module/demo';\n",
    )
    .unwrap();

    let result = generate(&fixture, "sw_card_header");
    assert!(
        !Path::new(&filepath::join(&[
            administration_source.as_str(),
            "extension"
        ]))
        .exists()
    );
    apply_generated_workspace_edit(result.edit.as_ref().unwrap());
    assert_eq!(result.component, "sw-card");
    assert_eq!(result.line, 0);

    let override_directory =
        filepath::join(&[administration_source.as_str(), "extension", "sw-card"]);
    let script =
        std::fs::read_to_string(filepath::join(&[override_directory.as_str(), "index.js"]))
            .unwrap();
    assert_eq!(
        script,
        "import template from './sw-card.html.twig';

Shopware.Component.override('sw-card', {
    template,
});
"
    );
    let template = std::fs::read_to_string(filepath::join(&[
        override_directory.as_str(),
        "sw-card.html.twig",
    ]))
    .unwrap();
    assert_eq!(template, "{% block sw_card_header %}\n\n{% endblock %}\n");
    let entry =
        std::fs::read_to_string(filepath::join(&[administration_source.as_str(), "main.js"]))
            .unwrap();
    assert_eq!(
        entry,
        "import './extension/sw-card';\nimport './module/demo';\n"
    );
    assert_eq!(
        result.uri,
        file_uri(&filepath::join(&[
            override_directory.as_str(),
            "sw-card.html.twig"
        ]))
    );
}

#[test]
fn generator_appends_and_is_idempotent() {
    let fixture = fixture();
    let request = |block: &str| {
        let result = generate(&fixture, block);
        apply_generated_workspace_edit(result.edit.as_ref().unwrap());
    };
    request("sw_card_header");
    request("sw_card_body");
    let template_path = filepath::join(&[
        fixture.plugin_source.as_str(),
        "Resources/app/administration/src/extension/sw-card/sw-card.html.twig",
    ]);
    let expected = "{% block sw_card_header %}

{% endblock %}

{% block sw_card_body %}

{% endblock %}
";
    assert_eq!(std::fs::read_to_string(&template_path).unwrap(), expected);
    request("sw_card_header");
    assert_eq!(std::fs::read_to_string(&template_path).unwrap(), expected);
    let entry = std::fs::read_to_string(filepath::join(&[
        fixture.plugin_source.as_str(),
        "Resources/app/administration/src/main.js",
    ]))
    .unwrap();
    assert_eq!(entry, "import './extension/sw-card';\n");
}

#[test]
fn generator_preserves_conflicting_files() {
    let fixture = fixture();
    let override_directory = filepath::join(&[
        fixture.plugin_source.as_str(),
        "Resources/app/administration/src/extension/sw-card",
    ]);
    std::fs::create_dir_all(&override_directory).unwrap();
    let script_path = filepath::join(&[override_directory.as_str(), "index.js"]);
    let original = "import template from './sw-card.html.twig';

Shopware.Component.override('another-component', { template });
";
    std::fs::write(&script_path, original).unwrap();

    let error = fixture
        .provider
        .generate_admin_twig_override(
            &Context::background(),
            Some(&override_request(&fixture, "sw_card_header")),
        )
        .unwrap()
        .unwrap_err();
    assert_eq!(error.code, "admin.override.file_conflict");
    assert!(
        error.message.contains("does not override component"),
        "{}",
        error.message
    );
    assert_eq!(std::fs::read_to_string(&script_path).unwrap(), original);
    assert!(
        !Path::new(&filepath::join(&[
            override_directory.as_str(),
            "sw-card.html.twig"
        ]))
        .exists()
    );
}

#[test]
fn preserves_unsaved_entry_without_writing_files() {
    let mut fixture = fixture();
    let entry = filepath::join(&[
        fixture.plugin_source.as_str(),
        "Resources/app/administration/src/main.js",
    ]);
    let uri = file_uri(&entry);
    let source = "import './unsaved-module';\n";
    let host = Arc::new(GenerationTestHost::default());
    // Register the unsaved entry snapshot.
    let host = {
        let mut inner = Arc::try_unwrap(host).ok().unwrap();
        inner
            .snapshots
            .insert(uri.clone(), crate::test_support::snapshot(&uri, source, 6));
        Arc::new(inner)
    };
    fixture.provider.host = Some(host.clone());
    let result = generate(&fixture, "sw_card_header");
    assert!(result.edit.is_some());
    let plan = host.plan.lock().unwrap().clone().unwrap();
    assert_eq!(plan.creates.len(), 2);
    assert_eq!(plan.documents.len(), 1);
    assert_eq!(plan.documents[0].version, Some(6));
    assert_eq!(
        plan.documents[0].apply().unwrap(),
        format!("import './extension/sw-card';\n{source}")
    );
    assert!(!Path::new(&entry).exists());
    assert!(!Path::new(&filepath::join(&[dir(&entry).as_str(), "extension"])).exists());
}

#[test]
fn rejects_invalid_requests_with_lsp_errors() {
    let fixture = fixture();
    let ctx = Context::background();
    let cases = [
        (None, "admin.override.invalid_request"),
        (
            Some(json!({"textUri": "", "blockName": "x", "extension": "DemoPlugin"})),
            "admin.override.invalid_request",
        ),
        (
            Some(
                json!({"textUri": "file:///a/b.html.twig", "blockName": "x", "extension": "DemoPlugin"}),
            ),
            "admin.override.not_administration_template",
        ),
        (
            Some(
                json!({"textUri": file_uri(&fixture.template_path), "blockName": "x", "extension": "Missing"}),
            ),
            "extension.not_found",
        ),
    ];
    for (raw, code) in cases {
        let error = fixture
            .provider
            .generate_admin_twig_override(&ctx, raw.as_ref())
            .unwrap()
            .unwrap_err();
        assert_eq!(error.code, code);
    }
}

fn dir(path: &str) -> String {
    path_string(Path::new(path).parent().unwrap())
}
