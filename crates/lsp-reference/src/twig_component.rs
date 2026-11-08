//! Twig component and Live action references (port of
//! `twig_component_reference.go`).

use std::collections::BTreeMap;
use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpIndex, symbol_at};
use swlsp_php_types::{equal_fold, go_to_lower};
use swlsp_syntax::TextRange;
use swlsp_twigcomponent::{
    Component, Index, LiveAction, declaration_at_php, live_action_reference_at,
    live_action_references_in_twig, usage_at, usages_in_twig,
};

use crate::{
    FileLines, document_location, lower_ext, php_context, request_offset, unique_locations,
};

pub struct TwigComponentReferenceProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl TwigComponentReferenceProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl ReferencesProvider for TwigComponentReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(root), Some(line_index), Some(offset)) = (
            &self.index,
            &request.root,
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(Vec::new());
        };
        let current_path =
            swlsp_uriutil::path(&request.params.text_document.uri).unwrap_or_default();
        let extension = lower_ext(&current_path);
        let name = match extension.as_str() {
            ".twig" => {
                if let Some(action) = live_action_reference_at(&current_path, root, offset)
                    && !action.name.is_empty()
                {
                    return self.live_action_references(
                        index,
                        &current_path,
                        &action.name,
                        request,
                        &[],
                    );
                }
                let Some(usage) = usage_at(&current_path, root, offset) else {
                    return Ok(Vec::new());
                };
                usage.name
            }
            ".php" => {
                if let Some(actions) = self.live_actions_at_php(index, request, offset) {
                    return self.live_action_references(
                        index,
                        &current_path,
                        &actions[0].name.clone(),
                        request,
                        &actions,
                    );
                }
                let Some(declaration) = declaration_at_php(&current_path, root, offset) else {
                    return Ok(Vec::new());
                };
                let Some(component) = index.resolve_declaration(&declaration)? else {
                    return Ok(Vec::new());
                };
                component.name
            }
            _ => return Ok(Vec::new()),
        };

        let mut files = FileLines::default();
        let mut result = Vec::new();
        for usage in index.usages(&name)? {
            if usage.file == current_path {
                continue;
            }
            result.extend(files.location(&usage.file, usage.range));
        }
        if extension == ".twig" {
            for usage in usages_in_twig(&current_path, root) {
                if usage.name == name {
                    result.push(document_location(request, line_index, usage.range));
                }
            }
        }
        if request.params.context.include_declaration {
            result.extend(self.declaration_locations(index, &mut files, &name)?);
        }
        Ok(unique_locations(result))
    }
}

impl TwigComponentReferenceProvider {
    fn live_actions_at_php(
        &self,
        index: &Index,
        request: &ReferenceRequest,
        offset: u32,
    ) -> Option<Vec<LiveAction>> {
        let context = php_context(self.php.as_ref(), request)?;
        let symbol = symbol_at(&context.document, &context.snapshot, offset)?;
        if !symbol.is_function_like() {
            return None;
        }
        let components = index.components().ok()?;
        let mut result = Vec::new();
        for component in &components {
            let Ok(actions) = index.live_actions(&component.name) else {
                continue;
            };
            for action in actions {
                if action.file == *symbol.path && action.range == symbol.selection_range {
                    result.push(action);
                }
            }
        }
        (!result.is_empty()).then_some(result)
    }

    fn live_action_references(
        &self,
        index: &Index,
        current_path: &str,
        action_name: &str,
        request: &ReferenceRequest,
        known_actions: &[LiveAction],
    ) -> ProviderResult<Vec<protocol::Location>> {
        // Go iterates a map here; a sorted map keeps the result deterministic.
        let mut components: BTreeMap<String, Component> = BTreeMap::new();
        let mut actions = Vec::new();
        let twig = lower_ext(current_path) == ".twig";
        if twig {
            for component in index.components_for_template(current_path)? {
                components.insert(go_to_lower(&component.name), component);
            }
            for action in index.live_actions_for_template(current_path)? {
                if equal_fold(&action.name, action_name) {
                    actions.push(action);
                }
            }
        } else {
            let all = index.components()?;
            for action in known_actions {
                if !equal_fold(&action.name, action_name) {
                    continue;
                }
                actions.push(action.clone());
                for component in &all {
                    let Ok(component_actions) = index.live_actions(&component.name) else {
                        continue;
                    };
                    if component_actions.iter().any(|candidate| {
                        candidate.file == action.file && candidate.range == action.range
                    }) {
                        components.insert(go_to_lower(&component.name), component.clone());
                    }
                }
            }
        }
        if components.is_empty() || actions.is_empty() {
            return Ok(Vec::new());
        }

        let mut files = FileLines::default();
        let mut result = Vec::new();
        for component in components.values() {
            for reference in index.live_action_references(&component.name, action_name)? {
                if reference.file == current_path {
                    continue;
                }
                result.extend(files.location(&reference.file, reference.range));
            }
        }
        if twig && let (Some(root), Some(line_index)) = (&request.root, request.line_index()) {
            for reference in live_action_references_in_twig(current_path, root) {
                if equal_fold(&reference.name, action_name) {
                    result.push(document_location(request, line_index, reference.range));
                }
            }
        }
        if request.params.context.include_declaration {
            for action in &actions {
                result.extend(files.location(&action.file, action.range));
            }
        }
        Ok(unique_locations(result))
    }

    fn declaration_locations(
        &self,
        index: &Index,
        files: &mut FileLines,
        name: &str,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let mut result = Vec::new();
        for component in index.find(name)? {
            if !component.class.is_empty()
                && let Some(php) = &self.php
                && let Some(class) = php.find_class(&component.class)
                && let Some(location) = files.location(&class.path, class.selection_range)
            {
                result.push(location);
                continue;
            }
            for template in index.template_files(&component)? {
                result.extend(files.location(&template, TextRange::default()));
            }
        }
        Ok(result)
    }
}
