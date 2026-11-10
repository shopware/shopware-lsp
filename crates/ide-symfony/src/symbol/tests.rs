use super::*;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, WorkspaceSymbolProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::SymbolKind;
use swlsp_uriutil::file_uri;

fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn require_symbol<'a>(
    symbols: &'a [protocol::SymbolInformation],
    name: &str,
    container: &str,
) -> &'a protocol::SymbolInformation {
    symbols
        .iter()
        .find(|current| current.name == name && current.container_name.contains(container))
        .unwrap_or_else(|| panic!("symbol {name:?} in {container:?} not found in {symbols:#?}"))
}

#[test]
fn symfony_workspace_symbols_cover_plugin_navigation_domains() {
    let root_dir = tempfile::tempdir().unwrap();
    let cache_dir = tempfile::tempdir().unwrap();
    let root = root_dir.path();
    let cache = cache_dir.path();
    let services = Arc::new(swlsp_symfony::ServiceIndex::new(&p(root), cache, None).unwrap());
    let routes = Arc::new(swlsp_symfony::RouteIndexer::new(cache, None).unwrap());
    let commands = Arc::new(swlsp_console::Index::new(cache, None).unwrap());
    let twig = Arc::new(swlsp_twig::TwigIndexer::new(cache, None).unwrap());
    let components = Arc::new(swlsp_twigcomponent::Index::new(cache, None).unwrap());
    components.set_dependencies(None, Some(services.clone()), Some(twig.clone()));
    let translations = Arc::new(swlsp_translation::Index::new(cache, None).unwrap());
    let php = swlsp_php::PhpIndex::open(cache, None).unwrap();

    let service_path = root.join("config/services.xml");
    let route_path = root.join("config/routes.yaml");
    let command_path = root.join("src/Command/CreateUser.php");
    let template_path = root.join("templates/layout/base.html.twig");
    let extension_path = root.join("src/Twig/ShopExtension.php");
    let component_path = root.join("src/Twig/Components/Alert.php");
    let controller_path = root.join("src/Controller/CatalogController.php");
    let translation_path = root.join("translations/messages.en.yaml");
    let files = [
        (
            &service_path,
            r#"<container><services>
<service id="app.mailer" class="App\Mailer"/>
</services><parameters>
<parameter key="kernel.environment">test</parameter>
</parameters></container>"#,
        ),
        (
            &route_path,
            r"catalog.show:
  path: /catalog/{id}
  methods: [GET]
  controller: App\Controller\CatalogController::show
",
        ),
        (
            &command_path,
            r"<?php
namespace App\Command;
#[AsCommand(name: 'app:user:create', description: 'Creates a user')]
final class CreateUser {}
",
        ),
        (
            &template_path,
            "{% block page_content %}{% endblock %}
{% macro render_card(title) %}{{ title }}{% endmacro %}
",
        ),
        (
            &extension_path,
            r"<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
final class ShopExtension extends AbstractExtension {
    public function getFunctions(): array {
        return [new TwigFunction('shop_currency', [$this, 'currency'])];
    }
    public function currency(string $iso): string {}
}
",
        ),
        (
            &component_path,
            r"<?php
namespace App\Twig\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent(name: 'Alert')]
final class Alert {}
",
        ),
        (
            &controller_path,
            r"<?php
namespace App\Controller;
final class CatalogController {
    public function show(): void {}
    private function helper(): void {}
}
",
        ),
        (&translation_path, "checkout.complete: Order completed\n"),
    ];
    for (path, source) in files {
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(path, source).unwrap();
        let parsed = ParsedFile::new(p(path), source.as_bytes().to_vec());
        services.index(&parsed).unwrap();
        routes.index(&parsed).unwrap();
        commands.index(&parsed).unwrap();
        twig.index(&parsed).unwrap();
        components.index(&parsed).unwrap();
        translations.index(&parsed).unwrap();
        if path.extension().is_some_and(|extension| extension == "php") {
            php.index(&parsed).unwrap();
        }
    }

    let provider = SymfonyWorkspaceSymbolProvider::new(
        Some(services),
        Some(routes),
        Some(commands),
        Some(twig),
        Some(components),
        Some(translations),
        Some(php),
    );
    let cases: &[(&str, &str, &str, &Path, SymbolKind, bool)] = &[
        (
            "app.mailer",
            "app.mailer",
            "Symfony service",
            &service_path,
            SymbolKind::OBJECT,
            false,
        ),
        (
            "kernel.environment",
            "kernel.environment",
            "Symfony parameter",
            &service_path,
            SymbolKind::CONSTANT,
            false,
        ),
        (
            "catalog.show",
            "catalog.show",
            "Symfony route · GET /catalog/{id} · CatalogController:show",
            &route_path,
            SymbolKind::METHOD,
            false,
        ),
        (
            "CatalogController::show",
            "App\\Controller\\CatalogController::show",
            "Symfony controller · App\\Controller\\CatalogController",
            &controller_path,
            SymbolKind::METHOD,
            true,
        ),
        (
            "/catalog/42",
            "/catalog/42",
            "Symfony route URL",
            &route_path,
            SymbolKind::METHOD,
            false,
        ),
        (
            "https://shop.example/catalog/foo.bar?preview=1#details",
            "https://shop.example/catalog/foo.bar?preview=1#details",
            "Symfony route URL",
            &route_path,
            SymbolKind::METHOD,
            false,
        ),
        (
            "app:user:create",
            "app:user:create",
            "Symfony command",
            &command_path,
            SymbolKind::FUNCTION,
            true,
        ),
        (
            "layout/base.html.twig",
            "layout/base.html.twig",
            "Twig template",
            &template_path,
            SymbolKind::FILE,
            false,
        ),
        (
            "page_content",
            "page_content",
            "Twig block",
            &template_path,
            SymbolKind::FIELD,
            true,
        ),
        (
            "render_card",
            "render_card",
            "Twig macro",
            &template_path,
            SymbolKind::FUNCTION,
            true,
        ),
        (
            "shop_currency",
            "shop_currency",
            "Twig function",
            &extension_path,
            SymbolKind::FUNCTION,
            false,
        ),
        (
            "Alert",
            "Alert",
            "Twig component",
            &component_path,
            SymbolKind::CLASS,
            true,
        ),
        (
            "checkout.complete",
            "checkout.complete",
            "Translation",
            &translation_path,
            SymbolKind::STRING,
            true,
        ),
        (
            "messages",
            "messages",
            "Translation domain",
            &translation_path,
            SymbolKind::NAMESPACE,
            true,
        ),
    ];
    for (query, name, container, path, kind, exact_range) in cases {
        let symbols = provider
            .workspace_symbols(&Context::background(), query)
            .unwrap();
        let current = require_symbol(&symbols, name, container);
        assert_eq!(current.kind, *kind, "{query}");
        assert_eq!(current.location.uri, file_uri(&p(path)), "{query}");
        if *exact_range {
            assert_ne!(
                current.location.range.start, current.location.range.end,
                "{query}"
            );
        }
    }
}
