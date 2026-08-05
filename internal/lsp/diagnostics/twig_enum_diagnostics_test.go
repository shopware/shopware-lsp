package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigEnumDiagnosticsValidateExistenceAndEnumKind(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "Types.php"),
		[]byte(`<?php
namespace App;
enum OrderStatus { case Open; }
class NotAnEnum {}`),
	)))
	source := `{{ enum('App\\OrderStatus') }}
{{ enum_cases('App\\OrderStatu') }}
{{ enum('App\\NotAnEnum') }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates", "order.html.twig")),
		source,
		1,
	)
	diagnostics, err := NewTwigEnumAnalyzer(
		phpIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, diagnostics, 2)
	assert.Equal(t, missingTwigEnumCode, diagnostics[0].ID)
	assert.Contains(t, diagnostics[0].Message, "OrderStatu")
	data, ok := diagnostics[0].Payload.(map[string]any)
	require.True(t, ok)
	assert.Contains(
		t,
		data["suggestions"],
		`App\\OrderStatus`,
	)
	assert.Equal(t, invalidTwigEnumCode, diagnostics[1].ID)
	assert.Contains(t, diagnostics[1].Message, "not an enum")
}
