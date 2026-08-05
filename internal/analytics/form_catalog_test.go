package analytics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormCatalogExposesTypesOptionsSourcesAndCacheRestore(t *testing.T) {
	root := t.TempDir()
	phpCache := t.TempDir()
	formCache := t.TempDir()
	frameworkPath := filepath.Join(root, "vendor", "symfony", "form.php")
	frameworkSource := `<?php
namespace Symfony\Component\Form;

interface FormTypeInterface {}
interface FormTypeExtensionInterface {}
interface FormBuilderInterface {}
class FormView {}
abstract class AbstractType implements FormTypeInterface {}
`
	typePath := filepath.Join(root, "src", "Form", "ProductType.php")
	typeSource := `<?php
namespace App\Form;

use App\Model\Product;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\FormBuilderInterface;
use Symfony\Component\Form\FormTypeExtensionInterface;
use Symfony\Component\Form\FormView;

class ParentType extends AbstractType
{
    public function configureOptions($resolver): void
    {
        $resolver->setDefault('base_option', true);
    }
}

class ProductType extends AbstractType
{
    public function getBlockPrefix(): string
    {
        return 'product';
    }

    public function getParent(): string
    {
        return ParentType::class;
    }

    public function configureOptions($resolver): void
    {
        $resolver->setDefaults([
            'currency' => 'EUR',
            'data_class' => Product::class,
        ]);
        $resolver->setRequired('currency');
        $resolver->setAllowedTypes('currency', ['string']);
    }

    public function buildForm(
        FormBuilderInterface $builder,
        array $options,
    ): void {
        $builder->add('name');
    }

    public function buildView(
        FormView $view,
        $form,
        array $options,
    ): void {
        $view->vars['theme'] = 'catalog';
    }
}

class ProductTypeExtension implements FormTypeExtensionInterface
{
    public static function getExtendedTypes(): iterable
    {
        return [ProductType::class];
    }

    public function configureOptions($resolver): void
    {
        $resolver->setDefault('tenant', null);
    }
}

namespace App\Model;
class Product {}
`
	servicePath := filepath.Join(root, "config", "services.yaml")
	serviceSource := `services:
    App\Form\ProductType:
        tags:
            - { name: form.type, alias: product_legacy }
`
	for path, source := range map[string]string{
		frameworkPath: frameworkSource,
		typePath:      typeSource,
		servicePath:   serviceSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	phpIndex, err := php.NewPHPIndex(phpCache)
	require.NoError(t, err)
	formIndex, err := form.NewIndex(formCache)
	require.NoError(t, err)
	formIndex.SetPHPIndex(phpIndex)
	for path, source := range map[string]string{
		frameworkPath: frameworkSource,
		typePath:      typeSource,
	} {
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, formIndex.Index(parsed))
	}
	require.NoError(t, formIndex.Index(indexer.NewParsedFile(
		servicePath,
		[]byte(serviceSource),
	)))

	provider := NewFormCatalogProvider(root, formIndex, phpIndex)
	request := FormTypeCatalogRequest{
		Query:    "product",
		FileGlob: "src/**/ProductType.php",
	}
	types, err := provider.Types(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, types, 3)
	product := formTypeCatalogEntry(t, types, "product")
	assert.Equal(t, "App\\Form\\ProductType", product.ClassName)
	assert.ElementsMatch(
		t,
		[]string{"product", "product_legacy"},
		product.Aliases,
	)
	assert.Equal(t, "App\\Form\\ParentType", product.Parent)
	assert.Equal(t, "App\\Model\\Product", product.DataClass)
	assert.Equal(t, uriutil.FileURI(typePath), product.FileURI)
	assert.Positive(t, product.SourceLine)
	assert.Equal(t, 4, product.OptionCount)
	assert.Equal(t, 1, product.FieldCount)
	assert.Equal(t, 1, product.ViewVarCount)

	optionRequest := FormOptionCatalogRequest{FormType: "product_legacy"}
	options, err := provider.Options(context.Background(), optionRequest)
	require.NoError(t, err)
	require.Len(t, options, 4)
	currency := formOptionCatalogEntry(t, options, "currency")
	assert.Equal(
		t,
		[]string{"default", "required", "allowedTypes"},
		currency.Kinds,
	)
	assert.Equal(t, []string{"string"}, currency.AllowedTypes)
	assert.Equal(t, "'EUR'", currency.Default)
	assert.Equal(t, "App\\Form\\ProductType", currency.SourceClass)
	assert.Equal(t, uriutil.FileURI(typePath), currency.FileURI)
	assert.Len(t, currency.Sources, 3)
	assert.Positive(t, currency.SourceLine)
	assert.Equal(
		t,
		"App\\Form\\ParentType",
		formOptionCatalogEntry(t, options, "base_option").SourceClass,
	)
	assert.Equal(
		t,
		"App\\Form\\ProductTypeExtension",
		formOptionCatalogEntry(t, options, "tenant").SourceClass,
	)

	_, err = provider.Options(
		context.Background(),
		FormOptionCatalogRequest{FormType: "missing"},
	)
	assert.ErrorContains(t, err, "was not found")

	require.NoError(t, formIndex.Close())
	require.NoError(t, phpIndex.Close())
	restoredPHP, err := php.NewPHPIndex(phpCache)
	require.NoError(t, err)
	restoredForms, err := form.NewIndex(formCache)
	require.NoError(t, err)
	restoredForms.SetPHPIndex(restoredPHP)
	t.Cleanup(func() {
		require.NoError(t, restoredForms.Close())
		require.NoError(t, restoredPHP.Close())
	})
	restoredProvider := NewFormCatalogProvider(
		root,
		restoredForms,
		restoredPHP,
	)
	restoredTypes, err := restoredProvider.Types(
		context.Background(),
		request,
	)
	require.NoError(t, err)
	assert.Equal(t, types, restoredTypes)
	restoredOptions, err := restoredProvider.Options(
		context.Background(),
		optionRequest,
	)
	require.NoError(t, err)
	assert.Equal(t, options, restoredOptions)
}

func formTypeCatalogEntry(
	t *testing.T,
	entries []FormTypeCatalogEntry,
	name string,
) FormTypeCatalogEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("form type %q not found in %#v", name, entries)
	return FormTypeCatalogEntry{}
}

func formOptionCatalogEntry(
	t *testing.T,
	entries []FormOptionCatalogEntry,
	name string,
) FormOptionCatalogEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("form option %q not found in %#v", name, entries)
	return FormOptionCatalogEntry{}
}
