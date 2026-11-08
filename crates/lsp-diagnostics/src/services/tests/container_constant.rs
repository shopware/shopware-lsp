use swlsp_lsp_protocol::DiagnosticSeverity;

use super::*;
use crate::services::ContainerConstantAnalyzer;

#[test]
fn container_constant_diagnostics_for_yaml_and_xml() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        dir.path(),
        &[(
            "/project/src/Mode.php",
            r"<?php
namespace {
    const APP_LIMIT = 10;
}
namespace App {
    class Mode {
        public const ACTIVE = 'active';
    }
    enum State {
        case ACTIVE;
    }
}
",
        )],
    );
    let analyzer = ContainerConstantAnalyzer::new(Some(php));
    let tests = [
        (
            "yaml",
            "file:///project/config/services.yaml",
            r"parameters:
  active: !php/const App\Mode::ACTIVE
  global: !php/const APP_LIMIT
  legacy_missing: !php/const:App\Mode::MISSING
  modern_missing: !php/const App\Missing::VALUE
  active_enum: !php/enum App\State::ACTIVE
  all_enum_cases: !php/enum App\State
  missing_enum_case: !php/enum App\State::MISSING
  missing_enum: !php/enum App\MissingState
",
        ),
        (
            "xml",
            "file:///project/config/services.xml",
            r#"<container><services><service id="app">
  <argument type="constant">App\Mode::ACTIVE</argument>
  <argument type="constant">App\Mode::MISSING</argument>
  <argument type="string">App\Mode::IGNORED</argument>
</service></services></container>"#,
        ),
    ];
    for (name, uri, source) in tests {
        let document = document(uri, source);
        let result = analyze(&analyzer, &document);
        if name == "yaml" {
            let texts: Vec<String> = result
                .iter()
                .map(|problem| range_text(&document, problem.range))
                .collect();
            assert_eq!(
                texts,
                [
                    "App\\Mode::MISSING",
                    "App\\Missing::VALUE",
                    "App\\State::MISSING",
                    "App\\MissingState"
                ]
            );
        } else {
            assert_eq!(result.len(), 1);
            assert_eq!(range_text(&document, result[0].range), "App\\Mode::MISSING");
        }
        for (index, problem) in result.iter().enumerate() {
            assert_eq!(problem.id.as_str(), "symfony.constant.missing");
            assert_eq!(problem.severity, Some(DiagnosticSeverity::ERROR));
            let expected = if name == "yaml" && index >= 2 {
                "Symfony: enum or case not found"
            } else {
                "Symfony: constant not found"
            };
            assert_eq!(problem.message, expected);
        }
    }
}
