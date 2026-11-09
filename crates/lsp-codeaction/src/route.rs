//! Symfony route actions: add a `#[Route]` attribute to a controller action
//! and inject framework parameters into a route action.

use crate::shared::document_snapshot;
use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_php::query as phpquery;
use swlsp_php::{NameResolver, PhpIndex};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::shared::{
    PhpSuggestedParameter, has_resolved_php_attribute, offset_range, php_class_qualifier,
    php_class_short_name, php_existing_parameter_types, php_line_indentation,
    php_required_parameter_edit, trim_backslashes, trimmed_start,
};

pub(crate) const ROUTE_ATTRIBUTE_CLASS: &str = "Symfony\\Component\\Routing\\Attribute\\Route";
const ABSTRACT_CONTROLLER_CLASS: &str =
    "Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController";
const AS_CONTROLLER_ATTRIBUTE_CLASS: &str =
    "Symfony\\Component\\HttpKernel\\Attribute\\AsController";

pub(crate) const ROUTE_ATTRIBUTE_CLASSES: &[&str] = &[
    ROUTE_ATTRIBUTE_CLASS,
    "Symfony\\Component\\Routing\\Annotation\\Route",
    "Sensio\\Bundle\\FrameworkExtraBundle\\Configuration\\Route",
];

const ROUTE_ACTION_PARAMETERS: &[PhpSuggestedParameter] = &[
    PhpSuggestedParameter {
        class_name: "Symfony\\Component\\HttpFoundation\\Request",
        variable_name: "request",
    },
    PhpSuggestedParameter {
        class_name: "Symfony\\Component\\Security\\Core\\User\\UserInterface",
        variable_name: "user",
    },
];

fn single_document_edit(
    request: &CodeActionRequest,
    edits: Vec<protocol::TextEdit>,
) -> protocol::WorkspaceEdit {
    protocol::WorkspaceEdit {
        changes: [(request.params.text_document.uri.clone(), edits)]
            .into_iter()
            .collect(),
        ..protocol::WorkspaceEdit::default()
    }
}

/// Adds a `#[Route]` attribute to a public controller action.
pub struct RouteAttributeCodeActionProvider {
    php_index: Option<PhpIndex>,
}

impl RouteAttributeCodeActionProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }

    fn is_controller_class(
        &self,
        index: &PhpIndex,
        request: &CodeActionRequest,
        root: &SyntaxNode,
        class: &SyntaxNode,
        resolver: &NameResolver,
    ) -> bool {
        let class_name = phpquery::class_name(class);
        if class_name.ends_with("Controller")
            || has_resolved_php_attribute(
                class,
                resolver,
                &[AS_CONTROLLER_ATTRIBUTE_CLASS, ROUTE_ATTRIBUTE_CLASS],
            )
        {
            return true;
        }
        if phpquery::methods(class).iter().any(|method| {
            is_public_instance_php_method(Some(method))
                && has_resolved_php_attribute(method, resolver, ROUTE_ATTRIBUTE_CLASSES)
        }) {
            return true;
        }
        if phpquery::class_extends(class).iter().any(|parent| {
            eq_ignore_case(
                trim_backslashes(&resolver.resolve(parent)),
                ABSTRACT_CONTROLLER_CLASS,
            )
        }) {
            return true;
        }
        let Some(document) = request.document.as_ref() else {
            return false;
        };
        let path = swlsp_uriutil::path(&document.uri).unwrap_or_default();
        let snapshot = document_snapshot(index, document, &path, root);
        let namespace = phpquery::namespace(root);
        let class_fqn = if namespace.is_empty() {
            class_name
        } else {
            format!("{namespace}\\{class_name}")
        };
        snapshot.is_subtype_of(&class_fqn, ABSTRACT_CONTROLLER_CLASS)
    }
}

impl ActionProvider for RouteAttributeCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let (Some(index), Some(document), Some(root), Some(node)) = (
            self.php_index.as_ref(),
            request.document.as_ref(),
            request.root.as_ref(),
            request.node.as_ref(),
        ) else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Php) {
            return Vec::new();
        }
        if index.find_class(ROUTE_ATTRIBUTE_CLASS).is_none() {
            return Vec::new();
        }
        let Some(method) = phpquery::method_at(node) else {
            return Vec::new();
        };
        let Some(class) = phpquery::class_at(&method) else {
            return Vec::new();
        };
        if !is_public_instance_php_method(Some(&method)) {
            return Vec::new();
        }
        let resolver = NameResolver::new(root);
        if has_resolved_php_attribute(&method, &resolver, ROUTE_ATTRIBUTE_CLASSES)
            || !self.is_controller_class(index, request, root, &class, &resolver)
        {
            return Vec::new();
        }
        let (qualifier, import_edit) = php_class_qualifier(request, ROUTE_ATTRIBUTE_CLASS);
        if qualifier.is_empty() {
            return Vec::new();
        }
        let class_name = phpquery::class_name(&class);
        let method_name = phpquery::method_name(&method);
        let namespace = phpquery::namespace(root);
        let class_fqn = if namespace.is_empty() {
            class_name
        } else {
            format!("{namespace}\\{class_name}")
        };
        let mut path = generated_route_path(&class_fqn, &method_name);
        if path.is_empty() {
            path = "/".to_owned();
        }
        let name = generated_route_name(&class_fqn, &method_name);
        let insert_offset = trimmed_start(&method);
        let indent = php_line_indentation(document.source(), insert_offset);
        let new_text = format!("#[{qualifier}('{path}', name: '{name}')]\n{indent}");
        let mut edits = vec![protocol::TextEdit::new(
            offset_range(request, insert_offset, insert_offset),
            new_text,
        )];
        edits.extend(import_edit);
        vec![protocol::CodeAction {
            title: "Symfony: Add Route attribute".to_owned(),
            kind: Some(CodeActionKind::REFACTOR_REWRITE),
            edit: Some(single_document_edit(request, edits)),
            ..protocol::CodeAction::default()
        }]
    }
}

fn is_public_instance_php_method(method: Option<&SyntaxNode>) -> bool {
    let Some(method) = method else {
        return false;
    };
    let visibility = phpquery::declaration_visibility(method);
    if !visibility.is_empty() && visibility != "public" {
        return false;
    }
    for token in method
        .descendants_with_tokens()
        .filter_map(|element| element.into_token())
    {
        let text = token.text();
        if text.eq_ignore_ascii_case("static") {
            return false;
        }
        if text.eq_ignore_ascii_case("function") {
            return true;
        }
    }
    true
}

pub(crate) fn generated_route_name(class_fqn: &str, method_name: &str) -> String {
    let method_name = method_name.strip_suffix("Action").unwrap_or(method_name);
    let mut result: Vec<String> = Vec::new();
    for part in trim_backslashes(class_fqn).split('\\') {
        if part.is_empty() || eq_ignore_case(part, "controller") {
            continue;
        }
        let mut part = lowercase(part);
        if part.ends_with("bundle") && part != "bundle" {
            part.truncate(part.len() - "bundle".len());
        } else if part.ends_with("controller") && part != "controller" {
            part.truncate(part.len() - "controller".len());
        }
        if !part.is_empty() {
            result.push(part);
        }
    }
    let name = lowercase(method_name);
    if result.is_empty() {
        return name;
    }
    if !name.is_empty() {
        result.push(name);
    }
    result.join("_")
}

pub(crate) fn generated_route_path(class_fqn: &str, method_name: &str) -> String {
    let method_name = method_name.strip_suffix("Action").unwrap_or(method_name);
    let mut found_controller = false;
    let mut result: Vec<String> = Vec::new();
    for part in trim_backslashes(class_fqn).split('\\') {
        if part.is_empty() {
            continue;
        }
        if eq_ignore_case(part, "controller") {
            found_controller = true;
            continue;
        }
        if !found_controller {
            continue;
        }
        let mut part = lowercase(part);
        if part.ends_with("controller") {
            part.truncate(part.len() - "controller".len());
        }
        if !part.is_empty() {
            result.push(part);
        }
    }
    if !eq_ignore_case(method_name, "index") && !method_name.is_empty() {
        result.push(lowercase(method_name));
    }
    if result.is_empty() {
        return String::new();
    }
    format!("/{}", result.join("/"))
}

/// Offers `Request`/`UserInterface` parameters on route actions.
#[derive(Default)]
pub struct RouteActionParameterCodeActionProvider;

impl RouteActionParameterCodeActionProvider {
    pub fn new() -> Self {
        Self
    }
}

impl ActionProvider for RouteActionParameterCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let (Some(document), Some(root), Some(node)) = (
            request.document.as_ref(),
            request.root.as_ref(),
            request.node.as_ref(),
        ) else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Php) {
            return Vec::new();
        }
        let Some(method) = phpquery::method_at(node) else {
            return Vec::new();
        };
        let Some(class) = phpquery::class_at(&method) else {
            return Vec::new();
        };
        let visibility = phpquery::declaration_visibility(&method);
        if !visibility.is_empty() && visibility != "public" {
            return Vec::new();
        }
        let resolver = NameResolver::new(root);
        if !is_php_route_action(&method, &class, &resolver) {
            return Vec::new();
        }
        let existing = php_existing_parameter_types(&method, &resolver);
        let mut result = Vec::new();
        for parameter in ROUTE_ACTION_PARAMETERS {
            if existing.contains(&lowercase(parameter.class_name)) {
                continue;
            }
            let (qualifier, import_edit) = php_class_qualifier(request, parameter.class_name);
            let Some(parameter_edit) = php_required_parameter_edit(
                request,
                &class,
                &method,
                &qualifier,
                parameter.variable_name,
            ) else {
                continue;
            };
            let mut edits = vec![parameter_edit];
            edits.extend(import_edit);
            result.push(protocol::CodeAction {
                title: format!(
                    "Symfony: Add {} parameter to route action",
                    php_class_short_name(parameter.class_name)
                ),
                kind: Some(CodeActionKind::REFACTOR_REWRITE),
                edit: Some(single_document_edit(request, edits)),
                ..protocol::CodeAction::default()
            });
        }
        result
    }
}

fn is_php_route_action(method: &SyntaxNode, class: &SyntaxNode, resolver: &NameResolver) -> bool {
    has_php_route_marker(Some(method), resolver)
        || eq_ignore_case(&phpquery::method_name(method), "__invoke")
            && has_php_route_marker(Some(class), resolver)
}

pub(crate) fn has_php_route_marker(node: Option<&SyntaxNode>, resolver: &NameResolver) -> bool {
    let Some(node) = node else {
        return false;
    };
    has_resolved_php_attribute(node, resolver, ROUTE_ATTRIBUTE_CLASSES)
        || php_doc_has_resolved_annotation(
            &leading_php_doc_comment(node),
            resolver,
            ROUTE_ATTRIBUTE_CLASSES,
        )
}

fn leading_php_doc_comment(node: &SyntaxNode) -> String {
    let range = node.text_range();
    let trimmed = swlsp_syntax::SyntaxNodeExt::range_trimmed_trivia(node);
    if trimmed.start() <= range.start() {
        return String::new();
    }
    let text = node.text().to_string();
    let prefix_length = (u32::from(trimmed.start() - range.start()) as usize).min(text.len());
    let prefix = &text[..prefix_length];
    let Some(start) = prefix.rfind("/**") else {
        return String::new();
    };
    let Some(end) = prefix[start..].find("*/") else {
        return String::new();
    };
    let end = end + start + 2;
    if !prefix[end..].trim().is_empty() {
        return String::new();
    }
    prefix[start..end].to_owned()
}

fn php_doc_has_resolved_annotation(
    documentation: &str,
    resolver: &NameResolver,
    targets: &[&str],
) -> bool {
    let documentation = documentation.trim();
    let documentation = documentation.strip_prefix("/**").unwrap_or(documentation);
    let documentation = documentation.strip_suffix("*/").unwrap_or(documentation);
    for line in documentation.split('\n') {
        let line = line.trim();
        let line = line.strip_prefix('*').unwrap_or(line).trim();
        if !line.starts_with('@') {
            continue;
        }
        let bytes = line.as_bytes();
        let start = 1;
        let mut end = start;
        while end < bytes.len() && is_php_annotation_name_byte(bytes[end]) {
            end += 1;
        }
        if end == start {
            continue;
        }
        let resolved = resolver.resolve(&line[start..end]);
        let name = trim_backslashes(&resolved);
        if targets
            .iter()
            .any(|target| eq_ignore_case(name, trim_backslashes(target)))
        {
            return true;
        }
    }
    false
}

fn is_php_annotation_name_byte(value: u8) -> bool {
    value == b'\\' || value == b'_' || value.is_ascii_alphanumeric()
}

#[cfg(test)]
#[path = "tests/route.rs"]
mod tests;
