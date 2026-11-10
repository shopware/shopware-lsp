//! App script inspection and its manifest permission fix.

use std::sync::Arc;
use swlsp_textutil::html_escape;

use crate::diagnostics::AppScriptAnalyzer;
use serde::{Deserialize, Serialize};
use swlsp_appscript::Index as AppScriptIndex;
use swlsp_extension::ExtensionIndexer;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, DiagnosticId, FixContext, FixId, FixPresentation, FixResolution, Inspection,
    InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix, RewriteQuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_xml::query as xmlquery;
use swlsp_rewrite::{Builder, DocumentPlan, WorkspacePlan};
use swlsp_syntax::SyntaxNodeExt;
use swlsp_uriutil::file_uri;

use crate::inspections::{BoundInspection, resolve_anchor};

pub const ADD_APP_READ_PERMISSION_FIX_ID: &str = "add-app-read-permission";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub(crate) struct AppPermissionPayload {
    #[serde(default)]
    pub entity: String,
    #[serde(default)]
    pub manifest: String,
}

pub fn new_app_script(
    index: Option<Arc<AppScriptIndex>>,
    extensions: Option<Arc<ExtensionIndexer>>,
) -> Arc<dyn Inspection> {
    let problem = |id: &str| ProblemDefinition {
        id: DiagnosticId::from(id),
        source: "shopware-lsp".to_owned(),
        default_severity: protocol::DiagnosticSeverity::ERROR,
        disabled_by_default: false,
    };
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.app_script".to_owned(),
            description: "App scripts using a service the hook does not provide, or one the app lacks the permission for.",
            domain: "shopware.appScripts",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Twig],
            problems: vec![
                problem("app_script.permission-missing"),
                problem("app_script.service-unavailable"),
            ],
        },
        analyzers: vec![Arc::new(AppScriptAnalyzer::new(index, extensions))],
        fixes: vec![Arc::new(AppReadPermissionFix)],
        bind: Some(Arc::new(|code, payload| {
            if code.as_str() != "app_script.permission-missing" {
                return Vec::new();
            }
            let string = |key: &str| {
                payload
                    .get(key)
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    .to_owned()
            };
            let value = AppPermissionPayload {
                entity: string("entity"),
                manifest: string("manifest"),
            };
            if value.entity.is_empty() || value.manifest.is_empty() {
                return Vec::new();
            }
            vec![bind_fix(ADD_APP_READ_PERMISSION_FIX_ID, value)]
        })),
    })
}

pub(crate) struct AppReadPermissionFix;

impl QuickFix for AppReadPermissionFix {
    fn id(&self) -> FixId {
        FixId::from(ADD_APP_READ_PERMISSION_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: AppPermissionPayload = fix.fix_payload()?;
        if payload.entity.is_empty() || payload.manifest.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: format!("Add read permission for '{}'", payload.entity),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for AppReadPermissionFix {
    fn build(&self, ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: AppPermissionPayload = fix.fix_payload()?;
        resolve_anchor(fix)?;
        let manifest_uri = file_uri(&payload.manifest);
        let target = fix.documents.resolve_document(ctx, &manifest_uri)?;
        let document = &target.document;
        let root = match (document.language, document.root()) {
            (Some(LanguageId::Xml), Some(root)) => root,
            _ => return Err("app manifest has no XML syntax tree".into()),
        };
        let Some(manifest) = xmlquery::elements(&root, &["manifest"]).into_iter().next() else {
            return Err("app manifest root is missing".into());
        };
        let permissions = xmlquery::child_element(&manifest, &["permissions"]);
        if let Some(permissions) = &permissions {
            for read in xmlquery::child_elements(permissions, &["read"]) {
                if xmlquery::text_content(&read).trim() == payload.entity {
                    return Err(
                        format!("read permission for {:?} already exists", payload.entity).into(),
                    );
                }
            }
        }
        let entity = html_escape(&payload.entity);
        let source = document.source();
        let (offset, insertion) = match &permissions {
            Some(permissions) => {
                let range = permissions.range_trimmed_trivia();
                xml_child_insertion(
                    source,
                    range.start().into(),
                    range.end().into(),
                    "permissions",
                    &format!("<read>{entity}</read>"),
                )?
            }
            None => {
                let range = manifest.range_trimmed_trivia();
                xml_child_insertion(
                    source,
                    range.start().into(),
                    range.end().into(),
                    "manifest",
                    &format!("<permissions>\n        <read>{entity}</read>\n    </permissions>"),
                )?
            }
        };
        let mut builder = Builder::new(source);
        builder.insert(offset, insertion)?;
        let edits = builder.finish()?;
        Ok(WorkspacePlan {
            documents: vec![DocumentPlan::new(
                manifest_uri,
                target.version,
                document.source_arc().clone(),
                edits,
            )],
            ..WorkspacePlan::default()
        })
    }
}

/// The offset and text inserting `child` before the closing tag of `parent`,
/// indented one level below it.
fn xml_child_insertion(
    source: &str,
    start: u32,
    end: u32,
    parent: &str,
    child: &str,
) -> Result<(u32, String), BoxError> {
    if start > end || end as usize > source.len() {
        return Err(format!("{parent} element range changed").into());
    }
    let fragment = &source.as_bytes()[start as usize..end as usize];
    let needle = format!("</{parent}");
    let Some(relative) = fragment
        .windows(needle.len())
        .rposition(|window| window == needle.as_bytes())
    else {
        return Err(format!("{parent} closing tag is missing").into());
    };
    let closing = start as usize + relative;
    let bytes = source.as_bytes();
    let mut line_start = closing;
    while line_start > 0 && bytes[line_start - 1] != b'\n' {
        line_start -= 1;
    }
    let indent = &source[line_start..closing];
    if indent.trim().is_empty() {
        return Ok((line_start as u32, format!("{indent}    {child}\n")));
    }
    Ok((closing as u32, format!("\n    {child}\n{indent}")))
}
