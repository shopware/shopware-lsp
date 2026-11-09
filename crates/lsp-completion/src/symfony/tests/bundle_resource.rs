//! Tests for bundle and directory-scoped resource completion.

use std::sync::Arc;

use swlsp_indexer::{CancelToken, FileScanner};
use swlsp_lsp_protocol as protocol;

use super::*;
use crate::symfony::BundleResourceCompletionProvider;

struct Fixture {
    _dir: tempfile::TempDir,
    root: String,
    scanner: Arc<FileScanner>,
    provider: BundleResourceCompletionProvider,
}

fn write(path: &Path, source: &str) {
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, source).unwrap();
}

fn fixture() -> Fixture {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().join("project");
    std::fs::create_dir_all(root.join("config")).unwrap();
    let php = PhpIndex::open(root.join(".cache"), None).unwrap();
    let bundle_root = root.join("vendor/acme/foo-bundle");
    for (path, source) in [
        (
            root.join("BundleInterface.php"),
            "<?php\nnamespace Symfony\\Component\\HttpKernel\\Bundle;\ninterface BundleInterface {}\n",
        ),
        (
            bundle_root.join("FooBundle.php"),
            "<?php\nnamespace Acme\\Foo;\nfinal class FooBundle implements \\Symfony\\Component\\HttpKernel\\Bundle\\BundleInterface {}\n",
        ),
    ] {
        write(&path, source);
        index_file(&php, &p(&path), source);
    }
    for (path, source) in [
        ("Resources/config/routing.yml", "routes"),
        ("config/routes.xml", "<routes/>"),
        ("Controller/CatalogController.php", "<?php"),
        ("src/Controller/ModernController.php", "<?php"),
        ("README.md", "not a conventional resource"),
    ] {
        write(&bundle_root.join(path), source);
    }
    let scanner =
        Arc::new(FileScanner::new(p(&root), dir.path().join("scanner.db"), None).unwrap());
    scanner.index_all(&CancelToken::new()).unwrap();
    let provider = BundleResourceCompletionProvider::new(Some(php), Some(scanner.clone()));
    Fixture {
        root: p(&root),
        _dir: dir,
        scanner,
        provider,
    }
}

#[test]
fn bundle_resource_completion_across_configuration_syntaxes() {
    let fixture = fixture();
    let label = "FooBundle/Resources/config/routing.yml";
    for (name, file, source, expected_at) in [
        (
            "quoted YAML resource",
            "routes.yaml",
            "imports:\n  - resource: '@Foo<caret>'\n",
            "@Foo",
        ),
        (
            "empty flow YAML resource",
            "routes.yaml",
            "imports:\n  - { resource: <caret> }\n",
            "",
        ),
        (
            "nested YAML route resource path",
            "routes.yaml",
            "controllers:\n  resource:\n    path: '@Foo<caret>'\n    namespace: App\\Controller\n  type: attribute\n",
            "@Foo",
        ),
        (
            "XML import",
            "routes.xml",
            r#"<routes><import resource="@Foo<caret>"/></routes>"#,
            "@Foo",
        ),
        (
            "PHP configurator import",
            "routes.php",
            r"<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;
return static function (RoutingConfigurator $routes): void {
    $routes->import('@Foo<caret>', 'attribute');
};",
            "@Foo",
        ),
        (
            "empty PHP configurator import",
            "routes.php",
            r"<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;
return static function (RoutingConfigurator $routes): void {
    $routes->import('<caret>', 'attribute');
};",
            "",
        ),
    ] {
        let (source, offset) = completion_caret(source);
        let path = format!("{}/config/{file}", fixture.root);
        std::fs::write(&path, &source).unwrap();
        let (document, request) = bundle_request(&path, &source, offset);
        let items = completions(&fixture.provider, &request);
        let item = require_completion(&items, label);
        assert_eq!(
            item.kind,
            kind(protocol::CompletionItemKind::FILE),
            "{name}"
        );
        let edit = edit(&item);
        assert_eq!(edit.new_text, "@FooBundle/Resources/config/routing.yml");
        let line_index = document.line_index();
        let start = line_index.offset_utf16(edit.range.start.line, edit.range.start.character);
        let end = line_index.offset_utf16(edit.range.end.line, edit.range.end.character);
        assert_eq!(&source[start as usize..end as usize], expected_at, "{name}");
        assert!(start as usize <= offset && end as usize >= offset, "{name}");
    }
}

#[test]
fn bundle_resource_completion_includes_conventional_and_local_files() {
    let fixture = fixture();
    let local = format!("{}/config/routes/local.yaml", fixture.root);
    write(Path::new(&local), "local: {}\n");
    let (source, offset) = completion_caret("imports:\n  - { resource: '<caret>' }\n");
    let path = format!("{}/config/app.yaml", fixture.root);
    std::fs::write(&path, &source).unwrap();
    fixture.scanner.index_all(&CancelToken::new()).unwrap();
    let (_, request) = bundle_request(&path, &source, offset);
    let items = completions(&fixture.provider, &request);
    require_completion(&items, "FooBundle/Resources/config/routing.yml");
    require_completion(&items, "FooBundle/config/routes.xml");
    require_completion(&items, "FooBundle/Controller/CatalogController.php");
    require_completion(&items, "FooBundle/src/Controller/ModernController.php");
    let folder = require_completion(&items, "routes");
    assert_eq!(folder.kind, kind(protocol::CompletionItemKind::FOLDER));
    let file = require_completion(&items, "routes/local.yaml");
    assert_eq!(file.kind, kind(protocol::CompletionItemKind::FILE));
}

#[test]
fn bundle_resource_completion_rejects_unrelated_contexts() {
    let fixture = fixture();
    for (file, source) in [
        ("routes.yaml", "other: '@Foo<caret>'\n"),
        (
            "services.xml",
            r#"<services><service resource="@Foo<caret>"/></services>"#,
        ),
        (
            "routes.php",
            "<?php\nreturn static function (object $loader): void {\n    $loader->import('@Foo<caret>');\n};",
        ),
    ] {
        let (source, offset) = completion_caret(source);
        let path = format!("{}/config/{file}", fixture.root);
        std::fs::write(&path, &source).unwrap();
        fixture.scanner.index_all(&CancelToken::new()).unwrap();
        let (_, request) = bundle_request(&path, &source, offset);
        assert!(
            completions(&fixture.provider, &request).is_empty(),
            "{file}"
        );
    }
}
