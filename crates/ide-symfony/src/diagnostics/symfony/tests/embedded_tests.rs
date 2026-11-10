use swlsp_lsp_protocol::DiagnosticSeverity;

use super::{index_file, open_php};
use crate::diagnostics::symfony::{
    EmbeddedCssAnalyzer, EmbeddedJsonAnalyzer, EmbeddedLanguageAnalyzer, EmbeddedXPathAnalyzer,
};
use crate::diagnostics::test_support::check_problems;

#[test]
fn embedded_css_diagnostics_validate_typed_dom_crawler_selectors() {
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/vendor/Crawler.php",
        r"<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filter(string $selector): static {}
    public function children(string $selector): static {}
}
",
    );
    index_file(
        &php,
        "/project/vendor/CssSelectorConverter.php",
        r"<?php
namespace Symfony\Component\CssSelector;
class CssSelectorConverter {
    public function toXPath(string $selector): string {}
}
",
    );
    let source = r#"<?php
use Symfony\Component\DomCrawler\Crawler;
use Symfony\Component\CssSelector\CssSelectorConverter;
final class Other { public function filter(string $selector): void {} }
function find(
    Crawler $crawler,
    CssSelectorConverter $converter,
    Other $other,
    string $name,
): void {
    $crawler->filter('.product > a[data-active="true"]');
    $crawler->children('.broken{|symfony.php.embedded_css.invalid:[|}');
    $converter->toXPath('button:not{|symfony.php.embedded_css.invalid:(|}');
    $other->filter('.ignored[');
    $crawler->filter(".dynamic-$name[");
}
"#;
    let (_, result) = check_problems(
        &EmbeddedCssAnalyzer::new(Some(php.clone())),
        "file:///project/src/CrawlerTest.php",
        source,
    );
    for problem in &result {
        assert!(problem.message.contains("Invalid CSS selector:"));
    }
}

#[test]
fn embedded_json_diagnostics_validate_typed_json_response_strings() {
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/vendor/JsonResponse.php",
        r"<?php
namespace Symfony\Component\HttpFoundation;
class JsonResponse {
    public static function fromJsonString(string $data): static {}
    public function setJson(string $data): static {}
}
",
    );
    let source = r#"<?php
use Symfony\Component\HttpFoundation\JsonResponse;

final class Other {
    public function setJson(string $data): void {}
}

function send(JsonResponse $response, Other $other, string $value): void {
    $response->setJson('{"valid":[1,true,null]}');
    $response->setJson('{"broken":{|symfony.php.embedded_json.invalid:}|}');
    JsonResponse::fromJsonString("{\"first\":true {|symfony.php.embedded_json.invalid:\"second\"|}:false}");
    $other->setJson('{"ignored":}');
    $response->setJson("{\"dynamic\":\"$value\"}");
}
"#;
    let (_, result) = check_problems(
        &EmbeddedJsonAnalyzer::new(Some(php.clone())),
        "file:///project/src/Controller.php",
        source,
    );
    for problem in &result {
        assert_eq!(problem.source, "symfony");
        assert_eq!(problem.severity, Some(DiagnosticSeverity::ERROR));
        assert!(problem.message.contains("Invalid JSON: expected"));
    }
}

#[test]
fn embedded_language_diagnostics_dispatch_all_languages() {
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/vendor/JsonResponse.php",
        r"<?php
namespace Symfony\Component\HttpFoundation;
class JsonResponse { public function setJson(string $data): static {} }
",
    );
    index_file(
        &php,
        "/project/vendor/Crawler.php",
        r"<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filter(string $selector): static {}
    public function evaluate(string $expression): mixed {}
}
",
    );
    let source = r#"<?php
use Symfony\Component\HttpFoundation\JsonResponse;
use Symfony\Component\DomCrawler\Crawler;
function inspect(JsonResponse $response, Crawler $crawler): void {
    $response->setJson('{"broken":{|symfony.php.embedded_json.invalid:}|}');
    $crawler->filter('.broken{|symfony.php.embedded_css.invalid:[|}');
    $crawler->evaluate('count(.//{|symfony.php.embedded_xpath.invalid:article|}');
}
"#;
    check_problems(
        &EmbeddedLanguageAnalyzer::new(Some(php.clone())),
        "file:///project/src/Inspector.php",
        source,
    );
}

#[test]
fn embedded_xpath_diagnostics_validate_typed_crawler_expressions() {
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/vendor/Crawler.php",
        r"<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filterXPath(string $expression): static {}
    public function evaluate(string $expression): mixed {}
}
",
    );
    let source = r#"<?php
use Symfony\Component\DomCrawler\Crawler;
final class Other { public function evaluate(string $expression): void {} }
function find(Crawler $crawler, Other $other, string $id): void {
    $crawler->filterXPath('.//article[@id="valid"]');
    $crawler->filterXPath('.//article[@id={|symfony.php.embedded_xpath.invalid:"missing"|}');
    $crawler->evaluate('count(.//{|symfony.php.embedded_xpath.invalid:article|}');
    $other->evaluate('.//ignored[');
    $crawler->evaluate(".//article[@id=\"$id\"");
}
"#;
    let (_, result) = check_problems(
        &EmbeddedXPathAnalyzer::new(Some(php.clone())),
        "file:///project/src/CrawlerTest.php",
        source,
    );
    for problem in &result {
        assert!(problem.message.contains("Invalid XPath expression:"));
    }
}
