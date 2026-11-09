//! Registry catalog queries.

use crate::*;

fn directive_from_local(local: &VueLocalDirective) -> AdminDirective {
    AdminDirective {
        name: local.name.clone(),
        file_path: local.file_path.clone(),
        line: local.line,
        name_range: local.name_range.clone(),
        local: true,
    }
}

impl AdminComponentIndexer {
    pub fn get_all_services(&self) -> IndexResult<Vec<AdminService>> {
        let values = self.service_index.get_all_values()?;
        Ok(overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminService| value.file_path.as_str(),
            |document| document.services.as_slice(),
            None,
        ))
    }

    pub fn get_service(&self, name: &str) -> IndexResult<Vec<AdminService>> {
        let values = self.service_index.get_values(name)?;
        let values = overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminService| value.file_path.as_str(),
            |document| document.services.as_slice(),
            Some(&|value: &AdminService| value.name == name),
        );
        Ok(preferred_services(&values))
    }

    pub fn get_all_directives(&self) -> IndexResult<Vec<AdminDirective>> {
        let values = self.directive_index.get_all_values()?;
        Ok(overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminDirective| value.file_path.as_str(),
            |document| document.directives.as_slice(),
            None,
        ))
    }

    pub fn get_all_filters(&self) -> IndexResult<Vec<AdminFilter>> {
        let values = self.filter_index.get_all_values()?;
        let values = overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminFilter| value.file_path.as_str(),
            |document| document.filters.as_slice(),
            None,
        );
        Ok(prefer_runtime_definitions(
            &values,
            |value: &AdminFilter| value.file_path.as_str(),
        ))
    }

    pub fn get_all_cms_registrations(&self) -> IndexResult<Vec<AdminCMSRegistration>> {
        let values = self.cms_index.get_all_values()?;
        let values = overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminCMSRegistration| value.file_path.as_str(),
            |document| document.cms.as_slice(),
            None,
        );
        Ok(preferred_cms_registrations(&values))
    }

    pub fn get_all_cms_registrations_by_kind(
        &self,
        kind: AdminCMSRegistrationKind,
    ) -> IndexResult<Vec<AdminCMSRegistration>> {
        let values = self.get_all_cms_registrations()?;
        let result: Vec<AdminCMSRegistration> = values
            .into_iter()
            .filter(|value| value.kind == kind)
            .collect();
        Ok(preferred_cms_registrations(&result))
    }

    pub fn get_cms_registration(
        &self,
        kind: AdminCMSRegistrationKind,
        name: &str,
    ) -> IndexResult<Vec<AdminCMSRegistration>> {
        let values = self.cms_index.get_values(&admin_cms_key(kind, name))?;
        let values = overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminCMSRegistration| value.file_path.as_str(),
            |document| document.cms.as_slice(),
            Some(&|value: &AdminCMSRegistration| value.kind == kind && value.name == name),
        );
        Ok(preferred_cms_registrations(&values))
    }

    pub fn get_directive(&self, name: &str) -> IndexResult<Vec<AdminDirective>> {
        let values = self.directive_index.get_values(name)?;
        Ok(overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminDirective| value.file_path.as_str(),
            |document| document.directives.as_slice(),
            Some(&|value: &AdminDirective| value.name == name),
        ))
    }

    pub fn get_filter(&self, name: &str) -> IndexResult<Vec<AdminFilter>> {
        let values = self.filter_index.get_values(name)?;
        let values = overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminFilter| value.file_path.as_str(),
            |document| document.filters.as_slice(),
            Some(&|value: &AdminFilter| value.name == name),
        );
        Ok(prefer_runtime_definitions(
            &values,
            |value: &AdminFilter| value.file_path.as_str(),
        ))
    }

    /// Resolves an Options API directive local to the component owning
    /// `template_path`.
    pub fn get_local_directive_for_template(
        &self,
        template_path: &str,
        name: &str,
    ) -> IndexResult<Option<VueLocalDirective>> {
        let Some(component) = self.get_component_by_template_path(template_path)? else {
            return Ok(None);
        };
        Ok(component.local_directive(name).cloned())
    }

    pub fn get_directive_for_template(
        &self,
        template_path: &str,
        name: &str,
    ) -> IndexResult<Vec<AdminDirective>> {
        if let Some(local) = self.get_local_directive_for_template(template_path, name)? {
            return Ok(vec![directive_from_local(&local)]);
        }
        self.get_directive(name)
    }

    pub fn get_all_directives_for_template(
        &self,
        template_path: &str,
    ) -> IndexResult<Vec<AdminDirective>> {
        let global = self.get_all_directives()?;
        let mut by_name: HashMap<String, AdminDirective> = HashMap::with_capacity(global.len());
        let mut order: Vec<String> = Vec::with_capacity(global.len());
        for directive in global {
            let key = directive.name.to_lowercase();
            if key.is_empty() {
                continue;
            }
            if !by_name.contains_key(&key) {
                order.push(key.clone());
            }
            by_name.insert(key, directive);
        }
        if let Some(component) = self.get_component_by_template_path(template_path)? {
            for local in &component.local_directives {
                let key = local.name.to_lowercase();
                if key.is_empty() {
                    continue;
                }
                if !by_name.contains_key(&key) {
                    order.push(key.clone());
                }
                by_name.insert(key, directive_from_local(local));
            }
        }
        Ok(order
            .into_iter()
            .filter_map(|key| by_name.remove(&key))
            .collect())
    }

    pub fn get_all_stores(&self) -> IndexResult<Vec<AdminStore>> {
        let values = self.store_index.get_all_values()?;
        Ok(overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminStore| value.file_path.as_str(),
            |document| document.stores.as_slice(),
            None,
        ))
    }

    pub fn get_store(&self, name: &str) -> IndexResult<Vec<AdminStore>> {
        let values = self.store_index.get_values(name)?;
        let documents = self.live_legacy_document_snapshots();
        let values = overlay_live_legacy_values(
            values,
            &documents,
            |value: &AdminStore| value.file_path.as_str(),
            |document| document.stores.as_slice(),
            Some(&|value: &AdminStore| value.name == name),
        );
        let mut values = preferred_stores(&values);
        for store in &mut values {
            if !store.factory_path.is_empty() {
                let factory_path = normalize_definition_path(&store.factory_path);
                let factories = self.store_factory_index.get_values(&factory_path)?;
                let factories = overlay_live_legacy_values(
                    factories,
                    &documents,
                    |value: &AdminStoreFactory| value.file_path.as_str(),
                    |document| document.store_factory.as_slice(),
                    Some(&|value: &AdminStoreFactory| {
                        normalize_definition_path(&value.file_path) == factory_path
                    }),
                );
                for factory in &factories {
                    store.members = merge_store_members(&factory.members, &store.members);
                }
            }
            if !store.state_type.is_empty() {
                let shape = self.resolve_vue_type(&store.state_type, &store.file_path, &[])?;
                for member in &mut store.members {
                    if member.kind != AdminStoreMemberKind::State {
                        continue;
                    }
                    if let Some(declared) = twig_vue_member_named(&shape.members, &member.name)
                        && !declared.type_.is_empty()
                    {
                        member.type_ = declared.type_.clone();
                    }
                }
            }
        }
        Ok(values)
    }

    pub fn get_all_privileges(&self) -> IndexResult<Vec<AdminPrivilege>> {
        let values = self.privilege_index.get_all_values()?;
        let mut values = overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminPrivilege| value.file_path.as_str(),
            |document| document.privileges.as_slice(),
            None,
        );
        if !values
            .iter()
            .any(|value| value.name == ADMIN_PRIVILEGE_ADMINISTRATOR)
            && let Some(administrator) = builtin_admin_privilege(ADMIN_PRIVILEGE_ADMINISTRATOR)
        {
            values.push(administrator);
        }
        Ok(values)
    }

    pub fn get_privilege(&self, name: &str) -> IndexResult<Vec<AdminPrivilege>> {
        let values = self.privilege_index.get_values(name)?;
        let values = overlay_live_legacy_values(
            values,
            &self.live_legacy_document_snapshots(),
            |value: &AdminPrivilege| value.file_path.as_str(),
            |document| document.privileges.as_slice(),
            Some(&|value: &AdminPrivilege| value.name == name),
        );
        let values = preferred_privileges(&values);
        if values.is_empty()
            && let Some(builtin) = builtin_admin_privilege(name)
        {
            return Ok(vec![builtin]);
        }
        Ok(values)
    }
}
