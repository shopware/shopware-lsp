//! Class-level EntityDefinition behavior.

use std::collections::HashSet;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as php;
use swlsp_syntax::SyntaxNode;

use crate::import::node_string;

use crate::import::{
    Resolver, import_fields, imported_boolean_return, returned_expression_text,
    returned_imported_class, single_return_only,
};
use crate::model::*;
use crate::render::{ImportTable, render_definition_field};
use crate::util::*;
use crate::validate_spec::{NAMESPACE_PATTERN, PROPERTY_PATTERN, SpecValidator};

pub(crate) fn import_definition_behavior(
    methods: &[SyntaxNode],
    resolve: &Resolver,
    lookup: Option<RelationLookup>,
    translation: bool,
) -> Option<DefinitionBehaviorSpec> {
    let mut behavior = DefinitionBehaviorSpec::default();
    for method in methods {
        let raw = || node_string(method).trim().to_owned();
        match phpquery::method_name(method).as_str() {
            "getParentDefinitionClass" => {
                if translation {
                    continue;
                }
                let class = returned_imported_class(Some(method), resolve);
                if !class.is_empty() {
                    behavior.parent_definition_class = class;
                } else {
                    behavior.parent_definition_method_raw = raw();
                }
            }
            "isVersionAware" => match imported_boolean_return(Some(method)) {
                Some(value) => behavior.version_aware = Some(value),
                None => behavior.version_aware_method_raw = raw(),
            },
            "isInheritanceAware" => {
                if translation {
                    continue;
                }
                if imported_boolean_return(Some(method)).is_none() {
                    behavior.inheritance_aware_method_raw = raw();
                }
            }
            "defaultFields" => {
                behavior.override_default_fields = true;
                match imported_definition_fields(method, resolve, lookup, "default-field") {
                    Some(fields) => behavior.default_fields = fields,
                    None => behavior.default_fields_method_raw = raw(),
                }
            }
            "getBaseFields" => {
                behavior.override_base_fields = true;
                match imported_definition_fields(method, resolve, lookup, "base-field") {
                    Some(fields) => behavior.base_fields = fields,
                    None => behavior.base_fields_method_raw = raw(),
                }
            }
            "getRestrictDeleteMetaFields" => {
                if translation {
                    continue;
                }
                match imported_restrict_delete_properties(method) {
                    Some(properties) => behavior.restrict_delete_meta_properties = properties,
                    None => behavior.restrict_delete_meta_method_raw = raw(),
                }
            }
            _ => {}
        }
    }
    if definition_behavior_empty(&behavior) {
        return None;
    }
    Some(behavior)
}

pub(crate) fn imported_definition_fields(
    method: &SyntaxNode,
    resolve: &Resolver,
    lookup: Option<RelationLookup>,
    id_prefix: &str,
) -> Option<Vec<FieldSpec>> {
    let returned = single_return_only(Some(method))?;
    let array = phpquery::direct_child(&returned, php::PHP_ARRAY)?;
    let array_text = node_string(&array);
    if array_text.trim() != returned_expression_text(&returned) {
        return None;
    }
    let synthetic = swlsp_parser_php::parse(&format!(
        "<?php class DesignerDefaultFields {{ protected function defineFields(): FieldCollection {{ return new FieldCollection({array_text}); }} }}"
    ));
    if !synthetic.errors.is_empty() {
        return None;
    }
    let classes = phpquery::classes(&synthetic.root());
    if classes.len() != 1 {
        return None;
    }
    let define_fields = phpquery::methods(&classes[0])
        .into_iter()
        .find(|candidate| phpquery::method_name(candidate) == "defineFields")?;
    let (mut fields, translation) = import_fields(&define_fields, resolve, lookup).ok()?;
    if translation.is_some() {
        return None;
    }
    for (index, field) in fields.iter_mut().enumerate() {
        if field.kind == FIELD_LOCKED {
            return None;
        }
        field.id = format!("{id_prefix}-{}", index + 1);
    }
    Some(fields)
}

pub(crate) fn imported_restrict_delete_properties(method: &SyntaxNode) -> Option<Vec<String>> {
    let returned = single_return_only(Some(method))?;
    let arrays = phpquery::nodes(&returned, &[php::PHP_ARRAY]);
    if arrays.len() != 1 {
        return None;
    }
    for array in &arrays {
        let items = phpquery::array_items(array);
        if items.is_empty() {
            continue;
        }
        let mut properties = Vec::with_capacity(items.len());
        let mut valid = true;
        for item in &items {
            if phpquery::array_item_key(item).is_some() {
                valid = false;
                break;
            }
            let Some(value) =
                phpquery::array_item_value(item).filter(|value| value.kind() == php::PHP_STRING)
            else {
                valid = false;
                break;
            };
            let property = phpquery::string_value(&value);
            if property.is_empty() {
                valid = false;
                break;
            }
            properties.push(property);
        }
        if valid {
            return Some(properties);
        }
    }
    None
}

pub(crate) fn definition_behavior_empty(behavior: &DefinitionBehaviorSpec) -> bool {
    behavior.parent_definition_class.is_empty()
        && behavior.version_aware.is_none()
        && !behavior.override_default_fields
        && behavior.default_fields.is_empty()
        && !behavior.override_base_fields
        && behavior.base_fields.is_empty()
        && behavior.restrict_delete_meta_properties.is_empty()
        && behavior.parent_definition_method_raw.is_empty()
        && behavior.version_aware_method_raw.is_empty()
        && behavior.inheritance_aware_method_raw.is_empty()
        && behavior.default_fields_method_raw.is_empty()
        && behavior.base_fields_method_raw.is_empty()
        && behavior.restrict_delete_meta_method_raw.is_empty()
}

pub(crate) fn validate_definition_behavior(
    v: &mut SpecValidator,
    behavior: Option<&DefinitionBehaviorSpec>,
    owner: &str,
    translation: bool,
) {
    let Some(behavior) = behavior else {
        return;
    };
    let field_id = if translation { "translation" } else { "" };
    if owner == DEFINITION_EXTENSION || owner == DEFINITION_BULK_EXTENSION {
        v.add(
            "entity.definitionBehavior.owner.unsupported",
            "EntityExtension classes cannot declare EntityDefinition behavior",
            field_id,
        );
        return;
    }
    if translation
        && (!behavior.parent_definition_class.is_empty()
            || !behavior.parent_definition_method_raw.is_empty()
            || !behavior.inheritance_aware_method_raw.is_empty()
            || !behavior.restrict_delete_meta_properties.is_empty()
            || !behavior.restrict_delete_meta_method_raw.is_empty())
    {
        v.add(
            "entity.definitionBehavior.translation.unsupported",
            "Translation behavior supports explicit version-awareness, default fields, and base fields only",
            field_id,
        );
    }
    if !behavior.parent_definition_class.is_empty()
        && !NAMESPACE_PATTERN.is_match(&behavior.parent_definition_class)
    {
        v.add(
            "entity.definitionBehavior.parent.invalid",
            "Aggregate parent must be a valid fully-qualified EntityDefinition class",
            field_id,
        );
    }
    let inheritance_aware = v.spec.inheritance_aware;
    let conflicts = [
        (
            !behavior.parent_definition_class.is_empty(),
            &behavior.parent_definition_method_raw,
            "getParentDefinitionClass",
        ),
        (
            behavior.version_aware.is_some(),
            &behavior.version_aware_method_raw,
            "isVersionAware",
        ),
        (
            inheritance_aware,
            &behavior.inheritance_aware_method_raw,
            "isInheritanceAware",
        ),
        (
            !behavior.default_fields.is_empty(),
            &behavior.default_fields_method_raw,
            "defaultFields",
        ),
        (
            !behavior.base_fields.is_empty(),
            &behavior.base_fields_method_raw,
            "getBaseFields",
        ),
        (
            !behavior.restrict_delete_meta_properties.is_empty(),
            &behavior.restrict_delete_meta_method_raw,
            "getRestrictDeleteMetaFields",
        ),
    ];
    for (typed, raw, method) in conflicts {
        if typed && !raw.trim().is_empty() {
            v.add(
                "entity.definitionBehavior.raw.conflict",
                format!(
                    "Structured {method} behavior cannot be combined with a preserved custom method"
                ),
                field_id,
            );
        }
    }
    if !behavior.default_fields_method_raw.is_empty() && !behavior.override_default_fields {
        v.add(
            "entity.definitionBehavior.defaultFields.override.missing",
            "A preserved defaultFields method requires overrideDefaultFields",
            field_id,
        );
    }
    if !behavior.default_fields.is_empty() && !behavior.override_default_fields {
        v.add(
            "entity.definitionBehavior.defaultFields.override.missing",
            "Structured default fields require overrideDefaultFields",
            field_id,
        );
    }
    if !behavior.base_fields_method_raw.is_empty() && !behavior.override_base_fields {
        v.add(
            "entity.definitionBehavior.baseFields.override.missing",
            "A preserved getBaseFields method requires overrideBaseFields",
            field_id,
        );
    }
    if !behavior.base_fields.is_empty() && !behavior.override_base_fields {
        v.add(
            "entity.definitionBehavior.baseFields.override.missing",
            "Structured base fields require overrideBaseFields",
            field_id,
        );
    }
    if owner == DEFINITION_MAPPING
        && (behavior.override_default_fields
            || !behavior.default_fields.is_empty()
            || !behavior.default_fields_method_raw.is_empty())
    {
        v.add(
            "entity.definitionBehavior.defaultFields.mapping.unsupported",
            "MappingEntityDefinition already disables framework default fields",
            field_id,
        );
    }
    for property in &behavior.restrict_delete_meta_properties {
        if !PROPERTY_PATTERN.is_match(property) {
            v.add(
                "entity.definitionBehavior.restrictDelete.property.invalid",
                format!(
                    "Restrict-delete metadata property {:?} is invalid",
                    property
                ),
                field_id,
            );
        }
    }
    let mut seen = HashSet::new();
    for property in &behavior.restrict_delete_meta_properties {
        if !seen.insert(property.as_str()) {
            v.add(
                "entity.definitionBehavior.restrictDelete.property.duplicate",
                format!(
                    "Restrict-delete metadata property {:?} is duplicated",
                    property
                ),
                field_id,
            );
        }
    }
    if v.spec.mode != "edit" && definition_behavior_has_raw(behavior) {
        v.add(
            "entity.definitionBehavior.raw.creation.unsupported",
            "Custom definition behavior can only be preserved from an existing PHP class",
            field_id,
        );
    }
}

fn definition_behavior_has_raw(behavior: &DefinitionBehaviorSpec) -> bool {
    [
        &behavior.parent_definition_method_raw,
        &behavior.version_aware_method_raw,
        &behavior.inheritance_aware_method_raw,
        &behavior.default_fields_method_raw,
        &behavior.base_fields_method_raw,
        &behavior.restrict_delete_meta_method_raw,
    ]
    .iter()
    .any(|raw| !raw.trim().is_empty())
}

pub(crate) fn render_definition_behavior(
    behavior: Option<&DefinitionBehaviorSpec>,
    imports: &ImportTable,
    translation: bool,
) -> Result<String> {
    let Some(behavior) = behavior else {
        return Ok(String::new());
    };
    let mut methods = Vec::new();
    if !translation {
        append_member(
            &mut methods,
            &behavior.parent_definition_method_raw,
            render_parent_definition_method(&behavior.parent_definition_class, imports),
        );
    }
    append_member(
        &mut methods,
        &behavior.version_aware_method_raw,
        render_boolean_definition_method("isVersionAware", behavior.version_aware),
    );
    if !translation {
        append_member(
            &mut methods,
            &behavior.inheritance_aware_method_raw,
            String::new(),
        );
    }
    let default_fields = render_default_fields_method(behavior, imports)?;
    append_member(
        &mut methods,
        &behavior.default_fields_method_raw,
        default_fields,
    );
    let base_fields = render_base_fields_method(behavior, imports)?;
    append_member(&mut methods, &behavior.base_fields_method_raw, base_fields);
    if !translation {
        append_member(
            &mut methods,
            &behavior.restrict_delete_meta_method_raw,
            render_restrict_delete_meta_method(&behavior.restrict_delete_meta_properties),
        );
    }
    Ok(methods.join("\n\n"))
}

/// A preserved raw member wins over the generated one.
pub(crate) fn append_member(methods: &mut Vec<String>, raw: &str, generated: String) {
    if !raw.trim().is_empty() {
        methods.push(dedent_preserved_member(raw));
    } else if !generated.is_empty() {
        methods.push(generated);
    }
}

pub(crate) fn dedent_preserved_member(raw: &str) -> String {
    let mut lines: Vec<String> = raw.trim().split('\n').map(str::to_owned).collect();
    if lines.len() < 2 {
        return lines.join("\n");
    }
    let mut indent: Option<usize> = None;
    for line in &lines[1..] {
        if line.trim().is_empty() {
            continue;
        }
        let width = line
            .bytes()
            .take_while(|byte| *byte == b' ' || *byte == b'\t')
            .count();
        indent = Some(indent.map_or(width, |current| current.min(width)));
    }
    let Some(indent) = indent.filter(|indent| *indent > 0) else {
        return lines.join("\n");
    };
    for line in lines.iter_mut().skip(1) {
        if line.trim().is_empty() {
            line.clear();
            continue;
        }
        *line = line[indent..].to_owned();
    }
    lines.join("\n")
}

fn render_parent_definition_method(class: &str, imports: &ImportTable) -> String {
    if class.is_empty() {
        return String::new();
    }
    format!(
        "protected function getParentDefinitionClass(): ?string\n{{\n    return {}::class;\n}}",
        imports.reference(class)
    )
}

fn render_boolean_definition_method(name: &str, value: Option<bool>) -> String {
    match value {
        Some(value) => format!("public function {name}(): bool\n{{\n    return {value};\n}}"),
        None => String::new(),
    }
}

fn render_default_fields_method(
    behavior: &DefinitionBehaviorSpec,
    imports: &ImportTable,
) -> Result<String> {
    if !behavior.override_default_fields || !behavior.default_fields_method_raw.trim().is_empty() {
        return Ok(String::new());
    }
    render_definition_fields_method("defaultFields", &behavior.default_fields, imports)
}

fn render_base_fields_method(
    behavior: &DefinitionBehaviorSpec,
    imports: &ImportTable,
) -> Result<String> {
    if !behavior.override_base_fields || !behavior.base_fields_method_raw.trim().is_empty() {
        return Ok(String::new());
    }
    render_definition_fields_method("getBaseFields", &behavior.base_fields, imports)
}

fn render_definition_fields_method(
    name: &str,
    fields: &[FieldSpec],
    imports: &ImportTable,
) -> Result<String> {
    let mut builder = format!("protected function {name}(): array\n{{\n    return [\n");
    for field in fields {
        for expression in render_definition_field(field, imports)? {
            for line in expression.trim().split('\n') {
                builder.push_str("        ");
                builder.push_str(line);
                builder.push('\n');
            }
        }
    }
    builder.push_str("    ];\n}");
    Ok(builder)
}

fn render_restrict_delete_meta_method(properties: &[String]) -> String {
    if properties.is_empty() {
        return String::new();
    }
    let quoted: Vec<String> = properties
        .iter()
        .map(|property| quote_php(property))
        .collect();
    format!(
        "public function getRestrictDeleteMetaFields(): FieldCollection\n{{\n    return $this->getFields()->filter(\n        static fn (Field $field): bool => \\in_array($field->getPropertyName(), [{}], true)\n    );\n}}",
        quoted.join(", ")
    )
}
