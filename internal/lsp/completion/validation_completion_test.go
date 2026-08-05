package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestConstraintOptionCompletionIncludesInheritedPublicProperties(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Constraint.php",
		[]byte(`<?php
namespace Symfony\Component\Validator;
class Constraint {
    public string $message;
    public array $groups;
}
`),
	)))
	source := `<?php
namespace App;
class UniqueName extends \Symfony\Component\Validator\Constraint {
    public string $repositoryMethod;
    protected string $internal;
}
new UniqueName(['' => 'value']);
`
	path := "/project/src/UniqueName.php"
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := uint32(strings.LastIndex(source, "''") + 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	items := NewValidationCompletionProvider().GetCompletions(
		ctx,
		securityCompletionRequest(document, node, offset),
	)
	requireCompletion(t, items, "message")
	requireCompletion(t, items, "groups")
	requireCompletion(t, items, "repositoryMethod")
}
