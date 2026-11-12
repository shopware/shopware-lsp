//! The Store rules for app manifests, theme.json and the app file layout.

use std::sync::Arc;

use swlsp_uriutil::file_uri;

use super::store_composer::{compliance, write_png};
use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

fn uri(directory: &tempfile::TempDir, relative: &str) -> String {
    file_uri(directory.path().join(relative).to_str().unwrap())
}

#[test]
fn app_manifest_requires_store_metadata() {
    let directory = tempfile::tempdir().unwrap();
    let uri = uri(&directory, "manifest.xml");
    let analyzer = AppManifestAnalyzer::new(compliance(Some(true)));
    // The icon defaults to Resources/config/plugin.png, which is missing.
    check_problems(
        &analyzer,
        &uri,
        "<manifest>\n  <{|shopware.store.app.author:{|shopware.store.app.copyright:\
         {|shopware.store.app.icon:{|shopware.store.app.license:meta|}|}|}|}>\n    <name>Acme</name>\n    \
         <author> </author>\n  </meta>\n  <setup>\n    <{|shopware.store.app.secret:secret|}>s\
         </secret>\n  </setup>\n</manifest>",
    );
    write_png(&directory.path().join("icon.png"), 32, 32, 512);
    check_problems(
        &analyzer,
        &uri,
        "<manifest><meta><author>A</author><copyright>A</copyright><license>MIT</license>\
         <{|shopware.store.app.icon-size:icon|}>icon.png</icon></meta></manifest>",
    );
    write_png(
        &directory.path().join("Resources/config/plugin.png"),
        128,
        128,
        512,
    );
    check_problems(
        &analyzer,
        &uri,
        "<manifest><meta><author>A</author><copyright>A</copyright><license>MIT</license>\
         </meta><setup><registrationUrl>https://example.com</registrationUrl></setup></manifest>",
    );
}

#[test]
fn app_manifest_requires_explicit_opt_in() {
    let source = "<manifest><meta></meta><setup><secret>s</secret></setup></manifest>";
    for enabled in [None, Some(false)] {
        check_problems(
            &AppManifestAnalyzer::new(compliance(enabled)),
            "file:///project/custom/apps/Acme/manifest.xml",
            source,
        );
    }
    // Other XML files are left alone.
    check_problems(
        &AppManifestAnalyzer::new(compliance(Some(true))),
        "file:///project/custom/apps/Acme/Resources/config/config.xml",
        source,
    );
}

#[test]
fn theme_json_requires_an_existing_preview() {
    let directory = tempfile::tempdir().unwrap();
    let uri = uri(&directory, "src/Resources/theme.json");
    let analyzer = StoreThemeAnalyzer::new(compliance(Some(true)));
    check_problems(
        &analyzer,
        &uri,
        r#"{|shopware.store.theme.preview-media:{"name": "Acme"}|}"#,
    );
    let preview = r#"{"name": "Acme", "{|shopware.store.theme.preview-media:previewMedia|}": "app/storefront/dist/assets/preview.jpg"}"#;
    check_problems(&analyzer, &uri, preview);
    let image = directory
        .path()
        .join("src/Resources/app/storefront/dist/assets/preview.jpg");
    std::fs::create_dir_all(image.parent().unwrap()).unwrap();
    std::fs::write(image, "image").unwrap();
    let valid = preview.replace(
        "{|shopware.store.theme.preview-media:previewMedia|}",
        "previewMedia",
    );
    check_problems(&analyzer, &uri, &valid);
    // Without the opt-in, and outside `Resources`, nothing is reported.
    check_problems(
        &StoreThemeAnalyzer::new(compliance(Some(false))),
        &uri,
        r#"{"name": "Acme"}"#,
    );
    check_problems(
        &analyzer,
        "file:///project/theme.json",
        r#"{"name": "Acme"}"#,
    );
}

struct AppAt(&'static str);

impl AppLocator for AppAt {
    fn app_root(&self, path: &str) -> Option<String> {
        path.starts_with(&format!("{}/", self.0))
            .then(|| self.0.to_owned())
    }
}

#[test]
fn app_layout_reports_php_and_misplaced_twig_files() {
    let analyzer = AppLayoutAnalyzer::new(
        Arc::new(AppAt("/project/apps/Acme")),
        compliance(Some(true)),
    );
    check_problems(
        &analyzer,
        "file:///project/apps/Acme/src/Service.php",
        "{|shopware.store.app.php-file:<?php|}\n\nclass Service {}\n",
    );
    check_problems(
        &analyzer,
        "file:///project/apps/Acme/views/page.html.twig",
        "{|shopware.store.app.twig-location:{% block page %}{% endblock %}|}\n",
    );
    for allowed in [
        "file:///project/apps/Acme/Resources/views/storefront/page.html.twig",
        "file:///project/apps/Acme/Resources/scripts/cart/discount.twig",
        // Not inside an app.
        "file:///project/custom/plugins/Acme/views/page.html.twig",
    ] {
        check_problems(&analyzer, allowed, "{% block page %}{% endblock %}\n");
    }
    check_problems(
        &analyzer,
        "file:///project/custom/plugins/Acme/src/Service.php",
        "<?php\n",
    );
    let disabled = AppLayoutAnalyzer::new(
        Arc::new(AppAt("/project/apps/Acme")),
        compliance(Some(false)),
    );
    check_problems(
        &disabled,
        "file:///project/apps/Acme/src/Service.php",
        "<?php\n",
    );
}
