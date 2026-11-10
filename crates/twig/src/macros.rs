//! Twig macros, imports and macro usages.

use std::collections::HashMap;

use serde::{Deserialize, Serialize};
use swlsp_parser_twig::ast::{AstNode, TwigFunctionCall};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, SyntaxToken, TextRange, TextSize};
use swlsp_textutil::lowercase;

use crate::compare_fold;
use crate::documentation::{documentation_before, documentation_comment_text};
use crate::path::template_names;

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct MacroParameter {
    pub name: String,
    pub default: String,
    pub documentation: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Macro {
    pub name: String,
    pub parameters: Vec<MacroParameter>,
    pub documentation: String,
    pub file_path: String,
    pub templates: Vec<String>,
    pub range: TextRange,
    pub name_range: TextRange,
}

impl Macro {
    pub fn signature(&self) -> String {
        let parameters: Vec<String> = self
            .parameters
            .iter()
            .map(|parameter| {
                if parameter.default.is_empty() {
                    parameter.name.clone()
                } else {
                    format!("{} = {}", parameter.name, parameter.default)
                }
            })
            .collect();
        format!("{}({})", self.name, parameters.join(", "))
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct MacroCatalog {
    pub template: String,
    pub file_path: String,
    pub macros: Vec<Macro>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct MacroUsage {
    pub template: String,
    pub name: String,
    pub file_path: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct MacroUsageRecord {
    pub template: String,
    pub name: String,
    pub file_path: String,
    pub usages: Vec<MacroUsage>,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum MacroReferenceRole {
    #[default]
    Declaration,
    Usage,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct MacroReference {
    pub name: String,
    pub templates: Vec<String>,
    pub range: TextRange,
    pub role: MacroReferenceRole,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct MacroImport {
    pub alias: String,
    pub name: String,
    pub templates: Vec<String>,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct MacroCompletionContext {
    pub templates: Vec<String>,
    pub direct: Vec<MacroImport>,
}

#[derive(Default)]
pub(crate) struct MacroImports {
    pub(crate) namespaces: HashMap<String, Vec<String>>,
    pub(crate) direct: HashMap<String, MacroImport>,
    references: Vec<MacroReference>,
}

pub fn macros_in_document(path: &str, root: &SyntaxNode) -> Vec<Macro> {
    let templates = template_names(path);
    let mut result = Vec::new();
    for node in twigquery::iterate_nodes(root, &[TWIG_MACRO]) {
        let Some(start) = direct_child(&node, TWIG_MACRO_STARTING_BLOCK) else {
            continue;
        };
        let Some(name_token) = token_after(&start, TK_MACRO, TK_WORD) else {
            continue;
        };
        if name_token.text().is_empty() {
            continue;
        }
        let mut definition = Macro {
            name: name_token.text().to_owned(),
            documentation: documentation_before(&node),
            file_path: path.to_owned(),
            templates: templates.clone(),
            range: node.range_trimmed_trivia(),
            name_range: name_token.text_range(),
            parameters: Vec::new(),
        };
        if let Some(arguments) = direct_child(&start, TWIG_ARGUMENTS) {
            let mut parameter_documentation: Vec<String> = Vec::new();
            for child in arguments.children() {
                match child.kind() {
                    TWIG_COMMENT => {
                        if let Some(documentation) =
                            documentation_comment_text(&child.text_string())
                            && !documentation.is_empty()
                        {
                            parameter_documentation.push(documentation);
                        }
                    }
                    TWIG_EXPRESSION | TWIG_NAMED_ARGUMENT => {
                        if let Some(mut parameter) = macro_parameter(&child) {
                            parameter.documentation = parameter_documentation.join("\n");
                            definition.parameters.push(parameter);
                        }
                        parameter_documentation.clear();
                    }
                    _ => {}
                }
            }
        }
        result.push(definition);
    }
    result
}

pub fn macro_references_in_document(path: &str, root: &SyntaxNode) -> Vec<MacroReference> {
    let mut result: Vec<MacroReference> = macros_in_document(path, root)
        .into_iter()
        .map(|definition| MacroReference {
            name: definition.name,
            templates: definition.templates,
            range: definition.name_range,
            role: MacroReferenceRole::Declaration,
        })
        .collect();
    let imports = collect_macro_imports(path, root);
    result.extend(imports.references.iter().cloned());
    for call in twigquery::iterate_nodes(root, &[TWIG_FUNCTION_CALL]) {
        let names = literal_names(function_name_operand(&call).as_ref());
        if names.is_empty() {
            continue;
        }
        if names.len() >= 2 {
            let namespace = &names[0].name;
            let mut templates = imports
                .namespaces
                .get(&lowercase(namespace))
                .cloned()
                .unwrap_or_default();
            if templates.is_empty() && namespace == "_self" {
                templates = template_names(path);
            }
            if templates.is_empty() {
                continue;
            }
            let name = &names[names.len() - 1];
            result.push(MacroReference {
                name: name.name.clone(),
                templates,
                range: name.range,
                role: MacroReferenceRole::Usage,
            });
            continue;
        }
        let Some(target) = imports.direct.get(&lowercase(&names[0].name)) else {
            continue;
        };
        result.push(MacroReference {
            name: target.name.clone(),
            templates: target.templates.clone(),
            range: names[0].range,
            role: MacroReferenceRole::Usage,
        });
    }
    unique_macro_references(result)
}

pub fn macro_reference_at(path: &str, root: &SyntaxNode, offset: u32) -> Option<MacroReference> {
    static MEMO: crate::DocumentMemo<Vec<MacroReference>> = crate::DocumentMemo::new(4);
    let offset = TextSize::from(offset);
    MEMO.get_or_compute(root, path, || macro_references_in_document(path, root))
        .iter()
        .find(|reference| offset >= reference.range.start() && offset <= reference.range.end())
        .cloned()
}

pub fn macro_completion_at(
    path: &str,
    root: &SyntaxNode,
    node: &SyntaxNode,
) -> Option<MacroCompletionContext> {
    let imports = collect_macro_imports(path, root);
    let namespace_templates = |namespace: &str| -> Vec<String> {
        let mut templates = imports
            .namespaces
            .get(&lowercase(namespace))
            .cloned()
            .unwrap_or_default();
        if templates.is_empty() && namespace == "_self" {
            templates = template_names(path);
        }
        templates
    };
    if let Some(call) = twigquery::function_call_at(node) {
        let names = literal_names(function_name_operand(&call).as_ref());
        if names.len() >= 2 {
            let templates = namespace_templates(&names[0].name);
            if !templates.is_empty() {
                return Some(MacroCompletionContext {
                    templates,
                    direct: Vec::new(),
                });
            }
        }
    }
    let mut accessor = twigquery::closest_node_of_kind(node, &[TWIG_ACCESSOR]);
    if accessor.is_none()
        && let Some(scope) = twigquery::closest_node_of_kind(node, &[TWIG_VAR])
    {
        for candidate in twigquery::iterate_nodes(&scope, &[TWIG_ACCESSOR]) {
            if candidate.text().contains_char('.') {
                accessor = Some(candidate);
            }
        }
    }
    if let Some(accessor) = accessor
        && accessor.text().contains_char('.')
    {
        let names = literal_names(Some(&accessor));
        if !names.is_empty() {
            let templates = namespace_templates(&names[0].name);
            if !templates.is_empty() {
                return Some(MacroCompletionContext {
                    templates,
                    direct: Vec::new(),
                });
            }
        }
    }
    if imports.direct.is_empty() {
        return None;
    }
    let mut direct: Vec<MacroImport> = imports.direct.values().cloned().collect();
    direct.sort_by(|left, right| {
        compare_fold(&left.alias, &right.alias).then_with(|| left.alias.cmp(&right.alias))
    });
    Some(MacroCompletionContext {
        templates: Vec::new(),
        direct,
    })
}

pub(crate) fn collect_macro_imports(path: &str, root: &SyntaxNode) -> MacroImports {
    let mut result = MacroImports::default();
    for node in twigquery::iterate_nodes(root, &[TWIG_IMPORT]) {
        let (templates, names) = macro_import_target(path, &node);
        if templates.is_empty() || names.is_empty() {
            continue;
        }
        let alias = &names[names.len() - 1];
        result.namespaces.insert(lowercase(&alias.name), templates);
    }
    for node in twigquery::iterate_nodes(root, &[TWIG_FROM]) {
        let (templates, _) = macro_import_target(path, &node);
        if templates.is_empty() {
            continue;
        }
        for override_node in twigquery::iterate_nodes(&node, &[TWIG_OVERRIDE]) {
            let names = literal_names(Some(&override_node));
            if names.is_empty() {
                continue;
            }
            let original = &names[0];
            let alias = &names[names.len() - 1];
            result.direct.insert(
                lowercase(&alias.name),
                MacroImport {
                    alias: alias.name.clone(),
                    name: original.name.clone(),
                    templates: templates.clone(),
                    range: alias.range,
                },
            );
            result.references.push(MacroReference {
                name: original.name.clone(),
                templates: templates.clone(),
                range: original.range,
                role: MacroReferenceRole::Usage,
            });
        }
    }
    result
}

fn macro_import_target(path: &str, node: &SyntaxNode) -> (Vec<String>, Vec<MacroName>) {
    for literal in twigquery::iterate_nodes(node, &[TWIG_LITERAL_STRING]) {
        if twigquery::string_is_static(&literal) {
            let name = twigquery::string_value(&literal).unwrap_or_default();
            if !name.is_empty() {
                return (vec![name], literal_names(Some(node)));
            }
        }
    }
    let names = literal_names(Some(node));
    if !names.is_empty() && names[0].name == "_self" {
        return (template_names(path), names);
    }
    (Vec::new(), names)
}

#[derive(Clone, Debug)]
pub(crate) struct MacroName {
    pub(crate) name: String,
    pub(crate) range: TextRange,
}

pub(crate) fn literal_names(node: Option<&SyntaxNode>) -> Vec<MacroName> {
    let Some(node) = node else {
        return Vec::new();
    };
    twigquery::iterate_nodes(node, &[TWIG_LITERAL_NAME])
        .filter_map(|literal| {
            let (name, range) = literal_name(&literal);
            (!name.is_empty()).then_some(MacroName { name, range })
        })
        .collect()
}

pub(crate) fn literal_name(node: &SyntaxNode) -> (String, TextRange) {
    if let Some(token) = node.child_token_of_kind(TK_WORD) {
        return (token.text().to_owned(), token.text_range());
    }
    (
        node.text_string().trim().to_owned(),
        node.range_trimmed_trivia(),
    )
}

fn macro_parameter(node: &SyntaxNode) -> Option<MacroParameter> {
    let (name, range) = if node.kind() == TWIG_NAMED_ARGUMENT {
        match node.child_token_of_kind(TK_WORD) {
            Some(token) => (token.text().to_owned(), token.text_range()),
            None => (String::new(), TextRange::default()),
        }
    } else {
        match literal_names(Some(node)).into_iter().next() {
            Some(first) => (first.name, first.range),
            None => (String::new(), TextRange::default()),
        }
    };
    if name.is_empty() {
        return None;
    }
    let mut parameter = MacroParameter {
        name,
        range,
        ..MacroParameter::default()
    };
    if node.kind() == TWIG_NAMED_ARGUMENT {
        let text = node.text_string();
        if let Some(equal) = text.find('=') {
            parameter.default = text[equal + 1..].trim().to_owned();
        }
    }
    Some(parameter)
}

pub(crate) fn function_name_operand(node: &SyntaxNode) -> Option<SyntaxNode> {
    Some(
        TwigFunctionCall::cast(node.clone())?
            .name_operand()?
            .syntax()
            .clone(),
    )
}

pub(crate) fn direct_child(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.children().find(|child| child.kind() == kind)
}

fn token_after(node: &SyntaxNode, after: Kind, kind: Kind) -> Option<SyntaxToken> {
    let mut found = false;
    for element in node.children_with_tokens() {
        let NodeOrToken::Token(token) = element else {
            continue;
        };
        if token.kind() == after {
            found = true;
            continue;
        }
        if found && token.kind() == kind {
            return Some(token);
        }
    }
    None
}

fn unique_macro_references(values: Vec<MacroReference>) -> Vec<MacroReference> {
    let mut seen = std::collections::HashSet::with_capacity(values.len());
    values
        .into_iter()
        .filter(|value| {
            if value.name.is_empty() || value.templates.is_empty() {
                return false;
            }
            seen.insert((
                lowercase(&value.name),
                value.templates.join("\0"),
                value.range,
                value.role,
            ))
        })
        .collect()
}

pub(crate) fn normalize_macro_template(template: &str) -> String {
    let normalized = template.trim().replace('\\', "/");
    match normalized.strip_prefix('/') {
        Some(stripped) => stripped.to_owned(),
        None => normalized,
    }
}

pub(crate) fn macro_usage_key(template: &str, name: &str) -> String {
    format!(
        "{}\0{}",
        normalize_macro_template(template),
        lowercase(name.trim())
    )
}

pub(crate) fn sort_macros(macros: &mut [Macro]) {
    macros.sort_by(|left, right| {
        if left.name != right.name {
            return compare_fold(&left.name, &right.name).then_with(|| left.name.cmp(&right.name));
        }
        left.file_path
            .cmp(&right.file_path)
            .then(left.name_range.start().cmp(&right.name_range.start()))
    });
}

pub(crate) fn sort_macro_usages(usages: &mut [MacroUsage]) {
    usages.sort_by(|left, right| {
        left.file_path
            .cmp(&right.file_path)
            .then(left.range.start().cmp(&right.range.start()))
    });
}
