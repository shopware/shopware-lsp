package codelens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestFormRelatedCodeLensLinksPublicMethodToFormType(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	stubPath := filepath.Join(root, "vendor", "symfony.php")
	formPath := filepath.Join(root, "src", "Form", "ProductType.php")
	controllerPath := filepath.Join(
		root,
		"src",
		"Controller",
		"ProductController.php",
	)
	for _, path := range []string{stubPath, formPath, controllerPath} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	}
	stubSource := `<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
abstract class AbstractType implements FormTypeInterface {}

namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {
    protected function createForm(string $type): mixed {}
}
`
	formSource := `<?php
namespace App\Form;

use Symfony\Component\Form\AbstractType;

final class ProductType extends AbstractType
{
    public function getBlockPrefix(): string
    {
        return 'product';
    }
}
`
	controllerSource := `<?php
namespace App\Controller;

use App\Form\ProductType;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;

final class ProductController extends AbstractController
{
    public function edit(): void
    {
        $this->createForm(ProductType::class);
        $this->createForm('product');
    }

    private function helper(): void
    {
        $this->createForm(ProductType::class);
    }
}
`
	for path, source := range map[string]string{
		stubPath:       stubSource,
		formPath:       formSource,
		controllerPath: controllerSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	formIndex, err := form.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, formIndex.Close()) })
	formIndex.SetPHPIndex(phpIndex)
	for path, source := range map[string]string{
		stubPath:       stubSource,
		formPath:       formSource,
		controllerPath: controllerSource,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	require.NoError(t, formIndex.Index(indexer.NewParsedFile(
		formPath,
		[]byte(formSource),
	)))

	lenses := relatedCodeLensesFor(
		t,
		NewFormRelatedCodeLensProvider(formIndex, phpIndex),
		controllerPath,
		controllerSource,
	)
	require.Len(t, lenses, 1)
	require.NotNil(t, lenses[0].Command)
	assert.Equal(t, "Open related form type", lenses[0].Command.Title)
	assert.Equal(t, 8, lenses[0].Range.Start.Line)
	assert.Equal(t, []string{
		relatedTarget(formPath, 6),
	}, relatedLensTargets(t, lenses[0]))
}

func TestFormRelatedCodeLensLinksTwigFormFunctionsToFormType(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	stubPath := filepath.Join(root, "vendor", "symfony.php")
	formPath := filepath.Join(root, "src", "Form", "CheckoutType.php")
	controllerPath := filepath.Join(
		root,
		"src",
		"Controller",
		"CheckoutController.php",
	)
	templatePath := filepath.Join(
		root,
		"templates",
		"checkout.html.twig",
	)
	stubSource := `<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
abstract class AbstractType implements FormTypeInterface {}
class FormInterface { public function createView(): mixed {} }

namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {
    protected function createForm(string $type): \Symfony\Component\Form\FormInterface {}
    protected function render(string $template, array $parameters = []): mixed {}
}
`
	formSource := `<?php
namespace App\Form;
use Symfony\Component\Form\AbstractType;
final class CheckoutType extends AbstractType {}
`
	controllerSource := `<?php
namespace App\Controller;
use App\Form\CheckoutType;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
final class CheckoutController extends AbstractController {
    public function index(): mixed {
        return $this->render('checkout.html.twig', [
            'checkout' => $this->createForm(CheckoutType::class)->createView(),
        ]);
    }
}
`
	templateSource := `{{ form_start(checkout) }}
{{ form(checkout.email) }}
{{ form_end(checkout) }}
{{ form_rest(checkout) }}
{{ form_row(checkout.email) }}`
	for path, source := range map[string]string{
		stubPath:       stubSource,
		formPath:       formSource,
		controllerPath: controllerSource,
		templatePath:   templateSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	formIndex, err := form.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, formIndex.Close()) })
	formIndex.SetPHPIndex(phpIndex)
	for path, source := range map[string]string{
		stubPath:       stubSource,
		formPath:       formSource,
		controllerPath: controllerSource,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	require.NoError(t, formIndex.Index(indexer.NewParsedFile(
		formPath,
		[]byte(formSource),
	)))

	lenses := relatedCodeLensesFor(
		t,
		NewFormRelatedCodeLensProvider(formIndex, phpIndex),
		templatePath,
		templateSource,
	)
	require.Len(t, lenses, 4)
	for index, lens := range lenses {
		require.NotNil(t, lens.Command)
		assert.Equal(t, "Open related form type", lens.Command.Title)
		assert.Equal(t, index, lens.Range.Start.Line)
		assert.Equal(t, []string{
			relatedTarget(formPath, 4),
		}, relatedLensTargets(t, lens))
	}
}

func TestFormRelatedCodeLensLinksFormTypesAndDataClassesBothWays(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	stubPath := filepath.Join(root, "vendor", "symfony.php")
	modelPath := filepath.Join(root, "src", "Model", "Product.php")
	replacementPath := filepath.Join(
		root,
		"src",
		"Model",
		"Replacement.php",
	)
	formPath := filepath.Join(root, "src", "Form", "ProductType.php")
	adminFormPath := filepath.Join(
		root,
		"src",
		"Form",
		"AdminProductType.php",
	)
	stubSource := `<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormTypeExtensionInterface {}
abstract class AbstractType implements FormTypeInterface {}

namespace Symfony\Component\OptionsResolver;
class OptionsResolver {}
`
	modelSource := `<?php
namespace App\Model;

final class Product {}
`
	replacementSource := `<?php
namespace App\Model;

final class Replacement {}
`
	formSource := `<?php
namespace App\Form;

use App\Model\Product;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\OptionsResolver\OptionsResolver;

final class ProductType extends AbstractType
{
    public function configureOptions(OptionsResolver $resolver): void
    {
        $resolver->setDefaults(['data_class' => Product::class]);
    }
}
`
	adminFormSource := `<?php
namespace App\Form;

use App\Model\Product;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\OptionsResolver\OptionsResolver;

final class AdminProductType extends AbstractType
{
    public function configureOptions(OptionsResolver $resolver): void
    {
        $resolver->setDefault('data_class', Product::class);
    }
}
`
	sources := map[string]string{
		stubPath:        stubSource,
		modelPath:       modelSource,
		replacementPath: replacementSource,
		formPath:        formSource,
		adminFormPath:   adminFormSource,
	}
	for path, source := range sources {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	formIndex, err := form.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, formIndex.Close()) })
	formIndex.SetPHPIndex(phpIndex)
	for path, source := range sources {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	for path, source := range map[string]string{
		formPath:      formSource,
		adminFormPath: adminFormSource,
	} {
		require.NoError(t, formIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	provider := NewFormRelatedCodeLensProvider(formIndex, phpIndex)
	formLenses := relatedCodeLensesFor(
		t,
		provider,
		formPath,
		formSource,
	)
	require.Len(t, formLenses, 1)
	assert.Equal(t, "Open form data class", formLenses[0].Command.Title)
	assert.Equal(t, 7, formLenses[0].Range.Start.Line)
	assert.Equal(t, []string{
		relatedTarget(modelPath, 4),
	}, relatedLensTargets(t, formLenses[0]))

	modelLenses := relatedCodeLensesFor(
		t,
		provider,
		modelPath,
		modelSource,
	)
	require.Len(t, modelLenses, 1)
	assert.Equal(
		t,
		"Open 2 related form types",
		modelLenses[0].Command.Title,
	)
	assert.Equal(t, []string{
		relatedTarget(adminFormPath, 8),
		relatedTarget(formPath, 8),
	}, relatedLensTargets(t, modelLenses[0]))

	unsavedFormSource := strings.ReplaceAll(
		formSource,
		"App\\Model\\Product",
		"App\\Model\\Replacement",
	)
	unsavedFormSource = strings.ReplaceAll(
		unsavedFormSource,
		"Product::class",
		"Replacement::class",
	)
	unsavedLenses := relatedCodeLensesFor(
		t,
		provider,
		formPath,
		unsavedFormSource,
	)
	require.Len(t, unsavedLenses, 1)
	assert.Equal(t, []string{
		relatedTarget(replacementPath, 4),
	}, relatedLensTargets(t, unsavedLenses[0]))
}
