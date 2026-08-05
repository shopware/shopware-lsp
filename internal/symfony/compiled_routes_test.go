package symfony

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCompiledRoutesRestoresPathsAndCanonicalNames(t *testing.T) {
	source := `<?php
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
];`
	routes := ParseCompiledRoutes(
		"/project/var/cache/dev/url_generating_routes.php",
		[]byte(source),
	)
	byName := make(map[string]Route, len(routes))
	for _, route := range routes {
		byName[route.Name] = route
	}

	preview := byName["_preview_error"]
	assert.Equal(t, "/_error/{code}.{_format}", preview.Path)
	assert.Equal(t, "error_controller::preview", preview.Controller)
	assert.Equal(t, []string{"code", "_format"}, preview.Parameters())

	canonical := byName["app.homepage"]
	assert.Equal(t, "/", canonical.Path)
	assert.Equal(
		t,
		"Company\\Controller\\HomepageController::index",
		canonical.Controller,
	)
	assert.Equal(t, "app.homepage", canonical.Name)
	assert.Contains(t, byName, "app.homepage.0")

	legacy := byName["legacy.shape"]
	assert.Equal(t, "/legacy/{slug}", legacy.Path)
	assert.Equal(t, "App\\Controller\\Legacy::show", legacy.Controller)
	assert.Greater(t, legacy.Line, 0)
}

func TestParseCompiledRoutesIgnoresUnrelatedAndIncompleteArrays(t *testing.T) {
	routes := ParseCompiledRoutes(
		"/project/cache.php",
		[]byte(`<?php
$unrelated = ['not.a.route' => ['value']];
return [
    'missing.tokens' => [[], ['_controller' => 'App\\Broken'], []],
    12 => [[], [], [], [['text', '/numeric']]],
];`),
	)
	assert.Empty(t, routes)
}

func TestParseCompiledRoutesReadsLegacyDeclaredRoutesProperty(t *testing.T) {
	source := `<?php
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
}`
	routes := ParseCompiledRoutes(
		"/project/app/cache/dev/appTestUrlGenerator.php",
		[]byte(source),
	)
	byName := make(map[string]Route, len(routes))
	for _, route := range routes {
		byName[route.Name] = route
	}

	assert.NotContains(t, byName, "_assetic_91dd2a8")
	assert.NotContains(t, byName, "ru__RG__page")
	assert.NotContains(t, byName, "en__RG__page")
	assert.NotContains(t, byName, "unrelated")
	require.Contains(t, byName, "api_users_getInfo")
	assert.Equal(t, "/api/users/getInfo", byName["api_users_getInfo"].Path)
	assert.Equal(
		t,
		"Api\\Controller\\Users::getInfo",
		byName["api_users_getInfo"].Controller,
	)
	require.Contains(t, byName, "feedback")
	assert.Equal(t, "/en/feedback/", byName["feedback"].Path)
	assert.Equal(
		t,
		"App\\Controller\\Feedback::english",
		byName["feedback"].Controller,
	)
	require.Contains(t, byName, "page")
	assert.Equal(t, "/en/{alias}", byName["page"].Path)
	assert.Equal(t, []string{"alias"}, byName["page"].Parameters())
	assert.Greater(t, byName["page"].Line, 0)
}

func TestParseCompiledRoutesReadsLegacyConstructorAssignment(t *testing.T) {
	source := `<?php
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
}`
	routes := ParseCompiledRoutes(
		"/project/var/cache/dev/appDevUrlGenerator.php",
		[]byte(source),
	)
	require.Len(t, routes, 1)
	assert.Equal(t, "_wdt", routes[0].Name)
	assert.Equal(t, "/_wdt/{token}", routes[0].Path)
	assert.Equal(
		t,
		"web_profiler.controller.profiler:toolbarAction",
		routes[0].Controller,
	)
	assert.Equal(t, []string{"token"}, routes[0].Parameters())
}

func TestProjectRouteIndexerMergesCompiledFallbackWithSourcePriority(
	t *testing.T,
) {
	projectRoot := t.TempDir()
	compiledPath := writeCompiledRouteFixture(
		t,
		projectRoot,
		"compiled.only",
		"/compiled/{id}",
	)
	idx, err := NewProjectRouteIndexer(
		projectRoot,
		t.TempDir(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	compiled, err := idx.GetRoute("compiled.only")
	require.NoError(t, err)
	require.Len(t, compiled, 1)
	assert.Equal(t, "/compiled/{id}", compiled[0].Path)
	assert.Equal(t, compiledPath, compiled[0].FilePath)
	require.Len(t, idx.GetCompiledRoutes(), 1)

	sourcePath := filepath.Join(projectRoot, "config", "routes.yaml")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		sourcePath,
		[]byte("compiled.only:\n    path: /source/{id}\n"),
	)))
	preferred, err := idx.GetRoute("compiled.only")
	require.NoError(t, err)
	require.Len(t, preferred, 1)
	assert.Equal(t, "/source/{id}", preferred[0].Path)
	assert.Equal(t, sourcePath, preferred[0].FilePath)
}

func TestProjectRouteIndexerReloadsCompiledRoutes(t *testing.T) {
	projectRoot := t.TempDir()
	idx, err := NewProjectRouteIndexer(
		projectRoot,
		t.TempDir(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	writeCompiledRouteFixture(
		t,
		projectRoot,
		"created.after.start",
		"/created",
	)
	require.Eventually(t, func() bool {
		routes, routeErr := idx.GetRoute("created.after.start")
		return routeErr == nil && len(routes) == 1 &&
			routes[0].Path == "/created"
	}, 3*time.Second, 20*time.Millisecond)

	writeCompiledRouteFixture(
		t,
		projectRoot,
		"changed.after.start",
		"/changed",
	)
	require.Eventually(t, func() bool {
		changed, changedErr := idx.GetRoute("changed.after.start")
		missing, missingErr := idx.GetRoute("created.after.start")
		return changedErr == nil && missingErr == nil &&
			len(changed) == 1 && changed[0].Path == "/changed" &&
			len(missing) == 0
	}, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, idx.ReloadCompiledRoutes())
}

func TestProjectRouteIndexerReloadsLegacyCompiledRoutes(t *testing.T) {
	projectRoot := t.TempDir()
	idx, err := NewProjectRouteIndexer(
		projectRoot,
		t.TempDir(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	writeLegacyCompiledRouteFixture(
		t,
		projectRoot,
		filepath.Join("app", "cache"),
		"dev_test",
		"appDevUrlGenerator.php",
		"legacy.created",
		"/legacy/{id}",
	)
	require.Eventually(t, func() bool {
		routes, routeErr := idx.GetRoute("legacy.created")
		return routeErr == nil && len(routes) == 1 &&
			routes[0].Path == "/legacy/{id}"
	}, 3*time.Second, 20*time.Millisecond)
}

func TestCompiledRouteWatcherDiscoversLegacyCacheFiles(t *testing.T) {
	t.Run("var cache with hashed dev environment", func(t *testing.T) {
		projectRoot := t.TempDir()
		expected := writeLegacyCompiledRouteFixture(
			t,
			projectRoot,
			filepath.Join("var", "cache"),
			"dev_123",
			"srcDevDebugProjectContainerUrlGenerator.php",
			"legacy.var",
			"/var",
		)
		watcher := &CompiledRouteWatcher{projectRoot: projectRoot}
		actual, err := watcher.findRouteFile()
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
	})

	t.Run("legacy app cache", func(t *testing.T) {
		projectRoot := t.TempDir()
		expected := writeLegacyCompiledRouteFixture(
			t,
			projectRoot,
			filepath.Join("app", "cache"),
			"dev",
			"appDevUrlGenerator.php",
			"legacy.app",
			"/app",
		)
		watcher := &CompiledRouteWatcher{projectRoot: projectRoot}
		actual, err := watcher.findRouteFile()
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
	})
}

func TestCompiledRouteWatcherPrefersModernDevCatalog(t *testing.T) {
	projectRoot := t.TempDir()
	legacy := writeLegacyCompiledRouteFixture(
		t,
		projectRoot,
		filepath.Join("var", "cache"),
		"dev",
		"appDevUrlGenerator.php",
		"legacy",
		"/legacy",
	)
	modern := writeCompiledRouteFixture(
		t,
		projectRoot,
		"modern",
		"/modern",
	)
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(modern, old, old))
	future := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(legacy, future, future))

	watcher := &CompiledRouteWatcher{projectRoot: projectRoot}
	actual, err := watcher.findRouteFile()
	require.NoError(t, err)
	assert.Equal(t, modern, actual)
}

func TestCompiledRouteWatcherOnlySubscribesToCacheDirectories(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "project")
	watcher := &CompiledRouteWatcher{projectRoot: root}
	assert.True(t, watcher.shouldWatchCreatedDirectory(
		filepath.Join(root, "var"),
	))
	assert.True(t, watcher.shouldWatchCreatedDirectory(
		filepath.Join(root, "var", "cache"),
	))
	assert.True(t, watcher.shouldWatchCreatedDirectory(
		filepath.Join(root, "var", "cache", "dev_hash"),
	))
	assert.True(t, watcher.shouldWatchCreatedDirectory(
		filepath.Join(root, "app"),
	))
	assert.True(t, watcher.shouldWatchCreatedDirectory(
		filepath.Join(root, "app", "cache"),
	))
	assert.True(t, watcher.shouldWatchCreatedDirectory(
		filepath.Join(root, "app", "cache", "dev"),
	))
	assert.False(t, watcher.shouldWatchCreatedDirectory(
		filepath.Join(root, "src"),
	))
	assert.False(t, watcher.shouldWatchCreatedDirectory(
		filepath.Join(root, "var", "cache", "dev_hash", "pools"),
	))
	assert.False(t, watcher.shouldWatchCreatedDirectory(
		filepath.Join(root, "app", "config"),
	))
}

func writeCompiledRouteFixture(
	t *testing.T,
	projectRoot,
	name,
	path string,
) string {
	t.Helper()
	cacheDir := filepath.Join(
		projectRoot,
		"var",
		"cache",
		"dev_test",
	)
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))
	target := filepath.Join(cacheDir, "url_generating_routes.php")
	source := fmt.Sprintf(
		"<?php return [%q => [[], [], [], [['text', %q]], [], []]];",
		name,
		path,
	)
	require.NoError(t, os.WriteFile(target, []byte(source), 0o644))
	return target
}

func writeLegacyCompiledRouteFixture(
	t *testing.T,
	projectRoot,
	cacheRoot,
	environment,
	fileName,
	name,
	path string,
) string {
	t.Helper()
	cacheDir := filepath.Join(projectRoot, cacheRoot, environment)
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))
	target := filepath.Join(cacheDir, fileName)
	source := fmt.Sprintf(
		`<?php
class appDevUrlGenerator extends Symfony\Component\Routing\Generator\UrlGenerator
{
    private static $declaredRoutes = [
        %q => [
            ['id'],
            [],
            [],
            [
                ['variable', '/', '[^/]++', 'id'],
                ['text', %q],
            ],
        ],
    ];
}`,
		name,
		strings.TrimSuffix(path, "/{id}"),
	)
	require.NoError(t, os.WriteFile(target, []byte(source), 0o644))
	return target
}
