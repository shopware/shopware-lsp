use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{HTML_TEXT, TWIG_LITERAL_STRING};
use tempfile::TempDir;

use super::*;
use crate::parser::catalogue_metadata;

fn new_test_index() -> (TempDir, Index) {
    let dir = tempfile::tempdir().expect("temp dir");
    let index = Index::new(dir.path(), None).expect("index");
    (dir, index)
}

fn index_source(index: &Index, path: &str, source: &str) {
    index
        .index(&ParsedFile::from_source(path, source))
        .expect("index file");
}

#[test]
fn index_yaml_translation_resource() {
    let (_dir, idx) = new_test_index();
    index_source(
        &idx,
        "/project/translations/messages.en.yaml",
        "checkout:\n  cart:\n    title: 'Your cart'\nplain: Plain value\n",
    );
    let messages = idx.messages_for("messages", "checkout.cart.title").unwrap();
    assert_eq!(messages.len(), 1);
    assert_eq!(messages[0].text, "Your cart");
    assert_eq!(messages[0].locale, "en");
    assert_eq!(messages[0].line, 2);
    assert_eq!(messages[0].format, "yaml");

    assert_eq!(
        idx.keys("messages").unwrap(),
        vec!["checkout.cart.title", "plain"]
    );
}

#[test]
fn index_xliff_translation_resources() {
    let (_dir, idx) = new_test_index();
    index_source(
        &idx,
        "/project/translations/validators.de.xlf",
        r#"<?xml version="1.0"?>
<xliff version="1.2"><file><body>
  <trans-unit id="1" resname="not.blank">
    <source>not.blank.fallback</source>
    <target>Dieser Wert sollte nicht leer sein.</target>
  </trans-unit>
</body></file></xliff>"#,
    );
    index_source(
        &idx,
        "/project/translations/messages.fr.xliff",
        r#"<?xml version="1.0"?>
<xliff version="2.0"><file>
  <unit id="hello"><segment>
    <source>hello.world</source>
    <target>Bonjour</target>
  </segment></unit>
</file></xliff>"#,
    );

    let validators = idx.messages_for("validators", "not.blank").unwrap();
    assert_eq!(validators.len(), 1);
    assert_eq!(validators[0].text, "Dieser Wert sollte nicht leer sein.");
    assert_eq!(validators[0].format, "xlf");

    let messages = idx.messages_for("messages", "hello.world").unwrap();
    assert_eq!(messages.len(), 1);
    assert_eq!(messages[0].text, "Bonjour");
}

#[test]
fn index_php_translation_resource() {
    let (_dir, idx) = new_test_index();
    index_source(
        &idx,
        "/project/translations/messages.en.php",
        "<?php\nreturn [\n    'hello.world' => 'Hello world',\n    'with.placeholder' => 'Hello %name%',\n];\n",
    );
    let messages = idx.messages_for("messages", "hello.world").unwrap();
    assert_eq!(messages.len(), 1);
    assert_eq!(messages[0].text, "Hello world");
    assert_eq!(messages[0].format, "php");
    assert_eq!(messages[0].line, 2);
}

#[test]
fn index_compiled_php_message_catalogue() {
    let (_dir, idx) = new_test_index();
    index_source(
        &idx,
        "/project/var/cache/dev/translations/catalogue.en.php",
        r#"<?php
$catalogue = new \Symfony\Component\Translation\MessageCatalogue('en_GB', [
    'messages' => [
        'hello.world' => 'Hello world',
    ],
    'validators' => [
        'not.blank' => 'This value should not be blank.',
    ],
    'shop+intl-icu' => [
        'items' => '{count, plural, one {item} other {items}}',
    ],
]);
"#,
    );
    assert_eq!(
        idx.domains().unwrap(),
        vec!["messages", "shop", "validators"]
    );
    let messages = idx.messages_for("messages", "hello.world").unwrap();
    assert_eq!(messages.len(), 1);
    assert_eq!(messages[0].locale, "en_GB");

    let icu = idx.messages_for("shop", "items").unwrap();
    assert_eq!(icu.len(), 1);
    assert_eq!(icu[0].text, "{count, plural, one {item} other {items}}");
}

#[test]
fn translation_index_replaces_and_removes_file() {
    let (_dir, idx) = new_test_index();
    let path = "/project/translations/messages.en.yaml";
    index_source(&idx, path, "first: First\n");
    index_source(&idx, path, "second: Second\n");

    assert!(idx.messages_for("messages", "first").unwrap().is_empty());
    assert_eq!(idx.messages_for("messages", "second").unwrap().len(), 1);

    idx.removed_files(&[path.to_owned()]).unwrap();
    assert!(idx.messages_for("messages", "second").unwrap().is_empty());
}

#[test]
fn catalogue_metadata_cases() {
    let cases = [
        (
            "/project/translations/messages.en.yaml",
            "messages",
            "en",
            false,
            true,
        ),
        (
            "/project/translations/admin+intl-icu.de_DE.xlf",
            "admin",
            "de_DE",
            false,
            true,
        ),
        (
            "/project/var/cache/dev/translations/catalogue.en.php",
            "catalogue",
            "en",
            true,
            true,
        ),
        ("/project/config/services.yaml", "", "", false, false),
    ];
    for (path, domain, locale, compiled, ok) in cases {
        let metadata = catalogue_metadata(path);
        assert_eq!(metadata.is_some(), ok, "{path}");
        let Some(metadata) = metadata else {
            continue;
        };
        assert_eq!(metadata.domain, domain, "{path}");
        assert_eq!(metadata.locale, locale, "{path}");
        assert_eq!(metadata.compiled, compiled, "{path}");
    }
}

fn write_resource(dir: &TempDir, name: &str, content: &str) -> String {
    let path = dir.path().join("translations").join(name);
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(&path, content).unwrap();
    path.to_string_lossy().into_owned()
}

#[test]
fn translation_yaml_insertion() {
    let (_dir, idx) = new_test_index();
    let files = tempfile::tempdir().unwrap();
    let content = "existing: Existing\n";
    let path = write_resource(&files, "messages.en.yaml", content);
    index_source(&idx, &path, content);

    let insertions = idx.insertions("messages", "new.key").unwrap();
    assert_eq!(insertions.len(), 1);
    assert_eq!(insertions[0].file, path);
    assert_eq!(insertions[0].line, 1);
    assert_eq!(insertions[0].character, 0);
    assert_eq!(insertions[0].new_text, "'new.key': 'new.key'\n");

    let extracted = idx
        .insertions_with_value("messages", "welcome.title", "Welcome to Bob's shop")
        .unwrap();
    assert_eq!(extracted.len(), 1);
    assert_eq!(extracted[0].locale, "en");
    assert_eq!(
        extracted[0].new_text,
        "'welcome.title': 'Welcome to Bob''s shop'\n"
    );
}

#[test]
fn translation_xliff_insertions() {
    let cases = [
        (
            "messages.en.xlf",
            "<xliff version=\"1.2\"><file><body>\n  <trans-unit id=\"4\"><source>existing</source><target>Existing</target></trans-unit>\n</body></file></xliff>",
            r#"<trans-unit id="5" resname="new.key"><source>new.key</source><target>new.key</target></trans-unit>"#,
        ),
        (
            "messages.en.xliff",
            "<xliff version=\"2.0\"><file><group>\n  <unit id=\"2\"><segment><source>existing</source><target>Existing</target></segment></unit>\n</group></file></xliff>",
            r#"<unit id="3"><segment><source>new.key</source><target>new.key</target></segment></unit>"#,
        ),
    ];
    for (filename, content, contains) in cases {
        let (_dir, idx) = new_test_index();
        let files = tempfile::tempdir().unwrap();
        let path = write_resource(&files, filename, content);
        index_source(&idx, &path, content);

        let insertions = idx.insertions("messages", "new.key").unwrap();
        assert_eq!(insertions.len(), 1, "{filename}");
        assert!(insertions[0].new_text.contains(contains), "{filename}");
        assert!(insertions[0].line > 0, "{filename}");

        let extracted = idx
            .insertions_with_value("messages", "welcome.title", "Welcome <friend>")
            .unwrap();
        assert_eq!(extracted.len(), 1, "{filename}");
        assert!(extracted[0].new_text.contains("Welcome &lt;friend&gt;"));
    }
}

#[test]
fn insertion_targets_need_only_indexed_metadata() {
    let (_dir, idx) = new_test_index();
    let files = tempfile::tempdir().unwrap();
    let path = files
        .path()
        .join("translations")
        .join("messages.en.yaml")
        .to_string_lossy()
        .into_owned();
    index_source(&idx, &path, "known: Known\n");
    let targets = idx.insertion_targets("messages").unwrap();
    assert_eq!(targets.len(), 1);
    assert_eq!(targets[0].file, path);
    assert_eq!(targets[0].locale, "en");
    assert!(targets[0].new_text.is_empty());
    assert!(!std::path::Path::new(&path).exists());
}

#[test]
fn insertion_rejects_duplicate_keys_in_current_snapshot() {
    for source in ["new.key: Unsaved\n", "new:\n  key: Unsaved\n"] {
        assert!(
            insertion_for_source("messages.en.yaml", source, "new.key", "Replacement").is_none()
        );
    }
    assert!(
        insertion_for_source(
            "messages.en.xlf",
            r#"<xliff version="1.2"><file><body><trans-unit id="1" resname="new.key"><source>new.key</source></trans-unit></body></file></xliff>"#,
            "new.key",
            "Replacement",
        )
        .is_none()
    );
}

#[test]
fn placeholders_cover_all_syntaxes() {
    let result = placeholders(
        "Hello %name%, {{ limit }}, @username, {title}, {count, plural, one {item} other {items}}",
    );
    for expected in [
        "%name%",
        "{{ limit }}",
        "@username",
        "{title}",
        "title",
        "count",
    ] {
        assert!(
            result.iter().any(|value| value == expected),
            "{expected}: {result:?}"
        );
    }
}

#[test]
fn php_translation_references() {
    let parse = swlsp_parser_php::parse(
        r#"<?php
$translator->trans('hello.world', [], 'admin');
$translator->trans(id: 'named.key', domain: 'named');
$translator->transChoice('plural.key', 2, [], 'plural');
new TranslatableMessage('message.key', domain: 'message');
t('function.key', domain: 'function');
"#,
    );
    let references = php_references(&parse.root());
    let mut values: Vec<String> = references
        .iter()
        .map(|reference| phpquery::string_value(reference.node.as_ref().unwrap()))
        .collect();
    values.sort();
    let mut expected = vec![
        "hello.world",
        "admin",
        "named.key",
        "named",
        "plural.key",
        "plural",
        "message.key",
        "message",
        "function.key",
        "function",
    ];
    expected.sort();
    assert_eq!(values, expected);

    let mut keys = std::collections::HashMap::new();
    let mut domains = std::collections::HashMap::new();
    for reference in &references {
        match reference.role {
            ReferenceRole::Key => {
                keys.insert(reference.key.clone(), reference.domain.clone());
            }
            ReferenceRole::Domain => {
                domains.insert(reference.domain.clone(), reference.role);
            }
            _ => {}
        }
    }
    assert_eq!(keys["hello.world"], "admin");
    assert_eq!(keys["named.key"], "named");
    assert_eq!(keys["plural.key"], "plural");
    assert_eq!(keys["message.key"], "message");
    assert_eq!(keys["function.key"], "function");
    assert_eq!(domains["admin"], ReferenceRole::Domain);
}

#[test]
fn php_translation_reference_rejects_nested_parameter_strings() {
    let parse = swlsp_parser_php::parse("<?php $translator->trans('key', ['nested' => 'value']);");
    for literal in phpquery::nodes(&parse.root(), &[PHP_STRING]) {
        let value = phpquery::string_value(&literal);
        let found = php_reference_at(&literal).is_some();
        assert_eq!(found, value == "key", "{value}");
    }
}

#[test]
fn php_translation_placeholder_reference() {
    let parse = swlsp_parser_php::parse(
        "<?php $translator->trans('hello.world', ['%na' => $name], 'admin');",
    );
    let literal = phpquery::nodes(&parse.root(), &[PHP_STRING])
        .into_iter()
        .find(|literal| phpquery::string_value(literal) == "%na")
        .expect("placeholder literal");
    let reference = php_placeholder_reference_at(&literal).expect("reference");
    assert_eq!(reference.role, ReferenceRole::Placeholder);
    assert_eq!(reference.key, "hello.world");
    assert_eq!(reference.domain, "admin");
}

#[test]
fn twig_translation_references() {
    let source = "{% trans_default_domain 'admin' %}\n{{ 'default.key'|trans }}\n{{ 'explicit.key'|trans({}, 'explicit') }}\n{{ 'plural.key'|transchoice(2, {}, 'plural') }}";
    let parse = swlsp_parser_twig::parse(source);
    let references = twig_references(&parse.root(), source);
    let mut keys = std::collections::HashMap::new();
    let mut domains = Vec::new();
    for reference in &references {
        match reference.role {
            ReferenceRole::Key => {
                keys.insert(reference.key.clone(), reference.domain.clone());
            }
            ReferenceRole::Domain => domains.push(reference.domain.clone()),
            _ => {}
        }
    }
    assert_eq!(keys["default.key"], "admin");
    assert_eq!(keys["explicit.key"], "explicit");
    assert_eq!(keys["plural.key"], "plural");
    domains.sort();
    assert_eq!(domains, vec!["admin", "explicit", "plural"]);
}

#[test]
fn twig_unquoted_default_domain_reference() {
    let source = "{% trans_default_domain admin %}{{ 'key'|trans }}";
    let parse = swlsp_parser_twig::parse(source);
    let node = twigquery::nodes(&parse.root(), &[HTML_TEXT])
        .into_iter()
        .find(|node| node.text().to_string().contains("admin"))
        .unwrap_or_else(|| panic!("admin node missing:\n{}", parse.debug_dump()));
    let reference =
        twig_reference_at(&node, source).unwrap_or_else(|| panic!("{}", parse.debug_dump()));
    assert_eq!(reference.role, ReferenceRole::Domain);
    assert_eq!(reference.domain, "admin");
}

#[test]
fn twig_translation_placeholder_reference() {
    let source = "{{ 'hello.world'|trans({'%na': name}, 'admin') }}";
    let parse = swlsp_parser_twig::parse(source);
    let literal = twigquery::nodes(&parse.root(), &[TWIG_LITERAL_STRING])
        .into_iter()
        .find(|literal| twigquery::string_value(literal).as_deref() == Some("%na"))
        .expect("placeholder literal");
    let reference = twig_reference_at(&literal, source).expect("reference");
    assert_eq!(reference.role, ReferenceRole::Placeholder);
    assert_eq!(reference.key, "hello.world");
    assert_eq!(reference.domain, "admin");
}
