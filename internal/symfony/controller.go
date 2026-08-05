package symfony

import (
	"fmt"
	"strings"

	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

type ControllerReference struct {
	Value  string
	Target string
	Method string
}

func ParseControllerReference(value string) (ControllerReference, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "%${}") {
		return ControllerReference{}, false
	}

	var target, method string
	switch {
	case strings.Count(value, "::") == 1:
		parts := strings.SplitN(value, "::", 2)
		target, method = parts[0], parts[1]
	case strings.Count(value, ":") == 1:
		parts := strings.SplitN(value, ":", 2)
		target, method = parts[0], parts[1]
	case !strings.Contains(value, ":"):
		target, method = value, "__invoke"
	default:
		// Legacy Bundle:Controller:action shortcuts require bundle metadata.
		return ControllerReference{}, false
	}
	target = strings.TrimSpace(target)
	method = strings.TrimSpace(method)
	if target == "" || method == "" {
		return ControllerReference{}, false
	}
	return ControllerReference{
		Value:  value,
		Target: target,
		Method: method,
	}, true
}

func YAMLControllerReference(
	node *yamlsyntax.Node,
) (ControllerReference, string, bool) {
	if node == nil || node.Kind() != yamlsyntax.YamlScalar {
		return ControllerReference{}, "", false
	}
	pair := yamlquery.AncestorPair(node)
	if pair == nil || yamlquery.PairKey(pair) == node {
		return ControllerReference{}, "", false
	}
	path := yamlquery.PairPath(node)
	if len(path) < 2 || path[0] == "services" {
		return ControllerReference{}, "", false
	}
	valid := len(path) == 2 && path[1] == "controller" ||
		len(path) == 3 && path[1] == "defaults" &&
			path[2] == "_controller"
	if !valid {
		return ControllerReference{}, "", false
	}
	reference, ok := ParseControllerReference(yamlquery.ScalarValue(node))
	return reference, path[0], ok
}

func XMLControllerReference(
	node *xmlsyntax.Node,
) (ControllerReference, string, bool) {
	if attribute := xmlquery.AttributeAt(node); attribute != nil {
		route := xmlquery.ElementAt(attribute)
		if route == nil || xmlquery.ElementName(route) != "route" ||
			xmlquery.AttributeName(attribute) != "controller" {
			return ControllerReference{}, "", false
		}
		reference, ok := ParseControllerReference(
			xmlquery.AttributeValue(attribute),
		)
		return reference,
			xmlquery.AttributeValue(xmlquery.Attribute(route, "id")),
			ok
	}

	text := xmlquery.TextAt(node)
	if text == nil {
		return ControllerReference{}, "", false
	}
	defaultNode := xmlquery.ElementAt(text)
	if defaultNode == nil || xmlquery.ElementName(defaultNode) != "default" ||
		xmlquery.AttributeValue(xmlquery.Attribute(defaultNode, "key")) !=
			"_controller" {
		return ControllerReference{}, "", false
	}
	route := xmlquery.ParentElement(defaultNode)
	if route == nil || xmlquery.ElementName(route) != "route" {
		return ControllerReference{}, "", false
	}
	reference, ok := ParseControllerReference(
		strings.TrimSpace(xmlquery.TextContent(defaultNode)),
	)
	return reference,
		xmlquery.AttributeValue(xmlquery.Attribute(route, "id")),
		ok
}

type ControllerResolution struct {
	Reference      ControllerReference
	Class          semantic.Symbol
	Method         semantic.Symbol
	TargetExists   bool
	ClassFound     bool
	MethodDeclared bool
	MethodFound    bool
}

// Deprecated reports whether the resolved public controller action or its
// containing class is marked @deprecated.
func (r ControllerResolution) Deprecated() bool {
	return r.ClassFound &&
		r.Class.Flags.Has(semantic.DeprecatedFlag) ||
		r.MethodFound &&
			r.Method.Flags.Has(semantic.DeprecatedFlag)
}

func ResolveControllerReference(
	reference ControllerReference,
	serviceIndex *ServiceIndex,
	phpIndex *php.PHPIndex,
) (ControllerResolution, error) {
	result := ControllerResolution{Reference: reference}
	if phpIndex == nil {
		return result, nil
	}

	target := strings.TrimPrefix(reference.Target, "\\")
	className := ""
	if strings.Contains(target, ":") {
		class, found := resolveLegacyTwigControllerClass(target, phpIndex)
		if !found {
			return result, nil
		}
		result.TargetExists = true
		result.ClassFound = true
		result.Class = class
		className = class.FullyQualified
	}
	if serviceIndex != nil {
		service, found, err := resolveControllerService(
			serviceIndex,
			target,
			make(map[string]struct{}),
		)
		if err != nil {
			return result, fmt.Errorf(
				"resolve controller service %q: %w",
				target,
				err,
			)
		}
		if found {
			result.TargetExists = true
			className = strings.TrimPrefix(service.Class, "\\")
			if className == "" && strings.Contains(service.ID, "\\") {
				className = strings.TrimPrefix(service.ID, "\\")
			}
		}
	}

	if class, found := phpIndex.FindClass(target); found {
		result.TargetExists = true
		result.ClassFound = true
		result.Class = class
		className = class.FullyQualified
	}
	if className == "" {
		return result, nil
	}
	if !result.ClassFound {
		class, found := phpIndex.FindClass(className)
		if !found {
			return result, nil
		}
		result.Class = class
		result.ClassFound = true
	}
	methodNames := []string{reference.Method}
	if strings.Contains(target, ":") {
		switch {
		case strings.HasSuffix(reference.Method, "Action"):
			methodNames = append(
				methodNames,
				strings.TrimSuffix(reference.Method, "Action"),
			)
		default:
			methodNames = append(
				methodNames,
				reference.Method+"Action",
			)
		}
	}
	for _, methodName := range methodNames {
		for _, method := range phpIndex.FindMethods(
			result.Class.FullyQualified,
			methodName,
		) {
			if !result.MethodDeclared {
				result.Method = method
				result.MethodDeclared = true
			}
			if method.Visibility != semantic.Public {
				continue
			}
			result.Method = method
			result.MethodFound = true
			return result, nil
		}
	}
	return result, nil
}

func resolveLegacyTwigControllerClass(
	target string,
	phpIndex *php.PHPIndex,
) (semantic.Symbol, bool) {
	parts := strings.SplitN(target, ":", 2)
	if len(parts) != 2 {
		return semantic.Symbol{}, false
	}
	bundle := strings.ToLower(strings.TrimSpace(parts[0]))
	controller := strings.Trim(
		strings.ReplaceAll(strings.TrimSpace(parts[1]), "/", `\`),
		`\`,
	)
	controller = strings.TrimSuffix(controller, "Controller")
	if bundle == "" || controller == "" {
		return semantic.Symbol{}, false
	}
	expectedSuffix := strings.ToLower(
		`\Controller\` + controller + "Controller",
	)
	for _, class := range phpIndex.ClassSymbols() {
		if class.Kind != semantic.ClassSymbol {
			continue
		}
		name := `\` + strings.TrimLeft(class.FullyQualified, `\`)
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, expectedSuffix) {
			continue
		}
		prefix := strings.TrimSuffix(lower, expectedSuffix)
		segment := prefix
		if offset := strings.LastIndex(segment, `\`); offset >= 0 {
			segment = segment[offset+1:]
		}
		if segment == bundle ||
			segment == bundle+"bundle" ||
			strings.TrimSuffix(segment, "bundle") ==
				strings.TrimSuffix(bundle, "bundle") {
			return class, true
		}
	}
	return semantic.Symbol{}, false
}

func resolveControllerService(
	index *ServiceIndex,
	id string,
	visited map[string]struct{},
) (Service, bool, error) {
	key := strings.ToLower(id)
	if _, exists := visited[key]; exists {
		return Service{}, false, nil
	}
	visited[key] = struct{}{}
	service, found, err := index.GetServiceByID(id)
	if err != nil || !found || service.AliasTarget == "" {
		return service, found, err
	}
	target, targetFound, targetErr := resolveControllerService(
		index,
		service.AliasTarget,
		visited,
	)
	if targetErr != nil || !targetFound {
		return service, found, targetErr
	}
	return target, true, nil
}
