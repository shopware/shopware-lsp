//! Ports of the services-group tests of Go `inspection_test.go`.

use std::collections::HashMap;
use std::sync::Arc;

use serde_json::json;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, DocumentResolver, DocumentSnapshot, Problem, TextDocument, bind_fix};
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::{SyntaxElement, SyntaxNodeExt, TextRange, TextSize};
use swlsp_uriutil::file_uri;

use super::service_argument::ServiceArgumentFix;
use super::*;
use crate::test_support::{
    StaticDocumentResolver, apply_single, check_diagnostics, check_fix, document, fix_context,
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
    let inspection = new_invokable_command(Some(php));
    let uri = "file:///project/src/Command.php";
    let before = r"<?php
use Symfony\Component\Console\Attribute\AsCommand;

#[AsCommand]
class Command
{
    public function {|symfony.console.invoke.return_type:__invoke|}(): string
    {
        return {|symfony.console.invoke.return_value:'failure'|};
    }
}
";
    let (_, problems) = check_diagnostics(inspection.as_ref(), uri, before);
    assert_eq!(problems[0].payload, Some(json!({})));
    check_fix(
        inspection.as_ref(),
        uri,
        before,
        INVOKABLE_RETURN_TYPE_FIX_ID,
        r"<?php
use Symfony\Component\Console\Attribute\AsCommand;

#[AsCommand]
class Command
{
    public function __invoke(): int
    {
        return 'failure';
    }
}
",
    );
}

/// Indexes `php` into a PHP index and `services` into a service index that
/// resolves classes through it.
fn symfony_indexes(
    dir: &std::path::Path,
    php: &[(&str, &str)],
    services: &[(&str, &str)],
) -> (PhpIndex, Arc<ServiceIndex>) {
    let php_index = PhpIndex::open(dir.join("php"), None).unwrap();
    for (path, source) in php {
        php_index
            .index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .unwrap();
    }
    let service_index =
        ServiceIndex::new(&dir.to_string_lossy(), dir.join("symfony-cache"), None).unwrap();
    service_index.set_php_index(Some(php_index.clone()));
    for (path, source) in services {
        service_index
            .index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .unwrap();
    }
    (php_index, Arc::new(service_index))
}

#[test]
fn service_argument_inspection_reports_missing_yaml_arguments() {
    let dir = tempfile::tempdir().unwrap();
    let (php, services) = symfony_indexes(
        dir.path(),
        &[(
            "/project/src/Consumer.php",
            r"<?php
namespace App;
interface LoggerInterface {}
class Logger implements LoggerInterface {}
class Consumer {
    public function __construct(LoggerInterface $logger, string $name) {}
}",
        )],
        &[(
            "/project/config/indexed.yaml",
            "services:\n  logger:\n    class: App\\Logger\n",
        )],
    );
    let inspection = new_service_argument(Some(services), Some(php));
    let (_, problems) = check_diagnostics(
        inspection.as_ref(),
        "file:///project/config/services.yaml",
        "services:\n  {|symfony.service.arguments.missing:app.consumer|}:\n    class: App\\Consumer\n",
    );
    let problem = &problems[0];
    assert_eq!(
        problem.payload,
        Some(json!({
            "className": "App\\Consumer",
            "format": "yaml",
            "serviceID": "app.consumer",
            "missingArguments": [
                {
                    "name": "$logger",
                    "type": "App\\LoggerInterface",
                    "suggestedService": "logger",
                    "candidates": ["logger"],
                },
                {"name": "$name", "type": "string", "suggestedService": "?", "candidates": null},
            ],
        }))
    );
    assert_eq!(problem.fixes.len(), 1);
    assert_eq!(problem.fixes[0].id.as_str(), SERVICE_ARGUMENTS_FIX_ID);
}

#[test]
fn controller_inspection_reports_missing_route_method() {
    let dir = tempfile::tempdir().unwrap();
    let (php, services) = symfony_indexes(
        dir.path(),
        &[(
            "/project/src/ProductController.php",
            "<?php\nnamespace App;\n\nclass ProductController\n{\n    public function list(): void {}\n}\n",
        )],
        &[],
    );
    let inspection = new_controller(None, Some(services), Some(php));
    let (_, problems) = check_diagnostics(
        inspection.as_ref(),
        "file:///project/config/routes.yaml",
        "product.show:\n  path: /products/{id}/{slug}\n  controller: App\\ProductController::{|symfony.controller.method.missing:show|}\n",
    );
    let problem = &problems[0];
    assert_eq!(
        problem.payload,
        Some(json!({
            "className": "App\\ProductController",
            "controller": "App\\ProductController::show",
            "methodName": "show",
            "routeName": "product.show",
            "routeParameters": ["id", "slug"],
        }))
    );
    assert_eq!(problem.fixes.len(), 1);
    assert_eq!(problem.fixes[0].id.as_str(), CONTROLLER_METHOD_FIX_ID);
    assert_eq!(
        problem.fixes[0].payload,
        Some(json!({
            "className": "App\\ProductController",
            "methodName": "show",
            "parameters": ["id", "slug"],
        }))
    );
}
