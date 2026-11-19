//! "Generate Administration API service" on a controller class, and "Add
//! to Administration API service" on its actions.
//!
//! The actions run [`GENERATE_API_SERVICE_COMMAND`], which answers with the
//! edit. Without a service it is the service file in the Administration of
//! the bundle that owns the controller, imported from its `main.ts` (or
//! `main.js`). With one, the actions the service does not request yet are
//! added to its class as methods; what the file holds is never replaced.

use std::collections::BTreeMap;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_admin::{ApiRequest, ApiRequestIndex, api_request, api_route_is_requested};
use swlsp_extension::ExtensionIndexer;
use swlsp_ide::codeaction::command_action;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    ActionProvider, BoxError, CodeActionRequest, CommandFunc, CommandProvider, Context,
    DocumentSnapshot, TextDocument, WorkspaceEditHost,
};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{
    JS_BLOCK, JS_FUNCTION, JS_METHOD, TK_CLOSE_BRACE, TK_IDENTIFIER, TK_KEYWORD, TK_LINE_BREAK,
    TK_WHITESPACE,
};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_BLOCK, PHP_CLASS_BODY};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::Snapshot;
use swlsp_rewrite::{Builder, CreateFilePlan, DocumentPlan, WorkspacePlan};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_uriutil::file_uri;

use super::generate::{Flavour, ServiceNames, Types, methods_source, service_source};
use crate::diagnostics::{ApiAction, ApiController, api_controller};
use crate::scaffold::paths::{self, Stat};

/// The client command of the code actions: `(uri, className, method)`.
pub const GENERATE_API_SERVICE_ACTION: &str = "shopware.generateAdminApiService";
/// The server command that builds the edit.
pub const GENERATE_API_SERVICE_COMMAND: &str = "shopware/admin/api-service/generate";

/// Where an extension keeps its API services, most specific first.
const SERVICE_DIRECTORIES: &[&[&str]] = &[
    &["core", "service", "api"],
    &["service", "api"],
    &["api"],
    &["service"],
    &["services"],
];
const ADMINISTRATION_BUNDLE: &str = "Shopware\\Administration\\Administration";

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ApiServiceRequest {
    /// The controller file.
    pub uri: String,
    /// The controller class; the first API controller of the file when
    /// empty.
    #[serde(rename = "className")]
    pub class_name: String,
    /// The PHP method of the one action to add to an existing service;
    /// every missing action when empty.
    pub method: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ApiServiceResponse {
    pub edit: Option<protocol::WorkspaceEdit>,
    #[serde(rename = "primaryFileUri")]
    pub primary_file_uri: String,
    /// Whether the service file is new.
    pub created: bool,
    /// The service methods the edit adds.
    pub methods: Vec<String>,
}

/// Where the service of a controller is, or would be.
struct ServiceTarget {
    source_directory: String,
    flavour: Flavour,
    /// The file a new service gets.
    path: String,
    /// The service the controller already has.
    existing: Option<DocumentSnapshot>,
}

impl ServiceTarget {
    /// The actions the existing service does not request.
    fn missing<'c>(&self, controller: &'c ApiController) -> Vec<&'c ApiAction> {
        let requests: Vec<ApiRequest> = self
            .existing
            .as_ref()
            .and_then(|service| service.document.root())
            .map(|root| {
                jsquery::iter_calls(&root, &[])
                    .filter_map(|call| api_request(&call))
                    .collect()
            })
            .unwrap_or_default();
        controller
            .actions
            .iter()
            .filter(|action| {
                !requests.iter().any(|request| {
                    request.verb.eq_ignore_ascii_case(&action.verb)
                        && api_route_is_requested(&action.path, &request.path)
                })
            })
            .collect()
    }

    fn file_name(&self) -> String {
        let path = self
            .existing
            .as_ref()
            .and_then(|service| swlsp_uriutil::path(&service.document.uri).ok())
            .unwrap_or_else(|| self.path.clone());
        path.rsplit('/').next().unwrap_or(&path).to_owned()
    }
}

#[derive(Clone)]
pub struct ApiServiceProvider {
    php_index: Option<PhpIndex>,
    extensions: Arc<ExtensionIndexer>,
    requests: Option<Arc<ApiRequestIndex>>,
    host: Option<Arc<dyn WorkspaceEditHost>>,
}

impl ApiServiceProvider {
    pub fn new(
        php_index: Option<PhpIndex>,
        extensions: Arc<ExtensionIndexer>,
        requests: Option<Arc<ApiRequestIndex>>,
        host: Option<Arc<dyn WorkspaceEditHost>>,
    ) -> Self {
        Self {
            php_index,
            extensions,
            requests,
            host,
        }
    }

    /// The Admin API actions of `class`, from the shared analysis of
    /// `document`.
    fn controller(
        &self,
        document: &TextDocument,
        class: &SyntaxNode,
    ) -> Option<(ApiController, Arc<swlsp_lsp_phpanalysis::State>)> {
        let state = swlsp_lsp_phpanalysis::for_document(self.php_index.as_ref()?, document)
            .ok()
            .flatten()?;
        let controller = api_controller(&state.document, &state.snapshot, class)?;
        Some((controller, state))
    }

    /// The Administration source directory the service belongs to: the one
    /// of the bundle that owns the controller, or Shopware's own for a
    /// controller of Shopware.
    fn administration(
        &self,
        path: &str,
        namespace: &str,
        snapshot: &Snapshot,
    ) -> Option<(String, Flavour)> {
        let extensions = self.extensions.get_all().ok()?;
        let owner = extensions
            .iter()
            .filter(|extension| paths::rel_within(&extension.root_path(), path).is_some())
            .max_by_key(|extension| extension.root_path().len());
        if let Some(owner) = owner {
            return Some((owner.administration_source_path(), Flavour::Extension));
        }
        // An installed Shopware is not the place for new files.
        if !namespace.starts_with("Shopware\\") || path.contains("/vendor/") {
            return None;
        }
        let bundle = snapshot.classes(ADMINISTRATION_BUNDLE).into_iter().next()?;
        let source = paths::join(&[
            &paths::dir(&bundle.path),
            "Resources",
            "app",
            "administration",
            "src",
        ]);
        Some((source, Flavour::Core))
    }

    /// Where the service of `controller` is or would be; `None` when the
    /// controller belongs to no bundle with an Administration.
    fn target(
        &self,
        ctx: &Context,
        document: &TextDocument,
        controller: &ApiController,
        state: &swlsp_lsp_phpanalysis::State,
    ) -> Result<Option<ServiceTarget>, BoxError> {
        let host = self
            .host
            .as_deref()
            .ok_or("workspace edit host is unavailable")?;
        let path = swlsp_uriutil::path(&document.uri)?;
        let Some((source_directory, flavour)) =
            self.administration(&path, &state.document.namespace, &state.snapshot)
        else {
            return Ok(None);
        };
        let names = ServiceNames::of(controller);
        let directory = match flavour {
            Flavour::Core => paths::join(&[&source_directory, "core", "service", "api"]),
            Flavour::Extension => SERVICE_DIRECTORIES
                .iter()
                .map(|parts| {
                    let mut all = vec![source_directory.as_str()];
                    all.extend_from_slice(parts);
                    paths::join(&all)
                })
                .find(|candidate| matches!(paths::stat(candidate), Stat::Exists(_)))
                .unwrap_or_else(|| paths::join(&[&source_directory, "service"])),
        };
        let path = paths::join(&[&directory, &names.file_name()]);
        let existing = if matches!(paths::stat(&path), Stat::Missing) {
            self.requesting_service(controller, &source_directory)?
        } else {
            Some(path.clone())
        };
        let existing = match existing {
            Some(existing) => {
                swlsp_lsp::resolve_optional_document(ctx, host, &file_uri(&existing))?
            }
            None => None,
        };
        Ok(Some(ServiceTarget {
            source_directory,
            flavour,
            path,
            existing,
        }))
    }

    /// The TypeScript file of the Administration in `source_directory` that
    /// requests most of the actions of `controller`.
    fn requesting_service(
        &self,
        controller: &ApiController,
        source_directory: &str,
    ) -> Result<Option<String>, BoxError> {
        let Some(index) = &self.requests else {
            return Ok(None);
        };
        let mut files: BTreeMap<String, usize> = BTreeMap::new();
        for action in &controller.actions {
            let mut requesting: Vec<String> = index
                .requests_to(&action.path, &[action.verb.to_uppercase()])?
                .into_iter()
                .map(|request| request.file)
                .filter(|file| {
                    file.ends_with(".ts") && paths::rel_within(source_directory, file).is_some()
                })
                .collect();
            requesting.dedup();
            for file in requesting {
                *files.entry(file).or_default() += 1;
            }
        }
        // The first of the files that request the most actions.
        let most = files.values().copied().max();
        Ok(files
            .into_iter()
            .find(|(_, count)| Some(*count) == most)
            .map(|(file, _)| file))
    }

    pub fn generate(
        &self,
        ctx: &Context,
        args: Option<&Value>,
    ) -> Result<ApiServiceResponse, BoxError> {
        ctx.check()?;
        let request: ApiServiceRequest = crate::scaffold::decode_args(
            args,
            "missing API service request",
            "invalid API service request",
        )?;
        let host = self
            .host
            .as_deref()
            .ok_or("workspace edit host is unavailable")?;
        let snapshot = host.resolve_document(ctx, &request.uri)?;
        let document = &snapshot.document;
        let root = document.root().ok_or("the controller has no syntax tree")?;
        // The class may be named with its namespace.
        let wanted = request.class_name.rsplit('\\').next().unwrap_or_default();
        let (controller, state) = phpquery::classes(&root)
            .into_iter()
            .filter(|class| wanted.is_empty() || phpquery::class_name(class) == wanted)
            .find_map(|class| self.controller(document, &class))
            .ok_or("no controller with Admin API actions was found")?;
        let target = self
            .target(ctx, document, &controller, &state)?
            .ok_or("the controller belongs to no bundle with an Administration")?;
        let mut plan = WorkspacePlan::default();
        let Some(service) = &target.existing else {
            plan.creates.push(CreateFilePlan {
                uri: file_uri(&target.path),
                content: service_source(&controller, target.flavour),
            });
            // The Administration loads its own services by directory.
            if target.flavour == Flavour::Extension {
                import_service(ctx, host, &mut plan, &target.source_directory, &target.path)?;
            }
            let mut types = Types::default();
            let methods = methods_source(
                &controller.actions,
                target.flavour,
                &mut types,
                &mut Vec::new(),
            );
            return Ok(ApiServiceResponse {
                edit: Some(host.workspace_edit(ctx, &plan)?),
                primary_file_uri: file_uri(&target.path),
                created: true,
                methods: methods.names,
            });
        };
        let mut missing = target.missing(&controller);
        if !request.method.is_empty() {
            if !controller
                .actions
                .iter()
                .any(|action| action.method == request.method)
            {
                return Err(format!(
                    "{} has no Admin API action {}",
                    controller.class_name, request.method
                )
                .into());
            }
            missing.retain(|action| action.method == request.method);
        }
        let file = target.file_name();
        if missing.is_empty() {
            return Err(format!("{file} already requests every selected action").into());
        }
        let methods = add_methods(&mut plan, service, &missing, target.flavour)
            .map_err(|error| format!("{file}: {error}"))?;
        Ok(ApiServiceResponse {
            edit: Some(host.workspace_edit(ctx, &plan)?),
            primary_file_uri: service.document.uri.clone(),
            created: false,
            methods,
        })
    }
}

impl ActionProvider for ApiServiceProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let (Some(document), Some(node)) = (request.document.as_ref(), request.node.as_ref())
        else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Php) || !document.source().contains("Route") {
            return Vec::new();
        }
        let Some(class) = phpquery::class_at(node) else {
            return Vec::new();
        };
        // Offered on the class and on the head of an action, not on every
        // line of a body.
        let start = node.range_trimmed_trivia().start();
        let before = |parent: &SyntaxNode, kind| {
            phpquery::direct_child(parent, kind)
                .is_none_or(|body| start < body.range_trimmed_trivia().start())
        };
        let method = phpquery::method_at(node);
        let selected = match &method {
            Some(method) if before(method, PHP_BLOCK) => Some(phpquery::method_name(method)),
            Some(_) => return Vec::new(),
            None if before(&class, PHP_CLASS_BODY) => None,
            None => return Vec::new(),
        };
        let Some((controller, state)) = self.controller(document, &class) else {
            return Vec::new();
        };
        let Ok(Some(target)) = self.target(ctx, document, &controller, &state) else {
            return Vec::new();
        };
        let action = |title: String, method: &str| {
            command_action(
                &title,
                CodeActionKind::REFACTOR_REWRITE,
                "Generate Administration API service",
                GENERATE_API_SERVICE_ACTION,
                vec![
                    Value::from(document.uri.clone()),
                    Value::from(controller.class_name.clone()),
                    Value::from(method),
                ],
            )
        };
        if target.existing.is_none() {
            // A whole service is generated from the class.
            if selected.is_some() {
                return Vec::new();
            }
            return vec![action(
                format!(
                    "Shopware: Generate Administration API service {}",
                    ServiceNames::of(&controller).class
                ),
                "",
            )];
        }
        let missing = target.missing(&controller);
        let file = target.file_name();
        match selected {
            Some(method) if missing.iter().any(|action| action.method == method) => {
                vec![action(
                    format!("Shopware: Add {method} to Administration API service {file}"),
                    &method,
                )]
            }
            None if !missing.is_empty() => {
                let actions = if missing.len() == 1 {
                    "1 missing action".to_owned()
                } else {
                    format!("{} missing actions", missing.len())
                };
                vec![action(
                    format!("Shopware: Add {actions} to Administration API service {file}"),
                    "",
                )]
            }
            _ => Vec::new(),
        }
    }
}

impl CommandProvider for ApiServiceProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let generate: CommandFunc =
            Arc::new(move |ctx, args| Ok(serde_json::to_value(provider.generate(ctx, args)?)?));
        BTreeMap::from([(GENERATE_API_SERVICE_COMMAND.to_owned(), generate)])
    }
}

/// Imports the service from the entry file of the extension, creating
/// `main.ts` when there is none.
fn import_service(
    ctx: &Context,
    host: &dyn WorkspaceEditHost,
    plan: &mut WorkspacePlan,
    source_directory: &str,
    target: &str,
) -> Result<(), BoxError> {
    let relative = paths::rel(source_directory, target)?;
    let relative = paths::to_slash(&relative);
    let import_path = format!("./{}", relative.strip_suffix(".ts").unwrap_or(&relative));
    let statement = format!("import '{import_path}';\n");
    for name in ["main.ts", "main.js"] {
        let uri = file_uri(&paths::join(&[source_directory, name]));
        let Some(main) = swlsp_lsp::resolve_optional_document(ctx, host, &uri)? else {
            continue;
        };
        let source = main.document.source();
        if source.contains(&format!("'{import_path}'"))
            || source.contains(&format!("\"{import_path}\""))
        {
            return Ok(());
        }
        let prefix = if source.is_empty() || source.ends_with('\n') {
            ""
        } else {
            "\n"
        };
        let mut builder = Builder::new(source);
        builder.insert(source.len() as u32, format!("{prefix}{statement}"))?;
        plan.documents.push(DocumentPlan::new(
            uri,
            main.version,
            main.document.source_arc().clone(),
            builder.finish()?,
        ));
        return Ok(());
    }
    plan.creates.push(CreateFilePlan {
        uri: file_uri(&paths::join(&[source_directory, "main.ts"])),
        content: statement,
    });
    Ok(())
}

/// Whether `node` is a class.
fn is_class(node: &SyntaxNode) -> bool {
    node.kind() == JS_FUNCTION
        && node
            .children_with_tokens()
            .filter_map(|element| element.into_token())
            .any(|token| token.kind() == TK_KEYWORD && token.text() == "class")
}

/// Adds one method per action to the class of `service` that sends the
/// requests, and the types they need before it. Answers with the names of
/// the methods.
fn add_methods(
    plan: &mut WorkspacePlan,
    service: &DocumentSnapshot,
    actions: &[&ApiAction],
    flavour: Flavour,
) -> Result<Vec<String>, BoxError> {
    let document = &service.document;
    let root = document.root().ok_or("the service has no syntax tree")?;
    let requests = |class: &SyntaxNode| {
        jsquery::iter_calls(class, &[])
            .filter(|call| api_request(call).is_some())
            .count()
    };
    let mut classes: Vec<SyntaxNode> = jsquery::nodes(&root, &[JS_FUNCTION])
        .into_iter()
        .filter(is_class)
        .collect();
    // The first class with the most requests.
    classes.sort_by_key(|class| std::cmp::Reverse(requests(class)));
    let class = classes.first().ok_or("the service declares no class")?;
    let body = class
        .children()
        .find(|child| child.kind() == JS_BLOCK)
        .ok_or("the service class has no body")?;
    let close = body
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .filter(|token| token.kind() == TK_CLOSE_BRACE)
        .last()
        .ok_or("the service class is not closed")?;
    let members: Vec<SyntaxNode> = body
        .children()
        .filter(|child| child.kind() == JS_METHOD)
        .collect();
    let mut used: Vec<String> = members.iter().filter_map(jsquery::property_name).collect();
    // An alias must not take a name the file gives something else.
    let taken: Vec<String> = root
        .descendants_with_tokens()
        .filter_map(|element| element.into_token())
        .filter(|token| token.kind() == TK_IDENTIFIER)
        .map(|token| token.text().to_owned())
        .collect();
    let mut types = Types::with_taken(taken);
    let methods = methods_source(actions.iter().copied(), flavour, &mut types, &mut used);
    if methods.names.is_empty() {
        return Err("no action can be named as a method".into());
    }

    let source = document.source();
    let mut builder = Builder::new(source);
    let aliases = types.source();
    if !aliases.is_empty() {
        // Before the statement of the class, and the comment above it.
        let statement = class
            .ancestors()
            .take_while(|ancestor| ancestor.parent().is_some())
            .last()
            .unwrap_or_else(|| class.clone());
        let start = statement
            .descendants_with_tokens()
            .filter_map(|element| element.into_token())
            .find(|token| !matches!(token.kind(), TK_WHITESPACE | TK_LINE_BREAK))
            .map_or(statement.text_range().start(), |token| {
                token.text_range().start()
            });
        builder.insert(start.into(), aliases)?;
    }
    let close_start: u32 = close.text_range().start().into();
    // The brace keeps its own line.
    let line_start = source[..close_start as usize]
        .rfind('\n')
        .map_or(0, |index| index + 1);
    let (offset, suffix) = if source[line_start..close_start as usize].trim().is_empty() {
        (line_start as u32, "")
    } else {
        (close_start, "\n")
    };
    let prefix = match (members.is_empty(), offset == close_start) {
        (true, false) => "",
        _ => "\n",
    };
    builder.insert(offset, format!("{prefix}{}{suffix}", methods.source))?;
    plan.documents.push(DocumentPlan::new(
        document.uri.clone(),
        service.version,
        document.source_arc().clone(),
        builder.finish()?,
    ));
    Ok(methods.names)
}
