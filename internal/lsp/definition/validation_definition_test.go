package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestConstraintOptionDefinitionNavigatesProperty(t *testing.T) {
	root := t.TempDir()
	constraintPath := filepath.Join(root, "vendor", "Constraint.php")
	usagePath := filepath.Join(root, "src", "Validation.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(constraintPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(usagePath), 0o755))
	constraintSource := `<?php
namespace Symfony\Component\Validator;
class Constraint { public string $message; }
`
	usageSource := `<?php
class UniqueName extends \Symfony\Component\Validator\Constraint {}
new UniqueName(['message' => 'key']);
`
	require.NoError(t, os.WriteFile(
		constraintPath,
		[]byte(constraintSource),
		0o644,
	))
	require.NoError(t, os.WriteFile(usagePath, []byte(usageSource), 0o644))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		constraintPath: constraintSource,
		usagePath:      usageSource,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(usagePath),
		usageSource,
		1,
	)
	offset := uint32(strings.Index(usageSource, "message") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		usagePath,
		1,
		node,
		document.SyntaxTree.Root,
	)
	locations := NewValidationDefinitionProvider().GetDefinition(
		ctx,
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(constraintPath), locations[0].URI)
}
