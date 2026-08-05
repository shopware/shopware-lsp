package shopware

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/php/project"
)

func TestVersionResolverPrefersExplicitTarget(t *testing.T) {
	t.Parallel()
	model := &project.Model{Dependencies: []project.Package{{
		Name: "shopware/core", Version: "v6.6.10.0",
	}}}
	resolved, err := NewVersionResolver(
		t.TempDir(),
		model,
		"v6.8.1.0-dev",
	).Resolve()
	require.NoError(t, err)
	require.Equal(t, ResolvedVersion{
		Version: project.Version{Major: 6, Minor: 8, Patch: 1},
		Source:  VersionSourceExplicit,
		Known:   true,
	}, resolved)
}

func TestVersionResolverRejectsInvalidExplicitTarget(t *testing.T) {
	t.Parallel()
	_, err := NewVersionResolver(t.TempDir(), nil, "next").Resolve()
	require.ErrorContains(t, err, "expected major.minor[.patch[.build]]")
}

func TestVersionResolverUsesInstalledComposerPackage(t *testing.T) {
	t.Parallel()
	model := &project.Model{Dependencies: []project.Package{
		{Name: "vendor/package", Version: "1.0.0"},
		{Name: "shopware/core", Version: "v6.7.3.1"},
	}}
	resolved, err := NewVersionResolver(t.TempDir(), model, "").Resolve()
	require.NoError(t, err)
	require.Equal(t, project.Version{Major: 6, Minor: 7, Patch: 3}, resolved.Version)
	require.Equal(t, VersionSourceComposerLock, resolved.Source)
	require.True(t, resolved.Known)
	require.True(t, resolved.AtLeast(6, 7, 3))
	require.False(t, resolved.AtLeast(6, 7, 4))
}

func TestVersionResolverUsesPlatformComposerVersion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeVersionFixture(t, root, "composer.json", `{
  "name": "shopware/platform",
  "version": "6.6.10.0"
}`)
	resolved, err := NewVersionResolver(root, nil, "").Resolve()
	require.NoError(t, err)
	require.Equal(t, project.Version{Major: 6, Minor: 6, Patch: 10}, resolved.Version)
	require.Equal(t, VersionSourcePlatformComposer, resolved.Source)
	require.True(t, resolved.Known)
}

func TestVersionResolverUsesPlatformKernelFallback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeVersionFixture(t, root, "composer.json", `{"name":"shopware/platform"}`)
	writeVersionFixture(t, root, filepath.Join("src", "Core", "Kernel.php"), `<?php
// const SHOPWARE_FALLBACK_VERSION = '1.0.0';
final class Kernel
{
    final public const SHOPWARE_FALLBACK_VERSION = '6.7.9999999-dev';
}`)
	resolved, err := NewVersionResolver(root, nil, "").Resolve()
	require.NoError(t, err)
	require.Equal(t, project.Version{Major: 6, Minor: 7, Patch: 9999999}, resolved.Version)
	require.Equal(t, VersionSourcePlatformKernel, resolved.Source)
	require.True(t, resolved.Known)
}

func TestVersionResolverUsesConsistentPlatformBranchAliases(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeVersionFixture(t, root, "composer.json", `{
  "name": "shopware/platform",
  "extra": {"branch-alias": {
    "dev-main": "6.7.x-dev",
    "dev-trunk": "6.7.x-dev"
  }}
}`)
	resolved, err := NewVersionResolver(root, nil, "").Resolve()
	require.NoError(t, err)
	require.Equal(t, project.Version{Major: 6, Minor: 7}, resolved.Version)
	require.Equal(t, VersionSourcePlatformBranchAlias, resolved.Source)
	require.True(t, resolved.Known)
}

func TestVersionResolverLeavesAmbiguousOrUnrelatedWorkspacesUnknown(t *testing.T) {
	t.Parallel()
	t.Run("ambiguous aliases", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeVersionFixture(t, root, "composer.json", `{
  "name": "shopware/platform",
  "extra": {"branch-alias": {
    "dev-main": "6.7.x-dev",
    "dev-next": "6.8.x-dev"
  }}
}`)
		resolved, err := NewVersionResolver(root, nil, "").Resolve()
		require.NoError(t, err)
		require.False(t, resolved.Known)
		require.False(t, resolved.AtLeast(1, 0, 0))
	})

	t.Run("non-platform root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeVersionFixture(t, root, "composer.json", `{"name":"vendor/plugin"}`)
		writeVersionFixture(t, root, filepath.Join("src", "Core", "Kernel.php"), `<?php
final class Kernel {
    public const SHOPWARE_FALLBACK_VERSION = '6.8.0.0';
}`)
		resolved, err := NewVersionResolver(root, nil, "").Resolve()
		require.NoError(t, err)
		require.False(t, resolved.Known)
	})

	t.Run("missing composer", func(t *testing.T) {
		t.Parallel()
		resolved, err := NewVersionResolver(t.TempDir(), nil, "").Resolve()
		require.NoError(t, err)
		require.False(t, resolved.Known)
	})
}

func TestVersionResolverReportsMalformedPlatformComposer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeVersionFixture(t, root, "composer.json", `{`)
	_, err := NewVersionResolver(root, nil, "").Resolve()
	require.ErrorContains(t, err, "parse root composer.json")
}

func writeVersionFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
