package query

import (
	"testing"

	scssparser "github.com/shopware/shopware-lsp/internal/parser/scss"
	"github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
)

func TestVariableAndFunctionQueries(t *testing.T) {
	result := scssparser.Parse(`
$sw-color-brand-primary: #0042a0;
.button {
  color: $sw-color-brand-primary;
  display: feature("ACCESSIBILITY_TWEAKS");
}`)
	if len(result.Errors) != 0 {
		t.Fatalf("parse errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}

	variables := Nodes(result.Tree.Root, syntax.ScssVariable)
	if len(variables) != 2 {
		t.Fatalf("variables = %d", len(variables))
	}
	if name := VariableName(variables[1]); name != "sw-color-brand-primary" {
		t.Fatalf("variable name = %q", name)
	}

	strings := Nodes(result.Tree.Root, syntax.ScssString)
	if len(strings) != 1 {
		t.Fatalf("strings = %d", len(strings))
	}
	if !StringInFunction(strings[0], "feature") {
		t.Fatal("feature string context not recognized")
	}
	if value := StringValue(strings[0]); value != "ACCESSIBILITY_TWEAKS" {
		t.Fatalf("feature name = %q", value)
	}
}

func TestNestedFunctionUsesNearestCall(t *testing.T) {
	result := scssparser.Parse(`feature(unquote("FLAG"))`)
	stringNode := Nodes(result.Tree.Root, syntax.ScssString)[0]
	if StringInFunction(stringNode, "feature") {
		t.Fatal("nested string should belong to unquote, not feature")
	}
	if !StringInFunction(stringNode, "unquote") {
		t.Fatal("nested string should belong to unquote")
	}
}
