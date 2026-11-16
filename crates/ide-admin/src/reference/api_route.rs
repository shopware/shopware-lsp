//! Find-references from a controller route to the Administration requests
//! it answers.
//!
//! With the cursor in the `#[Route]` attribute of an Admin API action, the
//! references are the `httpClient` calls that request its path with one of
//! its methods. They come from the request index, so no file is read.

use std::sync::Arc;

use swlsp_admin::{ApiRequestIndex, is_api_action_path};
use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_ATTRIBUTE;
use swlsp_symfony::RouteIndexer;
use swlsp_uriutil::{file_uri, filepath};

pub struct AdminApiRouteReferenceProvider {
    routes: Arc<RouteIndexer>,
    requests: Arc<ApiRequestIndex>,
}

impl AdminApiRouteReferenceProvider {
    pub fn new(routes: Arc<RouteIndexer>, requests: Arc<ApiRequestIndex>) -> Self {
        Self { routes, requests }
    }
}

impl ReferencesProvider for AdminApiRouteReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let uri = &request.params.text_document.uri;
        if filepath::ext(uri) != ".php" {
            return Ok(Vec::new());
        }
        let (Some(node), Some(lines)) = (&request.node, request.line_index()) else {
            return Ok(Vec::new());
        };
        // The attribute the cursor is in; a route of the index is known by
        // the line its attribute starts on.
        let Some(attribute) = node
            .ancestors()
            .find(|ancestor| ancestor.kind() == PHP_ATTRIBUTE)
            .filter(|attribute| {
                phpquery::attribute_name(attribute)
                    .rsplit('\\')
                    .next()
                    .is_some_and(|name| name == "Route")
            })
        else {
            return Ok(Vec::new());
        };
        let range = swlsp_syntax::SyntaxNodeExt::range_trimmed_trivia(&attribute);
        let (first, _) = lines.position_utf16(range.start().into());
        let (last, _) = lines.position_utf16(range.end().into());
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Ok(Vec::new());
        };
        let routes = self.routes.get_routes_view()?;
        let mut locations: Vec<protocol::Location> = Vec::new();
        for route in routes.iter().filter(|route| {
            is_api_action_path(&route.path)
                && filepath::clean(&route.file_path) == filepath::clean(&path)
                && (first + 1..=last + 1).contains(&route.line)
        }) {
            for usage in self.requests.requests_to(&route.path, &route.methods)? {
                let location = protocol::Location {
                    uri: file_uri(&usage.file),
                    range: protocol::Range {
                        start: protocol::Position {
                            line: usage.start_line,
                            character: usage.start_character,
                        },
                        end: protocol::Position {
                            line: usage.end_line,
                            character: usage.end_character,
                        },
                    },
                };
                if !locations.contains(&location) {
                    locations.push(location);
                }
            }
        }
        Ok(locations)
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use swlsp_admin::ApiRequestIndex;
    use swlsp_indexer::{Indexer, ParsedFile};
    use swlsp_lsp::{Context, ReferenceRequest, ReferencesProvider, SyntaxContext, TextDocument};
    use swlsp_lsp_protocol as protocol;
    use swlsp_symfony::RouteIndexer;
    use swlsp_syntax::{SyntaxNodeExt, TextSize};

    use super::AdminApiRouteReferenceProvider;

    const CONTROLLER: &str = "<?php\nnamespace Swag\\Controller;\n\nuse Symfony\\Component\\Routing\\Attribute\\Route;\n\n#[Route(path: '/api/_action/swag', defaults: ['_routeScope' => ['api']])]\nclass SwagController\n{\n    #[Route(path: '/vehicle/{id}/status', name: 'api.action.swag.status.read', methods: ['GET'])]\n    public function read(): void {}\n\n    #[Route(\n        path: '/vehicle/{id}/status',\n        name: 'api.action.swag.status.write',\n        methods: ['PATCH'],\n    )]\n    public function write(): void {}\n\n    #[Route(path: '/unused', name: 'api.action.swag.unused', methods: ['GET'])]\n    public function unused(): void {}\n}\n";

    const SERVICE: &str = "class SwagApiService extends ApiService {\n    constructor(httpClient, loginService, apiEndpoint = '_action/swag') {\n        super(httpClient, loginService, apiEndpoint);\n    }\n\n    write(id) {\n        const apiRoute = `${this.getApiBasePath()}/vehicle/${id}/status`;\n        return this.httpClient.patch(apiRoute, {});\n    }\n\n    read(id) {\n        return this.httpClient.get(`/_action/swag/vehicle/${id}/status`);\n    }\n}\n";

    const CONTROLLER_PATH: &str = "/project/src/Controller/SwagController.php";
    const SERVICE_PATH: &str =
        "/project/src/Resources/app/administration/src/service/swag.api.service.js";

    fn references(provider: &AdminApiRouteReferenceProvider, needle: &str) -> Vec<(String, u32)> {
        let document = Arc::new(TextDocument::new(
            swlsp_uriutil::file_uri(CONTROLLER_PATH),
            CONTROLLER,
            1,
        ));
        let root = document.root().unwrap();
        let offset = CONTROLLER.find(needle).expect(needle) as u32 + 1;
        let (line, character) = document.line_index().position_utf16(offset);
        let mut params = protocol::ReferenceParams::default();
        params.text_document.uri = document.uri.clone();
        params.position = protocol::Position { line, character };
        let request = ReferenceRequest {
            params,
            syntax: SyntaxContext {
                language: document.language,
                node: root.node_at(TextSize::from(offset)),
                token: root.token_at(TextSize::from(offset)),
                root: Some(root),
                document: Some(document),
            },
        };
        provider
            .get_references(&Context::background(), &request)
            .unwrap()
            .iter()
            .map(|location| {
                assert!(
                    location.uri.ends_with("/swag.api.service.js"),
                    "{}",
                    location.uri
                );
                let line = SERVICE
                    .lines()
                    .nth(location.range.start.line as usize)
                    .unwrap();
                let text: String = line
                    .chars()
                    .skip(location.range.start.character as usize)
                    .take((location.range.end.character - location.range.start.character) as usize)
                    .collect();
                (text, location.range.start.line + 1)
            })
            .collect()
    }

    #[test]
    fn a_route_lists_the_administration_requests_it_answers() {
        let cache = tempfile::tempdir().unwrap();
        let routes = RouteIndexer::new(cache.path().join("routes"), None).unwrap();
        routes
            .index(&ParsedFile::new(
                CONTROLLER_PATH,
                CONTROLLER.as_bytes().to_vec(),
            ))
            .unwrap();
        let requests = ApiRequestIndex::new(cache.path().join("requests"), None).unwrap();
        let service = ParsedFile::new(SERVICE_PATH, SERVICE.as_bytes().to_vec());
        requests.index(&service).unwrap();
        let (routes, requests) = (Arc::new(routes), Arc::new(requests));
        let provider = AdminApiRouteReferenceProvider::new(routes, requests.clone());

        // Each action lists the requests with its method.
        assert_eq!(
            references(&provider, "'api.action.swag.status.read'"),
            [("`/_action/swag/vehicle/${id}/status`".to_owned(), 12)]
        );
        // Anywhere in the attribute, also one over several lines.
        assert_eq!(
            references(&provider, "methods: ['PATCH']"),
            [("apiRoute".to_owned(), 8)]
        );
        for needle in [
            "'api.action.swag.unused'",
            "public function read",
            "path: '/api/_action/swag'",
        ] {
            assert_eq!(references(&provider, needle), Vec::new(), "{needle}");
        }

        // A file that no longer requests anything drops out of the index.
        requests
            .index(&ParsedFile::new(
                SERVICE_PATH,
                b"export default {};\n".to_vec(),
            ))
            .unwrap();
        assert_eq!(
            references(&provider, "'api.action.swag.status.read'"),
            Vec::new()
        );
        requests.index(&service).unwrap();
        requests.removed_files(&[SERVICE_PATH.to_owned()]).unwrap();
        assert_eq!(references(&provider, "methods: ['PATCH']"), Vec::new());
    }
}
