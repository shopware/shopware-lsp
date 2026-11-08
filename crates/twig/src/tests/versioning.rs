use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};

use crate::*;

fn s(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn setup() -> (tempfile::TempDir, Arc<TwigIndexer>) {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(TwigIndexer::new(root.path().join("cache"), None).unwrap());
    (root, index)
}

fn index(index: &TwigIndexer, path: &str, source: &str) {
    index
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
}

const CORE: &str = "src/Storefront/Resources/views/storefront/page/base.html.twig";

#[test]
fn versioning_service_follows_extends_chain() {
    let (root, idx) = setup();
    let core = s(&root.path().join(CORE));
    let theme = s(&root
        .path()
        .join("custom/plugins/Theme/src/Resources/views/storefront/theme/base.html.twig"));
    let plugin = s(&root
        .path()
        .join("custom/plugins/Plugin/src/Resources/views/storefront/custom/page.html.twig"));
    index(&idx, &core, "{% block page_content %}core{% endblock %}");
    index(
        &idx,
        &theme,
        "{% sw_extends '@Storefront/storefront/page/base.html.twig' %}\n{# shopware-block: deadbeef@1.0.0 #}\n{% block page_content %}theme{% endblock %}",
    );
    let file = parse_twig(
        &plugin,
        "{% sw_extends '@Theme/storefront/theme/base.html.twig' %}\n{% block page_content %}plugin{% endblock %}",
    );
    let resolution = VersioningService::new(&s(root.path()), Some(idx.clone()), "6.7.0")
        .resolve(&file, "page_content")
        .unwrap();
    assert!(resolution.parent_resolved);
    assert_eq!(resolution.candidates.len(), 2);
    assert_eq!(resolution.candidates[0].absolute_path, theme);
    assert_eq!(resolution.candidates[1].absolute_path, core);
    assert!(resolution.candidates[0].has_versioning_comment);
}

#[test]
fn versioning_service_resolves_all_blocks_against_one_chain() {
    let (root, idx) = setup();
    let core = s(&root.path().join(CORE));
    index(
        &idx,
        &core,
        "{% block first %}one{% endblock %}\n{% block second %}two{% endblock %}",
    );
    let file = parse_twig(
        &s(&root.path().join("custom/plugin.html.twig")),
        "{% sw_extends '@Storefront/storefront/page/base.html.twig' %}\n{% block first %}local one{% endblock %}\n{% block second %}local two{% endblock %}",
    );
    let resolutions = VersioningService::new(&s(root.path()), Some(idx.clone()), "")
        .resolve_blocks(&file)
        .unwrap();
    assert_eq!(resolutions.len(), 2);
    for name in ["first", "second"] {
        assert_eq!(resolutions[name].candidates.len(), 1);
        assert_eq!(resolutions[name].candidates[0].absolute_path, core);
        assert!(resolutions[name].parent_resolved);
    }
}

#[test]
fn versioning_service_fallback_ignores_tracked_sibling_overrides() {
    let (root, idx) = setup();
    let core = s(&root.path().join(CORE));
    let sibling = s(&root
        .path()
        .join("custom/plugins/A/src/Resources/views/storefront/page/base.html.twig"));
    let current = s(&root
        .path()
        .join("custom/plugins/B/src/Resources/views/storefront/page/base.html.twig"));
    index(&idx, &core, "{% block page_content %}core{% endblock %}");
    index(
        &idx,
        &sibling,
        "{# shopware-block: deadbeef@1.0.0 #}\n{% block page_content %}sibling{% endblock %}",
    );
    let file = parse_twig(
        &current,
        "{% sw_extends '@Missing/storefront/page/base.html.twig' %}\n{% block page_content %}current{% endblock %}",
    );
    let resolution = VersioningService::new(&s(root.path()), Some(idx.clone()), "")
        .resolve(&file, "page_content")
        .unwrap();
    assert_eq!(resolution.candidates.len(), 1);
    assert_eq!(resolution.candidates[0].absolute_path, core);
    assert!(!resolution.parent_resolved);
}

#[test]
fn versioning_service_distinguishes_removed_block_from_missing_checkout() {
    let (root, idx) = setup();
    index(
        &idx,
        &s(&root.path().join(CORE)),
        "{% block different_block %}core{% endblock %}",
    );
    let service = VersioningService::new(&s(root.path()), Some(idx.clone()), "");
    let resolved = parse_twig(
        &s(&root.path().join("custom/resolved.html.twig")),
        "{% sw_extends '@Storefront/storefront/page/base.html.twig' %}\n{% block removed_block %}current{% endblock %}",
    );
    let resolution = service.resolve(&resolved, "removed_block").unwrap();
    assert!(resolution.parent_resolved);
    assert!(resolution.candidates.is_empty());
    let standalone = parse_twig(
        &s(&root.path().join("custom/standalone.html.twig")),
        "{% sw_extends '@Missing/storefront/page/base.html.twig' %}\n{% block removed_block %}current{% endblock %}",
    );
    let resolution = service.resolve(&standalone, "removed_block").unwrap();
    assert!(!resolution.parent_resolved);
    assert!(resolution.candidates.is_empty());
}

#[test]
fn versioning_service_builds_portable_comment_edits() {
    let (root, idx) = setup();
    let upstream_root = root.path().join("custom/plugins/Theme");
    std::fs::create_dir_all(&upstream_root).unwrap();
    std::fs::write(
        upstream_root.join("composer.json"),
        r#"{"version":"2.5.0"}"#,
    )
    .unwrap();
    index(
        &idx,
        &s(&upstream_root.join("src/Resources/views/storefront/card.html.twig")),
        "{% block card %}upstream{% endblock %}",
    );
    let current = s(&root
        .path()
        .join("custom/plugins/Plugin/src/Resources/views/storefront/card.html.twig"));
    let source = "{% sw_extends '@Theme/storefront/card.html.twig' %}\n  {% block card %}local{% endblock %}\n";
    let service = VersioningService::new(&s(root.path()), Some(idx.clone()), "");
    let (range, replacement) = service
        .version_comment_edit(&current, source, "card")
        .unwrap();
    assert_eq!(format!("{}{}", &source[range], &replacement[..2]), "  ");
    assert!(replacement.contains("@2.5.0 #}"));
    let updated = format!(
        "{}{}{}",
        &source[..usize::from(range.start())],
        replacement,
        &source[usize::from(range.end())..]
    );
    let parsed = parse_twig(&current, &updated);
    assert_eq!(
        parsed.blocks["card"]
            .version_comment
            .as_ref()
            .expect("comment")
            .version,
        "2.5.0"
    );
}
