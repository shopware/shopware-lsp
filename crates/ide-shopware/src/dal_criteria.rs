//! The entity of a Criteria that Administration code inherits from the
//! component it overrides or extends.

use std::sync::Arc;

use swlsp_admin::AdminComponentIndexer;
use swlsp_shopware_dal::{Definition, Index};
use swlsp_syntax::SyntaxNode;

/// How many `Component.extend` parents are followed.
const MAX_PARENTS: usize = 4;

/// Resolves Criteria field paths, across component files where needed.
#[derive(Clone)]
pub(crate) struct CriteriaEntities {
    dal: Arc<Index>,
    admin: Option<Arc<AdminComponentIndexer>>,
}

impl CriteriaEntities {
    pub(crate) fn new(dal: Arc<Index>, admin: Option<Arc<AdminComponentIndexer>>) -> Self {
        Self { dal, admin }
    }

    /// The definition whose fields the path segment at `offset` names.
    pub(crate) fn definition<'a>(
        &self,
        definitions: &'a [Definition],
        node: &SyntaxNode,
        offset: u32,
    ) -> Option<&'a Definition> {
        swlsp_shopware_dal::js_criteria_definition(
            definitions,
            node,
            offset,
            &|component, member| self.inherited(component, member),
        )
    }

    /// The entity the member `member` of `component` is searched on, read
    /// from the files that register the component or its parents.
    fn inherited(&self, component: &str, member: &str) -> Option<String> {
        let admin = self.admin.as_deref()?;
        let mut name = component.to_owned();
        for _ in 0..MAX_PARENTS {
            let registrations = admin.get_component(&name).ok()?;
            for registration in &registrations {
                for file in [&registration.definition_path, &registration.file_path] {
                    if file.is_empty() {
                        continue;
                    }
                    if let Ok(Some(entity)) = self.dal.criteria_member_entity(file, member) {
                        return Some(entity);
                    }
                }
            }
            name = registrations
                .iter()
                .map(|registration| registration.extends_component.as_str())
                .find(|parent| !parent.is_empty() && *parent != name)?
                .to_owned();
        }
        None
    }
}
