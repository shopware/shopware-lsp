use swlsp_syntax::TextRange;

use super::{index, open};
use crate::{
    embedded_css_selectors, embedded_json_literals, embedded_language_strings,
    embedded_xpath_expressions,
};

#[test]
fn json_literals_resolve_receivers_decode_and_map_strings() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    index(
        &idx,
        "/project/vendor/JsonResponse.php",
        r#"<?php
namespace Symfony\Component\HttpFoundation;
class JsonResponse {
    public static function fromJsonString(string $data): static {}
    public function setJson(string $data): static {}
}
"#,
    );
    let source = r#"<?php
namespace App;
use Symfony\Component\HttpFoundation\JsonResponse as Response;
final class CustomResponse extends Response {}
final class OtherResponse {
    public function setJson(string $data): void {}
}
function send(
    Response $response,
    CustomResponse $custom,
    OtherResponse $other,
    string $value,
): void {
    $response->setJson('{"ok":true}');
    Response::fromJsonString("{\"name\":\"Shopware\"}");
    $custom->setJson(data: '{"items":[1]}');
    $other->setJson('{broken}');
    $response->setJson("{\"dynamic\":\"$value\"}");
}
"#;
    let parsed = swlsp_parser_php::parse(source);
    assert!(parsed.errors.is_empty());
    let literals = embedded_json_literals(
        &idx,
        "/project/src/Controller.php",
        1,
        source,
        &parsed.root(),
    );
    assert_eq!(literals.len(), 3);
    assert_eq!(literals[0].text(), r#"{"ok":true}"#);
    assert_eq!(literals[1].text(), r#"{"name":"Shopware"}"#);
    assert_eq!(literals[2].text(), r#"{"items":[1]}"#);
    let value = literals[1].text();
    let name_start = value.find("name").unwrap() as u32;
    let name_range =
        literals[1].source_range(TextRange::new(name_start.into(), (name_start + 4).into()));
    assert_eq!(&source[name_range], "name");
    assert_eq!(
        literals[1].source_offsets.len(),
        literals[1].value.len() + 1
    );
    idx.close().unwrap();
}

#[test]
fn css_selectors_resolve_symfony_receivers() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    index(
        &idx,
        "/project/vendor/Crawler.php",
        r#"<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filter(string $selector): static {}
    public function children(string $selector): static {}
}
"#,
    );
    index(
        &idx,
        "/project/vendor/CssSelectorConverter.php",
        r#"<?php
namespace Symfony\Component\CssSelector;
class CssSelectorConverter {
    public function toXPath(string $selector): string {}
}
"#,
    );
    let source = r#"<?php
namespace App;
use Symfony\Component\DomCrawler\Crawler;
use Symfony\Component\CssSelector\CssSelectorConverter;
final class CustomCrawler extends Crawler {}
final class Other {
    public function filter(string $selector): void {}
}
function find(
    Crawler $crawler,
    CustomCrawler $custom,
    CssSelectorConverter $converter,
    Other $other,
    string $name,
): void {
    $crawler->filter('.product[data-active="true"]');
    $crawler->children("ul > li:nth-child(2)");
    $custom->filter('#main .card');
    $converter->toXPath('[data-controller~="cart"]');
    $other->filter('.ignored[');
    $crawler->filter(".dynamic-$name");
}
"#;
    let parsed = swlsp_parser_php::parse(source);
    assert!(parsed.errors.is_empty());
    let selectors = embedded_css_selectors(
        &idx,
        "/project/src/CrawlerTest.php",
        1,
        source,
        &parsed.root(),
    );
    let values: Vec<_> = selectors
        .iter()
        .map(|selector| selector.text().into_owned())
        .collect();
    assert_eq!(
        values,
        [
            r#".product[data-active="true"]"#,
            "ul > li:nth-child(2)",
            "#main .card",
            r#"[data-controller~="cart"]"#,
        ]
    );
    idx.close().unwrap();
}

#[test]
fn xpath_expressions_resolve_symfony_crawler() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    index(
        &idx,
        "/project/vendor/Crawler.php",
        r#"<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filterXPath(string $expression): static {}
    public function evaluate(string $expression): mixed {}
}
"#,
    );
    let source = r#"<?php
use Symfony\Component\DomCrawler\Crawler;
final class CustomCrawler extends Crawler {}
final class Other { public function evaluate(string $expression): void {} }
function find(
    Crawler $crawler,
    CustomCrawler $custom,
    Other $other,
    string $id,
): void {
    $crawler->filterXPath('.//article[@data-id="42"]');
    $custom->evaluate("count(.//article[@id=\"main\"])");
    $other->evaluate('.//ignored[');
    $crawler->evaluate(".//article[@id=\"$id\"]");
}
"#;
    let parsed = swlsp_parser_php::parse(source);
    assert!(parsed.errors.is_empty());
    let expressions = embedded_xpath_expressions(
        &idx,
        "/project/src/CrawlerTest.php",
        1,
        source,
        &parsed.root(),
    );
    assert_eq!(expressions.len(), 2);
    assert_eq!(expressions[0].text(), r#".//article[@data-id="42"]"#);
    assert_eq!(expressions[1].text(), r#"count(.//article[@id="main"])"#);
    assert_eq!(
        embedded_language_strings(&idx, "/x.php", 1, source, &parsed.root()).len(),
        2
    );
    idx.close().unwrap();
}

#[test]
fn decodes_php_escapes() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    index(
        &idx,
        "/project/vendor/JsonResponse.php",
        "<?php\nnamespace Symfony\\Component\\HttpFoundation;\nclass JsonResponse { public function setJson(string $data): static {} }\n",
    );
    let source = "<?php\nuse Symfony\\Component\\HttpFoundation\\JsonResponse;\nfunction f(JsonResponse $r): void {\n    $r->setJson(\"\\x41\\101\\u{1F600}\\q\\777\");\n    $r->setJson('it\\'s \\\\ \\n');\n}\n";
    let parsed = swlsp_parser_php::parse(source);
    let literals = embedded_json_literals(&idx, "/x.php", 1, source, &parsed.root());
    assert_eq!(literals.len(), 2);
    assert_eq!(literals[0].text(), "AA\u{1F600}\\q\\777");
    assert_eq!(literals[1].text(), "it's \\ \\n");
    for literal in &literals {
        assert_eq!(literal.source_offsets.len(), literal.value.len() + 1);
    }
    idx.close().unwrap();
}
