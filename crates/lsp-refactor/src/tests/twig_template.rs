use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, FileRenameProvider, FileRenameRequest, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::LineIndex;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

use super::path_string;
use crate::TwigTemplateRenameProvider;
use crate::twig_template_rename::{pick_best_template_name, rename_template_basename};

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

fn rename_request(old: &str, new: &str, documents: Vec<Arc<TextDocument>>) -> FileRenameRequest {
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
            &rename_request(&old_path, &new_path, vec![document.clone()]),
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
            &rename_request(&target, &new_path, Vec::new()),
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
