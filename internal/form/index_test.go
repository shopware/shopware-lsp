package form

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexFormTypesOptionsFieldsAndDataClass(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	idx.SetPHPIndex(phpIndex)

	framework := indexer.NewParsedFile(
		"/project/vendor/symfony/form.php",
		[]byte(`<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormTypeExtensionInterface {}
abstract class AbstractType implements FormTypeInterface {}`),
	)
	require.NoError(t, phpIndex.Index(framework))
	require.NoError(t, idx.Index(framework))
	coreTypes := indexer.NewParsedFile(
		"/project/vendor/symfony/core_types.php",
		[]byte(`<?php
namespace Symfony\Component\Form\Extension\Core\Type;
class FormType extends \Symfony\Component\Form\AbstractType {}
class TextType extends \Symfony\Component\Form\AbstractType {
    public function configureOptions($resolver): void {
        $resolver->setDefaults(['trim' => true, 'empty_data' => '']);
        $resolver->setAllowedTypes('trim', 'bool');
    }
}`),
	)
	require.NoError(t, phpIndex.Index(coreTypes))
	require.NoError(t, idx.Index(coreTypes))

	source := indexer.NewParsedFile(
		"/project/src/ProfileType.php",
		[]byte(`<?php
namespace App\Form;

use App\Model\Profile;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\Extension\Core\Type\TextType;
use Symfony\Component\Form\FormBuilderInterface;
use Symfony\Component\OptionsResolver\OptionsResolver;

class ProfileType extends AbstractType
{
    public function getBlockPrefix(): string { return 'profile'; }
    public function getParent(): string { return TextType::class; }

    public function configureOptions(OptionsResolver $resolver): void
    {
        $resolver->setDefaults([
            'data_class' => Profile::class,
            'translation_domain' => 'profile',
        ]);
        $resolver->setRequired(['tenant']);
        $resolver->setDefined('audit');
        $resolver->setAllowedTypes('tenant', ['string', 'null']);
    }

    public function buildForm(FormBuilderInterface $builder, array $options): void
    {
        $builder
            ->add('displayName', TextType::class, [
                'property_path' => 'name',
            ])
            ->add('token', null, ['mapped' => false]);
    }
}

namespace App\Model;
class Profile
{
    public string $email;
    private string $secret;
    public function setDisplayName(string $name): void {}
}`),
	)
	require.NoError(t, phpIndex.Index(source))
	require.NoError(t, idx.Index(source))

	formType, found, err := idx.GetType("profile")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "App\\Form\\ProfileType", formType.Class)
	assert.Equal(
		t,
		"Symfony\\Component\\Form\\Extension\\Core\\Type\\TextType",
		formType.Parent,
	)
	assert.Equal(t, "App\\Model\\Profile", formType.DataClass)

	relations, err := idx.GetDataClassRelations()
	require.NoError(t, err)
	require.Len(t, relations, 1)
	assert.Equal(t, "App\\Form\\ProfileType", relations[0].Class)
	assert.Equal(t, "App\\Model\\Profile", relations[0].DataClass)
	assert.Equal(t, source.Path, relations[0].File)

	fields, err := idx.EffectiveFields(formType.Class)
	require.NoError(t, err)
	require.Len(t, fields, 2)
	assert.Equal(t, "name", findField(t, fields, "displayName").PropertyPath)
	assert.False(t, findField(t, fields, "token").Mapped)

	options, err := idx.EffectiveOptions(formType.Class)
	require.NoError(t, err)
	assertOptionNames(
		t,
		options,
		"audit",
		"data_class",
		"empty_data",
		"tenant",
		"translation_domain",
		"trim",
	)

	dataFields, err := idx.DataFieldsFor(formType.Class)
	require.NoError(t, err)
	assertDataFieldNames(t, dataFields, "displayName", "email")
	assert.Equal(t, "string", findDataField(t, dataFields, "displayName").Type)
}

func TestIndexFormExtensionsAndContainerAliases(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	idx.SetPHPIndex(phpIndex)

	framework := indexer.NewParsedFile(
		"/project/vendor/symfony/form.php",
		[]byte(`<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormTypeExtensionInterface {}
abstract class AbstractType implements FormTypeInterface {}`),
	)
	require.NoError(t, phpIndex.Index(framework))
	require.NoError(t, idx.Index(framework))
	source := indexer.NewParsedFile(
		"/project/src/Forms.php",
		[]byte(`<?php
namespace App\Form;
class ProductType extends \Symfony\Component\Form\AbstractType {
    public function configureOptions($resolver): void {
        $resolver->setDefault('currency', 'EUR');
    }
}
class ProductTypeExtension implements \Symfony\Component\Form\FormTypeExtensionInterface {
    public static function getExtendedTypes(): iterable {
        return [ProductType::class];
    }
    public function configureOptions($resolver): void {
        $resolver->setDefault('shopware_context', null);
    }
}`),
	)
	require.NoError(t, phpIndex.Index(source))
	require.NoError(t, idx.Index(source))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/services.yaml",
		[]byte(`services:
    App\Form\ProductType:
        tags:
            - { name: form.type, alias: product }
`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/services.xml",
		[]byte(`<container><services>
  <service id="App\Form\ProductType">
    <tag name="form.type" alias="catalog_product"/>
  </service>
</services></container>`),
	)))

	current, found, err := idx.GetType("catalog_product")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "App\\Form\\ProductType", current.Class)
	assert.ElementsMatch(t, []string{"catalog_product", "product"}, current.Aliases)

	options, err := idx.EffectiveOptions("product")
	require.NoError(t, err)
	assertOptionNames(t, options, "currency", "shopware_context")
}

func TestIndexFormViewVarsFromTypesExtensionsAndCore(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	idx.SetPHPIndex(phpIndex)

	for path, source := range map[string]string{
		"/project/vendor/symfony/form.php": `<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormTypeExtensionInterface {}
class FormView { public array $vars = []; }
abstract class AbstractType implements FormTypeInterface {}`,
		"/project/vendor/symfony/core.php": `<?php
namespace Symfony\Component\Form\Extension\Core\Type;
class FormType extends \Symfony\Component\Form\AbstractType {
    public function buildView(
        \Symfony\Component\Form\FormView $view,
        $form,
        array $options,
    ): void {
        $view->vars['compound'] = true;
        $view->vars = ['form_attr' => []];
    }
}`,
		"/project/src/ProductType.php": `<?php
namespace App\Form;
class ProductType extends \Symfony\Component\Form\AbstractType {
    public function buildView(
        \Symfony\Component\Form\FormView $view,
        $form,
        array $options,
    ): void {
        $view->vars['product_name'] = 'product';
    }
}
class ProductTypeExtension implements
    \Symfony\Component\Form\FormTypeExtensionInterface
{
    public static function getExtendedTypes(): iterable {
        return [ProductType::class];
    }
    public function finishView($view, $form, array $options): void {
        $view->vars = array_replace($view->vars, [
            'currency' => 'EUR',
            'precision' => 2,
        ]);
    }
}`,
	} {
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, idx.Index(parsed))
	}

	viewVars, err := idx.EffectiveViewVars("App\\Form\\ProductType")
	require.NoError(t, err)
	names := make([]string, 0, len(viewVars))
	for _, viewVar := range viewVars {
		names = append(names, viewVar.Name)
		assert.NotEmpty(t, viewVar.File)
		assert.NotZero(t, viewVar.Range.End)
	}
	assert.ElementsMatch(t, []string{
		"compound",
		"currency",
		"form_attr",
		"precision",
		"product_name",
	}, names)
	assert.Equal(t, "bool", findViewVar(t, viewVars, "compound").Type)
	assert.Equal(t, "string", findViewVar(t, viewVars, "currency").Type)
	assert.Equal(t, "int", findViewVar(t, viewVars, "precision").Type)
	assert.Equal(t, "array", findViewVar(t, viewVars, "form_attr").Type)
}

func TestIndexClearsFormRecordAfterCandidateRemoval(t *testing.T) {
	configDir := t.TempDir()
	path := "/project/src/ExampleType.php"
	idx, err := NewIndex(configDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(`<?php
class ExampleType extends \Symfony\Component\Form\AbstractType {
    public function getBlockPrefix(): string { return 'example'; }
}`),
	)))
	types, err := idx.records.GetAllValues()
	require.NoError(t, err)
	require.Len(t, types, 1)
	require.NoError(t, idx.Close())

	reopened, err := NewIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	require.NoError(t, reopened.Index(indexer.NewParsedFile(
		path,
		[]byte(`<?php class ExampleType {}`),
	)))
	types, err = reopened.records.GetAllValues()
	require.NoError(t, err)
	require.Empty(t, types)
}

func assertOptionNames(t *testing.T, options []Option, names ...string) {
	t.Helper()
	actual := make([]string, 0, len(options))
	for _, option := range options {
		actual = append(actual, option.Name)
	}
	assert.ElementsMatch(t, names, actual)
}

func assertDataFieldNames(t *testing.T, fields []DataField, names ...string) {
	t.Helper()
	actual := make([]string, 0, len(fields))
	for _, field := range fields {
		actual = append(actual, field.Name)
	}
	assert.ElementsMatch(t, names, actual)
}

func findField(t *testing.T, fields []Field, name string) Field {
	t.Helper()
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("field %q not found in %#v", name, fields)
	return Field{}
}

func findDataField(t *testing.T, fields []DataField, name string) DataField {
	t.Helper()
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("data field %q not found in %#v", name, fields)
	return DataField{}
}

func findViewVar(t *testing.T, viewVars []ViewVar, name string) ViewVar {
	t.Helper()
	for _, viewVar := range viewVars {
		if viewVar.Name == name {
			return viewVar
		}
	}
	t.Fatalf("view var %q not found in %#v", name, viewVars)
	return ViewVar{}
}
