#![allow(unused_imports)]
use super::support::*;
use crate::*;

#[test]
fn javascript_symbol_registration_contexts() {
    use AdminSymbolKind as K;
    for (source, needle, kind, name) in [
        (
            "Shopware.Component.extend('child', 'parent', {})",
            "child",
            K::Component,
            "child",
        ),
        (
            "Shopware.Component.extend('child', 'parent', {})",
            "parent",
            K::Component,
            "parent",
        ),
        (
            "Shopware.Component.register('card', { template: 'markup' })",
            "card",
            K::Component,
            "card",
        ),
        (
            "Shopware.Component.register('card', { template: 'markup' })",
            "markup",
            K::Unset,
            "",
        ),
        (
            "Shopware.Component.override('card', {})",
            "card",
            K::Component,
            "card",
        ),
        (
            "Mixin.register('listing', {})",
            "listing",
            K::Mixin,
            "listing",
        ),
        (
            "Directive.register('tooltip', {})",
            "tooltip",
            K::Directive,
            "tooltip",
        ),
        (
            "Filter.register('currency', {})",
            "currency",
            K::Filter,
            "currency",
        ),
        (
            "Module.register('product', { routes: { detail: { component: 'card' } } })",
            "card",
            K::Component,
            "card",
        ),
        (
            "Module.register('product', { routes: { detail: { component: 'card' } } })",
            "product",
            K::Module,
            "product",
        ),
        (
            "Shopware.Store.register({ id: 'session' })",
            "session",
            K::Store,
            "session",
        ),
        (
            "Shopware.Service().register('apiService', () => {})",
            "apiService",
            K::Service,
            "apiService",
        ),
        (
            "Shopware.Application.addServiceProvider('acl', factory)",
            "acl",
            K::Service,
            "acl",
        ),
        (
            "Shopware.Component.register('card', { component: 'ordinary' })",
            "ordinary",
            K::Unset,
            "",
        ),
        (
            "Shopware.Component.register('card', 'ordinary')",
            "ordinary",
            K::Unset,
            "",
        ),
        (
            "Shopware.Component.register('card', { nested:",
            "card",
            K::Component,
            "card",
        ),
    ] {
        let root = parse_js(source);
        assert_eq!(root.text().to_string(), source);
        let offset = source.find(&format!("'{needle}'")).expect("needle") + 1;
        let node = root.node_at(TextSize::from(offset as u32)).expect("node");
        let target = javascript_symbol_at(&node);
        assert_eq!(!kind.is_unset(), target.is_some(), "{source}/{needle}");
        if let Some(target) = target {
            assert_eq!(target.kind, kind, "{source}/{needle}");
            assert_eq!(target.name, name, "{source}/{needle}");
        }
    }
}
