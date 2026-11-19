//! The contract of Admin API actions and the service generated from it.

use swlsp_parser_php::query as phpquery;

use super::php_semantic::php_index_with;
use crate::api_service::generate::{Flavour, service_source};
use crate::diagnostics::shopware::{ApiController, api_controller};

pub(super) const FRAMEWORK: &[(&str, &str)] = &[
    (
        "/project/vendor/Routing.php",
        r"<?php
namespace Symfony\Component\Routing\Attribute;
class Route { public function __construct(?string $path = null, ?string $name = null, array $requirements = [], array $options = [], array $defaults = [], ?string $host = null, array $methods = []) {} }",
    ),
    (
        "/project/vendor/HttpFoundation.php",
        r"<?php
namespace Symfony\Component\HttpFoundation;
class ParameterBag
{
    public function get(string $key, mixed $default = null): mixed { return null; }
    public function getInt(string $key, int $default = 0): int { return 0; }
    public function getString(string $key, string $default = ''): string { return ''; }
    public function getBoolean(string $key, bool $default = false): bool { return false; }
    public function has(string $key): bool { return false; }
    public function all(?string $key = null): array { return []; }
}
class Request
{
    public const METHOD_POST = 'POST';
    public ParameterBag $request;
    public ParameterBag $query;
}
class Response
{
    public const HTTP_NO_CONTENT = 204;
    public const HTTP_NOT_FOUND = 404;
    public function __construct(?string $content = '', int $status = 200, array $headers = []) {}
}
class JsonResponse extends Response
{
    public function __construct(mixed $data = null, int $status = 200, array $headers = []) {}
}
class BinaryFileResponse extends Response {}",
    ),
    (
        "/project/vendor/Shopware.php",
        r"<?php
namespace Shopware\Core;
class PlatformRequest { public const ATTRIBUTE_ROUTE_SCOPE = '_routeScope'; }
namespace Shopware\Core\Framework\Routing;
class ApiRouteScope { public const ID = 'api'; }
namespace Shopware\Core\Framework\Validation\DataBag;
class DataBag extends \Symfony\Component\HttpFoundation\ParameterBag {}
class RequestDataBag extends DataBag {}
class QueryDataBag extends DataBag {}
namespace Shopware\Core\Framework\Struct;
abstract class Struct implements \JsonSerializable
{
    protected array $extensions = [];
    public function jsonSerialize(): array { return []; }
}",
    ),
    (
        "/project/vendor/HttpKernel.php",
        r"<?php
namespace Symfony\Component\HttpKernel\Attribute;
class MapRequestPayload {}
class MapQueryString {}",
    ),
];

fn controller(source: &str) -> ApiController {
    let (_dir, index) = php_index_with(FRAMEWORK);
    let path = "/project/src/Controller/ExampleController.php";
    let parse = swlsp_parser_php::parse(source);
    let root = parse.root();
    let semantic = index.analyze_document(path, 1, &root, source);
    let snapshot = index.semantic_snapshot().with_document(&semantic);
    let class = phpquery::classes(&root).pop().expect("a class");
    api_controller(&semantic, &snapshot, &class).expect("an API controller")
}

pub(super) const HEADER: &str = r"<?php
namespace Swag\Example\Controller;

use Shopware\Core\Framework\Routing\ApiRouteScope;
use Shopware\Core\Framework\Validation\DataBag\QueryDataBag;
use Shopware\Core\Framework\Validation\DataBag\RequestDataBag;
use Shopware\Core\PlatformRequest;
use Symfony\Component\HttpFoundation\BinaryFileResponse;
use Symfony\Component\HttpFoundation\JsonResponse;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\HttpKernel\Attribute\MapQueryString;
use Symfony\Component\HttpKernel\Attribute\MapRequestPayload;
use Symfony\Component\Routing\Attribute\Route;
";

#[test]
fn generates_a_typed_service_for_an_extension() {
    let source = format!(
        r"{HEADER}
class ImportPayload
{{
    public function __construct(
        public string $profileId,
        public ?int $limit = null,
        /** @var list<string> */
        public array $tags = [],
    ) {{}}
}}

#[Route(defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [ApiRouteScope::ID]])]
class SwagExampleController
{{
    #[Route(path: '/api/_action/swag-example/{{id}}/run', name: 'api.action.swag-example.run', methods: [Request::METHOD_POST])]
    public function run(string $id, Request $request): JsonResponse
    {{
        $name = (string) $request->request->get('name');
        $dryRun = $request->request->getBoolean('dryRun');
        $limit = $request->query->getInt('limit', 10);

        return new JsonResponse(['id' => $id, 'count' => 3, 'tags' => ['a', 'b'], 'done' => $dryRun]);
    }}

    #[Route(path: '/api/_action/swag-example/import', name: 'api.action.swag-example.import', methods: ['POST'])]
    public function import(#[MapRequestPayload] ImportPayload $payload): Response
    {{
        return new Response(null, Response::HTTP_NO_CONTENT);
    }}

    #[Route(path: '/api/_action/swag-example/export', name: 'api.action.swag-example.export', methods: ['GET'])]
    public function export(QueryDataBag $query): Response
    {{
        $query->getString('format');

        return new BinaryFileResponse('/tmp/file');
    }}

    #[Route(path: '/api/_action/swag-example/delete', name: 'api.action.swag-example.delete', methods: ['POST'])]
    public function delete(RequestDataBag $data): JsonResponse
    {{
        $this->service->delete($data);

        return $this->load();
    }}

    #[Route(path: '/swag-example/storefront', name: 'frontend.swag-example', methods: ['GET'], defaults: ['_routeScope' => ['storefront']])]
    public function storefront(): Response
    {{
        return new Response('');
    }}
}}"
    );
    let controller = controller(&source);
    let generated = service_source(&controller, Flavour::Extension);
    // The server only creates files its parser reads without errors.
    assert_eq!(swlsp_parser_js::parse(&generated).errors, []);
    assert_eq!(
        generated,
        r"const { ApiService } = Shopware.Classes;

type HttpClient = ConstructorParameters<typeof ApiService>[0];
type LoginService = ConstructorParameters<typeof ApiService>[1];

export type RunPayload = {
    name: string;
    dryRun?: boolean;
};

export type RunQuery = {
    limit?: number;
};

export type RunResponse = {
    count: number;
    done: boolean;
    id: string;
    tags: string[];
};

export type ImportPayload = {
    profileId: string;
    limit?: number | null;
    tags?: string[];
};

export type ExportQuery = {
    format: string;
};

/**
 * Gateway to the Admin API actions of SwagExampleController.
 */
export default class SwagExampleApiService extends ApiService {
    constructor(httpClient: HttpClient, loginService: LoginService, apiEndpoint = 'swag-example') {
        super(httpClient, loginService, apiEndpoint);
        this.name = 'swagExampleApiService';
    }

    /**
     * POST /api/_action/swag-example/{id}/run
     */
    public async run(
        id: string,
        payload: RunPayload,
        params: RunQuery = {},
        additionalHeaders: Record<string, string> = {},
    ): Promise<RunResponse> {
        const response = await this.httpClient.post(`/_action/swag-example/${id}/run`, payload, {
            params,
            headers: this.getBasicHeaders(additionalHeaders),
        });

        return ApiService.handleResponse(response) as RunResponse;
    }

    /**
     * POST /api/_action/swag-example/import
     */
    public async import(payload: ImportPayload, additionalHeaders: Record<string, string> = {}): Promise<void> {
        await this.httpClient.post('/_action/swag-example/import', payload, {
            headers: this.getBasicHeaders(additionalHeaders),
        });
    }

    /**
     * GET /api/_action/swag-example/export
     */
    public async export(params: ExportQuery, additionalHeaders: Record<string, string> = {}): Promise<Blob> {
        const response = await this.httpClient.get('/_action/swag-example/export', {
            params,
            headers: this.getBasicHeaders(additionalHeaders),
            responseType: 'blob',
        });

        return response.data as Blob;
    }

    /**
     * POST /api/_action/swag-example/delete
     */
    public async deletePost(
        payload: Record<string, unknown>,
        additionalHeaders: Record<string, string> = {},
    ): Promise<unknown> {
        const response = await this.httpClient.post('/_action/swag-example/delete', payload, {
            headers: this.getBasicHeaders(additionalHeaders),
        });

        return ApiService.handleResponse(response);
    }
}

Shopware.Application.addServiceProvider('swagExampleApiService', (container) => {
    const initContainer = Shopware.Application.getContainer('init');

    return new SwagExampleApiService(initContainer.httpClient, container.loginService);
});

declare global {
    interface ServiceContainer {
        swagExampleApiService: SwagExampleApiService;
    }
}
"
    );
}

#[test]
fn mapped_payloads_share_the_type_of_their_class() {
    let source = format!(
        r"{HEADER}
enum Format: string {{ case Pdf = 'pdf'; }}

class GenerationRequest
{{
    /** @param list<Format|string> $formats */
    public function __construct(public string $orderId, public array $formats, public ?string $comment = null) {{}}
}}

#[Route(defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [ApiRouteScope::ID]])]
class DocumentController
{{
    #[Route(path: '/api/_action/document/create', name: 'api.action.document.create', methods: ['POST'])]
    public function create(#[MapRequestPayload] GenerationRequest $request): JsonResponse
    {{
        return new JsonResponse(['id' => $request->orderId, 'comment' => $request->comment]);
    }}

    #[Route(path: '/api/_action/document/preview', name: 'api.action.document.preview', methods: ['POST'])]
    public function preview(#[MapRequestPayload] GenerationRequest $request): Response
    {{
        return new Response('');
    }}
}}"
    );
    let controller = controller(&source);
    let generated = service_source(&controller, Flavour::Core);
    assert_eq!(swlsp_parser_js::parse(&generated).errors, []);
    assert_eq!(
        generated,
        r"import type { HttpClient } from 'src/core/factory/http-client.types';
import ApiService from '../api.service';
import type { LoginService } from '../login.service';

export type GenerationRequest = {
    orderId: string;
    formats: string[];
    comment?: string | null;
};

export type CreateResponse = {
    comment: string | null;
    id: string;
};

/**
 * Gateway to the Admin API actions of DocumentController.
 */
export default class DocumentApiService extends ApiService {
    constructor(httpClient: HttpClient, loginService: LoginService, apiEndpoint = 'document') {
        super(httpClient, loginService, apiEndpoint);
        this.name = 'documentApiService';
    }

    /**
     * POST /api/_action/document/create
     */
    public async create(
        payload: GenerationRequest,
        additionalHeaders: Record<string, string> = {},
    ): Promise<CreateResponse> {
        const response = await this.httpClient.post('/_action/document/create', payload, {
            headers: this.getBasicHeaders(additionalHeaders),
        });

        return ApiService.handleResponse(response) as CreateResponse;
    }

    /**
     * POST /api/_action/document/preview
     */
    public async preview(payload: GenerationRequest, additionalHeaders: Record<string, string> = {}): Promise<void> {
        await this.httpClient.post('/_action/document/preview', payload, {
            headers: this.getBasicHeaders(additionalHeaders),
        });
    }
}
"
    );
}

#[test]
fn responses_follow_how_a_class_is_encoded() {
    let source = format!(
        r"{HEADER}
use Shopware\Core\Framework\Struct\Struct;

class Progress extends Struct
{{
    protected int $total = 0;
    private string $secret = '';
    public static int $instances = 0;
}}

class Money implements \JsonSerializable
{{
    public int $cents = 0;
    /** @return array{{amount: float, currency: string}} */
    public function jsonSerialize(): array {{ return ['amount' => 1.0, 'currency' => 'EUR']; }}
}}

#[Route(defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [ApiRouteScope::ID]])]
class StateController
{{
    #[Route(path: '/api/_action/state/{{id}}', name: 'api.action.state', methods: ['GET'])]
    public function state(string $id, Progress $progress, Money $money): JsonResponse
    {{
        if ($id === '') {{
            return new JsonResponse(null, Response::HTTP_NO_CONTENT);
        }}
        if ($id === 'missing') {{
            return new JsonResponse(['error' => 'Not found'], Response::HTTP_NOT_FOUND);
        }}

        return new JsonResponse(['progress' => $progress, 'price' => $money]);
    }}
}}"
    );
    let controller = controller(&source);
    let action = &controller.actions[0];
    assert_eq!(action.path_parameters, ["id"]);
    assert_eq!((&action.query, &action.body), (&None, &None));
    assert_eq!(
        action.response.typescript(0),
        r"{
    price: {
        amount: number;
        currency: string;
    };
    progress: {
        total: number;
        extensions: unknown;
    };
} | void"
    );
}
