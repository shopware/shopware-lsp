use std::sync::Arc;

use swlsp_indexer::{BatchIndexer, Indexer, SupplementalPathIndexer};
use swlsp_parser_php::query;
use swlsp_php_semantic::{Flags, NameKind, SymbolKind, WorkspaceGraphLoader};

use super::{file, index, open};
use crate::compare_fold;

#[test]
fn publishes_hierarchy_and_members() {
    let directory = tempfile::tempdir().unwrap();
    let idx = open(directory.path());
    index(
        &idx,
        "/project/types.php",
        r#"<?php
namespace App;
interface Marker {}
interface ChildMarker extends Marker {}
abstract class Base implements ChildMarker {
    protected string $name;
}
final class Concrete extends Base {
    public function name(): string { return $this->name; }
}"#,
    );
    let class = idx.find_class("App\\Concrete").unwrap();
    assert_eq!(class.kind, SymbolKind::Class);
    assert!(
        idx.semantic_snapshot()
            .is_subtype_of("App\\Concrete", "App\\Marker")
    );
    assert!(idx.class_names().contains(&"App\\Concrete".to_owned()));
    assert!(!idx.class_symbols_in("/project/types.php").is_empty());
    idx.close().unwrap();
}

#[test]
fn persists_phpstorm_meta_call_contracts() {
    let config = tempfile::tempdir().unwrap();
    let meta = tempfile::tempdir().unwrap();
    let meta_path = meta.path().join(".phpstorm.meta.php");
    let idx = open(config.path());
    index(
        &idx,
        meta_path.to_str().unwrap(),
        "<?php\nnamespace PHPSTORM_META;\noverride(\\identity(0), type(0));\n",
    );
    let assert_contract = |idx: &crate::PhpIndex| {
        let parsed = swlsp_parser_php::parse(
            "<?php\nclass Product {}\n$product = identity(new Product());\n",
        );
        let document = idx.analyze_tree("/consumer.php", 1, &parsed.tree);
        let call = query::calls(&parsed.root(), &[])
            .into_iter()
            .find(|call| query::call_method_name(call) == "identity")
            .expect("identity call");
        assert_eq!(document.type_of(&call).ty.to_string(), "Product");
    };
    assert_contract(&idx);
    idx.close().unwrap();

    let reopened = open(config.path());
    assert_contract(&reopened);
    reopened.close().unwrap();
}

#[test]
fn reopens_composer_autoload_dev_roots() {
    let project = tempfile::tempdir().unwrap();
    let tests_root = project.path().join("tests/unit");
    std::fs::create_dir_all(&tests_root).unwrap();
    std::fs::write(
        project.path().join("composer.json"),
        r#"{"autoload-dev":{"psr-4":{"App\\Tests\\":"tests/unit/"}}}"#,
    )
    .unwrap();
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    idx.configure_project(project.path()).unwrap();

    let path = |relative: &str| project.path().join(relative).to_string_lossy().into_owned();
    assert!(idx.should_enter_directory(&path("tests")));
    assert!(idx.should_enter_directory(&path("tests/unit")));
    assert!(!idx.should_enter_directory(&path("tests/fixtures")));
    assert!(idx.should_index_path(&path("tests/unit/ExampleTest.php")));
    assert!(!idx.should_index_path(&path("tests/unit/fixture.json")));
    assert!(!idx.should_index_path(&path("tests/fixtures/Example.php")));
    idx.close().unwrap();
}

#[test]
fn selects_runtime_stubs_from_composer_and_overrides() {
    let project = tempfile::tempdir().unwrap();
    std::fs::write(
        project.path().join("composer.json"),
        r#"{
  "require": {"php": "^8.3", "ext-curl": "*"},
  "config": {"platform": {"ext-imagick": false}}
}"#,
    )
    .unwrap();
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    idx.configure_project_with_extensions(project.path(), &["redis"], &[])
        .unwrap();
    let snapshot = idx.semantic_snapshot();
    assert_eq!(snapshot.functions("strlen").len(), 1);
    assert_eq!(snapshot.functions("curl_init").len(), 1);
    assert_eq!(snapshot.classes("Redis").len(), 1);
    assert!(snapshot.classes("Imagick").is_empty());
    assert!(snapshot.classes("SoapClient").is_empty());
    assert!(idx.project().unwrap().loaded_extensions.is_some());
    idx.close().unwrap();
}

#[test]
fn compare_fold_matches_case_insensitive_name_order() {
    use std::cmp::Ordering;
    assert_eq!(compare_fold("App\\Alpha", "app\\Beta"), Ordering::Less);
    assert_eq!(
        compare_fold("APP\\Service", "app\\service"),
        Ordering::Equal
    );
    assert_eq!(compare_fold("App\\Zulu", "app\\Beta"), Ordering::Greater);
    assert_eq!(compare_fold("Äpfel", "Öl"), Ordering::Less);
    assert_eq!(compare_fold("abc", "abcd"), Ordering::Less);
}

#[test]
fn rebuilds_semantic_documents_on_demand() {
    let config = tempfile::tempdir().unwrap();
    let sources = tempfile::tempdir().unwrap();
    let path = sources.path().join("Product.php");
    let path = path.to_str().unwrap();
    let source = r#"<?php
namespace App;
/** @final */
class Product {
    #[\Deprecated]
    public string $legacy;

    /** @deprecated */
    public function name(): string { return 'product'; }
}"#;
    std::fs::write(path, source).unwrap();

    let check = |idx: &crate::PhpIndex| {
        let document = idx.semantic_document(path).expect("semantic document");
        assert!(!document.symbols.is_empty());
        assert!(!document.scopes.is_empty());
        assert!(document.type_fact_count() > 0);
        assert_eq!(idx.semantic_snapshot().classes("App\\Product").len(), 1);
        assert!(
            idx.find_methods("App\\Product", "name")[0]
                .flags
                .has(Flags::DEPRECATED)
        );
        assert!(
            idx.find_properties("App\\Product", "legacy")[0]
                .flags
                .has(Flags::DEPRECATED)
        );
        assert!(
            idx.semantic_snapshot().classes("App\\Product")[0]
                .flags
                .has(Flags::SOFT_FINAL)
        );
    };
    let idx = open(config.path());
    index(&idx, path, source);
    check(&idx);
    idx.close().unwrap();

    let reopened = open(config.path());
    check(&reopened);
    reopened.close().unwrap();
}

#[test]
fn persists_compact_workspace_graph() {
    let config = tempfile::tempdir().unwrap();
    let sources = tempfile::tempdir().unwrap();
    let path = sources.path().join("Product.php");
    let path = path.to_str().unwrap();
    let source = r#"<?php
namespace App;
class Product {
    public function label(string $prefix): string {
        $suffix = 'product';
        return $prefix . $suffix;
    }
}"#;
    std::fs::write(path, source).unwrap();
    let idx = open(config.path());
    index(&idx, path, source);

    let values = idx.inner.workspace_graphs.get_values_by_path(path).unwrap();
    assert_eq!(values.len(), 1);
    let graph = values[0].decode().unwrap().document();
    assert!(graph.scopes.is_empty());
    assert_eq!(graph.type_fact_count(), 0);
    assert!(graph.type_aliases.is_empty());
    assert!(graph.issues.is_empty());
    for symbol in &graph.symbols {
        assert!(!matches!(
            symbol.kind,
            SymbolKind::Parameter | SymbolKind::Local | SymbolKind::Closure
        ));
    }
    assert!(
        graph
            .references
            .iter()
            .all(|reference| reference.kind != NameKind::Variable)
    );
    idx.close().unwrap();

    let reopened = open(config.path());
    assert_eq!(reopened.find_methods("App\\Product", "label").len(), 1);
    let restored = reopened.semantic_document(path).unwrap();
    assert!(!restored.scopes.is_empty());
    assert!(restored.type_fact_count() > 0);
    assert!(
        restored
            .symbols
            .iter()
            .any(|symbol| symbol.kind == SymbolKind::Local && symbol.name == "$suffix")
    );
    reopened.close().unwrap();
}

#[test]
fn pins_lazy_details_before_replacing_persisted_graph() {
    let config = tempfile::tempdir().unwrap();
    let path = "/project/Service.php";
    let idx = open(config.path());
    index(
        &idx,
        path,
        "<?php\nnamespace App;\nclass Service {\n    public function run(string $before): void {}\n}",
    );
    idx.close().unwrap();

    let reopened = open(config.path());
    let previous = reopened.semantic_snapshot();
    let classes = previous.class_views("App\\Service");
    assert_eq!(classes.len(), 1);
    let class_id = classes[0].id().to_owned();
    drop(classes);

    index(
        &reopened,
        path,
        "<?php\nnamespace App;\nclass Service {\n    public function run(int $after): void {}\n}",
    );
    let current = reopened.semantic_snapshot();

    let previous_methods = previous.members(&class_id, "run");
    assert_eq!(previous_methods.len(), 1);
    assert_eq!(previous_methods[0].parameters.len(), 1);
    assert_eq!(previous_methods[0].parameters[0].name, "$before");

    let current_classes = current.class_views("App\\Service");
    assert_eq!(current_classes.len(), 1);
    let current_methods = current.members(current_classes[0].id(), "run");
    assert_eq!(current_methods.len(), 1);
    assert_eq!(current_methods[0].parameters.len(), 1);
    assert_eq!(current_methods[0].parameters[0].name, "$after");
    drop(current_classes);
    reopened.close().unwrap();
}

#[test]
fn bounds_loaded_workspace_graph_details() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    let paths: Vec<String> = (0..crate::WORKSPACE_GRAPH_DETAIL_CACHE_SIZE + 2)
        .map(|number| format!("/project/C{number:03}.php"))
        .collect();
    for (number, path) in paths.iter().enumerate() {
        index(
            &idx,
            path,
            &format!(
                "<?php class C{number:03} {{ public function run(string $value): void {{}} }}"
            ),
        );
    }
    idx.close().unwrap();

    let reopened = open(config.path());
    for path in &paths {
        reopened.inner.loader.load_workspace_graph(path).unwrap();
    }
    let cached = reopened.inner.loader.cached_paths();
    assert_eq!(cached.len(), crate::WORKSPACE_GRAPH_DETAIL_CACHE_SIZE);
    assert!(!cached.contains(&paths[0]));
    assert!(cached.contains(paths.last().unwrap()));
    assert!(
        reopened
            .inner
            .loader
            .load_workspace_graph("/project/missing.php")
            .unwrap_err()
            .to_string()
            .contains("not found")
    );
    reopened.close().unwrap();
}

#[test]
fn concurrent_detail_loads_share_one_graph() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    index(
        &idx,
        "/project/A.php",
        "<?php class A { public function run(): void {} }",
    );
    idx.close().unwrap();
    let reopened = open(config.path());
    let loader = reopened.inner.loader.clone();
    let graphs: Vec<_> = std::thread::scope(|scope| {
        let handles: Vec<_> = (0..16)
            .map(|_| scope.spawn(|| loader.load_workspace_graph("/project/A.php").unwrap()))
            .collect();
        handles
            .into_iter()
            .map(|handle| handle.join().unwrap())
            .collect()
    });
    assert!(graphs.iter().all(|graph| graph.path() == "/project/A.php"));
    assert!(Arc::ptr_eq(
        &graphs[0],
        &loader.load_workspace_graph("/project/A.php").unwrap()
    ));
    reopened.close().unwrap();
}

#[test]
fn persists_constant_array_metadata() {
    let config = tempfile::tempdir().unwrap();
    let source = r#"<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
        'headers' => [],
    ];
}"#;
    let class = "Symfony\\Contracts\\HttpClient\\HttpClientInterface";
    let idx = open(config.path());
    index(&idx, "/project/HttpClientInterface.php", source);
    let constants = idx.find_constants(class, "OPTIONS_DEFAULTS");
    assert_eq!(constants.len(), 1);
    assert_eq!(constants[0].constant_array().len(), 2);
    assert_eq!(constants[0].constant_array()[0].key, "timeout");
    idx.close().unwrap();

    let reopened = open(config.path());
    let constants = reopened.find_constants(class, "OPTIONS_DEFAULTS");
    assert_eq!(constants.len(), 1);
    assert_eq!(constants[0].constant_array().len(), 2);
    assert_eq!(constants[0].constant_array()[1].ty.to_string(), "array");
    assert_eq!(constants[0].constant_array()[1].value, "[]");
    reopened.close().unwrap();
}

#[test]
fn persists_literal_method_returns() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    index(
        &idx,
        "/project/Helper.php",
        "<?php\nnamespace App;\nclass Helper {\n    public function getName(): string {\n        return 'question';\n    }\n}",
    );
    let methods = idx.find_methods("App\\Helper", "getName");
    assert_eq!(methods.len(), 1);
    assert_eq!(methods[0].literal_returns().len(), 1);
    assert_eq!(methods[0].literal_returns()[0].value, "question");
    idx.close().unwrap();

    let reopened = open(config.path());
    let methods = reopened.find_methods("App\\Helper", "getName");
    assert_eq!(methods.len(), 1);
    assert_eq!(methods[0].literal_returns().len(), 1);
    assert_eq!(
        methods[0].literal_returns()[0].ty.to_string(),
        "\"question\""
    );
    reopened.close().unwrap();
}

#[test]
fn persists_class_constant_method_returns() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    index(
        &idx,
        "/project/Helper.php",
        "<?php\nnamespace App;\nclass Helper {\n    private const NAME = 'question';\n    public function getName(): string {\n        return self::NAME;\n    }\n}",
    );
    let check = |idx: &crate::PhpIndex| {
        let methods = idx.find_methods("App\\Helper", "getName");
        assert_eq!(methods.len(), 1);
        assert_eq!(methods[0].constant_returns().len(), 1);
        assert_eq!(methods[0].constant_returns()[0].receiver, "self");
        assert_eq!(methods[0].constant_returns()[0].name, "NAME");
    };
    check(&idx);
    idx.close().unwrap();
    let reopened = open(config.path());
    check(&reopened);
    reopened.close().unwrap();
}

#[test]
fn semantic_context_resolves_call_receiver() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    let source = r#"<?php
namespace App;
use Shopware\Core\System\SystemConfig\SystemConfigService;
class Demo {
    public function __construct(private SystemConfigService $config) {}
    public function run(): void { $this->config->get('key'); }
}"#;
    let parsed = swlsp_parser_php::parse(source);
    let root = parsed.root();
    let calls = query::calls(&root, &["$this->config->get"]);
    assert_eq!(calls.len(), 1);
    let context = idx.document_context("/project/Demo.php", 1, Some(&calls[0]), &root, source);
    assert!(idx.is_method_called_on_class(
        Some(&context),
        &calls[0],
        "Shopware\\Core\\System\\SystemConfig\\SystemConfigService",
    ));
    assert!(!idx.is_method_called_on_class(Some(&context), &calls[0], "App\\Other"));
    assert!(idx.is_method_called_name(&calls[0], &["set", "get"]));
    assert_eq!(context.inside_class.as_ref().unwrap().name, "Demo");
    idx.close().unwrap();
}

#[test]
fn resolves_references_when_target_arrives_later() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    index(
        &idx,
        "/project/Consumer.php",
        "<?php\nnamespace App;\nclass Consumer {\n    public function run(): void {\n        (new Service())->execute();\n    }\n}",
    );
    index(
        &idx,
        "/project/Service.php",
        "<?php\nnamespace App;\nclass Service {\n    public function execute(): void {}\n}",
    );
    let class = idx.find_class("App\\Service").unwrap();
    let snapshot = idx.semantic_snapshot();
    assert!(!snapshot.references_to(class.id.as_str()).is_empty());
    let methods = snapshot.members(class.id.as_str(), "execute");
    assert_eq!(methods.len(), 1);
    assert!(!snapshot.references_to(methods[0].id.as_str()).is_empty());
    idx.close().unwrap();
}

#[test]
fn publishes_scanner_batch_as_one_generation() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    idx.begin_indexing_batch(&[
        "/project/Consumer.php".to_owned(),
        "/project/Service.php".to_owned(),
    ]);
    index(
        &idx,
        "/project/Consumer.php",
        "<?php\nnamespace App;\nclass Consumer {\n    public function run(Service $service): void { $service->execute(); }\n}",
    );
    assert!(idx.semantic_snapshot().classes("App\\Consumer").is_empty());
    index(
        &idx,
        "/project/Service.php",
        "<?php\nnamespace App;\nclass Service {\n    public function execute(): void {}\n}",
    );
    idx.end_indexing_batch().unwrap();
    assert!(idx.end_indexing_batch().is_err());

    let snapshot = idx.semantic_snapshot();
    assert_eq!(snapshot.revision, 1);
    assert_eq!(snapshot.classes("App\\Consumer").len(), 1);
    let services = snapshot.classes("App\\Service");
    assert_eq!(services.len(), 1);
    assert!(!snapshot.references_to(services[0].id.as_str()).is_empty());
    idx.close().unwrap();
}

#[test]
fn removes_and_clears_files() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    index(&idx, "/project/A.php", "<?php\nnamespace App;\nclass A {}");
    index(&idx, "/project/B.php", "<?php\nnamespace App;\nclass B {}");
    let revision = idx.revision();
    idx.removed_files(&["/project/A.php".to_owned()]).unwrap();
    assert!(idx.revision() > revision);
    assert!(idx.find_class("App\\A").is_none());
    assert!(idx.find_class("App\\B").is_some());
    assert!(
        idx.inner
            .workspace_graphs
            .get_values_by_path("/project/A.php")
            .unwrap()
            .is_empty()
    );
    Indexer::clear(&idx).unwrap();
    assert!(idx.find_class("App\\B").is_none());
    assert_eq!(idx.inner.workspace_graphs.count_all_values().unwrap(), 0);
    idx.close().unwrap();
}

#[test]
fn ignores_non_php_files() {
    let config = tempfile::tempdir().unwrap();
    let idx = open(config.path());
    let parsed = file("/project/config.json", "{}");
    Indexer::index(&idx, &parsed).unwrap();
    assert_eq!(idx.inner.workspace_graphs.count_all_values().unwrap(), 0);
    assert!(!idx.should_index_path("/project/A.php"));
    assert!(!idx.should_enter_directory("/project/tests"));
    idx.close().unwrap();
}
