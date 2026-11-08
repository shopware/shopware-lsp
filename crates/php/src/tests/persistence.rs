use std::io::Read;

use serde::Serialize;
use swlsp_php_semantic::{Document, Symbol, SymbolKind, TemplateParameter, WorkspaceGraph};
use swlsp_php_types::Type;

use crate::persistence::{
    PERSISTED_WORKSPACE_GRAPH_FORMAT, Payload, PersistedWorkspaceGraph,
    decode_persisted_workspace_graph_borrowed, encode_workspace_graph, release_codec_pool,
};

fn graph(path: &str) -> WorkspaceGraph {
    WorkspaceGraph::pack_owned(Document {
        path: path.into(),
        ..Document::default()
    })
}

#[test]
fn round_trip() {
    let mut symbol = Symbol {
        id: "product".into(),
        kind: SymbolKind::Class,
        name: "Product".into(),
        fully_qualified: "App\\Product".into(),
        path: "/project/Product.php".into(),
        ..Symbol::default()
    };
    symbol.set_extends(vec!["App\\Entity".into()]);
    symbol.set_templates(vec![TemplateParameter {
        name: "T".into(),
        bound: Type::named("App\\Entity", Vec::new()),
        default: Type::UNKNOWN,
        ..TemplateParameter::default()
    }]);
    let document = Document {
        path: "/project/Product.php".into(),
        namespace: "App".into(),
        symbols: vec![symbol],
        ..Document::default()
    }
    .workspace_graph();
    let packed = WorkspaceGraph::pack_owned(document.clone());

    let persisted = encode_workspace_graph(&packed).unwrap();
    assert_eq!(persisted.format, PERSISTED_WORKSPACE_GRAPH_FORMAT);
    let decoded = persisted.decode().unwrap().document();
    assert_eq!(decoded.path, document.path);
    assert_eq!(decoded.namespace, document.namespace);
    assert_eq!(decoded.symbols.len(), 1);
    assert_eq!(decoded.symbols[0].id, document.symbols[0].id);
    assert_eq!(decoded.symbols[0].extends(), document.symbols[0].extends());
    assert_eq!(decoded.symbols[0].templates().len(), 1);
    assert_eq!(
        decoded.symbols[0].templates()[0].bound,
        document.symbols[0].templates()[0].bound
    );

    // A later encoding reuses the pooled compressor while the first payload
    // stays owned by its envelope.
    let second = encode_workspace_graph(&graph("/project/Second.php")).unwrap();
    assert!(!second.payload.0.is_empty());
    assert_eq!(persisted.decode().unwrap().path(), document.path.as_str());
    release_codec_pool();
    assert_eq!(persisted.decode().unwrap().path(), document.path.as_str());
}

#[test]
fn uses_standard_raw_deflate() {
    let packed = graph("/project/Portable.php");
    let persisted = encode_workspace_graph(&packed).unwrap();
    let mut raw = Vec::new();
    flate2::read::DeflateDecoder::new(persisted.payload.0.as_slice())
        .read_to_end(&mut raw)
        .unwrap();
    assert_eq!(raw, packed.encode());
    assert_eq!(
        crate::persistence::inflate_payload(&persisted.payload.0).unwrap(),
        raw
    );
    assert_eq!(WorkspaceGraph::decode(&raw).unwrap().path(), packed.path());
}

#[test]
fn envelope_borrows_payload() {
    let original = PersistedWorkspaceGraph {
        format: PERSISTED_WORKSPACE_GRAPH_FORMAT,
        payload: Payload(vec![1, 2, 3, 4]),
    };
    let encoded = swlsp_indexer::encode_value(&original).unwrap();
    let decoded = decode_persisted_workspace_graph_borrowed(&encoded).unwrap();
    assert_eq!(decoded.format, original.format);
    assert_eq!(decoded.payload, original.payload.0.as_slice());
    let payload_start = decoded.payload.as_ptr() as usize - encoded.as_ptr() as usize;
    assert!(payload_start < encoded.len());
    let round_trip: PersistedWorkspaceGraph = swlsp_indexer::decode_value(&encoded).unwrap();
    assert_eq!(round_trip, original);
}

#[test]
fn envelope_accepts_either_field_order() {
    #[derive(Serialize)]
    struct Reversed {
        payload: Payload,
        format: u16,
    }
    let encoded = rmp_serde::to_vec_named(&Reversed {
        payload: Payload(vec![1, 2]),
        format: u16::from(PERSISTED_WORKSPACE_GRAPH_FORMAT),
    })
    .unwrap();
    let decoded = decode_persisted_workspace_graph_borrowed(&encoded).unwrap();
    assert_eq!(decoded.format, PERSISTED_WORKSPACE_GRAPH_FORMAT);
    assert_eq!(decoded.payload, &[1, 2]);
}

#[test]
fn envelope_rejects_malformed_values() {
    use std::collections::BTreeMap;
    #[derive(Serialize)]
    struct Envelope<F, P> {
        format: F,
        payload: P,
    }
    let not_a_map = rmp_serde::to_vec(&vec![1, 2]).unwrap();
    let mut wrong_fields = BTreeMap::new();
    wrong_fields.insert("format", 1);
    let wrong_fields = rmp_serde::to_vec_named(&wrong_fields).unwrap();
    #[derive(Serialize)]
    struct Unknown {
        format: u8,
        unknown: Payload,
    }
    let unknown = rmp_serde::to_vec_named(&Unknown {
        format: 1,
        unknown: Payload(vec![1]),
    })
    .unwrap();
    let overflow = rmp_serde::to_vec_named(&Envelope {
        format: 256u16,
        payload: Payload(vec![1]),
    })
    .unwrap();
    let invalid_payload = rmp_serde::to_vec_named(&Envelope {
        format: 1u8,
        payload: 1u8,
    })
    .unwrap();
    for (name, encoded) in [
        ("not a map", not_a_map),
        ("wrong fields", wrong_fields),
        ("unknown field", unknown),
        ("format overflow", overflow),
        ("invalid payload", invalid_payload),
    ] {
        assert!(
            decode_persisted_workspace_graph_borrowed(&encoded).is_err(),
            "{name}"
        );
    }

    let valid = swlsp_indexer::encode_value(&PersistedWorkspaceGraph {
        format: PERSISTED_WORKSPACE_GRAPH_FORMAT,
        payload: Payload(vec![1]),
    })
    .unwrap();
    let mut trailing = valid.clone();
    trailing.push(0);
    assert!(
        decode_persisted_workspace_graph_borrowed(&trailing)
            .unwrap_err()
            .to_string()
            .contains("trailing bytes")
    );
    assert!(decode_persisted_workspace_graph_borrowed(&valid[..valid.len() - 1]).is_err());
}

#[test]
fn rejects_unknown_format() {
    let error = PersistedWorkspaceGraph {
        format: 99,
        payload: Payload::default(),
    }
    .decode()
    .unwrap_err();
    assert!(
        error
            .to_string()
            .contains("unsupported workspace graph format")
    );
}

#[test]
fn rejects_corrupt_payload_without_poisoning_the_pool() {
    assert!(
        PersistedWorkspaceGraph {
            format: PERSISTED_WORKSPACE_GRAPH_FORMAT,
            payload: Payload(b"not-deflate".to_vec()),
        }
        .decode()
        .is_err()
    );
    let mut truncated = encode_workspace_graph(&graph("/project/Truncated.php")).unwrap();
    truncated.payload.0.truncate(truncated.payload.0.len() / 2);
    assert!(truncated.decode().is_err());
    let valid = encode_workspace_graph(&graph("/project/Valid.php")).unwrap();
    assert_eq!(valid.decode().unwrap().path(), "/project/Valid.php");
}

#[test]
fn decodes_concurrently() {
    let persisted = encode_workspace_graph(&graph("/project/Concurrent.php")).unwrap();
    std::thread::scope(|scope| {
        for _ in 0..32 {
            scope.spawn(|| {
                for _ in 0..8 {
                    assert_eq!(
                        persisted.decode().unwrap().path(),
                        "/project/Concurrent.php"
                    );
                }
            });
        }
    });
}

#[test]
fn compresses_large_graphs() {
    let symbols = (0..2000)
        .map(|index| Symbol {
            id: format!("class-{index}").into(),
            kind: SymbolKind::Class,
            name: format!("Class{index}").into(),
            fully_qualified: format!("App\\Class{index}").into(),
            path: "/project/Large.php".into(),
            ..Symbol::default()
        })
        .collect();
    let packed = WorkspaceGraph::pack_owned(Document {
        path: "/project/Large.php".into(),
        symbols,
        ..Document::default()
    });
    let persisted = encode_workspace_graph(&packed).unwrap();
    assert!(persisted.payload.0.len() < packed.encode().len());
    assert_eq!(persisted.decode().unwrap().symbol_count(), 2000);
}

#[test]
fn round_trips_highly_compressible_graphs_larger_than_the_initial_buffer() {
    let symbols = (0..20_000)
        .map(|index| Symbol {
            id: format!("method-{index}").into(),
            kind: SymbolKind::Method,
            name: "sameName".into(),
            fully_qualified: "App\\Service::sameName".into(),
            container: "service".into(),
            path: "/project/Repetitive.php".into(),
            ..Symbol::default()
        })
        .collect();
    let packed = WorkspaceGraph::pack_owned(Document {
        path: "/project/Repetitive.php".into(),
        symbols,
        ..Document::default()
    });
    let persisted = encode_workspace_graph(&packed).unwrap();
    // The decoded size exceeds the first output allocation, so inflation
    // spans several output chunks with back-references across them.
    assert!(packed.encode().len() > persisted.payload.0.len() * 4 + 4096);
    assert_eq!(persisted.decode().unwrap().symbol_count(), 20_000);
}
