//! Ports the reference plugin's decorator, parent, and prototype line markers
//! across PHP, XML, and YAML service config.

use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol::{self as protocol, Range};
use swlsp_php::PhpIndex;
use swlsp_symfony::{Service, ServiceIndex};
use swlsp_syntax::{LineIndex, TextRange};
use swlsp_textutil::eq_ignore_case;

use crate::{
    cancelled, line_range, lower_ext, related_lens, related_protocol_range, related_source_line,
    related_target, same_path, sort_related_code_lenses, unique_related_targets,
};

pub struct ServiceRelatedCodeLensProvider {
    services: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
}

impl ServiceRelatedCodeLensProvider {
    pub fn new(services: Option<Arc<ServiceIndex>>, php: Option<PhpIndex>) -> Self {
        Self { services, php }
    }
}

impl CodeLensProvider for ServiceRelatedCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let (Some(services), Some(_)) = (&self.services, &self.php) else {
            return Ok(Vec::new());
        };
        let document = &request.document;
        let Some(tree) = document.tree() else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        if !matches!(
            lower_ext(&path).as_str(),
            ".php" | ".xml" | ".yaml" | ".yml"
        ) {
            return Ok(Vec::new());
        }
        let lines = document.line_index();
        let current = swlsp_symfony::service_configuration_in_document(&path, Some(tree), lines);
        if current.services.is_empty() && current.prototypes.is_empty() {
            return Ok(Vec::new());
        }
        let indexed = services.service_definitions()?;
        let mut definitions: Vec<Service> =
            Vec::with_capacity(indexed.len() + current.services.len());
        definitions.extend(
            indexed
                .into_iter()
                .filter(|service| !same_path(&service.path, &path)),
        );
        definitions.extend(current.services.iter().cloned());

        let mut result = Vec::new();
        for service in &current.services {
            cancelled(ctx)?;
            let range = service_code_lens_range(service.id_range, service.line, lines);
            result.extend(service_forward_lenses(range, service, &definitions));
            result.extend(service_reverse_lenses(range, service, &definitions));
        }
        for prototype in &current.prototypes {
            cancelled(ctx)?;
            let targets = unique_related_targets(
                services
                    .prototype_classes(prototype)
                    .iter()
                    .map(|class| {
                        let range = if class.selection_range.is_empty() {
                            class.range
                        } else {
                            class.selection_range
                        };
                        related_target(
                            &class.path,
                            related_source_line(&class.path, range.start().into()),
                        )
                    })
                    .collect(),
            );
            if targets.is_empty() {
                continue;
            }
            let title = if targets.len() > 1 {
                format!("Open {} prototype classes", targets.len())
            } else {
                "Open prototype class".to_owned()
            };
            result.push(related_lens(
                service_code_lens_range(prototype.namespace_range, prototype.line, lines),
                title,
                targets,
            ));
        }
        sort_related_code_lenses(&mut result);
        Ok(result)
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}

fn service_forward_lenses(
    range: Range,
    service: &Service,
    definitions: &[Service],
) -> Vec<protocol::CodeLens> {
    [
        (&service.decorates, "Open decorated service"),
        (&service.parent, "Open parent service"),
    ]
    .into_iter()
    .filter_map(|(target, title)| {
        let targets = service_definition_targets(definitions, target);
        (!targets.is_empty()).then(|| related_lens(range, title, targets))
    })
    .collect()
}

fn service_reverse_lenses(
    range: Range,
    service: &Service,
    definitions: &[Service],
) -> Vec<protocol::CodeLens> {
    let mut decorators = Vec::new();
    let mut children = Vec::new();
    for candidate in definitions {
        if eq_ignore_case(&candidate.decorates, &service.id) {
            decorators.push(service_source_target(candidate));
        }
        if eq_ignore_case(&candidate.parent, &service.id) {
            children.push(service_source_target(candidate));
        }
    }
    let decorators = unique_related_targets(decorators);
    let children = unique_related_targets(children);
    let mut result = Vec::new();
    if !decorators.is_empty() {
        let title = if decorators.len() > 1 {
            format!("Open {} decorating services", decorators.len())
        } else {
            "Open decorating service".to_owned()
        };
        result.push(related_lens(range, title, decorators));
    }
    if !children.is_empty() {
        let title = if children.len() > 1 {
            format!("Open {} child services", children.len())
        } else {
            "Open child service".to_owned()
        };
        result.push(related_lens(range, title, children));
    }
    result
}

fn service_definition_targets(definitions: &[Service], id: &str) -> Vec<String> {
    if id.is_empty() {
        return Vec::new();
    }
    unique_related_targets(
        definitions
            .iter()
            .filter(|service| eq_ignore_case(&service.id, id))
            .map(service_source_target)
            .collect(),
    )
}

fn service_source_target(service: &Service) -> String {
    related_target(&service.path, i64::from(service.line))
}

fn service_code_lens_range(range: TextRange, line: u32, lines: &LineIndex) -> Range {
    if !range.is_empty() {
        return related_protocol_range(range, lines);
    }
    line_range(line.max(1) - 1)
}
