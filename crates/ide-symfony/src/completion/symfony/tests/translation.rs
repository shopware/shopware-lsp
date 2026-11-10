//! Tests for translation completion.

use std::sync::Arc;

use swlsp_lsp_protocol as protocol;
use swlsp_syntax::TextSize;
use swlsp_translation::Index;

use super::*;
use crate::completion::symfony::TranslationCompletionProvider;

fn fixture(dir: &Path) -> Arc<Index> {
    let index = Index::new(dir, None).unwrap();
    index_file(
        &index,
        "/project/translations/messages.en.yaml",
        "hello.world: Hello world\n",
    );
    index_file(
        &index,
        "/project/translations/admin.en.yaml",
        "admin.dashboard: Dashboard\n",
    );
    index_file(
        &index,
        "/project/translations/validators.en.yaml",
        "validator.message: Invalid value\n",
    );
    Arc::new(index)
}

/// A translation completion request: the node at `offset`, position zero.
fn translation_request(
    uri: &str,
    source: &str,
    offset: usize,
) -> (Arc<TextDocument>, CompletionRequest) {
    let document = Arc::new(TextDocument::new(uri, source, 1));
    let node = document
        .root()
        .unwrap()
        .node_at(TextSize::from(offset as u32));
    let mut request = request_for(&document, node, 0);
    request.params.position = protocol::Position::default();
    (document, request)
}

#[test]
fn translation_completion_uses_twig_default_and_explicit_domains() {
    let dir = tempfile::tempdir().unwrap();
    let provider = TranslationCompletionProvider::new(Some(fixture(dir.path())), None);
    for (source, label) in [
        (
            "{% trans_default_domain 'admin' %}{{ ''|trans }}",
            "admin.dashboard",
        ),
        ("{{ ''|trans({}, 'admin') }}", "admin.dashboard"),
        ("{{ ''|transchoice(2, {}, 'messages') }}", "hello.world"),
    ] {
        let offset = source.find("''").unwrap() + 1;
        let (_, request) = translation_request("file:///project/template.twig", source, offset);
        let items = completions(&provider, &request);
        let item = require_completion(&items, label);
        assert_eq!(item.kind, kind(protocol::CompletionItemKind::VALUE));
    }
}

#[test]
fn translation_domain_completion_in_twig_default_domain_tag() {
    let dir = tempfile::tempdir().unwrap();
    let provider = TranslationCompletionProvider::new(Some(fixture(dir.path())), None);
    let source = "{% trans_default_domain '' %}";
    let (_, request) = translation_request(
        "file:///project/template.twig",
        source,
        source.find("''").unwrap() + 1,
    );
    let items = completions(&provider, &request);
    let item = require_completion(&items, "admin");
    assert_eq!(item.kind, kind(protocol::CompletionItemKind::MODULE));
    assert_eq!(item.detail, "1 translation key");
}

#[test]
fn php_translation_completion_requires_translator_type() {
    let dir = tempfile::tempdir().unwrap();
    let index = fixture(&dir.path().join("translations"));
    let php = php_index(
        &dir.path().join("php"),
        &[(
            "/project/TranslatorInterface.php",
            r"<?php
namespace Symfony\Contracts\Translation;
interface TranslatorInterface {
    public function trans(string $id, array $parameters = [], ?string $domain = null): string;
}
",
        )],
    );
    let provider = TranslationCompletionProvider::new(Some(index), Some(php));
    let source = r"<?php
namespace App;
use Symfony\Contracts\Translation\TranslatorInterface;
function translate(TranslatorInterface $translator): string {
    return $translator->trans('');
}
";
    let (_, request) = translation_request(
        "file:///project/Controller.php",
        source,
        source.rfind("''").unwrap() + 1,
    );
    require_completion(&completions(&provider, &request), "hello.world");

    let untyped = "<?php $logger->trans('');";
    let (_, request) = translation_request(
        "file:///project/Logger.php",
        untyped,
        untyped.find("''").unwrap() + 1,
    );
    assert!(completions(&provider, &request).is_empty());
}

#[test]
fn validator_message_completion_uses_validators_domain() {
    let dir = tempfile::tempdir().unwrap();
    let index = fixture(&dir.path().join("translations"));
    let php = php_index(
        &dir.path().join("php"),
        &[
            (
                "/project/vendor/Constraint.php",
                "<?php\nnamespace Symfony\\Component\\Validator;\nclass Constraint {}\n",
            ),
            (
                "/project/vendor/NotBlank.php",
                r"<?php
namespace Symfony\Component\Validator\Constraints;
class NotBlank extends \Symfony\Component\Validator\Constraint {
    public string $message;
}
",
            ),
            (
                "/project/vendor/ValidatorContext.php",
                r"<?php
namespace Symfony\Component\Validator\Context;
interface ExecutionContextInterface {
    public function addViolation(string $message): void;
    public function buildViolation(string $message): object;
}
namespace Symfony\Component\Validator\Violation;
interface ConstraintViolationBuilderInterface {
    public function setTranslationDomain(string $domain): self;
}
",
            ),
        ],
    );
    let provider = TranslationCompletionProvider::new(Some(index), Some(php));
    for source in [
        r"<?php
use Symfony\Component\Validator\Constraints\NotBlank;
#[NotBlank(message: '')]
function validate(): void {}
",
        r"<?php
use Symfony\Component\Validator\Constraints\NotBlank;
new NotBlank(['message' => '']);
",
        r"<?php
class UniqueName extends \Symfony\Component\Validator\Constraint {
    public string $message = '';
}
",
        r"<?php
use Symfony\Component\Validator\Context\ExecutionContextInterface;
function validate(ExecutionContextInterface $context): void {
    $context->buildViolation('');
}
",
    ] {
        let (_, request) = translation_request(
            "file:///project/Validation.php",
            source,
            source.rfind("''").unwrap() + 1,
        );
        require_completion(&completions(&provider, &request), "validator.message");
    }

    let source = r"<?php
use Symfony\Component\Validator\Violation\ConstraintViolationBuilderInterface;
function configure(ConstraintViolationBuilderInterface $builder): void {
    $builder->setTranslationDomain('');
}
";
    let (_, request) = translation_request(
        "file:///project/Violation.php",
        source,
        source.rfind("''").unwrap() + 1,
    );
    let items = completions(&provider, &request);
    require_completion(&items, "validators");
    require_completion(&items, "admin");
}

#[test]
fn phpdoc_translation_assistant_tag_completion() {
    let dir = tempfile::tempdir().unwrap();
    let index = fixture(&dir.path().join("translations"));
    let php = php_index(
        &dir.path().join("php"),
        &[(
            "/project/TranslationAssistant.php",
            r"<?php
/**
 * @param string $key #TranslationKey
 * @param string $domain #TranslationDomain
 */
function resolve_translation(string $key, string $domain): void {}

/** @param string $domain #TranslationDomain */
function resolve_domain(string $domain): void {}

/** @param string $key #TranslationKey */
function resolve_default_translation(string $key): void {}
",
        )],
    );
    let provider = TranslationCompletionProvider::new(Some(index), Some(php));
    for (name, source, partial, expected) in [
        (
            "key with named sibling domain",
            "<?php resolve_translation(\n    domain: 'admin',\n    key: 'admin.',\n);",
            "admin.",
            "admin.dashboard",
        ),
        ("domain", "<?php resolve_domain('adm');", "adm", "admin"),
        (
            "default messages domain",
            "<?php resolve_default_translation('hello.');",
            "hello.",
            "hello.world",
        ),
    ] {
        let offset = source.rfind(partial).unwrap() + partial.len();
        let (document, request) = translation_request("file:///project/Usage.php", source, offset);
        let item = require_completion(&completions(&provider, &request), expected);
        let edit = edit(&item);
        assert_eq!(edit.new_text, expected, "{name}");
        assert_eq!(range_text(&document, edit.range), partial, "{name}");
    }
}

#[test]
fn translation_placeholder_completion() {
    let dir = tempfile::tempdir().unwrap();
    let index = fixture(dir.path());
    index_file(
        index.as_ref(),
        "/project/translations/placeholders.en.yaml",
        "greeting: 'Hello %name%, {count, plural, one {item} other {items}}'\n",
    );
    let provider = TranslationCompletionProvider::new(Some(index), None);
    let source = "{{ 'greeting'|trans({'': name}, 'placeholders') }}";
    let (_, request) = translation_request(
        "file:///project/template.twig",
        source,
        source.find("''").unwrap() + 1,
    );
    let items = completions(&provider, &request);
    require_completion(&items, "%name%");
    require_completion(&items, "count");
}
