//! ACL privileges (port of Go `privilege.go`).

use crate::*;

string_enum! {
    pub enum AdminPrivilegeKind {
        Role => "role",
        Permission => "permission",
    }
}

/// Shopware's built-in administrator-only ACL key.
pub const ADMIN_PRIVILEGE_ADMINISTRATOR: &str = "admin";

/// A public role key (`product.viewer`) or a concrete backend permission
/// contained by a role (`product:read`).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminPrivilege {
    pub name: String,
    pub kind: AdminPrivilegeKind,
    pub mapping_key: String,
    pub role: String,
    pub file_path: String,
    pub line: u32,
}

impl AdminPrivilege {
    /// Whether Shopware itself provides the privilege (no project source).
    pub fn is_builtin(&self) -> bool {
        self.name == ADMIN_PRIVILEGE_ADMINISTRATOR && self.file_path.is_empty()
    }
}

pub(crate) fn builtin_admin_privilege(name: &str) -> Option<AdminPrivilege> {
    (name == ADMIN_PRIVILEGE_ADMINISTRATOR).then(|| AdminPrivilege {
        name: ADMIN_PRIVILEGE_ADMINISTRATOR.to_owned(),
        kind: AdminPrivilegeKind::Role,
        ..Default::default()
    })
}

/// Role keys and their concrete permissions declared through
/// `addPrivilegeMappingEntry`.
pub(crate) fn parse_admin_privileges(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<AdminPrivilege> {
    let mut privileges = Vec::new();
    for call in jsquery::iter_calls(root, &[]) {
        if jsquery::call_method_name(&call).as_deref() != Some("addPrivilegeMappingEntry")
            || !jsquery::call_name(&call)
                .unwrap_or_default()
                .ends_with(".addPrivilegeMappingEntry")
        {
            continue;
        }
        let config = jsquery::object_argument(&call, 0);
        let mapping_key = string_property_of(config.as_ref(), "key");
        let roles = config
            .as_ref()
            .and_then(|config| jsquery::property(config, "roles"))
            .and_then(|property| jsquery::property_value(&property));
        let Some(roles) =
            roles.filter(|roles| !mapping_key.is_empty() && roles.kind() == jssyntax::JS_OBJECT)
        else {
            continue;
        };
        for role_property in jsquery::properties(&roles) {
            let role_name = jsquery::property_name(&role_property).unwrap_or_default();
            let Some(role_config) =
                jsquery::property_value(&role_property).filter(|_| !role_name.is_empty())
            else {
                continue;
            };
            privileges.push(AdminPrivilege {
                name: format!("{mapping_key}.{role_name}"),
                kind: AdminPrivilegeKind::Role,
                mapping_key: mapping_key.clone(),
                role: role_name.clone(),
                file_path: file_path.to_owned(),
                line: definition_node_line(line_index, &role_property),
            });
            let permissions = jsquery::property(&role_config, "privileges")
                .and_then(|property| jsquery::property_value(&property));
            let Some(permissions) = permissions else {
                continue;
            };
            for item in jsquery::array_items(&permissions) {
                if item.kind() != jssyntax::JS_STRING {
                    continue;
                }
                let permission = jsquery::string_value(&item);
                if permission.is_empty() {
                    continue;
                }
                privileges.push(AdminPrivilege {
                    name: permission,
                    kind: AdminPrivilegeKind::Permission,
                    mapping_key: mapping_key.clone(),
                    role: role_name.clone(),
                    file_path: file_path.to_owned(),
                    line: definition_node_line(line_index, &item),
                });
            }
        }
    }
    privileges
}

pub(crate) fn preferred_privileges(values: &[AdminPrivilege]) -> Vec<AdminPrivilege> {
    prefer_runtime_definitions(values, |value: &AdminPrivilege| value.file_path.as_str())
}
