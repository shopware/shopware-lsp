//! JavaScript symbol resolution against the index.

use crate::*;

impl AdminComponentIndexer {
    /// Resolves the Administration symbol under a JavaScript node, using the
    /// index for component contracts declared in `file_path`.
    pub fn javascript_symbol_at(
        &self,
        file_path: &str,
        node: &SyntaxNode,
    ) -> IndexResult<Option<AdminSymbolTarget>> {
        if let Some(target) = self.javascript_component_prop_at(file_path, node)? {
            return Ok(Some(target));
        }
        if let Some(target) = self.javascript_component_event_at(file_path, node)? {
            return Ok(Some(target));
        }
        if let Some(target) = self.javascript_definition_symbol_at(file_path, node)? {
            return Ok(Some(target));
        }
        if let Some(target) = javascript_symbol_at(node) {
            return Ok(Some(target));
        }
        self.javascript_this_member_at(file_path, node)
    }

    pub(crate) fn javascript_component_prop_at(
        &self,
        file_path: &str,
        node: &SyntaxNode,
    ) -> IndexResult<Option<AdminSymbolTarget>> {
        let Some(name) = javascript_component_prop_at(node) else {
            return Ok(None);
        };
        for component in self.get_components_by_definition_path(file_path)? {
            if let Some(prop) = component.component_prop(&name) {
                return Ok(Some(AdminSymbolTarget {
                    kind: AdminSymbolKind::ComponentProp,
                    owner: admin_component_symbol_owner(&[
                        &prop.file_path,
                        &component.definition_path,
                        file_path,
                    ]),
                    name: prop.name.clone(),
                }));
            }
        }
        Ok(None)
    }

    pub(crate) fn javascript_component_event_at(
        &self,
        file_path: &str,
        node: &SyntaxNode,
    ) -> IndexResult<Option<AdminSymbolTarget>> {
        let Some(name) = javascript_component_event_at(node) else {
            return Ok(None);
        };
        for component in self.get_components_by_definition_path(file_path)? {
            if let Some(event) = component.component_event(&name) {
                return Ok(Some(AdminSymbolTarget {
                    kind: AdminSymbolKind::ComponentEvent,
                    owner: admin_component_symbol_owner(&[
                        &event.file_path,
                        &component.definition_path,
                        file_path,
                    ]),
                    name: canonical_event_name(&event.name),
                }));
            }
        }
        Ok(None)
    }

    pub(crate) fn javascript_definition_symbol_at(
        &self,
        file_path: &str,
        node: &SyntaxNode,
    ) -> IndexResult<Option<AdminSymbolTarget>> {
        let root = node.ancestors().last().unwrap_or_else(|| node.clone());
        let line_index = LineIndex::new(root.text_string());
        let range = node.range_trimmed_trivia();
        if let Some(event) =
            self.shopware_event_bus_event_at_definition_range(file_path, range, &line_index)?
        {
            return Ok(Some(AdminSymbolTarget {
                kind: AdminSymbolKind::EventBusEvent,
                name: event.name,
                ..Default::default()
            }));
        }
        let (line, character) = line_index.position_utf16(u32::from(range.start()));
        if let Some((_, directive)) =
            self.get_local_directive_at_definition_position(file_path, line, character)?
        {
            return Ok(Some(AdminSymbolTarget {
                kind: AdminSymbolKind::Directive,
                owner: directive.file_path,
                name: directive.name,
            }));
        }
        Ok(self
            .get_component_member_at_definition_position(file_path, line, character)?
            .map(|member| component_member_target(&member)))
    }

    pub(crate) fn javascript_this_member_at(
        &self,
        file_path: &str,
        node: &SyntaxNode,
    ) -> IndexResult<Option<AdminSymbolTarget>> {
        let Some(name) = jsquery::this_member(node).filter(|name| !name.is_empty()) else {
            return Ok(None);
        };
        for component in self.get_components_by_definition_path(file_path)? {
            if let Some(target) = this_component_target(&component, &name, file_path) {
                return Ok(Some(target));
            }
        }
        self.declared_this_member_target(file_path, &name)
    }

    pub(crate) fn declared_this_member_target(
        &self,
        file_path: &str,
        name: &str,
    ) -> IndexResult<Option<AdminSymbolTarget>> {
        let declared = self.component_members_declared_in(file_path)?;
        let mut target: Option<&VueComponentMember> = None;
        for member in &declared {
            if member.name != name || !member.renameable() {
                continue;
            }
            if let Some(current) = target
                && current.source_identity() != member.source_identity()
            {
                return Ok(None);
            }
            target = Some(member);
        }
        Ok(target.map(component_member_target))
    }
}

pub(crate) fn admin_component_symbol_owner(paths: &[&str]) -> String {
    paths
        .iter()
        .find(|path| !path.is_empty())
        .map_or_else(String::new, |path| (*path).to_owned())
}

pub(crate) fn this_component_target(
    component: &VueComponent,
    name: &str,
    file_path: &str,
) -> Option<AdminSymbolTarget> {
    if let Some(prop) = component.component_prop(name) {
        return Some(AdminSymbolTarget {
            kind: AdminSymbolKind::ComponentProp,
            owner: admin_component_symbol_owner(&[
                &prop.file_path,
                &component.definition_path,
                file_path,
            ]),
            name: prop.name.clone(),
        });
    }
    if component.injected.iter().any(|injected| injected == name) {
        return Some(AdminSymbolTarget {
            kind: AdminSymbolKind::Service,
            name: name.to_owned(),
            ..Default::default()
        });
    }
    component
        .template_member(name)
        .filter(VueComponentMember::renameable)
        .map(|member| component_member_target(&member))
}
