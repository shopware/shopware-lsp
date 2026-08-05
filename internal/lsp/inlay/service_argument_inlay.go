package inlay

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

type ServiceArgumentProvider struct {
	serviceIndex *symfony.ServiceIndex
	phpIndex     *php.PHPIndex
}

func NewServiceArgumentProvider(
	serviceIndex *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *ServiceArgumentProvider {
	return &ServiceArgumentProvider{
		serviceIndex: serviceIndex,
		phpIndex:     phpIndex,
	}
}

type configuredServiceDescriptor struct {
	className string
	alias     string
}

type serviceArgumentBinding struct {
	serviceID    string
	className    string
	methodName   string
	parameter    string
	index        int
	hintPosition uint32
}

func (p *ServiceArgumentProvider) GetInlayHints(
	ctx context.Context,
	request *lsp.InlayHintRequest,
) ([]protocol.InlayHint, error) {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil ||
		request == nil || request.InlayHintParams == nil ||
		request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil {
		return nil, nil
	}

	var (
		bindings []serviceArgumentBinding
		services map[string]configuredServiceDescriptor
	)
	switch request.Document.SyntaxLanguage {
	case language.XML:
		services, bindings = xmlServiceArgumentBindings(
			request.Document.SyntaxTree.Root,
		)
	case language.YAML:
		services, bindings = yamlServiceArgumentBindings(
			request.Document.SyntaxTree.Root,
		)
	default:
		return nil, nil
	}

	rangeStart := request.Document.LineIndex.OffsetUTF16(
		uint32(max(request.Range.Start.Line, 0)),
		uint32(max(request.Range.Start.Character, 0)),
	)
	rangeEnd := request.Document.LineIndex.OffsetUTF16(
		uint32(max(request.Range.End.Line, 0)),
		uint32(max(request.Range.End.Character, 0)),
	)
	if rangeEnd < rangeStart {
		rangeStart, rangeEnd = rangeEnd, rangeStart
	}

	var result []protocol.InlayHint
	for _, binding := range bindings {
		if ctx.Err() != nil {
			return result, nil
		}
		if binding.hintPosition < rangeStart ||
			binding.hintPosition > rangeEnd {
			continue
		}
		className, found, err := p.resolveServiceClass(
			binding.serviceID,
			binding.className,
			services,
		)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		methodName := binding.methodName
		if methodName == "" {
			methodName = "__construct"
		}
		methods := p.phpIndex.FindMethods(className, methodName)
		if len(methods) == 0 {
			continue
		}
		parameter, found := serviceBindingParameter(
			methods[0],
			binding,
		)
		if !found {
			continue
		}
		label := serviceParameterHintLabel(parameter)
		if label == "" {
			continue
		}
		line, character := request.Document.LineIndex.PositionUTF16(
			binding.hintPosition,
		)
		result = append(result, protocol.InlayHint{
			Position: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
			Label:       label,
			Kind:        protocol.InlayHintKindParameter,
			Tooltip:     serviceParameterHintTooltip(parameter),
			PaddingLeft: true,
		})
	}
	return result, nil
}

func (p *ServiceArgumentProvider) resolveServiceClass(
	serviceID,
	explicitClass string,
	local map[string]configuredServiceDescriptor,
) (string, bool, error) {
	className := staticServiceClassName(explicitClass)
	if className != "" {
		return className, true, nil
	}
	visited := make(map[string]struct{})
	current := serviceID
	for current != "" {
		key := strings.ToLower(current)
		if _, duplicate := visited[key]; duplicate {
			return "", false, nil
		}
		visited[key] = struct{}{}
		descriptor, found := local[key]
		if !found {
			break
		}
		if className := staticServiceClassName(
			descriptor.className,
		); className != "" {
			return className, true, nil
		}
		if descriptor.alias == "" {
			break
		}
		current = strings.TrimPrefix(descriptor.alias, "@")
	}
	if className := staticServiceClassName(current); className != "" &&
		strings.Contains(className, `\`) {
		return className, true, nil
	}
	if p.serviceIndex == nil {
		return "", false, nil
	}
	className, found, err := p.serviceIndex.ResolveServiceClassName(current)
	if err != nil || !found {
		return "", false, err
	}
	return className, true, nil
}

func serviceBindingParameter(
	method semantic.Symbol,
	binding serviceArgumentBinding,
) (semantic.Parameter, bool) {
	if binding.parameter != "" {
		wanted := strings.TrimPrefix(binding.parameter, "$")
		for _, parameter := range method.Parameters {
			if strings.EqualFold(
				strings.TrimPrefix(parameter.Name, "$"),
				wanted,
			) {
				return parameter, true
			}
		}
		return semantic.Parameter{}, false
	}
	if binding.index < 0 || binding.index >= len(method.Parameters) {
		return semantic.Parameter{}, false
	}
	return method.Parameters[binding.index], true
}

func serviceParameterHintLabel(parameter semantic.Parameter) string {
	if className := firstObjectTypeName(parameter.Type); className != "" {
		if separator := strings.LastIndex(className, `\`); separator >= 0 {
			return className[separator+1:]
		}
		return className
	}
	return strings.TrimPrefix(parameter.Name, "$")
}

func serviceParameterHintTooltip(parameter semantic.Parameter) string {
	name := strings.TrimPrefix(parameter.Name, "$")
	if name == "" {
		return parameter.Type.String()
	}
	return fmt.Sprintf("$%s: %s", name, parameter.Type.String())
}

func firstObjectTypeName(value types.Type) string {
	switch value.Kind() {
	case types.ObjectKind:
		return strings.Trim(value.Name(), "\\ ")
	case types.UnionKind, types.IntersectionKind:
		for _, member := range value.Arguments() {
			if name := firstObjectTypeName(member); name != "" {
				return name
			}
		}
	}
	return ""
}

func xmlServiceArgumentBindings(
	root *cst.Node,
) (map[string]configuredServiceDescriptor, []serviceArgumentBinding) {
	services := make(map[string]configuredServiceDescriptor)
	var result []serviceArgumentBinding
	for _, service := range xmlquery.Elements(root, "service") {
		serviceID := xmlquery.AttributeValue(
			xmlquery.Attribute(service, "id"),
		)
		className := xmlquery.AttributeValue(
			xmlquery.Attribute(service, "class"),
		)
		services[strings.ToLower(serviceID)] = configuredServiceDescriptor{
			className: className,
			alias: xmlquery.AttributeValue(
				xmlquery.Attribute(service, "alias"),
			),
		}
		if xmlquery.ChildElement(service, "factory") == nil &&
			xmlquery.Attribute(service, "factory") == nil {
			result = append(
				result,
				xmlArgumentBindings(
					serviceID,
					className,
					"",
					xmlquery.ChildElements(service, "argument"),
				)...,
			)
		}
		for _, call := range xmlquery.ChildElements(service, "call") {
			methodName := xmlquery.AttributeValue(
				xmlquery.Attribute(call, "method"),
			)
			if methodName == "" {
				continue
			}
			result = append(
				result,
				xmlArgumentBindings(
					serviceID,
					className,
					methodName,
					xmlquery.ChildElements(call, "argument"),
				)...,
			)
		}
	}
	for _, alias := range xmlquery.Elements(root, "alias") {
		serviceID := xmlquery.AttributeValue(
			xmlquery.Attribute(alias, "id"),
		)
		services[strings.ToLower(serviceID)] = configuredServiceDescriptor{
			alias: xmlquery.AttributeValue(
				xmlquery.Attribute(alias, "alias"),
			),
		}
	}
	return services, result
}

func xmlArgumentBindings(
	serviceID,
	className,
	methodName string,
	arguments []*cst.Node,
) []serviceArgumentBinding {
	result := make([]serviceArgumentBinding, 0, len(arguments))
	positionalIndex := 0
	for _, argument := range arguments {
		key := xmlquery.AttributeValue(xmlquery.Attribute(argument, "key"))
		binding := serviceArgumentBinding{
			serviceID:    serviceID,
			className:    className,
			methodName:   methodName,
			index:        positionalIndex,
			hintPosition: argument.RangeTrimmedTrivia().End,
		}
		switch {
		case strings.HasPrefix(key, "$"):
			binding.parameter = key
			binding.index = -1
		case numericServiceArgumentIndex(key) >= 0:
			binding.index = numericServiceArgumentIndex(key)
		default:
			positionalIndex++
		}
		result = append(result, binding)
	}
	return result
}

func yamlServiceArgumentBindings(
	root *cst.Node,
) (map[string]configuredServiceDescriptor, []serviceArgumentBinding) {
	services := make(map[string]configuredServiceDescriptor)
	rootValue := yamlquery.RootValue(root)
	serviceMapping := yamlquery.Property(rootValue, "services")
	if !yamlquery.IsMapping(serviceMapping) {
		return services, nil
	}
	var result []serviceArgumentBinding
	for _, servicePair := range yamlquery.Pairs(serviceMapping) {
		serviceID := yamlquery.ScalarValue(yamlquery.PairKey(servicePair))
		if serviceID == "" || strings.HasPrefix(serviceID, "_") {
			continue
		}
		config := yamlquery.PairValue(servicePair)
		descriptor := configuredServiceDescriptor{}
		if yamlquery.IsMapping(config) {
			descriptor.className = yamlquery.ScalarValue(
				yamlquery.Property(config, "class"),
			)
			descriptor.alias = yamlquery.ScalarValue(
				yamlquery.Property(config, "alias"),
			)
		} else {
			descriptor.alias = strings.TrimPrefix(
				yamlquery.ScalarValue(config),
				"@",
			)
		}
		services[strings.ToLower(serviceID)] = descriptor
		if !yamlquery.IsMapping(config) {
			continue
		}
		if yamlquery.Property(config, "factory") == nil {
			result = append(result, yamlArgumentBindings(
				serviceID,
				descriptor.className,
				"",
				yamlquery.Property(config, "arguments"),
			)...)
		}
		calls := yamlquery.Property(config, "calls")
		if !yamlquery.IsSequence(calls) {
			continue
		}
		for _, callItem := range yamlquery.Items(calls) {
			call := yamlquery.ItemValue(callItem)
			if !yamlquery.IsSequence(call) {
				continue
			}
			parts := yamlquery.Items(call)
			if len(parts) < 2 {
				continue
			}
			methodName := yamlquery.ScalarValue(
				yamlquery.ItemValue(parts[0]),
			)
			if methodName == "" {
				continue
			}
			result = append(result, yamlArgumentBindings(
				serviceID,
				descriptor.className,
				methodName,
				yamlquery.ItemValue(parts[1]),
			)...)
		}
	}
	return services, result
}

func yamlArgumentBindings(
	serviceID,
	className,
	methodName string,
	arguments *cst.Node,
) []serviceArgumentBinding {
	if arguments == nil {
		return nil
	}
	var result []serviceArgumentBinding
	switch {
	case yamlquery.IsSequence(arguments):
		for index, item := range yamlquery.Items(arguments) {
			value := yamlquery.ItemValue(item)
			if !yamlHintableArgument(value) {
				continue
			}
			result = append(result, serviceArgumentBinding{
				serviceID:    serviceID,
				className:    className,
				methodName:   methodName,
				index:        index,
				hintPosition: value.RangeTrimmedTrivia().End,
			})
		}
	case yamlquery.IsMapping(arguments):
		positionalIndex := 0
		for _, pair := range yamlquery.Pairs(arguments) {
			value := yamlquery.PairValue(pair)
			if !yamlHintableArgument(value) {
				continue
			}
			key := yamlquery.ScalarValue(yamlquery.PairKey(pair))
			binding := serviceArgumentBinding{
				serviceID:    serviceID,
				className:    className,
				methodName:   methodName,
				index:        positionalIndex,
				hintPosition: value.RangeTrimmedTrivia().End,
			}
			switch {
			case strings.HasPrefix(key, "$"):
				binding.parameter = key
				binding.index = -1
			case numericServiceArgumentIndex(key) >= 0:
				binding.index = numericServiceArgumentIndex(key)
			default:
				positionalIndex++
			}
			result = append(result, binding)
		}
	default:
		if yamlHintableArgument(arguments) {
			result = append(result, serviceArgumentBinding{
				serviceID:    serviceID,
				className:    className,
				methodName:   methodName,
				index:        0,
				hintPosition: arguments.RangeTrimmedTrivia().End,
			})
		}
	}
	return result
}

func yamlHintableArgument(node *cst.Node) bool {
	return node != nil &&
		(yamlquery.ScalarValue(node) != "" || yamlquery.IsNull(node))
}

func numericServiceArgumentIndex(value string) int {
	if value == "" {
		return -1
	}
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 {
		return -1
	}
	return index
}

func staticServiceClassName(value string) string {
	value = strings.Trim(value, "\\ ")
	if value == "" || strings.ContainsAny(value, "%${}") ||
		strings.ContainsAny(value, " \t\r\n") {
		return ""
	}
	return value
}

var _ lsp.InlayHintProvider = (*ServiceArgumentProvider)(nil)
