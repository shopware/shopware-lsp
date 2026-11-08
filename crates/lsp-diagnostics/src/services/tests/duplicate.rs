use std::collections::BTreeMap;

use swlsp_lsp_protocol::DiagnosticSeverity;

use super::*;
use crate::services::DuplicateAnalyzer;

fn assert_duplicate_codes(problems: &[Problem], expected: &[(&str, usize)]) {
    let mut actual: BTreeMap<String, usize> = BTreeMap::new();
    for problem in problems {
        *actual.entry(problem.id.0.clone()).or_default() += 1;
        assert_eq!(problem.severity, Some(DiagnosticSeverity::WARNING));
    }
    let expected: BTreeMap<String, usize> = expected
        .iter()
        .map(|(code, count)| ((*code).to_owned(), *count))
        .collect();
    assert_eq!(actual, expected);
}

#[test]
fn duplicate_diagnostics_for_yaml_definitions() {
    let document = document(
        "file:///project/config.yaml",
        r"parameters:
  app.mode: one
  app.mode: two
services:
  app.handler:
    class: App\First
  app.handler:
    class: App\Second
product.show:
  path: /first
product.show:
  path: /second
",
    );
    let result = analyze(&DuplicateAnalyzer::new(), &document);
    assert_duplicate_codes(
        &result,
        &[
            ("symfony.route.duplicate", 2),
            ("symfony.service.duplicate", 2),
            ("symfony.parameter.duplicate", 2),
        ],
    );
}

#[test]
fn duplicate_diagnostics_for_xml_definitions() {
    let tests: [(&str, &[(&str, usize)]); 2] = [
        (
            r#"<container>
<parameters>
  <parameter key="app.mode">one</parameter>
  <parameter key="app.mode">two</parameter>
</parameters>
<services>
  <service id="app.handler" class="App\First"/>
  <alias id="app.handler" service="app.first"/>
</services>
</container>"#,
            &[
                ("symfony.service.duplicate", 2),
                ("symfony.parameter.duplicate", 2),
            ],
        ),
        (
            r#"<routes>
  <route id="product.show" path="/first"/>
  <route id="product.show" path="/second"/>
</routes>"#,
            &[("symfony.route.duplicate", 2)],
        ),
    ];
    for (source, expected) in tests {
        let document = document("file:///project/config.xml", source);
        let result = analyze(&DuplicateAnalyzer::new(), &document);
        assert_duplicate_codes(&result, expected);
    }
}

#[test]
fn duplicate_diagnostics_for_php_route_attributes() {
    let document = document(
        "file:///project/Controller.php",
        r"<?php
class Controller {
    #[Route('/first', name: 'product.show')]
    public function first(): void {}

    #[Route('/second', 'product.show')]
    public function second(): void {}
}",
    );
    let result = analyze(&DuplicateAnalyzer::new(), &document);
    assert_duplicate_codes(&result, &[("symfony.route.duplicate", 2)]);
}
