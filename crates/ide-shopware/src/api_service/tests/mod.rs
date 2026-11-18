//! The code action and the command that generate an API service.

use std::sync::Arc;

use serde_json::json;
use swlsp_extension::ExtensionIndexer;
use swlsp_ide::codeaction::testing::{GenerationTestHost, path_string, request_at};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, Context};
use swlsp_php::PhpIndex;
use tempfile::TempDir;

use super::{ApiServiceProvider, GENERATE_API_SERVICE_ACTION};

const FRAMEWORK: &str = r"<?php
namespace Shopware\Core\Framework;
abstract class Bundle {}
abstract class Plugin extends Bundle {}
namespace Shopware\Core\Framework\Routing;
class ApiRouteScope { public const ID = 'api'; }
namespace Symfony\Component\Routing\Attribute;
class Route {}
namespace Symfony\Component\HttpFoundation;
class Response {}
class JsonResponse extends Response { public function __construct(mixed $data = null) {} }
";
const PLUGIN: &str = r"<?php
namespace Swag\Example;
use Shopware\Core\Framework\Plugin;
class SwagExample extends Plugin {}
";
const CONTROLLER: &str = r"<?php
namespace Swag\Example\Controller;

use Shopware\Core\Framework\Routing\ApiRouteScope;
use Symfony\Component\HttpFoundation\JsonResponse;
use Symfony\Component\Routing\Attribute\Route;

#[Route(defaults: ['_routeScope' => [ApiRouteScope::ID]])]
class SyncController
{
    #[Route(path: '/api/_action/swag-example/sync', name: 'api.action.swag-example.sync', methods: ['POST'])]
    public function sync(): JsonResponse
    {
        return new JsonResponse(['synced' => 3]);
    }
}
";

struct Project {
    _root: TempDir,
    _cache: TempDir,
    provider: ApiServiceProvider,
    controller: String,
    administration: String,
}

fn project(files: &[(&str, &str)]) -> Project {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let base = path_string(&root.path().canonicalize().unwrap());
    let plugin = format!("{base}/custom/plugins/SwagExample/src");
    let php = PhpIndex::open(cache.path().join("php"), None).unwrap();
    let extensions =
        Arc::new(ExtensionIndexer::new(cache.path().join("extensions"), None).unwrap());
    extensions.set_php_index(Some(php.clone()));
    let write = |path: &str, source: &str| {
        std::fs::create_dir_all(std::path::Path::new(path).parent().unwrap()).unwrap();
        std::fs::write(path, source).unwrap();
    };
    let controller = format!("{plugin}/Controller/SyncController.php");
    for (path, source) in [
        (format!("{base}/vendor/framework.php"), FRAMEWORK),
        (format!("{plugin}/SwagExample.php"), PLUGIN),
        (controller.clone(), CONTROLLER),
    ] {
        write(&path, source);
        let file = ParsedFile::new(path, source.as_bytes().to_vec());
        php.index(&file).unwrap();
        extensions.index(&file).unwrap();
    }
    let administration = format!("{plugin}/Resources/app/administration/src");
    for (path, source) in files {
        write(&format!("{administration}/{path}"), source);
    }
    let host = Arc::new(GenerationTestHost::default());
    Project {
        provider: ApiServiceProvider::new(Some(php), extensions, Some(host)),
        controller,
        administration,
        _root: root,
        _cache: cache,
    }
}

/// The created files and the documents that are edited, as `(path, text)`.
fn generate(project: &Project) -> Vec<(String, String)> {
    let uri = swlsp_uriutil::file_uri(&project.controller);
    let response = project
        .provider
        .create(&Context::background(), Some(&json!({ "uri": uri })))
        .unwrap();
    let edit = response.edit.unwrap();
    edit.document_changes
        .iter()
        .filter_map(|change| {
            let uri = if change.kind == "create" {
                change.uri.clone()
            } else {
                change.text_document.as_ref()?.uri.clone()
            };
            let relative = swlsp_uriutil::path(&uri)
                .unwrap()
                .strip_prefix(&format!("{}/", project.administration))?
                .to_owned();
            let text = change
                .edits
                .iter()
                .map(|edit| edit.new_text.as_str())
                .collect::<String>();
            Some((relative, text))
        })
        .filter(|(_, text)| !text.is_empty())
        .collect()
}

#[test]
fn action_is_offered_on_the_class_of_an_api_controller() {
    let project = project(&[]);
    let uri = swlsp_uriutil::file_uri(&project.controller);
    let titles = |needle: &str| {
        let offset = CONTROLLER.find(needle).unwrap() as u32;
        project
            .provider
            .get_code_actions(
                &Context::background(),
                &request_at(&uri, CONTROLLER, offset, offset),
            )
            .into_iter()
            .map(|action| {
                let command = action.command.expect("a command");
                assert_eq!(command.command, GENERATE_API_SERVICE_ACTION);
                assert_eq!(command.arguments, [json!(uri), json!("SyncController")]);
                action.title
            })
            .collect::<Vec<_>>()
    };
    assert_eq!(
        titles("SyncController"),
        ["Shopware: Generate Administration API service SyncApiService"]
    );
    // Not on every line of the class.
    assert_eq!(titles("return new"), Vec::<String>::new());
}

#[test]
fn service_is_created_and_imported_from_the_entry_file() {
    let project = project(&[("main.js", "import './module/swag-example';\n")]);
    let changes = generate(&project);
    assert_eq!(changes.len(), 2, "{changes:?}");
    assert_eq!(changes[0].0, "service/sync.api.service.ts");
    assert!(
        changes[0]
            .1
            .contains("export default class SyncApiService extends ApiService")
    );
    assert!(changes[0].1.contains("public async sync("));
    assert!(changes[0].1.contains("synced: number;"));
    assert_eq!(
        changes[1],
        (
            "main.js".to_owned(),
            "import './service/sync.api.service';\n".to_owned()
        )
    );
}

#[test]
fn service_goes_where_the_extension_keeps_its_services() {
    let project = project(&[
        ("main.ts", "import './api/other.api.service';"),
        ("api/other.api.service.ts", ""),
    ]);
    assert_eq!(
        generate(&project)
            .into_iter()
            .map(|(path, text)| (path, text.lines().last().unwrap_or("").to_owned()))
            .collect::<Vec<_>>(),
        [
            ("api/sync.api.service.ts".to_owned(), "}".to_owned()),
            (
                "main.ts".to_owned(),
                "import './api/sync.api.service';".to_owned()
            ),
        ]
    );
}

#[test]
fn entry_file_is_created_and_an_existing_service_is_kept() {
    let project = project(&[]);
    let changes = generate(&project);
    assert_eq!(
        changes[1],
        (
            "main.ts".to_owned(),
            "import './service/sync.api.service';\n".to_owned()
        )
    );
    std::fs::create_dir_all(format!("{}/service", project.administration)).unwrap();
    std::fs::write(
        format!("{}/service/sync.api.service.ts", project.administration),
        "",
    )
    .unwrap();
    let uri = swlsp_uriutil::file_uri(&project.controller);
    let error = project
        .provider
        .create(&Context::background(), Some(&json!({ "uri": uri })))
        .unwrap_err();
    assert!(error.to_string().contains("already exists"), "{error}");
}
