//! Spec completion and PHP class generation (port of Go `render.go`).

use std::collections::{BTreeSet, HashMap, HashSet};

use crate::definition_behavior::render_definition_behavior;
use crate::definition_metadata::render_definition_metadata;
use crate::field_modification::{collect_field_modification_imports, render_field_modifications};
use crate::model::*;
use crate::specialized_fields::{CONSTRUCTOR_FIXED, CONSTRUCTOR_STORAGE_PROPERTY};
use crate::util::*;
use crate::validate_spec::{is_builtin_entity_type, valid_spec};

pub use crate::util::go_quote;

/// Fills derived identities, IDs and defaults. Go mutates slices shared with
/// the caller; this port is pure.
pub fn complete_spec(mut spec: EntitySpec) -> EntitySpec {
    if spec.definition_kind.is_empty() {
        spec.definition_kind = DEFINITION_ENTITY.to_owned();
    }
    let namespace = trim_class(&spec.namespace).to_owned();
    let base = spec.class_name.clone();
    complete_definition_identity(&mut spec, &namespace, &base);
    complete_bulk_extensions(&mut spec);
    complete_definition_behavior_fields(spec.definition_behavior.as_mut(), "");
    complete_hierarchy_fields(&mut spec);
    complete_owned_fields(&mut spec);
    complete_field_modifications(&mut spec);
    if spec.definition_kind == DEFINITION_ENTITY {
        complete_translation_spec(&mut spec, &namespace, &base);
        if let Some(translation) = spec.translation.as_mut() {
            complete_definition_behavior_fields(
                translation.definition_behavior.as_mut(),
                "translation-",
            );
        }
    }
    spec
}

fn complete_definition_identity(spec: &mut EntitySpec, namespace: &str, base: &str) {
    if spec.definition_class.is_empty() {
        let suffix = match spec.definition_kind.as_str() {
            DEFINITION_EXTENSION => "Extension",
            DEFINITION_BULK_EXTENSION => "BulkEntityExtension",
            _ => "Definition",
        };
        spec.definition_class = qualify(namespace, &format!("{base}{suffix}"));
    }
    if spec.definition_kind == DEFINITION_EXTENSION
        || spec.definition_kind == DEFINITION_BULK_EXTENSION
    {
        spec.entity_class.clear();
        spec.collection_class.clear();
        if spec.mode != "edit" {
            spec.entity_uri.clear();
            spec.collection_uri.clear();
        }
    } else if spec.definition_kind == DEFINITION_MAPPING && spec.mode != "edit" {
        // A fresh mapping definition uses the framework's generic mapping
        // entity; imported mappings keep explicit companion classes.
        spec.entity_class.clear();
        spec.collection_class.clear();
        spec.entity_uri.clear();
        spec.collection_uri.clear();
    } else if spec.definition_kind == DEFINITION_ENTITY {
        if spec.entity_class.is_empty() {
            spec.entity_class = qualify(namespace, &format!("{base}Entity"));
        }
        if spec.collection_class.is_empty() {
            spec.collection_class = qualify(namespace, &format!("{base}Collection"));
        }
    }
}

fn complete_bulk_extensions(spec: &mut EntitySpec) {
    if spec.definition_kind != DEFINITION_BULK_EXTENSION {
        return;
    }
    for (target_index, target) in spec.bulk_extensions.iter_mut().enumerate() {
        if target.id.is_empty() {
            target.id = format!("bulk-target-{target_index}");
        }
        for (index, field) in target.fields.iter_mut().enumerate() {
            complete_field_spec(field, index);
        }
    }
    spec.fields.clear();
    spec.indexes.clear();
}

fn complete_definition_behavior_fields(
    behavior: Option<&mut DefinitionBehaviorSpec>,
    prefix: &str,
) {
    let Some(behavior) = behavior else {
        return;
    };
    for (index, field) in behavior.default_fields.iter_mut().enumerate() {
        if field.id.is_empty() {
            field.id = format!("{prefix}default-field-{}", index + 1);
        }
        complete_field_spec(field, index);
    }
    for (index, field) in behavior.base_fields.iter_mut().enumerate() {
        if field.id.is_empty() {
            field.id = format!("{prefix}base-field-{}", index + 1);
        }
        complete_field_spec(field, index);
    }
}

fn complete_hierarchy_fields(spec: &mut EntitySpec) {
    let has_version = has_version_field(spec);
    let definition_class = spec.definition_class.clone();
    let entity_class = spec.entity_class.clone();
    let collection_class = spec.collection_class.clone();
    let entity_name = spec.entity_name.clone();
    let complete = |field: &mut FieldSpec| {
        if field.kind != FIELD_HIERARCHY {
            return;
        }
        field.target_definition_class = definition_class.clone();
        field.target_entity_class = entity_class.clone();
        field.target_collection_class = collection_class.clone();
        field.target_entity_name = entity_name.clone();
        field.hierarchy_version_aware = has_version;
    };
    if let Some(behavior) = spec.definition_behavior.as_mut() {
        behavior.default_fields.iter_mut().for_each(complete);
        behavior.base_fields.iter_mut().for_each(complete);
    }
    spec.fields.iter_mut().for_each(complete);
}

fn complete_owned_fields(spec: &mut EntitySpec) {
    let fields = std::mem::take(&mut spec.fields);
    let mut result = Vec::with_capacity(fields.len());
    for mut field in fields {
        if is_implicit_timestamp_field(spec, &field.kind) {
            continue;
        }
        complete_field_spec(&mut field, result.len());
        result.push(field);
    }
    spec.fields = result;
}

fn complete_field_modifications(spec: &mut EntitySpec) {
    for (index, modification) in spec.field_modifications.iter_mut().enumerate() {
        if modification.id.is_empty() {
            modification.id = format!("modify-{}-{index}", modification.property_name);
        }
    }
}

fn complete_translation_spec(spec: &mut EntitySpec, namespace: &str, base: &str) {
    let has_translated_fields = spec.fields.iter().any(|field| field.translated);
    if has_translated_fields && spec.translation.is_none() {
        spec.translation = Some(TranslationSpec {
            enabled: true,
            association_required: true,
            ..TranslationSpec::default()
        });
    }
    let entity_name = spec.entity_name.clone();
    let definition_class = spec.definition_class.clone();
    let Some(translation) = spec.translation.as_mut() else {
        return;
    };
    if !(translation.enabled || has_translated_fields) {
        return;
    }
    translation.enabled = true;
    let translation_base = format!("{base}Translation");
    let translation_namespace = qualify(namespace, &format!("Aggregate\\{translation_base}"));
    if translation.entity_name.is_empty() {
        translation.entity_name = format!("{entity_name}_translation");
    }
    if translation.definition_class.is_empty() {
        translation.definition_class = qualify(
            &translation_namespace,
            &format!("{translation_base}Definition"),
        );
    }
    if translation.entity_class.is_empty() {
        translation.entity_class =
            qualify(&translation_namespace, &format!("{translation_base}Entity"));
    }
    if translation.collection_class.is_empty() {
        translation.collection_class = qualify(
            &translation_namespace,
            &format!("{translation_base}Collection"),
        );
    }
    if translation.parent_definition_class.is_empty() {
        translation.parent_definition_class = definition_class;
    }
    if translation.parent_storage_name.is_empty() {
        translation.parent_storage_name = format!("{entity_name}_id");
    }
    if translation.parent_property_name.is_empty() {
        translation.parent_property_name = camelize_storage_name(&entity_name);
    }
    if translation.association_property.is_empty() {
        translation.association_property = "translations".to_owned();
    }
    if translation.association_local_field.is_empty() {
        translation.association_local_field = "id".to_owned();
    }
}

pub(crate) fn complete_field_spec(field: &mut FieldSpec, index: usize) {
    if field.id.is_empty() {
        field.id = format!("field-{}", index + 1);
    }
    if !field.target_definition_class.is_empty() && field.target_entity_name.is_empty() {
        field.target_entity_name = infer_entity_name(&field.target_definition_class);
    }
    match field.kind.as_str() {
        FIELD_ID => {
            field.property_name = "id".to_owned();
            field.storage_name = "id".to_owned();
            field.required = true;
            field.primary = true;
            field.editable = true;
        }
        FIELD_AUTO_INCREMENT => {
            field.property_name = "autoIncrement".to_owned();
            field.storage_name = "auto_increment".to_owned();
            field.required = true;
            field.editable = true;
        }
        FIELD_VERSION => {
            field.property_name = "versionId".to_owned();
            field.storage_name = "version_id".to_owned();
            field.required = true;
            field.primary = true;
            field.editable = true;
            if field.migration_default.is_empty() {
                field.migration_default = "UNHEX('0fa91ce3e96a4bc2be4bd9ce752c3425')".to_owned();
            }
        }
        FIELD_REFERENCE_VERSION => {
            if field.storage_name.is_empty() && !field.target_entity_name.is_empty() {
                field.storage_name = format!("{}_version_id", field.target_entity_name);
                field.property_name = camelize_storage_name(&field.storage_name);
            }
        }
        FIELD_FOREIGN_KEY => {
            if field.reference_field.is_empty() {
                field.reference_field = "id".to_owned();
            }
            if field.reference_storage_name.is_empty() {
                field.reference_storage_name = "id".to_owned();
            }
        }
        FIELD_CREATED_AT => {
            field.property_name = "createdAt".to_owned();
            field.storage_name = "created_at".to_owned();
            field.required = true;
            field.editable = true;
            if field.migration_default.is_empty() {
                field.migration_default = "CURRENT_TIMESTAMP(3)".to_owned();
            }
        }
        FIELD_UPDATED_AT => {
            field.property_name = "updatedAt".to_owned();
            field.storage_name = "updated_at".to_owned();
            field.required = false;
            field.editable = true;
        }
        FIELD_MANY_TO_ONE | FIELD_ONE_TO_ONE => {
            if !field.uses_existing_column && field.foreign_key_property_name.is_empty() {
                field.foreign_key_property_name = format!("{}Id", field.property_name);
            }
            if field.reference_field.is_empty() {
                field.reference_field = "id".to_owned();
            }
            if field.reference_storage_name.is_empty() {
                field.reference_storage_name = "id".to_owned();
            }
        }
        FIELD_ONE_TO_MANY => {
            if field.reference_storage_name.is_empty() {
                field.reference_storage_name = "id".to_owned();
            }
            if field.source_column.is_empty() {
                field.source_column = "id".to_owned();
            }
        }
        FIELD_MANY_TO_MANY => {
            if field.reference_field.is_empty() {
                field.reference_field = "id".to_owned();
            }
            if field.source_column.is_empty() {
                field.source_column = "id".to_owned();
            }
        }
        FIELD_HIERARCHY => {
            field.property_name = default_string(&field.property_name, "children");
            field.hierarchy_parent_property = "parent".to_owned();
            field.foreign_key_property_name = "parentId".to_owned();
            field.storage_name = "parent_id".to_owned();
            field.reference_field = "id".to_owned();
            field.reference_storage_name = "id".to_owned();
            field.required = false;
            field.primary = false;
            field.delete_behavior = DELETE_CASCADE.to_owned();
            field.editable = true;
        }
        _ => {}
    }
}

fn push_indented(builder: &mut String, text: &str, indent: &str) {
    for line in text.trim().split('\n') {
        builder.push_str(indent);
        builder.push_str(line);
        builder.push('\n');
    }
}

fn locked_expression(raw: &str) -> String {
    format!("{},", trim_suffix(raw.trim(), ","))
}

pub fn render_definition(spec: &EntitySpec) -> Result<String> {
    let spec = complete_spec(spec.clone());
    valid_spec(&spec)?;
    render_completed_definition(&spec)
}

pub(crate) fn render_completed_definition(spec: &EntitySpec) -> Result<String> {
    if spec.definition_kind == DEFINITION_BULK_EXTENSION {
        return render_bulk_entity_extension(spec);
    }
    let imports = ImportTable::new(definition_imports(spec));
    let mut fields = Vec::new();
    for field in &spec.fields {
        if field.translation_definition_only || is_implicit_timestamp_field(spec, &field.kind) {
            continue;
        }
        if field.kind == FIELD_LOCKED && !field.raw.trim().is_empty() {
            fields.push(locked_expression(&field.raw));
        } else if field.translated {
            fields.push(render_translated_field(field, &imports));
        } else if spec.definition_kind == DEFINITION_EXTENSION {
            fields.extend(render_definition_field(
                &with_entity_extension_flag(field.clone()),
                &imports,
            )?);
        } else {
            fields.extend(render_definition_field(field, &imports)?);
        }
    }
    if let Some(translation) = spec
        .translation
        .as_ref()
        .filter(|translation| translation.enabled)
    {
        fields.push(render_translations_association(translation, &imports));
    }
    let mut builder = String::new();
    builder.push_str("<?php declare(strict_types=1);\n\nnamespace ");
    builder.push_str(trim_backslash(&spec.namespace));
    builder.push_str(";\n\n");
    builder.push_str(&imports.render());
    builder.push_str("\nclass ");
    builder.push_str(short_class(&spec.definition_class));
    match spec.definition_kind.as_str() {
        DEFINITION_EXTENSION => {
            builder.push_str(" extends EntityExtension\n{\n");
            write_entity_extension_methods(&mut builder, spec, &imports, &fields);
            builder.push_str("}\n");
            return Ok(builder);
        }
        DEFINITION_MAPPING => builder.push_str(" extends MappingEntityDefinition\n{\n"),
        _ => builder.push_str(" extends EntityDefinition\n{\n"),
    }
    builder.push_str("    final public const ENTITY_NAME = '");
    builder.push_str(&escape_php_single(&spec.entity_name));
    builder.push_str("';\n\n");
    builder.push_str(
        "    public function getEntityName(): string\n    {\n        return self::ENTITY_NAME;\n    }\n\n",
    );
    if spec.definition_kind == DEFINITION_ENTITY
        || spec.definition_kind == DEFINITION_MAPPING
            && !spec.entity_class.is_empty()
            && !spec.collection_class.is_empty()
    {
        builder.push_str("    public function getEntityClass(): string\n    {\n        return ");
        builder.push_str(&imports.reference(&spec.entity_class));
        builder.push_str("::class;\n    }\n\n");
        builder
            .push_str("    public function getCollectionClass(): string\n    {\n        return ");
        builder.push_str(&imports.reference(&spec.collection_class));
        builder.push_str("::class;\n    }\n\n");
        if spec.definition_kind == DEFINITION_ENTITY && spec.inheritance_aware {
            builder.push_str(
                "    public function isInheritanceAware(): bool\n    {\n        return true;\n    }\n\n",
            );
        }
        if spec.definition_kind == DEFINITION_ENTITY {
            let protection_method = render_entity_protection_method(spec);
            if !protection_method.is_empty() {
                push_indented(&mut builder, &protection_method, "    ");
                builder.push('\n');
            }
        }
    }
    let behavior = render_definition_behavior(spec.definition_behavior.as_ref(), &imports, false)?;
    if !behavior.is_empty() {
        write_indented_class_member(&mut builder, &behavior);
        builder.push('\n');
    }
    let metadata = render_definition_metadata(spec.definition_metadata.as_ref(), &imports, false);
    if !metadata.is_empty() {
        write_indented_class_member(&mut builder, &metadata);
        builder.push('\n');
    }
    builder.push_str(
        "    protected function defineFields(): FieldCollection\n    {\n        return new FieldCollection([\n",
    );
    for field in &fields {
        push_indented(&mut builder, field, "            ");
    }
    builder.push_str("        ]);\n    }\n}\n");
    Ok(builder)
}

fn render_bulk_entity_extension(spec: &EntitySpec) -> Result<String> {
    let imports = ImportTable::new(definition_imports(spec));
    let mut builder = String::new();
    builder.push_str("<?php declare(strict_types=1);\n\nnamespace ");
    builder.push_str(trim_backslash(&spec.namespace));
    builder.push_str(";\n\n");
    builder.push_str(&imports.render());
    builder.push_str("\nclass ");
    builder.push_str(short_class(&spec.definition_class));
    builder.push_str(" extends BulkEntityExtension\n{\n");
    if !spec.collect_method_raw.trim().is_empty() {
        for (index, line) in spec.collect_method_raw.trim().split('\n').enumerate() {
            if index == 0 {
                builder.push_str("    ");
            }
            builder.push_str(line);
            builder.push('\n');
        }
        builder.push_str("}\n");
        return Ok(builder);
    }
    builder.push_str("    public function collect(): \\Generator\n    {\n");
    for target in &spec.bulk_extensions {
        builder.push_str("        yield ");
        if !target.extended_definition_class.is_empty() {
            builder.push_str(&imports.reference(&target.extended_definition_class));
            builder.push_str("::ENTITY_NAME");
        } else {
            builder.push_str(&quote_php(&target.entity_name));
        }
        builder.push_str(" => [\n");
        for original in &target.fields {
            let field = with_entity_extension_flag(original.clone());
            let rendered = if field.kind == FIELD_LOCKED && !field.raw.trim().is_empty() {
                vec![locked_expression(&field.raw)]
            } else {
                render_definition_field(&field, &imports)?
            };
            for expression in rendered {
                push_indented(&mut builder, &expression, "            ");
            }
        }
        builder.push_str("        ];\n\n");
    }
    builder.push_str("    }\n}\n");
    Ok(builder)
}

fn render_protections(spec: &EntitySpec) -> Vec<String> {
    let mut protections = Vec::new();
    if spec.read_protected {
        protections.push(format!(
            "new ReadProtection({})",
            render_string_arguments(&spec.read_protection_scopes)
        ));
    }
    if spec.write_protected {
        protections.push(format!(
            "new WriteProtection({})",
            render_string_arguments(&spec.write_protection_scopes)
        ));
    }
    protections.extend(spec.preserved_protections.iter().cloned());
    protections
}

pub(crate) fn render_entity_protection_method(spec: &EntitySpec) -> String {
    if !spec.protection_method_raw.trim().is_empty() {
        return spec.protection_method_raw.trim().to_owned();
    }
    let protections = render_protections(spec);
    if protections.is_empty() {
        return String::new();
    }
    let mut builder = String::from(
        "protected function defineProtections(): EntityProtectionCollection\n{\n    return new EntityProtectionCollection([\n",
    );
    for protection in protections {
        builder.push_str("        ");
        builder.push_str(trim_suffix(protection.trim(), ","));
        builder.push_str(",\n");
    }
    builder.push_str("    ]);\n}");
    builder
}

pub(crate) fn render_string_arguments(values: &[String]) -> String {
    values
        .iter()
        .map(|value| quote_php(value))
        .collect::<Vec<_>>()
        .join(", ")
}

pub(crate) fn with_entity_extension_flag(mut field: FieldSpec) -> FieldSpec {
    fn mark(metadata: &mut Option<FieldMetadata>) {
        metadata
            .get_or_insert_with(FieldMetadata::default)
            .extension = true;
    }
    let kind = field.kind.as_str();
    if kind != FIELD_ONE_TO_MANY
        && kind != FIELD_MANY_TO_MANY
        && ((kind != FIELD_ONE_TO_ONE && kind != FIELD_MANY_TO_ONE) || !field.uses_existing_column)
    {
        mark(&mut field.metadata);
    }
    if matches!(
        kind,
        FIELD_MANY_TO_ONE | FIELD_ONE_TO_ONE | FIELD_ONE_TO_MANY | FIELD_MANY_TO_MANY
    ) {
        mark(&mut field.association_metadata);
    }
    field
}

fn write_entity_extension_methods(
    builder: &mut String,
    spec: &EntitySpec,
    imports: &ImportTable,
    fields: &[String],
) {
    builder
        .push_str("    public function extendFields(FieldCollection $collection): void\n    {\n");
    for field in fields {
        builder.push_str("        $collection->add(\n");
        push_indented(builder, field, "            ");
        builder.push_str("        );\n");
    }
    builder.push_str("    }\n\n");
    let protection_method = render_entity_extension_protection_method(spec);
    if !protection_method.is_empty() {
        push_indented(builder, &protection_method, "    ");
        builder.push('\n');
    }
    let modification_method = render_field_modifications(spec, imports);
    if !modification_method.is_empty() {
        push_indented(builder, &modification_method, "    ");
        builder.push('\n');
    }
    let target = imports.reference(&spec.extended_definition_class);
    builder.push_str("    public function getEntityName(): string\n    {\n        return ");
    builder.push_str(&target);
    builder.push_str("::ENTITY_NAME;\n    }\n");
    if entity_extension_needs_legacy_method(&spec.shopware_version) {
        builder
            .push_str("\n    public function getDefinitionClass(): string\n    {\n        return ");
        builder.push_str(&target);
        builder.push_str("::class;\n    }\n");
    }
}

fn render_entity_extension_protection_method(spec: &EntitySpec) -> String {
    if !spec.protection_method_raw.trim().is_empty() {
        return spec.protection_method_raw.trim().to_owned();
    }
    let protections = render_protections(spec);
    if protections.is_empty() {
        return String::new();
    }
    let mut builder = String::from(
        "public function extendProtections(EntityProtectionCollection $protections): void\n{\n",
    );
    for protection in protections {
        builder.push_str("    $protections->add(");
        builder.push_str(trim_suffix(protection.trim(), ","));
        builder.push_str(");\n");
    }
    builder.push('}');
    builder
}

fn entity_extension_needs_legacy_method(version_constraint: &str) -> bool {
    match swlsp_php_project::parse_version_constraint(version_constraint) {
        Some(version) => !version.at_least_patch(6, 7, 0),
        None => true,
    }
}

pub fn render_entity(spec: &EntitySpec) -> Result<String> {
    let spec = complete_spec(spec.clone());
    if spec.definition_kind != DEFINITION_ENTITY {
        return Err(errorf!(
            "{} definitions do not have entity classes",
            spec.definition_kind
        ));
    }
    valid_spec(&spec)?;
    let imports = ImportTable::new(entity_imports(&spec));
    let mut properties = Vec::new();
    let mut methods = Vec::new();
    for field in schema_definition_fields(&spec).iter() {
        if field.translation_definition_only {
            continue;
        }
        let (field_properties, field_methods) = render_entity_field(field, &imports)?;
        properties.extend(field_properties);
        methods.extend(field_methods);
    }
    if let Some(translation) = spec
        .translation
        .as_ref()
        .filter(|translation| translation.enabled)
    {
        let collection = imports.reference(&translation.collection_class);
        let property = &translation.association_property;
        properties.push(format!("protected ?{collection} ${property} = null;"));
        methods.extend(accessors(property, &format!("?{collection}"), false));
    }
    let mut builder = String::new();
    builder.push_str("<?php declare(strict_types=1);\n\nnamespace ");
    builder.push_str(trim_backslash(&spec.namespace));
    builder.push_str(";\n\n");
    builder.push_str(&imports.render());
    builder.push_str("\nclass ");
    builder.push_str(short_class(&spec.entity_class));
    builder.push_str(" extends Entity\n{\n");
    for trait_name in entity_implementation_traits(&spec, false) {
        builder.push_str("    use ");
        builder.push_str(&imports.reference(&trait_name));
        builder.push_str(";\n");
    }
    builder.push_str("    use EntityIdTrait;\n");
    for property in &properties {
        builder.push_str("\n    ");
        builder.push_str(property);
        builder.push('\n');
    }
    for method in &methods {
        builder.push('\n');
        for line in method.split('\n') {
            builder.push_str("    ");
            builder.push_str(line);
            builder.push('\n');
        }
    }
    builder.push_str("}\n");
    Ok(builder)
}

fn render_collection_source(
    namespace: &str,
    entity: &str,
    collection: &str,
    entity_name: &str,
) -> String {
    format!(
        "<?php declare(strict_types=1);

namespace {namespace};

use Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityCollection;

/**
 * @extends EntityCollection<{entity}>
 */
class {collection} extends EntityCollection
{{
    public function getApiAlias(): string
    {{
        return '{entity_name}_collection';
    }}

    protected function getExpectedClass(): string
    {{
        return {entity}::class;
    }}
}}
"
    )
}

pub fn render_collection(spec: &EntitySpec) -> Result<String> {
    let spec = complete_spec(spec.clone());
    if spec.definition_kind != DEFINITION_ENTITY {
        return Err(errorf!(
            "{} definitions do not have collection classes",
            spec.definition_kind
        ));
    }
    valid_spec(&spec)?;
    Ok(render_collection_source(
        trim_backslash(&spec.namespace),
        short_class(&spec.entity_class),
        short_class(&spec.collection_class),
        &escape_php_single(&spec.entity_name),
    ))
}

/// Renders the EntityTranslationDefinition owned by `spec`.
pub fn render_translation_definition(spec: &EntitySpec) -> Result<String> {
    let spec = complete_spec(spec.clone());
    valid_spec(&spec)?;
    let Some(translation) = spec
        .translation
        .as_ref()
        .filter(|translation| translation.enabled)
    else {
        return Err(errorf!("entity has no translation bundle"));
    };
    let imports = ImportTable::new(translation_definition_imports(&spec));
    let mut fields = Vec::new();
    for field in &spec.fields {
        if !field.translated || field.kind == FIELD_LOCKED && !field.translation_definition_only {
            continue;
        }
        let mut storage_field = field.clone();
        storage_field.translated = false;
        fields.extend(render_definition_field(&storage_field, &imports)?);
    }
    let mut builder = String::new();
    builder.push_str("<?php declare(strict_types=1);\n\nnamespace ");
    builder.push_str(namespace_of(&translation.definition_class));
    builder.push_str(";\n\n");
    builder.push_str(&imports.render());
    builder.push_str("\nclass ");
    builder.push_str(short_class(&translation.definition_class));
    builder.push_str(" extends EntityTranslationDefinition\n{\n");
    builder.push_str("    final public const ENTITY_NAME = '");
    builder.push_str(&escape_php_single(&translation.entity_name));
    builder.push_str("';\n\n");
    builder.push_str(
        "    public function getEntityName(): string\n    {\n        return self::ENTITY_NAME;\n    }\n\n",
    );
    builder.push_str("    public function getEntityClass(): string\n    {\n        return ");
    builder.push_str(&imports.reference(&translation.entity_class));
    builder.push_str("::class;\n    }\n\n");
    builder.push_str("    public function getCollectionClass(): string\n    {\n        return ");
    builder.push_str(&imports.reference(&translation.collection_class));
    builder.push_str("::class;\n    }\n\n");
    builder.push_str(
        "    protected function getParentDefinitionClass(): string\n    {\n        return ",
    );
    builder.push_str(&imports.reference(&translation.parent_definition_class));
    builder.push_str("::class;\n    }\n\n");
    let behavior =
        render_definition_behavior(translation.definition_behavior.as_ref(), &imports, true)?;
    if !behavior.is_empty() {
        write_indented_class_member(&mut builder, &behavior);
        builder.push('\n');
    }
    let metadata =
        render_definition_metadata(translation.definition_metadata.as_ref(), &imports, true);
    if !metadata.is_empty() {
        write_indented_class_member(&mut builder, &metadata);
        builder.push('\n');
    }
    builder.push_str(
        "    protected function defineFields(): FieldCollection\n    {\n        return new FieldCollection([\n",
    );
    for field in &fields {
        push_indented(&mut builder, field, "            ");
    }
    builder.push_str("        ]);\n    }\n}\n");
    Ok(builder)
}

fn write_indented_class_member(builder: &mut String, member: &str) {
    for line in member.split('\n') {
        if !line.is_empty() {
            builder.push_str("    ");
            builder.push_str(line);
        }
        builder.push('\n');
    }
}

pub fn render_translation_entity(spec: &EntitySpec) -> Result<String> {
    let spec = complete_spec(spec.clone());
    valid_spec(&spec)?;
    let Some(translation) = spec
        .translation
        .as_ref()
        .filter(|translation| translation.enabled)
    else {
        return Err(errorf!("entity has no translation bundle"));
    };
    let imports = ImportTable::new(translation_entity_imports(&spec));
    let parent_property = &translation.parent_property_name;
    let parent_id_property = camelize_storage_name(&translation.parent_storage_name);
    let mut properties = Vec::new();
    let mut methods = Vec::new();
    let behavior = translation.definition_behavior.as_ref();
    let overrides_base_fields = behavior.is_some_and(|behavior| behavior.override_base_fields);
    if !overrides_base_fields {
        properties.push(format!("protected string ${parent_id_property};"));
        methods.extend(accessors(&parent_id_property, "string", false));
    }
    if !overrides_base_fields && has_version_field(&spec) {
        let version_property = camelize_storage_name(&format!(
            "{}_version_id",
            trim_suffix(&translation.parent_storage_name, "_id")
        ));
        properties.push(format!("protected string ${version_property};"));
        methods.extend(accessors(&version_property, "string", false));
    }
    if let Some(behavior) = behavior {
        if behavior.override_default_fields {
            for field in &behavior.default_fields {
                let (field_properties, field_methods) = render_entity_field(field, &imports)?;
                properties.extend(field_properties);
                methods.extend(field_methods);
            }
        }
        if behavior.override_base_fields {
            for field in &behavior.base_fields {
                let (field_properties, field_methods) = render_entity_field(field, &imports)?;
                properties.extend(field_properties);
                methods.extend(field_methods);
            }
        }
    }
    for field in &spec.fields {
        if !field.translated || field.kind == FIELD_LOCKED {
            continue;
        }
        if let Some(implementation) = &field.implementation
            && (!implementation.manage_entity || !implementation.entity_trait.is_empty())
        {
            continue;
        }
        let (mut php_type, mut boolean) = entity_field_php_type(field, &imports)?;
        if let Some(implementation) = &field.implementation {
            if !implementation.entity_type.is_empty() {
                php_type = entity_implementation_type(&implementation.entity_type, &imports);
            }
            boolean = implementation.entity_boolean_getter;
        }
        let php_type = nullable_php_type(&php_type);
        properties.push(format!(
            "protected {php_type} ${} = null;",
            field.property_name
        ));
        let mut field_methods = behavior_accessors(
            &field.property_name,
            &php_type,
            boolean,
            field.translation_behavior.as_ref(),
        );
        if field
            .implementation
            .as_ref()
            .is_some_and(|implementation| implementation.implicit_computed)
        {
            field_methods.truncate(1);
        }
        methods.extend(field_methods);
    }
    if !overrides_base_fields {
        let parent_entity = imports.reference(&spec.entity_class);
        properties.push(format!(
            "protected ?{parent_entity} ${parent_property} = null;"
        ));
        methods.extend(accessors(
            parent_property,
            &format!("?{parent_entity}"),
            false,
        ));
    }

    let mut builder = String::new();
    builder.push_str("<?php declare(strict_types=1);\n\nnamespace ");
    builder.push_str(namespace_of(&translation.entity_class));
    builder.push_str(";\n\n");
    builder.push_str(&imports.render());
    builder.push_str("\nclass ");
    builder.push_str(short_class(&translation.entity_class));
    builder.push_str(" extends TranslationEntity\n{");
    for trait_name in entity_implementation_traits(&spec, true) {
        builder.push_str("\n    use ");
        builder.push_str(&imports.reference(&trait_name));
        builder.push_str(";\n");
    }
    for property in &properties {
        builder.push_str("\n    ");
        builder.push_str(property);
        builder.push('\n');
    }
    for method in &methods {
        builder.push('\n');
        for line in method.split('\n') {
            builder.push_str("    ");
            builder.push_str(line);
            builder.push('\n');
        }
    }
    builder.push_str("}\n");
    Ok(builder)
}

pub fn render_translation_collection(spec: &EntitySpec) -> Result<String> {
    let spec = complete_spec(spec.clone());
    valid_spec(&spec)?;
    let Some(translation) = spec
        .translation
        .as_ref()
        .filter(|translation| translation.enabled)
    else {
        return Err(errorf!("entity has no translation bundle"));
    };
    Ok(render_collection_source(
        namespace_of(&translation.collection_class),
        short_class(&translation.entity_class),
        short_class(&translation.collection_class),
        &escape_php_single(&translation.entity_name),
    ))
}

pub fn render_migration(
    namespace: &str,
    class_name: &str,
    timestamp: i64,
    statements: &[String],
) -> String {
    let mut body = String::new();
    for statement in statements {
        let statement = statement.trim();
        if statement.is_empty() {
            continue;
        }
        body.push_str("        $connection->executeStatement(<<<'SQL'\n");
        for line in statement.split('\n') {
            body.push_str("            ");
            body.push_str(line);
            body.push('\n');
        }
        body.push_str("            SQL);\n");
    }
    format!(
        "<?php declare(strict_types=1);

namespace {}\\Migration;

use Doctrine\\DBAL\\Connection;
use Shopware\\Core\\Framework\\Migration\\MigrationStep;

class {class_name} extends MigrationStep
{{
    public function getCreationTimestamp(): int
    {{
        return {timestamp};
    }}

    public function update(Connection $connection): void
    {{
{body}    }}
}}
",
        trim_backslash(namespace)
    )
}

fn flags_suffix(values: Vec<String>) -> String {
    let active: Vec<String> = values
        .into_iter()
        .filter(|value| !value.is_empty())
        .collect();
    if active.is_empty() {
        return String::new();
    }
    format!("->addFlags({})", active.join(", "))
}

pub(crate) fn render_definition_field(
    field: &FieldSpec,
    imports: &ImportTable,
) -> Result<Vec<String>> {
    if field.kind == FIELD_LOCKED {
        if field.raw.trim().is_empty() {
            return Ok(Vec::new());
        }
        return Ok(vec![field.raw.trim().to_owned()]);
    }
    let required = if field.required {
        "new Required()".to_owned()
    } else {
        String::new()
    };
    let api_aware = if field.api_aware {
        render_api_aware(imports, &field.api_aware_sources)
    } else {
        String::new()
    };
    let ranking = if field.search_ranking > 0.0 {
        render_search_ranking(field.search_ranking, field.search_ranking_tokenize)
    } else {
        String::new()
    };
    let storage = quote_php(&field.storage_name);
    let property = quote_php(&field.property_name);
    let field_expression = |base: &str, values: &[&str]| -> String {
        with_modifiers(
            base,
            &field.modifiers_before_flags,
            &flags_suffix(definition_field_flag_values(field, values)),
            &field.modifiers_after_flags,
        )
    };
    let association_expression = |base: &str, values: &[&str]| -> String {
        with_modifiers(
            base,
            &field.association_before_flags,
            &flags_suffix(definition_association_flag_values(field, values)),
            &field.association_after_flags,
        )
    };
    let association_api = if field.association_api_aware {
        render_api_aware(imports, &field.association_api_aware_sources)
    } else {
        String::new()
    };
    let association_ranking = if field.association_search_rank > 0.0 {
        render_search_ranking(
            field.association_search_rank,
            field.association_search_tokenize,
        )
    } else {
        String::new()
    };
    if field.implementation.is_some() {
        return render_specialized_definition_field(
            field,
            imports,
            &field_expression,
            &[&required, &api_aware, &ranking],
        );
    }
    let simple = |base: String| -> Vec<String> {
        vec![field_expression(&base, &[&required, &api_aware, &ranking]) + ","]
    };
    Ok(match field.kind.as_str() {
        FIELD_ID => vec![
            field_expression(
                "(new IdField('id', 'id'))",
                &["new Required()", "new PrimaryKey()", &api_aware, &ranking],
            ) + ",",
        ],
        FIELD_BINARY_ID => simple(format!("(new IdField({storage}, {property}))")),
        FIELD_STRING => {
            let mut arguments = format!("{storage}, {property}");
            if field.max_length > 0 && field.max_length != 255 {
                arguments.push_str(&format!(", {}", field.max_length));
            }
            simple(format!("(new StringField({arguments}))"))
        }
        FIELD_ENUM => {
            let enumeration = imports.reference(&field.enum_class);
            simple(format!(
                "(new EnumField({storage}, {property}, {enumeration}::{}))",
                field.enum_case
            ))
        }
        FIELD_LONG_TEXT => simple(format!("(new LongTextField({storage}, {property}))")),
        FIELD_INT => {
            let mut arguments = format!("{storage}, {property}");
            if field.min.is_some() || field.max.is_some() {
                arguments.push_str(&format!(
                    ", {}, {}",
                    nullable_int(field.min),
                    nullable_int(field.max)
                ));
            }
            simple(format!("(new IntField({arguments}))"))
        }
        FIELD_FLOAT => simple(format!("(new FloatField({storage}, {property}))")),
        FIELD_BOOL => simple(format!("(new BoolField({storage}, {property}))")),
        FIELD_DATE => simple(format!("(new DateField({storage}, {property}))")),
        FIELD_DATE_TIME => simple(format!("(new DateTimeField({storage}, {property}))")),
        FIELD_JSON => {
            let mut arguments = format!("{storage}, {property}");
            let mut property_mapping = field.json_property_mapping_expression.trim().to_owned();
            let default_value = field.json_default_expression.trim();
            if !property_mapping.is_empty() || !default_value.is_empty() {
                if property_mapping.is_empty() {
                    property_mapping = "[]".to_owned();
                }
                arguments.push_str(", ");
                arguments.push_str(&property_mapping);
            }
            if !default_value.is_empty() {
                arguments.push_str(", ");
                arguments.push_str(default_value);
            }
            simple(format!("(new JsonField({arguments}))"))
        }
        FIELD_LIST => {
            let mut arguments = format!("{storage}, {property}");
            if !field.element_type_class.is_empty() {
                arguments.push_str(&format!(
                    ", {}::class",
                    imports.reference(&field.element_type_class)
                ));
            }
            simple(format!("(new ListField({arguments}))"))
        }
        FIELD_OBJECT => simple(format!("(new ObjectField({storage}, {property}))")),
        FIELD_BLOB => simple(format!("(new BlobField({storage}, {property}))")),
        FIELD_AUTO_INCREMENT => {
            vec![field_expression("(new AutoIncrementField())", &[&api_aware, &ranking]) + ","]
        }
        FIELD_VERSION => {
            vec![field_expression("(new VersionField())", &[&api_aware, &ranking]) + ","]
        }
        FIELD_REFERENCE_VERSION => {
            let target = imports.reference(&field.target_definition_class);
            simple(format!(
                "(new ReferenceVersionField({target}::class, {storage}))"
            ))
        }
        FIELD_CREATED_AT => {
            vec![field_expression("(new CreatedAtField())", &[&api_aware, &ranking]) + ","]
        }
        FIELD_UPDATED_AT => {
            vec![field_expression("(new UpdatedAtField())", &[&api_aware, &ranking]) + ","]
        }
        FIELD_FOREIGN_KEY => {
            let target = imports.reference(&field.target_definition_class);
            simple(format!(
                "(new FkField({storage}, {property}, {target}::class, {}))",
                quote_php(&default_string(&field.reference_field, "id"))
            ))
        }
        FIELD_MANY_TO_ONE | FIELD_ONE_TO_ONE => {
            let target = imports.reference(&field.target_definition_class);
            let mut result = Vec::new();
            if !field.uses_existing_column {
                result.push(
                    field_expression(
                        &format!(
                            "(new FkField({storage}, {}, {target}::class))",
                            quote_php(&field.foreign_key_property_name)
                        ),
                        &[&required, &api_aware, &ranking],
                    ) + ",",
                );
            }
            let mut association = render_to_one_constructor(
                &field.kind,
                &property,
                &storage,
                &target,
                &field.reference_field,
                field.association_autoload,
            );
            if let Some(conditional) = &field.conditional_association {
                let alternative = render_to_one_constructor(
                    &conditional.alternative_kind,
                    &property,
                    &storage,
                    &target,
                    &field.reference_field,
                    conditional.alternative_autoload,
                );
                association = format!(
                    "({} ? {association} : {alternative})",
                    conditional.condition_expression
                );
            }
            let delete_flag = render_delete_flag(field);
            let association = association_expression(
                &format!("({association})"),
                &[&delete_flag, &association_api, &association_ranking],
            );
            result.push(association + ",");
            result
        }
        FIELD_ONE_TO_MANY => {
            let target = imports.reference(&field.target_definition_class);
            let mut arguments = format!(
                "{property}, {target}::class, {}",
                quote_php(&field.reference_storage_name)
            );
            if !field.source_column.is_empty() && field.source_column != "id" {
                arguments.push_str(", ");
                arguments.push_str(&quote_php(&field.source_column));
            }
            let delete_flag = render_delete_flag(field);
            vec![
                association_expression(
                    &format!("(new OneToManyAssociationField({arguments}))"),
                    &[&delete_flag, &association_api, &association_ranking],
                ) + ",",
            ]
        }
        FIELD_MANY_TO_MANY => {
            let target = imports.reference(&field.target_definition_class);
            let mapping = imports.reference(&field.mapping_definition_class);
            let expression = format!(
                "new ManyToManyAssociationField({property}, {target}::class, {mapping}::class, {}, {}, {}, {})",
                quote_php(&field.mapping_local_column),
                quote_php(&field.mapping_reference_column),
                quote_php(&default_string(&field.source_column, "id")),
                quote_php(&default_string(&field.reference_field, "id"))
            );
            let delete_flag = render_delete_flag(field);
            vec![
                association_expression(
                    &format!("({expression})"),
                    &[&delete_flag, &association_api, &association_ranking],
                ) + ",",
            ]
        }
        FIELD_HIERARCHY => render_hierarchy_field(
            field,
            imports,
            &field_expression,
            &association_expression,
            &[&api_aware, &ranking],
            &[&association_api, &association_ranking],
            &property,
        ),
        _ => return Err(errorf!("unsupported field kind {}", go_quote(&field.kind))),
    })
}

fn render_hierarchy_field(
    field: &FieldSpec,
    imports: &ImportTable,
    field_expression: &dyn Fn(&str, &[&str]) -> String,
    association_expression: &dyn Fn(&str, &[&str]) -> String,
    field_values: &[&str],
    association_values: &[&str],
    property: &str,
) -> Vec<String> {
    let target = imports.reference(&field.target_definition_class);
    let mut result = vec![
        field_expression(
            &format!("(new ParentFkField({target}::class))"),
            field_values,
        ) + ",",
    ];
    if field.hierarchy_version_aware {
        let mut version_flags = Vec::new();
        if field.hierarchy_version_api_aware {
            version_flags.push(render_api_aware(
                imports,
                &field.hierarchy_version_api_sources,
            ));
        }
        version_flags.push("new Required()".to_owned());
        version_flags.extend(field.hierarchy_version_flags.iter().cloned());
        if field.hierarchy_version_inherited {
            version_flags.push(render_inherited_flag(&field.hierarchy_version_inherited_fk));
        }
        version_flags.extend(render_behavior_flags(
            field.hierarchy_version_behavior.as_ref(),
        ));
        version_flags.extend(render_metadata_flags(
            field.hierarchy_version_metadata.as_ref(),
        ));
        append_write_protected_flag(
            &mut version_flags,
            field.hierarchy_version_protected,
            &field.hierarchy_version_write_scopes,
        );
        let version = with_modifiers(
            &format!("(new ReferenceVersionField({target}::class, 'parent_version_id'))"),
            &field.hierarchy_version_before,
            &flags_suffix(version_flags),
            &field.hierarchy_version_after,
        );
        result.push(version + ",");
    }
    let parent = association_expression(
        &format!("(new ParentAssociationField({target}::class, 'id'))"),
        association_values,
    );
    let mut children_flags = field.hierarchy_children_flags.clone();
    if field.hierarchy_children_api_aware {
        children_flags.insert(
            0,
            render_api_aware(imports, &field.hierarchy_children_api_sources),
        );
    }
    if field.hierarchy_children_rank > 0.0 {
        children_flags.insert(
            0,
            render_search_ranking(
                field.hierarchy_children_rank,
                field.hierarchy_children_tokenize,
            ),
        );
    }
    if field.hierarchy_children_inherited {
        children_flags.push(render_inherited_flag(
            &field.hierarchy_children_inherited_fk,
        ));
    }
    if !field.hierarchy_children_reverse.is_empty() {
        children_flags.push(format!(
            "new ReverseInherited({})",
            quote_php(&field.hierarchy_children_reverse)
        ));
    }
    children_flags.extend(render_behavior_flags(
        field.hierarchy_children_behavior.as_ref(),
    ));
    children_flags.extend(render_metadata_flags(
        field.hierarchy_children_metadata.as_ref(),
    ));
    append_write_protected_flag(
        &mut children_flags,
        field.hierarchy_children_protected,
        &field.hierarchy_children_write_scopes,
    );
    let mut children = format!("(new ChildrenAssociationField({target}::class");
    if field.property_name != "children" {
        children.push_str(", ");
        children.push_str(property);
    }
    children.push_str("))");
    let children = with_modifiers(
        &children,
        &field.hierarchy_children_before,
        &flags_suffix(children_flags),
        &field.hierarchy_children_after,
    );
    result.push(parent + ",");
    result.push(children + ",");
    result
}

fn render_to_one_constructor(
    kind: &str,
    property: &str,
    storage: &str,
    target: &str,
    reference_field: &str,
    autoload: bool,
) -> String {
    let reference = quote_php(&default_string(reference_field, "id"));
    if kind == FIELD_ONE_TO_ONE {
        return format!(
            "new OneToOneAssociationField({property}, {storage}, {reference}, {target}::class, {autoload})"
        );
    }
    format!(
        "new ManyToOneAssociationField({property}, {storage}, {target}::class, {reference}, {autoload})"
    )
}

fn render_specialized_definition_field(
    field: &FieldSpec,
    imports: &ImportTable,
    field_expression: &dyn Fn(&str, &[&str]) -> String,
    values: &[&str],
) -> Result<Vec<String>> {
    let Some(implementation) = field
        .implementation
        .as_ref()
        .filter(|implementation| !implementation.class.is_empty())
    else {
        return Err(errorf!(
            "specialized field {} has no implementation class",
            go_quote(&field.id)
        ));
    };
    let mut arguments = Vec::new();
    match implementation.constructor_mode.as_str() {
        CONSTRUCTOR_STORAGE_PROPERTY => {
            arguments.push(quote_php(&field.storage_name));
            arguments.push(quote_php(&field.property_name));
            let mut additional = implementation.additional_arguments.clone();
            if implementation.max_length_argument {
                if additional.is_empty() {
                    if field.max_length != 64 {
                        additional.push(field.max_length.to_string());
                    }
                } else {
                    additional[0] = field.max_length.to_string();
                }
            }
            arguments.extend(additional);
        }
        CONSTRUCTOR_FIXED => arguments.extend(implementation.fixed_arguments.iter().cloned()),
        _ => {
            return Err(errorf!(
                "specialized field {} has unsupported constructor mode {}",
                go_quote(&field.id),
                go_quote(&implementation.constructor_mode)
            ));
        }
    }
    let expression = format!(
        "(new {}({}))",
        imports.reference(&implementation.class),
        arguments.join(", ")
    );
    Ok(vec![field_expression(&expression, values) + ","])
}

fn definition_field_flag_values(field: &FieldSpec, values: &[&str]) -> Vec<String> {
    let mut result: Vec<String> = values.iter().map(|value| (*value).to_owned()).collect();
    if field.primary && field.kind != FIELD_ID && field.kind != FIELD_VERSION {
        result.push("new PrimaryKey()".to_owned());
    }
    if field.inherited {
        result.push(render_inherited_flag(&field.inherited_foreign_key));
    }
    result.extend(render_behavior_flags(field.behavior.as_ref()));
    result.extend(render_metadata_flags(field.metadata.as_ref()));
    append_write_protected_flag(
        &mut result,
        field.write_protected,
        &field.write_protected_scopes,
    );
    result.extend(field.preserved_flags.iter().cloned());
    result
}

fn definition_association_flag_values(field: &FieldSpec, values: &[&str]) -> Vec<String> {
    let mut result: Vec<String> = values.iter().map(|value| (*value).to_owned()).collect();
    if field.association_inherited {
        result.push(render_inherited_flag(&field.association_inherited_fk));
    }
    if !field.reverse_inherited_property.is_empty() {
        result.push(format!(
            "new ReverseInherited({})",
            quote_php(&field.reverse_inherited_property)
        ));
    }
    result.extend(render_behavior_flags(field.association_behavior.as_ref()));
    result.extend(render_metadata_flags(field.association_metadata.as_ref()));
    append_write_protected_flag(
        &mut result,
        field.association_write_protected,
        &field.association_write_scopes,
    );
    result.extend(field.association_flags.iter().cloned());
    result
}

fn append_write_protected_flag(flags: &mut Vec<String>, enabled: bool, scopes: &[String]) {
    if enabled {
        flags.push(render_write_protected_flag(scopes));
    }
}

pub(crate) fn render_search_ranking(ranking: f64, tokenize: Option<bool>) -> String {
    let mut arguments = format_float_g(ranking);
    if let Some(tokenize) = tokenize {
        arguments.push_str(&format!(", {tokenize}"));
    }
    format!("new SearchRanking({arguments})")
}

pub(crate) fn render_api_aware(imports: &ImportTable, sources: &[String]) -> String {
    let arguments: Vec<String> = sources
        .iter()
        .map(|source| format!("{}::class", imports.reference(source)))
        .collect();
    format!("new ApiAware({})", arguments.join(", "))
}

fn render_delete_flag(field: &FieldSpec) -> String {
    match field.delete_behavior.as_str() {
        DELETE_CASCADE => match field.delete_clone_relevant {
            Some(value) => format!("new CascadeDelete({value})"),
            None => "new CascadeDelete()".to_owned(),
        },
        DELETE_SET_NULL => match field.delete_enforced_by_constraint {
            Some(value) => format!("new SetNullOnDelete({value})"),
            None => "new SetNullOnDelete()".to_owned(),
        },
        DELETE_RESTRICT => "new RestrictDelete()".to_owned(),
        _ => String::new(),
    }
}

pub(crate) fn render_behavior_flags(behavior: Option<&FieldBehavior>) -> Vec<String> {
    let Some(behavior) = behavior else {
        return Vec::new();
    };
    let mut flags = Vec::with_capacity(3);
    if behavior.runtime {
        let mut arguments = behavior.runtime_dependencies_expression.trim().to_owned();
        if arguments.is_empty() && !behavior.runtime_dependencies.is_empty() {
            let dependencies: Vec<String> = behavior
                .runtime_dependencies
                .iter()
                .map(|dependency| quote_php(dependency))
                .collect();
            arguments = format!("[{}]", dependencies.join(", "));
        }
        flags.push(format!("new Runtime({arguments})"));
    }
    if behavior.computed {
        flags.push("new Computed()".to_owned());
    }
    if behavior.no_constraint {
        flags.push("new NoConstraint()".to_owned());
    }
    flags
}

pub(crate) fn render_metadata_flags(metadata: Option<&FieldMetadata>) -> Vec<String> {
    let Some(metadata) = metadata else {
        return Vec::new();
    };
    let mut flags = Vec::new();
    if let Some(allow_html) = metadata.allow_html {
        flags.push(
            if allow_html {
                "new AllowHtml()"
            } else {
                "new AllowHtml(false)"
            }
            .to_owned(),
        );
    }
    if metadata.allow_empty_string {
        flags.push("new AllowEmptyString()".to_owned());
    }
    if metadata.as_array {
        flags.push("new AsArray()".to_owned());
    }
    if metadata.immutable {
        flags.push("new Immutable()".to_owned());
    }
    if !metadata.since.is_empty() {
        flags.push(format!("new Since({})", quote_php(&metadata.since)));
    }
    if let Some(deprecated) = &metadata.deprecated {
        let mut arguments = vec![
            quote_php(&deprecated.deprecated_since),
            quote_php(&deprecated.will_be_removed_in),
        ];
        if !deprecated.replaced_by.is_empty() {
            arguments.push(quote_php(&deprecated.replaced_by));
        }
        flags.push(format!("new Deprecated({})", arguments.join(", ")));
    }
    if metadata.ignore_in_open_api_schema {
        flags.push("new IgnoreInOpenapiSchema()".to_owned());
    }
    if metadata.ignore_in_unused_media_search {
        flags.push("new IgnoreInUnusedMediaSearch()".to_owned());
    }
    if metadata.api_criteria_aware {
        flags.push("new ApiCriteriaAware()".to_owned());
    }
    if !metadata.rule_areas.is_empty() {
        flags.push(format!(
            "new RuleAreas({})",
            render_string_arguments(&metadata.rule_areas)
        ));
    }
    if let Some(choice) = &metadata.choice {
        let mut arguments = format!("[{}]", choice.values.join(", "));
        if let Some(strict) = choice.strict {
            arguments.push_str(&format!(", strict: {strict}"));
        }
        flags.push(format!("new Choice({arguments})"));
    }
    if metadata.do_not_use_context {
        flags.push("new DoNotUseContext()".to_owned());
    }
    if metadata.extension {
        flags.push("new Extension()".to_owned());
    }
    flags
}

fn render_translated_field(field: &FieldSpec, imports: &ImportTable) -> String {
    let mut arguments = quote_php(&field.property_name);
    if field.translation_use_for_sort {
        arguments.push_str(", true");
    }
    let mut api_aware = field.api_aware;
    let mut api_aware_sources = &field.api_aware_sources;
    if let Some(value) = field.translation_api_aware {
        api_aware = value;
        api_aware_sources = &field.translation_api_aware_sources;
    }
    let search_ranking = field
        .translation_search_rank
        .unwrap_or(field.search_ranking);
    let mut flags = Vec::new();
    if api_aware {
        flags.push(render_api_aware(imports, api_aware_sources));
    }
    if search_ranking > 0.0 {
        flags.push(render_search_ranking(
            search_ranking,
            field.translation_search_tokenize,
        ));
    }
    if field.translation_inherited {
        flags.push(render_inherited_flag(&field.translation_inherited_fk));
    }
    if field.translation_write_protected {
        flags.push(render_write_protected_flag(&field.translation_write_scopes));
    }
    flags.extend(render_behavior_flags(field.translation_behavior.as_ref()));
    flags.extend(render_metadata_flags(field.translation_metadata.as_ref()));
    flags.extend(field.translation_flags.iter().cloned());
    let flag_suffix = if flags.is_empty() {
        String::new()
    } else {
        format!("->addFlags({})", flags.join(", "))
    };
    with_modifiers(
        &format!("(new TranslatedField({arguments}))"),
        &field.translation_before_flags,
        &flag_suffix,
        &field.translation_after_flags,
    ) + ","
}

pub(crate) fn render_inherited_flag(foreign_key: &str) -> String {
    if foreign_key.is_empty() {
        "new Inherited()".to_owned()
    } else {
        format!("new Inherited({})", quote_php(foreign_key))
    }
}

pub(crate) fn render_write_protected_flag(scopes: &[String]) -> String {
    format!("new WriteProtected({})", render_string_arguments(scopes))
}

fn render_translations_association(translation: &TranslationSpec, imports: &ImportTable) -> String {
    let mut arguments = format!(
        "{}::class, {}",
        imports.reference(&translation.definition_class),
        quote_php(&translation.parent_storage_name)
    );
    if translation.association_property != "translations"
        || translation.association_local_field != "id"
    {
        arguments.push_str(&format!(
            ", {}, {}",
            quote_php(&translation.association_property),
            quote_php(&translation.association_local_field)
        ));
    }
    let mut flags = Vec::new();
    if translation.association_required {
        flags.push("new Required()".to_owned());
    }
    if translation.association_api_aware {
        flags.push(render_api_aware(
            imports,
            &translation.association_api_aware_sources,
        ));
    }
    if translation.association_inherited {
        flags.push(render_inherited_flag(&translation.association_inherited_fk));
    }
    if !translation.reverse_inherited_property.is_empty() {
        flags.push(format!(
            "new ReverseInherited({})",
            quote_php(&translation.reverse_inherited_property)
        ));
    }
    if translation.association_write_protected {
        flags.push(render_write_protected_flag(
            &translation.association_write_scopes,
        ));
    }
    flags.extend(render_behavior_flags(
        translation.association_behavior.as_ref(),
    ));
    flags.extend(render_metadata_flags(
        translation.association_metadata.as_ref(),
    ));
    flags.extend(translation.association_flags.iter().cloned());
    let flag_suffix = if flags.is_empty() {
        String::new()
    } else {
        format!("->addFlags({})", flags.join(", "))
    };
    with_modifiers(
        &format!("(new TranslationsAssociationField({arguments}))"),
        &translation.association_before_flags,
        &flag_suffix,
        &translation.association_after_flags,
    ) + ","
}

pub(crate) fn render_entity_field(
    field: &FieldSpec,
    imports: &ImportTable,
) -> Result<(Vec<String>, Vec<String>)> {
    if matches!(
        field.kind.as_str(),
        FIELD_ID
            | FIELD_VERSION
            | FIELD_REFERENCE_VERSION
            | FIELD_CREATED_AT
            | FIELD_UPDATED_AT
            | FIELD_LOCKED
    ) {
        return Ok((Vec::new(), Vec::new()));
    }
    if let Some(implementation) = &field.implementation
        && (!implementation.manage_entity || !implementation.entity_trait.is_empty())
    {
        return Ok((Vec::new(), Vec::new()));
    }
    if field.kind == FIELD_MANY_TO_ONE || field.kind == FIELD_ONE_TO_ONE {
        let (fk_type, fk_default) = if field.required {
            ("string", "")
        } else {
            ("?string", " = null")
        };
        let target = imports.reference(&field.target_entity_class);
        let mut properties = vec![format!(
            "protected ?{target} ${} = null;",
            field.property_name
        )];
        let mut methods = behavior_accessors(
            &field.property_name,
            &format!("?{target}"),
            false,
            field.association_behavior.as_ref(),
        );
        if !field.uses_existing_column {
            properties.insert(
                0,
                format!(
                    "protected {fk_type} ${}{fk_default};",
                    field.foreign_key_property_name
                ),
            );
            let mut fk_methods = behavior_accessors(
                &field.foreign_key_property_name,
                fk_type,
                false,
                field.behavior.as_ref(),
            );
            fk_methods.extend(methods);
            methods = fk_methods;
        }
        return Ok((properties, methods));
    }
    if field.kind == FIELD_ONE_TO_MANY || field.kind == FIELD_MANY_TO_MANY {
        let target = imports.reference(&field.target_collection_class);
        return Ok((
            vec![format!(
                "protected ?{target} ${} = null;",
                field.property_name
            )],
            behavior_accessors(
                &field.property_name,
                &format!("?{target}"),
                false,
                field.association_behavior.as_ref(),
            ),
        ));
    }
    if field.kind == FIELD_HIERARCHY {
        let entity = imports.reference(&field.target_entity_class);
        let collection = imports.reference(&field.target_collection_class);
        let properties = vec![
            "protected ?string $parentId = null;".to_owned(),
            format!(
                "protected ?{entity} ${} = null;",
                field.hierarchy_parent_property
            ),
            format!("protected ?{collection} ${} = null;", field.property_name),
        ];
        let mut methods = behavior_accessors("parentId", "?string", false, field.behavior.as_ref());
        methods.extend(behavior_accessors(
            &field.hierarchy_parent_property,
            &format!("?{entity}"),
            false,
            field.association_behavior.as_ref(),
        ));
        methods.extend(behavior_accessors(
            &field.property_name,
            &format!("?{collection}"),
            false,
            field.hierarchy_children_behavior.as_ref(),
        ));
        return Ok((properties, methods));
    }
    let (mut php_type, mut boolean) = entity_field_php_type(field, imports)?;
    if let Some(implementation) = &field.implementation {
        if !implementation.entity_type.is_empty() {
            php_type = entity_implementation_type(&implementation.entity_type, imports);
        }
        boolean = implementation.entity_boolean_getter;
    }
    let mut default_value = "";
    if field.translated || !field.required {
        php_type = nullable_php_type(&php_type);
        default_value = " = null";
    }
    let property = format!(
        "protected {php_type} ${}{default_value};",
        field.property_name
    );
    let mut methods = behavior_accessors(
        &field.property_name,
        &php_type,
        boolean,
        field.behavior.as_ref(),
    );
    if field
        .implementation
        .as_ref()
        .is_some_and(|implementation| implementation.implicit_computed)
    {
        methods.truncate(1);
    }
    Ok((vec![property], methods))
}

fn entity_implementation_type(value: &str, imports: &ImportTable) -> String {
    value
        .split('|')
        .map(|part| {
            let part = trim_class(part);
            if is_builtin_entity_type(part) {
                part.to_owned()
            } else {
                imports.reference(part)
            }
        })
        .collect::<Vec<_>>()
        .join("|")
}

fn nullable_php_type(value: &str) -> String {
    if value == "mixed" || value.contains("null") {
        return value.to_owned();
    }
    if value.contains('|') {
        return format!("{value}|null");
    }
    format!("?{value}")
}

fn behavior_accessors(
    property: &str,
    php_type: &str,
    boolean: bool,
    behavior: Option<&FieldBehavior>,
) -> Vec<String> {
    let mut result = accessors(property, php_type, boolean);
    if behavior.is_some_and(|behavior| behavior.runtime || behavior.computed) {
        result.truncate(1);
    }
    result
}

fn accessors(property: &str, php_type: &str, boolean: bool) -> Vec<String> {
    let name = exported(property);
    let method = if boolean {
        format!("is{name}")
    } else {
        format!("get{name}")
    };
    vec![
        format!("public function {method}(): {php_type}\n{{\n    return $this->{property};\n}}"),
        format!(
            "public function set{name}({php_type} ${property}): void\n{{\n    $this->{property} = ${property};\n}}"
        ),
    ]
}

fn entity_php_type(kind: &str) -> Result<(String, bool)> {
    Ok(match kind {
        FIELD_STRING | FIELD_LONG_TEXT | FIELD_BINARY_ID | FIELD_FOREIGN_KEY => {
            ("string".to_owned(), false)
        }
        FIELD_BLOB => ("object".to_owned(), false),
        FIELD_INT | FIELD_AUTO_INCREMENT => ("int".to_owned(), false),
        FIELD_FLOAT => ("float".to_owned(), false),
        FIELD_BOOL => ("bool".to_owned(), true),
        FIELD_DATE | FIELD_DATE_TIME => ("\\DateTimeInterface".to_owned(), false),
        FIELD_JSON | FIELD_LIST | FIELD_OBJECT => ("array".to_owned(), false),
        _ => {
            return Err(errorf!(
                "field kind {} has no PHP entity type",
                go_quote(kind)
            ));
        }
    })
}

fn entity_field_php_type(field: &FieldSpec, imports: &ImportTable) -> Result<(String, bool)> {
    if field.kind == FIELD_ENUM {
        if field.enum_class.trim().is_empty() {
            return Err(errorf!(
                "enum field {} has no enum class",
                go_quote(&field.property_name)
            ));
        }
        return Ok((imports.reference(&field.enum_class), false));
    }
    entity_php_type(&field.kind)
}

const FIELD_NAMESPACE: &str = "Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\";
const FLAG_NAMESPACE: &str = "Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\Flag\\";

fn field_class(kind: &str) -> Option<&'static str> {
    Some(match kind {
        FIELD_ID | FIELD_BINARY_ID => "IdField",
        FIELD_STRING => "StringField",
        FIELD_ENUM => "EnumField",
        FIELD_LONG_TEXT => "LongTextField",
        FIELD_INT => "IntField",
        FIELD_FLOAT => "FloatField",
        FIELD_BOOL => "BoolField",
        FIELD_DATE => "DateField",
        FIELD_DATE_TIME => "DateTimeField",
        FIELD_JSON => "JsonField",
        FIELD_LIST => "ListField",
        FIELD_OBJECT => "ObjectField",
        FIELD_BLOB => "BlobField",
        FIELD_AUTO_INCREMENT => "AutoIncrementField",
        FIELD_CREATED_AT => "CreatedAtField",
        FIELD_UPDATED_AT => "UpdatedAtField",
        FIELD_VERSION => "VersionField",
        FIELD_REFERENCE_VERSION => "ReferenceVersionField",
        FIELD_FOREIGN_KEY => "FkField",
        FIELD_MANY_TO_ONE => "ManyToOneAssociationField",
        FIELD_ONE_TO_ONE => "OneToOneAssociationField",
        FIELD_ONE_TO_MANY => "OneToManyAssociationField",
        FIELD_MANY_TO_MANY => "ManyToManyAssociationField",
        FIELD_HIERARCHY => "ParentFkField",
        _ => return None,
    })
}

fn need_field(need: &mut BTreeSet<String>, name: &str) {
    need.insert(format!("{FIELD_NAMESPACE}{name}"));
}

fn need_flag(need: &mut BTreeSet<String>, name: &str) {
    need.insert(format!("{FLAG_NAMESPACE}{name}"));
}

pub(crate) fn definition_imports(spec: &EntitySpec) -> Vec<String> {
    let mut imports = Vec::new();
    if spec.definition_kind != DEFINITION_BULK_EXTENSION {
        imports.push("Shopware\\Core\\Framework\\DataAbstractionLayer\\FieldCollection".to_owned());
    }
    match spec.definition_kind.as_str() {
        DEFINITION_BULK_EXTENSION => {
            imports.push(
                "Shopware\\Core\\Framework\\DataAbstractionLayer\\BulkEntityExtension".to_owned(),
            );
            for target in &spec.bulk_extensions {
                imports.push(target.extended_definition_class.clone());
            }
        }
        DEFINITION_EXTENSION => {
            imports.push(
                "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityExtension".to_owned(),
            );
            imports.push(format!("{FLAG_NAMESPACE}Extension"));
            imports.push(spec.extended_definition_class.clone());
        }
        DEFINITION_MAPPING => {
            imports.push(
                "Shopware\\Core\\Framework\\DataAbstractionLayer\\MappingEntityDefinition"
                    .to_owned(),
            );
            if !spec.entity_class.is_empty() && !spec.collection_class.is_empty() {
                imports.push(spec.entity_class.clone());
                imports.push(spec.collection_class.clone());
            }
        }
        _ => {
            imports.push(
                "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition".to_owned(),
            );
            imports.push(spec.entity_class.clone());
            imports.push(spec.collection_class.clone());
        }
    }
    if (spec.definition_kind == DEFINITION_ENTITY || spec.definition_kind == DEFINITION_EXTENSION)
        && (spec.read_protected
            || spec.write_protected
            || !spec.preserved_protections.is_empty()
            || !spec.protection_method_raw.trim().is_empty())
    {
        imports.push(
            "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityProtection\\EntityProtectionCollection"
                .to_owned(),
        );
    }
    if spec.read_protected {
        imports.push(
            "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityProtection\\ReadProtection"
                .to_owned(),
        );
    }
    if spec.write_protected {
        imports.push(
            "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityProtection\\WriteProtection"
                .to_owned(),
        );
    }
    if let Some(metadata) = &spec.definition_metadata
        && !metadata.hydrator_class.is_empty()
    {
        imports.push(metadata.hydrator_class.clone());
    }
    if let Some(behavior) = &spec.definition_behavior {
        imports.push(behavior.parent_definition_class.clone());
        if !behavior.restrict_delete_meta_properties.is_empty() {
            imports.push(format!("{FIELD_NAMESPACE}Field"));
        }
    }
    let mut need = BTreeSet::new();
    if let Some(behavior) = &spec.definition_behavior {
        for field in behavior.default_fields.iter().chain(&behavior.base_fields) {
            collect_definition_field_imports(&mut need, field);
        }
    }
    for field in &spec.fields {
        if field.translation_definition_only || is_implicit_timestamp_field(spec, &field.kind) {
            continue;
        }
        collect_definition_field_imports(&mut need, field);
    }
    for target in &spec.bulk_extensions {
        for field in &target.fields {
            collect_definition_field_imports(&mut need, &with_entity_extension_flag(field.clone()));
        }
    }
    collect_translation_association_imports(&mut need, spec.translation.as_ref());
    collect_field_modification_imports(&mut need, &spec.field_modifications);
    imports.extend(need);
    imports
}

fn collect_definition_field_imports(need: &mut BTreeSet<String>, field: &FieldSpec) {
    if field.translated {
        collect_translated_facade_imports(need, field);
        return;
    }
    if let Some(implementation) = field
        .implementation
        .as_ref()
        .filter(|implementation| !implementation.class.is_empty())
    {
        need.insert(implementation.class.clone());
    } else if let Some(class) = field_class(&field.kind) {
        need_field(need, class);
    }
    collect_hierarchy_imports(need, field);
    collect_relation_imports(need, field);
    if field.kind == FIELD_REFERENCE_VERSION {
        need.insert(field.target_definition_class.clone());
    }
    if field.kind == FIELD_LIST && !field.element_type_class.is_empty() {
        need.insert(field.element_type_class.clone());
    }
    if field.kind == FIELD_ENUM && !field.enum_class.is_empty() {
        need.insert(field.enum_class.clone());
    }
    collect_definition_field_flag_imports(need, field);
}

fn collect_translated_facade_imports(need: &mut BTreeSet<String>, field: &FieldSpec) {
    need_field(need, "TranslatedField");
    if field.api_aware || field.translation_api_aware == Some(true) {
        need_flag(need, "ApiAware");
    }
    collect_class_imports(need, &field.api_aware_sources);
    collect_class_imports(need, &field.translation_api_aware_sources);
    if field.search_ranking > 0.0 || field.translation_search_rank.is_some_and(|rank| rank > 0.0) {
        need_flag(need, "SearchRanking");
    }
    if field.translation_inherited {
        need_flag(need, "Inherited");
    }
    if field.translation_write_protected {
        need_flag(need, "WriteProtected");
    }
    collect_behavior_imports(need, field.translation_behavior.as_ref());
    collect_metadata_imports(need, field.translation_metadata.as_ref());
}

fn collect_hierarchy_imports(need: &mut BTreeSet<String>, field: &FieldSpec) {
    if field.kind != FIELD_HIERARCHY {
        return;
    }
    need_field(need, "ParentAssociationField");
    need_field(need, "ChildrenAssociationField");
    need.insert(field.target_definition_class.clone());
    if field.hierarchy_version_aware {
        need_field(need, "ReferenceVersionField");
        need_flag(need, "Required");
    }
    collect_behavior_imports(need, field.hierarchy_children_behavior.as_ref());
    collect_behavior_imports(need, field.hierarchy_version_behavior.as_ref());
    collect_metadata_imports(need, field.hierarchy_children_metadata.as_ref());
    collect_metadata_imports(need, field.hierarchy_version_metadata.as_ref());
}

fn collect_relation_imports(need: &mut BTreeSet<String>, field: &FieldSpec) {
    let kind = field.kind.as_str();
    if kind == FIELD_FOREIGN_KEY && !field.target_definition_class.is_empty() {
        need.insert(field.target_definition_class.clone());
    }
    if (kind == FIELD_MANY_TO_ONE || kind == FIELD_ONE_TO_ONE) && !field.uses_existing_column {
        need_field(need, "FkField");
        need.insert(field.target_definition_class.clone());
    }
    if matches!(
        kind,
        FIELD_ONE_TO_MANY | FIELD_MANY_TO_ONE | FIELD_ONE_TO_ONE | FIELD_MANY_TO_MANY
    ) {
        need.insert(field.target_definition_class.clone());
        match field.delete_behavior.as_str() {
            DELETE_CASCADE => need_flag(need, "CascadeDelete"),
            DELETE_SET_NULL => need_flag(need, "SetNullOnDelete"),
            DELETE_RESTRICT => need_flag(need, "RestrictDelete"),
            _ => {}
        }
    }
    if kind == FIELD_MANY_TO_MANY {
        need.insert(field.target_definition_class.clone());
        need.insert(field.mapping_definition_class.clone());
    }
    if let Some(conditional) = &field.conditional_association {
        match conditional.alternative_kind.as_str() {
            FIELD_MANY_TO_ONE => need_field(need, "ManyToOneAssociationField"),
            FIELD_ONE_TO_ONE => need_field(need, "OneToOneAssociationField"),
            _ => {}
        }
    }
}

fn collect_definition_field_flag_imports(need: &mut BTreeSet<String>, field: &FieldSpec) {
    if field.required || field.kind == FIELD_ID {
        need_flag(need, "Required");
    }
    if field.kind == FIELD_ID || (field.primary && field.kind != FIELD_VERSION) {
        need_flag(need, "PrimaryKey");
    }
    if field.api_aware
        || field.association_api_aware
        || field.hierarchy_children_api_aware
        || field.hierarchy_version_api_aware
    {
        need_flag(need, "ApiAware");
    }
    collect_class_imports(need, &field.api_aware_sources);
    collect_class_imports(need, &field.association_api_aware_sources);
    collect_class_imports(need, &field.hierarchy_children_api_sources);
    collect_class_imports(need, &field.hierarchy_version_api_sources);
    if field.search_ranking > 0.0
        || field.association_search_rank > 0.0
        || field.hierarchy_children_rank > 0.0
    {
        need_flag(need, "SearchRanking");
    }
    if field.inherited
        || field.association_inherited
        || field.hierarchy_children_inherited
        || field.hierarchy_version_inherited
    {
        need_flag(need, "Inherited");
    }
    if !field.reverse_inherited_property.is_empty() || !field.hierarchy_children_reverse.is_empty()
    {
        need_flag(need, "ReverseInherited");
    }
    if field.write_protected
        || field.association_write_protected
        || field.hierarchy_children_protected
        || field.hierarchy_version_protected
    {
        need_flag(need, "WriteProtected");
    }
    collect_behavior_imports(need, field.behavior.as_ref());
    collect_behavior_imports(need, field.association_behavior.as_ref());
    collect_metadata_imports(need, field.metadata.as_ref());
    collect_metadata_imports(need, field.association_metadata.as_ref());
}

fn collect_behavior_imports(need: &mut BTreeSet<String>, behavior: Option<&FieldBehavior>) {
    let Some(behavior) = behavior else {
        return;
    };
    if behavior.runtime {
        need_flag(need, "Runtime");
    }
    if behavior.computed {
        need_flag(need, "Computed");
    }
    if behavior.no_constraint {
        need_flag(need, "NoConstraint");
    }
}

fn collect_metadata_imports(need: &mut BTreeSet<String>, metadata: Option<&FieldMetadata>) {
    let Some(metadata) = metadata else {
        return;
    };
    let flags = [
        (metadata.allow_html.is_some(), "AllowHtml"),
        (metadata.allow_empty_string, "AllowEmptyString"),
        (metadata.as_array, "AsArray"),
        (metadata.immutable, "Immutable"),
        (!metadata.since.is_empty(), "Since"),
        (metadata.deprecated.is_some(), "Deprecated"),
        (metadata.ignore_in_open_api_schema, "IgnoreInOpenapiSchema"),
        (
            metadata.ignore_in_unused_media_search,
            "IgnoreInUnusedMediaSearch",
        ),
        (metadata.api_criteria_aware, "ApiCriteriaAware"),
        (!metadata.rule_areas.is_empty(), "RuleAreas"),
        (metadata.choice.is_some(), "Choice"),
        (metadata.do_not_use_context, "DoNotUseContext"),
        (metadata.extension, "Extension"),
    ];
    for (enabled, name) in flags {
        if enabled {
            need_flag(need, name);
        }
    }
}

fn collect_translation_association_imports(
    need: &mut BTreeSet<String>,
    translation: Option<&TranslationSpec>,
) {
    let Some(translation) = translation.filter(|translation| translation.enabled) else {
        return;
    };
    need_field(need, "TranslationsAssociationField");
    need.insert(translation.definition_class.clone());
    if translation.association_required {
        need_flag(need, "Required");
    }
    if translation.association_api_aware {
        need_flag(need, "ApiAware");
    }
    collect_class_imports(need, &translation.association_api_aware_sources);
    if translation.association_inherited {
        need_flag(need, "Inherited");
    }
    if !translation.reverse_inherited_property.is_empty() {
        need_flag(need, "ReverseInherited");
    }
    if translation.association_write_protected {
        need_flag(need, "WriteProtected");
    }
    collect_behavior_imports(need, translation.association_behavior.as_ref());
    collect_metadata_imports(need, translation.association_metadata.as_ref());
}

pub(crate) fn collect_class_imports(need: &mut BTreeSet<String>, classes: &[String]) {
    for class in classes {
        let class = trim_class(class);
        if !class.is_empty() {
            need.insert(class.to_owned());
        }
    }
}

fn push_entity_types(imports: &mut Vec<String>, entity_type: &str) {
    for part in entity_type.split('|') {
        let part = trim_class(part);
        if !part.is_empty() && !is_builtin_entity_type(part) {
            imports.push(part.to_owned());
        }
    }
}

pub(crate) fn entity_imports(spec: &EntitySpec) -> Vec<String> {
    let mut imports = vec![
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Entity".to_owned(),
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityIdTrait".to_owned(),
    ];
    imports.extend(entity_implementation_traits(spec, false));
    if let Some(translation) = spec
        .translation
        .as_ref()
        .filter(|translation| translation.enabled)
    {
        imports.push(translation.collection_class.clone());
    }
    for field in schema_definition_fields(spec).iter() {
        if field.kind == FIELD_ENUM && !field.enum_class.is_empty() {
            imports.push(field.enum_class.clone());
        }
        if let Some(implementation) = field
            .implementation
            .as_ref()
            .filter(|implementation| implementation.manage_entity)
        {
            push_entity_types(&mut imports, &implementation.entity_type);
        }
        if (field.kind == FIELD_MANY_TO_ONE || field.kind == FIELD_ONE_TO_ONE)
            && !field.target_entity_class.is_empty()
        {
            imports.push(field.target_entity_class.clone());
        }
        if (field.kind == FIELD_ONE_TO_MANY || field.kind == FIELD_MANY_TO_MANY)
            && !field.target_collection_class.is_empty()
        {
            imports.push(field.target_collection_class.clone());
        }
        if field.kind == FIELD_HIERARCHY {
            imports.push(field.target_entity_class.clone());
            imports.push(field.target_collection_class.clone());
        }
    }
    imports
}

pub(crate) fn translation_entity_imports(spec: &EntitySpec) -> Vec<String> {
    let mut imports = vec![
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\TranslationEntity".to_owned(),
        spec.entity_class.clone(),
    ];
    imports.extend(entity_implementation_traits(spec, true));
    if let Some(behavior) = spec
        .translation
        .as_ref()
        .and_then(|translation| translation.definition_behavior.as_ref())
    {
        for field in behavior.default_fields.iter().chain(&behavior.base_fields) {
            if let Some(implementation) = field
                .implementation
                .as_ref()
                .filter(|implementation| implementation.manage_entity)
            {
                push_entity_types(&mut imports, &implementation.entity_type);
            }
            if (field.kind == FIELD_MANY_TO_ONE || field.kind == FIELD_ONE_TO_ONE)
                && !field.target_entity_class.is_empty()
            {
                imports.push(field.target_entity_class.clone());
            }
            if (field.kind == FIELD_ONE_TO_MANY || field.kind == FIELD_MANY_TO_MANY)
                && !field.target_collection_class.is_empty()
            {
                imports.push(field.target_collection_class.clone());
            }
        }
    }
    for field in &spec.fields {
        if !field.translated {
            continue;
        }
        if field.kind == FIELD_ENUM && !field.enum_class.is_empty() {
            imports.push(field.enum_class.clone());
        }
        if let Some(implementation) = &field.implementation {
            push_entity_types(&mut imports, &implementation.entity_type);
        }
    }
    imports
}

pub(crate) fn entity_implementation_traits(
    spec: &EntitySpec,
    translated_only: bool,
) -> Vec<String> {
    let fields: Vec<FieldSpec> = if translated_only {
        let mut fields: Vec<FieldSpec> = spec
            .fields
            .iter()
            .filter(|field| field.translated)
            .cloned()
            .collect();
        if let Some(behavior) = spec
            .translation
            .as_ref()
            .and_then(|translation| translation.definition_behavior.as_ref())
        {
            if behavior.override_default_fields {
                fields.extend(behavior.default_fields.iter().cloned());
            }
            if behavior.override_base_fields {
                fields.extend(behavior.base_fields.iter().cloned());
            }
        }
        fields
    } else {
        schema_definition_fields(spec).into_owned()
    };
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for field in &fields {
        let Some(implementation) = &field.implementation else {
            continue;
        };
        let trait_name = trim_class(&implementation.entity_trait);
        if trait_name.is_empty() || !seen.insert(go_to_lower(trait_name)) {
            continue;
        }
        result.push(trait_name.to_owned());
    }
    result.sort();
    result
}

pub(crate) fn translation_definition_imports(spec: &EntitySpec) -> Vec<String> {
    let translation = spec.translation.clone().unwrap_or_default();
    let mut imports = vec![
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityTranslationDefinition".to_owned(),
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\FieldCollection".to_owned(),
        translation.entity_class.clone(),
        translation.collection_class.clone(),
        translation.parent_definition_class.clone(),
    ];
    if let Some(metadata) = &translation.definition_metadata
        && !metadata.hydrator_class.is_empty()
    {
        imports.push(metadata.hydrator_class.clone());
    }
    let mut storage_spec = spec.clone();
    storage_spec.translation = None;
    storage_spec.fields = Vec::new();
    storage_spec.definition_behavior = translation.definition_behavior.clone();
    for field in &spec.fields {
        if !field.translated || field.kind == FIELD_LOCKED && !field.translation_definition_only {
            continue;
        }
        let mut field = field.clone();
        field.translated = false;
        storage_spec.fields.push(field);
    }
    for class in definition_imports(&storage_spec) {
        if class == "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition"
            || class == storage_spec.entity_class
            || class == storage_spec.collection_class
        {
            continue;
        }
        imports.push(class);
    }
    imports
}

/// Sorted, deduplicated `use` imports. Short names shared by more than one
/// class are referenced fully qualified instead.
pub(crate) struct ImportTable {
    pub(crate) classes: Vec<String>,
    short_count: HashMap<String, usize>,
}

impl ImportTable {
    pub(crate) fn new(classes: Vec<String>) -> Self {
        let mut seen = HashSet::new();
        let mut table = ImportTable {
            classes: Vec::new(),
            short_count: HashMap::new(),
        };
        for class in classes {
            let class = trim_class(&class);
            if class.is_empty() || !seen.insert(class.to_owned()) {
                continue;
            }
            table.classes.push(class.to_owned());
            *table
                .short_count
                .entry(go_to_lower(short_class(class)))
                .or_default() += 1;
        }
        table.classes.sort();
        table
    }

    fn ambiguous(&self, class: &str) -> bool {
        self.short_count
            .get(&go_to_lower(short_class(class)))
            .is_some_and(|count| *count > 1)
    }

    pub(crate) fn reference(&self, class: &str) -> String {
        let class = trim_class(class);
        if self.ambiguous(class) {
            return format!("\\{class}");
        }
        short_class(class).to_owned()
    }

    pub(crate) fn render(&self) -> String {
        let mut result = String::new();
        for class in &self.classes {
            if self.ambiguous(class) {
                continue;
            }
            result.push_str("use ");
            result.push_str(class);
            result.push_str(";\n");
        }
        result
    }
}

pub(crate) fn has_version_field(spec: &EntitySpec) -> bool {
    schema_definition_fields(spec)
        .iter()
        .any(|field| field.kind == FIELD_VERSION)
}
