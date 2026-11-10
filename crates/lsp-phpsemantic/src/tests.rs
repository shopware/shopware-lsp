//! Tests for the PHP semantic providers: diagnostics, completion and
//! imports, navigation, hierarchy and the editor features.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    CancellationToken, CompletionProvider, CompletionRequest, Context, DefinitionRequest,
    DocumentHighlightProvider, DocumentHighlightRequest, DocumentSymbolProvider,
    DocumentSymbolRequest, GotoDefinitionProvider, HoverProvider, HoverRequest,
    ImplementationProvider, ImplementationRequest, Problem, ReferenceRequest, ReferencesProvider,
    RenameProvider, RenameRequest, SignatureHelpProvider, SignatureHelpRequest, SyntaxContext,
    TextDocument, TypeHierarchyPrepareRequest, TypeHierarchyProvider, is_cancellation,
};
use swlsp_lsp_protocol::{
    self as protocol, CompletionItem, DiagnosticSeverity, DocumentHighlightKind, DocumentSymbol,
    Position, Range, SymbolKind, TextDocumentIdentifier, TextEdit,
};
use swlsp_php::PhpIndex;
use swlsp_php_project::{Model, Package};
use swlsp_syntax::{SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;
use tempfile::TempDir;

use crate::Provider;
use crate::completion_imports::{Packages, completion_package};

// ---------------------------------------------------------------------------
// Helpers

fn tempdir() -> TempDir {
    tempfile::tempdir().unwrap()
}

fn open_index() -> (TempDir, PhpIndex) {
    let dir = tempdir();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    (dir, index)
}

fn index_file(index: &PhpIndex, path: &Path, source: &str) {
    index
        .index(&ParsedFile::new(
            path.to_string_lossy().as_ref(),
            source.as_bytes().to_vec(),
        ))
        .unwrap();
}

fn write(path: &Path, source: &str) {
    std::fs::write(path, source).unwrap();
}

fn document(path: &Path, source: &str, version: i32) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(
        file_uri(&path.to_string_lossy()),
        source,
        version,
    ))
}

/// The syntax context of `document`: the node and token at a byte offset.
fn syntax(document: &Arc<TextDocument>, offset: u32) -> SyntaxContext {
    let root = document.root().unwrap();
    let at = TextSize::from(offset);
    SyntaxContext {
        document: Some(document.clone()),
        language: Some(LanguageId::Php),
        token: root.token_at(at),
        node: root.node_at(at),
        root: Some(root),
    }
}

fn byte_offset(source: &str, needle: &str) -> u32 {
    source.find(needle).map_or(0, |index| index as u32)
}

fn position(document: &TextDocument, offset: u32) -> Position {
    let (line, character) = document.line_index().position_utf16(offset);
    Position { line, character }
}

fn identifier(document: &TextDocument) -> TextDocumentIdentifier {
    TextDocumentIdentifier::new(document.uri.clone())
}

fn problem_start_line(document: &TextDocument, problem: &Problem) -> u32 {
    document
        .line_index()
        .position_utf16(problem.range.start().into())
        .0
}

fn source_range(document: &TextDocument, range: Range) -> String {
    let start = document
        .line_index()
        .offset_utf16(range.start.line, range.start.character) as usize;
    let end = document
        .line_index()
        .offset_utf16(range.end.line, range.end.character) as usize;
    document.source()[start..end].to_owned()
}

fn ctx() -> Context {
    Context::background()
}

fn analyze(provider: &Provider, document: &TextDocument) -> Vec<Problem> {
    provider.analyze(&ctx(), document).unwrap()
}

fn messages(problems: &[Problem], id: Option<&str>) -> Vec<String> {
    problems
        .iter()
        .filter(|problem| id.is_none_or(|id| problem.id.as_str() == id))
        .map(|problem| problem.message.clone())
        .collect()
}

fn ids(problems: &[Problem]) -> Vec<&str> {
    problems.iter().map(|problem| problem.id.as_str()).collect()
}

fn completion_request(document: &Arc<TextDocument>, offset: u32) -> CompletionRequest {
    CompletionRequest {
        params: protocol::CompletionParams {
            text_document: identifier(document),
            position: position(document, offset),
            ..Default::default()
        },
        syntax: syntax(document, offset),
    }
}

fn completions(
    provider: &Provider,
    document: &Arc<TextDocument>,
    offset: u32,
) -> Vec<CompletionItem> {
    provider.get_completions(&ctx(), &completion_request(document, offset))
}

fn labels(items: &[CompletionItem]) -> Vec<String> {
    items.iter().map(|item| item.label.clone()).collect()
}

fn text_edit(item: &CompletionItem) -> TextEdit {
    item.text_edit.clone().expect("text edit")
}

fn edit_source(document: &TextDocument, edit: &TextEdit) -> String {
    source_range(document, edit.range)
}

fn definitions(
    provider: &Provider,
    document: &Arc<TextDocument>,
    offset: u32,
) -> Vec<protocol::Location> {
    provider.get_definition(
        &ctx(),
        &DefinitionRequest {
            params: protocol::DefinitionParams {
                text_document: identifier(document),
                position: position(document, offset),
            },
            syntax: syntax(document, offset),
        },
    )
}

fn assert_elements_match(mut actual: Vec<String>, expected: &[&str]) {
    let mut expected: Vec<String> = expected.iter().map(|value| (*value).to_owned()).collect();
    actual.sort();
    expected.sort();
    assert_eq!(actual, expected);
}

// ---------------------------------------------------------------------------
// Diagnostics, completion and navigation

#[test]
fn provider_member_features() {
    let source = "<?php
namespace App;
class Service {
    /** Returns the display value. */
    public function value(string $prefix = ''): string { return $prefix . 'ok'; }
}
class Consumer {
    public function __construct(private Service $service) {}
    public function run(): string { return $this->service->value(); }
}";
    let dir = tempdir();
    let path = dir.path().join("Member.php");
    write(&path, source);
    let (_cache, index) = open_index();
    index_file(&index, &path, source);
    let document = document(&path, source, 1);
    let provider = Provider::new(Some(index.clone()));

    let offset = byte_offset(source, "value();");
    let hover = provider
        .get_hover(
            &ctx(),
            &HoverRequest {
                params: protocol::HoverParams {
                    text_document: identifier(&document),
                    position: position(&document, offset),
                    ..Default::default()
                },
                syntax: syntax(&document, offset),
            },
        )
        .unwrap()
        .unwrap();
    assert!(hover.contents.value.contains("function value"));
    assert!(hover.contents.value.contains("Returns the display value"));

    let definitions = definitions(&provider, &document, offset);
    assert_eq!(definitions.len(), 1);
    assert_eq!(definitions[0].uri, document.uri);
    assert_eq!(definitions[0].range.start.line, 4);

    let references = provider
        .get_references(
            &ctx(),
            &ReferenceRequest {
                params: protocol::ReferenceParams {
                    text_document: identifier(&document),
                    position: position(&document, offset),
                    context: protocol::ReferenceContext {
                        include_declaration: true,
                    },
                },
                syntax: syntax(&document, offset),
            },
        )
        .unwrap();
    assert!(references.len() >= 2);

    let items = completions(&provider, &document, offset);
    assert!(labels(&items).contains(&"value".to_owned()));

    let signature_offset = offset + "value(".len() as u32;
    let signature = provider
        .get_signature_help(
            &ctx(),
            &SignatureHelpRequest {
                params: protocol::SignatureHelpParams {
                    text_document: identifier(&document),
                    position: position(&document, signature_offset),
                    ..Default::default()
                },
                syntax: syntax(&document, offset),
            },
        )
        .unwrap()
        .unwrap();
    assert_eq!(signature.signatures.len(), 1);
    assert!(signature.signatures[0].label.contains("$prefix = ..."));

    let edit = provider
        .rename(
            &ctx(),
            &RenameRequest {
                params: protocol::RenameParams {
                    text_document: identifier(&document),
                    position: position(&document, offset),
                    new_name: "displayValue".into(),
                },
                syntax: syntax(&document, offset),
            },
        )
        .unwrap()
        .unwrap();
    let edits = &edit.changes[&document.uri];
    assert_eq!(edits.len(), 2);
    for edit in edits {
        assert_eq!(edit.new_text, "displayValue");
    }
    index.close().unwrap();
}

#[test]
fn provider_completes_phpstorm_expected_arguments() {
    let root = tempdir();
    let meta_source = "<?php
namespace PHPSTORM_META;
registerArgumentsSet('modes', 'fast', \\MODE_SAFE);
expectedArguments(\\configure(), 0, argumentsSet('modes'));
";
    let (_cache, index) = open_index();
    index_file(&index, &root.path().join(".phpstorm.meta.php"), meta_source);

    let source = "<?php configure('fa');";
    let document = document(&root.path().join("Usage.php"), source, 1);
    let offset = (source.find("fa'").unwrap() + "fa".len()) as u32;
    let items = completions(&Provider::new(Some(index.clone())), &document, offset);
    assert_elements_match(labels(&items), &["fast", "\\MODE_SAFE"]);
    let item = items
        .iter()
        .find(|item| item.label == "fast")
        .expect("fast");
    let edit = text_edit(item);
    assert_eq!(edit.new_text, "'fast'");
    assert_eq!(edit_source(&document, &edit), "'fa'");
    index.close().unwrap();
}

#[test]
fn provider_completes_framework_arguments_from_metadata() {
    let root = tempdir();
    let (_cache, index) = open_index();
    let declarations = "<?php
namespace Symfony\\Contracts\\Service;
interface ServiceProviderInterface { public function get(string $id): object; }
namespace Shopware\\Core\\Framework\\DependencyInjection;
final class Container implements \\Symfony\\Contracts\\Service\\ServiceProviderInterface {
    public function get(string $id): object {}
}
namespace Shopware\\Core\\Framework\\Adapter;
final class Factory { public static function create(string $type): object {} }
";
    let meta = "<?php
namespace PHPSTORM_META;
registerArgumentsSet('shopware.services', 'product.repository', 'event_dispatcher');
expectedArguments(
    \\Symfony\\Contracts\\Service\\ServiceProviderInterface::get(),
    0,
    argumentsSet('shopware.services'),
);
expectedArguments(
    \\Shopware\\Core\\Framework\\Adapter\\Factory::create(),
    0,
    'rule',
    'flow',
);
";
    index_file(&index, &root.path().join("Framework.php"), declarations);
    index_file(&index, &root.path().join(".phpstorm.meta.php"), meta);

    for (name, source, needle, expected) in [
        (
            "Symfony_service_provider",
            "<?php
use Shopware\\Core\\Framework\\DependencyInjection\\Container;
function run(Container $container): void { $container->get('prod'); }
",
            "prod",
            &["product.repository", "event_dispatcher"][..],
        ),
        (
            "Shopware_factory",
            "<?php
use Shopware\\Core\\Framework\\Adapter\\Factory;
Factory::create('ru');
",
            "ru",
            &["rule", "flow"][..],
        ),
    ] {
        let document = document(&root.path().join(format!("{name}.php")), source, 1);
        let offset = (source.rfind(needle).unwrap() + needle.len()) as u32;
        let items = completions(&Provider::new(Some(index.clone())), &document, offset);
        assert_elements_match(labels(&items), expected);
    }
    index.close().unwrap();
}

#[test]
fn provider_completes_expected_values_attribute() {
    let root = tempdir();
    let declaration = "<?php
namespace App;
final class Modes {
    public const SAFE = 1;
    public const FAST = 2;
}
function configure(
    #[\\JetBrains\\PhpStorm\\ExpectedValues(
        values: ['auto', 7],
        valuesFromClass: Modes::class,
    )]
    string|int $mode,
): void {}
";
    let (_cache, index) = open_index();
    index_file(&index, &root.path().join("Functions.php"), declaration);

    let usage = "<?php namespace App; configure('au');";
    let document = document(&root.path().join("Usage.php"), usage, 1);
    let offset = (usage.find("au'").unwrap() + "au".len()) as u32;
    let items = completions(&Provider::new(Some(index.clone())), &document, offset);
    assert_elements_match(
        labels(&items),
        &["auto", "7", "\\App\\Modes::SAFE", "\\App\\Modes::FAST"],
    );
    let item = items
        .iter()
        .find(|item| item.label == "auto")
        .expect("auto");
    assert_eq!(text_edit(item).new_text, "'auto'");
    index.close().unwrap();
}

#[test]
fn provider_completes_expected_values_from_generated_stubs() {
    let root = tempdir();
    write(
        &root.path().join("composer.json"),
        r#"{"require":{"php":"^8.3"}}"#,
    );
    let (_cache, index) = open_index();
    index.configure_project(root.path()).unwrap();

    let source = "<?php pathinfo('/tmp/file', PATH);";
    let document = document(&root.path().join("Usage.php"), source, 1);
    let offset = (source.find("PATH)").unwrap() + "PATH".len()) as u32;
    let labels = labels(&completions(
        &Provider::new(Some(index.clone())),
        &document,
        offset,
    ));
    for expected in [
        "PATHINFO_DIRNAME",
        "PATHINFO_BASENAME",
        "PATHINFO_EXTENSION",
        "PATHINFO_FILENAME",
    ] {
        assert!(labels.contains(&expected.to_owned()), "{expected}");
    }
    index.close().unwrap();
}

#[test]
fn provider_diagnostics() {
    let source = "<?php
namespace App;
class Consumer {
    public function run(): void {
        $value = new MissingType();
        echo $missing;
    }
}";
    let dir = tempdir();
    let (_cache, index) = open_index();
    let document = document(&dir.path().join("Diagnostics.php"), source, 1);
    let messages = messages(
        &analyze(&Provider::new(Some(index.clone())), &document),
        None,
    );
    assert!(messages.contains(&"Undefined class MissingType".to_owned()));
    assert!(messages.contains(&"Undefined variable $missing".to_owned()));
    index.close().unwrap();
}

#[test]
fn provider_reports_phpdoc_final_extension() {
    let root = tempdir();
    let declaration = "<?php
namespace Vendor;
/** @final */
class ExtensionPoint {}
/** @final */
final class ClosedExtensionPoint {}
";
    let (_cache, index) = open_index();
    index_file(&index, &root.path().join("ExtensionPoint.php"), declaration);

    let usage = "<?php
namespace App;
use Vendor\\ExtensionPoint;
use Vendor\\ClosedExtensionPoint;

/** @phpstan-ignore class.extendsFinal */
class Suppressed extends ExtensionPoint {}

class Invalid extends ExtensionPoint {}

class NativeInvalid extends ClosedExtensionPoint {}

function consume(ExtensionPoint $extension): void {}
";
    let document = document(&root.path().join("Usage.php"), usage, 1);
    let problems = analyze(&Provider::new(Some(index.clone())), &document);
    let inheritance: Vec<&Problem> = problems
        .iter()
        .filter(|problem| problem.id.as_str() == "php.inheritance")
        .collect();
    assert_eq!(inheritance.len(), 2);
    let warning: Vec<&&Problem> = inheritance
        .iter()
        .filter(|problem| problem.severity == Some(DiagnosticSeverity::WARNING))
        .collect();
    let errors = inheritance
        .iter()
        .filter(|problem| problem.severity == Some(DiagnosticSeverity::ERROR))
        .count();
    assert_eq!(warning.len(), 1);
    assert_eq!(errors, 1);
    let problem = warning[0];
    assert_eq!(
        problem.message,
        "Class Vendor\\ExtensionPoint is marked @final and should not be extended"
    );
    assert_eq!(
        &usage[usize::from(problem.range.start())..usize::from(problem.range.end())],
        "ExtensionPoint"
    );
    assert_eq!(problem_start_line(&document, problem), 8);
    index.close().unwrap();
}

#[test]
fn provider_diagnoses_only_explicitly_unavailable_php_extensions() {
    let root = tempdir();
    write(
        &root.path().join("composer.json"),
        r#"{"require":{"php":"^8.3"}}"#,
    );
    let (_cache, index) = open_index();
    index
        .configure_project_with_extensions(root.path(), &[] as &[&str], &["dom", "curl"])
        .unwrap();

    let source = "<?php
$document = new DOMDocument();
$handle = curl_init();
$optional = new Imagick();
";
    let document = document(&root.path().join("Runtime.php"), source, 1);
    let problems = analyze(&Provider::new(Some(index.clone())), &document);
    for message in messages(&problems, Some("php.undefined")) {
        assert!(!message.contains("Imagick"), "{message}");
    }
    assert_elements_match(
        messages(&problems, Some("php.extension")),
        &[
            "DOMDocument requires disabled PHP extension ext-dom",
            "curl_init requires disabled PHP extension ext-curl",
        ],
    );
    index.close().unwrap();
}

#[test]
fn provider_reports_structured_deprecated_attribute() {
    let root = tempdir();
    let declaration = "<?php
namespace App;
#[\\JetBrains\\PhpStorm\\Deprecated(
    reason: 'It loses precision',
    replacement: 'modern()',
    since: '2.0',
)]
function legacy(): int { return 1; }
";
    let (_cache, index) = open_index();
    index_file(&index, &root.path().join("Legacy.php"), declaration);

    let usage = "<?php namespace App; $value = legacy();";
    let document = document(&root.path().join("Usage.php"), usage, 1);
    let provider = Provider::new(Some(index.clone()));
    let deprecated = messages(&analyze(&provider, &document), Some("php.deprecated"));
    assert_eq!(
        deprecated.first().map(String::as_str),
        Some("legacy is deprecated since 2.0: It loses precision; use modern()")
    );

    let offset = byte_offset(usage, "legacy()");
    let hover = provider
        .get_hover(
            &ctx(),
            &HoverRequest {
                params: protocol::HoverParams {
                    text_document: identifier(&document),
                    position: position(&document, offset),
                    ..Default::default()
                },
                syntax: syntax(&document, offset),
            },
        )
        .unwrap()
        .unwrap();
    assert!(hover.contents.value.contains("**Deprecated** since 2.0"));
    assert!(hover.contents.value.contains("It loses precision"));
    assert!(hover.contents.value.contains("Replacement: `modern()`"));
    index.close().unwrap();
}

fn analyze_source(name: &str, source: &str, indexed: bool) -> Vec<Problem> {
    let dir = tempdir();
    let path = dir.path().join(name);
    let (_cache, index) = open_index();
    if indexed {
        write(&path, source);
        index_file(&index, &path, source);
    }
    let document = document(&path, source, 1);
    let problems = analyze(&Provider::new(Some(index.clone())), &document);
    index.close().unwrap();
    problems
}

#[test]
fn provider_allows_subclass_late_static_members() {
    let source = "<?php
abstract class Extension {
    public static function name(): string { return static::NAME; }
}
final class ClosedExtension {
    public static function name(): string { return static::MISSING; }
}";
    let problems = analyze_source("LateStatic.php", source, false);
    assert_eq!(
        messages(&problems, Some("php.undefined")),
        vec!["Undefined class constant MISSING on ClosedExtension".to_owned()]
    );
}

#[test]
fn provider_honors_method_exists_guards() {
    let source = "<?php
class Entity {}
function guardedReturn(Entity $entity): void {
    if (!method_exists($entity, 'dynamic')) {
        return;
    }
    $entity->dynamic();
}
function guardedBranch(Entity $entity): void {
    if ($entity && method_exists($entity, 'dynamic')) {
        $entity->dynamic();
    }
}
function guardedTernary(Entity $entity): mixed {
    return method_exists($entity, 'dynamic') ? $entity->dynamic() : null;
}
/** @param list<Entity> $entities */
function guardedContinue(array $entities): void {
    foreach ($entities as $entity) {
        if (!\\method_exists($entity, 'dynamic')) {
            continue;
        }
        $entity->dynamic();
    }
}
function unguarded(Entity $entity): void {
    $entity->missing();
}
function reassigned(Entity $entity): void {
    if (!method_exists($entity, 'dynamic')) {
        return;
    }
    $entity = new Entity();
    $entity->dynamic();
}";
    let problems = analyze_source("MethodExists.php", source, true);
    assert_elements_match(
        messages(&problems, Some("php.undefined")),
        &[
            "Undefined method missing on Entity",
            "Undefined method dynamic on Entity",
        ],
    );
}

#[test]
fn provider_honors_class_existence_guards() {
    let source = "<?php
namespace App;
use Optional\\Package\\Contract;
use Optional\\Package\\Runtime;
use Tideways\\Profiler;
function guardedReturn(): void {
    if (!class_exists(Runtime::class)) {
        return;
    }
    Runtime::start();
}
function guardedInterface(): void {
    if (!interface_exists(Contract::class)) {
        return;
    }
    Contract::VERSION;
}
function guardedString(): void {
    if (!class_exists('Tideways\\Profiler')) {
        return;
    }
    Profiler::createSpan('request');
}
function guardedBranch(): void {
    if (class_exists(Runtime::class)) {
        Runtime::start();
    }
}
function guardedTernary(): mixed {
    return class_exists(Runtime::class) ? Runtime::start() : null;
}
function guardedOperand(object $value): bool {
    return class_exists('Optional\\Package\\Runtime') && $value instanceof Runtime
        || !class_exists(Runtime::class) || $value instanceof Runtime;
}
function unguarded(): void {
    Runtime::missing();
}";
    let problems = analyze_source("ClassExists.php", source, true);
    assert_eq!(
        messages(&problems, Some("php.undefined")),
        vec!["Undefined class Runtime".to_owned()]
    );
}

#[test]
fn provider_resolves_trait_method_aliases() {
    let source = "<?php
namespace App;
trait Values {
    public function value(string $input): int { return strlen($input); }
}
class Consumer {
    use Values {
        Values::value as private aliasedValue;
    }
    public function run(): int { return $this->aliasedValue('value'); }
}";
    let dir = tempdir();
    let path = dir.path().join("TraitAlias.php");
    write(&path, source);
    let (_cache, index) = open_index();
    index_file(&index, &path, source);
    let document = document(&path, source, 1);
    let provider = Provider::new(Some(index.clone()));
    for problem in analyze(&provider, &document) {
        assert!(
            !matches!(
                problem.id.as_str(),
                "php.undefined" | "php.arguments" | "php.returnType"
            ),
            "{}",
            problem.message
        );
    }
    let offset = byte_offset(source, "aliasedValue('value')");
    let definitions = definitions(&provider, &document, offset);
    assert_eq!(definitions.len(), 1);
    assert_eq!(definitions[0].range.start.line, 3);
    index.close().unwrap();
}

#[test]
fn provider_suppresses_deprecations_inside_declaring_class() {
    let source = "<?php
namespace App;
/** @deprecated */
class Legacy {
    /** @deprecated */
    public string $value = '';
    /** @deprecated */
    public function old(): void {}
    public function compatibilityImplementation(): void {
        $this->old();
        $this->value = self::class;
    }
}
/** @deprecated */
class LegacyBridge {
    public function forward(Legacy $legacy): void {
        $legacy->old();
    }
}
class Bridge {
    /** @deprecated */
    public function forward(Legacy $legacy): void {
        $legacy->old();
    }
}
class Consumer {
    public function run(Legacy $legacy): void {
        $legacy->old();
        $legacy->value = Legacy::class;
    }
}";
    let dir = tempdir();
    let path = dir.path().join("Deprecations.php");
    write(&path, source);
    let (_cache, index) = open_index();
    index_file(&index, &path, source);
    let document = document(&path, source, 1);
    let consumer_line = source[..source.find("class Consumer").unwrap()]
        .matches('\n')
        .count() as u32;
    let mut deprecated = 0;
    for problem in analyze(&Provider::new(Some(index.clone())), &document) {
        if problem.id.as_str() != "php.deprecated" {
            continue;
        }
        deprecated += 1;
        assert!(
            problem_start_line(&document, &problem) >= consumer_line,
            "deprecations inside Legacy's own body must be suppressed"
        );
    }
    assert!(deprecated > 0, "consumer usages must remain deprecated");
    index.close().unwrap();
}

#[test]
fn provider_honors_targeted_phpstan_deprecation_ignores() {
    let source = "<?php
/** @deprecated */
function legacy(): void {}
function consume(): void {
    /** @phpstan-ignore function.deprecated */
    legacy();
    /** @phpstan-ignore arguments.count */
    legacy();
}";
    let dir = tempdir();
    let (_cache, index) = open_index();
    let document = document(&dir.path().join("DeprecationIgnores.php"), source, 1);
    let deprecated: Vec<Problem> = analyze(&Provider::new(Some(index.clone())), &document)
        .into_iter()
        .filter(|problem| problem.id.as_str() == "php.deprecated")
        .collect();
    assert_eq!(deprecated.len(), 1);
    assert_eq!(problem_start_line(&document, &deprecated[0]), 7);
    index.close().unwrap();
}

fn assert_no_undefined(problems: &[Problem], extra: &[&str]) {
    for problem in problems {
        assert_ne!(problem.id.as_str(), "php.undefined", "{}", problem.message);
        for id in extra {
            assert_ne!(problem.id.as_str(), *id, "{}", problem.message);
        }
    }
}

#[test]
fn provider_scopes_anonymous_class_methods() {
    let source = "<?php
function listen(): object {
    $prefix = 'event';
    return new class($prefix) {
        public function __construct(private string $prefix) {}
        public function handle(object $event): string {
            return $this->prefix . $event::class;
        }
    };
}";
    assert_no_undefined(&analyze_source("Anonymous.php", source, false), &[]);
}

#[test]
fn provider_resolves_static_property_names_as_members() {
    let source = "<?php
class Registry {
    /** @var list<string> */
    private static array $values = [];

    public static function add(string $value): void {
        static::$values[] = $value;
    }

    /** @return list<string> */
    public static function all(): array {
        return self::$values;
    }
}";
    assert_no_undefined(
        &analyze_source("StaticProperties.php", source, false),
        &["php.returnType"],
    );
}

#[test]
fn provider_scopes_top_level_foreach_variables() {
    let source = "<?php
$matches = [['first.php']];
foreach (array_unique($matches[0]) as $match) {
    echo $match;
}";
    assert_no_undefined(&analyze_source("TopLevelForeach.php", source, false), &[]);
}

#[test]
fn phpdoc_class_assistant_contracts() {
    let cache = tempdir();
    let path = cache.path().join("Assistant.php");
    let source = "<?php
namespace App;
class TargetClass {}
interface TargetInterface {}
trait TargetTrait {}
enum TargetEnum {}

/** @param string $name #Class */
function takes_class(string $name): void {}

/** @param string $name #Interface */
function takes_interface(string $name): void {}

/** @param string $name #ClassInterface */
function takes_class_or_interface(string $name): void {}
";
    write(&path, source);
    let index = PhpIndex::open(cache.path().join("php"), None).unwrap();
    index_file(&index, &path, source);
    let provider = Provider::new(Some(index.clone()));
    let usage_path = cache.path().join("Usage.php");

    for (function, expected, prohibited) in [
        (
            "takes_class",
            &["App\\TargetClass"][..],
            &[
                "App\\TargetInterface",
                "App\\TargetTrait",
                "App\\TargetEnum",
            ][..],
        ),
        (
            "takes_interface",
            &["App\\TargetInterface"][..],
            &["App\\TargetClass", "App\\TargetTrait", "App\\TargetEnum"][..],
        ),
        (
            "takes_class_or_interface",
            &["App\\TargetClass", "App\\TargetInterface"][..],
            &["App\\TargetTrait", "App\\TargetEnum"][..],
        ),
    ] {
        let partial = "App\\Tar";
        let usage = format!("<?php namespace App; {function}('{partial}');");
        let document = document(&usage_path, &usage, 1);
        let offset = (usage.find(partial).unwrap() + partial.len()) as u32;
        let items = completions(&provider, &document, offset);
        let labels = labels(&items);
        for expected in expected {
            assert!(
                labels.contains(&(*expected).to_owned()),
                "{function}: {expected}"
            );
        }
        for prohibited in prohibited {
            assert!(
                !labels.contains(&(*prohibited).to_owned()),
                "{function}: {prohibited}"
            );
        }
        let item = items.iter().find(|item| item.label == expected[0]).unwrap();
        let edit = text_edit(item);
        assert_eq!(edit.new_text, expected[0]);
        assert_eq!(edit_source(&document, &edit), partial);
    }

    for (function, target, has_location) in [
        ("takes_class", "App\\TargetClass", true),
        ("takes_interface", "App\\TargetInterface", true),
        ("takes_class_or_interface", "App\\TargetInterface", true),
        ("takes_class", "App\\TargetInterface", false),
    ] {
        let usage = format!("<?php namespace App; {function}('{target}');");
        let document = document(&usage_path, &usage, 1);
        let offset = (usage.find(target).unwrap() + 3) as u32;
        let locations = definitions(&provider, &document, offset);
        if !has_location {
            assert!(locations.is_empty(), "{function} {target}");
            continue;
        }
        assert_eq!(locations.len(), 1, "{function} {target}");
        assert_eq!(locations[0].uri, file_uri(&path.to_string_lossy()));
    }
    index.close().unwrap();
}

#[test]
fn provider_member_and_visibility_diagnostics() {
    let source = "<?php
class Service {
    public private(set) string $value = '';
    private function secret(): void {}
}
class DynamicService {
    public function __call(string $name, array $arguments): mixed {}
}
class Consumer {
    public function run(Service $service, DynamicService $dynamic): void {
        $service->missing();
        $service->secret();
        $service->value = 'changed';
        echo $service->value;
        $dynamic->anything();
    }
}";
    let messages = messages(&analyze_source("Members.php", source, false), None);
    assert!(messages.contains(&"Undefined method missing on Service".to_owned()));
    assert!(messages.contains(&"Cannot access private member secret".to_owned()));
    assert!(messages.contains(&"Cannot access private member value".to_owned()));
    assert!(messages.iter().all(|message| !message.contains("anything")));
}

fn visibility_problems(name: &str, source: &str) -> (Arc<TextDocument>, Vec<Problem>) {
    let dir = tempdir();
    let (_cache, index) = open_index();
    let document = document(&dir.path().join(name), source, 1);
    let problems = analyze(&Provider::new(Some(index.clone())), &document)
        .into_iter()
        .filter(|problem| problem.id.as_str() == "php.visibility")
        .collect();
    index.close().unwrap();
    (document, problems)
}

#[test]
fn provider_allows_protected_trait_member_from_subclass() {
    let source = "<?php
trait Helpers {
    protected function helper(): void {}
}
class Base {
    use Helpers;
}
class Child extends Base {
    public function run(): void {
        $this->helper();
    }
}
class Consumer {
    public function run(Child $child): void {
        $child->helper();
    }
}";
    let (document, problems) = visibility_problems("TraitVisibility.php", source);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].message, "Cannot access protected member helper");
    assert_eq!(problem_start_line(&document, &problems[0]), 14);
}

#[test]
fn provider_allows_private_trait_member_only_in_using_class() {
    let source = "<?php
trait PrivateHelpers {
    private function helper(): void {}
}
class UsesHelpers {
    use PrivateHelpers;

    public function run(): void {
        $this->helper();
    }
}
class Child extends UsesHelpers {
    public function runChild(): void {
        $this->helper();
    }
}";
    let (document, problems) = visibility_problems("PrivateTraitVisibility.php", source);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].message, "Cannot access private member helper");
    assert_eq!(problem_start_line(&document, &problems[0]), 13);
}

#[test]
fn provider_honors_closure_bind_visibility_scope() {
    let source = "<?php
class Secret {
    private string $value = 'secret';
}
function read(Secret $secret): string {
    $reader = Closure::bind(
        fn () => $secret->value,
        null,
        Secret::class,
    );
    $invalid = $secret->value;
    return $reader();
}";
    let (document, problems) = visibility_problems("ClosureBindVisibility.php", source);
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].message, "Cannot access private member value");
    assert_eq!(problem_start_line(&document, &problems[0]), 10);
}

#[test]
fn provider_allows_inaccessible_property_write_through_magic_setter() {
    let source = "<?php
class Entity {
    protected ?string $createdAt = null;

    public function __set(string $name, mixed $value): void {
        $this->$name = $value;
    }
}
function hydrate(Entity $entity): void {
    $entity->createdAt = 'now';
}";
    let (_document, problems) = visibility_problems("MagicSetter.php", source);
    assert!(problems.is_empty(), "{:?}", messages(&problems, None));
}

#[test]
fn provider_treats_missing_self_members_as_trait_requirements() {
    let source = "<?php
class Service {}
trait ConsumerRequirements {
    public function run(Service $service): void {
        $this->providedMethod();
        echo $this->providedProperty;
        $service->missing();
    }
}";
    let messages = messages(
        &analyze_source("TraitRequirements.php", source, false),
        None,
    );
    assert!(
        !messages.contains(&"Undefined method providedMethod on ConsumerRequirements".to_owned())
    );
    assert!(
        !messages
            .contains(&"Undefined property $providedProperty on ConsumerRequirements".to_owned())
    );
    assert!(messages.contains(&"Undefined method missing on Service".to_owned()));
}

#[test]
fn provider_does_not_diagnose_dynamic_member_names_as_literals() {
    let source = "<?php
class Config {
    public function setName(string $value): void {}
}
function assign(Config $config, string $setter): void {
    $config->$setter('value');
}";
    assert_no_undefined(&analyze_source("DynamicMember.php", source, false), &[]);
}

#[test]
fn provider_allows_runtime_dynamic_properties() {
    let source = "<?php
#[\\AllowDynamicProperties]
class DynamicBase {}
class DynamicChild extends DynamicBase {}
function assign(\\stdClass $plain, DynamicChild $custom): void {
    $plain->format = 'json';
    $custom->value = 1;
}";
    let root = tempdir();
    let (_cache, index) = open_index();
    index.configure_project(root.path()).unwrap();
    let document = document(&root.path().join("DynamicProperties.php"), source, 1);
    assert_no_undefined(
        &analyze(&Provider::new(Some(index.clone())), &document),
        &[],
    );
    index.close().unwrap();
}

#[test]
fn provider_php_version_diagnostics_use_composer_language_level() {
    for (composer, expected) in [
        (r#"{"require":{"php":"^8.4"}}"#, &[][..]),
        (
            r#"{"require":{"php":"^8.4"},"config":{"platform":{"php":"8.1"}}}"#,
            &["readonly-classes", "typed-class-constants"][..],
        ),
    ] {
        let root = tempdir();
        write(&root.path().join("composer.json"), composer);
        let (_cache, index) = open_index();
        index.configure_project(root.path()).unwrap();
        let source = "<?php
readonly class Subject {
    public const string KIND = 'subject';
}";
        let document = document(&root.path().join("Subject.php"), source, 1);
        let mut actual = Vec::new();
        for problem in analyze(&Provider::new(Some(index.clone())), &document) {
            if problem.id.as_str() != "php.version" {
                continue;
            }
            let payload = problem.payload.as_ref().unwrap();
            actual.push(payload["feature"].as_str().unwrap().to_owned());
            assert!(!payload["minimumPHP"].as_str().unwrap().is_empty());
            assert!(!payload["configuredPHP"].as_str().unwrap().is_empty());
        }
        assert_elements_match(actual, expected);
        index.close().unwrap();
    }
}

#[test]
fn provider_php_version_diagnostics_honor_specific_suppressions() {
    let root = tempdir();
    write(
        &root.path().join("composer.json"),
        r#"{"require":{"php":"8.1"}}"#,
    );
    let (_cache, index) = open_index();
    index.configure_project(root.path()).unwrap();
    let source = "<?php
/** @noinspection PhpVersionInspection */
readonly class Subject {}
";
    let document = document(&root.path().join("Subject.php"), source, 1);
    let problems = analyze(&Provider::new(Some(index.clone())), &document);
    assert!(!ids(&problems).contains(&"php.version"));
    index.close().unwrap();
}

#[test]
fn provider_analyze_ignores_non_php_and_reports_cancellation() {
    let (_cache, index) = open_index();
    let provider = Provider::new(Some(index.clone()));
    let other = TextDocument::new("file:///project/file.js", "class A {}", 1);
    assert!(analyze(&provider, &other).is_empty());
    let token = CancellationToken::new();
    token.cancel();
    let document = TextDocument::new("file:///project/file.php", "<?php echo $x;", 1);
    let error = provider
        .analyze(&Context::with_token(token), &document)
        .unwrap_err();
    assert!(is_cancellation(error.as_ref()));
    index.close().unwrap();
}

// ---------------------------------------------------------------------------
// Completion imports

fn global_completions(
    index: &PhpIndex,
    root: &Path,
    source: &str,
    needle: &str,
) -> (Vec<CompletionItem>, Arc<TextDocument>) {
    let document = document(&root.join("Usage.php"), source, 1);
    let offset = (source.rfind(needle).unwrap() + needle.len()) as u32;
    (
        completions(&Provider::new(Some(index.clone())), &document, offset),
        document,
    )
}

fn completion_for_detail<'a>(
    items: &'a [CompletionItem],
    fully_qualified: &str,
) -> &'a CompletionItem {
    items
        .iter()
        .find(|item| item.detail.contains(fully_qualified))
        .unwrap_or_else(|| panic!("missing completion for {fully_qualified}"))
}

fn additional_edits(item: &CompletionItem) -> Vec<TextEdit> {
    item.additional_text_edits.clone()
}

#[test]
fn php_class_completion_adds_and_reuses_imports() {
    let root = tempdir();
    let (_cache, index) = open_index();
    let declarations = "<?php
namespace Vendor\\Package;
class Order {}
class Product {}
namespace App\\Service;
class LocalThing {}
namespace Other;
class Order {}
";
    index_file(&index, &root.path().join("Declarations.php"), declarations);

    // add import
    let source = "<?php
namespace App\\Service;
use Existing\\Dependency;
class Consumer { public function run(): void { Ord } }
";
    let (items, document) = global_completions(&index, root.path(), source, "Ord");
    let order = completion_for_detail(&items, "Vendor\\Package\\Order");
    assert_eq!(order.label, "Order");
    let edit = text_edit(order);
    assert_eq!(edit.new_text, "Order");
    assert_eq!(edit_source(&document, &edit), "Ord");
    let imports = additional_edits(order);
    assert_eq!(imports.len(), 1);
    assert_eq!(imports[0].new_text, "\nuse Vendor\\Package\\Order;");

    // reuse grouped alias
    let source = "<?php
namespace App\\Service;
use Vendor\\Package\\{Order as PurchaseOrder, Product};
class Consumer { public function run(): void { Pur } }
";
    let (items, _) = global_completions(&index, root.path(), source, "Pur");
    let order = completion_for_detail(&items, "Vendor\\Package\\Order");
    assert_eq!(text_edit(order).new_text, "PurchaseOrder");
    assert!(order.additional_text_edits.is_empty());

    // avoid alias conflict
    let source = "<?php
namespace App\\Service;
use Other\\Order;
class Consumer { public function run(): void { Ord } }
";
    let (items, _) = global_completions(&index, root.path(), source, "Ord");
    let order = completion_for_detail(&items, "Vendor\\Package\\Order");
    assert_eq!(text_edit(order).new_text, "\\Vendor\\Package\\Order");
    assert!(order.additional_text_edits.is_empty());

    // current namespace
    let source = "<?php
namespace App\\Service;
class Consumer { public function run(): void { Loc } }
";
    let (items, _) = global_completions(&index, root.path(), source, "Loc");
    let local = completion_for_detail(&items, "App\\Service\\LocalThing");
    assert_eq!(text_edit(local).new_text, "LocalThing");
    assert!(local.additional_text_edits.is_empty());

    // explicit qualification
    let source = "<?php
namespace App\\Service;
class Consumer { public function run(): void { \\Vendor\\Pack } }
";
    let (items, document) = global_completions(&index, root.path(), source, "\\Vendor\\Pack");
    let order = completion_for_detail(&items, "Vendor\\Package\\Order");
    let edit = text_edit(order);
    assert_eq!(edit.new_text, "\\Vendor\\Package\\Order");
    assert_eq!(edit_source(&document, &edit), "\\Vendor\\Pack");
    assert!(order.additional_text_edits.is_empty());
    index.close().unwrap();
}

#[test]
fn php_class_completion_ranks_deterministically() {
    let root = tempdir();
    let (_cache, index) = open_index();
    let declarations = "<?php
namespace App\\Service; class Product {}
namespace App\\Domain; class Product {}
namespace Vendor\\Package; class Product {}
namespace External\\Package; /** @deprecated */ class Product {}
";
    index_file(&index, &root.path().join("Ranked.php"), declarations);
    let source = "<?php
namespace App\\Service;
use Vendor\\Package\\Product as VendorProduct;
class Consumer { public function run(): void { Pro } }
";
    let (items, _) = global_completions(&index, root.path(), source, "Pro");
    let ranked: Vec<&CompletionItem> = items
        .iter()
        .filter(|item| item.label == "Product")
        .collect();
    assert_eq!(ranked.len(), 4);
    assert!(ranked[0].detail.contains("App\\Service\\Product"));
    assert!(ranked[1].detail.contains("Vendor\\Package\\Product"));
    assert!(ranked[2].detail.contains("App\\Domain\\Product"));
    assert!(ranked[3].detail.contains("External\\Package\\Product"));
    assert!(ranked[3].deprecated);
    for pair in ranked.windows(2) {
        assert!(pair[0].sort_text < pair[1].sort_text);
    }
    index.close().unwrap();
}

#[test]
fn php_completion_package_uses_composer_psr4_ownership() {
    let package = |name: &str, prefixes: &[&str]| Package {
        name: name.into(),
        psr4: prefixes
            .iter()
            .map(|prefix| ((*prefix).to_owned(), vec![PathBuf::from("src")]))
            .collect(),
        ..Package::default()
    };
    let model = Model {
        psr4: [("App\\".to_owned(), vec![PathBuf::from("src")])].into(),
        dependencies: vec![
            package("vendor/library", &["Vendor\\Library\\", "Vendor\\"]),
            // A later owner of the same prefix loses.
            package("vendor/duplicate", &["vendor\\library\\"]),
        ],
        ..Model::default()
    };
    let packages = Packages::of(Some(&model));
    for (name, expected) in [
        ("App\\Domain\\Product", "@root"),
        ("\\app\\Service\\Consumer", "@root"),
        // The longest prefix wins, and only at a namespace boundary.
        ("Vendor\\Library\\Product", "vendor/library"),
        ("VENDOR\\LIBRARY", "vendor/library"),
        ("Vendor\\LibraryTools\\Product", "vendor/library"),
        ("Vendor\\Other", "vendor/library"),
        ("Application\\Product", ""),
        ("Unowned", ""),
        ("", ""),
    ] {
        assert_eq!(packages.package(name), expected, "{name}");
        // The hashed lookup answers exactly like the linear scan.
        assert_eq!(completion_package(Some(&model), name), expected, "{name}");
    }
    assert_eq!(Packages::of(None).package("App\\Product"), "");

    // Outside ASCII, case folding is not lowercasing: the scan decides.
    let model = Model {
        psr4: [("Stra\u{df}e\\".to_owned(), vec![PathBuf::from("src")])].into(),
        ..Model::default()
    };
    let packages = Packages::of(Some(&model));
    assert_eq!(packages.package("Stra\u{df}e\\Haus"), "@root");
    assert_eq!(packages.package("Other\\Haus"), "");
}

// ---------------------------------------------------------------------------
// Hierarchy

fn implementation_locations(
    provider: &Provider,
    document: &Arc<TextDocument>,
    offset: u32,
) -> Vec<protocol::Location> {
    provider.get_implementation(
        &ctx(),
        &ImplementationRequest {
            params: protocol::ImplementationParams {
                text_document: identifier(document),
                position: position(document, offset),
            },
            syntax: syntax(document, offset),
        },
    )
}

fn prepare_hierarchy(
    provider: &Provider,
    document: &Arc<TextDocument>,
    offset: u32,
) -> Vec<protocol::TypeHierarchyItem> {
    provider.prepare_type_hierarchy(
        &ctx(),
        &TypeHierarchyPrepareRequest {
            params: protocol::PrepareTypeHierarchyParams {
                text_document: identifier(document),
                position: position(document, offset),
            },
            syntax: syntax(document, offset),
        },
    )
}

#[test]
fn provider_implementation_and_type_hierarchy_navigation() {
    let root = tempdir();
    let base_path = root.path().join("Base.php");
    let child_path = root.path().join("Child.php");
    let base_source = "<?php
namespace App;
interface Contract { public function run(): void; }
trait Shared { public function helper(): void {} }
abstract class Base implements Contract {}
";
    let child_source = "<?php
namespace App;
class Child extends Base {
    use Shared;
    public function run(): void {}
}
class GrandChild extends Child {
    public function run(): void {}
}
class_alias(Child::class, \\Legacy\\ChildAlias::class);
";
    write(&base_path, base_source);
    write(&child_path, child_source);
    let (_cache, index) = open_index();
    index_file(&index, &base_path, base_source);
    index_file(&index, &child_path, child_source);
    let provider = Provider::new(Some(index.clone()));
    let base_uri = file_uri(&base_path.to_string_lossy());
    let child_uri = file_uri(&child_path.to_string_lossy());

    let base_document = document(&base_path, base_source, 1);
    let contract = implementation_locations(
        &provider,
        &base_document,
        byte_offset(base_source, "Contract"),
    );
    assert_eq!(contract.len(), 4);
    assert_eq!(contract[0].uri, base_uri);

    let methods =
        implementation_locations(&provider, &base_document, byte_offset(base_source, "run"));
    assert_eq!(methods.len(), 2);
    assert!(methods.iter().all(|location| location.uri == child_uri));

    let traits = implementation_locations(
        &provider,
        &base_document,
        byte_offset(base_source, "Shared"),
    );
    assert_eq!(traits.len(), 1);
    assert_eq!(traits[0].uri, child_uri);

    let child_document = document(&child_path, child_source, 1);
    let items = prepare_hierarchy(
        &provider,
        &child_document,
        byte_offset(child_source, "Child extends"),
    );
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].detail, "App\\Child");
    assert_eq!(items[0].kind, SymbolKind::CLASS);

    let supertypes = provider.supertypes(&ctx(), &items[0]);
    assert_eq!(supertypes.len(), 1);
    assert_eq!(supertypes[0].detail, "App\\Base");
    let subtypes = provider.subtypes(&ctx(), &items[0]);
    assert_eq!(subtypes.len(), 1);
    assert_eq!(subtypes[0].detail, "App\\GrandChild");

    let alias_items = prepare_hierarchy(
        &provider,
        &child_document,
        byte_offset(child_source, "ChildAlias"),
    );
    assert_eq!(alias_items.len(), 1);
    assert_eq!(alias_items[0].detail, "Legacy\\ChildAlias");
    let alias_parents = provider.supertypes(&ctx(), &alias_items[0]);
    assert_eq!(alias_parents.len(), 1);
    assert_eq!(alias_parents[0].detail, "App\\Child");
    index.close().unwrap();
}

// ---------------------------------------------------------------------------
// Editor features

fn outline(provider: &Provider, document: &Arc<TextDocument>) -> Vec<DocumentSymbol> {
    provider
        .get_document_symbols(
            &ctx(),
            &DocumentSymbolRequest {
                params: protocol::DocumentSymbolParams {
                    text_document: identifier(document),
                    ..Default::default()
                },
                document: document.clone(),
            },
        )
        .unwrap()
}

fn outline_names(symbols: &[DocumentSymbol]) -> Vec<&str> {
    symbols.iter().map(|symbol| symbol.name.as_str()).collect()
}

fn assert_outline_ranges(document: &TextDocument, symbols: &[DocumentSymbol]) {
    for symbol in symbols {
        let selection = source_range(document, symbol.selection_range);
        assert_eq!(
            symbol.name,
            selection.strip_prefix('$').unwrap_or(&selection)
        );
        let full = source_range(document, symbol.range);
        assert!(full.contains(&selection));
        for child in &symbol.children {
            assert!(full.contains(&source_range(document, child.range)));
        }
        assert_outline_ranges(document, &symbol.children);
    }
}

fn uri_document(uri: &str, source: &str, version: i32) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, version))
}

#[test]
fn php_document_outline() {
    let source = "<?php
namespace First;
use Other\\Imported;
interface Contract { public function run(); }
trait Shared { public string $label; }
enum State: string { case Ready = 'ready'; }
class Service implements Contract {
    const A = Imported::A, B = OTHER;
    private string $first, $second;
    public function __construct(private readonly Contract $inner) {}
    public function run(): void { $local = 1; }
}
function helper() {}
namespace Second;
const C = First\\Service::A;
class Next {}
";
    let document = uri_document("file:///workspace/Example.php", source, 1);
    let result = outline(&Provider::default(), &document);
    assert_eq!(result.len(), 2);
    assert_eq!(outline_names(&result), ["First", "Second"]);
    assert_eq!(
        outline_names(&result[0].children),
        ["Contract", "Shared", "State", "Service", "helper"]
    );
    assert_eq!(outline_names(&result[1].children), ["C", "Next"]);
    let service = &result[0].children[3];
    assert_eq!(
        outline_names(&service.children),
        ["A", "B", "first", "second", "__construct", "inner", "run"]
    );
    assert_eq!(service.children[4].kind, SymbolKind::CONSTRUCTOR);
    assert_eq!(service.children[5].kind, SymbolKind::PROPERTY);
    assert!(service.children[6].children.is_empty());
    assert_eq!(result[0].children[2].kind, SymbolKind::ENUM);
    assert_eq!(
        result[0].children[2].children[0].kind,
        SymbolKind::ENUM_MEMBER
    );
    assert_outline_ranges(&document, &result);
}

#[test]
fn php_outline_braced_namespaces_and_incomplete_source() {
    for source in [
        "<?php namespace One { class First {} } namespace Two { function second() {} }",
        "<?php namespace One { class First {} } namespace Two { function second(",
    ] {
        let document = uri_document("file:///workspace/live.php", source, 9);
        let result = outline(&Provider::default(), &document);
        assert_eq!(result.len(), 2);
        assert_eq!(outline_names(&result[0].children), ["First"]);
        assert_eq!(outline_names(&result[1].children), ["second"]);
        assert_outline_ranges(&document, &result);
    }
    let source = "<?php /* 😀 */ class Visible {}";
    let document = uri_document("file:///workspace/live.php", source, 10);
    let result = outline(&Provider::default(), &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].selection_range.start.character, 21);
    assert_outline_ranges(&document, &result);
}

fn highlights(
    provider: &Provider,
    document: &Arc<TextDocument>,
    offset: u32,
) -> Vec<protocol::DocumentHighlight> {
    provider
        .get_document_highlights(
            &ctx(),
            &DocumentHighlightRequest {
                params: protocol::DocumentHighlightParams {
                    text_document: identifier(document),
                    position: position(document, offset),
                },
                syntax: syntax(document, offset),
            },
        )
        .unwrap()
}

#[test]
fn php_document_highlights_use_live_semantic_identity() {
    let (_cache, index) = open_index();
    let dir = tempdir();
    let path = dir.path().join("live.php");
    index_file(
        &index,
        &path,
        "<?php function old($value) { return $value; }",
    );
    let source = "<?php
function first($value) {
    $value = '😀'; echo $value;
    return $value;
}
function second($value) { return $value; }
";
    let document = document(&path, source, 5);
    let offset = (source.find("echo $value").unwrap() + "echo $".len()) as u32;
    let result = highlights(&Provider::new(Some(index.clone())), &document, offset);
    assert_eq!(result.len(), 4);
    assert_eq!(
        result
            .iter()
            .map(|item| item.kind.unwrap())
            .collect::<Vec<_>>(),
        [
            DocumentHighlightKind::WRITE,
            DocumentHighlightKind::WRITE,
            DocumentHighlightKind::READ,
            DocumentHighlightKind::READ
        ]
    );
    for item in &result {
        assert_eq!(source_range(&document, item.range), "$value");
        assert!(item.range.start.line < 5);
    }
    assert_eq!(result[2].range.start.character, 24);
    index.close().unwrap();
}

#[test]
fn php_document_highlights_members_and_unresolved_names() {
    let (_cache, index) = open_index();
    let source = "<?php
class First { public int $value; function get() { $this->value = 1; return $this->value; } }
class Second { public int $value; }
function run(First $first, Second $second) { echo $first->value; echo $second->value; missing(); }
";
    let document = uri_document("file:///workspace/members.php", source, 1);
    for (needle, count) in [("$first->value", 4), ("missing", 0)] {
        let mut offset = source.find(needle).unwrap() as u32;
        if count != 0 {
            offset += "$first->".len() as u32;
        }
        let result = highlights(&Provider::new(Some(index.clone())), &document, offset);
        assert_eq!(result.len(), count, "{needle}");
    }
    index.close().unwrap();
}

#[test]
fn php_editor_providers_reject_unsupported_and_cancelled_requests() {
    let provider = Provider::default();
    let other = uri_document("file:///test.js", "class Other {}", 1);
    assert!(outline(&provider, &other).is_empty());
    let token = CancellationToken::new();
    token.cancel();
    let cancelled = Context::with_token(token);
    let document = uri_document("file:///test.php", "<?php class A {}", 1);
    let error = provider
        .get_document_symbols(
            &cancelled,
            &DocumentSymbolRequest {
                params: protocol::DocumentSymbolParams::default(),
                document: document.clone(),
            },
        )
        .unwrap_err();
    assert!(is_cancellation(error.as_ref()));
    let error = provider
        .get_document_highlights(
            &cancelled,
            &DocumentHighlightRequest {
                params: protocol::DocumentHighlightParams::default(),
                syntax: syntax(&document, 8),
            },
        )
        .unwrap_err();
    assert!(is_cancellation(error.as_ref()));
}

#[test]
fn php_outline_anonymous_class_keeps_members_in_their_container() {
    let source = "<?php namespace { $value = new class { public function run() {} }; }";
    let document = uri_document("file:///live.php", source, 1);
    let result = outline(&Provider::default(), &document);
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].name, "(global)");
    assert_eq!(
        source_range(&document, result[0].selection_range),
        "namespace"
    );
    assert_eq!(result[0].children.len(), 1);
    let anonymous = &result[0].children[0];
    assert_eq!(anonymous.name, "(anonymous class)");
    assert_eq!(source_range(&document, anonymous.selection_range), "class");
    assert_eq!(outline_names(&anonymous.children), ["run"]);
}
