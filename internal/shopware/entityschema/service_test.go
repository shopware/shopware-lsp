package entityschema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPatchServiceConfigurationFormats(t *testing.T) {
	definition := `Acme\Demo\ExampleDefinition`
	tests := map[string]string{
		"services.xml": `<container><services>
</services></container>`,
		"services.yaml": "services:\n  _defaults:\n    autowire: true\n",
		"services.php":  "<?php\nreturn static function ($container, $services): void {\n};\n",
	}
	for path, source := range tests {
		t.Run(path, func(t *testing.T) {
			result, err := PatchServiceConfiguration(path, source, definition)
			require.NoError(t, err)
			require.Contains(t, result, definition)
			require.Contains(t, result, "shopware.entity.definition")
			again, err := PatchServiceConfiguration(path, result, definition)
			require.NoError(t, err)
			require.Equal(t, 1, strings.Count(again, "shopware.entity.definition"))
		})
	}
}

func TestPatchServiceConfigurationCreatesXML(t *testing.T) {
	result, err := PatchServiceConfiguration("services.xml", "", `Acme\DemoDefinition`)
	require.NoError(t, err)
	require.Contains(t, result, `<service id="Acme\DemoDefinition">`)
}

func TestPatchServiceConfigurationCreatesYAML(t *testing.T) {
	result, err := PatchServiceConfiguration("services.yaml", "", `Acme\DemoDefinition`)
	require.NoError(t, err)
	require.Equal(t, "services:\n  Acme\\DemoDefinition:\n    tags:\n      - { name: shopware.entity.definition }\n", result)
}

func TestPatchYAMLServiceStaysInsideServicesRoot(t *testing.T) {
	source := "services:\n  existing: ~\n\nwhen@test:\n  services: {}\n"
	result, err := PatchServiceConfiguration("services.yaml", source, `Acme\DemoDefinition`)
	require.NoError(t, err)
	require.Less(t, strings.Index(result, `Acme\DemoDefinition`), strings.Index(result, "when@test:"))
}

func TestRemoveServiceConfigurationFormats(t *testing.T) {
	definition := `Acme\Demo\ExampleDefinition`
	tests := map[string]string{
		"services.xml": `<?xml version="1.0"?><container><services>
  <service id="Acme\Other"/>
  <service id="Acme\Demo\ExampleDefinition"><tag name="shopware.entity.definition"/></service>
</services></container>`,
		"services.yaml": "services:\n  Acme\\Other: ~\n  Acme\\Demo\\ExampleDefinition:\n    tags:\n      - { name: shopware.entity.definition }\n",
		"services.php":  "<?php\nreturn static function ($container, $services): void {\n    $services->set(\\Acme\\Other::class);\n    $services->set(\\Acme\\Demo\\ExampleDefinition::class)->tag('shopware.entity.definition');\n};\n",
	}
	for path, source := range tests {
		t.Run(path, func(t *testing.T) {
			result, err := RemoveServiceConfiguration(path, source, definition)
			require.NoError(t, err)
			require.NotContains(t, result, definition)
			require.Contains(t, result, `Acme\Other`)
			again, err := RemoveServiceConfiguration(path, result, definition)
			require.NoError(t, err)
			require.Equal(t, result, again)
		})
	}
}
