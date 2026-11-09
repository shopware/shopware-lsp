//! Administration registry and component-contract rename.

use std::collections::{BTreeMap, HashSet};
use std::sync::Arc;

use swlsp_admin::{
    AdminComponentIndexer, AdminSymbolKind, AdminSymbolTarget, AdminUsageSet, TwigVueMember,
    canonical_event_name, normalize_prop_name,
};
use swlsp_lsp::{BoxError, Context, ProviderResult, RenameProvider, RenameRequest};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::TextRange;
use swlsp_uriutil::{file_uri, filepath};

use crate::admin_rename_helpers::{
    Request, admin_rename_replacement, has_admin_declaration_occurrence,
    has_admin_owned_source_occurrence, is_identifier, source_range,
};
use crate::protocol_range;

/// Renames Administration registry identities whose declarations and all
/// supported usages are represented by one source token. Derived
/// identities such as privilege roles and module route names remain
/// references-only.
pub struct AdminRenameProvider {
    pub(crate) index: Option<Arc<AdminComponentIndexer>>,
}

impl AdminRenameProvider {
    pub fn new(index: Option<Arc<AdminComponentIndexer>>) -> Self {
        Self { index }
    }
}

pub(crate) type Changes = BTreeMap<String, Vec<protocol::TextEdit>>;

struct Plan<'a> {
    index: &'a AdminComponentIndexer,
    request: &'a Request<'a>,
    target: AdminSymbolTarget,
    new_name: String,
    sets: Vec<AdminUsageSet>,
    dynamic_ranges: Vec<TextRange>,
    event_bus: TwigVueMember,
    changes: Changes,
    live_path: String,
}

impl RenameProvider for AdminRenameProvider {
    fn rename(
        &self,
        _ctx: &Context,
        request: &RenameRequest,
    ) -> ProviderResult<Option<protocol::WorkspaceEdit>> {
        let Some(index) = self.index.as_deref() else {
            return Ok(None);
        };
        let Some(request) = Request::new(request) else {
            return Ok(None);
        };
        if let Some(result) = self.rename_local_symbol(index, &request) {
            return result;
        }
        let Some((target, dynamic_ranges)) = self.rename_target(index, &request)? else {
            return Ok(None);
        };
        if target.kind == AdminSymbolKind::ComponentModel {
            return Err("cannot safely rename compound v-model contracts; rename the prop and update event declarations together".into());
        }
        if !supported_kind(target.kind) {
            return Ok(None);
        }
        let new_name = normalized_name(target.kind, &request.params.new_name);
        if new_name == target.name {
            return Ok(Some(protocol::WorkspaceEdit::default()));
        }
        self.validate_rename(index, &target, &new_name)?;
        reject_conflict(index, &target, &new_name)?;
        let mut plan = Plan {
            index,
            request: &request,
            target,
            new_name,
            sets: Vec::new(),
            dynamic_ranges,
            event_bus: TwigVueMember::default(),
            changes: Changes::new(),
            live_path: String::new(),
        };
        plan.prepare()?;
        if plan.sets.is_empty() && plan.event_bus.definition_path.is_empty() {
            return Ok(None);
        }
        plan.build()?;
        Ok(Some(protocol::WorkspaceEdit {
            changes: plan.changes,
            ..protocol::WorkspaceEdit::default()
        }))
    }
}

/// The result of a local-symbol handler: `None` when not handled.
pub(crate) type Handled = Option<ProviderResult<Option<protocol::WorkspaceEdit>>>;

impl AdminRenameProvider {
    fn rename_local_symbol(&self, index: &AdminComponentIndexer, request: &Request<'_>) -> Handled {
        self.rename_local_component_declaration(index, request)
            .or_else(|| self.rename_local_component_tag(index, request))
            .or_else(|| self.rename_scoped_slot_local(index, request))
            .or_else(|| crate::admin_rename_helpers::rename_vue_local(request))
    }

    fn rename_target(
        &self,
        index: &AdminComponentIndexer,
        request: &Request<'_>,
    ) -> Result<Option<(AdminSymbolTarget, Vec<TextRange>)>, BoxError> {
        if let Some(result) = self.dynamic_twig_slot_rename_target(index, request) {
            let (target, ranges) = result?;
            return Ok((!target.name.is_empty()).then_some((target, ranges)));
        }
        Ok(self
            .target_at(index, request)?
            .map(|target| (target, Vec::new())))
    }
}

fn supported_kind(kind: AdminSymbolKind) -> bool {
    matches!(
        kind,
        AdminSymbolKind::Component
            | AdminSymbolKind::Service
            | AdminSymbolKind::Store
            | AdminSymbolKind::Mixin
            | AdminSymbolKind::Directive
            | AdminSymbolKind::Filter
            | AdminSymbolKind::CmsElement
            | AdminSymbolKind::CmsBlock
            | AdminSymbolKind::EventBusEvent
            | AdminSymbolKind::ComponentProp
            | AdminSymbolKind::ComponentEvent
            | AdminSymbolKind::ComponentSlot
            | AdminSymbolKind::ComponentMember
    )
}

fn normalized_name(kind: AdminSymbolKind, value: &str) -> String {
    let value = value.trim();
    match kind {
        AdminSymbolKind::ComponentProp => normalize_prop_name(value),
        AdminSymbolKind::ComponentEvent => canonical_event_name(value),
        _ => value.to_owned(),
    }
}

impl Plan<'_> {
    fn prepare(&mut self) -> Result<(), BoxError> {
        self.sets = self.index.get_symbol_usages(&self.target)?;
        self.resolve_event_bus_declaration()?;
        self.reject_compound_model_usages()?;
        self.reject_unsafe_dynamic_usages()?;
        self.validate_declaration_source()?;
        self.validate_service_identifiers()
    }

    fn resolve_event_bus_declaration(&mut self) -> Result<(), BoxError> {
        if self.target.kind != AdminSymbolKind::EventBusEvent {
            return Ok(());
        }
        let declaration = self
            .index
            .resolve_shopware_event_bus_event(&self.target.name, "")?;
        match declaration {
            Some(declaration)
                if !declaration.definition_path.is_empty()
                    && (declaration.definition_range.declaration
                        || declaration.definition_range.identifier) =>
            {
                self.event_bus = declaration;
                Ok(())
            }
            _ => Err(format!(
                "cannot safely rename Shopware EventBus event {:?} because its typed declaration is not indexed",
                self.target.name
            )
            .into()),
        }
    }

    fn reject_compound_model_usages(&self) -> Result<(), BoxError> {
        if !matches!(
            self.target.kind,
            AdminSymbolKind::ComponentProp | AdminSymbolKind::ComponentEvent
        ) {
            return Ok(());
        }
        if self
            .sets
            .iter()
            .any(|set| set.kind == AdminSymbolKind::ComponentModel)
        {
            return Err(format!(
                "cannot safely rename Administration {} {:?} because it participates in a compound v-model contract",
                self.target.kind.as_str(),
                self.target.name
            )
            .into());
        }
        Ok(())
    }

    fn reject_unsafe_dynamic_usages(&self) -> Result<(), BoxError> {
        if !matches!(
            self.target.kind,
            AdminSymbolKind::ComponentProp
                | AdminSymbolKind::ComponentEvent
                | AdminSymbolKind::ComponentSlot
        ) {
            return Ok(());
        }
        for set in &self.sets {
            for occurrence in &set.occurrences {
                if !self
                    .index
                    .dynamic_component_usage_rename_safe(set, occurrence, &self.target)?
                {
                    return Err(format!(
                        "cannot safely rename Administration {} {:?} because a dynamic component usage resolves to distinct declarations",
                        self.target.kind.as_str(),
                        self.target.name
                    )
                    .into());
                }
            }
        }
        Ok(())
    }

    fn validate_declaration_source(&self) -> Result<(), BoxError> {
        let target = &self.target;
        if matches!(
            target.kind,
            AdminSymbolKind::ComponentProp
                | AdminSymbolKind::ComponentEvent
                | AdminSymbolKind::ComponentSlot
        ) && !has_admin_owned_source_occurrence(&self.sets, &target.owner)
        {
            return Err(format!(
                "cannot safely rename Administration {} {:?} because its declaration source is not indexed",
                target.kind.as_str(),
                target.name
            )
            .into());
        }
        let declared = has_admin_declaration_occurrence(&self.sets);
        if target.kind == AdminSymbolKind::ComponentMember && !declared {
            return Err(format!(
                "cannot safely rename Administration component member {:?} because its declaration source is not indexed",
                target.name
            )
            .into());
        }
        if target.kind == AdminSymbolKind::Directive && !target.owner.is_empty() && !declared {
            return Err(format!(
                "cannot safely rename component-local Administration directive {:?} because its declaration source is not indexed",
                target.name
            )
            .into());
        }
        if matches!(
            target.kind,
            AdminSymbolKind::CmsElement | AdminSymbolKind::CmsBlock
        ) && !declared
        {
            return Err(format!(
                "cannot safely rename Shopware CMS registry {:?} because its declaration source is not indexed",
                target.name
            )
            .into());
        }
        Ok(())
    }

    fn validate_service_identifiers(&self) -> Result<(), BoxError> {
        if self.target.kind != AdminSymbolKind::Service || is_identifier(&self.new_name) {
            return Ok(());
        }
        if self
            .sets
            .iter()
            .flat_map(|set| &set.occurrences)
            .any(|occurrence| occurrence.identifier)
        {
            return Err(format!(
                "{:?} is not a valid JavaScript identifier for injected service {}",
                self.new_name, self.target.name
            )
            .into());
        }
        Ok(())
    }

    fn build(&mut self) -> Result<(), BoxError> {
        self.add_event_bus_declaration();
        self.add_dynamic_slot_ranges();
        self.add_live_template_members()?;
        self.add_indexed_occurrences();
        self.normalize_edits();
        Ok(())
    }

    fn push(&mut self, uri: &str, range: protocol::Range, text: String) {
        self.changes
            .entry(uri.to_owned())
            .or_default()
            .push(protocol::TextEdit::new(range, text));
    }

    fn add_event_bus_declaration(&mut self) {
        if self.event_bus.definition_path.is_empty() {
            return;
        }
        let uri = file_uri(&self.event_bus.definition_path);
        let range = source_range(&self.event_bus.definition_range);
        self.push(&uri, range, self.new_name.clone());
    }

    fn add_dynamic_slot_ranges(&mut self) {
        let uri = self.request.params.text_document.uri.clone();
        for range in self.dynamic_ranges.clone() {
            let range = protocol_range(self.request.document.line_index(), range);
            self.push(&uri, range, self.new_name.clone());
        }
    }

    fn add_live_template_members(&mut self) -> Result<(), BoxError> {
        if self.target.kind != AdminSymbolKind::ComponentMember || !self.request.is_template() {
            return Ok(());
        }
        let path = swlsp_uriutil::path(&self.request.params.text_document.uri)?;
        self.live_path = path.clone();
        let root = self.request.root;
        let content = self.request.document.source();
        let uri = self.request.params.text_document.uri.clone();
        for identifier in swlsp_admin::twig_vue_expression_root_identifiers(root, content) {
            let candidate = self
                .index
                .twig_component_member_at(
                    &path,
                    root,
                    content,
                    u32::from(identifier.range.start()),
                )?
                .map(|(target, _)| target);
            if candidate.as_ref() != Some(&self.target) {
                continue;
            }
            let range = protocol_range(self.request.document.line_index(), identifier.range);
            self.push(&uri, range, self.new_name.clone());
        }
        Ok(())
    }

    fn add_indexed_occurrences(&mut self) {
        let live = (!self.live_path.is_empty()).then(|| filepath::clean(&self.live_path));
        let sets = std::mem::take(&mut self.sets);
        for set in &sets {
            if live.as_deref() == Some(filepath::clean(&set.file_path).as_str()) {
                continue;
            }
            let uri = file_uri(&set.file_path);
            for occurrence in &set.occurrences {
                let text = admin_rename_replacement(&self.target, &self.new_name, occurrence);
                self.push(&uri, source_range(occurrence), text);
            }
        }
        self.sets = sets;
    }

    fn normalize_edits(&mut self) {
        for edits in self.changes.values_mut() {
            let mut seen = HashSet::with_capacity(edits.len());
            edits.retain(|edit| {
                seen.insert((
                    edit.range.start.line,
                    edit.range.start.character,
                    edit.range.end.line,
                    edit.range.end.character,
                    edit.new_text.clone(),
                ))
            });
        }
        crate::admin_rename_helpers::sort_admin_text_edits(&mut self.changes);
    }
}

pub(crate) fn reject_conflict(
    index: &AdminComponentIndexer,
    target: &AdminSymbolTarget,
    new_name: &str,
) -> Result<(), BoxError> {
    let exists = match target.kind {
        AdminSymbolKind::Component => !index.get_component(new_name)?.is_empty(),
        AdminSymbolKind::Service => !index.get_service(new_name)?.is_empty(),
        AdminSymbolKind::Store => !index.get_store(new_name)?.is_empty(),
        AdminSymbolKind::Mixin => !index.get_mixin(new_name)?.is_empty(),
        AdminSymbolKind::Directive if target.owner.is_empty() => {
            !index.get_directive(new_name)?.is_empty()
        }
        AdminSymbolKind::Directive => index
            .get_components_by_definition_path(&target.owner)?
            .iter()
            .any(|component| {
                component
                    .local_directive(new_name)
                    .is_some_and(|local| !swlsp_textutil::eq_ignore_case(&local.name, &target.name))
            }),
        AdminSymbolKind::Filter => !index.get_filter(new_name)?.is_empty(),
        AdminSymbolKind::CmsElement | AdminSymbolKind::CmsBlock => {
            let kind = if target.kind == AdminSymbolKind::CmsBlock {
                swlsp_admin::AdminCMSRegistrationKind::Block
            } else {
                swlsp_admin::AdminCMSRegistrationKind::Element
            };
            !index.get_cms_registration(kind, new_name)?.is_empty()
        }
        AdminSymbolKind::EventBusEvent => index
            .resolve_shopware_event_bus_event(new_name, "")?
            .is_some(),
        AdminSymbolKind::ComponentProp
        | AdminSymbolKind::ComponentEvent
        | AdminSymbolKind::ComponentSlot => index
            .get_components_exposing_symbol(target)?
            .iter()
            .any(|component| match target.kind {
                AdminSymbolKind::ComponentProp => component.component_prop(new_name).is_some(),
                AdminSymbolKind::ComponentEvent => component.component_event(new_name).is_some(),
                _ => component.slots.iter().any(|slot| slot.name == new_name),
            }),
        AdminSymbolKind::ComponentMember => index
            .get_components_exposing_member(target)?
            .iter()
            .any(|component| {
                component
                    .template_member(new_name)
                    .is_some_and(|member| member.source_identity() != target.owner)
            }),
        _ => false,
    };
    if exists {
        return Err(format!(
            "administration {} {:?} already exists",
            target.kind.as_str(),
            new_name
        )
        .into());
    }
    Ok(())
}
