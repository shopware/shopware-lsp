//! Port of Go `internal/app/workspace_asset_snapshot_test.go`.

use swlsp_asset::{ReferenceKind, Resource, Usage};

use super::workspace_test::{index_all, open_workspace, write_files};
use crate::Workspace;

/// The same asset probes serve the portable lifecycle test and real-world
/// cold/restored checks, so adding a probe cannot silently omit restart
/// coverage.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(super) struct WorkspaceAssetSnapshot {
    names: Vec<String>,
    packages: Vec<String>,
    encore: Vec<String>,
    importmap: Vec<String>,
    vite: Vec<String>,
    administration: Vec<Resource>,
    theme: Vec<Resource>,
    administration_usages: Vec<Usage>,
    html_usages: Vec<Usage>,
    vite_usages: Vec<Usage>,
    vite_administration_usages: Vec<Usage>,
}

pub(super) fn capture_workspace_assets(workspace: &Workspace) -> WorkspaceAssetSnapshot {
    let index = &workspace.services().assets;
    WorkspaceAssetSnapshot {
        names: index.names().unwrap(),
        packages: index.package_names().unwrap(),
        encore: index.entry_names().unwrap(),
        importmap: index.importmap_entry_names().unwrap(),
        vite: index.vite_entry_names().unwrap(),
        administration: index
            .find_assets_for_package(
                "administration/static/img/favicon/favicon-16x16.png",
                "@Administration",
            )
            .unwrap(),
        theme: index
            .find_assets_for_package("assets/illustration/404_error.svg", "theme")
            .unwrap(),
        administration_usages: index
            .usages("@Administration", ReferenceKind::AssetPackage)
            .unwrap(),
        html_usages: index
            .usages(
                "_webpack_hot_proxy_/storefront/hot-reloading.js",
                ReferenceKind::Asset,
            )
            .unwrap(),
        vite_usages: index.usages("app", ReferenceKind::ViteEntry).unwrap(),
        vite_administration_usages: index
            .usages("administration", ReferenceKind::ViteEntry)
            .unwrap(),
    }
}

fn sorted(mut values: Vec<String>) -> Vec<String> {
    values.sort();
    values
}

#[test]
fn workspace_asset_lifecycle_preserves_and_removes_catalog_state() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            (
                "public/bundles/administration/administration/static/img/favicon/favicon-16x16.png",
                "fixture",
            ),
            (
                "public/theme/test/assets/illustration/404_error.svg",
                "<svg/>",
            ),
            ("public/build/app.css", "body{}"),
            (
                "public/build/entrypoints.json",
                r#"{"entrypoints":{"app":{"css":["/build/app.css"]}}}"#,
            ),
            (
                "src/Administration/Resources/config/services.xml",
                r#"<container><services><service id="assets.theme"><tag name="assets.package" package="theme"/></service><service id="assets.asset"><tag name="assets.package" package="asset"/></service></services></container>"#,
            ),
            ("assets/app.js", "export default {};"),
            (
                "vite.config.ts",
                "export default defineConfig({build:{rollupOptions:{input:{app:'./assets/app.js',administration:'./assets/app.js'}}}});",
            ),
            (
                "importmap.php",
                "<?php return ['app' => ['path' => './assets/app.js', 'entrypoint' => true]];",
            ),
            (
                "templates/page.html.twig",
                "{{ asset('administration/static/img/favicon/favicon-16x16.png', '@Administration') }}\n{{ asset('assets/illustration/404_error.svg', 'theme') }}\n<script src=\"{{ asset('_webpack_hot_proxy_/storefront/hot-reloading.js') }}\"></script>\n{{ vite_entry_script_tags('app') }}{{ vite_entry_script_tags('administration') }}",
            ),
        ],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let before = capture_workspace_assets(&workspace);
    assert!(!before.names.is_empty());
    assert_eq!(
        vec!["@Administration", "asset", "theme"],
        sorted(before.packages.clone())
    );
    assert_eq!(vec!["app".to_owned()], before.encore);
    assert_eq!(vec!["app".to_owned()], before.importmap);
    assert_eq!(vec!["administration", "app"], sorted(before.vite.clone()));
    assert_eq!(1, before.administration.len());
    assert_eq!(1, before.theme.len());
    assert_eq!(1, before.administration_usages.len());
    assert_eq!(1, before.html_usages.len());
    assert_eq!(1, before.vite_usages.len());
    assert_eq!(1, before.vite_administration_usages.len());
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    assert_eq!(
        before,
        capture_workspace_assets(&reopened),
        "cache restore must not require rescanning"
    );
    std::fs::write(root.join("templates/page.html.twig"), "Changed template").unwrap();
    for relative in [
        "vite.config.ts",
        "public/bundles/administration/administration/static/img/favicon/favicon-16x16.png",
    ] {
        std::fs::remove_file(root.join(relative)).unwrap();
    }
    index_all(&reopened);
    let after = capture_workspace_assets(&reopened);
    assert!(after.vite.is_empty(), "{:?}", after.vite);
    assert!(after.administration.is_empty());
    assert!(after.administration_usages.is_empty());
    assert!(after.html_usages.is_empty());
    assert!(after.vite_usages.is_empty());
    assert!(after.vite_administration_usages.is_empty());
    assert_eq!(before.theme, after.theme);
    assert_eq!(before.encore, after.encore);
    assert_eq!(before.importmap, after.importmap);
    reopened.close_workspace().unwrap();

    let restored = open_workspace(root, cache.path());
    assert_eq!(
        after,
        capture_workspace_assets(&restored),
        "updates and removals must survive restart"
    );
    restored.close_workspace().unwrap();
}
