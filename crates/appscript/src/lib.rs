//! Shopware app-script hooks and facade availability (port of Go
//! `internal/appscript`).

use std::collections::{BTreeMap, HashMap, HashSet};
use std::path::Path;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Result, Store, TransactionalClearer,
    TransactionalRemover,
};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{PHP_MEMBER_ACCESS, PHP_SCOPED_ACCESS, PHP_STRING};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

/// A script hook class (`*Hook`).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Hook {
    pub name: String,
    pub class: String,
    pub parent: String,
    pub file: String,
    pub line: u32,
    pub service_factories: Vec<String>,
}

/// A script facade factory (`*HookFactory` / `*FacadeFactory`).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Facade {
    pub name: String,
    pub class: String,
    pub file: String,
    pub line: u32,
    /// Available to every hook (the ACL facade).
    pub default: bool,
}

/// Indexes script hooks and facades from PHP classes.
pub struct Index {
    hooks: DataIndexer<Hook>,
    facades: DataIndexer<Facade>,
}

/// The facades available to one hook, keyed by facade name.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct HookServices {
    pub services: BTreeMap<String, Facade>,
    /// `false` for unknown hooks so diagnostics can avoid false positives.
    pub found: bool,
}

impl Index {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let dir = config_dir.as_ref();
        let hooks = DataIndexer::repository(
            dir.join("app_script_hooks.db"),
            "shopware.app_script.hooks",
            store,
        )?;
        let facades = match DataIndexer::repository(
            dir.join("app_script_facades.db"),
            "shopware.app_script.facades",
            store,
        ) {
            Ok(facades) => facades,
            Err(error) => {
                let _ = hooks.close();
                return Err(error);
            }
        };
        Ok(Self { hooks, facades })
    }

    pub fn hooks(&self) -> Result<Arc<[Hook]>> {
        self.hooks.get_all_values_view()
    }

    pub fn facades(&self) -> Result<Arc<[Facade]>> {
        self.facades.get_all_values_view()
    }

    /// Resolves inherited facade factories and executor defaults.
    pub fn services_for_hook(&self, name: &str) -> Result<HookServices> {
        let hooks = self.hooks.get_all_values_view()?;
        let facades = self.facades.get_all_values_view()?;
        let mut result = HookServices::default();
        let mut facade_by_class: HashMap<&str, &Facade> = HashMap::with_capacity(facades.len());
        for facade in facades.iter() {
            facade_by_class.insert(short_class(&facade.class), facade);
            if facade.default {
                result.services.insert(facade.name.clone(), facade.clone());
            }
        }
        let mut hook_by_class: HashMap<&str, &Hook> = HashMap::with_capacity(hooks.len());
        let mut matched = Vec::new();
        for hook in hooks.iter() {
            hook_by_class.insert(short_class(&hook.class), hook);
            if hook.name == name {
                matched.push(hook);
            }
        }
        if matched.is_empty() {
            return Ok(result);
        }
        result.found = true;
        let mut visited = HashSet::new();
        for hook in matched {
            let mut current = Some(hook);
            // Walks the parent chain iteratively; `visited` breaks cycles.
            while let Some(hook) = current {
                if !visited.insert(short_class(&hook.class)) {
                    break;
                }
                for factory in &hook.service_factories {
                    if let Some(facade) = facade_by_class.get(short_class(factory)) {
                        result
                            .services
                            .insert(facade.name.clone(), (*facade).clone());
                    }
                }
                current = hook_by_class.get(short_class(&hook.parent)).copied();
            }
        }
        Ok(result)
    }

    fn save(
        &self,
        file: &ParsedFile,
        hooks: &[(String, Hook)],
        facades: &[(String, Facade)],
    ) -> Result<()> {
        let path = file.path();
        let hooks = hooks.iter().map(|(key, hook)| (key.as_str(), hook));
        self.hooks
            .batch_save_items_in(file.mutation(), [(path, hooks)])?;
        let facades = facades.iter().map(|(key, facade)| (key.as_str(), facade));
        self.facades
            .batch_save_items_in(file.mutation(), [(path, facades)])
    }
}

/// Inserts or replaces `key`, keeping first-insertion order (Go map semantics
/// for the persisted key set).
fn upsert<T>(items: &mut Vec<(String, T)>, key: String, value: T) {
    match items.iter_mut().find(|(existing, _)| *existing == key) {
        Some(item) => item.1 = value,
        None => items.push((key, value)),
    }
}

impl Indexer for Index {
    fn id(&self) -> &str {
        "shopware.app_script"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        if file.extension() != ".php" {
            return Ok(());
        }
        let source = file.source();
        if !source.contains("Hook") && !source.contains("Facade") {
            return self.save(file, &[], &[]);
        }
        let Some(tree) = file.syntax_tree() else {
            return Ok(());
        };
        let mut hooks = Vec::new();
        let mut facades = Vec::new();
        for class in query::classes(&tree.root()) {
            let class_name = query::class_name(&class);
            if class_name.is_empty() {
                continue;
            }
            let (line, _) = file
                .line_index()
                .position(class.range_trimmed_trivia().start().into());
            let line = line + 1;
            if (class_name.ends_with("HookFactory") || class_name.ends_with("FacadeFactory"))
                && let Some(name) = method_literal(&class, "getName")
            {
                let facade = Facade {
                    name,
                    class: class_name.clone(),
                    file: file.path().to_owned(),
                    line,
                    default: class_name == "AclFacadeHookFactory",
                };
                upsert(&mut facades, class_name.clone(), facade);
            }
            if !class_name.ends_with("Hook") {
                continue;
            }
            let mut hook = Hook {
                class: class_name.clone(),
                file: file.path().to_owned(),
                line,
                ..Hook::default()
            };
            if let Some(parent) = query::class_extends(&class).first() {
                hook.parent = short_class(parent).to_owned();
            }
            hook.name = method_literal(&class, "getName")
                .or_else(|| hook_name_constant(tree.node_text(&class)).map(str::to_owned))
                .unwrap_or_default();
            for method in query::methods(&class) {
                if query::method_name(&method) != "getServiceIds" {
                    continue;
                }
                let mut seen = HashSet::new();
                for access in method
                    .descendants()
                    .filter(|node| matches!(node.kind(), PHP_SCOPED_ACCESS | PHP_MEMBER_ACCESS))
                {
                    let factory = short_class(&query::class_constant_name(&access)).to_owned();
                    if !factory.is_empty() && seen.insert(factory.clone()) {
                        hook.service_factories.push(factory);
                    }
                }
            }
            upsert(&mut hooks, class_name, hook);
        }
        self.save(file, &hooks, &facades)
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        join(
            self.hooks.batch_delete_by_file_paths(paths),
            self.facades.batch_delete_by_file_paths(paths),
        )
    }

    fn close(&self) -> Result<()> {
        join(self.hooks.close(), self.facades.close())
    }

    fn clear(&self) -> Result<()> {
        join(self.hooks.clear(), self.facades.clear())
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
        join(
            self.hooks
                .batch_delete_by_file_paths_in(Some(mutation), paths),
            self.facades
                .batch_delete_by_file_paths_in(Some(mutation), paths),
        )
    }
}

impl TransactionalClearer for Index {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        join(
            self.hooks.clear_in(Some(mutation)),
            self.facades.clear_in(Some(mutation)),
        )
    }
}

fn join(first: Result<()>, second: Result<()>) -> Result<()> {
    match Error::join([first.err(), second.err()].into_iter().flatten()) {
        Some(error) => Err(error),
        None => Ok(()),
    }
}

/// The first string literal of the named method, when it is non-empty.
fn method_literal(class: &SyntaxNode, method_name: &str) -> Option<String> {
    for method in query::methods(class) {
        if query::method_name(&method) != method_name {
            continue;
        }
        if let Some(literal) = method.descendants().find(|node| node.kind() == PHP_STRING) {
            let value = query::string_value(&literal);
            return (!value.is_empty()).then_some(value);
        }
    }
    None
}

/// The class name after the last namespace separator.
fn short_class(name: &str) -> &str {
    let name = name.strip_prefix('\\').unwrap_or(name).trim();
    match name.rfind('\\') {
        Some(separator) => &name[separator + 1..],
        None => name,
    }
}

/// Go `\bHOOK_NAME\s*=\s*['"]([^'"]+)['"]` on the class text.
fn hook_name_constant(text: &str) -> Option<&str> {
    const NAME: &str = "HOOK_NAME";
    let bytes = text.as_bytes();
    let is_word = |byte: u8| byte.is_ascii_alphanumeric() || byte == b'_';
    let is_space = |byte: u8| matches!(byte, b' ' | b'\t' | b'\n' | b'\r' | b'\x0c');
    let skip_space = |mut cursor: usize| {
        while cursor < bytes.len() && is_space(bytes[cursor]) {
            cursor += 1;
        }
        cursor
    };
    let mut search = 0;
    while let Some(relative) = text[search..].find(NAME) {
        let start = search + relative;
        search = start + 1;
        if start > 0 && is_word(bytes[start - 1]) {
            continue;
        }
        let mut cursor = skip_space(start + NAME.len());
        if bytes.get(cursor) != Some(&b'=') {
            continue;
        }
        cursor = skip_space(cursor + 1);
        if !matches!(bytes.get(cursor), Some(b'\'' | b'"')) {
            continue;
        }
        let value_start = cursor + 1;
        let value_end = value_start
            + bytes[value_start..]
                .iter()
                .position(|byte| matches!(byte, b'\'' | b'"'))
                .unwrap_or(bytes.len() - value_start);
        if value_end > value_start && value_end < bytes.len() {
            return Some(&text[value_start..value_end]);
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn resolves_inherited_hook_services_and_defaults() {
        let dir = tempfile::tempdir().unwrap();
        let index = Index::new(dir.path(), None).unwrap();
        let files = [
            (
                "/src/RepositoryFacadeHookFactory.php",
                "<?php\nclass RepositoryFacadeHookFactory extends HookServiceFactory {\n    public function getName(): string { return 'repository'; }\n}",
            ),
            (
                "/src/AclFacadeHookFactory.php",
                "<?php\nclass AclFacadeHookFactory extends HookServiceFactory {\n    public function getName(): string { return 'acl'; }\n}",
            ),
            (
                "/src/PageLoadedHook.php",
                "<?php\nabstract class PageLoadedHook extends Hook {\n    public static function getServiceIds(): array {\n        return [RepositoryFacadeHookFactory::class];\n    }\n}",
            ),
            (
                "/src/ProductPageLoadedHook.php",
                "<?php\nclass ProductPageLoadedHook extends PageLoadedHook {\n    public const HOOK_NAME = 'product-page-loaded';\n    public function getName(): string { return self::HOOK_NAME; }\n}",
            ),
        ];
        for (path, source) in files {
            index
                .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
                .unwrap();
        }

        let services = index.services_for_hook("product-page-loaded").unwrap();
        assert!(services.found);
        assert!(services.services.contains_key("repository"));
        assert!(services.services.contains_key("acl"));

        assert!(!index.services_for_hook("unknown").unwrap().found);
        index.close().unwrap();
    }

    #[test]
    fn hook_name_constant_matches_go_pattern() {
        assert_eq!(hook_name_constant("const HOOK_NAME = 'a-b';"), Some("a-b"));
        assert_eq!(hook_name_constant("const HOOK_NAME=\"x\""), Some("x"));
        assert_eq!(
            hook_name_constant("const MY_HOOK_NAME = 'a'; const HOOK_NAME = 'b';"),
            Some("b")
        );
        assert_eq!(hook_name_constant("const HOOK_NAME = '';"), None);
        assert_eq!(hook_name_constant("const HOOK_NAME = 'open"), None);
        assert_eq!(hook_name_constant("HOOK_NAME = self::X"), None);
    }

    #[test]
    fn short_class_strips_namespace() {
        assert_eq!(short_class("\\Shopware\\Core\\Hook"), "Hook");
        assert_eq!(short_class(" Hook "), "Hook");
    }

    #[test]
    fn files_without_candidates_clear_previous_rows() {
        let dir = tempfile::tempdir().unwrap();
        let index = Index::new(dir.path(), None).unwrap();
        let path = "/src/DemoHook.php";
        index
            .index(&ParsedFile::new(
                path,
                b"<?php class DemoHook { const HOOK_NAME = 'demo'; }".to_vec(),
            ))
            .unwrap();
        assert_eq!(index.hooks().unwrap().len(), 1);
        assert_eq!(index.hooks().unwrap()[0].name, "demo");
        index
            .index(&ParsedFile::new(path, b"<?php class Other {}".to_vec()))
            .unwrap();
        assert!(index.hooks().unwrap().is_empty());
        index.close().unwrap();
    }
}
