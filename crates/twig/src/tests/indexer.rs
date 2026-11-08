use std::collections::BTreeSet;
use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{CancelToken, FileScanner, Indexer, ParsedFile};
use swlsp_php::PhpIndex;

use crate::*;

fn file(path: &str, source: &str) -> ParsedFile {
    ParsedFile::new(path, source.as_bytes().to_vec())
}

fn s(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn write(path: &Path, content: &str) {
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, content).unwrap();
}

pub(super) fn php_index(dir: &Path) -> PhpIndex {
    PhpIndex::open(dir, None).unwrap()
}

pub(super) fn index_php(index: &PhpIndex, path: &str, source: &str) {
    Indexer::index(index, &file(path, source)).unwrap();
}

fn scanner_with(root: &Path, twig: &Arc<TwigIndexer>) -> FileScanner {
    let mut scanner = FileScanner::new(s(root), root.join("scanner.db"), None).unwrap();
    scanner.add_indexer(twig.clone());
    scanner
}

#[test]
fn twig_indexer_uses_parser_through_file_scanner() {
    let dir = tempfile::tempdir().unwrap();
    let template = dir
        .path()
        .join("src/Storefront/Resources/views/storefront/page/example.html.twig");
    write(&template, "{% block page_example %}content{% endblock %}");
    let twig = Arc::new(TwigIndexer::new(dir.path().join("cache"), None).unwrap());
    let scanner = scanner_with(dir.path(), &twig);
    scanner
        .index_files(&CancelToken::new(), &[s(&template)])
        .unwrap();
    let files = twig
        .get_twig_files_by_rel_path("@Storefront/storefront/page/example.html.twig")
        .unwrap();
    assert_eq!(files.len(), 1);
    assert!(files[0].blocks.contains_key("page_example"));
    let hashes = twig.get_twig_block_hashes("page_example").unwrap();
    assert_eq!(hashes.len(), 1);
    assert_eq!(hashes[0].absolute_path, s(&template));
    twig.close().unwrap();
}

#[test]
fn twig_indexer_indexes_templates_without_a_twig_extension() {
    let dir = tempfile::tempdir().unwrap();
    let icon = dir
        .path()
        .join("src/Core/Profiling/Resources/views/Collector/checkmark.svg");
    let asset = dir
        .path()
        .join("src/Core/Profiling/Resources/public/checkmark.svg");
    write(&icon, "<svg></svg>");
    write(&asset, "<svg></svg>");
    let twig = Arc::new(TwigIndexer::new(dir.path().join("cache"), None).unwrap());
    let scanner = scanner_with(dir.path(), &twig);
    scanner
        .index_files(&CancelToken::new(), &[s(&icon), s(&asset)])
        .unwrap();
    let files = twig
        .get_twig_files_by_rel_path("@Profiling/Collector/checkmark.svg")
        .unwrap();
    assert_eq!(files.len(), 1);
    assert_eq!(files[0].path, s(&icon));
    assert!(twig.get_twig_file_by_path(&s(&asset)).unwrap().is_none());
    twig.removed_files(&[s(&icon)]).unwrap();
    assert!(
        twig.get_twig_files_by_rel_path("@Profiling/Collector/checkmark.svg")
            .unwrap()
            .is_empty()
    );
    twig.close().unwrap();
}

#[test]
fn twig_indexer_indexes_storefront_dist_through_file_scanner() {
    let dir = tempfile::tempdir().unwrap();
    let dist = dir
        .path()
        .join("src/Storefront/Resources/app/storefront/dist");
    let icon = dist.join("assets/icon/solid/arrow.svg");
    let fragment = dist.join("storefront/fragment.html.twig");
    write(&icon, "<svg></svg>");
    write(&fragment, "{% block fragment %}content{% endblock %}");
    write(
        &dir.path()
            .join("custom/apps/MyApp/Resources/scripts/ping/ping.twig"),
        "ping",
    );
    let twig = Arc::new(TwigIndexer::new(dir.path().join("cache"), None).unwrap());
    let scanner = scanner_with(dir.path(), &twig);
    scanner.index_all(&CancelToken::new()).unwrap();
    let files = twig
        .get_twig_files_by_rel_path("@Storefront/assets/icon/solid/arrow.svg")
        .unwrap();
    assert_eq!(files.len(), 1);
    assert_eq!(files[0].path, s(&icon));
    let files = twig
        .get_twig_files_by_rel_path("@Storefront/storefront/fragment.html.twig")
        .unwrap();
    assert_eq!(files.len(), 1);
    assert_eq!(files[0].path, s(&fragment));
    for name in twig.get_all_template_files().unwrap() {
        assert!(!name.contains(&s(dir.path())), "{name}");
        assert!(!name.contains("scripts/ping"), "{name}");
    }
    twig.removed_files(&[s(&icon)]).unwrap();
    assert!(
        twig.get_twig_files_by_rel_path("@Storefront/assets/icon/solid/arrow.svg")
            .unwrap()
            .is_empty()
    );
    twig.close().unwrap();
}

#[test]
fn twig_indexer_stores_symfony_template_aliases() {
    let cache = tempfile::tempdir().unwrap();
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    let path = "/project/MyBundle/src/Resources/views/card.html.twig";
    index.index(&file(path, "card")).unwrap();
    for name in [
        "card.html.twig",
        "@Storefront/card.html.twig",
        "@MyBundle/card.html.twig",
    ] {
        let files = index.get_twig_files_by_rel_path(name).unwrap();
        assert_eq!(files.len(), 1, "{name}");
        assert_eq!(files[0].path, path);
    }
    assert_eq!(
        index.get_twig_file_by_path(path).unwrap().unwrap().path,
        path
    );
    index.close().unwrap();
}

#[test]
fn twig_indexer_stores_and_restores_macros_and_usages() {
    let cache = tempfile::tempdir().unwrap();
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    let page = "/project/templates/page.html.twig";
    index
        .index(&file(
            "/project/templates/macros/forms.html.twig",
            "{## Renders a form input. ##}\n{% macro input(## Field name.\nname, value = '') %}{% endmacro %}",
        ))
        .unwrap();
    index
        .index(&file(
            page,
            "{% import 'macros/forms.html.twig' as forms %}\n{{ forms.input('email') }}\n",
        ))
        .unwrap();
    let check = |index: &TwigIndexer| {
        let macros = index.find_macro("macros/forms.html.twig", "input").unwrap();
        assert_eq!(macros.len(), 1);
        assert_eq!(macros[0].signature(), "input(name, value = '')");
        assert_eq!(macros[0].documentation, "Renders a form input.");
        assert_eq!(macros[0].parameters[0].documentation, "Field name.");
        let usages = index
            .get_macro_usages("macros/forms.html.twig", "input")
            .unwrap();
        assert_eq!(usages.len(), 1);
        assert_eq!(usages[0].file_path, page);
    };
    check(&index);
    index.close().unwrap();
    let restored = TwigIndexer::new(cache.path(), None).unwrap();
    check(&restored);
    restored.close().unwrap();
}

#[test]
fn twig_indexer_stores_restores_and_clears_template_references() {
    let cache = tempfile::tempdir().unwrap();
    let dir = tempfile::tempdir().unwrap();
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    let target = s(&dir.path().join("a/templates/layout/base.html.twig"));
    let twig_usage = s(&dir.path().join("b/templates/page.html.twig"));
    let php_usage = s(&dir.path().join("c/PageController.php"));
    index
        .index(&file(&target, "{% block body %}{% endblock %}"))
        .unwrap();
    index
        .index(&file(&twig_usage, "{% extends 'layout/base.html.twig' %}"))
        .unwrap();
    index
        .index(&file(
            &php_usage,
            "<?php $this->render('layout/base.html.twig');",
        ))
        .unwrap();
    let paths = |index: &TwigIndexer| -> BTreeSet<String> {
        index
            .get_template_references(&["layout/base.html.twig"])
            .unwrap()
            .into_iter()
            .map(|reference| reference.file_path)
            .collect()
    };
    assert_eq!(
        paths(&index),
        BTreeSet::from([php_usage.clone(), twig_usage.clone()])
    );
    index.close().unwrap();
    let restored = TwigIndexer::new(cache.path(), None).unwrap();
    assert_eq!(paths(&restored).len(), 2);
    restored
        .index(&file(&twig_usage, "no template reference"))
        .unwrap();
    assert_eq!(paths(&restored), BTreeSet::from([php_usage]));
    restored.close().unwrap();
}

#[test]
fn twig_indexer_stores_restores_and_clears_constant_references() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(cache.path());
    index_php(
        &php,
        "/project/src/CardSuite.php",
        "<?php\nnamespace App;\nclass CardSuite {\n    public const CLUBS = 'clubs';\n}",
    );
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    index.set_dependencies(Some(php.clone()), None);
    let direct = "/project/templates/direct.html.twig";
    let object = "/project/templates/object.html.twig";
    index
        .index(&file(direct, r"{{ constant('App\\CardSuite::CLUBS') }}"))
        .unwrap();
    index
        .index(&file(
            object,
            "{# @var suite \\App\\CardSuite #}\n{{ constant('CLUBS', suite) }}",
        ))
        .unwrap();
    let target = ConstantReference {
        class: r"App\CardSuite".into(),
        name: "CLUBS".into(),
        ..ConstantReference::default()
    };
    let paths = |index: &TwigIndexer| -> BTreeSet<String> {
        index
            .get_constant_references(&target)
            .unwrap()
            .into_iter()
            .map(|r| r.file_path)
            .collect()
    };
    assert_eq!(
        paths(&index),
        BTreeSet::from([direct.to_owned(), object.to_owned()])
    );
    index.close().unwrap();
    let restored = TwigIndexer::new(cache.path(), None).unwrap();
    restored.set_dependencies(Some(php.clone()), None);
    assert_eq!(paths(&restored).len(), 2);
    restored
        .index(&file(object, "no constant reference"))
        .unwrap();
    assert_eq!(paths(&restored), BTreeSet::from([direct.to_owned()]));
    restored.close().unwrap();
    php.close().unwrap();
}

#[test]
fn twig_indexer_stores_restores_and_clears_php_usages() {
    let cache = tempfile::tempdir().unwrap();
    let php = php_index(cache.path());
    index_php(
        &php,
        "/project/src/Product.php",
        "<?php\nnamespace App;\nclass Product {\n    public string $name;\n    public function getNumber(): string { return ''; }\n}\nenum Status { case ACTIVE; }\n",
    );
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    index.set_dependencies(Some(php.clone()), None);
    let method_path = "/project/templates/method.html.twig";
    index
        .index(&file(
            "/project/templates/property.html.twig",
            r"{# @var product \App\Product #} {{ product.name }}",
        ))
        .unwrap();
    index
        .index(&file(
            method_path,
            r"{# @var product \App\Product #} {{ product.number }}",
        ))
        .unwrap();
    index
        .index(&file(
            "/project/templates/class.html.twig",
            r"{{ enum('App\\Status') }}",
        ))
        .unwrap();
    let property = PhpUsageReference {
        class: r"App\Product".into(),
        member: "name".into(),
        kind: swlsp_php_semantic::SymbolKind::Property,
        ..PhpUsageReference::default()
    };
    let method = PhpUsageReference {
        class: r"App\Product".into(),
        member: "getNumber".into(),
        kind: swlsp_php_semantic::SymbolKind::Method,
        ..PhpUsageReference::default()
    };
    let class = PhpUsageReference {
        class: r"App\Status".into(),
        ..PhpUsageReference::default()
    };
    for target in [&property, &method, &class] {
        assert_eq!(
            index.get_php_usage_references(target).unwrap().len(),
            1,
            "{target:?}"
        );
    }
    index.close().unwrap();
    let restored = TwigIndexer::new(cache.path(), None).unwrap();
    restored.set_dependencies(Some(php.clone()), None);
    let references = restored.get_php_usage_references(&method).unwrap();
    assert_eq!(references.len(), 1);
    assert_eq!(references[0].file_path, method_path);
    restored.index(&file(method_path, "no PHP usage")).unwrap();
    assert!(
        restored
            .get_php_usage_references(&method)
            .unwrap()
            .is_empty()
    );
    restored.close().unwrap();
    php.close().unwrap();
}

#[test]
fn twig_indexer_stores_restores_and_clears_extension_usages() {
    let cache = tempfile::tempdir().unwrap();
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    let function_path = "/project/templates/function.html.twig";
    index
        .index(&file(function_path, "{{ form_start() }}"))
        .unwrap();
    index
        .index(&file(
            "/project/templates/filter.html.twig",
            "{{ value|trans }}",
        ))
        .unwrap();
    index
        .index(&file(
            "/project/templates/test.html.twig",
            "{% if value is positive %}{% endif %}",
        ))
        .unwrap();
    assert_eq!(
        index
            .get_extension_usages(ExtensionUsageKind::Function, "form_start")
            .unwrap()
            .len(),
        1
    );
    assert_eq!(
        index
            .get_extension_usages(ExtensionUsageKind::Filter, "trans")
            .unwrap()
            .len(),
        1
    );
    assert_eq!(
        index
            .get_extension_usages(ExtensionUsageKind::Test, "positive")
            .unwrap()
            .len(),
        1
    );
    index.close().unwrap();
    let restored = TwigIndexer::new(cache.path(), None).unwrap();
    assert_eq!(
        restored
            .get_extension_usages(ExtensionUsageKind::Function, "form_start")
            .unwrap()
            .len(),
        1
    );
    assert_eq!(
        restored
            .get_extension_usages(ExtensionUsageKind::Test, "positive")
            .unwrap()
            .len(),
        1
    );
    restored
        .index(&file(function_path, "no function usage"))
        .unwrap();
    assert!(
        restored
            .get_extension_usages(ExtensionUsageKind::Function, "form_start")
            .unwrap()
            .is_empty()
    );
    restored.close().unwrap();
}

#[test]
fn globals_in_yaml_cases() {
    let source = "twig:
  globals:
    clock: '@app.clock'
    site_name: 'Storefront'
    retries: 3
when@test:
  twig:
    globals:
      test_mode: true
";
    let root = swlsp_parser_yaml::parse(source).root();
    let globals = globals_in_yaml("/project/config/packages/twig.yaml", &root);
    assert_eq!(globals.len(), 4);
    let by = |name: &str| {
        globals
            .iter()
            .find(|global| global.name == name)
            .unwrap()
            .clone()
    };
    assert_eq!(by("clock").service_id, "app.clock");
    assert_eq!(by("clock").ty, "");
    assert_eq!(by("site_name").ty, "string");
    assert_eq!(by("retries").ty, "int");
    assert_eq!(by("test_mode").ty, "bool");
    assert_eq!(&source[by("clock").range], "clock");
}

#[test]
fn globals_in_php_extension_cases() {
    let source = r"<?php
namespace App\Twig;
use App\Clock;
use Twig\Extension\AbstractExtension;
final class StorefrontExtension extends AbstractExtension {
    public function getGlobals(): array {
        return [
            'clock' => new Clock(),
            'answer' => 42,
        ];
    }
}";
    let root = swlsp_parser_php::parse(source).root();
    let globals =
        globals_in_php_extension("/project/src/Twig/StorefrontExtension.php", &root, None);
    assert_eq!(globals.len(), 2);
    let by = |name: &str| {
        globals
            .iter()
            .find(|global| global.name == name)
            .unwrap()
            .clone()
    };
    assert_eq!(by("clock").ty, r"App\Clock");
    assert_eq!(by("answer").ty, "int");
    assert_eq!(&source[by("clock").range], "clock");
}

#[test]
fn twig_indexer_resolves_and_restores_typed_globals() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path();
    let cache = root.join(".cache");
    let php = php_index(&cache);
    let services = Arc::new(swlsp_symfony::ServiceIndex::new(&s(root), &cache, None).unwrap());
    services
        .index(&file(
            &s(&root.join("config/services.yaml")),
            "services:\n  app.clock:\n    class: App\\Clock\n",
        ))
        .unwrap();
    let first = TwigIndexer::new(&cache, None).unwrap();
    first.set_dependencies(Some(php.clone()), Some(services.clone()));
    let yaml_path = s(&root.join("config/packages/twig.yaml"));
    first
        .index(&file(
            &yaml_path,
            "twig:\n  globals:\n    clock: '@app.clock'\n    title: 'Store'\n",
        ))
        .unwrap();
    let php_path = s(&root.join("src/Twig/StorefrontExtension.php"));
    let php_source = r"<?php
namespace App\Twig;
use App\Feature;
use Twig\Extension\AbstractExtension;
final class StorefrontExtension extends AbstractExtension {
    public function getGlobals(): array {
        return ['feature' => new Feature()];
    }
}";
    index_php(&php, &php_path, php_source);
    first.index(&file(&php_path, php_source)).unwrap();
    let globals = first.get_all_globals().unwrap();
    let by = |name: &str| {
        globals
            .iter()
            .find(|global| global.name == name)
            .unwrap()
            .clone()
    };
    assert_eq!(by("clock").ty, r"App\Clock");
    assert_eq!(by("title").ty, "string");
    assert_eq!(by("feature").ty, r"App\Feature");
    first.close().unwrap();

    let restored = TwigIndexer::new(&cache, None).unwrap();
    restored.set_dependencies(Some(php.clone()), Some(services.clone()));
    assert_eq!(restored.get_all_globals().unwrap(), globals);
    restored
        .index(&file(&yaml_path, "framework:\n  secret: value\n"))
        .unwrap();
    for global in restored.get_all_globals().unwrap() {
        assert!(
            !global.name.eq_ignore_ascii_case("clock")
                && !global.name.eq_ignore_ascii_case("title")
        );
    }
    restored.close().unwrap();
    php.close().unwrap();
}

#[test]
fn render_block_references_support_positional_and_named_arguments() {
    let source = "<?php
$this->renderBlock('page.html.twig', 'content', []);
$this->renderBlockView(
    block: 'sidebar',
    parameters: [],
    view: 'named.html.twig',
);
$this->renderBlock($dynamic, 'ignored', []);
$this->renderBlock('page.html.twig', $dynamic, []);
";
    let references = render_block_references_in_php(&swlsp_parser_php::parse(source).root());
    assert_eq!(references.len(), 2);
    assert_eq!(references[0].template, "page.html.twig");
    assert_eq!(references[0].block, "content");
    assert_eq!(&source[references[0].block_range], "content");
    assert_eq!(references[1].template, "named.html.twig");
    assert_eq!(references[1].block, "sidebar");
}

#[test]
fn get_template_blocks_follows_extends_and_restores_ranges() {
    let cache = tempfile::tempdir().unwrap();
    let dir = tempfile::tempdir().unwrap();
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    let base = s(&dir.path().join("templates/base.html.twig"));
    let child = s(&dir.path().join("templates/child.html.twig"));
    index
        .index(&file(&base, "{## Main content. ##}\n{% block content %}{% endblock %}\n{% block sidebar %}{% endblock %}"))
        .unwrap();
    index
        .index(&file(&child, "{% extends 'base.html.twig' %}\n{% block content %}{% endblock %}\n{% block child %}{% endblock %}"))
        .unwrap();
    let blocks = index.get_template_blocks(&["child.html.twig"]).unwrap();
    assert_eq!(blocks.len(), 4);
    let mut names: Vec<&str> = blocks.iter().map(|block| block.name.as_str()).collect();
    names.sort_unstable();
    assert_eq!(names, ["child", "content", "content", "sidebar"]);
    assert!(blocks.iter().all(|block| !block.range.is_empty()));
    let documented = blocks
        .iter()
        .find(|b| b.file_path == base && b.name == "content")
        .unwrap();
    assert_eq!(documented.documentation, "Main content.");
    assert_eq!(documented.line, 2);
    index.close().unwrap();
    let restored = TwigIndexer::new(cache.path(), None).unwrap();
    assert_eq!(
        restored.get_template_blocks(&["child.html.twig"]).unwrap(),
        blocks
    );
    restored.close().unwrap();
}
