use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

use swlsp_php_types::Type;

use super::{document, member, named, symbol};
use crate::*;

/// Infers `int` for every flagged declaration, counting analyses. With
/// `reenter`, an analysis asks the generation for the inferred return of the
/// function it is analyzing, like a recursive function would.
#[derive(Default)]
struct CountingInference {
    calls: AtomicUsize,
    reenter: bool,
}

impl ReturnInference for CountingInference {
    fn infer_returns(
        &self,
        generation: &Arc<Snapshot>,
        path: &str,
    ) -> Option<Vec<(SymbolId, Type)>> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        if self.reenter {
            // A cycle answers with the stored type instead of recursing.
            let functions = generation.functions("App\\helper");
            assert_eq!(functions[0].return_type.to_string(), "App\\Stored");
        }
        let mut returns = Vec::new();
        for view in generation.function_views("App\\helper") {
            if view.path() == path {
                returns.push((view.symbol_id(), Type::INT));
            }
        }
        for view in generation.member_views_of("App\\Service") {
            if view.path() == path {
                returns.push((view.symbol_id(), Type::INT));
            }
        }
        Some(returns)
    }
}

fn inferred(mut symbol: Symbol) -> Symbol {
    symbol.flags.insert(Flags::INFERRED_RETURN);
    symbol.return_type = named("App\\Stored");
    symbol
}

fn documents() -> Vec<Document> {
    let declared = Symbol {
        return_type: named("App\\Declared"),
        ..symbol(
            "function:app\\declared",
            SymbolKind::Function,
            "declared",
            "App\\declared",
            "/declared.php",
        )
    };
    vec![
        document(
            "/helper.php",
            vec![inferred(symbol(
                "function:app\\helper",
                SymbolKind::Function,
                "helper",
                "App\\helper",
                "/helper.php",
            ))],
        ),
        document(
            "/service.php",
            vec![
                symbol(
                    "class:app\\service",
                    SymbolKind::Class,
                    "Service",
                    "App\\Service",
                    "/service.php",
                ),
                inferred(member(
                    "method:app\\service::run",
                    SymbolKind::Method,
                    "run",
                    "App\\Service",
                    "/service.php",
                )),
            ],
        ),
        document("/declared.php", vec![declared]),
    ]
}

fn store_with(inference: Arc<CountingInference>) -> Store {
    let store = Store::new();
    store.set_return_inference(inference);
    store.replace_many(&documents());
    store
}

#[test]
fn inferred_returns_are_recomputed_once_per_generation() {
    let inference = Arc::new(CountingInference::default());
    let store = store_with(inference.clone());
    let snapshot = store.snapshot();
    assert!(snapshot.infers_returns());

    let helper = snapshot.functions("App\\helper");
    assert_eq!(helper[0].return_type, Type::INT);
    assert_eq!(snapshot.functions("App\\helper")[0].return_type, Type::INT);
    let run = snapshot.members("App\\Service", "run");
    assert_eq!(run[0].return_type, Type::INT);
    assert_eq!(inference.calls.load(Ordering::SeqCst), 2, "one per file");
    assert_eq!(snapshot.inferred_return_files(), 2);

    // Declared return types never ask.
    let declared = snapshot.functions("App\\declared");
    assert_eq!(declared[0].return_type, named("App\\Declared"));
    assert_eq!(inference.calls.load(Ordering::SeqCst), 2);

    // Listings can keep the stored type.
    let views = snapshot.function_views("App\\helper");
    assert_eq!(
        views[0].materialize_stored().return_type,
        named("App\\Stored")
    );

    // Snapshots derived for one document share the generation's memo.
    let overlay = snapshot.with_declarations(&document("/open.php", Vec::new()));
    assert_eq!(overlay.functions("App\\helper")[0].return_type, Type::INT);
    assert_eq!(inference.calls.load(Ordering::SeqCst), 2);

    // A new generation answers for its own files.
    store.replace(&document("/other.php", Vec::new()));
    let next = store.snapshot();
    assert_eq!(next.functions("App\\helper")[0].return_type, Type::INT);
    assert_eq!(inference.calls.load(Ordering::SeqCst), 3);
}

#[test]
fn snapshots_without_return_inference_use_stored_types() {
    let inference = Arc::new(CountingInference::default());
    let store = store_with(inference.clone());
    let snapshot = store.snapshot().without_return_inference();
    assert!(!snapshot.infers_returns());
    assert_eq!(
        snapshot.functions("App\\helper")[0].return_type,
        named("App\\Stored")
    );
    let overlay = snapshot.with_declarations(&document("/open.php", Vec::new()));
    assert_eq!(
        overlay.functions("App\\helper")[0].return_type,
        named("App\\Stored")
    );
    assert_eq!(inference.calls.load(Ordering::SeqCst), 0);

    // Generations built without an inference never ask either.
    let plain = Snapshot::new(1, &documents());
    assert!(!plain.infers_returns());
    assert_eq!(
        plain.functions("App\\helper")[0].return_type,
        named("App\\Stored")
    );
}

#[test]
fn cyclic_inference_falls_back_to_stored_types() {
    let inference = Arc::new(CountingInference {
        reenter: true,
        ..CountingInference::default()
    });
    let store = store_with(inference.clone());
    let snapshot = store.snapshot();
    assert_eq!(snapshot.functions("App\\helper")[0].return_type, Type::INT);
    assert_eq!(inference.calls.load(Ordering::SeqCst), 1);
}

#[test]
fn inferred_return_flag_round_trips_through_packing_and_the_codec() {
    let documents = documents();
    let graph = WorkspaceGraph::project(&documents[0]);
    let stored = graph.document();
    assert!(stored.symbols[0].flags.has(Flags::INFERRED_RETURN));
    let decoded = WorkspaceGraph::decode(&graph.encode()).unwrap();
    assert!(
        decoded.document().symbols[0]
            .flags
            .has(Flags::INFERRED_RETURN)
    );
    assert!(!documents[2].symbols[0].flags.has(Flags::INFERRED_RETURN));
}
