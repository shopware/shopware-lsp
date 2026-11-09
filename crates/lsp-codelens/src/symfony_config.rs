//! The portable counterpart of the reference plugin's PHP/YAML configuration
//! line markers.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_symfonyconfig::Index;
use swlsp_textutil::lowercase;

use crate::{
    cancelled, lower_ext, related_lens, related_protocol_range, related_source_line,
    related_target, sort_related_code_lenses, unique_related_targets,
};

pub struct SymfonyConfigCodeLensProvider {
    index: Option<Arc<Index>>,
}

impl SymfonyConfigCodeLensProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl CodeLensProvider for SymfonyConfigCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        let document = &request.document;
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        let extension = lower_ext(&path);
        if !matches!(extension.as_str(), ".php" | ".yaml" | ".yml") {
            return Ok(Vec::new());
        }
        let wanted = if extension == ".php" {
            PHP_STRING
        } else {
            YAML_SCALAR
        };
        let lines = document.line_index();
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        let mut root_targets: HashMap<String, Vec<String>> = HashMap::new();
        for node in root.descendants() {
            cancelled(ctx)?;
            if node.kind() != wanted {
                continue;
            }
            if let Some(reference) = swlsp_symfonyconfig::root_reference_at(&node)
                && seen.insert(("root", reference.range))
            {
                let key = lowercase(&reference.name);
                let targets = match root_targets.get(&key) {
                    Some(targets) => targets.clone(),
                    None => {
                        let targets = root_targets_for(index, &reference.name)?;
                        root_targets.insert(key, targets.clone());
                        targets
                    }
                };
                if !targets.is_empty() {
                    let title = if targets.len() > 1 {
                        format!("Open {} configuration declarations", targets.len())
                    } else {
                        "Open configuration declaration".to_owned()
                    };
                    result.push(related_lens(
                        related_protocol_range(reference.range, lines),
                        title,
                        targets,
                    ));
                }
            }
            if let Some(reference) = swlsp_symfonyconfig::resource_reference_at(&node) {
                if !seen.insert(("resource", reference.range)) {
                    continue;
                }
                let targets = unique_related_targets(
                    swlsp_symfonyconfig::resource_files(&path, &reference.path)
                        .iter()
                        .map(|target| related_target(target, 1))
                        .collect(),
                );
                if targets.is_empty() {
                    continue;
                }
                let title = if targets.len() > 1 {
                    format!("Open {} configuration resources", targets.len())
                } else {
                    "Open configuration resource".to_owned()
                };
                result.push(related_lens(
                    related_protocol_range(reference.range, lines),
                    title,
                    targets,
                ));
            }
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

fn root_targets_for(index: &Index, name: &str) -> ProviderResult<Vec<String>> {
    Ok(unique_related_targets(
        index
            .roots(name)?
            .iter()
            .map(|root| {
                related_target(
                    &root.file,
                    related_source_line(&root.file, root.range.start().into()),
                )
            })
            .collect(),
    ))
}
