package symfony

import (
	"testing"

	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYAMLServiceContexts(t *testing.T) {
	result := yamlparser.Parse(`services:
  App\Service\Example:
    class: App\Service\Concrete
    arguments:
      $logger: '@logger'
`)
	root := yamlquery.RootValue(result.Tree.Root)
	services := yamlquery.Property(root, "services")
	servicePair := yamlquery.PropertyPair(services, `App\Service\Example`)
	service := yamlquery.PairValue(servicePair)
	class := yamlquery.Property(service, "class")
	arguments := yamlquery.Property(service, "arguments")
	logger := yamlquery.Property(arguments, "$logger")

	if !IsYAMLServiceID(servicePair) || YAMLContextValue(servicePair) != `App\Service\Example` {
		t.Fatalf("service context not recognized")
	}
	if !IsYAMLClassPropertyInService(class) || YAMLContextValue(class) != `App\Service\Concrete` {
		t.Fatalf("class context not recognized")
	}
	if !IsYAMLArgumentServiceID(logger) || YAMLContextValue(logger) != "@logger" {
		t.Fatalf("argument context not recognized")
	}
}

func TestYAMLServiceReferenceVariants(t *testing.T) {
	result := yamlparser.Parse(`services:
  App\Service\Example:
    class: App\Service\Concrete
    decorates: app.target
    parent: '@app.parent'
    arguments:
      - '@?app.optional'
  app.alias:
    alias: app.target
`)
	scalars := yamlquery.Nodes(result.Tree.Root, yamlsyntax.YamlScalar)
	values := make(map[string]bool)
	var classes []string
	for _, scalar := range scalars {
		if name, ok := YAMLServiceReferenceName(scalar); ok {
			values[name] = true
		}
		if name, ok := YAMLClassReferenceName(scalar); ok {
			classes = append(classes, name)
		}
	}
	require.NotEmpty(t, values)
	assert.True(t, values["app.target"])
	assert.True(t, values["app.parent"])
	assert.True(t, values["app.optional"])
	assert.NotContains(t, classes, `App\Service\Example`)
	assert.Contains(t, classes, `App\Service\Concrete`)
}
