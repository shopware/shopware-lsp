package symfony

import (
	"testing"

	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestXMLServiceContexts(t *testing.T) {
	root := xmlparser.Parse(`<service id="App\Service"><argument type="service" id="Other\Service"/><argument type="tagged_iterator" tag="app.handler"/><argument>%app.parameter%</argument><tag name="kernel.event_subscriber"/></service>`).Tree.Root

	service := xmlquery.Elements(root, "service")[0]
	assert.True(t, XMLServiceIsServiceID(xmlquery.Attribute(service, "id")))

	arguments := xmlquery.ChildElements(service, "argument")
	assert.True(t, XMLServiceIsServiceReference(xmlquery.Attribute(arguments[0], "id")))
	assert.Equal(t, "App\\Service", XMLCurrentServiceID(arguments[0]))
	assert.True(t, XMLServiceIsArgumentTag(xmlquery.Attribute(arguments[1], "tag")))
	assert.True(t, XMLServiceIsParameterReference(arguments[2]))
	assert.Equal(t, "app.parameter", XMLParameterReferenceName(arguments[2]))

	tag := xmlquery.ChildElement(service, "tag")
	require.NotNil(t, tag)
	assert.True(t, XMLServiceIsTagElement(xmlquery.Attribute(tag, "name")))
}

func TestXMLServiceReferenceVariants(t *testing.T) {
	root := xmlparser.Parse(`<container>
<services>
  <service id="decorator" class="App\Decorator" decorates="app.target">
    <factory service="@app.factory"/>
    <argument type="service" id="@?app.optional" on-invalid="ignore"/>
  </service>
  <alias id="alias" service="app.target"/>
</services>
</container>`).Tree.Root
	service := xmlquery.Elements(root, "service")[0]
	name, ok := XMLServiceReferenceName(xmlquery.Attribute(service, "decorates"))
	assert.True(t, ok)
	assert.Equal(t, "app.target", name)

	factory := xmlquery.ChildElement(service, "factory")
	name, ok = XMLServiceReferenceName(xmlquery.Attribute(factory, "service"))
	assert.True(t, ok)
	assert.Equal(t, "app.factory", name)

	argument := xmlquery.ChildElement(service, "argument")
	name, ok = XMLServiceReferenceName(xmlquery.Attribute(argument, "id"))
	assert.True(t, ok)
	assert.Equal(t, "app.optional", name)
	assert.True(t, XMLServiceReferenceOptional(argument))

	alias := xmlquery.Elements(root, "alias")[0]
	name, ok = XMLServiceReferenceName(xmlquery.Attribute(alias, "service"))
	assert.True(t, ok)
	assert.Equal(t, "app.target", name)

	class, ok := XMLClassReferenceName(xmlquery.Attribute(service, "class"))
	assert.True(t, ok)
	assert.Equal(t, `App\Decorator`, class)

	emptyRoot := xmlparser.Parse(
		`<argument type="service" id=""/>`,
	).Tree.Root
	empty := xmlquery.Elements(emptyRoot, "argument")[0]
	name, ok = XMLServiceReferenceName(xmlquery.Attribute(empty, "id"))
	assert.True(t, ok)
	assert.Empty(t, name)
}
