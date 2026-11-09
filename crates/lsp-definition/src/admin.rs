//! Go-to-definition for Shopware Administration Vue components, registries and
//! template members.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_admin::{
    self as admin, AdminCMSRegistrationKind, AdminComponentIndexer, AdminSourceRange,
    AdminSymbolKind, AdminSymbolTarget, ResolvedTwigSlotBinding, TwigBlock, TwigVueBinding,
    VueComponent, VueComponentEvent, VueComponentMember, VueComponentMemberKind, VueComponentProp,
    VueComponentSlot, VueComponentSlotMember, VueDynamicComponentCandidate,
};
use swlsp_language::LanguageId;
use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider, effective_syntax_language};
use swlsp_lsp_protocol::{self as protocol, Location, Position, Range};
use swlsp_parser_js::query as jsquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_ACCESSOR, TWIG_VAR};
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, SyntaxToken};
use swlsp_uriutil::{file_uri, filepath};

/// Go-to-definition for Shopware Admin Vue components.
pub struct AdminDefinitionProvider {
    admin: Arc<AdminComponentIndexer>,
}

impl AdminDefinitionProvider {
    pub fn new(admin: Arc<AdminComponentIndexer>) -> Self {
        Self { admin }
    }
}

impl GotoDefinitionProvider for AdminDefinitionProvider {
    fn get_definition(&self, _ctx: &Context, request: &DefinitionRequest) -> Vec<Location> {
        let uri = &request.params.text_document.uri;
        let ext = filepath::ext(uri).to_lowercase();
        let language_at_cursor = effective_syntax_language(request.language, request.node.as_ref());

        if ext == ".js"
            || ext == ".ts"
            || ext == ".vue" && language_at_cursor == Some(LanguageId::JavaScript)
        {
            let Some(node) = request.node.as_ref() else {
                return Vec::new();
            };
            return self.js_definition(request, node);
        }

        if ext == ".twig" || ext == ".vue" && language_at_cursor == Some(LanguageId::Twig) {
            let Some(node) = request.node.as_ref() else {
                return Vec::new();
            };
            if uri.contains("Resources/app/administration") {
                return self.twig_definition(request, node);
            }
        }
        Vec::new()
    }
}

impl AdminDefinitionProvider {
    fn live_owner(&self, request: &DefinitionRequest, template_path: &str) -> Option<VueComponent> {
        self.admin
            .get_component_for_document(
                template_path,
                request.root.as_ref(),
                request.source(),
                request.line_index(),
            )
            .ok()
            .flatten()
    }

    fn twig_definition(&self, request: &DefinitionRequest, node: &SyntaxNode) -> Vec<Location> {
        let template_path = admin_definition_template_path(&request.params.text_document.uri);
        let live_owner = self.live_owner(request, &template_path);
        let owner = live_owner.as_ref();
        if let Some(token) = request.token.as_ref()
            && let Some(block_name) = admin::twig_block_name_at(node, token)
        {
            return self.parent_block_definition(&template_path, &block_name);
        }
        let mut vue_expression = false;
        if let (Some(root), Some(lines)) = (request.root.as_ref(), request.line_index()) {
            let content = request.source();
            let offset = lines.offset_utf16(
                request.params.position.line,
                request.params.position.character,
            );
            if let Some(directive) = admin::twig_directive_at_offset(root, offset) {
                return self.directive_definition_for_template(&directive.name, &template_path);
            }
            if let Some(reference) = admin::twig_registry_reference_at_offset(root, offset)
                && !reference.name.is_empty()
            {
                match reference.kind {
                    AdminSymbolKind::Privilege => {
                        return self.privilege_definition(&reference.name);
                    }
                    AdminSymbolKind::ModuleRoute => {
                        return self.module_route_definition(&reference.name);
                    }
                    _ => {}
                }
            }
            if let Some(candidate) = admin_dynamic_component_candidate_at(node, offset) {
                return self.component_definition_by_name(&candidate.name, &template_path, owner);
            }
            if let Some((start_tag, field)) =
                admin::twig_component_object_binding_field_at_offset(root, offset)
            {
                return self.prop_definition_by_name(
                    Some(&start_tag),
                    &admin::normalize_prop_name(&field.name),
                    &template_path,
                    owner,
                );
            }
            if let Some(locations) = self.twig_vue_member_definition(request, root, node, offset) {
                return locations;
            }
            let Ok(resolved_slot) = self.admin.resolve_twig_scoped_slot_binding_for_owner(
                root,
                node,
                content,
                offset,
                &template_path,
                owner,
            ) else {
                return Vec::new();
            };
            let resolved_vue = self
                .admin
                .resolve_twig_vue_binding_for_component(
                    root,
                    content,
                    offset,
                    &template_path,
                    owner,
                )
                .ok()
                .flatten();
            if let Some(resolved_vue) = resolved_vue
                && resolved_slot.as_ref().is_none_or(|slot| {
                    resolved_vue.scope_range.len() <= slot.scoped.scope.template_range.len()
                })
            {
                return twig_vue_binding_definition(request, &resolved_vue);
            }
            if let Some(resolved_slot) = resolved_slot {
                return scoped_slot_binding_definition(&resolved_slot);
            }
            vue_expression = admin::is_twig_vue_expression_at(node, offset);
        }
        if twigquery::closest_node_of_kind(node, &[TWIG_VAR]).is_some() || vue_expression {
            return self.template_member_definition(request, node);
        }

        if let Some(token) = request.token.as_ref()
            && let Some(start_tag) = twigquery::starting_html_tag_at(node)
            && twigquery::html_tag_name(&start_tag).unwrap_or_default() == token.text()
        {
            return self.component_definition(&start_tag, &template_path, owner);
        }

        if let Some(attribute) = twigquery::html_attribute_at(node) {
            let name = twigquery::html_attribute_name(&attribute).unwrap_or_default();
            if admin::normalize_model_argument(&name).is_some() {
                return self.model_definition(&attribute, &template_path, owner);
            }
            if name.starts_with('#') || name.starts_with("v-slot:") || name == "v-slot" {
                return self.slot_definition(&attribute, &template_path, owner);
            }
            if !admin::normalize_event_name(&name).is_empty() {
                return self.event_definition(&attribute, &template_path, owner);
            }
            return self.prop_definition(&attribute, &template_path, owner);
        }
        Vec::new()
    }

    fn parent_block_definition(&self, template_path: &str, block_name: &str) -> Vec<Location> {
        if template_path.is_empty() || block_name.is_empty() {
            return Vec::new();
        }
        let Ok(Some(parent)) = self.admin.get_parent_component_for_template(template_path) else {
            return Vec::new();
        };
        match parent.component_block(block_name) {
            Some(block) if !block.file_path.is_empty() => vec![admin_block_location(block)],
            _ => Vec::new(),
        }
    }

    fn model_definition(
        &self,
        attribute: &SyntaxNode,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Vec<Location> {
        let start_tag = twigquery::starting_html_tag_at(attribute);
        let components = self.components_for_markup_tag(start_tag.as_ref(), template_path, owner);
        if components.is_empty() {
            return Vec::new();
        }
        let attribute_name = twigquery::html_attribute_name(attribute).unwrap_or_default();
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        let mut add =
            |component: &VueComponent, path: &str, line: u32, name_range: &AdminSourceRange| {
                let mut path = path;
                if path.is_empty() {
                    path = &component.definition_path;
                }
                if path.is_empty() {
                    path = &component.file_path;
                }
                if path.is_empty() {
                    return;
                }
                let key = format!(
                    "{path}:{line}:{}:{}",
                    name_range.start_line, name_range.start_character
                );
                if !seen.insert(key) {
                    return;
                }
                result.push(admin_declaration_location(path, line, name_range));
            };
        for component in &components {
            let Some(model) = component.component_model(&attribute_name) else {
                continue;
            };
            add(
                component,
                &model.prop.file_path,
                model.prop.line,
                &model.prop.name_range,
            );
            add(
                component,
                &model.event.file_path,
                model.event.line,
                &model.event.name_range,
            );
        }
        result
    }

    /// `None` when the cursor is not on a Vue expression member access.
    fn twig_vue_member_definition(
        &self,
        request: &DefinitionRequest,
        root: &SyntaxNode,
        node: &SyntaxNode,
        offset: u32,
    ) -> Option<Vec<Location>> {
        let content = request.source();
        let access = admin::twig_vue_expression_member_at_offset(root, content, offset)?;
        if access.member.is_empty() {
            return None;
        }
        let template_path = admin_definition_template_path(&request.params.text_document.uri);
        let live_component = self.live_owner(request, &template_path);
        let live = live_component.as_ref();
        let Ok(resolved_slot) = self.admin.resolve_twig_scoped_slot_member_for_owner(
            root,
            node,
            content,
            offset,
            &template_path,
            live,
        ) else {
            return Some(Vec::new());
        };
        let Ok(resolved_vue) = self.admin.resolve_twig_vue_member_for_component(
            root,
            content,
            offset,
            &template_path,
            live,
        ) else {
            return Some(Vec::new());
        };
        if let Some(resolved_vue) = resolved_vue
            && resolved_slot.as_ref().is_none_or(|slot| {
                resolved_vue.binding.scope_range.len() <= slot.scoped.scope.template_range.len()
            })
        {
            if resolved_vue.member_found && !resolved_vue.member.definition_path.is_empty() {
                return Some(vec![line_location(
                    &resolved_vue.member.definition_path,
                    resolved_vue.member.definition_line,
                )]);
            }
            // Observed JavaScript object properties have no authoritative declaration.
            return Some(Vec::new());
        }
        if let Some(resolved_slot) = resolved_slot {
            if !resolved_slot.member_found {
                return Some(Vec::new());
            }
            return Some(slot_member_definition_locations(
                &resolved_slot.members,
                &resolved_slot.member,
            ));
        }
        let Ok(resolved_instance) = self.admin.resolve_twig_vue_instance_member_for_component(
            root,
            content,
            offset,
            &template_path,
            live,
        ) else {
            return Some(Vec::new());
        };
        if let Some(instance) = resolved_instance {
            if !instance.member_found || instance.member.definition_path.is_empty() {
                return Some(Vec::new());
            }
            return Some(vec![line_location(
                &instance.member.definition_path,
                instance.member.definition_line,
            )]);
        }
        Some(Vec::new())
    }

    fn event_definition(
        &self,
        attribute: &SyntaxNode,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Vec<Location> {
        let event_name = admin::normalize_event_name(
            &twigquery::html_attribute_name(attribute).unwrap_or_default(),
        );
        if event_name.is_empty() {
            return Vec::new();
        }
        let start_tag = twigquery::starting_html_tag_at(attribute);
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for component in self.components_for_markup_tag(start_tag.as_ref(), template_path, owner) {
            let Some(event) = component.component_event(&event_name) else {
                continue;
            };
            let mut target_path = event.file_path.as_str();
            if target_path.is_empty() {
                target_path = &component.definition_path;
            }
            if target_path.is_empty() {
                target_path = &component.file_path;
            }
            let key = format!(
                "{target_path}:{}:{}:{}",
                event.line, event.name_range.start_line, event.name_range.start_character
            );
            if target_path.is_empty() || !seen.insert(key) {
                continue;
            }
            result.push(component_event_location(target_path, &event));
        }
        result
    }

    fn template_member_definition(
        &self,
        request: &DefinitionRequest,
        node: &SyntaxNode,
    ) -> Vec<Location> {
        let content = request.source();
        let mut name = String::new();
        if twigquery::closest_node_of_kind(node, &[TWIG_VAR]).is_some() {
            name = admin_template_root_name(node, request.token.as_ref(), content);
        } else if let Some(lines) = request.line_index() {
            let offset = lines.offset_utf16(
                request.params.position.line,
                request.params.position.character,
            );
            name = admin::expression_root_identifier_at_offset(content, offset)
                .map(|(name, _)| name)
                .unwrap_or_default();
        }
        if name.is_empty() {
            return Vec::new();
        }
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Vec::new();
        };
        let Ok(Some(component)) = self.admin.get_component_for_document(
            &path,
            request.root.as_ref(),
            content,
            request.line_index(),
        ) else {
            return Vec::new();
        };
        if let Some((scope_member, _)) = admin::twig_block_scope_member_at(&component, node, &name)
            && !scope_member.file_path.is_empty()
        {
            if scope_member.name_range.identifier || scope_member.name_range.declaration {
                return vec![source_range_location(
                    &scope_member.file_path,
                    &scope_member.name_range,
                )];
            }
            return vec![line_location(&scope_member.file_path, scope_member.line)];
        }
        match component.template_member(&name) {
            Some(member) if !member.file_path.is_empty() => {
                vec![component_member_location(&member)]
            }
            _ => Vec::new(),
        }
    }

    fn component_definition(
        &self,
        node: &SyntaxNode,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Vec<Location> {
        let component_name = twigquery::html_tag_name(node).unwrap_or_default();
        self.component_definition_by_name(&component_name, template_path, owner)
    }

    fn component_definition_by_name(
        &self,
        component_name: &str,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Vec<Location> {
        if component_name.is_empty() {
            return Vec::new();
        }
        let Ok(Some(comp)) =
            self.admin
                .get_component_for_template_tag(template_path, component_name, owner)
        else {
            return Vec::new();
        };
        let mut target_path = comp.definition_path.as_str();
        let mut target_line = 1;
        if !target_path.is_empty()
            && filepath::clean(target_path) == filepath::clean(&comp.file_path)
        {
            // Inline component definitions live at the registration call, not at
            // the beginning of the containing source file.
            target_line = comp.line.max(1);
        }
        if target_path.is_empty() {
            target_path = &comp.file_path;
            target_line = comp.line;
        }
        if target_path.is_empty() {
            return Vec::new();
        }
        vec![start_of_line_location(target_path, target_line)]
    }

    fn prop_definition(
        &self,
        node: &SyntaxNode,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Vec<Location> {
        let attr_name = twigquery::html_attribute_name(node).unwrap_or_default();
        if attr_name.is_empty() {
            return Vec::new();
        }
        let prop_name = admin::normalize_prop_name(&attr_name);
        if prop_name.is_empty() {
            return Vec::new();
        }
        let start_tag = twigquery::starting_html_tag_at(node);
        self.prop_definition_by_name(start_tag.as_ref(), &prop_name, template_path, owner)
    }

    fn prop_definition_by_name(
        &self,
        start_tag: Option<&SyntaxNode>,
        prop_name: &str,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Vec<Location> {
        if prop_name.is_empty() || start_tag.is_none() {
            return Vec::new();
        }
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for comp in self.components_for_markup_tag(start_tag, template_path, owner) {
            for prop in &comp.props {
                if prop.name != prop_name {
                    continue;
                }
                // Inherited props retain their owning definition path.
                let mut target_path = prop.file_path.as_str();
                if target_path.is_empty() {
                    target_path = &comp.definition_path;
                }
                if target_path.is_empty() {
                    target_path = &comp.file_path;
                }
                let key = format!(
                    "{target_path}:{}:{}:{}",
                    prop.line, prop.name_range.start_line, prop.name_range.start_character
                );
                if target_path.is_empty()
                    || prop.line == 0 && !prop.name_range.declaration
                    || seen.contains(&key)
                {
                    continue;
                }
                seen.insert(key);
                result.push(component_prop_location(target_path, prop));
            }
        }
        result
    }

    fn components_for_markup_tag(
        &self,
        start_tag: Option<&SyntaxNode>,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Vec<VueComponent> {
        let Some(start_tag) = start_tag else {
            return Vec::new();
        };
        if let Some(selector) = admin::twig_dynamic_component_selector(start_tag) {
            return match self.admin.resolve_dynamic_component_contracts_for_owner(
                template_path,
                &selector,
                owner,
                Some(start_tag),
            ) {
                Ok((_, components, true)) => components,
                _ => Vec::new(),
            };
        }
        let Some(component_name) = admin::static_component_name_for_tag(start_tag) else {
            return Vec::new();
        };
        match self
            .admin
            .get_component_for_template_tag(template_path, &component_name, owner)
        {
            Ok(Some(component)) => vec![component],
            _ => Vec::new(),
        }
    }

    fn slot_definition(
        &self,
        node: &SyntaxNode,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Vec<Location> {
        let slot_name = extract_slot_name(node);
        if slot_name.is_empty() {
            return Vec::new();
        }
        let Some(start_tag) = twigquery::starting_html_tag_at(node) else {
            return Vec::new();
        };
        let components =
            match self
                .admin
                .resolve_twig_slot_consumer_components(template_path, &start_tag, owner)
            {
                Ok((components, true)) => components,
                _ => return Vec::new(),
            };
        let mut locations = Vec::with_capacity(components.len());
        let mut seen = HashSet::new();
        for component in &components {
            let Some(slot) = component.component_slot(&slot_name) else {
                continue;
            };
            let mut source_path = slot.file_path.as_str();
            if source_path.is_empty() {
                source_path = &component.template_path;
            }
            if source_path.is_empty() {
                continue;
            }
            let key = range_key(source_path, slot.line, &slot.name_range);
            if !seen.insert(key) {
                continue;
            }
            locations.push(component_slot_location(source_path, slot));
        }
        locations
    }

    fn js_definition(&self, request: &DefinitionRequest, node: &SyntaxNode) -> Vec<Location> {
        let uri = &request.params.text_document.uri;
        if let Some((_, event_name)) = admin::javascript_shopware_event_bus_event_at(node)
            && !event_name.is_empty()
        {
            return self.shopware_event_bus_event_definition(uri, &event_name);
        }
        if let Some((receiver, member_name)) = admin::javascript_shopware_utils_member(node)
            && !member_name.is_empty()
        {
            return self.shopware_utils_member_definition(uri, &receiver.join("."), &member_name);
        }
        if let Some((receiver, member_name)) = admin::javascript_shopware_context_member(node)
            && !member_name.is_empty()
        {
            return self.shopware_context_member_definition(uri, &receiver.join("."), &member_name);
        }
        if let Some((container_name, member_name)) =
            admin::javascript_application_container_member(node)
            && !member_name.is_empty()
        {
            return self.application_container_member_definition(
                uri,
                &container_name,
                &member_name,
            );
        }
        if let Some(member) = jsquery::store_member(node)
            && !member.member.is_empty()
        {
            return self.store_member_definition(&member.store, &member.member);
        }
        if let Some(member) = jsquery::this_member(node)
            && !member.is_empty()
        {
            return self.this_member_definition(uri, &member);
        }
        if admin::is_service_reference(node) {
            return self.service_definition(&jsquery::string_value(node));
        }
        if admin::is_store_reference(node) {
            return self.store_definition(&jsquery::string_value(node));
        }
        if admin::is_privilege_reference(node) {
            return self.privilege_definition(&jsquery::string_value(node));
        }
        if jsquery::string_in_call(node, 0, &["Mixin.getByName", "Shopware.Mixin.getByName"]) {
            return self.mixin_definition(&jsquery::string_value(node));
        }
        if admin::is_javascript_module_route_reference(node) {
            return self.module_route_definition(&jsquery::string_value(node));
        }
        if let Ok(path) = swlsp_uriutil::path(uri)
            && let Ok(Some(indexed)) = self.admin.javascript_symbol_at(&path, node)
            && indexed.kind == AdminSymbolKind::Directive
            && !indexed.owner.is_empty()
        {
            return self.directive_definition_target(&indexed);
        }

        let Some(target) = admin::javascript_symbol_at(node) else {
            return Vec::new();
        };
        match target.kind {
            AdminSymbolKind::Module => return self.module_definition(&target.name),
            AdminSymbolKind::Mixin => return self.mixin_definition(&target.name),
            AdminSymbolKind::Directive => return self.directive_definition(&target.name),
            AdminSymbolKind::Filter => return self.filter_definition(&target.name),
            AdminSymbolKind::CmsElement => {
                return self.cms_definition(AdminCMSRegistrationKind::Element, &target.name);
            }
            AdminSymbolKind::CmsBlock => {
                return self.cms_definition(AdminCMSRegistrationKind::Block, &target.name);
            }
            AdminSymbolKind::Component => {}
            _ => return Vec::new(),
        }
        if target.name.is_empty() {
            return Vec::new();
        }
        let Ok(components) = self.admin.get_component(&target.name) else {
            return Vec::new();
        };
        let mut locations = Vec::new();
        for comp in &components {
            let mut target_path = comp.definition_path.as_str();
            let mut target_line = 1;
            if target_path.is_empty() {
                target_path = &comp.file_path;
                target_line = comp.line;
            }
            if target_path.is_empty() {
                continue;
            }
            locations.push(start_of_line_location(target_path, target_line));
        }
        locations
    }

    fn shopware_event_bus_event_definition(&self, uri: &str, event_name: &str) -> Vec<Location> {
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let Ok(Some(event)) = self
            .admin
            .resolve_shopware_event_bus_event(event_name, &path)
        else {
            return Vec::new();
        };
        if event.definition_path.is_empty() {
            return Vec::new();
        }
        vec![member_definition_location(&event)]
    }

    fn shopware_utils_member_definition(
        &self,
        uri: &str,
        receiver: &str,
        member_name: &str,
    ) -> Vec<Location> {
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let Ok(shape) = self.admin.resolve_shopware_utils(receiver, &path) else {
            return Vec::new();
        };
        shape_member_location(&shape.members, member_name)
    }

    fn application_container_member_definition(
        &self,
        uri: &str,
        container_name: &str,
        member_name: &str,
    ) -> Vec<Location> {
        if container_name == "service" {
            let locations = self.service_definition(member_name);
            if !locations.is_empty() {
                return locations;
            }
        }
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let Ok(shape) = self
            .admin
            .resolve_application_container(container_name, &path)
        else {
            return Vec::new();
        };
        shape_member_location(&shape.members, member_name)
    }

    fn shopware_context_member_definition(
        &self,
        uri: &str,
        receiver: &str,
        member_name: &str,
    ) -> Vec<Location> {
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let Ok(shape) = self.admin.resolve_shopware_context(receiver, &path) else {
            return Vec::new();
        };
        shape_member_location(&shape.members, member_name)
    }

    fn service_definition(&self, name: &str) -> Vec<Location> {
        let Ok(services) = self.admin.get_service(name) else {
            return Vec::new();
        };
        services
            .iter()
            .map(|service| {
                if !service.implementation_path.is_empty() {
                    line_location(&service.implementation_path, 1)
                } else {
                    line_location(&service.file_path, service.line)
                }
            })
            .collect()
    }

    fn store_definition(&self, name: &str) -> Vec<Location> {
        let Ok(stores) = self.admin.get_store(name) else {
            return Vec::new();
        };
        stores
            .iter()
            .map(|store| line_location(&store.file_path, store.line))
            .collect()
    }

    fn privilege_definition(&self, name: &str) -> Vec<Location> {
        let Ok(privileges) = self.admin.get_privilege(name) else {
            return Vec::new();
        };
        privileges
            .iter()
            .filter(|privilege| !privilege.is_builtin())
            .map(|privilege| line_location(&privilege.file_path, privilege.line))
            .collect()
    }

    fn store_member_definition(&self, store_name: &str, member_name: &str) -> Vec<Location> {
        let Ok(stores) = self.admin.get_store(store_name) else {
            return Vec::new();
        };
        stores
            .iter()
            .flat_map(|store| store.members.iter())
            .filter(|member| member.name == member_name)
            .map(|member| line_location(&member.file_path, member.line))
            .collect()
    }

    fn this_member_definition(&self, uri: &str, name: &str) -> Vec<Location> {
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let Ok(components) = self.admin.get_components_by_definition_path(&path) else {
            return Vec::new();
        };
        let mut locations = Vec::new();
        let mut seen = HashSet::new();
        for component in &components {
            let Some(member) = component.template_member(name) else {
                continue;
            };
            if member.file_path.is_empty() {
                continue;
            }
            let location = component_member_location(&member);
            let key = format!(
                "{}:{}:{}:{}:{}",
                location.uri,
                location.range.start.line,
                location.range.start.character,
                location.range.end.line,
                location.range.end.character
            );
            if !seen.insert(key) {
                continue;
            }
            locations.push(location);
            if member.kind == VueComponentMemberKind::Inject {
                locations.extend(self.service_definition(&member.name));
            }
        }
        locations
    }

    fn mixin_definition(&self, name: &str) -> Vec<Location> {
        let Ok(mixins) = self.admin.get_mixin(name) else {
            return Vec::new();
        };
        mixins
            .iter()
            .map(|mixin| line_location(&mixin.file_path, mixin.line))
            .collect()
    }

    fn directive_definition(&self, name: &str) -> Vec<Location> {
        let Ok(directives) = self.admin.get_directive(name) else {
            return Vec::new();
        };
        directives
            .iter()
            .map(|directive| line_location(&directive.file_path, directive.line))
            .collect()
    }

    fn filter_definition(&self, name: &str) -> Vec<Location> {
        let Ok(filters) = self.admin.get_filter(name) else {
            return Vec::new();
        };
        filters
            .iter()
            .map(|filter| line_location(&filter.file_path, filter.line))
            .collect()
    }

    fn cms_definition(&self, kind: AdminCMSRegistrationKind, name: &str) -> Vec<Location> {
        let Ok(registrations) = self.admin.get_cms_registration(kind, name) else {
            return Vec::new();
        };
        registrations
            .iter()
            .map(|registration| line_location(&registration.file_path, registration.line))
            .collect()
    }

    fn directive_definition_for_template(&self, name: &str, template_path: &str) -> Vec<Location> {
        let Ok(directives) = self.admin.get_directive_for_template(template_path, name) else {
            return Vec::new();
        };
        directives
            .iter()
            .map(|directive| line_location(&directive.file_path, directive.line))
            .collect()
    }

    fn directive_definition_target(&self, target: &AdminSymbolTarget) -> Vec<Location> {
        if target.owner.is_empty() {
            return self.directive_definition(&target.name);
        }
        let Ok(components) = self.admin.get_components_by_definition_path(&target.owner) else {
            return Vec::new();
        };
        components
            .iter()
            .filter_map(|component| component.local_directive(&target.name))
            .map(|local| line_location(&local.file_path, local.line))
            .collect()
    }

    fn module_route_definition(&self, name: &str) -> Vec<Location> {
        match self.admin.get_module_route(name) {
            Ok(Some((module, route))) => vec![line_location(&module.file_path, route.line)],
            _ => Vec::new(),
        }
    }

    fn module_definition(&self, name: &str) -> Vec<Location> {
        let Ok(modules) = self.admin.get_module(name) else {
            return Vec::new();
        };
        modules
            .iter()
            .map(|module| line_location(&module.file_path, module.line))
            .collect()
    }
}

fn admin_definition_template_path(uri: &str) -> String {
    swlsp_uriutil::path(uri).unwrap_or_default()
}

fn admin_block_location(block: &TwigBlock) -> Location {
    admin_declaration_location(&block.file_path, block.line, &block.name_range)
}

fn twig_vue_binding_definition(
    request: &DefinitionRequest,
    binding: &TwigVueBinding,
) -> Vec<Location> {
    if !binding.definition_path.is_empty() {
        return vec![line_location(
            &binding.definition_path,
            binding.definition_line,
        )];
    }
    if binding.declaration_range.is_empty() {
        return Vec::new();
    }
    let Some(lines) = request.line_index() else {
        return Vec::new();
    };
    vec![Location {
        uri: request.params.text_document.uri.clone(),
        range: byte_range(
            lines,
            binding.declaration_range.start().into(),
            binding.declaration_range.end().into(),
        ),
    }]
}

fn scoped_slot_binding_definition(resolved: &ResolvedTwigSlotBinding) -> Vec<Location> {
    if resolved.member_found && !resolved.binding.whole_object {
        let locations = slot_member_definition_locations(&resolved.members, &resolved.member);
        if !locations.is_empty() {
            return locations;
        }
    }
    let mut locations = Vec::new();
    let mut seen = HashSet::new();
    for contract in &resolved.scoped.contracts {
        let mut file_path = contract.slot.file_path.as_str();
        if file_path.is_empty() {
            file_path = &contract.component.template_path;
        }
        if file_path.is_empty() {
            file_path = &contract.component.definition_path;
        }
        if file_path.is_empty() {
            continue;
        }
        let key = range_key(file_path, contract.slot.line, &contract.slot.name_range);
        if !seen.insert(key) {
            continue;
        }
        locations.push(component_slot_location(file_path, &contract.slot));
    }
    if !locations.is_empty() {
        return locations;
    }
    let scoped = &resolved.scoped;
    let mut file_path = scoped.slot.file_path.as_str();
    if resolved.member_found && !resolved.member.file_path.is_empty() {
        file_path = &resolved.member.file_path;
    }
    if file_path.is_empty() {
        file_path = &scoped.component.template_path;
    }
    if file_path.is_empty() {
        file_path = &scoped.component.definition_path;
    }
    if file_path.is_empty() {
        return Vec::new();
    }
    if resolved.member_found && !resolved.member.file_path.is_empty() {
        return vec![component_slot_member_location(file_path, &resolved.member)];
    }
    vec![component_slot_location(file_path, &scoped.slot)]
}

fn slot_member_definition_locations(
    members: &[VueComponentSlotMember],
    fallback: &VueComponentSlotMember,
) -> Vec<Location> {
    let fallback_list;
    let members = if members.is_empty() && !fallback.name.is_empty() {
        fallback_list = [fallback.clone()];
        &fallback_list[..]
    } else {
        members
    };
    let mut locations = Vec::with_capacity(members.len());
    let mut seen = HashSet::new();
    for member in members {
        if member.file_path.is_empty() {
            continue;
        }
        let key = range_key(&member.file_path, member.line, &member.name_range);
        if !seen.insert(key) {
            continue;
        }
        locations.push(component_slot_member_location(&member.file_path, member));
    }
    locations
}

fn range_key(path: &str, line: u32, range: &AdminSourceRange) -> String {
    format!(
        "{path}:{line}:{}:{}:{}:{}",
        range.start_line, range.start_character, range.end_line, range.end_character
    )
}

fn admin_template_root_name(
    node: &SyntaxNode,
    token: Option<&SyntaxToken>,
    content: &str,
) -> String {
    let Some(token) = token else {
        return String::new();
    };
    if let Some(accessor) = twigquery::closest_node_of_kind(node, &[TWIG_ACCESSOR]) {
        let start: usize = accessor.range_trimmed_trivia().start().into();
        let end: usize = token.text_range().start().into();
        if start < end && end <= content.len() && content.as_bytes()[start..end].contains(&b'.') {
            return String::new();
        }
    }
    token.text().trim().to_owned()
}

fn admin_dynamic_component_candidate_at(
    node: &SyntaxNode,
    offset: u32,
) -> Option<VueDynamicComponentCandidate> {
    let start_tag = twigquery::starting_html_tag_at(node)?;
    let selector = admin::twig_dynamic_component_selector(&start_tag)?;
    selector.candidate_at(offset).cloned()
}

/// "#default" -> "default", "v-slot:actions" -> "actions"
fn extract_slot_name(node: &SyntaxNode) -> String {
    admin::normalize_slot_name(&twigquery::html_attribute_name(node).unwrap_or_default())
}

fn shape_member_location(members: &[admin::TwigVueMember], member_name: &str) -> Vec<Location> {
    members
        .iter()
        .find(|member| member.name == member_name && !member.definition_path.is_empty())
        .map(|member| vec![member_definition_location(member)])
        .unwrap_or_default()
}

fn member_definition_location(member: &admin::TwigVueMember) -> Location {
    if member.definition_range.declaration || member.definition_range.identifier {
        return source_range_location(&member.definition_path, &member.definition_range);
    }
    line_location(&member.definition_path, member.definition_line)
}

fn component_member_location(member: &VueComponentMember) -> Location {
    if member.renameable() {
        return source_range_location(&member.file_path, &member.name_range);
    }
    line_location(&member.file_path, member.line)
}

fn component_prop_location(file_path: &str, prop: &VueComponentProp) -> Location {
    admin_declaration_location(file_path, prop.line, &prop.name_range)
}

fn component_event_location(file_path: &str, event: &VueComponentEvent) -> Location {
    admin_declaration_location(file_path, event.line, &event.name_range)
}

fn component_slot_location(file_path: &str, slot: &VueComponentSlot) -> Location {
    admin_declaration_location(file_path, slot.line, &slot.name_range)
}

fn component_slot_member_location(file_path: &str, member: &VueComponentSlotMember) -> Location {
    admin_declaration_location(file_path, member.line, &member.name_range)
}

fn admin_declaration_location(
    file_path: &str,
    line: u32,
    name_range: &AdminSourceRange,
) -> Location {
    if name_range.declaration || name_range.identifier {
        return source_range_location(file_path, name_range);
    }
    line_location(file_path, line)
}

fn source_range_location(file_path: &str, range: &AdminSourceRange) -> Location {
    Location {
        uri: file_uri(file_path),
        range: Range {
            start: Position {
                line: range.start_line,
                character: range.start_character,
            },
            end: Position {
                line: range.end_line,
                character: range.end_character,
            },
        },
    }
}

/// A zero-width location at the start of a one-based line, clamped to 1.
fn line_location(file_path: &str, line: u32) -> Location {
    start_of_line_location(file_path, line.max(1))
}

/// A location at the start of a one-based line. Line 0 saturates to the
/// first line because protocol lines are unsigned.
fn start_of_line_location(file_path: &str, line: u32) -> Location {
    let position = Position {
        line: line.saturating_sub(1),
        character: 0,
    };
    Location {
        uri: file_uri(file_path),
        range: Range {
            start: position,
            end: position,
        },
    }
}

fn byte_range(lines: &LineIndex, start: u32, end: u32) -> protocol::Range {
    let (start_line, start_character) = lines.position_utf16(start);
    let (end_line, end_character) = lines.position_utf16(end);
    Range {
        start: Position {
            line: start_line,
            character: start_character,
        },
        end: Position {
            line: end_line,
            character: end_character,
        },
    }
}

#[cfg(test)]
fn is_in_component_call(node: &SyntaxNode) -> bool {
    admin::javascript_symbol_at(node)
        .is_some_and(|target| target.kind == AdminSymbolKind::Component)
}

#[cfg(test)]
fn extract_component_name(node: &SyntaxNode) -> String {
    match admin::javascript_symbol_at(node) {
        Some(target) if target.kind == AdminSymbolKind::Component => target.name,
        _ => String::new(),
    }
}

#[cfg(test)]
#[path = "admin_tests.rs"]
mod tests;
