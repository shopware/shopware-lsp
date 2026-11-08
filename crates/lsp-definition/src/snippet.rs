use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_snippet::{Snippet, SnippetIndexer};

use crate::common::{ext_lower, request_offset, request_uri};
use crate::php_common::unclamped_line_location;

pub struct SnippetDefinitionProvider {
    snippets: Option<Arc<SnippetIndexer>>,
}

impl SnippetDefinitionProvider {
    pub fn new(snippets: Option<Arc<SnippetIndexer>>) -> Self {
        Self { snippets }
    }

    fn admin_locations(&self, key: &str) -> Vec<protocol::Location> {
        let Some(index) = &self.snippets else {
            return Vec::new();
        };
        snippet_locations(&index.admin_snippet(key).unwrap_or_default())
    }

    fn frontend_locations(&self, key: &str) -> Vec<protocol::Location> {
        let Some(index) = &self.snippets else {
            return Vec::new();
        };
        snippet_locations(&index.frontend_snippet(key).unwrap_or_default())
    }

    fn twig_definitions(&self, request: &DefinitionRequest) -> Vec<protocol::Location> {
        let node = request.node.as_ref().expect("checked");
        if let (Some(root), Some(offset)) = (request.root.as_ref(), request_offset(request))
            && let Some(reference) = swlsp_snippet::admin_twig_reference_at_offset(root, offset)
            && !reference.key.is_empty()
        {
            return self.admin_locations(&reference.key);
        }
        if twigquery::string_in_filter(node, &["trans"]) {
            return self.frontend_locations(&twig_string_value(node));
        }
        if twigquery::string_in_function(node, &["$tc", "$t"]) {
            return self.admin_locations(&twig_string_value(node));
        }
        Vec::new()
    }
}

pub(crate) fn twig_string_value(node: &swlsp_syntax::SyntaxNode) -> String {
    twigquery::literal_string_at(node)
        .and_then(|literal| twigquery::string_value(&literal))
        .unwrap_or_default()
}

fn snippet_locations(snippets: &[Snippet]) -> Vec<protocol::Location> {
    snippets
        .iter()
        .map(|snippet| unclamped_line_location(&snippet.file, snippet.line))
        .collect()
}

impl GotoDefinitionProvider for SnippetDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        match ext_lower(request_uri(request)).as_str() {
            ".twig" => self.twig_definitions(request),
            ".php" => {
                if phpquery::string_in_call(node, 0, &["trans"]) {
                    return self.frontend_locations(&phpquery::string_value(node));
                }
                Vec::new()
            }
            ".js" | ".ts" => {
                if swlsp_snippet::admin_javascript_string_reference(node) {
                    return self.admin_locations(&jsquery::string_value(node));
                }
                Vec::new()
            }
            _ => Vec::new(),
        }
    }
}
