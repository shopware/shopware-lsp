//! Tests ported from `internal/php/inference/*_test.go`.

use std::sync::Arc;

use swlsp_parser_php::query;
use swlsp_php_binder::{Binder, link};
use swlsp_php_project::Version;
use swlsp_php_semantic::{Document, Snapshot, Symbol};
use swlsp_syntax::{Kind, SyntaxNode, Tree};

use crate::{Analyzer, Extension};

mod contracts;
mod environment;
mod flow;
mod inference_a;
mod inference_b;
mod inference_c;
mod inference_d;
mod member_links;

/// A parsed source file.
pub(crate) struct Parsed {
    pub(crate) tree: Tree,
    pub(crate) root: SyntaxNode,
}

pub(crate) fn parse(source: &str) -> Parsed {
    let tree = swlsp_parser_php::parse(source).tree;
    let root = tree.root();
    Parsed { tree, root }
}

/// `binder.New().Bind(path, 1, root)`.
pub(crate) fn bind(path: &str, parsed: &Parsed) -> Document {
    Binder::new().bind(path, 1, &parsed.tree)
}

/// `semantic.NewSnapshot(1, documents)`.
pub(crate) fn snapshot(documents: &[Document]) -> Arc<Snapshot> {
    Snapshot::new(1, documents)
}

/// `stubs.Document(project.Version{Major: 8, Minor: 3})`.
pub(crate) fn stubs83() -> Document {
    swlsp_php_stubs::document(Version::new(8, 3, 0))
}

pub(crate) fn analyzer(snapshot: &Arc<Snapshot>, extensions: Vec<Arc<dyn Extension>>) -> Analyzer {
    Analyzer::new(Some(snapshot.clone()), extensions)
}

/// `New(snapshot, extensions...).Analyze(binder.Link(bound, snapshot), root)`.
pub(crate) fn analyze_linked(
    snapshot: &Arc<Snapshot>,
    extensions: Vec<Arc<dyn Extension>>,
    bound: &Document,
    parsed: &Parsed,
) -> Document {
    analyzer(snapshot, extensions).analyze(&link(bound, snapshot), &parsed.root)
}

/// The most common fixture: bind one file, snapshot it alone (optionally
/// with PHP 8.3 stubs), link and analyze it.
pub(crate) fn analyze_source(
    path: &str,
    source: &str,
    with_stubs: bool,
    extensions: Vec<Arc<dyn Extension>>,
) -> (Parsed, Document, Arc<Snapshot>) {
    let parsed = parse(source);
    let bound = bind(path, &parsed);
    let mut documents = vec![bound.clone()];
    if with_stubs {
        documents.push(stubs83());
    }
    let snapshot = snapshot(&documents);
    let analyzed = analyze_linked(&snapshot, extensions, &bound, &parsed);
    (parsed, analyzed, snapshot)
}

pub(crate) fn builtins() -> Vec<Arc<dyn Extension>> {
    vec![Arc::new(crate::Builtins)]
}

pub(crate) fn text_of(node: &SyntaxNode) -> String {
    node.text().to_string()
}

/// Go `assertTextType`: the first node with the trimmed text whose fact is
/// known must have the expected type.
#[track_caller]
pub(crate) fn assert_text_type(document: &Document, root: &SyntaxNode, text: &str, expected: &str) {
    for node in root.descendants() {
        if text_of(&node).trim() != text {
            continue;
        }
        let fact = document.type_of(&node);
        if !fact.ty.is_unknown() {
            assert_eq!(fact.ty.to_string(), expected, "type of {text:?}");
            return;
        }
    }
    panic!("no inferred node {text:?}");
}

/// Go `assertNodeType`: the last node of `kind` has the expected type.
#[track_caller]
pub(crate) fn assert_node_type(document: &Document, root: &SyntaxNode, kind: Kind, expected: &str) {
    let nodes = query::nodes(root, &[kind]);
    let node = nodes.last().expect("no node of kind");
    assert_eq!(document.type_of(node).ty.to_string(), expected);
}

#[allow(dead_code)]
/// The type of the last node of `kind` (unknown renders as its Go text).
pub(crate) fn last_node_type(document: &Document, root: &SyntaxNode, kind: Kind) -> String {
    let nodes = query::nodes(root, &[kind]);
    document
        .type_of(nodes.last().expect("no node of kind"))
        .ty
        .to_string()
}

#[track_caller]
pub(crate) fn symbol_named(document: &Document, name: &str) -> Symbol {
    document
        .symbols
        .iter()
        .find(|symbol| &*symbol.name == name)
        .cloned()
        .unwrap_or_else(|| panic!("missing symbol {name:?}"))
}

/// Messages of issues with `code`.
pub(crate) fn messages(document: &Document, code: &str) -> Vec<String> {
    document
        .issues
        .iter()
        .filter(|issue| &*issue.code == code)
        .map(|issue| issue.message.to_string())
        .collect()
}

pub(crate) fn all_messages(document: &Document) -> Vec<String> {
    document
        .issues
        .iter()
        .map(|issue| format!("{}: {}", issue.code, issue.message))
        .collect()
}

pub(crate) fn contains_message(messages: &[String], needle: &str) -> bool {
    messages.iter().any(|message| message.contains(needle))
}

#[allow(dead_code)]
/// Type of the last fact-bearing node whose trimmed text equals `text`.
pub(crate) fn type_of_text(document: &Document, root: &SyntaxNode, text: &str) -> Option<String> {
    root.descendants()
        .filter(|node| text_of(node).trim() == text)
        .map(|node| document.type_of(&node).ty)
        .find(|ty| !ty.is_unknown())
        .map(|ty| ty.to_string())
}
