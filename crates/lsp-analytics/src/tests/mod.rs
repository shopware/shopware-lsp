//! Ports of the Go `internal/lsp/analytics` tests.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Context;
use swlsp_php::PhpIndex;
use swlsp_symfony::{RouteIndexer, ServiceIndex};
use swlsp_twig::TwigIndexer;
use swlsp_twigcomponent::Index as ComponentIndex;
use swlsp_uriutil::file_uri;

mod catalogs;
mod profiler;

pub(crate) fn ctx() -> Context {
    Context::background()
}

pub(crate) fn path_string(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

/// Writes `source` below `root` and returns the absolute path.
pub(crate) fn write_fixture(root: &Path, relative: &str, source: &str) -> String {
    let path: PathBuf = root.join(relative);
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(&path, source).unwrap();
    path_string(&path)
}

pub(crate) fn parsed(path: &str) -> ParsedFile {
    ParsedFile::new(path, std::fs::read(path).unwrap())
}

pub(crate) fn uri(path: &str) -> String {
    file_uri(path)
}

pub(crate) fn open_php(cache: &Path) -> PhpIndex {
    PhpIndex::open(cache, None).unwrap()
}

pub(crate) fn open_twig(cache: &Path) -> Arc<TwigIndexer> {
    Arc::new(TwigIndexer::new(cache, None).unwrap())
}

pub(crate) fn open_routes(cache: &Path) -> Arc<RouteIndexer> {
    Arc::new(RouteIndexer::new(cache, None).unwrap())
}

pub(crate) fn open_services(root: &Path, cache: &Path, php: &PhpIndex) -> Arc<ServiceIndex> {
    let services = Arc::new(ServiceIndex::new(&path_string(root), cache, None).unwrap());
    services.set_php_index(Some(php.clone()));
    services
}

pub(crate) fn open_components(cache: &Path) -> Arc<ComponentIndex> {
    Arc::new(ComponentIndex::new(cache, None).unwrap())
}

pub(crate) fn close(indexer: &dyn Indexer) {
    indexer.close().unwrap();
}

/// Parses an expected Go wire response for comparison with a command result.
pub(crate) fn json_value(text: &str) -> serde_json::Value {
    serde_json::from_str(text).unwrap()
}
