package stimulus

import (
	"testing"

	jsparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsonparser "github.com/shopware/shopware-lsp/internal/parser/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControllersInJavaScript(t *testing.T) {
	source := `import { Controller } from '@hotwired/stimulus';
export default class extends Controller {}`
	root := jsparser.Parse(source).Tree.Root
	controllers := ControllersInJavaScript(
		"/project/assets/controllers/users/list_controller.js",
		root,
		source,
	)
	require.Len(t, controllers, 1)
	assert.Equal(t, "users--list", controllers[0].Name)
	assert.Equal(t, JavaScriptSource, controllers[0].Source)

	bootstrap := `const app = startStimulusApp();
app.register('search-form', SearchController);
other.register('ignored', OtherController);`
	registered := ControllersInJavaScript(
		"/project/assets/bootstrap.ts",
		jsparser.Parse(bootstrap).Tree.Root,
		bootstrap,
	)
	require.Len(t, registered, 1)
	assert.Equal(t, "search-form", registered[0].Name)
	assert.Equal(t, RegisteredSource, registered[0].Source)
	assert.Equal(
		t,
		"search-form",
		bootstrap[registered[0].Range.Start:registered[0].Range.End],
	)
}

func TestControllersInJavaScriptRequiresStimulusClass(t *testing.T) {
	for _, source := range []string{
		`export default class extends Controller {}`,
		`import { Controller } from './local'; export default class extends Controller {}`,
		`import { Controller } from '@hotwired/stimulus'; export default class Foo {}`,
	} {
		controllers := ControllersInJavaScript(
			"/project/assets/controllers/hello_controller.js",
			jsparser.Parse(source).Tree.Root,
			source,
		)
		assert.Empty(t, controllers)
	}
}

func TestControllersInJSON(t *testing.T) {
	source := `{
  "controllers": {
    "@symfony/ux-chartjs": {
      "chart": {"enabled": true},
      "disabled": {"enabled": false}
    },
    "@symfony/ux-dropzone": {
      "dropzone": {}
    }
  }
}`
	controllers := ControllersInJSON(
		"/project/assets/controllers.json",
		jsonparser.Parse(source).Tree.Root,
	)
	require.Len(t, controllers, 2)
	assert.Equal(t, "symfony--ux-chartjs--chart", controllers[0].Name)
	assert.Equal(t, "@symfony/ux-chartjs/chart", controllers[0].TwigName())
	assert.Equal(t, ControllersJSONSource, controllers[0].Source)
	assert.Equal(
		t,
		"chart",
		source[controllers[0].Range.Start:controllers[0].Range.End],
	)
	assert.Equal(t, "symfony--ux-dropzone--dropzone", controllers[1].Name)
}
