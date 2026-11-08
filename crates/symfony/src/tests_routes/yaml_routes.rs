use super::*;

#[test]
fn parses_yaml_routes() {
    let routes = parse_yaml_routes(
        "test.yaml",
        "# Routes file
app_homepage:
    path: /
    controller: App\\Controller\\DefaultController::index

app_product:
    path: /product/{id}
    controller: App\\Controller\\ProductController::show
    methods: [GET]
    
app_product_create:
    path: /product/create
    controller: App\\Controller\\ProductController::create
    methods: [GET, POST]
    defaults:
        color: blue
",
    );
    assert_eq!(routes.len(), 3);
    assert_eq!(routes[0].name, "app_homepage");
    assert_eq!(routes[0].path, "/");
    assert_eq!(
        routes[0].controller,
        r"App\Controller\DefaultController::index"
    );
    assert_eq!(routes[0].file_path, "test.yaml");
    assert!(routes[0].line > 0);
    assert_eq!(routes[1].name, "app_product");
    assert_eq!(routes[1].path, "/product/{id}");
    assert_eq!(
        routes[1].controller,
        r"App\Controller\ProductController::show"
    );
    assert_eq!(routes[1].methods, vec!["GET"]);
    assert!(routes[1].line > 0);
    assert_eq!(routes[2].name, "app_product_create");
    assert_eq!(routes[2].path, "/product/create");
    assert_eq!(
        routes[2].controller,
        r"App\Controller\ProductController::create"
    );
    assert_eq!(routes[2].methods, vec!["GET", "POST"]);
    assert!(routes[2].line > 0);
}
