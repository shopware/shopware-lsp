//! Typed EntityExtension `modifyFields` flag changes.

use std::collections::{BTreeSet, HashSet};

use crate::model::*;
use crate::render::{
    ImportTable, collect_class_imports, render_api_aware, render_behavior_flags,
    render_inherited_flag, render_metadata_flags, render_search_ranking,
    render_write_protected_flag,
};
use crate::util::*;
use crate::validate_spec::{
    PROPERTY_PATTERN, STORAGE_PATTERN, SpecValidator, valid_inline_php_expression,
};

const FLAG_CLASS_NAMESPACE: &str = "Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\Flag\\";

/// Every flag kind with its DAL short class name, sorted by kind.
pub(crate) const FIELD_FLAG_CLASSES: &[(&str, &str)] = &[
    (FLAG_ALLOW_EMPTY_STRING, "AllowEmptyString"),
    (FLAG_ALLOW_HTML, "AllowHtml"),
    (FLAG_API_AWARE, "ApiAware"),
    (FLAG_API_CRITERIA_AWARE, "ApiCriteriaAware"),
    (FLAG_AS_ARRAY, "AsArray"),
    (FLAG_CASCADE_DELETE, "CascadeDelete"),
    (FLAG_CHOICE, "Choice"),
    (FLAG_COMPUTED, "Computed"),
    (FLAG_DEPRECATED, "Deprecated"),
    (FLAG_DO_NOT_USE_CONTEXT, "DoNotUseContext"),
    (FLAG_EXTENSION, "Extension"),
    (FLAG_IGNORE_IN_OPEN_API_SCHEMA, "IgnoreInOpenapiSchema"),
    (
        FLAG_IGNORE_IN_UNUSED_MEDIA_SEARCH,
        "IgnoreInUnusedMediaSearch",
    ),
    (FLAG_IMMUTABLE, "Immutable"),
    (FLAG_INHERITED, "Inherited"),
    (FLAG_NO_CONSTRAINT, "NoConstraint"),
    (FLAG_PRIMARY_KEY, "PrimaryKey"),
    (FLAG_REQUIRED, "Required"),
    (FLAG_RESTRICT_DELETE, "RestrictDelete"),
    (FLAG_REVERSE_INHERITED, "ReverseInherited"),
    (FLAG_RULE_AREAS, "RuleAreas"),
    (FLAG_RUNTIME, "Runtime"),
    (FLAG_SEARCH_RANKING, "SearchRanking"),
    (FLAG_SET_NULL_ON_DELETE, "SetNullOnDelete"),
    (FLAG_SINCE, "Since"),
    (FLAG_WRITE_PROTECTED, "WriteProtected"),
];

/// The fully-qualified DAL flag class of `kind`.
pub(crate) fn field_flag_class(kind: &str) -> Option<String> {
    FIELD_FLAG_CLASSES
        .iter()
        .find(|(candidate, _)| *candidate == kind)
        .map(|(_, short)| format!("{FLAG_CLASS_NAMESPACE}{short}"))
}

#[cfg(test)]
pub(crate) fn field_flag_kinds() -> Vec<FieldFlagKind> {
    FIELD_FLAG_CLASSES
        .iter()
        .map(|(kind, _)| (*kind).to_owned())
        .collect()
}

pub(crate) fn field_flag_kind_for_class(class_name: &str) -> Option<FieldFlagKind> {
    let short = short_class(class_name);
    FIELD_FLAG_CLASSES
        .iter()
        .find(|(_, candidate)| *candidate == short)
        .map(|(kind, _)| (*kind).to_owned())
}

pub(crate) fn validate_field_modifications(v: &mut SpecValidator) {
    let modifications = v.spec.field_modifications.clone();
    let raw = v.spec.modify_fields_method_raw.trim().to_owned();
    let configured = !modifications.is_empty() || !raw.is_empty();
    if configured && v.spec.definition_kind != DEFINITION_EXTENSION {
        v.add(
            "entity.extension.modifyFields.owner.unsupported",
            "Only EntityExtension can modify existing target fields",
            "",
        );
        return;
    }
    if !raw.is_empty() && !modifications.is_empty() {
        v.add(
            "entity.extension.modifyFields.raw.conflict",
            "A preserved custom modifyFields method cannot be combined with typed field modifications",
            "",
        );
        return;
    }
    let mut seen_ids = HashSet::new();
    let mut seen_properties = HashSet::new();
    for (index, modification) in modifications.iter().enumerate() {
        let id = if modification.id.is_empty() {
            format!("modification-{index}")
        } else {
            modification.id.clone()
        };
        if !seen_ids.insert(id.clone()) {
            v.add(
                "entity.extension.modifyFields.id.duplicate",
                format!("Field modification ID {:?} is duplicated", id),
                &id,
            );
        }
        if !PROPERTY_PATTERN.is_match(&modification.property_name) {
            v.add(
                "entity.extension.modifyFields.property.invalid",
                format!("Invalid target property {:?}", modification.property_name),
                &id,
            );
        } else if seen_properties.contains(&modification.property_name) {
            v.add(
                "entity.extension.modifyFields.property.duplicate",
                format!(
                    "Target property {:?} is modified more than once",
                    modification.property_name
                ),
                &id,
            );
        }
        seen_properties.insert(modification.property_name.clone());
        if modification.add_flags.is_empty() && modification.remove_flags.is_empty() {
            v.add(
                "entity.extension.modifyFields.empty",
                "A field modification must add or remove at least one flag",
                &id,
            );
        }
        let mut added = HashSet::new();
        for flag in &modification.add_flags {
            if !added.insert(flag.kind.clone()) {
                v.add(
                    "entity.extension.modifyFields.flag.duplicate",
                    format!("Flag {:?} is added more than once", flag.kind),
                    &id,
                );
            }
            validate_field_modification_flag(v, flag, &id);
        }
        let mut removed = HashSet::new();
        for kind in &modification.remove_flags {
            if field_flag_class(kind).is_none() {
                v.add(
                    "entity.extension.modifyFields.flag.invalid",
                    format!("Unsupported DAL flag {:?}", kind),
                    &id,
                );
            }
            if !removed.insert(kind.clone()) {
                v.add(
                    "entity.extension.modifyFields.flag.duplicate",
                    format!("Flag {:?} is removed more than once", kind),
                    &id,
                );
            }
            if added.contains(kind) {
                v.add(
                    "entity.extension.modifyFields.flag.conflict",
                    format!("Flag {:?} cannot be added and removed together", kind),
                    &id,
                );
            }
        }
    }
}

fn validate_field_modification_flag(v: &mut SpecValidator, flag: &FieldFlagSpec, field_id: &str) {
    if field_flag_class(&flag.kind).is_none() {
        v.add(
            "entity.extension.modifyFields.flag.invalid",
            format!("Unsupported DAL flag {:?}", flag.kind),
            field_id,
        );
        return;
    }
    let arguments = unexpected_field_flag_arguments(flag);
    if !arguments.is_empty() {
        v.add(
            "entity.extension.modifyFields.flag.arguments.invalid",
            format!(
                "Flag {:?} does not accept {}",
                flag.kind,
                arguments.join(", ")
            ),
            field_id,
        );
    }
    match flag.kind.as_str() {
        FLAG_API_AWARE => v.validate_api_aware(
            true,
            &flag.api_sources,
            "entity.extension.modifyFields.apiAware.invalid",
            field_id,
        ),
        FLAG_SEARCH_RANKING => {
            if flag.search_ranking < 0.0
                || flag.search_ranking.is_nan()
                || flag.search_ranking.is_infinite()
            {
                v.add(
                    "entity.extension.modifyFields.searchRanking.invalid",
                    "Search ranking must be a finite non-negative number",
                    field_id,
                );
            }
        }
        FLAG_RUNTIME => {
            let behavior = FieldBehavior {
                runtime: true,
                runtime_dependencies: flag.runtime_dependencies.clone(),
                runtime_dependencies_expression: flag.runtime_dependencies_expression.clone(),
                ..FieldBehavior::default()
            };
            let field = FieldSpec {
                id: field_id.to_owned(),
                ..FieldSpec::default()
            };
            v.validate_behavior(&field, "modified field", Some(&behavior));
        }
        FLAG_INHERITED => {
            if !flag.inherited_foreign_key.is_empty()
                && !STORAGE_PATTERN.is_match(&flag.inherited_foreign_key)
            {
                v.add(
                    "entity.extension.modifyFields.inherited.invalid",
                    "Inherited foreign-key override must be a valid storage name",
                    field_id,
                );
            }
        }
        FLAG_REVERSE_INHERITED => {
            if !PROPERTY_PATTERN.is_match(&flag.reverse_property) {
                v.add(
                    "entity.extension.modifyFields.reverseInherited.invalid",
                    "ReverseInherited requires a valid target property",
                    field_id,
                );
            }
        }
        FLAG_WRITE_PROTECTED => v.validate_write_protected_scopes(
            true,
            &flag.write_scopes,
            "entity.extension.modifyFields.writeProtected.invalid",
            field_id,
        ),
        FLAG_SINCE => {
            if flag.since.trim().is_empty() {
                v.add(
                    "entity.extension.modifyFields.since.invalid",
                    "Since requires a non-empty version",
                    field_id,
                );
            }
        }
        FLAG_DEPRECATED => {
            if flag.deprecated.as_ref().is_none_or(|deprecated| {
                deprecated.deprecated_since.trim().is_empty()
                    || deprecated.will_be_removed_in.trim().is_empty()
            }) {
                v.add(
                    "entity.extension.modifyFields.deprecated.invalid",
                    "Deprecated requires deprecation and removal versions",
                    field_id,
                );
            }
        }
        FLAG_RULE_AREAS => {
            if flag.rule_areas.is_empty() {
                v.add(
                    "entity.extension.modifyFields.ruleAreas.empty",
                    "RuleAreas requires at least one area",
                    field_id,
                );
            }
            if flag.rule_areas.iter().any(|area| area.trim().is_empty()) {
                v.add(
                    "entity.extension.modifyFields.ruleAreas.invalid",
                    "RuleAreas cannot contain empty values",
                    field_id,
                );
            }
        }
        FLAG_CHOICE => {
            let Some(choice) = flag
                .choice
                .as_ref()
                .filter(|choice| !choice.values.is_empty())
            else {
                v.add(
                    "entity.extension.modifyFields.choice.empty",
                    "Choice requires at least one value",
                    field_id,
                );
                return;
            };
            if choice
                .values
                .iter()
                .any(|value| !valid_inline_php_expression(value))
            {
                v.add(
                    "entity.extension.modifyFields.choice.invalid",
                    "Choice values must be safe PHP scalar or constant expressions",
                    field_id,
                );
            }
        }
        _ => {}
    }
}

fn unexpected_field_flag_arguments(flag: &FieldFlagSpec) -> Vec<&'static str> {
    let kind = flag.kind.as_str();
    let checks = [
        (
            kind == FLAG_API_AWARE,
            "apiSources",
            !flag.api_sources.is_empty(),
        ),
        (
            kind == FLAG_SEARCH_RANKING,
            "searchRanking",
            flag.search_ranking != 0.0,
        ),
        (
            kind == FLAG_SEARCH_RANKING,
            "searchTokenize",
            flag.search_tokenize.is_some(),
        ),
        (
            kind == FLAG_RUNTIME,
            "runtimeDependencies",
            !flag.runtime_dependencies.is_empty(),
        ),
        (
            kind == FLAG_RUNTIME,
            "runtimeDependenciesExpression",
            !flag.runtime_dependencies_expression.trim().is_empty(),
        ),
        (
            kind == FLAG_INHERITED,
            "inheritedForeignKey",
            !flag.inherited_foreign_key.is_empty(),
        ),
        (
            kind == FLAG_REVERSE_INHERITED,
            "reverseProperty",
            !flag.reverse_property.is_empty(),
        ),
        (
            kind == FLAG_WRITE_PROTECTED,
            "writeScopes",
            !flag.write_scopes.is_empty(),
        ),
        (
            kind == FLAG_ALLOW_HTML,
            "allowHtmlSanitized",
            flag.allow_html_sanitized.is_some(),
        ),
        (
            kind == FLAG_CASCADE_DELETE,
            "cloneRelevant",
            flag.clone_relevant.is_some(),
        ),
        (
            kind == FLAG_SET_NULL_ON_DELETE,
            "enforcedByConstraint",
            flag.enforced_by_constraint.is_some(),
        ),
        (kind == FLAG_SINCE, "since", !flag.since.is_empty()),
        (
            kind == FLAG_DEPRECATED,
            "deprecated",
            flag.deprecated.is_some(),
        ),
        (
            kind == FLAG_RULE_AREAS,
            "ruleAreas",
            !flag.rule_areas.is_empty(),
        ),
        (kind == FLAG_CHOICE, "choice", flag.choice.is_some()),
    ];
    checks
        .into_iter()
        .filter(|(allowed, _, present)| *present && !*allowed)
        .map(|(_, name, _)| name)
        .collect()
}

fn render_field_modification_flag(flag: &FieldFlagSpec, imports: &ImportTable) -> String {
    let first = |flags: Vec<String>| flags.into_iter().next().unwrap_or_default();
    match flag.kind.as_str() {
        FLAG_API_AWARE => return render_api_aware(imports, &flag.api_sources),
        FLAG_SEARCH_RANKING => {
            return render_search_ranking(flag.search_ranking, flag.search_tokenize);
        }
        FLAG_CASCADE_DELETE => {
            if let Some(value) = flag.clone_relevant {
                return format!("new CascadeDelete({value})");
            }
        }
        FLAG_SET_NULL_ON_DELETE => {
            if let Some(value) = flag.enforced_by_constraint {
                return format!("new SetNullOnDelete({value})");
            }
        }
        FLAG_RUNTIME => {
            return first(render_behavior_flags(Some(&FieldBehavior {
                runtime: true,
                runtime_dependencies: flag.runtime_dependencies.clone(),
                runtime_dependencies_expression: flag.runtime_dependencies_expression.clone(),
                ..FieldBehavior::default()
            })));
        }
        FLAG_INHERITED => return render_inherited_flag(&flag.inherited_foreign_key),
        FLAG_REVERSE_INHERITED => {
            return format!(
                "new ReverseInherited({})",
                quote_php(&flag.reverse_property)
            );
        }
        FLAG_WRITE_PROTECTED => return render_write_protected_flag(&flag.write_scopes),
        FLAG_ALLOW_HTML => {
            return first(render_metadata_flags(Some(&FieldMetadata {
                allow_html: Some(flag.allow_html_sanitized.unwrap_or(true)),
                ..FieldMetadata::default()
            })));
        }
        FLAG_SINCE => {
            return first(render_metadata_flags(Some(&FieldMetadata {
                since: flag.since.clone(),
                ..FieldMetadata::default()
            })));
        }
        FLAG_DEPRECATED => {
            return first(render_metadata_flags(Some(&FieldMetadata {
                deprecated: flag.deprecated.clone(),
                ..FieldMetadata::default()
            })));
        }
        FLAG_RULE_AREAS => {
            return first(render_metadata_flags(Some(&FieldMetadata {
                rule_areas: flag.rule_areas.clone(),
                ..FieldMetadata::default()
            })));
        }
        FLAG_CHOICE => {
            return first(render_metadata_flags(Some(&FieldMetadata {
                choice: flag.choice.clone(),
                ..FieldMetadata::default()
            })));
        }
        _ => {}
    }
    format!(
        "new {}()",
        imports.reference(&field_flag_class(&flag.kind).unwrap_or_default())
    )
}

pub(crate) fn render_field_modifications(spec: &EntitySpec, imports: &ImportTable) -> String {
    if !spec.modify_fields_method_raw.trim().is_empty() {
        return spec.modify_fields_method_raw.trim().to_owned();
    }
    if spec.field_modifications.is_empty() {
        return String::new();
    }
    let mut builder =
        String::from("public function modifyFields(FieldCollection $collection): void\n{\n");
    for modification in &spec.field_modifications {
        let receiver = format!(
            "$collection->get({})?->",
            quote_php(&modification.property_name)
        );
        if !modification.add_flags.is_empty() {
            let flags: Vec<String> = modification
                .add_flags
                .iter()
                .map(|flag| render_field_modification_flag(flag, imports))
                .collect();
            builder.push_str(&format!("    {receiver}addFlags({});\n", flags.join(", ")));
        }
        for kind in &modification.remove_flags {
            builder.push_str(&format!(
                "    {receiver}removeFlag({}::class);\n",
                imports.reference(&field_flag_class(kind).unwrap_or_default())
            ));
        }
    }
    builder.push('}');
    builder
}

pub(crate) fn collect_field_modification_imports(
    need: &mut BTreeSet<String>,
    modifications: &[FieldModificationSpec],
) {
    for modification in modifications {
        for flag in &modification.add_flags {
            if let Some(class) = field_flag_class(&flag.kind) {
                need.insert(class);
            }
            collect_class_imports(need, &flag.api_sources);
        }
        for kind in &modification.remove_flags {
            if let Some(class) = field_flag_class(kind) {
                need.insert(class);
            }
        }
    }
}
