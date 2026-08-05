package codeaction

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestTwigFormFieldGeneratorFindsControllerFormView(t *testing.T) {
	provider := newTwigFormFieldGeneratorFixture(t)
	candidates, err := provider.twigFormCandidates(
		"file:///project/templates/checkout.html.twig",
	)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "checkout", candidates[0].Variable)
	assert.Equal(t, "App\\Form\\CheckoutType", candidates[0].FormType)
	assert.Equal(
		t,
		[]string{"billing-address", "email", "name"},
		candidates[0].Fields,
	)

	request := symfonyGeneratorCodeActionRequest(
		"file:///project/templates/checkout.html.twig",
		"<h1>Checkout</h1>",
		"Checkout",
	)
	actions := provider.GetCodeActions(context.Background(), request)
	require.Len(t, actions, 1)
	assert.Equal(t, "Symfony: Generate Twig form rows", actions[0].Title)
	assert.Equal(
		t,
		generateTwigFormFieldsAction,
		actions[0].Command.Command,
	)
}

func TestTwigFormFieldGeneratorRendersSelectedRows(t *testing.T) {
	provider := newTwigFormFieldGeneratorFixture(t)
	raw := mustGeneratorJSON(t, twigFormFieldRequest{
		FileURI:  "file:///project/templates/checkout.html.twig",
		Variable: "checkout",
		FormType: "App\\Form\\CheckoutType",
		SelectedFields: []string{
			"name",
			"billing-address",
		},
	})
	value, err := provider.generateTwigFormFields(
		context.Background(),
		&raw,
	)
	require.NoError(t, err)
	result := value.(twigFormFieldGenerationResponse)
	assert.Equal(
		t,
		"{{ form_row(attribute(checkout, 'billing-address')) }}\n"+
			"{{ form_row(checkout.name) }}\n",
		result.Content,
	)
}

func newTwigFormFieldGeneratorFixture(
	t *testing.T,
) *TwigFormFieldGeneratorProvider {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	formStubs := `<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormBuilderInterface {}
abstract class AbstractType implements FormTypeInterface {}
`
	formType := `<?php
namespace App\Form;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\FormBuilderInterface;
class CheckoutType extends AbstractType
{
    public function buildForm(FormBuilderInterface $builder, array $options): void
    {
        $builder->add('name');
        $builder->add('email');
        $builder->add('billing-address');
    }
}
`
	controller := `<?php
namespace App\Controller;
use App\Form\CheckoutType;
class CheckoutController
{
    public function checkout()
    {
        $form = $this->createForm(CheckoutType::class);
        return $this->render('checkout.html.twig', [
            'checkout' => $form->createView(),
        ]);
    }
}
`
	for path, source := range map[string]string{
		"/vendor/Form.php":                    formStubs,
		"/project/src/Form/CheckoutType.php":  formType,
		"/project/src/CheckoutController.php": controller,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	forms, err := form.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, forms.Close()) })
	forms.SetPHPIndex(phpIndex)
	require.NoError(t, forms.Index(indexer.NewParsedFile(
		"/project/src/Form/CheckoutType.php",
		[]byte(formType),
	)))
	return NewTwigFormFieldGeneratorProvider(forms, phpIndex)
}
