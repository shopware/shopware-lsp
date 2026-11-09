//! Class-based definition metadata hooks.

use std::collections::HashSet;
use std::sync::LazyLock;

use regex::Regex;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as php;
use swlsp_syntax::SyntaxNode;

use crate::import::node_string;

use crate::definition_behavior::append_member;
use crate::import::{
    Resolver, returned_expression_text, returned_imported_class, single_return_only,
};
use crate::model::*;
use crate::render::ImportTable;
use crate::util::*;
use crate::validate_spec::{NAMESPACE_PATTERN, PROPERTY_PATTERN, SpecValidator};

static DEFINITION_SINCE_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"^[0-9]+\.[0-9]+\.[0-9]+(?:\.[0-9]+)?$").expect("valid pattern"));

pub(crate) fn import_definition_metadata(
    methods: &[SyntaxNode],
    resolve: &Resolver,
    allow_defaults: bool,
    allow_child_defaults: bool,
    allow_hydrator: bool,
) -> Option<DefinitionMetadataSpec> {
    let mut metadata = DefinitionMetadataSpec::default();
    for method in methods {
        let raw = || node_string(method).trim().to_owned();
        match phpquery::method_name(method).as_str() {
            "since" => match imported_literal_string_return(Some(method)) {
                Some(value) => metadata.since = value,
                None => metadata.since_method_raw = raw(),
            },
            "getDefaults" => {
                if !allow_defaults {
                    continue;
                }
                match imported_definition_defaults(method) {
                    Some(values) => metadata.defaults = values,
                    None => metadata.defaults_method_raw = raw(),
                }
            }
            "getChildDefaults" => {
                if !allow_child_defaults {
                    continue;
                }
                match imported_definition_defaults(method) {
                    Some(values) => metadata.child_defaults = values,
                    None => metadata.child_defaults_method_raw = raw(),
                }
            }
            "getHydratorClass" => {
                if !allow_hydrator {
                    continue;
                }
                let class = returned_imported_class(Some(method), resolve);
                if !class.is_empty() {
                    metadata.hydrator_class = class;
                } else {
                    metadata.hydrator_method_raw = raw();
                }
            }
            _ => {}
        }
    }
    if definition_metadata_empty(&metadata) {
        return None;
    }
    Some(metadata)
}

/// A single literal, non-empty string returned by a method.
pub(crate) fn imported_literal_string_return(method: Option<&SyntaxNode>) -> Option<String> {
    let returned = single_return_only(method)?;
    let strings = phpquery::nodes(&returned, &[php::PHP_STRING]);
    if strings.len() != 1 || node_string(&strings[0]).trim() != returned_expression_text(&returned)
    {
        return None;
    }
    let value = phpquery::string_value(&strings[0]);
    (!value.is_empty()).then_some(value)
}

pub(crate) fn imported_definition_defaults(
    method: &SyntaxNode,
) -> Option<Vec<DefinitionDefaultSpec>> {
    let returned = single_return_only(Some(method))?;
    let array = phpquery::direct_child(&returned, php::PHP_ARRAY)?;
    if node_string(&array).trim() != returned_expression_text(&returned) {
        return None;
    }
    let items = phpquery::array_items(&array);
    let mut result = Vec::with_capacity(items.len());
    for item in &items {
        let key = phpquery::array_item_key(item).filter(|key| key.kind() == php::PHP_STRING)?;
        let value = phpquery::array_item_value(item)?;
        let property = phpquery::string_value(&key);
        let expression = node_string(&value).trim().to_owned();
        if property.is_empty() || expression.is_empty() {
            return None;
        }
        result.push(DefinitionDefaultSpec {
            property_name: property,
            value_expression: expression,
        });
    }
    Some(result)
}

fn definition_metadata_empty(metadata: &DefinitionMetadataSpec) -> bool {
    metadata.since.is_empty()
        && metadata.defaults.is_empty()
        && metadata.child_defaults.is_empty()
        && metadata.hydrator_class.is_empty()
        && metadata.since_method_raw.is_empty()
        && metadata.defaults_method_raw.is_empty()
        && metadata.child_defaults_method_raw.is_empty()
        && metadata.hydrator_method_raw.is_empty()
}

pub(crate) fn validate_definition_metadata(
    v: &mut SpecValidator,
    metadata: Option<&DefinitionMetadataSpec>,
    owner: &str,
    translation: bool,
) {
    let Some(metadata) = metadata else {
        return;
    };
    let field_id = if translation { "translation" } else { "" };
    if owner == DEFINITION_EXTENSION || owner == DEFINITION_BULK_EXTENSION {
        v.add(
            "entity.definitionMetadata.owner.unsupported",
            "EntityExtension classes cannot declare EntityDefinition metadata",
            field_id,
        );
        return;
    }
    if !metadata.since.is_empty() && !DEFINITION_SINCE_PATTERN.is_match(&metadata.since) {
        v.add(
            "entity.definitionMetadata.since.invalid",
            "Definition availability must use a numeric Shopware version such as 6.7.1.0",
            field_id,
        );
    }
    let conflicts = [
        (
            !metadata.since.is_empty(),
            &metadata.since_method_raw,
            "since",
        ),
        (
            !metadata.defaults.is_empty(),
            &metadata.defaults_method_raw,
            "getDefaults",
        ),
        (
            !metadata.child_defaults.is_empty(),
            &metadata.child_defaults_method_raw,
            "getChildDefaults",
        ),
        (
            !metadata.hydrator_class.is_empty(),
            &metadata.hydrator_method_raw,
            "getHydratorClass",
        ),
    ];
    for (typed, raw, method) in conflicts {
        if typed && !raw.trim().is_empty() {
            v.add(
                "entity.definitionMetadata.raw.conflict",
                format!(
                    "Structured {method} metadata cannot be combined with a preserved custom method"
                ),
                field_id,
            );
        }
    }
    let child_or_hydrator = !metadata.child_defaults.is_empty()
        || !metadata.child_defaults_method_raw.is_empty()
        || !metadata.hydrator_class.is_empty()
        || !metadata.hydrator_method_raw.is_empty();
    if translation && child_or_hydrator {
        v.add(
            "entity.definitionMetadata.translation.unsupported",
            "Translation definitions support since and defaults, but not child defaults or custom hydrators",
            field_id,
        );
    }
    if owner != DEFINITION_ENTITY && child_or_hydrator {
        v.add(
            "entity.definitionMetadata.kind.unsupported",
            "Only normal entity definitions support child defaults and custom hydrators",
            field_id,
        );
    }
    validate_definition_defaults(v, &metadata.defaults, "defaults", field_id);
    validate_definition_defaults(v, &metadata.child_defaults, "child defaults", field_id);
    if !metadata.hydrator_class.is_empty() && !NAMESPACE_PATTERN.is_match(&metadata.hydrator_class)
    {
        v.add(
            "entity.definitionMetadata.hydrator.invalid",
            "Custom hydrator must be a valid fully-qualified PHP class",
            field_id,
        );
    }
    let has_raw = [
        &metadata.since_method_raw,
        &metadata.defaults_method_raw,
        &metadata.child_defaults_method_raw,
        &metadata.hydrator_method_raw,
    ]
    .iter()
    .any(|raw| !raw.trim().is_empty());
    if v.spec.mode != "edit" && has_raw {
        v.add(
            "entity.definitionMetadata.raw.creation.unsupported",
            "Custom definition metadata can only be preserved from an existing PHP class",
            field_id,
        );
    }
}

fn validate_definition_defaults(
    v: &mut SpecValidator,
    values: &[DefinitionDefaultSpec],
    label: &str,
    field_id: &str,
) {
    let mut seen = HashSet::new();
    for value in values {
        if !PROPERTY_PATTERN.is_match(&value.property_name) {
            v.add(
                "entity.definitionMetadata.default.property.invalid",
                format!(
                    "{label} property {:?} is not a valid DAL property name",
                    value.property_name
                ),
                field_id,
            );
        }
        if !seen.insert(value.property_name.as_str()) {
            v.add(
                "entity.definitionMetadata.default.property.duplicate",
                format!("{label} property {:?} is duplicated", value.property_name),
                field_id,
            );
        }
        if !valid_php_expression(&value.value_expression) {
            v.add(
                "entity.definitionMetadata.default.expression.invalid",
                format!(
                    "{label} value for {:?} is not a valid PHP expression",
                    value.property_name
                ),
                field_id,
            );
        }
    }
}

fn valid_php_expression(expression: &str) -> bool {
    let expression = expression.trim();
    if expression.is_empty() {
        return false;
    }
    swlsp_parser_php::parse(&format!("<?php $value = {expression};"))
        .errors
        .is_empty()
}

pub(crate) fn render_definition_metadata(
    metadata: Option<&DefinitionMetadataSpec>,
    imports: &ImportTable,
    translation: bool,
) -> String {
    let Some(metadata) = metadata else {
        return String::new();
    };
    let mut methods = Vec::new();
    append_member(
        &mut methods,
        &metadata.since_method_raw,
        render_since_method(&metadata.since),
    );
    append_member(
        &mut methods,
        &metadata.defaults_method_raw,
        render_defaults_method("getDefaults", &metadata.defaults),
    );
    if !translation {
        append_member(
            &mut methods,
            &metadata.child_defaults_method_raw,
            render_defaults_method("getChildDefaults", &metadata.child_defaults),
        );
        append_member(
            &mut methods,
            &metadata.hydrator_method_raw,
            render_hydrator_method(&metadata.hydrator_class, imports),
        );
    }
    methods.join("\n\n")
}

fn render_since_method(since: &str) -> String {
    if since.is_empty() {
        return String::new();
    }
    format!(
        "public function since(): ?string\n{{\n    return {};\n}}",
        quote_php(since)
    )
}

fn render_defaults_method(name: &str, values: &[DefinitionDefaultSpec]) -> String {
    if values.is_empty() {
        return String::new();
    }
    let mut builder = format!("public function {name}(): array\n{{\n    return [\n");
    for value in values {
        builder.push_str("        ");
        builder.push_str(&quote_php(&value.property_name));
        builder.push_str(" => ");
        builder.push_str(value.value_expression.trim());
        builder.push_str(",\n");
    }
    builder.push_str("    ];\n}");
    builder
}

fn render_hydrator_method(class: &str, imports: &ImportTable) -> String {
    if class.is_empty() {
        return String::new();
    }
    format!(
        "public function getHydratorClass(): string\n{{\n    return {}::class;\n}}",
        imports.reference(class)
    )
}
