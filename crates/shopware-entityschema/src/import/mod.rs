//! Lossless import of class-based DAL definitions (port of Go `import*.go`).
//!
//! Literal field declarations become editable [`FieldSpec`]s; anything the
//! typed model cannot prove stays a locked raw expression.

pub(crate) mod extensions;
pub(crate) mod fields;
pub(crate) mod flags;
pub(crate) mod translation;

use std::collections::HashMap;
use std::sync::LazyLock;

use regex::Regex;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as php;
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_syntax::SyntaxNode;

use crate::definition_behavior::import_definition_behavior;
use crate::definition_metadata::{import_definition_metadata, imported_literal_string_return};
use crate::model::*;
use crate::render::complete_spec;
use crate::util::*;
use crate::validate_spec::PROPERTY_PATTERN;
use fields::import_fields_with;
use flags::{
    ImportedFlagSet, ImportedModifierSet, with_association_modifiers, with_field_modifiers,
};

pub use extensions::{import_bulk_extension, import_extension};
pub use translation::{attach_translation, import_translation_definition};

pub(crate) use extensions::{
    import_bulk_extension_class, import_bulk_extension_parsed,
    import_entity_extension_field_modifications, import_entity_extension_protections,
    import_extension_class, import_extension_parsed,
};
pub(crate) use fields::import_fields;
pub(crate) use translation::import_translation_parsed;

// Go RE2: `\b` and `\s` are ASCII-only.
static ENTITY_NAME_CONSTANT_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?m)(?-u:\b)ENTITY_NAME[\t\n\x0C\r ]*=[\t\n\x0C\r ]*['"]([^'"]+)['"]"#)
        .expect("valid pattern")
});
static ACRONYM_BOUNDARY: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"([A-Z]+)([A-Z][a-z])").expect("valid pattern"));
static WORD_BOUNDARY: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"([a-z0-9])([A-Z])").expect("valid pattern"));

pub(crate) fn entity_name_constant(text: &str) -> Option<String> {
    ENTITY_NAME_CONSTANT_PATTERN
        .captures(text)
        .map(|captures| captures[1].to_owned())
}

/// Resolves class names through a file's namespace and `use` imports.
pub(crate) struct Resolver {
    namespace: String,
    current_class: String,
    aliases: HashMap<String, String>,
    identity: bool,
}

impl Resolver {
    pub(crate) fn new(root: &SyntaxNode) -> Self {
        let namespace = trim_backslash(&phpquery::namespace(root)).to_owned();
        let classes = phpquery::classes(root);
        Self::with_classes(root, namespace, &classes)
    }

    fn with_classes(root: &SyntaxNode, namespace: String, classes: &[SyntaxNode]) -> Self {
        let current_class = classes
            .first()
            .map(|class| qualify(&namespace, &phpquery::class_name(class)))
            .unwrap_or_default();
        let mut aliases = HashMap::new();
        for declaration in phpquery::use_declarations(root) {
            for imported in parse_use_declaration(&node_string(&declaration)) {
                if imported.kind == ImportKind::Class {
                    aliases.insert(
                        go_to_lower(&imported.alias),
                        trim_backslash(&imported.target).to_owned(),
                    );
                }
            }
        }
        Resolver {
            namespace,
            current_class,
            aliases,
            identity: false,
        }
    }

    /// Returns names unchanged (Go `func(value string) string { return value }`).
    pub(crate) fn identity() -> Self {
        Resolver {
            namespace: String::new(),
            current_class: String::new(),
            aliases: HashMap::new(),
            identity: true,
        }
    }

    pub(crate) fn resolve(&self, name: &str) -> String {
        if self.identity {
            return name.to_owned();
        }
        let name = trim_backslash(name.trim());
        if name.is_empty() {
            return String::new();
        }
        if equal_fold(name, "self") || equal_fold(name, "static") {
            return self.current_class.clone();
        }
        let (first, rest) = match name.split_once('\\') {
            Some((first, rest)) => (first, Some(rest)),
            None => (name, None),
        };
        if let Some(target) = self.aliases.get(&go_to_lower(first)) {
            return match rest {
                Some(rest) => format!("{target}\\{rest}"),
                None => target.clone(),
            };
        }
        if !self.namespace.is_empty() {
            return format!("{}\\{name}", self.namespace);
        }
        name.to_owned()
    }
}

/// A source parsed once and shared by every importer that inspects it.
pub(crate) struct ParsedSource {
    pub(crate) valid: bool,
    pub(crate) root: SyntaxNode,
    pub(crate) namespace: String,
    pub(crate) classes: Vec<SyntaxNode>,
    pub(crate) resolver: Resolver,
}

impl ParsedSource {
    pub(crate) fn new(source: &str) -> Self {
        let parsed = swlsp_parser_php::parse(source);
        let root = parsed.root();
        let namespace = trim_backslash(&phpquery::namespace(&root)).to_owned();
        let classes = phpquery::classes(&root);
        let resolver = Resolver::with_classes(&root, namespace.clone(), &classes);
        ParsedSource {
            valid: parsed.errors.is_empty(),
            root,
            namespace,
            classes,
            resolver,
        }
    }
}

/// Node text without going through `Display` (which pads every token).
pub(crate) fn node_string(node: &SyntaxNode) -> String {
    let mut text = String::with_capacity(u32::from(node.text_range().len()) as usize);
    node.text().for_each_chunk(|chunk| text.push_str(chunk));
    text
}

pub fn import_definition(source: &str, lookup: Option<RelationLookup>) -> Result<EntitySpec> {
    import_definition_class(source, lookup, "", "")
}

/// Imports a class whose effective DAL base kind was resolved by the semantic
/// index or the plugin-local ancestry scanner.
pub fn import_class_based_definition(
    source: &str,
    definition_class: &str,
    kind: &str,
    lookup: Option<RelationLookup>,
) -> Result<EntitySpec> {
    match kind {
        DEFINITION_ENTITY | DEFINITION_MAPPING => {
            import_definition_class(source, lookup, definition_class, kind)
        }
        DEFINITION_EXTENSION => import_extension_class(source, lookup, definition_class),
        DEFINITION_BULK_EXTENSION => import_bulk_extension_class(source, lookup, definition_class),
        _ => Err(errorf!(
            "unsupported class-based DAL kind {}",
            go_quote(kind)
        )),
    }
}

/// The class selected by name, or the first class whose direct parent has
/// one of `parents` as short name.
pub(crate) fn select_class(
    classes: &[SyntaxNode],
    namespace: &str,
    selected_class: &str,
    parents: &[&str],
) -> Option<(SyntaxNode, Option<String>)> {
    for class in classes.iter().cloned() {
        if !selected_class.is_empty()
            && equal_fold(
                &qualify(namespace, &phpquery::class_name(&class)),
                trim_backslash(selected_class),
            )
        {
            return Some((class, None));
        }
        for parent in phpquery::class_extends(&class) {
            let short = short_class(&parent);
            if parents.contains(&short) {
                return Some((class, Some(short.to_owned())));
            }
        }
    }
    None
}

pub(crate) fn import_definition_class(
    source: &str,
    lookup: Option<RelationLookup>,
    selected_class: &str,
    selected_kind: &str,
) -> Result<EntitySpec> {
    import_definition_parsed(
        &ParsedSource::new(source),
        lookup,
        selected_class,
        selected_kind,
    )
}

pub(crate) fn import_definition_parsed(
    parsed: &ParsedSource,
    lookup: Option<RelationLookup>,
    selected_class: &str,
    selected_kind: &str,
) -> Result<EntitySpec> {
    if !parsed.valid {
        return Err(errorf!(
            "parse entity definition: PHP source contains syntax errors"
        ));
    }
    let namespace = parsed.namespace.clone();
    let Some((definition, parent)) = select_class(
        &parsed.classes,
        &namespace,
        selected_class,
        &["EntityDefinition", "MappingEntityDefinition"],
    ) else {
        return Err(errorf!("PHP file contains no concrete EntityDefinition"));
    };
    let definition_kind = match parent.as_deref() {
        None => selected_kind.to_owned(),
        Some("MappingEntityDefinition") => DEFINITION_MAPPING.to_owned(),
        Some(_) => DEFINITION_ENTITY.to_owned(),
    };
    let class_name = phpquery::class_name(&definition);
    let mut base_name = trim_suffix(&class_name, "Definition");
    if base_name == class_name {
        base_name = trim_suffix(&class_name, "Extension");
    }
    let base_name = base_name.to_owned();
    let resolve = &parsed.resolver;
    let mut spec = EntitySpec {
        mode: "edit".to_owned(),
        definition_kind: definition_kind.clone(),
        namespace: namespace.clone(),
        class_name: base_name.clone(),
        definition_class: qualify(&namespace, &class_name),
        create_migration: true,
        ..EntitySpec::default()
    };
    if definition_kind == DEFINITION_ENTITY {
        spec.entity_class = qualify(&namespace, &format!("{base_name}Entity"));
        spec.collection_class = qualify(&namespace, &format!("{base_name}Collection"));
    }
    if let Some(name) = entity_name_constant(&node_string(&definition)) {
        spec.entity_name = name;
    }
    let methods = phpquery::methods(&definition);
    let mut define_fields = None;
    let mut define_protections = None;
    for method in &methods {
        match phpquery::method_name(method).as_str() {
            "getEntityName" => {
                if spec.entity_name.is_empty()
                    && let Some(literal) = imported_literal_string_return(Some(method))
                {
                    spec.entity_name = literal;
                }
            }
            "getEntityClass" => {
                let class = returned_imported_class(Some(method), resolve);
                if !class.is_empty() {
                    spec.entity_class = class;
                }
            }
            "getCollectionClass" => {
                let class = returned_imported_class(Some(method), resolve);
                if !class.is_empty() {
                    spec.collection_class = class;
                }
            }
            "isInheritanceAware" => {
                if let Some(value) = imported_boolean_return(Some(method)) {
                    spec.inheritance_aware = value;
                }
            }
            "defineFields" => define_fields = Some(method.clone()),
            "defineProtections" => define_protections = Some(method.clone()),
            _ => {}
        }
    }
    spec.definition_behavior = import_definition_behavior(&methods, resolve, lookup, false);
    spec.definition_metadata = import_definition_metadata(
        &methods,
        resolve,
        true,
        definition_kind == DEFINITION_ENTITY,
        definition_kind == DEFINITION_ENTITY,
    );
    if spec.entity_name.is_empty() {
        return Err(errorf!("entity definition has no literal entity name"));
    }
    let Some(define_fields) = define_fields else {
        return Err(errorf!("entity definition has no defineFields method"));
    };
    let (fields, translation) = import_fields_with(&define_fields, resolve, lookup)?;
    spec.fields = fields;
    import_entity_protections(&mut spec, define_protections.as_ref());
    if let Some(mut translation) = translation {
        translation.parent_definition_class = spec.definition_class.clone();
        spec.translation = Some(translation);
    }
    Ok(complete_spec(spec))
}

pub(crate) fn import_entity_protections(spec: &mut EntitySpec, method: Option<&SyntaxNode>) {
    let Some(method) = method else {
        return;
    };
    let collections = phpquery::object_creations(method, &["EntityProtectionCollection"]);
    if collections.len() != 1 {
        spec.protection_method_raw = node_string(method).trim().to_owned();
        return;
    }
    let arrays = phpquery::arrays(&collections[0]);
    if arrays.len() != 1 {
        spec.protection_method_raw = node_string(method).trim().to_owned();
        return;
    }
    for item in phpquery::array_items(&arrays[0]) {
        let item_text = || node_string(&item).trim().to_owned();
        let Some(value) = phpquery::array_item_value(&item) else {
            spec.preserved_protections.push(item_text());
            continue;
        };
        let creations = phpquery::object_creations(&value, &[]);
        if creations.len() != 1 {
            spec.preserved_protections.push(item_text());
            continue;
        }
        apply_protection(spec, &creations[0], item_text());
    }
}

/// Records a literal Read/WriteProtection or preserves the expression.
pub(crate) fn apply_protection(spec: &mut EntitySpec, creation: &SyntaxNode, preserved: String) {
    let Some(scopes) = imported_protection_scopes(creation) else {
        spec.preserved_protections.push(preserved);
        return;
    };
    match short_class(&phpquery::object_class_name(creation)) {
        "ReadProtection" => {
            spec.read_protected = true;
            append_unique_strings(&mut spec.read_protection_scopes, scopes);
        }
        "WriteProtection" => {
            spec.write_protected = true;
            append_unique_strings(&mut spec.write_protection_scopes, scopes);
        }
        _ => spec.preserved_protections.push(preserved),
    }
}

fn imported_protection_scopes(creation: &SyntaxNode) -> Option<Vec<String>> {
    let arguments: Vec<SyntaxNode> = phpquery::arguments(creation).collect();
    let mut scopes = Vec::with_capacity(arguments.len());
    for (index, argument) in arguments.iter().enumerate() {
        if !phpquery::argument_name(argument).is_empty() {
            return None;
        }
        if let Some(expression) = phpquery::argument_expression(creation, index)
            && expression.kind() == php::PHP_STRING
        {
            scopes.push(phpquery::string_value(&expression));
            continue;
        }
        scopes.push(write_scope_constant(&phpquery::argument_value_text(
            creation, index,
        ))?);
    }
    Some(scopes)
}

pub(crate) fn imported_enum_case(creation: &SyntaxNode, resolve: &Resolver) -> (String, String) {
    let Some(expression) = phpquery::argument_expression(creation, 2) else {
        return (String::new(), String::new());
    };
    let text = node_string(&expression);
    let text = text.trim();
    let Some(separator) = text.rfind("::").filter(|separator| *separator > 0) else {
        return (String::new(), String::new());
    };
    let class_name = text[..separator].trim();
    let case_name = text[separator + 2..].trim();
    if class_name.is_empty() || !PROPERTY_PATTERN.is_match(case_name) {
        return (String::new(), String::new());
    }
    (resolve.resolve(class_name), case_name.to_owned())
}

/// Supports both `new FieldCollection([...])` and `$fields = [...]; return
/// new FieldCollection($fields);`. Mutation between the literal and the
/// constructor makes the literal a partial view and is refused.
pub(crate) fn imported_field_collection_array(
    method: &SyntaxNode,
    collection: &SyntaxNode,
) -> Option<SyntaxNode> {
    if let Some(array) = phpquery::arrays(collection).into_iter().next() {
        return Some(array);
    }
    let variable =
        direct_variable_expression(phpquery::argument_expression(collection, 0).as_ref());
    if variable.is_empty() {
        return None;
    }
    let mut result: Option<SyntaxNode> = None;
    for statement in phpquery::expression_statements(method) {
        if statement.text_range().start() >= collection.text_range().start() {
            break;
        }
        if phpquery::assigned_variable(&statement) != variable {
            continue;
        }
        result =
            phpquery::assignment_value(&statement).filter(|value| value.kind() == php::PHP_ARRAY);
    }
    let result = result?;
    let text = node_string(method);
    let base = u32::from(method.text_range().start()) as usize;
    let start = u32::from(result.text_range().end()) as usize - base;
    let end = u32::from(collection.text_range().start()) as usize - base;
    let between = &text[start..end];
    if between.contains(&format!("{variable}[")) || between.contains(&format!("{variable}->")) {
        return None;
    }
    Some(result)
}

pub(crate) fn import_conditional_association_value(
    id: &str,
    raw: &str,
    value: &SyntaxNode,
    flags: &ImportedFlagSet,
    modifiers: &ImportedModifierSet,
    resolve: &Resolver,
    lookup: Option<RelationLookup>,
) -> Option<FieldSpec> {
    let ternaries = phpquery::nodes(value, &[php::PHP_TERNARY_EXPRESSION]);
    if ternaries.len() != 1 {
        return None;
    }
    let mut first = conditional_association(&ternaries[0], resolve)?;
    first.id = id.to_owned();
    first.raw = raw.to_owned();
    flags.apply_association(&mut first);
    let mut first = with_association_modifiers(first, modifiers);
    if let Some(target) = lookup_relation(lookup, &first.target_definition_class) {
        enrich_relation(&mut first, &target);
    }
    Some(first)
}

/// A ternary whose two branches construct the same logical to-one
/// association; the first branch is the primary shape.
fn conditional_association(ternary: &SyntaxNode, resolve: &Resolver) -> Option<FieldSpec> {
    let creations = phpquery::object_creations(ternary, &[]);
    if creations.len() != 2 {
        return None;
    }
    let mut first = imported_standalone_association(&creations[0], resolve)?;
    let second = imported_standalone_association(&creations[1], resolve)?;
    if !same_imported_association_identity(&first, &second) {
        return None;
    }
    let parts: Vec<SyntaxNode> = ternary.children().collect();
    if parts.len() < 3 {
        return None;
    }
    first.conditional_association = Some(ConditionalAssociation {
        condition_expression: flags::normalize_imported_php_expression(
            &node_string(&parts[0]),
            resolve,
        ),
        alternative_kind: second.kind,
        alternative_autoload: second.association_autoload,
    });
    Some(first)
}

pub(crate) fn direct_variable_expression(node: Option<&SyntaxNode>) -> String {
    let Some(node) = node.filter(|node| node.kind() == php::PHP_VARIABLE) else {
        return String::new();
    };
    let name = phpquery::variable_name(node);
    if name.is_empty() {
        String::new()
    } else {
        format!("${name}")
    }
}

/// Resolves version-gated Shopware associations declared as a local variable
/// holding a ternary of two association constructors, followed by
/// `addFlags()`/modifier calls.
pub(crate) fn imported_local_fields(
    method: &SyntaxNode,
    resolve: &Resolver,
    lookup: Option<RelationLookup>,
) -> HashMap<String, FieldSpec> {
    let mut result = HashMap::new();
    let statements = phpquery::expression_statements(method);
    for (index, statement) in statements.iter().enumerate() {
        let variable = phpquery::assigned_variable(statement);
        let Some(value) = phpquery::assignment_value(statement) else {
            continue;
        };
        if variable.is_empty() || value.kind() != php::PHP_TERNARY_EXPRESSION {
            continue;
        }
        let Some(mut field) = conditional_association(&value, resolve) else {
            continue;
        };
        for following in &statements[index + 1..] {
            if !phpquery::assigned_variable(following).is_empty() {
                break;
            }
            let text = node_string(following);
            if !text.trim().starts_with(&format!("{variable}->")) {
                continue;
            }
            let flag_set = imported_variable_flags(following, resolve);
            flag_set.apply_association(&mut field);
            field.association_before_flags = imported_variable_modifiers(following, true);
            field.association_after_flags = imported_variable_modifiers(following, false);
            break;
        }
        if let Some(target) = lookup_relation(lookup, &field.target_definition_class) {
            enrich_relation(&mut field, &target);
        }
        result.insert(variable, field);
    }
    result
}

fn imported_standalone_association(creation: &SyntaxNode, resolve: &Resolver) -> Option<FieldSpec> {
    let mut field = FieldSpec {
        editable: true,
        uses_existing_column: true,
        raw: node_string(creation).trim().to_owned(),
        ..FieldSpec::default()
    };
    match short_class(&phpquery::object_class_name(creation)) {
        "OneToOneAssociationField" => {
            field.kind = FIELD_ONE_TO_ONE.to_owned();
            field.property_name = imported_string_argument(creation, 0);
            field.storage_name = imported_string_argument(creation, 1);
            field.reference_field = default_string(&imported_string_argument(creation, 2), "id");
            field.reference_storage_name = field.reference_field.clone();
            field.target_definition_class = resolve.resolve(&imported_class_argument(creation, 3));
            field.association_autoload = imported_association_autoload(creation, true);
        }
        "ManyToOneAssociationField" => {
            field.kind = FIELD_MANY_TO_ONE.to_owned();
            field.property_name = imported_string_argument(creation, 0);
            field.storage_name = imported_string_argument(creation, 1);
            field.target_definition_class = resolve.resolve(&imported_class_argument(creation, 2));
            field.reference_field = default_string(&imported_string_argument(creation, 3), "id");
            field.reference_storage_name = field.reference_field.clone();
            field.association_autoload = imported_association_autoload(creation, false);
        }
        _ => return None,
    }
    (!field.property_name.is_empty()
        && !field.storage_name.is_empty()
        && !field.target_definition_class.is_empty())
    .then_some(field)
}

fn same_imported_association_identity(left: &FieldSpec, right: &FieldSpec) -> bool {
    left.property_name == right.property_name
        && left.storage_name == right.storage_name
        && left.reference_field == right.reference_field
        && left.target_definition_class == right.target_definition_class
}

fn imported_variable_flags(statement: &SyntaxNode, resolve: &Resolver) -> ImportedFlagSet {
    let mut result = ImportedFlagSet::default();
    for call in phpquery::calls(statement, &[]) {
        if call.kind() != php::PHP_MEMBER_CALL || phpquery::call_method_name(&call) != "addFlags" {
            continue;
        }
        flags::import_add_flags_arguments(&mut result, &call, resolve);
    }
    result
}

fn imported_variable_modifiers(statement: &SyntaxNode, before: bool) -> Vec<String> {
    let mut seen_flags = false;
    let mut result = Vec::new();
    for call in phpquery::calls(statement, &[]) {
        let method = phpquery::call_method_name(&call);
        if method == "addFlags" {
            seen_flags = true;
            continue;
        }
        if seen_flags == before {
            continue;
        }
        let suffix = flags::imported_call_suffix(&call, &method);
        if !suffix.is_empty() {
            result.push(suffix);
        }
    }
    result
}

pub(crate) fn promote_imported_write_protection(
    fields: &mut [FieldSpec],
    translation: Option<&mut TranslationSpec>,
) {
    for field in fields.iter_mut() {
        promote_write_protected_flags(
            &mut field.write_protected,
            &mut field.write_protected_scopes,
            &mut field.preserved_flags,
        );
        promote_write_protected_flags(
            &mut field.association_write_protected,
            &mut field.association_write_scopes,
            &mut field.association_flags,
        );
        promote_write_protected_flags(
            &mut field.translation_write_protected,
            &mut field.translation_write_scopes,
            &mut field.translation_flags,
        );
        promote_write_protected_flags(
            &mut field.hierarchy_children_protected,
            &mut field.hierarchy_children_write_scopes,
            &mut field.hierarchy_children_flags,
        );
        promote_write_protected_flags(
            &mut field.hierarchy_version_protected,
            &mut field.hierarchy_version_write_scopes,
            &mut field.hierarchy_version_flags,
        );
    }
    if let Some(translation) = translation {
        promote_write_protected_flags(
            &mut translation.association_write_protected,
            &mut translation.association_write_scopes,
            &mut translation.association_flags,
        );
    }
}

fn promote_write_protected_flags(
    enabled: &mut bool,
    scopes: &mut Vec<String>,
    values: &mut Vec<String>,
) {
    let mut remaining = Vec::with_capacity(values.len());
    for value in values.drain(..) {
        match parse_write_protected_flag(&value) {
            Some(parsed) => {
                *enabled = true;
                append_unique_strings(scopes, parsed);
            }
            None => remaining.push(value),
        }
    }
    *values = remaining;
}

pub(crate) fn parse_write_protected_flag(source: &str) -> Option<Vec<String>> {
    let parsed = swlsp_parser_php::parse(&format!("<?php {source};"));
    if !parsed.errors.is_empty() {
        return None;
    }
    let creations = phpquery::object_creations(&parsed.root(), &[]);
    if creations.len() != 1
        || short_class(&phpquery::object_class_name(&creations[0])) != "WriteProtected"
    {
        return None;
    }
    let creation = &creations[0];
    let arguments: Vec<SyntaxNode> = phpquery::arguments(creation).collect();
    let mut scopes = Vec::with_capacity(arguments.len());
    for (index, argument) in arguments.iter().enumerate() {
        if let Some(expression) = phpquery::argument_expression(creation, index)
            && expression.kind() == php::PHP_STRING
        {
            scopes.push(phpquery::string_value(&expression));
            continue;
        }
        let scope = write_scope_constant(&phpquery::argument_value_text(creation, index))?;
        if !phpquery::argument_name(argument).is_empty() {
            return None;
        }
        scopes.push(scope);
    }
    Some(scopes)
}

fn write_scope_constant(expression: &str) -> Option<String> {
    let normalized = expression.trim().replace(' ', "");
    let normalized = trim_prefix(&normalized, "\\");
    let constant = trim_prefix(normalized, "Shopware\\Core\\Framework\\");
    if constant == normalized && normalized.contains('\\') {
        return None;
    }
    Some(
        match constant {
            "Context::SYSTEM_SCOPE" => "system",
            "Context::USER_SCOPE" => "user",
            "Context::CRUD_API_SCOPE" => "crud",
            "Context::SYSTEM_SCOPE_DAL_WRITE_EVENT" => "system-scope-dal-write-event",
            _ => return None,
        }
        .to_owned(),
    )
}

pub(crate) fn append_unique_strings(values: &mut Vec<String>, additions: Vec<String>) {
    for value in additions {
        if !values.contains(&value) {
            values.push(value);
        }
    }
}

pub(crate) fn import_reference_version_field(
    id: &str,
    raw: &str,
    creation: &SyntaxNode,
    flags: &ImportedFlagSet,
    modifiers: &ImportedModifierSet,
    resolve: &Resolver,
    lookup: Option<RelationLookup>,
) -> FieldSpec {
    let storage = imported_string_argument(creation, 1);
    let target_class = resolve.resolve(&imported_class_argument(creation, 0));
    let mut field = FieldSpec {
        id: id.to_owned(),
        kind: FIELD_REFERENCE_VERSION.to_owned(),
        property_name: camelize_storage_name(&storage),
        storage_name: storage,
        target_definition_class: target_class.clone(),
        editable: true,
        raw: raw.to_owned(),
        ..FieldSpec::default()
    };
    flags.apply_stored(&mut field);
    if let Some(target) = lookup_relation(lookup, &target_class) {
        enrich_relation(&mut field, &target);
        if field.storage_name.is_empty() {
            field.storage_name = format!("{}_version_id", target.entity_name);
            field.property_name = camelize_storage_name(&field.storage_name);
        }
    }
    with_field_modifiers(field, modifiers)
}

pub(crate) fn imported_scalar_kind(name: &str) -> &'static str {
    match name {
        "StringField" => FIELD_STRING,
        "LongTextField" => FIELD_LONG_TEXT,
        "IntField" => FIELD_INT,
        "FloatField" => FIELD_FLOAT,
        "BoolField" => FIELD_BOOL,
        "DateField" => FIELD_DATE,
        "DateTimeField" => FIELD_DATE_TIME,
        "JsonField" => FIELD_JSON,
        "ListField" => FIELD_LIST,
        "ObjectField" => FIELD_OBJECT,
        "BlobField" => FIELD_BLOB,
        _ => FIELD_LOCKED,
    }
}

pub(crate) fn imported_delete_behavior(creations: &[SyntaxNode]) -> String {
    for creation in creations.iter().skip(1) {
        match short_class(&phpquery::object_class_name(creation)) {
            "CascadeDelete" => return DELETE_CASCADE.to_owned(),
            "SetNullOnDelete" => return DELETE_SET_NULL.to_owned(),
            "RestrictDelete" => return DELETE_RESTRICT.to_owned(),
            _ => {}
        }
    }
    String::new()
}

pub(crate) fn apply_imported_delete_options(field: &mut FieldSpec, creations: &[SyntaxNode]) {
    for creation in creations.iter().skip(1) {
        if phpquery::arguments(creation).next().is_none() {
            continue;
        }
        let value = go_to_lower(phpquery::argument_value_text(creation, 0).trim());
        if value != "true" && value != "false" {
            continue;
        }
        let option = value == "true";
        match short_class(&phpquery::object_class_name(creation)) {
            "CascadeDelete" => field.delete_clone_relevant = Some(option),
            "SetNullOnDelete" => field.delete_enforced_by_constraint = Some(option),
            _ => {}
        }
    }
}

pub(crate) fn imported_string_argument(creation: &SyntaxNode, index: usize) -> String {
    phpquery::string_argument(creation, index)
        .map(|string| phpquery::string_value(&string))
        .unwrap_or_default()
}

pub(crate) fn imported_class_argument(creation: &SyntaxNode, index: usize) -> String {
    phpquery::arguments(creation)
        .nth(index)
        .map(|argument| phpquery::class_constant_name(&argument))
        .unwrap_or_default()
}

/// Go `strconv.Atoi` on the trimmed argument text.
fn imported_int(creation: &SyntaxNode, index: usize) -> Option<i64> {
    let argument = phpquery::arguments(creation).nth(index)?;
    node_string(&argument).trim().parse::<i64>().ok()
}

pub(crate) fn imported_int_argument(creation: &SyntaxNode, index: usize, fallback: i64) -> i64 {
    imported_int(creation, index).unwrap_or(fallback)
}

pub(crate) fn imported_bool_argument(creation: &SyntaxNode, index: usize) -> bool {
    phpquery::arguments(creation)
        .nth(index)
        .is_some_and(|argument| equal_fold(node_string(&argument).trim(), "true"))
}

pub(crate) fn imported_association_autoload(creation: &SyntaxNode, fallback: bool) -> bool {
    let arguments: Vec<SyntaxNode> = phpquery::arguments(creation).collect();
    for (index, argument) in arguments.iter().enumerate() {
        if equal_fold(&phpquery::argument_name(argument), "autoload") {
            return equal_fold(&phpquery::argument_value_text(creation, index), "true");
        }
    }
    if arguments.len() <= 4 {
        return fallback;
    }
    equal_fold(&phpquery::argument_value_text(creation, 4), "true")
}

pub(crate) fn imported_optional_int_argument(creation: &SyntaxNode, index: usize) -> Option<i64> {
    imported_int(creation, index)
}

pub(crate) fn returned_imported_class(method: Option<&SyntaxNode>, resolve: &Resolver) -> String {
    let Some(returned) = single_return_only(method) else {
        return String::new();
    };
    let expression = returned_expression_text(&returned);
    for access in phpquery::nodes(&returned, &[php::PHP_SCOPED_ACCESS, php::PHP_MEMBER_ACCESS]) {
        if node_string(&access).trim() != expression {
            continue;
        }
        let class_name = phpquery::class_constant_name(&access);
        if !class_name.is_empty() {
            return resolve.resolve(&class_name);
        }
    }
    String::new()
}

/// The only statement of a method, when it is a single return.
pub(crate) fn single_return_only(method: Option<&SyntaxNode>) -> Option<SyntaxNode> {
    let method = method?;
    let returns = phpquery::nodes(method, &[php::PHP_RETURN_STATEMENT]);
    if returns.len() != 1 {
        return None;
    }
    let text = node_string(method);
    let open = text.find('{')?;
    let close = text.rfind('}')?;
    if close <= open || text[open + 1..close].trim() != node_string(&returns[0]).trim() {
        return None;
    }
    returns.into_iter().next()
}

pub(crate) fn returned_expression_text(returned: &SyntaxNode) -> String {
    let statement = node_string(returned);
    let statement = statement.trim();
    if statement.len() < 6
        || !statement.is_char_boundary(6)
        || !equal_fold(&statement[..6], "return")
    {
        return String::new();
    }
    trim_suffix(statement[6..].trim(), ";").trim().to_owned()
}

pub(crate) fn lookup_relation(
    lookup: Option<RelationLookup>,
    class: &str,
) -> Option<RelationTarget> {
    if let Some(lookup) = lookup
        && let Some(target) = lookup(class)
    {
        return Some(target);
    }
    let class = trim_backslash(class);
    let short = short_class(class);
    let base = short.strip_suffix("Definition")?;
    let namespace = trim_suffix(class, short);
    let entity_name = ACRONYM_BOUNDARY.replace_all(base, "${1}_${2}");
    let entity_name = WORD_BOUNDARY.replace_all(&entity_name, "${1}_${2}");
    Some(RelationTarget {
        definition_class: class.to_owned(),
        entity_class: format!("{namespace}{base}Entity"),
        collection_class: format!("{namespace}{base}Collection"),
        entity_name: go_to_lower(&entity_name),
        fields: vec![RelationTargetField {
            property_name: "id".to_owned(),
            storage_name: "id".to_owned(),
            primary: true,
        }],
        ..RelationTarget::default()
    })
}

pub(crate) fn lookup_relation_by_entity_name(
    lookup: Option<RelationLookup>,
    entity_name: &str,
) -> Option<RelationTarget> {
    let lookup = lookup?;
    if entity_name.is_empty() {
        return None;
    }
    lookup(entity_name)
        .filter(|target| !target.definition_class.is_empty() && target.entity_name == entity_name)
}

pub(crate) fn enrich_relation(field: &mut FieldSpec, target: &RelationTarget) {
    field.target_entity_class = target.entity_class.clone();
    field.target_collection_class = target.collection_class.clone();
    field.target_entity_name = target.entity_name.clone();
    if field.kind == FIELD_ONE_TO_MANY {
        return;
    }
    if field.reference_field.is_empty() {
        field.reference_field = "id".to_owned();
    }
    if field.reference_storage_name.is_empty() {
        field.reference_storage_name = "id".to_owned();
    }
    for candidate in &target.fields {
        if candidate.property_name == field.reference_field || candidate.primary {
            field.reference_storage_name = candidate.storage_name.clone();
            if candidate.property_name == field.reference_field {
                break;
            }
        }
    }
}

pub(crate) fn locked_field(index: usize, raw: &str) -> FieldSpec {
    FieldSpec {
        id: format!("locked-{}", index + 1),
        kind: FIELD_LOCKED.to_owned(),
        editable: false,
        raw: raw.trim().to_owned(),
        ..FieldSpec::default()
    }
}

pub(crate) fn imported_boolean_return(method: Option<&SyntaxNode>) -> Option<bool> {
    let returned = single_return_only(method)?;
    let booleans = phpquery::nodes(&returned, &[php::PHP_BOOLEAN]);
    if booleans.len() != 1 {
        return None;
    }
    let text = node_string(&booleans[0]);
    if text.trim() != returned_expression_text(&returned) {
        return None;
    }
    match go_to_lower(text.trim()).as_str() {
        "true" => Some(true),
        "false" => Some(false),
        _ => None,
    }
}
