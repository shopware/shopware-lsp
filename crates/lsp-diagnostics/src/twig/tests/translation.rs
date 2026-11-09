use std::sync::Arc;

use swlsp_snippet::SnippetIndexer;
use swlsp_translation::Index;

use super::*;
use crate::twig::{SnippetAnalyzer, TranslationAnalyzer};

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
    let (_, result) = check_problems(
        &provider,
        "file:///project/template.twig",
        "{{ '{|symfony.translation.key.missing:hello.wrold|}'|trans }}\n{{ 'admin.dashboard'|trans({}, 'admin') }}\n{{ 'missing.key'|trans({}, '{|symfony.translation.domain.missing:admn|}') }}",
    );
    let [key, domain] = result.as_slice() else {
        unreachable!()
    };
    assert!(key.message.contains("hello.wrold"));
    assert!(suggestions(key).contains(&"hello.world".to_owned()));
    assert_eq!(payload_str(key, "domain"), "messages");
    assert_eq!(payload_str(key, "key"), "hello.wrold");
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
    let (_, result) = check_problems(
        &provider,
        "file:///project/Controller.php",
        "<?php
namespace App;
use Symfony\\Contracts\\Translation\\TranslatorInterface;
function translate(TranslatorInterface $translator, object $logger): void {
    $translator->trans('hello.world');
    $translator->trans('{|symfony.translation.key.missing:hello.wrold|}');
    $translator->trans('missing.key', domain: '{|symfony.translation.domain.missing:admn|}');
    $logger->trans('ignored');
}",
    );
    for problem in &result {
        assert!(!problem.message.contains("ignored"));
    }
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
    let (_, result) = check_problems(
        &provider,
        "file:///project/Usage.php",
        "<?php
resolve_translation(key: '{|symfony.translation.key.missing:admin.dashbord|}', domain: 'admin');
resolve_translation(domain: '{|symfony.translation.domain.missing:admn|}', key: 'admin.dashboard');
resolve_default_translation('{|symfony.translation.key.missing:hello.wrold|}');
resolve_default_translation('hello.world');
",
    );
    let [admin_key, domain, default_key] = result.as_slice() else {
        unreachable!()
    };
    assert_eq!(payload_str(admin_key, "domain"), "admin");
    assert!(suggestions(admin_key).contains(&"admin.dashboard".to_owned()));
    assert!(suggestions(domain).contains(&"admin".to_owned()));
    assert_eq!(payload_str(default_key, "domain"), "messages");
    assert!(suggestions(default_key).contains(&"hello.world".to_owned()));
}

#[test]
fn snippet_diagnostics_accept_symfony_message_catalogue_key() {
    let (_dirs, index) = translation_index();
    let snippet_dir = tempdir();
    let snippets = Arc::new(SnippetIndexer::new(snippet_dir.path(), None).unwrap());
    let provider = SnippetAnalyzer::new(Some(snippets), Some(index));
    check_problems(
        &provider,
        "file:///project/template.twig",
        "{{ 'hello.world'|trans }}",
    );
}

#[test]
fn suggestions_are_empty_when_nothing_is_similar() {
    let (_dirs, index) = translation_index();
    let provider = TranslationAnalyzer::new(Some(index), None, None);
    let (_, result) = check_problems(
        &provider,
        "file:///project/template.twig",
        "{{ '{|symfony.translation.key.missing:zzz|}'|trans }}",
    );
    let payload = result[0].payload.as_ref().unwrap();
    assert_eq!(payload["suggestions"], serde_json::json!([]));
}
