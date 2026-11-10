use std::sync::Arc;

use swlsp_indexer::Indexer;
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_twig::TwigIndexer;

use super::*;
use crate::diagnostics::twig::{TwigMemberDeprecationAnalyzer, TwigMemberMissingAnalyzer};

fn member_indexes() -> (tempfile::TempDir, PhpIndex, Arc<TwigIndexer>) {
    let cache = tempdir();
    let php = php_index(cache.path());
    let twig = twig_index(cache.path());
    twig.set_dependencies(Some(php.clone()), None);
    let path = "/project/src/Twig/StringExtension.php";
    let source = r#"<?php
namespace Foo {
    class Bar {
        /** @deprecated */
        public string $deprecatedProperty;

        #[\Deprecated]
        public string $deprecatedAttributeProperty;

        public function getNext(): Bar { return new Bar(); }

        /** @deprecated */
        public function getDeprecated(): static { return $this; }
    }
}
namespace Twig\Extra\String {
    class StringExtension extends \Twig_Extension {
        public function getFilters(): array {
            return [new \Twig_SimpleFilter('u', [$this, 'createBar'])];
        }
        public function getFunctions(): array {
            return [new \Twig_SimpleFunction('ustring', [$this, 'createBar'])];
        }
        public function createBar(?string $text = null): \Foo\Bar {
            return new \Foo\Bar();
        }
    }
}"#;
    index_file(&php, path, source);
    index_file(twig.as_ref(), path, source);
    (cache, php, twig)
}

#[test]
fn twig_member_deprecation_diagnostics() {
    let (_cache, php, twig) = member_indexes();
    let (_, result) = check_problems(
        &TwigMemberDeprecationAnalyzer::new(Some(twig), Some(php)),
        "file:///project/templates/page.html.twig",
        "{# @var bar \\Foo\\Bar #}
{{ bar.next.{|twig.member.deprecated:deprecated|} }}
{{ bar.{|twig.member.deprecated:deprecatedProperty|} }}
{{ bar.{|twig.member.deprecated:deprecatedAttributeProperty|} }}
{{ ustring('value').{|twig.member.deprecated:deprecated|} }}
{{ 'value'|u.{|twig.member.deprecated:deprecated|} }}
",
    );
    let mut messages = Vec::new();
    for problem in &result {
        assert_eq!(problem.severity, Some(protocol::DiagnosticSeverity::HINT));
        assert_eq!(problem.tags, vec![protocol::DiagnosticTag::DEPRECATED]);
        messages.push(problem.message.clone());
    }
    assert!(messages.contains(&"Method 'Bar::getDeprecated' is deprecated".to_owned()));
    assert!(messages.contains(&"Field 'Bar::$deprecatedProperty' is deprecated".to_owned()));
    assert!(
        messages.contains(&"Field 'Bar::$deprecatedAttributeProperty' is deprecated".to_owned())
    );
}

fn summary(problems: &[Problem]) -> Vec<(String, TextRange, String)> {
    problems
        .iter()
        .map(|problem| (problem.id.0.clone(), problem.range, problem.message.clone()))
        .collect()
}

#[test]
fn twig_member_missing_diagnostics_reports_only_definite_objects() {
    let cache = tempdir();
    let source_dir = tempdir();
    let php = php_index(cache.path());
    index_file(
        &php,
        &source_dir.path().join("src/Model.php").to_string_lossy(),
        r#"<?php
namespace App;
class Category {
    public string $title;
}
class Product {
    public const FOO = 'foo';
    public string $name;
    private string $secret;
    public function getCategory(): Category {}
    public function isActive(): bool {}
}
class MagicModel {
    public function __get(string $name): mixed {}
}
class ProductCollection implements \IteratorAggregate {
    public function getIterator(): \Traversable {}
}
"#,
    );
    let fixture = r#"{# @var product \App\Product #}
{# @var nullable \App\Product|null #}
{# @var magic \App\MagicModel #}
{# @var collection \App\ProductCollection #}
{# @var list list<\App\Product> #}
{# @var unknown \App\NotIndexed #}
{# @var broad object #}
{# @var scalar string #}
{{ product.name }}
{{ product.FOO }}
{{ product.category.title }}
{{ product.active }}
{{ product.{|twig.member.missing:missing|} }}
{{ product.category.{|twig.member.missing:missingNested|} }}
{{ product.{|twig.member.missing:missing|}.deep }}
{{ product.{|twig.member.missing:secret|} }}
{{ nullable.{|twig.member.missing:missingNullable|} }}
{{ magic.anything }}
{{ collection.anything }}
{{ list.anything }}
{{ unknown.anything }}
{{ broad.anything }}
{{ scalar.anything }}
"#;
    let uri = "file:///project/templates/product.html.twig";
    let (_, result) = check_problems(
        &TwigMemberMissingAnalyzer::new(None, Some(php.clone())),
        uri,
        fixture,
    );
    for problem in &result {
        assert_eq!(problem.source, "twig");
        assert_eq!(
            problem.severity,
            Some(protocol::DiagnosticSeverity::WARNING)
        );
    }
    Indexer::close(&php).unwrap();
    drop(php);

    let reopened = php_index(cache.path());
    let (_, restored) = check_problems(
        &TwigMemberMissingAnalyzer::new(None, Some(reopened)),
        uri,
        fixture,
    );
    assert_eq!(summary(&result), summary(&restored));
}

#[test]
fn twig_member_missing_diagnostics_supports_known_union_members() {
    let dir = tempdir();
    let php = php_index(dir.path());
    index_file(
        &php,
        "/project/src/Union.php",
        r#"<?php
namespace App;
class First { public string $shared; }
class Second { public function getShared(): string {} }
class DynamicSecond { public function __call(string $name, array $args): mixed {} }
"#,
    );
    let (_, result) = check_problems(
        &TwigMemberMissingAnalyzer::new(None, Some(php)),
        "file:///project/templates/union.html.twig",
        r#"{# @var known \App\First|\App\Second #}
{# @var dynamic \App\First|\App\DynamicSecond #}
{{ known.shared }}
{{ known.{|twig.member.missing:missing|} }}
{{ dynamic.missing }}
"#,
    );
    assert!(result[0].message.contains("App\\First|App\\Second"));
}

#[test]
fn twig_member_missing_diagnostics_uses_twig_callable_return_types() {
    let (_cache, php, twig) = member_indexes();
    check_problems(
        &TwigMemberMissingAnalyzer::new(Some(twig), Some(php)),
        "file:///project/templates/callable.html.twig",
        "{{ ustring('value').deprecated }}
{{ ustring(text: 'value').{|twig.member.missing:unknownFunctionMember|} }}
{{ 'value'|u.deprecated }}
{{ 'value'|u.{|twig.member.missing:unknownFilterMember|} }}
",
    );
}
