use std::collections::HashMap;
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, RenameProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

use super::{path_string, rename_request};
use crate::PhpTwigRenameProvider;

const PHP_SOURCE: &str = r"<?php
namespace MyNamespace;
class TestBrokenRefactor {
    public string $RestVar;
    public function getFoo(): string { return ''; }
}

namespace Foo;
class CardSuite { public const CLUBS = 'clubs'; }
enum Suit { case CLUBS; }

namespace BugDemo;
const NAMESPACED_CONST = 'value';
";

#[test]
fn php_twig_rename_provider_updates_typed_twig_usages() {
    let project = tempfile::tempdir().unwrap();
    let php_cache = tempfile::tempdir().unwrap();
    let twig_cache = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(php_cache.path(), None).unwrap();
    let php_path = path_string(&project.path().join("src/Symbols.php"));
    std::fs::create_dir_all(project.path().join("src")).unwrap();
    std::fs::write(&php_path, PHP_SOURCE).unwrap();
    php_index
        .index(&ParsedFile::new(
            php_path.clone(),
            PHP_SOURCE.as_bytes().to_vec(),
        ))
        .unwrap();
    let twig_index = Arc::new(TwigIndexer::new(twig_cache.path(), None).unwrap());
    twig_index.set_dependencies(Some(php_index.clone()), None);
    let templates = [
        (
            "field.html.twig",
            r"{# @var test \MyNamespace\TestBrokenRefactor #} {{ test.RestVar }}",
        ),
        (
            "shortcut.html.twig",
            r"{# @var test \MyNamespace\TestBrokenRefactor #} {{ test.foo }}",
        ),
        (
            "method.html.twig",
            r"{# @var test \MyNamespace\TestBrokenRefactor #} {{ test.getFoo() }}",
        ),
        (
            "constant.html.twig",
            r"{{ constant('Foo\\CardSuite::CLUBS') }}",
        ),
        (
            "constant_accessor.html.twig",
            r"{# @var card \Foo\CardSuite #} {{ card.CLUBS }}",
        ),
        (
            "global.html.twig",
            r"{{ constant('BugDemo\\NAMESPACED_CONST') }}",
        ),
        ("case.html.twig", r"{{ constant('Foo\\Suit::CLUBS') }}"),
        (
            "class.html.twig",
            r"{# @var test \MyNamespace\TestBrokenRefactor #}",
        ),
    ];
    let mut paths = HashMap::new();
    std::fs::create_dir_all(project.path().join("templates")).unwrap();
    for (name, source) in templates {
        let path = path_string(&project.path().join("templates").join(name));
        std::fs::write(&path, source).unwrap();
        twig_index
            .index(&ParsedFile::new(path.clone(), source.as_bytes().to_vec()))
            .unwrap();
        paths.insert(name, path);
    }
    let base: Arc<dyn RenameProvider> = Arc::new(swlsp_lsp_phpsemantic::Provider::new(Some(
        php_index.clone(),
    )));
    let provider = PhpTwigRenameProvider::new(
        Some(base),
        Some(twig_index.clone()),
        Some(php_index.clone()),
    );
    let rename = |needle: &str, new_name: &str| -> protocol::WorkspaceEdit {
        let offset = (PHP_SOURCE.find(needle).expect("needle") + 2) as u32;
        provider
            .rename(
                &Context::background(),
                &rename_request(&php_path, PHP_SOURCE, offset, new_name),
            )
            .unwrap()
            .expect("edit")
    };
    let texts = |edit: &protocol::WorkspaceEdit, template: &str| -> Vec<String> {
        edit.changes
            .get(&file_uri(&paths[template]))
            .into_iter()
            .flatten()
            .map(|edit| edit.new_text.clone())
            .collect()
    };

    let edit = rename("RestVar", "RenamedVar");
    assert_eq!(texts(&edit, "field.html.twig"), ["RenamedVar"]);
    let edit = rename("getFoo", "getBar");
    assert_eq!(texts(&edit, "shortcut.html.twig"), ["bar"]);
    assert_eq!(texts(&edit, "method.html.twig"), ["getBar"]);
    let edit = rename("CLUBS = 'clubs'", "HEARTS");
    assert_eq!(
        texts(&edit, "constant.html.twig"),
        [r"Foo\\CardSuite::HEARTS"]
    );
    assert_eq!(texts(&edit, "constant_accessor.html.twig"), ["HEARTS"]);
    let edit = rename("NAMESPACED_CONST", "RENAMED_CONST");
    assert_eq!(
        texts(&edit, "global.html.twig"),
        [r"BugDemo\\RENAMED_CONST"]
    );
    let edit = rename("CLUBS; }", "HEARTS");
    assert_eq!(texts(&edit, "case.html.twig"), [r"Foo\\Suit::HEARTS"]);
    let edit = rename("TestBrokenRefactor {", "RenamedClass");
    assert_eq!(
        texts(&edit, "class.html.twig"),
        [r"\MyNamespace\RenamedClass"]
    );
    twig_index.close().unwrap();
    php_index.close().unwrap();
}
