package symfony

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

func parsePHPArrayServiceConfig(
	path string,
	root *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
) phpServiceConfig {
	if root == nil || lineIndex == nil {
		return phpServiceConfig{}
	}
	resolver := php.NewNameResolver(root)
	services := make(map[string]Service)
	parameters := make(map[string]Parameter)
	var prototypes []ServicePrototype
	for _, config := range phpArrayConfigRoots(root) {
		for _, entry := range phpquery.ArrayItems(config) {
			key := phpArrayStaticValue(
				phpquery.ArrayItemKey(entry),
				resolver,
				path,
			)
			value := phpquery.ArrayItemValue(entry)
			switch key {
			case "parameters":
				parsePHPArrayParameters(
					path,
					phpArrayNode(value),
					lineIndex,
					resolver,
					parameters,
				)
			case "services":
				parsedServices, parsedPrototypes := parsePHPArrayServices(
					path,
					phpArrayNode(value),
					lineIndex,
					resolver,
				)
				for id, service := range parsedServices {
					services[id] = service
				}
				prototypes = append(prototypes, parsedPrototypes...)
			}
		}
	}

	result := phpServiceConfig{
		Services:   make([]Service, 0, len(services)),
		Parameters: make([]Parameter, 0, len(parameters)),
		Prototypes: prototypes,
	}
	for _, service := range services {
		result.Services = append(result.Services, service)
	}
	for _, parameter := range parameters {
		result.Parameters = append(result.Parameters, parameter)
	}
	sort.Slice(result.Services, func(left, right int) bool {
		if result.Services[left].Line == result.Services[right].Line {
			return result.Services[left].ID < result.Services[right].ID
		}
		return result.Services[left].Line < result.Services[right].Line
	})
	sort.Slice(result.Parameters, func(left, right int) bool {
		if result.Parameters[left].Line == result.Parameters[right].Line {
			return result.Parameters[left].Name < result.Parameters[right].Name
		}
		return result.Parameters[left].Line < result.Parameters[right].Line
	})
	return result
}

func phpArrayConfigRoots(root *phpsyntax.Node) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	for _, array := range phpquery.Arrays(root) {
		if phpArrayAcceptedRoot(array) {
			result = append(result, array)
		}
	}
	return result
}

func parsePHPArrayParameters(
	path string,
	array *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
	resolver *php.NameResolver,
	result map[string]Parameter,
) {
	for _, item := range phpquery.ArrayItems(array) {
		name := phpArrayStaticValue(
			phpquery.ArrayItemKey(item),
			resolver,
			path,
		)
		if name == "" {
			continue
		}
		expression := phpquery.ArrayItemValue(item)
		value, static := phpArrayStaticValueOK(expression, resolver, path)
		if !static && expression != nil &&
			isStaticParameterExpression(expression.Text()) {
			value = strings.TrimSpace(expression.Text())
		}
		result[name] = Parameter{
			Name:  name,
			Value: value,
			Path:  path,
			Line:  phpArrayLine(lineIndex, item),
		}
	}
}

func parsePHPArrayServices(
	path string,
	array *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
	resolver *php.NameResolver,
) (map[string]Service, []ServicePrototype) {
	result := make(map[string]Service)
	if array == nil {
		return result, nil
	}
	defaultTags := phpArrayDefaultTags(array, resolver, path)
	defaultAutowire, defaultAutowireSet := phpArrayAutowire(
		phpArrayEntryArray(array, "_defaults", resolver, path),
		resolver,
		path,
	)
	instanceofTags := phpArrayInstanceofTags(array, resolver, path)
	var prototypes []ServicePrototype
	for _, item := range phpquery.ArrayItems(array) {
		idNode := phpquery.ArrayItemKey(item)
		id := phpArrayStaticValue(idNode, resolver, path)
		if id == "" || strings.HasPrefix(id, "_") {
			continue
		}
		value := phpquery.ArrayItemValue(item)
		if value == nil {
			continue
		}
		if definition := phpArrayNode(value); definition != nil {
			options := phpArrayOptions(definition, resolver, path)
			if resource, exists := options["resource"]; exists &&
				strings.HasSuffix(id, "\\") {
				prototypes = append(prototypes, phpArrayPrototype(
					path,
					id,
					idNode,
					resource,
					definition,
					lineIndex,
					resolver,
					defaultTags,
					instanceofTags,
					defaultAutowire,
					defaultAutowireSet,
				))
				continue
			}
			service := Service{
				ID:              id,
				Autowire:        defaultAutowire,
				AutowireSet:     defaultAutowireSet,
				Tags:            cloneStringMap(defaultTags),
				InstanceofTags:  cloneStringMap(instanceofTags),
				Path:            path,
				Line:            phpArrayLine(lineIndex, item),
				Range:           item.RangeTrimmedTrivia(),
				IDRange:         phpExpressionContentRange(idNode),
				DeprecatedRange: cst.TextRange{},
			}
			if autowire := phpArrayOptionValue(
				definition,
				"autowire",
				resolver,
				path,
			); autowire != nil {
				service.Autowire, _ = configuredServiceBool(
					phpArrayStaticValue(autowire, resolver, path),
				)
				service.AutowireSet = true
			}
			if class, exists := options["class"]; exists {
				service.Class = class
				service.ClassRange = phpExpressionContentRange(
					phpArrayOptionValue(definition, "class", resolver, path),
				)
			}
			if target, exists := options["alias"]; exists {
				service.AliasTarget = phpArrayNormalizeService(target)
			}
			if target, exists := options["decorates"]; exists {
				service.Decorates = phpArrayNormalizeService(target)
				service.DecoratesRange = phpExpressionContentRange(
					phpArrayOptionValue(
						definition,
						"decorates",
						resolver,
						path,
					),
				)
			}
			if parent, exists := options["parent"]; exists {
				service.Parent = phpArrayNormalizeService(parent)
				service.ParentRange = phpExpressionContentRange(
					phpArrayOptionValue(
						definition,
						"parent",
						resolver,
						path,
					),
				)
			}
			if deprecated, exists := options["deprecated"]; exists &&
				deprecated != "" && !strings.EqualFold(deprecated, "false") {
				service.Deprecated = true
				if !strings.EqualFold(deprecated, "true") {
					service.Deprecation = deprecated
				}
				service.DeprecatedRange = phpExpressionContentRange(
					phpArrayOptionValue(
						definition,
						"deprecated",
						resolver,
						path,
					),
				)
			}
			for tag, value := range phpArrayTags(
				phpArrayNode(phpArrayOptionValue(
					definition,
					"tags",
					resolver,
					path,
				)),
				resolver,
				path,
			) {
				if service.Tags == nil {
					service.Tags = make(map[string]string)
				}
				service.Tags[tag] = value
			}
			if service.Class == "" &&
				service.AliasTarget == "" &&
				!strings.EqualFold(options["abstract"], "true") &&
				strings.Contains(id, "\\") {
				service.Class = strings.TrimPrefix(id, "\\")
				service.ClassRange = service.IDRange
			}
			result[id] = service
			continue
		}

		target, static := phpArrayStaticValueOK(value, resolver, path)
		if !static {
			continue
		}
		target = phpArrayNormalizeService(target)
		if target == "" {
			continue
		}
		result[id] = Service{
			ID:          id,
			AliasTarget: target,
			Path:        path,
			Line:        phpArrayLine(lineIndex, item),
			Range:       item.RangeTrimmedTrivia(),
			IDRange:     phpExpressionContentRange(idNode),
		}
	}
	return result, prototypes
}

func phpArrayDefaultTags(
	array *phpsyntax.Node,
	resolver *php.NameResolver,
	path string,
) map[string]string {
	definition := phpArrayEntryArray(array, "_defaults", resolver, path)
	return phpArrayTags(
		phpArrayNode(phpArrayOptionValue(
			definition,
			"tags",
			resolver,
			path,
		)),
		resolver,
		path,
	)
}

func phpArrayInstanceofTags(
	array *phpsyntax.Node,
	resolver *php.NameResolver,
	path string,
) map[string]string {
	definitions := phpArrayEntryArray(array, "_instanceof", resolver, path)
	result := make(map[string]string)
	for _, item := range phpquery.ArrayItems(definitions) {
		className := phpArrayStaticValue(
			phpquery.ArrayItemKey(item),
			resolver,
			path,
		)
		config := phpArrayNode(phpquery.ArrayItemValue(item))
		if className == "" || config == nil {
			continue
		}
		for tag := range phpArrayTags(
			phpArrayNode(phpArrayOptionValue(
				config,
				"tags",
				resolver,
				path,
			)),
			resolver,
			path,
		) {
			result[tag] = strings.TrimPrefix(className, "\\")
		}
	}
	return result
}

func phpArrayPrototype(
	path,
	namespace string,
	namespaceNode *phpsyntax.Node,
	resource string,
	definition *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
	resolver *php.NameResolver,
	defaultTags,
	instanceofTags map[string]string,
	defaultAutowire bool,
	defaultAutowireSet bool,
) ServicePrototype {
	prototype := ServicePrototype{
		Namespace:      strings.TrimPrefix(namespace, "\\"),
		Resource:       phpArrayConfigPath(path, resource),
		Autowire:       defaultAutowire,
		AutowireSet:    defaultAutowireSet,
		Tags:           cloneStringMap(defaultTags),
		InstanceofTags: cloneStringMap(instanceofTags),
		Path:           path,
		Line:           phpArrayLine(lineIndex, definition),
		Range: cst.TextRange{
			Start: namespaceNode.RangeTrimmedTrivia().Start,
			End:   definition.RangeTrimmedTrivia().End,
		},
		NamespaceRange: phpExpressionContentRange(namespaceNode),
		ResourceRange: phpExpressionContentRange(
			phpArrayOptionValue(
				definition,
				"resource",
				resolver,
				path,
			),
		),
	}
	if autowire := phpArrayOptionValue(
		definition,
		"autowire",
		resolver,
		path,
	); autowire != nil {
		prototype.Autowire, _ = configuredServiceBool(
			phpArrayStaticValue(autowire, resolver, path),
		)
		prototype.AutowireSet = true
	}
	exclude := phpArrayOptionValue(definition, "exclude", resolver, path)
	if values := phpArrayNode(exclude); values != nil {
		for _, item := range phpquery.ArrayItems(values) {
			if value, found := phpArrayStaticValueOK(
				phpquery.ArrayItemValue(item),
				resolver,
				path,
			); found {
				prototype.Excludes = append(
					prototype.Excludes,
					phpArrayConfigPath(path, value),
				)
			}
		}
	} else if value, found := phpArrayStaticValueOK(
		exclude,
		resolver,
		path,
	); found {
		prototype.Excludes = []string{phpArrayConfigPath(path, value)}
	}
	for tag, value := range phpArrayTags(
		phpArrayNode(phpArrayOptionValue(
			definition,
			"tags",
			resolver,
			path,
		)),
		resolver,
		path,
	) {
		if prototype.Tags == nil {
			prototype.Tags = make(map[string]string)
		}
		prototype.Tags[tag] = value
	}
	return prototype
}

func phpArrayAutowire(
	definition *phpsyntax.Node,
	resolver *php.NameResolver,
	path string,
) (bool, bool) {
	if definition == nil {
		return false, false
	}
	value := phpArrayOptionValue(
		definition,
		"autowire",
		resolver,
		path,
	)
	if value == nil {
		return false, false
	}
	autowire, _ := configuredServiceBool(
		phpArrayStaticValue(value, resolver, path),
	)
	return autowire, true
}

func phpArrayTags(
	array *phpsyntax.Node,
	resolver *php.NameResolver,
	path string,
) map[string]string {
	result := make(map[string]string)
	for _, item := range phpquery.ArrayItems(array) {
		value := phpquery.ArrayItemValue(item)
		if config := phpArrayNode(value); config != nil {
			name := phpArrayStaticValue(
				phpArrayOptionValue(config, "name", resolver, path),
				resolver,
				path,
			)
			if name != "" {
				result[name] = ""
			}
			continue
		}
		name := phpArrayStaticValue(value, resolver, path)
		if name != "" {
			result[name] = ""
		}
	}
	return result
}

func phpArrayOptions(
	array *phpsyntax.Node,
	resolver *php.NameResolver,
	path string,
) map[string]string {
	result := make(map[string]string)
	for _, item := range phpquery.ArrayItems(array) {
		key := phpArrayStaticValue(
			phpquery.ArrayItemKey(item),
			resolver,
			path,
		)
		if key == "" {
			continue
		}
		if value, found := phpArrayStaticValueOK(
			phpquery.ArrayItemValue(item),
			resolver,
			path,
		); found {
			result[key] = value
		}
	}
	return result
}

func phpArrayOptionValue(
	array *phpsyntax.Node,
	name string,
	resolver *php.NameResolver,
	path string,
) *phpsyntax.Node {
	for _, item := range phpquery.ArrayItems(array) {
		if phpArrayStaticValue(
			phpquery.ArrayItemKey(item),
			resolver,
			path,
		) == name {
			return phpquery.ArrayItemValue(item)
		}
	}
	return nil
}

func phpArrayEntryArray(
	array *phpsyntax.Node,
	name string,
	resolver *php.NameResolver,
	path string,
) *phpsyntax.Node {
	return phpArrayNode(
		phpArrayOptionValue(array, name, resolver, path),
	)
}

func phpArrayNode(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil || node.Kind() != phpsyntax.PhpArray {
		return nil
	}
	return node
}

func phpArrayStaticValue(
	node *phpsyntax.Node,
	resolver *php.NameResolver,
	path string,
) string {
	value, _ := phpArrayStaticValueOK(node, resolver, path)
	return value
}

func phpArrayStaticValueOK(
	node *phpsyntax.Node,
	resolver *php.NameResolver,
	path string,
) (string, bool) {
	if node == nil {
		return "", false
	}
	return staticPHPValue(node.Text(), resolver, path)
}

func phpArrayNormalizeService(value string) string {
	if service, _, found := ParseServiceReference(value); found {
		return service
	}
	return strings.TrimSpace(value)
}

func phpArrayConfigPath(configPath, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(configPath), value))
}

func phpArrayLine(
	lineIndex *phpsyntax.LineIndex,
	node *phpsyntax.Node,
) int {
	if lineIndex == nil || node == nil {
		return 1
	}
	line, _ := lineIndex.PositionUTF16(node.Range().Start)
	return int(line) + 1
}

func mergePHPServiceConfigs(
	base,
	additional phpServiceConfig,
) phpServiceConfig {
	serviceByID := make(map[string]Service)
	for _, service := range additional.Services {
		serviceByID[service.ID] = service
	}
	for _, service := range base.Services {
		serviceByID[service.ID] = service
	}
	parameterByName := make(map[string]Parameter)
	for _, parameter := range additional.Parameters {
		parameterByName[parameter.Name] = parameter
	}
	for _, parameter := range base.Parameters {
		parameterByName[parameter.Name] = parameter
	}
	base.Services = base.Services[:0]
	for _, service := range serviceByID {
		base.Services = append(base.Services, service)
	}
	base.Parameters = base.Parameters[:0]
	for _, parameter := range parameterByName {
		base.Parameters = append(base.Parameters, parameter)
	}
	base.Prototypes = append(base.Prototypes, additional.Prototypes...)
	base.MethodReferences = append(
		base.MethodReferences,
		additional.MethodReferences...,
	)
	sort.Slice(base.Services, func(left, right int) bool {
		if base.Services[left].Line == base.Services[right].Line {
			return base.Services[left].ID < base.Services[right].ID
		}
		return base.Services[left].Line < base.Services[right].Line
	})
	sort.Slice(base.Parameters, func(left, right int) bool {
		if base.Parameters[left].Line == base.Parameters[right].Line {
			return base.Parameters[left].Name < base.Parameters[right].Name
		}
		return base.Parameters[left].Line < base.Parameters[right].Line
	})
	return base
}
