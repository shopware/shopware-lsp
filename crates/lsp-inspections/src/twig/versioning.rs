//! Shopware Twig block versioning inspection and its quick fixes.

use std::sync::Arc;

use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, CommandQuickFix, Context, FixContext, FixId, FixPresentation, FixResolution,
    Inspection, InspectionDefinition, InspectionScope, ProblemDefinition, QuickFix,
    RewriteQuickFix, bind_fix,
};
use swlsp_lsp_diagnostics::{
    TWIG_BLOCK_DEPRECATED_CODE, TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE,
    TWIG_VERSIONING_COMMENT_MISSING_CODE, TWIG_VERSIONING_ORIGINAL_MISSING_CODE,
    TWIG_VERSIONING_OUTDATED_CODE, TwigVersioningAnalyzer, TwigVersioningPayload,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{Builder, DocumentPlan, WorkspacePlan};
use swlsp_syntax::{TextRange, TextSize};
use swlsp_twig::VersioningService;

use crate::BoundInspection;

pub(crate) const TWIG_VERSION_COMMENT_FIX_ID: &str = "twig-version-comment";
pub(crate) const TWIG_BLOCK_DIFF_FIX_ID: &str = "twig-block-diff";
pub(crate) const TWIG_PARENT_DELEGATION_FIX_ID: &str = "twig-parent-delegation";

pub fn new_twig_versioning(versioning: Option<Arc<VersioningService>>) -> Arc<dyn Inspection> {
    let problem = |id: &str, severity, disabled_by_default| ProblemDefinition {
        id: id.into(),
        source: "shopware-lsp".into(),
        default_severity: severity,
        disabled_by_default,
    };
    use protocol::DiagnosticSeverity as Severity;
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.twig_versioning".into(),
            description: "Storefront block overrides whose `shopware-block` version comment is missing, outdated or unmatched, and overrides of deprecated or unchanged blocks. Opt-in.",
            domain: "shopware.twigVersioning",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Twig],
            problems: vec![
                problem(
                    TWIG_VERSIONING_COMMENT_MISSING_CODE,
                    Severity::WARNING,
                    true,
                ),
                problem(TWIG_BLOCK_DEPRECATED_CODE, Severity::WARNING, false),
                problem(TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE, Severity::HINT, false),
                problem(
                    TWIG_VERSIONING_ORIGINAL_MISSING_CODE,
                    Severity::WARNING,
                    false,
                ),
                problem(TWIG_VERSIONING_OUTDATED_CODE, Severity::WARNING, false),
            ],
        },
        analyzers: vec![Arc::new(TwigVersioningAnalyzer::new(versioning.clone()))],
        fixes: vec![
            Arc::new(TwigVersionCommentFix {
                versioning: versioning.clone(),
            }),
            Arc::new(TwigBlockDiffFix),
            Arc::new(TwigParentDelegationFix { versioning }),
        ],
        bind: Some(Arc::new(|code, payload| match code.as_str() {
            TWIG_VERSIONING_COMMENT_MISSING_CODE => {
                vec![bind_fix(TWIG_VERSION_COMMENT_FIX_ID, payload)]
            }
            TWIG_VERSIONING_OUTDATED_CODE => {
                let mut fixes = vec![bind_fix(TWIG_VERSION_COMMENT_FIX_ID, payload)];
                if matches!(payload.get("coreDiff"), Some(Value::Bool(true))) {
                    fixes.push(bind_fix(TWIG_BLOCK_DIFF_FIX_ID, payload));
                }
                fixes
            }
            TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE => {
                vec![bind_fix(TWIG_PARENT_DELEGATION_FIX_ID, payload)]
            }
            _ => Vec::new(),
        })),
    })
}

fn diagnostic_code(fix: &FixContext) -> String {
    match &fix.diagnostic.code {
        Some(Value::String(code)) => code.clone(),
        Some(other) => other.to_string(),
        None => String::new(),
    }
}

fn single_document_plan(fix: &FixContext, builder: &Builder) -> Result<WorkspacePlan, BoxError> {
    let edits = builder.finish()?;
    let document = &fix.document;
    Ok(WorkspacePlan {
        documents: vec![
            DocumentPlan::new(
                document.uri.clone(),
                Some(document.version),
                document.source_arc().clone(),
                edits,
            )
            .with_line_index(document.line_index_arc().clone()),
        ],
        ..WorkspacePlan::default()
    })
}

pub(crate) struct TwigVersionCommentFix {
    pub(crate) versioning: Option<Arc<VersioningService>>,
}

impl QuickFix for TwigVersionCommentFix {
    fn id(&self) -> FixId {
        FixId::from(TWIG_VERSION_COMMENT_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: TwigVersioningPayload = fix.fix_payload()?;
        if payload.block_name.is_empty() {
            return Ok(None);
        }
        let update = diagnostic_code(fix) == TWIG_VERSIONING_OUTDATED_CODE;
        let title = if update {
            "Shopware: Update Twig block version comment"
        } else {
            "Shopware: Add Twig block version comment"
        };
        Ok(Some(FixPresentation {
            title: title.into(),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: !update,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for TwigVersionCommentFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let Some(versioning) = &self.versioning else {
            return Err("twig versioning is unavailable".into());
        };
        let payload: TwigVersioningPayload = fix.fix_payload()?;
        let path = swlsp_uriutil::path(&fix.document.uri)?;
        let (range, replacement) =
            versioning.version_comment_edit(&path, fix.document.source(), &payload.block_name)?;
        let mut builder = Builder::new(fix.document.source());
        builder.replace_range(range, replacement)?;
        single_document_plan(fix, &builder)
    }
}

pub(crate) struct TwigParentDelegationFix {
    pub(crate) versioning: Option<Arc<VersioningService>>,
}

impl QuickFix for TwigParentDelegationFix {
    fn id(&self) -> FixId {
        FixId::from(TWIG_PARENT_DELEGATION_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: TwigVersioningPayload = fix.fix_payload()?;
        if payload.block_name.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: "Shopware: Delegate Twig block to parent".into(),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for TwigParentDelegationFix {
    fn build(&self, ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let Some(versioning) = &self.versioning else {
            return Err("twig versioning is unavailable".into());
        };
        ctx.check()?;
        let payload: TwigVersioningPayload = fix.fix_payload()?;
        let path = swlsp_uriutil::path(&fix.document.uri)?;
        let source = fix.document.source();
        let (_, block, resolution) =
            versioning.resolve_document(&path, source, &payload.block_name)?;
        if swlsp_twig::redundant_block_override(&block, &resolution).is_none() {
            return Err(format!(
                "twig block {:?} no longer duplicates its resolved parent",
                payload.block_name
            )
            .into());
        }
        let parsed = swlsp_twig::parse_block_body(&block.text);
        let Some((body, relative)) = parsed
            .filter(|(body, _)| !body.trim().is_empty() && !swlsp_twig::is_parent_delegation(body))
        else {
            return Err(format!(
                "twig block {:?} no longer has replaceable duplicate content",
                payload.block_name
            )
            .into());
        };
        let start = u32::from(block.range.start()) + u32::from(relative.start());
        let end = u32::from(block.range.start()) + u32::from(relative.end());
        if start > end || end as usize > source.len() {
            return Err(format!(
                "twig block {:?} body is outside the document",
                payload.block_name
            )
            .into());
        }
        let body_range = TextRange::new(TextSize::from(start), TextSize::from(end));
        let trimmed = body.trim();
        let content_start = body.find(trimmed).unwrap_or(0);
        let content_end = content_start + trimmed.len();
        let replacement = format!(
            "{}{{{{ parent() }}}}{}",
            &body[..content_start],
            &body[content_end..]
        );
        let mut builder = Builder::new(source);
        builder.replace_range(body_range, replacement)?;
        if let Some(comment) = block.version_comment_range {
            builder.replace_range(twig_comment_removal_range(source, comment), "")?;
        }
        single_document_plan(fix, &builder)
    }
}

/// The whole line when the comment is alone on it, otherwise the comment.
fn twig_comment_removal_range(source: &str, range: TextRange) -> TextRange {
    let start = u32::from(range.start()) as usize;
    let end = u32::from(range.end()) as usize;
    if end < start || end > source.len() {
        return range;
    }
    let line_start = source[..start].rfind('\n').map_or(0, |index| index + 1);
    let line_end = source[end..]
        .find('\n')
        .map_or(source.len(), |offset| end + offset + 1);
    let mut without_newline_end = line_end;
    if without_newline_end > end && source.as_bytes()[without_newline_end - 1] == b'\n' {
        without_newline_end -= 1;
    }
    if source[line_start..start].trim().is_empty()
        && source[end..without_newline_end].trim().is_empty()
    {
        return TextRange::new(
            TextSize::from(line_start as u32),
            TextSize::from(line_end as u32),
        );
    }
    range
}

struct TwigBlockDiffFix;

impl QuickFix for TwigBlockDiffFix {
    fn id(&self) -> FixId {
        FixId::from(TWIG_BLOCK_DIFF_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: TwigVersioningPayload = fix.fix_payload()?;
        if !payload.core_diff || payload.block_name.is_empty() {
            return Ok(None);
        }
        Ok(Some(FixPresentation {
            title: "Shopware: Show Twig block difference".into(),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_command(&self) -> Option<&dyn CommandQuickFix> {
        Some(self)
    }
}

impl CommandQuickFix for TwigBlockDiffFix {
    fn build_command(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<protocol::CommandAction>, BoxError> {
        let payload: TwigVersioningPayload = fix.fix_payload()?;
        Ok(Some(protocol::CommandAction {
            title: "Show Twig Block Difference".into(),
            command: "shopware.twig.showBlockDiff".into(),
            arguments: vec![
                Value::String(fix.document.uri.clone()),
                Value::String(payload.block_name),
            ],
        }))
    }
}
