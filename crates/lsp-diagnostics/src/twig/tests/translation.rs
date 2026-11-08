use std::collections::HashMap;
use std::sync::Arc;

use swlsp_snippet::SnippetIndexer;
use swlsp_translation::Index;

use super::*;
use crate::twig::{SnippetAnalyzer, TranslationAnalyzer};

const KEY: &str = "symfony.translation.key.missing";
const DOMAIN: &str = "symfony.translation.domain.missing";

pub(super) fn translation_index() -> (Vec<tempfile::TempDir>, Arc<Index>) {
    let cache = tempdir();
    let root = tempdir();
    let index = Arc::new(Index::new(cache.path(), None).unwrap());
    for (filename, source) in [
        ("messages.en.yaml", "hello.world: Hello world\n"),
        ("admin.en.yaml", "admin.dashboard: Dashboard\n"),
    ] {
        let path = root.path().join("translations").join(filename);
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(&path, source).unwrap();
        index_file(index.as_ref(), &path.to_string_lossy(), source);
    }
    (vec![cache, root], index)
}

#[test]
fn twig_translation_diagnostics() {
    let (_dirs, index) = translation_index();
    let provider = TranslationAnalyzer::new(Some(index), None, None);
    let document = doc(
        "file:///project/template.twig",
        "{{ 'hello.wrold'|trans }}\n{{ 'admin.dashboard'|trans({}, 'admin') }}\n{{ 'missing.key'|trans({}, 'admn') }}",
    );
    let result = analyze(&provider, &document);
    assert_eq!(result.len(), 2, "{result:?}");
    let by_code: HashMap<&str, &Problem> = result
        .iter()
        .map(|problem| (problem.id.as_str(), problem))
        .collect();
    let key = by_code[KEY];
    assert!(key.message.contains("hello.wrold"));
    assert_eq!(range_text(&document, key.range), "hello.wrold");
    assert!(suggestions(key).contains(&"hello.world".to_owned()));
    assert_eq!(payload_str(key, "domain"), "messages");
    assert_eq!(payload_str(key, "key"), "hello.wrold");
    let domain = by_code[DOMAIN];
    assert!(domain.message.contains("admn"));
    assert!(suggestions(domain).contains(&"admin".to_owned()));
}

#[test]
fn php_translation_diagnostics_use_receiver_type() {
    let (_dirs, index) = translation_index();
    let php_dir = tempdir();
    let php = php_index(php_dir.path());
    index_file(
        &php,
        "/project/TranslatorInterface.php",
        "<?php
namespace Symfony\\Contracts\\Translation;
interface TranslatorInterface {
    public function trans(string $id, array $parameters = [], ?string $domain = null): string;
}
",
    );
    let provider = TranslationAnalyzer::new(Some(index), Some(php), None);
    let document = doc(
        "file:///project/Controller.php",
        "<?php
namespace App;
use Symfony\\Contracts\\Translation\\TranslatorInterface;
function translate(TranslatorInterface $translator, object $logger): void {
    $translator->trans('hello.world');
    $translator->trans('hello.wrold');
    $translator->trans('missing.key', domain: 'admn');
    $logger->trans('ignored');
}",
    );
    let result = analyze(&provider, &document);
    assert_eq!(result.len(), 2, "{result:?}");
    for problem in &result {
        assert!(!problem.message.contains("ignored"));
    }
    let mut codes = ids(&result);
    codes.sort();
    assert_eq!(codes, vec![DOMAIN, KEY]);
}

#[test]
fn translation_diagnostics_validate_php_doc_assistant_references() {
    let (_dirs, index) = translation_index();
    let php_dir = tempdir();
    let php = php_index(php_dir.path());
    index_file(
        &php,
        "/project/TranslationAssistant.php",
        "<?php
/**
 * @param string $key #TranslationKey
 * @param string $domain #TranslationDomain
 */
function resolve_translation(string $key, string $domain): void {}
/** @param string $key #TranslationKey */
function resolve_default_translation(string $key): void {}
",
    );
    let provider = TranslationAnalyzer::new(Some(index), Some(php), None);
    let document = doc(
        "file:///project/Usage.php",
        "<?php
resolve_translation(key: 'admin.dashbord', domain: 'admin');
resolve_translation(domain: 'admn', key: 'admin.dashboard');
resolve_default_translation('hello.wrold');
resolve_default_translation('hello.world');
",
    );
    let result = analyze(&provider, &document);
    assert_eq!(result.len(), 3, "{result:?}");
    let by_text: HashMap<&str, &Problem> = result
        .iter()
        .map(|problem| (range_text(&document, problem.range), problem))
        .collect();
    let admin_key = by_text["admin.dashbord"];
    assert_eq!(admin_key.id.as_str(), KEY);
    assert_eq!(payload_str(admin_key, "domain"), "admin");
    assert!(suggestions(admin_key).contains(&"admin.dashboard".to_owned()));
    let domain = by_text["admn"];
    assert_eq!(domain.id.as_str(), DOMAIN);
    assert!(suggestions(domain).contains(&"admin".to_owned()));
    let default_key = by_text["hello.wrold"];
    assert_eq!(default_key.id.as_str(), KEY);
    assert_eq!(payload_str(default_key, "domain"), "messages");
    assert!(suggestions(default_key).contains(&"hello.world".to_owned()));
}

#[test]
fn snippet_diagnostics_accept_symfony_message_catalogue_key() {
    let (_dirs, index) = translation_index();
    let snippet_dir = tempdir();
    let snippets = Arc::new(SnippetIndexer::new(snippet_dir.path(), None).unwrap());
    let provider = SnippetAnalyzer::new(Some(snippets), Some(index));
    let document = doc("file:///project/template.twig", "{{ 'hello.world'|trans }}");
    assert!(analyze(&provider, &document).is_empty());
}

#[test]
fn suggestions_are_null_when_nothing_is_similar() {
    let (_dirs, index) = translation_index();
    let provider = TranslationAnalyzer::new(Some(index), None, None);
    let document = doc("file:///project/template.twig", "{{ 'zzz'|trans }}");
    let result = analyze(&provider, &document);
    assert_eq!(result.len(), 1);
    let payload = result[0].payload.as_ref().unwrap();
    assert_eq!(payload["suggestions"], serde_json::Value::Null);
}
