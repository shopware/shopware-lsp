package form

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReferenceAtFormTypesFieldsAndOptions(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	stubs := indexer.NewParsedFile(
		"/project/vendor/forms.php",
		[]byte(`<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormBuilderInterface {
    public function add(string $name, mixed $type = null, array $options = []): static;
}
interface FormInterface {
    public function get(string $name): self;
}
interface FormFactoryInterface {
    public function create(string $type, mixed $data = null, array $options = []): FormInterface;
}
abstract class AbstractType implements FormTypeInterface {}

namespace Symfony\Component\OptionsResolver;
class OptionsResolver {
    public function setDefault(string $name, mixed $value): self;
}`),
	)
	require.NoError(t, phpIndex.Index(stubs))

	source := `<?php
namespace App\Form;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\FormBuilderInterface;
use Symfony\Component\Form\FormFactoryInterface;
use Symfony\Component\OptionsResolver\OptionsResolver;

class ProductType extends AbstractType
{
    public function buildForm(FormBuilderInterface $builder, array $options): void
    {
        $builder->add('title', 'text', ['required' => true]);
        $options['translation_domain'];
    }

    public function configureOptions(OptionsResolver $resolver): void
    {
        $resolver->setDefault('data_class', Product::class);
    }
}

function factory(FormFactoryInterface $factory): void
{
    $form = $factory->create('product', null, ['csrf_protection' => false]);
    $form->get('title');
}`
	parsed := indexer.NewParsedFile("/project/src/ProductType.php", []byte(source))
	root := parsed.SyntaxTree().Root

	tests := []struct {
		needle   string
		role     ReferenceRole
		formType string
		legacy   bool
	}{
		{"'title', 'text'", ReferenceField, "App\\Form\\ProductType", false},
		{"'text', ['required'", ReferenceType, "", true},
		{"'required' => true", ReferenceOption, "text", false},
		{"'translation_domain'", ReferenceOption, "App\\Form\\ProductType", false},
		{"'data_class'", ReferenceOption, "App\\Form\\ProductType", false},
		{"'product', null", ReferenceType, "", false},
		{"'csrf_protection'", ReferenceOption, "product", false},
		{"'title');", ReferenceField, "product", false},
	}
	for _, test := range tests {
		offset := strings.Index(source, test.needle)
		require.NotEqual(t, -1, offset, test.needle)
		if strings.HasPrefix(test.needle, "'") {
			offset++
		}
		node := root.NodeAtOffset(uint32(offset))
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			parsed.Path,
			1,
			node,
			root,
		)
		reference, found := ReferenceAt(ctx, root, node)
		require.True(t, found, test.needle)
		assert.Equal(t, test.role, reference.Role, test.needle)
		assert.Equal(t, test.formType, reference.FormType, test.needle)
		assert.Equal(
			t,
			test.legacy,
			IsLegacyBuilderTypeAlias(ctx, reference),
			test.needle,
		)
	}
}

func TestReferenceAtDoesNotMatchUnrelatedCalls(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	source := `<?php
class Collection {
    public function add(string $name, string $value): void {}
}
function run(Collection $items): void {
    $items->add('field', 'type');
}`
	parsed := indexer.NewParsedFile("/project/src/Collection.php", []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	root := parsed.SyntaxTree().Root
	offset := strings.Index(source, "'field'") + 1
	node := root.NodeAtOffset(uint32(offset))
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		parsed.Path,
		1,
		node,
		root,
	)
	_, found := ReferenceAt(ctx, root, node)
	assert.False(t, found)
}

func TestFactoryTypeReferencesResolveTypedFactoryCalls(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/form.php",
		[]byte(`<?php
namespace Symfony\Component\Form;
interface FormFactoryInterface {
    public function create(string $type): mixed;
    public function createBuilder(string $type): mixed;
    public function createNamed(string $name, string $type): mixed;
    public function createNamedBuilder(string $name, string $type): mixed;
}

namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {
    protected function createForm(string $type): mixed {}
}
`),
	)))
	source := `<?php
namespace App\Controller;

use App\Form\ProductType;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
use Symfony\Component\Form\FormFactoryInterface;

final class ProductController extends AbstractController
{
    public function edit(FormFactoryInterface $forms, OtherFactory $other): void
    {
        $this->createForm(ProductType::class);
        $this->createForm(type: 'product');
        $forms->createBuilder(ProductType::class);
        $forms->createNamed('product', ProductType::class);
        $forms->createNamedBuilder(name: 'product', type: ProductType::class);
        $other->create(ProductType::class);
        $this->create('product');
    }
}
`
	parsed := indexer.NewParsedFile(
		"/project/src/ProductController.php",
		[]byte(source),
	)
	root := parsed.SyntaxTree().Root
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		parsed.Path,
		1,
		root,
		root,
	)
	references := FactoryTypeReferences(ctx, root)
	require.Len(t, references, 5)
	assert.Equal(t, []string{
		"App\\Form\\ProductType",
		"product",
		"App\\Form\\ProductType",
		"App\\Form\\ProductType",
		"App\\Form\\ProductType",
	}, []string{
		references[0].Name,
		references[1].Name,
		references[2].Name,
		references[3].Name,
		references[4].Name,
	})
	for _, reference := range references {
		assert.NotZero(t, reference.Range.Len())
	}
}
