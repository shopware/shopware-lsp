//! Administration symbol identities and persisted usage sets (port of Go
//! `usage_types.go`).

use crate::*;

string_enum! {
    pub enum AdminSymbolKind {
        Component => "component",
        Service => "service",
        Store => "store",
        StoreMember => "store_member",
        Privilege => "privilege",
        Mixin => "mixin",
        Directive => "directive",
        Filter => "filter",
        CmsElement => "cms_element",
        CmsBlock => "cms_block",
        Module => "module",
        ModuleRoute => "module_route",
        ComponentProp => "component_prop",
        ComponentEvent => "component_event",
        ComponentModel => "component_model",
        ComponentSlot => "component_slot",
        ComponentMember => "component_member",
        EventBusEvent => "event_bus_event",
    }
}

/// Reserved owner for template usages whose concrete component is inferred
/// from a dynamic selector at query time. NUL is not a valid tag character.
pub(crate) const ADMIN_DYNAMIC_COMPONENT_USAGE_OWNER: &str = "\x00dynamic-component";

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminSymbolTarget {
    pub kind: AdminSymbolKind,
    pub owner: String,
    pub name: String,
}

/// A static registry lookup such as
/// `Component.getComponentRegistry().get('sw-card')` (Go embeds
/// `AdminSymbolTarget`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct JavaScriptRegistryReference {
    pub target: AdminSymbolTarget,
    pub operation: String,
}

/// A persisted source range in UTF-16 line/character coordinates plus
/// occurrence metadata.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminSourceRange {
    pub start_line: u32,
    pub start_character: u32,
    pub end_line: u32,
    pub end_character: u32,
    pub declaration: bool,
    pub identifier: bool,
    pub name_style: AdminNameStyle,
    pub dynamic_component_selector: String,
    pub dynamic_router_view: bool,
}

/// Persisted as Go's `uint8` value.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum AdminNameStyle {
    #[default]
    Exact,
    Camel,
    Shorthand,
}

impl Serialize for AdminNameStyle {
    fn serialize<S: serde::Serializer>(
        &self,
        serializer: S,
    ) -> std::result::Result<S::Ok, S::Error> {
        serializer.serialize_u8(*self as u8)
    }
}

impl<'de> Deserialize<'de> for AdminNameStyle {
    fn deserialize<D: serde::Deserializer<'de>>(
        deserializer: D,
    ) -> std::result::Result<Self, D::Error> {
        Ok(match u8::deserialize(deserializer)? {
            1 => Self::Camel,
            2 => Self::Shorthand,
            _ => Self::Exact,
        })
    }
}

/// All occurrences of one symbol in one source file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminUsageSet {
    pub kind: AdminSymbolKind,
    pub owner: String,
    pub name: String,
    pub file_path: String,
    pub occurrences: Vec<AdminSourceRange>,
}

pub fn admin_usage_key(kind: AdminSymbolKind, owner: &str, name: &str) -> String {
    format!("{}\x00{}\x00{}", kind.as_str(), owner, name)
}

/// Resolves the filter behind a callable expression: an immediate
/// `getByName` lookup or a lexically visible `const` bound to one.
pub fn javascript_filter_name_for_callee(callee: &SyntaxNode) -> Option<String> {
    if let Some(name) = javascript_filter_lookup_name(callee) {
        return Some(name);
    }
    let identifier = jsquery::identifier_text(callee).unwrap_or_default();
    if identifier.is_empty() || callee.kind() != jssyntax::JS_IDENTIFIER {
        return None;
    }
    let root = callee.ancestors().last().unwrap_or_else(|| callee.clone());
    let expression = visible_javascript_const_initializer(callee, &identifier, &root)?;
    let parsed = swlsp_parser_js::parse(&expression).root();
    javascript_filter_lookup_name(&parsed)
}

pub(crate) fn visible_javascript_const_initializer(
    use_node: &SyntaxNode,
    identifier: &str,
    root: &SyntaxNode,
) -> Option<String> {
    visible_javascript_const_initializer_indexed(use_node, identifier, root, None)
}

pub(crate) fn visible_javascript_const_initializer_indexed(
    use_node: &SyntaxNode,
    identifier: &str,
    root: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> Option<String> {
    visible_javascript_const_declaration_indexed(use_node, identifier, root, analysis)
        .map(|(_, expression)| expression)
}

/// The closest visible earlier `const` declaration of `identifier` in the
/// same function scope, with its initializer expression.
pub(crate) fn visible_javascript_const_declaration(
    use_node: &SyntaxNode,
    identifier: &str,
    root: &SyntaxNode,
) -> Option<(SyntaxNode, String)> {
    visible_javascript_const_declaration_indexed(use_node, identifier, root, None)
}

pub(crate) fn visible_javascript_const_declaration_indexed(
    use_node: &SyntaxNode,
    identifier: &str,
    root: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> Option<(SyntaxNode, String)> {
    if !is_static_vue_identifier(identifier) {
        return None;
    }
    let use_function = closest_javascript_function_scope(use_node);
    let use_blocks = visible_javascript_block_scopes(use_node, use_function.as_ref());
    let use_start = use_node.range_trimmed_trivia().start();
    let mut best_depth = use_blocks.len() + 1;
    let mut best_start = TextSize::from(0);
    let mut best: Option<(SyntaxNode, String)> = None;
    let declarations = match analysis {
        Some(analysis) => analysis.nodes(&[jssyntax::JS_VARIABLE_DECLARATION]),
        None => jsquery::nodes(root, &[jssyntax::JS_VARIABLE_DECLARATION]),
    };
    for declaration in declarations {
        let initializer = match analysis {
            Some(analysis) => analysis.const_initializer(&declaration),
            None => direct_component_const_initializer(&declaration.text().to_string()),
        };
        let Some((name, expression)) = initializer else {
            continue;
        };
        if name != identifier || closest_javascript_function_scope(&declaration) != use_function {
            continue;
        }
        let start = declaration.range_trimmed_trivia().start();
        if start >= use_start {
            continue;
        }
        let block = closest_javascript_block_scope(&declaration, use_function.as_ref());
        let depth = match block {
            Some(block) => match use_blocks.get(&block) {
                Some(&depth) => depth,
                None => continue,
            },
            None => use_blocks.len(),
        };
        if best.is_none() || depth < best_depth || (depth == best_depth && start > best_start) {
            best = Some((declaration.clone(), expression));
            best_depth = depth;
            best_start = start;
        }
    }
    best
}

fn javascript_filter_lookup_name(root: &SyntaxNode) -> Option<String> {
    jsquery::nodes(root, &[jssyntax::JS_STRING])
        .iter()
        .find_map(|literal| {
            let reference = javascript_registry_reference_at(literal)?;
            (reference.target.kind == AdminSymbolKind::Filter && reference.operation == "getByName")
                .then_some(reference.target.name)
        })
}
