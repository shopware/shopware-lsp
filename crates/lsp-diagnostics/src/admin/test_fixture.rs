//! Shared fixture for the Administration analyzer tests.
#![allow(dead_code)]

use std::collections::HashSet;
use std::path::{Path, PathBuf};
use std::sync::Arc;

use swlsp_admin::{AdminComponentIndexer, VueComponent, VueComponentProp};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, Problem, TextDocument};
use swlsp_syntax::TextRange;

use super::AdminAnalyzer;
use crate::ProblemAnalyzer;

pub struct Fixture {
    _dir: tempfile::TempDir,
    pub root: PathBuf,
    pub indexer: Arc<AdminComponentIndexer>,
}

impl Fixture {
    /// An indexer whose cache lives below `<root>/cache`.
    pub fn new() -> Self {
        let dir = tempfile::tempdir().expect("temporary directory");
        let root = dir.path().to_path_buf();
        Self::with_cache(dir, root.clone(), &root.join("cache"))
    }

    /// An indexer whose cache is the temporary root itself.
    pub fn in_root() -> Self {
        let dir = tempfile::tempdir().expect("temporary directory");
        let root = dir.path().to_path_buf();
        Self::with_cache(dir, root.clone(), &root)
    }

    fn with_cache(dir: tempfile::TempDir, root: PathBuf, cache: &Path) -> Self {
        std::fs::create_dir_all(cache).expect("cache");
        let indexer =
            AdminComponentIndexer::new(cache.to_str().expect("utf-8"), None).expect("indexer");
        Self {
            _dir: dir,
            root,
            indexer: Arc::new(indexer),
        }
    }

    pub fn path(&self, relative: &str) -> String {
        self.root.join(relative).to_str().expect("utf-8").to_owned()
    }

    pub fn index(&self, path: &str, source: &str) {
        self.indexer
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .expect("index");
    }

    pub fn write(&self, path: &str, source: &str) {
        if let Some(parent) = Path::new(path).parent() {
            std::fs::create_dir_all(parent).expect("directory");
        }
        std::fs::write(path, source).expect("write");
    }

    pub fn save(&self, component: VueComponent) {
        self.indexer
            .save_component(&component)
            .expect("save component");
    }

    pub fn analyzer(&self) -> AdminAnalyzer {
        AdminAnalyzer::new(Some(self.indexer.clone()))
    }

    pub fn analyze_path(&self, path: &str, source: &str) -> Vec<Problem> {
        analyze(
            &self.analyzer(),
            &document(&swlsp_uriutil::file_uri(path), source),
        )
    }

    pub fn update_live(&self, path: &str, source: &str) {
        let document = document(&swlsp_uriutil::file_uri(path), source);
        self.indexer.update_live_document(
            path,
            document.root().as_ref(),
            source,
            Some(document.line_index()),
        );
    }
}

pub fn document(uri: &str, source: &str) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, 1))
}

pub fn analyze(analyzer: &AdminAnalyzer, document: &Arc<TextDocument>) -> Vec<Problem> {
    analyzer
        .analyze(&Context::background(), document)
        .expect("analyze")
}

pub fn codes(problems: &[Problem]) -> HashSet<String> {
    problems
        .iter()
        .map(|problem| problem.id.0.clone())
        .collect()
}

pub fn with_code<'a>(problems: &'a [Problem], code: &str) -> Vec<&'a Problem> {
    problems
        .iter()
        .filter(|problem| problem.id.0 == code)
        .collect()
}

pub fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

pub fn prop(name: &str, type_: &str, required: bool) -> VueComponentProp {
    VueComponentProp {
        name: name.to_owned(),
        type_: type_.to_owned(),
        required,
        ..VueComponentProp::default()
    }
}

pub fn suggestions(problem: &Problem) -> Vec<String> {
    match problem
        .payload
        .as_ref()
        .and_then(|payload| payload.get("suggestions"))
    {
        Some(serde_json::Value::Array(values)) => values
            .iter()
            .filter_map(|value| value.as_str().map(str::to_owned))
            .collect(),
        _ => Vec::new(),
    }
}

pub fn payload_str(problem: &Problem, key: &str) -> String {
    problem
        .payload
        .as_ref()
        .and_then(|payload| payload.get(key))
        .and_then(|value| value.as_str())
        .unwrap_or_default()
        .to_owned()
}

pub const ADMIN_SRC: &str = "src/Administration/Resources/app/administration/src";

/// Builds a `VueComponent` (its private fields rule out struct literals).
macro_rules! vue_component {
    ($($field:ident: $value:expr),* $(,)?) => {{
        let mut component = swlsp_admin::VueComponent::default();
        $(component.$field = $value;)*
        component
    }};
}
pub(crate) use vue_component;
