package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestFormTypeOptionAndDataFieldCompletion(t *testing.T) {
	formIndex, phpIndex := formCompletionFixture(t)
	source := `<?php
namespace App\Form;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\FormBuilderInterface;

class ProfileType extends AbstractType
{
    public function getBlockPrefix(): string { return 'profile'; }
    public function configureOptions($resolver): void
    {
        $resolver->setDefaults([
            'data_class' => \App\Model\Profile::class,
            'translation_domain' => 'profile',
        ]);
        $resolver->setDefined('audit');
    }
    public function buildForm(FormBuilderInterface $builder, array $options): void
    {
        $builder->add('');
        $builder->add('field', '', []);
        $builder->add('other', 'profile', ['']);
    }
}`
	path := "/project/src/ProfileType.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, formIndex.Index(parsed))
	document := lsp.NewTextDocument("file://"+path, source, 1)
	provider := NewFormCompletionProvider(formIndex)

	for _, test := range []struct {
		needle string
		label  string
	}{
		{"$builder->add('');", "displayName"},
		{"$builder->add('field', ''", "profile"},
		{"['']);", "audit"},
		{"['']);", "translation_domain"},
	} {
		offset := strings.Index(source, test.needle)
		require.NotEqual(t, -1, offset, test.needle)
		offset += strings.LastIndex(test.needle, "''") + 1
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		items := provider.GetCompletions(
			ctx,
			consoleCompletionRequest(document, node),
		)
		requireCompletion(t, items, test.label)
	}
}

func TestPHPDocFormTypeAssistantTagCompletion(t *testing.T) {
	formIndex, phpIndex := formCompletionFixture(t)
	formType := indexer.NewParsedFile(
		"/project/src/ProfileType.php",
		[]byte(`<?php
namespace App\Form;
class ProfileType extends \Symfony\Component\Form\AbstractType {
    public function getBlockPrefix(): string { return 'profile'; }
}
`),
	)
	require.NoError(t, phpIndex.Index(formType))
	require.NoError(t, formIndex.Index(formType))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/FormAssistant.php",
		[]byte(`<?php
/** @param string $type #FormType */
function resolve_form(string $type): void {}
`),
	)))
	source := "<?php resolve_form('prof');"
	path := "/project/src/Usage.php"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := uint32(strings.Index(source, "prof") + len("prof"))
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		document.Version,
		node,
		document.SyntaxTree.Root,
	)
	item := requireCompletion(
		t,
		NewFormCompletionProvider(formIndex, phpIndex).GetCompletions(
			ctx,
			consoleCompletionRequest(document, node),
		),
		"profile",
	)
	edit, ok := item.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, "profile", edit.NewText)
	require.Equal(t, "prof", completionRangeText(document, edit.Range))
}

func TestFormCompletionUsesUnsavedDocumentOptions(t *testing.T) {
	formIndex, phpIndex := formCompletionFixture(t)
	path := "/project/src/LiveType.php"
	indexedSource := `<?php
namespace App\Form;
class LiveType extends \Symfony\Component\Form\AbstractType {
    public function getBlockPrefix(): string { return 'live'; }
}`
	parsed := indexer.NewParsedFile(path, []byte(indexedSource))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, formIndex.Index(parsed))

	liveSource := `<?php
namespace App\Form;
class LiveType extends \Symfony\Component\Form\AbstractType {
    public function getBlockPrefix(): string { return 'live'; }
    public function configureOptions($resolver): void {
        $resolver->setDefined('live_option');
    }
    public function buildForm(
        \Symfony\Component\Form\FormBuilderInterface $builder,
        array $options,
    ): void {
        $builder->add('field', 'live', ['']);
    }
}`
	document := lsp.NewTextDocument("file://"+path, liveSource, 2)
	offset := strings.LastIndex(liveSource, "''") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		2,
		node,
		document.SyntaxTree.Root,
	)
	items := NewFormCompletionProvider(formIndex).GetCompletions(
		ctx,
		consoleCompletionRequest(document, node),
	)
	requireCompletion(t, items, "live_option")
}

func TestTwigFormViewFieldCompletionUsesControllerProvenance(t *testing.T) {
	formIndex, phpIndex := formCompletionFixture(t)
	for path, source := range map[string]string{
		"/project/src/CheckoutType.php": `<?php
namespace App\Form;
class CheckoutType extends \Symfony\Component\Form\AbstractType {
    public function buildForm(
        \Symfony\Component\Form\FormBuilderInterface $builder,
        array $options,
    ): void {
        $builder->add('email', \App\Form\EmailType::class);
        $builder->add('terms');
    }
    public function buildView($view, $form, array $options): void {
        $view->vars['checkout_theme'] = 'standard';
    }
}`,
		"/project/src/CheckoutController.php": `<?php
namespace App\Controller;
class CheckoutController {
    public function checkout() {
        return $this->render('checkout.html.twig', [
            'checkout' => $this->createForm(
                \App\Form\CheckoutType::class,
            )->createView(),
        ]);
    }
}`,
	} {
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, formIndex.Index(parsed))
	}

	source := `{{ checkout. }}`
	offset := strings.Index(source, ".") + 1
	_, request := twigCompletionAt(
		"file:///project/templates/checkout.html.twig",
		source,
		offset,
	)
	items := NewFormCompletionProvider(
		formIndex,
		phpIndex,
	).GetCompletions(context.Background(), request)
	email := requireCompletion(t, items, "email")
	require.Equal(t, "App\\Form\\EmailType · App\\Form\\CheckoutType", email.Detail)
	requireCompletion(t, items, "terms")

	viewSource := `{{ checkout.vars. }}`
	_, request = twigCompletionAt(
		"file:///project/templates/checkout.html.twig",
		viewSource,
		strings.LastIndex(viewSource, ".")+1,
	)
	viewItems := NewFormCompletionProvider(
		formIndex,
		phpIndex,
	).GetCompletions(context.Background(), request)
	viewVar := requireCompletion(t, viewItems, "checkout_theme")
	require.Contains(t, viewVar.Detail, "string")
	require.NotContains(t, completionLabels(viewItems), "email")

	ordinary := `{{ ordinary. }}`
	_, request = twigCompletionAt(
		"file:///project/templates/checkout.html.twig",
		ordinary,
		strings.Index(ordinary, ".")+1,
	)
	require.Empty(t, NewFormCompletionProvider(
		formIndex,
		phpIndex,
	).GetCompletions(context.Background(), request))
}

func formCompletionFixture(
	t *testing.T,
) (*form.Index, *php.PHPIndex) {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	formIndex, err := form.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, formIndex.Close()) })
	formIndex.SetPHPIndex(phpIndex)

	framework := indexer.NewParsedFile(
		"/project/vendor/Form.php",
		[]byte(`<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormBuilderInterface {
    public function add(string $name, mixed $type = null, array $options = []): static;
}
abstract class AbstractType implements FormTypeInterface {}`),
	)
	require.NoError(t, phpIndex.Index(framework))
	require.NoError(t, formIndex.Index(framework))
	model := indexer.NewParsedFile(
		"/project/src/Profile.php",
		[]byte(`<?php
namespace App\Model;
class Profile {
    public string $email;
    public function setDisplayName(string $name): void {}
}`),
	)
	require.NoError(t, phpIndex.Index(model))
	require.NoError(t, formIndex.Index(model))
	return formIndex, phpIndex
}
