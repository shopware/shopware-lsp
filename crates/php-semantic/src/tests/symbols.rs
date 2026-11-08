use super::*;

#[test]
fn new_symbol_id_case_folds_ascii_and_unicode() {
    assert_eq!(
        new_symbol_id(SymbolKind::Class, "app\\product", "/product.php", 0),
        new_symbol_id(SymbolKind::Class, "\\APP\\Product", "/product.php", 0)
    );
    assert_eq!(
        new_symbol_id(SymbolKind::Class, "äpp\\größe", "/unicode.php", 0),
        new_symbol_id(SymbolKind::Class, "\\ÄPP\\GRÖẞE", "/unicode.php", 0)
    );
    assert_eq!(
        new_symbol_id(SymbolKind::Class, "App\\Product", "/first.php", 1),
        new_symbol_id(
            SymbolKind::Class,
            "App\\Product",
            "/other/long/path.php",
            99
        )
    );
    assert_ne!(
        new_symbol_id(SymbolKind::Local, "", "/first.php", 1),
        new_symbol_id(SymbolKind::Local, "", "/first.php", 2)
    );
    assert_eq!(
        new_symbol_id(SymbolKind::Class, "App\\Product", "/p.php", 0),
        "1:app\\product"
    );
    assert_eq!(
        new_symbol_id(SymbolKind::Local, "", "/p.php", 7),
        "13:/p.php:7"
    );
    assert_eq!(anonymous_class_name("/a.php", 12), "{anonymous@/a.php:12}");
}

#[test]
fn symbol_metadata_is_copy_on_write() {
    let mut symbol = Symbol::default();
    assert!(symbol.attributes().is_empty());
    assert!(symbol.constant_array().is_empty());
    assert_eq!(symbol.doc_summary(), "");
    symbol.set_metadata(
        vec![Attribute {
            name: "Route".into(),
            ..Attribute::default()
        }],
        vec![ConstantArrayItem {
            key: "one".into(),
            value: "1".into(),
            ..ConstantArrayItem::default()
        }],
        "Original summary.",
    );
    let mut copied = symbol.clone();
    copied.set_doc_summary("Changed summary.");
    copied.set_attributes(Vec::new());
    assert_eq!(symbol.doc_summary(), "Original summary.");
    assert_eq!(&*symbol.attributes()[0].name, "Route");
    assert_eq!(copied.doc_summary(), "Changed summary.");
    assert!(copied.attributes().is_empty());
    assert_eq!(&*copied.constant_array()[0].key, "one");
    copied.set_metadata(Vec::new(), Vec::new(), "");
    assert!(copied.metadata.get().is_none());
    assert_eq!(
        copied,
        Symbol {
            metadata: Default::default(),
            ..copied.clone()
        }
    );
}

#[test]
fn symbol_signature_and_hierarchy_are_copy_on_write() {
    let mut symbol = Symbol::default();
    symbol.set_templates(vec![TemplateParameter {
        name: "T".into(),
        ..TemplateParameter::default()
    }]);
    symbol.set_throws(vec![named("RuntimeException")]);
    symbol.set_extends(names(&["Base"]));
    symbol.set_trait_aliases(vec![TraitAlias {
        trait_name: "T".into(),
        method: "a".into(),
        alias: "b".into(),
        ..TraitAlias::default()
    }]);
    let mut copied = symbol.clone();
    copied.set_templates(Vec::new());
    copied.set_implements(names(&["Contract"]));
    assert_eq!(symbol.templates().len(), 1);
    assert!(copied.templates().is_empty());
    assert_eq!(copied.throws().len(), 1);
    assert!(symbol.implements().is_empty());
    assert_eq!(copied.extends(), &names(&["Base"])[..]);
    assert_eq!(&*copied.trait_aliases()[0].alias, "b");
    copied.set_signature_extras(Vec::new(), Vec::new(), Vec::new(), Vec::new(), Vec::new());
    assert!(copied.signature.get().is_none());
    copied.set_hierarchy(
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
        Vec::new(),
    );
    assert!(copied.hierarchy.get().is_none());
}

#[test]
fn attribute_named_matches_qualified_and_short_names() {
    let attributes = vec![Attribute {
        name: "\\JetBrains\\PhpStorm\\NoReturn\\".into(),
        ..Attribute::default()
    }];
    for name in [
        "NoReturn",
        "noreturn",
        "JetBrains\\PhpStorm\\NoReturn",
        "\\JETBRAINS\\PHPSTORM\\NORETURN\\",
    ] {
        assert!(attribute_named(&attributes, name).is_some(), "{name}");
    }
    assert!(attribute_named(&attributes, "Other\\NoReturn").is_none());
}

#[test]
fn deprecation_of_named_attribute_arguments() {
    let string = |value: &str| AttributeValue {
        kind: AttributeValueKind::String,
        value: value.into(),
        ..AttributeValue::default()
    };
    let attribute = Attribute {
        name: "JetBrains\\PhpStorm\\Deprecated".into(),
        arguments: vec![
            AttributeArgument {
                name: "replacement".into(),
                value: string("next()"),
                ..AttributeArgument::default()
            },
            AttributeArgument {
                name: "since".into(),
                value: string("2.0"),
                ..AttributeArgument::default()
            },
            AttributeArgument {
                name: "reason".into(),
                value: string("Legacy API"),
                ..AttributeArgument::default()
            },
        ],
        ..Attribute::default()
    };
    assert_eq!(
        deprecation_of(&[attribute]),
        Some(Deprecation {
            reason: "Legacy API".into(),
            replacement: "next()".into(),
            since: "2.0".into()
        })
    );
    let positional = Attribute {
        name: "Deprecated".into(),
        arguments: vec![AttributeArgument {
            value: string("Use other"),
            ..AttributeArgument::default()
        }],
        ..Attribute::default()
    };
    assert_eq!(
        deprecation_of(&[positional]).map(|details| details.reason),
        Some("Use other".into())
    );
    assert!(deprecation_of(&[]).is_none());
}

#[test]
fn call_targets_normalize_names() {
    let function = CallTarget::function(" \\App\\make ");
    assert_eq!(&*function.name, "App\\make");
    let method = CallTarget::method("\\App\\Service", " run ");
    assert_eq!((&*method.class, &*method.name), ("App\\Service", "run"));
    let value = CallValue {
        kind: CallValueKind::String,
        value: "x".into(),
        expression: "'x'".into(),
    };
    assert_eq!(value.label(), "x");
    let constant = CallValue {
        kind: CallValueKind::Constant,
        value: "X".into(),
        expression: "\\X".into(),
    };
    assert_eq!(constant.label(), "\\X");
}

#[test]
fn packed_symbol_is_compact() {
    assert_eq!(std::mem::size_of::<crate::packed::PackedSymbol>(), 72);
    assert_eq!(std::mem::size_of::<crate::packed::PackedParameter>(), 48);
    assert_eq!(std::mem::size_of::<Str>(), 16);
    assert_eq!(std::mem::size_of::<Type>(), 8);
}
