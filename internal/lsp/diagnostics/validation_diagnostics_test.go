package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestConstraintOptionDiagnosticsReportUnknownProperty(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Constraint.php",
		[]byte(`<?php
namespace Symfony\Component\Validator;
class Constraint { public string $message; public array $groups; }
`),
	)))
	source := []byte(`<?php
class UniqueName extends \Symfony\Component\Validator\Constraint {}
new UniqueName(['messag' => 'key', 'groups' => ['Default']]);
`)
	path := "/project/src/Validation.php"
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, source)))
	diagnostics, err := NewValidationAnalyzer(
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument("file://"+path, source),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, missingConstraintOptionCode, diagnostics[0].ID)
	assert.Contains(
		t,
		diagnostics[0].Payload.(map[string]any)["suggestions"],
		"message",
	)
}
