package query

import (
	"testing"

	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	"github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestElementAndAttributeQueries(t *testing.T) {
	source := `<container><services><service id="App\Service" class='App\Service'><tag name="kernel.event_subscriber"/></service></services></container>`
	root := xmlparser.Parse(source).Tree.Root

	services := Elements(root, "service")
	require.Len(t, services, 1)
	assert.Equal(t, "App\\Service", AttributeValue(Attribute(services[0], "id")))
	assert.Equal(t, "App\\Service", AttributeValues(services[0])["class"])
	require.Len(t, ChildElements(services[0], "tag"), 1)
}

func TestTextAndCdataQueries(t *testing.T) {
	root := xmlparser.Parse(`<meta><name> Test <![CDATA[App]]> </name><label lang="de">Deutsch</label><label>English</label></meta>`).Tree.Root
	meta := Elements(root, "meta")[0]
	name := ChildElement(meta, "name")
	assert.Equal(t, " Test App ", TextContent(name))
	assert.Equal(t, "English", TextContent(ChildElements(meta, "label")[1]))
}

func TestIncompleteAttributeContext(t *testing.T) {
	source := `<argument type="service" id="App\Service`
	root := xmlparser.Parse(source).Tree.Root
	offset := uint32(len(source) - 1)
	node := root.NodeAtOffset(offset)

	attribute := AttributeAt(node)
	require.NotNil(t, attribute)
	assert.Equal(t, "id", AttributeName(attribute))
	assert.Equal(t, `App\Service`, AttributeValue(attribute))
	assert.Equal(t, syntax.XmlElement, ElementAt(attribute).Kind())
}
