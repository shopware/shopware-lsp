use super::*;
use crate::definition::php_common::test_support::*;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::Indexer;
use swlsp_uriutil::file_uri;

fn cache(dir: &Path) -> std::path::PathBuf {
    dir.join("cache")
}

fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    index(indexer, path, source);
}

// -- style classes ------------------------------------------------------------

#[test]
fn style_class_definition_navigates_from_twig_to_nested_scss() {
    let root = tempfile::tempdir().unwrap();
    let style_path = p(&root.path().join("component.scss"));
    let index = Arc::new(swlsp_style::Index::new(cache(root.path()), None).unwrap());
    index_file(
        &*index,
        &style_path,
        ".sw-card {\n  &__title { color: red; }\n}",
    );
    let source = r#"<div class="sw-card__title"></div>"#;
    let document = document(&file_uri(&p(&root.path().join("page.html.twig"))), source);
    let locations = definitions(
        &StyleClassDefinitionProvider::new(Some(index)),
        &request_at(&document, index_of(source, "sw-card__title") + 3),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&style_path));
    assert_eq!(locations[0].range.start.line, 1);
    assert_eq!(locations[0].range.start.character, 2);
}

#[test]
fn style_class_definition_uses_live_vue_template_and_style() {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(swlsp_style::Index::new(cache(root.path()), None).unwrap());
    let path = p(&root.path().join("component.vue"));
    let source = "<template><div class=\"sw-panel\"></div></template>
<style lang=\"scss\">.sw-panel { color: red; }</style>";
    let document = document(&file_uri(&path), source);
    let locations = definitions(
        &StyleClassDefinitionProvider::new(Some(index)),
        &request_at(&document, index_of(source, "sw-panel") + 3),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&path));
    assert_eq!(locations[0].range.start.line, 1);
}
