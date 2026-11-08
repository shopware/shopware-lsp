use swlsp_indexer::{Indexer, ParsedFile};

use super::*;
use crate::RouteIndexer;

#[test]
fn parses_xml_routes() {
    let routes = parse_xml_routes(
        "/project/config/routes.xml",
        r#"<?xml version="1.0" encoding="UTF-8"?>
<routes xmlns="http://symfony.com/schema/routing">
    <route id="product.show" path="/products/{id&lt;\d+&gt;}">
        <default key="_controller">App\Controller\ProductController::show</default>
    </route>
    <route id="product.edit" path="/products/{id}/edit" methods="GET|POST" controller="App\Controller\ProductController::edit"/>
    <import resource="attributes.yaml" type="attribute"/>
</routes>"#,
    );
    assert_eq!(routes.len(), 2);
    assert_eq!(routes[0].name, "product.show");
    assert_eq!(routes[0].path, r"/products/{id<\d+>}");
    assert_eq!(
        routes[0].controller,
        r"App\Controller\ProductController::show"
    );
    assert_eq!(routes[0].parameters(), vec!["id"]);
    assert_eq!(routes[1].name, "product.edit");
    assert_eq!(routes[1].methods, vec!["GET", "POST"]);
    assert!(routes[0].line > 0);
}

#[test]
fn route_indexer_indexes_xml() {
    let dir = tempfile::tempdir().unwrap();
    let index = RouteIndexer::new(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/project/config/routes.xml",
            br#"<routes><route id="catalog" path="/catalog/{page}"/></routes>"#.to_vec(),
        ))
        .unwrap();
    let routes = index.get_route("catalog").unwrap();
    assert_eq!(routes.len(), 1);
    assert_eq!(routes[0].parameters(), vec!["page"]);
}

#[test]
fn route_parameters_normalize_inline_metadata() {
    let route = Route {
        path: r"/orders/{id<\d+>}/{!page<\d+>?1}/{id}".into(),
        ..Route::default()
    };
    assert_eq!(route.parameters(), vec!["id", "page"]);
}
