package symfony

import (
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigControllerReferencesNormalizeStaticFirstArguments(t *testing.T) {
	source := `{{ controller('test::action') }}
{{ controller      ('te\\st::action2') }}
{{ render(controller('\\App\\Controller\\NavController::menuAction', {
    menu: current
})) }}
{{ CONTROLLER("app.navigation:menu") }}
{{ controller('DemoBundle:Admin/Nav:show') }}
{{ controller(dynamic) }}
{{ controller("App\\Controller\\#{name}Controller::show") }}`
	root := twigparser.Parse(source).Tree.Root
	references := TwigControllerReferences(root)
	require.Len(t, references, 5)
	assert.Equal(t, []string{
		"test::action",
		`te\st::action2`,
		`App\Controller\NavController::menuAction`,
		"app.navigation:menu",
		"DemoBundle:Admin/Nav:show",
	}, []string{
		references[0].Value,
		references[1].Value,
		references[2].Value,
		references[3].Value,
		references[4].Value,
	})
	assert.Equal(t, "app\\controller\\navcontroller::menuaction",
		ControllerReferenceKey(references[2].ControllerReference))
	assert.Equal(t, "DemoBundle:Admin/Nav", references[4].Target)
	assert.Equal(t, "show", references[4].Method)

	var emptyString *twigsyntax.Node
	emptyRoot := twigparser.Parse(`{{ controller('') }}`).Tree.Root
	for _, literal := range twigquery.Nodes(
		emptyRoot,
		twigsyntax.TwigLiteralString,
	) {
		emptyString = literal
		break
	}
	require.NotNil(t, emptyString)
	value, ok := TwigControllerValueAt(emptyString)
	require.True(t, ok)
	assert.Empty(t, value.Value)
}
