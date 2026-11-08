//! Spec validation (port of Go `validate_spec.go` and `validate_migration.go`).

use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

use regex::Regex;
use serde::{Deserialize, Serialize};

use crate::definition_behavior::validate_definition_behavior;
use crate::definition_metadata::validate_definition_metadata;
use crate::diff::SchemaDiff;
use crate::field_modification::validate_field_modifications;
use crate::model::*;
use crate::render::{complete_spec, has_version_field};
use crate::snapshot::Decision;
use crate::specialized_fields::{
    CONSTRUCTOR_FIXED, CONSTRUCTOR_STORAGE_PROPERTY, specialized_field_supported,
};
use crate::util::*;
use crate::version::{bulk_entity_extension_supported, enum_field_supported};

fn pattern(source: &str) -> Regex {
    Regex::new(source).expect("valid pattern")
}

pub(crate) static CLASS_NAME_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| pattern(r"^[A-Za-z_\x{80}-\x{ff}][A-Za-z0-9_\x{80}-\x{ff}]*$"));
pub(crate) static ENTITY_NAME_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| pattern(r"^_*[a-z][a-z0-9_]*$"));
pub(crate) static PROPERTY_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| pattern(r"^[A-Za-z_][A-Za-z0-9_]*$"));
pub(crate) static STORAGE_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| pattern(r"^_*[a-z][a-z0-9_]*$"));
static ASSOCIATION_STORAGE_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| pattern(r"^_*[a-z][a-z0-9_]*(?:\._*[a-z][a-z0-9_]*)*$"));
static INDEX_NAME_PATTERN: LazyLock<Regex> = LazyLock::new(|| pattern(r"^[A-Za-z0-9_.-]+$"));
pub(crate) static NAMESPACE_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    pattern(
        r"^\\?[A-Za-z_\x{80}-\x{ff}][A-Za-z0-9_\x{80}-\x{ff}]*(?:\\[A-Za-z_\x{80}-\x{ff}][A-Za-z0-9_\x{80}-\x{ff}]*)*$",
    )
});

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct ValidationIssue {
    pub code: String,
    pub message: String,
    #[serde(rename = "fieldId", skip_serializing_if = "String::is_empty")]
    pub field_id: String,
    pub severity: String,
}

struct ExistingColumnReference {
    name: String,
    field_id: String,
    allow_missing: bool,
}

pub(crate) struct SpecValidator {
    pub(crate) spec: EntitySpec,
    pub(crate) issues: Vec<ValidationIssue>,
    properties: HashMap<String, FieldSpec>,
    storage_names: HashMap<String, FieldSpec>,
    extended_storage_names: HashMap<String, RelationTargetField>,
    translated_storage_names: HashMap<String, String>,
    existing_column_references: Vec<ExistingColumnReference>,
    reference_version_targets: HashMap<String, Vec<FieldSpec>>,
    /// Go iterates a map here; insertion order keeps issue order stable.
    required_relation_targets: Vec<(String, String)>,
    id_count: usize,
    auto_increment_count: usize,
    primary_count: usize,
    hierarchy_count: usize,
}

impl SpecValidator {
    fn new(spec: EntitySpec) -> Self {
        SpecValidator {
            spec,
            issues: Vec::new(),
            properties: HashMap::new(),
            storage_names: HashMap::new(),
            extended_storage_names: HashMap::new(),
            translated_storage_names: HashMap::new(),
            existing_column_references: Vec::new(),
            reference_version_targets: HashMap::new(),
            required_relation_targets: Vec::new(),
            id_count: 0,
            auto_increment_count: 0,
            primary_count: 0,
            hierarchy_count: 0,
        }
    }

    pub(crate) fn add(&mut self, code: &str, message: impl Into<String>, field_id: &str) {
        self.issues.push(ValidationIssue {
            code: code.to_owned(),
            message: message.into(),
            field_id: field_id.to_owned(),
            severity: "error".to_owned(),
        });
    }
}

pub fn validate_spec(spec: &EntitySpec) -> Vec<ValidationIssue> {
    validate_completed_spec(complete_spec(spec.clone()))
}

fn validate_completed_spec(spec: EntitySpec) -> Vec<ValidationIssue> {
    let mut validator = SpecValidator::new(spec);
    if validator.spec.definition_kind == DEFINITION_EXTENSION {
        for field in &validator.spec.extended_fields {
            if STORAGE_PATTERN.is_match(&field.storage_name) {
                validator
                    .extended_storage_names
                    .insert(field.storage_name.clone(), field.clone());
            }
        }
    }
    validator.validate_identity();
    if validator.spec.definition_kind == DEFINITION_BULK_EXTENSION {
        validate_bulk_extension_spec(&mut validator);
        return validator.issues;
    }
    let mut fields = Vec::new();
    if let Some(behavior) = &validator.spec.definition_behavior {
        if behavior.override_default_fields {
            fields.extend(behavior.default_fields.iter().cloned());
        }
        if behavior.override_base_fields {
            fields.extend(behavior.base_fields.iter().cloned());
        }
    }
    fields.extend(validator.spec.fields.iter().cloned());
    for field in &fields {
        validator.validate_field(field);
    }
    validator.validate_field_set();
    validator.validate_indexes();
    validate_field_modifications(&mut validator);
    validator.issues
}

impl SpecValidator {
    fn validate_identity(&mut self) {
        let spec = self.spec.clone();
        let kind = spec.definition_kind.as_str();
        if kind != DEFINITION_ENTITY
            && kind != DEFINITION_MAPPING
            && kind != DEFINITION_EXTENSION
            && kind != DEFINITION_BULK_EXTENSION
        {
            self.add(
                "entity.definitionKind.invalid",
                "Definition kind must be entity, mapping, extension, or bulk-extension",
                "",
            );
        }
        if kind == DEFINITION_BULK_EXTENSION
            && !bulk_entity_extension_supported(&spec.shopware_version)
        {
            self.add(
                "entity.bulkExtension.version.unsupported",
                "BulkEntityExtension requires Shopware 6.6.10 or newer throughout the configured compatibility range",
                "",
            );
        }
        if !spec.collect_method_raw.trim().is_empty() && kind != DEFINITION_BULK_EXTENSION {
            self.add(
                "entity.bulkExtension.collectRaw.owner.unsupported",
                "A preserved collect method belongs only to BulkEntityExtension",
                "",
            );
        }
        if !spec.collect_method_raw.trim().is_empty() && spec.mode != "edit" {
            self.add(
                "entity.bulkExtension.collectRaw.creation.unsupported",
                "Custom collect methods can only be preserved from an existing BulkEntityExtension",
                "",
            );
        }
        if !CLASS_NAME_PATTERN.is_match(&spec.class_name) {
            self.add(
                "entity.class.invalid",
                "Enter a valid PHP base class name",
                "",
            );
        }
        if kind != DEFINITION_BULK_EXTENSION && !ENTITY_NAME_PATTERN.is_match(&spec.entity_name) {
            self.add(
                "entity.name.invalid",
                "Entity name must use lowercase snake_case",
                "",
            );
        } else if kind != DEFINITION_BULK_EXTENSION && spec.entity_name.len() > 64 {
            self.add(
                "entity.name.tooLong",
                "Entity/table names cannot exceed 64 bytes",
                "",
            );
        }
        if trim_class(&spec.namespace).is_empty() {
            self.add(
                "entity.namespace.missing",
                "A PSR-4 namespace is required",
                "",
            );
        } else if !NAMESPACE_PATTERN.is_match(&spec.namespace) {
            self.add(
                "entity.namespace.invalid",
                "Enter a valid PHP namespace",
                "",
            );
        }
        if kind != DEFINITION_ENTITY
            && spec
                .translation
                .as_ref()
                .is_some_and(|translation| translation.enabled)
        {
            self.add(
                "entity.translation.owner.unsupported",
                "Only entity definitions can own translation bundles",
                "",
            );
        }
        validate_definition_behavior(self, spec.definition_behavior.as_ref(), kind, false);
        validate_definition_metadata(self, spec.definition_metadata.as_ref(), kind, false);
        if spec.inheritance_aware && kind != DEFINITION_ENTITY {
            self.add(
                "entity.inheritance.owner.unsupported",
                "Only entity definitions can be inheritance-aware",
                "",
            );
        }
        let protections_configured = spec.read_protected
            || spec.write_protected
            || !spec.preserved_protections.is_empty()
            || !spec.protection_method_raw.trim().is_empty();
        if protections_configured && kind != DEFINITION_ENTITY && kind != DEFINITION_EXTENSION {
            self.add(
                "entity.protection.owner.unsupported",
                "Only entity definitions and entity extensions can declare entity protections",
                "",
            );
        }
        if !spec.protection_method_raw.trim().is_empty()
            && (spec.read_protected
                || spec.write_protected
                || !spec.preserved_protections.is_empty())
        {
            self.add(
                "entity.protection.raw.conflict",
                "A preserved custom protection method cannot be combined with editable protections",
                "",
            );
        }
        self.validate_write_protected_scopes(
            spec.read_protected,
            &spec.read_protection_scopes,
            "entity.protection.read.invalid",
            "",
        );
        self.validate_write_protected_scopes(
            spec.write_protected,
            &spec.write_protection_scopes,
            "entity.protection.write.invalid",
            "",
        );
        if kind == DEFINITION_EXTENSION
            && (spec.extended_definition_class.is_empty()
                || !NAMESPACE_PATTERN.is_match(&spec.extended_definition_class))
        {
            self.add(
                "entity.extension.target.missing",
                "Select an indexed entity definition to extend",
                "",
            );
        }
        if let Some(translation) = spec
            .translation
            .as_ref()
            .filter(|translation| translation.enabled)
        {
            self.validate_translation_identity(&spec, translation);
        }
        if spec.mode == "edit"
            || !CLASS_NAME_PATTERN.is_match(&spec.class_name)
            || !NAMESPACE_PATTERN.is_match(&spec.namespace)
        {
            return;
        }
        let namespace = trim_backslash(&spec.namespace);
        let definition_suffix = match kind {
            DEFINITION_EXTENSION => "Extension",
            DEFINITION_BULK_EXTENSION => "BulkEntityExtension",
            _ => "Definition",
        };
        let mut identities = vec![(
            spec.definition_class.as_str(),
            definition_suffix,
            "entity.definition.identity",
            "Generated class must match the selected namespace and base class",
        )];
        if kind == DEFINITION_ENTITY {
            identities.push((
                spec.entity_class.as_str(),
                "Entity",
                "entity.class.identity",
                "Entity class must match the selected namespace and base class",
            ));
            identities.push((
                spec.collection_class.as_str(),
                "Collection",
                "entity.collection.identity",
                "Collection class must match the selected namespace and base class",
            ));
        }
        for (actual, suffix, code, message) in identities {
            if !actual.is_empty()
                && trim_backslash(actual) != format!("{namespace}\\{}{suffix}", spec.class_name)
            {
                self.add(code, message, "");
            }
        }
    }

    fn validate_translation_identity(&mut self, spec: &EntitySpec, translation: &TranslationSpec) {
        validate_definition_behavior(
            self,
            translation.definition_behavior.as_ref(),
            DEFINITION_ENTITY,
            true,
        );
        validate_translation_behavior_fields(self, translation.definition_behavior.as_ref());
        validate_definition_metadata(
            self,
            translation.definition_metadata.as_ref(),
            DEFINITION_ENTITY,
            true,
        );
        if !ENTITY_NAME_PATTERN.is_match(&translation.entity_name)
            || translation.entity_name.len() > 64
        {
            self.add(
                "entity.translation.name.invalid",
                "Translation entity name must use lowercase snake_case and fit within 64 bytes",
                "",
            );
        }
        if !NAMESPACE_PATTERN.is_match(&translation.definition_class)
            || !NAMESPACE_PATTERN.is_match(&translation.entity_class)
            || !NAMESPACE_PATTERN.is_match(&translation.collection_class)
        {
            self.add(
                "entity.translation.identity.invalid",
                "Translation definition, entity, and collection classes must be valid fully-qualified PHP classes",
                "",
            );
        }
        if !STORAGE_PATTERN.is_match(&translation.parent_storage_name)
            || translation.parent_storage_name.len() > 64
        {
            self.add(
                "entity.translation.parentStorage.invalid",
                "Translation parent storage name must use lowercase snake_case and fit within 64 bytes",
                "",
            );
        } else if translation.parent_storage_name != format!("{}_id", spec.entity_name) {
            self.add(
                "entity.translation.parentStorage.mismatch",
                "Translation parent storage must match the DAL-generated <entity_name>_id field",
                "",
            );
        }
        if !STORAGE_PATTERN.is_match(&translation.association_local_field) {
            self.add(
                "entity.translation.localField.invalid",
                "Translation association local field must use lowercase snake_case",
                "",
            );
        }
        if !PROPERTY_PATTERN.is_match(&translation.parent_property_name)
            || !PROPERTY_PATTERN.is_match(&translation.association_property)
        {
            self.add(
                "entity.translation.property.invalid",
                "Translation parent and association properties must be valid PHP property names",
                "",
            );
        }
        if trim_backslash(&translation.parent_definition_class)
            != trim_backslash(&spec.definition_class)
        {
            self.add(
                "entity.translation.parentDefinition.mismatch",
                "Translation definition must reference the owning parent definition",
                "",
            );
        }
        if !translation.association_inherited_fk.is_empty()
            && (!translation.association_inherited
                || !STORAGE_PATTERN.is_match(&translation.association_inherited_fk))
        {
            self.add(
                "entity.inheritance.translationAssociationForeignKey.invalid",
                "Translations-association inherited foreign-key overrides require a valid storage name",
                "",
            );
        }
        if !translation.reverse_inherited_property.is_empty()
            && !PROPERTY_PATTERN.is_match(&translation.reverse_inherited_property)
        {
            self.add(
                "entity.inheritance.translationAssociationReverse.invalid",
                "Translations-association reverse inheritance requires a valid target property",
                "",
            );
        }
        self.validate_write_protected_scopes(
            translation.association_write_protected,
            &translation.association_write_scopes,
            "entity.translation.association.writeProtection.invalid",
            "",
        );
        self.validate_behavior(
            &FieldSpec::default(),
            "translation association",
            translation.association_behavior.as_ref(),
        );
        self.validate_metadata(
            &FieldSpec::default(),
            translation.association_metadata.as_ref(),
        );
        self.validate_api_aware(
            translation.association_api_aware,
            &translation.association_api_aware_sources,
            "entity.translation.association.apiAware.invalid",
            "",
        );
    }
}

fn validate_translation_behavior_fields(
    parent: &mut SpecValidator,
    behavior: Option<&DefinitionBehaviorSpec>,
) {
    let Some(behavior) = behavior else {
        return;
    };
    let spec = EntitySpec {
        mode: parent.spec.mode.clone(),
        definition_kind: DEFINITION_ENTITY.to_owned(),
        entity_name: parent
            .spec
            .translation
            .as_ref()
            .map(|translation| translation.entity_name.clone())
            .unwrap_or_default(),
        ..EntitySpec::default()
    };
    let mut validator = SpecValidator::new(spec);
    if behavior.override_default_fields {
        for field in &behavior.default_fields {
            validator.validate_field(field);
        }
    }
    if behavior.override_base_fields {
        for field in &behavior.base_fields {
            validator.validate_field(field);
        }
    }
    parent.issues.append(&mut validator.issues);
}

fn validate_bulk_extension_spec(validator: &mut SpecValidator) {
    let spec = validator.spec.clone();
    if !spec.collect_method_raw.trim().is_empty() {
        if !spec.bulk_extensions.is_empty()
            || !spec.fields.is_empty()
            || !spec.indexes.is_empty()
            || !spec.extended_definition_class.is_empty()
            || !spec.extended_fields.is_empty()
        {
            validator.add(
                "entity.bulkExtension.collectRaw.conflict",
                "A preserved custom collect method cannot be combined with editable bulk targets, fields, or indexes",
                "",
            );
        }
        return;
    }
    if spec.bulk_extensions.is_empty() {
        validator.add(
            "entity.bulkExtension.targets.empty",
            "BulkEntityExtension requires at least one indexed target",
            "",
        );
    }
    if !spec.fields.is_empty()
        || !spec.indexes.is_empty()
        || !spec.extended_definition_class.is_empty()
        || !spec.extended_fields.is_empty()
    {
        validator.add(
            "entity.bulkExtension.singleTarget.conflict",
            "BulkEntityExtension fields and indexes belong inside bulkExtensions targets",
            "",
        );
    }
    let mut seen_ids = HashSet::new();
    let mut seen_entities = HashSet::new();
    let mut seen_definitions = HashSet::new();
    for (index, target) in spec.bulk_extensions.iter().enumerate() {
        let id = if target.id.is_empty() {
            format!("bulk-target-{index}")
        } else {
            target.id.clone()
        };
        if !seen_ids.insert(id.clone()) {
            validator.add(
                "entity.bulkExtension.target.id.duplicate",
                format!("Bulk target ID {} is duplicated", go_quote(&id)),
                &id,
            );
        }
        if !seen_entities.insert(go_to_lower(&target.entity_name)) {
            validator.add(
                "entity.bulkExtension.target.entity.duplicate",
                format!(
                    "Entity {} is extended more than once",
                    go_quote(&target.entity_name)
                ),
                &id,
            );
        }
        let definition_key = go_to_lower(trim_class(&target.extended_definition_class));
        if !definition_key.is_empty() && !seen_definitions.insert(definition_key) {
            validator.add(
                "entity.bulkExtension.target.definition.duplicate",
                format!(
                    "Definition {} is extended more than once",
                    go_quote(&target.extended_definition_class)
                ),
                &id,
            );
        }
        let mut target_spec = bulk_target_entity_spec(&spec, target);
        if target_spec.extended_definition_class.is_empty() {
            target_spec.extended_definition_class = spec.definition_class.clone();
        }
        for mut issue in validate_spec(&target_spec) {
            if issue.field_id.is_empty() {
                issue.field_id = id.clone();
            }
            issue.message = format!(
                "Bulk target {}: {}",
                default_string(&target.entity_name, &id),
                issue.message
            );
            validator.issues.push(issue);
        }
    }
}

impl SpecValidator {
    fn validate_field(&mut self, field: &FieldSpec) {
        if field.kind == FIELD_LOCKED {
            return;
        }
        if !supported_field_kind(&field.kind) {
            self.add(
                "entity.field.kind.unsupported",
                format!("Unsupported field kind {}", go_quote(&field.kind)),
                &field.id,
            );
            return;
        }
        self.validate_implementation(field);
        if field.kind == FIELD_ID {
            self.id_count += 1;
        }
        if field.kind == FIELD_AUTO_INCREMENT {
            self.auto_increment_count += 1;
        }
        if field.kind == FIELD_HIERARCHY {
            self.hierarchy_count += 1;
        }
        if field.primary || field.kind == FIELD_ID || field.kind == FIELD_VERSION {
            self.primary_count += 1;
        }
        self.validate_field_settings(field);
        self.validate_property(field);
        if field.translated {
            self.validate_translated_field(field);
            return;
        }
        if field.kind == FIELD_HIERARCHY {
            self.validate_hierarchy(field);
            return;
        }
        if field.uses_existing_column
            && field.kind != FIELD_ONE_TO_ONE
            && field.kind != FIELD_MANY_TO_ONE
        {
            self.add(
                "entity.relation.existingColumn.unsupported",
                "Only to-one associations can reuse an existing local column",
                &field.id,
            );
        }
        self.validate_reference_version(field);
        if is_foreign_key_kind(&field.kind) {
            if self.validate_foreign_key(field) {
                return;
            }
        } else if field.kind == FIELD_ONE_TO_MANY {
            self.validate_one_to_many(field);
            return;
        } else if field.kind == FIELD_MANY_TO_MANY {
            self.validate_many_to_many(field);
            return;
        }
        self.validate_storage(field);
    }

    fn validate_implementation(&mut self, field: &FieldSpec) {
        let Some(implementation) = &field.implementation else {
            return;
        };
        if implementation.class.is_empty() || !NAMESPACE_PATTERN.is_match(&implementation.class) {
            self.add(
                "entity.field.implementation.class.invalid",
                "Specialized fields require a fully-qualified PHP class",
                &field.id,
            );
        }
        if !specialized_field_supported(&implementation.class, &self.spec.shopware_version) {
            let message = format!(
                "Specialized field {} is unavailable for Shopware {}",
                short_class(&implementation.class),
                self.spec.shopware_version
            );
            self.add(
                "entity.field.implementation.version.unsupported",
                message,
                &field.id,
            );
        }
        match implementation.constructor_mode.as_str() {
            CONSTRUCTOR_STORAGE_PROPERTY => {
                if !implementation.fixed_arguments.is_empty() {
                    self.add(
                        "entity.field.implementation.arguments.invalid",
                        "Storage/property constructors cannot use fixed arguments",
                        &field.id,
                    );
                }
            }
            CONSTRUCTOR_FIXED => {
                if !implementation.additional_arguments.is_empty() {
                    self.add(
                        "entity.field.implementation.arguments.invalid",
                        "Fixed constructors cannot use storage/property arguments",
                        &field.id,
                    );
                }
                if field.storage_name != implementation.fixed_storage_name
                    || field.property_name != implementation.fixed_property_name
                {
                    self.add(
                        "entity.field.implementation.fixedIdentity.invalid",
                        "This specialized field constructor fixes its storage and property names",
                        &field.id,
                    );
                }
            }
            _ => self.add(
                "entity.field.implementation.constructor.invalid",
                "Unsupported specialized field constructor shape",
                &field.id,
            ),
        }
        if implementation.max_length_argument && field.kind != FIELD_STRING {
            self.add(
                "entity.field.implementation.maxLength.invalid",
                "Only string-based specialized fields can expose a maximum-length argument",
                &field.id,
            );
        }
        if implementation
            .additional_arguments
            .iter()
            .chain(&implementation.fixed_arguments)
            .any(|expression| !valid_inline_php_expression(expression))
        {
            self.add(
                "entity.field.implementation.expression.invalid",
                "Specialized-field constructor arguments must be safe inline PHP expressions",
                &field.id,
            );
        }
        if implementation.constructor_mode == CONSTRUCTOR_STORAGE_PROPERTY
            && (implementation.additional_arguments.len() as i64)
                < implementation.minimum_additional_arguments
        {
            let message = format!(
                "This specialized field requires at least {} additional constructor argument(s)",
                implementation.minimum_additional_arguments
            );
            self.add(
                "entity.field.implementation.arguments.missing",
                message,
                &field.id,
            );
        }
        if implementation.manage_entity {
            if !valid_entity_type_expression(&implementation.entity_type) {
                self.add(
                    "entity.field.implementation.entityType.invalid",
                    "Managed specialized fields require a builtin or fully-qualified entity value type",
                    &field.id,
                );
            }
        } else if self.spec.mode != "edit" {
            self.add(
                "entity.field.implementation.entity.unmanaged",
                "New specialized fields require a safe entity property type",
                &field.id,
            );
        }
        if !implementation.entity_trait.is_empty()
            && !NAMESPACE_PATTERN.is_match(&implementation.entity_trait)
        {
            self.add(
                "entity.field.implementation.entityTrait.invalid",
                "Specialized entity traits require a fully-qualified PHP trait name",
                &field.id,
            );
        }
    }
}

fn valid_entity_type_expression(value: &str) -> bool {
    let mut seen = HashSet::new();
    for part in value.split('|') {
        let part = trim_class(part);
        if part.is_empty()
            || part == "null"
            || !is_builtin_entity_type(part) && !NAMESPACE_PATTERN.is_match(part)
        {
            return false;
        }
        if !seen.insert(go_to_lower(part)) {
            return false;
        }
    }
    true
}

pub(crate) fn is_builtin_entity_type(value: &str) -> bool {
    matches!(
        value,
        "string" | "int" | "float" | "bool" | "array" | "object" | "mixed"
    )
}

fn invalid_ranking(value: f64) -> bool {
    value < 0.0 || value.is_nan() || value.is_infinite()
}

impl SpecValidator {
    fn validate_field_settings(&mut self, field: &FieldSpec) {
        self.validate_extension_field_settings(field);
        self.validate_inheritance_field_settings(field);
        self.validate_stored_value_settings(field);
        self.validate_field_api_and_search_settings(field);
        self.validate_field_behavior_and_metadata(field);
        self.validate_delete_and_migration_settings(field);
    }

    fn validate_extension_field_settings(&mut self, field: &FieldSpec) {
        if self.spec.definition_kind == DEFINITION_EXTENSION {
            match field.kind.as_str() {
                FIELD_ID | FIELD_AUTO_INCREMENT | FIELD_CREATED_AT | FIELD_UPDATED_AT
                | FIELD_VERSION | FIELD_HIERARCHY => self.add(
                    "entity.extension.field.unsupported",
                    format!(
                        "Field kind {} cannot be added by an EntityExtension",
                        go_quote(&field.kind)
                    ),
                    &field.id,
                ),
                FIELD_FOREIGN_KEY => {
                    if !field
                        .behavior
                        .as_ref()
                        .is_some_and(|behavior| behavior.runtime)
                    {
                        self.add(
                            "entity.extension.foreignKey.association.required",
                            "A persisted EntityExtension foreign key must be added together with its association; use a to-one relation row",
                            &field.id,
                        );
                    }
                }
                _ => {
                    if !valid_entity_extension_field(field) {
                        self.add(
                            "entity.extension.field.runtime.required",
                            "EntityExtension scalar fields must be Runtime; persisted columns require a to-one association with its paired foreign key",
                            &field.id,
                        );
                    }
                }
            }
            if field.primary {
                self.add(
                    "entity.extension.primary.unsupported",
                    "EntityExtension fields cannot change the target primary key",
                    &field.id,
                );
            }
        }
        if field.primary && !field.required {
            self.add(
                "entity.field.primary.required",
                "Primary-key fields must also be required",
                &field.id,
            );
        }
    }

    fn validate_inheritance_field_settings(&mut self, field: &FieldSpec) {
        let inherited =
            field.inherited || field.association_inherited || field.translation_inherited;
        if self.spec.definition_kind == DEFINITION_MAPPING && inherited {
            self.add(
                "entity.inheritance.field.mapping",
                "Mapping-definition fields cannot be inherited",
                &field.id,
            );
        }
        if self.spec.definition_kind == DEFINITION_ENTITY
            && !self.spec.inheritance_aware
            && inherited
        {
            self.add(
                "entity.inheritance.field.owner",
                "Enable inheritance on the entity before marking fields as inherited",
                &field.id,
            );
        }
        if !field.inherited_foreign_key.is_empty()
            && (!field.inherited || !STORAGE_PATTERN.is_match(&field.inherited_foreign_key))
        {
            self.add(
                "entity.inheritance.foreignKey.invalid",
                "Inherited foreign-key overrides require an inherited field and a valid storage name",
                &field.id,
            );
        }
        if !field.association_inherited_fk.is_empty()
            && (!field.association_inherited
                || !STORAGE_PATTERN.is_match(&field.association_inherited_fk))
        {
            self.add(
                "entity.inheritance.associationForeignKey.invalid",
                "Association inherited foreign-key overrides require an inherited association and a valid storage name",
                &field.id,
            );
        }
        if !field.translation_inherited_fk.is_empty()
            && (!field.translation_inherited
                || !STORAGE_PATTERN.is_match(&field.translation_inherited_fk))
        {
            self.add(
                "entity.inheritance.translationForeignKey.invalid",
                "Translated inherited foreign-key overrides require an inherited facade and a valid storage name",
                &field.id,
            );
        }
        if field.translation_inherited && !field.translated {
            self.add(
                "entity.inheritance.translation.unsupported",
                "Only translated fields can inherit through a TranslatedField facade",
                &field.id,
            );
        }
        if (field.hierarchy_children_inherited || field.hierarchy_version_inherited)
            && !self.spec.inheritance_aware
        {
            self.add(
                "entity.inheritance.hierarchyField.owner",
                "Enable inheritance before marking hierarchy components as inherited",
                &field.id,
            );
        }
        if !field.hierarchy_children_inherited_fk.is_empty()
            && (!field.hierarchy_children_inherited
                || !STORAGE_PATTERN.is_match(&field.hierarchy_children_inherited_fk))
        {
            self.add(
                "entity.inheritance.hierarchyChildrenForeignKey.invalid",
                "Children-association inherited foreign-key overrides require a valid storage name",
                &field.id,
            );
        }
        if !field.hierarchy_version_inherited_fk.is_empty()
            && (!field.hierarchy_version_inherited
                || !STORAGE_PATTERN.is_match(&field.hierarchy_version_inherited_fk))
        {
            self.add(
                "entity.inheritance.hierarchyVersionForeignKey.invalid",
                "Parent-version inherited foreign-key overrides require a valid storage name",
                &field.id,
            );
        }
        if field.hierarchy_version_inherited && !field.hierarchy_version_aware {
            self.add(
                "entity.inheritance.hierarchyVersion.unsupported",
                "Only version-aware hierarchies can inherit the parent version field",
                &field.id,
            );
        }
        if !field.hierarchy_children_reverse.is_empty()
            && !PROPERTY_PATTERN.is_match(&field.hierarchy_children_reverse)
        {
            self.add(
                "entity.inheritance.hierarchyChildrenReverse.invalid",
                "Children reverse inheritance requires a valid target property",
                &field.id,
            );
        }
        if !field.reverse_inherited_property.is_empty()
            && (!is_association_kind(&field.kind)
                || !PROPERTY_PATTERN.is_match(&field.reverse_inherited_property))
        {
            self.add(
                "entity.inheritance.reverse.invalid",
                "Reverse inheritance requires an association and a valid target property",
                &field.id,
            );
        }
    }

    fn validate_stored_value_settings(&mut self, field: &FieldSpec) {
        if field.kind == FIELD_STRING && (field.max_length < 0 || field.max_length > 16383) {
            self.add(
                "entity.field.length.invalid",
                "String length must be between 1 and 16383 when specified",
                &field.id,
            );
        }
        if field.kind == FIELD_INT
            && let (Some(min), Some(max)) = (field.min, field.max)
            && min > max
        {
            self.add(
                "entity.field.range.invalid",
                "Integer minimum cannot be greater than its maximum",
                &field.id,
            );
        }
        if field.kind == FIELD_ENUM {
            if !NAMESPACE_PATTERN.is_match(&field.enum_class) {
                self.add(
                    "entity.field.enum.class.invalid",
                    "Enum fields require a fully-qualified PHP enum class",
                    &field.id,
                );
            }
            if !PROPERTY_PATTERN.is_match(&field.enum_case) {
                self.add(
                    "entity.field.enum.case.invalid",
                    "Enum fields require a valid enum case name",
                    &field.id,
                );
            }
            if field.enum_backing_type != "string" && field.enum_backing_type != "int" {
                self.add(
                    "entity.field.enum.backing.invalid",
                    "Enum fields require a string or int backing type",
                    &field.id,
                );
            }
            if !enum_field_supported(&self.spec.shopware_version) {
                let message = format!(
                    "EnumField is unavailable for Shopware {}",
                    self.spec.shopware_version
                );
                self.add("entity.field.enum.version.unsupported", message, &field.id);
            }
        } else if !field.enum_class.is_empty()
            || !field.enum_case.is_empty()
            || !field.enum_backing_type.is_empty()
        {
            self.add(
                "entity.field.enum.arguments.unsupported",
                "Enum class, case, and backing type apply only to EnumField",
                &field.id,
            );
        }
        let mapping = field.json_property_mapping_expression.trim();
        let default = field.json_default_expression.trim();
        if field.kind != FIELD_JSON && (!mapping.is_empty() || !default.is_empty()) {
            self.add(
                "entity.field.json.arguments.unsupported",
                "JSON property mappings and defaults apply only to JsonField",
                &field.id,
            );
        }
        if !mapping.is_empty()
            && !valid_inline_php_expression(&field.json_property_mapping_expression)
        {
            self.add(
                "entity.field.json.propertyMapping.invalid",
                "JSON property mapping must be one safe inline PHP expression",
                &field.id,
            );
        }
        if !default.is_empty() && !valid_inline_php_expression(&field.json_default_expression) {
            self.add(
                "entity.field.json.default.invalid",
                "JSON default must be one safe inline PHP expression",
                &field.id,
            );
        }
    }

    fn validate_field_api_and_search_settings(&mut self, field: &FieldSpec) {
        self.validate_api_aware(
            field.api_aware,
            &field.api_aware_sources,
            "entity.field.apiAware.invalid",
            &field.id,
        );
        self.validate_api_aware(
            field.association_api_aware,
            &field.association_api_aware_sources,
            "entity.association.apiAware.invalid",
            &field.id,
        );
        self.validate_api_aware(
            field.hierarchy_children_api_aware,
            &field.hierarchy_children_api_sources,
            "entity.hierarchy.children.apiAware.invalid",
            &field.id,
        );
        self.validate_api_aware(
            field.hierarchy_version_api_aware,
            &field.hierarchy_version_api_sources,
            "entity.hierarchy.version.apiAware.invalid",
            &field.id,
        );
        let translation_api_aware = field.translation_api_aware.unwrap_or(field.api_aware);
        self.validate_api_aware(
            translation_api_aware,
            &field.translation_api_aware_sources,
            "entity.translation.apiAware.invalid",
            &field.id,
        );
        if invalid_ranking(field.search_ranking) {
            self.add(
                "entity.field.searchRanking.invalid",
                "Search ranking must be a finite non-negative number",
                &field.id,
            );
        }
        if invalid_ranking(field.association_search_rank) {
            self.add(
                "entity.association.searchRanking.invalid",
                "Association search ranking must be a finite non-negative number",
                &field.id,
            );
        }
        if field.association_autoload
            && field.kind != FIELD_MANY_TO_ONE
            && field.kind != FIELD_ONE_TO_ONE
        {
            self.add(
                "entity.association.autoload.unsupported",
                "Only many-to-one and one-to-one associations can be autoloaded",
                &field.id,
            );
        }
        if let Some(conditional) = &field.conditional_association {
            let to_one = |kind: &str| kind == FIELD_MANY_TO_ONE || kind == FIELD_ONE_TO_ONE;
            if !to_one(&field.kind)
                || !to_one(&conditional.alternative_kind)
                || conditional.alternative_kind == field.kind
            {
                self.add(
                    "entity.association.conditional.kind.invalid",
                    "A conditional association must alternate between many-to-one and one-to-one",
                    &field.id,
                );
            }
            if !valid_inline_php_expression(&conditional.condition_expression) {
                self.add(
                    "entity.association.conditional.expression.invalid",
                    "Conditional association checks must be one safe inline PHP expression",
                    &field.id,
                );
            }
        }
        self.validate_write_protection(field);
        if invalid_ranking(field.hierarchy_children_rank) {
            self.add(
                "entity.hierarchy.children.searchRanking.invalid",
                "Hierarchy children search ranking must be a finite non-negative number",
                &field.id,
            );
        }
        if field.translation_search_rank.is_some_and(invalid_ranking) {
            self.add(
                "entity.translation.searchRanking.invalid",
                "Translated-field search ranking must be a finite non-negative number",
                &field.id,
            );
        }
    }

    fn validate_field_behavior_and_metadata(&mut self, field: &FieldSpec) {
        self.validate_behavior(field, "stored", field.behavior.as_ref());
        self.validate_behavior(field, "association", field.association_behavior.as_ref());
        self.validate_behavior(field, "translation", field.translation_behavior.as_ref());
        self.validate_behavior(
            field,
            "hierarchy children",
            field.hierarchy_children_behavior.as_ref(),
        );
        self.validate_behavior(
            field,
            "hierarchy version",
            field.hierarchy_version_behavior.as_ref(),
        );
        self.validate_metadata(field, field.metadata.as_ref());
        self.validate_metadata(field, field.association_metadata.as_ref());
        self.validate_metadata(field, field.translation_metadata.as_ref());
        self.validate_metadata(field, field.hierarchy_children_metadata.as_ref());
        self.validate_metadata(field, field.hierarchy_version_metadata.as_ref());
        if field.search_ranking_tokenize.is_some() && field.search_ranking <= 0.0 {
            self.add(
                "entity.field.searchRanking.tokenizeWithoutRanking",
                "Search tokenization requires a positive search ranking",
                &field.id,
            );
        }
        if field.association_search_tokenize.is_some() && field.association_search_rank <= 0.0 {
            self.add(
                "entity.association.searchRanking.tokenizeWithoutRanking",
                "Association search tokenization requires a positive search ranking",
                &field.id,
            );
        }
        if field.hierarchy_children_tokenize.is_some() && field.hierarchy_children_rank <= 0.0 {
            self.add(
                "entity.hierarchy.children.searchRanking.tokenizeWithoutRanking",
                "Hierarchy search tokenization requires a positive search ranking",
                &field.id,
            );
        }
        if field.translation_search_tokenize.is_some()
            && !field.translation_search_rank.is_some_and(|rank| rank > 0.0)
        {
            self.add(
                "entity.translation.searchRanking.tokenizeWithoutRanking",
                "Translated-field search tokenization requires a positive search ranking",
                &field.id,
            );
        }
    }

    fn validate_delete_and_migration_settings(&mut self, field: &FieldSpec) {
        let delete = field.delete_behavior.as_str();
        if !delete.is_empty()
            && delete != DELETE_RESTRICT
            && delete != DELETE_CASCADE
            && delete != DELETE_SET_NULL
        {
            self.add(
                "entity.relation.delete.invalid",
                "Unsupported delete behavior",
                &field.id,
            );
        }
        if delete == DELETE_SET_NULL && field.required {
            self.add(
                "entity.relation.delete.required",
                "SET NULL cannot be used by a required foreign key",
                &field.id,
            );
        }
        if field.delete_clone_relevant.is_some() && delete != DELETE_CASCADE {
            self.add(
                "entity.relation.delete.cloneRelevant.invalid",
                "Clone relevance applies only to cascade delete",
                &field.id,
            );
        }
        if field.delete_enforced_by_constraint.is_some() && delete != DELETE_SET_NULL {
            self.add(
                "entity.relation.delete.constraint.invalid",
                "Constraint enforcement applies only to set-null delete",
                &field.id,
            );
        }
        if !field.migration_default.is_empty()
            && !valid_backfill_expression(&field.migration_default)
        {
            self.add(
                "entity.migration.backfill.invalid",
                "Backfill must be one SQL expression without comments or statement separators",
                &field.id,
            );
        }
    }
}

/// EntityExtension accepts only associations, runtime fields,
/// ReferenceVersionField, and an FkField paired with its association.
fn valid_entity_extension_field(field: &FieldSpec) -> bool {
    if field
        .behavior
        .as_ref()
        .is_some_and(|behavior| behavior.runtime)
    {
        return true;
    }
    matches!(
        field.kind.as_str(),
        FIELD_REFERENCE_VERSION
            | FIELD_FOREIGN_KEY
            | FIELD_MANY_TO_ONE
            | FIELD_ONE_TO_ONE
            | FIELD_ONE_TO_MANY
            | FIELD_MANY_TO_MANY
    )
}

impl SpecValidator {
    pub(crate) fn validate_behavior(
        &mut self,
        field: &FieldSpec,
        location: &str,
        behavior: Option<&FieldBehavior>,
    ) {
        let Some(behavior) = behavior else {
            return;
        };
        let expression = behavior.runtime_dependencies_expression.trim();
        if !behavior.runtime
            && (!behavior.runtime_dependencies.is_empty() || !expression.is_empty())
        {
            self.add(
                "entity.field.runtime.dependenciesWithoutRuntime",
                "Runtime dependencies require the Runtime flag",
                &field.id,
            );
        }
        if !behavior.runtime_dependencies.is_empty() && !expression.is_empty() {
            self.add(
                "entity.field.runtime.dependencies.ambiguous",
                "Runtime dependencies must use either a literal list or an imported expression",
                &field.id,
            );
        }
        if !expression.is_empty() && !valid_inline_php_expression(expression) {
            self.add(
                "entity.field.runtime.dependencies.invalid",
                "Runtime dependencies must be one safe inline PHP expression",
                &field.id,
            );
        }
        if behavior
            .runtime_dependencies
            .iter()
            .any(|dependency| dependency.trim().is_empty())
        {
            self.add(
                "entity.field.runtime.dependency.empty",
                "Runtime dependencies cannot contain empty values",
                &field.id,
            );
        }
        if !behavior.no_constraint {
            return;
        }
        let stored_foreign_key = location == "stored"
            && matches!(
                field.kind.as_str(),
                FIELD_FOREIGN_KEY | FIELD_MANY_TO_ONE | FIELD_ONE_TO_ONE | FIELD_HIERARCHY
            )
            && !field.uses_existing_column;
        if !stored_foreign_key {
            self.add(
                "entity.field.noConstraint.unsupported",
                "NoConstraint applies only to a stored foreign-key field",
                &field.id,
            );
        }
    }

    pub(crate) fn validate_api_aware(
        &mut self,
        enabled: bool,
        sources: &[String],
        code: &str,
        field_id: &str,
    ) {
        if !sources.is_empty() && !enabled {
            self.add(
                code,
                "API source restrictions require the ApiAware flag",
                field_id,
            );
            return;
        }
        let mut seen = HashSet::new();
        for source in sources {
            let normalized = trim_class(source);
            if normalized.is_empty() || !NAMESPACE_PATTERN.is_match(normalized) {
                self.add(
                    code,
                    "API source restrictions must be fully-qualified PHP classes",
                    field_id,
                );
                return;
            }
            if !seen.insert(go_to_lower(normalized)) {
                self.add(
                    code,
                    "API source restrictions cannot contain duplicates",
                    field_id,
                );
                return;
            }
        }
    }

    fn validate_metadata(&mut self, field: &FieldSpec, metadata: Option<&FieldMetadata>) {
        let Some(metadata) = metadata else {
            return;
        };
        if let Some(deprecated) = &metadata.deprecated
            && (deprecated.deprecated_since.trim().is_empty()
                || deprecated.will_be_removed_in.trim().is_empty())
        {
            self.add(
                "entity.field.metadata.deprecated.invalid",
                "Deprecated metadata requires both the deprecation and removal versions",
                &field.id,
            );
        }
        if metadata
            .rule_areas
            .iter()
            .any(|area| area.trim().is_empty())
        {
            self.add(
                "entity.field.metadata.ruleArea.invalid",
                "Rule areas cannot contain empty values",
                &field.id,
            );
        }
        if let Some(choice) = &metadata.choice {
            if choice.values.is_empty() {
                self.add(
                    "entity.field.metadata.choice.empty",
                    "Choice metadata requires at least one value",
                    &field.id,
                );
            }
            if choice
                .values
                .iter()
                .any(|value| !valid_inline_php_expression(value))
            {
                self.add(
                    "entity.field.metadata.choice.invalid",
                    "Choice values must be safe inline scalar or constant expressions",
                    &field.id,
                );
            }
        }
        if metadata.extension && self.spec.definition_kind != DEFINITION_EXTENSION {
            self.add(
                "entity.field.metadata.extension.unsupported",
                "The Extension flag is valid only inside an EntityExtension",
                &field.id,
            );
        }
    }
}

pub(crate) fn valid_inline_php_expression(value: &str) -> bool {
    let value = value.trim();
    !value.is_empty()
        && !value.contains(';')
        && !value.contains("//")
        && !value.contains("/*")
        && !value.contains("*/")
        && !value.contains('\0')
}

impl SpecValidator {
    fn validate_write_protection(&mut self, field: &FieldSpec) {
        let kind = field.kind.as_str();
        let stored = kind != FIELD_ONE_TO_MANY
            && kind != FIELD_MANY_TO_MANY
            && ((kind != FIELD_ONE_TO_ONE && kind != FIELD_MANY_TO_ONE)
                || !field.uses_existing_column);
        if field.write_protected && !stored {
            self.add(
                "entity.field.writeProtection.unsupported",
                "This field has no stored DAL field to mark write-protected",
                &field.id,
            );
        }
        if field.association_write_protected && !is_association_kind(kind) {
            self.add(
                "entity.association.writeProtection.unsupported",
                "Only association fields can carry association write protection",
                &field.id,
            );
        }
        if field.translation_write_protected && !field.translated {
            self.add(
                "entity.translation.writeProtection.unsupported",
                "Only translated fields have a translated facade to mark write-protected",
                &field.id,
            );
        }
        if (field.hierarchy_children_protected || field.hierarchy_version_protected)
            && kind != FIELD_HIERARCHY
        {
            self.add(
                "entity.hierarchy.writeProtection.unsupported",
                "Hierarchy-component write protection requires a hierarchy field",
                &field.id,
            );
        }
        self.validate_write_protected_scopes(
            field.write_protected,
            &field.write_protected_scopes,
            "entity.field.writeProtection.invalid",
            &field.id,
        );
        self.validate_write_protected_scopes(
            field.association_write_protected,
            &field.association_write_scopes,
            "entity.association.writeProtection.invalid",
            &field.id,
        );
        self.validate_write_protected_scopes(
            field.translation_write_protected,
            &field.translation_write_scopes,
            "entity.translation.writeProtection.invalid",
            &field.id,
        );
        self.validate_write_protected_scopes(
            field.hierarchy_children_protected,
            &field.hierarchy_children_write_scopes,
            "entity.hierarchy.children.writeProtection.invalid",
            &field.id,
        );
        self.validate_write_protected_scopes(
            field.hierarchy_version_protected,
            &field.hierarchy_version_write_scopes,
            "entity.hierarchy.version.writeProtection.invalid",
            &field.id,
        );
    }

    pub(crate) fn validate_write_protected_scopes(
        &mut self,
        enabled: bool,
        scopes: &[String],
        code: &str,
        field_id: &str,
    ) {
        let mut seen = HashSet::new();
        for scope in scopes {
            if !enabled || scope.trim().is_empty() || scope.contains(['\r', '\n', '\0']) {
                self.add(
                    code,
                    "Write-protection scopes require an enabled flag and non-empty literal values",
                    field_id,
                );
                return;
            }
            if !seen.insert(scope.as_str()) {
                self.add(
                    code,
                    "Write-protection scopes must not contain duplicates",
                    field_id,
                );
                return;
            }
        }
    }

    fn validate_translated_field(&mut self, field: &FieldSpec) {
        if self.spec.definition_kind != DEFINITION_ENTITY {
            self.add(
                "entity.translation.owner.unsupported",
                "Only entity-definition fields can be translated",
                &field.id,
            );
            return;
        }
        if !self
            .spec
            .translation
            .as_ref()
            .is_some_and(|translation| translation.enabled)
        {
            self.add(
                "entity.translation.missing",
                "Translated fields require a translation bundle",
                &field.id,
            );
        }
        if !is_translatable_kind(&field.kind) {
            self.add(
                "entity.translation.kind.unsupported",
                format!("Field kind {} cannot be translated", go_quote(&field.kind)),
                &field.id,
            );
            return;
        }
        if field.primary {
            self.add(
                "entity.translation.primary.unsupported",
                "Translated fields cannot be primary keys",
                &field.id,
            );
        }
        if !STORAGE_PATTERN.is_match(&field.storage_name) {
            self.add(
                "entity.field.storage.invalid",
                format!(
                    "Invalid translated storage name {}",
                    go_quote(&field.storage_name)
                ),
                &field.id,
            );
            return;
        }
        if field.storage_name.len() > 64 {
            self.add(
                "entity.field.storage.tooLong",
                "Translated storage column names cannot exceed 64 bytes",
                &field.id,
            );
            return;
        }
        if let Some(previous) = self.translated_storage_names.get(&field.storage_name) {
            let message = format!(
                "Translated storage column {} is already used by field {previous}",
                go_quote(&field.storage_name)
            );
            self.add("entity.field.storage.duplicate", message, &field.id);
            return;
        }
        self.translated_storage_names
            .insert(field.storage_name.clone(), field.id.clone());
    }
}

fn is_translatable_kind(kind: &str) -> bool {
    matches!(
        kind,
        FIELD_BINARY_ID
            | FIELD_STRING
            | FIELD_ENUM
            | FIELD_LONG_TEXT
            | FIELD_INT
            | FIELD_FLOAT
            | FIELD_BOOL
            | FIELD_DATE
            | FIELD_DATE_TIME
            | FIELD_JSON
            | FIELD_LIST
            | FIELD_OBJECT
            | FIELD_BLOB
    )
}

impl SpecValidator {
    fn validate_property(&mut self, field: &FieldSpec) {
        if !PROPERTY_PATTERN.is_match(&field.property_name) {
            self.add(
                "entity.field.property.invalid",
                format!("Invalid property name {}", go_quote(&field.property_name)),
                &field.id,
            );
            return;
        }
        if let Some(previous) = self.properties.get(&field.property_name) {
            if imported_compatible_property_alias(previous, field) {
                return;
            }
            let message = format!(
                "Property {} is already used by field {}",
                go_quote(&field.property_name),
                previous.id
            );
            self.add("entity.field.property.duplicate", message, &field.id);
            return;
        }
        self.properties
            .insert(field.property_name.clone(), field.clone());
    }
}

fn imported_compatible_property_alias(left: &FieldSpec, right: &FieldSpec) -> bool {
    if left.raw.trim().is_empty() || right.raw.trim().is_empty() {
        return false;
    }
    if left.kind == right.kind && left.storage_name == right.storage_name {
        return true;
    }
    is_association_kind(&left.kind)
        && is_association_kind(&right.kind)
        && !left.target_definition_class.is_empty()
        && left.target_definition_class == right.target_definition_class
}

fn imported_compatible_storage_alias(left: &FieldSpec, right: &FieldSpec) -> bool {
    if left.raw.trim().is_empty() || right.raw.trim().is_empty() {
        return false;
    }
    match (sql_type(left), sql_type(right)) {
        (Ok(left_type), Ok(right_type)) => {
            left_type == right_type
                && left.required == right.required
                && left.primary == right.primary
        }
        _ => false,
    }
}

impl SpecValidator {
    fn validate_reference_version(&mut self, field: &FieldSpec) {
        if field.kind != FIELD_REFERENCE_VERSION {
            return;
        }
        if field.target_definition_class.is_empty() || field.target_entity_name.is_empty() {
            self.add(
                "entity.referenceVersion.target.missing",
                "Select an indexed version-aware target entity",
                &field.id,
            );
            return;
        }
        self.reference_version_targets
            .entry(field.target_definition_class.clone())
            .or_default()
            .push(field.clone());
    }

    /// Returns whether storage validation must be skipped because the
    /// association intentionally reuses a local column.
    fn validate_foreign_key(&mut self, field: &FieldSpec) -> bool {
        if field.required && !field.target_definition_class.is_empty() {
            match self
                .required_relation_targets
                .iter_mut()
                .find(|(target, _)| *target == field.target_definition_class)
            {
                Some(entry) => entry.1 = field.id.clone(),
                None => self
                    .required_relation_targets
                    .push((field.target_definition_class.clone(), field.id.clone())),
            }
        }
        if !field.uses_existing_column && field.kind != FIELD_FOREIGN_KEY {
            self.validate_foreign_key_property(field);
        }
        if field.target_definition_class.is_empty()
            || field.target_entity_name.is_empty()
            || (field.kind != FIELD_FOREIGN_KEY && field.target_entity_class.is_empty())
        {
            self.add(
                "entity.relation.target.missing",
                "Select an indexed target entity",
                &field.id,
            );
        }
        if !STORAGE_PATTERN.is_match(&default_string(&field.reference_storage_name, "id")) {
            self.add(
                "entity.relation.reference.invalid",
                "Enter a valid referenced storage column",
                &field.id,
            );
        }
        if !field.uses_existing_column {
            return false;
        }
        if !ASSOCIATION_STORAGE_PATTERN.is_match(&field.storage_name) {
            self.add(
                "entity.field.storage.invalid",
                format!(
                    "Invalid existing storage name {}",
                    go_quote(&field.storage_name)
                ),
                &field.id,
            );
        } else if field.storage_name.contains('.') {
            // Qualified local-field paths are resolver expressions, not
            // physical columns owned by this definition.
            return true;
        } else {
            self.existing_column_references
                .push(ExistingColumnReference {
                    name: field.storage_name.clone(),
                    field_id: field.id.clone(),
                    allow_missing: field.kind == FIELD_MANY_TO_ONE
                        && field
                            .association_behavior
                            .as_ref()
                            .is_some_and(|behavior| behavior.runtime),
                });
        }
        true
    }

    fn validate_foreign_key_property(&mut self, field: &FieldSpec) {
        if !PROPERTY_PATTERN.is_match(&field.foreign_key_property_name) {
            self.add(
                "entity.field.foreignKeyProperty.invalid",
                "To-one relations require a valid foreign-key property",
                &field.id,
            );
            return;
        }
        if let Some(previous) = self.properties.get(&field.foreign_key_property_name) {
            let message = format!(
                "Property {} is already used by field {}",
                go_quote(&field.foreign_key_property_name),
                previous.id
            );
            self.add("entity.field.property.duplicate", message, &field.id);
            return;
        }
        let mut foreign_key_field = field.clone();
        foreign_key_field.property_name = field.foreign_key_property_name.clone();
        self.properties
            .insert(field.foreign_key_property_name.clone(), foreign_key_field);
    }

    fn validate_hierarchy(&mut self, field: &FieldSpec) {
        if self.spec.definition_kind != DEFINITION_ENTITY {
            self.add(
                "entity.hierarchy.owner.unsupported",
                "Only entity definitions can own a hierarchy",
                &field.id,
            );
        }
        if field.target_definition_class != self.spec.definition_class
            || field.target_entity_class != self.spec.entity_class
            || field.target_collection_class != self.spec.collection_class
            || field.target_entity_name != self.spec.entity_name
        {
            self.add(
                "entity.hierarchy.target.invalid",
                "Hierarchy fields must target their owning entity definition",
                &field.id,
            );
        }
        if field.storage_name != "parent_id"
            || field.foreign_key_property_name != "parentId"
            || field.hierarchy_parent_property != "parent"
        {
            self.add(
                "entity.hierarchy.identity.invalid",
                "Hierarchy parent fields must use parent_id, parentId, and parent",
                &field.id,
            );
        }
        if field.delete_behavior != DELETE_CASCADE {
            self.add(
                "entity.hierarchy.delete.invalid",
                "Hierarchy children require cascading parent deletion",
                &field.id,
            );
        }
        self.validate_foreign_key_property(field);
        self.validate_property(&FieldSpec {
            id: field.id.clone(),
            property_name: field.hierarchy_parent_property.clone(),
            ..FieldSpec::default()
        });
        if field.target_collection_class.is_empty() {
            self.add(
                "entity.hierarchy.collection.missing",
                "Hierarchy requires the owning entity collection",
                &field.id,
            );
        }
        self.validate_storage(field);
    }

    fn validate_one_to_many(&mut self, field: &FieldSpec) {
        if field.target_definition_class.is_empty()
            || field.target_collection_class.is_empty()
            || field.reference_storage_name.is_empty()
        {
            self.add(
                "entity.relation.target.missing",
                "Select a target definition, collection, and foreign-key column",
                &field.id,
            );
        }
        if !field.reference_storage_name.is_empty()
            && !STORAGE_PATTERN.is_match(&field.reference_storage_name)
        {
            self.add(
                "entity.relation.reference.invalid",
                "Enter a valid target foreign-key column",
                &field.id,
            );
        }
        let source = default_string(&field.source_column, "id");
        if !STORAGE_PATTERN.is_match(&source) {
            self.add(
                "entity.relation.source.invalid",
                "Enter a valid local source column",
                &field.id,
            );
        } else {
            self.existing_column_references
                .push(ExistingColumnReference {
                    name: source,
                    field_id: field.id.clone(),
                    allow_missing: false,
                });
        }
    }

    fn validate_many_to_many(&mut self, field: &FieldSpec) {
        if field.target_definition_class.is_empty() || field.target_collection_class.is_empty() {
            self.add(
                "entity.relation.target.missing",
                "Select an indexed target entity",
                &field.id,
            );
        }
        if field.mapping_definition_class.is_empty()
            || !NAMESPACE_PATTERN.is_match(&field.mapping_definition_class)
        {
            self.add(
                "entity.relation.mapping.missing",
                "Select a valid mapping entity definition",
                &field.id,
            );
        }
        let columns = [
            ("mapping local column", &field.mapping_local_column),
            ("mapping reference column", &field.mapping_reference_column),
            ("source column", &field.source_column),
            ("reference field", &field.reference_field),
        ];
        for (name, value) in columns {
            if !STORAGE_PATTERN.is_match(value) {
                self.add(
                    "entity.relation.mappingColumn.invalid",
                    format!("Enter a valid {name}"),
                    &field.id,
                );
            }
        }
        let source = default_string(&field.source_column, "id");
        if STORAGE_PATTERN.is_match(&source) {
            self.existing_column_references
                .push(ExistingColumnReference {
                    name: source,
                    field_id: field.id.clone(),
                    allow_missing: false,
                });
        }
    }

    fn validate_storage(&mut self, field: &FieldSpec) {
        if field
            .behavior
            .as_ref()
            .is_some_and(|behavior| behavior.runtime)
        {
            if !PROPERTY_PATTERN.is_match(&field.storage_name) {
                self.add(
                    "entity.field.storage.invalid",
                    format!(
                        "Invalid runtime field name {}",
                        go_quote(&field.storage_name)
                    ),
                    &field.id,
                );
            }
            return;
        }
        if !STORAGE_PATTERN.is_match(&field.storage_name) {
            self.add(
                "entity.field.storage.invalid",
                format!("Invalid storage name {}", go_quote(&field.storage_name)),
                &field.id,
            );
            return;
        }
        if field.storage_name.len() > 64 {
            self.add(
                "entity.field.storage.tooLong",
                "Storage column names cannot exceed 64 bytes",
                &field.id,
            );
            return;
        }
        if let Some(target) = self.extended_storage_names.get(&field.storage_name) {
            let message = format!(
                "Extension field {} conflicts with existing target field {}",
                go_quote(&field.storage_name),
                target.property_name
            );
            self.add("entity.extension.column.duplicate", message, &field.id);
            return;
        }
        if let Some(previous) = self.storage_names.get(&field.storage_name) {
            if imported_compatible_storage_alias(previous, field) {
                return;
            }
            let message = format!(
                "Storage column {} is already used by field {}",
                go_quote(&field.storage_name),
                previous.id
            );
            self.add("entity.field.storage.duplicate", message, &field.id);
            return;
        }
        self.storage_names
            .insert(field.storage_name.clone(), field.clone());
    }

    fn validate_field_set(&mut self) {
        if self.spec.definition_kind == DEFINITION_ENTITY && self.id_count != 1 {
            self.add(
                "entity.id.required",
                "Exactly one primary ID field is required",
                "",
            );
        }
        if self.spec.definition_kind == DEFINITION_MAPPING && self.primary_count == 0 {
            self.add(
                "entity.mapping.primary.required",
                "Mapping definitions require at least one primary-key field",
                "",
            );
        }
        if self.auto_increment_count > 1 {
            self.add(
                "entity.autoIncrement.duplicate",
                "At most one auto-increment field is allowed",
                "",
            );
        }
        if self.hierarchy_count > 1 {
            self.add(
                "entity.hierarchy.duplicate",
                "At most one hierarchy field is allowed",
                "",
            );
        }
        if self.spec.inheritance_aware && self.hierarchy_count != 1 {
            self.add(
                "entity.inheritance.hierarchy.required",
                "Inheritance-aware definitions require exactly one parent/children hierarchy",
                "",
            );
        }
        let mut pending = Vec::new();
        for (target, relation_id) in &self.required_relation_targets {
            if let Some(versions) = self.reference_version_targets.get(target) {
                for version in versions {
                    if !version.required {
                        pending.push((
                            format!(
                                "Reference-version field must be required because relation field {relation_id} is required"
                            ),
                            version.id.clone(),
                        ));
                    }
                }
            }
        }
        for (message, id) in pending {
            self.add("entity.referenceVersion.required", message, &id);
        }
        let references = std::mem::take(&mut self.existing_column_references);
        for reference in &references {
            if self.spec.definition_kind == DEFINITION_EXTENSION {
                if self.extended_storage_names.is_empty()
                    || self.storage_names.contains_key(&reference.name)
                    || self.extended_storage_names.contains_key(&reference.name)
                {
                    continue;
                }
                if !reference.allow_missing {
                    self.add(
                        "entity.relation.existingColumn.missing",
                        format!(
                            "Association references unknown local or target column {}",
                            go_quote(&reference.name)
                        ),
                        &reference.field_id,
                    );
                }
                continue;
            }
            if !self.storage_names.contains_key(&reference.name) && !reference.allow_missing {
                self.add(
                    "entity.relation.existingColumn.missing",
                    format!(
                        "Association references unknown local column {}",
                        go_quote(&reference.name)
                    ),
                    &reference.field_id,
                );
            }
        }
        self.existing_column_references = references;
    }

    fn validate_indexes(&mut self) {
        let mut index_names = HashSet::new();
        let indexes = self.spec.indexes.clone();
        for index in &indexes {
            self.validate_index(index, &mut index_names);
        }
    }

    fn validate_index(&mut self, index: &IndexSpec, index_names: &mut HashSet<String>) {
        if !INDEX_NAME_PATTERN.is_match(&index.name) {
            self.add(
                "entity.index.name.invalid",
                format!("Invalid index name {}", go_quote(&index.name)),
                "",
            );
        } else if index.name.len() > 64 {
            self.add(
                "entity.index.name.tooLong",
                format!("Index name {} exceeds 64 bytes", go_quote(&index.name)),
                "",
            );
        }
        let scope = if index.translation {
            "translation"
        } else {
            "parent"
        };
        if !index_names.insert(format!("{scope}\0{}", index.name)) {
            self.add(
                "entity.index.name.duplicate",
                format!("Index {} is duplicated", go_quote(&index.name)),
                "",
            );
        }
        let translation_enabled = self
            .spec
            .translation
            .as_ref()
            .is_some_and(|translation| translation.enabled);
        if index.translation && !translation_enabled {
            self.add(
                "entity.index.translation.missing",
                format!(
                    "Index {} targets a disabled translation table",
                    go_quote(&index.name)
                ),
                "",
            );
        }
        if index.kind != INDEX_NORMAL && index.kind != INDEX_UNIQUE {
            self.add(
                "entity.index.kind.invalid",
                format!("Unsupported index kind {}", go_quote(&index.kind)),
                "",
            );
        }
        if index.columns.is_empty() {
            self.add(
                "entity.index.columns.empty",
                format!("Index {} has no columns", go_quote(&index.name)),
                "",
            );
        }
        let columns: HashSet<String> = if index.translation {
            let mut columns: HashSet<String> =
                self.translated_storage_names.keys().cloned().collect();
            if let Some(translation) = &self.spec.translation {
                columns.insert(translation.parent_storage_name.clone());
                if has_version_field(&self.spec) {
                    columns.insert(format!(
                        "{}_version_id",
                        trim_suffix(&translation.parent_storage_name, "_id")
                    ));
                }
            }
            for name in ["language_id", "created_at", "updated_at"] {
                columns.insert(name.to_owned());
            }
            columns
        } else {
            let mut columns: HashSet<String> = self.storage_names.keys().cloned().collect();
            if self.spec.definition_kind == DEFINITION_EXTENSION {
                columns.extend(self.extended_storage_names.keys().cloned());
            }
            columns
        };
        let mut seen_columns = HashSet::new();
        for column in &index.columns {
            if !columns.contains(column) {
                if !index.translation && self.translated_storage_names.contains_key(column) {
                    self.add(
                        "entity.index.column.translated",
                        format!(
                            "Index {} cannot reference translated column {} on the parent table",
                            go_quote(&index.name),
                            go_quote(column)
                        ),
                        "",
                    );
                } else {
                    self.add(
                        "entity.index.column.unknown",
                        format!(
                            "Index {} references unknown column {}",
                            go_quote(&index.name),
                            go_quote(column)
                        ),
                        "",
                    );
                }
            }
            if !seen_columns.insert(column.as_str()) {
                self.add(
                    "entity.index.column.duplicate",
                    format!(
                        "Index {} repeats column {}",
                        go_quote(&index.name),
                        go_quote(column)
                    ),
                    "",
                );
            }
        }
        if self.spec.definition_kind == DEFINITION_EXTENSION
            && !index.translation
            && !index
                .columns
                .iter()
                .any(|column| self.storage_names.contains_key(column))
        {
            self.add(
                "entity.extension.index.ownedColumn.required",
                format!(
                    "Extension index {} must include at least one column contributed by this extension",
                    go_quote(&index.name)
                ),
                "",
            );
        }
    }
}

pub(crate) fn is_association_kind(kind: &str) -> bool {
    matches!(
        kind,
        FIELD_MANY_TO_ONE
            | FIELD_ONE_TO_ONE
            | FIELD_ONE_TO_MANY
            | FIELD_MANY_TO_MANY
            | FIELD_HIERARCHY
    )
}

pub fn validate_migration(
    previous: &Schema,
    _next: &Schema,
    diff: &SchemaDiff,
    decisions: &[Decision],
) -> Vec<ValidationIssue> {
    let mut decision_by_target: HashMap<String, &Decision> = HashMap::new();
    for decision in decisions {
        decision_by_target.insert(format!("{}\0{}", decision.entity, decision.to), decision);
    }
    let mut issues = Vec::new();
    let missing = |issues: &mut Vec<ValidationIssue>, entity: &str, column: &Column| {
        issues.push(ValidationIssue {
            code: "entity.migration.backfill.required".to_owned(),
            message: format!(
                "Adding NOT NULL column {entity}.{} to existing rows requires a backfill SQL expression",
                column.name
            ),
            field_id: String::new(),
            severity: "error".to_owned(),
        });
    };
    for change in &diff.added_columns {
        let Some(after) = &change.after else {
            continue;
        };
        if !after.not_null || after.auto_increment || !after.backfill_sql.trim().is_empty() {
            continue;
        }
        if let Some(decision) =
            decision_by_target.get(&format!("{}\0{}", change.entity, after.name))
            && decision.kind == "columnRename"
            && previous
                .entities
                .get(&change.entity)
                .and_then(|entity| entity.columns.get(&decision.from))
                .is_some_and(|old| old.not_null)
        {
            continue;
        }
        missing(&mut issues, &change.entity, after);
    }
    for change in &diff.changed_columns {
        let (Some(before), Some(after)) = (&change.before, &change.after) else {
            continue;
        };
        if before.not_null
            || !after.not_null
            || after.auto_increment
            || !after.backfill_sql.trim().is_empty()
        {
            continue;
        }
        missing(&mut issues, &change.entity, after);
    }
    issues
}

pub(crate) fn supported_field_kind(kind: &str) -> bool {
    matches!(
        kind,
        FIELD_ID
            | FIELD_BINARY_ID
            | FIELD_STRING
            | FIELD_ENUM
            | FIELD_LONG_TEXT
            | FIELD_INT
            | FIELD_FLOAT
            | FIELD_BOOL
            | FIELD_DATE
            | FIELD_DATE_TIME
            | FIELD_JSON
            | FIELD_LIST
            | FIELD_OBJECT
            | FIELD_BLOB
            | FIELD_AUTO_INCREMENT
            | FIELD_CREATED_AT
            | FIELD_UPDATED_AT
            | FIELD_VERSION
            | FIELD_REFERENCE_VERSION
            | FIELD_FOREIGN_KEY
            | FIELD_MANY_TO_ONE
            | FIELD_ONE_TO_ONE
            | FIELD_ONE_TO_MANY
            | FIELD_MANY_TO_MANY
            | FIELD_HIERARCHY
    )
}

pub(crate) fn valid_backfill_expression(value: &str) -> bool {
    let value = value.trim();
    !value.is_empty()
        && !value.contains(';')
        && !value.contains("--")
        && !value.contains("/*")
        && !value.contains("*/")
        && !value.contains('\0')
}

pub fn valid_spec(spec: &EntitySpec) -> Result<()> {
    let issues = validate_spec(spec);
    match issues.first() {
        None => Ok(()),
        Some(issue) => Err(errorf!("{}: {}", issue.code, issue.message)),
    }
}
