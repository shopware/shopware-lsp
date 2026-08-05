package validation

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestConstraintOptionReferenceAndInheritedProperties(t *testing.T) {
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
	source := `<?php
namespace App\Validation;
class UniqueName extends \Symfony\Component\Validator\Constraint {
    public string $repositoryMethod;
    protected string $internal;
}
function validate(): void {
    new UniqueName(['repositoryMethod' => 'find', 'message' => 'key']);
}
`
	path := "/project/src/UniqueName.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	root := parsed.SyntaxTree().Root
	offset := uint32(strings.Index(source, "repositoryMethod'") + 2)
	node := root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		root,
	)
	reference, found := OptionReferenceAt(ctx, root, node)
	require.True(t, found)
	assert.Equal(t, "repositoryMethod", reference.Name)
	assert.Equal(t, "App\\Validation\\UniqueName", reference.Constraint)

	properties := ConstraintPropertiesInContext(ctx, reference.Constraint)
	names := make([]string, 0, len(properties))
	for _, property := range properties {
		names = append(names, property.Name)
	}
	assert.ElementsMatch(t, []string{
		"groups",
		"message",
		"repositoryMethod",
	}, names)
}
