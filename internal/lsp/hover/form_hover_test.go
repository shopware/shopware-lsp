package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormHoverShowsTypeOptionAndDataClassDetails(t *testing.T) {
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
class ProfileType extends \Symfony\Component\Form\AbstractType {
    public function getBlockPrefix(): string { return 'profile'; }
    public function configureOptions($resolver): void {
        $resolver->setDefaults([
            'data_class' => \App\Model\Profile::class,
            'translation_domain' => 'profile',
        ]);
    }
    public function buildForm(\Symfony\Component\Form\FormBuilderInterface $builder, array $options): void {
        $builder->add('displayName', 'profile', ['translation_domain' => 'messages']);
    }
    public function buildView($view, $form, array $options): void {
        $view->vars['profile_theme'] = 'standard';
    }
}`
	path := "/project/src/ProfileType.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, formIndex.Index(parsed))
	document := lsp.NewTextDocument("file://"+path, source, 1)
	provider := NewFormHoverProvider("/project", formIndex)

	for _, test := range []struct {
		needle   string
		contains []string
	}{
		{
			needle: "'profile', ['translation_domain'",
			contains: []string{
				"Symfony form type",
				"App\\Form\\ProfileType",
				"Data class: `App\\Model\\Profile`",
			},
		},
		{
			needle: "'translation_domain' => 'messages'",
			contains: []string{
				"Symfony form option",
				"Defined by `App\\Form\\ProfileType`",
				"Default: `'profile'`",
			},
		},
		{
			needle: "'displayName', 'profile'",
			contains: []string{
				"Symfony form field",
				"Data property: `App\\Model\\Profile::$displayName`",
				"PHP type: `string`",
			},
		},
	} {
		offset := strings.Index(source, test.needle) + 2
		if strings.Contains(test.needle, "profile', [") {
			offset = strings.Index(source, test.needle) + 2
		}
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		if strings.HasPrefix(test.needle, "'translation") {
			node = document.SyntaxTree.Root.NodeAtOffset(
				uint32(strings.Index(source, test.needle) + 2),
			)
		}
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		params := &protocol.HoverParams{}
		params.TextDocument.URI = document.URI
		result, hoverErr := provider.GetHover(ctx, &lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		})
		require.NoError(t, hoverErr)
		require.NotNil(t, result, test.needle)
		for _, expected := range test.contains {
			assert.Contains(t, result.Contents.Value, expected)
		}
	}

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
	templateSource := `{{ profile.displayName }}`
	template := lsp.NewTextDocument(
		"file:///project/templates/profile/edit.html.twig",
		templateSource,
		1,
	)
	offset := strings.Index(templateSource, "displayName") + 2
	node := template.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	line, character := template.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.HoverParams{}
	params.TextDocument.URI = template.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, hoverErr := NewFormHoverProvider(
		"/project",
		formIndex,
		phpIndex,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        template,
			Language:        template.SyntaxLanguage,
			DocumentContent: template.Text,
			DocumentTree:    template.SyntaxTree,
			LineIndex:       template.LineIndex,
			Root:            template.SyntaxTree.Root,
			Node:            node,
		},
	})
	require.NoError(t, hoverErr)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Symfony form field")
	assert.Contains(
		t,
		result.Contents.Value,
		"Data property: `App\\Model\\Profile::$displayName`",
	)

	viewTemplateSource := `{{ profile.vars.profile_theme }}`
	viewTemplate := lsp.NewTextDocument(
		"file:///project/templates/profile/edit.html.twig",
		viewTemplateSource,
		1,
	)
	viewOffset := strings.Index(viewTemplateSource, "profile_theme") + 2
	viewNode := viewTemplate.SyntaxTree.Root.NodeAtOffset(uint32(viewOffset))
	line, character = viewTemplate.LineIndex.PositionUTF16(uint32(viewOffset))
	viewParams := &protocol.HoverParams{}
	viewParams.TextDocument.URI = viewTemplate.URI
	viewParams.Position.Line = int(line)
	viewParams.Position.Character = int(character)
	viewResult, hoverErr := NewFormHoverProvider(
		"/project",
		formIndex,
		phpIndex,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: viewParams,
		SyntaxContext: lsp.SyntaxContext{
			Document:        viewTemplate,
			Language:        viewTemplate.SyntaxLanguage,
			DocumentContent: viewTemplate.Text,
			DocumentTree:    viewTemplate.SyntaxTree,
			LineIndex:       viewTemplate.LineIndex,
			Root:            viewTemplate.SyntaxTree.Root,
			Node:            viewNode,
		},
	})
	require.NoError(t, hoverErr)
	require.NotNil(t, viewResult)
	assert.Contains(t, viewResult.Contents.Value, "Symfony FormView variable")
	assert.Contains(t, viewResult.Contents.Value, "PHP type: `string`")
	assert.Contains(t, viewResult.Contents.Value, "Assigned value: `'standard'`")
}
