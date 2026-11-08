use swlsp_lsp_protocol::DiagnosticSeverity;

use super::{analyze, index_file, open_php, start_line};
use crate::symfony::{
    EmbeddedCssAnalyzer, EmbeddedJsonAnalyzer, EmbeddedLanguageAnalyzer, EmbeddedXPathAnalyzer,
    INVALID_EMBEDDED_CSS_CODE, INVALID_EMBEDDED_JSON_CODE, INVALID_EMBEDDED_XPATH_CODE,
};
use crate::test_support::diagnostics_document;

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
    $crawler->children('.broken[');
    $converter->toXPath('button:not(');
    $other->filter('.ignored[');
    $crawler->filter(".dynamic-$name[");
}
"#;
    let document = diagnostics_document("file:///project/src/CrawlerTest.php", source);
    let result = analyze(&EmbeddedCssAnalyzer::new(Some(php.clone())), &document);
    assert_eq!(result.len(), 2);
    for problem in &result {
        assert_eq!(problem.id.0, INVALID_EMBEDDED_CSS_CODE);
        assert!(problem.message.contains("Invalid CSS selector:"));
        assert!([11, 12].contains(&start_line(&document, problem)));
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
    $response->setJson('{"broken":}');
    JsonResponse::fromJsonString("{\"first\":true \"second\":false}");
    $other->setJson('{"ignored":}');
    $response->setJson("{\"dynamic\":\"$value\"}");
}
"#;
    let document = diagnostics_document("file:///project/src/Controller.php", source);
    let result = analyze(&EmbeddedJsonAnalyzer::new(Some(php.clone())), &document);
    assert_eq!(result.len(), 2);
    for problem in &result {
        assert_eq!(problem.id.0, INVALID_EMBEDDED_JSON_CODE);
        assert_eq!(problem.source, "symfony");
        assert_eq!(problem.severity, Some(DiagnosticSeverity::ERROR));
        assert!(problem.message.contains("Invalid JSON: expected"));
    }
    assert_eq!(start_line(&document, &result[0]), 9);
    assert_eq!(start_line(&document, &result[1]), 10);
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
    $response->setJson('{"broken":}');
    $crawler->filter('.broken[');
    $crawler->evaluate('count(.//article');
}
"#;
    let document = diagnostics_document("file:///project/src/Inspector.php", source);
    let result = analyze(&EmbeddedLanguageAnalyzer::new(Some(php.clone())), &document);
    assert_eq!(result.len(), 3);
    let mut codes: Vec<&str> = result.iter().map(|problem| problem.id.0.as_str()).collect();
    codes.sort_unstable();
    assert_eq!(
        codes,
        vec![
            INVALID_EMBEDDED_CSS_CODE,
            INVALID_EMBEDDED_JSON_CODE,
            INVALID_EMBEDDED_XPATH_CODE
        ]
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
    $crawler->filterXPath('.//article[@id="missing"');
    $crawler->evaluate('count(.//article');
    $other->evaluate('.//ignored[');
    $crawler->evaluate(".//article[@id=\"$id\"");
}
"#;
    let document = diagnostics_document("file:///project/src/CrawlerTest.php", source);
    let result = analyze(&EmbeddedXPathAnalyzer::new(Some(php.clone())), &document);
    assert_eq!(result.len(), 2);
    for problem in &result {
        assert_eq!(problem.id.0, INVALID_EMBEDDED_XPATH_CODE);
        assert!(problem.message.contains("Invalid XPath expression:"));
        assert!([5, 6].contains(&start_line(&document, problem)));
    }
}
