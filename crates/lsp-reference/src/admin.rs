//! Administration reference graph.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_admin::{
    AdminCMSRegistrationKind, AdminComponentIndexer, AdminSourceRange, AdminSymbolKind,
    AdminSymbolTarget, AdminUsageSet, VueComponent, VueComponentEvent, VueComponentProp,
    VueComponentSlotMember, VueLocalComponent, admin_usage_key, canonical_event_name,
    collect_javascript_usages, collect_twig_usages, normalize_event_name, normalize_model_argument,
    normalize_prop_name, normalize_slot_name, twig_component_object_binding_field_at_offset,
    twig_dynamic_component_selector, twig_scoped_slot_binding_references,
    twig_scoped_slot_member_references, twig_slot_owner_starting_tag, twig_symbol_at_offset,
    twig_vue_binding_at_offset, twig_vue_binding_member_access_references,
    twig_vue_binding_references, twig_vue_expression_member_accesses,
    twig_vue_expression_member_at_offset, twig_vue_expression_root_identifiers,
    vue_object_binding_fields, vue_slot_reference_for_attribute,
};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    Context, ProviderResult, ReferenceRequest, ReferencesProvider, effective_syntax_language,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::ast::{AstNode, HtmlEndingTag, HtmlStartingTag};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_uriutil::filepath;

use crate::{line_location, lower_ext, request_offset, sort_locations};

type Handled = ProviderResult<Option<Vec<protocol::Location>>>;

/// Exposes the cached cross-language usage graph for Administration
/// registry symbols.
pub struct AdminReferenceProvider {
    index: Option<Arc<AdminComponentIndexer>>,
}

impl AdminReferenceProvider {
    pub fn new(index: Option<Arc<AdminComponentIndexer>>) -> Self {
        Self { index }
    }
}

impl ReferencesProvider for AdminReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(_)) = (&self.index, &request.root) else {
            return Ok(Vec::new());
        };
        let provider = Admin { index, request };
        macro_rules! try_handlers {
            ($($handler:ident),*) => {
                $(
                    if let Some(locations) = provider.$handler()? {
                        return Ok(locations);
                    }
                )*
            };
        }
        try_handlers!(
            scoped_slot_member_references,
            scoped_slot_local_references,
            vue_local_references,
            component_object_binding_references,
            vue_instance_root_references,
            vue_instance_member_references,
            local_component_tag_references,
            dynamic_component_slot_references,
            dynamic_component_attribute_references,
            component_model_references
        );
        match provider.target_at()? {
            Some(target) => provider.symbol_references(&[target]),
            None => Ok(Vec::new()),
        }
    }
}

struct Admin<'a> {
    index: &'a AdminComponentIndexer,
    request: &'a ReferenceRequest,
}

/// The request inputs every template handler needs.
struct TemplateInputs<'a> {
    root: &'a SyntaxNode,
    offset: u32,
}

impl<'a> Admin<'a> {
    fn uri(&self) -> &str {
        &self.request.params.text_document.uri
    }

    fn include_declaration(&self) -> bool {
        self.request.params.context.include_declaration
    }

    fn path(&self) -> ProviderResult<String> {
        Ok(swlsp_uriutil::path(self.uri())?)
    }

    fn is_template(&self) -> bool {
        let uri = self.uri();
        if uri.is_empty() {
            return false;
        }
        match lower_ext(uri).as_str() {
            ".twig" => true,
            ".vue" => {
                effective_syntax_language(Some(LanguageId::Vue), self.request.node.as_ref())
                    == Some(LanguageId::Twig)
            }
            _ => false,
        }
    }

    fn is_script(&self) -> bool {
        let uri = self.uri();
        if uri.is_empty() {
            return false;
        }
        match lower_ext(uri).as_str() {
            ".js" | ".ts" => true,
            ".vue" => {
                effective_syntax_language(Some(LanguageId::Vue), self.request.node.as_ref())
                    == Some(LanguageId::JavaScript)
            }
            _ => false,
        }
    }

    /// The root and cursor offset of a template request with a line index.
    fn template_inputs(&self) -> Option<TemplateInputs<'a>> {
        let root = self.request.root.as_ref()?;
        let offset = request_offset(self.request)?;
        if !self.is_template() {
            return None;
        }
        Some(TemplateInputs { root, offset })
    }

    fn source(&self) -> &str {
        self.request.source()
    }

    fn live_owner(&self, path: &str, root: &SyntaxNode) -> ProviderResult<Option<VueComponent>> {
        Ok(self.index.get_component_for_document(
            path,
            Some(root),
            self.source(),
            self.request.line_index(),
        )?)
    }

    fn local_ranges_to_locations(&self, ranges: &[TextRange]) -> Vec<protocol::Location> {
        let Some(line_index) = self.request.line_index() else {
            return Vec::new();
        };
        ranges
            .iter()
            .map(|range| protocol::Location {
                uri: self.uri().to_owned(),
                range: swlsp_lsp::protocol_range(line_index, *range),
            })
            .collect()
    }

    fn scoped_slot_local_references(&self) -> Handled {
        let Some(inputs) = self.template_inputs() else {
            return Ok(None);
        };
        let Some(node) = &self.request.node else {
            return Ok(None);
        };
        let template_path = self.path()?;
        let live_owner = self.live_owner(&template_path, inputs.root)?;
        let Some(resolved) = self.index.resolve_twig_scoped_slot_binding_for_owner(
            inputs.root,
            node,
            self.source(),
            inputs.offset,
            &template_path,
            live_owner.as_ref(),
        )?
        else {
            return Ok(None);
        };
        if resolved.scoped.scope.is_binding_offset(inputs.offset)
            && resolved.identifier == resolved.binding.member_name
            && resolved.binding.member_name != resolved.binding.local_name
        {
            return Ok(Some(Vec::new()));
        }
        let mut ranges = twig_scoped_slot_binding_references(inputs.root, self.source(), &resolved);
        if !self.include_declaration() {
            ranges.retain(|range| *range != resolved.binding.local_range);
        }
        Ok(Some(self.local_ranges_to_locations(&ranges)))
    }

    fn dynamic_component_slot_references(&self) -> Handled {
        let Some(inputs) = self.template_inputs() else {
            return Ok(None);
        };
        let Some(attribute) = inputs
            .root
            .node_at(TextSize::from(inputs.offset))
            .and_then(|node| twigquery::html_attribute_at(&node))
        else {
            return Ok(None);
        };
        let attribute_name = twigquery::html_attribute_name(&attribute).unwrap_or_default();
        if normalize_slot_name(&attribute_name).is_empty() {
            return Ok(None);
        }
        let Some(start_tag) = twigquery::starting_html_tag_at(&attribute) else {
            return Ok(None);
        };
        let owner = twig_slot_owner_starting_tag(&start_tag);
        if owner
            .as_ref()
            .and_then(twig_dynamic_component_selector)
            .is_none()
        {
            return Ok(None);
        }
        let Some(name_token) = swlsp_parser_twig::ast::HtmlAttribute::cast(attribute.clone())
            .and_then(|attribute| attribute.name())
        else {
            return Ok(Some(Vec::new()));
        };
        let Some(reference) =
            vue_slot_reference_for_attribute(&attribute_name, name_token.text_range())
        else {
            return Ok(Some(Vec::new()));
        };
        if inputs.offset < u32::from(reference.range.start())
            || inputs.offset > u32::from(reference.range.end())
        {
            return Ok(Some(Vec::new()));
        }
        let template_path = self.path()?;
        let live_owner = self.live_owner(&template_path, inputs.root)?;
        let (components, complete) = self.index.resolve_twig_slot_consumer_components(
            &template_path,
            &start_tag,
            live_owner.as_ref(),
        )?;
        if !complete {
            return Ok(Some(Vec::new()));
        }
        let targets = dynamic_component_attribute_targets(&components, &attribute_name);
        if targets.is_empty() {
            return Ok(Some(Vec::new()));
        }
        let locations = self.symbol_references(&targets)?;
        let local = self.dynamic_component_slot_locations(
            inputs.root,
            &template_path,
            live_owner.as_ref(),
            &targets,
        )?;
        Ok(Some(merge_locations(&[locations, local])))
    }

    fn component_object_binding_references(&self) -> Handled {
        let Some(inputs) = self.template_inputs() else {
            return Ok(None);
        };
        let Some((start_tag, field)) =
            twig_component_object_binding_field_at_offset(inputs.root, inputs.offset)
        else {
            return Ok(None);
        };
        let template_path = self.path()?;
        let live_owner = self.live_owner(&template_path, inputs.root)?;
        if let Some(selector) = twig_dynamic_component_selector(&start_tag) {
            let (_, components, complete) =
                self.index.resolve_dynamic_component_contracts_for_owner(
                    &template_path,
                    &selector,
                    live_owner.as_ref(),
                    Some(&start_tag),
                )?;
            if !complete {
                return Ok(Some(Vec::new()));
            }
            let targets = dynamic_component_attribute_targets(&components, &field.name);
            let locations = self.symbol_references(&targets)?;
            let local = self.dynamic_component_attribute_locations(
                inputs.root,
                &template_path,
                live_owner.as_ref(),
                &targets,
            )?;
            return Ok(Some(merge_locations(&[locations, local])));
        }
        let Some(target) = self
            .index
            .twig_symbol_at(&template_path, inputs.root, inputs.offset)?
        else {
            return Ok(Some(Vec::new()));
        };
        Ok(Some(self.symbol_references(&[target])?))
    }

    fn dynamic_component_attribute_references(&self) -> Handled {
        let Some(inputs) = self.template_inputs() else {
            return Ok(None);
        };
        let attribute = inputs
            .root
            .node_at(TextSize::from(inputs.offset))
            .and_then(|node| twigquery::html_attribute_at(&node));
        let start_tag = attribute.as_ref().and_then(twigquery::starting_html_tag_at);
        let Some(selector) = start_tag.as_ref().and_then(twig_dynamic_component_selector) else {
            return Ok(None);
        };
        let (Some(attribute), Some(start_tag)) = (attribute, start_tag) else {
            return Ok(None);
        };
        let attribute_name = twigquery::html_attribute_name(&attribute).unwrap_or_default();
        if attribute_name == selector.attribute_name {
            return Ok(None);
        }
        let template_path = self.path()?;
        let live_owner = self.live_owner(&template_path, inputs.root)?;
        let (_, components, complete) = self.index.resolve_dynamic_component_contracts_for_owner(
            &template_path,
            &selector,
            live_owner.as_ref(),
            Some(&start_tag),
        )?;
        if !complete {
            return Ok(None);
        }
        let targets = dynamic_component_attribute_targets(&components, &attribute_name);
        if targets.is_empty() {
            return Ok(Some(Vec::new()));
        }
        let locations = self.symbol_references(&targets)?;
        let local = self.dynamic_component_attribute_locations(
            inputs.root,
            &template_path,
            live_owner.as_ref(),
            &targets,
        )?;
        Ok(Some(merge_locations(&[locations, local])))
    }

    fn dynamic_component_attribute_locations(
        &self,
        root: &SyntaxNode,
        template_path: &str,
        live_owner: Option<&VueComponent>,
        targets: &[AdminSymbolTarget],
    ) -> ProviderResult<Vec<protocol::Location>> {
        let target_set: HashSet<&AdminSymbolTarget> = targets.iter().collect();
        let matches = |components: &[VueComponent], name: &str| {
            dynamic_component_attribute_targets(components, name)
                .iter()
                .any(|target| target_set.contains(target))
        };
        let mut ranges = Vec::new();
        for start_tag in twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG]) {
            let Some(selector) = twig_dynamic_component_selector(&start_tag) else {
                continue;
            };
            let (_, components, complete) =
                self.index.resolve_dynamic_component_contracts_for_owner(
                    template_path,
                    &selector,
                    live_owner,
                    Some(&start_tag),
                )?;
            if !complete {
                continue;
            }
            let Some(tag) = HtmlStartingTag::cast(start_tag.clone()) else {
                continue;
            };
            for attribute in tag.attributes() {
                let Some(name_token) = attribute.name() else {
                    continue;
                };
                let name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
                if name == selector.attribute_name {
                    continue;
                }
                if name == "v-bind"
                    && let Some(inner) = attribute.value().and_then(|value| value.inner())
                {
                    let inner = inner.syntax();
                    let (fields, _) = vue_object_binding_fields(
                        &inner.text().to_string(),
                        inner.text_range().start().into(),
                    );
                    for field in fields {
                        if matches(&components, &field.name) {
                            ranges.push(field.name_range);
                        }
                    }
                }
                if matches(&components, &name) {
                    ranges.push(name_token.text_range());
                }
            }
        }
        Ok(self.local_ranges_to_locations(&ranges))
    }

    fn dynamic_component_slot_locations(
        &self,
        root: &SyntaxNode,
        template_path: &str,
        live_owner: Option<&VueComponent>,
        targets: &[AdminSymbolTarget],
    ) -> ProviderResult<Vec<protocol::Location>> {
        let target_set: HashSet<&AdminSymbolTarget> = targets.iter().collect();
        let mut ranges = Vec::new();
        for start_tag in twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG]) {
            let owner = twig_slot_owner_starting_tag(&start_tag);
            if owner
                .as_ref()
                .and_then(twig_dynamic_component_selector)
                .is_none()
            {
                continue;
            }
            let (components, complete) = self.index.resolve_twig_slot_consumer_components(
                template_path,
                &start_tag,
                live_owner,
            )?;
            if !complete {
                continue;
            }
            let Some(tag) = HtmlStartingTag::cast(start_tag.clone()) else {
                continue;
            };
            for attribute in tag.attributes() {
                let Some(name_token) = attribute.name() else {
                    continue;
                };
                let name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
                let Some(reference) =
                    vue_slot_reference_for_attribute(&name, name_token.text_range())
                else {
                    continue;
                };
                if dynamic_component_attribute_targets(&components, &name)
                    .iter()
                    .any(|target| target_set.contains(target))
                {
                    ranges.push(reference.range);
                }
            }
        }
        Ok(self.local_ranges_to_locations(&ranges))
    }

    fn component_model_references(&self) -> Handled {
        let Some(inputs) = self.template_inputs() else {
            return Ok(None);
        };
        let Some(raw) = twig_symbol_at_offset(inputs.root, inputs.offset) else {
            return Ok(None);
        };
        if raw.kind != AdminSymbolKind::ComponentModel {
            return Ok(None);
        }
        let path = self.path()?;
        let live_owner = self.live_owner(&path, inputs.root)?;
        let Some(component) =
            self.index
                .get_component_for_template_tag(&path, &raw.owner, live_owner.as_ref())?
        else {
            return Ok(Some(Vec::new()));
        };
        let Some(binding) = component.component_model(&raw.name) else {
            return Ok(Some(Vec::new()));
        };
        let prop_owner = first_non_empty(&[
            &binding.prop.file_path,
            &component.definition_path,
            &component.file_path,
        ]);
        let event_owner = first_non_empty(&[
            &binding.event.file_path,
            &component.definition_path,
            &component.file_path,
        ]);
        let mut targets = Vec::with_capacity(2);
        if !prop_owner.is_empty() {
            targets.push(AdminSymbolTarget {
                kind: AdminSymbolKind::ComponentProp,
                owner: prop_owner.to_owned(),
                name: binding.prop_name.clone(),
            });
        }
        if !event_owner.is_empty() {
            targets.push(AdminSymbolTarget {
                kind: AdminSymbolKind::ComponentEvent,
                owner: event_owner.to_owned(),
                name: binding.event_name.clone(),
            });
        }
        Ok(Some(self.symbol_references(&targets)?))
    }

    fn symbol_references(
        &self,
        targets: &[AdminSymbolTarget],
    ) -> ProviderResult<Vec<protocol::Location>> {
        let include_declaration = self.include_declaration();
        let (live_path, live_sets) = self.live_usage_sets()?;
        let live_path = live_path.map(|path| normalize_path(&path));
        let mut locations = Vec::new();
        let mut seen = HashSet::new();
        let mut add = |locations: &mut Vec<protocol::Location>, location: protocol::Location| {
            if seen.insert(location.clone()) {
                locations.push(location);
            }
        };
        for target in targets {
            let sets = self.index.get_symbol_usages(target)?;
            let mut has_declaration = false;
            for set in &sets {
                let mut set = set;
                if let Some(live_path) = &live_path
                    && normalize_path(&set.file_path) == *live_path
                {
                    let Some(live) =
                        live_sets.get(&admin_usage_key(set.kind, &set.owner, &set.name))
                    else {
                        continue;
                    };
                    set = live;
                }
                for occurrence in &set.occurrences {
                    if occurrence.declaration {
                        has_declaration = true;
                        if !include_declaration {
                            continue;
                        }
                    }
                    add(&mut locations, usage_location(&set.file_path, occurrence));
                }
            }
            if include_declaration && !has_declaration {
                for declaration in self.declaration_locations(target)? {
                    if let Some(live_path) = &live_path
                        && uri_path_equal(&declaration.uri, live_path)
                    {
                        continue;
                    }
                    add(&mut locations, declaration);
                }
            }
        }
        sort_locations(&mut locations);
        Ok(locations)
    }

    /// The usage sets of the open document keyed by usage key, with the
    /// document's path; `None` for unsupported documents.
    fn live_usage_sets(&self) -> ProviderResult<(Option<String>, HashMap<String, AdminUsageSet>)> {
        let (Some(_), Some(root), Some(line_index)) = (
            &self.request.document,
            &self.request.root,
            self.request.line_index(),
        ) else {
            return Ok((None, HashMap::new()));
        };
        let file_path = self.path()?;
        let sets = match lower_ext(&file_path).as_str() {
            ".js" | ".ts" => collect_javascript_usages(root, &file_path, line_index),
            ".twig" => collect_twig_usages(root, &file_path, line_index),
            ".vue" => {
                let mut sets = collect_javascript_usages(root, &file_path, line_index);
                sets.extend(collect_twig_usages(root, &file_path, line_index));
                sets
            }
            _ => return Ok((None, HashMap::new())),
        };
        let result = sets
            .into_iter()
            .map(|set| (admin_usage_key(set.kind, &set.owner, &set.name), set))
            .collect();
        Ok((Some(file_path), result))
    }

    fn vue_instance_root_references(&self) -> Handled {
        let Some(inputs) = self.template_inputs() else {
            return Ok(None);
        };
        let template_path = self.path()?;
        let source = self.source();
        let Ok(Some((target, member))) =
            self.index
                .twig_component_member_at(&template_path, inputs.root, source, inputs.offset)
        else {
            return Ok(None);
        };
        let sets = self.index.get_symbol_usages(&target)?;
        let mut ranges = Vec::new();
        for identifier in twig_vue_expression_root_identifiers(inputs.root, source) {
            let candidate = self.index.twig_component_member_at(
                &template_path,
                inputs.root,
                source,
                identifier.range.start().into(),
            )?;
            if candidate.is_some_and(|(candidate, _)| candidate == target) {
                ranges.push(identifier.range);
            }
        }
        let mut locations = self.local_ranges_to_locations(&ranges);
        let mut seen_declaration = false;
        let normalized_template = normalize_path(&template_path);
        for set in &sets {
            if normalize_path(&set.file_path) == normalized_template {
                continue;
            }
            for occurrence in &set.occurrences {
                if occurrence.declaration {
                    seen_declaration = true;
                    if !self.include_declaration() {
                        continue;
                    }
                }
                locations.push(usage_location(&set.file_path, occurrence));
            }
        }
        if self.include_declaration() && !seen_declaration && !member.file_path.is_empty() {
            if member.renameable() {
                locations.push(usage_location(&member.file_path, &member.name_range));
            } else {
                locations.push(line_location(&member.file_path, i64::from(member.line)));
            }
        }
        sort_locations(&mut locations);
        Ok(Some(locations))
    }

    fn local_component_tag_references(&self) -> Handled {
        let (Some(root), Some(offset)) = (&self.request.root, request_offset(self.request)) else {
            return Ok(None);
        };
        let extension = lower_ext(self.uri());
        if extension == ".js" || extension == ".ts" || (extension == ".vue" && self.is_script()) {
            return self.local_component_definition_references();
        }
        if extension != ".twig" && (extension != ".vue" || !self.is_template()) {
            return Ok(None);
        }
        let template_path = self.path()?;
        let Some(target) = twig_symbol_at_offset(root, offset) else {
            return Ok(None);
        };
        if target.kind != AdminSymbolKind::Component {
            return Ok(None);
        }
        let Some(owner) = self.index.get_component_by_template_path(&template_path)? else {
            return Ok(None);
        };
        let Some(local) = owner.local_component(&target.name) else {
            return Ok(None);
        };
        let mut ranges = Vec::new();
        for node in twigquery::iterate_nodes(
            root,
            &[twigsyntax::HTML_STARTING_TAG, twigsyntax::HTML_ENDING_TAG],
        ) {
            if node.kind() == twigsyntax::HTML_STARTING_TAG {
                if let Some(name) = HtmlStartingTag::cast(node.clone()).and_then(|tag| tag.name())
                    && name.text() == local.name
                {
                    ranges.push(name.text_range());
                }
                if let Some(selector) = twig_dynamic_component_selector(&node) {
                    for candidate in &selector.candidates {
                        if candidate.name == local.name {
                            ranges.push(candidate.range);
                        }
                    }
                }
            } else if let Some(name) = HtmlEndingTag::cast(node.clone()).and_then(|tag| tag.name())
                && name.text() == local.name
            {
                ranges.push(name.text_range());
            }
        }
        let mut locations = self.local_ranges_to_locations(&ranges);
        if self.include_declaration() && !local.file_path.is_empty() {
            locations.push(local_component_declaration(local));
        }
        sort_locations(&mut locations);
        Ok(Some(locations))
    }

    fn local_component_definition_references(&self) -> Handled {
        let definition_path = self.path()?;
        let position = self.request.params.position;
        let Some((owner, local)) = self.index.get_local_component_at_definition_position(
            &definition_path,
            position.line,
            position.character,
        )?
        else {
            return Ok(None);
        };
        let sets = self
            .index
            .get_usages(AdminSymbolKind::Component, "", &local.name)?;
        let owner_template = filepath::clean(&owner.template_path);
        let mut locations = Vec::new();
        for set in &sets {
            if filepath::clean(&set.file_path) != owner_template {
                continue;
            }
            for occurrence in &set.occurrences {
                locations.push(usage_location(&set.file_path, occurrence));
            }
        }
        if self.include_declaration() {
            locations.push(local_component_declaration(&local));
        }
        sort_locations(&mut locations);
        Ok(Some(locations))
    }

    fn scoped_slot_member_references(&self) -> Handled {
        let Some(inputs) = self.template_inputs() else {
            return Ok(None);
        };
        let Some(node) = &self.request.node else {
            return Ok(None);
        };
        let source = self.source();
        let Some(access) = twig_vue_expression_member_at_offset(inputs.root, source, inputs.offset)
        else {
            return Ok(None);
        };
        if access.member.is_empty() {
            return Ok(None);
        }
        let template_path = self.path()?;
        let live_owner = self.live_owner(&template_path, inputs.root)?;
        let Some(resolved) = self.index.resolve_twig_scoped_slot_member_for_owner(
            inputs.root,
            node,
            source,
            inputs.offset,
            &template_path,
            live_owner.as_ref(),
        )?
        else {
            return Ok(None);
        };
        if let Some(binding) =
            twig_vue_binding_at_offset(inputs.root, source, access.root_range.start().into())
            && binding.scope_range.len() <= resolved.scoped.scope.template_range.len()
        {
            return Ok(None);
        }
        let ranges = twig_scoped_slot_member_references(inputs.root, source, &resolved);
        let mut locations = self.local_ranges_to_locations(&ranges);
        if self.include_declaration() && resolved.member_found {
            let single = [resolved.member.clone()];
            let members: &[VueComponentSlotMember] = if resolved.members.is_empty() {
                &single
            } else {
                &resolved.members
            };
            for member in members {
                if member.file_path.is_empty() {
                    continue;
                }
                if member.name_range.declaration || member.name_range.identifier {
                    locations.push(usage_location(&member.file_path, &member.name_range));
                } else {
                    locations.push(line_location(&member.file_path, i64::from(member.line)));
                }
            }
        }
        Ok(Some(merge_locations(&[locations])))
    }

    fn vue_local_references(&self) -> Handled {
        let Some(inputs) = self.template_inputs() else {
            return Ok(None);
        };
        let source = self.source();
        if let Some(access) =
            twig_vue_expression_member_at_offset(inputs.root, source, inputs.offset)
            && !access.member.is_empty()
            && let Some(binding) =
                twig_vue_binding_at_offset(inputs.root, source, access.root_range.start().into())
        {
            let ranges =
                twig_vue_binding_member_access_references(inputs.root, source, &binding, &access);
            return Ok(Some(self.local_ranges_to_locations(&ranges)));
        }
        let Some(binding) = twig_vue_binding_at_offset(inputs.root, source, inputs.offset) else {
            return Ok(None);
        };
        let mut ranges = twig_vue_binding_references(inputs.root, source, &binding);
        if !self.include_declaration() {
            ranges.retain(|range| *range != binding.declaration_range);
        }
        Ok(Some(self.local_ranges_to_locations(&ranges)))
    }

    fn vue_instance_member_references(&self) -> Handled {
        let Some(inputs) = self.template_inputs() else {
            return Ok(None);
        };
        let source = self.source();
        let Some(access) = twig_vue_expression_member_at_offset(inputs.root, source, inputs.offset)
        else {
            return Ok(None);
        };
        if access.member.is_empty() {
            return Ok(None);
        }
        let template_path = self.path()?;
        let live_component = self.live_owner(&template_path, inputs.root)?;
        let Some(target) = self.index.resolve_twig_vue_instance_member_for_component(
            inputs.root,
            source,
            inputs.offset,
            &template_path,
            live_component.as_ref(),
        )?
        else {
            return Ok(None);
        };
        if !target.member_found {
            return Ok(Some(Vec::new()));
        }
        let mut ranges = Vec::new();
        for candidate in twig_vue_expression_member_accesses(inputs.root, source) {
            if candidate.root != access.root || !candidate.same_path(&access) {
                continue;
            }
            let resolved = self.index.resolve_twig_vue_instance_member_for_component(
                inputs.root,
                source,
                candidate.member_range.start().into(),
                &template_path,
                live_component.as_ref(),
            )?;
            let Some(resolved) = resolved else {
                continue;
            };
            if !resolved.member_found
                || resolved.root_member.name != target.root_member.name
                || resolved.member.name != target.member.name
                || resolved.member.definition_path != target.member.definition_path
                || resolved.member.definition_line != target.member.definition_line
            {
                continue;
            }
            ranges.push(candidate.member_range);
        }
        let mut locations = self.local_ranges_to_locations(&ranges);
        if self.include_declaration() && !target.member.definition_path.is_empty() {
            locations.push(line_location(
                &target.member.definition_path,
                i64::from(target.member.definition_line),
            ));
        }
        Ok(Some(locations))
    }

    fn target_at(&self) -> ProviderResult<Option<AdminSymbolTarget>> {
        let (Some(_), Some(root)) = (&self.request.document, &self.request.root) else {
            return Ok(None);
        };
        let javascript = |path: &str| -> ProviderResult<Option<AdminSymbolTarget>> {
            match &self.request.node {
                Some(node) => Ok(self.index.javascript_symbol_at(path, node)?),
                None => Ok(None),
            }
        };
        match lower_ext(self.uri()).as_str() {
            ".js" | ".ts" => javascript(&self.path()?),
            ".twig" => {
                let Some(offset) = request_offset(self.request) else {
                    return Ok(None);
                };
                Ok(self.index.twig_symbol_at(&self.path()?, root, offset)?)
            }
            ".vue" => {
                let path = self.path()?;
                if self.is_script() {
                    return javascript(&path);
                }
                let Some(offset) = request_offset(self.request).filter(|_| self.is_template())
                else {
                    return Ok(None);
                };
                Ok(self.index.twig_symbol_at(&path, root, offset)?)
            }
            _ => Ok(None),
        }
    }

    fn declaration_locations(
        &self,
        target: &AdminSymbolTarget,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let mut collector = DeclarationCollector {
            index: self.index,
            result: Vec::new(),
        };
        collector.collect(target)?;
        Ok(collector.result)
    }
}

fn first_non_empty<'s>(values: &[&'s str]) -> &'s str {
    values
        .iter()
        .copied()
        .find(|value| !value.is_empty())
        .unwrap_or("")
}

fn normalize_path(value: &str) -> String {
    if value.is_empty() {
        String::new()
    } else {
        filepath::clean(value)
    }
}

fn uri_path_equal(uri: &str, normalized_path: &str) -> bool {
    swlsp_uriutil::path(uri).is_ok_and(|resolved| normalize_path(&resolved) == normalized_path)
}

fn dynamic_component_attribute_targets(
    components: &[VueComponent],
    attribute_name: &str,
) -> Vec<AdminSymbolTarget> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    let mut add = |target: AdminSymbolTarget| {
        if target.name.is_empty() || target.owner.is_empty() || !seen.insert(target.clone()) {
            return;
        }
        result.push(target);
    };
    for component in components {
        if normalize_model_argument(attribute_name).is_some() {
            let Some(binding) = component.component_model(attribute_name) else {
                continue;
            };
            add(prop_reference_target(component, &binding.prop));
            add(event_reference_target(component, &binding.event));
            continue;
        }
        let event_name = normalize_event_name(attribute_name);
        if !event_name.is_empty() {
            if let Some(event) = component.component_event(&event_name) {
                add(event_reference_target(component, &event));
            }
            continue;
        }
        let slot_name = normalize_slot_name(attribute_name);
        if !slot_name.is_empty() {
            if let Some(slot) = component.component_slot(&slot_name) {
                let owner = if slot.file_path.is_empty() {
                    component.template_path.clone()
                } else {
                    slot.file_path.clone()
                };
                add(AdminSymbolTarget {
                    kind: AdminSymbolKind::ComponentSlot,
                    owner,
                    name: slot_name,
                });
            }
            continue;
        }
        if let Some(prop) = component.component_prop(&normalize_prop_name(attribute_name)) {
            add(prop_reference_target(component, prop));
        }
    }
    result
}

fn prop_reference_target(component: &VueComponent, prop: &VueComponentProp) -> AdminSymbolTarget {
    AdminSymbolTarget {
        kind: AdminSymbolKind::ComponentProp,
        owner: first_non_empty(&[
            &prop.file_path,
            &component.definition_path,
            &component.file_path,
        ])
        .to_owned(),
        name: prop.name.clone(),
    }
}

fn event_reference_target(
    component: &VueComponent,
    event: &VueComponentEvent,
) -> AdminSymbolTarget {
    AdminSymbolTarget {
        kind: AdminSymbolKind::ComponentEvent,
        owner: first_non_empty(&[
            &event.file_path,
            &component.definition_path,
            &component.file_path,
        ])
        .to_owned(),
        name: canonical_event_name(&event.name),
    }
}

/// Deduplicates the concatenated groups and sorts by URI and start.
fn merge_locations(groups: &[Vec<protocol::Location>]) -> Vec<protocol::Location> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for location in groups.iter().flatten() {
        if seen.insert(location) {
            result.push(location.clone());
        }
    }
    sort_locations(&mut result);
    result
}

fn usage_location(file_path: &str, occurrence: &AdminSourceRange) -> protocol::Location {
    protocol::Location {
        uri: swlsp_uriutil::file_uri(file_path),
        range: protocol::Range {
            start: protocol::Position {
                line: occurrence.start_line,
                character: occurrence.start_character,
            },
            end: protocol::Position {
                line: occurrence.end_line,
                character: occurrence.end_character,
            },
        },
    }
}

fn local_component_declaration(local: &VueLocalComponent) -> protocol::Location {
    let mut location = usage_location(&local.file_path, &local.name_range);
    if local.name_range.end_line == 0 && local.name_range.end_character == 0 && local.line > 1 {
        location.range.start.line = local.line - 1;
        location.range.end.line = local.line - 1;
    }
    location
}

struct DeclarationCollector<'a> {
    index: &'a AdminComponentIndexer,
    result: Vec<protocol::Location>,
}

impl DeclarationCollector<'_> {
    fn collect(&mut self, target: &AdminSymbolTarget) -> ProviderResult<()> {
        let index = self.index;
        match target.kind {
            AdminSymbolKind::Component => {
                for value in index.get_component(&target.name)? {
                    self.add(&value.file_path, value.line);
                }
            }
            AdminSymbolKind::Service => {
                for value in index.get_service(&target.name)? {
                    self.add(&value.file_path, value.line);
                }
            }
            AdminSymbolKind::Store => {
                for value in index.get_store(&target.name)? {
                    self.add(&value.file_path, value.line);
                }
            }
            AdminSymbolKind::StoreMember => {
                for value in index.get_store(&target.owner)? {
                    if let Some(member) = value.member(&target.name) {
                        self.add(&member.file_path, member.line);
                    }
                }
            }
            AdminSymbolKind::Privilege => {
                for value in index.get_privilege(&target.name)? {
                    self.add(&value.file_path, value.line);
                }
            }
            AdminSymbolKind::Mixin => {
                for value in index.get_mixin(&target.name)? {
                    self.add(&value.file_path, value.line);
                }
            }
            AdminSymbolKind::Directive if !target.owner.is_empty() => {
                for component in index.get_components_by_definition_path(&target.owner)? {
                    if let Some(local) = component.local_directive(&target.name) {
                        self.add(&local.file_path, local.line);
                    }
                }
            }
            AdminSymbolKind::Directive => {
                for value in index.get_directive(&target.name)? {
                    self.add(&value.file_path, value.line);
                }
            }
            AdminSymbolKind::Filter => {
                for value in index.get_filter(&target.name)? {
                    self.add(&value.file_path, value.line);
                }
            }
            AdminSymbolKind::CmsElement | AdminSymbolKind::CmsBlock => {
                let kind = if target.kind == AdminSymbolKind::CmsBlock {
                    AdminCMSRegistrationKind::Block
                } else {
                    AdminCMSRegistrationKind::Element
                };
                for value in index.get_cms_registration(kind, &target.name)? {
                    self.add(&value.file_path, value.line);
                }
            }
            AdminSymbolKind::Module => {
                for value in index.get_module(&target.name)? {
                    self.add(&value.file_path, value.line);
                }
            }
            AdminSymbolKind::ModuleRoute => {
                if let Some((module, route)) = index.get_module_route(&target.name)? {
                    self.add(&module.file_path, route.line);
                }
            }
            AdminSymbolKind::EventBusEvent => {
                if let Some(event) = index.resolve_shopware_event_bus_event(&target.name, "")?
                    && !event.definition_path.is_empty()
                {
                    if event.definition_range.declaration || event.definition_range.identifier {
                        self.result.push(usage_location(
                            &event.definition_path,
                            &event.definition_range,
                        ));
                    } else {
                        self.add(&event.definition_path, event.definition_line);
                    }
                }
            }
            AdminSymbolKind::ComponentProp
            | AdminSymbolKind::ComponentEvent
            | AdminSymbolKind::ComponentSlot => {
                for component in index.get_components_exposing_symbol(target)? {
                    self.add_component_contract(&component, target);
                }
            }
            _ => {}
        }
        Ok(())
    }

    fn add(&mut self, path: &str, line: u32) {
        if path.is_empty() {
            return;
        }
        self.result.push(line_location(path, i64::from(line)));
    }

    fn add_declaration(&mut self, path: &str, line: u32, name_range: &AdminSourceRange) {
        if !path.is_empty() && (name_range.declaration || name_range.identifier) {
            self.result.push(usage_location(path, name_range));
            return;
        }
        self.add(path, line);
    }

    fn add_component_contract(&mut self, component: &VueComponent, target: &AdminSymbolTarget) {
        match target.kind {
            AdminSymbolKind::ComponentProp => {
                let prop = component
                    .component_prop(&target.name)
                    .cloned()
                    .unwrap_or_default();
                self.add_declaration(&target.owner, prop.line, &prop.name_range);
            }
            AdminSymbolKind::ComponentEvent => {
                let event = component.component_event(&target.name).unwrap_or_default();
                self.add_declaration(&target.owner, event.line, &event.name_range);
            }
            AdminSymbolKind::ComponentSlot => {
                if let Some(slot) = component.component_slot(&target.name) {
                    self.add_declaration(&target.owner, slot.line, &slot.name_range);
                }
            }
            _ => {}
        }
    }
}
