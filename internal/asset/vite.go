package asset

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func parseViteConfig(path string, tree *cst.Tree) []Resource {
	if tree == nil || tree.Root == nil {
		return nil
	}
	variables := viteObjectVariables(tree.Root)
	var result []Resource
	seen := make(map[string]struct{})
	for _, property := range jsquery.Nodes(
		tree.Root,
		jssyntax.JsProperty,
	) {
		if jsquery.PropertyName(property) != "input" {
			continue
		}
		value := jsquery.PropertyValue(property)
		object := viteObjectValue(value, variables)
		collectViteEntries(
			path,
			object,
			variables,
			seen,
			make(map[*jssyntax.Node]struct{}),
			&result,
		)
	}
	return result
}

func viteObjectVariables(
	root *jssyntax.Node,
) map[string]*jssyntax.Node {
	result := make(map[string]*jssyntax.Node)
	for _, declaration := range jsquery.Nodes(
		root,
		jssyntax.JsVariableDeclaration,
	) {
		var name string
		var object *jssyntax.Node
		for child := range declaration.ChildNodes() {
			switch child.Kind() {
			case jssyntax.JsIdentifier:
				if name == "" {
					name = jsquery.IdentifierText(child)
				}
			case jssyntax.JsObject:
				if object == nil {
					object = child
				}
			}
		}
		if name != "" && object != nil {
			result[name] = object
		}
	}
	return result
}

func viteObjectValue(
	value *jssyntax.Node,
	variables map[string]*jssyntax.Node,
) *jssyntax.Node {
	if value == nil {
		return nil
	}
	if value.Kind() == jssyntax.JsObject {
		return value
	}
	if value.Kind() == jssyntax.JsIdentifier {
		return variables[jsquery.IdentifierText(value)]
	}
	return nil
}

func collectViteEntries(
	path string,
	object *jssyntax.Node,
	variables map[string]*jssyntax.Node,
	seen map[string]struct{},
	visited map[*jssyntax.Node]struct{},
	result *[]Resource,
) {
	if object == nil {
		return
	}
	if _, duplicate := visited[object]; duplicate {
		return
	}
	visited[object] = struct{}{}
	for _, property := range jsquery.Properties(object) {
		text := strings.TrimSpace(property.Text())
		name := jsquery.PropertyName(property)
		if strings.HasPrefix(text, "...") {
			collectViteEntries(
				path,
				variables[name],
				variables,
				seen,
				visited,
				result,
			)
			continue
		}
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		target := ""
		if value := jsquery.PropertyValue(property); value != nil &&
			value.Kind() == jssyntax.JsString {
			target = resolveRelativeTarget(
				filepath.Dir(path),
				jsquery.StringValue(value),
			)
		}
		*result = append(*result, Resource{
			Name:   name,
			File:   path,
			Target: target,
			Kind:   ViteEntry,
			Range:  javascriptPropertyNameRange(property),
		})
	}
}

func javascriptPropertyNameRange(
	property *jssyntax.Node,
) cst.TextRange {
	name := jsquery.PropertyNameNode(property)
	if name == nil {
		return cst.TextRange{}
	}
	return javascriptStringRange(name)
}
