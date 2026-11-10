use super::*;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Context;
use swlsp_php::PhpIndex;
use swlsp_symfony::{RouteIndexer, ServiceIndex};
use swlsp_twig::TwigIndexer;
use swlsp_twigcomponent::Index as ComponentIndex;
use swlsp_uriutil::file_uri;

fn extension_entry<'a>(
    entries: &'a [TwigExtensionCatalogEntry],
    kind: &str,
    name: &str,
) -> &'a TwigExtensionCatalogEntry {
    entries
        .iter()
        .find(|entry| entry.ty == kind && entry.name == name)
        .unwrap_or_else(|| panic!("Twig {kind} {name:?} not found in {entries:#?}"))
}

struct UsageIndexes {
    twig: Arc<TwigIndexer>,
    routes: Arc<RouteIndexer>,
    services: Arc<ServiceIndex>,
    components: Arc<ComponentIndex>,
}

impl UsageIndexes {
    fn open(root: &Path, cache: &Path, php: &PhpIndex) -> Self {
        let twig = open_twig(cache);
        let routes = open_routes(cache);
        let services = open_services(root, cache, php);
        let components = open_components(cache);
        components.set_dependencies(
            Some(php.clone()),
            Some(services.clone()),
            Some(twig.clone()),
        );
        Self {
            twig,
            routes,
            services,
            components,
        }
    }

    fn provider(&self, root: &Path, php: &PhpIndex) -> TwigTemplateUsageCatalogProvider {
        TwigTemplateUsageCatalogProvider::new(
            &path_string(root),
            Some(self.twig.clone()),
            Some(php.clone()),
            Some(self.routes.clone()),
            Some(self.services.clone()),
            Some(self.components.clone()),
        )
    }

    fn close(&self) {
        close(self.twig.as_ref());
        close(self.routes.as_ref());
        close(self.services.as_ref());
        close(self.components.as_ref());
    }
}

fn assert_usage_file(usages: &[TwigTemplateReferenceUsage], path: &str) {
    assert_eq!(usages.len(), 1, "{usages:#?}");
    assert_eq!(usages[0].file_uri, uri(path));
    assert_eq!(usages[0].line, 1);
}

fn variable_entry<'a>(
    entries: &'a [TwigTemplateVariableEntry],
    name: &str,
) -> &'a TwigTemplateVariableEntry {
    entries
        .iter()
        .find(|entry| entry.name == name)
        .unwrap_or_else(|| panic!("Twig variable {name:?} not found in {entries:#?}"))
}

fn assert_property(properties: &[TwigTemplateVariableProperty], name: &str, kind: &str) {
    let property = properties
        .iter()
        .find(|property| property.name == name)
        .unwrap_or_else(|| panic!("Twig property {name:?} not found in {properties:#?}"));
    assert_eq!(property.kind, kind);
}

fn assert_source(sources: &[TwigTemplateVariableSource], kind: &str) {
    assert!(
        sources.iter().any(|source| source.kind == kind),
        "Twig variable source {kind:?} not found in {sources:#?}"
    );
}

pub(crate) fn ctx() -> Context {
    Context::background()
}

pub(crate) fn path_string(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

/// Writes `source` below `root` and returns the absolute path.
pub(crate) fn write_fixture(root: &Path, relative: &str, source: &str) -> String {
    let path: PathBuf = root.join(relative);
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(&path, source).unwrap();
    path_string(&path)
}

pub(crate) fn parsed(path: &str) -> ParsedFile {
    ParsedFile::new(path, std::fs::read(path).unwrap())
}

pub(crate) fn uri(path: &str) -> String {
    file_uri(path)
}

pub(crate) fn open_php(cache: &Path) -> PhpIndex {
    PhpIndex::open(cache, None).unwrap()
}

pub(crate) fn open_twig(cache: &Path) -> Arc<TwigIndexer> {
    Arc::new(TwigIndexer::new(cache, None).unwrap())
}

pub(crate) fn open_routes(cache: &Path) -> Arc<RouteIndexer> {
    Arc::new(RouteIndexer::new(cache, None).unwrap())
}

pub(crate) fn open_services(root: &Path, cache: &Path, php: &PhpIndex) -> Arc<ServiceIndex> {
    let services = Arc::new(ServiceIndex::new(&path_string(root), cache, None).unwrap());
    services.set_php_index(Some(php.clone()));
    services
}

pub(crate) fn open_components(cache: &Path) -> Arc<ComponentIndex> {
    Arc::new(ComponentIndex::new(cache, None).unwrap())
}

pub(crate) fn close(indexer: &dyn Indexer) {
    indexer.close().unwrap();
}

/// Parses an expected wire response for comparison with a command result.
pub(crate) fn json_value(text: &str) -> serde_json::Value {
    serde_json::from_str(text).unwrap()
}

#[test]
fn twig_component_catalog_aggregates_metadata_syntax_and_locations() {
    let root_dir = tempfile::tempdir().unwrap();
    let cache_dir = tempfile::tempdir().unwrap();
    let (root, cache) = (root_dir.path(), cache_dir.path());
    let php = open_php(cache);
    let twig = open_twig(cache);
    let components = open_components(cache);
    components.set_dependencies(Some(php.clone()), None, Some(twig.clone()));

    let class_path = write_fixture(
        root,
        "src/Twig/Component/Search.php",
        r"<?php
namespace App\Twig\Component;

use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveProp;

#[AsLiveComponent(name: 'Search', template: 'components/Search.html.twig')]
final class Search
{
    #[LiveProp(writable: true)]
    public string $query = '';

    public function getResults(): array
    {
        return [];
    }
}
",
    );
    let class_file = parsed(&class_path);
    php.index(&class_file).unwrap();
    components.index(&class_file).unwrap();
    let template_path = write_fixture(
        root,
        "templates/components/Search.html.twig",
        "{% props title = 'Products' %}\n{% block footer %}{% endblock %}\n{{ title }} {{ query }}\n",
    );
    let template_file = parsed(&template_path);
    twig.index(&template_file).unwrap();
    components.index(&template_file).unwrap();
    let usage_path = write_fixture(
        root,
        "templates/page.html.twig",
        "{{ component('Search') }}",
    );
    components.index(&parsed(&usage_path)).unwrap();

    let request = TwigComponentCatalogRequest {
        search: "sea".to_owned(),
    };
    let entries = TwigComponentCatalogProvider::new(Some(components.clone()))
        .catalog(&ctx(), &request)
        .unwrap();
    assert_eq!(entries.len(), 1);
    let entry = &entries[0];
    assert_eq!(entry.name, "Search");
    assert_eq!(entry.syntax.html_tag, "<twig:Search></twig:Search>");
    assert_eq!(entry.syntax.function, "{{ component('Search') }}");
    assert_eq!(
        entry.syntax.composition,
        "{% component 'Search' %}{% block footer %}{% endblock %}{% endcomponent %}"
    );
    assert_eq!(entry.declarations.len(), 2);
    let declaration = entry
        .declarations
        .iter()
        .find(|declaration| declaration.source == "attribute")
        .unwrap();
    assert_eq!(declaration.class, r"App\Twig\Component\Search");
    assert_eq!(declaration.template, "components/Search.html.twig");
    assert!(declaration.live);
    assert_eq!(declaration.file_uri, uri(&class_path));
    assert!(declaration.source_line > 0);
    assert_eq!(
        entry.templates,
        [TwigComponentTemplateEntry {
            template: "components/Search.html.twig".to_owned(),
            file_uri: uri(&template_path),
        }]
    );
    let prop = |props: &[TwigComponentPropEntry], name: &str| {
        props
            .iter()
            .find(|prop| prop.name == name)
            .cloned()
            .unwrap_or_else(|| panic!("prop {name:?} not found in {props:#?}"))
    };
    let query = prop(&entry.props, "query");
    assert_eq!(query.ty, "string");
    assert!(query.live);
    assert!(query.writable);
    assert_eq!(prop(&entry.props, "title").default_value, "'Products'");
    assert_eq!(prop(&entry.computed, "results").ty, "array");
    assert_eq!(entry.blocks.len(), 1);
    assert_eq!(entry.blocks[0].name, "footer");
    assert_eq!(entry.blocks[0].print, "{{ block('footer') }}");
    assert_eq!(entry.blocks[0].file_uri, uri(&template_path));
    assert_eq!(entry.usages.len(), 1);
    assert_eq!(entry.usages[0].syntax, "component()");
    assert_eq!(entry.usages[0].file_uri, uri(&usage_path));

    close(components.as_ref());
    close(twig.as_ref());
    let restored_twig = open_twig(cache);
    let restored_components = open_components(cache);
    restored_components.set_dependencies(Some(php.clone()), None, Some(restored_twig.clone()));
    let restored = TwigComponentCatalogProvider::new(Some(restored_components.clone()))
        .catalog(&ctx(), &request)
        .unwrap();
    assert_eq!(entries, restored);
    close(restored_components.as_ref());
    close(restored_twig.as_ref());
    close(&php);
}

#[test]
fn twig_extension_catalog_exposes_typed_callables_tags_and_filters() {
    let root_dir = tempfile::tempdir().unwrap();
    let cache_dir = tempfile::tempdir().unwrap();
    let (root, cache) = (root_dir.path(), cache_dir.path());
    let path = write_fixture(
        root,
        "src/Twig/AppExtension.php",
        r"<?php
namespace App\Twig;

use Twig\Extension\AbstractExtension;
use Twig\TokenParser\TokenParserInterface;
use Twig\TwigFilter;
use Twig\TwigFunction;
use Twig\TwigTest;

final class AppExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction('product_url', [$this, 'productUrl']),
        ];
    }

    public function getFilters(): array
    {
        return [
            new TwigFilter('money', $this->money(...)),
        ];
    }

    public function getTests(): array
    {
        return [
            new TwigTest('sellable', [$this, 'isSellable']),
        ];
    }

    public function productUrl(
        string $id,
        bool $absolute = false,
        string $_internal = '',
    ): string {
        return '';
    }

    public function money(float $value): string
    {
        return '';
    }

    public function isSellable(object $product): bool
    {
        return true;
    }
}

/** @deprecated Use ModernTokenParser instead. */
final class LegacyTokenParser implements TokenParserInterface
{
    public function getTag(): string
    {
        return 'catalog';
    }
}
",
    );
    let php = open_php(cache);
    let twig = open_twig(cache);
    let file = parsed(&path);
    php.index(&file).unwrap();
    twig.index(&file).unwrap();

    let provider = TwigExtensionCatalogProvider::new(Some(twig.clone()), Some(php.clone()));
    let entries = provider
        .catalog(&ctx(), &TwigExtensionCatalogRequest::default())
        .unwrap();
    let product_url = extension_entry(&entries, "function", "product_url");
    assert_eq!(product_url.class_name, r"App\Twig\AppExtension");
    assert_eq!(product_url.method_name, "productUrl");
    assert_eq!(product_url.callable, "$this->productUrl");
    assert_eq!(product_url.usage, "product_url($id, $absolute, $_internal)");
    assert_eq!(
        product_url.parameters,
        [
            TwigExtensionCatalogParameter {
                name: "id".to_owned(),
                ty: "string".to_owned(),
                optional: false,
            },
            TwigExtensionCatalogParameter {
                name: "absolute".to_owned(),
                ty: "bool".to_owned(),
                optional: true,
            },
        ]
    );
    assert_eq!(product_url.file_uri, uri(&path));
    assert!(product_url.source_line > 0);

    let money = extension_entry(&entries, "filter", "money");
    assert_eq!(money.class_name, r"App\Twig\AppExtension");
    assert_eq!(money.method_name, "money");
    assert_eq!(
        money.parameters,
        [TwigExtensionCatalogParameter {
            name: "value".to_owned(),
            ty: "float".to_owned(),
            optional: false,
        }]
    );
    let sellable = extension_entry(&entries, "test", "sellable");
    assert_eq!(sellable.class_name, r"App\Twig\AppExtension");
    assert_eq!(sellable.method_name, "isSellable");
    let tag = extension_entry(&entries, "tag", "catalog");
    assert_eq!(tag.class_name, r"App\Twig\LegacyTokenParser");
    assert!(tag.deprecated);
    assert!(tag.deprecation.contains("ModernTokenParser"));

    let filtered = provider
        .catalog(
            &ctx(),
            &TwigExtensionCatalogRequest {
                search: "PRODUCT".to_owned(),
                include_filters: Some(false),
                include_functions: Some(true),
                include_tests: Some(false),
                include_tags: Some(false),
            },
        )
        .unwrap();
    assert_eq!(filtered, std::slice::from_ref(product_url));

    let commands = swlsp_lsp::CommandProvider::commands(&provider, &ctx());
    let value = commands[LIST_TWIG_EXTENSIONS_COMMAND](
        &ctx(),
        Some(&serde_json::json!({"search": "money", "includeTags": null})),
    )
    .unwrap();
    assert_eq!(
        value,
        json_value(&format!(
            r#"[{{"type":"filter","name":"money","className":"App\\Twig\\AppExtension","methodName":"money","callable":"$this->money","usage":"money($value)","parameters":[{{"name":"value","type":"float"}}],"fileUri":"{}","sourceLine":{}}}]"#,
            uri(&path),
            money.source_line
        ))
    );

    close(twig.as_ref());
    let restored = open_twig(cache);
    let restored_entries =
        TwigExtensionCatalogProvider::new(Some(restored.clone()), Some(php.clone()))
            .catalog(&ctx(), &TwigExtensionCatalogRequest::default())
            .unwrap();
    assert_eq!(entries, restored_entries);
    close(restored.as_ref());
    close(&php);
}

#[test]
fn twig_template_usage_catalog_correlates_every_usage_kind() {
    let root_dir = tempfile::tempdir().unwrap();
    let cache_dir = tempfile::tempdir().unwrap();
    let (root, cache) = (root_dir.path(), cache_dir.path());
    let php = open_php(cache);
    let indexes = UsageIndexes::open(root, cache, &php);

    let target_path = write_fixture(
        root,
        "templates/layout/base.html.twig",
        "{% block body %}{% endblock %}",
    );
    indexes.twig.index(&parsed(&target_path)).unwrap();
    for (relative, source) in [
        (
            "templates/page/include.html.twig",
            "{% include 'layout/base.html.twig' %}",
        ),
        (
            "templates/page/embed.html.twig",
            "{% embed 'layout/base.html.twig' %}{% endembed %}",
        ),
        (
            "templates/page/extends.html.twig",
            "{% extends 'layout/base.html.twig' %}",
        ),
        (
            "templates/page/import.html.twig",
            "{% import 'layout/base.html.twig' as layout %}",
        ),
        (
            "templates/page/use.html.twig",
            "{% use 'layout/base.html.twig' %}",
        ),
        (
            "templates/page/form.html.twig",
            "{% form_theme form 'layout/base.html.twig' %}",
        ),
    ] {
        let path = write_fixture(root, relative, source);
        indexes.twig.index(&parsed(&path)).unwrap();
    }
    let controller_path = write_fixture(
        root,
        "src/Controller/PageController.php",
        r"<?php
namespace App\Controller;

use Symfony\Component\Routing\Attribute\Route;

final class PageController
{
    #[Route('/page', name: 'app.page', methods: ['GET'])]
    public function index(): array
    {
        return $this->render('layout/base.html.twig');
    }
}
",
    );
    let controller_file = parsed(&controller_path);
    php.index(&controller_file).unwrap();
    indexes.twig.index(&controller_file).unwrap();
    indexes.routes.index(&controller_file).unwrap();
    let component_path = write_fixture(
        root,
        "src/Twig/LayoutComponent.php",
        r"<?php
namespace App\Twig;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;

#[AsTwigComponent(name: 'Layout', template: 'layout/base.html.twig')]
final class LayoutComponent {}
",
    );
    let component_file = parsed(&component_path);
    php.index(&component_file).unwrap();
    indexes.components.index(&component_file).unwrap();
    let component_usage_path = write_fixture(
        root,
        "templates/page/component.html.twig",
        "{{ component('Layout') }}",
    );
    indexes.twig.index(&parsed(&component_usage_path)).unwrap();
    indexes
        .components
        .index(&parsed(&component_usage_path))
        .unwrap();

    let provider = indexes.provider(root, &php);
    let request = TwigTemplateUsageCatalogRequest {
        template: "templates/layout/base.html.twig".to_owned(),
        file_glob: "templates/**/base.html.twig".to_owned(),
    };
    let entries = provider.catalog(&ctx(), &request).unwrap();
    assert_eq!(entries.len(), 1);
    let entry = &entries[0];
    assert_eq!(entry.template, "layout/base.html.twig");
    assert_eq!(
        entry.files,
        [TwigTemplateSourceLocation {
            file_uri: uri(&target_path),
            line: 1,
        }]
    );
    assert_eq!(entry.controllers.len(), 1);
    assert_eq!(
        entry.controllers[0].controller,
        r"App\Controller\PageController::index"
    );
    assert_eq!(entry.controllers[0].file_uri, uri(&controller_path));
    assert_eq!(entry.controllers[0].line, 9);
    assert_eq!(
        entry.controllers[0].routes,
        [TwigTemplateRouteEntry {
            name: "app.page".to_owned(),
            path: "/page".to_owned(),
            methods: vec!["GET".to_owned()],
        }]
    );
    let page = |name: &str| path_string(&root.join("templates/page").join(name));
    assert_usage_file(&entry.includes, &page("include.html.twig"));
    assert_usage_file(&entry.embeds, &page("embed.html.twig"));
    assert_usage_file(&entry.extends, &page("extends.html.twig"));
    assert_usage_file(&entry.imports, &page("import.html.twig"));
    assert_usage_file(&entry.uses, &page("use.html.twig"));
    assert_usage_file(&entry.form_themes, &page("form.html.twig"));
    assert_eq!(entry.components.len(), 1);
    assert_eq!(entry.components[0].component, "Layout");
    assert_eq!(entry.components[0].syntax, "component()");
    assert_eq!(entry.components[0].file_uri, uri(&component_usage_path));

    let partial = provider
        .catalog(
            &ctx(),
            &TwigTemplateUsageCatalogRequest {
                template: "BASE.HTML".to_owned(),
                ..TwigTemplateUsageCatalogRequest::default()
            },
        )
        .unwrap();
    assert_eq!(partial, std::slice::from_ref(entry));
    let error = provider
        .catalog(&ctx(), &TwigTemplateUsageCatalogRequest::default())
        .unwrap_err();
    assert!(error.to_string().contains("at least one"), "{error}");
    let commands = swlsp_lsp::CommandProvider::commands(&provider, &ctx());
    let error = commands[LIST_TWIG_TEMPLATE_USAGES_COMMAND](&ctx(), Some(&serde_json::Value::Null))
        .unwrap_err();
    assert_eq!(
        error.to_string(),
        "at least one of template or fileGlob is required"
    );
    let error = commands[LIST_TWIG_TEMPLATE_USAGES_COMMAND](&ctx(), Some(&serde_json::json!([])))
        .unwrap_err();
    assert_eq!(
        error.to_string(),
        "invalid twig template usage request: expected a JSON object, found array"
    );

    indexes.close();
    let restored = UsageIndexes::open(root, cache, &php);
    let restored_entries = restored
        .provider(root, &php)
        .catalog(&ctx(), &request)
        .unwrap();
    assert_eq!(entries, restored_entries);
    restored.close();
    close(&php);
}

#[test]
fn twig_template_variable_catalog_merges_types_sources_and_properties() {
    let root_dir = tempfile::tempdir().unwrap();
    let cache_dir = tempfile::tempdir().unwrap();
    let (root, cache) = (root_dir.path(), cache_dir.path());
    let php = open_php(cache);
    let twig = open_twig(cache);
    twig.set_dependencies(Some(php.clone()), None);

    let php_path = write_fixture(
        root,
        "src/Controller/ProductController.php",
        r"<?php
namespace App;

final class Product
{
    public static string $ignoredStatic = '';
    public string $name = '';

    public function getPrice(): float { return 0.0; }
    public function refresh(): void {}
    public function setName(string $name): void {}
    public function __call(string $name, array $arguments): mixed {}
}

final class ProductController
{
    public function show(Product $product): array
    {
        return $this->render('catalog/show.html.twig', [
            'product' => $product,
            'count' => 1,
        ]);
    }
}
",
    );
    php.index(&parsed(&php_path)).unwrap();
    let template_path = write_fixture(
        root,
        "templates/catalog/show.html.twig",
        "{# @var annotated \\App\\Product #}\n{{ product.name }}\n{{ annotated.price }}\n{{ missing }}\n",
    );
    twig.index(&parsed(&template_path)).unwrap();
    let global_path = write_fixture(
        root,
        "config/packages/twig.yaml",
        "twig:\n  globals:\n    shop_name: 'Demo'\n",
    );
    twig.index(&parsed(&global_path)).unwrap();

    let provider = TwigTemplateVariableCatalogProvider::new(
        &path_string(root),
        Some(twig.clone()),
        Some(php.clone()),
        None,
    );
    let request = TwigTemplateVariableCatalogRequest {
        template: "templates/catalog/show.html.twig".to_owned(),
        file_glob: "templates/**/show.html.twig".to_owned(),
    };
    let entries = provider.catalog(&ctx(), &request).unwrap();
    assert_eq!(entries.len(), 1);
    let entry = &entries[0];
    assert_eq!(entry.template, "catalog/show.html.twig");
    assert_eq!(
        entry.files,
        [TwigTemplateSourceLocation {
            file_uri: uri(&template_path),
            line: 1,
        }]
    );

    let product = variable_entry(&entry.variables, "product");
    assert_eq!(product.ty, r"App\Product");
    assert!(product.types.contains(&r"App\Product".to_owned()));
    assert_property(&product.properties, "name", "property");
    assert_property(&product.properties, "price", "getter");
    assert_property(&product.properties, "refresh", "method");
    let names: Vec<&str> = product
        .properties
        .iter()
        .map(|property| property.name.as_str())
        .collect();
    assert!(!names.contains(&"setName"));
    assert!(!names.contains(&"__call"));
    assert!(!names.contains(&"ignoredStatic"));
    assert_source(&product.sources, "controller");
    assert_source(&product.sources, "templateInput");

    let annotated = variable_entry(&entry.variables, "annotated");
    assert_eq!(annotated.ty, r"App\Product");
    assert_source(&annotated.sources, "annotation");
    assert_property(&annotated.properties, "price", "getter");

    let missing = variable_entry(&entry.variables, "missing");
    assert_eq!(missing.ty, "unknown");
    assert_source(&missing.sources, "templateInput");

    let count = variable_entry(&entry.variables, "count");
    assert_eq!(count.ty, "1");
    assert_source(&count.sources, "controller");

    let global = variable_entry(&entry.variables, "shop_name");
    assert_eq!(global.ty, "string");
    assert_source(&global.sources, "global");

    let error = provider
        .catalog(&ctx(), &TwigTemplateVariableCatalogRequest::default())
        .unwrap_err();
    assert!(error.to_string().contains("at least one"), "{error}");

    close(twig.as_ref());
    let restored = open_twig(cache);
    restored.set_dependencies(Some(php.clone()), None);
    let restored_entries = TwigTemplateVariableCatalogProvider::new(
        &path_string(root),
        Some(restored.clone()),
        Some(php.clone()),
        None,
    )
    .catalog(&ctx(), &request)
    .unwrap();
    assert_eq!(entries, restored_entries);
    close(restored.as_ref());
    close(&php);
}
