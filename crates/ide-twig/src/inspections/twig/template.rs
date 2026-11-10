//! Missing template inspection with suggestion and create-file quick fixes.

use std::io::ErrorKind;
use std::path::{Path, PathBuf};
use std::sync::Arc;

use crate::diagnostics::TemplateAnalyzer;
use serde::{Deserialize, Serialize};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, FixContext, FixId, FixPresentation, FixResolution, Inspection,
    InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix, RewriteQuickFix, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_rewrite::{CreateFilePlan, WorkspacePlan};
use swlsp_twig::TwigIndexer;

use super::{clean_path, map_string};
use crate::inspections::{BoundInspection, SuggestionFix, resolve_anchor, suggestion_bound_fixes};

const CREATE_TEMPLATE_FIX_ID: &str = "create-template";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct CreateTemplatePayload {
    #[serde(default)]
    name: String,
    #[serde(default)]
    uri: String,
}

pub fn new_template(
    root: &str,
    twig_index: Option<Arc<TwigIndexer>>,
    php_index: Option<PhpIndex>,
) -> Arc<dyn Inspection> {
    let root = root.to_owned();
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "twig.template".into(),
            description: "References to templates that do not exist.",
            domain: "twig",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Php, LanguageId::Twig],
            problems: vec![ProblemDefinition {
                id: "twig.template.missing".into(),
                source: "twig".into(),
                default_severity: protocol::DiagnosticSeverity::ERROR,
                disabled_by_default: false,
            }],
        },
        analyzers: vec![Arc::new(TemplateAnalyzer::new(twig_index, php_index))],
        fixes: vec![Arc::new(SuggestionFix), Arc::new(CreateTemplateFix)],
        bind: Some(Arc::new(move |_, payload| {
            let mut bound = suggestion_bound_fixes(payload);
            let name = map_string(payload, "templateName");
            if let Some(target) = safe_template_creation_target(&root, &name) {
                bound.push(bind_fix(
                    CREATE_TEMPLATE_FIX_ID,
                    CreateTemplatePayload {
                        name,
                        uri: swlsp_uriutil::file_uri(&target),
                    },
                ));
            }
            bound
        })),
    })
}

struct CreateTemplateFix;

impl QuickFix for CreateTemplateFix {
    fn id(&self) -> FixId {
        FixId::from(CREATE_TEMPLATE_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: CreateTemplatePayload = fix.fix_payload()?;
        if payload.name.is_empty() || payload.uri.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: format!("Symfony: Create template '{}'", payload.name),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: false,
            resolution: FixResolution::Eager,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for CreateTemplateFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: CreateTemplatePayload = fix.fix_payload()?;
        resolve_anchor(fix)?;
        Ok(WorkspacePlan {
            creates: vec![CreateFilePlan {
                uri: payload.uri,
                content: String::new(),
            }],
            ..WorkspacePlan::default()
        })
    }
}

/// `path` made absolute against the working directory, then cleaned.
fn absolute(path: &str) -> Option<String> {
    let joined = if path.starts_with('/') {
        path.to_owned()
    } else {
        let cwd = std::env::current_dir().ok()?;
        format!("{}/{path}", cwd.to_string_lossy())
    };
    Some(clean_path(&joined))
}

/// `target` equals `root` or is below it (both clean and absolute).
fn path_inside(root: &str, target: &str) -> bool {
    if target == root {
        return true;
    }
    if root == "/" {
        return target.starts_with('/');
    }
    target
        .strip_prefix(root)
        .is_some_and(|rest| rest.starts_with('/'))
}

fn canonical(path: &str) -> Option<String> {
    std::fs::canonicalize(path)
        .ok()
        .map(|path| path.to_string_lossy().into_owned())
}

fn parent_dir(path: &str) -> String {
    match path.rfind('/') {
        Some(0) => "/".to_owned(),
        Some(index) => path[..index].to_owned(),
        None => ".".to_owned(),
    }
}

/// A non-existing `templates/<name>` file below the workspace root, also after
/// resolving symlinks.
fn safe_template_creation_target(root: &str, name: &str) -> Option<String> {
    if root.trim().is_empty() {
        return None;
    }
    let name = name.trim();
    let name = name.strip_prefix('/').unwrap_or(name);
    if name.is_empty()
        || name.starts_with('@')
        || name.contains([':', '\\'])
        || !swlsp_textutil::lowercase(name).ends_with(".twig")
    {
        return None;
    }
    let clean = clean_path(name);
    if clean == "." || clean == ".." || clean.starts_with("../") {
        return None;
    }
    let root_path = absolute(root)?;
    let target = clean_path(&format!("{root_path}/templates/{clean}"));
    if !path_inside(&root_path, &target) {
        return None;
    }
    match std::fs::metadata(&target) {
        Ok(_) => return None,
        Err(error) if error.kind() != ErrorKind::NotFound => return None,
        Err(_) => {}
    }
    let canonical_root = canonical(&root_path)?;
    let mut existing = parent_dir(&target);
    loop {
        match std::fs::metadata(&existing) {
            Ok(_) => break,
            Err(error) if error.kind() != ErrorKind::NotFound => return None,
            Err(_) => {}
        }
        let parent = parent_dir(&existing);
        if parent == existing {
            return None;
        }
        existing = parent;
    }
    let canonical_existing = canonical(&existing)?;
    let remainder = Path::new(&target).strip_prefix(&existing).ok()?;
    let canonical_target: PathBuf = Path::new(&canonical_existing).join(remainder);
    if !path_inside(&canonical_root, &canonical_target.to_string_lossy()) {
        return None;
    }
    Some(target)
}
