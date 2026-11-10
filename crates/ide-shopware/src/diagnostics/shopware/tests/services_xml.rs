use serde_json::json;
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_uriutil::file_uri;

use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

fn temp_uri(dir: &tempfile::TempDir, relative: &str) -> String {
    file_uri(&dir.path().join(relative).to_string_lossy())
}

#[test]
fn services_xml_deprecation_diagnostic() {
    let dir = tempfile::tempdir().unwrap();
    let (_, problems) = check_problems(
        &ServicesXmlDeprecationAnalyzer::new(),
        &temp_uri(&dir, "src/Resources/config/services.xml"),
        r#"<?xml version="1.0"?>
<{|symfony.services_xml.deprecated:container|} xmlns="http://symfony.com/schema/dic/services">
    <services>
        <service id="App\Example" class="App\Example"/>
    </services>
</container>"#,
    );
    assert_eq!(problems[0].severity, Some(DiagnosticSeverity::WARNING));
    assert_eq!(problems[0].tags, vec![DiagnosticTag::DEPRECATED]);
    assert_eq!(problems[0].payload, Some(json!({"convertible": true})));
}

#[test]
fn services_xml_deprecation_diagnostic_has_no_automatic_conversion_for_unsupported_xml() {
    let dir = tempfile::tempdir().unwrap();
    let (_, problems) = check_problems(
        &ServicesXmlDeprecationAnalyzer::new(),
        &temp_uri(&dir, "Resources/config/services.xml"),
        r#"<{|symfony.services_xml.deprecated:container|}><services><stack id="app"/></services></container>"#,
    );
    assert_eq!(problems[0].payload, Some(json!({"convertible": false})));
}

#[test]
fn services_xml_deprecation_diagnostic_ignores_explicitly_loaded_xml() {
    check_problems(
        &ServicesXmlDeprecationAnalyzer::new(),
        "file:///project/config/services.xml",
        "<container/>",
    );
}
