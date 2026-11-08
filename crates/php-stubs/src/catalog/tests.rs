use super::*;

use swlsp_php_semantic::{AttributeArgument, CallReturnContract, CallTarget};

fn version(major: i64, minor: i64) -> ProjectVersion {
    ProjectVersion::new(major, minor, 0)
}

#[test]
fn catalog_round_trip_and_materialization() {
    let value = Catalog {
        format: FORMAT_VERSION,
        repository: "https://example.com/stubs.git".into(),
        commit: "0123456789012345678901234567890123456789".into(),
        versions: vec![
            Version { major: 8, minor: 1 },
            Version { major: 8, minor: 2 },
        ],
        symbols: vec![
            Symbol {
                version_mask: 0b11,
                kind: SymbolKind::Interface,
                name: "Contract".into(),
                fully_qualified: "Runtime\\Contract".into(),
                implements: vec!["Stringable".into()],
                ..Symbol::default()
            },
            Symbol {
                version_mask: 0b10,
                kind: SymbolKind::Method,
                name: "value".into(),
                fully_qualified: "Runtime\\Contract::value".into(),
                container: "Runtime\\Contract".into(),
                return_type: Type::string(),
                native_type: Type::string(),
                attributes: vec![Attribute {
                    name: "JetBrains\\PhpStorm\\Deprecated".into(),
                    arguments: vec![AttributeArgument {
                        name: "reason".into(),
                        value: AttributeValue {
                            kind: AttributeValueKind::String,
                            value: "Use valueNew()".into(),
                            expression: "'Use valueNew()'".into(),
                            items: Vec::new(),
                        },
                        ..AttributeArgument::default()
                    }],
                    ..Attribute::default()
                }],
                ..Symbol::default()
            },
        ],
        contracts: vec![CallContract {
            target: CallTarget::function("identity"),
            return_contract: CallReturnContract {
                kind: CallReturnKind::ArgumentType,
                argument: 0,
                map: Vec::new(),
            },
            ..CallContract::default()
        }],
        ..Catalog::default()
    };
    let encoded = value.encode();
    let decoded = Catalog::decode(&encoded).unwrap();
    assert_eq!(decoded.repository, value.repository);
    assert_eq!(decoded.commit, value.commit);
    assert_eq!(decoded.symbols, value.symbols);
    assert_eq!(decoded.version_mask(version(8, 1)), 1);
    assert_eq!(decoded.version_mask(version(8, 4)), 2);
    assert_eq!(decoded.version_mask(version(7, 4)), 1);
    assert_eq!(decoded.materialize_contracts(), value.contracts);

    let one = decoded.materialize(version(8, 1), "phpstub://8.1/core");
    assert_eq!(one.len(), 1);
    let two = decoded.materialize(version(8, 2), "phpstub://8.2/core");
    assert_eq!(two.len(), 2);
    assert_eq!(two[0].id, two[1].container);
    assert!(two[1].flags.has(Flags::INTERNAL));
    assert_eq!(
        two[1].attributes()[0].arguments[0].value.value,
        "Use valueNew()"
    );
    assert_eq!(two[0].implements(), ["Stringable"]);
}

#[test]
fn decode_rejects_unknown_format() {
    let encoded = Catalog {
        format: FORMAT_VERSION + 1,
        versions: vec![Version { major: 8, minor: 2 }],
        ..Catalog::default()
    }
    .encode();
    let error = Catalog::decode(&encoded).unwrap_err();
    assert!(error.to_string().contains("unsupported format"), "{error}");
}

#[test]
fn decode_rejects_invalid_version_count() {
    let encoded = Catalog {
        format: FORMAT_VERSION,
        ..Catalog::default()
    }
    .encode();
    let error = Catalog::decode(&encoded).unwrap_err();
    assert!(
        error.to_string().contains("invalid version count"),
        "{error}"
    );
}

#[test]
fn catalog_materializes_selected_extension_bundles() {
    let function = |extension: &str, name: &str| Symbol {
        version_mask: 1,
        extension: extension.into(),
        kind: SymbolKind::Function,
        name: name.into(),
        fully_qualified: name.into(),
        ..Symbol::default()
    };
    let exit = |name: &str| CallContract {
        target: CallTarget::function(name),
        exit_point: true,
        ..CallContract::default()
    };
    let mut value = Catalog {
        format: FORMAT_VERSION,
        versions: vec![Version { major: 8, minor: 3 }],
        symbols: vec![function("core", "strlen"), function("curl", "curl_init")],
        contracts: vec![exit("strlen"), exit("curl_init")],
        contract_extensions: vec!["core".into(), "curl".into()],
        ..Catalog::default()
    };
    value.pack_bundles();
    assert!(value.symbols.is_empty());
    assert!(value.contracts.is_empty());
    assert_eq!(value.bundles.len(), 2);
    assert_eq!(value.extension_symbols.len(), 2);
    let value = Catalog::decode(&value.encode()).unwrap();

    let core = ["core".to_owned()];
    let symbols =
        value.materialize_for_extensions(version(8, 3), "phpstub://8.3/selected", Some(&core));
    assert_eq!(symbols.len(), 1);
    assert_eq!(symbols[0].name, "strlen");
    let contracts = value.materialize_contracts_for_extensions(Some(&core));
    assert_eq!(contracts.len(), 1);
    assert_eq!(contracts[0].target.name, "strlen");

    assert_eq!(
        value.materialize(version(8, 3), "phpstub://8.3/all").len(),
        2
    );
}

#[test]
fn reader_skips_unknown_trailing_fields() {
    let mut writer = msgpack::Writer::default();
    writer.array(3);
    writer.str("TKey");
    writer.str("array-key");
    writer.str("mixed");
    let mut decoder = Decoder::new(&writer.data, |value: &str| Str::from(value), owned_blob);
    let template = decoder.template().unwrap();
    assert_eq!(template.name, "TKey");
    assert_eq!(template.bound.text(), "array-key");
    assert!(!template.covariant);

    let mut writer = msgpack::Writer::default();
    writer.map(2);
    writer.str("Future");
    writer.array(2);
    writer.uint(1);
    writer.str("x");
    writer.str("Name");
    writer.str("Deprecated");
    let mut decoder = Decoder::new(&writer.data, |value: &str| Str::from(value), owned_blob);
    assert_eq!(decoder.attribute().unwrap().name, "Deprecated");
}

#[test]
fn embedded_catalog_decodes_without_copying_names() {
    let catalog = crate::generated_catalog();
    assert_eq!(catalog.format, FORMAT_VERSION);
    assert!(!catalog.bundles.is_empty());
    assert!(
        catalog
            .bundles
            .iter()
            .all(|bundle| matches!(bundle.data, Cow::Borrowed(_)))
    );
    assert!(
        catalog
            .extension_symbols
            .iter()
            .all(|symbol| symbol.name.is_static())
    );
}
