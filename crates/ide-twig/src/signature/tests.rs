use super::*;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    Context, SignatureHelpProvider, SignatureHelpRequest, SyntaxContext, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNodeExt, TextSize};
use swlsp_twig::TwigIndexer;

fn request_at(document: TextDocument, offset: u32) -> SignatureHelpRequest {
    let document = Arc::new(document);
    let root = document.root().unwrap();
    let node = root.node_at(TextSize::from(offset));
    let (line, character) = document.line_index().position_utf16(offset);
    let mut params = protocol::SignatureHelpParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    SignatureHelpRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document),
            root: Some(root),
            token: None,
            node,
        },
    }
}

#[test]
fn twig_macro_signature_help() {
    let cache = tempfile::tempdir().unwrap();
    let index = Arc::new(TwigIndexer::new(cache.path(), None).unwrap());
    index
        .index(&ParsedFile::new(
            "/project/templates/macros/forms.html.twig",
            b"{## Renders a form input. ##}
{% macro input(
    ## HTML field name.
    name,
    ## Initial field value.
    value = '',
    required = false,
) %}{% endmacro %}"
                .to_vec(),
        ))
        .unwrap();
    let source = "{% import 'macros/forms.html.twig' as forms %}
{{ forms.input('email', 'value') }}
";
    let document = TextDocument::new("file:///project/templates/page.html.twig", source, 1);
    let offset = (source.find("'value'").unwrap() + 2) as u32;
    let result = TwigMacroSignatureProvider::new(index)
        .get_signature_help(&Context::background(), &request_at(document, offset))
        .unwrap()
        .unwrap();
    assert_eq!(result.signatures.len(), 1);
    let signature = &result.signatures[0];
    assert_eq!(signature.label, "input(name, value = '', required = false)");
    assert_eq!(result.active_parameter, 1);
    assert_eq!(signature.active_parameter, 1);
    assert_eq!(signature.parameters.len(), 3);
    assert_eq!(
        signature.documentation.as_ref().unwrap().value,
        "Renders a form input."
    );
    assert_eq!(
        signature.parameters[0]
            .documentation
            .as_ref()
            .unwrap()
            .value,
        "HTML field name."
    );
    assert_eq!(
        signature.parameters[1]
            .documentation
            .as_ref()
            .unwrap()
            .value,
        "Initial field value."
    );
}
