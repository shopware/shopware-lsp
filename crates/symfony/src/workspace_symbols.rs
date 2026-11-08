//! Workspace symbols for services, parameters, and routes.

use swlsp_indexer::{
    ParsedFile, WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK, WorkspaceSymbol, WorkspaceSymbolKind,
    WorkspaceSymbolRange,
};

use crate::model::{Parameter, Route, Service};

pub(crate) fn service_workspace_symbols(
    file: &ParsedFile,
    services: &[Service],
    parameters: &[Parameter],
) -> Vec<WorkspaceSymbol> {
    let mut result = Vec::with_capacity(services.len() + parameters.len());
    for service in services {
        if service.id.is_empty() || service.path.is_empty() {
            continue;
        }
        let mut container = "Symfony service".to_owned();
        if !service.class.is_empty() {
            container.push_str(" · ");
            container.push_str(&service.class);
        }
        let range = if service.id_range.is_empty() {
            WorkspaceSymbolRange::at_line(service.line as i32)
        } else {
            WorkspaceSymbolRange::from_text(file, service.id_range)
        };
        result.push(WorkspaceSymbol {
            name: service.id.clone(),
            container_name: container,
            aliases: vec![service.class.clone()],
            path: service.path.clone(),
            domain: "symfony.service".to_owned(),
            kind: WorkspaceSymbolKind::OBJECT,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range,
        });
    }
    for parameter in parameters {
        if parameter.name.is_empty() || parameter.path.is_empty() {
            continue;
        }
        result.push(WorkspaceSymbol {
            name: parameter.name.clone(),
            container_name: "Symfony parameter".to_owned(),
            aliases: Vec::new(),
            path: parameter.path.clone(),
            domain: "symfony.parameter".to_owned(),
            kind: WorkspaceSymbolKind::CONSTANT,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: WorkspaceSymbolRange::at_line(parameter.line as i32),
        });
    }
    result
}

pub(crate) fn add_service_workspace_symbols(
    file: &ParsedFile,
    services: &[Service],
    parameters: &[Parameter],
) {
    file.add_workspace_symbols(service_workspace_symbols(file, services, parameters));
}

#[allow(dead_code)]
pub(crate) fn route_workspace_symbols(routes: &[Route]) -> Vec<WorkspaceSymbol> {
    let mut result = Vec::with_capacity(routes.len());
    for route in routes {
        if route.name.is_empty() || route.file_path.is_empty() {
            continue;
        }
        let mut detail = if route.methods.is_empty() {
            "ANY".to_owned()
        } else {
            route.methods.join("|")
        };
        if !route.path.is_empty() {
            detail.push(' ');
            detail.push_str(&route.path);
        }
        if !route.controller.is_empty() {
            detail.push_str(" · ");
            detail.push_str(&route.controller);
        }
        result.push(WorkspaceSymbol {
            name: route.name.clone(),
            container_name: format!("Symfony route · {detail}"),
            aliases: vec![route.path.clone(), route.controller.clone()],
            path: route.file_path.clone(),
            domain: "symfony.route".to_owned(),
            kind: WorkspaceSymbolKind::METHOD,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: WorkspaceSymbolRange::at_line(route.line as i32),
        });
    }
    result
}

#[allow(dead_code)]
pub(crate) fn add_route_workspace_symbols(file: &ParsedFile, routes: &[Route]) {
    file.add_workspace_symbols(route_workspace_symbols(routes));
}
