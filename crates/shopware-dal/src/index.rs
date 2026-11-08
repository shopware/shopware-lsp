//! Entity definition indexing (port of Go `dal/index.go` and
//! `dal/workspace_symbols.go`).

use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::sync::LazyLock;

use regex::Regex;
use serde::{Deserialize, Serialize};
use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Result, Store, TransactionalClearer,
    TransactionalRemover, WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
    WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER, WorkspaceSymbol, WorkspaceSymbolKind,
    WorkspaceSymbolRange,
};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as php;
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_php_types::{equal_fold as go_equal_fold, go_to_lower};
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::context::go_trim_space;

// Go RE2: `\b` and `\s` are ASCII-only.
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
    pub primary: bool,
    pub line: u32,
    pub range: TextRange,
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
        all.retain(|definition| definition.kind != DEFINITION_KIND_UNRESOLVED);
        Ok(all)
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
        self.definitions.get_values(name)
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
        for definition in self.definitions()? {
            for field in &definition.fields {
                if field.name != name || associations_only && !field.association {
                    continue;
                }
                result.push(FieldDefinition {
                    entity: definition.name.clone(),
                    class: definition.class.clone(),
                    file: definition.file.clone(),
                    field: field.clone(),
                });
            }
        }
        Ok(result)
    }
}

impl Indexer for Index {
    fn id(&self) -> &str {
        "shopware.dal"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        if file.extension() != ".php" {
            return Ok(());
        }
        let path = file.path();
        let source = file.source();
        let mut definitions: Vec<Definition> = Vec::new();
        let candidate = source.contains("defineFields")
            && (source.contains("ENTITY_NAME") || source.contains("getEntityName"));
        if candidate && let Some(tree) = file.syntax_tree() {
            let root = tree.root();
            for class in phpquery::classes(&root) {
                let Some(definition) = parse_definition(&class, &root, path, file.line_index())
                else {
                    continue;
                };
                // A repeated entity name keeps the last definition (Go map).
                match definitions
                    .iter_mut()
                    .find(|existing| existing.name == definition.name)
                {
                    Some(existing) => *existing = definition,
                    None => definitions.push(definition),
                }
            }
        }
        // Saving an empty set clears records of a file that stopped defining
        // entities.
        self.definitions.batch_save_items_in(
            file.mutation(),
            [(
                path,
                definitions
                    .iter()
                    .map(|definition| (definition.name.as_str(), definition)),
            )],
        )?;
        if !definitions.is_empty() {
            add_definition_workspace_symbols(file, &definitions);
        }
        Ok(())
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

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
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

fn short_name(name: &str) -> &str {
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
    for method in phpquery::methods(class) {
        match phpquery::method_name(&method).as_str() {
            "getEntityName" => {
                if let Some(string) = phpquery::nodes(&method, &[php::PHP_STRING]).first() {
                    name = phpquery::string_value(string);
                    name_range = phpquery::string_content_range(string);
                }
            }
            "defineFields" => {
                defines_fields = true;
                fields = parse_fields(&method, lines, &resolver);
                if fields.iter().any(|field| field.type_ == "VersionField") {
                    version_aware = true;
                }
            }
            "getEntityClass" => entity_class = returned_class_name(&method, &resolver),
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
        version_aware,
        inheritance_aware,
    })
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
    Some(go_equal_fold(go_trim_space(&text), "true"))
}

fn parse_fields(method: &SyntaxNode, lines: &LineIndex, resolver: &ClassResolver) -> Vec<Field> {
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
        if let Some(item) = phpquery::array_item_at(&creation) {
            for flag in phpquery::object_creations(&item, &[]) {
                match short_name(phpquery::object_class_name(&flag).trim_matches('\\')) {
                    "PrimaryKey" => primary = true,
                    "Runtime" => stored = false,
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
            primary,
            line: line + 1,
            range,
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

/// Resolves class names against the file's namespace and class imports
/// (Go `definitionClassResolver`).
struct ClassResolver {
    namespace: String,
    current_class: String,
    aliases: HashMap<String, String>,
}

impl ClassResolver {
    fn new(root: &SyntaxNode) -> Self {
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
                        go_to_lower(&imported.alias),
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

    fn resolve(&self, name: &str) -> String {
        let name = go_trim_space(name).trim_matches('\\');
        if name.is_empty() {
            return String::new();
        }
        if go_equal_fold(name, "self") || go_equal_fold(name, "static") {
            return self.current_class.clone();
        }
        let (head, rest) = match name.split_once('\\') {
            Some((head, rest)) => (head, Some(rest)),
            None => (name, None),
        };
        if let Some(target) = self.aliases.get(&go_to_lower(head)) {
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

fn add_definition_workspace_symbols(file: &ParsedFile, definitions: &[Definition]) {
    let mut result = Vec::new();
    for definition in definitions {
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
    file.add_workspace_symbols(result);
}
