//! Administration hover in JavaScript and TypeScript.

use std::fmt::Write as _;

use swlsp_admin::{AdminCMSRegistrationKind, AdminSymbolKind, TwigVueMember};
use swlsp_lsp::HoverRequest;
use swlsp_parser_js::query as jsquery;
use swlsp_syntax::SyntaxNode;

use super::{AdminHoverProvider, HoverResult};
use crate::hover::{js_string_content_range, markdown_hover, request_byte_range, request_uri};

impl AdminHoverProvider {
    pub(crate) fn javascript_hover(
        &self,
        request: &HoverRequest,
        node: &SyntaxNode,
    ) -> HoverResult {
        let uri = request_uri(request);
        if let Some((_, event_name)) = swlsp_admin::javascript_shopware_event_bus_event_at(node)
            && !event_name.is_empty()
        {
            return self.shopware_event_bus_event_hover(uri, &event_name);
        }
        if let Some((receiver, member_name)) = swlsp_admin::javascript_shopware_utils_member(node)
            && !member_name.is_empty()
        {
            return self.shopware_utils_member_hover(uri, &receiver.join("."), &member_name);
        }
        if let Some((receiver, member_name)) = swlsp_admin::javascript_shopware_context_member(node)
            && !member_name.is_empty()
        {
            return self.shopware_context_member_hover(uri, &receiver.join("."), &member_name);
        }
        if swlsp_admin::is_application_container_name_reference(node)
            && let Some(container) =
                swlsp_admin::application_container_named(&jsquery::string_value(node))
        {
            return Ok(Some(markdown_hover(
                format!(
                    "**Application container** `{}`\n\n{}.\n\nType: `{}`.",
                    container.name, container.description, container.interface_name
                ),
                None,
            )));
        }
        if let Some((container_name, member_name)) =
            swlsp_admin::javascript_application_container_member(node)
            && !member_name.is_empty()
        {
            return self.application_container_member_hover(uri, &container_name, &member_name);
        }
        if let Some(store_member) = jsquery::store_member(node)
            && !store_member.member.is_empty()
        {
            return self.store_member_hover(&store_member.store, &store_member.member);
        }
        if let Some(member) = jsquery::this_member(node)
            && !member.is_empty()
        {
            return self.this_member_hover(uri, &member);
        }
        if swlsp_admin::is_service_reference(node) {
            return self.service_hover(&jsquery::string_value(node));
        }
        if swlsp_admin::is_store_reference(node) {
            return self.store_hover(&jsquery::string_value(node));
        }
        if swlsp_admin::is_privilege_reference(node) {
            return self.privilege_hover(&jsquery::string_value(node));
        }
        if swlsp_admin::is_javascript_module_route_reference(node) {
            return self.module_route_hover(&jsquery::string_value(node));
        }
        if let Ok(path) = swlsp_uriutil::path(uri)
            && let Some(target) = self.index.javascript_symbol_at(&path, node)?
            && target.kind == AdminSymbolKind::Directive
            && !target.owner.is_empty()
        {
            return self.directive_hover_target(&target);
        }
        let Some(target) = swlsp_admin::javascript_symbol_at(node) else {
            return Ok(None);
        };
        match target.kind {
            AdminSymbolKind::Mixin => return self.mixin_hover(&target.name),
            AdminSymbolKind::Directive => return self.directive_hover(&target.name),
            AdminSymbolKind::Filter => return self.filter_hover(&target.name),
            AdminSymbolKind::CmsElement => {
                return self.cms_hover(AdminCMSRegistrationKind::Element, &target.name);
            }
            AdminSymbolKind::CmsBlock => {
                return self.cms_hover(AdminCMSRegistrationKind::Block, &target.name);
            }
            AdminSymbolKind::Module => return self.module_hover(&target.name),
            AdminSymbolKind::Component => {}
            _ => return Ok(None),
        }
        let component_name = target.name;
        if component_name.is_empty() {
            return Ok(None);
        }
        let components = match self.index.get_component_with_definition(&component_name) {
            Ok(components) if !components.is_empty() => components,
            _ => return Ok(None),
        };
        Ok(Some(markdown_hover(
            self.build_hover_content(&components),
            request_byte_range(request, js_string_content_range(node)),
        )))
    }

    fn shopware_event_bus_event_hover(&self, uri: &str, event_name: &str) -> HoverResult {
        let path = swlsp_uriutil::path(uri)?;
        let Some(event) = self
            .index
            .resolve_shopware_event_bus_event(event_name, &path)?
        else {
            return Ok(None);
        };
        let mut value = format!("**Shopware EventBus event** `{event_name}`");
        if !event.type_.is_empty() {
            let _ = write!(value, "\n\nPayload: `{}`.", event.type_);
        }
        if !event.definition_path.is_empty() {
            let _ = write!(
                value,
                "\n\nDeclared in `{}:{}`.",
                self.make_relative_path(&event.definition_path),
                event.definition_line
            );
        }
        Ok(Some(markdown_hover(value, None)))
    }

    fn shopware_utils_member_hover(
        &self,
        uri: &str,
        receiver: &str,
        member_name: &str,
    ) -> HoverResult {
        let path = swlsp_uriutil::path(uri)?;
        let shape = self.index.resolve_shopware_utils(receiver, &path)?;
        let Some(member) = shape
            .members
            .iter()
            .find(|member| member.name == member_name)
        else {
            return Ok(None);
        };
        Ok(Some(self.qualified_member_hover(
            "Shopware.Utils",
            "Shopware utility",
            "Exported in",
            receiver,
            member,
        )))
    }

    fn shopware_context_member_hover(
        &self,
        uri: &str,
        receiver: &str,
        member_name: &str,
    ) -> HoverResult {
        let path = swlsp_uriutil::path(uri)?;
        let shape = self.index.resolve_shopware_context(receiver, &path)?;
        let Some(member) = shape
            .members
            .iter()
            .find(|member| member.name == member_name)
        else {
            return Ok(None);
        };
        Ok(Some(self.qualified_member_hover(
            "Shopware.Context",
            "Shopware context member",
            "Defined in",
            receiver,
            member,
        )))
    }

    fn qualified_member_hover(
        &self,
        prefix: &str,
        title: &str,
        location_label: &str,
        receiver: &str,
        member: &TwigVueMember,
    ) -> swlsp_lsp_protocol::Hover {
        let mut qualified = prefix.to_owned();
        if !receiver.is_empty() {
            qualified.push('.');
            qualified.push_str(receiver);
        }
        qualified.push('.');
        qualified.push_str(&member.name);
        let mut value = format!("**{title}** `{qualified}`");
        if !member.type_.is_empty() {
            let _ = write!(value, "\n\nType: `{}`.", member.type_);
        }
        if !member.definition_path.is_empty() {
            let _ = write!(
                value,
                "\n\n{location_label} `{}:{}`.",
                self.make_relative_path(&member.definition_path),
                member.definition_line
            );
        }
        markdown_hover(value, None)
    }

    fn application_container_member_hover(
        &self,
        uri: &str,
        container_name: &str,
        member_name: &str,
    ) -> HoverResult {
        let path = swlsp_uriutil::path(uri)?;
        let shape = self
            .index
            .resolve_application_container(container_name, &path)?;
        let resolved = shape
            .members
            .iter()
            .find(|member| member.name == member_name);
        if container_name == "service"
            && let Some(mut service) = self.service_hover(member_name)?
        {
            if let Some(resolved) = resolved
                && !resolved.type_.is_empty()
            {
                let _ = write!(
                    service.contents.value,
                    "\n\nContainer type: `{}`.",
                    resolved.type_
                );
            }
            return Ok(Some(service));
        }
        let Some(resolved) = resolved else {
            return Ok(None);
        };
        let mut value =
            format!("**Application `{container_name}` container member** `{member_name}`");
        if !resolved.type_.is_empty() {
            let _ = write!(value, "\n\nType: `{}`.", resolved.type_);
        }
        if !resolved.definition_path.is_empty() {
            let _ = write!(
                value,
                "\n\nDefined in `{}:{}`.",
                self.make_relative_path(&resolved.definition_path),
                resolved.definition_line
            );
        }
        Ok(Some(markdown_hover(value, None)))
    }
}
