use std::sync::Arc;

use swlsp_indexer::Indexer;
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_twig::TwigIndexer;

use super::*;
use crate::twig::{TwigMemberDeprecationAnalyzer, TwigMemberMissingAnalyzer};

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
    let document = doc(
        "file:///project/templates/page.html.twig",
        "{# @var bar \\Foo\\Bar #}
{{ bar.next.deprecated }}
{{ bar.deprecatedProperty }}
{{ bar.deprecatedAttributeProperty }}
{{ ustring('value').deprecated }}
{{ 'value'|u.deprecated }}
",
    );
    let result = analyze(
        &TwigMemberDeprecationAnalyzer::new(Some(twig), Some(php)),
        &document,
    );
    assert_eq!(result.len(), 5, "{result:?}");
    let mut messages = Vec::new();
    let mut ranges = Vec::new();
    for problem in &result {
        assert_eq!(problem.id.as_str(), "twig.member.deprecated");
        assert_eq!(problem.severity, Some(protocol::DiagnosticSeverity::HINT));
        assert_eq!(problem.tags, vec![protocol::DiagnosticTag::DEPRECATED]);
        messages.push(problem.message.clone());
        ranges.push(range_text(&document, problem.range).to_owned());
    }
    assert!(messages.contains(&"Method 'Bar::getDeprecated' is deprecated".to_owned()));
    assert!(messages.contains(&"Field 'Bar::$deprecatedProperty' is deprecated".to_owned()));
    assert!(
        messages.contains(&"Field 'Bar::$deprecatedAttributeProperty' is deprecated".to_owned())
    );
    assert_eq!(
        ranges,
        vec![
            "deprecated",
            "deprecatedProperty",
            "deprecatedAttributeProperty",
            "deprecated",
            "deprecated",
        ]
    );
}

fn summary(problems: &[Problem]) -> Vec<(String, TextRange, String)> {
    problems
        .iter()
        .map(|problem| (problem.id.0.clone(), problem.range, problem.message.clone()))
        .collect()
}

fn sorted(mut values: Vec<String>) -> Vec<String> {
    values.sort();
    values
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
    let document = doc(
        "file:///project/templates/product.html.twig",
        r#"{# @var product \App\Product #}
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
{{ product.missing }}
{{ product.category.missingNested }}
{{ product.missing.deep }}
{{ product.secret }}
{{ nullable.missingNullable }}
{{ magic.anything }}
{{ collection.anything }}
{{ list.anything }}
{{ unknown.anything }}
{{ broad.anything }}
{{ scalar.anything }}
"#,
    );
    let result = analyze(
        &TwigMemberMissingAnalyzer::new(None, Some(php.clone())),
        &document,
    );
    assert_eq!(result.len(), 5, "{result:?}");
    let mut ranges = Vec::new();
    for problem in &result {
        assert_eq!(problem.id.as_str(), "twig.member.missing");
        assert_eq!(problem.source, "twig");
        assert_eq!(
            problem.severity,
            Some(protocol::DiagnosticSeverity::WARNING)
        );
        ranges.push(range_text(&document, problem.range).to_owned());
    }
    assert_eq!(
        sorted(ranges),
        sorted(
            [
                "missing",
                "missingNested",
                "missing",
                "secret",
                "missingNullable"
            ]
            .map(str::to_owned)
            .to_vec()
        )
    );
    Indexer::close(&php).unwrap();
    drop(php);

    let reopened = php_index(cache.path());
    let restored = analyze(
        &TwigMemberMissingAnalyzer::new(None, Some(reopened)),
        &document,
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
    let document = doc(
        "file:///project/templates/union.html.twig",
        r#"{# @var known \App\First|\App\Second #}
{# @var dynamic \App\First|\App\DynamicSecond #}
{{ known.shared }}
{{ known.missing }}
{{ dynamic.missing }}
"#,
    );
    let result = analyze(&TwigMemberMissingAnalyzer::new(None, Some(php)), &document);
    assert_eq!(result.len(), 1, "{result:?}");
    assert_eq!(range_text(&document, result[0].range), "missing");
    assert!(result[0].message.contains("App\\First|App\\Second"));
}

#[test]
fn twig_member_missing_diagnostics_uses_twig_callable_return_types() {
    let (_cache, php, twig) = member_indexes();
    let document = doc(
        "file:///project/templates/callable.html.twig",
        "{{ ustring('value').deprecated }}
{{ ustring(text: 'value').unknownFunctionMember }}
{{ 'value'|u.deprecated }}
{{ 'value'|u.unknownFilterMember }}
",
    );
    let result = analyze(
        &TwigMemberMissingAnalyzer::new(Some(twig), Some(php)),
        &document,
    );
    assert_eq!(result.len(), 2, "{result:?}");
    let ranges: Vec<String> = result
        .iter()
        .map(|problem| range_text(&document, problem.range).to_owned())
        .collect();
    assert_eq!(
        sorted(ranges),
        sorted(vec![
            "unknownFunctionMember".to_owned(),
            "unknownFilterMember".to_owned()
        ])
    );
}
