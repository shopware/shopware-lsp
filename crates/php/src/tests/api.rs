use std::sync::Arc;

use swlsp_indexer::{PreparingIndexer, WorkspaceSymbolContributor, WorkspaceSymbolKind};
use swlsp_php_semantic::{Document, Symbol, SymbolKind};

use super::{file, open};
use crate::{NameResolver, PhpIndex, name_resolver_for, reference_at, symbol_at};

fn class(id: &str, name: &str, fully_qualified: &str, path: &str) -> Symbol {
    Symbol {
        id: id.into(),
        kind: SymbolKind::Class,
        name: name.into(),
        fully_qualified: fully_qualified.into(),
        path: path.into(),
        ..Symbol::default()
    }
}

#[test]
fn class_catalog_views_are_reused_and_invalidated_by_snapshot() {
    let config = tempfile::tempdir().unwrap();
    let index = open(config.path());
    index.inner.semantic_store.replace(&Document {
        path: "/project/One.php".into(),
        symbols: vec![class("one", "One", "App\\One", "/project/One.php")],
        ..Document::default()
    });
    let first = index.class_symbols_view();
    let second = index.class_symbols_view();
    assert_eq!(first.len(), 1);
    assert!(Arc::ptr_eq(&first, &second));
    assert_eq!(&*index.class_names_view(), ["App\\One".to_owned()]);

    index.inner.semantic_store.replace(&Document {
        path: "/project/Two.php".into(),
        symbols: vec![class("two", "Two", "App\\Two", "/project/Two.php")],
        ..Document::default()
    });
    assert_eq!(
        &*index.class_names_view(),
        ["App\\One".to_owned(), "App\\Two".to_owned()]
    );
    index.close().unwrap();
}

#[test]
fn workspace_symbols_use_prepared_semantic_graph() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    let parsed = file(
        "/project/SystemConfigService.php",
        r#"<?php
namespace App\Config;

final class SystemConfigService
{
    public const DOMAIN = 'core';
    private string $value;

    public function getValue(): string
    {
        return $this->value;
    }
}
"#,
    );
    let prepared = idx.prepare(&parsed).unwrap();
    let symbols = idx.workspace_symbols(&parsed, prepared.as_ref()).unwrap();
    let by_name = |name: &str| symbols.iter().find(|symbol| symbol.name == name).unwrap();
    let service = by_name("SystemConfigService");
    assert_eq!(service.kind, WorkspaceSymbolKind::CLASS);
    assert_eq!(
        service.priority,
        swlsp_indexer::WORKSPACE_SYMBOL_PRIORITY_PHP_TYPE
    );
    assert!(
        service
            .aliases
            .contains(&"App\\Config\\SystemConfigService".to_owned())
    );
    assert_eq!(service.container_name, "App\\Config");
    let method = by_name("getValue");
    assert_eq!(method.kind, WorkspaceSymbolKind::METHOD);
    assert_eq!(
        method.priority,
        swlsp_indexer::WORKSPACE_SYMBOL_PRIORITY_PHP_MEMBER
    );
    assert_eq!(method.container_name, "App\\Config\\SystemConfigService");
    assert_eq!(method.range.start.line, 8);
    assert!(method.aliases.is_empty());
    assert_eq!(by_name("DOMAIN").kind, WorkspaceSymbolKind::CONSTANT);
    assert_eq!(by_name("value").kind, WorkspaceSymbolKind::PROPERTY);
    idx.close().unwrap();
}

#[test]
fn member_listings_and_lookup_helpers() {
    let config = tempfile::tempdir().unwrap();
    let idx: PhpIndex = open(config.path());
    let source = r#"<?php
namespace App;
const VERSION = '1';
interface HasName { public const PREFIX = 'p'; }
class Base implements HasName {
    public string $zeta;
    public function beta(): void {}
}
class Child extends Base {
    public const ALPHA = 1;
    public int $alpha;
    public function Alpha(): void {}
    public function gamma(): void { $this->beta(); }
}"#;
    super::index(&idx, "/project/Child.php", source);
    let names = |symbols: Vec<Symbol>| -> Vec<String> {
        symbols
            .into_iter()
            .map(|symbol| symbol.name.to_string())
            .collect()
    };
    assert_eq!(
        names(idx.methods("\\App\\Child")),
        ["Alpha", "beta", "gamma"]
    );
    assert_eq!(names(idx.properties("App\\Child")), ["alpha", "zeta"]);
    // Like Go, the member resolver lets the method `Alpha` shadow the
    // constant `ALPHA` (names fold case across member kinds).
    assert_eq!(names(idx.constants("App\\Child")), ["PREFIX"]);
    assert_eq!(idx.find_properties("App\\Child", "$zeta").len(), 1);
    assert_eq!(idx.find_global_constants("App\\VERSION").len(), 1);
    let constants: Vec<String> = idx
        .constant_symbols()
        .iter()
        .filter(|symbol| symbol.path == "/project/Child.php")
        .map(|symbol| idx.constant_symbol_name(symbol))
        .collect();
    assert_eq!(
        constants,
        ["App\\Child::ALPHA", "App\\HasName::PREFIX", "App\\VERSION"]
    );
    assert!(idx.is_subtype_of("App\\Child", "App\\HasName"));
    assert!(!idx.is_subtype_of("", "App\\HasName"));

    let parsed = swlsp_parser_php::parse(source);
    let document = idx.analyze_tree("/project/Child.php", 1, &parsed.tree);
    let snapshot = idx.semantic_snapshot().with_document(&document);
    let call = source.find("beta();").unwrap() as u32;
    assert_eq!(
        symbol_at(&document, &snapshot, call + 1).unwrap().name,
        "beta"
    );
    assert!(reference_at(&document, call + 1).is_some());
    let declaration = source.find("gamma").unwrap() as u32;
    assert_eq!(
        symbol_at(&document, &snapshot, declaration).unwrap().name,
        "gamma"
    );
    idx.close().unwrap();
}

#[test]
fn name_resolver_uses_namespace_and_imports() {
    let source = "<?php\nnamespace App;\nuse Vendor\\Lib\\Thing as Alias;\nuse Vendor\\{Other};\n";
    let root = swlsp_parser_php::root(source);
    let resolver = NameResolver::new(&root);
    assert_eq!(resolver.namespace(), "App");
    assert_eq!(resolver.resolve("Alias"), "Vendor\\Lib\\Thing");
    assert_eq!(resolver.resolve("Other"), "Vendor\\Other");
    assert_eq!(resolver.resolve("Local"), "App\\Local");

    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    let context = idx.context_for_node(&root, source);
    let first = name_resolver_for(Some(&context), &root);
    let second = name_resolver_for(Some(&context), &root);
    assert!(Arc::ptr_eq(&first, &second));
    idx.close().unwrap();
}

#[test]
fn debug_ast_prints_tree_and_errors() {
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("broken.php");
    std::fs::write(&path, "<?php\nclass {").unwrap();
    let (tree, errors) = crate::debug_ast(&path).unwrap();
    assert!(tree.contains("PhpClassDeclaration") || !tree.is_empty());
    assert!(!errors.is_empty());
    assert!(crate::debug_ast(&directory.path().join("missing.php")).is_err());
}

#[test]
fn assistant_tags_resolve_inherited_parameters_and_siblings() {
    use swlsp_parser_php::query;
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    super::index(
        &idx,
        "/project/Router.php",
        r#"<?php
namespace App;
interface RouterInterface {
    /**
     * @param string $route #Route
     * @param string $domain #TranslationDomain
     */
    public function generate(string $route, string $domain = 'messages'): string;
}
class Router implements RouterInterface {
    public function generate(string $route, string $domain = 'messages'): string { return ''; }
}
/** @param string $id #Service */
function service(string $id): object { return new \stdClass(); }
"#,
    );
    let source = r#"<?php
namespace App;
function run(Router $router): void {
    $router->generate('home', 'admin');
    $router->generate(domain: 'shop', route: 'cart');
    service('mailer');
    $router->generate($dynamic);
}"#;
    let parsed = swlsp_parser_php::parse(source);
    let root = parsed.root();
    let strings = query::nodes(&root, &[swlsp_parser_php::syntax::PHP_STRING]);
    let at = |text: &str| {
        strings
            .iter()
            .find(|node| query::string_value(node) == text)
            .unwrap()
            .clone()
    };
    let context = idx.document_context("/project/run.php", 1, None, &root, source);

    let (range, tags) =
        crate::assistant_argument_tags(&context, &at("home"), &["Route", "Service"]).unwrap();
    assert_eq!(tags, ["Route"]);
    assert_eq!(&source[range], "home");
    assert!(crate::assistant_argument_reference(&context, &at("cart"), "route").is_some());
    assert!(crate::assistant_argument_reference(&context, &at("shop"), "Route").is_none());
    assert!(crate::assistant_argument_reference(&context, &at("mailer"), "Service").is_some());
    assert!(crate::assistant_argument_reference(&context, &at("admin"), "Route").is_none());
    assert_eq!(
        crate::assistant_sibling_string_argument(&context, &at("home"), "TranslationDomain")
            .as_deref(),
        Some("admin")
    );
    assert_eq!(
        crate::assistant_sibling_string_argument(&context, &at("cart"), "TranslationDomain")
            .as_deref(),
        Some("shop")
    );
    idx.close().unwrap();
}

#[test]
fn identity_is_shared_by_clones_and_distinct_per_index() {
    let first_dir = tempfile::tempdir().unwrap();
    let second_dir = tempfile::tempdir().unwrap();
    let first = open(first_dir.path());
    let second = open(second_dir.path());
    assert_eq!(first.identity(), first.clone().identity());
    assert_ne!(first.identity(), second.identity());
    first.close().unwrap();
    second.close().unwrap();
}

/// A phar polyfill declaring `\Attribute` shares the declaration ID of the
/// runtime stub. Like Go, the document whose path sorts last owns the ID
/// (here the `phpstub://` stub, since `/home/...` sorts before it) and every
/// lookup resolves to it. A polyfill below a relative cache directory such as
/// `rs-cache/` sorts after `phpstub://` and wins instead, in Go as in Rust.
#[test]
fn polyfill_sharing_a_stub_id_resolves_like_go() {
    let project = tempfile::tempdir().unwrap();
    std::fs::write(
        project.path().join("composer.json"),
        r#"{"require":{"php":"^8.2"}}"#,
    )
    .unwrap();
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    idx.configure_project(project.path()).unwrap();
    super::index(
        &idx,
        "/home/x/vendor/polyfill/Attribute.php",
        "<?php\n#[Attribute(Attribute::TARGET_CLASS)]\nfinal class Attribute { const TARGET_CLASS = 1; public function __construct(int $flags = Attribute::TARGET_ALL) {} }\n",
    );
    let stub = "phpstub://8.2.0/selected";
    let snapshot = idx.semantic_snapshot();
    let classes: Vec<String> = snapshot
        .classes("Attribute")
        .iter()
        .map(|class| class.path.to_string())
        .collect();
    assert_eq!(classes, [stub]);
    assert_eq!(idx.find_class("Attribute").unwrap().path, stub);
    let globals: Vec<String> = snapshot
        .global_class_views()
        .iter()
        .filter(|view| view.name() == "Attribute")
        .map(|view| view.path().to_owned())
        .collect();
    assert_eq!(globals, [stub]);
    let methods: Vec<String> = idx
        .find_methods("Attribute", "__construct")
        .iter()
        .map(|method| method.path.to_string())
        .collect();
    assert_eq!(methods, [stub]);
    assert!(
        idx.class_symbols()
            .iter()
            .all(|class| class.name != "Attribute")
    );

    let relative = "rs-cache/phar-sources/x/Stringable.php";
    super::index(
        &idx,
        relative,
        "<?php\ninterface Stringable { public function __toString(): string; }\n",
    );
    assert_eq!(idx.find_class("Stringable").unwrap().path, relative);
    assert!(
        idx.class_symbols()
            .iter()
            .any(|class| class.name == "Stringable")
    );
    idx.close().unwrap();
}

/// The class catalog sorts like Go's `sort.Slice` (pdqsort): in a catalog
/// larger than the insertion-sort cutoff, an interface and its `class_alias`
/// declared under the same name in the same file keep Go's (unstable) order.
/// Expected IDs recorded from the Go implementation.
#[test]
fn class_catalog_breaks_same_name_ties_by_position() {
    // Declarations of one name (an interface and its `class_alias`) are
    // ordered by path, source position and ID, the same way for every name
    // and independently of indexing order.
    const COMPATIBILITY: &str = r#"<?php declare(strict_types=1);

namespace Nette\Utils;

use Nette;

if (false) {
	/** @deprecated use Nette\HtmlStringable */
	interface IHtmlString extends Nette\HtmlStringable
	{
	}
} elseif (!interface_exists(IHtmlString::class)) {
	class_alias(Nette\HtmlStringable::class, IHtmlString::class);
}

namespace Nette\Localization;

if (false) {
	/** @deprecated use Nette\Localization\Translator */
	interface ITranslator extends Translator
	{
	}
} elseif (!interface_exists(ITranslator::class)) {
	class_alias(Translator::class, ITranslator::class);
}
"#;
    const EXPECTED: &[&str] = &[
        "2:nette\\localization\\itranslator",
        "1:nette\\localization\\itranslator",
        "1:nette\\utils\\i00",
        "1:nette\\utils\\i01",
        "1:nette\\utils\\i02",
        "1:nette\\utils\\i03",
        "1:nette\\utils\\i04",
        "1:nette\\utils\\i05",
        "1:nette\\utils\\i06",
        "1:nette\\utils\\i07",
        "1:nette\\utils\\i08",
        "1:nette\\utils\\i09",
        "1:nette\\utils\\i10",
        "1:nette\\utils\\i11",
        "1:nette\\utils\\i12",
        "1:nette\\utils\\i13",
        "1:nette\\utils\\i14",
        "1:nette\\utils\\i15",
        "1:nette\\utils\\i16",
        "1:nette\\utils\\i17",
        "1:nette\\utils\\i18",
        "1:nette\\utils\\i19",
        "1:nette\\utils\\i20",
        "1:nette\\utils\\i21",
        "1:nette\\utils\\i22",
        "1:nette\\utils\\i23",
        "1:nette\\utils\\i24",
        "1:nette\\utils\\i25",
        "1:nette\\utils\\i26",
        "1:nette\\utils\\i27",
        "1:nette\\utils\\i28",
        "1:nette\\utils\\i29",
        "1:nette\\utils\\i30",
        "1:nette\\utils\\i31",
        "1:nette\\utils\\i32",
        "1:nette\\utils\\i33",
        "1:nette\\utils\\i34",
        "1:nette\\utils\\i35",
        "1:nette\\utils\\i36",
        "1:nette\\utils\\i37",
        "1:nette\\utils\\i38",
        "1:nette\\utils\\i39",
        "2:nette\\utils\\ihtmlstring",
        "1:nette\\utils\\ihtmlstring",
    ];
    use swlsp_indexer::BatchIndexer;
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    idx.begin_indexing_batch(&[]);
    for number in 0..40 {
        super::index(
            &idx,
            &format!("/project/src/C{number:02}.php"),
            &format!(
                "<?php\nnamespace Nette\\Utils;\nclass I{:02} {{}}\n",
                (number * 17) % 40
            ),
        );
    }
    super::index(&idx, "/project/src/compatibility.php", COMPATIBILITY);
    idx.end_indexing_batch().unwrap();
    let ids: Vec<String> = idx
        .class_symbols()
        .iter()
        .map(|class| class.id.to_string())
        .collect();
    assert_eq!(ids, EXPECTED);
    idx.close().unwrap();
}
