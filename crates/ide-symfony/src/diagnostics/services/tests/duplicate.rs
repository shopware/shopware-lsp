use swlsp_lsp_protocol::DiagnosticSeverity;

use crate::diagnostics::services::DuplicateAnalyzer;
use crate::diagnostics::test_support::check_problems;

fn check_duplicates(uri: &str, fixture: &str) {
    let (_, problems) = check_problems(&DuplicateAnalyzer::new(), uri, fixture);
    for problem in &problems {
        assert_eq!(problem.severity, Some(DiagnosticSeverity::WARNING));
    }
}

#[test]
fn duplicate_diagnostics_for_yaml_definitions() {
    check_duplicates(
        "file:///project/config.yaml",
        r"parameters:
  {|symfony.parameter.duplicate:app.mode|}: one
  {|symfony.parameter.duplicate:app.mode|}: two
services:
  {|symfony.service.duplicate:app.handler|}:
    class: App\First
  {|symfony.service.duplicate:app.handler|}:
    class: App\Second
{|symfony.route.duplicate:product.show|}:
  path: /first
{|symfony.route.duplicate:product.show|}:
  path: /second
",
    );
}

#[test]
fn duplicate_diagnostics_for_xml_definitions() {
    for fixture in [
        r#"<container>
<parameters>
  <parameter key="{|symfony.parameter.duplicate:app.mode|}">one</parameter>
  <parameter key="{|symfony.parameter.duplicate:app.mode|}">two</parameter>
</parameters>
<services>
  <service id="{|symfony.service.duplicate:app.handler|}" class="App\First"/>
  <alias id="{|symfony.service.duplicate:app.handler|}" service="app.first"/>
</services>
</container>"#,
        r#"<routes>
  <route id="{|symfony.route.duplicate:product.show|}" path="/first"/>
  <route id="{|symfony.route.duplicate:product.show|}" path="/second"/>
</routes>"#,
    ] {
        check_duplicates("file:///project/config.xml", fixture);
    }
}

#[test]
fn duplicate_diagnostics_for_php_route_attributes() {
    check_duplicates(
        "file:///project/Controller.php",
        r"<?php
class Controller {
    #[Route('/first', name: '{|symfony.route.duplicate:product.show|}')]
    public function first(): void {}

    #[Route('/second', '{|symfony.route.duplicate:product.show|}')]
    public function second(): void {}
}",
    );
}
