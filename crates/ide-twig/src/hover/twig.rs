//! Twig variable, global, PHP member, `sw_icon`, and documentation hover.

use std::collections::HashSet;
use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_extension::ExtensionIndexer;
use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_ACCESSOR, TWIG_LITERAL_NAME, TWIG_VAR};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, SymbolKind};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_theme::IconProvider;
use swlsp_twig::{PhpAccessResolver, TwigIndexer};
use swlsp_uriutil::filepath;

use crate::hover::{
    hover_protocol_range, markdown_hover, rel_or, request_byte_range, request_uri,
    twig_string_content_range,
};

type HoverResult = ProviderResult<Option<protocol::Hover>>;

pub struct TwigHoverProvider {
    icon_provider: Option<IconProvider>,
    project_root: String,
    php: Option<PhpIndex>,
    twig: Option<Arc<TwigIndexer>>,
}

impl TwigHoverProvider {
    pub fn new(
        project_root: impl Into<String>,
        extensions: Option<Arc<ExtensionIndexer>>,
        php: Option<PhpIndex>,
        twig: Option<Arc<TwigIndexer>>,
    ) -> Self {
        let project_root = project_root.into();
        Self {
            icon_provider: extensions
                .map(|extensions| IconProvider::new(project_root.clone(), extensions)),
            project_root,
            php,
            twig,
        }
    }

    fn php_member_hover(&self, request: &HoverRequest, node: &SyntaxNode) -> HoverResult {
        let (Some(root), Some(php)) = (request.root.as_ref(), self.php.as_ref()) else {
            return Ok(None);
        };
        let Some(name_node) = twigquery::closest_node_of_kind(node, &[TWIG_LITERAL_NAME]) else {
            return Ok(None);
        };
        let template_path = swlsp_uriutil::path(request_uri(request))?;
        let resolver = PhpAccessResolver::new(Some(php), self.twig.as_deref());
        let Some(resolution) = resolver.resolve_name(&template_path, root, &name_node) else {
            return Ok(None);
        };
        let mut sections = Vec::new();
        let mut seen = HashSet::new();
        for member in &resolution.members {
            let symbol = &member.symbol;
            if !seen.insert(symbol.id.clone()) {
                continue;
            }
            let signature = match symbol.kind {
                SymbolKind::Property => format!("{}: {}", symbol.fully_qualified, member.ty),
                SymbolKind::Method => format!("{}(): {}", symbol.fully_qualified, member.ty),
                SymbolKind::ClassConstant | SymbolKind::EnumCase => {
                    php.constant_symbol_name(symbol)
                }
                _ => symbol.fully_qualified.to_string(),
            };
            let mut value = format!("```php\n{signature}\n```");
            if !symbol.doc_summary().is_empty() {
                value.push_str("\n\n");
                value.push_str(symbol.doc_summary());
            }
            if symbol.flags.has(Flags::DEPRECATED) {
                value.push_str("\n\n**Deprecated**");
            }
            sections.push(value);
        }
        if sections.is_empty() {
            return Ok(None);
        }
        let Some(line_index) = request.line_index() else {
            return Ok(None);
        };
        Ok(Some(markdown_hover(
            sections.join("\n\n---\n\n"),
            Some(hover_protocol_range(
                resolution.name_node.range_trimmed_trivia(),
                line_index,
            )),
        )))
    }

    fn variable_hover(&self, request: &HoverRequest, node: &SyntaxNode) -> HoverResult {
        let Some(name_node) = twigquery::closest_node_of_kind(node, &[TWIG_LITERAL_NAME]) else {
            return Ok(None);
        };
        if twigquery::closest_node_of_kind(&name_node, &[TWIG_VAR]).is_none() {
            return Ok(None);
        }
        if let Some(accessor) = twigquery::closest_node_of_kind(&name_node, &[TWIG_ACCESSOR]) {
            let first = twigquery::iterate_nodes(&accessor, &[TWIG_LITERAL_NAME]).next();
            if first.as_ref() != Some(&name_node) {
                return Ok(None);
            }
        }
        let Some(line_index) = request.line_index() else {
            return Ok(None);
        };
        let range = Some(hover_protocol_range(
            name_node.range_trimmed_trivia(),
            line_index,
        ));
        let name = name_node.text().to_string().trim().to_owned();
        let path = swlsp_uriutil::path(request_uri(request))?;
        if let Some(root) = request.root.as_ref()
            && let Some(declaration) = swlsp_twig::twig_type_declarations(root).get(&name)
        {
            let mut markdown = format!(
                "**Twig variable** `{name}`\n\nDeclared type: `{}`",
                declaration.ty
            );
            if declaration.from_types_tag {
                let status = if declaration.optional {
                    "Optional"
                } else {
                    "Required"
                };
                let _ = write!(markdown, "\n\n{status} in the Twig `types` tag.");
            }
            if !declaration.documentation.is_empty() {
                markdown.push_str("\n\n");
                markdown.push_str(&declaration.documentation);
            }
            return Ok(Some(markdown_hover(markdown, range)));
        }
        if let Some(php) = self.php.as_ref() {
            let variables = php.twig_template_variables(&swlsp_twig::template_names(&path))?;
            if let Some(variable) = variables.iter().find(|variable| variable.name == name) {
                let display = rel_or(&self.project_root, &variable.file);
                return Ok(Some(markdown_hover(
                    format!(
                        "**Twig variable** `{}`\n\nPHP type: `{}`\n\nProvided by `{}` for `{}`.",
                        variable.name,
                        variable.ty,
                        filepath::to_slash(&display),
                        variable.template
                    ),
                    range,
                )));
            }
        }
        let Some(twig) = self.twig.as_deref() else {
            return Ok(None);
        };
        let globals = twig.get_globals(&name)?;
        let Some(global) = globals.first() else {
            return Ok(None);
        };
        let type_name = if global.ty.is_empty() {
            "mixed"
        } else {
            global.ty.as_str()
        };
        let mut markdown = format!(
            "**Twig global** `{}`\n\nPHP type: `{type_name}`\n\nSource: {}.",
            global.name, global.source
        );
        if !global.service_id.is_empty() {
            let _ = write!(markdown, "\n\nService: `{}`", global.service_id);
        }
        if !global.file.is_empty() {
            let display = rel_or(&self.project_root, &global.file);
            let _ = write!(
                markdown,
                "\n\nDeclared in `{}`.",
                filepath::to_slash(&display)
            );
        }
        if globals.len() > 1 {
            let _ = write!(markdown, "\n\n{} indexed definitions.", globals.len());
        }
        Ok(Some(markdown_hover(markdown, range)))
    }

    fn icon_hover(&self, request: &HoverRequest, node: &SyntaxNode) -> HoverResult {
        if !twigquery::string_in_tag(node, &["sw_icon"]) {
            return Ok(None);
        }
        let Some(icon_provider) = self.icon_provider.as_ref() else {
            return Ok(None);
        };
        let literal = twigquery::literal_string_at(node);
        let icon_name = literal
            .as_ref()
            .and_then(twigquery::string_value)
            .unwrap_or_default();
        let icon_range = literal.as_ref().and_then(twig_string_content_range);
        let config = twigquery::hash_string_map(node);
        let pack = config
            .get("pack")
            .cloned()
            .unwrap_or_else(|| "default".to_owned());
        let icon_path = icon_provider.icon(&pack, &icon_name);
        if icon_path.is_empty() {
            return Ok(None);
        }
        let image_uri = if icon_path.starts_with('/') {
            swlsp_uriutil::file_uri(&icon_path)
        } else {
            let document_path = swlsp_uriutil::path(request_uri(request))?;
            match filepath::rel(&filepath::dir(&document_path), &icon_path) {
                Ok(relative) => relative,
                Err(_) => swlsp_uriutil::file_uri(&icon_path),
            }
        };
        let display = rel_or(&self.project_root, &icon_path);
        Ok(Some(markdown_hover(
            format!(
                "**Icon**: `{icon_name}`\n\n**Pack**: `{pack}`\n\n**Preview**:\n\n![{icon_name}]({image_uri})\n\n**Path**: `{display}`"
            ),
            request_byte_range(request, icon_range),
        )))
    }
}

fn append_twig_documentation(
    hover: Option<protocol::Hover>,
    documentation: &str,
) -> Option<protocol::Hover> {
    let mut hover = hover?;
    if documentation.is_empty() {
        return Some(hover);
    }
    if !hover.contents.value.is_empty() {
        hover.contents.value.push_str("\n\n");
    }
    hover.contents.kind = protocol::MarkupKind::Markdown;
    hover.contents.value.push_str(documentation);
    Some(hover)
}

impl HoverProvider for TwigHoverProvider {
    fn get_hover(&self, _ctx: &Context, request: &HoverRequest) -> HoverResult {
        if !request_uri(request).to_lowercase().ends_with(".twig") {
            return Ok(None);
        }
        let Some(node) = request.node.as_ref() else {
            return Ok(None);
        };
        let documentation = swlsp_twig::documentation_for_node(node);
        match self.php_member_hover(request, node) {
            Ok(None) => {}
            result => return result.map(|hover| append_twig_documentation(hover, &documentation)),
        }
        match self.variable_hover(request, node) {
            Ok(None) => {}
            result => return result.map(|hover| append_twig_documentation(hover, &documentation)),
        }
        match self.icon_hover(request, node) {
            Ok(None) => {}
            result => return result.map(|hover| append_twig_documentation(hover, &documentation)),
        }
        if !documentation.is_empty() {
            let Some(line_index) = request.line_index() else {
                return Ok(None);
            };
            return Ok(Some(markdown_hover(
                documentation,
                Some(hover_protocol_range(
                    node.range_trimmed_trivia(),
                    line_index,
                )),
            )));
        }
        Ok(None)
    }
}
