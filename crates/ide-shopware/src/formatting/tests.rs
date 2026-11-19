use std::sync::Arc;

use swlsp_lsp::{Context, DocumentFormattingProvider, DocumentFormattingRequest, TextDocument};
use swlsp_lsp_protocol as protocol;

use super::BiomeProvider;

fn format(uri: &str, source: &str) -> Option<String> {
    let request = DocumentFormattingRequest {
        params: protocol::DocumentFormattingParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: uri.to_owned(),
            },
            options: protocol::FormattingOptions {
                tab_size: 2,
                insert_spaces: true,
                ..protocol::FormattingOptions::default()
            },
        },
        document: Arc::new(TextDocument::new(uri, source, 1)),
    };
    BiomeProvider
        .format_document(&Context::background(), &request)
        .expect("format")
}

#[test]
fn biome_provider_formats_by_extension_with_the_editor_indentation() {
    assert_eq!(
        format(
            "file:///workspace/composer.json",
            "{\n\"name\":\"swag/example\"}"
        )
        .as_deref(),
        Some("{\n  \"name\": \"swag/example\"\n}\n")
    );
    assert_eq!(
        format(
            "file:///workspace/src/main.ts",
            "export function run(x:number){return x}"
        )
        .as_deref(),
        Some("export function run(x: number) {\n  return x;\n}\n")
    );
    assert_eq!(
        format("file:///workspace/src/base.scss", ".card{color:red}").as_deref(),
        Some(".card {\n  color: red;\n}\n")
    );
    // Broken documents and other languages are not its business.
    assert_eq!(
        format("file:///workspace/composer.json", "{\"name\":"),
        None
    );
    assert_eq!(format("file:///workspace/page.html.twig", "{}"), None);
}
