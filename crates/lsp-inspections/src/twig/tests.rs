//! Ports of Go `twig_versioning_test.go`, `translation_snapshot_test.go` and
//! `TestTemplateInspectionCreatesAValidatedFilePlan`.

use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;

use serde_json::Value;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    Context, DocumentSnapshot, FixContext, ProblemDefinition, QuickFix, RewriteQuickFix,
    TextDocument,
};
use swlsp_lsp_diagnostics::{
    TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE, TWIG_VERSIONING_COMMENT_MISSING_CODE,
    TWIG_VERSIONING_OUTDATED_CODE, TwigVersioningAnalyzer, TwigVersioningPayload,
};
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::ElementHandle;
use swlsp_twig::{TwigIndexer, VersioningService};

use super::translation::{AddTranslationFix, AddTranslationPayload};
use super::versioning::{
    TWIG_PARENT_DELEGATION_FIX_ID, TWIG_VERSION_COMMENT_FIX_ID, TwigParentDelegationFix,
    TwigVersionCommentFix,
};
use super::{new_template, new_twig_versioning};
use crate::test_support::{StaticDocumentResolver, fix_context, inspect, quick_fix_with_id};

fn s(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    indexer
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
}

const UPSTREAM: &str = "src/Storefront/Resources/views/storefront/page/example.html.twig";
const PLUGIN: &str = "custom/plugins/Example/src/Resources/views/storefront/page/example.html.twig";

fn versioning_fixture(block: &str, version: &str) -> (tempfile::TempDir, Arc<VersioningService>) {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(TwigIndexer::new(root.path().join("cache"), None).unwrap());
    index_file(index.as_ref(), &s(&root.path().join(UPSTREAM)), block);
    let service = Arc::new(VersioningService::new(
        &s(root.path()),
        Some(index),
        version,
    ));
    (root, service)
}

/// A fix context carrying only the document, diagnostic code and fix
/// payload (the Go tests build `lsp.FixContext` literals the same way).
fn bare_fix_context(document: Arc<TextDocument>, code: &str, payload: Value) -> FixContext {
    let root = document.root().unwrap();
    let anchor = ElementHandle::new(
        document.uri.clone(),
        document.version,
        document.language.unwrap(),
        root,
    );
    FixContext {
        document,
        diagnostic: protocol::Diagnostic {
            code: Some(Value::String(code.to_owned())),
            ..protocol::Diagnostic::default()
        },
        anchor,
        problem_payload: None,
        fix_payload: Some(payload),
        documents: Arc::new(StaticDocumentResolver::default()),
    }
}

fn content_payload() -> Value {
    serde_json::to_value(TwigVersioningPayload {
        block_name: "content".into(),
        ..TwigVersioningPayload::default()
    })
    .unwrap()
}

#[test]
fn twig_version_comment_fix_adds_and_updates_comment() {
    let (root, service) = versioning_fixture("{% block content %}upstream{% endblock %}", "6.7.2");
    let current = s(&root.path().join(PLUGIN));
    let cases = [
        (
            "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{% block content %}local{% endblock %}",
            TWIG_VERSIONING_COMMENT_MISSING_CODE,
            "Shopware: Add Twig block version comment",
        ),
        (
            "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{# shopware-block: deadbeef@6.6.0.0 #}\n{% block content %}local{% endblock %}",
            TWIG_VERSIONING_OUTDATED_CODE,
            "Shopware: Update Twig block version comment",
        ),
    ];
    for (source, code, title) in cases {
        let document = Arc::new(TextDocument::new(
            swlsp_uriutil::file_uri(&current),
            source,
            3,
        ));
        let context = bare_fix_context(document, code, content_payload());
        let fix = TwigVersionCommentFix {
            versioning: Some(service.clone()),
        };
        let presentation = fix
            .present(&Context::background(), &context)
            .unwrap()
            .expect("available");
        assert_eq!(presentation.title, title);
        let plan = fix.build(&Context::background(), &context).unwrap();
        assert_eq!(plan.documents.len(), 1);
        let updated = plan.documents[0].apply().unwrap();
        assert!(updated.contains("{# shopware-block: "), "{updated}");
        assert!(updated.contains("@6.7.2 #}"), "{updated}");
        assert!(!updated.contains("deadbeef"), "{updated}");
        let parsed = swlsp_twig::parse_twig(&current, &updated);
        assert!(parsed.blocks["content"].version_comment.is_some());
    }
}

#[test]
fn twig_versioning_inspection_declares_opt_in_missing_rule_and_bound_actions() {
    let (root, service) = versioning_fixture("{% block content %}upstream{% endblock %}", "6.7.2");
    let inspection = new_twig_versioning(Some(service));
    let definition = inspection.definition();
    let find = |id: &str| -> ProblemDefinition {
        definition
            .problems
            .iter()
            .find(|problem| problem.id.as_str() == id)
            .cloned()
            .unwrap()
    };
    let missing = find(TWIG_VERSIONING_COMMENT_MISSING_CODE);
    let redundant = find(TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE);
    assert!(missing.disabled_by_default);
    assert_eq!(
        redundant.default_severity,
        protocol::DiagnosticSeverity::HINT
    );
    assert!(!redundant.disabled_by_default);

    let document = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&s(&root.path().join(PLUGIN))),
        "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{% block content %}local{% endblock %}",
        1,
    ));
    let problems = inspect(inspection.as_ref(), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(
        problems[0].id.as_str(),
        TWIG_VERSIONING_COMMENT_MISSING_CODE
    );
    assert_eq!(problems[0].fixes.len(), 1);
    assert_eq!(
        problems[0].fixes[0].id.as_str(),
        TWIG_VERSION_COMMENT_FIX_ID
    );
}

#[test]
fn twig_versioning_inspection_binds_parent_delegation_fix() {
    let block = "{% block content %}same{% endblock %}";
    let (root, service) = versioning_fixture(block, "");
    let inspection = new_twig_versioning(Some(service));
    let document = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&s(&root.path().join(PLUGIN))),
        &format!("{{% sw_extends '@Storefront/storefront/page/example.html.twig' %}}\n{block}"),
        1,
    ));
    let problems = inspect(inspection.as_ref(), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].id.as_str(), TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE);
    assert_eq!(problems[0].fixes.len(), 1);
    assert_eq!(
        problems[0].fixes[0].id.as_str(),
        TWIG_PARENT_DELEGATION_FIX_ID
    );
}

#[test]
fn twig_parent_delegation_fix_replaces_body_and_removes_version_comment() {
    let block = "{% block content %}\n    <div>same</div>\n{% endblock %}";
    let (root, service) = versioning_fixture(block, "6.7.2");
    let path = s(&root.path().join(PLUGIN));
    let source = format!(
        "{{% sw_extends '@Storefront/storefront/page/example.html.twig' %}}\n{{# shopware-block: deadbeef@6.6.0.0 #}}\n{block}"
    );
    let document = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&path),
        &source,
        7,
    ));
    let context = bare_fix_context(
        document,
        TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE,
        content_payload(),
    );
    let fix = TwigParentDelegationFix {
        versioning: Some(service.clone()),
    };
    let presentation = fix
        .present(&Context::background(), &context)
        .unwrap()
        .expect("available");
    assert_eq!(
        presentation.title,
        "Shopware: Delegate Twig block to parent"
    );
    assert!(presentation.preferred);

    let plan = fix.build(&Context::background(), &context).unwrap();
    assert_eq!(plan.documents.len(), 1);
    let updated = plan.documents[0].apply().unwrap();
    assert_eq!(
        updated,
        "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{% block content %}\n    {{ parent() }}\n{% endblock %}"
    );
    let parsed = swlsp_twig::parse_twig(&path, &updated);
    assert!(parsed.blocks["content"].version_comment.is_none());

    let analyzer = TwigVersioningAnalyzer::new(Some(service));
    let after = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&path),
        &updated,
        8,
    ));
    let problems =
        swlsp_lsp_diagnostics::ProblemAnalyzer::analyze(&analyzer, &Context::background(), &after)
            .unwrap();
    assert!(problems.is_empty(), "{problems:?}");
}

#[test]
fn twig_parent_delegation_fix_rejects_stale_content() {
    let (root, service) = versioning_fixture("{% block content %}same{% endblock %}", "");
    let path = s(&root.path().join("custom/plugin.html.twig"));
    let document = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&path),
        "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{% block content %}changed{% endblock %}",
        2,
    ));
    let context = bare_fix_context(
        document,
        TWIG_BLOCK_REDUNDANT_OVERRIDE_CODE,
        content_payload(),
    );
    let error = TwigParentDelegationFix {
        versioning: Some(service),
    }
    .build(&Context::background(), &context)
    .unwrap_err();
    assert!(
        error.to_string().contains("no longer duplicates"),
        "{error}"
    );
}

#[test]
fn translation_fix_preserves_unsaved_target() {
    let cache = tempfile::tempdir().unwrap();
    let files = tempfile::tempdir().unwrap();
    let index = Arc::new(swlsp_translation::Index::new(cache.path(), None).unwrap());
    let path = s(&files.path().join("translations/messages.en.yaml"));
    std::fs::create_dir_all(Path::new(&path).parent().unwrap()).unwrap();
    let disk = "known: Known\n";
    std::fs::write(&path, disk).unwrap();
    index_file(index.as_ref(), &path, disk);
    let document = Arc::new(TextDocument::new(
        "file:///source.twig",
        "{{ 'missing'|trans }}",
        1,
    ));
    let uri = swlsp_uriutil::file_uri(&path);
    let target = Arc::new(TextDocument::new(&uri, "known: Unsaved\n", 2));
    let mut documents = HashMap::new();
    documents.insert(
        uri,
        DocumentSnapshot {
            document: target,
            version: Some(2),
        },
    );
    let mut context = bare_fix_context(
        document,
        "symfony.translation.key.missing",
        serde_json::to_value(AddTranslationPayload {
            domain: "messages".into(),
            key: "missing".into(),
            file: path.clone(),
        })
        .unwrap(),
    );
    context.documents = Arc::new(StaticDocumentResolver(documents));
    let plan = AddTranslationFix { index: Some(index) }
        .build(&Context::background(), &context)
        .unwrap();
    assert_eq!(plan.documents.len(), 1);
    assert_eq!(plan.documents[0].version, Some(2));
    assert_eq!(
        plan.documents[0].apply().unwrap(),
        "known: Unsaved\n'missing': 'missing'\n"
    );
    assert_eq!(std::fs::read_to_string(&path).unwrap(), disk);
}

#[test]
fn template_inspection_creates_a_validated_file_plan() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let twig = Arc::new(TwigIndexer::new(cache.path().join("twig-index"), None).unwrap());
    let document = Arc::new(TextDocument::new(
        swlsp_uriutil::file_uri(&s(&root.path().join("templates/page.html.twig"))),
        "{% include 'missing/card.html.twig' %}\n",
        1,
    ));
    let inspection = new_template(&s(root.path()), Some(twig), None);
    let problems = inspect(inspection.as_ref(), &document);
    assert_eq!(problems.len(), 1);
    let problem = &problems[0];
    assert_eq!(problem.id.as_str(), "twig.template.missing");
    assert_eq!(problem.fixes.len(), 1);
    assert_eq!(problem.fixes[0].id.as_str(), "create-template");

    let fix = quick_fix_with_id(inspection.as_ref(), "create-template");
    let plan = fix
        .as_rewrite()
        .unwrap()
        .build(
            &Context::background(),
            &fix_context(&document, problem, &problem.fixes[0], None),
        )
        .unwrap();
    assert!(plan.documents.is_empty());
    assert_eq!(plan.creates.len(), 1);
    assert_eq!(
        plan.creates[0].uri,
        swlsp_uriutil::file_uri(&s(&root.path().join("templates/missing/card.html.twig")))
    );
    assert!(
        !root
            .path()
            .join("templates/missing/card.html.twig")
            .exists()
    );
}
