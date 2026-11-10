use std::sync::Arc;

use swlsp_admin::AdminComponentIndexer;
use swlsp_ide::codelens::testing::*;
use swlsp_indexer::Indexer;

use super::*;

// -- admin components --

#[test]
fn admin_component_code_lenses_link_definitions_templates_and_extensions() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let index = Arc::new(AdminComponentIndexer::new(&p(&root.join("cache")), None).unwrap());
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let component_dir = admin_root.join("app/component/sw-card");
    let template_path = component_dir.join("sw-card.html.twig");
    let template_source = "<div><slot name=\"actions\"></slot></div>";
    write(&template_path, template_source);
    let base_path = component_dir.join("index.js");
    let base_source = "\nimport template from './sw-card.html.twig';\nComponent.register('sw-card', {\n    template,\n    props: { title: String },\n});";
    let extension_path = admin_root.join("extension/sw-card/index.js");
    let extension_source = "Component.override('sw-card', {\n    props: { subtitle: String },\n});";
    index.index(&parsed(&base_path, base_source)).unwrap();
    index
        .index(&parsed(&extension_path, extension_source))
        .unwrap();
    let provider = AdminComponentCodeLensProvider::new(Some(index));

    let base = lenses_for(&provider, &base_path, base_source);
    assert_eq!(
        sorted(titles(&base)),
        vec!["Open component extensions", "Open component template"]
    );
    assert_eq!(
        targets(by_title(&base, "Open component template")),
        vec![target(&template_path, 1)]
    );
    assert_eq!(
        targets(by_title(&base, "Open component extensions")),
        vec![target(&extension_path, 1)]
    );

    let extension = lenses_for(&provider, &extension_path, extension_source);
    assert_eq!(titles(&extension), vec!["Open base component"]);
    assert_eq!(targets(&extension[0]), vec![target(&base_path, 3)]);

    let template = lenses_for(&provider, &template_path, template_source);
    assert_eq!(
        sorted(titles(&template)),
        vec![
            "Open component extensions",
            "Open sw-card component definition"
        ]
    );
    assert_eq!(
        targets(by_title(&template, "Open sw-card component definition")),
        vec![target(&base_path, 3)]
    );
}
