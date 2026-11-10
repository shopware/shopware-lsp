use super::*;
use serde_json::Value;
use std::path::Path;
use std::sync::Arc;
use swlsp_ide_php::inlay::PhpUnitProviderProvider;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, TextDocument};
use swlsp_lsp_protocol::{self as protocol};
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

// -- twig variables -----------------------------------------------------------

#[test]
fn twig_variable_inlay_hint_summarizes_controller_context() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        dir.path(),
        &[
            (
                "/project/src/Product.php",
                "<?php\nnamespace App;\nclass Product {}\n",
            ),
            (
                "/project/src/ProductController.php",
                r"<?php
namespace App;
class ProductController
{
    public function show(Product $product): mixed
    {
        return $this->render('product/show.html.twig', [
            'product' => $product,
            'count' => 1,
        ]);
    }
}
",
            ),
        ],
    );
    let result = hints(
        &TwigVariableProvider::new(Some(php)),
        &request(TextDocument::new(
            "file:///project/templates/product/show.html.twig",
            "{{ product }}",
            1,
        )),
    );
    assert_eq!(result.len(), 1);
    assert_eq!(labels(&result), vec!["Variables (2)"]);
    assert!(result[0].tooltip.contains("count: 1"));
    assert!(result[0].tooltip.contains("product: App\\Product"));
    assert!(result[0].padding_right);
    let parts = parts(&result[0]);
    assert_eq!(parts.len(), 1);
    assert_eq!(
        parts[0].tooltip,
        "Browse typed Twig variables and insert expressions"
    );
    let command = parts[0].command.as_ref().unwrap();
    assert_eq!(command.command, BROWSE_TWIG_VARIABLES_COMMAND);
    assert_eq!(command.title, "Browse Twig variables");
    assert_eq!(
        command.arguments,
        vec![
            Value::from("file:///project/templates/product/show.html.twig"),
            serde_json::json!(["count", "product"]),
        ]
    );
}

#[test]
fn twig_variable_inlay_hint_only_appears_at_document_top() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        dir.path(),
        &[(
            "/project/src/Controller.php",
            "<?php\nfunction page() {\n    return render('page.html.twig', ['value' => 1]);\n}\n",
        )],
    );
    let mut request = request(TextDocument::new(
        "file:///project/templates/page.html.twig",
        "\n{{ value }}",
        1,
    ));
    request.params.range.start.line = 1;
    assert!(hints(&TwigVariableProvider::new(Some(php)), &request).is_empty());
}

#[test]
fn php_index_backed_hints_are_empty_without_php_index() {
    let php_request = request(TextDocument::new(
        "file:///project/tests/ProductTest.php",
        "<?php\nfinal class ProductTest extends \\PHPUnit\\Framework\\TestCase {}\n",
        1,
    ));
    assert!(hints(&PhpUnitProviderProvider::new(None), &php_request).is_empty());
    let twig_request = request(TextDocument::new(
        "file:///project/templates/product.html.twig",
        "{{ product }}",
        1,
    ));
    assert!(hints(&TwigVariableProvider::new(None), &twig_request).is_empty());
}
