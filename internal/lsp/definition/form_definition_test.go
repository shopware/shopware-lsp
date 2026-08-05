package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormDefinitionsNavigateTypesOptionsAndFields(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	formIndex, err := form.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, formIndex.Close()) })
	formIndex.SetPHPIndex(phpIndex)

	frameworkPath := filepath.Join(root, "vendor", "Form.php")
	framework := `<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormBuilderInterface {
    public function add(string $name, mixed $type = null, array $options = []): static;
}
interface FormInterface { public function get(string $name): self; }
interface FormFactoryInterface {
    public function create(string $type, mixed $data = null, array $options = []): FormInterface;
}
abstract class AbstractType implements FormTypeInterface {}`
	indexFormDefinitionFile(
		t,
		phpIndex,
		formIndex,
		frameworkPath,
		framework,
	)

	modelPath := filepath.Join(root, "src", "Profile.php")
	model := `<?php
namespace App\Model;
class Profile {
    public function setDisplayName(string $name): void {}
}`
	indexFormDefinitionFile(t, phpIndex, formIndex, modelPath, model)

	formPath := filepath.Join(root, "src", "ProfileType.php")
	formSource := `<?php
namespace App\Form;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\FormBuilderInterface;
class ProfileType extends AbstractType {
    public function getBlockPrefix(): string { return 'profile'; }
    public function configureOptions($resolver): void {
        $resolver->setDefaults([
            'data_class' => \App\Model\Profile::class,
            'translation_domain' => 'profile',
        ]);
    }
    public function buildForm(FormBuilderInterface $builder, array $options): void {
        $builder->add('displayName');
    }
    public function buildView($view, $form, array $options): void {
        $view->vars['profile_theme'] = 'standard';
    }
}`
	indexFormDefinitionFile(
		t,
		phpIndex,
		formIndex,
		formPath,
		formSource,
	)

	usagePath := filepath.Join(root, "src", "Usage.php")
	usage := `<?php
namespace App;
use Symfony\Component\Form\FormBuilderInterface;
use Symfony\Component\Form\FormFactoryInterface;
function build(FormBuilderInterface $builder, FormFactoryInterface $factory): void {
    $builder->add('field', 'profile', ['translation_domain' => 'messages']);
    $form = $factory->create('profile');
    $form->get('displayName');
}`
	indexFormDefinitionFile(t, phpIndex, formIndex, usagePath, usage)
	document := lsp.NewTextDocument(uriutil.FileURI(usagePath), usage, 1)
	provider := NewFormDefinitionProvider(formIndex, phpIndex)

	typeLocations := formDefinitionsAt(
		t,
		provider,
		phpIndex,
		document,
		usagePath,
		strings.Index(usage, "'profile'")+2,
	)
	require.Len(t, typeLocations, 1)
	assert.Equal(t, uriutil.FileURI(formPath), typeLocations[0].URI)

	optionLocations := formDefinitionsAt(
		t,
		provider,
		phpIndex,
		document,
		usagePath,
		strings.Index(usage, "'translation_domain'")+2,
	)
	require.Len(t, optionLocations, 1)
	assert.Equal(t, uriutil.FileURI(formPath), optionLocations[0].URI)

	fieldLocations := formDefinitionsAt(
		t,
		provider,
		phpIndex,
		document,
		usagePath,
		strings.Index(usage, "'displayName'")+2,
	)
	require.Len(t, fieldLocations, 2)
	uris := []string{fieldLocations[0].URI, fieldLocations[1].URI}
	assert.Contains(t, uris, uriutil.FileURI(formPath))
	assert.Contains(t, uris, uriutil.FileURI(modelPath))

	controllerPath := filepath.Join(root, "src", "ProfileController.php")
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
	indexFormDefinitionFile(
		t,
		phpIndex,
		formIndex,
		controllerPath,
		controllerSource,
	)
	templatePath := filepath.Join(root, "templates", "profile", "edit.html.twig")
	templateSource := `{{ profile.displayName }}
{{ profile.vars.profile_theme }}`
	template := lsp.NewTextDocument(
		uriutil.FileURI(templatePath),
		templateSource,
		1,
	)
	templateOffset := strings.Index(templateSource, "displayName") + 2
	templateNode := template.SyntaxTree.Root.NodeAtOffset(
		uint32(templateOffset),
	)
	templateRequest := consoleDefinitionRequest(template, templateNode)
	line, character := template.LineIndex.PositionUTF16(
		uint32(templateOffset),
	)
	templateRequest.Position.Line = int(line)
	templateRequest.Position.Character = int(character)
	twigFieldLocations := provider.GetDefinition(
		context.Background(),
		templateRequest,
	)
	require.Len(t, twigFieldLocations, 2)
	twigURIs := []string{
		twigFieldLocations[0].URI,
		twigFieldLocations[1].URI,
	}
	assert.Contains(t, twigURIs, uriutil.FileURI(formPath))
	assert.Contains(t, twigURIs, uriutil.FileURI(modelPath))

	viewVarOffset := strings.Index(templateSource, "profile_theme") + 2
	viewVarNode := template.SyntaxTree.Root.NodeAtOffset(
		uint32(viewVarOffset),
	)
	viewVarRequest := consoleDefinitionRequest(template, viewVarNode)
	line, character = template.LineIndex.PositionUTF16(uint32(viewVarOffset))
	viewVarRequest.Position.Line = int(line)
	viewVarRequest.Position.Character = int(character)
	viewVarLocations := provider.GetDefinition(
		context.Background(),
		viewVarRequest,
	)
	require.Len(t, viewVarLocations, 1)
	assert.Equal(t, uriutil.FileURI(formPath), viewVarLocations[0].URI)
}

func TestPHPDocFormTypeAssistantTagDefinition(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, "php"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	formIndex, err := form.NewIndex(filepath.Join(root, "form"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, formIndex.Close()) })
	formIndex.SetPHPIndex(phpIndex)

	frameworkPath := filepath.Join(root, "vendor", "Form.php")
	indexFormDefinitionFile(
		t,
		phpIndex,
		formIndex,
		frameworkPath,
		`<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
abstract class AbstractType implements FormTypeInterface {}
`,
	)
	formPath := filepath.Join(root, "src", "ProfileType.php")
	indexFormDefinitionFile(
		t,
		phpIndex,
		formIndex,
		formPath,
		`<?php
namespace App\Form;
class ProfileType extends \Symfony\Component\Form\AbstractType {
    public function getBlockPrefix(): string { return 'profile'; }
}
`,
	)
	assistantPath := filepath.Join(root, "src", "FormAssistant.php")
	indexFormDefinitionFile(
		t,
		phpIndex,
		formIndex,
		assistantPath,
		`<?php
/** @param string $type #FormType */
function resolve_form(string $type): void {}
`,
	)
	usagePath := filepath.Join(root, "src", "Usage.php")
	usage := "<?php resolve_form('profile');"
	document := lsp.NewTextDocument(uriutil.FileURI(usagePath), usage, 1)
	locations := formDefinitionsAt(
		t,
		NewFormDefinitionProvider(formIndex, phpIndex),
		phpIndex,
		document,
		usagePath,
		strings.Index(usage, "profile")+2,
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(formPath), locations[0].URI)
}

func indexFormDefinitionFile(
	t *testing.T,
	phpIndex *php.PHPIndex,
	formIndex *form.Index,
	path,
	source string,
) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, formIndex.Index(parsed))
}

func formDefinitionsAt(
	t *testing.T,
	provider *FormDefinitionProvider,
	phpIndex *php.PHPIndex,
	document *lsp.TextDocument,
	path string,
	offset int,
) []protocol.Location {
	t.Helper()
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	return provider.GetDefinition(
		ctx,
		consoleDefinitionRequest(document, node),
	)
}
