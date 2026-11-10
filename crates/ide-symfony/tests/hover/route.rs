use std::sync::Arc;

use crate::common::*;
use swlsp_ide_symfony::hover::RouteHoverProvider;
use swlsp_lsp::TextDocument;
use swlsp_symfony::RouteIndexer;

fn hover_value(provider: &RouteHoverProvider, source: &str, needle: &str) -> String {
    let document = TextDocument::new("file:///project/view.twig", source, 1);
    let request = request_at(document, source.find(needle).unwrap() + 2);
    let result = hover(provider, &request).expect("hover");
    assert!(result.range.is_some());
    result.contents.value
}

#[test]
fn route_hover() {
    let cache = tempfile::tempdir().unwrap();
    let index = Arc::new(RouteIndexer::new(cache.path(), None).unwrap());
    index_file(
        index.as_ref(),
        "/project/config/routes.xml",
        r#"<routes><route id="product.show" path="/products/{id}" controller="App\Controller\ProductController::show"/></routes>"#,
    );
    let provider = RouteHoverProvider::new(Some(index), None);

    let value = hover_value(&provider, "{{ path('product.show') }}", "product.show");
    assert!(value.contains("**Symfony route** `product.show`"));
    assert!(value.contains("- Path: `/products/{id}`"));
    assert!(value.contains("- Parameters: `id`"));
    assert!(value.contains("- Controller: `App\\Controller\\ProductController::show`"));

    let value = hover_value(
        &provider,
        "{% if app.request.attributes.get('_route') == 'product.show' %}{% endif %}",
        "product.show",
    );
    assert!(value.contains("**Symfony route** `product.show`"));

    let value = hover_value(
        &provider,
        r#"<a href="/products/42">Product</a>"#,
        "/products/42",
    );
    assert!(value.contains("**Symfony route URL** `/products/42`"));
    assert!(value.contains("- Name: `product.show`"));
}
