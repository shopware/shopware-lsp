package query

import (
	"testing"

	jsonparser "github.com/shopware/shopware-lsp/internal/parser/json"
	"github.com/shopware/shopware-lsp/internal/parser/json/syntax"
)

func TestObjectQueries(t *testing.T) {
	result := jsonparser.Parse(`{
		"config": {
			"name": "theme",
			"enabled": true,
			"order": 100,
			"items": [1, 2]
		}
	}`)
	if len(result.Errors) != 0 {
		t.Fatalf("parse errors: %v", result.Errors)
	}

	root := RootValue(result.Tree.Root)
	config := Property(root, "config")
	if config == nil || config.Kind() != syntax.JsonObject {
		t.Fatalf("config property = %v", config)
	}
	if value := StringValue(Property(config, "name")); value != "theme" {
		t.Fatalf("name = %q", value)
	}
	if value, ok := BooleanValue(Property(config, "enabled")); !ok || !value {
		t.Fatalf("enabled = %v, %v", value, ok)
	}
	if value, ok := IntegerValue(Property(config, "order")); !ok || value != 100 {
		t.Fatalf("order = %v, %v", value, ok)
	}
	if items := Property(config, "items"); items == nil || items.Kind() != syntax.JsonArray {
		t.Fatalf("items = %v", items)
	}
}
