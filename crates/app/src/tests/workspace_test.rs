//! Port of Go `internal/app/workspace_test.go`.

use std::collections::HashMap;
use std::path::Path;

use swlsp_indexer::CancelToken;
use swlsp_lsp::{Context, Server};

use crate::Workspace;

/// Go `NewWorkspace(ctx, root, lsp.NewServer(nil, root, "test"))` with an
/// isolated cache base.
pub(super) fn open_workspace(root: &Path, cache: &Path) -> Workspace {
    let root = root.to_string_lossy();
    let server = Server::new(None, &root, "test");
    Workspace::new_in(&Context::background(), &root, &server, cache).expect("create workspace")
}

pub(super) fn index_all(workspace: &Workspace) {
    workspace
        .file_scanner()
        .index_all(&CancelToken::new())
        .expect("index workspace");
}

pub(super) fn write_files(root: &Path, files: &[(&str, &str)]) {
    for (relative, content) in files {
        let path = root.join(relative);
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(path, content).unwrap();
    }
}

fn path_string(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

#[test]
fn workspace_construction_and_close() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = path_string(project.path());

    let server = Server::new(None, "", "test");
    let workspace =
        Workspace::new_in(&Context::background(), &root, &server, cache.path()).unwrap();
    assert_eq!(root, workspace.root());
    let _scanner = workspace.file_scanner();

    let cache_dir = crate::project_cache_folder_in(cache.path(), &root).unwrap();
    assert!(cache_dir.join("indexes.db").exists());
    assert!(cache_dir.join("file_scanner.db").exists());

    workspace.close_workspace().unwrap();
    workspace.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_translation_resources() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[(
            "translations/messages.en.yaml",
            "checkout.complete: Order completed\n",
        )],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let messages = workspace
        .services()
        .translations
        .messages_for("messages", "checkout.complete")
        .unwrap();
    assert_eq!(1, messages.len());
    assert_eq!("Order completed", messages[0].text);
    assert_eq!(
        path_string(&root.join("translations").join("messages.en.yaml")),
        messages[0].file
    );
    workspace.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_twig_controller_variables() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            (
                "src/Controller/ProductController.php",
                r#"<?php
namespace App\Controller;
class Product {}
class ProductController {
    public function show(Product $product) {
        return $this->render('product/show.html.twig', [
            'product' => $product,
        ]);
    }
}"#,
            ),
            ("templates/product/show.html.twig", "{{ product }}"),
        ],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let variables = workspace
        .services()
        .php
        .twig_template_variables(&["product/show.html.twig"])
        .unwrap();
    assert_eq!(1, variables.len());
    assert_eq!("product", variables[0].name);
    assert_eq!("App\\Controller\\Product", variables[0].ty);
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = reopened
        .services()
        .php
        .twig_template_variables(&["product/show.html.twig"])
        .unwrap();
    assert_eq!(1, restored.len());
    assert_eq!(variables[0], restored[0]);
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_symfony_events() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            (
                "vendor/EventSubscriberInterface.php",
                r#"<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}"#,
            ),
            (
                "src/Events.php",
                r#"<?php
namespace App;
class DomainEvent {}
class Subscriber implements \Symfony\Component\EventDispatcher\EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return [DomainEvent::class => 'onEvent'];
    }
    public function onEvent(DomainEvent $event): void {}
}"#,
            ),
        ],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let indexed = workspace
        .services()
        .events
        .event("App\\DomainEvent")
        .unwrap()
        .expect("event indexed");
    assert_eq!(1, indexed.listeners().len());
    assert_eq!("App\\Subscriber", indexed.listeners()[0].class);
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = reopened
        .services()
        .events
        .event("App\\DomainEvent")
        .unwrap()
        .expect("event restored");
    assert_eq!(indexed, restored);
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_serializer_targets() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            ("src/Model.php", "<?php\nnamespace App;\nclass Model {}\n"),
            (
                "src/Handler.php",
                r#"<?php
namespace App;
function load($serializer): void {
    $serializer->deserialize($data, Model::class, 'json');
    $serializer->deserialize($data, 'App\Model[]', 'json');
}
"#,
            ),
        ],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let usages = workspace
        .services()
        .serializer
        .usages("App\\Model")
        .unwrap();
    assert_eq!(2, usages.len());
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = reopened.services().serializer.usages("App\\Model").unwrap();
    assert_eq!(usages, restored);
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_public_assets() {
    use swlsp_asset::ReferenceKind;

    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    let bundle_asset_path = root.join("public/bundles/administration/administration/app.js");
    write_files(
        root,
        &[
            ("public/build/app.css", "body{}"),
            (
                "public/bundles/administration/administration/app.js",
                "console.log('app')",
            ),
            (
                "src/Administration/Resources/config/routes.xml",
                "<routes/>",
            ),
            (
                "public/build/entrypoints.json",
                r#"{"entrypoints":{"app":{"css":["/build/app.css"]}}}"#,
            ),
            (
                "templates/page.html.twig",
                "{{ asset('build/app.css') }}\n{{ asset('administration/app.js', '@Administration') }}",
            ),
        ],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let assets = &workspace.services().assets;
    let names = assets.names().unwrap();
    assert!(names.contains(&"build/app.css".to_owned()), "{names:?}");
    let entries = assets.entry_names().unwrap();
    assert_eq!(vec!["app".to_owned()], entries);
    let usages = assets
        .usages("build/app.css", ReferenceKind::Asset)
        .unwrap();
    assert_eq!(1, usages.len());
    let package_names = assets.package_names().unwrap();
    assert!(
        package_names.contains(&"@Administration".to_owned()),
        "{package_names:?}"
    );
    let package_usages = assets
        .usages("@Administration", ReferenceKind::AssetPackage)
        .unwrap();
    assert_eq!(1, package_usages.len());
    let resolved_bundle_assets = assets
        .find_assets_for_package("administration/app.js", "@Administration")
        .unwrap();
    assert_eq!(1, resolved_bundle_assets.len());
    assert_eq!(
        path_string(&bundle_asset_path),
        resolved_bundle_assets[0].target
    );
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let assets = &reopened.services().assets;
    assert_eq!(names, assets.names().unwrap());
    assert_eq!(
        usages,
        assets
            .usages("build/app.css", ReferenceKind::Asset)
            .unwrap()
    );
    assert_eq!(package_names, assets.package_names().unwrap());
    assert_eq!(
        package_usages,
        assets
            .usages("@Administration", ReferenceKind::AssetPackage)
            .unwrap()
    );
    assert_eq!(
        resolved_bundle_assets,
        assets
            .find_assets_for_package("administration/app.js", "@Administration")
            .unwrap()
    );
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_twig_components() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            (
                "src/Twig/Components/Alert.php",
                r#"<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveProp;
#[AsLiveComponent]
final class Alert {
    #[LiveProp(writable: true)]
    public string $title = '';
}"#,
            ),
            (
                "templates/components/Alert.html.twig",
                "\n{% props variant = 'info' %}\n{% block content %}{% endblock %}\n<div>{{ title }}</div>\n",
            ),
            (
                "templates/page.html.twig",
                "\n{{ component('Alert') }}\n<twig:Alert><twig:block name=\"content\">Hi</twig:block></twig:Alert>\n",
            ),
        ],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let components = &workspace.services().twig_components;
    let names = components.names().unwrap();
    assert!(names.contains(&"Alert".to_owned()), "{names:?}");
    let usages = components.usages("Alert").unwrap();
    assert_eq!(2, usages.len());
    let props = components.props("Alert").unwrap();
    assert_eq!(2, props.len(), "{props:?}");
    assert!(props[0].live);
    assert!(props[0].writable);
    let indexed_components = components.find("Alert").unwrap();
    assert!(!indexed_components.is_empty());
    assert!(indexed_components[0].live);
    let blocks = components.blocks("Alert").unwrap();
    assert_eq!(1, blocks.len());
    assert_eq!("content", blocks[0].name);
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = &reopened.services().twig_components;
    assert!(restored.names().unwrap().contains(&"Alert".to_owned()));
    assert_eq!(usages, restored.usages("Alert").unwrap());
    let restored_props = restored.props("Alert").unwrap();
    assert_eq!(2, restored_props.len());
    assert!(restored_props[0].live);
    assert!(restored_props[0].writable);
    let restored_components = restored.find("Alert").unwrap();
    assert!(!restored_components.is_empty());
    assert!(restored_components[0].live);
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_vite_entrypoints() {
    use swlsp_asset::ReferenceKind;

    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            (
                "vite.config.ts",
                "export default defineConfig({\n  build: {rollupOptions: {input: {app: './assets/app.ts'}}}\n});",
            ),
            ("assets/app.ts", "export const app = {};"),
            (
                "templates/base.html.twig",
                "{{ vite_entry_script_tags('app') }}",
            ),
        ],
    );
    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let assets = &workspace.services().assets;
    let entries = assets.vite_entry_names().unwrap();
    assert_eq!(vec!["app".to_owned()], entries);
    let usages = assets.usages("app", ReferenceKind::ViteEntry).unwrap();
    assert_eq!(1, usages.len());
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let assets = &reopened.services().assets;
    assert_eq!(entries, assets.vite_entry_names().unwrap());
    assert_eq!(
        usages,
        assets.usages("app", ReferenceKind::ViteEntry).unwrap()
    );
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_messenger_graph() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            (
                "vendor/MessageBusInterface.php",
                r#"<?php
namespace Symfony\Component\Messenger;
interface MessageBusInterface {
    public function dispatch(object $message, array $stamps = []): object;
}"#,
            ),
            ("src/Message.php", "<?php\nnamespace App;\nclass Message {}"),
            (
                "src/Handler.php",
                r#"<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
#[AsMessageHandler]
class Handler {
    public function __invoke(Message $message): void {}
}"#,
            ),
            (
                "src/Publisher.php",
                r#"<?php
namespace App;
use Symfony\Component\Messenger\MessageBusInterface;
function publish(MessageBusInterface $messageBus): void {
    $messageBus->dispatch(new Message());
}"#,
            ),
        ],
    );
    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let message = workspace
        .services()
        .messenger
        .message("App\\Message")
        .unwrap()
        .expect("message indexed");
    assert_eq!(1, message.handlers().len());
    assert_eq!(1, message.dispatches().len());
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = reopened
        .services()
        .messenger
        .message("App\\Message")
        .unwrap()
        .expect("message restored");
    assert_eq!(message, restored);
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_environment_graph() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            (
                ".env",
                "\nDATABASE_URL=mysql://localhost/app\nAPP_ENV=dev\n",
            ),
            ("Dockerfile", "\nENV APP_RUNTIME Runtime\n"),
            (
                "config/services.yaml",
                "\nparameters:\n  database_url: '%env(resolve:DATABASE_URL)%'\n",
            ),
        ],
    );
    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let environment = &workspace.services().environment;
    let variable = environment
        .variable("DATABASE_URL")
        .unwrap()
        .expect("variable indexed");
    assert_eq!(1, variable.declarations.len());
    assert_eq!(1, variable.references.len());
    let names = environment.names().unwrap();
    assert!(names.contains(&"APP_RUNTIME".to_owned()), "{names:?}");
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = reopened
        .services()
        .environment
        .variable("DATABASE_URL")
        .unwrap()
        .expect("variable restored");
    assert_eq!(variable, restored);
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_deprecated_symfony_services() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[(
            "config/services.yaml",
            "services:\n  app.legacy:\n    class: App\\Legacy\n    deprecated: 'The %service_id% service is deprecated; use app.modern.'\n",
        )],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let service = workspace
        .services()
        .services
        .get_service_by_id("app.legacy")
        .unwrap()
        .expect("service indexed");
    assert!(service.deprecated);
    assert!(service.deprecation.contains("%service_id%"));
    assert!(!service.deprecated_range.is_empty());
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = reopened
        .services()
        .services
        .get_service_by_id("app.legacy")
        .unwrap()
        .expect("service restored");
    assert_eq!(service, restored);
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_twig_globals() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            (
                "config/services.yaml",
                "services:\n  app.clock:\n    class: App\\Clock\n",
            ),
            (
                "config/packages/twig.yaml",
                "twig:\n  globals:\n    clock: '@app.clock'\n    site_name: 'Store'\n",
            ),
            (
                "src/StorefrontExtension.php",
                r#"<?php
namespace App;
use Twig\Extension\AbstractExtension;
class Feature {}
class StorefrontExtension extends AbstractExtension {
    public function getGlobals(): array {
        return ['feature' => new Feature()];
    }
}"#,
            ),
        ],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let globals = workspace.services().twig.get_all_globals().unwrap();
    let by_name: HashMap<&str, &swlsp_twig::Global> = globals
        .iter()
        .map(|global| (global.name.as_str(), global))
        .collect();
    let type_of = |name: &str| {
        by_name
            .get(name)
            .map(|global| global.ty.as_str())
            .unwrap_or("")
    };
    assert_eq!("App\\Clock", type_of("clock"));
    assert_eq!("string", type_of("site_name"));
    assert_eq!("App\\Feature", type_of("feature"));
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = reopened.services().twig.get_all_globals().unwrap();
    assert_eq!(globals, restored);
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_deprecated_twig_callables() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[(
            "src/LegacyExtension.php",
            r#"<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class LegacyExtension extends AbstractExtension {
    public function getFunctions(): array {
        return [new TwigFunction('legacy_function', $this->legacy(...))];
    }
    public function legacy(): string {
        trigger_deprecation(
            'app',
            '1.0',
            'The "legacy_function" Twig function is deprecated. Use modern_function instead.',
        );
        return '';
    }
}"#,
        )],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let indexed = workspace
        .services()
        .twig
        .get_twig_function("legacy_function")
        .unwrap();
    assert_eq!(1, indexed.len());
    assert!(indexed[0].deprecated);
    assert!(indexed[0].deprecation.contains("modern_function"));
    assert!(!indexed[0].deprecated_range.is_empty());
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = reopened
        .services()
        .twig
        .get_twig_function("legacy_function")
        .unwrap();
    assert_eq!(indexed, restored);
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_template_references() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            (
                "templates/layout/base.html.twig",
                "{% block body %}{% endblock %}",
            ),
            (
                "templates/page.html.twig",
                "{% extends 'layout/base.html.twig' %}",
            ),
            (
                "src/PageController.php",
                r#"<?php
final class PageController {
    public function page() {
        return $this->render('layout/base.html.twig');
    }
}"#,
            ),
        ],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let references = workspace
        .services()
        .twig
        .get_template_references(&["layout/base.html.twig"])
        .unwrap();
    assert_eq!(2, references.len(), "{references:?}");
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = reopened
        .services()
        .twig
        .get_template_references(&["layout/base.html.twig"])
        .unwrap();
    assert_eq!(references, restored);
    reopened.close_workspace().unwrap();
}

#[test]
fn workspace_indexes_and_restores_inherited_twig_blocks() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = project.path();
    write_files(
        root,
        &[
            (
                "templates/layout/base.html.twig",
                "{% block body %}{% endblock %}\n{% block sidebar %}{% endblock %}",
            ),
            (
                "templates/page.html.twig",
                "{% extends 'layout/base.html.twig' %}\n{% block body %}{% endblock %}\n{% block page_actions %}{% endblock %}",
            ),
        ],
    );

    let workspace = open_workspace(root, cache.path());
    index_all(&workspace);
    let blocks = workspace
        .services()
        .twig
        .get_template_blocks(&["page.html.twig"])
        .unwrap();
    assert_eq!(4, blocks.len());
    let mut names: Vec<&str> = blocks.iter().map(|block| block.name.as_str()).collect();
    names.sort_unstable();
    assert_eq!(vec!["body", "body", "page_actions", "sidebar"], names);
    for block in &blocks {
        assert!(!block.range.is_empty());
    }
    workspace.close_workspace().unwrap();

    let reopened = open_workspace(root, cache.path());
    let restored = reopened
        .services()
        .twig
        .get_template_blocks(&["page.html.twig"])
        .unwrap();
    assert_eq!(blocks, restored);
    reopened.close_workspace().unwrap();
}
