//! Entity definition indexing.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::path::Path;
use std::sync::LazyLock;

use regex::Regex;
use serde::{Deserialize, Serialize};
use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover, WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
    WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER, WorkspaceSymbol, WorkspaceSymbolKind,
    WorkspaceSymbolRange,
};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as php;
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::extension::{
    ConstantNames, DEFINITION_KIND_EXTENSION, apply_extensions, parse_extensions, string_constants,
};

// Word boundaries and whitespace are ASCII-only.
static ENTITY_NAME_CONSTANT_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?m)(?-u:\b)ENTITY_NAME[\t\n\x0C\r ]*=[\t\n\x0C\r ]*['"]([^'"]+)['"]"#)
        .expect("valid ENTITY_NAME pattern")
});

pub const DEFINITION_KIND_UNRESOLVED: &str = "unresolved";

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Definition {
    pub name: String,
    pub kind: String,
    pub class: String,
    pub fully_qualified_class: String,
    pub entity_class: String,
    pub collection_class: String,
    pub file: String,
    pub line: u32,
    pub name_range: TextRange,
    pub class_range: TextRange,
    pub fields: Vec<Field>,
    pub version_aware: bool,
    pub inheritance_aware: bool,
    /// Whether the definition has its own `getEntityClass()`; without
    /// one, `entity_class` is the conventional name and the DAL hydrates
    /// the generic `ArrayEntity`.
    pub entity_class_declared: bool,
    /// The string constants of a classic definition class. Extensions name
    /// entities and associations through them.
    pub constants: BTreeMap<String, String>,
}

impl Definition {
    /// The file that declares `field`: this definition's, or the entity
    /// extension that added it.
    pub fn field_file<'a>(&'a self, field: &'a Field) -> &'a str {
        if field.source_file.is_empty() {
            &self.file
        } else {
            &field.source_file
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Field {
    pub name: String,
    pub storage_name: String,
    #[serde(rename = "type")]
    pub type_: String,
    pub stored: bool,
    pub association: bool,
    pub target_class: String,
    /// The entity an association declared by attribute points at. Classic
    /// definitions name the target by `target_class` instead.
    pub target_entity: String,
    pub primary: bool,
    pub line: u32,
    pub range: TextRange,
    /// Flagged `Runtime`: filled by PHP code after loading.
    pub runtime: bool,
    /// Flagged `Computed`: written by the database or an indexer.
    pub computed: bool,
    /// The file of the entity extension that added the field; empty for a
    /// field of the definition itself. Set when extensions are applied.
    #[serde(skip)]
    pub source_file: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct FieldDefinition {
    pub entity: String,
    pub class: String,
    pub file: String,
    pub field: Field,
}

/// Indexes Shopware entity definitions independently of the PHP semantic
/// index so non-PHP frontends can resolve technical entity and field names
/// without knowing about PHP internals.
pub struct Index {
    definitions: DataIndexer<Definition>,
}

impl Index {
    /// Advanced after every committed change to this index; inspections
    /// declare it as an input (`ProblemAnalyzer::input_revision`).
    pub fn revision(&self) -> u64 {
        self.definitions.revision()
    }

    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let definitions = DataIndexer::repository(
            config_dir.as_ref().join("shopware_dal.db"),
            "shopware.dal.definitions",
            store,
        )?;
        Ok(Self { definitions })
    }

    /// Resolved entity, mapping and translation definitions.
    pub fn definitions(&self) -> Result<Vec<Definition>> {
        let mut all = self.definitions.get_all_values()?;
        all.retain(|definition| {
            definition.kind != DEFINITION_KIND_UNRESOLVED
                && definition.kind != DEFINITION_KIND_EXTENSION
        });
        Ok(all)
    }

    /// [`Self::definitions`] with the associations entity extensions add:
    /// the fields an entity has at runtime.
    pub fn extended_definitions(&self) -> Result<Vec<Definition>> {
        let (extensions, mut definitions): (Vec<_>, Vec<_>) = self
            .definitions
            .get_all_values()?
            .into_iter()
            .filter(|definition| definition.kind != DEFINITION_KIND_UNRESOLVED)
            .partition(|definition| definition.kind == DEFINITION_KIND_EXTENSION);
        apply_extensions(&mut definitions, extensions);
        Ok(definitions)
    }

    /// Entity-shaped classes whose effective DAL parent must be resolved
    /// through the shared PHP semantic hierarchy. They are excluded from
    /// ordinary DAL consumers until that proof is available.
    pub fn unresolved_definitions(&self) -> Result<Vec<Definition>> {
        let mut all = self.definitions.get_all_values()?;
        all.retain(|definition| definition.kind == DEFINITION_KIND_UNRESOLVED);
        Ok(all)
    }

    pub fn definition(&self, name: &str) -> Result<Vec<Definition>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        let mut definitions = self.definitions.get_values(name)?;
        definitions.retain(|definition| definition.kind != DEFINITION_KIND_EXTENSION);
        Ok(definitions)
    }

    /// The first field of each distinct name across resolved definitions.
    pub fn fields(&self) -> Result<Vec<Field>> {
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for definition in self.definitions()? {
            for field in definition.fields {
                if seen.insert(field.name.clone()) {
                    result.push(field);
                }
            }
        }
        Ok(result)
    }

    pub fn field_definitions(
        &self,
        name: &str,
        associations_only: bool,
    ) -> Result<Vec<FieldDefinition>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for definition in self.extended_definitions()? {
            for field in &definition.fields {
                if field.name != name || associations_only && !field.association {
                    continue;
                }
                result.push(FieldDefinition {
                    entity: definition.name.clone(),
                    class: definition.class.clone(),
                    file: definition.field_file(field).to_owned(),
                    field: field.clone(),
                });
            }
        }
        Ok(result)
    }
}

/// The definition an association field points at.
pub fn association_target<'a>(
    definitions: &'a [Definition],
    field: &Field,
) -> Option<&'a Definition> {
    if !field.association {
        return None;
    }
    if !field.target_entity.is_empty() {
        return entity_definition(definitions, &field.target_entity);
    }
    let class = field.target_class.trim_start_matches('\\');
    if class.is_empty() {
        return None;
    }
    definitions
        .iter()
        .find(|definition| definition.fully_qualified_class.trim_start_matches('\\') == class)
}

/// The definition of `entity`; the entity itself wins over a same-named
/// record of another kind.
pub fn entity_definition<'a>(
    definitions: &'a [Definition],
    entity: &str,
) -> Option<&'a Definition> {
    definitions
        .iter()
        .filter(|definition| definition.name == entity)
        .min_by_key(|definition| definition.kind != "entity")
}

/// Follows `associations` from `entity`, one association field per segment.
/// `None` when a segment is no association of the entity reached so far.
pub fn resolve_association_path<'a>(
    definitions: &'a [Definition],
    entity: &str,
    associations: &[String],
) -> Option<&'a Definition> {
    let mut current = entity_definition(definitions, entity)?;
    for segment in associations {
        let field = current
            .fields
            .iter()
            .find(|field| field.association && field.name == *segment)?;
        current = association_target(definitions, field)?;
    }
    Some(current)
}

impl Index {
    /// Stores extracted definitions and their workspace symbols.
    fn persist(&self, file: &ParsedFile, prepared: &PreparedDefinitions) -> Result<()> {
        let Some(extracted) = &prepared.0 else {
            return Ok(());
        };
        // Saving an empty set clears records of a file that stopped defining
        // entities.
        self.definitions.batch_save_items_in(
            file.mutation(),
            [(
                file.path(),
                extracted
                    .definitions
                    .iter()
                    .map(|definition| (definition.name.as_str(), definition)),
            )],
        )?;
        if !extracted.symbols.is_empty() {
            file.add_workspace_symbols(extracted.symbols.iter().cloned());
        }
        Ok(())
    }
}

/// Definitions extracted from one file; `None` leaves the file alone.
struct PreparedDefinitions(Option<ExtractedDefinitions>);

struct ExtractedDefinitions {
    definitions: Vec<Definition>,
    symbols: Vec<WorkspaceSymbol>,
}

/// Parses one PHP file's entity definitions without writing anything.
fn extract_definitions(file: &ParsedFile) -> PreparedDefinitions {
    if file.extension() != ".php" {
        return PreparedDefinitions(None);
    }
    let path = file.path();
    let source = file.source();
    let mut definitions: Vec<Definition> = Vec::new();
    let candidate = source.contains("defineFields")
        && (source.contains("ENTITY_NAME") || source.contains("getEntityName"));
    // An entity can also be a class with the `#[Entity('name')]` attribute.
    let attributed = source.contains("#[Entity(");
    let extending = source.contains("EntityExtension");
    if (candidate || attributed || extending)
        && let Some(tree) = file.syntax_tree()
    {
        let root = tree.root();
        for class in phpquery::classes(&root) {
            let parsed = if candidate {
                parse_definition(&class, &root, path, file.line_index())
                    .into_iter()
                    .collect()
            } else {
                Vec::new()
            };
            let parsed = if parsed.is_empty() && attributed {
                parse_attribute_entity(&class, &root, path, file.line_index())
            } else {
                parsed
            };
            for definition in parsed {
                // A repeated entity name keeps the last definition.
                match definitions.iter_mut().find(|existing| {
                    existing.name == definition.name && existing.kind != DEFINITION_KIND_EXTENSION
                }) {
                    Some(existing) => *existing = definition,
                    None => definitions.push(definition),
                }
            }
            if extending {
                definitions.extend(parse_extensions(&class, &root, path, file.line_index()));
            }
        }
    }
    let symbols = definition_workspace_symbols(file, &definitions);
    PreparedDefinitions(Some(ExtractedDefinitions {
        definitions,
        symbols,
    }))
}

impl Indexer for Index {
    fn id(&self) -> &str {
        "shopware.dal"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let prepared = extract_definitions(file);
        self.persist(file, &prepared)
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.definitions.batch_delete_by_file_paths(paths)
    }

    fn close(&self) -> Result<()> {
        self.definitions.close()
    }

    fn clear(&self) -> Result<()> {
        self.definitions.clear()
    }

    fn as_preparing(&self) -> Option<&dyn PreparingIndexer> {
        Some(self)
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl PreparingIndexer for Index {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(Some(Box::new(extract_definitions(file))))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        match prepared.and_then(|value| value.downcast_ref::<PreparedDefinitions>()) {
            Some(prepared) => self.persist(file, prepared),
            None => self.index(file),
        }
    }
}

impl TransactionalRemover for Index {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.definitions
            .batch_delete_by_file_paths_in(Some(mutation), paths)
    }
}

impl TransactionalClearer for Index {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.definitions.clear_in(Some(mutation))
    }
}

pub(crate) fn short_name(name: &str) -> &str {
    name.rsplit('\\').next().unwrap_or(name)
}

fn parse_definition(
    class: &SyntaxNode,
    root: &SyntaxNode,
    file: &str,
    lines: &LineIndex,
) -> Option<Definition> {
    if phpquery::is_abstract(class) {
        return None;
    }
    let mut definition_kind = "";
    for parent in phpquery::class_extends(class) {
        match short_name(&parent) {
            "EntityDefinition" => definition_kind = "entity",
            "MappingEntityDefinition" => definition_kind = "mapping",
            "EntityTranslationDefinition" => definition_kind = "translation",
            _ => {}
        }
    }
    if definition_kind.is_empty() {
        definition_kind = DEFINITION_KIND_UNRESOLVED;
    }
    let class_name = phpquery::class_name(class);
    if class_name.is_empty() {
        return None;
    }
    let resolver = ClassResolver::new(root);
    let mut name = String::new();
    let mut name_range = TextRange::default();
    let mut entity_class = String::new();
    let mut collection_class = String::new();
    let mut fields = Vec::new();
    let mut version_aware = false;
    let mut explicit_version_aware = None;
    let mut inheritance_aware = false;
    let mut defines_fields = false;
    // The fields of an overridden `defaultFields()`.
    let mut default_fields = None;
    let mut entity_class_declared = false;
    for method in phpquery::methods(class) {
        match phpquery::method_name(&method).as_str() {
            "getEntityName" => {
                if let Some(string) = phpquery::nodes(&method, &[php::PHP_STRING]).first() {
                    name = phpquery::string_value(string);
                    name_range = phpquery::string_content_range(string);
                }
            }
            "defaultFields" => default_fields = Some(parse_fields(&method, lines, &resolver, None)),
            "defineFields" => {
                defines_fields = true;
                fields = parse_fields(&method, lines, &resolver, None);
                if fields.iter().any(|field| field.type_ == "VersionField") {
                    version_aware = true;
                }
            }
            "getEntityClass" => {
                entity_class = returned_class_name(&method, &resolver);
                entity_class_declared = !entity_class.is_empty();
            }
            "getCollectionClass" => collection_class = returned_class_name(&method, &resolver),
            "isInheritanceAware" => {
                inheritance_aware = literal_boolean_return_value(&method) == Some(true);
            }
            "isVersionAware" => {
                if let Some(value) = literal_boolean_return_value(&method) {
                    explicit_version_aware = Some(value);
                }
            }
            _ => {}
        }
    }
    if let Some(value) = explicit_version_aware {
        version_aware = value;
    }
    if name.is_empty() {
        let text = class.text().to_string();
        if let Some(group) = ENTITY_NAME_CONSTANT_PATTERN
            .captures(&text)
            .and_then(|captures| captures.get(1))
        {
            name = group.as_str().to_owned();
            let start = class.text_range().start();
            name_range = TextRange::new(
                start + TextSize::from(group.start() as u32),
                start + TextSize::from(group.end() as u32),
            );
        }
    }
    if name.is_empty() || definition_kind == DEFINITION_KIND_UNRESOLVED && !defines_fields {
        return None;
    }
    let fully_qualified_class = resolver.resolve(&class_name);
    if definition_kind != "mapping"
        && let Some(base) = fully_qualified_class.strip_suffix("Definition")
    {
        if entity_class.is_empty() {
            entity_class = format!("{base}Entity");
        }
        if collection_class.is_empty() {
            collection_class = format!("{base}Collection");
        }
    }
    let class_range = class.range_trimmed_trivia();
    let (line, _) = lines.position(class_range.start().into());
    let defaults = match default_fields {
        Some(defaults) => defaults,
        None if matches!(definition_kind, "entity" | "translation") => {
            let (name_line, _) = lines.position(name_range.start().into());
            timestamp_fields(name_range, name_line + 1)
        }
        // A mapping definition has no default fields; an unresolved one may
        // turn out to be a mapping.
        None => Vec::new(),
    };
    append_missing(&mut fields, defaults);
    Some(Definition {
        name,
        kind: definition_kind.to_owned(),
        class: class_name,
        fully_qualified_class,
        entity_class,
        collection_class,
        file: file.to_owned(),
        line: line + 1,
        name_range,
        class_range,
        fields,
        constants: string_constants(class),
        version_aware,
        inheritance_aware,
        entity_class_declared,
    })
}

/// The associations among the field attributes of an attribute entity.
const ASSOCIATION_ATTRIBUTES: &[&str] = &[
    "OneToMany",
    "ManyToOne",
    "ManyToMany",
    "OneToOne",
    "Translations",
];
/// The attributes that make a property a field.
const FIELD_ATTRIBUTES: &[&str] = &[
    "Field",
    "ForeignKey",
    "PrimaryKey",
    "AutoIncrement",
    "Serialized",
    "Version",
    "ReferenceVersion",
    "State",
    "CustomFields",
];

/// The entity a class with `#[Entity('name')]` declares: its properties
/// carrying a field attribute are the fields. With a `#[Translations]`
/// property the DAL generates `<name>_translation` as well, holding the
/// translated fields.
fn parse_attribute_entity(
    class: &SyntaxNode,
    root: &SyntaxNode,
    file: &str,
    lines: &LineIndex,
) -> Vec<Definition> {
    let Some(attribute) = phpquery::attributes(class)
        .into_iter()
        .find(|attribute| short_name(&phpquery::attribute_name(attribute)) == "Entity")
    else {
        return Vec::new();
    };
    let Some(name_node) = phpquery::arguments(&attribute)
        .find(|argument| {
            let name = phpquery::argument_name(argument);
            name.is_empty() || name == "name"
        })
        .and_then(|argument| {
            phpquery::nodes(&argument, &[php::PHP_STRING])
                .into_iter()
                .next()
        })
    else {
        return Vec::new();
    };
    let name = phpquery::string_value(&name_node);
    let class_name = phpquery::class_name(class);
    if name.is_empty() || class_name.is_empty() {
        return Vec::new();
    }
    let mut fields = Vec::new();
    let mut translated = Vec::new();
    let mut has_translations = false;
    for property in phpquery::properties(class) {
        let attributes = phpquery::attributes(&property);
        let kinds: Vec<String> = attributes
            .iter()
            .map(|attribute| short_name(&phpquery::attribute_name(attribute)).to_owned())
            .collect();
        let association = kinds
            .iter()
            .find(|kind| ASSOCIATION_ATTRIBUTES.contains(&kind.as_str()));
        if association.is_none()
            && !kinds
                .iter()
                .any(|kind| FIELD_ATTRIBUTES.contains(&kind.as_str()))
        {
            continue;
        }
        let Some(variable) = phpquery::nodes(&property, &[php::PHP_VARIABLE])
            .into_iter()
            .next()
        else {
            continue;
        };
        let field_name = phpquery::variable_name(&variable)
            .trim_start_matches('$')
            .to_owned();
        if field_name.is_empty() {
            continue;
        }
        has_translations |= kinds.iter().any(|kind| kind == "Translations");
        let range = variable.range_trimmed_trivia();
        let (line, _) = lines.position(range.start().into());
        let field = Field {
            storage_name: snake_case(&field_name),
            name: field_name,
            type_: association.or(kinds.first()).cloned().unwrap_or_default(),
            stored: association.is_none(),
            association: association.is_some(),
            primary: kinds.iter().any(|kind| kind == "PrimaryKey"),
            target_entity: association
                .map(|kind| association_entity(&attributes, kind, &name))
                .unwrap_or_default(),
            line: line + 1,
            range,
            ..Field::default()
        };
        let is_translated = attributes.iter().any(|attribute| {
            phpquery::arguments(attribute).any(|argument| {
                phpquery::argument_name(&argument) == "translated"
                    && argument.text().to_string().to_lowercase().contains("true")
            })
        });
        if is_translated {
            translated.push(field.clone());
        }
        fields.push(field);
    }
    let name_range = phpquery::string_content_range(&name_node);
    let (name_line, _) = lines.position(name_range.start().into());
    append_missing(&mut fields, timestamp_fields(name_range, name_line + 1));
    append_missing(&mut translated, timestamp_fields(name_range, name_line + 1));
    let resolver = ClassResolver::new(root);
    let fully_qualified_class = resolver.resolve(&class_name);
    let class_range = class.range_trimmed_trivia();
    let (line, _) = lines.position(class_range.start().into());
    let entity = Definition {
        name: name.clone(),
        kind: "entity".to_owned(),
        class: class_name,
        entity_class: fully_qualified_class.clone(),
        fully_qualified_class,
        file: file.to_owned(),
        line: line + 1,
        name_range,
        class_range,
        fields,
        entity_class_declared: true,
        ..Definition::default()
    };
    let mut definitions = Vec::new();
    if has_translations {
        definitions.push(Definition {
            name: format!("{name}_translation"),
            kind: "translation".to_owned(),
            fields: translated,
            collection_class: String::new(),
            entity_class: String::new(),
            entity_class_declared: false,
            ..entity.clone()
        });
    }
    definitions.insert(0, entity);
    definitions
}

/// `createdAt` and `updatedAt`, which `EntityDefinition::defaultFields()`
/// adds to every entity. They have no declaration of their own, so they
/// point at `range`, the entity name.
fn timestamp_fields(range: TextRange, line: u32) -> Vec<Field> {
    [
        ("createdAt", "created_at", "CreatedAtField"),
        ("updatedAt", "updated_at", "UpdatedAtField"),
    ]
    .into_iter()
    .map(|(name, storage_name, type_)| Field {
        name: name.to_owned(),
        storage_name: storage_name.to_owned(),
        type_: type_.to_owned(),
        stored: true,
        line,
        range,
        ..Field::default()
    })
    .collect()
}

/// Appends the fields of `defaults` whose name `fields` does not declare.
fn append_missing(fields: &mut Vec<Field>, defaults: Vec<Field>) {
    for field in defaults {
        if !fields.iter().any(|existing| existing.name == field.name) {
            fields.push(field);
        }
    }
}

/// The entity named by an association attribute: its `entity` argument, or
/// the translation entity of `owner` for `#[Translations]`.
fn association_entity(attributes: &[SyntaxNode], kind: &str, owner: &str) -> String {
    if kind == "Translations" {
        return format!("{owner}_translation");
    }
    attributes
        .iter()
        .find(|attribute| short_name(&phpquery::attribute_name(attribute)) == kind)
        .and_then(|attribute| {
            phpquery::arguments(attribute).find(|argument| {
                let name = phpquery::argument_name(argument);
                name.is_empty() || name == "entity"
            })
        })
        .and_then(|argument| {
            phpquery::nodes(&argument, &[php::PHP_STRING])
                .into_iter()
                .next()
        })
        .map(|string| phpquery::string_value(&string))
        .unwrap_or_default()
}

fn snake_case(name: &str) -> String {
    let mut result = String::with_capacity(name.len() + 4);
    for character in name.chars() {
        if character.is_uppercase() {
            if !result.is_empty() {
                result.push('_');
            }
            result.extend(character.to_lowercase());
        } else {
            result.push(character);
        }
    }
    result
}

/// The literal boolean of a method with exactly one return statement holding
/// exactly one boolean literal.
fn literal_boolean_return_value(method: &SyntaxNode) -> Option<bool> {
    let returns = phpquery::nodes(method, &[php::PHP_RETURN_STATEMENT]);
    let [statement] = returns.as_slice() else {
        return None;
    };
    let values = phpquery::nodes(statement, &[php::PHP_BOOLEAN]);
    let [value] = values.as_slice() else {
        return None;
    };
    let text = value.text().to_string();
    Some(eq_ignore_case(text.trim(), "true"))
}

/// The fields created inside `method`. With `constants`, an association
/// may take its name from a class constant.
pub(crate) fn parse_fields(
    method: &SyntaxNode,
    lines: &LineIndex,
    resolver: &ClassResolver,
    constants: Option<&ConstantNames<'_>>,
) -> Vec<Field> {
    let mut fields = Vec::new();
    let mut seen = HashSet::new();
    for creation in phpquery::object_creations(method, &[]) {
        let field_type = phpquery::object_class_name(&creation);
        let short_type = short_name(&field_type);
        if !short_type.ends_with("Field") {
            continue;
        }
        let association = short_type.contains("AssociationField");
        let mut name_node = phpquery::string_argument(&creation, if association { 0 } else { 1 });
        if name_node.is_none() {
            name_node = phpquery::string_argument(&creation, 0);
        }
        let string_argument_value = |index| {
            phpquery::string_argument(&creation, index)
                .map_or_else(String::new, |node| phpquery::string_value(&node))
        };
        let mut name = name_node
            .as_ref()
            .map_or_else(String::new, phpquery::string_value);
        if name.is_empty()
            && association
            && let Some(constants) = constants
            && let Some(constant) =
                phpquery::argument_expression(&creation, 0).and_then(|value| constants.name(&value))
        {
            name = constant;
        }
        let mut storage_name = string_argument_value(0);
        match short_type {
            "ParentFkField" => {
                name = "parentId".to_owned();
                storage_name = "parent_id".to_owned();
            }
            "ParentAssociationField" => {
                name = "parent".to_owned();
                storage_name = "parent_id".to_owned();
            }
            "ChildrenAssociationField" => {
                name = string_argument_value(1);
                if name.is_empty() {
                    name = "children".to_owned();
                }
                storage_name = "parent_id".to_owned();
            }
            "VersionField" => {
                name = "versionId".to_owned();
                storage_name = "version_id".to_owned();
            }
            "TranslationsAssociationField" => {
                // The property name is the optional third argument.
                name = string_argument_value(2);
                if name.is_empty() {
                    name = "translations".to_owned();
                }
                storage_name = String::new();
            }
            "ChildCountField" => {
                name = "childCount".to_owned();
                storage_name = "child_count".to_owned();
            }
            "CreatedAtField" => {
                name = "createdAt".to_owned();
                storage_name = "created_at".to_owned();
            }
            "UpdatedAtField" => {
                name = "updatedAt".to_owned();
                storage_name = "updated_at".to_owned();
            }
            "AutoIncrementField" => {
                name = "autoIncrement".to_owned();
                storage_name = "auto_increment".to_owned();
            }
            _ => {}
        }
        if name.is_empty() || !seen.insert(name.clone()) {
            continue;
        }
        let range = name_node.as_ref().map_or_else(
            || creation.range_trimmed_trivia(),
            phpquery::string_content_range,
        );
        let (line, _) = lines.position(range.start().into());
        let mut primary = short_type == "IdField" || short_type == "VersionField";
        let mut stored = !association && short_type != "TranslatedField";
        let mut runtime = false;
        // Counted by an indexer, never written through the entity.
        let mut computed = short_type == "ChildCountField";
        if let Some(item) = phpquery::array_item_at(&creation) {
            for flag in phpquery::object_creations(&item, &[]) {
                match short_name(phpquery::object_class_name(&flag).trim_matches('\\')) {
                    "PrimaryKey" => primary = true,
                    "Runtime" => {
                        stored = false;
                        runtime = true;
                    }
                    "Computed" => computed = true,
                    _ => {}
                }
            }
        }
        fields.push(Field {
            name,
            storage_name,
            type_: short_type.to_owned(),
            stored,
            association,
            target_class: resolver.resolve(&phpquery::class_constant_name(&creation)),
            target_entity: String::new(),
            source_file: String::new(),
            primary,
            line: line + 1,
            range,
            runtime,
            computed,
        });
    }
    fields
}

fn returned_class_name(method: &SyntaxNode, resolver: &ClassResolver) -> String {
    for access in phpquery::nodes(method, &[php::PHP_SCOPED_ACCESS, php::PHP_MEMBER_ACCESS]) {
        let class_name = phpquery::class_constant_name(&access);
        if !class_name.is_empty() {
            return resolver.resolve(&class_name);
        }
    }
    String::new()
}

/// Resolves class names against the file's namespace and class imports.
pub(crate) struct ClassResolver {
    namespace: String,
    current_class: String,
    aliases: HashMap<String, String>,
}

impl ClassResolver {
    pub(crate) fn new(root: &SyntaxNode) -> Self {
        let namespace = phpquery::namespace(root).trim_matches('\\').to_owned();
        let mut current_class = phpquery::classes(root)
            .first()
            .map(phpquery::class_name)
            .unwrap_or_default();
        if !namespace.is_empty() && !current_class.is_empty() {
            current_class = format!("{namespace}\\{current_class}");
        }
        let mut aliases = HashMap::new();
        for declaration in phpquery::use_declarations(root) {
            for imported in parse_use_declaration(&declaration.text().to_string()) {
                if imported.kind == ImportKind::Class {
                    aliases.insert(
                        lowercase(&imported.alias),
                        imported.target.trim_matches('\\').to_owned(),
                    );
                }
            }
        }
        Self {
            namespace,
            current_class,
            aliases,
        }
    }

    pub(crate) fn resolve(&self, name: &str) -> String {
        let name = name.trim().trim_matches('\\');
        if name.is_empty() {
            return String::new();
        }
        if eq_ignore_case(name, "self") || eq_ignore_case(name, "static") {
            return self.current_class.clone();
        }
        let (head, rest) = match name.split_once('\\') {
            Some((head, rest)) => (head, Some(rest)),
            None => (name, None),
        };
        if let Some(target) = self.aliases.get(&lowercase(head)) {
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

fn definition_workspace_symbols(
    file: &ParsedFile,
    definitions: &[Definition],
) -> Vec<WorkspaceSymbol> {
    let mut result = Vec::new();
    for definition in definitions {
        // An extension is no entity of its own.
        if definition.kind == DEFINITION_KIND_EXTENSION {
            continue;
        }
        result.push(WorkspaceSymbol {
            name: definition.name.clone(),
            container_name: format!("Shopware DAL entity · {}", definition.class),
            aliases: vec![definition.class.clone()],
            path: definition.file.clone(),
            domain: "shopware.dal.entity".to_owned(),
            kind: WorkspaceSymbolKind::CLASS,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: WorkspaceSymbolRange::from_text(file, definition.name_range),
        });
        for field in &definition.fields {
            result.push(WorkspaceSymbol {
                name: field.name.clone(),
                container_name: format!(
                    "Shopware DAL field · {} · {}",
                    definition.name, field.type_
                ),
                aliases: Vec::new(),
                path: definition.file.clone(),
                domain: "shopware.dal.field".to_owned(),
                kind: WorkspaceSymbolKind::FIELD,
                priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
                range: WorkspaceSymbolRange::from_text(file, field.range),
            });
        }
    }
    result
}

impl swlsp_indexer::Revisioned for Index {
    fn revision(&self) -> u64 {
        Index::revision(self)
    }
}
