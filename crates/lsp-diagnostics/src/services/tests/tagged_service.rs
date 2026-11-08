use swlsp_lsp_protocol::DiagnosticSeverity;

use super::*;
use crate::services::TaggedServiceAnalyzer;

fn tagged_php_index(dir: &Path) -> PhpIndex {
    php_index(
        dir,
        &[(
            "/project/src/Types.php",
            r"<?php
namespace Twig\Extension {
    interface ExtensionInterface {}
}
namespace {
    interface Twig_ExtensionInterface {}
}
namespace Symfony\Component\Form {
    interface FormTypeInterface {}
}
namespace App {
    class BadExtension {}
    class GoodExtension implements \Twig\Extension\ExtensionInterface {}
    class LegacyExtension implements \Twig_ExtensionInterface {}
    class BadForm {}
    class GoodForm implements \Symfony\Component\Form\FormTypeInterface {}
}
",
        )],
    )
}

#[test]
fn tagged_service_diagnostics_for_yaml() {
    let dir = tempfile::tempdir().unwrap();
    let analyzer = TaggedServiceAnalyzer::new(Some(tagged_php_index(dir.path())));
    let document = document(
        "file:///project/config/services.yaml",
        r"services:
  bad:
    class: App\BadExtension
    tags:
      - { name: twig.extension }
  good:
    class: App\GoodExtension
    tags: [twig.extension]
  legacy:
    class: App\LegacyExtension
    tags:
      - twig.extension
  unknown_tag:
    class: App\BadExtension
    tags: [app.custom]
  App\BadExtension:
    tags:
      - { name: twig.extension }
",
    );
    let result = analyze(&analyzer, &document);
    assert_eq!(result.len(), 2);
    for problem in &result {
        assert_eq!(problem.id.as_str(), "symfony.service.tag_type");
        assert_eq!(problem.severity, Some(DiagnosticSeverity::WARNING));
        assert_eq!(range_text(&document, problem.range), "App\\BadExtension");
        assert!(problem.message.contains("twig.extension"));
        assert!(problem.message.contains("Twig_ExtensionInterface"));
    }
}

#[test]
fn tagged_service_diagnostics_for_xml() {
    let dir = tempfile::tempdir().unwrap();
    let analyzer = TaggedServiceAnalyzer::new(Some(tagged_php_index(dir.path())));
    let document = document(
        "file:///project/config/services.xml",
        r#"<?xml version="1.0"?>
<container>
  <services>
    <service id="bad" class="App\BadForm">
      <tag name="form.type"/>
    </service>
    <service id="App\GoodForm">
      <tag name="form.type"/>
    </service>
    <service id="App\BadForm">
      <tag name="form.type"/>
    </service>
  </services>
</container>"#,
    );
    let result = analyze(&analyzer, &document);
    assert_eq!(result.len(), 2);
    for problem in &result {
        assert_eq!(range_text(&document, problem.range), "App\\BadForm");
        assert!(
            problem
                .message
                .contains("Symfony\\Component\\Form\\FormTypeInterface")
        );
    }
}

#[test]
fn tagged_service_diagnostics_skip_unknown_contracts() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        dir.path(),
        &[(
            "/project/src/Extension.php",
            "<?php\nnamespace App;\nclass Extension {}\n",
        )],
    );
    let document = document(
        "file:///project/config/services.yaml",
        "services:\n  App\\Extension:\n    tags: [twig.extension]\n",
    );
    assert!(analyze(&TaggedServiceAnalyzer::new(Some(php)), &document).is_empty());
}
