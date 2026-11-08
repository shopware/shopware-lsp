use swlsp_php_semantic::{Symbol, SymbolKind};
use swlsp_php_types::Type;

use super::*;
use crate::route_indexer::tests::{StubConstantLookup, literal_constant};

fn lookup() -> StubConstantLookup {
    let computed = Symbol {
        kind: SymbolKind::ClassConstant,
        name: "ROUTE_NAME".into(),
        ty: Type::string(),
        ..Symbol::default()
    };
    StubConstantLookup(vec![
        (
            r"App\Seo\ProductPageSeoUrlRoute",
            vec![
                literal_constant("ROUTE_NAME", "frontend.detail.page"),
                literal_constant("OTHER", "frontend.other"),
            ],
        ),
        (r"App\Computed", vec![computed]),
    ])
}

#[test]
fn resolves_constant_route_names() {
    let lookup = lookup();
    for (reference, expected) in [
        (
            r"App\Seo\ProductPageSeoUrlRoute::ROUTE_NAME",
            "frontend.detail.page",
        ),
        (r"App\Seo\ProductPageSeoUrlRoute::OTHER", "frontend.other"),
        (r"App\Missing::ROUTE_NAME", ""),
        (r"App\Seo\ProductPageSeoUrlRoute::ABSENT", ""),
        (r"App\Computed::ROUTE_NAME", ""),
        ("frontend.detail.page", ""),
        ("", ""),
    ] {
        assert_eq!(
            resolve_constant_route_name(reference, Some(&lookup)),
            expected,
            "{reference}"
        );
    }
}

#[test]
fn resolve_without_lookup_is_empty() {
    assert_eq!(resolve_constant_route_name(r"App\Foo::BAR", None), "");
}

#[test]
fn resolve_constant_route_names_drops_unresolved() {
    let lookup = StubConstantLookup(vec![(
        r"App\Seo\ProductPageSeoUrlRoute",
        vec![literal_constant("ROUTE_NAME", "frontend.detail.page")],
    )]);
    let route = |name: &str, constant: &str, path: &str| Route {
        name: name.into(),
        name_constant: constant.into(),
        path: path.into(),
        ..Route::default()
    };
    let resolved = resolve_constant_route_names(
        vec![
            route("frontend.account.address.page", "", "/account/address"),
            route("", r"App\Seo\ProductPageSeoUrlRoute::ROUTE_NAME", "/detail"),
            route("", r"App\Missing::ROUTE_NAME", "/gone"),
            route("", "", "/nameless"),
        ],
        Some(&lookup),
    );
    assert_eq!(
        resolved,
        vec![
            route("frontend.account.address.page", "", "/account/address"),
            route(
                "frontend.detail.page",
                r"App\Seo\ProductPageSeoUrlRoute::ROUTE_NAME",
                "/detail"
            ),
        ]
    );
}
