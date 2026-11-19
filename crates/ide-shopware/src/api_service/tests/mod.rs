//! The code action and the command that generate an API service.

use std::sync::Arc;

use serde_json::json;
use swlsp_admin::ApiRequestIndex;
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
    project_of(CONTROLLER, files)
}

fn project_of(controller_source: &str, files: &[(&str, &str)]) -> Project {
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
        (controller.clone(), controller_source),
    ] {
        write(&path, source);
        let file = ParsedFile::new(path, source.as_bytes().to_vec());
        php.index(&file).unwrap();
        extensions.index(&file).unwrap();
    }
    let administration = format!("{plugin}/Resources/app/administration/src");
    let requests = Arc::new(ApiRequestIndex::new(cache.path().join("requests"), None).unwrap());
    for (path, source) in files {
        let path = format!("{administration}/{path}");
        write(&path, source);
        requests
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }
    let host = Arc::new(GenerationTestHost::default());
    Project {
        provider: ApiServiceProvider::new(Some(php), extensions, Some(requests), Some(host)),
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
        .generate(&Context::background(), Some(&json!({ "uri": uri })))
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
                assert_eq!(
                    command.arguments,
                    [json!(uri), json!("SyncController"), json!("")]
                );
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
fn the_class_is_selected_by_its_short_or_qualified_name() {
    let project = project(&[]);
    let uri = swlsp_uriutil::file_uri(&project.controller);
    let created = |class: &str| {
        project
            .provider
            .generate(
                &Context::background(),
                Some(&json!({ "uri": uri, "className": class })),
            )
            .map(|response| response.created)
            .map_err(|error| error.to_string())
    };
    assert_eq!(created("SyncController"), Ok(true));
    assert_eq!(
        created("Swag\\Example\\Controller\\SyncController"),
        Ok(true)
    );
    assert_eq!(
        created("OtherController"),
        Err("no controller with Admin API actions was found".to_owned())
    );
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
fn entry_file_is_created_without_one() {
    let project = project(&[]);
    let changes = generate(&project);
    assert_eq!(
        changes[1],
        (
            "main.ts".to_owned(),
            "import './service/sync.api.service';\n".to_owned()
        )
    );
}

const TWO_ACTIONS: &str = r"<?php
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

    #[Route(path: '/api/_action/swag-example/status/{id}', name: 'api.action.swag-example.status', methods: ['GET'])]
    public function status(string $id): JsonResponse
    {
        return new JsonResponse(['running' => true]);
    }
}
";
const SERVICE: &str = "const { ApiService } = Shopware.Classes;

type StatusResponse = string;

/**
 * Hand-written.
 */
export default class SyncService extends ApiService {
    constructor(httpClient, loginService, apiEndpoint = 'swag-example') {
        super(httpClient, loginService, apiEndpoint);
    }

    sync() {
        return this.httpClient.post('/_action/swag-example/sync', null, {
            headers: this.getBasicHeaders(),
        });
    }
}
";

fn titles_at(project: &Project, source: &str, needle: &str) -> Vec<String> {
    let uri = swlsp_uriutil::file_uri(&project.controller);
    let offset = source.find(needle).unwrap() as u32;
    project
        .provider
        .get_code_actions(
            &Context::background(),
            &request_at(&uri, source, offset, offset),
        )
        .into_iter()
        .map(|action| action.title)
        .collect()
}

#[test]
fn missing_actions_are_added_to_the_service_that_requests_the_others() {
    // Found by its requests: the file is not named after the controller.
    let project = project_of(TWO_ACTIONS, &[("api/swag.service.ts", SERVICE)]);
    let uri = swlsp_uriutil::file_uri(&project.controller);
    let response = project
        .provider
        .generate(&Context::background(), Some(&json!({ "uri": uri })))
        .unwrap();
    assert!(!response.created);
    assert_eq!(response.methods, ["status"]);
    assert!(response.primary_file_uri.ends_with("/api/swag.service.ts"));
    let mut edits = response.edit.unwrap().document_changes[0].edits.clone();
    edits.sort_by_key(|edit| edit.range.start.line);
    let edits: Vec<(u32, &str)> = edits
        .iter()
        .map(|edit| (edit.range.start.line, edit.new_text.as_str()))
        .collect();
    assert_eq!(
        edits,
        [
            // Above the comment of the class; `StatusResponse` is taken.
            (
                4,
                "export type StatusResponse2 = {\n    running: boolean;\n};\n\n"
            ),
            (
                17,
                "
    /**
     * GET /api/_action/swag-example/status/{id}
     */
    public async status(id: string, additionalHeaders: Record<string, string> = {}): Promise<StatusResponse2> {
        const response = await this.httpClient.get(`/_action/swag-example/status/${id}`, {
            headers: this.getBasicHeaders(additionalHeaders),
        });

        return ApiService.handleResponse(response) as StatusResponse2;
    }
"
            ),
        ]
    );

    assert_eq!(
        titles_at(&project, TWO_ACTIONS, "SyncController"),
        ["Shopware: Add 1 missing action to Administration API service swag.service.ts"]
    );
    assert_eq!(
        titles_at(&project, TWO_ACTIONS, "status(string"),
        ["Shopware: Add status to Administration API service swag.service.ts"]
    );
    // Requested already, and not inside a body.
    assert_eq!(
        titles_at(&project, TWO_ACTIONS, "sync()"),
        Vec::<String>::new()
    );
    assert_eq!(
        titles_at(&project, TWO_ACTIONS, "'running'"),
        Vec::<String>::new()
    );

    let error = |method: &str| {
        project
            .provider
            .generate(
                &Context::background(),
                Some(&json!({ "uri": uri, "method": method })),
            )
            .unwrap_err()
            .to_string()
    };
    assert_eq!(
        error("sync"),
        "swag.service.ts already requests every selected action"
    );
    assert_eq!(
        error("other"),
        "SyncController has no Admin API action other"
    );
}

/// The edits of the one changed document, by line.
fn extended(project: &Project) -> (String, Vec<(u32, String)>) {
    let uri = swlsp_uriutil::file_uri(&project.controller);
    let response = project
        .provider
        .generate(&Context::background(), Some(&json!({ "uri": uri })))
        .unwrap();
    assert!(!response.created);
    let mut edits = response.edit.unwrap().document_changes[0].edits.clone();
    edits.sort_by_key(|edit| edit.range.start.line);
    (
        response.primary_file_uri,
        edits
            .into_iter()
            .map(|edit| (edit.range.start.line, edit.new_text))
            .collect(),
    )
}

#[test]
fn a_javascript_service_gets_untyped_methods() {
    let service = SERVICE.replace("type StatusResponse = string;\n\n", "");
    let project = project_of(TWO_ACTIONS, &[("api/swag.service.js", &service)]);
    let (file, edits) = extended(&project);
    assert!(file.ends_with("/api/swag.service.js"));
    assert_eq!(
        edits,
        [(
            15,
            "
    /**
     * GET /api/_action/swag-example/status/{id}
     */
    async status(id, additionalHeaders = {}) {
        const response = await this.httpClient.get(`/_action/swag-example/status/${id}`, {
            headers: this.getBasicHeaders(additionalHeaders),
        });

        return ApiService.handleResponse(response);
    }
"
            .to_owned()
        )]
    );
    assert_eq!(
        titles_at(&project, TWO_ACTIONS, "SyncController"),
        ["Shopware: Add 1 missing action to Administration API service swag.service.js"]
    );
}

#[test]
fn a_javascript_service_without_the_class_at_hand_answers_the_data() {
    let service = SERVICE
        .replace("type StatusResponse = string;\n\n", "")
        .replace("const { ApiService } = Shopware.Classes;\n\n", "")
        .replace("extends ApiService", "extends Shopware.Classes.ApiService");
    // The JavaScript file of the name the generator would write.
    let project = project_of(TWO_ACTIONS, &[("service/sync.api.service.js", &service)]);
    let (file, edits) = extended(&project);
    assert!(file.ends_with("/service/sync.api.service.js"));
    assert_eq!(edits.len(), 1);
    assert!(
        edits[0]
            .1
            .ends_with("        return response.data;\n    }\n"),
        "{}",
        edits[0].1
    );
}

#[test]
fn the_typescript_service_wins_over_a_javascript_one() {
    let javascript = SERVICE.replace("type StatusResponse = string;\n\n", "");
    let project = project_of(
        TWO_ACTIONS,
        &[
            ("api/swag.service.js", javascript.as_str()),
            ("api/typed.service.ts", SERVICE),
        ],
    );
    assert!(extended(&project).0.ends_with("/api/typed.service.ts"));
}

#[test]
fn a_file_that_is_no_service_is_not_extended() {
    let javascript = SERVICE.replace("type StatusResponse = string;\n\n", "");
    let project = project_of(TWO_ACTIONS, &[("module/swag-generator.js", &javascript)]);
    let uri = swlsp_uriutil::file_uri(&project.controller);
    let response = project
        .provider
        .generate(&Context::background(), Some(&json!({ "uri": uri })))
        .unwrap();
    assert!(response.created);
    assert!(response.primary_file_uri.ends_with("/sync.api.service.ts"));
}

#[test]
fn a_service_without_a_class_is_not_extended() {
    let project = project(&[("service/sync.api.service.ts", "")]);
    let uri = swlsp_uriutil::file_uri(&project.controller);
    let error = project
        .provider
        .generate(&Context::background(), Some(&json!({ "uri": uri })))
        .unwrap_err();
    assert_eq!(
        error.to_string(),
        "sync.api.service.ts: the service declares no class"
    );
}
