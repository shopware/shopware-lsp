//! What `this.$super('name')` reaches from one registration.

use std::sync::Arc;

use crate::*;

/// The methods and computed properties `this.$super` can call from a
/// component override or extension.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SuperMembers {
    /// The component the names come from.
    pub component: String,
    pub names: BTreeSet<String>,
    /// False when the list may miss names: an unresolved parent or mixin, a
    /// spread in the configuration, or a base that is not indexed.
    pub complete: bool,
}

impl AdminComponentIndexer {
    /// The override and extend registrations whose configuration `file_path`
    /// owns, in name then line order.
    pub fn get_derived_registrations_by_definition_path(
        &self,
        file_path: &str,
    ) -> IndexResult<Vec<VueComponent>> {
        let normalized = normalize_definition_path(file_path);
        let mut result = Vec::new();
        for component in self.get_components_by_definition_path(file_path)? {
            for registration in self.get_component(&component.name)? {
                let owned = if registration.definition_path.is_empty() {
                    registration.file_path == file_path
                } else {
                    normalize_definition_path(&registration.definition_path) == normalized
                };
                let derived = registration.kind == ComponentRegistrationKind::Override
                    || !registration.extends_component.is_empty();
                if owned && derived {
                    result.push(registration);
                }
            }
        }
        result.sort_by(|left, right| {
            (&left.name, left.line, &left.file_path).cmp(&(
                &right.name,
                right.line,
                &right.file_path,
            ))
        });
        Ok(result)
    }

    /// What `this.$super` reaches from `registration`: for an override the
    /// other registrations of the same component, for an extension the
    /// effective parent. `None` for a registration that derives from nothing.
    pub fn get_super_members(
        &self,
        registration: &VueComponent,
    ) -> IndexResult<Option<SuperMembers>> {
        let mut resolving = HashSet::new();
        let (component, base, mut complete) =
            if registration.kind == ComponentRegistrationKind::Override {
                let (base, complete) = self.overridden_component(registration, &mut resolving)?;
                (registration.name.clone(), base, complete)
            } else if !registration.extends_component.is_empty() {
                let parent =
                    self.effective_component(&registration.extends_component, &mut resolving)?;
                let complete = parent.is_some();
                (
                    registration.extends_component.clone(),
                    parent.map(Arc::unwrap_or_clone).unwrap_or_default(),
                    complete,
                )
            } else {
                return Ok(None);
            };
        let mut names: BTreeSet<String> =
            base.methods.iter().chain(&base.computed).cloned().collect();
        names.extend(
            base.members
                .iter()
                .filter(|member| {
                    matches!(
                        member.kind,
                        VueComponentMemberKind::Method | VueComponentMemberKind::Computed
                    )
                })
                .map(|member| member.name.clone()),
        );
        complete = complete && !base.open_callables && !names.is_empty();
        Ok(Some(SuperMembers {
            component,
            names,
            complete,
        }))
    }

    /// Every registration of the overridden component except `registration`
    /// itself, overlaid; and whether the component's own registration and
    /// all parents were found.
    fn overridden_component(
        &self,
        registration: &VueComponent,
        resolving: &mut HashSet<String>,
    ) -> IndexResult<(VueComponent, bool)> {
        let mut result = VueComponent::default();
        let mut registered = false;
        let mut parents = true;
        resolving.insert(registration.name.clone());
        for other in self.get_component(&registration.name)? {
            if other.file_path == registration.file_path
                && other.definition_path == registration.definition_path
                && other.line == registration.line
            {
                continue;
            }
            registered |= other.kind != ComponentRegistrationKind::Override;
            let mut other = other;
            self.populate_component_definition(&mut other)?;
            let mut own = self.component_with_mixins(&other)?;
            if !own.extends_component.is_empty() {
                match self.effective_component(&own.extends_component.clone(), resolving)? {
                    Some(parent) => own = overlay_components(&parent, &own),
                    None => parents = false,
                }
            }
            result = overlay_components(&result, &own);
        }
        resolving.remove(&registration.name);
        Ok((result, registered && parents))
    }
}
