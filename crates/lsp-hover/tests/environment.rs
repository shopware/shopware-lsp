mod common;

use std::sync::Arc;

use common::*;
use swlsp_environment::Index;
use swlsp_lsp::{HoverRequest, SyntaxContext, TextDocument};
use swlsp_lsp_hover::EnvironmentHoverProvider;

#[test]
fn environment_hover_shows_processors_and_redacts_secrets() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let index = Arc::new(Index::new(cache.path(), None).unwrap());
    index_file(
        index.as_ref(),
        &p(&root.path().join(".env.local")),
        "APP_SECRET=do-not-display",
    );
    let source = "secret: '%env(resolve:APP_SECRET)%'";
    let path = root.path().join("config/services.yaml");
    index_file(index.as_ref(), &p(&path), source);
    let document = TextDocument::new(uri(&path), source, 1);
    let mut request: HoverRequest = request_at(document, source.find("APP_SECRET").unwrap() + 2);
    // Go passes only the source and line index.
    request.syntax = SyntaxContext {
        document: request.syntax.document.clone(),
        language: None,
        root: None,
        token: None,
        node: None,
    };
    let result = hover(
        &EnvironmentHoverProvider::new(p(root.path()), Some(index)),
        &request,
    )
    .expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("APP_SECRET"));
    assert!(value.contains("resolve"));
    assert!(value.contains(".env.local"));
    assert!(value.contains("••••••••"));
    assert!(!value.contains("do-not-display"));
}

#[test]
fn environment_hover_supports_autowire_env_processors() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let index = Arc::new(Index::new(cache.path(), None).unwrap());
    index_file(index.as_ref(), &p(&root.path().join(".env")), "APP_ENV=dev");
    let source = "<?php #[Autowire(env: 'resolve:int:APP_ENV')] class Config {}";
    let path = root.path().join("src/Config.php");
    let document = TextDocument::new(uri(&path), source, 1);
    let request = request_at(document, source.find("APP_ENV").unwrap() + 2);
    let result = hover(
        &EnvironmentHoverProvider::new(p(root.path()), Some(index)),
        &request,
    )
    .expect("hover");
    assert!(result.contents.value.contains("resolve → int"));
}
