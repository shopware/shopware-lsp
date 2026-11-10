use super::*;
use std::sync::Arc;
use swlsp_console::{CatalogEntry, CatalogInput, CatalogRequest, Index, LIST_CATALOG_COMMAND};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CommandProvider, Context};
use swlsp_uriutil::file_uri;

// --- console ---------------------------------------------------------------

#[test]
fn console_catalog_provides_commands_aliases_and_inputs() {
    let dir = tempfile::tempdir().unwrap();
    let index = Arc::new(Index::new(dir.path(), None).unwrap());
    let path = "/project/src/Command/CreateUserCommand.php";
    index
        .index(&ParsedFile::new(
            path.to_owned(),
            br"<?php
namespace App\Command;
#[AsCommand(
    name: 'app:user:create',
    description: 'Creates a user',
    aliases: ['app:user:add'],
)]
final class CreateUserCommand
{
    public function __invoke(
        #[Argument(description: 'User ID')] string $userId,
        #[Option(name: 'force', shortcut: 'f')] bool $force = false,
    ): int {
        return 0;
    }
}
"
            .to_vec(),
        ))
        .unwrap();
    let provider = ConsoleCatalogProvider::new(index.clone(), Some("/project"));
    let entries = provider.catalog(&Context::background(), "").unwrap();
    assert_eq!(entries.len(), 2);
    assert_eq!(entries[0].name, "app:user:add");
    assert_eq!(entries[0].canonical, "app:user:create");
    assert_eq!(entries[1].name, "app:user:create");
    assert_eq!(entries[1].description, "Creates a user");
    assert_eq!(entries[1].class, "App\\Command\\CreateUserCommand");
    assert_eq!(entries[1].file_uri, file_uri(path));
    assert_eq!(entries[1].file_path, "src/Command/CreateUserCommand.php");
    assert_eq!(
        entries[1].arguments,
        vec![CatalogInput {
            name: "userId".into(),
            description: "User ID".into(),
            ..CatalogInput::default()
        }]
    );
    assert_eq!(
        entries[1].options,
        vec![CatalogInput {
            name: "force".into(),
            shortcut: "f".into(),
            default: "false".into(),
            ..CatalogInput::default()
        }]
    );
    index.close().unwrap();
}

#[test]
fn console_catalog_command_filters_case_insensitively() {
    let dir = tempfile::tempdir().unwrap();
    let index = Arc::new(Index::new(dir.path(), None).unwrap());
    index
        .index(&ParsedFile::new(
            "/project/src/Commands.php".to_owned(),
            br"<?php
namespace App\Command;
#[AsCommand('app:user:create')]
final class CreateUser {}
#[AsCommand('app:cache:warm')]
final class WarmCache {}
"
            .to_vec(),
        ))
        .unwrap();
    let provider = ConsoleCatalogProvider::new(index.clone(), Some("/project"));
    let ctx = Context::background();
    let args = serde_json::to_value(CatalogRequest {
        query: "USER".into(),
        file_glob: "src/**/Commands.php".into(),
    })
    .unwrap();
    let value = (provider.commands(&ctx)[LIST_CATALOG_COMMAND])(&ctx, Some(&args)).unwrap();
    let expected = vec![CatalogEntry {
        name: "app:user:create".into(),
        canonical: "app:user:create".into(),
        class: "App\\Command\\CreateUser".into(),
        file_uri: "file:///project/src/Commands.php".into(),
        file_path: "src/Commands.php".into(),
        ..CatalogEntry::default()
    }];
    assert_eq!(value, serde_json::to_value(&expected).unwrap());
    let entries = provider
        .catalog_with_request(
            &ctx,
            &CatalogRequest {
                file_glob: "tests/**".into(),
                ..CatalogRequest::default()
            },
        )
        .unwrap();
    assert!(entries.is_empty());
    let unavailable = ConsoleCatalogProvider::unavailable();
    let error = (unavailable.commands(&ctx)[LIST_CATALOG_COMMAND])(&ctx, None).unwrap_err();
    assert_eq!(error.to_string(), "symfony console catalog is unavailable");
    index.close().unwrap();
}
