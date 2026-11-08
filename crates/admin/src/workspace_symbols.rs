//! Workspace-symbol publication (port of Go `workspace_symbols.go`).

use swlsp_indexer::{
    WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK, WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
    WorkspaceSymbol, WorkspaceSymbolKind, WorkspaceSymbolPosition, WorkspaceSymbolRange,
};

use crate::*;

pub(crate) fn admin_workspace_range(source: &AdminSourceRange, line: u32) -> WorkspaceSymbolRange {
    if source.end_line > source.start_line || source.end_character > source.start_character {
        return WorkspaceSymbolRange {
            start: WorkspaceSymbolPosition {
                line: source.start_line as i32,
                character: source.start_character as i32,
            },
            end: WorkspaceSymbolPosition {
                line: source.end_line as i32,
                character: source.end_character as i32,
            },
        };
    }
    WorkspaceSymbolRange::at_line(line as i32)
}

fn at_line(line: u32) -> WorkspaceSymbolRange {
    WorkspaceSymbolRange::at_line(line as i32)
}

pub(crate) fn add_admin_component_workspace_symbols(
    file: &mut AdminWrites,
    components: &[VueComponent],
) {
    let mut result = Vec::new();
    for component in components {
        if component.name.is_empty() || component.file_path.is_empty() {
            continue;
        }
        let mut container = "Shopware Administration component".to_owned();
        match component.kind {
            ComponentRegistrationKind::Override => container.push_str(" override"),
            ComponentRegistrationKind::Extend => container.push_str(" extension"),
            _ => {}
        }
        result.push(WorkspaceSymbol {
            name: component.name.clone(),
            container_name: container,
            aliases: vec![component.target_component.clone()],
            path: component.file_path.clone(),
            domain: "admin.component".to_owned(),
            kind: WorkspaceSymbolKind::CLASS,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: at_line(component.line),
        });
        for prop in &component.props {
            let path = if prop.file_path.is_empty() {
                component.definition_path.clone()
            } else {
                prop.file_path.clone()
            };
            result.push(WorkspaceSymbol {
                name: prop.name.clone(),
                container_name: format!("{} · component prop", component.name),
                aliases: vec![camel_to_kebab(&prop.name)],
                path,
                domain: "admin.component.prop".to_owned(),
                kind: WorkspaceSymbolKind::PROPERTY,
                priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
                range: admin_workspace_range(&prop.name_range, prop.line),
            });
        }
        for event in component.component_events() {
            let name = canonical_event_name(&event.name);
            result.push(WorkspaceSymbol {
                name: name.clone(),
                container_name: format!("{} · component event", component.name),
                aliases: vec![format!("@{name}")],
                path: event.file_path.clone(),
                domain: "admin.component.event".to_owned(),
                kind: WorkspaceSymbolKind::EVENT,
                priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
                range: admin_workspace_range(&event.name_range, event.line),
            });
        }
        for slot in &component.slots {
            let name = slot.display_name();
            result.push(WorkspaceSymbol {
                name: name.clone(),
                container_name: format!("{} · component slot", component.name),
                aliases: vec![format!("#{name}")],
                path: slot.file_path.clone(),
                domain: "admin.component.slot".to_owned(),
                kind: WorkspaceSymbolKind::PROPERTY,
                priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
                range: admin_workspace_range(&slot.name_range, slot.line),
            });
        }
        for directive in &component.local_directives {
            result.push(WorkspaceSymbol {
                name: format!("v-{}", directive.name),
                container_name: format!("{} · local Vue directive", component.name),
                aliases: Vec::new(),
                path: directive.file_path.clone(),
                domain: "admin.component.directive".to_owned(),
                kind: WorkspaceSymbolKind::FUNCTION,
                priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
                range: admin_workspace_range(&directive.name_range, directive.line),
            });
        }
    }
    file.add_workspace_symbols(result);
}

pub(crate) fn add_admin_mixins_and_modules_workspace_symbols(
    file: &mut AdminWrites,
    mixins: &[AdminMixin],
    modules: &[AdminModule],
) {
    let mut result = Vec::new();
    for mixin in mixins {
        result.push(WorkspaceSymbol {
            name: mixin.name.clone(),
            container_name: "Shopware Administration mixin".to_owned(),
            aliases: Vec::new(),
            path: mixin.file_path.clone(),
            domain: "admin.mixin".to_owned(),
            kind: WorkspaceSymbolKind::OBJECT,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: at_line(mixin.line),
        });
    }
    for module in modules {
        result.push(WorkspaceSymbol {
            name: module.name.clone(),
            container_name: "Shopware Administration module".to_owned(),
            aliases: vec![module.display_name.clone(), module.title.clone()],
            path: module.file_path.clone(),
            domain: "admin.module".to_owned(),
            kind: WorkspaceSymbolKind::MODULE,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: at_line(module.line),
        });
        for route in &module.routes {
            result.push(WorkspaceSymbol {
                name: route.name.clone(),
                container_name: module.name.clone(),
                aliases: vec![
                    route.local_name.clone(),
                    route.path.clone(),
                    route.component.clone(),
                ],
                path: module.file_path.clone(),
                domain: "admin.module.route".to_owned(),
                kind: WorkspaceSymbolKind::FUNCTION,
                priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
                range: at_line(route.line),
            });
        }
    }
    file.add_workspace_symbols(result);
}

pub(crate) fn add_admin_runtime_workspace_symbols(
    file: &mut AdminWrites,
    services: &[AdminService],
    stores: &[AdminStore],
    directives: &[AdminDirective],
    filters: &[AdminFilter],
    cms: &[AdminCMSRegistration],
    privileges: &[AdminPrivilege],
) {
    let mut result = Vec::new();
    for service in services {
        result.push(WorkspaceSymbol {
            name: service.name.clone(),
            container_name: format!(
                "Shopware Administration service · {}",
                service.kind.as_str()
            ),
            aliases: vec![service.implementation_name.clone()],
            path: service.file_path.clone(),
            domain: "admin.service".to_owned(),
            kind: WorkspaceSymbolKind::OBJECT,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: at_line(service.line),
        });
    }
    for store in stores {
        result.push(WorkspaceSymbol {
            name: store.name.clone(),
            container_name: "Shopware Administration store".to_owned(),
            aliases: Vec::new(),
            path: store.file_path.clone(),
            domain: "admin.store".to_owned(),
            kind: WorkspaceSymbolKind::OBJECT,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: at_line(store.line),
        });
        for member in &store.members {
            let kind = match member.kind {
                AdminStoreMemberKind::Getter => WorkspaceSymbolKind::PROPERTY,
                AdminStoreMemberKind::Action => WorkspaceSymbolKind::METHOD,
                _ => WorkspaceSymbolKind::FIELD,
            };
            result.push(WorkspaceSymbol {
                name: member.name.clone(),
                container_name: format!("Shopware Administration store · {}", store.name),
                aliases: Vec::new(),
                path: member.file_path.clone(),
                domain: "admin.store.member".to_owned(),
                kind,
                priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
                range: at_line(member.line),
            });
        }
    }
    for directive in directives {
        result.push(WorkspaceSymbol {
            name: format!("v-{}", directive.name),
            container_name: "Shopware Administration Vue directive".to_owned(),
            aliases: Vec::new(),
            path: directive.file_path.clone(),
            domain: "admin.directive".to_owned(),
            kind: WorkspaceSymbolKind::FUNCTION,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: admin_workspace_range(&directive.name_range, directive.line),
        });
    }
    for filter in filters {
        result.push(WorkspaceSymbol {
            name: filter.name.clone(),
            container_name: "Shopware Administration filter".to_owned(),
            aliases: Vec::new(),
            path: filter.file_path.clone(),
            domain: "admin.filter".to_owned(),
            kind: WorkspaceSymbolKind::FUNCTION,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: admin_workspace_range(&filter.name_range, filter.line),
        });
    }
    for registration in cms {
        result.push(WorkspaceSymbol {
            name: registration.name.clone(),
            container_name: format!("Shopware CMS {}", registration.kind.as_str()),
            aliases: Vec::new(),
            path: registration.file_path.clone(),
            domain: "admin.cms".to_owned(),
            kind: WorkspaceSymbolKind::OBJECT,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: admin_workspace_range(&registration.name_range, registration.line),
        });
    }
    for privilege in privileges {
        let (kind, container) = if privilege.kind == AdminPrivilegeKind::Role {
            (
                WorkspaceSymbolKind::ENUM_MEMBER,
                "Shopware Administration ACL role",
            )
        } else {
            (
                WorkspaceSymbolKind::CONSTANT,
                "Shopware Administration ACL permission",
            )
        };
        result.push(WorkspaceSymbol {
            name: privilege.name.clone(),
            container_name: container.to_owned(),
            aliases: vec![privilege.mapping_key.clone()],
            path: privilege.file_path.clone(),
            domain: "admin.privilege".to_owned(),
            kind,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: at_line(privilege.line),
        });
    }
    file.add_workspace_symbols(result);
}
