use std::path::Path;
use std::sync::Arc;

use swlsp_admin::{AdminComponentIndexer, VueComponent};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    Context, DocumentSymbolProvider, DocumentSymbolRequest, TextDocument, WorkspaceSymbolProvider,
};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::SymbolKind;
use swlsp_uriutil::file_uri;

use crate::{
    AdminDocumentSymbolProvider, AdminWorkspaceSymbolProvider, DalWorkspaceSymbolProvider,
    SymfonyWorkspaceSymbolProvider,
};

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

fn admin_index(root: &Path) -> Arc<AdminComponentIndexer> {
    Arc::new(AdminComponentIndexer::new(&p(&root.join("cache")), None).unwrap())
}

fn index(indexer: &dyn Indexer, path: &Path, source: &str) {
    indexer
        .index(&ParsedFile::new(p(path), source.as_bytes().to_vec()))
        .unwrap();
}

#[test]
fn admin_workspace_symbols_cover_runtime_registries() {
    let root = tempfile::tempdir().unwrap();
    let admin = admin_index(root.path());
    let file_path = root
        .path()
        .join("src/Administration/Resources/app/administration/src/main.ts");
    index(
        admin.as_ref(),
        &file_path,
        r#"
Shopware.Application.addServiceProvider('acl', () => createAcl());
Shopware.Store.register('session', {
    state: () => ({ currentUser: null }),
    getters: { locale() { return 'en-GB'; } },
    actions: { login() {} },
});
Shopware.Mixin.register('notification', {});
Shopware.Directive.register('tooltip', {});
Shopware.Filter.register('currency', (value) => value);
Shopware.Component.register('sw-owner', { directives: { hide: {} } });
Shopware.Service('cmsService').registerCmsElement({ name: 'hero' });
Shopware.Service('cmsService').registerCmsBlock({ name: 'hero-grid' });
Shopware.Module.register('sw-catalog', {
    routes: { index: { path: 'index', component: 'sw-catalog-list' } },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: { viewer: { privileges: ['product:read'] } },
});"#,
    );
    let provider = AdminWorkspaceSymbolProvider::new(Some(admin));
    let cases = [
        ("acl", "acl", "Administration service", SymbolKind::OBJECT),
        (
            "session",
            "session",
            "Administration store",
            SymbolKind::OBJECT,
        ),
        (
            "currentUser",
            "currentUser",
            "store · session",
            SymbolKind::FIELD,
        ),
        ("locale", "locale", "store · session", SymbolKind::PROPERTY),
        ("login", "login", "store · session", SymbolKind::METHOD),
        (
            "notification",
            "notification",
            "Administration mixin",
            SymbolKind::OBJECT,
        ),
        (
            "tooltip",
            "v-tooltip",
            "Vue directive",
            SymbolKind::FUNCTION,
        ),
        (
            "currency",
            "currency",
            "Administration filter",
            SymbolKind::FUNCTION,
        ),
        (
            "hide",
            "v-hide",
            "sw-owner · local Vue directive",
            SymbolKind::FUNCTION,
        ),
        ("hero", "hero", "Shopware CMS element", SymbolKind::OBJECT),
        (
            "hero-grid",
            "hero-grid",
            "Shopware CMS block",
            SymbolKind::OBJECT,
        ),
        (
            "sw-catalog",
            "sw-catalog",
            "Administration module",
            SymbolKind::MODULE,
        ),
        (
            "sw.catalog.index",
            "sw.catalog.index",
            "sw-catalog",
            SymbolKind::FUNCTION,
        ),
        (
            "product.viewer",
            "product.viewer",
            "ACL role",
            SymbolKind::ENUM_MEMBER,
        ),
        (
            "product:read",
            "product:read",
            "ACL permission",
            SymbolKind::CONSTANT,
        ),
    ];
    for (query, name, container, kind) in cases {
        let symbols = provider
            .workspace_symbols(&Context::background(), query)
            .unwrap();
        let current = require_symbol(&symbols, name, container);
        assert_eq!(current.kind, kind, "{query}");
        assert_eq!(current.location.uri, file_uri(&p(&file_path)), "{query}");
    }
}

#[test]
fn admin_workspace_symbols_expose_component_markup_contracts() {
    let root = tempfile::tempdir().unwrap();
    let admin = admin_index(root.path());
    let component_dir = root
        .path()
        .join("src/Administration/Resources/app/administration/src/app/component/sw-card");
    std::fs::create_dir_all(&component_dir).unwrap();
    let template_path = component_dir.join("sw-card.html.twig");
    std::fs::write(
        &template_path,
        "\n<slot name=\"actions\" :item=\"card\"></slot>\n",
    )
    .unwrap();
    let definition_path = component_dir.join("index.js");
    index(
        admin.as_ref(),
        &definition_path,
        r#"
import template from './sw-card.html.twig';

Shopware.Component.register('sw-card', {
    template,
    props: {
        helpText: { type: String },
        modelValue: { type: String },
    },
    emits: ['save', 'update:modelValue'],
});"#,
    );
    let provider = AdminWorkspaceSymbolProvider::new(Some(admin));
    let definition_uri = file_uri(&p(&definition_path));
    let template_uri = file_uri(&p(&template_path));
    let cases = [
        (
            "help-text",
            "helpText",
            "sw-card · component prop",
            SymbolKind::PROPERTY,
            &definition_uri,
        ),
        (
            "@save",
            "save",
            "sw-card · component event",
            SymbolKind::EVENT,
            &definition_uri,
        ),
        (
            "v-model",
            "v-model",
            "sw-card · component model",
            SymbolKind::PROPERTY,
            &definition_uri,
        ),
        (
            "#actions",
            "actions",
            "sw-card · component slot",
            SymbolKind::PROPERTY,
            &template_uri,
        ),
    ];
    for (query, name, container, kind, uri) in cases {
        let symbols = provider
            .workspace_symbols(&Context::background(), query)
            .unwrap();
        let current = require_symbol(&symbols, name, container);
        assert_eq!(current.kind, kind, "{query}");
        assert_eq!(&current.location.uri, uri, "{query}");
    }
}

#[test]
fn dal_workspace_symbols_expose_technical_entity_and_field_names() {
    let cache = tempfile::tempdir().unwrap();
    let dal = Arc::new(swlsp_shopware_dal::Index::new(cache.path(), None).unwrap());
    dal.index(&ParsedFile::new(
        "/project/src/Core/Content/Product/ProductDefinition.php",
        br#"<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new StringField('product_number', 'productNumber'),
        ]);
    }
}"#
        .to_vec(),
    ))
    .unwrap();
    let provider = DalWorkspaceSymbolProvider::new(Some(dal));
    let entity = provider
        .workspace_symbols(&Context::background(), "productdef")
        .unwrap();
    assert_eq!(entity.len(), 1);
    assert_eq!(entity[0].name, "product");
    assert_eq!(entity[0].kind, SymbolKind::CLASS);
    assert!(entity[0].container_name.contains("ProductDefinition"));

    let fields = provider
        .workspace_symbols(&Context::background(), "productNumber")
        .unwrap();
    assert_eq!(fields.len(), 1);
    assert_eq!(fields[0].name, "productNumber");
    assert_eq!(fields[0].kind, SymbolKind::FIELD);
    assert!(fields[0].container_name.contains("product"));
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

fn admin_document_symbols_for(
    provider: &AdminDocumentSymbolProvider,
    path: &Path,
    source: &str,
) -> Vec<protocol::DocumentSymbol> {
    let document = Arc::new(TextDocument::new(file_uri(&p(path)), source, 1));
    let mut params = protocol::DocumentSymbolParams::default();
    params.text_document.uri = document.uri.clone();
    provider
        .get_document_symbols(
            &Context::background(),
            &DocumentSymbolRequest { params, document },
        )
        .unwrap()
}

fn require_document_symbol<'a>(
    symbols: &'a [protocol::DocumentSymbol],
    name: &str,
) -> &'a protocol::DocumentSymbol {
    symbols
        .iter()
        .find(|symbol| symbol.name == name)
        .unwrap_or_else(|| panic!("document symbol {name:?} not found in {symbols:#?}"))
}

fn range_text(source: &str, range: protocol::Range) -> String {
    let document = TextDocument::new("file:///range.ts", source, 1);
    let lines = document.line_index();
    let start = lines.offset_utf16(range.start.line, range.start.character) as usize;
    let end = lines.offset_utf16(range.end.line, range.end.character) as usize;
    assert!(start <= end && end <= source.len());
    source[start..end].to_owned()
}

#[test]
fn admin_document_symbols_use_live_component_definition() {
    let root = tempfile::tempdir().unwrap();
    let path = root
        .path()
        .join("src/Administration/Resources/app/administration/src/app/component/sw-card/index.ts");
    let source = "
import LocalCard from './local-card';

/** @deprecated Use mt-card instead. */
Component.register('sw-card', {
    components: { LocalCard },
    directives: { tooltip: {} },
    inject: ['repositoryFactory'],
    props: {
        /** @deprecated Use heading instead. */
        title: { type: String, required: true },
        count: Number,
    },
    emits: ['save'],
    data() { return { loading: false }; },
    computed: { headline() { return this.title; } },
\tmethods: {
\t\t/** @deprecated Use save instead. */
\t\tsubmit(value) { this.$emit('save', value); },
\t},
});";
    let symbols =
        admin_document_symbols_for(&AdminDocumentSymbolProvider::new(None), &path, source);
    assert_eq!(symbols.len(), 1);
    let component = &symbols[0];
    assert_eq!(component.name, "sw-card");
    assert_eq!(component.kind, SymbolKind::CLASS);
    assert!(component.deprecated);
    assert_eq!(range_text(source, component.selection_range), "sw-card");

    let expected = [
        ("local-card", SymbolKind::CLASS),
        ("v-tooltip", SymbolKind::FUNCTION),
        ("repositoryFactory", SymbolKind::FIELD),
        ("title", SymbolKind::PROPERTY),
        ("count", SymbolKind::PROPERTY),
        ("save", SymbolKind::EVENT),
        ("loading", SymbolKind::FIELD),
        ("headline", SymbolKind::PROPERTY),
        ("submit", SymbolKind::METHOD),
    ];
    for (name, kind) in expected {
        let current = require_document_symbol(&component.children, name);
        assert_eq!(current.kind, kind, "{name}");
        assert!(
            !range_text(source, current.selection_range).is_empty(),
            "{name}"
        );
    }
    let title = require_document_symbol(&component.children, "title");
    assert!(title.detail.contains("required"));
    assert!(title.deprecated);
    assert!(title.detail.contains("deprecated"));
    let submit = require_document_symbol(&component.children, "submit");
    assert!(range_text(source, submit.range).contains("submit(value)"));
    assert!(submit.deprecated);
    assert!(submit.detail.contains("deprecated"));
}

#[test]
fn admin_document_symbols_nest_live_slots_under_twig_blocks() {
    let root = tempfile::tempdir().unwrap();
    let admin = admin_index(root.path());
    let path = root.path().join(
        "src/Administration/Resources/app/administration/src/app/component/sw-card/template.html.twig",
    );
    let definition = p(&path.parent().unwrap().join("index.js"));
    let mut component = VueComponent::default();
    component.name = "sw-card".to_owned();
    component.file_path = definition.clone();
    component.definition_path = definition;
    component.template_path = p(&path);
    admin.save_component(&component).unwrap();
    let source = r#"{# @deprecated Use sw_card_body instead. #}
{% block sw_card_content %}
    <slot
        name="header"
        :title="title"
        v-bind="{ actions, active: enabled }"
    ></slot>
{% endblock %}
<slot></slot>"#;
    let symbols = admin_document_symbols_for(
        &AdminDocumentSymbolProvider::new(Some(admin)),
        &path,
        source,
    );
    assert_eq!(symbols.len(), 1);
    let component = &symbols[0];
    assert_eq!(component.name, "sw-card");
    let block = require_document_symbol(&component.children, "sw_card_content");
    assert_eq!(block.kind, SymbolKind::METHOD);
    assert!(block.deprecated);
    assert!(block.detail.contains("deprecated"));
    let header = require_document_symbol(&block.children, "header");
    assert_eq!(header.kind, SymbolKind::PROPERTY);
    for field in ["title", "actions", "active"] {
        assert_eq!(
            require_document_symbol(&header.children, field).kind,
            SymbolKind::FIELD
        );
    }
    assert_eq!(
        require_document_symbol(&component.children, "default").kind,
        SymbolKind::PROPERTY
    );
}
