use super::*;
use crate::{FileScanner, Indexer};
use std::sync::Arc;

fn store() -> (tempfile::TempDir, Store) {
    let directory = tempfile::tempdir().unwrap();
    let store = Store::open(directory.path().join("indexes.db")).unwrap();
    (directory, store)
}

fn symbol(name: &str, kind: WorkspaceSymbolKind, priority: i32) -> WorkspaceSymbol {
    WorkspaceSymbol {
        name: name.into(),
        kind,
        priority,
        ..WorkspaceSymbol::default()
    }
}

#[test]
fn query_and_priority() {
    let (_directory, store) = store();
    let catalog = WorkspaceSymbolCatalog::new(&store).unwrap();
    let cancel = CancelToken::new();
    catalog
        .replace_files(
            &cancel,
            &[
                WorkspaceSymbolDocument {
                    path: "/project/SystemConfigService.php".into(),
                    symbols: vec![
                        WorkspaceSymbol {
                            aliases: vec![r"Shopware\Core\SystemConfig\SystemConfigService".into()],
                            ..symbol(
                                "SystemConfigService",
                                WorkspaceSymbolKind::CLASS,
                                WORKSPACE_SYMBOL_PRIORITY_PHP_TYPE,
                            )
                        },
                        WorkspaceSymbol {
                            container_name: "Fixture".into(),
                            ..symbol(
                                "SystemConfigService",
                                WorkspaceSymbolKind::METHOD,
                                WORKSPACE_SYMBOL_PRIORITY_PHP_MEMBER,
                            )
                        },
                    ],
                },
                WorkspaceSymbolDocument {
                    path: "/project/services.xml".into(),
                    symbols: vec![WorkspaceSymbol {
                        aliases: vec!["SystemConfigService".into()],
                        ..symbol(
                            "Shopware_SystemConfigService",
                            WorkspaceSymbolKind::OBJECT,
                            WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
                        )
                    }],
                },
                WorkspaceSymbolDocument {
                    path: "/project/Unrelated.php".into(),
                    symbols: vec![WorkspaceSymbol {
                        container_name: "Config Service".into(),
                        ..symbol(
                            "testSystem",
                            WorkspaceSymbolKind::METHOD,
                            WORKSPACE_SYMBOL_PRIORITY_PHP_MEMBER,
                        )
                    }],
                },
            ],
        )
        .unwrap();

    let results = catalog.query("SystemConfigService", 20).unwrap();
    let kinds: Vec<_> = results.iter().map(|symbol| symbol.kind).collect();
    assert_eq!(
        kinds,
        [
            WorkspaceSymbolKind::CLASS,
            WorkspaceSymbolKind::OBJECT,
            WorkspaceSymbolKind::METHOD
        ]
    );
    assert_eq!(results[0].path, "/project/SystemConfigService.php");
    assert_eq!(
        results[0].aliases,
        [r"Shopware\Core\SystemConfig\SystemConfigService"]
    );

    let results = catalog.query("ConfigService", 20).unwrap();
    assert_eq!(results[0].name, "SystemConfigService");
    store.close().unwrap();
}

#[test]
fn excludes_domains_in_sql_query() {
    let (_directory, store) = store();
    let catalog = WorkspaceSymbolCatalog::new(&store).unwrap();
    catalog
        .replace_files(
            &CancelToken::new(),
            &[WorkspaceSymbolDocument {
                path: "/project/Symbols.php".into(),
                symbols: vec![
                    WorkspaceSymbol {
                        domain: "php".into(),
                        ..symbol("ProductRepository", WorkspaceSymbolKind::CLASS, 0)
                    },
                    WorkspaceSymbol {
                        domain: "symfony.service".into(),
                        ..symbol("ProductRepository", WorkspaceSymbolKind::OBJECT, 0)
                    },
                ],
            }],
        )
        .unwrap();
    let options = WorkspaceSymbolQueryOptions {
        excluded_domains: vec!["php".into(), "php".into(), " ".into()],
    };
    for query in ["", "ProductRepository"] {
        let results = catalog.query_with_options(query, 20, &options).unwrap();
        assert_eq!(results.len(), 1, "{query}");
        assert_eq!(results[0].domain, "symfony.service");
    }
    store.close().unwrap();
}

#[test]
fn replace_delete_and_read_only() {
    let (directory, store) = store();
    let catalog = WorkspaceSymbolCatalog::new(&store).unwrap();
    let cancel = CancelToken::new();
    for name in ["BeforeReplacement", "AfterReplacement"] {
        catalog
            .replace_files(
                &cancel,
                &[WorkspaceSymbolDocument {
                    path: "/project/Fixture.php".into(),
                    symbols: vec![symbol(
                        name,
                        WorkspaceSymbolKind::CLASS,
                        WORKSPACE_SYMBOL_PRIORITY_PHP_TYPE,
                    )],
                }],
            )
            .unwrap();
    }
    catalog.set_ready(&cancel, true).unwrap();
    assert!(catalog.query("Before", 20).unwrap().is_empty());
    assert_eq!(catalog.query("After", 20).unwrap().len(), 1);
    catalog
        .delete_files(&cancel, &["/project/Other.php".to_owned()])
        .unwrap();
    assert_eq!(catalog.query("After", 20).unwrap().len(), 1);
    store.close().unwrap();

    let read_only =
        WorkspaceSymbolCatalog::open_read_only(directory.path().join("indexes.db")).unwrap();
    assert!(read_only.ready().unwrap());
    assert_eq!(read_only.query("After", 20).unwrap().len(), 1);
    assert!(read_only.set_ready(&cancel, false).is_err());
    read_only.close().unwrap();
    assert!(read_only.query("After", 20).is_err());

    let empty = directory.path().join("empty.db");
    rusqlite::Connection::open(&empty).unwrap();
    assert!(WorkspaceSymbolCatalog::open_read_only(&empty).is_err());
}

#[test]
fn bulk_insert_preserves_fts_doc_ids() {
    let (_directory, store) = store();
    let catalog = WorkspaceSymbolCatalog::new(&store).unwrap();
    let symbols: Vec<_> = (0..161)
        .map(|index| {
            symbol(
                &format!("BulkSymbol{index:03}"),
                WorkspaceSymbolKind::CLASS,
                WORKSPACE_SYMBOL_PRIORITY_PHP_TYPE,
            )
        })
        .collect();
    catalog
        .replace_files(
            &CancelToken::new(),
            &[WorkspaceSymbolDocument {
                path: "/project/Bulk.php".into(),
                symbols,
            }],
        )
        .unwrap();
    let result = catalog.query("BulkSymbol160", 20).unwrap();
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].name, "BulkSymbol160");
    store.close().unwrap();
}

#[test]
fn bulk_population_defers_and_rebuilds_fts() {
    let (_directory, store) = store();
    let catalog = WorkspaceSymbolCatalog::new(&store).unwrap();
    let cancel = CancelToken::new();
    catalog.begin_bulk_population();
    catalog
        .replace_files(
            &cancel,
            &[WorkspaceSymbolDocument {
                path: "/project/Deferred.php".into(),
                symbols: vec![symbol("DeferredSymbol", WorkspaceSymbolKind::CLASS, 0)],
            }],
        )
        .unwrap();
    assert!(catalog.query("DeferredSymbol", 20).unwrap().is_empty());
    catalog.end_bulk_population(&cancel).unwrap();
    assert_eq!(catalog.query("DeferredSymbol", 20).unwrap().len(), 1);
    store.close().unwrap();
}

fn has_priority_index(store: &Store) -> bool {
    store
        .read(|connection| {
            Ok(connection.query_row(
                "SELECT EXISTS (SELECT 1 FROM sqlite_master
                 WHERE type = 'index' AND name = 'idx_workspace_symbols_priority')",
                [],
                |row| row.get(0),
            )?)
        })
        .unwrap()
}

#[test]
fn populating_an_empty_catalog_defers_the_priority_index() {
    let (_directory, store) = store();
    let catalog = WorkspaceSymbolCatalog::new(&store).unwrap();
    let cancel = CancelToken::new();
    let document = |path: &str, name: &str| WorkspaceSymbolDocument {
        path: path.into(),
        symbols: vec![symbol(name, WorkspaceSymbolKind::CLASS, 0)],
    };
    assert!(has_priority_index(&store));

    catalog.begin_bulk_population();
    assert!(!has_priority_index(&store));
    catalog
        .replace_files(&cancel, &[document("/project/First.php", "First")])
        .unwrap();
    catalog.end_bulk_population(&cancel).unwrap();
    assert!(has_priority_index(&store));
    assert_eq!(catalog.query("First", 20).unwrap().len(), 1);

    // A populated catalog keeps the index: a warm run changes few rows.
    catalog.begin_bulk_population();
    assert!(has_priority_index(&store));
    catalog
        .replace_files(&cancel, &[document("/project/Second.php", "Second")])
        .unwrap();
    catalog.end_bulk_population(&cancel).unwrap();
    assert_eq!(catalog.query("Second", 20).unwrap().len(), 1);
    store.close().unwrap();
}

#[test]
fn an_interrupted_cold_population_gets_its_priority_index_back() {
    let (_directory, store) = store();
    let catalog = WorkspaceSymbolCatalog::new(&store).unwrap();
    catalog.begin_bulk_population();
    assert!(!has_priority_index(&store));
    // The process ends before `end_bulk_population`; the next one reopens.
    drop(catalog);
    let reopened = WorkspaceSymbolCatalog::new(&store).unwrap();
    assert!(has_priority_index(&store));
    drop(reopened);
    store.close().unwrap();
}

#[test]
fn an_empty_bulk_population_restores_the_priority_index() {
    let (_directory, store) = store();
    let catalog = WorkspaceSymbolCatalog::new(&store).unwrap();
    catalog.begin_bulk_population();
    catalog.end_bulk_population(&CancelToken::new()).unwrap();
    assert!(has_priority_index(&store));
    store.close().unwrap();
}

#[test]
fn index_text_normalization() {
    let symbol = WorkspaceSymbol {
        name: "SystemConfigServiceFactory42".into(),
        container_name: r"Shopware\\Core\\System\\SystemConfigService".into(),
        aliases: vec![
            r"Shopware\\Core\\System\\SystemConfig\\SystemConfigServiceFactory42".into(),
            "system-config-service-factory-42".into(),
        ],
        ..WorkspaceSymbol::default()
    };
    assert_eq!(
        workspace_symbol_index_text(&symbol),
        "system config service factory 42 systemconfigservicefactory42 configservicefactory42 servicefactory42 factory42 systemconfigservice configservice"
    );
    assert_eq!(
        workspace_symbol_leaf_name("Vendor·RésuméÜber"),
        "RésuméÜber"
    );
    assert_eq!(
        workspace_symbol_leaf_name("Vendor·RésuméÜber/"),
        "RésuméÜber"
    );
    assert_eq!(workspace_symbol_leaf_name(r"\\"), r"\\");
    assert_eq!(
        workspace_symbol_search_text(&["XMLHttpRequest2", "RésuméÜber"]),
        "xml http request 2 xmlhttprequest2 httprequest2 request2 résumé über résuméüber"
    );
    assert_eq!(
        workspace_symbol_match_query("ConfigService"),
        "configservice*"
    );
    assert_eq!(
        workspace_symbol_match_query("config service"),
        "config* AND service*"
    );
    assert_eq!(workspace_symbol_match_query("::"), "");
    assert_eq!(
        workspace_symbol_aliases(&[" a ".into(), "a".into(), "".into(), "b".into()]),
        "a\u{1f}b"
    );
}

#[test]
fn range_helpers() {
    let file = ParsedFile::from_source("/project/a.php", "a\n😀b");
    let range = WorkspaceSymbolRange::from_text(&file, TextRange::new(2.into(), 7.into()));
    assert_eq!(
        range.start,
        WorkspaceSymbolPosition {
            line: 1,
            character: 0
        }
    );
    assert_eq!(
        range.end,
        WorkspaceSymbolPosition {
            line: 1,
            character: 3
        }
    );
    assert_eq!(
        WorkspaceSymbolRange::from_text(&file, TextRange::default()),
        WorkspaceSymbolRange::default()
    );
    assert_eq!(WorkspaceSymbolRange::at_line(0).start.line, 0);
    assert_eq!(WorkspaceSymbolRange::at_line(5).start.line, 4);
}

struct SymbolIndexer;

impl Indexer for SymbolIndexer {
    fn id(&self) -> &str {
        "test.symbols"
    }
    fn index(&self, _file: &ParsedFile) -> Result<()> {
        Ok(())
    }
    fn removed_files(&self, _paths: &[String]) -> Result<()> {
        Ok(())
    }
    fn clear(&self) -> Result<()> {
        Ok(())
    }
    fn as_transactional_remover(&self) -> Option<&dyn crate::TransactionalRemover> {
        Some(self)
    }
    fn as_transactional_clearer(&self) -> Option<&dyn crate::TransactionalClearer> {
        Some(self)
    }
    fn as_workspace_symbol_contributor(&self) -> Option<&dyn crate::WorkspaceSymbolContributor> {
        Some(self)
    }
}

impl crate::TransactionalRemover for SymbolIndexer {
    fn removed_files_in(&self, _paths: &[String], _mutation: &Mutation) -> Result<()> {
        Ok(())
    }
}

impl crate::TransactionalClearer for SymbolIndexer {
    fn clear_in(&self, _mutation: &Mutation) -> Result<()> {
        Ok(())
    }
}

impl crate::WorkspaceSymbolContributor for SymbolIndexer {
    fn workspace_symbols(
        &self,
        file: &ParsedFile,
        _prepared: Option<&crate::Prepared>,
    ) -> Result<Vec<WorkspaceSymbol>> {
        Ok(vec![symbol(
            file.source(),
            WorkspaceSymbolKind::CLASS,
            WORKSPACE_SYMBOL_PRIORITY_PHP_TYPE,
        )])
    }
}

#[test]
fn file_scanner_maintains_catalog_lifecycle() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let path = root.path().join("Fixture.php");
    let path_text = path.to_str().unwrap().to_owned();
    std::fs::write(&path, "Before").unwrap();
    let store = Store::open(cache.path().join("indexes.db")).unwrap();
    let catalog = Arc::new(WorkspaceSymbolCatalog::new(&store).unwrap());
    let mut scanner = FileScanner::new(
        root.path().to_str().unwrap(),
        cache.path().join("files.db"),
        Some(store.clone()),
    )
    .unwrap();
    scanner.set_workspace_symbol_catalog(catalog.clone());
    scanner.add_indexer(Arc::new(SymbolIndexer));
    let cancel = CancelToken::new();

    scanner.index_all(&cancel).unwrap();
    assert!(catalog.ready().unwrap());
    assert_eq!(catalog.query("Before", 20).unwrap().len(), 1);

    std::fs::write(&path, "After").unwrap();
    scanner.index_files(&cancel, &[&path_text]).unwrap();
    assert!(catalog.query("Before", 20).unwrap().is_empty());
    assert_eq!(catalog.query("After", 20).unwrap().len(), 1);

    scanner.remove_files(&cancel, &[&path_text]).unwrap();
    assert!(catalog.query("After", 20).unwrap().is_empty());

    scanner.clear_hashes().unwrap();
    assert!(!catalog.ready().unwrap());
    scanner.close().unwrap();
    store.close().unwrap();
}
