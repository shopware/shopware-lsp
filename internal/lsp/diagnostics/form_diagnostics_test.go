package diagnostics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormDiagnosticsReportMissingTypesOptionsAndFields(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	formIndex, err := form.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, formIndex.Close()) })
	formIndex.SetPHPIndex(phpIndex)

	for path, source := range map[string]string{
		"/project/vendor/Form.php": `<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormBuilderInterface {
    public function add(string $name, mixed $type = null, array $options = []): static;
}
interface FormInterface { public function get(string $name): self; }
interface FormFactoryInterface {
    public function create(string $type, mixed $data = null, array $options = []): FormInterface;
}
abstract class AbstractType implements FormTypeInterface {}`,
		"/project/src/Profile.php": `<?php
namespace App\Model;
class Profile { public function setDisplayName(string $name): void {} }`,
	} {
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, formIndex.Index(parsed))
	}

	source := `<?php
namespace App\Form;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\FormBuilderInterface;
use Symfony\Component\Form\FormFactoryInterface;
class ProfileType extends AbstractType {
    public function getBlockPrefix(): string { return 'profile'; }
    public function configureOptions($resolver): void {
        $resolver->setDefaults([
            'data_class' => \App\Model\Profile::class,
            'translation_domain' => 'profile',
            'mapped' => true,
        ]);
    }
    public function buildForm(FormBuilderInterface $builder, array $options): void {
        $builder->add('displayNmae');
        $builder->add('other', 'profile', [
            'mapped' => false,
            'translatoin_domain' => true,
        ]);
    }
    public function buildView($view, $form, array $options): void {
        $view->vars['profile_theme'] = 'standard';
    }
}
function useForms(FormFactoryInterface $factory): void {
    $factory->create('profiel');
    $form = $factory->create('profile');
    $form->get('missing');
}`
	path := "/project/src/ProfileType.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, formIndex.Index(parsed))

	result, err := NewFormAnalyzer(
		formIndex,
		phpIndex,
	).Analyze(
		context.Background(),
		diagnosticsDocument("file://"+path, []byte(source)),
	)
	require.NoError(t, err)
	require.Len(t, result, 4)
	codes := make([]any, 0, len(result))
	for _, diagnostic := range result {
		codes = append(codes, diagnostic.ID)
		assert.NotEmpty(t, diagnostic.Payload)
	}
	assert.ElementsMatch(t, []any{
		missingFormTypeCode,
		missingFormOptionCode,
		missingFormFieldCode,
		missingFormFieldCode,
	}, codes)

	controllerSource := `<?php
namespace App\Controller;
class ProfileController {
    public function edit() {
        return $this->render('profile/edit.html.twig', [
            'profile' => $this->createForm(
                \App\Form\ProfileType::class,
            )->createView(),
        ]);
    }
}`
	controller := indexer.NewParsedFile(
		"/project/src/ProfileController.php",
		[]byte(controllerSource),
	)
	require.NoError(t, phpIndex.Index(controller))
	require.NoError(t, formIndex.Index(controller))
	template := diagnosticsDocument(
		"file:///project/templates/profile/edit.html.twig",
		[]byte(`{{ profile.displayNmae }}
{{ profile.displayName }}
{{ profile.vars }}
{{ profile.vars.profile_theme }}
{{ profile.vars.profile_them }}
{{ ordinary.missing }}`),
	)
	twigDiagnostics, err := NewFormAnalyzer(
		formIndex,
		phpIndex,
	).Analyze(context.Background(), template)
	require.NoError(t, err)
	require.Len(t, twigDiagnostics, 2)
	byCode := make(map[lsp.DiagnosticID]lsp.Problem)
	for _, diagnostic := range twigDiagnostics {
		byCode[diagnostic.ID] = diagnostic
	}
	fieldDiagnostic := byCode[missingFormFieldCode]
	assert.Contains(t, fieldDiagnostic.Message, "displayName")
	assert.Contains(
		t,
		fieldDiagnostic.Payload.(map[string]any)["suggestions"],
		"displayNmae",
	)
	viewVarDiagnostic := byCode[missingFormViewVarCode]
	assert.Contains(t, viewVarDiagnostic.Message, "profile_them")
	assert.Contains(
		t,
		viewVarDiagnostic.Payload.(map[string]any)["suggestions"],
		"profile_theme",
	)
}

func TestFormDiagnosticsReportLegacyBuilderTypeAliases(t *testing.T) {
	provider := legacyFormDiagnosticsFixture(t, "v7.3.2")
	document := lsp.NewTextDocument(
		"file:///project/src/Form.php",
		`<?php
namespace App;

use Symfony\Component\Form\FormBuilderInterface;
use Symfony\Component\Form\FormFactoryInterface;

function build(
    FormBuilderInterface $builder,
    FormFactoryInterface $factory,
): void {
    $builder->add('first', 'legacy');
    $builder->create('second', 'missing');
    $builder->add('third', 'App\Form\LegacyType');
    $factory->create('legacy');
}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)

	var legacyDiagnostics []lsp.Problem
	for _, diagnostic := range result {
		if diagnostic.ID == legacyFormTypeAliasCode {
			legacyDiagnostics = append(legacyDiagnostics, diagnostic)
		}
	}
	require.Len(t, legacyDiagnostics, 2, fmt.Sprintf("%#v", result))
	assert.Equal(
		t,
		"legacy",
		problemRangeText(document, legacyDiagnostics[0].Range),
	)
	assert.Equal(
		t,
		"App\\Form\\LegacyType",
		legacyDiagnostics[0].Payload.(map[string]any)["className"],
	)
	assert.Empty(
		t,
		legacyDiagnostics[1].Payload.(map[string]any)["className"],
	)
}

func TestFormDiagnosticsKeepAliasesBeforeSymfony28(t *testing.T) {
	provider := legacyFormDiagnosticsFixture(t, "v2.7.9")
	document := lsp.NewTextDocument(
		"file:///project/src/Form.php",
		`<?php
use Symfony\Component\Form\FormBuilderInterface;
function build(FormBuilderInterface $builder): void {
    $builder->add('first', 'legacy');
}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	for _, diagnostic := range result {
		assert.NotEqual(t, legacyFormTypeAliasCode, diagnostic.ID)
	}
}

func TestFormDiagnosticsValidatePHPDocAssistantReferences(t *testing.T) {
	provider := legacyFormDiagnosticsFixture(t, "v7.3.2")
	require.NoError(t, provider.phpIndex.Index(indexer.NewParsedFile(
		"/project/src/FormAssistant.php",
		[]byte(`<?php
/** @param string $type #FormType */
function resolve_form(string $type): void {}
`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/src/Usage.php",
		`<?php
resolve_form('legcy');
resolve_form('legacy');
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, missingFormTypeCode, result[0].ID)
	assert.Equal(t, "legcy", problemRangeText(document, result[0].Range))
	assert.Contains(
		t,
		problemSuggestionStrings(result[0]),
		"legacy",
	)
}

func legacyFormDiagnosticsFixture(
	t *testing.T,
	symfonyVersion string,
) *FormAnalyzer {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.json"),
		[]byte(`{}`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.lock"),
		[]byte(fmt.Sprintf(`{
  "packages": [{
    "name": "symfony/http-kernel",
    "version": %q
  }]
}`, symfonyVersion)),
		0o644,
	))
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.ConfigureProject(root))
	formIndex, err := form.NewIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, formIndex.Close()) })
	formIndex.SetPHPIndex(phpIndex)

	for path, source := range map[string]string{
		filepath.Join(root, "vendor/Form.php"): `<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormBuilderInterface {
    public function add(string $name, mixed $type = null): static;
    public function create(string $name, mixed $type = null): static;
}
interface FormFactoryInterface {
    public function create(string $type): mixed;
}
abstract class AbstractType implements FormTypeInterface {}`,
		filepath.Join(root, "src/LegacyType.php"): `<?php
namespace App\Form;
use Symfony\Component\Form\AbstractType;
class LegacyType extends AbstractType {
    public function getBlockPrefix(): string { return 'legacy'; }
}`,
	} {
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, formIndex.Index(parsed))
	}
	return NewFormAnalyzer(formIndex, phpIndex)
}
