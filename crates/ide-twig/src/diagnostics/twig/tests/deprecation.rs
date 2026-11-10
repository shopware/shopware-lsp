use std::sync::Arc;

use swlsp_lsp_protocol as protocol;
use swlsp_twig::TwigIndexer;

use super::*;
use crate::diagnostics::twig::TwigDeprecationAnalyzer;

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
    let (_, result) = check_problems(
        &TwigDeprecationAnalyzer::new(Some(index)),
        "file:///project/templates/page.html.twig",
        "{{ {|twig.function.deprecated:legacy_function|}() }}
{{ active_function() }}
{{ value|{|twig.filter.deprecated:legacy_filter|} }}
{{ value|active_filter }}
{% if value is {|twig.test.deprecated:legacy_test|} %}{% endif %}
{% if value is active_test %}{% endif %}
",
    );
    assert!(result[0].message.contains("Use active_function"));
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
    check_problems(
        &TwigDeprecationAnalyzer::new(Some(index)),
        "file:///project/templates/page.html.twig",
        "{{ legacy_function() }}",
    );
}

#[test]
fn twig_deprecation_diagnostics_for_custom_tags() {
    let (_dir, index) = deprecation_index();
    let (_, result) = check_problems(
        &TwigDeprecationAnalyzer::new(Some(index)),
        "file:///project/templates/page.html.twig",
        "{% {|twig.tag.deprecated:legacy_tag|} %}
{% {|twig.tag.deprecated:endlegacy_tag|} %}
{% active_tag %}
{% raw %}{% legacy_tag %}{% endraw %}
{% verbatim %}{% legacy_tag %}{% endverbatim %}
",
    );
    for problem in &result {
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
    check_problems(
        &TwigDeprecationAnalyzer::new(Some(index)),
        "file:///project/templates/page.html.twig",
        "{% legacy_tag %}",
    );
}
