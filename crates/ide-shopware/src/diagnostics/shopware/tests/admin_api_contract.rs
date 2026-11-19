//! Administration requests checked against the controller of their route.

use std::sync::Arc;

use swlsp_ide::inspections::REWRITE_NODE_FIX_ID;
use swlsp_ide::inspections::test_support::{check_diagnostics, check_fix};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Inspection;
use swlsp_php::PhpIndex;
use swlsp_symfony::RouteIndexer;
use tempfile::TempDir;

use super::php_api_contract::{FRAMEWORK, HEADER};
use crate::inspections::{ADD_API_TYPE_MEMBER_FIX_ID, new_admin_api_contract};

struct Project {
    _root: TempDir,
    _cache: TempDir,
    inspection: Arc<dyn Inspection>,
    uri: String,
}

fn project() -> Project {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let base = root.path().canonicalize().unwrap();
    let base = base.to_str().unwrap();
    let source = format!(
        r"{HEADER}
class ImportPayload
{{
    public function __construct(
        public string $profileId,
        public ?int $limit = null,
    ) {{}}
}}

#[Route(defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [ApiRouteScope::ID]])]
class SwagController
{{
    #[Route(path: '/api/_action/swag/run', name: 'api.action.swag.run', methods: ['POST'])]
    public function run(Request $request): JsonResponse
    {{
        $name = (string) $request->request->get('name');
        $dryRun = $request->request->getBoolean('dryRun');
        $limit = $request->query->getInt('limit', 10);

        return new JsonResponse(['count' => 3, 'done' => $dryRun]);
    }}

    #[Route(path: '/api/_action/swag/import', name: 'api.action.swag.import', methods: ['POST'])]
    public function import(#[MapRequestPayload] ImportPayload $payload): Response
    {{
        return new Response(null, Response::HTTP_NO_CONTENT);
    }}

    #[Route(path: '/api/_action/swag/resolved', name: 'api.action.swag.resolved', methods: ['POST'])]
    public function resolved(#[MapRequestPayload(resolver: ImportResolver::class)] ImportPayload $payload): Response
    {{
        return new Response(null, Response::HTTP_NO_CONTENT);
    }}

    #[Route(path: '/api/_action/swag/forward', name: 'api.action.swag.forward', methods: ['POST'])]
    public function forward(RequestDataBag $data): JsonResponse
    {{
        $data->get('id');

        return $this->service->forward($data);
    }}
}}"
    );
    let php = PhpIndex::open(cache.path().join("php"), None).unwrap();
    let routes = RouteIndexer::new(cache.path().join("routes"), None).unwrap();
    for (path, source) in FRAMEWORK {
        php.index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .unwrap();
    }
    let controller = format!("{base}/src/Controller/SwagController.php");
    std::fs::create_dir_all(format!("{base}/src/Controller")).unwrap();
    std::fs::write(&controller, &source).unwrap();
    let file = ParsedFile::new(controller, source.into_bytes());
    php.index(&file).unwrap();
    routes.index(&file).unwrap();
    Project {
        inspection: new_admin_api_contract(Some(Arc::new(routes)), Some(php)),
        uri: format!(
            "file://{base}/src/Resources/app/administration/src/service/swag.api.service.ts"
        ),
        _root: root,
        _cache: cache,
    }
}

#[test]
fn request_keys_are_checked_against_what_the_controller_reads() {
    let project = project();
    let (_, problems) = check_diagnostics(
        &*project.inspection,
        &project.uri,
        r"type RunPayload = {
    name: string;
    dryRun?: boolean;
    {|admin.api.request-unknown-key:force: boolean;|}
};

type ImportPayload = {
    limit?: number;
};

class SwagApiService extends ApiService {
    run(payload: RunPayload) {
        return this.httpClient.post('/_action/swag/run', payload, {
            params: { limit: 5, {|admin.api.request-unknown-key:page|}: 1 },
        });
    }

    literal(name) {
        const payload = { {|admin.api.request-unknown-key:nam|}: name };
        return this.httpClient.post('/_action/swag/run', {|admin.api.request-missing-key:payload|});
    }

    spread(name, rest) {
        return this.httpClient.post('/_action/swag/run', { ...rest });
    }

    import(payload: ImportPayload) {
        return this.httpClient.post('/_action/swag/import', {|admin.api.request-missing-key:payload|});
    }

    // The controller hands the bag on: any key may be read.
    forward() {
        return this.httpClient.post('/_action/swag/forward', { other: 1 });
    }

    // A resolver of its own takes a request of another form.
    resolved() {
        return this.httpClient.post('/_action/swag/resolved', { id: 1 });
    }

    unknown(payload) {
        return this.httpClient.post('/_action/swag/run', payload);
    }
}
",
    );
    assert_eq!(
        problems
            .iter()
            .map(|problem| problem.message.as_str())
            .collect::<Vec<_>>(),
        [
            "SwagController::run does not read the payload key 'force'",
            "SwagController::run does not read the query key 'page'",
            "SwagController::run does not read the payload key 'nam'",
            "SwagController::run reads the payload key 'name', which is not sent",
            "SwagController::import reads the payload key 'profileId', which is not sent",
        ]
    );
}

const RESPONSE: &str = r"type RunResponse = {
    count: {|admin.api.response-type:string|};
    done: boolean;
    {|admin.api.response-type:total: number;|}
};

class SwagApiService extends ApiService {
    public async run(): Promise<RunResponse> {
        const response = await this.httpClient.post('/_action/swag/run', { name: 'a' });

        return ApiService.handleResponse(response) as RunResponse;
    }
}
";

#[test]
fn declared_response_type_is_checked_and_fixed() {
    let project = project();
    let (_, problems) = check_diagnostics(&*project.inspection, &project.uri, RESPONSE);
    assert_eq!(
        problems
            .iter()
            .map(|problem| problem.message.as_str())
            .collect::<Vec<_>>(),
        [
            "SwagController::run answers 'count' as number, not string",
            "SwagController::run does not answer the key 'total'",
        ]
    );
    let fixed = |before: &str, after: &str| {
        check_fix(
            &*project.inspection,
            &project.uri,
            before,
            REWRITE_NODE_FIX_ID,
            after,
        )
        .title
    };
    let plain = RESPONSE
        .replace("{|admin.api.response-type:", "")
        .replace("|}", "");
    assert_eq!(
        fixed(
            &RESPONSE.replace(
                "{|admin.api.response-type:total: number;|}",
                "total: number;"
            ),
            &plain.replace("count: string", "count: number"),
        ),
        "Change the type to number"
    );
    assert_eq!(
        fixed(
            &RESPONSE.replace("{|admin.api.response-type:string|}", "string"),
            &plain.replace("    total: number;\n", ""),
        ),
        "Remove 'total' from the type"
    );
}

#[test]
fn a_missing_key_is_added_to_the_declared_type() {
    let project = project();
    let service = |declaration: &str, marked: bool| {
        let argument = if marked {
            "{|admin.api.request-missing-key:payload|}"
        } else {
            "payload"
        };
        format!(
            "{declaration}

class SwagApiService extends ApiService {{
    import(payload: ImportPayload) {{
        return this.httpClient.post('/_action/swag/import', {argument});
    }}
}}
"
        )
    };
    let cases = [
        (
            "type ImportPayload = {\n    limit?: number;\n};",
            "type ImportPayload = {\n    limit?: number;\n    profileId: string;\n};",
        ),
        // The separator and a missing one follow the type.
        (
            "interface ImportPayload {\n  limit?: number,\n  page?: number\n}",
            "interface ImportPayload {\n  limit?: number,\n  page?: number,\n  profileId: string,\n}",
        ),
        (
            "type ImportPayload = { limit?: number };",
            "type ImportPayload = { limit?: number; profileId: string };",
        ),
        (
            "type ImportPayload = {};",
            "type ImportPayload = { profileId: string };",
        ),
        (
            "type ImportPayload = {\n};",
            "type ImportPayload = {\n    profileId: string;\n};",
        ),
    ];
    for (before, after) in cases {
        let fix = check_fix(
            &*project.inspection,
            &project.uri,
            &service(before, true),
            ADD_API_TYPE_MEMBER_FIX_ID,
            &service(after, false),
        );
        assert_eq!(fix.title, "Add 'profileId' to the type ImportPayload");
    }
}

#[test]
fn a_literal_request_has_no_fix_for_a_missing_key() {
    let project = project();
    let (_, problems) = check_diagnostics(
        &*project.inspection,
        &project.uri,
        "class SwagApiService extends ApiService {
    import() {
        return this.httpClient.post('/_action/swag/import', {|admin.api.request-missing-key:{ limit: 1 }|});
    }
}
",
    );
    assert_eq!(problems.len(), 1);
    assert!(
        !format!("{:?}", problems[0].payload).contains("insertAt"),
        "{:?}",
        problems[0].payload
    );
}
