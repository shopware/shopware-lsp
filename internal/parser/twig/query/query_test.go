package query

import (
	"slices"
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func TestSemanticQueries(t *testing.T) {
	source := `{{ 'checkout.cart'|trans }} {{ path('frontend.home', {'nested': 'not-a-route'}) }} {% sw_icon 'missing' {'pack': 'custom'} %}`
	result := twigparser.Parse(source)

	if strings := StringArgumentsInFunctions(result.Tree.Root, "path"); len(strings) != 1 || StringValue(strings[0]) != "frontend.home" {
		t.Fatalf("function string query failed:\n%s", syntax.DebugTree(result.Tree.Root))
	}

	var translated bool
	for _, literal := range Nodes(result.Tree.Root, syntax.TwigLiteralString) {
		if StringInFilter(literal, "trans") && StringValue(literal) == "checkout.cart" {
			translated = true
		}
	}
	if !translated {
		t.Fatalf("filter string query failed:\n%s", syntax.DebugTree(result.Tree.Root))
	}

	icons := Nodes(result.Tree.Root, syntax.ShopwareIcon)
	if len(icons) != 1 {
		t.Fatalf("icon query failed:\n%s", syntax.DebugTree(result.Tree.Root))
	}
	config := HashStringMap(icons[0])
	if config["pack"] != "custom" {
		t.Fatalf("hash query returned %#v:\n%s", config, syntax.DebugTree(result.Tree.Root))
	}

	iconStrings := Nodes(icons[0], syntax.TwigLiteralString)
	if len(iconStrings) != 3 {
		t.Fatalf("expected icon name, pack key, and pack value:\n%s", syntax.DebugTree(result.Tree.Root))
	}
	if !StringInTag(iconStrings[0], "sw_icon") {
		t.Fatal("icon name should be the direct sw_icon string argument")
	}
	if StringInTag(iconStrings[1], "sw_icon") || StringInTag(iconStrings[2], "sw_icon") {
		t.Fatal("strings nested in the sw_icon option hash must not be tag arguments")
	}
	if !StringIsHashValueForKey(iconStrings[2], "pack") {
		t.Fatal("pack value was not recognized as the value of the pack option")
	}
}

func TestFunctionArgumentAndHashKeyQueries(t *testing.T) {
	result := twigparser.Parse(`{{ path('product.show', {'id': product.id}) }}`)
	calls := Nodes(result.Tree.Root, syntax.TwigFunctionCall)
	if len(calls) != 1 {
		t.Fatalf("function calls = %#v", calls)
	}
	route := StringArgument(calls[0], 0)
	if route == nil || StringValue(route) != "product.show" ||
		FunctionArgumentIndex(route) != 0 {
		t.Fatalf("route argument not recognized:\n%s", syntax.DebugTree(result.Tree.Root))
	}
	if argument := FunctionArgument(calls[0], 1); argument == nil ||
		FunctionArgumentIndex(argument) != 1 {
		t.Fatalf("second argument not recognized:\n%s", syntax.DebugTree(result.Tree.Root))
	}
	hashes := Nodes(calls[0], syntax.TwigLiteralHash)
	keys := Nodes(calls[0], syntax.TwigLiteralHashKey)
	if len(hashes) != 1 || len(keys) != 1 ||
		FunctionArgumentIndex(hashes[0]) != 1 ||
		HashAt(keys[0]) != hashes[0] ||
		HashKeyAt(keys[0]) != keys[0] {
		t.Fatalf("hash key context not recognized:\n%s", syntax.DebugTree(result.Tree.Root))
	}
}

func TestTemplateTagQueries(t *testing.T) {
	result := twigparser.Parse(`{% use 'macros.html.twig' %}
{% embed 'card.html.twig' %}{% endembed %}
{% from 'forms.html.twig' import input %}
{% import 'icons.html.twig' as icons %}`)
	expected := map[string]string{
		"use":    "macros.html.twig",
		"embed":  "card.html.twig",
		"from":   "forms.html.twig",
		"import": "icons.html.twig",
	}
	found := make(map[string]string)
	for _, literal := range Nodes(result.Tree.Root, syntax.TwigLiteralString) {
		tag := TagName(TagAt(literal))
		if StringInTag(literal, tag) {
			found[tag] = StringValue(literal)
		}
	}
	if len(found) != len(expected) {
		t.Fatalf("template tags = %#v\n%s", found, syntax.DebugTree(result.Tree.Root))
	}
	for tag, value := range expected {
		if found[tag] != value {
			t.Fatalf("%s value = %q", tag, found[tag])
		}
	}
}

func TestFormThemeTagQuery(t *testing.T) {
	result := twigparser.Parse(`{% form_theme form with [
    'forms/base.html.twig',
    'forms/custom.html.twig'
] %}`)
	if len(result.Errors) != 0 {
		t.Fatalf("form_theme parse errors: %#v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
	tags := Nodes(result.Tree.Root, syntax.TwigFormTheme)
	if len(tags) != 1 || TagName(tags[0]) != "form_theme" {
		t.Fatalf("form_theme tag not preserved:\n%s", syntax.DebugTree(result.Tree.Root))
	}
	var names []string
	for _, literal := range Nodes(tags[0], syntax.TwigLiteralString) {
		if StringInTag(literal, "form_theme") {
			names = append(names, StringValue(literal))
		}
	}
	if len(names) != 2 ||
		names[0] != "forms/base.html.twig" ||
		names[1] != "forms/custom.html.twig" {
		t.Fatalf("form themes = %#v", names)
	}
}

func TestAsseticTagQuery(t *testing.T) {
	result := twigparser.Parse(`{% stylesheets
    'css/app.css'
    'css/base.css'
    'css/' ~ dynamic_name
    filter='cssrewrite'
%}
<link href="{{ asset_url }}">
{% endstylesheets %}`)
	if len(result.Errors) != 0 {
		t.Fatalf(
			"Assetic tag parse errors: %#v\n%s",
			result.Errors,
			syntax.DebugTree(result.Tree.Root),
		)
	}
	var names []string
	for _, literal := range Nodes(
		result.Tree.Root,
		syntax.TwigLiteralString,
	) {
		if StringInTag(literal, "stylesheets") {
			names = append(names, StringValue(literal))
		}
	}
	if !slices.Equal(names, []string{"css/app.css", "css/base.css"}) {
		t.Fatalf("Assetic assets = %#v", names)
	}
}
