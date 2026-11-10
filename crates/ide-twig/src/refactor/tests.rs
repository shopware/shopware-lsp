use super::*;
use crate::refactor::twig_template_rename::{pick_best_template_name, rename_template_basename};
use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    Context, FileRenameProvider, FileRenameRequest, RenameProvider, RenameRequest, SyntaxContext,
    TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::{LineIndex, SyntaxNodeExt, TextSize};
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

pub(crate) fn path_string(path: &Path) -> String {
    path.to_str().expect("utf-8 path").to_owned()
}

/// A rename request for `path` with the cursor at `offset`.
pub(crate) fn rename_request(
    path: &str,
    source: &str,
    offset: u32,
    new_name: &str,
) -> RenameRequest {
    let document = Arc::new(TextDocument::new(file_uri(path), source, 1));
    let (line, character) = document.line_index().position_utf16(offset);
    let root = document.root().expect("root");
    let node = root.node_at(TextSize::from(offset));
    let token = root.token_at(TextSize::from(offset));
    RenameRequest {
        params: protocol::RenameParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: document.uri.clone(),
            },
            position: protocol::Position { line, character },
            new_name: new_name.to_owned(),
        },
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document),
            root: Some(root),
            token,
            node,
        },
    }
}

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

fn write_and_index(index: &TwigIndexer, files: &[(&str, &str)]) {
    for (path, source) in files {
        std::fs::create_dir_all(std::path::Path::new(path).parent().unwrap()).unwrap();
        std::fs::write(path, source).unwrap();
        index
            .index(&ParsedFile::new(
                (*path).to_owned(),
                source.as_bytes().to_vec(),
            ))
            .unwrap();
    }
}

fn assert_ranges(source: &str, line_index: &LineIndex, edits: &[protocol::TextEdit]) {
    for edit in edits {
        let start = line_index.offset_utf16(edit.range.start.line, edit.range.start.character);
        let end = line_index.offset_utf16(edit.range.end.line, edit.range.end.character);
        assert!(end as usize <= source.len());
        assert!(source[start as usize..end as usize].contains("old.html.twig"));
    }
}

fn rename_request_twig_template(
    old: &str,
    new: &str,
    documents: Vec<Arc<TextDocument>>,
) -> FileRenameRequest {
    FileRenameRequest {
        params: protocol::RenameFilesParams {
            files: vec![protocol::FileRename {
                old_uri: file_uri(old),
                new_uri: file_uri(new),
            }],
        },
        documents,
    }
}

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

#[test]
fn twig_template_rename_updates_namespaced_twig_and_php_references() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let base = root.path().join("MyBundle/src/Resources/views");
    let old_path = path_string(&base.join("foo/old.html.twig"));
    let new_path = path_string(&base.join("bar/new.html.twig"));
    let twig_usage = path_string(&root.path().join("templates/usage.html.twig"));
    let php_usage = path_string(&root.path().join("src/Controller.php"));
    let disk_twig = "{% include 'foo/old.html.twig' %}
{% include '@Storefront/foo/old.html.twig' %}
{% include '@MyBundle/foo/old.html.twig' %}
{% include 'MyBundle:foo:old.html.twig' %}";
    let overlay_twig = format!("😀 {disk_twig}");
    let php_source = "<?php $this->render('foo/old.html.twig');";
    let index = Arc::new(TwigIndexer::new(cache.path(), None).unwrap());
    write_and_index(
        &index,
        &[
            (&old_path, "target"),
            (&twig_usage, disk_twig),
            (&php_usage, php_source),
        ],
    );
    let document = Arc::new(TextDocument::new(file_uri(&twig_usage), &overlay_twig, 2));
    let edit = TwigTemplateRenameProvider::new(Some(index.clone()))
        .will_rename_files(
            &Context::background(),
            &rename_request_twig_template(&old_path, &new_path, vec![document.clone()]),
        )
        .unwrap()
        .expect("edit");
    assert_eq!(edit.changes.len(), 2);
    let twig_edits = &edit.changes[&document.uri];
    let php_edits = &edit.changes[&file_uri(&php_usage)];
    assert_eq!(twig_edits.len(), 4);
    assert_eq!(php_edits.len(), 1);
    assert_ranges(&overlay_twig, document.line_index(), twig_edits);
    assert_ranges(
        php_source,
        &LineIndex::new(php_source.to_owned()),
        php_edits,
    );
    let mut replacements: Vec<&str> = twig_edits
        .iter()
        .chain(php_edits)
        .map(|edit| edit.new_text.as_str())
        .collect();
    replacements.sort_unstable();
    let mut expected = vec![
        "bar/new.html.twig",
        "@Storefront/bar/new.html.twig",
        "@MyBundle/bar/new.html.twig",
        "MyBundle:bar:new.html.twig",
        "bar/new.html.twig",
    ];
    expected.sort_unstable();
    assert_eq!(replacements, expected);
    index.close().unwrap();
}

#[test]
fn twig_template_rename_skips_ambiguous_override_names() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let target = path_string(
        &root
            .path()
            .join("MyBundle/src/Resources/views/foo/old.html.twig"),
    );
    let override_path = path_string(
        &root
            .path()
            .join("OtherBundle/src/Resources/views/foo/old.html.twig"),
    );
    let new_path = target.replace("old.html.twig", "new.html.twig");
    let usage = path_string(&root.path().join("templates/usage.html.twig"));
    let usage_source = "{% include 'foo/old.html.twig' %}
{% include '@MyBundle/foo/old.html.twig' %}";
    let index = Arc::new(TwigIndexer::new(cache.path(), None).unwrap());
    write_and_index(
        &index,
        &[
            (&target, "target"),
            (&override_path, "override"),
            (&usage, usage_source),
        ],
    );
    let edit = TwigTemplateRenameProvider::new(Some(index.clone()))
        .will_rename_files(
            &Context::background(),
            &rename_request_twig_template(&target, &new_path, Vec::new()),
        )
        .unwrap()
        .expect("edit");
    let changes = &edit.changes[&file_uri(&usage)];
    assert_eq!(changes.len(), 1);
    assert_eq!(changes[0].new_text, "@MyBundle/foo/new.html.twig");
    index.close().unwrap();
}

#[test]
fn template_rename_name_selection() {
    let names = |values: &[&str]| {
        values
            .iter()
            .map(|value| (*value).to_owned())
            .collect::<Vec<_>>()
    };
    assert_eq!(
        pick_best_template_name(
            &names(&["new.html.twig", "@App/new.html.twig"]),
            "@App/old.html.twig"
        ),
        "@App/new.html.twig"
    );
    assert_eq!(
        pick_best_template_name(
            &names(&["new.html.twig", "Bundle:dir:new.html.twig"]),
            "Bundle:dir:old.html.twig"
        ),
        "Bundle:dir:new.html.twig"
    );
    assert_eq!(
        rename_template_basename("foo/old.html.twig", "new.html.twig"),
        "foo/new.html.twig"
    );
}
