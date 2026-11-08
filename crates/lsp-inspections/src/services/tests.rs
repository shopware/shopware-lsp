//! Ports of the services-group tests of Go `inspection_test.go`.

use std::collections::HashMap;
use std::sync::Arc;

use serde_json::json;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, DocumentResolver, DocumentSnapshot, Problem, TextDocument, bind_fix};
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxElement, SyntaxNodeExt, TextRange, TextSize};
use swlsp_uriutil::file_uri;

use super::service_argument::ServiceArgumentFix;
use super::*;
use crate::test_support::{
    StaticDocumentResolver, apply_single, document, fix_context, inspect, quick_fix_with_id,
};

#[test]
fn service_argument_fix_rewrites_yaml_service_definition() {
    let source = "services:\n  app.consumer:\n    class: App\\Consumer\n";
    let document = document("file:///project/config/services.yaml", source);
    let offset = source.find("app.consumer").unwrap() as u32;
    let node = document
        .root()
        .unwrap()
        .node_at(TextSize::from(offset))
        .unwrap();
    let problem = Problem {
        id: "symfony.service.arguments.missing".into(),
        range: node.text_range(),
        element: Some(SyntaxElement::Node(node)),
        message: "Missing service arguments".to_owned(),
        payload: Some(json!({
            "format": "yaml",
            "missingArguments": [
                {"name": "$logger", "suggestedService": "logger"},
                {"name": "$name", "suggestedService": "?"},
            ],
        })),
        ..Problem::default()
    };
    let bound = bind_fix(SERVICE_ARGUMENTS_FIX_ID, json!({}));
    let fix: Arc<dyn swlsp_lsp::QuickFix> = Arc::new(ServiceArgumentFix);
    let updated = apply_single(&fix, &fix_context(&document, &problem, &bound, None));
    assert!(updated.contains("    arguments:\n"), "{updated}");
    assert!(updated.contains("      - '@logger'\n"), "{updated}");
    assert!(updated.contains("      - '@?'"), "{updated}");
    assert!(
        TextDocument::new(document.uri.clone(), &updated, 2)
            .parse_errors()
            .is_empty()
    );
}

#[test]
fn php_method_fix_resolves_indexed_class_and_builds_cross_file_edit() {
    let project = tempfile::tempdir().unwrap();
    let target_path = project.path().join("src").join("ProductController.php");
    std::fs::create_dir_all(target_path.parent().unwrap()).unwrap();
    let target_source = "<?php\nnamespace App;\n\nclass ProductController\n{\n}\n";
    std::fs::write(&target_path, target_source).unwrap();
    let target_path = target_path.to_string_lossy().into_owned();
    let cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(cache.path().join("php-index"), None).unwrap();
    php.index(&ParsedFile::new(
        target_path.as_str(),
        target_source.as_bytes().to_vec(),
    ))
    .unwrap();

    let route_source = "controller: App\\ProductController::show\n";
    let route_document = Arc::new(TextDocument::new(
        file_uri(&project.path().join("config/routes.yaml").to_string_lossy()),
        route_source,
        4,
    ));
    let anchor_range = TextRange::new(
        TextSize::from(route_source.find("App\\ProductController").unwrap() as u32),
        TextSize::from(route_source.find("::show").unwrap() as u32),
    );
    let anchor = route_document
        .root()
        .unwrap()
        .descendant_for_range(anchor_range)
        .unwrap();
    let problem = Problem {
        id: "symfony.controller.method.missing".into(),
        range: anchor_range,
        element: Some(anchor),
        message: "Controller method is missing".to_owned(),
        ..Problem::default()
    };
    let bound = bind_fix(
        CONTROLLER_METHOD_FIX_ID,
        PhpMethodPayload {
            class_name: "App\\ProductController".to_owned(),
            method_name: "show".to_owned(),
            parameters: vec!["id".to_owned(), "$slug".to_owned()],
            typed_parameters: Vec::new(),
        },
    );
    let target_uri = file_uri(&target_path);
    let target_version = 8;
    let resolver = |source: &str, version: i32| -> Arc<dyn DocumentResolver> {
        let mut documents = HashMap::new();
        documents.insert(
            target_uri.clone(),
            DocumentSnapshot {
                document: Arc::new(TextDocument::new(target_uri.clone(), source, version)),
                version: Some(version),
            },
        );
        Arc::new(StaticDocumentResolver(documents))
    };
    let fix = PhpMethodFix {
        id: CONTROLLER_METHOD_FIX_ID,
        title_prefix: "controller",
        php_index: Some(php),
    };
    let context = fix_context(
        &route_document,
        &problem,
        &bound,
        Some(resolver(target_source, target_version)),
    );
    let plan = swlsp_lsp::RewriteQuickFix::build(&fix, &Context::background(), &context).unwrap();
    assert_eq!(plan.documents.len(), 1);
    assert_eq!(plan.documents[0].uri, target_uri);
    assert_eq!(plan.documents[0].version, Some(target_version));
    let updated = plan.documents[0].apply().unwrap();
    assert!(
        updated.contains("    public function show($id, $slug)\n"),
        "{updated}"
    );
    assert!(updated.contains("    {\n    }\n}"), "{updated}");
    assert!(
        TextDocument::new(target_uri.clone(), &updated, target_version + 1)
            .parse_errors()
            .is_empty()
    );

    // The target document can change independently from the diagnostic's
    // source document; a lazy action never creates a duplicate method.
    let context = fix_context(
        &route_document,
        &problem,
        &bound,
        Some(resolver(&updated, target_version + 1)),
    );
    let error =
        swlsp_lsp::RewriteQuickFix::build(&fix, &Context::background(), &context).unwrap_err();
    assert!(error.to_string().contains("already exists"), "{error}");
}

#[test]
fn invokable_command_inspection_rewrites_return_type_from_the_cst() {
    let cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(cache.path().join("php-index"), None).unwrap();
    let document = document(
        "file:///project/src/Command.php",
        r"<?php
use Symfony\Component\Console\Attribute\AsCommand;

#[AsCommand]
class Command
{
    public function __invoke(): string
    {
        return 'failure';
    }
}
",
    );
    let inspection = new_invokable_command(Some(php));
    let problems = inspect(inspection.as_ref(), &document);
    assert!(!problems.is_empty());
    let problem = &problems[0];
    assert_eq!(problem.id.as_str(), "symfony.console.invoke.return_type");
    assert_eq!(problem.fixes.len(), 1);
    assert_eq!(problem.payload, Some(json!({})));

    let fix = quick_fix_with_id(inspection.as_ref(), INVOKABLE_RETURN_TYPE_FIX_ID);
    let updated = apply_single(
        &fix,
        &fix_context(&document, problem, &problem.fixes[0], None),
    );
    assert!(
        updated.contains("public function __invoke(): int"),
        "{updated}"
    );
    assert!(
        TextDocument::new(document.uri.clone(), &updated, 2)
            .parse_errors()
            .is_empty()
    );
}
