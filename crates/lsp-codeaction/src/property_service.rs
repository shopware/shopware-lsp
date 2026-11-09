//! Ports the Symfony plugin's "Add
//! Property Service" intention. It infers an injection type from an
//! undefined `$this->property` reference and updates the constructor using
//! the class's existing promoted or traditional injection style.

use crate::shared::document_snapshot;
use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_BLOCK, PHP_CLASS_DECLARATION, PHP_MEMBER_ACCESS, PHP_MEMBER_CALL, PHP_NAME, PHP_VARIABLE,
};
use swlsp_php::PhpIndex;
use swlsp_php_languagelevel::{Feature, supports};
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Flags, Symbol, SymbolKind};
use swlsp_php_types::Type;
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::shared::{
    child_tokens, php_block_delimiters, php_class_constructor, php_class_fully_qualified_name,
    php_class_is_readonly, php_class_qualifier, php_class_short_name, php_line_indentation,
    php_member_indent_unit, php_required_parameter_edit, request_document_path, request_source,
    text_edit, token_end, token_start, trimmed_start,
};

const MAXIMUM_PROPERTY_SERVICE_CANDIDATES: usize = 8;

pub struct PropertyServiceCodeActionProvider {
    php_index: Option<PhpIndex>,
    service_index: Option<Arc<ServiceIndex>>,
}

impl PropertyServiceCodeActionProvider {
    pub fn new(php_index: Option<PhpIndex>, service_index: Option<Arc<ServiceIndex>>) -> Self {
        Self {
            php_index,
            service_index,
        }
    }

    fn is_service_class(&self, services: &ServiceIndex, class_name: &str) -> bool {
        services
            .get_services_usage_by_class_name(class_name.strip_prefix('\\').unwrap_or(class_name))
            .is_ok_and(|locations| !locations.is_empty())
    }

    fn supports(&self, feature: Feature) -> bool {
        self.php_index
            .as_ref()
            .and_then(PhpIndex::project)
            .is_none_or(|model| supports(model.php_version, feature))
    }

    fn supports_property_promotion(&self) -> bool {
        self.supports(Feature::PropertyPromotion)
    }

    fn supports_readonly_properties(&self) -> bool {
        self.supports(Feature::ReadonlyProperties)
    }

    fn promoted_modifiers(&self, class: &SyntaxNode) -> String {
        let mut modifiers = "private ".to_owned();
        if self.supports_readonly_properties() && !php_class_is_readonly(Some(class)) {
            modifiers.push_str("readonly ");
        }
        modifiers
    }

    fn property_injection_edits(
        &self,
        request: &CodeActionRequest,
        class: &SyntaxNode,
        property_name: &str,
        qualifier: &str,
    ) -> Option<Vec<protocol::TextEdit>> {
        let Some(constructor) = php_class_constructor(class) else {
            return self
                .new_property_constructor_edit(request, class, property_name, qualifier)
                .map(|edit| vec![edit]);
        };
        let promotion = self.supports_property_promotion()
            && (phpquery::parameters(&constructor).next().is_none()
                || php_constructor_uses_promotion(&constructor));
        if promotion {
            let modifiers = self.promoted_modifiers(class);
            let parameter_edit = php_required_parameter_edit(
                request,
                class,
                &constructor,
                &format!("{modifiers}{qualifier}"),
                property_name,
            )?;
            return Some(vec![parameter_edit]);
        }
        let parameter_edit =
            php_required_parameter_edit(request, class, &constructor, qualifier, property_name)?;
        let property_edit =
            php_traditional_property_edit(request, &constructor, qualifier, property_name)?;
        let assignment_edit =
            php_constructor_assignment_edit(request, class, &constructor, property_name)?;
        Some(vec![parameter_edit, property_edit, assignment_edit])
    }

    fn new_property_constructor_edit(
        &self,
        request: &CodeActionRequest,
        class: &SyntaxNode,
        property_name: &str,
        qualifier: &str,
    ) -> Option<protocol::TextEdit> {
        let methods = phpquery::methods(class);
        let first_method = methods.first()?;
        let source = request_source(request);
        let offset = trimmed_start(first_method);
        let method_indent = php_line_indentation(source, offset);
        let indent_unit = php_member_indent_unit(source, Some(class), method_indent);
        let parameter_indent = format!("{method_indent}{indent_unit}");

        if self.supports_property_promotion() {
            let modifiers = self.promoted_modifiers(class);
            let new_text = format!(
                "public function __construct(\n{parameter_indent}{modifiers}{qualifier} ${property_name},\n{method_indent}) {{\n{method_indent}}}\n\n{method_indent}"
            );
            return Some(text_edit(request, offset, offset, new_text));
        }
        let new_text = format!(
            "private {qualifier} ${property_name};\n\n{method_indent}public function __construct({qualifier} ${property_name})\n{method_indent}{{\n{parameter_indent}$this->{property_name} = ${property_name};\n{method_indent}}}\n\n{method_indent}"
        );
        Some(text_edit(request, offset, offset, new_text))
    }
}

impl ActionProvider for PropertyServiceCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let (Some(php_index), Some(services)) =
            (self.php_index.as_ref(), self.service_index.as_deref())
        else {
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
        let Some((access, property_name, called_method)) = php_this_property_access(node) else {
            return Vec::new();
        };
        let Some(class) = phpquery::class_at(&access) else {
            return Vec::new();
        };
        if property_name.len() <= 2 || class.kind() != PHP_CLASS_DECLARATION {
            return Vec::new();
        }
        let class_name = php_class_fully_qualified_name(root, &class);
        if class_name.is_empty() || !self.is_service_class(services, &class_name) {
            return Vec::new();
        }
        let snapshot =
            document_snapshot(php_index, document, &request_document_path(request), root);
        if !MemberResolver::new(&snapshot)
            .properties(&Type::named(&class_name, Vec::new()), &property_name)
            .is_empty()
        {
            return Vec::new();
        }

        let candidates =
            property_service_candidates(Some(php_index), &property_name, &called_method);
        let mut result = Vec::with_capacity(candidates.len());
        for candidate in candidates {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            let (qualifier, import_edit) = php_class_qualifier(request, &candidate.class_name);
            if qualifier.is_empty() {
                continue;
            }
            let Some(mut edits) =
                self.property_injection_edits(request, &class, &property_name, &qualifier)
            else {
                continue;
            };
            edits.extend(import_edit);
            result.push(protocol::CodeAction {
                title: format!(
                    "Symfony: Inject {} as ${property_name}",
                    php_class_short_name(&candidate.class_name)
                ),
                kind: Some(CodeActionKind::REFACTOR_REWRITE),
                edit: Some(protocol::WorkspaceEdit {
                    changes: BTreeMap::from([(request.params.text_document.uri.clone(), edits)]),
                    ..protocol::WorkspaceEdit::default()
                }),
                ..protocol::CodeAction::default()
            });
        }
        result
    }
}

/// The nearest `$this->name` access around
/// `node`, its property name and the method called on it.
fn php_this_property_access(node: &SyntaxNode) -> Option<(SyntaxNode, String, String)> {
    for current in node.ancestors() {
        if current.kind() != PHP_MEMBER_ACCESS {
            continue;
        }
        let receiver = phpquery::direct_child(&current, PHP_VARIABLE);
        if !eq_ignore_case(
            &receiver
                .as_ref()
                .map(phpquery::variable_name)
                .unwrap_or_default(),
            "this",
        ) {
            continue;
        }
        let property_name = phpquery::direct_child(&current, PHP_NAME)
            .map(|name| phpquery::name_value(&name))
            .unwrap_or_default();
        if property_name.is_empty() {
            return None;
        }
        let called_method = current
            .parent()
            .filter(|parent| parent.kind() == PHP_MEMBER_CALL)
            .and_then(|parent| phpquery::direct_child(&parent, PHP_NAME))
            .map(|name| phpquery::name_value(&name))
            .unwrap_or_default();
        return Some((current, property_name, called_method));
    }
    None
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct PropertyServiceCandidate {
    pub class_name: String,
    pub score: i32,
}

pub(crate) fn property_service_candidates(
    php_index: Option<&PhpIndex>,
    property_name: &str,
    called_method: &str,
) -> Vec<PropertyServiceCandidate> {
    let Some(php_index) = php_index else {
        return Vec::new();
    };
    let normalized_property = normalize_property_service_name(property_name);
    if normalized_property.is_empty() {
        return Vec::new();
    }
    let mut search_names: HashSet<String> = HashSet::from([normalized_property.clone()]);
    if !eq_ignore_case(&normalized_property, "logger")
        && lowercase(property_name.trim_matches('_')).ends_with("logger")
    {
        search_names.insert("logger".to_owned());
    }
    let mut scores: HashMap<String, i32> = HashMap::new();
    for search_name in &search_names {
        let target = match search_name.as_str() {
            "twig" | "template" => "Twig\\Environment",
            "router" => "Symfony\\Component\\Routing\\Generator\\UrlGeneratorInterface",
            _ => continue,
        };
        if php_index.find_class(target).is_some() {
            *scores.entry(target.to_owned()).or_default() += 4;
        }
    }

    let allow_suffix = property_service_word_count(property_name) >= 3;
    let symbols = php_index.class_symbols();
    let mut symbol_by_name: HashMap<String, Symbol> = HashMap::with_capacity(symbols.len());
    for symbol in symbols {
        let qualified: &str = &symbol.fully_qualified;
        let class_name = qualified.strip_prefix('\\').unwrap_or(qualified).to_owned();
        if property_service_garbage_class(&class_name, &symbol.path) {
            continue;
        }
        let normalized_class = normalize_property_service_name(php_class_short_name(&class_name));
        symbol_by_name.insert(class_name.clone(), symbol);
        if normalized_class.is_empty() {
            continue;
        }
        let mut score = 0;
        for search_name in &search_names {
            if eq_ignore_case(&normalized_class, search_name) {
                score = score.max(3);
            } else if allow_suffix
                && lowercase(&normalized_class).ends_with(&lowercase(search_name))
            {
                score = score.max(1);
            }
        }
        if score == 0 {
            continue;
        }
        *scores.entry(class_name).or_default() += score;
    }

    let mut result = Vec::with_capacity(scores.len());
    for (class_name, mut score) in scores {
        let symbol = match symbol_by_name.get(&class_name) {
            Some(symbol) => symbol.clone(),
            None => match php_index.find_class(&class_name) {
                Some(symbol) => symbol,
                None => continue,
            },
        };
        if !called_method.is_empty()
            && php_index
                .find_methods(&class_name, called_method)
                .is_empty()
        {
            score -= 4;
        }
        match symbol.kind {
            SymbolKind::Interface => {
                score += 2;
                let lower = lowercase(&class_name);
                if lower.contains("\\symfony\\") && lower.contains("\\contracts\\") {
                    score += 2;
                } else if lower.starts_with("psr\\") {
                    score += 3;
                }
            }
            SymbolKind::Class if symbol.flags.has(Flags::ABSTRACT) => score += 1,
            _ => {}
        }
        if lowercase(php_class_short_name(&class_name)).contains("decorator") {
            score -= 3;
        }
        if score < 0 {
            continue;
        }
        result.push(PropertyServiceCandidate { class_name, score });
    }
    result.sort_by(|left, right| {
        right
            .score
            .cmp(&left.score)
            .then_with(|| lowercase(&left.class_name).cmp(&lowercase(&right.class_name)))
    });
    result.truncate(MAXIMUM_PROPERTY_SERVICE_CANDIDATES);
    result
}

fn normalize_property_service_name(value: &str) -> String {
    let mut value = lowercase(&value.trim_matches('_').replace('_', ""));
    loop {
        let original = value.clone();
        for keyword in ["interface", "abstract", "decorator"] {
            if let Some(rest) = value.strip_prefix(keyword) {
                value = rest.to_owned();
            }
            if let Some(rest) = value.strip_suffix(keyword) {
                value = rest.to_owned();
            }
        }
        if value == original {
            return value;
        }
    }
}

fn property_service_word_count(value: &str) -> usize {
    let value = value.trim_matches('_');
    if value.is_empty() {
        return 0;
    }
    let runes: Vec<char> = value.chars().collect();
    let mut count = 1;
    for (index, &current) in runes.iter().enumerate() {
        if current == '_' {
            if index + 1 < runes.len() && runes[index + 1] != '_' {
                count += 1;
            }
            continue;
        }
        if index == 0 || !current.is_uppercase() {
            continue;
        }
        let previous = runes[index - 1];
        let next_lower = index + 1 < runes.len() && runes[index + 1].is_lowercase();
        if previous.is_lowercase()
            || previous.is_numeric()
            || (previous.is_uppercase() && next_lower)
        {
            count += 1;
        }
    }
    count
}

fn property_service_garbage_class(class_name: &str, path: &str) -> bool {
    let lower_name = lowercase(class_name);
    let lower_path = lowercase(&path.replace('\\', "/"));
    lower_name.ends_with("test")
        || lower_name.contains("\\test\\")
        || lower_name.contains("_phpstan_")
        || lower_name.contains("ecsprefix")
        || lower_name.contains("_humbugbox")
        || lower_name.contains("rectorprefix")
        || lower_path.contains("/tests/")
}

fn php_constructor_uses_promotion(constructor: &SyntaxNode) -> bool {
    phpquery::parameters(constructor).any(|parameter| {
        child_tokens(&parameter).any(|token| {
            matches!(
                lowercase(token.text()).as_str(),
                "public" | "protected" | "private"
            )
        })
    })
}

fn php_traditional_property_edit(
    request: &CodeActionRequest,
    constructor: &SyntaxNode,
    qualifier: &str,
    property_name: &str,
) -> Option<protocol::TextEdit> {
    request.document.as_ref()?;
    let offset = trimmed_start(constructor);
    let indent = php_line_indentation(request_source(request), offset);
    Some(text_edit(
        request,
        offset,
        offset,
        format!("private {qualifier} ${property_name};\n\n{indent}"),
    ))
}

fn php_constructor_assignment_edit(
    request: &CodeActionRequest,
    class: &SyntaxNode,
    constructor: &SyntaxNode,
    property_name: &str,
) -> Option<protocol::TextEdit> {
    let block = phpquery::direct_child(constructor, PHP_BLOCK)?;
    request.document.as_ref()?;
    let source = request_source(request);
    let (open, close) = php_block_delimiters(&block);
    let (open, close) = (open?, close?);
    let close_start = token_start(&close);
    if token_end(&open) > close_start || close_start as usize > source.len() {
        return None;
    }
    let start = token_end(&open);
    let mut end = start;
    while end < close_start
        && matches!(
            source.as_bytes()[end as usize],
            b' ' | b'\t' | b'\r' | b'\n'
        )
    {
        end += 1;
    }
    let method_indent = php_line_indentation(source, trimmed_start(constructor));
    let statement_indent = format!(
        "{method_indent}{}",
        php_member_indent_unit(source, Some(class), method_indent)
    );
    let mut new_text = format!("\n{statement_indent}$this->{property_name} = ${property_name};");
    if end < close_start {
        new_text.push('\n');
        new_text.push_str(&statement_indent);
    } else {
        new_text.push('\n');
        new_text.push_str(method_indent);
    }
    Some(text_edit(request, start, end, new_text))
}

#[cfg(test)]
#[path = "tests/property_service.rs"]
mod tests;
