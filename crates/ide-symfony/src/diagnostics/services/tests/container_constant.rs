use swlsp_lsp_protocol::DiagnosticSeverity;

use super::*;
use crate::diagnostics::services::ContainerConstantAnalyzer;
use crate::diagnostics::test_support::check_problems;

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
            "file:///project/config/services.yaml",
            r"parameters:
  active: !php/const App\Mode::ACTIVE
  global: !php/const APP_LIMIT
  legacy_missing: !php/const:{|symfony.constant.missing:App\Mode::MISSING|}
  modern_missing: !php/const {|symfony.constant.missing:App\Missing::VALUE|}
  active_enum: !php/enum App\State::ACTIVE
  all_enum_cases: !php/enum App\State
  missing_enum_case: !php/enum {|symfony.constant.missing:App\State::MISSING|}
  missing_enum: !php/enum {|symfony.constant.missing:App\MissingState|}
",
            [
                "Symfony: constant not found",
                "Symfony: constant not found",
                "Symfony: enum or case not found",
                "Symfony: enum or case not found",
            ]
            .as_slice(),
        ),
        (
            "file:///project/config/services.xml",
            r#"<container><services><service id="app">
  <argument type="constant">App\Mode::ACTIVE</argument>
  <argument type="constant">{|symfony.constant.missing:App\Mode::MISSING|}</argument>
  <argument type="string">App\Mode::IGNORED</argument>
</service></services></container>"#,
            ["Symfony: constant not found"].as_slice(),
        ),
    ];
    for (uri, fixture, messages) in tests {
        let (_, problems) = check_problems(&analyzer, uri, fixture);
        for (problem, message) in problems.iter().zip(messages) {
            assert_eq!(problem.severity, Some(DiagnosticSeverity::ERROR));
            assert_eq!(problem.message, *message);
        }
    }
}
