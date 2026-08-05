package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadComposerModel(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
	  "require": {"php": "^8.2", "ext-json": "*", "ext-curl": "^8.0"},
	  "require-dev": {"ext-mbstring": "*"},
	  "config": {"platform": {"php": "8.3.4", "ext-imagick": false}},
  "autoload": {"psr-4": {"App\\": "src/"}, "files": ["bootstrap.php"]},
  "autoload-dev": {"psr-4": {"App\\Tests\\": ["tests/", "fixtures/"]}}
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.lock"), []byte(`{
  "packages": [{
    "name": "vendor/package",
    "version": "1.0.0",
    "autoload": {"psr-4": {"Vendor\\Package\\": "src/"}}
  }]
}`), 0o644))
	model, err := Load(root)
	require.NoError(t, err)
	require.Equal(t, "8.3.4", model.PHPVersion.String())
	require.Equal(t, []string{filepath.Join(root, "src")}, model.PSR4["App\\"])
	require.Contains(t, model.SourceRoots(), filepath.Join(root, "tests"))
	require.Contains(
		t,
		model.SourceRoots(),
		filepath.Join(root, "vendor", "vendor", "package", "src"),
	)
	require.Equal(t, []string{filepath.Join(root, "bootstrap.php")}, model.Files)
	require.Equal(t, []string{"curl", "json", "mbstring"}, model.RequiredExtensions)
	require.Equal(t, []string{"imagick"}, model.DisabledExtensions)
}

func TestPSR4MappingsForDirectoryUsesNearestComposerPackage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(
		filepath.Join(root, "src"),
		0o755,
	))
	pluginRoot := filepath.Join(root, "custom", "plugins", "FroshTools")
	commandDirectory := filepath.Join(pluginRoot, "src", "Command")
	require.NoError(t, os.MkdirAll(commandDirectory, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.json"),
		[]byte(`{"autoload":{"psr-4":{"Shopware\\":"src/"}}}`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(pluginRoot, "composer.json"),
		[]byte(`{
  "autoload": {"psr-4": {"Frosh\\Tools\\": "src/"}},
  "autoload-dev": {"psr-4": {"Frosh\\Tools\\Tests\\": ["tests/"]}}
}`),
		0o644,
	))

	model, err := Load(root)
	require.NoError(t, err)
	mappings, err := model.PSR4MappingsForDirectory(commandDirectory)
	require.NoError(t, err)
	require.Equal(t, []PSR4Mapping{
		{
			Namespace: "Frosh\\Tools\\",
			Root:      filepath.Join(pluginRoot, "src"),
		},
		{
			Namespace: "Frosh\\Tools\\Tests\\",
			Root:      filepath.Join(pluginRoot, "tests"),
		},
	}, mappings)
}

func TestComposerExtensionSelectionAndOverrides(t *testing.T) {
	t.Parallel()
	model := &Model{
		RequiredExtensions: []string{"json", "curl"},
		DisabledExtensions: []string{"curl"},
	}
	model.ConfigureExtensions(
		[]string{"ext-Redis", "pdo-mysql", "json"},
		[]string{"redis"},
	)
	require.Equal(t, []string{"json", "pdo_mysql"}, model.StubExtensions())

	enabled, known := model.ExtensionAvailability("ext-pdo-mysql")
	require.True(t, known)
	require.True(t, enabled)
	enabled, known = model.ExtensionAvailability("curl")
	require.True(t, known)
	require.False(t, enabled)
	_, known = model.ExtensionAvailability("soap")
	require.False(t, known)

	model.RequiredExtensions = append(model.RequiredExtensions, "dom")
	model.LoadedExtensions = []string{"json", "pdo_mysql"}
	enabled, known = model.ExtensionAvailability("dom")
	require.True(t, known)
	require.False(t, enabled)
}

func TestLoadDiscoversPHPUnitBootstrapPSR4Mappings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourceRoot := filepath.Join(
		root,
		"vendor-bin",
		"tool",
		"vendor",
		"vendor",
		"package",
		"src",
	)
	require.NoError(t, os.MkdirAll(sourceRoot, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.json"),
		[]byte(`{}`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "phpunit.xml.dist"),
		[]byte(`<phpunit bootstrap="tests/TestBootstrap.php"/>`),
		0o644,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "tests", "TestBootstrap.php"),
		[]byte(`<?php
$toolSource = dirname(__DIR__) . '/vendor-bin/tool/vendor/vendor/package/src';
$loader->addPsr4('Tool\\Api\\', $toolSource);
`),
		0o644,
	))

	model, err := Load(root)
	require.NoError(t, err)
	require.Equal(t, []string{sourceRoot}, model.PSR4["Tool\\Api\\"])
	require.Contains(t, model.SourceRoots(), sourceRoot)
}

func TestParseVersionConstraint(t *testing.T) {
	t.Parallel()
	version, ok := ParseVersionConstraint("^8.2 || ^8.3")
	require.True(t, ok)
	require.Equal(t, Version{Major: 8, Minor: 2}, version)
}

func TestVersionComparisonIncludesPatch(t *testing.T) {
	t.Parallel()
	version := Version{Major: 6, Minor: 6, Patch: 10}
	require.True(t, version.AtLeast(6, 6))
	require.True(t, version.AtLeastPatch(6, 6, 4))
	require.True(t, version.AtLeastPatch(6, 6, 10))
	require.False(t, version.AtLeastPatch(6, 6, 11))
	require.Equal(t, 1, version.Compare(Version{Major: 6, Minor: 5, Patch: 99}))
	require.Equal(t, 0, version.Compare(Version{Major: 6, Minor: 6, Patch: 10}))
	require.Equal(t, -1, version.Compare(Version{Major: 6, Minor: 7}))
}

func TestDependencyVersion(t *testing.T) {
	t.Parallel()
	model := &Model{Dependencies: []Package{
		{Name: "symfony/http-kernel", Version: "v7.3.2"},
		{Name: "symfony/form", Version: "2.8.x-dev"},
	}}
	version, found := model.DependencyVersion(
		"symfony/missing",
		"symfony/http-kernel",
	)
	require.True(t, found)
	require.Equal(t, "7.3.2", version.String())
	require.True(t, version.AtLeast(2, 8))

	_, found = model.DependencyVersion("symfony/missing")
	require.False(t, found)
}
