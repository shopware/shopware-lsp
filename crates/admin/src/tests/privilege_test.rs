#![allow(unused_imports)]
use super::support::*;
use crate::*;
use swlsp_indexer::Indexer;

#[test]
fn parse_admin_privileges_from_fluent_mappings() {
    let source = "
Shopware.Service('privileges')
    .addPrivilegeMappingEntry({
        key: 'product',
        roles: {
            viewer: {
                privileges: ['product:read'],
                dependencies: [],
            },
        },
    })
    .addPrivilegeMappingEntry({
        key: 'order',
        roles: {
            editor: {
                privileges: ['order:read', 'order:update'],
            },
        },
    });";
    let file_path = "/project/Resources/app/administration/src/privileges.ts";
    let privileges = parse_admin_privileges(&parse_js(source), file_path, &LineIndex::new(source));
    let by_name: HashMap<String, AdminPrivilege> = privileges
        .into_iter()
        .map(|privilege| (privilege.name.clone(), privilege))
        .collect();
    for name in [
        "product.viewer",
        "product:read",
        "order.editor",
        "order:read",
        "order:update",
    ] {
        assert!(by_name.contains_key(name), "{name}");
    }
    assert_eq!(by_name["product.viewer"].kind, AdminPrivilegeKind::Role);
    assert_eq!(by_name["product.viewer"].mapping_key, "product");
    assert_eq!(by_name["product.viewer"].role, "viewer");
    assert_eq!(by_name["product:read"].kind, AdminPrivilegeKind::Permission);
    assert_eq!(by_name["product:read"].file_path, file_path);
}

#[test]
fn admin_privilege_index_replaces_stale_mappings() {
    let dir = temp_dir();
    let root = dir.path();
    let idx = new_indexer(&root.join("cache"));
    let file_path = path_string(
        root,
        "src/Administration/Resources/app/administration/src/privileges.ts",
    );

    index_source(
        &idx,
        &file_path,
        "
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: { viewer: { privileges: ['product:read'] } },
});",
    );
    let values = idx.get_privilege("product.viewer").unwrap();
    assert_eq!(values.len(), 1);
    assert_eq!(values[0].kind, AdminPrivilegeKind::Role);

    index_source(
        &idx,
        &file_path,
        "
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'customer',
    roles: { editor: { privileges: ['customer:update'] } },
});",
    );
    assert!(idx.get_privilege("product.viewer").unwrap().is_empty());
    let values = idx.get_privilege("customer:update").unwrap();
    assert_eq!(values.len(), 1);
    assert_eq!(
        format!("{}.{}", values[0].mapping_key, values[0].role),
        "customer.editor"
    );
    idx.close().unwrap();
}

#[test]
fn admin_privilege_index_includes_builtin_administrator() {
    let dir = temp_dir();
    let idx = new_indexer(&dir.path().join("cache"));

    let values = idx.get_privilege(ADMIN_PRIVILEGE_ADMINISTRATOR).unwrap();
    assert_eq!(values.len(), 1);
    assert!(values[0].is_builtin());
    assert_eq!(values[0].kind, AdminPrivilegeKind::Role);
    assert!(values[0].file_path.is_empty());

    let all = idx.get_all_privileges().unwrap();
    assert_eq!(all.len(), 1);
    assert_eq!(all[0].name, ADMIN_PRIVILEGE_ADMINISTRATOR);
    assert!(all[0].is_builtin());
    idx.close().unwrap();
}
