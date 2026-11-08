//! Go `twig_extension_attribute_codeaction_test.go`.

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context, TextDocument};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_php::PhpIndex;

use super::*;
use crate::test_support::request_at;

const URI: &str = "file:///project/src/TestCommand.php";

/// Go `commandInvokeParameterRequest`: selects `needle`.
fn request(source: &str, needle: &str) -> CodeActionRequest {
    let offset = source.find(needle).unwrap() as u32;
    request_at(URI, source, offset, offset + needle.len() as u32)
}

fn fixture() -> (tempfile::TempDir, TwigExtensionAttributeCodeActionProvider) {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/vendor/TwigExtensionStubs.php".to_owned(),
            br"<?php
namespace Twig\Extension;
abstract class AbstractExtension {}
namespace App\Twig;
abstract class BaseExtension extends \Twig\Extension\AbstractExtension {}
"
            .to_vec(),
        ))
        .unwrap();
    (
        dir,
        TwigExtensionAttributeCodeActionProvider::new(Some(index)),
    )
}

fn actions(source: &str) -> Vec<protocol::CodeAction> {
    let (_dir, provider) = fixture();
    provider.get_code_actions(&Context::background(), &request(source, "MyExtension"))
}

fn apply(source: &str) -> String {
    let actions = actions(source);
    assert_eq!(actions.len(), 1);
    let edits = &actions[0].edit.as_ref().unwrap().changes[URI];
    assert_eq!(edits.len(), 1);
    let rewritten = TextDocument::new(URI, &edits[0].new_text, 2);
    assert!(rewritten.parse_errors().is_empty(), "{}", edits[0].new_text);
    edits[0].new_text.clone()
}

#[test]
fn recognizes_registries() {
    for (registry, registration, target) in [
        ("getFilters", "TwigFilter", "filterValue"),
        ("getFunctions", "TwigFunction", "functionValue"),
        ("getTests", "TwigTest", "testValue"),
    ] {
        let source = format!(
            r"<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\{registration};
class MyExtension extends AbstractExtension
{{
    public function {registry}(): array
    {{
        return [
            new {registration}('twig_name', [$this, '{target}']),
        ];
    }}

    public function {target}(mixed $value): mixed
    {{
        return $value;
    }}
}}"
        );
        let actions = actions(&source);
        assert_eq!(actions.len(), 1, "{registry}");
        assert_eq!(actions[0].title, "Migrate to TwigExtension attributes");
        assert_eq!(actions[0].kind, Some(CodeActionKind::REFACTOR_REWRITE));
    }
}

#[test]
fn migrates_mixed_registries() {
    let result = apply(
        r"<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\Environment;
use Twig\TwigFilter;
use Twig\TwigFunction;
class MyExtension extends AbstractExtension
{
    public function getFilters(): array
    {
        return [
            new TwigFilter('filter_one', [$this, 'filterOne']),
            new TwigFilter('filter_two', [$this, 'filterTwo']),
        ];
    }

    public function getFunctions(): array
    {
        return [
            new TwigFunction('with_environment', $this->withEnvironment(...), [
                'needs_environment' => true,
                'needs_context' => true,
                'is_safe' => ['html'],
            ]),
        ];
    }

    public function filterOne(mixed $value): mixed { return $value; }
    public function filterTwo(mixed $value): mixed { return $value; }
    public function withEnvironment(
        Environment $environment,
        array $context,
        mixed $value,
    ): mixed {
        return $value;
    }
}",
    );
    assert!(result.contains("use Twig\\Attribute\\AsTwigFilter;"));
    assert!(result.contains("use Twig\\Attribute\\AsTwigFunction;"));
    assert!(result.contains("#[AsTwigFilter('filter_one')]"));
    assert!(result.contains("#[AsTwigFilter('filter_two')]"));
    assert!(result.contains(
        "#[AsTwigFunction('with_environment', needsEnvironment: true, needsContext: true, isSafe: ['html'])]"
    ));
    assert!(!result.contains("function getFilters"));
    assert!(!result.contains("function getFunctions"));
    assert!(!result.contains("extends AbstractExtension"));
    assert!(!result.contains("use Twig\\Extension\\AbstractExtension;"));
    assert!(result.contains("function filterOne"));
    assert!(result.contains("function withEnvironment"));
}

#[test]
fn preserves_partial_registry() {
    let result = apply(
        r"<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class MyExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction('local', [$this, 'local']),
            new TwigFunction('runtime', [Runtime::class, 'runtime']),
        ];
    }

    public function local(mixed $value): mixed { return $value; }
}",
    );
    assert!(result.contains("#[AsTwigFunction('local')]"));
    assert!(result.contains("function getFunctions"));
    assert!(result.contains("new TwigFunction('runtime'"));
    assert!(!result.contains("new TwigFunction('local'"));
    assert!(result.contains("extends AbstractExtension"));
    assert!(result.contains("use Twig\\Extension\\AbstractExtension;"));
}

#[test]
fn preserves_complex_options() {
    let result = apply(
        r"<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
use Twig\DeprecatedCallableInfo;
class MyExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction('safe', [$this, 'safe'], [
                'is_safe_callback' => [self::class, 'checkSafe'],
                'deprecation_info' => new DeprecatedCallableInfo('pkg', '1.0'),
            ]),
        ];
    }

    public function safe(mixed $value): mixed { return $value; }
    public static function checkSafe(): array { return []; }
}",
    );
    assert!(result.contains("isSafeCallback: [self::class, 'checkSafe']"));
    assert!(result.contains("deprecationInfo: new DeprecatedCallableInfo('pkg', '1.0')"));
}

#[test]
fn reuses_attribute_alias() {
    let result = apply(
        r"<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\Attribute\AsTwigFilter as FilterAttribute;
use Twig\TwigFilter;
class MyExtension extends AbstractExtension
{
    public function getFilters(): array
    {
        return [
            new TwigFilter('filter_name', [$this, 'filterValue']),
        ];
    }

    public function filterValue(mixed $value): mixed { return $value; }
}",
    );
    assert!(result.contains("#[FilterAttribute('filter_name')]"));
    assert_eq!(
        result
            .matches("use Twig\\Attribute\\AsTwigFilter as FilterAttribute;")
            .count(),
        1
    );
}

#[test]
fn rejects_unsafe_targets() {
    for (name, source) in [
        (
            "non extension",
            r"<?php
use Twig\TwigFunction;
class MyExtension
{
    public function getFunctions(): array
    {
        return [new TwigFunction('local', [$this, 'local'])];
    }
    public function local(): void {}
}",
        ),
        (
            "external callable only",
            r"<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class MyExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [new TwigFunction('runtime', [Runtime::class, 'run'])];
    }
}",
        ),
        (
            "missing target method",
            r"<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class MyExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [new TwigFunction('missing', [$this, 'missing'])];
    }
}",
        ),
        (
            "dynamic registry",
            r"<?php
use Twig\Extension\AbstractExtension;
class MyExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return $this->buildFunctions();
    }
}",
        ),
    ] {
        assert!(actions(source).is_empty(), "{name}");
    }
}

#[test]
fn keeps_extends_for_remaining_registry() {
    let result = apply(
        r"<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
class MyExtension extends AbstractExtension
{
    public function getFilters(): array
    {
        return [new TwigFilter('local', [$this, 'local'])];
    }

    public function getTests(): array
    {
        return [];
    }

    public function local(mixed $value): mixed { return $value; }
}",
    );
    assert!(!result.contains("function getFilters"));
    assert!(result.contains("function getTests"));
    assert!(result.contains("extends AbstractExtension"));
}

#[test]
fn supports_indirect_extension() {
    let result = apply(
        r"<?php
namespace App\Twig;
use Twig\TwigFilter;
class MyExtension extends BaseExtension
{
    public function getFilters(): array
    {
        return [new TwigFilter('local', [$this, 'local'])];
    }

    public function local(mixed $value): mixed { return $value; }
}",
    );
    assert!(result.contains("#[AsTwigFilter('local')]"));
    assert!(!result.contains("function getFilters"));
    assert!(result.contains("extends BaseExtension"));
}

#[test]
fn retains_shared_abstract_import() {
    let result = apply(
        r"<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
class MyExtension extends AbstractExtension
{
    public function getFilters(): array
    {
        return [new TwigFilter('local', [$this, 'local'])];
    }

    public function local(mixed $value): mixed { return $value; }
}

class OtherExtension extends AbstractExtension
{
}",
    );
    assert!(!result.contains("class MyExtension extends"));
    assert!(result.contains("class OtherExtension extends AbstractExtension"));
    assert!(result.contains("use Twig\\Extension\\AbstractExtension;"));
}

#[test]
fn lower_camel_case_matches_go() {
    assert_eq!(lower_camel_case("needs_environment"), "needsEnvironment");
    assert_eq!(lower_camel_case("Single"), "single");
    assert_eq!(lower_camel_case("IS-SAFE now"), "isSAFENow");
    assert_eq!(lower_camel_case("__"), "");
}
