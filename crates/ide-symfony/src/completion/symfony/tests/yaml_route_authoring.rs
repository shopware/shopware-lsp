//! Tests for YAML route authoring completion.

use std::sync::Arc;

use swlsp_lsp_protocol as protocol;
use swlsp_symfony::RouteIndexer;

use super::*;
use crate::completion::symfony::YamlRouteAuthoringCompletionProvider;

fn run(
    provider: &YamlRouteAuthoringCompletionProvider,
    path: &str,
    source: &str,
) -> (Arc<TextDocument>, Vec<protocol::CompletionItem>) {
    let (source, offset) = completion_caret(source);
    let (document, request) = bundle_request(path, &source, offset);
    let items = completions(provider, &request);
    (document, items)
}

#[test]
fn yaml_route_option_completion_block_and_flow_mappings() {
    let provider = YamlRouteAuthoringCompletionProvider::new(None);
    for (name, path, source, label, replaced, new_text, not_present, deprecated) in [
        (
            "modern block partial key",
            "/project/config/routes.yaml",
            "catalog:\n  path: /catalog\n  meth<caret>\n",
            "methods",
            "meth",
            "methods: ",
            "path",
            false,
        ),
        (
            "legacy routing filename",
            "/project/app/config/routing.yml",
            "catalog:\n  pat<caret>\n",
            "pattern",
            "pat",
            "pattern: ",
            "",
            true,
        ),
        (
            "empty key under config routes directory",
            "/project/config/routes/catalog.yaml",
            "catalog:\n  <caret>\n",
            "controller",
            "",
            "controller: ",
            "",
            false,
        ),
        (
            "complete quoted key",
            "/project/config/routes.yaml",
            "catalog:\n  'hos<caret>t':\n",
            "host",
            "'host'",
            "'host'",
            "",
            false,
        ),
        (
            "flow mapping after path placeholder",
            "/project/config/routes.yaml",
            "catalog: { path: /catalog/{id}, req<caret> }\n",
            "requirements",
            "req",
            "requirements: ",
            "path",
            false,
        ),
    ] {
        let (document, items) = run(&provider, path, source);
        let item = require_completion(&items, label);
        assert_eq!(
            item.kind,
            kind(protocol::CompletionItemKind::PROPERTY),
            "{name}"
        );
        assert_eq!(item.deprecated, deprecated, "{name}");
        let edit = edit(&item);
        assert_eq!(edit.new_text, new_text, "{name}");
        assert_eq!(range_text(&document, edit.range), replaced, "{name}");
        if !not_present.is_empty() {
            assert!(!labels(&items).contains(&not_present.to_string()), "{name}");
        }
    }
}

#[test]
fn yaml_route_option_completion_rejects_unrelated_and_nested_mappings() {
    let provider = YamlRouteAuthoringCompletionProvider::new(None);
    for (path, source) in [
        ("/project/config/services.yaml", "catalog:\n  pat<caret>\n"),
        (
            "/project/config/routes.yaml",
            "catalog:\n  defaults:\n    pat<caret>\n",
        ),
        (
            "/project/config/routes.yaml",
            "catalog:\n  requirements:\n    i<caret>\n",
        ),
        ("/project/config/routes.yaml", "top<caret>\n"),
    ] {
        let (_, items) = run(&provider, path, source);
        assert!(items.is_empty(), "{source}: {:?}", labels(&items));
    }
}

#[test]
fn yaml_route_requirement_completion_uses_path_placeholders() {
    let provider = YamlRouteAuthoringCompletionProvider::new(None);
    for (name, source, label, replaced, new_text, not_present) in [
        (
            "unquoted partial",
            "catalog:\n  path: /catalog/{id}/{slug}\n  requirements:\n    i<caret>\n",
            "id",
            "i",
            "id: ",
            "",
        ),
        (
            "quoted partial legacy pattern",
            "catalog:\n  pattern: /catalog/{name}\n  requirements:\n    'n<caret>'\n",
            "name",
            "'n'",
            "'name': ",
            "",
        ),
        (
            "inline requirement and duplicate suppression",
            "catalog:\n  path: '/catalog/{id<\\d+>}/{slug}'\n  requirements:\n    id: '\\d+'\n    sl<caret>\n",
            "slug",
            "sl",
            "slug: ",
            "id",
        ),
        (
            "flow mapping",
            "catalog: { path: '/catalog/{id}/{slug}', requirements: { id: '\\d+', sl<caret> } }\n",
            "slug",
            "sl",
            "slug: ",
            "id",
        ),
    ] {
        let (document, items) = run(&provider, "/tmp/project/config/routes.yaml", source);
        let item = require_completion(&items, label);
        let edit = edit(&item);
        assert_eq!(edit.new_text, new_text, "{name}");
        assert_eq!(range_text(&document, edit.range), replaced, "{name}");
        if !not_present.is_empty() {
            assert!(!labels(&items).contains(&not_present.to_string()), "{name}");
        }
    }
}

#[test]
fn yaml_route_requirement_completion_rejects_missing_path_and_wrong_level() {
    let provider = YamlRouteAuthoringCompletionProvider::new(None);
    for source in [
        "catalog:\n  requirements:\n    i<caret>\n",
        "catalog:\n  path: /catalog/{id}\n  defaults:\n    i<caret>\n",
    ] {
        let (_, items) = run(&provider, "/project/config/routes.yaml", source);
        assert!(items.is_empty(), "{source}: {:?}", labels(&items));
    }
}

#[test]
fn yaml_route_path_completion_uses_indexed_routes_with_exact_edits() {
    let dir = tempfile::tempdir().unwrap();
    let routes = RouteIndexer::new(dir.path(), None).unwrap();
    index_file(
        &routes,
        "/project/config/routes.yaml",
        "product.show:\n  path: /products/{id}\nproduct.duplicate:\n  path: /products/{id}\ncatalog:\n  path: /catalog\n_internal:\n  path: /_internal\n",
    );
    let provider = YamlRouteAuthoringCompletionProvider::new(Some(Arc::new(routes)));
    for (name, source, replaced) in [
        ("empty block value", "draft:\n  path: <caret>\n", ""),
        (
            "unquoted partial block value",
            "draft:\n  path: /pro<caret>\n",
            "/pro",
        ),
        (
            "quoted partial block value",
            "draft:\n  path: '/pro<caret>'\n",
            "/pro",
        ),
        (
            "flow value",
            "draft: { path: /pro<caret>, methods: [GET] }\n",
            "/pro",
        ),
    ] {
        let (document, items) = run(&provider, "/project/config/routes.yaml", source);
        let item = require_completion(&items, "/products/{id}");
        assert_eq!(
            item.kind,
            kind(protocol::CompletionItemKind::REFERENCE),
            "{name}"
        );
        assert_eq!(item.detail, "product.duplicate", "{name}");
        let edit = edit(&item);
        assert_eq!(edit.new_text, "/products/{id}");
        assert_eq!(range_text(&document, edit.range), replaced, "{name}");
        let all = labels(&items);
        assert!(!all.contains(&"/_internal".to_string()), "{name}");
        assert_eq!(
            all.iter()
                .filter(|label| *label == "/products/{id}")
                .count(),
            1
        );
    }
}

#[test]
fn yaml_route_path_completion_rejects_nested_resource_paths() {
    let dir = tempfile::tempdir().unwrap();
    let routes = RouteIndexer::new(dir.path(), None).unwrap();
    let provider = YamlRouteAuthoringCompletionProvider::new(Some(Arc::new(routes)));
    for source in [
        "controllers:\n  resource:\n    path: <caret>\n",
        "controllers: { resource: { path: <caret> } }\n",
    ] {
        let (_, items) = run(&provider, "/project/config/routes.yaml", source);
        assert!(items.is_empty(), "{source}: {:?}", labels(&items));
    }
}
