//! Administration call hierarchy.
//!
//! Exposes the native Administration call graph for source-owned component
//! methods and Pinia actions. The persisted usage graph narrows incoming-call
//! candidate files; their live/disk CSTs remain the authority for whether an
//! occurrence is actually invoked and who contains it.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_admin::{
    AdminComponentIndexer, AdminSourceRange, AdminStoreMemberKind, AdminSymbolKind,
    AdminSymbolTarget, VueComponentMember, VueComponentMemberKind, twig_vue_calls,
};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    CallHierarchyCallsRequest, CallHierarchyPrepareRequest, CallHierarchyProvider, Context,
    ProviderResult, TextDocument, effective_syntax_language, protocol_range,
};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::SymbolKind;
use swlsp_parser_js::{query as jsquery, syntax as js};
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_uriutil::{file_uri, filepath};

#[cfg(test)]
mod tests;

const DOMAIN_COMPONENT_MEMBER: &str = "component-member";
const DOMAIN_STORE_ACTION: &str = "store-action";
const DOMAIN_TEMPLATE: &str = "template";
const DOMAIN_FILE: &str = "file";

/// The item payload round-tripped through the client.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
struct CallData {
    #[serde(default)]
    domain: String,
    #[serde(default, skip_serializing_if = "kind_unset")]
    kind: AdminSymbolKind,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    owner: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    name: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    path: String,
}

fn kind_unset(kind: &AdminSymbolKind) -> bool {
    kind.is_unset()
}

impl CallData {
    fn target(&self) -> AdminSymbolTarget {
        AdminSymbolTarget {
            kind: self.kind,
            owner: self.owner.clone(),
            name: self.name.clone(),
        }
    }

    fn value(&self) -> Option<serde_json::Value> {
        serde_json::to_value(self).ok()
    }

    fn decode(value: Option<&serde_json::Value>) -> Option<Self> {
        let data: Self = serde_json::from_value(value?.clone()).ok()?;
        (!data.domain.is_empty()).then_some(data)
    }
}

struct ResolvedCall {
    target: CallData,
    target_item: protocol::CallHierarchyItem,
    caller: CallData,
    caller_item: protocol::CallHierarchyItem,
    range: protocol::Range,
}

#[derive(Clone)]
struct ResolvedCallable {
    data: CallData,
    item: Option<protocol::CallHierarchyItem>,
}

type CallableCache = HashMap<AdminSymbolTarget, ResolvedCallable>;

pub struct AdminCallHierarchyProvider {
    index: Arc<AdminComponentIndexer>,
}

impl AdminCallHierarchyProvider {
    pub fn new(index: Arc<AdminComponentIndexer>) -> Self {
        Self { index }
    }
}

impl CallHierarchyProvider for AdminCallHierarchyProvider {
    fn prepare_call_hierarchy(
        &self,
        ctx: &Context,
        request: &CallHierarchyPrepareRequest,
    ) -> ProviderResult<Vec<protocol::CallHierarchyItem>> {
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        let (Some(document), Some(root), Some(node)) = (
            request.document.as_deref(),
            request.root.as_ref(),
            request.node.as_ref(),
        ) else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        let lines = document.line_index();
        let mut targets = Vec::new();
        match effective_syntax_language(document.language, Some(node)) {
            Some(LanguageId::JavaScript) => {
                if let Some(target) = self.index.javascript_symbol_at(&path, node)? {
                    targets.push(target);
                }
                if targets.is_empty() {
                    targets.extend(self.store_actions_at_javascript_node(&path, node, lines)?);
                }
            }
            Some(LanguageId::Twig) => {
                let position = request.params.position;
                let offset = lines.offset_utf16(position.line, position.character);
                if let Some((target, _)) =
                    self.index
                        .twig_component_member_at(&path, root, document.source(), offset)?
                {
                    targets.push(target);
                }
            }
            _ => return Ok(Vec::new()),
        }
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for target in targets {
            if !seen.insert(target.clone()) {
                continue;
            }
            let (data, item) = self.callable_item(&target)?;
            if let Some(mut item) = item {
                item.data = data.value();
                result.push(item);
            }
        }
        Ok(result)
    }

    fn incoming_calls(
        &self,
        ctx: &Context,
        request: &CallHierarchyCallsRequest,
    ) -> ProviderResult<Vec<protocol::CallHierarchyIncomingCall>> {
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        let Some(data) = CallData::decode(request.item.data.as_ref()) else {
            return Ok(Vec::new());
        };
        if data.domain != DOMAIN_COMPONENT_MEMBER && data.domain != DOMAIN_STORE_ACTION {
            return Ok(Vec::new());
        }
        if self.callable_item(&data.target())?.1.is_none() {
            return Ok(Vec::new());
        }
        let documents =
            self.incoming_candidate_documents(ctx, &data.target(), &request.documents)?;
        let mut groups: BTreeMap<String, (protocol::CallHierarchyItem, Vec<protocol::Range>)> =
            BTreeMap::new();
        let mut callables = CallableCache::new();
        for document in &documents {
            ctx.check()?;
            for call in self.resolved_calls(document, document.language, &mut callables)? {
                if !same_target(&call.target, &data) {
                    continue;
                }
                groups
                    .entry(item_key(&call.caller_item))
                    .or_insert_with(|| (call.caller_item, Vec::new()))
                    .1
                    .push(call.range);
            }
        }
        Ok(groups
            .into_values()
            .map(|(from, ranges)| protocol::CallHierarchyIncomingCall {
                from,
                from_ranges: unique_ranges(ranges),
            })
            .collect())
    }

    fn outgoing_calls(
        &self,
        ctx: &Context,
        request: &CallHierarchyCallsRequest,
    ) -> ProviderResult<Vec<protocol::CallHierarchyOutgoingCall>> {
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        let Some(data) = CallData::decode(request.item.data.as_ref()) else {
            return Ok(Vec::new());
        };
        if data.path.is_empty() {
            return Ok(Vec::new());
        }
        let Some(document) = load_document(&data.path, &request.documents)? else {
            return Ok(Vec::new());
        };
        let calls = self.resolved_calls(&document, document.language, &mut CallableCache::new())?;
        let mut groups: BTreeMap<String, (protocol::CallHierarchyItem, Vec<protocol::Range>)> =
            BTreeMap::new();
        for call in calls {
            if !same_caller(&call.caller, &data) {
                continue;
            }
            groups
                .entry(item_key(&call.target_item))
                .or_insert_with(|| (call.target_item, Vec::new()))
                .1
                .push(call.range);
        }
        Ok(groups
            .into_values()
            .map(|(to, ranges)| protocol::CallHierarchyOutgoingCall {
                to,
                from_ranges: unique_ranges(ranges),
            })
            .collect())
    }
}

impl AdminCallHierarchyProvider {
    /// Every statically resolved call of `document` read as `language`; a
    /// Vue document contributes both its script and template calls.
    fn resolved_calls(
        &self,
        document: &TextDocument,
        language: Option<LanguageId>,
        callables: &mut CallableCache,
    ) -> ProviderResult<Vec<ResolvedCall>> {
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if language == Some(LanguageId::Vue) {
            let mut result =
                self.resolved_calls(document, Some(LanguageId::JavaScript), callables)?;
            result.extend(self.resolved_calls(document, Some(LanguageId::Twig), callables)?);
            return Ok(result);
        }
        let path = swlsp_uriutil::path(&document.uri)?;
        let lines = document.line_index();
        let mut result = Vec::new();
        match language {
            Some(LanguageId::JavaScript) => {
                for call in jsquery::iter_calls(&root, &[]) {
                    let Some(callee) = jsquery::call_callee(&call) else {
                        continue;
                    };
                    let Some(target) = self.index.javascript_symbol_at(&path, &callee)? else {
                        continue;
                    };
                    let callable = self.cached_callable_item(&target, callables)?;
                    let Some(target_item) = callable.item else {
                        continue;
                    };
                    let Some(name_node) = jsquery::member_name_node(&callee) else {
                        continue;
                    };
                    let (caller, caller_item) = self.javascript_caller(document, &path, &call)?;
                    result.push(ResolvedCall {
                        target: callable.data,
                        target_item,
                        caller,
                        caller_item,
                        range: protocol_range(lines, name_node.range_trimmed_trivia()),
                    });
                }
            }
            Some(LanguageId::Twig) => {
                let (caller, caller_item) = self.template_caller(document, &path)?;
                for site in twig_vue_calls(&root, document.source()) {
                    if site.call.filter {
                        continue;
                    }
                    let Some((target, _)) = self.index.twig_component_member_at(
                        &path,
                        &root,
                        document.source(),
                        site.call.name_range.start().into(),
                    )?
                    else {
                        continue;
                    };
                    let callable = self.cached_callable_item(&target, callables)?;
                    let Some(target_item) = callable.item else {
                        continue;
                    };
                    result.push(ResolvedCall {
                        target: callable.data,
                        target_item,
                        caller: caller.clone(),
                        caller_item: caller_item.clone(),
                        range: protocol_range(lines, site.call.name_range),
                    });
                }
            }
            _ => {}
        }
        Ok(result)
    }

    fn cached_callable_item(
        &self,
        target: &AdminSymbolTarget,
        cache: &mut CallableCache,
    ) -> ProviderResult<ResolvedCallable> {
        if let Some(cached) = cache.get(target) {
            return Ok(cached.clone());
        }
        let (data, item) = self.callable_item(target)?;
        let resolved = ResolvedCallable { data, item };
        cache.insert(target.clone(), resolved.clone());
        Ok(resolved)
    }

    /// The component method, computed value or store action containing
    /// `call`, else the file itself.
    fn javascript_caller(
        &self,
        document: &TextDocument,
        path: &str,
        call: &SyntaxNode,
    ) -> ProviderResult<(CallData, protocol::CallHierarchyItem)> {
        let lines = document.line_index();
        for current in call.ancestors().skip(1) {
            if !matches!(current.kind(), js::JS_METHOD | js::JS_PROPERTY) {
                continue;
            }
            let (Some(name_node), Some(name)) = (
                jsquery::property_name_node(&current),
                jsquery::property_name(&current).filter(|name| !name.is_empty()),
            ) else {
                continue;
            };
            let (line, character) =
                lines.position_utf16(name_node.range_trimmed_trivia().start().into());
            if let Some(member) = self
                .index
                .get_component_member_at_definition_position(path, line, character)?
                && matches!(
                    member.kind,
                    VueComponentMemberKind::Method | VueComponentMemberKind::Computed
                )
            {
                let data = component_member_call_data(&member);
                if let Some(item) = self.item_for_data(&data, Some(document))? {
                    return Ok((data, item));
                }
            }
            let actions = self
                .index
                .store_action_targets_at_definition_position(path, &name, line)?;
            if let [action] = actions.as_slice() {
                let (data, item) = self.callable_item(action)?;
                if let Some(item) = item {
                    let live = self.item_for_data(&data, Some(document))?;
                    return Ok((data, live.unwrap_or(item)));
                }
            }
        }
        let data = CallData {
            domain: DOMAIN_FILE.to_owned(),
            path: path.to_owned(),
            ..CallData::default()
        };
        let item = self
            .item_for_data(&data, Some(document))?
            .unwrap_or_else(empty_item);
        Ok((data, item))
    }

    fn template_caller(
        &self,
        document: &TextDocument,
        path: &str,
    ) -> ProviderResult<(CallData, protocol::CallHierarchyItem)> {
        let data = CallData {
            domain: DOMAIN_TEMPLATE.to_owned(),
            path: path.to_owned(),
            ..CallData::default()
        };
        let item = self
            .item_for_data(&data, Some(document))?
            .unwrap_or_else(empty_item);
        Ok((data, item))
    }

    /// The call data and item of a callable target: a component method or a
    /// store action. Other targets have no item.
    fn callable_item(
        &self,
        target: &AdminSymbolTarget,
    ) -> ProviderResult<(CallData, Option<protocol::CallHierarchyItem>)> {
        let data = match target.kind {
            AdminSymbolKind::ComponentMember => {
                match self.index.resolve_component_member_target(target)? {
                    Some(member) if member.kind == VueComponentMemberKind::Method => {
                        component_member_call_data(&member)
                    }
                    _ => return Ok((CallData::default(), None)),
                }
            }
            AdminSymbolKind::StoreMember => {
                let stores = self.index.get_store(&target.owner)?;
                let member = stores.iter().find_map(|store| {
                    store
                        .member(&target.name)
                        .filter(|member| member.kind == AdminStoreMemberKind::Action)
                });
                let Some(member) = member else {
                    return Ok((CallData::default(), None));
                };
                CallData {
                    domain: DOMAIN_STORE_ACTION.to_owned(),
                    kind: target.kind,
                    owner: target.owner.clone(),
                    name: target.name.clone(),
                    path: member.file_path.clone(),
                }
            }
            _ => return Ok((CallData::default(), None)),
        };
        let item = self.item_for_data(&data, None)?;
        Ok((data, item))
    }

    fn item_for_data(
        &self,
        data: &CallData,
        document: Option<&TextDocument>,
    ) -> ProviderResult<Option<protocol::CallHierarchyItem>> {
        match data.domain.as_str() {
            DOMAIN_COMPONENT_MEMBER => {
                let Some(member) = self.index.resolve_component_member_target(&data.target())?
                else {
                    return Ok(None);
                };
                let kind = if member.kind == VueComponentMemberKind::Computed {
                    SymbolKind::PROPERTY
                } else {
                    SymbolKind::METHOD
                };
                let selection = source_range(&member.name_range);
                let mut names: Vec<String> = self
                    .index
                    .get_components_exposing_member(&data.target())?
                    .into_iter()
                    .map(|component| component.name)
                    .collect();
                names.sort();
                let mut detail = format!("Administration component {}", member.kind.as_str());
                if !names.is_empty() {
                    detail.push_str(" · ");
                    detail.push_str(&names.join(", "));
                }
                Ok(Some(protocol::CallHierarchyItem {
                    name: member.name.clone(),
                    kind,
                    tags: Vec::new(),
                    detail,
                    uri: file_uri(&member.file_path),
                    range: selection,
                    selection_range: selection,
                    data: data.value(),
                }))
            }
            DOMAIN_STORE_ACTION => {
                for store in self.index.get_store(&data.owner)? {
                    let Some(member) = store
                        .member(&data.name)
                        .filter(|member| member.kind == AdminStoreMemberKind::Action)
                    else {
                        continue;
                    };
                    let line = member.line.saturating_sub(1);
                    let mut selection = protocol::Range {
                        start: protocol::Position { line, character: 0 },
                        end: protocol::Position { line, character: 0 },
                    };
                    let loaded;
                    let mut document = document;
                    if document.is_none_or(|document| {
                        normalize_path(&member.file_path) != normalize_document_path(document)
                    }) {
                        loaded = load_document(&member.file_path, &[])?;
                        document = loaded.as_deref();
                    }
                    if let Some(exact) = name_range(document, &member.name, line) {
                        selection = exact;
                    }
                    return Ok(Some(protocol::CallHierarchyItem {
                        name: member.name.clone(),
                        kind: SymbolKind::METHOD,
                        tags: Vec::new(),
                        detail: format!("Administration store action · {}", store.name),
                        uri: file_uri(&member.file_path),
                        range: selection,
                        selection_range: selection,
                        data: data.value(),
                    }));
                }
                Ok(None)
            }
            DOMAIN_TEMPLATE | DOMAIN_FILE => {
                let loaded;
                let document = match document {
                    Some(document) => document,
                    None => {
                        loaded = load_document(&data.path, &[])?;
                        match loaded.as_deref() {
                            Some(document) => document,
                            None => return Ok(None),
                        }
                    }
                };
                let (end_line, end_character) = document
                    .line_index()
                    .position_utf16(document.source().len() as u32);
                let mut name = swlsp_uriutil::filepath::base(&data.path);
                let mut detail = "Administration JavaScript file";
                if data.domain == DOMAIN_TEMPLATE {
                    detail = "Administration component template";
                    if let Some(component) =
                        self.index.get_component_by_template_path(&data.path)?
                    {
                        name = format!("{} template", component.name);
                    }
                }
                Ok(Some(protocol::CallHierarchyItem {
                    name,
                    kind: SymbolKind::FILE,
                    tags: Vec::new(),
                    detail: detail.to_owned(),
                    uri: document.uri.clone(),
                    range: protocol::Range {
                        start: protocol::Position::default(),
                        end: protocol::Position {
                            line: end_line,
                            character: end_character,
                        },
                    },
                    selection_range: protocol::Range::default(),
                    data: data.value(),
                }))
            }
            _ => Ok(None),
        }
    }

    fn store_actions_at_javascript_node(
        &self,
        path: &str,
        node: &SyntaxNode,
        lines: &LineIndex,
    ) -> ProviderResult<Vec<AdminSymbolTarget>> {
        for current in node.ancestors() {
            if !matches!(current.kind(), js::JS_METHOD | js::JS_PROPERTY) {
                continue;
            }
            let (Some(name_node), Some(name)) = (
                jsquery::property_name_node(&current),
                jsquery::property_name(&current).filter(|name| !name.is_empty()),
            ) else {
                continue;
            };
            let (line, _) = lines.position_utf16(name_node.range_trimmed_trivia().start().into());
            return Ok(self
                .index
                .store_action_targets_at_definition_position(path, &name, line)?);
        }
        Ok(Vec::new())
    }

    /// Files that may call `target`: persisted usage files plus every open
    /// Administration-language document, live snapshots preferred.
    fn incoming_candidate_documents(
        &self,
        ctx: &Context,
        target: &AdminSymbolTarget,
        open: &[Arc<TextDocument>],
    ) -> ProviderResult<Vec<Arc<TextDocument>>> {
        let mut paths: BTreeMap<String, String> = BTreeMap::new();
        for set in self.index.get_symbol_usages(target)? {
            paths.insert(normalize_path(&set.file_path), set.file_path);
        }
        let mut open_by_path: HashMap<String, &Arc<TextDocument>> = HashMap::new();
        for document in open {
            let Ok(path) = swlsp_uriutil::path(&document.uri) else {
                continue;
            };
            let normalized = normalize_path(&path);
            open_by_path.insert(normalized.clone(), document);
            if matches!(
                document.language,
                Some(LanguageId::JavaScript | LanguageId::Twig | LanguageId::Vue)
            ) {
                paths.insert(normalized, path);
            }
        }
        let mut result = Vec::with_capacity(paths.len());
        for (normalized, path) in &paths {
            ctx.check()?;
            if let Some(document) = open_by_path.get(normalized) {
                result.push(Arc::clone(document));
                continue;
            }
            if let Some(document) = load_document(path, &[])? {
                result.push(document);
            }
        }
        Ok(result)
    }
}

fn component_member_call_data(member: &VueComponentMember) -> CallData {
    CallData {
        domain: DOMAIN_COMPONENT_MEMBER.to_owned(),
        kind: AdminSymbolKind::ComponentMember,
        owner: member.source_identity(),
        name: member.name.clone(),
        path: member.file_path.clone(),
    }
}

/// The open document for `path`, else the file read from disk; `None` when
/// the file does not exist.
fn load_document(
    path: &str,
    open: &[Arc<TextDocument>],
) -> ProviderResult<Option<Arc<TextDocument>>> {
    let normalized = normalize_path(path);
    for document in open {
        if swlsp_uriutil::path(&document.uri)
            .is_ok_and(|candidate| normalize_path(&candidate) == normalized)
        {
            return Ok(Some(Arc::clone(document)));
        }
    }
    match std::fs::read(path) {
        Ok(source) => Ok(Some(Arc::new(TextDocument::new(
            file_uri(path),
            &String::from_utf8_lossy(&source),
            0,
        )))),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(error.into()),
    }
}

/// The item of a file or template caller that has no resolvable item.
fn empty_item() -> protocol::CallHierarchyItem {
    protocol::CallHierarchyItem {
        name: String::new(),
        kind: SymbolKind::FILE,
        tags: Vec::new(),
        detail: String::new(),
        uri: String::new(),
        range: protocol::Range::default(),
        selection_range: protocol::Range::default(),
        data: None,
    }
}

fn source_range(value: &AdminSourceRange) -> protocol::Range {
    protocol::Range {
        start: protocol::Position {
            line: value.start_line,
            character: value.start_character,
        },
        end: protocol::Position {
            line: value.end_line,
            character: value.end_character,
        },
    }
}

fn same_target(left: &CallData, right: &CallData) -> bool {
    left.kind == right.kind && left.owner == right.owner && left.name == right.name
}

fn same_caller(left: &CallData, right: &CallData) -> bool {
    if left.domain != right.domain || normalize_path(&left.path) != normalize_path(&right.path) {
        return false;
    }
    if left.domain == DOMAIN_COMPONENT_MEMBER || left.domain == DOMAIN_STORE_ACTION {
        return same_target(left, right);
    }
    true
}

fn normalize_path(value: &str) -> String {
    if value.is_empty() {
        return String::new();
    }
    filepath::clean(value)
}

fn normalize_document_path(document: &TextDocument) -> String {
    swlsp_uriutil::path(&document.uri).map_or_else(|_| String::new(), |path| normalize_path(&path))
}

/// The exact range of the identifier `name` on `line` of `document`.
fn name_range(document: Option<&TextDocument>, name: &str, line: u32) -> Option<protocol::Range> {
    let document = document?;
    if name.is_empty() {
        return None;
    }
    let lines = document.line_index();
    let start = lines.offset_utf16(line, 0) as usize;
    let end = lines.line_end(line) as usize;
    let source = document.source().as_bytes();
    if start > end || end > source.len() {
        return None;
    }
    let line_source = &source[start..end];
    let needle = name.as_bytes();
    let mut search_from = 0;
    loop {
        let index = search_from
            + line_source[search_from..]
                .windows(needle.len())
                .position(|window| window == needle)?;
        let before_ok = index == 0 || !identifier_byte(line_source[index - 1]);
        let after = index + needle.len();
        let after_ok = after == line_source.len() || !identifier_byte(line_source[after]);
        if before_ok && after_ok {
            return Some(protocol_range(
                lines,
                TextRange::new(
                    TextSize::from((start + index) as u32),
                    TextSize::from((start + after) as u32),
                ),
            ));
        }
        search_from = after;
        if search_from >= line_source.len() {
            return None;
        }
    }
}

fn identifier_byte(value: u8) -> bool {
    value == b'_' || value == b'$' || value.is_ascii_alphanumeric()
}

fn item_key(item: &protocol::CallHierarchyItem) -> String {
    format!(
        "{}\x00{}\x00{}:{}",
        item.uri, item.name, item.selection_range.start.line, item.selection_range.start.character
    )
}

fn unique_ranges(values: Vec<protocol::Range>) -> Vec<protocol::Range> {
    let mut seen = HashSet::new();
    let mut result: Vec<protocol::Range> = values
        .into_iter()
        .filter(|value| seen.insert(*value))
        .collect();
    result.sort_by_key(|range| (range.start.line, range.start.character));
    result
}
