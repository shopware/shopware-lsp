//! "Generate Administration API service" on a controller class.
//!
//! The action runs [`CREATE_API_SERVICE_COMMAND`], which answers with the
//! edit: the service file in the Administration of the bundle that owns the
//! controller, imported from its `main.ts` (or `main.js`). An existing
//! service file is never replaced.

use std::collections::BTreeMap;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_extension::ExtensionIndexer;
use swlsp_ide::codeaction::command_action;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    ActionProvider, BoxError, CodeActionRequest, CommandFunc, CommandProvider, Context,
    TextDocument, WorkspaceEditHost,
};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_CLASS_BODY;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::Snapshot;
use swlsp_rewrite::{Builder, CreateFilePlan, DocumentPlan, WorkspacePlan};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_uriutil::file_uri;

use super::generate::{Flavour, ServiceNames, service_source};
use crate::diagnostics::{ApiController, api_controller};
use crate::scaffold::paths::{self, Stat};

/// The client command of the code action: `(uri, className)`.
pub const GENERATE_API_SERVICE_ACTION: &str = "shopware.generateAdminApiService";
/// The server command that builds the edit.
pub const CREATE_API_SERVICE_COMMAND: &str = "shopware/admin/api-service/create";

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
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ApiServiceResponse {
    pub edit: Option<protocol::WorkspaceEdit>,
    #[serde(rename = "primaryFileUri")]
    pub primary_file_uri: String,
}

#[derive(Clone)]
pub struct ApiServiceProvider {
    php_index: Option<PhpIndex>,
    extensions: Arc<ExtensionIndexer>,
    host: Option<Arc<dyn WorkspaceEditHost>>,
}

impl ApiServiceProvider {
    pub fn new(
        php_index: Option<PhpIndex>,
        extensions: Arc<ExtensionIndexer>,
        host: Option<Arc<dyn WorkspaceEditHost>>,
    ) -> Self {
        Self {
            php_index,
            extensions,
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

    pub fn create(
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
        let path = swlsp_uriutil::path(&request.uri)?;
        let root = document.root().ok_or("the controller has no syntax tree")?;
        let (controller, state) = phpquery::classes(&root)
            .into_iter()
            .filter(|class| {
                request.class_name.is_empty() || phpquery::class_name(class) == request.class_name
            })
            .find_map(|class| self.controller(document, &class))
            .ok_or("no controller with Admin API actions was found")?;
        let (source_directory, flavour) = self
            .administration(&path, &state.document.namespace, &state.snapshot)
            .ok_or("the controller belongs to no bundle with an Administration")?;
        let names = ServiceNames::of(&controller);
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
        let target = paths::join(&[&directory, &names.file_name()]);
        if !matches!(paths::stat(&target), Stat::Missing) {
            return Err(format!("the API service already exists: {target}").into());
        }
        let mut plan = WorkspacePlan::default();
        plan.creates.push(CreateFilePlan {
            uri: file_uri(&target),
            content: service_source(&controller, flavour),
        });
        // The Administration loads its own services by directory.
        if flavour == Flavour::Extension {
            import_service(ctx, host, &mut plan, &source_directory, &target)?;
        }
        Ok(ApiServiceResponse {
            edit: Some(host.workspace_edit(ctx, &plan)?),
            primary_file_uri: file_uri(&target),
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
        // Offered on the class, not on every line of its body.
        let in_body = phpquery::direct_child(&class, PHP_CLASS_BODY).is_some_and(|body| {
            node.range_trimmed_trivia().start() >= body.range_trimmed_trivia().start()
        });
        if in_body {
            return Vec::new();
        }
        let Some((controller, _)) = self.controller(document, &class) else {
            return Vec::new();
        };
        let names = ServiceNames::of(&controller);
        vec![command_action(
            &format!(
                "Shopware: Generate Administration API service {}",
                names.class
            ),
            CodeActionKind::REFACTOR_REWRITE,
            "Generate Administration API service",
            GENERATE_API_SERVICE_ACTION,
            vec![
                Value::from(document.uri.clone()),
                Value::from(controller.class_name),
            ],
        )]
    }
}

impl CommandProvider for ApiServiceProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let create: CommandFunc =
            Arc::new(move |ctx, args| Ok(serde_json::to_value(provider.create(ctx, args)?)?));
        BTreeMap::from([(CREATE_API_SERVICE_COMMAND.to_owned(), create)])
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
