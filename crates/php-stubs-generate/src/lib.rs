//! Builds the embedded PHP runtime catalog from JetBrains' phpstorm-stubs
//! repository.
//!
//! The generator parses the locked upstream extensions with the native PHP
//! frontend, binds them with the semantic binder, applies `@since`,
//! `@removed`, `PhpStormStubsElementAvailable`, `LanguageLevelTypeAware`
//! and versioned `Deprecated` metadata per PHP version, and coalesces
//! identical declarations across versions into one record with a version
//! mask. [`encode`] writes the MessagePack catalog that
//! `swlsp-php-stubs` embeds.
//!
//! The catalog format writes an absent list as `nil` and distinguishes it
//! from an empty array, so [`encode`] takes an explicit [`EmptyListPolicy`].
//! [`EmptyListPolicy::Committed`] reproduces the committed artifact
//! (`crates/php-stubs/data/phpstorm-stubs.msgpack`), which keeps empty
//! hierarchy and `@throws` lists for class-likes and non-synthetic callables;
//! [`EmptyListPolicy::OmitEmpty`] writes those lists as `nil`.
//!
//! Declaration prefixes are cut on a character boundary after ASCII
//! lower-casing. The locked stubs only use ASCII in declaration prefixes.

mod encode;
#[cfg(test)]
mod tests;

use std::collections::{BTreeMap, HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::LazyLock;

use regex::Regex;
use serde::Deserialize;
use swlsp_php_binder::Binder;
use swlsp_php_project::Version as ProjectVersion;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{
    Attribute, CallContract, Document, Flags, Str, Symbol as SemanticSymbol, SymbolId, SymbolKind,
};
use swlsp_php_stubs::catalog::{
    Bundle, Catalog, ExtensionSymbol, FORMAT_VERSION, Parameter, Symbol, TemplateParameter,
    Version, normalize_extension,
};
use swlsp_php_types::{Kind, Type};
use swlsp_textutil::lowercase;

pub use encode::{EmptyListPolicy, encode, encode_symbol_record};

/// Regex white space: ASCII white space without `\v`.
const WS: &str = r"[\t\n\f\r ]";

static VERSION_PAIR: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(
        r#"["']([0-9]+\.[0-9]+)["']{WS}*=>{WS}*["']([^"']*)["']"#
    ))
    .unwrap()
});
static DEFAULT_TYPE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(r#"(?-u:\b)default{WS}*:{WS}*["']([^"']*)["']"#)).unwrap()
});
static FROM_VERSION: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(
        r#"(?-u:\b)from{WS}*:{WS}*["']([0-9]+(?:\.[0-9]+){{1,2}})["']"#
    ))
    .unwrap()
});
static TO_VERSION: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(
        r#"(?-u:\b)to{WS}*:{WS}*["']([0-9]+(?:\.[0-9]+){{1,2}})["']"#
    ))
    .unwrap()
});
static SINCE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(
        r"(?m)@since{WS}+([0-9]+(?:\.[0-9]+){{1,2}})(?-u:\b)"
    ))
    .unwrap()
});
static REMOVED: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(
        r"(?m)@removed{WS}+([0-9]+(?:\.[0-9]+){{1,2}})(?-u:\b)"
    ))
    .unwrap()
});
static DEPRECATED_DOC: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?mi)@deprecated(?-u:\b)").unwrap());
static DEPRECATED_SINCE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(
        r#"(?i)(?-u:\b)since{WS}*:{WS}*["']?([0-9]+(?:\.[0-9]+){{1,2}})["']?"#
    ))
    .unwrap()
});

/// Generator failure with operation context.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    /// A filesystem or process operation failed.
    #[error("{context}: {source}")]
    Io {
        context: String,
        #[source]
        source: std::io::Error,
    },
    /// The lock file is not valid JSON for a [`Lock`].
    #[error("decode PhpStorm stubs lock: {0}")]
    LockSyntax(#[from] serde_json::Error),
    /// A catalog PHP version is malformed.
    #[error("parse catalog PHP version {value:?}: {source}")]
    Version {
        value: String,
        #[source]
        source: VersionError,
    },
    /// A `LanguageLevelTypeAware` version or type does not parse.
    #[error("parse LanguageLevelTypeAware {text:?}: {reason}")]
    LanguageLevelType { text: String, reason: String },
    /// A stub or metadata file does not parse.
    #[error("parse {path}: {first} ({count} total errors)")]
    Syntax {
        path: String,
        first: String,
        count: usize,
    },
    /// A declaration could not be converted into a catalog record.
    #[error("convert {symbol} in {path}: {source}")]
    Convert {
        symbol: String,
        path: String,
        #[source]
        source: Box<Error>,
    },
    /// A parameter's type could not be determined.
    #[error("parameter {name}: {source}")]
    Parameter {
        name: String,
        #[source]
        source: Box<Error>,
    },
    /// The lock, the source checkout or the generated catalog is invalid.
    #[error("{0}")]
    Invalid(String),
}

/// A malformed `major.minor[.patch]` version.
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
pub enum VersionError {
    #[error("expected major.minor[.patch]")]
    Shape,
    #[error("invalid numeric component {0:?}")]
    Component(String),
}

fn io_error(context: impl Into<String>) -> impl FnOnce(std::io::Error) -> Error {
    let context = context.into();
    move |source| Error::Io { context, source }
}

/// The pinned source manifest (`phpstorm-stubs.lock.json`).
#[derive(Clone, Debug, Default, Deserialize, PartialEq, Eq)]
pub struct Lock {
    #[serde(default)]
    pub repository: String,
    #[serde(default)]
    pub commit: String,
    #[serde(default)]
    pub versions: Vec<String>,
    #[serde(default)]
    pub directories: Vec<String>,
}

/// Generation statistics.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Stats {
    pub files: usize,
    pub source_bytes: u64,
    pub parsed_symbols: usize,
    pub records: usize,
    pub parser_errors: usize,
}

struct SourceDocument {
    path: String,
    extension: String,
    source: String,
    document: Document,
    containers: HashMap<SymbolId, Str>,
}

pub fn load_lock(path: &Path) -> Result<Lock, Error> {
    let data = std::fs::read(path).map_err(io_error("read PhpStorm stubs lock"))?;
    let lock: Lock = serde_json::from_slice(&data)?;
    if lock.repository.is_empty()
        || lock.commit.len() != 40
        || lock.versions.is_empty()
        || lock.directories.is_empty()
    {
        return Err(Error::Invalid(
            "decode PhpStorm stubs lock: incomplete lock file".to_owned(),
        ));
    }
    if lock.versions.len() > 16 {
        return Err(Error::Invalid(
            "decode PhpStorm stubs lock: at most 16 versions are supported".to_owned(),
        ));
    }
    Ok(lock)
}

/// Verifies that the source checkout is at the locked Git revision.
pub fn verify_source(root: &Path, lock: &Lock) -> Result<(), Error> {
    let output = Command::new("git")
        .arg("-C")
        .arg(root)
        .args(["rev-parse", "HEAD"])
        .output()
        .map_err(io_error("read PhpStorm stubs source revision"))?;
    if !output.status.success() {
        return Err(Error::Invalid(format!(
            "read PhpStorm stubs source revision: {}",
            output.status
        )));
    }
    let revision = String::from_utf8_lossy(&output.stdout).trim().to_owned();
    if revision != lock.commit {
        return Err(Error::Invalid(format!(
            "PhpStorm stubs source is at {revision}; expected locked revision {}",
            lock.commit
        )));
    }
    Ok(())
}

/// Builds the packed catalog. Encode it with [`encode`].
pub fn build(root: &Path, lock: &Lock, policy: EmptyListPolicy) -> Result<(Catalog, Stats), Error> {
    let root = swlsp_php_project::clean(root);
    let (versions, catalog_versions) = parse_versions(&lock.versions)?;
    let files = source_files(&root, &lock.directories)?;
    let mut stats = Stats {
        files: files.len(),
        ..Stats::default()
    };
    let documents = load_stub_source_documents(&root, &files, &mut stats)?;
    let mut result = Catalog {
        format: FORMAT_VERSION,
        repository: lock.repository.as_str().into(),
        commit: lock.commit.as_str().into(),
        versions: catalog_versions,
        ..Catalog::default()
    };
    append_metadata_contracts(&root, &lock.directories, &mut result)?;
    append_versioned_symbols(&versions, &documents, &mut result, policy)?;
    stats.records = result.symbols.len();
    pack_bundles(&mut result, policy);
    Ok((result, stats))
}

fn load_stub_source_documents(
    root: &Path,
    files: &[PathBuf],
    stats: &mut Stats,
) -> Result<Vec<SourceDocument>, Error> {
    let binder = Binder::new();
    files
        .iter()
        .map(|path| load_stub_source_document(root, path, &binder, stats))
        .collect()
}

fn relative_slash(root: &Path, path: &Path) -> Result<String, Error> {
    let relative = path.strip_prefix(root).map_err(|_| {
        Error::Invalid(format!(
            "resolve stub path {}: not below root",
            path.display()
        ))
    })?;
    Ok(relative
        .components()
        .map(|component| component.as_os_str().to_string_lossy())
        .collect::<Vec<_>>()
        .join("/"))
}

fn load_stub_source_document(
    root: &Path,
    path: &Path,
    binder: &Binder,
    stats: &mut Stats,
) -> Result<SourceDocument, Error> {
    let content = std::fs::read(path).map_err(io_error(format!("read stub {}", path.display())))?;
    stats.source_bytes += content.len() as u64;
    let source = String::from_utf8(content).map_err(|_| {
        Error::Invalid(format!(
            "read stub {}: source is not valid UTF-8",
            path.display()
        ))
    })?;
    let parsed = swlsp_parser_php::parse(&source);
    if !parsed.errors.is_empty() {
        stats.parser_errors += parsed.errors.len();
        return Err(Error::Syntax {
            path: format!("stub {}", path.display()),
            first: parsed.errors[0].to_string(),
            count: parsed.errors.len(),
        });
    }
    let relative = relative_slash(root, path)?;
    let document = binder.bind(&format!("phpstorm-stubs://{relative}"), 1, &parsed.tree);
    stats.parsed_symbols += document.symbols.len();
    let containers = document
        .symbols
        .iter()
        .filter(|symbol| symbol.is_class_like())
        .map(|symbol| (symbol.id.clone(), symbol.fully_qualified.clone()))
        .collect();
    Ok(SourceDocument {
        extension: stub_extension(&relative),
        path: relative,
        source,
        document,
        containers,
    })
}

fn append_metadata_contracts(
    root: &Path,
    directories: &[String],
    result: &mut Catalog,
) -> Result<(), Error> {
    let mut keys = HashSet::new();
    for path in metadata_source_files(root, directories)? {
        let content = std::fs::read(&path).map_err(io_error(format!(
            "read PhpStorm metadata {}",
            path.display()
        )))?;
        let source = String::from_utf8_lossy(&content);
        let parsed = swlsp_parser_php::parse(&source);
        if !parsed.errors.is_empty() {
            return Err(Error::Syntax {
                path: format!("PhpStorm metadata {}", path.display()),
                first: parsed.errors[0].to_string(),
                count: parsed.errors.len(),
            });
        }
        for contract in swlsp_php_phpstormmeta::parse(&parsed.root()) {
            if !keys.insert(call_contract_key(&contract)) {
                continue;
            }
            result.contracts.push(contract);
            let relative = relative_slash(root, &path)?;
            result
                .contract_extensions
                .push(stub_extension(&relative).into());
        }
    }
    Ok(())
}

fn append_versioned_symbols(
    versions: &[ProjectVersion],
    documents: &[SourceDocument],
    result: &mut Catalog,
    policy: EmptyListPolicy,
) -> Result<(), Error> {
    let mut record_index: HashMap<Vec<u8>, usize> = HashMap::new();
    for (version_index, version) in versions.iter().enumerate() {
        let version_symbols = symbols_for_version(documents, *version)?;
        // BTreeMap iteration keeps the records in sorted key order.
        for (_, mut symbol) in version_symbols {
            symbol.version_mask = 0;
            let key = encode_symbol_record(&symbol, policy);
            if let Some(&index) = record_index.get(&key) {
                result.symbols[index].version_mask |= 1 << version_index;
                continue;
            }
            symbol.version_mask = 1 << version_index;
            record_index.insert(key, result.symbols.len());
            result.symbols.push(symbol);
        }
    }
    Ok(())
}

fn symbols_for_version(
    documents: &[SourceDocument],
    version: ProjectVersion,
) -> Result<BTreeMap<String, Symbol>, Error> {
    let mut result: BTreeMap<String, Symbol> = BTreeMap::new();
    for document in documents {
        let available_classes = available_class_names(document, version);
        for symbol in &document.document.symbols {
            if !supported_kind(symbol.kind) {
                continue;
            }
            let owner = document
                .containers
                .get(&symbol.container)
                .map_or("", |owner| owner.as_str());
            if !owner.is_empty() && !available_classes.contains(&lowercase(owner)) {
                continue;
            }
            let converted =
                convert_symbol(document, symbol, owner, version).map_err(|err| Error::Convert {
                    symbol: symbol.fully_qualified.to_string(),
                    path: document.path.clone(),
                    source: Box::new(err),
                })?;
            let Some(mut converted) = converted else {
                continue;
            };
            converted.extension = document.extension.as_str().into();
            let key = symbol_key(&converted);
            match result.get_mut(&key) {
                Some(existing) => {
                    if symbol_score(&converted) > symbol_score(existing) {
                        *existing = converted;
                    }
                }
                None => {
                    result.insert(key, converted);
                }
            }
        }
    }
    Ok(result)
}

/// Converts generator output into lazy extension payloads, encoded with
/// `policy`.
fn pack_bundles(catalog: &mut Catalog, policy: EmptyListPolicy) {
    if !catalog.bundles.is_empty() {
        return;
    }
    let mut grouped: BTreeMap<String, (Vec<Symbol>, Vec<CallContract>)> = BTreeMap::new();
    let mut seen_names = HashSet::new();
    for symbol in std::mem::take(&mut catalog.symbols) {
        let extension = normalize_extension(&symbol.extension);
        if symbol.container.is_empty()
            && !symbol.fully_qualified.is_empty()
            && !extension.is_empty()
            && seen_names.insert((lowercase(&symbol.fully_qualified), extension.clone()))
        {
            catalog.extension_symbols.push(ExtensionSymbol {
                name: symbol.fully_qualified.clone(),
                extension: extension.as_str().into(),
            });
        }
        grouped.entry(extension).or_default().0.push(symbol);
    }
    let extensions = std::mem::take(&mut catalog.contract_extensions);
    for (index, contract) in std::mem::take(&mut catalog.contracts)
        .into_iter()
        .enumerate()
    {
        let extension = extensions
            .get(index)
            .map_or_else(String::new, |extension| normalize_extension(extension));
        grouped.entry(extension).or_default().1.push(contract);
    }
    for (extension, (symbols, contracts)) in grouped {
        catalog.bundles.push(Bundle {
            extension: extension.as_str().into(),
            data: std::borrow::Cow::Owned(encode::encode_bundle_payload(
                &symbols, &contracts, policy,
            )),
        });
    }
    catalog.extension_symbols.sort_by(|left, right| {
        (left.name.as_str(), left.extension.as_str())
            .cmp(&(right.name.as_str(), right.extension.as_str()))
    });
}

fn stub_extension(relative: &str) -> String {
    let directory = relative.split('/').next().unwrap_or_default();
    match lowercase(directory).as_str() {
        "core" | "standard" | "superglobals" => "core".to_owned(),
        "sqlite" => "pdo_sqlite".to_owned(),
        _ => swlsp_php_project::normalize_extension(directory),
    }
}

/// The identity of a contract with a lowercased target: two contracts are
/// duplicates when their derived `Debug` output is equal.
fn call_contract_key(contract: &CallContract) -> String {
    let mut contract = contract.clone();
    contract.target.name = lowercase(&contract.target.name).into();
    contract.target.class = lowercase(&contract.target.class).into();
    format!("{contract:?}")
}

fn parse_versions(values: &[String]) -> Result<(Vec<ProjectVersion>, Vec<Version>), Error> {
    let mut versions: Vec<ProjectVersion> = Vec::with_capacity(values.len());
    let mut wire = Vec::with_capacity(values.len());
    for value in values {
        let parsed = parse_version(value).map_err(|source| Error::Version {
            value: value.clone(),
            source,
        })?;
        if versions.last().is_some_and(|previous| parsed <= *previous) {
            return Err(Error::Invalid(
                "catalog PHP versions must be strictly increasing".to_owned(),
            ));
        }
        if parsed.major > 255 || parsed.minor > 255 {
            return Err(Error::Invalid(format!(
                "catalog PHP version {value:?} exceeds format limits"
            )));
        }
        versions.push(parsed);
        wire.push(Version {
            major: parsed.major as u8,
            minor: parsed.minor as u8,
        });
    }
    Ok((versions, wire))
}

/// Recursively lists files below `base` in lexical order, skipping `tests`
/// and `vendor` directories. Symbolic links are not
/// followed.
fn walk(base: &Path, visit: &mut dyn FnMut(&Path, &str)) -> std::io::Result<()> {
    let mut entries: Vec<_> = std::fs::read_dir(base)?.collect::<Result<_, _>>()?;
    entries.sort_by_key(|entry| entry.file_name());
    for entry in entries {
        let name = entry.file_name().to_string_lossy().into_owned();
        let path = entry.path();
        if entry.file_type()?.is_dir() {
            if name == "tests" || name == "vendor" {
                continue;
            }
            walk(&path, visit)?;
        } else {
            visit(&path, &name);
        }
    }
    Ok(())
}

fn sort_paths(paths: &mut [PathBuf]) {
    paths.sort_by(|left, right| {
        left.as_os_str()
            .as_encoded_bytes()
            .cmp(right.as_os_str().as_encoded_bytes())
    });
}

fn source_files(root: &Path, directories: &[String]) -> Result<Vec<PathBuf>, Error> {
    let mut files = Vec::new();
    for directory in directories {
        let base = swlsp_php_project::clean(&root.join(directory));
        let metadata = std::fs::metadata(&base).map_err(io_error(format!(
            "inspect locked stub directory {directory}"
        )))?;
        if !metadata.is_dir() {
            return Err(Error::Invalid(format!(
                "locked stub path {directory} is not a directory"
            )));
        }
        walk(&base, &mut |path, name| {
            if Path::new(name).extension().and_then(|ext| ext.to_str()) == Some("php")
                && name != ".phpstorm.meta.php"
            {
                files.push(path.to_path_buf());
            }
        })
        .map_err(io_error(format!("scan locked stub directory {directory}")))?;
    }
    sort_paths(&mut files);
    Ok(files)
}

fn metadata_source_files(root: &Path, directories: &[String]) -> Result<Vec<PathBuf>, Error> {
    let mut search_roots = Vec::with_capacity(directories.len() + 1);
    let meta_root = root.join("meta");
    match std::fs::metadata(&meta_root) {
        Ok(metadata) if metadata.is_dir() => search_roots.push(meta_root),
        Ok(_) => {}
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => {}
        Err(err) => return Err(io_error("inspect PhpStorm metadata directory")(err)),
    }
    for directory in directories {
        search_roots.push(swlsp_php_project::clean(&root.join(directory)));
    }
    let mut seen = HashSet::new();
    let mut files = Vec::new();
    for search_root in &search_roots {
        walk(search_root, &mut |path, name| {
            if name == ".phpstorm.meta.php" && seen.insert(path.to_path_buf()) {
                files.push(path.to_path_buf());
            }
        })
        .map_err(io_error(format!(
            "scan PhpStorm metadata in {}",
            search_root.display()
        )))?;
    }
    sort_paths(&mut files);
    Ok(files)
}

fn range_start(range: swlsp_syntax::TextRange) -> u32 {
    u32::from(range.start())
}

fn available_class_names(document: &SourceDocument, version: ProjectVersion) -> HashSet<String> {
    document
        .document
        .symbols
        .iter()
        .filter(|symbol| {
            symbol.is_class_like()
                && available(
                    &document.source,
                    range_start(symbol.range),
                    declaration_prefix(&document.source, symbol),
                    version,
                )
        })
        .map(|symbol| lowercase(&symbol.fully_qualified))
        .collect()
}

fn supported_kind(kind: SymbolKind) -> bool {
    matches!(
        kind,
        SymbolKind::Class
            | SymbolKind::Interface
            | SymbolKind::Trait
            | SymbolKind::Enum
            | SymbolKind::Method
            | SymbolKind::Function
            | SymbolKind::Property
            | SymbolKind::ClassConstant
            | SymbolKind::GlobalConstant
            | SymbolKind::EnumCase
    )
}

fn unknown_or_error(value: &Type) -> bool {
    value.is_unknown() || value.kind() == Kind::Error
}

fn convert_symbol(
    document: &SourceDocument,
    source: &SemanticSymbol,
    owner: &str,
    version: ProjectVersion,
) -> Result<Option<Symbol>, Error> {
    let prefix = declaration_prefix(&document.source, source);
    if !available(&document.source, range_start(source.range), prefix, version) {
        return Ok(None);
    }
    let fully_qualified = canonical_name(source, owner);
    let namespace = namespace_of(&fully_qualified).to_owned();
    let mut parameters = Vec::with_capacity(source.parameters.len());
    for parameter in &source.parameters {
        let fragment = range_text(
            &document.source,
            u32::from(parameter.range.start()),
            u32::from(parameter.range.end()),
        );
        if !available("", 0, fragment, version) {
            continue;
        }
        let mut native_type = parameter.native_type.clone();
        let mut effective_type = parameter.ty.clone();
        if unknown_or_error(&native_type) {
            let aware = language_level_type(fragment, version, &namespace).map_err(|err| {
                Error::Parameter {
                    name: parameter.name.to_string(),
                    source: Box::new(err),
                }
            })?;
            if !aware.is_unknown() {
                native_type = aware.clone();
                // Upstream defines LanguageLevelTypeAware as the effective
                // signature for this PHP version. PHPDoc often describes the
                // pre-change default and must not reintroduce removed arms.
                effective_type = aware;
            }
        }
        parameters.push(Parameter {
            name: parameter.name.clone(),
            ty: effective_type,
            native_type,
            doc_type: parameter.doc_type.clone(),
            assistant_tags: parameter.assistant_tags.clone(),
            attributes: catalog_attributes(&parameter.attributes),
            default_value: parameter.default_value.as_deref().cloned(),
            flags: parameter.flags,
            optional: parameter.optional,
        });
    }
    let mut native_type = source.native_type.clone();
    let mut effective_type = source.ty.clone();
    let mut return_type = source.return_type.clone();
    if unknown_or_error(&native_type) {
        let aware = language_level_type(prefix, version, &namespace)?;
        if !aware.is_unknown() {
            native_type = aware.clone();
            match source.kind {
                SymbolKind::Method | SymbolKind::Function => return_type = aware,
                _ => effective_type = aware,
            }
        }
    }
    let templates = source
        .templates()
        .iter()
        .map(|template| TemplateParameter {
            name: template.name.clone(),
            bound: template.bound.clone(),
            default: template.default.clone(),
            covariant: template.covariant,
            contravariant: template.contravariant,
        })
        .collect();
    Ok(Some(Symbol {
        version_mask: 0,
        extension: Str::default(),
        kind: source.kind,
        name: source.name.clone(),
        fully_qualified: fully_qualified.as_str().into(),
        container: owner.into(),
        visibility: source.visibility,
        write_visibility: source.write_visibility,
        has_write_visibility: source.has_write_visibility,
        flags: versioned_flags(
            source.flags,
            &document.source,
            range_start(source.range),
            prefix,
            version,
        ) | Flags::INTERNAL
            | Flags::GENERATED_STUB,
        ty: effective_type,
        native_type,
        doc_type: source.doc_type.clone(),
        return_type,
        parameters,
        templates,
        extends: source.extends().to_vec(),
        implements: source.implements().to_vec(),
        traits: source.traits().to_vec(),
        extends_types: source.extends_types().to_vec(),
        implements_types: source.implements_types().to_vec(),
        trait_types: source.trait_types().to_vec(),
        throws: source.throws().to_vec(),
        attributes: catalog_attributes(source.attributes()),
        doc_summary: source.doc_summary().into(),
    }))
}

fn catalog_attributes(source: &[Attribute]) -> Vec<Attribute> {
    let mut result = source.to_vec();
    for attribute in &mut result {
        attribute.range = Default::default();
        if !retain_catalog_attribute_arguments(&attribute.name) {
            attribute.arguments.clear();
            continue;
        }
        for argument in &mut attribute.arguments {
            argument.range = Default::default();
        }
    }
    result
}

fn retain_catalog_attribute_arguments(name: &str) -> bool {
    let name = lowercase(name.trim_matches('\\'));
    [
        "\\arrayshape",
        "\\deprecated",
        "\\expectedvalues",
        "\\noreturn",
        "\\objectshape",
        "\\returntypecontract",
    ]
    .iter()
    .any(|suffix| name.ends_with(suffix))
}

fn versioned_flags(
    flags: Flags,
    source: &str,
    start: u32,
    fragment: &str,
    version: ProjectVersion,
) -> Flags {
    if !flags.has(Flags::DEPRECATED) {
        return flags;
    }
    let Some(arguments) = attribute_arguments(fragment, "Deprecated") else {
        return flags;
    };
    let since = version_from_match(DEPRECATED_SINCE.captures(arguments));
    if since == ProjectVersion::default() || version >= since {
        return flags;
    }
    // A PHPDoc deprecation is an independent declaration. Do not erase it
    // merely because a second, versioned attribute starts in a future release.
    if DEPRECATED_DOC.is_match(leading_doc_comment(source, start)) {
        return flags;
    }
    let mut flags = flags;
    flags.remove(Flags::DEPRECATED);
    flags
}

fn canonical_name(source: &SemanticSymbol, owner: &str) -> String {
    if owner.is_empty() {
        return source
            .fully_qualified
            .strip_prefix('\\')
            .unwrap_or(&source.fully_qualified)
            .to_owned();
    }
    match source.kind {
        SymbolKind::Property => format!(
            "{owner}::${}",
            source.name.strip_prefix('$').unwrap_or(&source.name)
        ),
        _ => format!("{owner}::{}", source.name),
    }
}

fn symbol_key(symbol: &Symbol) -> String {
    format!(
        "{}:{}",
        symbol.kind as u8,
        lowercase(&symbol.fully_qualified)
    )
}

fn symbol_score(symbol: &Symbol) -> usize {
    let mut score = symbol.parameters.len() * 4 + symbol.doc_summary.len() / 80;
    for value in [
        &symbol.ty,
        &symbol.native_type,
        &symbol.doc_type,
        &symbol.return_type,
    ] {
        if !unknown_or_error(value) {
            score += 3;
        }
    }
    score
}

fn declaration_prefix<'a>(source: &'a str, symbol: &SemanticSymbol) -> &'a str {
    let text = range_text(
        source,
        u32::from(symbol.range.start()),
        u32::from(symbol.range.end()),
    );
    let property_keyword;
    let keyword = match symbol.kind {
        SymbolKind::Class => "class",
        SymbolKind::Interface => "interface",
        SymbolKind::Trait => "trait",
        SymbolKind::Enum => "enum",
        SymbolKind::Method | SymbolKind::Function => "function",
        SymbolKind::ClassConstant | SymbolKind::GlobalConstant => "const",
        SymbolKind::EnumCase => "case",
        SymbolKind::Property => {
            property_keyword = format!(
                "${}",
                lowercase(symbol.name.strip_prefix('$').unwrap_or(&symbol.name))
            );
            property_keyword.as_str()
        }
        _ => "",
    };
    if !keyword.is_empty() {
        let lower = text.to_ascii_lowercase();
        if let Some(index) = lower.find(keyword) {
            let mut cut = index;
            while !text.is_char_boundary(cut) {
                cut -= 1;
            }
            return &text[..cut];
        }
    }
    text
}

fn available(source: &str, start: u32, fragment: &str, version: ProjectVersion) -> bool {
    let zero = ProjectVersion::default();
    let (from, to) = match availability_attribute(fragment) {
        Some(range) => range,
        None => {
            let doc = leading_doc_comment(source, start);
            let from = version_from_match(SINCE.captures(doc));
            let to = version_from_match(REMOVED.captures(doc));
            if to != zero {
                // @removed marks the first unavailable version, unlike the
                // inclusive `to` argument on PhpStormStubsElementAvailable.
                if version >= to {
                    return false;
                }
            }
            (from, zero)
        }
    };
    if from != zero && version < from {
        return false;
    }
    if to != zero && version > to {
        return false;
    }
    true
}

fn availability_attribute(fragment: &str) -> Option<(ProjectVersion, ProjectVersion)> {
    let arguments = attribute_arguments(fragment, "PhpStormStubsElementAvailable")?;
    Some((
        version_from_match(FROM_VERSION.captures(arguments)),
        version_from_match(TO_VERSION.captures(arguments)),
    ))
}

fn language_level_type(
    fragment: &str,
    version: ProjectVersion,
    namespace: &str,
) -> Result<Type, Error> {
    let Some(arguments) = attribute_arguments(fragment, "LanguageLevelTypeAware") else {
        return Ok(Type::UNKNOWN);
    };
    let mut selected = DEFAULT_TYPE
        .captures(arguments)
        .and_then(|captures| captures.get(1))
        .map_or("", |value| value.as_str());
    let mut selected_version = ProjectVersion::default();
    for captures in VERSION_PAIR.captures_iter(arguments) {
        let candidate = parse_version(&captures[1]).map_err(|err| Error::LanguageLevelType {
            text: captures[1].to_owned(),
            reason: err.to_string(),
        })?;
        if candidate <= version
            && (selected_version == ProjectVersion::default() || candidate > selected_version)
        {
            selected = captures.get(2).map_or("", |value| value.as_str());
            selected_version = candidate;
        }
    }
    let selected = selected.replace("\\\\", "\\");
    let selected = selected.trim();
    if selected.is_empty() {
        return Ok(Type::UNKNOWN);
    }
    let value = swlsp_php_types::parse(selected).map_err(|err| Error::LanguageLevelType {
        text: selected.to_owned(),
        reason: err.to_string(),
    })?;
    Ok(NameContext::new(namespace).resolve_phpdoc_type::<&str>(&value, &[]))
}

/// The argument text of the first `name(...)` call in `fragment`, honoring
/// nested parentheses and quoted strings.
fn attribute_arguments<'a>(fragment: &'a str, name: &str) -> Option<&'a str> {
    let bytes = fragment.as_bytes();
    let mut search_from = 0;
    while search_from < bytes.len() {
        let relative = fragment[search_from..].find(name)?;
        let mut index = search_from + relative + name.len();
        while index < bytes.len() && matches!(bytes[index], b' ' | b'\t' | b'\r' | b'\n') {
            index += 1;
        }
        if index >= bytes.len() || bytes[index] != b'(' {
            search_from = index;
            continue;
        }
        let start = index + 1;
        let mut depth = 1;
        let mut quote = 0u8;
        let mut escaped = false;
        index = start;
        while index < bytes.len() {
            let current = bytes[index];
            if quote != 0 {
                if escaped {
                    escaped = false;
                } else if current == b'\\' {
                    escaped = true;
                } else if current == quote {
                    quote = 0;
                }
                index += 1;
                continue;
            }
            match current {
                b'\'' | b'"' => quote = current,
                b'(' => depth += 1,
                b')' => {
                    depth -= 1;
                    if depth == 0 {
                        return Some(&fragment[start..index]);
                    }
                }
                _ => {}
            }
            index += 1;
        }
        return None;
    }
    None
}

fn leading_doc_comment(source: &str, start: u32) -> &str {
    let start = start as usize;
    if source.is_empty() || start > source.len() || !source.is_char_boundary(start) {
        return "";
    }
    let prefix = source[..start].trim();
    if !prefix.ends_with("*/") {
        return "";
    }
    match prefix.rfind("/**") {
        Some(begin) => &prefix[begin..],
        None => "",
    }
}

fn range_text(source: &str, start: u32, end: u32) -> &str {
    let (start, mut end) = (start as usize, end as usize);
    if start > end || start > source.len() {
        return "";
    }
    end = end.min(source.len());
    source.get(start..end).unwrap_or("")
}

fn namespace_of(fully_qualified: &str) -> &str {
    let owner = match fully_qualified.find("::") {
        Some(separator) => &fully_qualified[..separator],
        None => fully_qualified,
    };
    match owner.rfind('\\') {
        Some(separator) => &owner[..separator],
        None => "",
    }
}

fn version_from_match(captures: Option<regex::Captures<'_>>) -> ProjectVersion {
    captures
        .and_then(|captures| captures.get(1))
        .and_then(|value| parse_version(value.as_str()).ok())
        .unwrap_or_default()
}

fn parse_version(value: &str) -> Result<ProjectVersion, VersionError> {
    let parts: Vec<&str> = value.split('.').collect();
    if parts.len() < 2 || parts.len() > 3 {
        return Err(VersionError::Shape);
    }
    let mut numbers = [0i64; 3];
    for (index, part) in parts.iter().enumerate() {
        // A sign is accepted (`+1`); negative values are not.
        let parsed: i64 = part
            .parse()
            .ok()
            .filter(|parsed: &i64| *parsed >= 0)
            .ok_or_else(|| VersionError::Component((*part).to_owned()))?;
        numbers[index] = parsed;
    }
    Ok(ProjectVersion::new(numbers[0], numbers[1], numbers[2]))
}
