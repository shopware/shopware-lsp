package symfony

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

// ParseYAMLServices parses Symfony YAML service definitions and parameters.
func ParseYAMLServices(path string, data []byte) ([]Service, []Parameter, error) {
	result := yamlparser.Parse(string(data))
	return ParseYAMLServicesTree(path, result.Tree, yamlsyntax.NewLineIndex(result.Tree.Source))
}

func ParseYAMLServicesTree(path string, tree *yamlsyntax.Tree, lineIndex *yamlsyntax.LineIndex) ([]Service, []Parameter, error) {
	services, parameters, _, err := parseYAMLServiceConfigTree(
		path,
		tree,
		lineIndex,
	)
	return services, parameters, err
}

func parseYAMLServiceConfigTree(
	path string,
	tree *yamlsyntax.Tree,
	lineIndex *yamlsyntax.LineIndex,
) ([]Service, []Parameter, []ServicePrototype, error) {
	if tree == nil || tree.Root == nil {
		return []Service{}, []Parameter{}, []ServicePrototype{}, nil
	}
	root := yamlquery.RootValue(tree.Root)
	if !yamlquery.IsMapping(root) {
		return []Service{}, []Parameter{}, []ServicePrototype{}, nil
	}

	services, prototypes := parseYAMLServices(
		yamlquery.Property(root, "services"),
		path,
		lineIndex,
	)
	parameters := parseYAMLParameters(yamlquery.Property(root, "parameters"), path, lineIndex)
	return services, parameters, prototypes, nil
}

func parseYAMLServices(
	mapping *yamlsyntax.Node,
	path string,
	lineIndex *yamlsyntax.LineIndex,
) ([]Service, []ServicePrototype) {
	if !yamlquery.IsMapping(mapping) {
		return []Service{}, []ServicePrototype{}
	}

	services := make([]Service, 0, len(yamlquery.Pairs(mapping)))
	prototypes := make([]ServicePrototype, 0, 4)
	defaultAutowire, defaultAutowireSet := yamlServiceAutowire(
		yamlquery.Property(
			yamlquery.Property(mapping, "_defaults"),
			"autowire",
		),
	)
	for _, pair := range yamlquery.Pairs(mapping) {
		key := yamlquery.PairKey(pair)
		serviceID := yamlquery.ScalarValue(key)
		if serviceID == "" || strings.HasPrefix(serviceID, "_") {
			continue
		}

		line, _ := lineIndex.Position(key.RangeTrimmedTrivia().Start)
		value := yamlquery.PairValue(pair)
		if strings.HasSuffix(serviceID, "\\") &&
			yamlquery.IsMapping(value) &&
			yamlquery.Property(value, "resource") != nil {
			prototype := parseYAMLPrototype(
				serviceID,
				key,
				value,
				path,
				int(line)+1,
				defaultAutowire,
				defaultAutowireSet,
			)
			if prototype.Resource != "" {
				prototypes = append(prototypes, prototype)
			}
			continue
		}
		service := Service{
			ID:          serviceID,
			Tags:        make(map[string]string),
			Path:        path,
			Line:        int(line) + 1,
			Range:       pair.RangeTrimmedTrivia(),
			IDRange:     yamlScalarContentRange(key),
			Autowire:    defaultAutowire,
			AutowireSet: defaultAutowireSet,
		}

		switch {
		case yamlquery.IsMapping(value):
			parseYAMLServiceConfig(&service, value)
		case value != nil && value.Kind() == yamlsyntax.YamlScalar && !yamlquery.IsNull(value):
			scalar := yamlquery.ScalarValue(value)
			if strings.HasPrefix(scalar, "@") {
				service.AliasTarget = strings.TrimPrefix(scalar, "@")
			} else {
				service.Class = scalar
				service.ClassRange = yamlScalarContentRange(value)
			}
		}

		if service.Class == "" && service.AliasTarget == "" {
			service.Class = service.ID
			service.ClassRange = service.IDRange
		}
		services = append(services, service)
	}
	return services, prototypes
}

func parseYAMLServiceConfig(service *Service, mapping *yamlsyntax.Node) {
	if value := yamlquery.Property(mapping, "autowire"); value != nil {
		service.Autowire, _ = configuredServiceBool(
			yamlquery.ScalarValue(value),
		)
		service.AutowireSet = true
	}
	if value := yamlquery.Property(mapping, "class"); value != nil {
		service.Class = yamlquery.ScalarValue(value)
		service.ClassRange = yamlScalarContentRange(value)
	}
	if value := yamlquery.Property(mapping, "alias"); value != nil {
		service.AliasTarget = strings.TrimPrefix(yamlquery.ScalarValue(value), "@")
	}
	if value := yamlquery.Property(mapping, "decorates"); value != nil {
		service.Decorates = strings.TrimPrefix(
			yamlquery.ScalarValue(value),
			"@",
		)
		service.DecoratesRange = yamlScalarContentRange(value)
	}
	if value := yamlquery.Property(mapping, "parent"); value != nil {
		service.Parent = strings.TrimPrefix(
			yamlquery.ScalarValue(value),
			"@",
		)
		service.ParentRange = yamlScalarContentRange(value)
	}
	if value := yamlquery.Property(mapping, "deprecated"); value != nil {
		raw := yamlquery.ScalarValue(value)
		service.Deprecated = deprecatedConfigValue(raw)
		service.Deprecation = deprecationMessage(raw)
		service.DeprecatedRange = yamlScalarContentRange(value)
	}
	if value := yamlquery.Property(mapping, "tags"); value != nil {
		parseYAMLTags(service, value)
	}
}

func parseYAMLPrototype(
	namespace string,
	namespaceNode,
	mapping *yamlsyntax.Node,
	path string,
	line int,
	defaultAutowire bool,
	defaultAutowireSet bool,
) ServicePrototype {
	resourceNode := yamlquery.Property(mapping, "resource")
	prototype := ServicePrototype{
		Namespace: strings.TrimPrefix(namespace, "\\"),
		Resource: serviceConfigPath(
			path,
			yamlquery.ScalarValue(resourceNode),
		),
		Autowire:    defaultAutowire,
		AutowireSet: defaultAutowireSet,
		Tags:        make(map[string]string),
		Path:        path,
		Line:        line,
		Range: cst.TextRange{
			Start: namespaceNode.RangeTrimmedTrivia().Start,
			End:   mapping.RangeTrimmedTrivia().End,
		},
		NamespaceRange: yamlScalarContentRange(namespaceNode),
		ResourceRange:  yamlScalarContentRange(resourceNode),
	}
	if value := yamlquery.Property(mapping, "autowire"); value != nil {
		prototype.Autowire, _ = configuredServiceBool(
			yamlquery.ScalarValue(value),
		)
		prototype.AutowireSet = true
	}
	if exclude := yamlquery.Property(mapping, "exclude"); exclude != nil {
		for _, value := range yamlStringValues(exclude) {
			prototype.Excludes = append(
				prototype.Excludes,
				serviceConfigPath(path, value),
			)
		}
	}
	if tags := yamlquery.Property(mapping, "tags"); tags != nil {
		service := Service{Tags: prototype.Tags}
		parseYAMLTags(&service, tags)
		prototype.Tags = service.Tags
	}
	return prototype
}

func yamlServiceAutowire(node *yamlsyntax.Node) (bool, bool) {
	if node == nil {
		return false, false
	}
	value, _ := configuredServiceBool(yamlquery.ScalarValue(node))
	return value, true
}

func parseYAMLTags(service *Service, value *yamlsyntax.Node) {
	if yamlquery.IsSequence(value) {
		for _, item := range yamlquery.Items(value) {
			parseYAMLTag(service, yamlquery.ItemValue(item))
		}
		return
	}
	parseYAMLTag(service, value)
}

func parseYAMLTag(service *Service, value *yamlsyntax.Node) {
	if value == nil {
		return
	}
	switch value.Kind() {
	case yamlsyntax.YamlScalar:
		if tag := yamlquery.ScalarValue(value); tag != "" {
			service.Tags[tag] = ""
		}
	case yamlsyntax.YamlFlowMapping:
		if tag := yamlquery.ScalarValue(
			yamlquery.Property(value, "name"),
		); tag != "" {
			service.Tags[tag] = ""
		}
	case yamlsyntax.YamlMapping:
		if tag := yamlquery.ScalarValue(yamlquery.Property(value, "name")); tag != "" {
			service.Tags[tag] = ""
		}
	}
}

func parseYAMLParameters(
	mapping *yamlsyntax.Node,
	path string,
	lineIndex *yamlsyntax.LineIndex,
) []Parameter {
	if !yamlquery.IsMapping(mapping) {
		return []Parameter{}
	}

	parameters := make([]Parameter, 0, len(yamlquery.Pairs(mapping)))
	for _, pair := range yamlquery.Pairs(mapping) {
		key := yamlquery.PairKey(pair)
		name := yamlquery.ScalarValue(key)
		if name == "" {
			continue
		}
		line, _ := lineIndex.Position(key.RangeTrimmedTrivia().Start)
		value := yamlquery.PairValue(pair)

		parameter := Parameter{
			Name: name,
			Path: path,
			Line: int(line) + 1,
		}
		if value != nil && value.Kind() == yamlsyntax.YamlScalar && !yamlquery.IsNull(value) {
			parameter.Value = yamlquery.ScalarValue(value)
		}
		parameters = append(parameters, parameter)
	}
	return parameters
}

func yamlStringValues(node *yamlsyntax.Node) []string {
	if node == nil {
		return nil
	}
	if yamlquery.IsSequence(node) {
		var result []string
		for _, item := range yamlquery.Items(node) {
			value := yamlquery.ScalarValue(yamlquery.ItemValue(item))
			if value != "" {
				result = append(result, value)
			}
		}
		return result
	}
	if value := yamlquery.ScalarValue(node); value != "" {
		return []string{value}
	}
	return nil
}

func yamlScalarContentRange(node *yamlsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] &&
		rng.End > rng.Start+1 {
		rng.Start++
		rng.End--
	}
	return rng
}
