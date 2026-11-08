//! Plugin discovery and class-based schema scanning (port of Go
//! `context.go`).

use std::collections::{BTreeMap, HashMap};
use std::io::ErrorKind;

use serde::{Deserialize, Serialize};
use swlsp_parser_php::query as phpquery;

use crate::gopath;
use crate::import::{
    ParsedSource, Resolver, attach_translation, import_bulk_extension_parsed,
    import_definition_parsed, import_extension_parsed, import_translation_parsed,
};
use crate::model::*;
use crate::snapshot::SNAPSHOT_RELATIVE_DIRECTORY;
use crate::util::*;

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct PluginContext {
    #[serde(skip)]
    pub root: String,
    #[serde(rename = "rootUri")]
    pub root_uri: String,
    pub composer_name: String,
    pub plugin_class: String,
    #[serde(skip)]
    pub source_root: String,
    #[serde(rename = "sourceRootUri")]
    pub source_root_uri: String,
    pub base_namespace: String,
    pub namespace: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub shopware_version: String,
    #[serde(rename = "serviceUris", skip_serializing_if = "Vec::is_empty")]
    pub service_uris: Vec<String>,
    #[serde(skip)]
    pub snapshot_directory: String,
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct ComposerFile {
    name: String,
    #[serde(rename = "type")]
    kind: String,
    require: BTreeMap<String, String>,
    autoload: ComposerAutoload,
    extra: ComposerExtra,
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct ComposerAutoload {
    #[serde(rename = "psr-4")]
    psr4: BTreeMap<String, serde_json::Value>,
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct ComposerExtra {
    #[serde(rename = "shopware-plugin-class")]
    plugin_class: String,
}

fn absolute(path: &str) -> String {
    if path.starts_with('/') {
        return gopath::clean(path);
    }
    let current = std::env::current_dir()
        .map(|current| current.to_string_lossy().into_owned())
        .unwrap_or_default();
    gopath::join(&[&current, path])
}

/// Locates the nearest Composer package without escaping the workspace.
pub fn find_plugin_context(
    workspace_root: &str,
    selected_directory: &str,
) -> Result<PluginContext> {
    let workspace_root = absolute(workspace_root);
    let selected_directory = absolute(selected_directory);
    let mut root = selected_directory.clone();
    while gopath::within_directory(&workspace_root, &root) {
        let composer_path = gopath::join(&[&root, "composer.json"]);
        match std::fs::read(&composer_path) {
            Ok(content) => return parse_plugin_context(&root, &selected_directory, &content),
            Err(error) if error.kind() == ErrorKind::NotFound => {}
            Err(error) => return Err(error.into()),
        }
        let parent = gopath::dir(&root);
        if parent == root {
            break;
        }
        root = parent;
    }
    Err(errorf!(
        "no plugin composer.json contains {selected_directory}"
    ))
}

fn parse_plugin_context(root: &str, selected: &str, content: &[u8]) -> Result<PluginContext> {
    let composer: ComposerFile = crate::gojson::from_slice(content)
        .map_err(|error| errorf!("parse plugin composer.json: {error}"))?;
    if composer.name.is_empty() {
        return Err(errorf!("plugin composer.json has no package name"));
    }
    if composer.extra.plugin_class.is_empty() && composer.kind != "shopware-platform-plugin" {
        return Err(errorf!(
            "selected Composer package is not a Shopware plugin"
        ));
    }
    let mut mappings: Vec<(String, String)> = Vec::new();
    for (namespace, raw) in &composer.autoload.psr4 {
        let paths: Vec<String> = match raw {
            serde_json::Value::String(single) => vec![single.clone()],
            serde_json::Value::Array(items) => {
                let mut paths = Vec::with_capacity(items.len());
                for item in items {
                    match item {
                        serde_json::Value::String(path) => paths.push(path.clone()),
                        _ => {
                            return Err(errorf!(
                                "parse PSR-4 mapping {namespace}: json: cannot unmarshal into []string"
                            ));
                        }
                    }
                }
                paths
            }
            serde_json::Value::Null => Vec::new(),
            _ => {
                return Err(errorf!(
                    "parse PSR-4 mapping {namespace}: json: cannot unmarshal into []string"
                ));
            }
        };
        for relative in paths {
            mappings.push((
                trim_backslash(namespace).to_owned(),
                gopath::join(&[root, &relative]),
            ));
        }
    }
    mappings.sort_by_key(|mapping| std::cmp::Reverse(mapping.1.len()));
    let selected_mapping = mappings
        .iter()
        .find(|(_, mapping_root)| gopath::within_directory(mapping_root, selected))
        .or_else(|| {
            mappings
                .iter()
                .find(|(_, mapping_root)| gopath::base(mapping_root) == "src")
        });
    let Some((base_namespace, source_root)) = selected_mapping else {
        return Err(errorf!("plugin has no usable PSR-4 source mapping"));
    };
    let mut namespace = base_namespace.clone();
    if let Some(relative) = gopath::rel(source_root, selected)
        && relative != "."
        && !relative.starts_with("..")
    {
        namespace.push('\\');
        namespace.push_str(&relative.replace('/', "\\"));
    }
    let mut context = PluginContext {
        root: root.to_owned(),
        composer_name: composer.name.clone(),
        plugin_class: trim_backslash(&composer.extra.plugin_class).to_owned(),
        source_root: source_root.clone(),
        base_namespace: base_namespace.clone(),
        namespace: trim_backslash(&namespace).to_owned(),
        shopware_version: composer
            .require
            .get("shopware/core")
            .cloned()
            .unwrap_or_default(),
        snapshot_directory: gopath::join(&[root, SNAPSHOT_RELATIVE_DIRECTORY]),
        ..PluginContext::default()
    };
    for name in [
        "services.yaml",
        "services.yml",
        "services.php",
        "services.xml",
    ] {
        let path = gopath::join(&[source_root, "Resources", "config", name]);
        if std::fs::metadata(&path).is_ok_and(|metadata| !metadata.is_dir()) {
            context.service_uris.push(path);
        }
    }
    Ok(context)
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct ScannedDefinition {
    pub path: String,
    pub spec: EntitySpec,
}

/// Imports the literal portion of every class-based definition, extension
/// and bulk extension in the plugin. Unsupported field expressions stay
/// locked.
pub fn scan_plugin_schema(plugin_root: &str) -> Result<(Schema, Vec<ScannedDefinition>)> {
    scan_plugin_schema_with_lookup(plugin_root, None)
}

fn walk_php_files(directory: &str, files: &mut Vec<String>) -> std::io::Result<()> {
    let mut entries: Vec<(String, std::fs::FileType)> = std::fs::read_dir(directory)?
        .map(|entry| {
            entry.and_then(|entry| {
                Ok((
                    entry.file_name().to_string_lossy().into_owned(),
                    entry.file_type()?,
                ))
            })
        })
        .collect::<std::io::Result<_>>()?;
    entries.sort_by(|left, right| left.0.cmp(&right.0));
    for (name, file_type) in entries {
        let path = gopath::join(&[directory, &name]);
        if file_type.is_dir() {
            if matches!(
                name.as_str(),
                "vendor" | "node_modules" | "var" | "Test" | "Tests"
            ) {
                continue;
            }
            walk_php_files(&path, files)?;
        } else if gopath::ext(&path) == ".php" {
            files.push(path);
        }
    }
    Ok(())
}

/// Augments plugin-local relation targets with the workspace DAL index; the
/// importer falls back to Shopware class-name conventions.
pub fn scan_plugin_schema_with_lookup(
    plugin_root: &str,
    external: Option<RelationLookup>,
) -> Result<(Schema, Vec<ScannedDefinition>)> {
    let source_root = gopath::join(&[plugin_root, "src"]);
    let mut files = Vec::new();
    match std::fs::metadata(&source_root) {
        Err(error) if error.kind() == ErrorKind::NotFound => {
            return Ok((empty_schema(), Vec::new()));
        }
        Err(error) => return Err(error.into()),
        Ok(_) => {}
    }
    if let Err(error) = walk_php_files(&source_root, &mut files) {
        if error.kind() == ErrorKind::NotFound {
            return Ok((empty_schema(), Vec::new()));
        }
        return Err(error.into());
    }
    let mut declarations = Vec::new();
    for path in files {
        let content = std::fs::read(&path)?;
        let source = String::from_utf8_lossy(&content).into_owned();
        let parsed = swlsp_parser_php::parse(&source);
        let root = parsed.root();
        let namespace = trim_backslash(&phpquery::namespace(&root)).to_owned();
        let resolve = Resolver::new(&root);
        for class in phpquery::classes(&root) {
            let name = phpquery::class_name(&class);
            if name.is_empty() {
                continue;
            }
            declarations.push(ClassBasedDeclaration {
                path: path.clone(),
                source: source.clone(),
                class: qualify(&namespace, &name),
                parents: phpquery::class_extends(&class)
                    .iter()
                    .map(|parent| resolve.resolve(parent))
                    .collect(),
                abstract_class: phpquery::is_abstract(&class),
            });
        }
    }
    scan_class_based_declarations(plugin_root, declarations, external, true, None, None)
}

/// Class metadata needed to discover class-based DAL definitions without
/// scanning the filesystem; paths and inheritance come from the PHP index.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct IndexedClassDeclaration {
    pub path: String,
    pub class: String,
    pub parents: Vec<String>,
    pub abstract_class: bool,
    pub source: String,
}

/// Imports only class-based DAL candidates selected by the PHP semantic
/// index. Stale deleted entries are ignored.
pub fn scan_indexed_plugin_schema(
    plugin_root: &str,
    indexed: &[IndexedClassDeclaration],
    external: Option<RelationLookup>,
) -> Result<(Schema, Vec<ScannedDefinition>)> {
    scan_indexed_plugin_schema_with_enricher(plugin_root, indexed, external, true, None, None)
}

/// The production request path: every selected DAL class must have source
/// captured by the source index; missing source is never read from disk.
pub fn scan_indexed_plugin_schema_sources(
    plugin_root: &str,
    indexed: &[IndexedClassDeclaration],
    external: Option<RelationLookup>,
) -> Result<(Schema, Vec<ScannedDefinition>)> {
    scan_indexed_plugin_schema_with_enricher(plugin_root, indexed, external, false, None, None)
}

pub(crate) type Enricher<'a> = Option<&'a dyn Fn(&mut EntitySpec)>;

pub(crate) fn scan_indexed_plugin_schema_with_enricher(
    plugin_root: &str,
    indexed: &[IndexedClassDeclaration],
    external: Option<RelationLookup>,
    allow_disk: bool,
    enrich: Enricher,
    memo: Option<&ImportMemo>,
) -> Result<(Schema, Vec<ScannedDefinition>)> {
    let declarations = indexed
        .iter()
        .filter(|candidate| {
            !candidate.path.is_empty()
                && !candidate.class.is_empty()
                && gopath::within_directory(plugin_root, &candidate.path)
        })
        .map(|candidate| ClassBasedDeclaration {
            path: gopath::clean(&candidate.path),
            source: candidate.source.clone(),
            class: trim_backslash(&candidate.class).to_owned(),
            parents: candidate.parents.clone(),
            abstract_class: candidate.abstract_class,
        })
        .collect();
    scan_class_based_declarations(
        plugin_root,
        declarations,
        external,
        allow_disk,
        enrich,
        memo,
    )
}

pub(crate) fn scan_class_based_declarations(
    _plugin_root: &str,
    mut declarations: Vec<ClassBasedDeclaration>,
    external: Option<RelationLookup>,
    read_missing_sources: bool,
    enrich: Enricher,
    memo: Option<&ImportMemo>,
) -> Result<(Schema, Vec<ScannedDefinition>)> {
    let kinds = resolve_class_based_kinds(&declarations);
    hydrate_class_based_sources(&mut declarations, &kinds, read_missing_sources)?;
    let cache = SourceCache {
        parsed: Default::default(),
        memo,
    };
    let targets = class_based_relation_targets(&declarations, &kinds, enrich, &cache);
    let lookup = |class: &str| -> Option<RelationTarget> {
        if let Some(target) = targets.get(trim_backslash(class)) {
            return Some(target.clone());
        }
        external.and_then(|external| external(class))
    };
    let (mut definitions, definition_indexes) =
        import_scanned_class_based_definitions(&declarations, &kinds, &lookup, enrich, &cache);
    attach_scanned_translations(
        &mut definitions,
        &definition_indexes,
        &declarations,
        &kinds,
        &lookup,
        enrich,
        &cache,
    );
    let mut schema = empty_schema();
    for definition in &definitions {
        merge_spec_schema(&mut schema, &definition.spec)
            .map_err(|error| errorf!("import {}: {error}", definition.path))?;
    }
    definitions.sort_by(|left, right| {
        left.spec
            .entity_name
            .cmp(&right.spec.entity_name)
            .then_with(|| left.spec.definition_class.cmp(&right.spec.definition_class))
    });
    Ok((schema, definitions))
}

fn hydrate_class_based_sources(
    declarations: &mut [ClassBasedDeclaration],
    kinds: &HashMap<String, String>,
    read_missing_sources: bool,
) -> Result<()> {
    let mut sources: HashMap<String, String> = HashMap::new();
    for declaration in declarations.iter_mut() {
        let kind = kinds
            .get(&go_to_lower(&declaration.class))
            .map(String::as_str)
            .unwrap_or_default();
        if declaration.abstract_class || kind.is_empty() || !declaration.source.is_empty() {
            continue;
        }
        if !read_missing_sources {
            return Err(errorf!(
                "indexed DAL class source is unavailable for {}",
                declaration.class
            ));
        }
        if let Some(source) = sources.get(&declaration.path) {
            declaration.source = source.clone();
            continue;
        }
        match std::fs::read(&declaration.path) {
            Ok(content) => {
                let source = String::from_utf8_lossy(&content).into_owned();
                sources.insert(declaration.path.clone(), source.clone());
                declaration.source = source;
            }
            Err(error) if error.kind() == ErrorKind::NotFound => {}
            Err(error) => {
                return Err(errorf!(
                    "read indexed DAL class {}: {error}",
                    declaration.path
                ));
            }
        }
    }
    Ok(())
}

fn kind_of<'a>(kinds: &'a HashMap<String, String>, class: &str) -> &'a str {
    kinds
        .get(&go_to_lower(class))
        .map(String::as_str)
        .unwrap_or_default()
}

fn class_based_relation_targets(
    declarations: &[ClassBasedDeclaration],
    kinds: &HashMap<String, String>,
    enrich: Enricher,
    cache: &SourceCache,
) -> HashMap<String, RelationTarget> {
    let mut targets = HashMap::new();
    for declaration in declarations {
        let kind = kind_of(kinds, &declaration.class);
        if declaration.abstract_class
            || declaration.source.is_empty()
            || kind != CLASS_BASED_ENTITY && kind != CLASS_BASED_MAPPING
        {
            continue;
        }
        let Ok(mut spec) =
            cache.import_spec(PASS_TARGETS, declaration, kind, None, |parsed, lookup| {
                import_definition_parsed(parsed, lookup, &declaration.class, kind)
            })
        else {
            continue;
        };
        if let Some(enrich) = enrich {
            enrich(&mut spec);
        }
        let mut target = RelationTarget {
            definition_class: spec.definition_class.clone(),
            entity_class: spec.entity_class.clone(),
            collection_class: spec.collection_class.clone(),
            entity_name: spec.entity_name.clone(),
            file_uri: declaration.path.clone(),
            inheritance_aware: spec.inheritance_aware,
            ..RelationTarget::default()
        };
        let explicit_version = spec
            .definition_behavior
            .as_ref()
            .and_then(|behavior| behavior.version_aware);
        if let Some(version_aware) = explicit_version {
            target.version_aware = version_aware;
        }
        for field in schema_definition_fields(&spec).iter() {
            if field.kind == FIELD_VERSION && explicit_version.is_none() {
                target.version_aware = true;
            }
            if field.kind == FIELD_LOCKED
                || field.storage_name.is_empty()
                || field.kind == FIELD_ONE_TO_MANY
                || field.kind == FIELD_MANY_TO_MANY
                || field.uses_existing_column
            {
                continue;
            }
            target.fields.push(RelationTargetField {
                property_name: field.property_name.clone(),
                storage_name: field.storage_name.clone(),
                primary: field.primary || field.kind == FIELD_ID || field.kind == FIELD_VERSION,
            });
        }
        if !spec.entity_name.is_empty() {
            targets.insert(spec.entity_name.clone(), target.clone());
        }
        targets.insert(spec.definition_class.clone(), target);
    }
    targets
}

fn import_scanned_class_based_definitions(
    declarations: &[ClassBasedDeclaration],
    kinds: &HashMap<String, String>,
    lookup: RelationLookup,
    enrich: Enricher,
    cache: &SourceCache,
) -> (Vec<ScannedDefinition>, HashMap<String, usize>) {
    let mut definitions = Vec::new();
    let mut definition_indexes = HashMap::new();
    for declaration in declarations {
        if declaration.abstract_class || declaration.source.is_empty() {
            continue;
        }
        let kind = kind_of(kinds, &declaration.class);
        if kind.is_empty() || kind == CLASS_BASED_TRANSLATION {
            continue;
        }
        let Ok(mut spec) = cache.import_spec(
            PASS_DEFINITIONS,
            declaration,
            kind,
            Some(lookup),
            |parsed, lookup| import_class_based_spec(parsed, &declaration.class, kind, lookup),
        ) else {
            continue;
        };
        if let Some(enrich) = enrich {
            enrich(&mut spec);
        }
        if spec.definition_kind == DEFINITION_ENTITY || spec.definition_kind == DEFINITION_MAPPING {
            definition_indexes.insert(spec.definition_class.clone(), definitions.len());
        }
        definitions.push(ScannedDefinition {
            path: declaration.path.clone(),
            spec,
        });
    }
    (definitions, definition_indexes)
}

fn attach_scanned_translations(
    definitions: &mut [ScannedDefinition],
    definition_indexes: &HashMap<String, usize>,
    declarations: &[ClassBasedDeclaration],
    kinds: &HashMap<String, String>,
    lookup: RelationLookup,
    enrich: Enricher,
    cache: &SourceCache,
) {
    for declaration in declarations {
        if declaration.abstract_class
            || declaration.source.is_empty()
            || kind_of(kinds, &declaration.class) != CLASS_BASED_TRANSLATION
        {
            continue;
        }
        let Ok(mut translation) =
            cache.import_translation(declaration, Some(lookup), |parsed, lookup| {
                import_translation_parsed(parsed, lookup, &declaration.class)
            })
        else {
            continue;
        };
        let Some(&index) = definition_indexes.get(&translation.spec.parent_definition_class) else {
            continue;
        };
        let matches_parent = definitions[index]
            .spec
            .translation
            .as_ref()
            .is_some_and(|parent| {
                equal_fold(
                    trim_backslash(&parent.definition_class),
                    trim_backslash(&translation.spec.definition_class),
                )
            });
        if !matches_parent {
            continue;
        }
        translation.spec.definition_uri = declaration.path.clone();
        let directory = gopath::dir(&declaration.path);
        translation.spec.entity_uri = gopath::join(&[
            &directory,
            &format!("{}.php", short_class(&translation.spec.entity_class)),
        ]);
        translation.spec.collection_uri = gopath::join(&[
            &directory,
            &format!("{}.php", short_class(&translation.spec.collection_class)),
        ]);
        let parent = std::mem::take(&mut definitions[index].spec);
        definitions[index].spec = attach_translation(parent, translation);
        if let Some(enrich) = enrich {
            enrich(&mut definitions[index].spec);
        }
    }
}

const CLASS_BASED_ENTITY: &str = "entity";
const CLASS_BASED_MAPPING: &str = "mapping";
const CLASS_BASED_TRANSLATION: &str = "translation";
const CLASS_BASED_EXTENSION: &str = "extension";
const CLASS_BASED_BULK_EXTENSION: &str = "bulk-extension";

#[derive(Clone, Debug, Default)]
pub(crate) struct ClassBasedDeclaration {
    pub(crate) path: String,
    pub(crate) source: String,
    pub(crate) class: String,
    pub(crate) parents: Vec<String>,
    pub(crate) abstract_class: bool,
}

pub(crate) fn resolve_class_based_kinds(
    declarations: &[ClassBasedDeclaration],
) -> HashMap<String, String> {
    let mut by_class: HashMap<String, &ClassBasedDeclaration> = HashMap::new();
    for declaration in declarations {
        by_class.insert(go_to_lower(trim_backslash(&declaration.class)), declaration);
    }
    let mut result: HashMap<String, String> = HashMap::new();
    let mut resolving: std::collections::HashSet<String> = std::collections::HashSet::new();

    fn resolve(
        class: &str,
        by_class: &HashMap<String, &ClassBasedDeclaration>,
        result: &mut HashMap<String, String>,
        resolving: &mut std::collections::HashSet<String>,
    ) -> String {
        let key = go_to_lower(trim_backslash(class));
        if let Some(kind) = result.get(&key) {
            return kind.clone();
        }
        if resolving.contains(&key) {
            return String::new();
        }
        let framework = match key.as_str() {
            "shopware\\core\\framework\\dataabstractionlayer\\entitytranslationdefinition" => {
                Some(CLASS_BASED_TRANSLATION)
            }
            "shopware\\core\\framework\\dataabstractionlayer\\mappingentitydefinition" => {
                Some(CLASS_BASED_MAPPING)
            }
            "shopware\\core\\framework\\dataabstractionlayer\\entitydefinition" => {
                Some(CLASS_BASED_ENTITY)
            }
            "shopware\\core\\framework\\dataabstractionlayer\\bulkentityextension" => {
                Some(CLASS_BASED_BULK_EXTENSION)
            }
            "shopware\\core\\framework\\dataabstractionlayer\\entityextension" => {
                Some(CLASS_BASED_EXTENSION)
            }
            _ => None,
        };
        if let Some(kind) = framework {
            return kind.to_owned();
        }
        let Some(declaration) = by_class.get(&key).copied() else {
            result.insert(key, String::new());
            return String::new();
        };
        if is_runtime_class_based_template(short_class(&declaration.class)) {
            result.insert(key, String::new());
            return String::new();
        }
        resolving.insert(key.clone());
        for parent in &declaration.parents {
            let kind = resolve(parent, by_class, result, resolving);
            if !kind.is_empty() {
                resolving.remove(&key);
                result.insert(key, kind.clone());
                return kind;
            }
        }
        resolving.remove(&key);
        result.insert(key, String::new());
        String::new()
    }

    for declaration in declarations {
        let kind = resolve(&declaration.class, &by_class, &mut result, &mut resolving);
        result.insert(go_to_lower(trim_backslash(&declaration.class)), kind);
    }
    result
}

fn is_runtime_class_based_template(name: &str) -> bool {
    matches!(
        name,
        "AttributeEntityDefinition"
            | "AttributeMappingDefinition"
            | "AttributeTranslationDefinition"
            | "DynamicEntityDefinition"
            | "DynamicMappingEntityDefinition"
            | "DynamicTranslationEntityDefinition"
            | "FilteredBulkEntityExtension"
    )
}

fn import_class_based_spec(
    parsed: &ParsedSource,
    class: &str,
    kind: &str,
    lookup: Option<RelationLookup>,
) -> Result<EntitySpec> {
    match kind {
        CLASS_BASED_ENTITY | CLASS_BASED_MAPPING => {
            import_definition_parsed(parsed, lookup, class, kind)
        }
        CLASS_BASED_EXTENSION => import_extension_parsed(parsed, lookup, class),
        CLASS_BASED_BULK_EXTENSION => import_bulk_extension_parsed(parsed, lookup, class),
        _ => Err(errorf!(
            "unsupported class-based DAL kind {}",
            go_quote(kind)
        )),
    }
}

/// Parses every declaration source once per scan: several classes share a
/// file, and each definition is imported twice (relation target and spec).
struct SourceCache<'m> {
    parsed: std::cell::RefCell<HashMap<String, (String, std::rc::Rc<ParsedSource>)>>,
    memo: Option<&'m ImportMemo>,
}

const PASS_TARGETS: u8 = 0;
const PASS_DEFINITIONS: u8 = 1;
const PASS_TRANSLATIONS: u8 = 2;

impl SourceCache<'_> {
    fn get(&self, declaration: &ClassBasedDeclaration) -> std::rc::Rc<ParsedSource> {
        if let Some((source, parsed)) = self.parsed.borrow().get(&declaration.path)
            && *source == declaration.source
        {
            return parsed.clone();
        }
        let parsed = std::rc::Rc::new(ParsedSource::new(&declaration.source));
        self.parsed.borrow_mut().insert(
            declaration.path.clone(),
            (declaration.source.clone(), parsed.clone()),
        );
        parsed
    }

    fn import_spec(
        &self,
        pass: u8,
        declaration: &ClassBasedDeclaration,
        kind: &str,
        lookup: Option<RelationLookup>,
        run: impl FnOnce(&ParsedSource, Option<RelationLookup>) -> Result<EntitySpec>,
    ) -> Result<EntitySpec> {
        let imported = self.memoized(pass, declaration, kind, lookup, |parsed, lookup| {
            Imported::Spec(Box::new(run(parsed, lookup)))
        });
        match imported {
            Imported::Spec(result) => *result,
            Imported::Translation(_) => unreachable!("spec passes memoize specs"),
        }
    }

    fn import_translation(
        &self,
        declaration: &ClassBasedDeclaration,
        lookup: Option<RelationLookup>,
        run: impl FnOnce(&ParsedSource, Option<RelationLookup>) -> Result<ImportedTranslation>,
    ) -> Result<ImportedTranslation> {
        let imported = self.memoized(
            PASS_TRANSLATIONS,
            declaration,
            "",
            lookup,
            |parsed, lookup| Imported::Translation(Box::new(run(parsed, lookup))),
        );
        match imported {
            Imported::Translation(result) => *result,
            Imported::Spec(_) => unreachable!("the translation pass memoizes translations"),
        }
    }

    /// Imports are deterministic in (source, class, kind) and the answers of
    /// the relation lookups they make, so a previous result is reused when
    /// the source is unchanged and replaying its lookups gives equal answers.
    fn memoized(
        &self,
        pass: u8,
        declaration: &ClassBasedDeclaration,
        kind: &str,
        lookup: Option<RelationLookup>,
        run: impl FnOnce(&ParsedSource, Option<RelationLookup>) -> Imported,
    ) -> Imported {
        let Some(memo) = self.memo else {
            return run(&self.get(declaration), lookup);
        };
        let key = (
            pass,
            declaration.path.clone(),
            declaration.class.clone(),
            kind.to_owned(),
        );
        if let Some((trace, value)) = memo.cached(&key, &declaration.source)
            && trace
                .iter()
                .all(|(query, answer)| lookup.and_then(|lookup| lookup(query)) == *answer)
        {
            return value;
        }
        let trace = std::cell::RefCell::new(Vec::new());
        let record = |query: &str| -> Option<RelationTarget> {
            let answer = lookup.and_then(|lookup| lookup(query));
            trace.borrow_mut().push((query.to_owned(), answer.clone()));
            answer
        };
        let recording: Option<RelationLookup> = lookup.map(|_| &record as RelationLookup);
        let value = run(&self.get(declaration), recording);
        memo.store(
            key,
            declaration.source.clone(),
            trace.into_inner(),
            value.clone(),
        );
        value
    }
}

impl Drop for SourceCache<'_> {
    fn drop(&mut self) {
        if let Some(memo) = self.memo {
            memo.finish_scan();
        }
    }
}

#[derive(Clone)]
enum Imported {
    Spec(Box<Result<EntitySpec>>),
    Translation(Box<Result<ImportedTranslation>>),
}

type MemoKey = (u8, String, String, String);
type LookupTrace = Vec<(String, Option<RelationTarget>)>;

struct MemoEntry {
    source: String,
    trace: LookupTrace,
    value: Imported,
    last_scan: u64,
}

/// Import results of previous scans, owned by a long-lived catalog.
#[derive(Default)]
pub(crate) struct ImportMemo {
    state: std::sync::Mutex<MemoState>,
}

#[derive(Default)]
struct MemoState {
    scan: u64,
    entries: HashMap<MemoKey, MemoEntry>,
}

/// Entries unused for this many scans are dropped.
const MEMO_RETENTION_SCANS: u64 = 16;

impl ImportMemo {
    fn lock(&self) -> std::sync::MutexGuard<'_, MemoState> {
        self.state
            .lock()
            .unwrap_or_else(|poison| poison.into_inner())
    }

    fn cached(&self, key: &MemoKey, source: &str) -> Option<(LookupTrace, Imported)> {
        let mut state = self.lock();
        let scan = state.scan;
        let entry = state
            .entries
            .get_mut(key)
            .filter(|entry| entry.source == source)?;
        entry.last_scan = scan;
        Some((entry.trace.clone(), entry.value.clone()))
    }

    fn store(&self, key: MemoKey, source: String, trace: LookupTrace, value: Imported) {
        let mut state = self.lock();
        let last_scan = state.scan;
        state.entries.insert(
            key,
            MemoEntry {
                source,
                trace,
                value,
                last_scan,
            },
        );
    }

    fn finish_scan(&self) {
        let mut state = self.lock();
        state.scan += 1;
        let scan = state.scan;
        state
            .entries
            .retain(|_, entry| entry.last_scan + MEMO_RETENTION_SCANS >= scan);
    }
}
