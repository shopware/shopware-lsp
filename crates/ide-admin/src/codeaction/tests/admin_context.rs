//! Shopware context action tests.

use swlsp_syntax::SyntaxNodeExt;

use swlsp_ide::codeaction::testing::request_at;

use super::*;

fn needle_request(uri: &str, source: &str, needle: &str) -> CodeActionRequest {
    let offset = source.find(needle).unwrap_or(0) as u32;
    let mut request = request_at(uri, source, offset, offset);
    let root = request.root.clone().unwrap();
    request.syntax.node = root.node_at(offset.into());
    request
}

#[test]
fn admin_context_actions_expose_component_and_method_generators() {
    let provider = AdminContextProvider::new();
    let source = "Shopware.Component.register('sw-product-list', {
    methods: {
        loadProducts(criteria, context) {
            return [];
        },
    },
});";
    let uri = "file:///project/Resources/app/administration/src/product.js";
    let component = provider.get_code_actions(
        &Context::background(),
        &needle_request(uri, source, "sw-product-list"),
    );
    assert_eq!(component.len(), 1);
    assert_eq!(
        component[0].command.as_ref().unwrap().command,
        EXTEND_ADMIN_COMPONENT_ACTION
    );

    let method = provider.get_code_actions(
        &Context::background(),
        &needle_request(uri, source, "loadProducts"),
    );
    assert_eq!(method.len(), 1);
    let command = method[0].command.as_ref().unwrap();
    assert_eq!(command.command, OVERRIDE_ADMIN_METHOD_ACTION);
    assert_eq!(command.arguments[0], "sw-product-list");
    assert_eq!(command.arguments[1], "loadProducts");
    assert_eq!(command.arguments[2], "methods");
    assert_eq!(command.arguments[3], "criteria,context");
}
