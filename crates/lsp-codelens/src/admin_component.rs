//! The portable equivalent of the PhpStorm component registration/override
//! gutter markers.

use std::collections::BTreeSet;
use std::sync::Arc;

use swlsp_admin::{AdminComponentIndexer, ComponentRegistrationKind, VueComponent};
use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol::{self as protocol, Range};

use crate::{
    by_line_title, cancelled, line_range, lower_ext, related_lens, related_target, same_path,
    unique_related_targets,
};

pub struct AdminComponentCodeLensProvider {
    index: Option<Arc<AdminComponentIndexer>>,
}

impl AdminComponentCodeLensProvider {
    pub fn new(index: Option<Arc<AdminComponentIndexer>>) -> Self {
        Self { index }
    }
}

fn is_register(kind: ComponentRegistrationKind) -> bool {
    matches!(
        kind,
        ComponentRegistrationKind::Register | ComponentRegistrationKind::Unset
    )
}

impl CodeLensProvider for AdminComponentCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        let ext = lower_ext(&request.params.text_document.uri);
        if !matches!(ext.as_str(), ".js" | ".ts" | ".twig" | ".vue") {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        let mut components: Vec<VueComponent> = index.get_all_components_view()?.to_vec();
        components.sort_by(|left, right| {
            left.file_path
                .cmp(&right.file_path)
                .then(left.line.cmp(&right.line))
                .then_with(|| left.name.cmp(&right.name))
        });
        match ext.as_str() {
            ".twig" => self.template_code_lenses(index, ctx, &path, &components),
            ".vue" => {
                let mut result = self.script_code_lenses(index, ctx, &path, &components)?;
                result.extend(self.template_code_lenses(index, ctx, &path, &components)?);
                result.sort_by(by_line_title);
                Ok(result)
            }
            _ => self.script_code_lenses(index, ctx, &path, &components),
        }
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}

impl AdminComponentCodeLensProvider {
    fn script_code_lenses(
        &self,
        index: &AdminComponentIndexer,
        ctx: &Context,
        path: &str,
        components: &[VueComponent],
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let mut result = Vec::new();
        for component in components {
            cancelled(ctx)?;
            if !component_source_matches_path(component, path) {
                continue;
            }
            let mut line = i64::from(component.line) - 1;
            if line < 0
                || (!component.definition_path.is_empty()
                    && same_path(&component.definition_path, path)
                    && !same_path(&component.file_path, path))
            {
                line = 0;
            }
            let line = line as u32;
            let template_path = component_template_path(index, component)?;
            if !template_path.is_empty() && !same_path(&template_path, path) {
                result.push(related_lens(
                    line_range(line),
                    "Open component template",
                    vec![related_target(&template_path, 1)],
                ));
            }
            let mut targets = Vec::new();
            let mut title = "Open base component";
            if is_register(component.kind) {
                title = "Open component extensions";
                for candidate in components {
                    if candidate.target_component == component.name
                        && candidate.file_path != component.file_path
                    {
                        targets.push(related_target(
                            &candidate.file_path,
                            i64::from(candidate.line),
                        ));
                    }
                }
            } else {
                for candidate in components {
                    if candidate.name == component.target_component && is_register(candidate.kind) {
                        targets.push(related_target(
                            &candidate.file_path,
                            i64::from(candidate.line),
                        ));
                    }
                }
            }
            let targets = unique_related_targets(targets);
            if targets.is_empty() {
                continue;
            }
            let title = if targets.len() > 1 {
                format!("{title} ({})", targets.len())
            } else {
                title.to_owned()
            };
            result.push(related_lens(line_range(line), title, targets));
        }
        result.sort_by(by_line_title);
        Ok(result)
    }

    fn template_code_lenses(
        &self,
        index: &AdminComponentIndexer,
        ctx: &Context,
        path: &str,
        components: &[VueComponent],
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let Some(owner) = index.get_component_by_template_path(path)? else {
            return Ok(Vec::new());
        };
        let mut definition_targets = Vec::new();
        let component_names: BTreeSet<&str> = BTreeSet::from([owner.name.as_str()]);
        for component in components {
            cancelled(ctx)?;
            if component.name != owner.name {
                continue;
            }
            let template_path = component_template_path(index, component)?;
            if template_path.is_empty() || !same_path(&template_path, path) {
                continue;
            }
            let (target_path, target_line) = if component.definition_path.is_empty() {
                (component.file_path.as_str(), i64::from(component.line))
            } else if same_path(&component.definition_path, &component.file_path) {
                (
                    component.definition_path.as_str(),
                    i64::from(component.line),
                )
            } else {
                (component.definition_path.as_str(), 1)
            };
            if !same_path(target_path, path) {
                definition_targets.push(related_target(target_path, target_line));
            }
        }
        let definition_targets = unique_related_targets(definition_targets);
        let mut result = Vec::new();
        if !definition_targets.is_empty() {
            let title = if component_names.len() == 1 {
                format!("Open {} component definition", owner.name)
            } else if definition_targets.len() > 1 {
                format!("Open component definitions ({})", definition_targets.len())
            } else {
                "Open component definition".to_owned()
            };
            result.push(related_lens(Range::default(), title, definition_targets));
        }
        let extension_targets = unique_related_targets(
            components
                .iter()
                .filter(|component| component_names.contains(component.target_component.as_str()))
                .map(|component| related_target(&component.file_path, i64::from(component.line)))
                .collect(),
        );
        if !extension_targets.is_empty() {
            let title = if extension_targets.len() > 1 {
                format!("Open component extensions ({})", extension_targets.len())
            } else {
                "Open component extensions".to_owned()
            };
            result.push(related_lens(Range::default(), title, extension_targets));
        }
        Ok(result)
    }
}

fn component_template_path(
    index: &AdminComponentIndexer,
    component: &VueComponent,
) -> ProviderResult<String> {
    if !component.template_path.is_empty() {
        return Ok(component.template_path.clone());
    }
    Ok(index
        .get_component_registration_with_definition(component)?
        .template_path)
}

fn component_source_matches_path(component: &VueComponent, path: &str) -> bool {
    same_path(&component.file_path, path)
        || (!component.definition_path.is_empty() && same_path(&component.definition_path, path))
}
