use swlsp_php_types::{Kind, must_parse, parse_native};

use crate::{Import, ImportKind, NameContext, parse_use_declaration};

#[test]
fn imported_name_lookup() {
    let mut context = NameContext::new("App");
    context.add_import(&Import::new(
        ImportKind::Class,
        "ProductRepository",
        "Shopware\\ProductRepository",
    ));
    context.add_import(&Import::new(
        ImportKind::Function,
        "BuildProduct",
        "Shopware\\BuildProduct",
    ));
    assert_eq!(
        context.resolve_class("ProductRepository"),
        "Shopware\\ProductRepository"
    );
    let mut count = 0;
    context.visit_function_names("BuildProduct", |_| {
        count += 1;
        true
    });
    assert_eq!(count, 1);
}

#[test]
fn name_resolution() {
    let mut context = NameContext::new("App\\Domain");
    assert!(context.imports.classes.is_empty());
    assert!(context.imports.functions.is_empty());
    assert!(context.imports.constants.is_empty());
    for imported in parse_use_declaration(
        r"use Vendor\Package\{Thing as Imported, function build, const VERSION};",
    ) {
        context.add_import(&imported);
    }
    context.add_import(&Import::new(ImportKind::Class, "Base", "Vendor\\Base"));

    assert_eq!(context.resolve_class("Imported"), "Vendor\\Package\\Thing");
    assert_eq!(context.resolve_class("Base\\Child"), "Vendor\\Base\\Child");
    assert_eq!(context.resolve_class("Local"), "App\\Domain\\Local");
    assert_eq!(context.resolve_class("\\Absolute\\Name"), "Absolute\\Name");
    assert_eq!(context.resolve_class("namespace\\Sub"), "App\\Domain\\Sub");
    assert_eq!(
        context.resolve_function("build"),
        ["Vendor\\Package\\build"]
    );
    assert_eq!(
        context.resolve_function("fallback"),
        ["App\\Domain\\fallback", "fallback"]
    );
    assert_eq!(
        context.resolve_constant("VERSION"),
        ["Vendor\\Package\\VERSION"]
    );
    assert_eq!(
        context.resolve_constant("version"),
        ["App\\Domain\\version", "version"]
    );

    let collect = |function: bool, name: &str| {
        let mut result = Vec::new();
        let visit = |value: &str| {
            result.push(value.to_owned());
            true
        };
        let complete = if function {
            context.visit_function_names(name, visit)
        } else {
            context.visit_constant_names(name, visit)
        };
        assert!(complete);
        result
    };
    assert_eq!(
        context.resolve_function("fallback"),
        collect(true, "fallback")
    );
    assert_eq!(
        context.resolve_constant("VERSION"),
        collect(false, "VERSION")
    );
    let mut visited = 0;
    assert!(!context.visit_function_names("fallback", |_| {
        visited += 1;
        false
    }));
    assert_eq!(visited, 1);
}

#[test]
fn name_context_resolves_absolute_native_type() {
    let context = NameContext::new("App\\Feature");
    let value = parse_native(r"\Throwable").unwrap();
    assert_eq!(context.resolve_type(&value).text(), "Throwable");
}

#[test]
fn resolve_nested_type_names() {
    let mut context = NameContext::new("App");
    context.add_import(&Import::new(
        ImportKind::Class,
        "Product",
        "Shopware\\Product",
    ));
    let value = must_parse("array<string,list<Product|null>>");
    assert_eq!(
        context.resolve_type(&value).text(),
        "array<string,list<Shopware\\Product|null>>"
    );
}

#[test]
fn name_context_preserves_phpdoc_templates() {
    let context = NameContext::new("App");
    let value = must_parse("array<T,Entity>");
    let resolved = context.resolve_phpdoc_type(&value, &["T"]);
    assert_eq!(resolved.text(), "array<T,App\\Entity>");
    assert_eq!(resolved.arguments()[0].kind(), Kind::Template);
}

#[test]
fn name_context_resolves_generic_self_arguments() {
    let context = NameContext::new("App");
    let value = must_parse("self<T&RuleError>");
    let resolved = context.resolve_phpdoc_type(&value, &["T"]);
    assert_eq!(resolved.text(), "self<App\\RuleError&T>");
    assert_eq!(resolved.argument(0).argument(1).kind(), Kind::Template);
}

#[test]
fn name_context_applies_phpdoc_aliases() {
    let mut context = NameContext::new("App");
    let alias = swlsp_php_types::phpdoc_alias("App\\Repo", "Row");
    context.set_phpdoc_aliases([("Row".into(), alias.clone())].into_iter().collect());
    assert_eq!(
        context.resolve_type(&must_parse("list<Row>")),
        swlsp_php_types::Type::list(alias)
    );
    assert_eq!(
        context.resolve_type(&must_parse("Other\\Row")).text(),
        "App\\Other\\Row"
    );
}

#[test]
fn parse_use_declarations() {
    assert_eq!(
        parse_use_declaration(r"use function Vendor\run, Other\build as local;"),
        [
            Import::new(ImportKind::Function, "run", "Vendor\\run"),
            Import::new(ImportKind::Function, "local", "Other\\build"),
        ]
    );
    assert_eq!(
        parse_use_declaration("use Vendor\\Package\n\tas  Alias;"),
        [Import::new(ImportKind::Class, "Alias", "Vendor\\Package")]
    );
    assert_eq!(
        parse_use_declaration("use /* c */ Vendor\\A, # note\n Vendor\\B; // trailing"),
        [
            Import::new(ImportKind::Class, "A", "Vendor\\A"),
            Import::new(ImportKind::Class, "B", "Vendor\\B"),
        ]
    );
    assert!(parse_use_declaration("use Vendor\\{A").is_empty());
    assert_eq!(
        parse_use_declaration("use Foo /* é"),
        [Import::new(ImportKind::Class, "Foo", "Foo")]
    );
}

#[test]
fn add_use_declaration_populates_lowercase_keys() {
    let mut context = NameContext::default();
    context.add_use_declaration("use function vendor\\Run, other\\build as Local;");
    context.add_use_declaration("use const vendor\\Max;");
    assert_eq!(
        context.imports.functions.get("run").map(|v| &**v),
        Some("vendor\\Run")
    );
    assert_eq!(
        context.imports.functions.get("local").map(|v| &**v),
        Some("other\\build")
    );
    assert_eq!(
        context.imports.constants.get("Max").map(|v| &**v),
        Some("vendor\\Max")
    );
    // Cloned contexts share imports until one of them changes.
    let shared = context.clone();
    context.add_use_declaration("use Vendor\\Thing;");
    assert!(shared.imports.classes.is_empty());
    assert_eq!(context.resolve_class("thing"), "Vendor\\Thing");
}
