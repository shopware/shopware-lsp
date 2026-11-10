use super::*;
use crate::semantic::embedded::embedded_xpath_semantic_tokens;
use std::collections::HashMap;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    Context, SemanticToken, SemanticTokensProvider, SemanticTokensRequest, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::{
    SEMANTIC_TOKEN_CLASS as CLASS, SEMANTIC_TOKEN_FUNCTION as FUNCTION,
    SEMANTIC_TOKEN_KEYWORD as KEYWORD, SEMANTIC_TOKEN_NUMBER as NUMBER,
    SEMANTIC_TOKEN_OPERATOR as OPERATOR, SEMANTIC_TOKEN_PROPERTY as PROPERTY,
    SEMANTIC_TOKEN_STRING as STRING, SEMANTIC_TOKEN_TYPE as TYPE,
    SEMANTIC_TOKEN_VARIABLE as VARIABLE,
};
use swlsp_php::{EmbeddedPhpString, PhpIndex};

fn request(uri: &str, source: &str, version: i32) -> SemanticTokensRequest {
    SemanticTokensRequest {
        params: protocol::SemanticTokensParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: uri.to_owned(),
            },
            ..Default::default()
        },
        document: Arc::new(TextDocument::new(uri, source, version)),
    }
}

fn tokens(
    provider: &dyn SemanticTokensProvider,
    uri: &str,
    source: &str,
    version: i32,
) -> Vec<SemanticToken> {
    provider
        .get_semantic_tokens(&Context::background(), &request(uri, source, version))
        .expect("semantic tokens")
}

fn text<'a>(source: &'a str, token: &SemanticToken) -> &'a str {
    &source[token.range]
}

fn assert_tokens(source: &str, actual: &[SemanticToken], expected: &[(&str, u32)]) {
    let actual: Vec<(&str, u32)> = actual
        .iter()
        .map(|token| (text(source, token), token.token_type))
        .collect();
    assert_eq!(actual, expected);
}

// --- PHP embedded languages -------------------------------------------------

struct Php {
    _dir: tempfile::TempDir,
    index: Arc<PhpIndex>,
}

impl Drop for Php {
    fn drop(&mut self) {
        self.index.close().expect("close PHP index");
    }
}

fn php_index(files: &[(&str, &str)]) -> Php {
    let dir = tempfile::tempdir().unwrap();
    let index = Arc::new(PhpIndex::open(dir.path(), None).expect("open PHP index"));
    for (path, source) in files {
        index
            .index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .expect("index file");
    }
    Php { _dir: dir, index }
}

const CRAWLER_FILTER: &str = r#"<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filter(string $selector): static {}
}
"#;

#[test]
fn embedded_css_semantic_tokens_color_selector_parts() {
    let php = php_index(&[("/project/vendor/Crawler.php", CRAWLER_FILTER)]);
    let source = r#"<?php
use Symfony\Component\DomCrawler\Crawler;
function find(Crawler $crawler): void {
    $crawler->filter('.product > a[data-active="true"]:nth-child(2), #main');
}
"#;
    let actual = tokens(
        &EmbeddedCssProvider::new(Some((*php.index).clone())),
        "file:///project/src/CrawlerTest.php",
        source,
        1,
    );
    assert_tokens(
        source,
        &actual,
        &[
            ("product", CLASS),
            (">", OPERATOR),
            ("a", TYPE),
            ("data-active", PROPERTY),
            ("=", OPERATOR),
            ("true", STRING),
            ("nth-child", FUNCTION),
            ("2", NUMBER),
            ("main", VARIABLE),
        ],
    );
}

#[test]
fn embedded_json_semantic_tokens_color_keys_and_scalar_values() {
    let php = php_index(&[(
        "/project/vendor/JsonResponse.php",
        r#"<?php
namespace Symfony\Component\HttpFoundation;
class JsonResponse {
    public static function fromJsonString(string $data): static {}
    public function setJson(string $data): static {}
}
"#,
    )]);
    let source = r#"<?php
use Symfony\Component\HttpFoundation\JsonResponse;
function send(JsonResponse $response): void {
    $response->setJson('{"name":"Shopware","count":2,"active":true,"empty":null}');
    JsonResponse::fromJsonString("{\"second\":\"value\"}");
}
"#;
    let actual = tokens(
        &EmbeddedJsonProvider::new(Some((*php.index).clone())),
        "file:///project/src/Controller.php",
        source,
        1,
    );
    assert_tokens(
        source,
        &actual,
        &[
            ("name", PROPERTY),
            ("Shopware", STRING),
            ("count", PROPERTY),
            ("2", NUMBER),
            ("active", PROPERTY),
            ("true", KEYWORD),
            ("empty", PROPERTY),
            ("null", KEYWORD),
            ("second", PROPERTY),
            ("value", STRING),
        ],
    );
}

#[test]
fn embedded_language_semantic_tokens_dispatch_all_languages() {
    let php = php_index(&[
        (
            "/project/vendor/JsonResponse.php",
            r#"<?php
namespace Symfony\Component\HttpFoundation;
class JsonResponse { public function setJson(string $data): static {} }
"#,
        ),
        (
            "/project/vendor/Crawler.php",
            r#"<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filter(string $selector): static {}
    public function evaluate(string $expression): mixed {}
}
"#,
        ),
    ]);
    let source = r#"<?php
use Symfony\Component\HttpFoundation\JsonResponse;
use Symfony\Component\DomCrawler\Crawler;
function inspect(JsonResponse $response, Crawler $crawler): void {
    $response->setJson('{"payload":"ready"}');
    $crawler->filter('.product[data-active="yes"]');
    $crawler->evaluate('.//article[@data-id=$identifier]');
}
"#;
    let actual = tokens(
        &EmbeddedLanguageProvider::new(Some((*php.index).clone())),
        "file:///project/src/Inspector.php",
        source,
        1,
    );
    let types: HashMap<&str, u32> = actual
        .iter()
        .map(|token| (text(source, token), token.token_type))
        .collect();
    assert_eq!(types.get("payload"), Some(&PROPERTY));
    assert_eq!(types.get("ready"), Some(&STRING));
    assert_eq!(types.get("product"), Some(&CLASS));
    assert_eq!(types.get("article"), Some(&TYPE));
    assert_eq!(types.get("identifier"), Some(&VARIABLE));
}

#[test]
fn embedded_xpath_operator_words_remain_names_in_path_contexts() {
    let value = ".//and/@or | .//item[price div 2]";
    let expression = EmbeddedPhpString {
        value: value.as_bytes().to_vec(),
        source_offsets: (0..=value.len() as u32).collect(),
        ..Default::default()
    };
    let mut and_type = None;
    let mut or_type = None;
    let mut div_type = None;
    for token in embedded_xpath_semantic_tokens(&expression) {
        match &value[token.range] {
            "and" => and_type = Some(token.token_type),
            "or" => or_type = Some(token.token_type),
            "div" => div_type = Some(token.token_type),
            _ => {}
        }
    }
    assert_eq!(and_type, Some(TYPE));
    assert_eq!(or_type, Some(PROPERTY));
    assert_eq!(div_type, Some(OPERATOR));
}

#[test]
fn embedded_xpath_semantic_tokens_color_expression_parts() {
    let php = php_index(&[(
        "/project/vendor/Crawler.php",
        r#"<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function evaluate(string $expression): mixed {}
}
"#,
    )]);
    let source = r#"<?php
use Symfony\Component\DomCrawler\Crawler;
function find(Crawler $crawler): void {
    $crawler->evaluate('count(.//article[@data-id=$id and contains(@class, "active")]) > 0');
}
"#;
    let actual = tokens(
        &EmbeddedXPathProvider::new(Some((*php.index).clone())),
        "file:///project/src/CrawlerTest.php",
        source,
        1,
    );
    assert_tokens(
        source,
        &actual,
        &[
            ("count", FUNCTION),
            (".", OPERATOR),
            ("//", OPERATOR),
            ("article", TYPE),
            ("@", OPERATOR),
            ("data-id", PROPERTY),
            ("=", OPERATOR),
            ("id", VARIABLE),
            ("and", OPERATOR),
            ("contains", FUNCTION),
            ("@", OPERATOR),
            ("class", PROPERTY),
            ("active", STRING),
            (">", OPERATOR),
            ("0", NUMBER),
        ],
    );
}

#[test]
fn embedded_providers_ignore_non_php_documents() {
    let php = php_index(&[("/project/vendor/Crawler.php", CRAWLER_FILTER)]);
    let source = "$crawler->filter('.product');";
    assert!(
        tokens(
            &EmbeddedLanguageProvider::new(Some((*php.index).clone())),
            "file:///project/src/test.twig",
            source,
            1,
        )
        .is_empty()
    );
}

#[test]
fn embedded_tokens_keep_invalid_utf8_bytes_inside_identifiers() {
    // An invalid byte such as `\xff` counts as a non-ASCII character, so it
    // continues an identifier instead of ending it.
    let value = b"a\xff.b".to_vec();
    let expression = EmbeddedPhpString {
        source_offsets: (0..=value.len() as u32).collect(),
        value,
        ..Default::default()
    };
    let ranges: Vec<(u32, u32, u32)> =
        crate::semantic::embedded::embedded_css_semantic_tokens(&expression)
            .into_iter()
            .map(|token| {
                (
                    u32::from(token.range.start()),
                    u32::from(token.range.end()),
                    token.token_type,
                )
            })
            .collect();
    assert_eq!(ranges, vec![(0, 2, TYPE), (3, 4, CLASS)]);
    let ranges: Vec<(u32, u32, u32)> = embedded_xpath_semantic_tokens(&expression)
        .into_iter()
        .map(|token| {
            (
                u32::from(token.range.start()),
                u32::from(token.range.end()),
                token.token_type,
            )
        })
        .collect();
    assert_eq!(ranges, vec![(0, 4, TYPE)]);
}

#[test]
fn embedded_providers_without_php_index_return_nothing() {
    let source = "<?php\n$crawler->filter('.product > a');\n";
    let providers: [&dyn SemanticTokensProvider; 4] = [
        &EmbeddedLanguageProvider::new(None),
        &EmbeddedJsonProvider::new(None),
        &EmbeddedCssProvider::new(None),
        &EmbeddedXPathProvider::new(None),
    ];
    for provider in providers {
        assert!(tokens(provider, "file:///project/src/A.php", source, 1).is_empty());
    }
}
