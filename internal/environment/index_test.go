package environment

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexCollectsEnvironmentDeclarationsAndReferences(t *testing.T) {
	cacheDir := t.TempDir()
	idx, err := NewIndex(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	files := map[string]string{
		filepath.Join(cacheDir, ".env"): `
APP_ENV=dev
export DATABASE_URL = "mysql://localhost/app" # local database
EMPTY=
`,
		filepath.Join(cacheDir, "env.env"): `
APP_ENV=test
`,
		filepath.Join(cacheDir, "Dockerfile"): `
ENV APP_RUNTIME Runtime
ENV PHP_MEMORY_LIMIT=512M APP_DEBUG=1
`,
		filepath.Join(cacheDir, "docker-compose.override.yaml"): `
services:
  app:
    environment:
      DATABASE_HOST: database
      - APP_SECRET=not-secret
      - WORKER_ENABLED
    labels:
      example: value
`,
		filepath.Join(cacheDir, "config", "services.yaml"): `
parameters:
  database_url: '%env(resolve:DATABASE_URL)%'
  memory_limit: '%env(int:PHP_MEMORY_LIMIT)%'
`,
		filepath.Join(cacheDir, "src", "Config.php"): `<?php
#[Autowire('%env(default:app.env:APP_ENV)%')]
#[Autowire(env: 'resolve:DATABASE_URL')]
final class Config {}
env('bool:APP_ENV');
`,
	}
	for path, source := range files {
		require.NoError(t, idx.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	names, err := idx.Names()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"APP_DEBUG",
		"APP_ENV",
		"APP_RUNTIME",
		"APP_SECRET",
		"DATABASE_HOST",
		"DATABASE_URL",
		"EMPTY",
		"PHP_MEMORY_LIMIT",
		"WORKER_ENABLED",
	}, names)

	databaseURL, found, err := idx.Variable("DATABASE_URL")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, databaseURL.Declarations, 1)
	assert.Equal(
		t,
		"mysql://localhost/app",
		databaseURL.Declarations[0].Value,
	)
	require.Len(t, databaseURL.References, 2)
	assert.Equal(
		t,
		[]string{"resolve"},
		databaseURL.References[0].Processors,
	)
	assert.Equal(
		t,
		[]string{"resolve"},
		databaseURL.References[1].Processors,
	)

	appEnv, found, err := idx.Variable("APP_ENV")
	require.NoError(t, err)
	require.True(t, found)
	assert.Len(t, appEnv.Declarations, 2)
	require.Len(t, appEnv.References, 2)
	assert.Equal(
		t,
		[]string{"default", "app.env"},
		appEnv.References[0].Processors,
	)
	assert.Equal(t, []string{"bool"}, appEnv.References[1].Processors)
}

func TestPHPEnvironmentCandidateRecognizesWhitespaceBeforeCall(t *testing.T) {
	assert.True(t, hasPHPEnvCallCandidate("<?php env ('APP_ENV');"))
	assert.True(t, hasPHPEnvCallCandidate("<?php \\env\n('APP_ENV');"))
	assert.False(t, hasPHPEnvCallCandidate("<?php $environment = 'dev';"))
}

func TestIndexRestoresAndRemovesEnvironmentOccurrences(t *testing.T) {
	cacheDir := t.TempDir()
	path := filepath.Join(cacheDir, ".env.local")
	idx, err := NewIndex(cacheDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte("APP_SECRET=temporary"),
	)))
	require.NoError(t, idx.Close())

	restored, err := NewIndex(cacheDir)
	require.NoError(t, err)
	variable, found, err := restored.Variable("APP_SECRET")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, variable.Declarations, 1)
	require.NoError(t, restored.RemovedFiles([]string{path}))
	_, found, err = restored.Variable("APP_SECRET")
	require.NoError(t, err)
	assert.False(t, found)
	require.NoError(t, restored.Close())
}

func TestEnvironmentSupplementalPathSelection(t *testing.T) {
	idx := &Index{}
	assert.True(t, idx.ShouldIndexPath("/project/.env"))
	assert.True(t, idx.ShouldIndexPath("/project/.env.test"))
	assert.True(t, idx.ShouldIndexPath("/project/config/env.env"))
	assert.True(t, idx.ShouldIndexPath("/project/Dockerfile"))
	assert.True(t, idx.ShouldIndexPath("/project/Dockerfile.dev"))
	assert.False(t, idx.ShouldIndexPath("/project/services.yaml"))
	assert.False(t, idx.ShouldIndexPath("/project/config.json"))
	assert.False(t, idx.ShouldEnterDirectory("/project/vendor"))
}

func TestEnvironmentPathClassificationDoesNotAllocate(t *testing.T) {
	var matched bool
	allocations := testing.AllocsPerRun(100, func() {
		matched = isDotEnvPath("/project/.ENV.local") &&
			isDockerfilePath("/project/DOCKERFILE.dev") &&
			isDockerComposePath("/project/Docker-Compose.override.YAML") &&
			supportsSymfonyEnvReference("/project/config/services.XML")
	})
	require.Zero(t, allocations)
	require.True(t, matched)
}
