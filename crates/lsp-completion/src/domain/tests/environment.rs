use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, SyntaxContext};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};

use super::*;
use crate::EnvironmentCompletionProvider;

fn env_index(cache: &tempfile::TempDir, root: &str, env: &str) -> Arc<swlsp_environment::Index> {
    let index = Arc::new(swlsp_environment::Index::new(cache.path(), None).unwrap());
    index_file(index.as_ref(), &format!("{root}/.env"), env);
    index
}

fn positioned(
    document: &Arc<swlsp_lsp::TextDocument>,
    offset: usize,
    with_node: bool,
) -> CompletionRequest {
    let (line, character) = document.line_index().position_utf16(offset as u32);
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    let syntax = if with_node {
        SyntaxContext {
            language: document.language,
            root: document.root(),
            token: None,
            node: node_at(document, offset),
            document: Some(document.clone()),
        }
    } else {
        SyntaxContext {
            document: Some(document.clone()),
            ..SyntaxContext::default()
        }
    };
    CompletionRequest { params, syntax }
}

#[test]
fn environment_completion_replaces_incomplete_variable_name() {
    let (cache, root) = (tempdir(), tempdir());
    let root = root.path().to_string_lossy().into_owned();
    let index = env_index(&cache, &root, "DATABASE_URL=mysql://localhost\nAPP_ENV=dev");
    let source = "parameters:\n  database: '%env(resolve:DATAB";
    let document = document(
        &swlsp_uriutil::file_uri(&format!("{root}/config/services.yaml")),
        source,
    );
    let offset = source.find("DATAB").unwrap() + "DATAB".len();
    let items = EnvironmentCompletionProvider::new(index)
        .get_completions(&ctx(), &positioned(&document, offset, false));
    let database = require_completion(&items, "DATABASE_URL");
    assert_eq!(database.kind, Some(CompletionItemKind::VARIABLE));
    let edit = text_edit(database);
    assert_eq!(edit.new_text, "DATABASE_URL");
    assert_eq!(edit.range.end.character - edit.range.start.character, 5);
}

#[test]
fn environment_completion_supports_autowire_env_attribute() {
    let (cache, root) = (tempdir(), tempdir());
    let root = root.path().to_string_lossy().into_owned();
    let index = env_index(&cache, &root, "DATABASE_URL=mysql://localhost");
    let source = "<?php #[Autowire(env: 'resolve:DATAB')] class Config {}";
    let document = document(
        &swlsp_uriutil::file_uri(&format!("{root}/src/Config.php")),
        source,
    );
    let offset = source.find("DATAB").unwrap() + "DATAB".len();
    let items = EnvironmentCompletionProvider::new(index)
        .get_completions(&ctx(), &positioned(&document, offset, true));
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].label, "DATABASE_URL");
    let edit = text_edit(&items[0]);
    assert_eq!(edit.range.end.character - edit.range.start.character, 5);
}

#[test]
fn environment_completion_supports_php_env_function() {
    let (cache, root) = (tempdir(), tempdir());
    let root = root.path().to_string_lossy().into_owned();
    let index = env_index(&cache, &root, "APP_ENV=dev");
    let source = "<?php env('bool:APP_')";
    let document = document(
        &swlsp_uriutil::file_uri(&format!("{root}/config/services.php")),
        source,
    );
    let offset = source.find("APP_").unwrap() + "APP_".len();
    let items = EnvironmentCompletionProvider::new(index)
        .get_completions(&ctx(), &positioned(&document, offset, true));
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].label, "APP_ENV");
}
