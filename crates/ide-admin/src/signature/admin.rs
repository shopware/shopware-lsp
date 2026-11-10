//! Administration signature help.
//!
//! Exposes callable types already retained by the Administration component,
//! structural-type, Vue builtin, and Pinia indexes. It deliberately resolves
//! only statically named calls whose owning scope is known; arbitrary
//! JavaScript receivers continue to belong to a JS/TS language service.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_admin::{
    AdminComponentIndexer, AdminStoreMemberKind, ResolvedTwigScopedSlot, TwigVueCall,
};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    Context, ProviderResult, SignatureHelpProvider, SignatureHelpRequest, SyntaxContext,
    TextDocument, effective_syntax_language,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{JS_ARGUMENT_LIST, JS_CALL_EXPRESSION, TK_COMMA};
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextSize};
use swlsp_uriutil::filepath;

use swlsp_ide_twig::signature::twig_macro::markdown;

pub struct AdminSignatureProvider {
    index: Arc<AdminComponentIndexer>,
}

#[derive(Clone, Debug)]
struct AdminCallable {
    name: String,
    type_: String,
    detail: String,
}

impl AdminCallable {
    fn new(name: impl Into<String>, type_: impl Into<String>, detail: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            type_: type_.into(),
            detail: detail.into(),
        }
    }
}

impl AdminSignatureProvider {
    pub fn new(index: Arc<AdminComponentIndexer>) -> Self {
        Self { index }
    }

    /// Resolves the same Administration signatures used by interactive
    /// signature help for another document-oriented LSP feature. `node` may
    /// identify a JavaScript call directly; Twig callers can pass `None` and
    /// let the live document select the expression at `offset`.
    pub fn get_signature_help_at_offset(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        node: Option<SyntaxNode>,
        offset: u32,
    ) -> ProviderResult<Option<protocol::SignatureHelp>> {
        let Some(root) = document.root() else {
            return Ok(None);
        };
        if offset as usize > document.source().len() {
            return Ok(None);
        }
        let node = node.or_else(|| {
            root.node_at(TextSize::from(offset)).or_else(|| {
                (offset > 0)
                    .then(|| root.node_at(TextSize::from(offset - 1)))
                    .flatten()
            })
        });
        let (line, character) = document.line_index().position_utf16(offset);
        let mut params = protocol::SignatureHelpParams::default();
        params.text_document.uri = document.uri.clone();
        params.position = protocol::Position { line, character };
        self.get_signature_help(
            ctx,
            &SignatureHelpRequest {
                params,
                syntax: SyntaxContext {
                    document: Some(document.clone()),
                    language: document.language,
                    root: Some(root),
                    token: None,
                    node,
                },
            },
        )
    }

    fn javascript_help(
        &self,
        path: &str,
        node: &SyntaxNode,
        offset: u32,
    ) -> ProviderResult<Option<protocol::SignatureHelp>> {
        let Some(call) = jsquery::call_at(node) else {
            return Ok(None);
        };
        let callables = self.javascript_callables(path, &call)?;
        Ok(admin_signature_help(
            callables,
            admin_active_argument(
                direct_admin_child(&call, JS_ARGUMENT_LIST).as_ref(),
                TK_COMMA,
                offset,
            ),
        ))
    }

    fn twig_help(
        &self,
        path: &str,
        request: &SignatureHelpRequest,
        root: &SyntaxNode,
        offset: u32,
    ) -> ProviderResult<Option<protocol::SignatureHelp>> {
        let Some(call) = swlsp_admin::twig_vue_call_at_offset(root, request.source(), offset)
        else {
            return Ok(None);
        };
        if call.filter {
            return Ok(None);
        }
        let callables = self.twig_callables(path, request, root, &call)?;
        Ok(admin_signature_help(callables, call.active_argument as i32))
    }

    fn javascript_callables(
        &self,
        path: &str,
        call: &SyntaxNode,
    ) -> ProviderResult<Vec<AdminCallable>> {
        let Some(callee) = admin_javascript_call_callee(call) else {
            return Ok(Vec::new());
        };
        if let Some(callable) = self.shopware_event_bus_callable(path, call)? {
            return Ok(vec![callable]);
        }
        if let Some(name) = jsquery::this_member(&callee) {
            let components = self.index.get_components_by_definition_path(path)?;
            let mut result = Vec::new();
            for component in components {
                let Some(member) = component.template_member(&name) else {
                    continue;
                };
                result.push(AdminCallable::new(
                    name.clone(),
                    member.type_,
                    format!("Administration component `{}`", component.name),
                ));
            }
            return Ok(result);
        }
        if let Some((receiver, member_name)) =
            swlsp_admin::javascript_shopware_utils_member(&callee)
            && !member_name.is_empty()
        {
            let shape = self
                .index
                .resolve_shopware_utils(&receiver.join("."), path)?;
            let Some(member) = shape
                .members
                .into_iter()
                .find(|candidate| candidate.name == member_name)
            else {
                return Ok(Vec::new());
            };
            let mut segments = receiver;
            segments.push(member_name.clone());
            return Ok(vec![AdminCallable::new(
                member_name,
                member.type_,
                format!("Shopware utility `Shopware.Utils.{}`", segments.join(".")),
            )]);
        }
        if let Some(filter_name) = swlsp_admin::javascript_filter_name_for_callee(&callee) {
            let filters = self.index.get_filter(&filter_name)?;
            return Ok(filters
                .into_iter()
                .filter(|filter| !filter.signature.is_empty())
                .map(|filter| {
                    let detail = format!("Administration filter `{}`", filter.name);
                    AdminCallable::new(filter.name, filter.signature, detail)
                })
                .collect());
        }
        let Some(store_member) = jsquery::store_member(&callee) else {
            return Ok(Vec::new());
        };
        if store_member.member.is_empty() {
            return Ok(Vec::new());
        }
        let stores = self.index.get_store(&store_member.store)?;
        let mut result = Vec::new();
        for store in stores {
            let Some(member) = store.member(&store_member.member) else {
                continue;
            };
            if member.kind != AdminStoreMemberKind::Action {
                continue;
            }
            result.push(AdminCallable::new(
                store_member.member.clone(),
                member.type_.clone(),
                format!("Administration store `{}`", store.name),
            ));
        }
        Ok(result)
    }

    fn shopware_event_bus_callable(
        &self,
        path: &str,
        call: &SyntaxNode,
    ) -> ProviderResult<Option<AdminCallable>> {
        let Some(event_node) = jsquery::string_argument(call, 0) else {
            return Ok(None);
        };
        let Some((operation, event_name)) =
            swlsp_admin::javascript_shopware_event_bus_event_at(&event_node)
        else {
            return Ok(None);
        };
        if event_name.is_empty() {
            return Ok(None);
        }
        let Some(event) = self
            .index
            .resolve_shopware_event_bus_event(&event_name, path)?
        else {
            return Ok(None);
        };
        let mut payload_type = event.type_.trim().to_string();
        if payload_type.is_empty() {
            payload_type = "unknown".to_string();
        }
        let event_type = format!("{event_name:?}");
        let callable_type = match operation.as_str() {
            "emit" => {
                let optional = if swlsp_admin::vue_type_allows_undefined(&payload_type) {
                    "?"
                } else {
                    ""
                };
                format!("(event: {event_type}, payload{optional}: {payload_type}) => void")
            }
            "on" => {
                format!("(event: {event_type}, handler: (payload: {payload_type}) => void) => void")
            }
            "off" => format!(
                "(event: {event_type}, handler?: (payload: {payload_type}) => void) => void"
            ),
            _ => return Ok(None),
        };
        let detail = format!("Shopware EventBus event `{event_name}`\n\nPayload: `{payload_type}`");
        Ok(Some(AdminCallable::new(operation, callable_type, detail)))
    }

    fn twig_callables(
        &self,
        path: &str,
        request: &SignatureHelpRequest,
        root: &SyntaxNode,
        call: &TwigVueCall,
    ) -> ProviderResult<Vec<AdminCallable>> {
        let source = request.source();
        let callee_offset = u32::from(call.name_range.end()).wrapping_sub(1);
        let callee_node = root.node_at(TextSize::from(callee_offset));
        let live_component = self.index.get_component_for_document(
            path,
            Some(root),
            source,
            request.line_index(),
        )?;
        let owner = live_component.as_ref();

        let resolved_slot = match &callee_node {
            Some(node) => self.index.resolve_twig_scoped_slot_member_for_owner(
                root,
                node,
                source,
                callee_offset,
                path,
                owner,
            )?,
            None => None,
        };
        if let Some(slot) = resolved_slot {
            if !slot.member_found {
                return Ok(Vec::new());
            }
            let callables = scoped_slot_callables(&slot.scoped, &slot.access.member);
            if !callables.is_empty() {
                return Ok(callables);
            }
            return Ok(vec![AdminCallable::new(
                slot.member.name.clone(),
                slot.member.type_.clone(),
                format!("slot `{}`", slot.scoped.qualified_name()),
            )]);
        }
        if let Some(lexical) = self.index.resolve_twig_vue_member_for_component(
            root,
            source,
            callee_offset,
            path,
            owner,
        )? {
            if !lexical.member_found {
                return Ok(Vec::new());
            }
            return Ok(vec![AdminCallable::new(
                lexical.member.name,
                lexical.member.type_,
                format!("typed Vue binding `{}`", lexical.binding.name),
            )]);
        }
        if let Some(instance) = self.index.resolve_twig_vue_instance_member_for_component(
            root,
            source,
            callee_offset,
            path,
            owner,
        )? {
            if !instance.member_found {
                return Ok(Vec::new());
            }
            return Ok(vec![AdminCallable::new(
                instance.member.name,
                instance.member.type_,
                format!("Administration component `{}`", instance.component.name),
            )]);
        }

        let Some((name, root_range)) =
            swlsp_admin::twig_vue_expression_root_identifier_at_offset(root, source, callee_offset)
        else {
            return Ok(Vec::new());
        };
        let root_start = u32::from(root_range.start());
        if let Some(binding) = self
            .index
            .resolve_twig_vue_binding_for_component(root, source, root_start, path, owner)?
        {
            return Ok(vec![AdminCallable::new(
                name,
                binding.type_,
                "Vue template binding",
            )]);
        }
        if let Some(node) = &callee_node
            && let Some(binding) = self.index.resolve_twig_scoped_slot_binding_for_owner(
                root, node, source, root_start, path, owner,
            )?
        {
            let callables = scoped_slot_callables(&binding.scoped, &binding.binding.member_name);
            if !callables.is_empty() {
                return Ok(callables);
            }
            return Ok(vec![AdminCallable::new(
                name,
                binding.member.type_.clone(),
                format!("slot `{}`", binding.scoped.qualified_name()),
            )]);
        }
        if let Some(component) = self.index.get_component_by_template_path(path)?
            && let Some(member) = component.template_member(&name)
        {
            return Ok(vec![AdminCallable::new(
                name,
                member.type_,
                format!("Administration component `{}`", component.name),
            )]);
        }
        if let Some(member) = swlsp_admin::vue_builtin_member(&name) {
            return Ok(vec![AdminCallable::new(
                name,
                member.type_,
                "Vue instance builtin",
            )]);
        }
        Ok(Vec::new())
    }
}

impl SignatureHelpProvider for AdminSignatureProvider {
    fn get_signature_help(
        &self,
        _ctx: &Context,
        request: &SignatureHelpRequest,
    ) -> ProviderResult<Option<protocol::SignatureHelp>> {
        let (Some(node), Some(root), Some(lines)) = (
            request.node.as_ref(),
            request.root.as_ref(),
            request.line_index(),
        ) else {
            return Ok(None);
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        let position = request.params.position;
        let offset = lines.offset_utf16(position.line, position.character);
        match filepath::ext(&path).to_lowercase().as_str() {
            ".js" | ".ts" => self.javascript_help(&path, node, offset),
            ".twig" => {
                if !filepath::to_slash(&path).contains("/Resources/app/administration/") {
                    return Ok(None);
                }
                self.twig_help(&path, request, root, offset)
            }
            ".vue" => match effective_syntax_language(Some(LanguageId::Vue), Some(node)) {
                Some(LanguageId::JavaScript) => self.javascript_help(&path, node, offset),
                Some(LanguageId::Twig) => self.twig_help(&path, request, root, offset),
                _ => Ok(None),
            },
            _ => Ok(None),
        }
    }
}

fn scoped_slot_callables(
    resolved: &ResolvedTwigScopedSlot,
    member_name: &str,
) -> Vec<AdminCallable> {
    if member_name.is_empty() {
        return Vec::new();
    }
    let mut result = Vec::new();
    for contract in &resolved.contracts {
        let Some(member) = contract.slot.member(member_name) else {
            continue;
        };
        if member.type_.is_empty() {
            continue;
        }
        result.push(AdminCallable::new(
            member_name,
            member.type_.clone(),
            format!(
                "slot `{}.{}`",
                contract.component.name, resolved.scope.slot_name
            ),
        ));
    }
    result
}

fn admin_signature_help(
    mut callables: Vec<AdminCallable>,
    active: i32,
) -> Option<protocol::SignatureHelp> {
    if callables.is_empty() {
        return None;
    }
    callables.sort_by(|left, right| {
        left.name
            .cmp(&right.name)
            .then_with(|| left.type_.cmp(&right.type_))
    });
    let mut result = protocol::SignatureHelp::default();
    let mut seen = HashSet::new();
    for callable in callables {
        let Some((parameters, return_type)) = swlsp_admin::vue_callable_signature(&callable.type_)
        else {
            continue;
        };
        let mut label = format!("{}({})", callable.name, parameters.join(", "));
        if !return_type.is_empty() {
            label.push_str(": ");
            label.push_str(&return_type);
        }
        if !seen.insert(label.clone()) {
            continue;
        }
        let mut active_parameter = active;
        if parameters.is_empty() {
            active_parameter = 0;
        } else if active_parameter >= parameters.len() as i32 {
            active_parameter = parameters.len() as i32 - 1;
        }
        result.signatures.push(protocol::SignatureInformation {
            label,
            active_parameter,
            documentation: (!callable.detail.is_empty()).then(|| markdown(callable.detail)),
            parameters: parameters
                .into_iter()
                .map(|label| protocol::ParameterInformation {
                    label,
                    documentation: None,
                })
                .collect(),
        });
    }
    let first = result.signatures.first()?;
    result.active_parameter = first.active_parameter;
    Some(result)
}

fn admin_javascript_call_callee(call: &SyntaxNode) -> Option<SyntaxNode> {
    if call.kind() != JS_CALL_EXPRESSION {
        return None;
    }
    call.children().next()
}

fn direct_admin_child(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.children().find(|child| child.kind() == kind)
}

fn admin_active_argument(arguments: Option<&SyntaxNode>, comma: Kind, offset: u32) -> i32 {
    let Some(arguments) = arguments else {
        return 0;
    };
    let mut active = 0;
    for token in arguments
        .children_with_tokens()
        .filter_map(|element| element.into_token())
    {
        if token.text_range().start() >= TextSize::from(offset) {
            break;
        }
        if token.kind() == comma {
            active += 1;
        }
    }
    active
}
