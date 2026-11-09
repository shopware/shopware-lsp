mod api;
mod embedded;
mod indexer;
mod persistence;
mod return_inference;
mod settle;
mod twig;

use std::path::Path;

use swlsp_indexer::ParsedFile;

use crate::PhpIndex;

pub(crate) fn open(directory: &Path) -> PhpIndex {
    PhpIndex::open(directory, None).expect("open PHP index")
}

pub(crate) fn file(path: &str, source: &str) -> ParsedFile {
    ParsedFile::new(path, source.as_bytes().to_vec())
}

pub(crate) fn index(index: &PhpIndex, path: &str, source: &str) {
    swlsp_indexer::Indexer::index(index, &file(path, source)).expect("index file");
}
