package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/serializer"
)

func TestSerializerDiagnosticsReportMissingStringClass(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Model.php",
		[]byte("<?php namespace App; class Model {}"),
	)))
	serializerIndex, err := serializer.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serializerIndex.Close()) })
	source := []byte(`<?php
$serializer->deserialize($data, 'App\Modle[]', 'json');
$serializer->deserialize($data, 'App\Model', 'json');
`)
	diagnostics, err := NewSerializerAnalyzer(
		serializerIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument("file:///project/src/Handler.php", source),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, missingSerializerClassCode, diagnostics[0].ID)
	assert.Contains(t, diagnostics[0].Message, "App\\Modle")
	assert.Contains(
		t,
		diagnostics[0].Payload.(map[string]any)["suggestions"],
		"App\\Model",
	)
}
