use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime};

use swlsp_indexer::{CancelToken, FileScanner, Indexer, ParsedFile, Store};

use crate::{
    CompiledCatalogIndex, CompiledRouteCatalog, Route, RouteIndexer, parse_compiled_routes,
};

fn by_name(routes: Vec<Route>) -> HashMap<String, Route> {
    routes
        .into_iter()
        .map(|route| (route.name.clone(), route))
        .collect()
}

#[test]
fn restores_paths_and_canonical_names() {
    let source = r"<?php
return [
    '_preview_error' => [
        ['code', '_format'],
        ['_controller' => 'error_controller::preview', '_format' => 'html'],
        ['code' => '\\d+'],
        [
            ['variable', '.', '[^/]++', '_format', true],
            ['variable', '/', '\\d+', 'code', true],
            ['text', '/_error'],
        ],
        [],
        [],
    ],
    'app.homepage.0' => [
        [],
        [
            '_controller' => 'Company\\Controller\\HomepageController::index',
            '_canonical_route' => 'app.homepage',
        ],
        [],
        [['text', '/']],
        [],
        [],
    ],
    'legacy.shape' => array(
        0 => array('slug'),
        1 => array('_controller' => 'App\\Controller\\Legacy::show'),
        2 => array(),
        3 => array(
            0 => array(0 => 'variable', 1 => '/', 3 => 'slug'),
            1 => array(0 => 'text', 1 => '/legacy'),
        ),
    ),
];";
    let routes = by_name(parse_compiled_routes(
        "/project/var/cache/dev/url_generating_routes.php",
        source,
    ));
    let preview = &routes["_preview_error"];
    assert_eq!(preview.path, "/_error/{code}.{_format}");
    assert_eq!(preview.controller, "error_controller::preview");
    assert_eq!(preview.parameters(), vec!["code", "_format"]);
    let canonical = &routes["app.homepage"];
    assert_eq!(canonical.path, "/");
    assert_eq!(
        canonical.controller,
        r"Company\Controller\HomepageController::index"
    );
    assert!(routes.contains_key("app.homepage.0"));
    let legacy = &routes["legacy.shape"];
    assert_eq!(legacy.path, "/legacy/{slug}");
    assert_eq!(legacy.controller, r"App\Controller\Legacy::show");
    assert!(legacy.line > 0);
}

#[test]
fn ignores_unrelated_and_incomplete_arrays() {
    let routes = parse_compiled_routes(
        "/project/cache.php",
        r"<?php
$unrelated = ['not.a.route' => ['value']];
return [
    'missing.tokens' => [[], ['_controller' => 'App\\Broken'], []],
    12 => [[], [], [], [['text', '/numeric']]],
];",
    );
    assert!(routes.is_empty());
}

#[test]
fn reads_legacy_declared_routes_property() {
    let source = r"<?php
class appTestUrlGenerator extends Symfony\Component\Routing\Generator\UrlGenerator
{
    static private $declaredRoutes = array(
        '_assetic_91dd2a8' => array(
            array(), array(), array(),
            array(array('text', '/ignored')),
        ),
        'api_users_getInfo' => array(
            array(),
            array('_controller' => 'Api\\Controller\\Users::getInfo'),
            array(),
            array(array('text', '/api/users/getInfo')),
        ),
        'ru__RG__feedback' => array(
            array(),
            array('_controller' => 'App\\Controller\\Feedback::russian'),
            array(),
            array(array('text', '/ru/feedback/')),
        ),
        'en__RG__feedback' => array(
            array(),
            array('_controller' => 'App\\Controller\\Feedback::english'),
            array(),
            array(array('text', '/en/feedback/')),
        ),
        'ru__RG__page' => array(
            array('alias'), array(), array(),
            array(
                array('variable', '/', '[^/]++', 'alias'),
                array('text', '/ru'),
            ),
        ),
        'en__RG__page' => array(
            array('alias'), array(), array(),
            array(
                array('variable', '/', '[^/]++', 'alias'),
                array('text', '/en'),
            ),
        ),
    );
}

class UnrelatedCatalog
{
    private static $declaredRoutes = array(
        'unrelated' => array(
            array(), array(), array(),
            array(array('text', '/unrelated')),
        ),
    );
}";
    let routes = by_name(parse_compiled_routes(
        "/project/app/cache/dev/appTestUrlGenerator.php",
        source,
    ));
    for missing in [
        "_assetic_91dd2a8",
        "ru__RG__page",
        "en__RG__page",
        "unrelated",
    ] {
        assert!(!routes.contains_key(missing), "{missing}");
    }
    assert_eq!(routes["api_users_getInfo"].path, "/api/users/getInfo");
    assert_eq!(
        routes["api_users_getInfo"].controller,
        r"Api\Controller\Users::getInfo"
    );
    assert_eq!(routes["feedback"].path, "/en/feedback/");
    assert_eq!(
        routes["feedback"].controller,
        r"App\Controller\Feedback::english"
    );
    assert_eq!(routes["page"].path, "/en/{alias}");
    assert_eq!(routes["page"].parameters(), vec!["alias"]);
    assert!(routes["page"].line > 0);
}

#[test]
fn reads_legacy_constructor_assignment() {
    let source = r"<?php
class appDevUrlGenerator implements \Symfony\Component\Routing\Generator\UrlGeneratorInterface
{
    private static $declaredRoutes;

    public function __construct()
    {
        if (null === self::$declaredRoutes) {
            self::$declaredRoutes = array(
                '_wdt' => array(
                    array('token'),
                    array(
                        '_controller' => 'web_profiler.controller.profiler:toolbarAction',
                    ),
                    array(),
                    array(
                        array('variable', '/', '[^/]++', 'token'),
                        array('text', '/_wdt'),
                    ),
                ),
            );
        }
    }
}";
    let routes = parse_compiled_routes("/project/var/cache/dev/appDevUrlGenerator.php", source);
    assert_eq!(routes.len(), 1);
    assert_eq!(routes[0].name, "_wdt");
    assert_eq!(routes[0].path, "/_wdt/{token}");
    assert_eq!(
        routes[0].controller,
        "web_profiler.controller.profiler:toolbarAction"
    );
    assert_eq!(routes[0].parameters(), vec!["token"]);
}

fn write_compiled_route_fixture(project_root: &str, name: &str, path: &str) -> String {
    let cache_dir = format!("{project_root}/var/cache/dev_test");
    std::fs::create_dir_all(&cache_dir).unwrap();
    let target = format!("{cache_dir}/url_generating_routes.php");
    std::fs::write(
        &target,
        format!("<?php return [\"{name}\" => [[], [], [], [['text', \"{path}\"]], [], []]];"),
    )
    .unwrap();
    target
}

fn write_legacy_compiled_route_fixture(
    project_root: &str,
    cache_root: &str,
    environment: &str,
    file_name: &str,
    name: &str,
    path: &str,
) -> String {
    let cache_dir = format!("{project_root}/{cache_root}/{environment}");
    std::fs::create_dir_all(&cache_dir).unwrap();
    let target = format!("{cache_dir}/{file_name}");
    let prefix = path.strip_suffix("/{id}").unwrap_or(path);
    std::fs::write(
        &target,
        format!(
            r#"<?php
class appDevUrlGenerator extends Symfony\Component\Routing\Generator\UrlGenerator
{{
    private static $declaredRoutes = [
        "{name}" => [
            ['id'],
            [],
            [],
            [
                ['variable', '/', '[^/]++', 'id'],
                ['text', "{prefix}"],
            ],
        ],
    ];
}}"#
        ),
    )
    .unwrap();
    target
}

struct Attached {
    _cache: tempfile::TempDir,
    scanner: FileScanner,
    generated: Arc<CompiledCatalogIndex>,
    store: Store,
}

impl Drop for Attached {
    fn drop(&mut self) {
        self.scanner.stop_watcher();
        let _ = self.scanner.close();
        let _ = self.generated.close();
        let _ = self.store.close();
    }
}

fn attach_compiled_route_scanner(root: &str, routes: &RouteIndexer, watch: bool) -> Attached {
    let cache = tempfile::tempdir().unwrap();
    let store = Store::open(cache.path().join("indexes.db")).unwrap();
    let generated = Arc::new(
        routes
            .compiled_route_index(root, cache.path(), Some(&store))
            .unwrap(),
    );
    let mut scanner =
        FileScanner::new(root, cache.path().join("scanner.db"), Some(store.clone())).unwrap();
    scanner.add_indexer(generated.clone());
    scanner.index_all(&CancelToken::new()).unwrap();
    if watch {
        scanner.start_watcher().unwrap();
    }
    Attached {
        _cache: cache,
        scanner,
        generated,
        store,
    }
}

fn eventually(mut condition: impl FnMut() -> bool) {
    let deadline = Instant::now() + Duration::from_secs(3);
    while Instant::now() < deadline {
        if condition() {
            return;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    assert!(condition(), "condition not met within 3s");
}

#[test]
fn project_route_indexer_merges_compiled_fallback_with_source_priority() {
    let project = tempfile::tempdir().unwrap();
    let root = project.path().to_str().unwrap();
    let compiled_path = write_compiled_route_fixture(root, "compiled.only", "/compiled/{id}");
    let config = tempfile::tempdir().unwrap();
    let index = RouteIndexer::new_project(root, config.path(), None).unwrap();
    let _attached = attach_compiled_route_scanner(root, &index, false);

    let compiled = index.get_route("compiled.only").unwrap();
    assert_eq!(compiled.len(), 1);
    assert_eq!(compiled[0].path, "/compiled/{id}");
    assert_eq!(compiled[0].file_path, compiled_path);
    assert_eq!(index.get_compiled_routes().len(), 1);

    let source_path = format!("{root}/config/routes.yaml");
    index
        .index(&ParsedFile::new(
            source_path.clone(),
            b"compiled.only:\n    path: /source/{id}\n".to_vec(),
        ))
        .unwrap();
    let preferred = index.get_route("compiled.only").unwrap();
    assert_eq!(preferred.len(), 1);
    assert_eq!(preferred[0].path, "/source/{id}");
    assert_eq!(preferred[0].file_path, source_path);
    index.close().unwrap();
}

#[test]
fn project_route_indexer_reloads_compiled_routes() {
    let project = tempfile::tempdir().unwrap();
    let root = project.path().to_str().unwrap();
    let config = tempfile::tempdir().unwrap();
    let index = RouteIndexer::new_project(root, config.path(), None).unwrap();
    let _attached = attach_compiled_route_scanner(root, &index, true);

    write_compiled_route_fixture(root, "created.after.start", "/created");
    eventually(|| {
        index
            .get_route("created.after.start")
            .is_ok_and(|routes| routes.len() == 1 && routes[0].path == "/created")
    });
    write_compiled_route_fixture(root, "changed.after.start", "/changed");
    eventually(|| {
        let changed = index.get_route("changed.after.start").unwrap();
        let missing = index.get_route("created.after.start").unwrap();
        changed.len() == 1 && changed[0].path == "/changed" && missing.is_empty()
    });
    index.reload_compiled_routes().unwrap();
}

#[test]
fn project_route_indexer_reloads_legacy_compiled_routes() {
    let project = tempfile::tempdir().unwrap();
    let root = project.path().to_str().unwrap();
    let config = tempfile::tempdir().unwrap();
    let index = RouteIndexer::new_project(root, config.path(), None).unwrap();
    let _attached = attach_compiled_route_scanner(root, &index, true);

    write_legacy_compiled_route_fixture(
        root,
        "app/cache",
        "dev_test",
        "appDevUrlGenerator.php",
        "legacy.created",
        "/legacy/{id}",
    );
    eventually(|| {
        index
            .get_route("legacy.created")
            .is_ok_and(|routes| routes.len() == 1 && routes[0].path == "/legacy/{id}")
    });
}

fn discovered_path(project_root: &str) -> String {
    let catalog = CompiledRouteCatalog::new(project_root);
    catalog.refresh().unwrap();
    catalog.path()
}

#[test]
fn compiled_route_catalog_discovers_legacy_cache_files() {
    let project = tempfile::tempdir().unwrap();
    let root = project.path().to_str().unwrap();
    let expected = write_legacy_compiled_route_fixture(
        root,
        "var/cache",
        "dev_123",
        "srcDevDebugProjectContainerUrlGenerator.php",
        "legacy.var",
        "/var",
    );
    assert_eq!(discovered_path(root), expected);

    let project = tempfile::tempdir().unwrap();
    let root = project.path().to_str().unwrap();
    let expected = write_legacy_compiled_route_fixture(
        root,
        "app/cache",
        "dev",
        "appDevUrlGenerator.php",
        "legacy.app",
        "/app",
    );
    assert_eq!(discovered_path(root), expected);
}

fn set_modified(path: &str, time: SystemTime) {
    std::fs::File::options()
        .write(true)
        .open(Path::new(path))
        .unwrap()
        .set_modified(time)
        .unwrap();
}

#[test]
fn compiled_route_catalog_prefers_modern_dev_catalog() {
    let project = tempfile::tempdir().unwrap();
    let root = project.path().to_str().unwrap();
    let legacy = write_legacy_compiled_route_fixture(
        root,
        "var/cache",
        "dev",
        "appDevUrlGenerator.php",
        "legacy",
        "/legacy",
    );
    let modern = write_compiled_route_fixture(root, "modern", "/modern");
    set_modified(&modern, SystemTime::now() - Duration::from_secs(3600));
    set_modified(&legacy, SystemTime::now() + Duration::from_secs(3600));
    assert_eq!(discovered_path(root), modern);
}
