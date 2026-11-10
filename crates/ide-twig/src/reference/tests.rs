use super::*;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, ReferenceRequest, ReferencesProvider, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

fn php_index(dir: &Path) -> PhpIndex {
    PhpIndex::open(dir, None).unwrap()
}

pub(crate) fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

pub(crate) fn write(path: &Path, source: &str) {
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, source).unwrap();
}

pub(crate) fn index(indexer: &dyn Indexer, path: &Path, source: &str) {
    indexer
        .index(&ParsedFile::new(p(path), source.as_bytes().to_vec()))
        .unwrap();
}

pub(crate) fn document(path: &Path, source: &str, version: i32) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(file_uri(&p(path)), source, version))
}

pub(crate) fn node_at(document: &TextDocument, offset: usize) -> Option<SyntaxNode> {
    document.root()?.node_at(TextSize::from(offset as u32))
}

/// A request at byte `offset` with the deepest node at that offset.
pub(crate) fn request(
    document: &Arc<TextDocument>,
    offset: usize,
    include_declaration: bool,
) -> ReferenceRequest {
    request_with_node(
        document,
        offset,
        node_at(document, offset),
        include_declaration,
    )
}

pub(crate) fn request_with_node(
    document: &Arc<TextDocument>,
    offset: usize,
    node: Option<SyntaxNode>,
    include_declaration: bool,
) -> ReferenceRequest {
    let (line, character) = document.line_index().position_utf16(offset as u32);
    let mut params = protocol::ReferenceParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    params.context.include_declaration = include_declaration;
    ReferenceRequest {
        params,
        syntax: SyntaxContext {
            document: Some(document.clone()),
            language: document.language,
            root: document.root(),
            token: None,
            node,
        },
    }
}

pub(crate) fn references(
    provider: &dyn ReferencesProvider,
    request: &ReferenceRequest,
) -> Vec<protocol::Location> {
    provider
        .get_references(&Context::background(), request)
        .unwrap()
}

pub(crate) fn uris(locations: &[protocol::Location]) -> Vec<String> {
    locations
        .iter()
        .map(|location| location.uri.clone())
        .collect()
}

pub(crate) fn count_uri(locations: &[protocol::Location], uri: &str) -> usize {
    locations
        .iter()
        .filter(|location| location.uri == uri)
        .count()
}

/// Asserts that the location URIs equal `expected`, ignoring order.
pub(crate) fn assert_uris_match(locations: &[protocol::Location], expected: &[String]) {
    let mut actual = uris(locations);
    let mut expected = expected.to_vec();
    actual.sort();
    expected.sort();
    assert_eq!(actual, expected);
}

fn twig_index(cache: &Path) -> Arc<TwigIndexer> {
    Arc::new(TwigIndexer::new(cache, None).unwrap())
}

fn needle_request(path: &Path, source: &str, needle: &str, include: bool) -> ReferenceRequest {
    let document = document(path, source, 1);
    request(&document, source.find(needle).unwrap() + 2, include)
}

#[test]
fn live_component_event_references_include_php_and_twig_emissions() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let php = php_index(&cache);
    let components = Arc::new(swlsp_twigcomponent::Index::new(&cache, None).unwrap());
    components.set_dependencies(Some(php.clone()), None, None);
    let class_path = root.path().join("src/Twig/Components/Cart.php");
    let php_emitter = root.path().join("src/Emitter.php");
    let twig_emitter = root.path().join("templates/Cart.html.twig");
    let class = r"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveListener;
#[AsLiveComponent]
final class Cart {
    #[LiveListener('productAdded')]
    public function refresh(): void {}
}";
    let twig_source =
        r#"<button data-action="live#emit" data-live-event-param="productAdded"></button>"#;
    for (path, source) in [
        (&class_path, class),
        (
            &php_emitter,
            "<?php class Emitter {\n    public function save(): void { $this->emit('productAdded'); }\n}",
        ),
        (&twig_emitter, twig_source),
    ] {
        write(path, source);
        self::index(components.as_ref(), path, source);
    }
    self::index(&php, &class_path, class);
    self::index(components.as_ref(), &class_path, class);

    let document = document(&twig_emitter, twig_source, 1);
    let offset = twig_source.find("productAdded").unwrap() + 1;
    let locations = references(
        &LiveComponentEventReferenceProvider::new(Some(components)),
        &request(&document, offset, true),
    );
    assert_eq!(locations.len(), 3);
    for path in [&twig_emitter, &php_emitter, &class_path] {
        assert_eq!(count_uri(&locations, &file_uri(&p(path))), 1, "{path:?}");
    }
}

#[test]
fn twig_component_references_overlay_current_document() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let template = root.path().join("templates/components/Alert.html.twig");
    let current = root.path().join("templates/page.html.twig");
    let other = root.path().join("templates/other.html.twig");
    let twig = twig_index(&cache);
    let components = Arc::new(swlsp_twigcomponent::Index::new(&cache, None).unwrap());
    components.set_dependencies(None, None, Some(twig.clone()));
    for (path, source) in [
        (&template, "<div></div>"),
        (&current, "{{ component('Alert') }}"),
        (&other, "<twig:Alert />"),
    ] {
        write(path, source);
        self::index(twig.as_ref(), path, source);
        self::index(components.as_ref(), path, source);
    }
    let unsaved = "<twig:Alert /> {{ component('Alert') }}";
    let document = document(&current, unsaved, 2);
    let offset = unsaved.find("Alert").unwrap() + 2;
    let locations = references(
        &TwigComponentReferenceProvider::new(Some(components), None),
        &request(&document, offset, true),
    );
    assert_eq!(locations.len(), 4, "{locations:#?}");
    assert_eq!(count_uri(&locations, &file_uri(&p(&current))), 2);
    assert_eq!(count_uri(&locations, &file_uri(&p(&other))), 1);
    assert_eq!(count_uri(&locations, &file_uri(&p(&template))), 1);
}

#[test]
fn twig_live_action_references_include_every_use_and_declaration() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let php = PhpIndex::open(&cache, None).unwrap();
    let twig = twig_index(&cache);
    let components = Arc::new(swlsp_twigcomponent::Index::new(&cache, None).unwrap());
    components.set_dependencies(Some(php.clone()), None, Some(twig.clone()));
    let class_path = root.path().join("src/Twig/Components/Cart.php");
    let template_path = root.path().join("templates/components/Cart.html.twig");
    let class = r"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveAction;
#[AsLiveComponent]
final class Cart {
    #[LiveAction]
    public function save(): void {}
}";
    let template = "{{ live_action('save') }}\n<button data-live-action-param=\"debounce(300)|save\">Save</button>";
    for (path, source) in [(&class_path, class), (&template_path, template)] {
        write(path, source);
        self::index(components.as_ref(), path, source);
    }
    self::index(&php, &class_path, class);
    self::index(twig.as_ref(), &template_path, template);
    // Reindex the component after the PHP dependency has the method symbols.
    self::index(components.as_ref(), &class_path, class);

    let document = document(&template_path, template, 1);
    let offset = template.find("save").unwrap() + 2;
    let locations = references(
        &TwigComponentReferenceProvider::new(Some(components), Some(php)),
        &request(&document, offset, true),
    );
    assert_eq!(locations.len(), 3, "{locations:#?}");
    assert_eq!(count_uri(&locations, &file_uri(&p(&template_path))), 2);
    assert_eq!(count_uri(&locations, &file_uri(&p(&class_path))), 1);
}

#[test]
fn twig_macro_references_connect_declaration_imports_and_calls() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let macro_path = root.path().join("templates/macros/forms.html.twig");
    let page_path = root.path().join("templates/page.html.twig");
    let other_path = root.path().join("templates/other.html.twig");
    let page_source =
        "{% import 'macros/forms.html.twig' as forms %}\n{{ forms.input('email') }}\n";
    let index = twig_index(cache.path());
    for (path, source) in [
        (&macro_path, "{% macro input(name) %}{% endmacro %}"),
        (&page_path, page_source),
        (
            &other_path,
            "{% from 'macros/forms.html.twig' import input as field %}\n{{ field('name') }}\n",
        ),
    ] {
        write(path, source);
        self::index(index.as_ref(), path, source);
    }
    let document = document(&page_path, page_source, 1);
    let offset = page_source.find("forms.input").unwrap() + "forms.".len() + 2;
    let locations = references(
        &TwigMacroReferenceProvider::new(Some(index)),
        &request(&document, offset, true),
    );
    assert_eq!(locations.len(), 4, "{locations:#?}");
    assert_eq!(count_uri(&locations, &file_uri(&p(&macro_path))), 1);
    assert_eq!(count_uri(&locations, &file_uri(&p(&page_path))), 1);
    assert_eq!(count_uri(&locations, &file_uri(&p(&other_path))), 2);
}

#[test]
fn twig_constant_references_bridge_php_and_twig_usages() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let constants_path = root.path().join("src/CardSuite.php");
    let constants = r"<?php
namespace App;
class CardSuite {
    public const CLUBS = 'clubs';
    public const SPADES = 'spades';
}
const GLOBAL_SUIT = 'global';";
    write(&constants_path, constants);
    let php = PhpIndex::open(cache.path(), None).unwrap();
    self::index(&php, &constants_path, constants);
    let twig = twig_index(cache.path());
    twig.set_dependencies(Some(php.clone()), None);
    let direct_path = root.path().join("templates/direct.html.twig");
    let direct = r"{{ constant('App\\CardSuite::CLUBS') }}";
    let object_path = root.path().join("templates/object.html.twig");
    let other_path = root.path().join("templates/other.html.twig");
    for (path, source) in [
        (&direct_path, direct),
        (
            &object_path,
            "{# @var suite \\App\\CardSuite #}\n{{ constant('CLUBS', suite) }}",
        ),
        (&other_path, r"{{ constant('App\\CardSuite::SPADES') }}"),
    ] {
        write(path, source);
        self::index(twig.as_ref(), path, source);
    }
    let provider = TwigConstantReferenceProvider::new(Some(twig), Some(php));
    let from_twig = references(
        &provider,
        &needle_request(&direct_path, direct, "CLUBS", true),
    );
    assert_uris_match(
        &from_twig,
        &[&constants_path, &direct_path, &object_path].map(|path| file_uri(&p(path))),
    );
    let from_php = references(
        &provider,
        &needle_request(&constants_path, constants, "CLUBS", true),
    );
    assert_uris_match(
        &from_php,
        &[&direct_path, &object_path].map(|path| file_uri(&p(path))),
    );
}

#[test]
fn twig_global_constant_references_normalize_leading_slash() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let constants_path = root.path().join("src/Global.php");
    let constants =
        "<?php namespace BugDemo;\nconst NAMESPACED_CONST = 'value';\nconst OTHER_CONST = 'other';";
    write(&constants_path, constants);
    let php = PhpIndex::open(cache.path(), None).unwrap();
    self::index(&php, &constants_path, constants);
    let twig = twig_index(cache.path());
    twig.set_dependencies(Some(php.clone()), None);
    for (name, source) in [
        ("plain", r"{{ constant('BugDemo\\NAMESPACED_CONST') }}"),
        ("leading", r"{{ constant('\\BugDemo\\NAMESPACED_CONST') }}"),
        ("other", r"{{ constant('BugDemo\\OTHER_CONST') }}"),
    ] {
        let path = root.path().join(format!("templates/{name}.html.twig"));
        write(&path, source);
        self::index(twig.as_ref(), &path, source);
    }
    let provider = TwigConstantReferenceProvider::new(Some(twig), Some(php));
    let path = root.path().join("templates/plain.html.twig");
    let locations = references(
        &provider,
        &needle_request(
            &path,
            r"{{ constant('BugDemo\\NAMESPACED_CONST') }}",
            "NAMESPACED_CONST",
            false,
        ),
    );
    assert_eq!(locations.len(), 2, "{locations:#?}");
}

#[test]
fn twig_php_reference_provider_finds_typed_members_and_classes() {
    let root = tempfile::tempdir().unwrap();
    let php_cache = tempfile::tempdir().unwrap();
    let twig_cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(php_cache.path(), None).unwrap();
    let php_path = root.path().join("src/Model.php");
    let php_source = r"<?php
namespace App;
class Product {
    public string $name;
    public function getNumber(): string { return ''; }
}
enum Status { case ACTIVE; }
";
    write(&php_path, php_source);
    self::index(&php, &php_path, php_source);
    let twig = twig_index(twig_cache.path());
    twig.set_dependencies(Some(php.clone()), None);
    for (name, source) in [
        (
            "member.html.twig",
            "{# @var product \\App\\Product #}\n{{ product.number }} {{ product.getNumber() }}",
        ),
        (
            "class.html.twig",
            "{# @var status \\App\\Status #}\n{{ enum_cases('App\\\\Status') }}",
        ),
    ] {
        let path = root.path().join("templates").join(name);
        write(&path, source);
        self::index(twig.as_ref(), &path, source);
    }
    let provider = TwigPhpReferenceProvider::new(Some(twig), Some(php));
    for (needle, expected) in [("getNumber", 2), ("Status {", 2)] {
        let locations = references(
            &provider,
            &needle_request(&php_path, php_source, needle, false),
        );
        assert_eq!(locations.len(), expected, "{needle}: {locations:#?}");
    }
}

#[test]
fn twig_php_reference_provider_finds_exact_extension_symbol_usages_from_twig() {
    let root = tempfile::tempdir().unwrap();
    let php_cache = tempfile::tempdir().unwrap();
    let twig_cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(php_cache.path(), None).unwrap();
    let twig = twig_index(twig_cache.path());
    twig.set_dependencies(Some(php.clone()), None);
    let mut paths = std::collections::HashMap::new();
    for (name, source) in [
        ("index.html.twig", "{{ form_start() }}"),
        ("secondary.html.twig", "{{ form_start() }}"),
        ("other.html.twig", "{{ form() }} {{ form_end() }}"),
    ] {
        let path = root.path().join("templates").join(name);
        write(&path, source);
        self::index(twig.as_ref(), &path, source);
        paths.insert(name, path);
    }
    let source = "{{ form_start() }}";
    let document = document(&paths["index.html.twig"], source, 2);
    let offset = source.find("form_start").unwrap() + 3;
    let locations = references(
        &TwigPhpReferenceProvider::new(Some(twig), Some(php)),
        &request(&document, offset, false),
    );
    assert_uris_match(
        &locations,
        &[&paths["index.html.twig"], &paths["secondary.html.twig"]].map(|path| file_uri(&p(path))),
    );
}

#[test]
fn twig_php_reference_provider_links_extension_methods_to_twig_usages() {
    let root = tempfile::tempdir().unwrap();
    let php_cache = tempfile::tempdir().unwrap();
    let twig_cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(php_cache.path(), None).unwrap();
    let php_path = root.path().join("src/AppExtension.php");
    let php_source = r"<?php
namespace App\Twig;
use Twig\Attribute\AsTwigFunction;
use Twig\Attribute\AsTwigTest;
class AppExtension {
    #[AsTwigFunction('product_number_function')]
    public function formatProductNumberFunction(string $number): string {
        return $number;
    }

    #[AsTwigTest('product_number_test')]
    public function isProductNumber(string $number): bool {
        return $number !== '';
    }
}";
    write(&php_path, php_source);
    self::index(&php, &php_path, php_source);
    let twig = twig_index(twig_cache.path());
    twig.set_dependencies(Some(php.clone()), None);
    for name in ["first.html.twig", "second.html.twig"] {
        let path = root.path().join("templates").join(name);
        let source = "{{ product_number_function('123') }}\n{% if '123' is product_number_test %}{% endif %}";
        write(&path, source);
        self::index(twig.as_ref(), &path, source);
    }
    // Index the extension after its templates to prove query-time callback
    // resolution does not depend on discovery order.
    self::index(twig.as_ref(), &php_path, php_source);
    let provider = TwigPhpReferenceProvider::new(Some(twig), Some(php));
    let document = document(&php_path, php_source, 1);
    for method in ["formatProductNumberFunction", "isProductNumber"] {
        let offset = php_source.find(method).unwrap() + 3;
        let locations = references(&provider, &request(&document, offset, false));
        assert_eq!(locations.len(), 2, "{method}: {locations:#?}");
    }
}

#[test]
fn twig_template_references_connect_twig_php_and_declaration() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let target = root.path().join("templates/layout/base.html.twig");
    let page = root.path().join("templates/page.html.twig");
    let controller = root.path().join("src/PageController.php");
    let page_source = "{% extends 'layout/base.html.twig' %}";
    let index = twig_index(cache.path());
    for (path, source) in [
        (&target, "{% block body %}{% endblock %}"),
        (&page, page_source),
        (
            &controller,
            "<?php\n#[Template('layout/base.html.twig')]\nfinal class PageController {}\n",
        ),
    ] {
        write(path, source);
        self::index(index.as_ref(), path, source);
    }
    let document = document(&page, page_source, 2);
    let offset = page_source.find("layout/base").unwrap() + 2;
    let locations = references(
        &TwigTemplateReferenceProvider::new(Some(index)),
        &request(&document, offset, true),
    );
    assert_uris_match(
        &locations,
        &[&target, &page, &controller].map(|path| file_uri(&p(path))),
    );
    let start = page_source.find("layout/base.html.twig").unwrap() as u32;
    for location in &locations {
        if location.uri == file_uri(&p(&page)) {
            assert_eq!(location.range.start.character, start);
            assert_eq!(
                location.range.end.character,
                start + "layout/base.html.twig".len() as u32
            );
        }
    }
}

#[test]
fn twig_template_references_use_unsaved_current_document() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let target = root.path().join("templates/new.html.twig");
    let page = root.path().join("templates/page.html.twig");
    let index = twig_index(cache.path());
    for (path, source) in [
        (&target, "target"),
        (&page, "{% include 'old.html.twig' %}"),
    ] {
        write(path, source);
        self::index(index.as_ref(), path, source);
    }
    let source = "{% include 'new.html.twig' %}";
    let document = document(&page, source, 2);
    let offset = source.find("new.html.twig").unwrap() + 1;
    let locations = references(
        &TwigTemplateReferenceProvider::new(Some(index)),
        &request(&document, offset, false),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, document.uri);
}
