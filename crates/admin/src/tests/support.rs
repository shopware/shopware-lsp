//! Helpers shared by several Go test files.
#![allow(dead_code)]

use crate::*;

/// Go `parseJS`.
pub(crate) fn parse_js(code: &str) -> SyntaxNode {
    swlsp_parser_js::parse(code).root()
}

/// Go `parseTwig`-style helper.
pub(crate) fn parse_twig(code: &str) -> SyntaxNode {
    swlsp_parser_twig::parse(code).root()
}

/// Go `propNames` / `adminPropNamesForTest`.
pub(crate) fn prop_names(props: &[VueComponentProp]) -> Vec<String> {
    props.iter().map(|prop| prop.name.clone()).collect()
}

pub(crate) fn admin_prop_names_for_test(props: &[VueComponentProp]) -> Vec<String> {
    prop_names(props)
}

/// Go `slotNames`.
pub(crate) fn slot_names(slots: &[VueComponentSlot]) -> Vec<String> {
    slots.iter().map(|slot| slot.name.clone()).collect()
}

/// Go `slotMemberNames`.
pub(crate) fn slot_member_names(members: &[VueComponentSlotMember]) -> Vec<String> {
    members.iter().map(|member| member.name.clone()).collect()
}

/// Go `vueMemberNames`.
pub(crate) fn vue_member_names(members: &[TwigVueMember]) -> Vec<String> {
    members.iter().map(|member| member.name.clone()).collect()
}

/// A temporary directory for one test (Go `t.TempDir()`).
pub(crate) fn temp_dir() -> tempfile::TempDir {
    tempfile::tempdir().expect("temporary directory")
}

/// Go `NewAdminComponentIndexer(filepath.Join(root, "cache"))`.
pub(crate) fn new_indexer(cache_dir: &std::path::Path) -> AdminComponentIndexer {
    std::fs::create_dir_all(cache_dir).expect("cache directory");
    AdminComponentIndexer::new(cache_dir.to_str().expect("utf-8 path"), None).expect("indexer")
}

/// Writes `content` to `path`, creating parent directories (Go
/// `os.MkdirAll` + `os.WriteFile`).
pub(crate) fn write_file(path: &std::path::Path, content: &str) {
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent).expect("parent directory");
    }
    std::fs::write(path, content).expect("write file");
}

/// Go `idx.Index(indexer.NewParsedFile(path, content))` asserting success.
pub(crate) fn index_source(idx: &AdminComponentIndexer, path: &str, content: &str) {
    use swlsp_indexer::Indexer;
    idx.index(&ParsedFile::new(path, content.as_bytes().to_vec()))
        .expect("index file");
}

/// Joins a path below a temporary root into a `String`.
pub(crate) fn path_string(root: &std::path::Path, relative: &str) -> String {
    root.join(relative).to_str().expect("utf-8 path").to_owned()
}
