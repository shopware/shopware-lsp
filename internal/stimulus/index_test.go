package stimulus

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexPersistsControllersUsagesAndRemovesStaleData(t *testing.T) {
	cacheDir := t.TempDir()
	index, err := NewIndex(cacheDir)
	require.NoError(t, err)

	controllerPath := "/project/assets/controllers/hello_controller.js"
	controllerSource := `import { Controller } from '@hotwired/stimulus';
export default class extends Controller {}`
	jsonPath := "/project/assets/controllers.json"
	jsonSource := `{"controllers":{"@symfony/ux-chartjs":{"chart":{"enabled":true}}}}`
	templatePath := "/project/templates/page.html.twig"
	templateSource := `<div data-controller="hello"></div>
{{ stimulus_controller('@symfony/ux-chartjs/chart') }}`
	for path, source := range map[string]string{
		controllerPath: controllerSource,
		jsonPath:       jsonSource,
		templatePath:   templateSource,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	controllers, err := index.Controllers()
	require.NoError(t, err)
	require.Len(t, controllers, 2)
	assert.Equal(t, []string{
		"hello",
		"@symfony/ux-chartjs/chart",
	}, controllerNames(controllers))
	usages, err := index.Usages("symfony--ux-chartjs--chart")
	require.NoError(t, err)
	require.Len(t, usages, 1)
	assert.Equal(t, templatePath, usages[0].File)
	require.NoError(t, index.Close())

	restored, err := NewIndex(cacheDir)
	require.NoError(t, err)
	controllers, err = restored.Controllers()
	require.NoError(t, err)
	require.Len(t, controllers, 2)
	require.NoError(t, restored.Index(indexer.NewParsedFile(
		controllerPath,
		[]byte("export default class {}"),
	)))
	require.NoError(t, restored.Index(indexer.NewParsedFile(
		templatePath,
		[]byte("<div></div>"),
	)))
	controllers, err = restored.Controllers()
	require.NoError(t, err)
	require.Len(t, controllers, 1)
	usages, err = restored.Usages("hello")
	require.NoError(t, err)
	assert.Empty(t, usages)
	require.NoError(t, restored.Close())
}

func controllerNames(controllers []Controller) []string {
	result := make([]string, 0, len(controllers))
	for _, controller := range controllers {
		result = append(result, controller.TwigName())
	}
	return result
}
