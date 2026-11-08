//! DAL flag and fluent-modifier import (port of Go `import_flags.go`).

use std::sync::LazyLock;

use regex::{Captures, Regex};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as php;
use swlsp_syntax::SyntaxNode;

use crate::import::node_string;

use super::{Resolver, imported_string_argument};
use crate::model::*;
use crate::util::*;

static NEW_CLASS_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?i)((?-u:\b)new[\t\n\x0C\r ]+)([\\A-Za-z_][\\A-Za-z0-9_]*)")
        .expect("valid pattern")
});
static SCOPED_REF_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"([\\A-Za-z_][\\A-Za-z0-9_]*)::([A-Za-z_][A-Za-z0-9_]*)(?-u:\b)")
        .expect("valid pattern")
});

#[derive(Clone, Debug, Default)]
pub(crate) struct ImportedFlagSet {
    pub(crate) required: bool,
    pub(crate) primary: bool,
    pub(crate) api_aware: bool,
    pub(crate) api_aware_sources: Vec<String>,
    pub(crate) ranking: f64,
    pub(crate) ranking_tokenize: Option<bool>,
    pub(crate) behavior: Option<FieldBehavior>,
    pub(crate) metadata: Option<FieldMetadata>,
    pub(crate) inherited: bool,
    pub(crate) inherited_foreign_key: String,
    pub(crate) reverse_inherited_property: String,
    pub(crate) preserved: Vec<String>,
}

impl ImportedFlagSet {
    /// Applies the flags carried by a stored (non-association) DAL field.
    pub(crate) fn apply_stored(&self, field: &mut FieldSpec) {
        field.required = self.required;
        field.primary = self.primary;
        field.api_aware = self.api_aware;
        field.api_aware_sources = self.api_aware_sources.clone();
        field.search_ranking = self.ranking;
        field.search_ranking_tokenize = self.ranking_tokenize;
        field.behavior = self.behavior.clone();
        field.metadata = self.metadata.clone();
        field.inherited = self.inherited;
        field.inherited_foreign_key = self.inherited_foreign_key.clone();
        field.preserved_flags = self.preserved.clone();
    }

    /// Applies the flags carried by the association half of a relation.
    pub(crate) fn apply_association(&self, field: &mut FieldSpec) {
        field.association_api_aware = self.api_aware;
        field.association_api_aware_sources = self.api_aware_sources.clone();
        field.association_search_rank = self.ranking;
        field.association_search_tokenize = self.ranking_tokenize;
        field.association_behavior = self.behavior.clone();
        field.association_metadata = self.metadata.clone();
        field.association_inherited = self.inherited;
        field.association_inherited_fk = self.inherited_foreign_key.clone();
        field.reverse_inherited_property = self.reverse_inherited_property.clone();
        field.association_flags = self.preserved.clone();
    }
}

#[derive(Clone, Debug, Default)]
pub(crate) struct ImportedModifierSet {
    pub(crate) before_flags: Vec<String>,
    pub(crate) after_flags: Vec<String>,
}

fn encloses(outer: &SyntaxNode, inner: &SyntaxNode) -> bool {
    let outer = outer.text_range();
    let inner = inner.text_range();
    outer.start() <= inner.start() && outer.end() >= inner.end()
}

/// Fluent modifiers around a field creation, from the value's collected calls.
pub(crate) fn imported_modifiers_in(
    mut calls: Vec<SyntaxNode>,
    creation: &SyntaxNode,
) -> ImportedModifierSet {
    calls.sort_by(|left, right| {
        let (left, right) = (left.text_range(), right.text_range());
        left.end()
            .cmp(&right.end())
            .then_with(|| right.start().cmp(&left.start()))
    });
    let mut result = ImportedModifierSet::default();
    let mut seen_flags = false;
    for call in &calls {
        if call.kind() != php::PHP_MEMBER_CALL || !encloses(call, creation) {
            continue;
        }
        let method = phpquery::call_method_name(call);
        if method == "addFlags" {
            seen_flags = true;
            continue;
        }
        let suffix = imported_call_suffix(call, &method);
        if suffix.is_empty() {
            continue;
        }
        if seen_flags {
            result.after_flags.push(suffix);
        } else {
            result.before_flags.push(suffix);
        }
    }
    result
}

pub(crate) fn imported_call_suffix(call: &SyntaxNode, method: &str) -> String {
    if method.is_empty() {
        return String::new();
    }
    let text = node_string(call);
    match text.rfind(&format!("->{method}")) {
        Some(index) => dedent_inline_php_expression(&text[index..]),
        None => String::new(),
    }
}

pub(crate) fn with_field_modifiers(
    mut field: FieldSpec,
    modifiers: &ImportedModifierSet,
) -> FieldSpec {
    field.modifiers_before_flags = modifiers.before_flags.clone();
    field.modifiers_after_flags = modifiers.after_flags.clone();
    field
}

pub(crate) fn with_association_modifiers(
    mut field: FieldSpec,
    modifiers: &ImportedModifierSet,
) -> FieldSpec {
    field.association_before_flags = modifiers.before_flags.clone();
    field.association_after_flags = modifiers.after_flags.clone();
    field
}

/// `addFlags` arguments around a field creation, from the value's collected calls.
pub(crate) fn imported_flags_in(
    calls: &[SyntaxNode],
    field_creation: &SyntaxNode,
    resolve: &Resolver,
) -> ImportedFlagSet {
    let mut result = ImportedFlagSet::default();
    for call in calls {
        if call.kind() != php::PHP_MEMBER_CALL
            || !encloses(call, field_creation)
            || phpquery::call_method_name(call) != "addFlags"
        {
            continue;
        }
        import_add_flags_arguments(&mut result, call, resolve);
    }
    result
}

pub(crate) fn import_add_flags_arguments(
    result: &mut ImportedFlagSet,
    call: &SyntaxNode,
    resolve: &Resolver,
) {
    let count = phpquery::arguments(call).count();
    for index in 0..count {
        let source = phpquery::argument_value_text(call, index).trim().to_owned();
        let expression = phpquery::argument_expression(call, index);
        match outer_object_creation(expression.as_ref()) {
            Some(creation) => import_flag_creation(result, &creation, source, resolve),
            None => {
                if !source.is_empty() {
                    result.preserved.push(source);
                }
            }
        }
    }
}

pub(crate) fn outer_object_creation(expression: Option<&SyntaxNode>) -> Option<SyntaxNode> {
    let creations = phpquery::object_creations(expression?, &[]);
    let mut outer = creations.first()?.clone();
    for creation in &creations[1..] {
        if creation.text_range().len() > outer.text_range().len() {
            outer = creation.clone();
        }
    }
    Some(outer)
}

pub(crate) fn import_flag_creation(
    result: &mut ImportedFlagSet,
    creation: &SyntaxNode,
    source: String,
    resolve: &Resolver,
) {
    let preserve = |result: &mut ImportedFlagSet| {
        let source = if source.is_empty() {
            node_string(creation).trim().to_owned()
        } else {
            source.clone()
        };
        result.preserved.push(source);
    };
    fn metadata(result: &mut ImportedFlagSet) -> &mut FieldMetadata {
        result.metadata.get_or_insert_with(FieldMetadata::default)
    }
    fn behavior(result: &mut ImportedFlagSet) -> &mut FieldBehavior {
        result.behavior.get_or_insert_with(FieldBehavior::default)
    }
    match short_class(&phpquery::object_class_name(creation)) {
        "Required" => result.required = true,
        "PrimaryKey" => result.primary = true,
        "ApiAware" => match imported_api_aware_sources(creation, resolve) {
            Some(sources) => {
                result.api_aware = true;
                result.api_aware_sources = sources;
            }
            None => preserve(result),
        },
        "SearchRanking" => match imported_search_ranking(creation) {
            Some((ranking, tokenize)) => {
                result.ranking = ranking;
                result.ranking_tokenize = tokenize;
            }
            None => preserve(result),
        },
        "Runtime" => {
            let (dependencies, expression) = imported_runtime_dependencies(creation);
            let behavior = behavior(result);
            behavior.runtime = true;
            behavior.runtime_dependencies = dependencies;
            behavior.runtime_dependencies_expression = expression;
        }
        "Computed" => behavior(result).computed = true,
        "NoConstraint" => behavior(result).no_constraint = true,
        "AllowHtml" => match imported_default_bool_argument(creation, 0, true) {
            Some(value) => metadata(result).allow_html = Some(value),
            None => preserve(result),
        },
        "AllowEmptyString" => metadata(result).allow_empty_string = true,
        "AsArray" => metadata(result).as_array = true,
        "Immutable" => metadata(result).immutable = true,
        "Since" => {
            let value = imported_string_argument(creation, 0);
            if value.is_empty() {
                preserve(result);
            } else {
                metadata(result).since = value;
            }
        }
        "Deprecated" => match imported_deprecation(creation) {
            Some(deprecated) => metadata(result).deprecated = Some(deprecated),
            None => preserve(result),
        },
        "IgnoreInOpenapiSchema" => metadata(result).ignore_in_open_api_schema = true,
        "IgnoreInUnusedMediaSearch" => metadata(result).ignore_in_unused_media_search = true,
        "ApiCriteriaAware" => metadata(result).api_criteria_aware = true,
        "RuleAreas" => match imported_rule_areas(creation) {
            Some(areas) => metadata(result).rule_areas = areas,
            None => preserve(result),
        },
        "Choice" => match imported_choice(creation) {
            Some(choice) => metadata(result).choice = Some(choice),
            None => preserve(result),
        },
        "DoNotUseContext" => metadata(result).do_not_use_context = true,
        "Extension" => metadata(result).extension = true,
        "Inherited" => {
            result.inherited = true;
            result.inherited_foreign_key = imported_string_argument(creation, 0);
        }
        "ReverseInherited" => {
            result.reverse_inherited_property = imported_string_argument(creation, 0);
        }
        // Represented structurally by the field kind or relation behavior.
        "CascadeDelete" | "SetNullOnDelete" | "RestrictDelete" => {}
        _ => preserve(result),
    }
}

/// `None` means the argument is not a literal boolean.
pub(crate) fn imported_default_bool_argument(
    creation: &SyntaxNode,
    index: usize,
    fallback: bool,
) -> Option<bool> {
    if index >= phpquery::arguments(creation).count() {
        return Some(fallback);
    }
    match go_to_lower(phpquery::argument_value_text(creation, index).trim()).as_str() {
        "true" => Some(true),
        "false" => Some(false),
        _ => None,
    }
}

pub(crate) fn imported_api_aware_sources(
    creation: &SyntaxNode,
    resolve: &Resolver,
) -> Option<Vec<String>> {
    let count = phpquery::arguments(creation).count();
    let mut sources: Vec<String> = Vec::with_capacity(count);
    for index in 0..count {
        let mut class = super::imported_class_argument(creation, index);
        if class.is_empty() {
            class = imported_string_argument(creation, index);
        }
        let class = trim_class(&resolve.resolve(&class)).to_owned();
        if class.is_empty() {
            return None;
        }
        if !sources.contains(&class) {
            sources.push(class);
        }
    }
    Some(sources)
}

/// Makes class references self-contained so a generated definition does not
/// depend on the source file's old `use` list. Other syntax is unchanged.
pub(crate) fn normalize_imported_php_expression(source: &str, resolve: &Resolver) -> String {
    let source = dedent_inline_php_expression(source);
    let qualify = |class: &str| -> String {
        if class.trim().starts_with('\\') {
            return format!("\\{}", trim_class(class));
        }
        match go_to_lower(class.trim()).as_str() {
            "" | "self" | "static" | "parent" | "class" => return class.to_owned(),
            _ => {}
        }
        let resolved = resolve.resolve(class);
        let resolved = trim_class(&resolved);
        if resolved.is_empty() {
            class.to_owned()
        } else {
            format!("\\{resolved}")
        }
    };
    let source = NEW_CLASS_PATTERN.replace_all(&source, |captures: &Captures| {
        format!("{}{}", &captures[1], qualify(&captures[2]))
    });
    SCOPED_REF_PATTERN
        .replace_all(&source, |captures: &Captures| {
            format!("{}::{}", qualify(&captures[1]), &captures[2])
        })
        .into_owned()
}

pub(crate) fn dedent_inline_php_expression(source: &str) -> String {
    let trimmed = source.trim();
    let mut lines: Vec<&str> = trimmed.split('\n').collect();
    if lines.len() < 2 {
        return trimmed.to_owned();
    }
    let mut minimum: Option<usize> = None;
    for line in &lines[1..] {
        if line.trim().is_empty() {
            continue;
        }
        let indent = line.len() - line.trim_start_matches([' ', '\t']).len();
        minimum = Some(minimum.map_or(indent, |current| current.min(indent)));
    }
    if let Some(minimum) = minimum.filter(|minimum| *minimum > 0) {
        for line in lines.iter_mut().skip(1) {
            if line.len() >= minimum && line.is_char_boundary(minimum) {
                *line = &line[minimum..];
            }
        }
    }
    lines.join("\n")
}

pub(crate) fn imported_deprecation(creation: &SyntaxNode) -> Option<Deprecation> {
    let count = phpquery::arguments(creation).count();
    if !(2..=3).contains(&count) {
        return None;
    }
    let mut deprecated = Deprecation {
        deprecated_since: imported_string_argument(creation, 0),
        will_be_removed_in: imported_string_argument(creation, 1),
        replaced_by: String::new(),
    };
    if count == 3 {
        deprecated.replaced_by = imported_string_argument(creation, 2);
    }
    (!deprecated.deprecated_since.is_empty() && !deprecated.will_be_removed_in.is_empty())
        .then_some(deprecated)
}

pub(crate) fn imported_rule_areas(creation: &SyntaxNode) -> Option<Vec<String>> {
    let count = phpquery::arguments(creation).count();
    let mut areas = Vec::with_capacity(count);
    for index in 0..count {
        if let Some(expression) = phpquery::argument_expression(creation, index)
            && expression.kind() == php::PHP_STRING
        {
            areas.push(phpquery::string_value(&expression));
            continue;
        }
        let normalized = phpquery::argument_value_text(creation, index)
            .trim()
            .replace(' ', "");
        let separator = normalized.rfind("::")?;
        let area = match &normalized[separator + 2..] {
            "PRODUCT_AREA" => "product",
            "PAYMENT_AREA" => "payment",
            "SHIPPING_AREA" => "shipping",
            "PROMOTION_AREA" => "promotion",
            "FLOW_AREA" => "flow",
            "FLOW_CONDITION_AREA" => "flow-condition",
            "CATEGORY_AREA" => "category",
            "LANDING_PAGE_AREA" => "landing-page",
            _ => return None,
        };
        areas.push(area.to_owned());
    }
    Some(areas)
}

pub(crate) fn imported_choice(creation: &SyntaxNode) -> Option<ChoiceSpec> {
    let count = phpquery::arguments(creation).count();
    if count == 0 || count > 2 {
        return None;
    }
    let expression = phpquery::argument_expression(creation, 0)?;
    let arrays = phpquery::arrays(&expression);
    if arrays.len() != 1 || arrays[0].text_range() != expression.text_range() {
        return None;
    }
    let mut choice = ChoiceSpec::default();
    for item in phpquery::array_items(&arrays[0]) {
        let value = phpquery::array_item_value(&item)?;
        choice.values.push(node_string(&value).trim().to_owned());
    }
    if count == 2 {
        choice.strict = Some(imported_default_bool_argument(creation, 1, false)?);
    }
    Some(choice)
}

/// Go `strconv.ParseFloat`: decimal and special values only.
fn parse_go_float(text: &str) -> Option<f64> {
    if text.is_empty() || text.contains('_') {
        return None;
    }
    let lower = text.to_ascii_lowercase();
    let unsigned = lower.trim_start_matches(['+', '-']);
    if unsigned.starts_with("0x") {
        return None;
    }
    text.parse::<f64>().ok()
}

pub(crate) fn imported_search_ranking(creation: &SyntaxNode) -> Option<(f64, Option<bool>)> {
    let count = phpquery::arguments(creation).count();
    if count == 0 || count > 2 {
        return None;
    }
    let expression = phpquery::argument_value_text(creation, 0).trim().to_owned();
    let ranking = match parse_go_float(&expression) {
        Some(ranking) => ranking,
        None => {
            let normalized = trim_prefix(&expression, "\\").replace(' ', "");
            if normalized.ends_with("SearchRanking::ASSOCIATION_SEARCH_RANKING") {
                0.25
            } else if normalized.ends_with("SearchRanking::LOW_SEARCH_RANKING") {
                80.0
            } else if normalized.ends_with("SearchRanking::MIDDLE_SEARCH_RANKING") {
                250.0
            } else if normalized.ends_with("SearchRanking::HIGH_SEARCH_RANKING") {
                500.0
            } else {
                return None;
            }
        }
    };
    if count == 1 {
        return Some((ranking, None));
    }
    match go_to_lower(phpquery::argument_value_text(creation, 1).trim()).as_str() {
        "true" => Some((ranking, Some(true))),
        "false" => Some((ranking, Some(false))),
        _ => None,
    }
}

pub(crate) fn imported_runtime_dependencies(creation: &SyntaxNode) -> (Vec<String>, String) {
    if phpquery::arguments(creation).next().is_none() {
        return (Vec::new(), String::new());
    }
    let Some(expression) = phpquery::argument_expression(creation, 0) else {
        return (
            Vec::new(),
            phpquery::argument_value_text(creation, 0).trim().to_owned(),
        );
    };
    let expression_text = || node_string(&expression).trim().to_owned();
    let arrays = phpquery::arrays(&expression);
    if arrays.len() != 1 || arrays[0].text_range() != expression.text_range() {
        return (Vec::new(), expression_text());
    }
    let items = phpquery::array_items(&arrays[0]);
    let mut dependencies = Vec::with_capacity(items.len());
    for item in &items {
        match phpquery::array_item_value(item).filter(|value| value.kind() == php::PHP_STRING) {
            Some(value) => dependencies.push(phpquery::string_value(&value)),
            None => return (Vec::new(), expression_text()),
        }
    }
    (dependencies, String::new())
}
