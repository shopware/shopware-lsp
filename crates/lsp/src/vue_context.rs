//! Maps a cursor in a mixed Vue SFC tree back to its embedded language.

use swlsp_language::LanguageId;
use swlsp_parser_vue::syntax as vue;
use swlsp_syntax::SyntaxNode;

/// The embedded language at `node` inside a Vue document; other documents
/// keep their registered language. Vue stays a first-class file type for
/// document-wide operations.
pub fn effective_syntax_language(
    document_language: Option<LanguageId>,
    node: Option<&SyntaxNode>,
) -> Option<LanguageId> {
    if document_language != Some(LanguageId::Vue) {
        return document_language;
    }
    for current in node.into_iter().flat_map(SyntaxNode::ancestors) {
        let kind = current.kind();
        if kind == vue::VUE_TEMPLATE_SECTION {
            return Some(LanguageId::Twig);
        }
        if kind == vue::VUE_SCRIPT_SECTION {
            return Some(LanguageId::JavaScript);
        }
        if kind == vue::VUE_STYLE_SECTION {
            return Some(LanguageId::Scss);
        }
    }
    Some(LanguageId::Vue)
}
