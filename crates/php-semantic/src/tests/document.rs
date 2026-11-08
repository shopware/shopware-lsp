use std::borrow::Cow;

use swlsp_parser_php::syntax::{PHP_NUMBER, PHP_VARIABLE};
use swlsp_syntax::{Kind, SyntaxNodeExt};

use super::*;

#[test]
fn reference_record_is_compact() {
    assert!(
        std::mem::size_of::<Reference>() <= 64,
        "{}",
        std::mem::size_of::<Reference>()
    );
}

#[test]
fn reference_releases_empty_lazy_targets() {
    let mut reference = Reference::default();
    assert!(!reference.has_targets_record());
    reference.set_qualified_name("App\\Service");
    assert!(reference.has_targets_record());
    assert_eq!(reference.qualified_name_count(), 1);
    assert_eq!(&*reference.qualified_names()[0], "App\\Service");
    reference.add_candidate("first".into());
    assert_eq!(reference.resolved, "first");
    reference.add_candidate("second".into());
    assert_eq!(
        reference.candidate_ids(),
        &[SymbolId::from("first"), SymbolId::from("second")]
    );
    reference.clear_candidate_ids();
    assert_eq!(reference.qualified_names(), &names(&["App\\Service"])[..]);
    assert!(reference.has_targets_record());
    reference.set_qualified_names(Vec::new());
    assert!(!reference.has_targets_record());
}

#[test]
fn reference_add_candidate_keeps_unique_target_inline() {
    let mut reference = Reference::default();
    reference.add_candidate(SymbolId::default());
    assert!(reference.resolved.is_empty());
    assert!(reference.candidate_ids().is_empty());
    reference.add_candidate("first".into());
    assert_eq!(reference.resolved, "first");
    assert!(reference.candidate_ids().is_empty());
    reference.add_candidate("second".into());
    assert!(reference.resolved.is_empty());
    assert_eq!(
        reference.candidate_ids(),
        &[SymbolId::from("first"), SymbolId::from("second")]
    );
    reference.add_candidate("third".into());
    assert_eq!(reference.candidate_ids().len(), 3);
    reference.set_qualified_names(names(&["App\\run", "run"]));
    assert_eq!(
        reference.qualified_names(),
        &names(&["App\\run", "run"])[..]
    );
    reference.set_qualified_name("App\\Service");
    assert_eq!(reference.qualified_names(), &names(&["App\\Service"])[..]);
}

#[test]
fn scope_symbol_ids_preserve_duplicates_and_insertion_order() {
    let symbols = vec![
        symbol("first", SymbolKind::Local, "$value", "", ""),
        symbol("second", SymbolKind::Local, "$value", "", ""),
        symbol("other", SymbolKind::Local, "$other", "", ""),
        symbol("method", SymbolKind::Method, "DoWork", "", ""),
        symbol("constant", SymbolKind::GlobalConstant, "EXACT", "", ""),
        symbol("unicode", SymbolKind::Method, "Äction", "", ""),
    ];
    let mut scope = Scope::default();
    for index in 0..symbols.len() {
        scope.add_symbol(index as u32);
    }
    assert!(scope.has_symbol(&symbols, "$value"));
    assert!(scope.has_symbol(&symbols, "dowork"));
    assert!(scope.has_symbol(&symbols, "äction"));
    assert!(scope.has_symbol(&symbols, "EXACT"));
    assert!(!scope.has_symbol(&symbols, "DoWork"));
    assert!(!scope.has_symbol(&symbols, "exact"));
    assert!(!scope.has_symbol(&symbols, "$missing"));
    let mut target = vec![SymbolId::from("prefix")];
    scope.append_symbol_ids(&symbols, &mut target, "$value");
    assert_eq!(
        target,
        vec![SymbolId::from("prefix"), "first".into(), "second".into()]
    );
    let values: Vec<_> = scope
        .symbol_ids(&symbols, "$value")
        .map(|id| id.to_string())
        .collect();
    assert_eq!(values, ["first", "second"]);
    let all: Vec<_> = scope
        .all_symbol_ids(&symbols)
        .map(|id| id.to_string())
        .collect();
    assert_eq!(
        all,
        ["first", "second", "other", "method", "constant", "unicode"]
    );
}

#[test]
fn document_clone_detaches_scope_symbols() {
    let mut scope = Scope::default();
    scope.add_symbol(0);
    scope.add_symbol(1);
    let document = Document {
        symbols: vec![
            symbol("first", SymbolKind::Local, "$value", "", ""),
            symbol("second", SymbolKind::Local, "$value", "", ""),
        ],
        scopes: vec![scope],
        ..Document::default()
    };
    let mut cloned = document.clone();
    cloned
        .symbols
        .push(symbol("third", SymbolKind::Local, "$value", "", ""));
    cloned.scopes[0].add_symbol(2);
    let mut original = Vec::new();
    document.scopes[0].append_symbol_ids(&document.symbols, &mut original, "$value");
    assert_eq!(original.len(), 2);
    let mut copied = Vec::new();
    cloned.scopes[0].append_symbol_ids(&cloned.symbols, &mut copied, "$value");
    assert_eq!(copied.len(), 3);
}

#[test]
fn document_type_facts_and_scopes() {
    let root = swlsp_parser_php::root("<?php $value;");
    let variable = &swlsp_parser_php::query::nodes(&root, &[PHP_VARIABLE])[0];
    let id = node_identity(variable);
    let mut document = Document {
        path: "/file.php".into(),
        scopes: vec![
            Scope {
                id: 0,
                kind: ScopeKind::File,
                range: range(0, 20),
                ..Scope::default()
            },
            Scope {
                id: 1,
                kind: ScopeKind::Function,
                parent: 0,
                range: range(5, 15),
                ..Scope::default()
            },
        ],
        ..Document::default()
    };
    document.type_facts.insert(
        id,
        TypeFact {
            ty: Type::string(),
            confidence: Confidence::Declared,
            source: TypeSource::Native,
            ..TypeFact::default()
        },
    );
    assert_eq!(document.type_of(variable).ty, Type::string());
    assert_eq!(document.scope_at(10).map(|scope| scope.id), Some(1));
    assert_eq!(document.scope_at(17).map(|scope| scope.id), Some(0));
    assert!(document.scope_at(30).is_none());
}

#[test]
fn document_compacts_type_facts_without_reasons() {
    let root = swlsp_parser_php::root("<?php $value;");
    let node = swlsp_parser_php::query::nodes(&root, &[PHP_VARIABLE])[0].clone();
    let identity = node_identity(&node);
    let mut document = Document::default();
    document.set_type_fact(
        identity,
        TypeFact {
            ty: Type::string(),
            confidence: Confidence::Inferred,
            source: TypeSource::Assignment,
            origin: node.text_range(),
            reason: Cow::Borrowed(""),
        },
    );
    assert!(document.type_facts.is_empty());
    assert_eq!(document.type_fact_count(), 1);
    let fact = document.type_of(&node);
    assert_eq!(fact.ty, Type::string());
    assert_eq!(fact.confidence, Confidence::Inferred);
    assert_eq!(fact.source, TypeSource::Assignment);
    assert_eq!(fact.origin, node.range_trimmed_trivia());

    let mut cloned = document.clone();
    document.delete_type_fact(identity);
    assert_eq!(document.type_fact_count(), 0);
    assert_eq!(cloned.type_fact_count(), 1);
    assert_eq!(cloned.type_of(&node).ty, Type::string());

    cloned.set_type_fact(
        identity,
        TypeFact {
            ty: Type::int(),
            confidence: Confidence::Inferred,
            source: TypeSource::Assignment,
            reason: Cow::Borrowed("assignment"),
            ..TypeFact::default()
        },
    );
    assert!(cloned.type_facts.is_empty());
    assert_eq!(cloned.type_of(&node).reason, "assignment");

    cloned.set_type_fact(
        identity,
        TypeFact {
            ty: Type::int(),
            reason: Cow::Owned("extension-specific reason".to_owned()),
            origin: range(1, 2),
            ..TypeFact::default()
        },
    );
    assert_eq!(cloned.type_facts.len(), 1);
    assert_eq!(cloned.type_fact_count(), 1);
    assert_eq!(cloned.type_fact(identity).unwrap().origin, range(1, 2));
}

#[test]
fn compact_type_facts_preserve_packed_and_overflow_identities() {
    let identities = [
        NodeId {
            kind: Kind(42),
            start: 17,
            end: 117,
        },
        NodeId {
            kind: Kind(43),
            start: 17,
            end: 117,
        },
        NodeId {
            kind: Kind(42),
            start: 17,
            end: 70_000,
        },
        NodeId {
            kind: Kind(42),
            start: 20,
            end: 19,
        },
    ];
    let mut document = Document::default();
    document.reserve_type_facts(4);
    for (index, identity) in identities.iter().enumerate() {
        document.set_type_fact(
            *identity,
            TypeFact {
                ty: Type::literal_int(&index.to_string()),
                confidence: Confidence::Inferred,
                source: TypeSource::Flow,
                ..TypeFact::default()
            },
        );
    }
    assert_eq!(document.type_fact_count(), 4);
    for (index, identity) in identities.iter().enumerate() {
        assert_eq!(
            document.type_fact(*identity).unwrap().ty.to_string(),
            index.to_string()
        );
    }
    let cloned = document.clone();
    document.delete_type_fact(identities[0]);
    document.delete_type_fact(identities[2]);
    assert_eq!(document.type_fact_count(), 2);
    assert_eq!(cloned.type_fact_count(), 4);
}

#[test]
fn compact_type_fact_reasons_round_trip() {
    let mut document = Document::default();
    let reasons = [
        "",
        "assignment",
        "foreach value",
        "instanceof",
        "null comparison",
        "flow expression",
        "conditional predicate",
        "by-reference argument",
        "logical condition",
        "truthiness",
        "is_string",
        "is_int",
        "is_integer",
        "is_long",
        "is_float",
        "is_double",
        "is_real",
        "is_bool",
        "is_array",
        "is_callable",
        "is_iterable",
        "is_object",
        "is_null",
    ];
    for (index, reason) in reasons.iter().enumerate() {
        let identity = NodeId {
            kind: Kind(1),
            start: index as u32,
            end: index as u32 + 1,
        };
        document.set_type_fact(
            identity,
            TypeFact {
                reason: Cow::Borrowed(reason),
                ..TypeFact::default()
            },
        );
        assert!(document.type_facts.is_empty(), "{reason}");
        assert_eq!(document.type_fact(identity).unwrap().reason, *reason);
    }
    let stats = profile_type_facts(&document);
    assert_eq!(stats.total, reasons.len());
    assert_eq!(stats.detailed, 0);
}

#[test]
fn literal_types_come_from_syntax() {
    let root = swlsp_parser_php::root("<?php 42;");
    let number = &swlsp_parser_php::query::nodes(&root, &[PHP_NUMBER])[0];
    let fact = Document::default().type_of(number);
    assert_eq!(fact.ty.to_string(), "42");
    assert_eq!(fact.source, TypeSource::Literal);
}

#[test]
fn workspace_graph_projection_drops_local_state() {
    let mut reference = Reference {
        name: "Service".into(),
        scope: 7,
        kind: NameKind::Class,
        ..Reference::default()
    };
    reference.set_qualified_name("App\\Service");
    let full = Document {
        path: "/a.php".into(),
        symbols: vec![
            symbol("class", SymbolKind::Class, "A", "A", "/a.php"),
            symbol("local", SymbolKind::Local, "$x", "", "/a.php"),
            symbol("parameter", SymbolKind::Parameter, "$y", "", "/a.php"),
        ],
        references: vec![
            reference,
            Reference {
                name: "$x".into(),
                kind: NameKind::Variable,
                ..Reference::default()
            },
        ],
        scopes: vec![Scope::default()],
        ..Document::default()
    };
    let projected = full.workspace_graph();
    assert_eq!(ids(&projected.symbols), ["class"]);
    assert_eq!(projected.references.len(), 1);
    assert_eq!(projected.references[0].scope, 0);
    assert!(projected.scopes.is_empty());
}
