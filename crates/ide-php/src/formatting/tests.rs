use std::sync::Arc;

use swlsp_lsp::{
    Context, DocumentFormattingProvider, DocumentFormattingRequest,
    DocumentRangeFormattingProvider, DocumentRangeFormattingRequest, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_php_format::Options;

use super::PhpProvider;

fn format(provider: PhpProvider, uri: &str, source: &str) -> Option<String> {
    let request = DocumentFormattingRequest {
        params: protocol::DocumentFormattingParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: uri.to_owned(),
            },
            // PER Coding Style fixes the indentation: these are not used.
            options: protocol::FormattingOptions {
                tab_size: 2,
                insert_spaces: false,
                ..protocol::FormattingOptions::default()
            },
        },
        document: Arc::new(TextDocument::new(uri, source, 1)),
    };
    provider
        .format_document(&Context::background(), &request)
        .expect("format")
}

const SOURCE: &str =
    "<?php declare(strict_types=1);\n\nclass A {\n  function f() { return 1; }\n}\n";

#[test]
fn php_provider_formats_to_per_coding_style() {
    assert_eq!(
        format(
            PhpProvider::default(),
            "file:///workspace/src/A.php",
            SOURCE
        )
        .as_deref(),
        Some(
            "<?php\n\ndeclare(strict_types=1);\n\nclass A\n{\n    public function f()\n    {\n        return 1;\n    }\n}\n"
        )
    );
    // Broken documents and other languages are not its business.
    assert_eq!(
        format(
            PhpProvider::default(),
            "file:///workspace/src/A.php",
            "<?php\nclass {"
        ),
        None
    );
    assert_eq!(
        format(PhpProvider::default(), "file:///workspace/a.twig", SOURCE),
        None
    );
}

#[test]
fn php_provider_keeps_the_declare_on_the_opening_tag_when_configured() {
    let provider = PhpProvider::new(Options {
        declare_on_opening_tag: true,
        ..Options::default()
    });
    let formatted = format(provider, "file:///workspace/src/A.php", SOURCE).expect("formatted");
    assert!(
        formatted.starts_with("<?php declare(strict_types=1);\n\nclass A\n{\n"),
        "{formatted}"
    );
}

#[test]
fn php_provider_formats_the_selected_lines() {
    let uri = "file:///workspace/src/A.php";
    let source = "<?php\n\n$a=1;\n$b=2;\n$c=3;\n";
    let request = DocumentRangeFormattingRequest {
        params: protocol::DocumentRangeFormattingParams::default(),
        document: Arc::new(TextDocument::new(uri, source, 1)),
        range: 13..14,
    };
    assert_eq!(
        PhpProvider::default()
            .format_range(&Context::background(), &request)
            .expect("format")
            .as_deref(),
        Some("<?php\n\n$a=1;\n$b = 2;\n$c=3;\n")
    );
    let other = DocumentRangeFormattingRequest {
        document: Arc::new(TextDocument::new("file:///workspace/a.twig", source, 1)),
        ..request
    };
    assert_eq!(
        PhpProvider::default()
            .format_range(&Context::background(), &other)
            .expect("format"),
        None
    );
}
