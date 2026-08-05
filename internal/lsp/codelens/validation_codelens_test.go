package codelens

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestValidationCodeLensLinksConstraintAndValidator(t *testing.T) {
	root := t.TempDir()
	stubPath := filepath.Join(root, "vendor", "Validator.php")
	constraintPath := filepath.Join(root, "src", "UniqueName.php")
	validatorPath := filepath.Join(root, "src", "UniqueNameValidator.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(stubPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(constraintPath), 0o755))
	stubSource := `<?php
namespace Symfony\Component\Validator;
class Constraint {}
interface ConstraintValidatorInterface {}
`
	constraintSource := `<?php
namespace App\Validation;
class UniqueName extends \Symfony\Component\Validator\Constraint {}
`
	validatorSource := `<?php
namespace App\Validation;
class UniqueNameValidator implements \Symfony\Component\Validator\ConstraintValidatorInterface {}
`
	for path, source := range map[string]string{
		stubPath:       stubSource,
		constraintPath: constraintSource,
		validatorPath:  validatorSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		stubPath:       stubSource,
		constraintPath: constraintSource,
		validatorPath:  validatorSource,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	for path, source := range map[string]string{
		constraintPath: constraintSource,
		validatorPath:  validatorSource,
	} {
		document := lsp.NewTextDocument(
			uriutil.FileURI(path),
			source,
			1,
		)
		params := &protocol.CodeLensParams{}
		params.TextDocument.URI = document.URI
		lenses, lensErr := NewValidationCodeLensProvider(
			phpIndex,
		).GetCodeLenses(
			context.Background(),
			&lsp.CodeLensRequest{
				CodeLensParams: params,
				Document:       document,
			},
		)
		require.NoError(t, lensErr)
		require.Len(t, lenses, 1)
		require.NotNil(t, lenses[0].Command)
		if path == constraintPath {
			assert.Equal(t, "Open validator", lenses[0].Command.Title)
			assert.Equal(t, []string{
				relatedTarget(validatorPath, 3),
			}, relatedLensTargets(t, lenses[0]))
		} else {
			assert.Equal(t, "Open constraint", lenses[0].Command.Title)
			assert.Equal(t, []string{
				relatedTarget(constraintPath, 3),
			}, relatedLensTargets(t, lenses[0]))
		}
	}
}

func TestValidationCodeLensLinksConstraintMessageToTranslations(
	t *testing.T,
) {
	root := t.TempDir()
	stubPath := filepath.Join(root, "vendor", "Constraint.php")
	constraintPath := filepath.Join(root, "src", "UniqueName.php")
	translationPath := filepath.Join(
		root,
		"translations",
		"validators.en.yaml",
	)
	for _, path := range []string{
		stubPath,
		constraintPath,
		translationPath,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	}
	stubSource := `<?php
namespace Symfony\Component\Validator;
class Constraint {}
`
	constraintSource := `<?php
namespace App\Validation;
final class UniqueName extends \Symfony\Component\Validator\Constraint
{
    public string $message = 'validator.unique_name';
}
`
	translationSource := `validator.unique_name: 'The name is already used.'
`
	for path, source := range map[string]string{
		stubPath:        stubSource,
		constraintPath:  constraintSource,
		translationPath: translationSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	translationIndex, err := translation.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, translationIndex.Close()) })
	for path, source := range map[string]string{
		stubPath:       stubSource,
		constraintPath: constraintSource,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	require.NoError(t, translationIndex.Index(indexer.NewParsedFile(
		translationPath,
		[]byte(translationSource),
	)))

	lenses := relatedCodeLensesFor(
		t,
		NewValidationCodeLensProvider(phpIndex, translationIndex),
		constraintPath,
		constraintSource,
	)
	require.Len(t, lenses, 1)
	assert.Equal(
		t,
		"Open validator translation",
		lenses[0].Command.Title,
	)
	assert.Equal(t, 4, lenses[0].Range.Start.Line)
	assert.Equal(t, []string{
		relatedTarget(translationPath, 1),
	}, relatedLensTargets(t, lenses[0]))
}
