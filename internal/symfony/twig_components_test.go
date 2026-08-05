package symfony

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseXMLTwigComponents(t *testing.T) {
	source := `<container>
  <services>
    <service id="ux.twig_component.component_factory">
      <argument><argument key="nested">ignored</argument></argument>
      <argument/>
      <argument/>
      <argument/>
      <argument type="collection">
        <argument key="Shop:Card" type="collection">
          <argument key="class">\App\Twig\Components\ShopCard\</argument>
          <argument key="template">components/shop/Card.html.twig</argument>
          <argument key="mount" type="collection">
            <argument key="template">wrong/nested.html.twig</argument>
          </argument>
        </argument>
        <argument key="OuterName" type="collection">
          <argument key="key">Renamed</argument>
          <argument key="template">components/Renamed.html.twig</argument>
        </argument>
        <argument key="DynamicCard" type="collection">
          <argument key="class">App\Twig\Components\DynamicCard</argument>
          <argument key="template_from_method">getTemplate</argument>
        </argument>
      </argument>
      <argument type="collection">
        <argument key="App\Twig\Components\Renamed">Renamed</argument>
        <argument key="\App\Twig\Components\ClassOnly\">ClassOnly</argument>
      </argument>
    </service>
  </services>
</container>`
	path := "/project/var/cache/dev/Container.xml"
	components := ParseXMLTwigComponents(path, []byte(source))
	require.Len(t, components, 4)

	byName := make(map[string]ContainerTwigComponent, len(components))
	for _, component := range components {
		byName[component.Name] = component
		assert.Equal(t, path, component.Path)
	}

	shop := byName["Shop:Card"]
	assert.Equal(t, "App\\Twig\\Components\\ShopCard", shop.Class)
	assert.Equal(t, "components/shop/Card.html.twig", shop.Template)
	assert.Equal(
		t,
		"Shop:Card",
		source[shop.NameRange.Start:shop.NameRange.End],
	)
	assert.Equal(
		t,
		`\App\Twig\Components\ShopCard\`,
		source[shop.ClassRange.Start:shop.ClassRange.End],
	)
	assert.Equal(
		t,
		"components/shop/Card.html.twig",
		source[shop.TemplateRange.Start:shop.TemplateRange.End],
	)

	renamed := byName["Renamed"]
	assert.Equal(t, "App\\Twig\\Components\\Renamed", renamed.Class)
	assert.Equal(t, "components/Renamed.html.twig", renamed.Template)
	assert.Equal(
		t,
		"Renamed",
		source[renamed.NameRange.Start:renamed.NameRange.End],
	)

	dynamic := byName["DynamicCard"]
	assert.Empty(t, dynamic.Template)
	assert.Equal(t, "getTemplate", dynamic.TemplateFromMethod)

	classOnly := byName["ClassOnly"]
	assert.Equal(t, "App\\Twig\\Components\\ClassOnly", classOnly.Class)
	assert.Empty(t, classOnly.Template)
}

func TestParseXMLTwigComponentsIgnoresUnrelatedOrIncompleteServices(
	t *testing.T,
) {
	source := `<container><services>
  <service id="other">
    <argument/><argument/><argument/><argument/>
    <argument><argument key="Wrong"/></argument>
  </service>
  <service id="ux.twig_component.component_factory">
    <argument/><argument/><argument/><argument/>
  </service>
</services></container>`
	assert.Empty(t, ParseXMLTwigComponents("container.xml", []byte(source)))
}
