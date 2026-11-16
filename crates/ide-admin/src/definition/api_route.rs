//! Go-to-definition from an Administration request to the controller
//! action that answers it.
//!
//! The cursor may be on the path given to `httpClient`, or on the constant
//! that holds it:
//!
//! ```js
//! const apiRoute = `${this.getApiBasePath()}/vehicle/status`;
//! return this.httpClient.get(apiRoute);
//! ```

use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol::Location;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{JS_IDENTIFIER, JS_VARIABLE_DECLARATION};
use swlsp_symfony::RouteIndexer;
use swlsp_syntax::SyntaxNode;
use swlsp_uriutil::filepath;

use swlsp_admin::api_request;

use crate::diagnostics::{action_routes, answering_routes};
use swlsp_ide::definition::php_common::unclamped_line_location;

pub struct AdminApiRouteDefinitionProvider {
    routes: Arc<RouteIndexer>,
}

impl AdminApiRouteDefinitionProvider {
    pub fn new(routes: Arc<RouteIndexer>) -> Self {
        Self { routes }
    }
}

impl GotoDefinitionProvider for AdminApiRouteDefinitionProvider {
    fn get_definition(&self, _ctx: &Context, request: &DefinitionRequest) -> Vec<Location> {
        let uri = &request.params.text_document.uri;
        let extension = filepath::ext(uri).to_lowercase();
        if !uri.contains("Resources/app/administration")
            || !(extension == ".js" || extension == ".ts")
        {
            return Vec::new();
        }
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let Some(call) = request_call(node) else {
            return Vec::new();
        };
        let Some(api) = api_request(&call) else {
            return Vec::new();
        };
        let Ok(routes) = self.routes.get_routes_view() else {
            return Vec::new();
        };
        let actions = action_routes(&routes);
        let mut answering = answering_routes(&actions, &api.path);
        // The action for the method that is called comes first.
        answering.sort_by_key(|route| {
            let answers = route.methods.is_empty()
                || route
                    .methods
                    .iter()
                    .any(|method| method.eq_ignore_ascii_case(&api.verb));
            (!answers, route.file_path.clone(), route.line)
        });
        answering
            .iter()
            .filter(|route| !route.file_path.is_empty())
            .map(|route| unclamped_line_location(&route.file_path, route.line))
            .collect()
    }
}

/// The `httpClient` call a cursor position stands for: the call whose path
/// argument holds `node`, or the one that sends the constant `node` is
/// declared in.
fn request_call(node: &SyntaxNode) -> Option<SyntaxNode> {
    let within = |outer: &SyntaxNode| outer.text_range().contains_range(node.text_range());
    let direct = node.ancestors().find(|ancestor| {
        jsquery::iter_arguments(ancestor)
            .next()
            .is_some_and(|argument| within(&argument))
            && api_request(ancestor).is_some()
    });
    if direct.is_some() {
        return direct;
    }
    let declaration = node
        .ancestors()
        .find(|ancestor| ancestor.kind() == JS_VARIABLE_DECLARATION)?;
    let name = declaration
        .children()
        .next()
        .filter(|binding| binding.kind() == JS_IDENTIFIER)?
        .text()
        .to_string();
    let name = name.trim();
    let scope = declaration.parent()?;
    jsquery::iter_calls(&scope, &[]).find(|call| {
        api_request(call).is_some_and(|api| api.argument.text().to_string().trim() == name)
    })
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use swlsp_indexer::{Indexer, ParsedFile};
    use swlsp_lsp::{
        Context, DefinitionRequest, GotoDefinitionProvider, SyntaxContext, TextDocument,
    };
    use swlsp_lsp_protocol::{self as protocol, Position};
    use swlsp_symfony::RouteIndexer;
    use swlsp_syntax::{SyntaxNodeExt, TextSize};

    use super::AdminApiRouteDefinitionProvider;

    const CONTROLLER: &str = "<?php\nnamespace Swag\\Controller;\n\nuse Symfony\\Component\\Routing\\Attribute\\Route;\n\n#[Route(path: '/api/_action/swag', defaults: ['_routeScope' => ['api']])]\nclass SwagController\n{\n    #[Route(path: '/vehicle/{id}/status', name: 'api.action.swag.status.read', methods: ['GET'])]\n    public function read(): void {}\n\n    #[Route(path: '/vehicle/{id}/status', name: 'api.action.swag.status.write', methods: ['PATCH'])]\n    public function write(): void {}\n}\n";

    const SERVICE: &str = "class SwagApiService extends ApiService {\n    constructor(httpClient, loginService, apiEndpoint = '_action/swag') {\n        super(httpClient, loginService, apiEndpoint);\n    }\n\n    write(id) {\n        const apiRoute = `${this.getApiBasePath()}/vehicle/${id}/status`;\n        return this.httpClient.patch(apiRoute, {});\n    }\n\n    read(id) {\n        return this.httpClient.get(`/_action/swag/vehicle/${id}/status`);\n    }\n\n    other() {\n        this.httpClient.get('/_action/swag/unknown', { path: '/x' });\n        this.httpClient.get('/product');\n        const label = 'vehicle';\n    }\n}\n";

    fn lines_at(provider: &AdminApiRouteDefinitionProvider, uri: &str, needle: &str) -> Vec<u32> {
        let document = Arc::new(TextDocument::new(uri, SERVICE, 1));
        let root = document.root().unwrap();
        let offset = SERVICE.find(needle).expect(needle) as u32 + 2;
        let (line, character) = document.line_index().position_utf16(offset);
        let mut params = protocol::DefinitionParams::default();
        params.text_document.uri = document.uri.clone();
        params.position = Position { line, character };
        let request = DefinitionRequest {
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
            .get_definition(&Context::background(), &request)
            .iter()
            .map(|location| {
                assert!(
                    location.uri.ends_with("/SwagController.php"),
                    "{}",
                    location.uri
                );
                location.range.start.line + 1
            })
            .collect()
    }

    #[test]
    fn a_request_path_leads_to_the_action_that_answers_it() {
        let cache = tempfile::tempdir().unwrap();
        let index = RouteIndexer::new(cache.path(), None).unwrap();
        index
            .index(&ParsedFile::new(
                "/project/src/Controller/SwagController.php",
                CONTROLLER.as_bytes().to_vec(),
            ))
            .unwrap();
        let provider = AdminApiRouteDefinitionProvider::new(Arc::new(index));
        let uri =
            "file:///project/src/Resources/app/administration/src/service/swag.api.service.js";
        // The action for the method that is called comes first.
        assert_eq!(lines_at(&provider, uri, "`/_action/swag/vehicle"), [9, 12]);
        assert_eq!(lines_at(&provider, uri, "apiRoute, {}"), [12, 9]);
        // From the constant that holds the path as well.
        assert_eq!(
            lines_at(&provider, uri, "`${this.getApiBasePath()}"),
            [12, 9]
        );
        // No route, an entity endpoint, another argument, another constant.
        for needle in [
            "'/_action/swag/unknown'",
            "'/product'",
            "path: '/x'",
            "'vehicle';",
        ] {
            assert_eq!(
                lines_at(&provider, uri, needle),
                Vec::<u32>::new(),
                "{needle}"
            );
        }
        assert!(
            lines_at(
                &provider,
                "file:///project/src/Resources/app/storefront/src/swag.js",
                "`/_action/swag/vehicle"
            )
            .is_empty()
        );
    }
}
