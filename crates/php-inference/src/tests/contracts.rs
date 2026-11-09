use swlsp_php_semantic::{Attribute, Document, Snapshot, Str, Symbol, SymbolId, SymbolKind};
use swlsp_php_types::Type;

use crate::{AttributeContracts, CallContext, Extension};

fn attribute_contract_method_snapshot() -> std::sync::Arc<Snapshot> {
    let class_id = SymbolId::new("service");
    let mut terminate = Symbol {
        id: SymbolId::new("terminate"),
        kind: SymbolKind::Method,
        name: Str::from("terminate"),
        fully_qualified: Str::from("Service::terminate"),
        container: class_id.clone(),
        path: Str::from("/service.php"),
        ..Symbol::default()
    };
    terminate.set_attributes(vec![Attribute {
        name: Str::from("JetBrains\\PhpStorm\\NoReturn"),
        ..Attribute::default()
    }]);
    Snapshot::new(
        1,
        &[Document {
            path: Str::from("/service.php"),
            symbols: vec![
                Symbol {
                    id: class_id,
                    kind: SymbolKind::Class,
                    name: Str::from("Service"),
                    fully_qualified: Str::from("Service"),
                    path: Str::from("/service.php"),
                    ..Symbol::default()
                },
                terminate,
            ],
            ..Document::default()
        }],
    )
}

#[test]
fn attribute_contracts_method() {
    let snapshot = attribute_contract_method_snapshot();
    let fact = AttributeContracts
        .infer_call(&CallContext {
            snapshot: Some(&snapshot),
            receiver: Type::named("Service", Vec::new()),
            name: "terminate",
            ..CallContext::default()
        })
        .expect("NoReturn contract");
    assert_eq!(fact.ty, Type::NEVER);
}
