//! Port of Go `admin_completion_javascript.go`.

use std::collections::HashMap;

use swlsp_admin::{
    AdminCMSRegistrationKind, AdminPrivilegeKind, AdminStoreMemberKind, AdminSymbolKind,
    VueComponentMemberKind, VueTypeShape, application_containers,
    is_application_container_name_reference, is_javascript_module_route_reference,
    is_privilege_reference, is_service_reference, is_store_reference,
    javascript_application_container_member, javascript_cms_completion_kind_at,
    javascript_cms_component_reference_at, javascript_registry_reference_at,
    javascript_shopware_context_member, javascript_shopware_event_bus_event_at,
    javascript_shopware_utils_member, vue_builtin_members, vue_callable_signature,
};
use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol as protocol;
use swlsp_parser_js::query as jsquery;
use swlsp_syntax::SyntaxNode;

use super::AdminCompletionProvider;
use super::components::{item, mark_admin_completion_deprecated, snippet};
use super::scope::{admin_member_completion_kind, admin_member_detail};
use crate::common::sort_completion_items;

fn sorted(
    items_by_name: HashMap<String, protocol::CompletionItem>,
) -> Vec<protocol::CompletionItem> {
    let mut items: Vec<_> = items_by_name.into_values().collect();
    sort_completion_items(&mut items);
    items
}

fn sorted_items(mut items: Vec<protocol::CompletionItem>) -> Vec<protocol::CompletionItem> {
    sort_completion_items(&mut items);
    items
}

impl AdminCompletionProvider {
    /// Go `jsCompletions`: completions in JS/TS files and Vue scripts.
    pub(super) fn js_completions(
        &self,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let uri = request.params.text_document.uri.as_str();
        if is_application_container_name_reference(node) {
            return application_container_name_completions();
        }
        if javascript_shopware_event_bus_event_at(node).is_some() {
            return self.get_shopware_event_bus_event_completions(uri);
        }
        if let Some((receiver, _)) = javascript_shopware_utils_member(node) {
            return self.get_shopware_utils_member_completions(&receiver.join("."), uri);
        }
        if let Some((receiver, _)) = javascript_shopware_context_member(node) {
            return self.get_shopware_context_member_completions(&receiver.join("."), uri);
        }
        if let Some((container_name, _)) = javascript_application_container_member(node) {
            return self.get_application_container_member_completions(&container_name, uri);
        }
        if let Some(member) = jsquery::store_member(node) {
            return self.get_store_member_completions(&member.store);
        }
        if jsquery::this_member(node).is_some()
            && let Some(items) = self.get_this_member_completions(uri)
        {
            return items;
        }
        let mut items = Vec::new();
        if is_service_reference(node) {
            items.extend(self.get_service_completions());
        }
        if is_store_reference(node) {
            items.extend(self.get_store_completions());
        }
        if is_privilege_reference(node) {
            items.extend(self.get_privilege_completions());
        }
        if let Some(kind) = javascript_cms_completion_kind_at(node) {
            items.extend(self.get_cms_completions(kind));
        }
        if javascript_cms_component_reference_at(node).is_some() {
            items.extend(self.get_component_completions());
        }
        if self.is_in_extend_parent_argument(node) {
            items.extend(self.get_component_completions());
        }
        if jsquery::string_in_call(
            node,
            0,
            &["Component.override", "Shopware.Component.override"],
        ) {
            items.extend(self.get_component_completions());
        }
        if jsquery::string_in_call(node, 0, &["Mixin.getByName", "Shopware.Mixin.getByName"]) {
            items.extend(self.get_mixin_completions());
        }
        if jsquery::string_in_call(
            node,
            0,
            &["Directive.getByName", "Shopware.Directive.getByName"],
        ) {
            items.extend(self.get_directive_completions(false, ""));
        }
        if jsquery::string_in_call(node, 0, &["Filter.getByName", "Shopware.Filter.getByName"]) {
            items.extend(self.get_filter_completions());
        }
        if let Some(reference) = javascript_registry_reference_at(node) {
            match reference.target.kind {
                AdminSymbolKind::Component => items.extend(self.get_component_completions()),
                AdminSymbolKind::Module => items.extend(self.get_module_completions()),
                _ => {}
            }
        }
        if self.is_module_route_reference(node) {
            items.extend(self.get_module_route_completions());
        }
        items
    }

    fn get_shopware_event_bus_event_completions(&self, uri: &str) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let Ok(shape) = index.resolve_shopware_event_bus_events(&path) else {
            return Vec::new();
        };
        sorted_items(
            shape
                .members
                .iter()
                .map(|event| {
                    let mut detail = String::from("Shopware EventBus event");
                    if !event.type_.is_empty() {
                        detail += " · ";
                        detail += &event.type_;
                    }
                    item(&event.name, protocol::CompletionItemKind::VALUE, detail)
                })
                .collect(),
        )
    }

    fn get_shopware_utils_member_completions(
        &self,
        receiver: &str,
        uri: &str,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let Ok(shape) = index.resolve_shopware_utils(receiver, &path) else {
            return Vec::new();
        };
        let mut prefix = String::from("Shopware.Utils");
        if !receiver.is_empty() {
            prefix += ".";
            prefix += receiver;
        }
        shape_member_items(&shape, &prefix, |member_type| {
            vue_callable_signature(member_type).is_some()
        })
    }

    fn get_shopware_context_member_completions(
        &self,
        receiver: &str,
        uri: &str,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let Ok(shape) = index.resolve_shopware_context(receiver, &path) else {
            return Vec::new();
        };
        let mut prefix = String::from("Shopware.Context");
        if !receiver.is_empty() {
            prefix += ".";
            prefix += receiver;
        }
        shape_member_items(&shape, &prefix, arrow_or_parenthesized)
    }

    fn get_application_container_member_completions(
        &self,
        container_name: &str,
        uri: &str,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let Ok(shape) = index.resolve_application_container(container_name, &path) else {
            return Vec::new();
        };
        shape_member_items(
            &shape,
            &format!("Application {container_name} container"),
            arrow_or_parenthesized,
        )
    }

    fn get_service_completions(&self) -> Vec<protocol::CompletionItem> {
        let Some(Ok(services)) = self.index.as_ref().map(|index| index.get_all_services()) else {
            return Vec::new();
        };
        sorted(
            services
                .into_iter()
                .map(|service| {
                    let completion = item(
                        &service.name,
                        protocol::CompletionItemKind::REFERENCE,
                        "Shopware Administration service",
                    );
                    (service.name, completion)
                })
                .collect(),
        )
    }

    fn get_cms_completions(&self, kind: AdminCMSRegistrationKind) -> Vec<protocol::CompletionItem> {
        let Some(Ok(registrations)) = self
            .index
            .as_ref()
            .map(|index| index.get_all_cms_registrations_by_kind(kind))
        else {
            return Vec::new();
        };
        sorted(
            registrations
                .into_iter()
                .map(|registration| {
                    let mut detail = format!("Shopware CMS {}", kind.as_str());
                    if !registration.label.is_empty() {
                        detail += " · ";
                        detail += &registration.label;
                    }
                    let completion = item(
                        &registration.name,
                        protocol::CompletionItemKind::REFERENCE,
                        detail,
                    );
                    (registration.name, completion)
                })
                .collect(),
        )
    }

    fn get_store_completions(&self) -> Vec<protocol::CompletionItem> {
        let Some(Ok(stores)) = self.index.as_ref().map(|index| index.get_all_stores()) else {
            return Vec::new();
        };
        sorted(
            stores
                .into_iter()
                .map(|store| {
                    let completion = item(
                        &store.name,
                        protocol::CompletionItemKind::REFERENCE,
                        "Shopware Administration store",
                    );
                    (store.name, completion)
                })
                .collect(),
        )
    }

    pub(super) fn get_privilege_completions(&self) -> Vec<protocol::CompletionItem> {
        let Some(Ok(privileges)) = self.index.as_ref().map(|index| index.get_all_privileges())
        else {
            return Vec::new();
        };
        let mut items_by_name: HashMap<String, protocol::CompletionItem> = HashMap::new();
        for privilege in privileges {
            let mut detail = if privilege.is_builtin() {
                String::from("Built-in Shopware Administration privilege")
            } else if privilege.kind == AdminPrivilegeKind::Permission {
                String::from("Administration permission")
            } else {
                String::from("Administration privilege role")
            };
            if !privilege.mapping_key.is_empty() {
                detail += " • ";
                detail += &privilege.mapping_key;
                if privilege.kind == AdminPrivilegeKind::Permission && !privilege.role.is_empty() {
                    detail += ".";
                    detail += &privilege.role;
                }
            }
            let completion = item(
                &privilege.name,
                protocol::CompletionItemKind::REFERENCE,
                detail,
            );
            // Prefer the public role key when a mapping uses the same spelling
            // for both a role and a concrete permission.
            let replace = items_by_name
                .get(&privilege.name)
                .is_none_or(|existing| existing.detail.contains("permission"));
            if replace {
                items_by_name.insert(privilege.name, completion);
            }
        }
        sorted(items_by_name)
    }

    fn get_store_member_completions(&self, store_name: &str) -> Vec<protocol::CompletionItem> {
        let Some(Ok(stores)) = self.index.as_ref().map(|index| index.get_store(store_name)) else {
            return Vec::new();
        };
        let mut items_by_name: HashMap<String, protocol::CompletionItem> = HashMap::new();
        for store in &stores {
            for member in &store.members {
                let mut detail = format!("{} • {}", member.kind.as_str(), store.name);
                if !member.type_.is_empty() {
                    detail += " • ";
                    detail += &member.type_;
                }
                let kind = if member.kind == AdminStoreMemberKind::Action {
                    protocol::CompletionItemKind::METHOD
                } else {
                    protocol::CompletionItemKind::PROPERTY
                };
                let mut completion = item(&member.name, kind, detail);
                if member.kind == AdminStoreMemberKind::Action {
                    snippet(&mut completion, format!("{}($0)", member.name));
                }
                items_by_name.insert(member.name.clone(), completion);
            }
        }
        sorted(items_by_name)
    }

    /// Go `getThisMemberCompletions`; `None` outside component definitions.
    pub(super) fn get_this_member_completions(
        &self,
        uri: &str,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let index = self.index.as_ref()?;
        let path = swlsp_uriutil::path(uri).ok()?;
        let components = index.get_components_by_definition_path(&path).ok()?;
        if components.is_empty() {
            return None;
        }
        let mut items_by_name: HashMap<String, protocol::CompletionItem> = HashMap::new();
        for component in &components {
            for member in component.template_members() {
                let mut completion = item(
                    &member.name,
                    admin_member_completion_kind(member.kind),
                    format!("{} • {}", admin_member_detail(&member), component.name),
                );
                if member.kind == VueComponentMemberKind::Method {
                    snippet(&mut completion, format!("{}($0)", member.name));
                }
                mark_admin_completion_deprecated(&mut completion, &member.deprecated);
                if member.kind == VueComponentMemberKind::Prop
                    && let Some(prop) = component.component_prop(&member.name)
                    && !completion.deprecated
                {
                    mark_admin_completion_deprecated(&mut completion, &prop.deprecated);
                }
                items_by_name.insert(member.name.clone(), completion);
            }
        }
        for member in vue_builtin_members() {
            if items_by_name.contains_key(&member.name) {
                continue;
            }
            let mut completion = item(
                &member.name,
                admin_member_completion_kind(member.kind),
                "Vue instance member",
            );
            if member.kind == VueComponentMemberKind::Method {
                snippet(&mut completion, format!("{}($0)", member.name));
            }
            items_by_name.insert(member.name.clone(), completion);
        }
        Some(sorted(items_by_name))
    }

    fn get_mixin_completions(&self) -> Vec<protocol::CompletionItem> {
        let Some(Ok(mixins)) = self.index.as_ref().map(|index| index.get_all_mixins()) else {
            return Vec::new();
        };
        mixins
            .iter()
            .map(|mixin| {
                item(
                    &mixin.name,
                    protocol::CompletionItemKind::REFERENCE,
                    "Shopware Administration mixin",
                )
            })
            .collect()
    }

    /// Go `getDirectiveCompletions`; `markup` prefixes `v-` and scopes the
    /// list to `template_path`.
    pub(super) fn get_directive_completions(
        &self,
        markup: bool,
        template_path: &str,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let directives = if markup && !template_path.is_empty() {
            index.get_all_directives_for_template(template_path)
        } else {
            index.get_all_directives()
        };
        let Ok(directives) = directives else {
            return Vec::new();
        };
        sorted(
            directives
                .into_iter()
                .map(|directive| {
                    let label = if markup {
                        format!("v-{}", directive.name)
                    } else {
                        directive.name
                    };
                    let completion = item(
                        &label,
                        protocol::CompletionItemKind::FUNCTION,
                        "Shopware Administration Vue directive",
                    );
                    (label, completion)
                })
                .collect(),
        )
    }

    fn get_filter_completions(&self) -> Vec<protocol::CompletionItem> {
        let Some(Ok(filters)) = self.index.as_ref().map(|index| index.get_all_filters()) else {
            return Vec::new();
        };
        sorted(
            filters
                .into_iter()
                .map(|filter| {
                    let mut detail = String::from("Shopware Administration filter");
                    if !filter.signature.is_empty() {
                        detail += " · ";
                        detail += &filter.signature;
                    }
                    let completion =
                        item(&filter.name, protocol::CompletionItemKind::FUNCTION, detail);
                    (filter.name, completion)
                })
                .collect(),
        )
    }

    fn get_module_completions(&self) -> Vec<protocol::CompletionItem> {
        let Some(Ok(modules)) = self.index.as_ref().map(|index| index.get_all_modules()) else {
            return Vec::new();
        };
        sorted(
            modules
                .into_iter()
                .map(|module| {
                    let mut detail = String::from("Shopware Administration module");
                    if !module.title.is_empty() {
                        detail += " • ";
                        detail += &module.title;
                    }
                    let completion =
                        item(&module.name, protocol::CompletionItemKind::MODULE, detail);
                    (module.name, completion)
                })
                .collect(),
        )
    }

    pub(super) fn get_module_route_completions(&self) -> Vec<protocol::CompletionItem> {
        let Some(Ok(routes)) = self
            .index
            .as_ref()
            .map(|index| index.get_all_module_routes())
        else {
            return Vec::new();
        };
        routes
            .iter()
            .map(|route| {
                item(
                    &route.name,
                    protocol::CompletionItemKind::REFERENCE,
                    &route.component,
                )
            })
            .collect()
    }

    fn is_module_route_reference(&self, node: &SyntaxNode) -> bool {
        is_javascript_module_route_reference(node)
    }
}

fn arrow_or_parenthesized(member_type: &str) -> bool {
    member_type.contains("=>") || member_type.starts_with('(')
}

/// Members of a resolved type shape; callable members insert `name($0)`.
fn shape_member_items(
    shape: &VueTypeShape,
    detail_prefix: &str,
    callable: impl Fn(&str) -> bool,
) -> Vec<protocol::CompletionItem> {
    let mut items = Vec::with_capacity(shape.members.len());
    for member in &shape.members {
        let is_callable = callable(member.type_.trim());
        let kind = if is_callable {
            protocol::CompletionItemKind::METHOD
        } else {
            protocol::CompletionItemKind::PROPERTY
        };
        let mut detail = detail_prefix.to_owned();
        if !member.type_.is_empty() {
            detail += " · ";
            detail += &member.type_;
        }
        let mut completion = item(&member.name, kind, detail);
        if is_callable {
            snippet(&mut completion, format!("{}($0)", member.name));
        }
        items.push(completion);
    }
    sort_completion_items(&mut items);
    items
}

fn application_container_name_completions() -> Vec<protocol::CompletionItem> {
    sorted_items(
        application_containers()
            .into_iter()
            .map(|container| {
                item(
                    &container.name,
                    protocol::CompletionItemKind::VALUE,
                    format!("{} · {}", container.description, container.interface_name),
                )
            })
            .collect(),
    )
}
