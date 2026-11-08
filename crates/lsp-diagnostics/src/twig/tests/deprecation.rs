use std::sync::Arc;

use swlsp_lsp_protocol as protocol;
use swlsp_twig::TwigIndexer;

use super::*;
use crate::twig::TwigDeprecationAnalyzer;

fn deprecation_index() -> (tempfile::TempDir, Arc<TwigIndexer>) {
    let dir = tempdir();
    let index = twig_index(dir.path());
    index_file(
        index.as_ref(),
        "/project/src/AppExtension.php",
        r#"<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
use Twig\TwigFunction;
use Twig\TwigTest;
class AppExtension extends AbstractExtension {
    public function getFunctions(): array {
        return [
            new TwigFunction('legacy_function', $this->legacyFunction(...)),
            new TwigFunction('active_function', $this->activeFunction(...)),
        ];
    }
    public function getFilters(): array {
        return [
            new TwigFilter('legacy_filter', $this->legacyFilter(...)),
            new TwigFilter('active_filter', $this->activeFilter(...)),
        ];
    }
    public function getTests(): array {
        return [
            new TwigTest('legacy_test', $this->legacyTest(...), [
                'deprecated' => '1.0',
            ]),
            new TwigTest('active_test', $this->activeTest(...)),
        ];
    }
    public function legacyFunction(): string {
        trigger_deprecation(
            'app',
            '1.0',
            'The "legacy_function" Twig function is deprecated. Use active_function instead.',
        );
        return '';
    }
    public function activeFunction(): string { return ''; }
    public function legacyFilter(string $value): string {
        Feature::triggerDeprecationOrThrow(
            'v2',
            'The "legacy_filter" Twig filter is deprecated. Use active_filter instead.',
        );
        return $value;
    }
    public function activeFilter(string $value): string { return $value; }
    public function legacyTest(string $value): bool { return false; }
    public function activeTest(string $value): bool { return true; }
}"#,
    );
    index_file(
        index.as_ref(),
        "/project/src/LegacyTokenParser.php",
        r#"<?php
/** @deprecated Use modern_tag instead. */
class LegacyTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy_tag'; }
}
class ActiveTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'active_tag'; }
}"#,
    );
    (dir, index)
}

fn assert_deprecated_hint(problem: &Problem) {
    assert_eq!(problem.severity, Some(protocol::DiagnosticSeverity::HINT));
    assert_eq!(problem.tags, vec![protocol::DiagnosticTag::DEPRECATED]);
}

#[test]
fn twig_deprecation_diagnostics_for_functions_filters_and_tests() {
    let (_dir, index) = deprecation_index();
    let document = doc(
        "file:///project/templates/page.html.twig",
        "{{ legacy_function() }}
{{ active_function() }}
{{ value|legacy_filter }}
{{ value|active_filter }}
{% if value is legacy_test %}{% endif %}
{% if value is active_test %}{% endif %}
",
    );
    let result = analyze(&TwigDeprecationAnalyzer::new(Some(index)), &document);
    assert_eq!(result.len(), 3, "{result:?}");
    assert_eq!(result[0].id.as_str(), "twig.function.deprecated");
    assert_eq!(range_text(&document, result[0].range), "legacy_function");
    assert!(result[0].message.contains("Use active_function"));
    assert_eq!(result[1].id.as_str(), "twig.filter.deprecated");
    assert_eq!(range_text(&document, result[1].range), "legacy_filter");
    assert_eq!(result[2].id.as_str(), "twig.test.deprecated");
    assert_eq!(range_text(&document, result[2].range), "legacy_test");
    for problem in &result {
        assert_deprecated_hint(problem);
    }
}

#[test]
fn twig_deprecation_diagnostics_respect_active_duplicate() {
    let (_dir, index) = deprecation_index();
    index_file(
        index.as_ref(),
        "/project/src/OverrideExtension.php",
        r#"<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class OverrideExtension extends AbstractExtension {
    public function getFunctions(): array {
        return [new TwigFunction('legacy_function', $this->active(...))];
    }
    public function active(): string { return ''; }
}"#,
    );
    let document = doc(
        "file:///project/templates/page.html.twig",
        "{{ legacy_function() }}",
    );
    assert!(analyze(&TwigDeprecationAnalyzer::new(Some(index)), &document).is_empty());
}

#[test]
fn twig_deprecation_diagnostics_for_custom_tags() {
    let (_dir, index) = deprecation_index();
    let document = doc(
        "file:///project/templates/page.html.twig",
        "{% legacy_tag %}
{% endlegacy_tag %}
{% active_tag %}
{% raw %}{% legacy_tag %}{% endraw %}
{% verbatim %}{% legacy_tag %}{% endverbatim %}
",
    );
    let result = analyze(&TwigDeprecationAnalyzer::new(Some(index)), &document);
    assert_eq!(result.len(), 2, "{result:?}");
    for (problem, expected) in result.iter().zip(["legacy_tag", "endlegacy_tag"]) {
        assert_eq!(problem.id.as_str(), "twig.tag.deprecated");
        assert_eq!(range_text(&document, problem.range), expected);
        assert!(problem.message.contains("modern_tag"));
        assert_deprecated_hint(problem);
    }
}

#[test]
fn twig_tag_deprecation_diagnostics_respect_active_duplicate() {
    let (_dir, index) = deprecation_index();
    index_file(
        index.as_ref(),
        "/project/src/OverrideTokenParser.php",
        r#"<?php
class OverrideTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy_tag'; }
}"#,
    );
    let document = doc(
        "file:///project/templates/page.html.twig",
        "{% legacy_tag %}",
    );
    assert!(analyze(&TwigDeprecationAnalyzer::new(Some(index)), &document).is_empty());
}
