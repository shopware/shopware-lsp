package codeaction

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

func TestFormFieldGeneratorActionOnlyInsideTypedBuildForm(t *testing.T) {
	provider, _, _ := newFormFieldGeneratorFixture(t)
	source := formFieldGeneratorSource("")
	request := symfonyGeneratorCodeActionRequest(
		"file:///project/src/Form/ProfileType.php",
		source,
		"$builder->add",
	)
	actions := provider.GetCodeActions(context.Background(), request)
	require.Len(t, actions, 1)
	assert.Equal(t, "Symfony: Generate form fields", actions[0].Title)
	assert.Equal(t, generateFormFieldsAction, actions[0].Command.Command)
	assert.Equal(
		t,
		[]any{
			"file:///project/src/Form/ProfileType.php",
			"App\\Form\\ProfileType",
		},
		actions[0].Command.Arguments,
	)

	outside := symfonyGeneratorCodeActionRequest(
		"file:///project/src/Form/ProfileType.php",
		source,
		"configureOptions",
	)
	assert.Empty(t, provider.GetCodeActions(context.Background(), outside))
}

func TestFormFieldGeneratorRecognizesDirectSymfonyTypesWithoutVendorSources(
	t *testing.T,
) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	forms, err := form.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, forms.Close()) })
	forms.SetPHPIndex(phpIndex)
	provider := NewFormFieldGeneratorProvider(forms, phpIndex, nil)
	source := `<?php
namespace App\Form;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\FormBuilderInterface;
class CheckoutType extends AbstractType
{
    public function buildForm(FormBuilderInterface $builder, array $options): void
    {
    }
}
`
	actions := provider.GetCodeActions(
		context.Background(),
		symfonyGeneratorCodeActionRequest(
			"file:///project/src/Form/CheckoutType.php",
			source,
			"function buildForm",
		),
	)
	require.Len(t, actions, 1)
	assert.Equal(t, generateFormFieldsAction, actions[0].Command.Command)
}

func TestFormFieldGeneratorCandidatesUseSemanticDataClass(t *testing.T) {
	provider, _, _ := newFormFieldGeneratorFixture(t)
	source := formFieldGeneratorSource("")
	raw := mustGeneratorJSON(t, formFieldGeneratorRequest{
		FileURI:   "file:///project/src/Form/ProfileType.php",
		ClassName: "App\\Form\\ProfileType",
		Source:    source,
		Version:   4,
	})
	value, err := provider.formFieldCandidates(
		context.Background(),
		&raw,
	)
	require.NoError(t, err)
	result := value.(formFieldCandidatesResponse)

	assert.Equal(t, "App\\Model\\Profile", result.DataClass)
	assert.Equal(
		t,
		[]string{
			"active:bool:CheckboxType",
			"createdAt:DateTimeImmutable:DateTimeType",
			"description:null|string:TextareaType",
			"password:string:PasswordType",
			"product:App\\Model\\Product:EntityType",
			"status:App\\Model\\Status:EnumType",
			"title:string:TextType",
		},
		formCandidateStrings(result.Fields),
	)
	for _, candidate := range result.Fields {
		assert.NotEqual(t, "email", candidate.Name)
	}
}

func TestFormFieldGeneratorWritesFieldsOptionsAndImports(t *testing.T) {
	provider, _, _ := newFormFieldGeneratorFixture(t)
	source := formFieldGeneratorSource("")
	raw := mustGeneratorJSON(t, formFieldGeneratorRequest{
		FileURI:   "file:///project/src/Form/ProfileType.php",
		ClassName: "App\\Form\\ProfileType",
		Source:    source,
		Version:   4,
		SelectedFields: []string{
			"status",
			"description",
			"createdAt",
			"product",
		},
	})
	value, err := provider.generateFormFields(
		context.Background(),
		&raw,
	)
	require.NoError(t, err)
	result := value.(formFieldGenerationResponse)

	assert.Contains(
		t,
		result.Content,
		"use Symfony\\Bridge\\Doctrine\\Form\\Type\\EntityType;",
	)
	assert.Contains(
		t,
		result.Content,
		"use Symfony\\Component\\Form\\Extension\\Core\\Type\\DateTimeType;",
	)
	assert.Contains(
		t,
		result.Content,
		"use Symfony\\Component\\Form\\Extension\\Core\\Type\\EnumType;",
	)
	assert.Contains(
		t,
		result.Content,
		"use Symfony\\Component\\Form\\Extension\\Core\\Type\\TextareaType;",
	)
	assert.Contains(
		t,
		result.Content,
		"$builder->add('createdAt', DateTimeType::class, "+
			"['input' => 'datetime_immutable']);",
	)
	assert.Contains(
		t,
		result.Content,
		"$builder->add('description', TextareaType::class);",
	)
	assert.Contains(
		t,
		result.Content,
		"$builder->add('product', EntityType::class, "+
			"['class' => Product::class]);",
	)
	assert.Contains(
		t,
		result.Content,
		"$builder->add('status', EnumType::class, "+
			"['class' => Status::class]);",
	)
	assert.Equal(t, 1, strings.Count(result.Content, "->add('email'"))
	assert.Empty(t, phpparser.Parse(result.Content).Errors)
}

func TestFormFieldGeneratorFallsBackToQualifiedNamesOnImportConflict(
	t *testing.T,
) {
	provider, _, _ := newFormFieldGeneratorFixture(t)
	source := formFieldGeneratorSource(`
use App\Other\EnumType;
use App\Other\Status;
`)
	raw := mustGeneratorJSON(t, formFieldGeneratorRequest{
		FileURI:        "file:///project/src/Form/ProfileType.php",
		ClassName:      "App\\Form\\ProfileType",
		Source:         source,
		Version:        5,
		SelectedFields: []string{"status"},
	})
	value, err := provider.generateFormFields(
		context.Background(),
		&raw,
	)
	require.NoError(t, err)
	result := value.(formFieldGenerationResponse)
	assert.Contains(
		t,
		result.Content,
		"$builder->add('status', "+
			"\\Symfony\\Component\\Form\\Extension\\Core\\Type\\"+
			"EnumType::class, ['class' => "+
			"\\App\\Model\\Status::class]);",
	)
	assert.NotContains(
		t,
		result.Content,
		"use Symfony\\Component\\Form\\Extension\\Core\\Type\\EnumType;",
	)
	assert.Empty(t, phpparser.Parse(result.Content).Errors)
}

func TestDataFieldsForClassInSnapshotIncludesPublicSetProperties(
	t *testing.T,
) {
	_, phpIndex, _ := newFormFieldGeneratorFixture(t)
	snapshot := phpIndex.SemanticSnapshot()
	fields := form.DataFieldsForClassInSnapshot(
		snapshot,
		"App\\Model\\Profile",
	)
	var passwordSymbol semantic.Symbol
	for _, field := range fields {
		if field.Name == "password" {
			passwordSymbol = field.Symbol
			break
		}
	}
	assert.Equal(t, semantic.MethodSymbol, passwordSymbol.Kind)
}

func newFormFieldGeneratorFixture(
	t *testing.T,
) (*FormFieldGeneratorProvider, *php.PHPIndex, *doctrine.Index) {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	files := map[string]string{
		"/vendor/Form.php": `<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormBuilderInterface {}
abstract class AbstractType implements FormTypeInterface {}
`,
		"/project/src/Model/BaseProfile.php": `<?php
namespace App\Model;
class BaseProfile
{
    public string $title;
}
`,
		"/project/src/Model/Profile.php": `<?php
namespace App\Model;
class Profile extends BaseProfile
{
    public string $email;
    public ?string $description = null;
    public bool $active = false;
    public \DateTimeImmutable $createdAt;
    public Status $status;
    public Product $product;
    private string $password;

    public function setPassword(string $password): void {}
}

enum Status: string
{
    case Active = 'active';
}
`,
		"/project/src/Model/Product.php": `<?php
namespace App\Model;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Product {}
`,
	}
	for path, source := range files {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	root := t.TempDir()
	forms, err := form.NewIndex(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, forms.Close()) })
	forms.SetPHPIndex(phpIndex)
	doctrineIndex, err := doctrine.NewIndex(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	require.NoError(t, doctrineIndex.Index(indexer.NewParsedFile(
		"/project/src/Model/Product.php",
		[]byte(files["/project/src/Model/Product.php"]),
	)))
	return NewFormFieldGeneratorProvider(
		forms,
		phpIndex,
		doctrineIndex,
	), phpIndex, doctrineIndex
}

func formFieldGeneratorSource(extraImports string) string {
	return `<?php
namespace App\Form;

use App\Model\Profile;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\Extension\Core\Type\TextType;
use Symfony\Component\Form\FormBuilderInterface;
use Symfony\Component\OptionsResolver\OptionsResolver;
` + extraImports + `
class ProfileType extends AbstractType
{
    public function configureOptions(OptionsResolver $resolver): void
    {
        $resolver->setDefaults(['data_class' => Profile::class]);
    }

    public function buildForm(FormBuilderInterface $builder, array $options): void
    {
        $builder->add('email', TextType::class);
    }
}
`
}

func formCandidateStrings(fields []formFieldCandidate) []string {
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		result = append(
			result,
			field.Name+":"+field.PHPType+":"+field.SuggestedType,
		)
	}
	return result
}
