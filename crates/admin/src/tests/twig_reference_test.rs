#![allow(unused_imports)]
use super::support::*;
use crate::*;

fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

#[test]
fn twig_registry_references_find_acl_calls_in_vue_expressions() {
    let source = r#"<sw-card
    :disabled="!acl.can('product.viewer')"
	    v-tooltip='{ disabled: acl . can ( "order.editor" ) }'
    title="acl.can('ignored.attribute')"
>
    {{ acl.can('customer.viewer') }}
    acl.can('ignored.text')
    {# acl.can('ignored.comment') #}
</sw-card>"#;
    let root = parse_twig(source);
    let references = twig_registry_references(&root);
    assert_eq!(references.len(), 3);
    assert_eq!(
        references
            .iter()
            .map(|r| r.name.as_str())
            .collect::<Vec<_>>(),
        ["product.viewer", "order.editor", "customer.viewer"]
    );
    for reference in &references {
        assert_eq!(reference.kind, AdminSymbolKind::Privilege);
        assert_eq!(reference.name, slice(source, reference.range));
    }
}

#[test]
fn twig_registry_reference_at_offset_supports_incomplete_privilege() {
    let source = r#"<mt-button :disabled="acl.can('product.vie"></mt-button>"#;
    let root = parse_twig(source);
    let start = source.find("product.vie").unwrap() as u32;
    let offset = start + "product.vie".len() as u32;
    let reference = twig_registry_reference_at_offset(&root, offset).expect("reference");
    assert_eq!(reference.name, "product.vie");
    assert_eq!(u32::from(reference.range.start()), start);
    assert_eq!(u32::from(reference.range.end()), offset);
}

#[test]
fn twig_registry_reference_at_offset_rejects_ordinary_attribute() {
    let source = r#"<div title="acl.can('product.viewer')"></div>"#;
    let root = parse_twig(source);
    let offset = source.find("product.viewer").unwrap() as u32 + 2;
    assert!(twig_registry_reference_at_offset(&root, offset).is_none());
}

#[test]
fn twig_registry_references_find_administration_routes() {
    let source = r#"<div>
    <router-link :to="{ name: 'sw.product.detail', params: { id: product.id } }" />
    <mt-button @click="$router.push({ name: 'sw.product.create' })" />
    <mt-link :link-href="$router.resolve({ name: 'sw.product.index' }).href" />
    <span :title="{ name: 'ignored.property' }" />
</div>"#;
    let root = parse_twig(source);
    let references = twig_registry_references(&root);
    assert_eq!(references.len(), 3);
    assert_eq!(
        references
            .iter()
            .map(|r| r.name.as_str())
            .collect::<Vec<_>>(),
        ["sw.product.detail", "sw.product.create", "sw.product.index"]
    );
    for reference in &references {
        assert_eq!(reference.kind, AdminSymbolKind::ModuleRoute);
        assert_eq!(reference.name, slice(source, reference.range));
    }
}

#[test]
fn twig_registry_reference_at_offset_supports_incomplete_route() {
    let source = r#"<router-link :to="{ name: 'sw.product.det }" />"#;
    let root = parse_twig(source);
    let offset = (source.find("sw.product.det").unwrap() + "sw.product.det".len()) as u32;
    let reference = twig_registry_reference_at_offset(&root, offset).expect("reference");
    assert_eq!(reference.kind, AdminSymbolKind::ModuleRoute);
    assert_eq!(reference.name, "sw.product.det");
}
