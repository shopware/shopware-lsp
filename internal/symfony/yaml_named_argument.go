package symfony

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
)

// YAMLServiceNamedArgument describes a named constructor argument inside a
// Symfony YAML service definition.
type YAMLServiceNamedArgument struct {
	Name       string
	ServiceID  string
	ClassName  string
	Range      cst.TextRange
	Existing   []string
	Complete   bool
	HasFactory bool
}

// YAMLServiceDefaultBinding describes a name-only or typed constructor
// argument binding under services._defaults.bind.
type YAMLServiceDefaultBinding struct {
	Name  string
	Type  string
	Range cst.TextRange
}

// YAMLServiceDefaultBindingAt returns the default binding key under the
// cursor. Symfony accepts both "$name" and "Type $name" keys.
func YAMLServiceDefaultBindingAt(
	root *cst.Node,
	offset uint32,
) (YAMLServiceDefaultBinding, bool) {
	document := yamlquery.RootValue(root)
	services := yamlquery.Property(document, "services")
	if !yamlquery.IsMapping(services) {
		return YAMLServiceDefaultBinding{}, false
	}
	defaults := yamlquery.Property(services, "_defaults")
	if !yamlquery.IsMapping(defaults) {
		return YAMLServiceDefaultBinding{}, false
	}
	bindings := yamlquery.Property(defaults, "bind")
	if !yamlquery.IsMapping(bindings) {
		return YAMLServiceDefaultBinding{}, false
	}

	for _, pair := range yamlquery.Pairs(bindings) {
		key := yamlquery.PairKey(pair)
		rng := yamlScalarContentRange(key)
		if offset < rng.Start || offset > rng.End {
			continue
		}
		name, typeName, valid := parseYAMLServiceDefaultBindingKey(
			yamlquery.ScalarValue(key),
		)
		if !valid {
			return YAMLServiceDefaultBinding{}, false
		}
		return YAMLServiceDefaultBinding{
			Name:  name,
			Type:  typeName,
			Range: rng,
		}, true
	}
	return YAMLServiceDefaultBinding{}, false
}

// YAMLServiceNamedArguments returns complete "$name: value" argument keys.
func YAMLServiceNamedArguments(
	root *cst.Node,
) []YAMLServiceNamedArgument {
	var result []YAMLServiceNamedArgument
	for _, context := range yamlServiceNamedArgumentContexts(root) {
		if context.Complete {
			result = append(result, context)
		}
	}
	return result
}

// YAMLServiceNamedArgumentAt returns the named-argument key under the cursor.
// Incomplete keys such as "$log" are included so completion remains available
// while the YAML document is temporarily malformed.
func YAMLServiceNamedArgumentAt(
	root *cst.Node,
	offset uint32,
) (YAMLServiceNamedArgument, bool) {
	for _, servicePair := range yamlServicePairs(root) {
		serviceRange := servicePair.RangeTrimmedTrivia()
		if offset < serviceRange.Start || offset > serviceRange.End {
			continue
		}
		for _, context := range yamlServiceNamedArgumentContextsForService(
			servicePair,
		) {
			if offset >= context.Range.Start && offset <= context.Range.End {
				return context, true
			}
		}
		return YAMLServiceNamedArgument{}, false
	}
	return YAMLServiceNamedArgument{}, false
}

// ResolveYAMLServiceNamedArgumentClass resolves the configured class, falling
// back to the indexed service and following aliases when the local YAML
// definition only provides a service ID.
func ResolveYAMLServiceNamedArgumentClass(
	index *ServiceIndex,
	argument YAMLServiceNamedArgument,
) (string, bool, error) {
	if argument.ClassName != "" {
		return strings.TrimPrefix(argument.ClassName, "\\"), true, nil
	}
	if index == nil || argument.ServiceID == "" {
		return "", false, nil
	}
	return index.ResolveServiceClassName(argument.ServiceID)
}

func yamlServiceNamedArgumentContexts(
	root *cst.Node,
) []YAMLServiceNamedArgument {
	var result []YAMLServiceNamedArgument
	for _, servicePair := range yamlServicePairs(root) {
		result = append(
			result,
			yamlServiceNamedArgumentContextsForService(servicePair)...,
		)
	}
	return result
}

func yamlServicePairs(root *cst.Node) []*cst.Node {
	document := yamlquery.RootValue(root)
	services := yamlquery.Property(document, "services")
	if !yamlquery.IsMapping(services) {
		return nil
	}
	return yamlquery.Pairs(services)
}

func yamlServiceNamedArgumentContextsForService(
	servicePair *cst.Node,
) []YAMLServiceNamedArgument {
	serviceID := yamlquery.ScalarValue(yamlquery.PairKey(servicePair))
	if serviceID == "" || strings.HasPrefix(serviceID, "_") {
		return nil
	}
	config := yamlquery.PairValue(servicePair)
	if !yamlquery.IsMapping(config) {
		return nil
	}
	arguments := yamlquery.Property(config, "arguments")
	if arguments == nil {
		return nil
	}

	base := YAMLServiceNamedArgument{
		ServiceID:  serviceID,
		ClassName:  yamlServiceClassName(serviceID, config),
		HasFactory: yamlquery.Property(config, "factory") != nil,
	}
	if yamlquery.IsMapping(arguments) {
		pairs := yamlquery.Pairs(arguments)
		existing := make([]string, 0, len(pairs))
		for _, argumentPair := range pairs {
			name := yamlquery.ScalarValue(yamlquery.PairKey(argumentPair))
			if isYAMLNamedArgument(name) {
				existing = append(existing, name)
			}
		}
		result := make([]YAMLServiceNamedArgument, 0, len(existing))
		for _, argumentPair := range pairs {
			key := yamlquery.PairKey(argumentPair)
			name := yamlquery.ScalarValue(key)
			if !isYAMLNamedArgument(name) {
				continue
			}
			context := base
			context.Name = name
			context.Range = yamlScalarContentRange(key)
			context.Existing = existing
			context.Complete = true
			result = append(result, context)
		}
		return result
	}

	// During completion, "$name" without a trailing colon is parsed as the
	// scalar value of the arguments pair instead of as a nested mapping.
	name := yamlquery.ScalarValue(arguments)
	if !isYAMLNamedArgument(name) {
		return nil
	}
	context := base
	context.Name = name
	context.Range = yamlScalarContentRange(arguments)
	context.Existing = []string{name}
	return []YAMLServiceNamedArgument{context}
}

func yamlServiceClassName(serviceID string, config *cst.Node) string {
	if class := yamlquery.ScalarValue(
		yamlquery.Property(config, "class"),
	); staticYAMLServiceClassName(class) {
		return strings.TrimPrefix(class, "\\")
	}
	if staticPHPClassName(serviceID) {
		return strings.TrimPrefix(serviceID, "\\")
	}
	return ""
}

func staticYAMLServiceClassName(value string) bool {
	value = strings.TrimSpace(strings.TrimPrefix(value, "\\"))
	return value != "" && !strings.ContainsAny(value, "%${}@ \t")
}

func isYAMLNamedArgument(name string) bool {
	name = strings.TrimSpace(name)
	return len(name) > 1 && strings.HasPrefix(name, "$") &&
		!strings.ContainsAny(name[1:], " \t:$")
}

func parseYAMLServiceDefaultBindingKey(
	value string,
) (name, typeName string, valid bool) {
	value = strings.TrimSpace(value)
	dollar := strings.LastIndex(value, "$")
	if dollar < 0 {
		return "", "", false
	}
	name = strings.TrimSpace(value[dollar:])
	if !isYAMLNamedArgument(name) {
		return "", "", false
	}
	typeName = strings.TrimSpace(value[:dollar])
	if strings.Contains(typeName, "$") {
		return "", "", false
	}
	return name, typeName, true
}
