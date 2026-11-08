use super::*;

fn file(path: &str, source: &str) -> ParsedFile {
    ParsedFile::new(path, source.as_bytes().to_vec())
}

#[test]
fn index_connects_scss_declarations_and_twig_usages() {
    let cache = tempfile::tempdir().unwrap();
    let index = Index::new(cache.path(), None).unwrap();
    index
        .index(&file(
            "/project/component.scss",
            ".sw-card { &__title { color: red; } }",
        ))
        .unwrap();
    index
        .index(&file(
            "/project/page.html.twig",
            r#"<div class="sw-card sw-card__title"></div>"#,
        ))
        .unwrap();

    let declarations = index.declarations("sw-card__title").unwrap();
    assert_eq!(declarations.len(), 1);
    assert_eq!(declarations[0].file, "/project/component.scss");
    assert_eq!(declarations[0].start.line, 0);
    assert!(declarations[0].start.character > 0);

    let usages = index.usages("sw-card__title").unwrap();
    assert_eq!(usages.len(), 1);
    assert_eq!(usages[0].file, "/project/page.html.twig");
    assert!(usages[0].start.character > 0);

    index
        .index(&file(
            "/project/page.html.twig",
            r#"<div class="other"></div>"#,
        ))
        .unwrap();
    assert!(index.usages("sw-card__title").unwrap().is_empty());
    index.close().unwrap();
}

#[test]
fn index_supports_vue_template_and_style_sections() {
    let cache = tempfile::tempdir().unwrap();
    let index = Index::new(cache.path(), None).unwrap();
    let source = "<template><div class=\"sw-panel\"></div></template>\n<style lang=\"scss\">.sw-panel { color: red; }</style>";
    index
        .index(&file("/project/component.vue", source))
        .unwrap();

    let declarations = index.declarations("sw-panel").unwrap();
    assert_eq!(declarations.len(), 1);
    let usages = index.usages("sw-panel").unwrap();
    assert_eq!(usages.len(), 1);
    assert_eq!(declarations[0].start.line, 1);
    assert_eq!(usages[0].start.line, 0);
    index.close().unwrap();
}

#[test]
fn index_tracks_global_and_theme_scss_variables() {
    let cache = tempfile::tempdir().unwrap();
    let index = Index::new(cache.path(), None).unwrap();
    let variables_path = "/project/_variables.scss";
    index
        .index(&file(
            variables_path,
            "$brand-primary: #0042a0;\n.scope { $local-only: red; }",
        ))
        .unwrap();
    index
        .index(&file(
            "/project/theme.json",
            r#"{"config":{"fields":{
            "theme-color":{"type":"color"},
            "not-in-scss":{"type":"text","scss":false}
        }}}"#,
        ))
        .unwrap();

    assert!(index.has_variable_declaration("brand_primary", "").unwrap());
    assert!(index.has_variable_declaration("theme-color", "").unwrap());
    assert!(!index.has_variable_declaration("local-only", "").unwrap());
    assert!(!index.has_variable_declaration("not-in-scss", "").unwrap());

    let declarations = index.variable_declarations("brand-primary").unwrap();
    assert_eq!(declarations.len(), 1);
    assert_eq!(declarations[0].file, variables_path);
    assert_eq!(
        declarations[0].start,
        SourcePosition {
            line: 0,
            character: 0
        }
    );

    assert!(
        !index
            .has_variable_declaration("brand-primary", variables_path)
            .unwrap(),
        "the open document must not resolve against its stale disk entry"
    );
    index
        .index(&file(variables_path, "$replacement: blue;"))
        .unwrap();
    assert!(!index.has_variable_declaration("brand-primary", "").unwrap());
    assert!(index.has_variable_declaration("replacement", "").unwrap());

    index
        .removed_files(&["/project/theme.json".to_owned()])
        .unwrap();
    assert!(!index.has_variable_declaration("theme-color", "").unwrap());
    index.close().unwrap();
}

#[test]
fn index_restores_scss_variables() {
    let cache = tempfile::tempdir().unwrap();
    let index = Index::new(cache.path(), None).unwrap();
    index
        .index(&file("/project/_variables.scss", "$persisted: red;"))
        .unwrap();
    index.close().unwrap();

    let restored = Index::new(cache.path(), None).unwrap();
    assert!(restored.has_variable_declaration("persisted", "").unwrap());
    restored.close().unwrap();
}
