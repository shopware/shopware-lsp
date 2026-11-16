//! Tests of the Administration API route analyzer.

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_symfony::RouteIndexer;

use super::*;
use crate::diagnostics::test_support::check_problems;

const CONTROLLER: &str = r"<?php
namespace Swag\Controller;

use Symfony\Component\Routing\Attribute\Route;

#[Route(path: '/api/_action/swag', defaults: ['_routeScope' => ['api']])]
class SwagController
{
    #[Route(path: '/run', name: 'api.action.swag.run', methods: ['POST'])]
    public function run(): void {}

    #[Route(path: '/vehicle/{id}/status', name: 'api.action.swag.status', methods: ['GET', 'PATCH'])]
    public function status(): void {}

    #[Route(path: '/any', name: 'api.action.swag.any')]
    public function any(): void {}
}

class OtherController
{
    #[Route(path: '/api/_info/swag', name: 'api.info.swag', methods: ['GET'])]
    public function info(): void {}

    #[Route(path: '/api/_action/proxy/{path}', name: 'api.action.proxy', requirements: ['path' => '.+'], methods: ['GET'])]
    public function proxy(): void {}
}
";

fn analyzer(routes: bool) -> (tempfile::TempDir, AdminApiRouteAnalyzer) {
    let cache = tempfile::tempdir().unwrap();
    let index = RouteIndexer::new(cache.path(), None).unwrap();
    if routes {
        index
            .index(&ParsedFile::new(
                "/project/src/Controller/SwagController.php",
                CONTROLLER.as_bytes().to_vec(),
            ))
            .unwrap();
    }
    (cache, AdminApiRouteAnalyzer::new(Some(Arc::new(index))))
}

const URI: &str =
    "file:///project/src/Resources/app/administration/src/service/swag.api.service.js";

const SERVICE: &str = r"class SwagApiService extends ApiService {
    constructor(httpClient, loginService, apiEndpoint = '_action/swag') {
        super(httpClient, loginService, apiEndpoint);
    }

    run() {
        return this.httpClient.post('/_action/swag/run', {}, { headers: this.getBasicHeaders() });
    }

    viaBasePath(id) {
        const apiRoute = `${this.getApiBasePath()}/vehicle/${id}/status`;
        return this.httpClient
            .get(apiRoute, { headers: this.getBasicHeaders() })
            .then((response) => ApiService.handleResponse(response));
    }

    typo() {
        const apiRoute = `${this.getApiBasePath()}/vehicle/statuss`;
        return this.httpClient.get({|admin.api.route-not-found:apiRoute|});
    }

    wrongMethod(id) {
        this.httpClient.post({|admin.api.route-method:`/_action/swag/vehicle/${id}/status`|});
        this.httpClient.delete({|admin.api.route-method:'_action/swag/run'|});
    }

    fine(id, name) {
        this.httpClient.get('/_action/swag/any');
        this.httpClient.delete('/_action/swag/any?force=1');
        this.httpClient.get('/api/_info/swag');
        this.httpClient.get(`/_action/proxy/${name}/more/parts`);
        this.httpClient.get(`/_action/${name}/run`);
        this.httpClient.post(`/${this.apiEndpoint}/run`);
        // Entity endpoints are generated.
        this.httpClient.get('/product');
        this.httpClient.post(`/search/${name}`, {});
        // Paths that are not known here.
        this.httpClient.get(name);
        this.httpClient.get(`${name}/run`);
        this.httpClient.get('/_action/' + name);
        this.httpClient.get(`${this.getApiBasePath(id)}/unknown`);
        this.httpClient.get('https://example.com/_action/unknown');
        other.get('/_action/unknown');
    }

    unknown() {
        this.httpClient.get({|admin.api.route-not-found:'/_action/swag/unknown'|});
        this.httpClient.get({|admin.api.route-not-found:'/_info/unknown'|});
        client.httpClient.get({|admin.api.route-not-found:`/_action/swag/${id}/unknown`|});
        this.httpClient.get({|admin.api.route-not-found:`/${this.apiEndpoint}/gone`|});
    }
}
";

#[test]
fn admin_requests_are_matched_against_the_declared_routes() {
    let (_cache, analyzer) = analyzer(true);
    let (_, problems) = check_problems(&analyzer, URI, SERVICE);
    let messages: Vec<&str> = problems
        .iter()
        .map(|problem| problem.message.as_str())
        .collect();
    assert_eq!(
        messages,
        [
            "No Admin API route answers GET /api/_action/swag/vehicle/statuss",
            "The route /api/_action/swag/vehicle/{…}/status answers GET, PATCH, not POST",
            "The route /api/_action/swag/run answers POST, not DELETE",
            "No Admin API route answers GET /api/_action/swag/unknown",
            "No Admin API route answers GET /api/_info/unknown",
            "No Admin API route answers GET /api/_action/swag/{…}/unknown",
            "No Admin API route answers GET /api/_action/swag/gone",
        ]
    );
}

#[test]
fn nothing_is_said_without_routes_or_outside_of_administration_code() {
    let plain = SERVICE
        .replace("{|admin.api.route-not-found:", "")
        .replace("{|admin.api.route-method:", "")
        .replace("|}", "");
    // The PHP side is not indexed: every path would be unknown.
    let (_cache, empty) = analyzer(false);
    check_problems(&empty, URI, &plain);
    let (_cache, analyzer) = analyzer(true);
    for uri in [
        "file:///project/src/Resources/app/storefront/src/service/swag.api.service.js",
        "file:///project/src/Resources/app/administration/src/service/swag.api.service.spec.js",
    ] {
        check_problems(&analyzer, uri, &plain);
    }
    check_problems(&AdminApiRouteAnalyzer::new(None), URI, &plain);
}
