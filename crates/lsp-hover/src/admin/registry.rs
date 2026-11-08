//! Administration registry hovers: services, stores, privileges, modules,
//! mixins, directives, filters, CMS registrations, and `this` members.

use std::collections::HashSet;
use std::fmt::Write as _;

use swlsp_admin::{
    AdminCMSRegistrationKind, AdminPrivilegeKind, AdminSymbolTarget, VueComponentMemberKind,
};

use super::{AdminHoverProvider, HoverResult, sections_hover};
use crate::markdown_hover;

impl AdminHoverProvider {
    pub(crate) fn service_hover(&self, name: &str) -> HoverResult {
        let services = self.index.get_service(name)?;
        let sections = services
            .iter()
            .map(|service| {
                let mut value = format!(
                    "**Administration service** `{}`\n\nRegistered in `{}:{}`.",
                    service.name,
                    self.make_relative_path(&service.file_path),
                    service.line
                );
                if !service.implementation_path.is_empty() {
                    let _ = write!(
                        value,
                        "\n\nImplementation: `{}`.",
                        self.make_relative_path(&service.implementation_path)
                    );
                }
                value
            })
            .collect();
        Ok(sections_hover(sections))
    }

    pub(crate) fn store_hover(&self, name: &str) -> HoverResult {
        let stores = self.index.get_store(name)?;
        let sections = stores
            .iter()
            .map(|store| {
                let mut value = format!(
                    "**Administration store** `{}`\n\nRegistered in `{}:{}`.",
                    store.name,
                    self.make_relative_path(&store.file_path),
                    store.line
                );
                if !store.members.is_empty() {
                    let _ = write!(value, "\n\nIndexed members: {}.", store.members.len());
                }
                value
            })
            .collect();
        Ok(sections_hover(sections))
    }

    #[doc(hidden)]
    pub fn privilege_hover(&self, name: &str) -> HoverResult {
        let privileges = self.index.get_privilege(name)?;
        let sections = privileges
            .iter()
            .map(|privilege| {
                if privilege.is_builtin() {
                    return format!(
                        "**Built-in Administration privilege** `{}`\n\nProvided by Shopware for administrator-only access.",
                        privilege.name
                    );
                }
                let mut kind = "Administration privilege role";
                let mut owner = privilege.mapping_key.clone();
                if privilege.kind == AdminPrivilegeKind::Permission {
                    kind = "Administration permission";
                    if !privilege.role.is_empty() {
                        owner.push('.');
                        owner.push_str(&privilege.role);
                    }
                }
                let mut value = format!("**{kind}** `{}`", privilege.name);
                if !owner.is_empty() {
                    let _ = write!(value, "\n\nDeclared by `{owner}`.");
                }
                let _ = write!(
                    value,
                    "\n\nDefined in `{}:{}`.",
                    self.make_relative_path(&privilege.file_path),
                    privilege.line
                );
                value
            })
            .collect();
        Ok(sections_hover(sections))
    }

    pub(crate) fn module_route_hover(&self, name: &str) -> HoverResult {
        let Some((module, route)) = self.index.get_module_route(name)? else {
            return Ok(None);
        };
        let mut value = format!(
            "**Administration module route** `{}`\n\nModule: `{}`",
            route.name, module.name
        );
        if !route.path.is_empty() {
            let _ = write!(value, "\n\nPath: `{}`", route.path);
        }
        if !route.component.is_empty() {
            let _ = write!(value, "\n\nComponent: `{}`", route.component);
        }
        let _ = write!(
            value,
            "\n\nDefined in `{}:{}`.",
            self.make_relative_path(&module.file_path),
            route.line
        );
        Ok(Some(markdown_hover(value, None)))
    }

    pub(crate) fn mixin_hover(&self, name: &str) -> HoverResult {
        let mixins = self.index.get_mixin(name)?;
        let sections = mixins
            .iter()
            .map(|mixin| {
                let mut value = format!(
                    "**Administration mixin** `{}`\n\nDefined in `{}:{}`.",
                    mixin.name,
                    self.make_relative_path(&mixin.file_path),
                    mixin.line
                );
                let definition = &mixin.definition;
                let mut member_count = definition.members.len();
                if member_count == 0 {
                    member_count = definition.props.len()
                        + definition.data.len()
                        + definition.computed.len()
                        + definition.methods.len()
                        + definition.injected.len();
                }
                if member_count > 0 {
                    let _ = write!(value, "\n\nIndexed members: {member_count}.");
                }
                value
            })
            .collect();
        Ok(sections_hover(sections))
    }

    pub(crate) fn directive_hover(&self, name: &str) -> HoverResult {
        let directives = self.index.get_directive(name)?;
        let sections = directives
            .iter()
            .map(|directive| {
                format!(
                    "**Administration Vue directive** `v-{}`\n\nDefined in `{}:{}`.",
                    directive.name,
                    self.make_relative_path(&directive.file_path),
                    directive.line
                )
            })
            .collect();
        Ok(sections_hover(sections))
    }

    pub(crate) fn filter_hover(&self, name: &str) -> HoverResult {
        let filters = self.index.get_filter(name)?;
        let sections = filters
            .iter()
            .map(|filter| {
                let mut value = format!("**Administration filter** `{}`", filter.name);
                if !filter.signature.is_empty() {
                    let _ = write!(value, "\n\n```typescript\n{}\n```", filter.signature);
                }
                let _ = write!(
                    value,
                    "\n\nDefined in `{}:{}`.",
                    self.make_relative_path(&filter.file_path),
                    filter.line
                );
                value
            })
            .collect();
        Ok(sections_hover(sections))
    }

    pub(crate) fn cms_hover(&self, kind: AdminCMSRegistrationKind, name: &str) -> HoverResult {
        let registrations = self.index.get_cms_registration(kind, name)?;
        let sections = registrations
            .iter()
            .map(|registration| {
                let mut value =
                    format!("**Shopware CMS {}** `{}`", kind.as_str(), registration.name);
                if !registration.label.is_empty() {
                    let _ = write!(value, "\n\nLabel: `{}`.", registration.label);
                }
                if !registration.category.is_empty() {
                    let _ = write!(value, "\n\nCategory: `{}`.", registration.category);
                }
                for (label, component) in [
                    ("Component", &registration.component),
                    ("Configuration component", &registration.config_component),
                    ("Preview component", &registration.preview_component),
                ] {
                    if !component.is_empty() {
                        let _ = write!(value, "\n\n{label}: `{component}`.");
                    }
                }
                if !registration.slots.is_empty() {
                    let _ = write!(value, "\n\nIndexed slots: {}.", registration.slots.len());
                }
                let _ = write!(
                    value,
                    "\n\nDefined in `{}:{}`.",
                    self.make_relative_path(&registration.file_path),
                    registration.line
                );
                value
            })
            .collect();
        Ok(sections_hover(sections))
    }

    pub(crate) fn directive_hover_for_template(
        &self,
        name: &str,
        template_path: &str,
    ) -> HoverResult {
        let directives = self.index.get_directive_for_template(template_path, name)?;
        let sections = directives
            .iter()
            .map(|directive| {
                let kind = if directive.local {
                    "Component-local Administration Vue directive"
                } else {
                    "Administration Vue directive"
                };
                format!(
                    "**{kind}** `v-{}`\n\nDefined in `{}:{}`.",
                    directive.name,
                    self.make_relative_path(&directive.file_path),
                    directive.line
                )
            })
            .collect();
        Ok(sections_hover(sections))
    }

    pub(crate) fn directive_hover_target(&self, target: &AdminSymbolTarget) -> HoverResult {
        if target.owner.is_empty() {
            return self.directive_hover(&target.name);
        }
        for component in self
            .index
            .get_components_by_definition_path(&target.owner)?
        {
            if let Some(local) = component.local_directive(&target.name) {
                return Ok(Some(markdown_hover(
                    format!(
                        "**Component-local Administration Vue directive** `v-{}`\n\nDefined in `{}:{}`.",
                        local.name,
                        self.make_relative_path(&local.file_path),
                        local.line
                    ),
                    None,
                )));
            }
        }
        Ok(None)
    }

    pub(crate) fn module_hover(&self, name: &str) -> HoverResult {
        let modules = self.index.get_module(name)?;
        let sections = modules
            .iter()
            .map(|module| {
                let mut value = format!("**Administration module** `{}`", module.name);
                if !module.title.is_empty() {
                    let _ = write!(value, "\n\nTitle: `{}`.", module.title);
                }
                if !module.type_.is_empty() {
                    let _ = write!(value, "\n\nType: `{}`.", module.type_);
                }
                if !module.display_name.is_empty() {
                    let _ = write!(value, "\n\nName: `{}`.", module.display_name);
                }
                let _ = write!(value, "\n\nIndexed routes: {}.", module.routes.len());
                let _ = write!(
                    value,
                    "\n\nDefined in `{}:{}`.",
                    self.make_relative_path(&module.file_path),
                    module.line
                );
                value
            })
            .collect();
        Ok(sections_hover(sections))
    }

    pub(crate) fn store_member_hover(&self, store_name: &str, member_name: &str) -> HoverResult {
        let stores = self.index.get_store(store_name)?;
        let mut sections = Vec::new();
        for store in &stores {
            let Some(member) = store.member(member_name) else {
                continue;
            };
            let mut value = format!("**{}** `{}`", member.kind.as_str(), member.name);
            if !member.type_.is_empty() {
                let _ = write!(value, ": `{}`", member.type_);
            }
            let _ = write!(
                value,
                "\n\nMember of Administration store `{}`.\n\nDefined in `{}:{}`.",
                store.name,
                self.make_relative_path(&member.file_path),
                member.line
            );
            sections.push(value);
        }
        Ok(sections_hover(sections))
    }

    #[doc(hidden)]
    pub fn this_member_hover(&self, uri: &str, name: &str) -> HoverResult {
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Ok(None);
        };
        let components = self.index.get_components_by_definition_path(&path)?;
        if components.is_empty() {
            return Ok(None);
        }
        let mut sections = Vec::new();
        let mut seen = HashSet::new();
        for component in &components {
            let Some(member) = component.template_member(name) else {
                continue;
            };
            let key = format!(
                "{}\x00{}\x00{}",
                component.name,
                member.kind.as_str(),
                member.name
            );
            if !seen.insert(key) {
                continue;
            }
            let mut value = format!("**{}** `{}`", member.kind.as_str(), member.name);
            if !member.type_.is_empty() {
                let _ = write!(value, ": `{}`", member.type_);
            }
            let _ = write!(value, "\n\nVue instance member of `{}`.", component.name);
            if !member.deprecated.is_empty() {
                let _ = write!(value, "\n\n**Deprecated:** {}", member.deprecated);
            } else if member.kind == VueComponentMemberKind::Prop
                && let Some(prop) = component.component_prop(&member.name)
                && !prop.deprecated.is_empty()
            {
                let _ = write!(value, "\n\n**Deprecated:** {}", prop.deprecated);
            }
            if !member.file_path.is_empty() {
                let _ = write!(
                    value,
                    "\n\nDefined in `{}`.",
                    self.make_relative_path(&member.file_path)
                );
            }
            sections.push(value);
        }
        if sections.is_empty() {
            if swlsp_admin::vue_builtin_members()
                .iter()
                .any(|builtin| builtin.name == name)
            {
                return Ok(Some(markdown_hover(
                    format!("**Vue instance member** `{name}`"),
                    None,
                )));
            }
            return Ok(None);
        }
        Ok(sections_hover(sections))
    }
}
