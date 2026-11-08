use std::sync::Arc;

use swlsp_lsp::{Context, GotoDefinitionProvider, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

use super::*;
use crate::twig_common::test_support::*;

fn definitions(
    provider: &TwigDefinitionProvider,
    request: &swlsp_lsp::DefinitionRequest,
) -> Vec<protocol::Location> {
    provider.get_definition(&Context::background(), request)
}

/// Go `documentOffsetForPosition`.
fn document_offset_for_position(source: &str, position: protocol::Position) -> usize {
    let document = TextDocument::new("file:///source.php", source, 1);
    document
        .line_index()
        .offset_utf16(position.line, position.character) as usize
}

/// A request with `<caret>` removed and the cursor at its position.
fn caret_request(path: &str, source_with_caret: &str) -> swlsp_lsp::DefinitionRequest {
    let offset = offset_of(source_with_caret, "<caret>");
    let source = source_with_caret.replacen("<caret>", "", 1);
    request_at(TextDocument::new(file_uri(path), &source, 1), offset)
}

// -- templates ----------------------------------------------------------------

#[test]
fn shopware_template_definition_uses_bundle_hierarchy() {
    let cache = tempfile::tempdir().unwrap();
    let twig = twig_index(cache.path());
    let paths = [
        "/project/vendor/store.shopware.com/swagcmsextensions/src/Resources/views/storefront/card.html.twig",
        "/project/custom/plugins/MyTheme/src/Resources/views/storefront/card.html.twig",
    ];
    for path in paths {
        index(&*twig, path, "card");
    }
    let provider = TwigDefinitionProvider::new("/project", Some(twig), None, None);
    for tag in ["sw_include", "sw_extends", "include", "extends"] {
        let source = format!("{{% {tag} \"@SwagCmsExtensions/storefront/card.html.twig\" %}}");
        let offset = offset_of(&source, "SwagCmsExtensions") + 2;
        let locations = definitions(
            &provider,
            &request_at(
                TextDocument::new("file:///project/page.html.twig", &source, 1),
                offset,
            ),
        );
        if !tag.starts_with("sw_") {
            assert!(locations.is_empty(), "{tag}");
            continue;
        }
        assert_eq!(locations.len(), 2, "{tag}");
        let mut uris: Vec<_> = locations.iter().map(|l| l.uri.clone()).collect();
        uris.sort();
        let mut expected = vec![file_uri(paths[0]), file_uri(paths[1])];
        expected.sort();
        assert_eq!(uris, expected);
    }
}

#[test]
fn twig_template_definition_ignores_template_annotation() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let target = format!("{root_path}/templates/page.html.twig");
    write(&target, "page");
    let twig = twig_index(cache.path());
    index(&*twig, &target, "page");
    let source = "<?php\n/** @Template(\"page.html.twig\") */\nfinal class PageController {}\n";
    let path = format!("{root_path}/src/PageController.php");
    let offset = offset_of(source, "page.html.twig") + 2;
    let mut request = request_at(TextDocument::new(file_uri(&path), source, 1), offset);
    // Go leaves the language unset in this request.
    request.syntax.language = None;
    let locations = definitions(
        &TwigDefinitionProvider::new(root_path, Some(twig), None, None),
        &request,
    );
    assert!(locations.is_empty());
}

#[test]
fn phpdoc_template_assistant_tag_definition() {
    let root = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let target = format!("{root_path}/templates/page.html.twig");
    write(&target, "page");
    let twig = twig_index(&root.path().join("twig"));
    index(&*twig, &target, "page");
    let php = php_index(&root.path().join("php"));
    index_php(
        &php,
        &format!("{root_path}/src/TemplateAssistant.php"),
        "<?php\n/** @param string $template #Template */\nfunction resolve_template(string $template): void {}\n",
    );
    let source = "<?php resolve_template('page.html.twig');";
    let path = format!("{root_path}/src/Usage.php");
    let offset = offset_of(source, "page.html.twig") + 3;
    let locations = definitions(
        &TwigDefinitionProvider::new(root_path, Some(twig), None, Some(php)),
        &request_at(TextDocument::new(file_uri(&path), source, 1), offset),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&target));
}

#[test]
fn standalone_php_template_string_definition_is_exact_and_low_noise() {
    let root = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let target = format!("{root_path}/templates/page.html.twig");
    write(&target, "page");
    let twig = twig_index(&root.path().join("twig"));
    index(&*twig, &target, "page");
    let provider = TwigDefinitionProvider::new(root_path.clone(), Some(twig), None, None);
    for (name, source, marker, want) in [
        (
            "standalone exact template",
            "<?php $template = 'page.html.twig';",
            "page.html.twig",
            1,
        ),
        (
            "unknown template",
            "<?php $template = 'missing.html.twig';",
            "missing.html.twig",
            0,
        ),
        (
            "dynamic interpolated string",
            "<?php $template = \"page.$locale.html.twig\";",
            "page.",
            0,
        ),
        (
            "ordinary PHP string",
            "<?php $template = 'page.html.txt';",
            "page.html.txt",
            0,
        ),
    ] {
        let path = format!("{root_path}/src/Standalone.php");
        let offset = offset_of(source, marker) + 2;
        let locations = definitions(
            &provider,
            &request_at(TextDocument::new(file_uri(&path), source, 1), offset),
        );
        assert_eq!(locations.len(), want, "{name}");
        if want != 0 {
            assert_eq!(locations[0].uri, file_uri(&target));
        }
    }
}

// -- custom tags --------------------------------------------------------------

#[test]
fn twig_custom_tag_definition_for_opening_and_closing_tag() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let parser_path = format!("{root_path}/src/LegacyTokenParser.php");
    let parser_source = "<?php
class LegacyTokenParser implements \\Twig\\TokenParser\\TokenParserInterface {
    public function getTag(): string { return 'legacy'; }
}";
    write(&parser_path, parser_source);
    let twig = twig_index(cache.path());
    index(&*twig, &parser_path, parser_source);
    let provider = TwigDefinitionProvider::new(root_path.clone(), Some(twig), None, None);
    for name in ["legacy", "endlegacy"] {
        let source = "{% legacy %}\n{% endlegacy %}";
        let offset = offset_of(source, name) + 2;
        let request = request_at(
            TextDocument::new(
                file_uri(&format!("{root_path}/templates/page.html.twig")),
                source,
                1,
            ),
            offset,
        );
        let locations = definitions(&provider, &request);
        assert_eq!(locations.len(), 1, "{name}");
        assert_eq!(locations[0].uri, file_uri(&parser_path));
        let start = document_offset_for_position(parser_source, locations[0].range.start);
        let end = document_offset_for_position(parser_source, locations[0].range.end);
        assert_eq!(&parser_source[start..end], "legacy");
    }
}

// -- guard --------------------------------------------------------------------

fn single_line(values: Vec<i64>) -> i64 {
    assert_eq!(values.len(), 1);
    values[0]
}

#[test]
fn twig_guard_definition_navigates_to_registered_callables() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let extension_path = format!("{root_path}/src/AppExtension.php");
    let extension_source = "<?php
class AppExtension extends \\Twig\\Extension\\AbstractExtension
{
    public function getFunctions(): array
    {
        return [new \\Twig\\TwigFunction('asset_url', $this->asset(...))];
    }
    public function getFilters(): array
    {
        return [new \\Twig\\TwigFilter('money', $this->money(...))];
    }
    public function getTests(): array
    {
        return [new \\Twig\\TwigTest('positive', $this->positive(...))];
    }
    public function asset(string $path): string { return $path; }
    public function money(int $value): string { return (string) $value; }
    public function positive(int $value): bool { return $value > 0; }
}";
    let twig: Arc<TwigIndexer> = twig_index(cache.path());
    index(&*twig, &extension_path, extension_source);
    let function_line = single_line(
        twig.get_twig_function("asset_url")
            .unwrap()
            .iter()
            .map(|value| value.line)
            .collect(),
    );
    let filter_line = single_line(
        twig.get_twig_filter("money")
            .unwrap()
            .iter()
            .map(|value| value.line)
            .collect(),
    );
    let test_line = single_line(
        twig.get_twig_test("positive")
            .unwrap()
            .iter()
            .map(|value| value.line)
            .collect(),
    );
    let provider = TwigDefinitionProvider::new(root_path.clone(), Some(twig), None, None);
    let page = format!("{root_path}/templates/page.html.twig");
    for (source, line) in [
        ("{% guard function asset_<caret>url %}", function_line),
        ("{% guard filter mon<caret>ey %}", filter_line),
        ("{% guard test pos<caret>itive %}", test_line),
    ] {
        let locations = definitions(&provider, &caret_request(&page, source));
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, file_uri(&extension_path));
        assert_eq!(i64::from(locations[0].range.start.line), line - 1);
    }
}

// -- @see ---------------------------------------------------------------------

#[test]
fn twig_see_definition_resolves_php_template_and_relative_files() {
    let root = tempfile::tempdir().unwrap();
    let php_cache = tempfile::tempdir().unwrap();
    let twig_cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let php_path = format!("{root_path}/src/ProductController.php");
    let php_source = "<?php
namespace App\\Controller;
class ProductController {
    public function show(): void {}
}";
    write(&php_path, php_source);
    let php = php_index(php_cache.path());
    index_php(&php, &php_path, php_source);

    let template_path = format!("{root_path}/templates/product/show.html.twig");
    write(&template_path, "product");
    let twig = twig_index(twig_cache.path());
    index(&*twig, &template_path, "product");

    let current_path = format!("{root_path}/templates/docs/page.html.twig");
    let notes_path = format!("{root_path}/templates/docs/notes.md");
    write(&notes_path, "notes");
    let provider = TwigDefinitionProvider::new(root_path, Some(twig), None, Some(php));
    for (source, target, line) in [
        (
            "{# @see App\\Controller\\ProductController #}",
            &php_path,
            2,
        ),
        (
            "{# @see App\\Controller\\ProductController::show #}",
            &php_path,
            3,
        ),
        ("{# @see product/show.html.twig #}", &template_path, 0),
        ("{# @see notes.md #}", &notes_path, 0),
        (
            "{# App\\Controller\\ProductController:show #}",
            &php_path,
            3,
        ),
    ] {
        let target_start = match source.find("@see ") {
            Some(index) => index + "@see ".len(),
            None => source.find("{# ").unwrap() + "{# ".len(),
        };
        let offset = (target_start + 1) as u32;
        let locations = definitions(
            &provider,
            &request_at(
                TextDocument::new(file_uri(&current_path), source, 1),
                offset,
            ),
        );
        assert!(!locations.is_empty(), "{source}");
        assert_eq!(locations[0].uri, file_uri(target), "{source}");
        assert_eq!(locations[0].range.start.line, line, "{source}");
    }
}

// -- types tag ----------------------------------------------------------------

#[test]
fn twig_types_tag_definition_navigates_to_php_class() {
    let root = tempfile::tempdir().unwrap();
    let php_cache = tempfile::tempdir().unwrap();
    let twig_cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let php_path = format!("{root_path}/src/User.php");
    let php_source = "<?php\nnamespace App\\Entity;\nclass User {}";
    write(&php_path, php_source);
    let php = php_index(php_cache.path());
    index_php(&php, &php_path, php_source);
    let twig = twig_index(twig_cache.path());
    let provider = TwigDefinitionProvider::new(root_path.clone(), Some(twig), None, Some(php));
    let source = "{% types {\n    user?: 'App\\\\Ent<caret>ity\\\\User[]',\n} %}";
    let locations = definitions(
        &provider,
        &caret_request(&format!("{root_path}/templates/page.html.twig"), source),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&php_path));
}

// -- tests --------------------------------------------------------------------

#[test]
fn twig_test_definition_navigates_to_registration() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let extension_path = format!("{root_path}/src/AppExtension.php");
    let source = "<?php
class AppExtension extends \\Twig\\Extension\\AbstractExtension
{
    public function getTests(): array
    {
        return [new \\Twig\\TwigTest('positive', $this->positive(...))];
    }
    public function positive(int $value): bool { return $value > 0; }
}";
    write(&extension_path, source);
    let twig = twig_index(cache.path());
    index(&*twig, &extension_path, source);
    let values = twig.get_twig_test("positive").unwrap();
    assert_eq!(values.len(), 1);
    let provider = TwigDefinitionProvider::new(root_path.clone(), Some(twig), None, None);
    for template in [
        "{% if value is pos<caret>itive %}{% endif %}",
        "{{ value is positive<caret>(1) }}",
        "{% set valid = value is not positive<caret> %}",
    ] {
        let locations = definitions(
            &provider,
            &caret_request(&format!("{root_path}/templates/page.html.twig"), template),
        );
        assert_eq!(locations.len(), 1, "{template}");
        assert_eq!(locations[0].uri, file_uri(&extension_path));
        assert_eq!(i64::from(locations[0].range.start.line), values[0].line - 1);
    }
}

// -- variables ----------------------------------------------------------------

#[test]
fn twig_controller_variable_definition() {
    let root = tempfile::tempdir().unwrap();
    let php_cache = tempfile::tempdir().unwrap();
    let twig_cache = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let controller_path = format!("{root_path}/src/ProductController.php");
    let controller_source = "<?php
namespace App;
class Product {}
class ProductController {
    public function show(Product $product) {
        return $this->render('product/show.html.twig', [
            'product' => $product,
        ]);
    }
}";
    write(&controller_path, controller_source);
    let php = php_index(php_cache.path());
    index_php(&php, &controller_path, controller_source);
    let twig = twig_index(twig_cache.path());

    let template_path = format!("{root_path}/templates/product/show.html.twig");
    let source = "{{ product }}";
    let offset = offset_of(source, "product") + 1;
    // Go leaves the cursor position at 0:0 here.
    let request = request_with_position(
        TextDocument::new(file_uri(&template_path), source, 1),
        offset,
        protocol::Position::default(),
    );
    let locations = definitions(
        &TwigDefinitionProvider::new(root_path, Some(twig), None, Some(php)),
        &request,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&controller_path));
    assert_eq!(locations[0].range.start.line, 6);
}

#[test]
fn twig_global_variable_definition() {
    let root = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let config_path = format!("{root_path}/config/packages/twig.yaml");
    let config = "twig:\n  globals:\n    clock: '@app.clock'\n";
    write(&config_path, config);
    let twig = twig_index(&root.path().join(".cache"));
    index(&*twig, &config_path, config);

    let template_path = format!("{root_path}/templates/page.html.twig");
    let source = "{{ clock }}";
    let offset = offset_of(source, "clock") + 1;
    let request = request_with_position(
        TextDocument::new(file_uri(&template_path), source, 1),
        offset,
        protocol::Position {
            line: 0,
            character: offset,
        },
    );
    let locations = definitions(
        &TwigDefinitionProvider::new(root_path, Some(twig), None, None),
        &request,
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&config_path));
    assert_eq!(locations[0].range.start.line, 2);
    assert_eq!(locations[0].range.start.character, 4);
    assert_eq!(locations[0].range.end.character, 9);
}

// -- PHP members --------------------------------------------------------------

fn php_member_locations(
    php_source: &str,
    source: &str,
    needle: &str,
) -> (String, Vec<protocol::Location>) {
    let root = tempfile::tempdir().unwrap();
    let root_path = p(root.path());
    let php_path = format!("{root_path}/src/Bar.php");
    write(&php_path, php_source);
    let php = php_index(&root.path().join(".cache"));
    index_php(&php, &php_path, php_source);
    let twig = twig_index(&root.path().join(".twig-cache"));
    let offset = source.rfind(needle).unwrap() as u32 + 1;
    let request = request_with_position(
        TextDocument::new(
            file_uri(&format!("{root_path}/templates/page.html.twig")),
            source,
            1,
        ),
        offset,
        protocol::Position {
            line: 0,
            character: offset,
        },
    );
    let locations = definitions(
        &TwigDefinitionProvider::new(root_path, Some(twig), None, Some(php)),
        &request,
    );
    (php_path, locations)
}

#[test]
fn twig_php_member_definition() {
    let (php_path, locations) = php_member_locations(
        "<?php
namespace Foo;
class Bar {
    public const KIND = 'bar';
    /** @deprecated */
    public function getDeprecated(): static { return $this; }
}",
        "{# @var bar \\Foo\\Bar #} {{ bar.deprecated }}",
        "deprecated",
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&php_path));
    assert_eq!(locations[0].range.start.line, 5);
    assert_eq!(locations[0].range.start.character, 20);
}

#[test]
fn twig_php_class_constant_definition() {
    let (php_path, locations) = php_member_locations(
        "<?php
namespace Foo;
class Bar {
    public const KIND = 'bar';
}",
        "{# @var bar \\Foo\\Bar #} {{ bar.KIND }}",
        "KIND",
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&php_path));
    assert_eq!(locations[0].range.start.line, 3);
    assert_eq!(locations[0].range.start.character, 17);
}
