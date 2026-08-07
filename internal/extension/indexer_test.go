package extension

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestExtensionIndexerReusesPreparedPHPAnalysis(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	extensionIndex, err := NewExtensionIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, extensionIndex.Close()) })
	extensionIndex.SetPHPIndex(phpIndex)

	file := indexer.NewParsedFile(
		"/project/src/DemoPlugin.php",
		[]byte(`<?php
namespace App;
use Shopware\Core\Framework\Plugin;
final class DemoPlugin extends Plugin {}
`),
	)
	document := phpIndex.AnalyzeParsedFile(file)
	prepared, err := extensionIndex.Prepare(file)
	require.NoError(t, err)
	require.Same(t, document, phpIndex.AnalyzeParsedFile(file))
	require.NoError(t, extensionIndex.IndexPrepared(file, prepared))

	indexed := extensionIndex.GetExtensionByName("DemoPlugin")
	require.NotNil(t, indexed)
	require.Equal(t, ShopwareExtensionTypeBundle, indexed.Type)
	require.Equal(t, file.Path, indexed.Path)
	require.Equal(t, filepath.Dir(file.Path), indexed.GetRootPath())
	require.Equal(
		t,
		filepath.Join(
			filepath.Dir(file.Path),
			"Resources/app/administration/src",
		),
		indexed.GetAdministrationSourcePath(),
	)
}
