package query

import (
	"testing"

	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

func TestMappingSequenceAndPathQueries(t *testing.T) {
	result := yamlparser.Parse(`services:
  App\Service\Example:
    class: App\Service\Example
    tags:
      - name: kernel.event_subscriber
      - { name: doctrine.listener }
`)
	if len(result.Errors) != 0 {
		t.Fatalf("parse errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}

	root := RootValue(result.Tree.Root)
	services := Property(root, "services")
	service := Property(services, `App\Service\Example`)
	if !IsMapping(service) {
		t.Fatalf("service = %v", service)
	}
	if class := ScalarValue(Property(service, "class")); class != `App\Service\Example` {
		t.Fatalf("class = %q", class)
	}

	tags := Property(service, "tags")
	items := Items(tags)
	if len(items) != 2 {
		t.Fatalf("tag items = %d", len(items))
	}
	firstName := Property(ItemValue(items[0]), "name")
	if ScalarValue(firstName) != "kernel.event_subscriber" {
		t.Fatalf("first tag = %q", ScalarValue(firstName))
	}

	classPair := PropertyPair(service, "class")
	if path := PairPath(PairValue(classPair)); len(path) != 3 ||
		path[0] != "services" ||
		path[1] != `App\Service\Example` ||
		path[2] != "class" {
		t.Fatalf("class path = %#v", path)
	}
}

func TestDecoratedCollectionQuery(t *testing.T) {
	result := yamlparser.Parse("defaults: &defaults\n  enabled: true\n")
	root := RootValue(result.Tree.Root)
	defaults := Property(root, "defaults")
	if !IsMapping(defaults) || ScalarValue(Property(defaults, "enabled")) != "true" {
		t.Fatalf("decorated mapping = %v\n%s", defaults, syntax.DebugTree(result.Tree.Root))
	}
}

func TestNullQueryDistinguishesQuotedNull(t *testing.T) {
	result := yamlparser.Parse("empty: ~\nword: null\nquoted: 'null'\nmissing:\n")
	root := RootValue(result.Tree.Root)
	if !IsNull(Property(root, "empty")) ||
		!IsNull(Property(root, "word")) ||
		!IsNull(Property(root, "missing")) ||
		IsNull(Property(root, "quoted")) {
		t.Fatalf("null classification failed\n%s", syntax.DebugTree(result.Tree.Root))
	}
}
