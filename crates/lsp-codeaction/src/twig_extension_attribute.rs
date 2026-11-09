//! Migrates `getFilters`,
//! `getFunctions` and `getTests` registries to `#[AsTwig*]` attributes.

use crate::shared::document_snapshot;
use std::collections::HashMap;

use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARRAY, PHP_BLOCK, PHP_EXTENDS_CLAUSE, PHP_MEMBER_CALL, PHP_OBJECT_CREATION,
    PHP_RETURN_STATEMENT, PHP_STRING, TK_COMMA,
};
use swlsp_php::{NameResolver, PhpIndex};
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_syntax::{SyntaxNode, SyntaxToken};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::shared::{
    PhpSourceReplacement, apply_php_source_replacements, child_tokens, end_of,
    escape_php_single_quoted, offset_range, php_class_name_used_outside, php_class_qualifier,
    php_import_block, php_import_insertion_offset, php_line_indentation, php_methods_by_name,
    start_of, token_end, token_start, trim_backslashes, trimmed_end, trimmed_start,
};

const TWIG_ABSTRACT_EXTENSION_CLASS: &str = "Twig\\Extension\\AbstractExtension";

struct TwigExtensionRegistrySpec {
    method_name: &'static str,
    registration: &'static str,
    attribute_class: &'static str,
}

const TWIG_EXTENSION_REGISTRY_SPECS: [TwigExtensionRegistrySpec; 3] = [
    TwigExtensionRegistrySpec {
        method_name: "getFilters",
        registration: "Twig\\TwigFilter",
        attribute_class: "Twig\\Attribute\\AsTwigFilter",
    },
    TwigExtensionRegistrySpec {
        method_name: "getFunctions",
        registration: "Twig\\TwigFunction",
        attribute_class: "Twig\\Attribute\\AsTwigFunction",
    },
    TwigExtensionRegistrySpec {
        method_name: "getTests",
        registration: "Twig\\TwigTest",
        attribute_class: "Twig\\Attribute\\AsTwigTest",
    },
];

#[derive(Clone)]
struct TwigExtensionTransformation {
    name: String,
    options: String,
    attribute_class: &'static str,
    target_method: SyntaxNode,
    array_item: SyntaxNode,
}

struct TwigExtensionRegistryPlan {
    method: SyntaxNode,
    array: SyntaxNode,
    transformations: Vec<TwigExtensionTransformation>,
    remove_method: bool,
}

pub struct TwigExtensionAttributeCodeActionProvider {
    php_index: Option<PhpIndex>,
}

impl TwigExtensionAttributeCodeActionProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ActionProvider for TwigExtensionAttributeCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(php_index) = self.php_index.as_ref() else {
            return Vec::new();
        };
        let (Some(document), Some(root), Some(node)) = (
            request.document.as_ref(),
            request.root.as_ref(),
            request.node.as_ref(),
        ) else {
            return Vec::new();
        };
        if ctx.is_cancelled() || document.language != Some(LanguageId::Php) {
            return Vec::new();
        }
        let Some(class) = phpquery::class_at(node) else {
            return Vec::new();
        };
        if !is_twig_extension(php_index, request, root, &class) {
            return Vec::new();
        }
        let resolver = NameResolver::new(root);
        let methods = php_methods_by_name(&class);
        let mut plans = Vec::new();
        let mut transformations = Vec::new();
        for spec in &TWIG_EXTENSION_REGISTRY_SPECS {
            let Some(method) = methods.get(&lowercase(spec.method_name)) else {
                continue;
            };
            let Some(array) = direct_php_return_array(method) else {
                continue;
            };
            let items = phpquery::array_items(&array);
            let current: Vec<TwigExtensionTransformation> = items
                .iter()
                .filter_map(|item| {
                    twig_extension_array_transformation(item, method, &methods, &resolver, spec)
                })
                .collect();
            if current.is_empty() {
                continue;
            }
            transformations.extend(current.iter().cloned());
            plans.push(TwigExtensionRegistryPlan {
                method: method.clone(),
                array,
                remove_method: current.len() == items.len(),
                transformations: current,
            });
        }
        if transformations.is_empty() {
            return Vec::new();
        }
        let Some(rewritten) = build_twig_extension_attribute_rewrite(
            request,
            root,
            &class,
            &resolver,
            &methods,
            &plans,
            &transformations,
        ) else {
            return Vec::new();
        };
        let source = document.source();
        if rewritten == source {
            return Vec::new();
        }
        let mut edit = protocol::WorkspaceEdit::default();
        edit.changes.insert(
            request.params.text_document.uri.clone(),
            vec![protocol::TextEdit::new(
                offset_range(request, 0, source.len() as u32),
                rewritten,
            )],
        );
        vec![protocol::CodeAction {
            title: "Migrate to TwigExtension attributes".to_owned(),
            kind: Some(CodeActionKind::REFACTOR_REWRITE),
            edit: Some(edit),
            ..protocol::CodeAction::default()
        }]
    }
}

fn is_twig_extension(
    php_index: &PhpIndex,
    request: &CodeActionRequest,
    root: &SyntaxNode,
    class: &SyntaxNode,
) -> bool {
    let resolver = NameResolver::new(root);
    if phpquery::class_extends(class).iter().any(|parent| {
        eq_ignore_case(
            trim_backslashes(&resolver.resolve(parent)),
            TWIG_ABSTRACT_EXTENSION_CLASS,
        )
    }) {
        return true;
    }
    let Some(document) = request.document.as_ref() else {
        return false;
    };
    let Ok(path) = swlsp_uriutil::path(&document.uri) else {
        return false;
    };
    let snapshot = document_snapshot(php_index, document, &path, root);
    let mut class_name = phpquery::class_name(class);
    let namespace = phpquery::namespace(root);
    if !namespace.is_empty() {
        class_name = format!("{namespace}\\{class_name}");
    }
    snapshot.is_subtype_of(&class_name, TWIG_ABSTRACT_EXTENSION_CLASS)
}

fn direct_php_return_array(method: &SyntaxNode) -> Option<SyntaxNode> {
    let body = phpquery::direct_child(method, PHP_BLOCK)?;
    let mut result = None;
    for php_return in phpquery::nodes(method, &[PHP_RETURN_STATEMENT]) {
        if phpquery::function_like_at(&php_return).as_ref() != Some(method)
            || php_return.parent().as_ref() != Some(&body)
        {
            continue;
        }
        let Some(expression) = php_return.children().next() else {
            continue;
        };
        if expression.kind() != PHP_ARRAY {
            continue;
        }
        if result.is_some() {
            return None;
        }
        result = Some(expression);
    }
    result
}

fn twig_extension_array_transformation(
    item: &SyntaxNode,
    registry_method: &SyntaxNode,
    methods: &HashMap<String, SyntaxNode>,
    resolver: &NameResolver,
    spec: &TwigExtensionRegistrySpec,
) -> Option<TwigExtensionTransformation> {
    let value = phpquery::array_item_value(item)?;
    if value.kind() != PHP_OBJECT_CREATION
        || !eq_ignore_case(
            trim_backslashes(&resolver.resolve(&phpquery::object_class_name(&value))),
            spec.registration,
        )
    {
        return None;
    }
    let name_expression = phpquery::argument_expression(&value, 0)?;
    if name_expression.kind() != PHP_STRING {
        return None;
    }
    let name = phpquery::string_value(&name_expression);
    if name.is_empty() {
        return None;
    }
    let method_name = twig_this_callable_method(phpquery::argument_expression(&value, 1).as_ref());
    if method_name.is_empty() {
        return None;
    }
    let target_method = methods.get(&lowercase(&method_name))?;
    if target_method == registry_method {
        return None;
    }
    let options = twig_attribute_options(phpquery::argument_expression(&value, 2).as_ref())?;
    Some(TwigExtensionTransformation {
        name,
        options,
        attribute_class: spec.attribute_class,
        target_method: target_method.clone(),
        array_item: item.clone(),
    })
}

fn twig_this_callable_method(expression: Option<&SyntaxNode>) -> String {
    let Some(expression) = expression else {
        return String::new();
    };
    if expression.kind() == PHP_ARRAY {
        let items = phpquery::array_items(expression);
        if items.len() < 2 {
            return String::new();
        }
        let (Some(receiver), Some(method)) = (
            phpquery::array_item_value(&items[0]),
            phpquery::array_item_value(&items[1]),
        ) else {
            return String::new();
        };
        if receiver.text().to_string().trim() != "$this" || method.kind() != PHP_STRING {
            return String::new();
        }
        return phpquery::string_value(&method);
    }
    if expression.kind() != PHP_MEMBER_CALL || !expression.text().to_string().contains("...") {
        return String::new();
    }
    match phpquery::call_receiver(expression) {
        Some(receiver) if receiver.text().to_string().trim() == "$this" => {
            phpquery::call_method_name(expression)
        }
        _ => String::new(),
    }
}

/// `None` when the options argument is not a literal array.
fn twig_attribute_options(expression: Option<&SyntaxNode>) -> Option<String> {
    let Some(expression) = expression else {
        return Some(String::new());
    };
    if expression.kind() != PHP_ARRAY {
        return None;
    }
    let mut options = Vec::new();
    for item in phpquery::array_items(expression) {
        let (Some(key), Some(value)) = (
            phpquery::array_item_key(&item),
            phpquery::array_item_value(&item),
        ) else {
            continue;
        };
        if key.kind() != PHP_STRING {
            continue;
        }
        let name = lower_camel_case(&phpquery::string_value(&key));
        if name.is_empty() {
            continue;
        }
        options.push(format!("{name}: {}", value.text().to_string().trim()));
    }
    Some(options.join(", "))
}

/// `value` in lowerCamelCase: parts split at `_`, `-` and spaces; only the
/// first character of each part changes case (the first part is lowercased
/// entirely when there are several).
fn lower_camel_case(value: &str) -> String {
    let parts: Vec<&str> = value
        .split(['_', '-', ' '])
        .filter(|part| !part.is_empty())
        .collect();
    let Some(first) = parts.first() else {
        return String::new();
    };
    if parts.len() == 1 {
        return format!(
            "{}{}",
            lowercase(first_char(first)),
            &first[first_char(first).len()..]
        );
    }
    let mut result = lowercase(first);
    for part in &parts[1..] {
        let head = first_char(part);
        result.push_str(&head.to_uppercase());
        result.push_str(&part[head.len()..]);
    }
    result
}

fn first_char(value: &str) -> &str {
    let end = value.chars().next().map_or(0, char::len_utf8);
    &value[..end]
}

fn build_twig_extension_attribute_rewrite(
    request: &CodeActionRequest,
    root: &SyntaxNode,
    class: &SyntaxNode,
    resolver: &NameResolver,
    methods: &HashMap<String, SyntaxNode>,
    plans: &[TwigExtensionRegistryPlan],
    transformations: &[TwigExtensionTransformation],
) -> Option<String> {
    let source = request.source();
    let mut replacements = Vec::new();
    let mut removed_methods: Vec<&SyntaxNode> = Vec::new();
    for plan in plans {
        if plan.remove_method {
            replacements.push(PhpSourceReplacement {
                start: start_of(&plan.method),
                end: end_of(&plan.method),
                text: String::new(),
            });
            removed_methods.push(&plan.method);
            continue;
        }
        for transformation in &plan.transformations {
            let start = trimmed_start(&transformation.array_item);
            let mut end = trimmed_end(&transformation.array_item);
            if let Some(comma) = php_array_item_trailing_comma(&plan.array, end) {
                end = token_end(&comma);
            }
            replacements.push(PhpSourceReplacement {
                start,
                end,
                text: String::new(),
            });
        }
    }

    let mut qualifiers: HashMap<&str, String> = HashMap::new();
    let mut imports = Vec::new();
    for transformation in transformations {
        if qualifiers.contains_key(transformation.attribute_class) {
            continue;
        }
        let (qualifier, import_edit) = php_class_qualifier(request, transformation.attribute_class);
        if qualifier.is_empty() {
            return None;
        }
        qualifiers.insert(transformation.attribute_class, qualifier);
        if import_edit.is_some() {
            imports.push(transformation.attribute_class.to_owned());
        }
    }
    if !imports.is_empty() {
        let offset = php_import_insertion_offset(root);
        replacements.push(PhpSourceReplacement {
            start: offset,
            end: offset,
            text: php_import_block(root, &imports),
        });
    }

    let mut attributes_by_method: Vec<(&SyntaxNode, Vec<String>)> = Vec::new();
    for transformation in transformations {
        let mut attribute = format!(
            "#[{}('{}'",
            qualifiers[transformation.attribute_class],
            escape_php_single_quoted(&transformation.name)
        );
        if !transformation.options.is_empty() {
            attribute.push_str(", ");
            attribute.push_str(&transformation.options);
        }
        attribute.push_str(")]");
        match attributes_by_method
            .iter_mut()
            .find(|(method, _)| *method == &transformation.target_method)
        {
            Some((_, attributes)) => attributes.push(attribute),
            None => attributes_by_method.push((&transformation.target_method, vec![attribute])),
        }
    }
    for (method, attributes) in &attributes_by_method {
        if removed_methods.contains(method) {
            return None;
        }
        let offset = trimmed_start(method);
        let indent = php_line_indentation(source, offset);
        replacements.push(PhpSourceReplacement {
            start: offset,
            end: offset,
            text: format!("{}\n{indent}", attributes.join(&format!("\n{indent}"))),
        });
    }

    if twig_registry_methods_all_removed(methods, &removed_methods)
        && let Some(extends) = direct_twig_abstract_extension_clause(class, resolver)
    {
        let (start, end) =
            horizontal_whitespace_range(source, trimmed_start(&extends), trimmed_end(&extends));
        replacements.push(PhpSourceReplacement {
            start,
            end,
            text: String::new(),
        });
        if let Some(declaration) = standalone_php_import(root, TWIG_ABSTRACT_EXTENSION_CLASS)
            && !php_class_name_used_outside(
                root,
                resolver,
                TWIG_ABSTRACT_EXTENSION_CLASS,
                &[Some(&declaration), Some(&extends)],
            )
        {
            replacements.push(PhpSourceReplacement {
                start: start_of(&declaration),
                end: end_of(&declaration),
                text: String::new(),
            });
        }
    }
    apply_php_source_replacements(source, &mut replacements)
}

fn php_array_item_trailing_comma(array: &SyntaxNode, item_end: u32) -> Option<SyntaxToken> {
    child_tokens(array).find(|token| token.kind() == TK_COMMA && token_start(token) >= item_end)
}

fn twig_registry_methods_all_removed(
    methods: &HashMap<String, SyntaxNode>,
    removed: &[&SyntaxNode],
) -> bool {
    TWIG_EXTENSION_REGISTRY_SPECS.iter().all(|spec| {
        methods
            .get(&lowercase(spec.method_name))
            .is_none_or(|method| removed.contains(&method))
    })
}

fn direct_twig_abstract_extension_clause(
    class: &SyntaxNode,
    resolver: &NameResolver,
) -> Option<SyntaxNode> {
    let clause = phpquery::direct_child(class, PHP_EXTENDS_CLAUSE)?;
    phpquery::class_extends(class)
        .iter()
        .any(|parent| {
            eq_ignore_case(
                trim_backslashes(&resolver.resolve(parent)),
                TWIG_ABSTRACT_EXTENSION_CLASS,
            )
        })
        .then_some(clause)
}

fn standalone_php_import(root: &SyntaxNode, class_name: &str) -> Option<SyntaxNode> {
    phpquery::use_declarations(root)
        .into_iter()
        .find(|declaration| {
            let imports = parse_use_declaration(&declaration.text().to_string());
            imports.len() == 1
                && imports[0].kind == ImportKind::Class
                && eq_ignore_case(
                    trim_backslashes(&imports[0].target),
                    trim_backslashes(class_name),
                )
        })
}

fn horizontal_whitespace_range(source: &str, mut start: u32, mut end: u32) -> (u32, u32) {
    let bytes = source.as_bytes();
    while start > 0 && matches!(bytes[start as usize - 1], b' ' | b'\t') {
        start -= 1;
    }
    while (end as usize) < bytes.len() && matches!(bytes[end as usize], b' ' | b'\t') {
        end += 1;
    }
    (start, end)
}

#[cfg(test)]
#[path = "tests/twig_extension_attribute.rs"]
mod tests;
