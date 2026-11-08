use swlsp_php_inference::CallArgument;
use swlsp_php_semantic::{Document, Snapshot, Symbol, SymbolId, SymbolKind};

use super::*;

fn context<'a>(snapshot: &'a Snapshot, arguments: &'a [CallArgument]) -> CallContext<'a> {
    CallContext {
        snapshot: Some(snapshot),
        document: None,
        node: None,
        source: "",
        name: "get",
        receiver: named("App\\CustomHeaderBag"),
        arguments,
        current_class: Type::UNKNOWN,
        is_static: false,
    }
}

#[test]
fn php_type_extension_infers_header_bag_default() {
    let mut custom = Symbol {
        id: SymbolId::new("custom-header-bag"),
        kind: SymbolKind::Class,
        name: "CustomHeaderBag".into(),
        fully_qualified: "App\\CustomHeaderBag".into(),
        path: "/header-bag.php".into(),
        ..Symbol::default()
    };
    custom.set_hierarchy(
        vec!["Symfony\\Component\\HttpFoundation\\HeaderBag".into()],
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
    );
    let header_bag = Symbol {
        id: SymbolId::new("header-bag"),
        kind: SymbolKind::Class,
        name: "HeaderBag".into(),
        fully_qualified: "Symfony\\Component\\HttpFoundation\\HeaderBag".into(),
        path: "/header-bag.php".into(),
        ..Symbol::default()
    };
    let snapshot = Snapshot::new(
        1,
        &[Document {
            path: "/header-bag.php".into(),
            symbols: vec![header_bag, custom],
            ..Document::default()
        }],
    );
    let extension = PhpTypeExtension::new(None);
    let arguments = [
        CallArgument::of(Type::literal_string("CONTENT_TYPE")),
        CallArgument::of(Type::literal_string("")),
    ];
    let fact = extension
        .infer_call(&context(&snapshot, &arguments))
        .unwrap();
    assert_eq!(fact.ty.to_string(), "string");

    let arguments = [
        CallArgument::of(Type::literal_string("CONTENT_TYPE")),
        CallArgument::of(Type::int()),
    ];
    assert!(
        extension
            .infer_call(&context(&snapshot, &arguments))
            .is_none(),
        "an invalid default must remain visible to signature diagnostics"
    );
}
