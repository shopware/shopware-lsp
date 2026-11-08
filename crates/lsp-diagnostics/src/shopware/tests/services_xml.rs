use serde_json::json;
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_uriutil::file_uri;

use super::{analyze, highlight};
use crate::shopware::*;
use crate::test_support::document;

fn temp_uri(dir: &tempfile::TempDir, relative: &str) -> String {
    file_uri(&dir.path().join(relative).to_string_lossy())
}

#[test]
fn services_xml_deprecation_diagnostic() {
    let dir = tempfile::tempdir().unwrap();
    let document = document(
        &temp_uri(&dir, "src/Resources/config/services.xml"),
        r#"<?xml version="1.0"?>
<container xmlns="http://symfony.com/schema/dic/services">
    <services>
        <service id="App\Example" class="App\Example"/>
    </services>
</container>"#,
    );
    let problems = analyze(&ServicesXmlDeprecationAnalyzer::new(), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].id.as_str(), SERVICES_XML_DEPRECATED_CODE);
    assert_eq!(problems[0].severity, Some(DiagnosticSeverity::WARNING));
    assert_eq!(problems[0].tags, vec![DiagnosticTag::DEPRECATED]);
    assert_eq!(highlight(&document, &problems[0]), "container");
    assert_eq!(problems[0].payload, Some(json!({"convertible": true})));
}

#[test]
fn services_xml_deprecation_diagnostic_has_no_automatic_conversion_for_unsupported_xml() {
    let dir = tempfile::tempdir().unwrap();
    let document = document(
        &temp_uri(&dir, "Resources/config/services.xml"),
        r#"<container><services><stack id="app"/></services></container>"#,
    );
    let problems = analyze(&ServicesXmlDeprecationAnalyzer::new(), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].payload, Some(json!({"convertible": false})));
}

#[test]
fn services_xml_deprecation_diagnostic_ignores_explicitly_loaded_xml() {
    let document = document("file:///project/config/services.xml", "<container/>");
    assert!(analyze(&ServicesXmlDeprecationAnalyzer::new(), &document).is_empty());
}
