#![allow(unused_imports)]
use super::support::*;
use crate::*;

#[test]
fn javascript_shopware_context_member_tracks_nested_receivers() {
    let source = "
Shopware.Context.api.languageId;
Shopware.Context.app.config.version;
Shopware.Context.api.;
other.Context.api.languageId;
";
    let root = parse_js(source);
    let cases: [(&str, &[&str], &str, bool); 4] = [
        ("languageId", &["api"], "languageId", true),
        ("version", &["app", "config"], "version", true),
        ("api.;", &["api"], "", true),
        ("other.Context", &[], "", false),
    ];
    for (needle, receiver, member, matched) in cases {
        let mut offset = source.find(needle).expect("needle");
        if needle == "api.;" {
            offset += "api.".len() - 1;
        } else {
            offset += 1;
        }
        let node = root.node_at(TextSize::from(offset as u32)).expect("node");
        let result = javascript_shopware_context_member(&node);
        assert_eq!(result.is_some(), matched, "{needle}");
        let (actual_receiver, actual_member) = result.unwrap_or_default();
        assert_eq!(actual_receiver, receiver, "{needle}");
        assert_eq!(actual_member, member, "{needle}");
    }
}

#[test]
fn resolve_shopware_context_uses_indexed_context_state() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = "src/Administration/Resources/app/administration/src";
    let context_path = path_string(
        root.path(),
        &format!("{admin_root}/app/composables/use-context.ts"),
    );
    let consumer_path = path_string(
        root.path(),
        &format!("{admin_root}/module/example/index.ts"),
    );
    index_source(
        &idx,
        &context_path,
        "export interface ContextState {
    app: {
        config: { version: null | string; };
        environment: null | 'development' | 'production';
    };
    api: {
        languageId: null | string;
        versionId: null | string;
    };
}",
    );

    let sorted = |members: &[TwigVueMember]| {
        let mut names = vue_member_names(members);
        names.sort();
        names
    };

    let root_shape = idx
        .resolve_shopware_context("", &consumer_path)
        .expect("root");
    assert!(!root_shape.complete);
    assert_eq!(sorted(&root_shape.members), ["api", "app"]);

    let api_shape = idx
        .resolve_shopware_context("api", &consumer_path)
        .expect("api");
    assert!(api_shape.complete);
    assert_eq!(sorted(&api_shape.members), ["languageId", "versionId"]);
    let language_id = twig_vue_member_named(&api_shape.members, "languageId").expect("languageId");
    assert_eq!(language_id.type_, "null | string");
    assert_eq!(language_id.definition_path, context_path);
    assert!(language_id.definition_range.identifier);

    let config_shape = idx
        .resolve_shopware_context("app.config", &consumer_path)
        .expect("config");
    assert!(config_shape.complete);
    assert_eq!(vue_member_names(&config_shape.members), ["version"]);

    let (resolved, found) = idx
        .resolve_component_expression_type(
            &VueComponent::default(),
            "Shopware.Context.api.languageId",
            &consumer_path,
        )
        .expect("resolve");
    assert!(found);
    assert_eq!(resolved.type_, "null | string");
    assert_eq!(resolved.context_path, context_path);
}
