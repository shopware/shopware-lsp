//! Source-preserving rewrites of generated DAL classes. Only managed members
//! change; other methods, attributes, constants and comments stay byte-for-byte
//! intact.

use std::collections::HashSet;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as php;
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_php_rewrite::Editor;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::definition_behavior::{imported_definition_fields, imported_restrict_delete_properties};
use crate::definition_metadata::imported_definition_defaults;
use crate::import::{
    Resolver, import_entity_extension_field_modifications, import_entity_extension_protections,
    import_entity_protections, imported_boolean_return, returned_expression_text,
    single_return_only,
};
use crate::model::*;
use crate::render::{
    ImportTable, complete_spec, definition_imports, entity_implementation_traits, entity_imports,
    has_version_field, render_collection, render_definition, render_entity,
    render_translation_collection, render_translation_definition, render_translation_entity,
    translation_definition_imports, translation_entity_imports,
};
use crate::service::whole_line_range;
use crate::util::*;

fn text(node: &SyntaxNode) -> String {
    crate::import::node_string(node)
}

fn has_comment_or_attribute(text: &str) -> bool {
    text.contains("//") || text.contains("/*") || text.contains("#[")
}

fn trimmed(node: &SyntaxNode) -> String {
    text(node).trim().to_owned()
}

/// Replaces the managed defineFields, inheritance and protection methods and
/// adds missing imports.
pub fn rewrite_definition(source: &str, spec: &EntitySpec) -> Result<String> {
    let spec = complete_spec(spec.clone());
    let generated = render_definition(&spec)?;
    rewrite_definition_source(
        source,
        &generated,
        definition_imports(&spec),
        spec.definition_kind == DEFINITION_ENTITY,
    )
}

/// Rewrites managed members and removes imports that belonged to the
/// previous schema but are unused by the next one. Imports referenced by
/// custom source are preserved.
pub fn rewrite_definition_from(
    source: &str,
    previous: &EntitySpec,
    next: &EntitySpec,
) -> Result<String> {
    let previous = complete_spec(previous.clone());
    let next = complete_spec(next.clone());
    let result = if previous.definition_kind != next.definition_kind {
        rewrite_definition_kind_transition(source, &previous, &next)?
    } else {
        let mut result = rewrite_definition(source, &next)?;
        if next.definition_kind == DEFINITION_ENTITY || next.definition_kind == DEFINITION_MAPPING {
            let generated = render_definition(&next)?;
            result = rewrite_definition_entity_name(
                &result,
                &generated,
                &previous.entity_name,
                &next.entity_name,
            )?;
        }
        result
    };
    remove_obsolete_managed_imports(
        &result,
        &definition_imports(&previous),
        &definition_imports(&next),
    )
}

fn check_import_conflicts(
    editor: &mut Editor,
    classes: &[String],
    table: &ImportTable,
    generated: &str,
) -> Result<()> {
    for class_name in classes {
        if class_name.is_empty() {
            continue;
        }
        let reference = editor.class_reference(class_name)?;
        let expected = table.reference(class_name);
        if reference != expected && generated.contains(&expected) {
            return Err(errorf!("class import conflict for {class_name}"));
        }
    }
    Ok(())
}

fn rewrite_definition_kind_transition(
    source: &str,
    previous: &EntitySpec,
    next: &EntitySpec,
) -> Result<String> {
    let generated = render_definition(next)?;
    let (root, class) = parsed_php_class(source)?;
    let (_, generated_class) = parsed_php_class(&generated)?;
    let mut editor = Editor::new(source, Some(&root));
    let expected = ImportTable::new(definition_imports(next));
    check_import_conflicts(
        &mut editor,
        &expected.classes.clone(),
        &expected,
        &generated,
    )?;
    let parent_reference =
        editor.class_reference(definition_parent_class(&next.definition_kind))?;
    editor.set_extends(&class, &parent_reference)?;

    let desired_methods = phpquery::methods(&generated_class);
    let desired_names: HashSet<String> =
        desired_methods.iter().map(phpquery::method_name).collect();
    let previous_managed = managed_definition_methods(previous);
    let mut removed_methods = HashSet::new();
    for method in phpquery::methods(&class) {
        let name = phpquery::method_name(&method);
        if !previous_managed.contains(name.as_str()) {
            continue;
        }
        if !safe_definition_transition_method(&method, &name) {
            if desired_names.contains(&name) {
                return Err(errorf!(
                    "cannot overwrite customized definition method {name}"
                ));
            }
            continue;
        }
        editor.remove_class_member(&method)?;
        removed_methods.insert(name);
    }
    for method in &desired_methods {
        let name = phpquery::method_name(method);
        if named_method(&class, &name).is_some() && !removed_methods.contains(&name) {
            return Err(errorf!(
                "cannot add managed definition method {name} because a custom method already exists"
            ));
        }
        editor.insert_class_member(&class, text(method).trim_matches(['\r', '\n']))?;
    }

    let previous_constants = direct_class_constants(&class);
    let desired_constants = direct_class_constants(&generated_class);
    let mut removed_entity_name = false;
    for constant in &previous_constants {
        if !is_entity_name_constant(constant) {
            continue;
        }
        if !safe_entity_name_constant(constant) {
            if !desired_constants.is_empty() {
                return Err(errorf!("cannot overwrite customized ENTITY_NAME constant"));
            }
            continue;
        }
        editor.remove_class_member(constant)?;
        removed_entity_name = true;
    }
    for constant in &desired_constants {
        if !is_entity_name_constant(constant) {
            continue;
        }
        if !removed_entity_name && previous_constants.iter().any(is_entity_name_constant) {
            return Err(errorf!(
                "cannot add managed ENTITY_NAME constant because a custom constant already exists"
            ));
        }
        editor.insert_class_member(&class, text(constant).trim_matches(['\r', '\n']))?;
    }
    let edits = editor.finish()?;
    Ok(swlsp_rewrite::apply(source, &edits)?)
}

fn definition_parent_class(kind: &str) -> &'static str {
    match kind {
        DEFINITION_BULK_EXTENSION => {
            "Shopware\\Core\\Framework\\DataAbstractionLayer\\BulkEntityExtension"
        }
        DEFINITION_EXTENSION => "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityExtension",
        DEFINITION_MAPPING => {
            "Shopware\\Core\\Framework\\DataAbstractionLayer\\MappingEntityDefinition"
        }
        _ => "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition",
    }
}

fn managed_definition_methods(spec: &EntitySpec) -> HashSet<&'static str> {
    if spec.definition_kind == DEFINITION_BULK_EXTENSION {
        return HashSet::from(["collect"]);
    }
    let mut result = HashSet::from(["getEntityName"]);
    if spec.definition_kind == DEFINITION_EXTENSION {
        result.insert("extendFields");
        result.insert("getDefinitionClass");
        if spec.protection_method_raw.trim().is_empty() {
            result.insert("extendProtections");
        }
        if spec.modify_fields_method_raw.trim().is_empty() {
            result.insert("modifyFields");
        }
        return result;
    }
    result.extend(["defineFields", "getEntityClass", "getCollectionClass"]);
    if let Some(metadata) = &spec.definition_metadata {
        if !metadata.since.is_empty() || !metadata.since_method_raw.is_empty() {
            result.insert("since");
        }
        if !metadata.defaults.is_empty() || !metadata.defaults_method_raw.is_empty() {
            result.insert("getDefaults");
        }
        if !metadata.child_defaults.is_empty() || !metadata.child_defaults_method_raw.is_empty() {
            result.insert("getChildDefaults");
        }
        if !metadata.hydrator_class.is_empty() || !metadata.hydrator_method_raw.is_empty() {
            result.insert("getHydratorClass");
        }
    }
    if let Some(behavior) = &spec.definition_behavior {
        if !behavior.parent_definition_class.is_empty()
            || !behavior.parent_definition_method_raw.is_empty()
        {
            result.insert("getParentDefinitionClass");
        }
        if behavior.version_aware.is_some() || !behavior.version_aware_method_raw.is_empty() {
            result.insert("isVersionAware");
        }
        if !behavior.inheritance_aware_method_raw.is_empty() {
            result.insert("isInheritanceAware");
        }
        if behavior.override_default_fields || !behavior.default_fields_method_raw.is_empty() {
            result.insert("defaultFields");
        }
        if behavior.override_base_fields || !behavior.base_fields_method_raw.is_empty() {
            result.insert("getBaseFields");
        }
        if !behavior.restrict_delete_meta_properties.is_empty()
            || !behavior.restrict_delete_meta_method_raw.is_empty()
        {
            result.insert("getRestrictDeleteMetaFields");
        }
    }
    if spec.definition_kind == DEFINITION_ENTITY {
        result.insert("isInheritanceAware");
        if spec.protection_method_raw.trim().is_empty() {
            result.insert("defineProtections");
        }
    }
    result
}

fn safe_definition_transition_method(method: &SyntaxNode, name: &str) -> bool {
    match name {
        "defineFields" | "extendFields" | "collect" => true,
        "defineProtections" => safe_protection_method(Some(method)),
        "extendProtections" => safe_extension_protection_method(Some(method)),
        "modifyFields" => safe_field_modification_method(Some(method)),
        "isInheritanceAware" | "isVersionAware" => {
            imported_boolean_return(Some(method)).is_some() && safe_boolean_method(Some(method))
        }
        "defaultFields"
        | "getBaseFields"
        | "getRestrictDeleteMetaFields"
        | "getParentDefinitionClass" => safe_definition_behavior_method(Some(method), name),
        "since" | "getDefaults" | "getChildDefaults" | "getHydratorClass" => {
            safe_definition_metadata_method(Some(method), name)
        }
        _ => safe_literal_return_method(Some(method)),
    }
}

fn direct_class_constants(class: &SyntaxNode) -> Vec<SyntaxNode> {
    let Some(body) = phpquery::class_body(class) else {
        return Vec::new();
    };
    body.children()
        .filter(|child| child.kind() == php::PHP_CLASS_CONST_DECLARATION)
        .collect()
}

fn is_entity_name_constant(constant: &SyntaxNode) -> bool {
    text(constant).contains("ENTITY_NAME")
}

fn safe_entity_name_constant(constant: &SyntaxNode) -> bool {
    let source = text(constant);
    if !is_entity_name_constant(constant) || has_comment_or_attribute(&source) {
        return false;
    }
    let source = source.trim();
    source.matches('=').count() == 1 && !source.contains(',') && source.ends_with(';')
}

fn rewrite_definition_entity_name(
    source: &str,
    generated: &str,
    previous: &str,
    next: &str,
) -> Result<String> {
    if previous == next {
        return Ok(source.to_owned());
    }
    let (root, class) = parsed_php_class(source)?;
    let (_, generated_class) = parsed_php_class(generated)?;
    let mut editor = Editor::new(source, Some(&root));
    let existing = direct_class_constants(&class)
        .into_iter()
        .find(is_entity_name_constant);
    let Some(desired) = direct_class_constants(&generated_class)
        .into_iter()
        .find(is_entity_name_constant)
    else {
        return Err(errorf!("generated definition has no ENTITY_NAME constant"));
    };
    match existing {
        None => editor.insert_class_member(&class, &trimmed(&desired))?,
        Some(existing) => {
            if !safe_entity_name_constant(&existing) {
                return Err(errorf!("cannot overwrite customized ENTITY_NAME constant"));
            }
            editor.replace_range(existing.range_trimmed_trivia(), trimmed(&desired))?;
        }
    }
    let existing_method = named_method(&class, "getEntityName");
    let Some(desired_method) = named_method(&generated_class, "getEntityName") else {
        return Err(errorf!("generated definition has no getEntityName method"));
    };
    match existing_method {
        None => editor.insert_class_member(&class, &trimmed(&desired_method))?,
        Some(existing_method) => {
            if trimmed(&existing_method) != trimmed(&desired_method) {
                if !safe_literal_return_method(Some(&existing_method)) {
                    return Err(errorf!(
                        "cannot overwrite customized definition identity method getEntityName"
                    ));
                }
                editor.replace_range(
                    existing_method.range_trimmed_trivia(),
                    trimmed(&desired_method),
                )?;
            }
        }
    }
    let edits = editor.finish()?;
    Ok(swlsp_rewrite::apply(source, &edits)?)
}

pub fn rewrite_translation_definition(source: &str, spec: &EntitySpec) -> Result<String> {
    let generated = render_translation_definition(spec)?;
    rewrite_definition_source(
        source,
        &generated,
        translation_definition_imports(&complete_spec(spec.clone())),
        false,
    )
}

/// Additionally updates the literal entity-name identity when a translation
/// table is renamed.
pub fn rewrite_translation_definition_from(
    source: &str,
    previous: &EntitySpec,
    next: &EntitySpec,
) -> Result<String> {
    let result = rewrite_translation_definition(source, next)?;
    let (Some(previous_translation), Some(next_translation)) =
        (&previous.translation, &next.translation)
    else {
        return Err(errorf!("translation identity metadata is missing"));
    };
    let generated = render_translation_definition(next)?;
    rewrite_definition_entity_name(
        &result,
        &generated,
        &previous_translation.entity_name,
        &next_translation.entity_name,
    )
}

fn rewrite_definition_source(
    source: &str,
    generated: &str,
    definition_classes: Vec<String>,
    manage_inheritance: bool,
) -> Result<String> {
    let (root, class) = parsed_php_class(source)?;
    let (_, generated_class) = parsed_php_class(generated)?;
    let method_name = if named_method(&generated_class, "extendFields").is_some() {
        "extendFields"
    } else if named_method(&generated_class, "collect").is_some() {
        "collect"
    } else {
        "defineFields"
    };
    let existing_method = named_method(&class, method_name);
    let desired_method = named_method(&generated_class, method_name);
    let Some(desired_method) =
        desired_method.filter(|_| existing_method.is_some() || method_name == "extendFields")
    else {
        return Err(errorf!(
            "cannot safely rewrite definition without {method_name}"
        ));
    };
    let mut editor = Editor::new(source, Some(&root));
    let expected = ImportTable::new(definition_classes);
    check_import_conflicts(&mut editor, &expected.classes.clone(), &expected, generated)?;
    match &existing_method {
        None => editor.insert_class_member(&class, &trimmed(&desired_method))?,
        Some(existing) => {
            editor.replace_range(existing.range_trimmed_trivia(), trimmed(&desired_method))?
        }
    }
    if manage_inheritance {
        rewrite_inheritance_aware_method(&mut editor, &class, &generated_class)?;
        rewrite_member(
            &mut editor,
            &class,
            &generated_class,
            "defineProtections",
            false,
            |method| safe_protection_method(Some(method)),
            "cannot overwrite customized defineProtections method",
        )?;
    } else if method_name == "extendFields" {
        rewrite_member(
            &mut editor,
            &class,
            &generated_class,
            "extendProtections",
            true,
            |method| safe_extension_protection_method(Some(method)),
            "cannot overwrite customized extendProtections method",
        )?;
        rewrite_member(
            &mut editor,
            &class,
            &generated_class,
            "modifyFields",
            true,
            |method| safe_field_modification_method(Some(method)),
            "cannot overwrite customized modifyFields method",
        )?;
        for name in ["getEntityName", "getDefinitionClass"] {
            rewrite_member(
                &mut editor,
                &class,
                &generated_class,
                name,
                false,
                |method| safe_literal_return_method(Some(method)),
                &format!("cannot overwrite customized entity extension method {name}"),
            )?;
        }
    }
    if method_name == "defineFields" {
        for name in [
            "since",
            "getDefaults",
            "getChildDefaults",
            "getHydratorClass",
        ] {
            rewrite_member(
                &mut editor,
                &class,
                &generated_class,
                name,
                false,
                |method| safe_definition_metadata_method(Some(method), name),
                &format!("cannot overwrite customized definition metadata method {name}"),
            )?;
        }
        for name in [
            "getParentDefinitionClass",
            "isVersionAware",
            "defaultFields",
            "getBaseFields",
            "getRestrictDeleteMetaFields",
        ] {
            rewrite_member(
                &mut editor,
                &class,
                &generated_class,
                name,
                false,
                |method| safe_definition_behavior_method(Some(method), name),
                &format!("cannot overwrite customized definition behavior method {name}"),
            )?;
        }
    }
    let edits = editor.finish()?;
    Ok(swlsp_rewrite::apply(source, &edits)?)
}

/// Inserts, replaces or removes one managed method. With
/// `keep_unsafe_desired`, a desired method that is itself an immutable raw
/// fragment leaves the existing method untouched.
fn rewrite_member(
    editor: &mut Editor,
    class: &SyntaxNode,
    generated_class: &SyntaxNode,
    name: &str,
    keep_unsafe_desired: bool,
    safe: impl Fn(&SyntaxNode) -> bool,
    error: &str,
) -> Result<()> {
    let existing = named_method(class, name);
    let desired = named_method(generated_class, name);
    let existing = match (existing, &desired) {
        (None, None) => return Ok(()),
        (Some(existing), Some(desired)) if trimmed(&existing) == trimmed(desired) => return Ok(()),
        (None, Some(desired)) => return Ok(editor.insert_class_member(class, &trimmed(desired))?),
        (Some(existing), _) => existing,
    };
    if keep_unsafe_desired && desired.as_ref().is_some_and(|desired| !safe(desired)) {
        return Ok(());
    }
    if !safe(&existing) {
        return Err(errorf!("{error}"));
    }
    match desired {
        None => editor.remove_class_member(&existing)?,
        Some(desired) => {
            editor.replace_range(existing.range_trimmed_trivia(), trimmed(&desired))?
        }
    }
    Ok(())
}

fn safe_definition_behavior_method(method: Option<&SyntaxNode>, name: &str) -> bool {
    let Some(method) = method.filter(|method| !has_comment_or_attribute(&text(method))) else {
        return false;
    };
    match name {
        "isVersionAware" => imported_boolean_return(Some(method)).is_some(),
        "defaultFields" | "getBaseFields" => {
            imported_definition_fields(method, &Resolver::identity(), None, "rewrite-field")
                .is_some()
        }
        "getRestrictDeleteMetaFields" => imported_restrict_delete_properties(method).is_some(),
        _ => safe_literal_return_method(Some(method)),
    }
}

fn safe_definition_metadata_method(method: Option<&SyntaxNode>, name: &str) -> bool {
    let Some(method) = method.filter(|method| !has_comment_or_attribute(&text(method))) else {
        return false;
    };
    if name == "getDefaults" || name == "getChildDefaults" {
        return imported_definition_defaults(method).is_some();
    }
    safe_literal_return_method(Some(method))
}

fn safe_field_modification_method(method: Option<&SyntaxNode>) -> bool {
    let mut spec = EntitySpec::default();
    let Some(method) = method else {
        return false;
    };
    import_entity_extension_field_modifications(&mut spec, Some(method), &Resolver::identity());
    spec.modify_fields_method_raw.trim().is_empty()
}

pub(crate) fn safe_extension_protection_method(method: Option<&SyntaxNode>) -> bool {
    let mut spec = EntitySpec::default();
    let Some(method) = method else {
        return false;
    };
    import_entity_extension_protections(&mut spec, Some(method));
    spec.protection_method_raw.trim().is_empty()
}

fn safe_literal_return_method(method: Option<&SyntaxNode>) -> bool {
    let Some(method) = method.filter(|method| !has_comment_or_attribute(&text(method))) else {
        return false;
    };
    let Some(returned) = single_return_only(Some(method)) else {
        return false;
    };
    let expression = returned_expression_text(&returned);
    if phpquery::direct_child(&returned, php::PHP_STRING)
        .is_some_and(|literal| trimmed(&literal) == expression)
    {
        return true;
    }
    phpquery::nodes(&returned, &[php::PHP_SCOPED_ACCESS, php::PHP_MEMBER_ACCESS])
        .iter()
        .any(|access| trimmed(access) == expression)
}

fn remove_obsolete_managed_imports(
    source: &str,
    previous: &[String],
    next: &[String],
) -> Result<String> {
    let retained: HashSet<String> = next
        .iter()
        .map(|class| lowercase(trim_class(class)))
        .collect();
    let obsolete: HashSet<String> = previous
        .iter()
        .map(|class| lowercase(trim_class(class)))
        .filter(|key| !key.is_empty() && !retained.contains(key))
        .collect();
    if obsolete.is_empty() {
        return Ok(source.to_owned());
    }
    let parsed = swlsp_parser_php::parse(source);
    if !parsed.errors.is_empty() {
        return Err(errorf!("rewritten PHP source contains syntax errors"));
    }
    let mut builder = swlsp_rewrite::Builder::new(source);
    for declaration in phpquery::use_declarations(&parsed.root()) {
        let imports = parse_use_declaration(&text(&declaration));
        let [imported] = imports.as_slice() else {
            continue;
        };
        if imported.kind != ImportKind::Class
            || !obsolete.contains(&lowercase(trim_class(&imported.target)))
        {
            continue;
        }
        let range = declaration.text_range();
        let (start, end) = (
            u32::from(range.start()) as usize,
            u32::from(range.end()) as usize,
        );
        if identifier_used_outside_range(source, &imported.alias, start, end) {
            continue;
        }
        let (start, end) = whole_line_range(source, start, end);
        builder.replace_range(
            TextRange::new(TextSize::from(start as u32), TextSize::from(end as u32)),
            "",
        )?;
    }
    let edits = builder.finish()?;
    Ok(swlsp_rewrite::apply(source, &edits)?)
}

fn identifier_used_outside_range(source: &str, identifier: &str, start: usize, end: usize) -> bool {
    let identifier = identifier.trim();
    if identifier.is_empty() {
        return false;
    }
    contains_php_identifier(&source.as_bytes()[..start], identifier.as_bytes())
        || contains_php_identifier(&source.as_bytes()[end..], identifier.as_bytes())
}

fn contains_php_identifier(source: &[u8], identifier: &[u8]) -> bool {
    let mut offset = 0;
    while offset + identifier.len() <= source.len() {
        let Some(relative) = source[offset..]
            .windows(identifier.len())
            .position(|window| window == identifier)
        else {
            return false;
        };
        let start = offset + relative;
        let end = start + identifier.len();
        let before = start == 0 || !is_php_identifier_byte(source[start - 1]);
        let after = end == source.len() || !is_php_identifier_byte(source[end]);
        if before && after {
            return true;
        }
        offset = start + 1;
    }
    false
}

fn is_php_identifier_byte(value: u8) -> bool {
    value == b'_' || value >= 0x80 || value.is_ascii_alphanumeric()
}

fn safe_protection_method(method: Option<&SyntaxNode>) -> bool {
    let Some(method) = method.filter(|method| !has_comment_or_attribute(&text(method))) else {
        return false;
    };
    let mut spec = EntitySpec::default();
    import_entity_protections(&mut spec, Some(method));
    spec.protection_method_raw.trim().is_empty()
}

fn rewrite_inheritance_aware_method(
    editor: &mut Editor,
    class: &SyntaxNode,
    generated_class: &SyntaxNode,
) -> Result<()> {
    let existing = named_method(class, "isInheritanceAware");
    let desired = named_method(generated_class, "isInheritanceAware");
    if let (Some(existing), Some(desired)) = (&existing, &desired)
        && trimmed(existing) == trimmed(desired)
    {
        return Ok(());
    }
    let Some(desired) = desired else {
        if imported_boolean_return(existing.as_ref()) != Some(true) {
            return Ok(());
        }
        if !safe_boolean_method(existing.as_ref()) {
            return Err(errorf!(
                "cannot remove customized isInheritanceAware method"
            ));
        }
        if let Some(existing) = existing {
            editor.remove_class_member(&existing)?;
        }
        return Ok(());
    };
    let Some(existing) = existing else {
        editor.insert_class_member(class, &trimmed(&desired))?;
        return Ok(());
    };
    match imported_boolean_return(Some(&existing)) {
        None => Err(errorf!(
            "cannot overwrite customized isInheritanceAware method"
        )),
        Some(true) => Ok(()),
        Some(false) => {
            if !safe_boolean_method(Some(&existing)) {
                return Err(errorf!(
                    "cannot overwrite customized isInheritanceAware method"
                ));
            }
            editor.replace_range(existing.range_trimmed_trivia(), trimmed(&desired))?;
            Ok(())
        }
    }
}

fn safe_boolean_method(method: Option<&SyntaxNode>) -> bool {
    method.is_some_and(|method| !has_comment_or_attribute(&text(method)))
}

/// Updates the generated API alias after a technical entity rename while
/// preserving every unrelated collection member.
pub fn rewrite_collection(
    source: &str,
    previous: &EntitySpec,
    next: &EntitySpec,
) -> Result<String> {
    let desired = render_collection(next)?;
    rewrite_collection_alias(source, &desired, &previous.entity_name, &next.entity_name)
}

/// Updates the translation collection API alias after a translation-table
/// rename.
pub fn rewrite_translation_collection(
    source: &str,
    previous: &EntitySpec,
    next: &EntitySpec,
) -> Result<String> {
    let desired = render_translation_collection(next)?;
    let (Some(previous_translation), Some(next_translation)) =
        (&previous.translation, &next.translation)
    else {
        return Err(errorf!(
            "translation collection identity metadata is missing"
        ));
    };
    rewrite_collection_alias(
        source,
        &desired,
        &previous_translation.entity_name,
        &next_translation.entity_name,
    )
}

fn rewrite_collection_alias(
    source: &str,
    desired: &str,
    previous_name: &str,
    next_name: &str,
) -> Result<String> {
    if previous_name == next_name {
        return Ok(source.to_owned());
    }
    let (root, class) = parsed_php_class(source)?;
    let (_, desired_class) = parsed_php_class(desired)?;
    let existing = named_method(&class, "getApiAlias");
    let Some(replacement) = named_method(&desired_class, "getApiAlias") else {
        return Err(errorf!("generated collection has no getApiAlias method"));
    };
    let mut editor = Editor::new(source, Some(&root));
    match existing {
        None => editor.insert_class_member(&class, &trimmed(&replacement))?,
        Some(existing) => {
            if !safe_literal_return_method(Some(&existing)) {
                return Err(errorf!(
                    "cannot overwrite customized collection getApiAlias method"
                ));
            }
            editor.replace_range(existing.range_trimmed_trivia(), trimmed(&replacement))?;
        }
    }
    let edits = editor.finish()?;
    Ok(swlsp_rewrite::apply(source, &edits)?)
}

/// Owns only properties and trivial accessors represented by the previous
/// schema; a customized accessor is rejected instead of overwritten.
pub fn rewrite_entity(source: &str, previous: &EntitySpec, next: &EntitySpec) -> Result<String> {
    let desired = render_entity(next)?;
    let (properties, methods) = managed_entity_members(previous);
    let next_completed = complete_spec(next.clone());
    let previous_completed = complete_spec(previous.clone());
    let result = rewrite_entity_source(
        source,
        &desired,
        &properties,
        &methods,
        &entity_imports(&next_completed),
        &entity_implementation_traits(previous, false),
        &entity_implementation_traits(next, false),
    )?;
    remove_obsolete_managed_imports(
        &result,
        &entity_imports(&previous_completed),
        &entity_imports(&next_completed),
    )
}

pub fn rewrite_translation_entity(
    source: &str,
    previous: &EntitySpec,
    next: &EntitySpec,
) -> Result<String> {
    let desired = render_translation_entity(next)?;
    let (properties, methods) = managed_translation_entity_members(previous);
    let previous_classes = translation_entity_imports(&complete_spec(previous.clone()));
    let next_classes = translation_entity_imports(&complete_spec(next.clone()));
    let result = rewrite_entity_source(
        source,
        &desired,
        &properties,
        &methods,
        &next_classes,
        &entity_implementation_traits(previous, true),
        &entity_implementation_traits(next, true),
    )?;
    remove_obsolete_managed_imports(&result, &previous_classes, &next_classes)
}

fn rewrite_entity_source(
    source: &str,
    desired: &str,
    managed_properties: &HashSet<String>,
    managed_methods: &HashSet<String>,
    entity_classes: &[String],
    previous_traits: &[String],
    next_traits: &[String],
) -> Result<String> {
    let (root, class) = parsed_php_class(source)?;
    let (_, desired_class) = parsed_php_class(desired)?;
    let mut editor = Editor::new(source, Some(&root));
    let expected = ImportTable::new(entity_classes.to_vec());
    check_import_conflicts(&mut editor, entity_classes, &expected, desired)?;
    for property in phpquery::properties(&class) {
        let variables = phpquery::property_variables(&property);
        let remove = variables
            .iter()
            .any(|variable| managed_properties.contains(&phpquery::variable_name(variable)));
        if !remove {
            continue;
        }
        if variables.len() != 1 {
            return Err(errorf!(
                "cannot safely rewrite combined managed property declaration"
            ));
        }
        editor.remove_class_member(&property)?;
    }
    for method in phpquery::methods(&class) {
        let name = phpquery::method_name(&method);
        if !managed_methods.contains(&name) {
            continue;
        }
        if !trivial_entity_accessor(&text(&method)) {
            return Err(errorf!(
                "cannot overwrite customized entity accessor {name}"
            ));
        }
        editor.remove_class_member(&method)?;
    }
    rewrite_managed_entity_traits(&mut editor, &root, &class, previous_traits, next_traits)?;
    let mut declarations: Vec<String> = phpquery::properties(&desired_class)
        .iter()
        .map(trimmed)
        .collect();
    declarations.extend(phpquery::methods(&desired_class).iter().map(trimmed));
    if !declarations.is_empty() {
        editor.insert_class_member(&class, &declarations.join("\n\n"))?;
    }
    let edits = editor.finish()?;
    Ok(swlsp_rewrite::apply(source, &edits)?)
}

fn rewrite_managed_entity_traits(
    editor: &mut Editor,
    root: &SyntaxNode,
    class: &SyntaxNode,
    previous_traits: &[String],
    next_traits: &[String],
) -> Result<()> {
    let previous = class_name_set(previous_traits);
    let next = class_name_set(next_traits);
    let mut existing = HashSet::new();
    let resolve = Resolver::new(root);
    let Some(body) = phpquery::class_body(class) else {
        return Err(errorf!("entity class body is unavailable"));
    };
    for declaration in body.children() {
        if declaration.kind() != php::PHP_TRAIT_USE_DECLARATION {
            continue;
        }
        let source = text(&declaration);
        if source.contains('{')
            || source.contains(',')
            || source.contains("//")
            || source.contains("/*")
        {
            continue;
        }
        let names = phpquery::nodes(&declaration, &[php::PHP_NAME]);
        let [name] = names.as_slice() else {
            continue;
        };
        let class_name = resolve.resolve(&phpquery::name_value(name));
        let key = lowercase(trim_class(&class_name));
        existing.insert(key.clone());
        if !previous.contains(&key) || next.contains(&key) {
            continue;
        }
        editor.remove_class_member(&declaration)?;
        existing.remove(&key);
    }
    for trait_name in next_traits {
        let key = lowercase(trim_class(trait_name));
        if existing.contains(&key) {
            continue;
        }
        let reference = editor.class_reference(trait_name)?;
        editor.insert_class_member(class, &format!("use {reference};"))?;
        existing.insert(key);
    }
    Ok(())
}

fn class_name_set(classes: &[String]) -> HashSet<String> {
    classes
        .iter()
        .map(|class| lowercase(trim_class(class)))
        .filter(|class| !class.is_empty())
        .collect()
}

pub(crate) fn parsed_php_class(source: &str) -> Result<(SyntaxNode, SyntaxNode)> {
    let parsed = swlsp_parser_php::parse(source);
    if !parsed.errors.is_empty() {
        return Err(errorf!("PHP source contains syntax errors"));
    }
    let root = parsed.root();
    let classes = phpquery::classes(&root);
    let [class] = classes.as_slice() else {
        return Err(errorf!("expected exactly one PHP class"));
    };
    let class = class.clone();
    Ok((root, class))
}

pub(crate) fn named_method(class: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    phpquery::methods(class)
        .into_iter()
        .find(|method| phpquery::method_name(method) == name)
}

fn add_member_names(properties: &mut HashSet<String>, methods: &mut HashSet<String>, name: &str) {
    properties.insert(name.to_owned());
    let exported = uppercase_first(name);
    methods.insert(format!("get{exported}"));
    methods.insert(format!("is{exported}"));
    methods.insert(format!("set{exported}"));
}

fn managed_field_names(field: &FieldSpec, names: &mut Vec<String>) {
    names.push(field.property_name.clone());
    if (field.kind == FIELD_MANY_TO_ONE || field.kind == FIELD_ONE_TO_ONE)
        && !field.uses_existing_column
    {
        names.push(field.foreign_key_property_name.clone());
    }
    if field.kind == FIELD_HIERARCHY {
        names.push(field.foreign_key_property_name.clone());
        names.push(field.hierarchy_parent_property.clone());
    }
}

fn unmanaged_entity_field(field: &FieldSpec) -> bool {
    matches!(
        field.kind.as_str(),
        FIELD_ID
            | FIELD_VERSION
            | FIELD_REFERENCE_VERSION
            | FIELD_CREATED_AT
            | FIELD_UPDATED_AT
            | FIELD_LOCKED
    ) || field.implementation.as_ref().is_some_and(|implementation| {
        !implementation.manage_entity || !implementation.entity_trait.is_empty()
    })
}

fn managed_entity_members(spec: &EntitySpec) -> (HashSet<String>, HashSet<String>) {
    let spec = complete_spec(spec.clone());
    let mut names = Vec::new();
    for field in schema_definition_fields(&spec).iter() {
        if unmanaged_entity_field(field) {
            continue;
        }
        managed_field_names(field, &mut names);
    }
    let mut properties = HashSet::new();
    let mut methods = HashSet::new();
    for name in names {
        add_member_names(&mut properties, &mut methods, &name);
    }
    (properties, methods)
}

fn managed_translation_entity_members(spec: &EntitySpec) -> (HashSet<String>, HashSet<String>) {
    let spec = complete_spec(spec.clone());
    let mut properties = HashSet::new();
    let mut methods = HashSet::new();
    let Some(translation) = spec
        .translation
        .as_ref()
        .filter(|translation| translation.enabled)
    else {
        return (properties, methods);
    };
    let mut names = Vec::new();
    let behavior = translation.definition_behavior.as_ref();
    if !behavior.is_some_and(|behavior| behavior.override_base_fields) {
        names.push(camelize_storage_name(&translation.parent_storage_name));
        names.push(translation.parent_property_name.clone());
        if has_version_field(&spec) {
            names.push(camelize_storage_name(&format!(
                "{}_version_id",
                trim_suffix(&translation.parent_storage_name, "_id")
            )));
        }
    }
    if let Some(behavior) = behavior {
        if behavior.override_default_fields {
            for field in &behavior.default_fields {
                if !unmanaged_entity_field(field) {
                    managed_field_names(field, &mut names);
                }
            }
        }
        if behavior.override_base_fields {
            for field in &behavior.base_fields {
                if !unmanaged_entity_field(field) {
                    managed_field_names(field, &mut names);
                }
            }
        }
    }
    for field in &spec.fields {
        if field.translated && field.kind != FIELD_LOCKED {
            if field.implementation.as_ref().is_some_and(|implementation| {
                !implementation.manage_entity || !implementation.entity_trait.is_empty()
            }) {
                continue;
            }
            names.push(field.property_name.clone());
        }
    }
    for name in names {
        add_member_names(&mut properties, &mut methods, &name);
    }
    (properties, methods)
}

fn trivial_entity_accessor(source: &str) -> bool {
    if has_comment_or_attribute(source) {
        return false;
    }
    let compact = compact_fields(source);
    compact.contains("return $this->") || (compact.contains("$this->") && compact.contains(" = $"))
}
