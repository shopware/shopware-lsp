use super::*;

const PATH: &str = "/project/Hierarchy.php";

fn class_symbol(
    id: &str,
    kind: SymbolKind,
    name: &str,
    extends: &[&str],
    implements: &[&str],
) -> Symbol {
    let mut symbol = Symbol {
        range: range(1, 10),
        selection_range: range(2, 9),
        ..symbol(id, kind, name.rsplit('\\').next().unwrap(), name, PATH)
    };
    symbol.set_hierarchy(
        names(extends),
        names(implements),
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
    );
    symbol
}

fn method_symbol(id: &str, container: &str, name: &str, visibility: Visibility) -> Symbol {
    let class_name = match container {
        "contract" => "Contract",
        "trait" => "Reusable",
        "base" => "Base",
        "child" => "Child",
        "grand" => "GrandChild",
        other => other,
    };
    Symbol {
        fully_qualified: format!("App\\{class_name}::{name}").into(),
        visibility,
        range: range(20, 30),
        selection_range: range(22, 29),
        ..member(id, SymbolKind::Method, name, container, PATH)
    }
}

#[test]
fn reverse_hierarchy_indexes_direct_edges_and_method_overrides() {
    let child_execute = Symbol {
        fully_qualified: "App\\Child::execute".into(),
        range: range(40, 50),
        selection_range: range(42, 49),
        ..member(
            "child.execute",
            SymbolKind::Method,
            "execute",
            "child",
            PATH,
        )
    };
    let snapshot = Snapshot::new(
        1,
        &[document(
            PATH,
            vec![
                class_symbol("contract", SymbolKind::Interface, "App\\Contract", &[], &[]),
                method_symbol(
                    "contract.execute",
                    "contract",
                    "execute",
                    Visibility::Public,
                ),
                class_symbol("trait", SymbolKind::Trait, "App\\Reusable", &[], &[]),
                method_symbol("trait.execute", "trait", "execute", Visibility::Public),
                class_symbol(
                    "base",
                    SymbolKind::Class,
                    "App\\Base",
                    &[],
                    &["App\\Contract"],
                ),
                class_symbol(
                    "child",
                    SymbolKind::Class,
                    "App\\Child",
                    &["App\\Base"],
                    &[],
                ),
                child_execute,
                method_symbol("child.private", "child", "hidden", Visibility::Private),
                class_symbol(
                    "grand",
                    SymbolKind::Class,
                    "App\\GrandChild",
                    &["App\\Child"],
                    &[],
                ),
                method_symbol("grand.execute", "grand", "execute", Visibility::Public),
                method_symbol("grand.private", "grand", "hidden", Visibility::Public),
            ],
        )],
    );
    let mut child = snapshot.symbol("child").unwrap();
    child.set_traits(names(&["App\\Reusable"]));
    let snapshot = snapshot.with_updated_symbols(&Document {
        symbols: vec![child.clone()],
        ..Document::default()
    });
    let child_document = document(
        PATH,
        vec![
            class_symbol("contract", SymbolKind::Interface, "App\\Contract", &[], &[]),
            method_symbol(
                "contract.execute",
                "contract",
                "execute",
                Visibility::Public,
            ),
            class_symbol("trait", SymbolKind::Trait, "App\\Reusable", &[], &[]),
            method_symbol("trait.execute", "trait", "execute", Visibility::Public),
            class_symbol(
                "base",
                SymbolKind::Class,
                "App\\Base",
                &[],
                &["App\\Contract"],
            ),
            child,
            method_symbol("child.execute", "child", "execute", Visibility::Public),
            method_symbol("child.private", "child", "hidden", Visibility::Private),
            class_symbol(
                "grand",
                SymbolKind::Class,
                "App\\GrandChild",
                &["App\\Child"],
                &[],
            ),
            method_symbol("grand.execute", "grand", "execute", Visibility::Public),
            method_symbol("grand.private", "grand", "hidden", Visibility::Public),
        ],
    );
    let snapshot = snapshot.with_document(&child_document);
    assert_eq!(
        fully_qualified(&snapshot.direct_subtypes("App\\Contract")),
        ["App\\Base"]
    );
    assert_eq!(
        fully_qualified(&snapshot.direct_subtypes("App\\Base")),
        ["App\\Child"]
    );
    assert_eq!(
        fully_qualified(&snapshot.direct_subtypes("\\app\\child")),
        ["App\\GrandChild"]
    );
    assert_eq!(
        fully_qualified(&snapshot.trait_consumers("App\\Reusable")),
        ["App\\Child"]
    );
    assert_eq!(
        fully_qualified(&snapshot.direct_supertypes("child")),
        ["App\\Base"]
    );
    assert_eq!(
        fully_qualified(&snapshot.method_overrides("contract.execute")),
        ["App\\Child::execute", "App\\GrandChild::execute"]
    );
    assert_eq!(
        fully_qualified(&snapshot.method_overrides("trait.execute")),
        ["App\\Child::execute", "App\\GrandChild::execute"]
    );
    assert_eq!(
        fully_qualified(&snapshot.method_overrides("child.execute")),
        ["App\\GrandChild::execute"]
    );
    assert!(snapshot.method_overrides("child.private").is_empty());
}

#[test]
fn reverse_hierarchy_honors_document_overlays() {
    let snapshot = Snapshot::new(
        1,
        &[
            document(
                "/project/Parents.php",
                vec![
                    class_symbol("base", SymbolKind::Class, "App\\Base", &[], &[]),
                    class_symbol("other", SymbolKind::Class, "App\\Other", &[], &[]),
                ],
            ),
            document(
                "/project/Child.php",
                vec![class_symbol(
                    "child",
                    SymbolKind::Class,
                    "App\\Child",
                    &["App\\Base"],
                    &[],
                )],
            ),
        ],
    );
    assert_eq!(
        fully_qualified(&snapshot.direct_subtypes("App\\Base")),
        ["App\\Child"]
    );
    let updated = snapshot.with_document(&document(
        "/project/Child.php",
        vec![class_symbol(
            "child",
            SymbolKind::Class,
            "App\\Child",
            &["App\\Other"],
            &[],
        )],
    ));
    assert!(updated.direct_subtypes("App\\Base").is_empty());
    assert_eq!(
        fully_qualified(&updated.direct_subtypes("App\\Other")),
        ["App\\Child"]
    );
}

#[test]
fn snapshot_indexes_class_alias_relationships() {
    let target = class_symbol("target", SymbolKind::Class, "App\\Target", &[], &[]);
    let mut alias = class_symbol(
        "alias",
        SymbolKind::Class,
        "Legacy\\Alias",
        &["App\\Target"],
        &[],
    );
    alias.flags = Flags::SYNTHETIC | Flags::CLASS_ALIAS;
    let snapshot = Snapshot::new(1, &[document("/project/Alias.php", vec![target, alias])]);
    assert_eq!(
        fully_qualified(&snapshot.class_aliases("App\\Target")),
        ["Legacy\\Alias"]
    );
    assert!(snapshot.direct_subtypes("App\\Target").is_empty());
    assert_eq!(
        &*snapshot
            .class_alias_target("alias")
            .unwrap()
            .fully_qualified,
        "App\\Target"
    );
    assert!(snapshot.class_alias_target("target").is_none());
    assert!(snapshot.is_subtype_of("Legacy\\Alias", "App\\Target"));
    assert!(snapshot.is_subtype_of("App\\Target", "Legacy\\Alias"));
}

#[test]
fn store_publishes_immutable_snapshots() {
    let store = Store::new();
    let base = symbol(
        &new_symbol_id(SymbolKind::Class, "App\\Base", "/base.php", 0),
        SymbolKind::Class,
        "Base",
        "App\\Base",
        "/base.php",
    );
    let mut child = symbol(
        &new_symbol_id(SymbolKind::Class, "App\\Child", "/child.php", 0),
        SymbolKind::Class,
        "Child",
        "App\\Child",
        "/child.php",
    );
    child.set_extends(names(&["App\\Base"]));
    let first = store.replace(&document("/base.php", vec![base]));
    let second = store.replace(&document("/child.php", vec![child]));
    assert!(!first.is_subtype_of("App\\Child", "App\\Base"));
    assert!(second.is_subtype_of("App\\Child", "App\\Base"));
    assert!(second.is_subtype_of("\\app\\child", "APP\\BASE"));
    assert!(!second.is_subtype_of("App\\Base", "App\\Child"));
    assert_eq!(second.revision, 2);
}
