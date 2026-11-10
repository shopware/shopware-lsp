use super::*;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, TextDocument};
use swlsp_lsp_protocol::{self as protocol, InlayHintKind};
use swlsp_php::PhpIndex;

fn request(document: TextDocument) -> InlayHintRequest {
    let mut params = protocol::InlayHintParams::default();
    params.text_document.uri = document.uri.clone();
    let (line, character) = document
        .line_index()
        .position_utf16(document.source().len() as u32);
    params.range.end = protocol::Position { line, character };
    InlayHintRequest {
        params,
        document: Arc::new(document),
    }
}

fn hints(provider: &dyn InlayHintProvider, request: &InlayHintRequest) -> Vec<protocol::InlayHint> {
    provider
        .get_inlay_hints(&Context::background(), request)
        .unwrap()
}

fn parts(hint: &protocol::InlayHint) -> Vec<protocol::InlayHintLabelPart> {
    match &hint.label {
        protocol::InlayHintLabel::Parts(parts) => parts.clone(),
        protocol::InlayHintLabel::String(_) => panic!("label parts expected"),
    }
}

fn labels(hints: &[protocol::InlayHint]) -> Vec<String> {
    hints
        .iter()
        .map(|hint| match &hint.label {
            protocol::InlayHintLabel::String(label) => label.clone(),
            protocol::InlayHintLabel::Parts(parts) => {
                parts.iter().map(|part| part.value.as_str()).collect()
            }
        })
        .collect()
}

fn php_index(dir: &Path, files: &[(&str, &str)]) -> PhpIndex {
    let php = PhpIndex::open(dir, None).unwrap();
    for (path, source) in files {
        php.index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .unwrap();
    }
    php
}

// -- PHPUnit ------------------------------------------------------------------

#[test]
fn phpunit_provider_inlay_hints_support_docblocks_and_attributes() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        dir.path(),
        &[(
            "/project/vendor/phpunit/TestCase.php",
            r"<?php namespace PHPUnit\Framework; abstract class TestCase {}",
        )],
    );
    let source = r"<?php
namespace App\Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

final class ProductTest extends TestCase
{
    /** @dataProvider productCases */
    public function testProduct(string $name, int $stock): void {}

    #[DataProvider('priceCases')]
    public function testPrice(int $gross, int $net): void {}

    public static function productCases(): iterable
    {
        yield ['Hat', 4];
        yield 'empty' => ['Shoe', 0];
    }

    public static function priceCases(): iterable
    {
        yield [119, 100];
    }
}
";
    let result = hints(
        &PhpUnitProviderProvider::new(Some(php)),
        &request(TextDocument::new(
            "file:///project/tests/ProductTest.php",
            source,
            1,
        )),
    );
    assert_eq!(result.len(), 6);
    for hint in &result {
        let parts = parts(hint);
        assert_eq!(parts.len(), 1);
        assert!(parts[0].location.is_some());
        assert_eq!(hint.kind, Some(InlayHintKind::PARAMETER));
    }
    assert_eq!(
        labels(&result),
        vec!["$name:", "$stock:", "$name:", "$stock:", "$gross:", "$net:"]
    );
}
