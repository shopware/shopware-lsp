//! Tests of the services analyzer group.

mod container_constant;
mod controller;
mod duplicate;
mod event;
mod invokable_command;
mod route;
mod route_redefined;
mod service;
mod service_argument;
mod tagged_service;

use std::path::Path;
use std::sync::Arc;

use serde_json::Value;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Problem;
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;

pub(super) fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

pub(super) fn php_index(dir: &Path, files: &[(&str, &str)]) -> PhpIndex {
    let php = PhpIndex::open(dir, None).expect("php index");
    for (path, source) in files {
        php.index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .expect("index php");
    }
    php
}

pub(super) fn service_index(
    dir: &Path,
    php: Option<&PhpIndex>,
    files: &[(&str, &str)],
) -> Arc<ServiceIndex> {
    // A compiled container: without one, missing services and parameters
    // are not reported.
    let compiled = dir.join("var/cache/dev");
    std::fs::create_dir_all(&compiled).expect("container directory");
    std::fs::write(
        compiled.join("App_KernelDevDebugContainer.xml"),
        "<container><services/></container>",
    )
    .expect("container");
    let services = ServiceIndex::new(&p(dir), dir.join("symfony-cache"), None).expect("services");
    services
        .reload_compiled_container()
        .expect("compiled container");
    if let Some(php) = php {
        services.set_php_index(Some(php.clone()));
    }
    for (path, source) in files {
        services
            .index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .expect("index services");
    }
    Arc::new(services)
}

pub(super) fn payload(problem: &Problem) -> &serde_json::Map<String, Value> {
    problem
        .payload
        .as_ref()
        .and_then(Value::as_object)
        .expect("object payload")
}

pub(super) fn suggestions(problem: &Problem) -> Vec<String> {
    strings(
        problem
            .payload
            .as_ref()
            .and_then(|payload| payload.get("suggestions")),
    )
}

pub(super) fn strings(value: Option<&Value>) -> Vec<String> {
    value
        .and_then(Value::as_array)
        .map(|values| {
            values
                .iter()
                .filter_map(|value| value.as_str().map(str::to_owned))
                .collect()
        })
        .unwrap_or_default()
}
