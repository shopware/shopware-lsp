#![allow(unused_imports)]
use super::support::*;
use crate::*;

#[test]
fn is_privilege_reference_restricts_dependency_arrays_to_privilege_mappings() {
    let tests = [
        (
            "privilege mapping dependency",
            "Shopware.Service('privileges').addPrivilegeMappingEntry({
                dependencies: ['product.viewer'],
            })",
            true,
        ),
        (
            "unrelated dependency metadata",
            "registerPlugin({ dependencies: ['product.viewer'] })",
            false,
        ),
        (
            "component privilege property",
            "export default { privilege: 'product.viewer' }",
            true,
        ),
    ];
    for (name, source, expected) in tests {
        let root = parse_js(source);
        let offset = source.find("product.viewer").unwrap() + 1;
        let node = root.node_at(TextSize::from(offset as u32)).unwrap();
        assert_eq!(is_privilege_reference(&node), expected, "{name}");
    }
}
