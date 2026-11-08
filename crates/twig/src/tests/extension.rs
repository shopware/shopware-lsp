use std::collections::{BTreeSet, HashMap};

use swlsp_indexer::{Indexer, ParsedFile};

use crate::*;

fn callables(path: &str, source: &str) -> TwigCallables {
    parse_twig_extension(path, &swlsp_parser_php::parse(source).root(), source)
}

fn fixture(name: &str) -> (String, String) {
    let path = format!("{}/testdata/{name}", env!("CARGO_MANIFEST_DIR"));
    let content = std::fs::read_to_string(&path).unwrap();
    (path, content)
}

#[test]
fn parse_twig_extension_fixture() {
    let (path, content) = fixture("extension.php");
    let result = callables(&path, &content);
    let functions = &result.functions;
    assert_eq!(functions.len(), 2);
    assert_eq!(
        (functions[0].name.as_str(), functions[0].file_path.as_str()),
        ("test", path.as_str())
    );
    assert_eq!(functions[1].name, "test2");
    assert_eq!(functions[0].parameters.len(), 1);
    assert_eq!(functions[0].parameters[0].name, "$test");
    assert_eq!(functions[0].parameters[0].ty, "string");
    assert!(!functions[0].parameters[0].optional);
    let names: Vec<&str> = result.filters.iter().map(|f| f.name.as_str()).collect();
    assert_eq!(names, ["abs", "test", "test2"]);
    assert!(result.filters.iter().all(|f| f.file_path == path));
}

#[test]
fn parse_twig_extension_fixture2() {
    let (path, content) = fixture("extension2.php");
    let functions = callables(&path, &content).functions;
    assert_eq!(functions.len(), 2);
    assert_eq!(functions[0].name, "inAppPurchase");
    assert_eq!(functions[0].parameters.len(), 2);
    assert_eq!(
        (
            functions[0].parameters[0].ty.as_str(),
            functions[0].parameters[0].name.as_str()
        ),
        ("string", "$extensionName")
    );
    assert_eq!(
        (
            functions[0].parameters[1].ty.as_str(),
            functions[0].parameters[1].name.as_str()
        ),
        ("string", "$identifier")
    );
    assert_eq!(functions[1].name, "allInAppPurchases");
}

#[test]
fn parse_twig_operators_supports_legacy_and_expression_parser_apis() {
    let source = r#"<?php
use Twig\Extension\AbstractExtension;

class AppExtension extends AbstractExtension
{
    public function getOperators(): array
    {
        return [
            [
                'not' => ['precedence' => 50, 'class' => NotNode::class],
            ],
            [
                'or' => ['precedence' => 10, 'class' => OrNode::class],
            ],
        ];
    }

    public function getExpressionParsers(): array
    {
        return [
            new BinaryOperatorExpressionParser(AndNode::class, 'b-and', 18),
            new BinaryOperatorExpressionParser(
                ElvisNode::class,
                '?:',
                5,
                aliases: ['? :'],
            ),
            new BinaryOperatorExpressionParser(
                PositionalNode::class,
                'positioned',
                5,
                null,
                null,
                null,
                ['position alias'],
            ),
            new UnaryOperatorExpressionParser(NotNode::class, 'expression_not', 70),
            new UnaryOperatorExpressionParser(
                BangNode::class,
                'bang',
                70,
                null,
                null,
                ['! alias'],
            ),
            new UnrelatedParser(IgnoredNode::class, 'ignored', 10),
        ];
    }
}

class OtherExtension implements \Twig\Extension\ExtensionInterface
{
    public function getExpressionParsers(): array
    {
        if (feature_enabled()) {
            return [
                new BinaryOperatorExpressionParser(Node::class, 'inside-control-flow', 20),
            ];
        }
        return [];
    }
}

class NotAnExtension
{
    public function getExpressionParsers(): array
    {
        return [
            new BinaryOperatorExpressionParser(Node::class, 'not-an-extension', 20),
        ];
    }
}"#;
    let operators = parse_twig_operators(
        "/project/AppExtension.php",
        &swlsp_parser_php::parse(source).root(),
        source,
    );
    let mut by_name = HashMap::new();
    let mut names = Vec::new();
    for operator in &operators {
        assert_eq!(operator.name, &source[operator.range]);
        by_name.insert(operator.name.as_str(), operator);
        names.push(operator.name.as_str());
    }
    assert_eq!(names.len(), 11);
    assert_eq!(
        names.iter().copied().collect::<BTreeSet<_>>(),
        BTreeSet::from([
            "not",
            "or",
            "b-and",
            "?:",
            "? :",
            "positioned",
            "position alias",
            "expression_not",
            "bang",
            "! alias",
            "inside-control-flow"
        ])
    );
    assert!(by_name["not"].legacy && by_name["not"].unary);
    assert!(by_name["or"].legacy && !by_name["or"].unary);
    assert!(by_name["? :"].alias && !by_name["? :"].unary);
    assert!(by_name["! alias"].alias && by_name["! alias"].unary);
}

#[test]
fn twig_indexer_persists_and_clears_operators() {
    let cache = tempfile::tempdir().unwrap();
    let dir = tempfile::tempdir().unwrap();
    let path = format!("{}/AppExtension.php", dir.path().display());
    let source = r#"<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getExpressionParsers(): array
    {
        return [
            new BinaryOperatorExpressionParser(Node::class, 'custom-op', 20, aliases: ['custom alias']),
        ];
    }
}"#;
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    index
        .index(&ParsedFile::new(path.clone(), source.as_bytes().to_vec()))
        .unwrap();
    let mut operators = index.get_all_twig_operators().unwrap();
    assert_eq!(operators.len(), 2);
    index.close().unwrap();
    let restored = TwigIndexer::new(cache.path(), None).unwrap();
    let mut restored_operators = restored.get_all_twig_operators().unwrap();
    operators.sort_by(|a, b| a.name.cmp(&b.name));
    restored_operators.sort_by(|a, b| a.name.cmp(&b.name));
    assert_eq!(operators, restored_operators);
    restored
        .index(&ParsedFile::new(
            path,
            b"<?php class AppExtension {}".to_vec(),
        ))
        .unwrap();
    assert!(restored.get_all_twig_operators().unwrap().is_empty());
    restored.close().unwrap();
}

#[test]
fn parse_twig_extension_attributes_without_abstract_extension() {
    let source = r#"<?php
namespace App\Twig;
use Twig\Attribute\AsTwigFilter as Filter;
use Twig\Attribute\AsTwigFunction;
use Twig\DeprecatedCallableInfo;

class AppExtension
{
    #[AsTwigFunction(
        name: 'product_url',
        deprecationInfo: new DeprecatedCallableInfo('app', '2.0', 'new_url'),
    )]
    public function productUrl(string $id, bool $absolute = false): string
    {
        return '';
    }

    #[Filter('price')]
    public function price(float $value): string
    {
        return '';
    }
}"#;
    let result = callables("AppExtension.php", source);
    assert_eq!(result.functions.len(), 1);
    assert_eq!(result.filters.len(), 1);
    let function = &result.functions[0];
    assert_eq!(function.name, "product_url");
    assert_eq!(function.method, r"App\Twig\AppExtension::productUrl");
    assert_eq!(function.usage, "product_url($id, $absolute)");
    assert_eq!(function.parameters.len(), 2);
    assert!(function.parameters[1].optional);
    assert!(function.deprecated);
    assert!(function.deprecation.contains("app 2.0"));
    assert!(function.deprecation.contains("new_url"));
    assert_eq!(result.filters[0].name, "price");
    assert_eq!(result.filters[0].method, r"App\Twig\AppExtension::price");
    assert_eq!(result.filters[0].usage, "price($value)");
}

#[test]
fn parse_twig_tests_from_registries_and_attributes() {
    let source = r#"<?php
namespace App\Twig;

use Twig\Attribute\AsTwigTest;
use Twig\Extension\AbstractExtension;
use Twig\TwigTest;
use App\Twig\Node\PositiveNode;

class AppExtension extends AbstractExtension
{
    public function getTests(): array
    {
        return [
            new TwigTest('positive', $this->positive(...)),
            new \Twig_SimpleTest('legacy_even', [$this, 'even']),
            new \Twig_Test('legacy_test', [$this, 'even']),
            new TwigTest('node_positive', null, [
                'node_class' => PositiveNode::class,
            ]),
        ];
    }

    public function positive(int $value): bool { return $value > 0; }
    public function even(int $value): bool { return $value % 2 === 0; }
}

class AttributeTests
{
    #[AsTwigTest('uuid')]
    public function isUuid(string $value): bool { return true; }
}"#;
    let tests = parse_twig_tests(
        "/project/src/AppExtension.php",
        &swlsp_parser_php::parse(source).root(),
        source,
    );
    assert_eq!(tests.len(), 5);
    let by_name: HashMap<&str, &TwigTest> = tests
        .iter()
        .map(|test| (test.name.as_str(), test))
        .collect();
    assert_eq!(by_name["positive"].method, "$this->positive");
    assert_eq!(by_name["positive"].usage, "positive($value)");
    assert_eq!(by_name["positive"].parameters.len(), 1);
    assert_eq!(by_name["positive"].parameters[0].ty, "int");
    assert_eq!(by_name["legacy_even"].method, "$this->even");
    assert_eq!(by_name["legacy_test"].method, "$this->even");
    assert_eq!(
        by_name["node_positive"].method,
        r"App\Twig\Node\PositiveNode::compile"
    );
    assert_eq!(by_name["node_positive"].usage, "node_positive()");
    assert_eq!(by_name["uuid"].method, r"App\Twig\AttributeTests::isUuid");
    assert_eq!(by_name["uuid"].usage, "uuid($value)");
}

#[test]
fn twig_indexer_persists_and_clears_tests() {
    let cache = tempfile::tempdir().unwrap();
    let dir = tempfile::tempdir().unwrap();
    let path = format!("{}/AppExtension.php", dir.path().display());
    let source = r#"<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getTests(): array
    {
        return [new \Twig\TwigTest('positive', $this->positive(...))];
    }

    public function positive(int $value): bool { return $value > 0; }
}"#;
    let index = TwigIndexer::new(cache.path(), None).unwrap();
    index
        .index(&ParsedFile::new(path.clone(), source.as_bytes().to_vec()))
        .unwrap();
    let tests = index.get_all_twig_tests().unwrap();
    assert_eq!(tests.len(), 1);
    assert_eq!(tests[0].name, "positive");
    index.close().unwrap();
    let restored = TwigIndexer::new(cache.path(), None).unwrap();
    assert_eq!(restored.get_all_twig_tests().unwrap(), tests);
    restored
        .index(&ParsedFile::new(
            path,
            b"<?php class AppExtension {}".to_vec(),
        ))
        .unwrap();
    assert!(restored.get_all_twig_tests().unwrap().is_empty());
    restored.close().unwrap();
}

#[test]
fn parse_deprecated_twig_extension_callables() {
    let source = r#"<?php
namespace App\Twig;
use Twig\DeprecatedCallableInfo;
use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
use Twig\TwigFunction;

class AppExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction(
                'legacy_option',
                $this->active(...),
                ['deprecated' => '1.2', 'alternative' => 'modern'],
            ),
            new TwigFunction(
                'legacy_info',
                $this->active(...),
                ['deprecation_info' => new DeprecatedCallableInfo('app', '2.0', 'modern')],
            ),
            new TwigFunction('legacy_trigger', $this->legacyTrigger(...)),
            new TwigFunction('legacy_method_trigger', $this->legacyMethodTrigger(...)),
            new TwigFunction('legacy_generated', $this->legacyGenerated(...)),
            new TwigFunction('argument_warning', $this->argumentWarning(...)),
            new TwigFunction('active', $this->active(...)),
        ];
    }

    public function getFilters(): array
    {
        return [
            new TwigFilter('legacy_filter', $this->legacyFilter(...)),
        ];
    }

    public function active(): string { return ''; }

    public function legacyTrigger(): string
    {
        trigger_deprecation(
            'app',
            '1.0',
            'The "legacy_trigger" Twig function is deprecated. Use modern instead.',
        );
        return '';
    }

    public function legacyMethodTrigger(): string
    {
        $info = new DeprecatedCallableInfo('app', '1.0');
        $info->triggerDeprecation(__FILE__, __LINE__);
        return '';
    }

    public function legacyGenerated(): string
    {
        Feature::triggerDeprecationOrThrow(
            'v2',
            Feature::deprecatedMethodMessage(self::class, __METHOD__, 'v2', 'modern'),
        );
        return '';
    }

    public function argumentWarning(mixed $value): string
    {
        if (!is_string($value)) {
            Feature::triggerDeprecationOrThrow(
                'v2',
                'Passing a non-string value is deprecated.',
            );
        }
        return '';
    }

    public function legacyFilter(string $value): string
    {
        Feature::triggerDeprecationOrThrow(
            'v2',
            'The "legacy_filter" Twig filter is deprecated. Use active_filter instead.',
        );
        return $value;
    }
}"#;
    let result = callables("AppExtension.php", source);
    assert_eq!(result.functions.len(), 7);
    assert_eq!(result.filters.len(), 1);
    let by: HashMap<&str, &TwigFunction> = result
        .functions
        .iter()
        .map(|f| (f.name.as_str(), f))
        .collect();
    assert!(by["legacy_option"].deprecated);
    assert!(by["legacy_option"].deprecation.contains("modern"));
    assert!(!by["legacy_option"].deprecated_range.is_empty());
    assert!(by["legacy_info"].deprecated);
    assert!(by["legacy_info"].deprecation.contains("app 2.0"));
    assert!(by["legacy_info"].deprecation.contains("modern"));
    assert!(by["legacy_trigger"].deprecated);
    assert!(by["legacy_trigger"].deprecation.contains("legacy_trigger"));
    assert!(by["legacy_method_trigger"].deprecated);
    assert!(by["legacy_method_trigger"].deprecation.is_empty());
    assert!(by["legacy_generated"].deprecated);
    assert!(by["legacy_generated"].deprecation.contains("modern"));
    assert!(!by["argument_warning"].deprecated);
    assert!(!by["active"].deprecated);
    assert!(result.filters[0].deprecated);
    assert!(result.filters[0].deprecation.contains("active_filter"));
}

#[test]
fn parse_deprecated_legacy_twig_extension_interface() {
    let source = r#"<?php
use Twig\TwigFunction;
class LegacyExtension implements \Twig_ExtensionInterface
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction(
                'legacy_callable_info',
                [self::class, 'legacyCallableInfo'],
            ),
        ];
    }

    public static function legacyCallableInfo(): string
    {
        $info = new \Twig\DeprecatedCallableInfo('twig/twig', '3.12');
        $info->triggerDeprecation(__FILE__, __LINE__);
        return '';
    }
}"#;
    let result = callables("LegacyExtension.php", source);
    assert_eq!(result.functions.len(), 1);
    assert_eq!(result.functions[0].name, "legacy_callable_info");
    assert!(result.functions[0].deprecated);
    assert!(result.functions[0].deprecation.is_empty());
    assert!(result.filters.is_empty());
}

#[test]
fn parse_twig_extension_not_extending() {
    let source = r#"<?php
namespace App\Twig;

class NotExtension
{
    public function test() {}
}"#;
    let result = callables("not_extension.php", source);
    assert!(result.functions.is_empty());
    assert!(result.filters.is_empty());
}
