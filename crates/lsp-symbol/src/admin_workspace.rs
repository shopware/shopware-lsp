//! Administration registry workspace symbols.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_admin::{
    AdminComponentIndexer, AdminPrivilegeKind, AdminStore, AdminStoreMemberKind,
    ComponentRegistrationKind, VueComponent, camel_to_kebab, canonical_event_name,
};
use swlsp_lsp::{Context, ProviderResult, WorkspaceSymbolProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::SymbolKind;
use swlsp_textutil::lowercase;

use crate::{line_location, lower_contains};

/// Exposes the registry-like Administration declarations that do not have
/// equivalents in the PHP symbol graph.
pub struct AdminWorkspaceSymbolProvider {
    index: Option<Arc<AdminComponentIndexer>>,
}

impl AdminWorkspaceSymbolProvider {
    pub fn new(index: Option<Arc<AdminComponentIndexer>>) -> Self {
        Self { index }
    }
}

impl WorkspaceSymbolProvider for AdminWorkspaceSymbolProvider {
    fn workspace_symbols(
        &self,
        ctx: &Context,
        query: &str,
    ) -> ProviderResult<Vec<protocol::SymbolInformation>> {
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        let mut collector = Collector {
            ctx,
            index,
            query: lowercase(query.trim()),
            result: Vec::new(),
            seen_api: HashSet::new(),
        };
        collector.components()?;
        collector.component_api()?;
        collector.mixins()?;
        collector.directives()?;
        collector.filters()?;
        collector.cms()?;
        collector.modules()?;
        collector.services()?;
        collector.stores()?;
        collector.privileges()?;
        Ok(collector.result)
    }
}

struct Collector<'a> {
    ctx: &'a Context,
    index: &'a AdminComponentIndexer,
    query: String,
    result: Vec<protocol::SymbolInformation>,
    seen_api: HashSet<(String, String, u32, i64)>,
}

fn first_path<'p>(paths: &[&'p str]) -> &'p str {
    paths
        .iter()
        .copied()
        .find(|path| !path.is_empty())
        .unwrap_or("")
}

impl Collector<'_> {
    fn append(
        &mut self,
        name: &str,
        container: String,
        path: &str,
        line: u32,
        kind: SymbolKind,
        aliases: &[&str],
    ) {
        if name.is_empty() || path.is_empty() {
            return;
        }
        if !self.query.is_empty() {
            let mut search = format!("{name} {container}");
            if !aliases.is_empty() {
                search.push(' ');
                search.push_str(&aliases.join(" "));
            }
            if !lower_contains(&search, &self.query) {
                return;
            }
        }
        self.result.push(protocol::SymbolInformation {
            name: name.to_owned(),
            kind,
            location: line_location(path, i64::from(line)),
            container_name: container,
            priority: 0,
        });
    }

    fn append_api(
        &mut self,
        name: &str,
        container: String,
        path: &str,
        line: u32,
        kind: SymbolKind,
        aliases: &[&str],
    ) {
        if !self.seen_api.insert((
            name.to_owned(),
            path.to_owned(),
            line,
            crate::symbol_kind_number(kind),
        )) {
            return;
        }
        self.append(name, container, path, line, kind, aliases);
    }

    fn components(&mut self) -> ProviderResult<()> {
        let components = self.index.get_all_components_view()?;
        for component in components.iter() {
            self.ctx.check()?;
            let mut container = "Shopware Administration component".to_owned();
            match component.kind {
                ComponentRegistrationKind::Override => container.push_str(" override"),
                ComponentRegistrationKind::Extend => container.push_str(" extension"),
                _ => {}
            }
            self.append(
                &component.name,
                container,
                &component.file_path,
                component.line,
                SymbolKind::CLASS,
                &[],
            );
        }
        Ok(())
    }

    fn component_api(&mut self) -> ProviderResult<()> {
        let mut names = self.index.get_all_component_names()?;
        names.sort();
        for name in &names {
            if let Some(component) = self.index.get_effective_component(name)? {
                self.one_component_api(&component);
            }
        }
        Ok(())
    }

    fn one_component_api(&mut self, component: &VueComponent) {
        for prop in &component.props {
            let path = first_path(&[
                &prop.file_path,
                &component.definition_path,
                &component.file_path,
            ]);
            let alias = camel_to_kebab(&prop.name);
            self.append_api(
                &prop.name,
                format!("{} · component prop", component.name),
                path,
                prop.line,
                SymbolKind::PROPERTY,
                &[&alias],
            );
        }
        for event in component.component_events() {
            let path = first_path(&[
                &event.file_path,
                &component.definition_path,
                &component.file_path,
            ]);
            let name = canonical_event_name(&event.name);
            let alias = format!("@{name}");
            self.append_api(
                &name,
                format!("{} · component event", component.name),
                path,
                event.line,
                SymbolKind::EVENT,
                &[&alias],
            );
        }
        for model in component.component_models() {
            let path = first_path(&[
                &model.prop.file_path,
                &component.definition_path,
                &component.file_path,
            ]);
            self.append_api(
                &model.attribute_name,
                format!("{} · component model", component.name),
                path,
                model.prop.line,
                SymbolKind::PROPERTY,
                &[&model.prop_name, &model.event_name],
            );
        }
        for slot in &component.slots {
            let name = slot.display_name();
            let path = first_path(&[&slot.file_path, &component.template_path]);
            let alias = format!("#{name}");
            self.append_api(
                &name,
                format!("{} · component slot", component.name),
                path,
                slot.line,
                SymbolKind::PROPERTY,
                &[&alias],
            );
        }
        for directive in &component.local_directives {
            self.append(
                &format!("v-{}", directive.name),
                format!("{} · local Vue directive", component.name),
                &directive.file_path,
                directive.line,
                SymbolKind::FUNCTION,
                &[],
            );
        }
    }

    fn mixins(&mut self) -> ProviderResult<()> {
        for value in self.index.get_all_mixins()? {
            self.append(
                &value.name,
                "Shopware Administration mixin".to_owned(),
                &value.file_path,
                value.line,
                SymbolKind::OBJECT,
                &[],
            );
        }
        Ok(())
    }

    fn directives(&mut self) -> ProviderResult<()> {
        for value in self.index.get_all_directives()? {
            self.append(
                &format!("v-{}", value.name),
                "Shopware Administration Vue directive".to_owned(),
                &value.file_path,
                value.line,
                SymbolKind::FUNCTION,
                &[],
            );
        }
        Ok(())
    }

    fn filters(&mut self) -> ProviderResult<()> {
        for value in self.index.get_all_filters()? {
            self.append(
                &value.name,
                "Shopware Administration filter".to_owned(),
                &value.file_path,
                value.line,
                SymbolKind::FUNCTION,
                &[],
            );
        }
        Ok(())
    }

    fn cms(&mut self) -> ProviderResult<()> {
        for value in self.index.get_all_cms_registrations()? {
            self.append(
                &value.name,
                format!("Shopware CMS {}", value.kind.as_str()),
                &value.file_path,
                value.line,
                SymbolKind::OBJECT,
                &[],
            );
        }
        Ok(())
    }

    fn modules(&mut self) -> ProviderResult<()> {
        for module in self.index.get_all_modules()? {
            self.ctx.check()?;
            self.append(
                &module.name,
                "Shopware Administration module".to_owned(),
                &module.file_path,
                module.line,
                SymbolKind::MODULE,
                &[],
            );
            for route in &module.routes {
                self.append(
                    &route.name,
                    module.name.clone(),
                    &module.file_path,
                    route.line,
                    SymbolKind::FUNCTION,
                    &[],
                );
            }
        }
        Ok(())
    }

    fn services(&mut self) -> ProviderResult<()> {
        for service in self.index.get_all_services()? {
            self.ctx.check()?;
            let mut container = "Shopware Administration service".to_owned();
            if !service.kind.is_unset() {
                container.push_str(" · ");
                container.push_str(service.kind.as_str());
            }
            self.append(
                &service.name,
                container,
                &service.file_path,
                service.line,
                SymbolKind::OBJECT,
                &[],
            );
        }
        Ok(())
    }

    fn stores(&mut self) -> ProviderResult<()> {
        let stores = self.index.get_all_stores()?;
        let mut seen = HashSet::with_capacity(stores.len());
        for raw in &stores {
            self.ctx.check()?;
            if !seen.insert(raw.name.clone()) {
                continue;
            }
            for store in self.index.get_store(&raw.name)? {
                self.store(&store);
            }
        }
        Ok(())
    }

    fn store(&mut self, store: &AdminStore) {
        self.append(
            &store.name,
            "Shopware Administration store".to_owned(),
            &store.file_path,
            store.line,
            SymbolKind::OBJECT,
            &[],
        );
        for member in &store.members {
            let kind = match member.kind {
                AdminStoreMemberKind::Getter => SymbolKind::PROPERTY,
                AdminStoreMemberKind::Action => SymbolKind::METHOD,
                _ => SymbolKind::FIELD,
            };
            self.append(
                &member.name,
                format!("Shopware Administration store · {}", store.name),
                first_path(&[&member.file_path, &store.file_path]),
                member.line,
                kind,
                &[],
            );
        }
    }

    fn privileges(&mut self) -> ProviderResult<()> {
        for privilege in self.index.get_all_privileges()? {
            self.ctx.check()?;
            let (mut container, kind) = if privilege.kind == AdminPrivilegeKind::Role {
                (
                    "Shopware Administration ACL role".to_owned(),
                    SymbolKind::ENUM_MEMBER,
                )
            } else {
                (
                    "Shopware Administration ACL permission".to_owned(),
                    SymbolKind::CONSTANT,
                )
            };
            if !privilege.mapping_key.is_empty() {
                container.push_str(" · ");
                container.push_str(&privilege.mapping_key);
            }
            self.append(
                &privilege.name,
                container,
                &privilege.file_path,
                privilege.line,
                kind,
                &[],
            );
        }
        Ok(())
    }
}
