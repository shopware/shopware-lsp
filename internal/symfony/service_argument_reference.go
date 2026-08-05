package symfony

import (
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

// ServiceArgumentReference is a static service reference supplied to a
// configured service constructor or method call.
type ServiceArgumentReference struct {
	OwnerServiceID string
	OwnerClass     string
	ServiceID      string
	MethodName     string
	ParameterName  string
	ParameterIndex int
	Range          cst.TextRange
	Format         string
	Replacement    string
}

// ServiceMethodReference is a statically configured call, tag callback, or
// factory method on a service definition.
type ServiceMethodReference struct {
	OwnerServiceID  string
	OwnerClass      string
	ReceiverService string
	ReceiverClass   string
	MethodName      string
	Range           cst.TextRange
	Format          string
	Factory         bool
}

// Receiver returns the service/class on which the configured method is
// invoked. Ordinary post-construction calls use the owning service; factories
// carry their explicit receiver.
func (reference ServiceMethodReference) Receiver() (serviceID, className string) {
	if reference.ReceiverService != "" || reference.ReceiverClass != "" {
		return reference.ReceiverService, reference.ReceiverClass
	}
	return reference.OwnerServiceID, reference.OwnerClass
}

func YAMLServiceMethodReferences(root *cst.Node) []ServiceMethodReference {
	return yamlServiceMethodReferences(root, false)
}

// YAMLServiceMethodReferenceAt recognizes configured call, tag callback, and
// factory method values at a cursor position, including an empty scalar while
// it is being edited.
func YAMLServiceMethodReferenceAt(
	root *cst.Node,
	offset uint32,
) (ServiceMethodReference, bool) {
	for _, reference := range yamlServiceMethodReferences(root, true) {
		if serviceMethodRangeContains(reference.Range, offset) {
			return reference, true
		}
	}
	return ServiceMethodReference{}, false
}

func yamlServiceMethodReferences(
	root *cst.Node,
	includeEmpty bool,
) []ServiceMethodReference {
	var result []ServiceMethodReference
	for _, servicePair := range yamlServicePairs(root) {
		serviceID := yamlquery.ScalarValue(yamlquery.PairKey(servicePair))
		if serviceID == "" || strings.HasPrefix(serviceID, "_") {
			continue
		}
		config := yamlquery.PairValue(servicePair)
		if !yamlquery.IsMapping(config) {
			continue
		}
		base := ServiceMethodReference{
			OwnerServiceID: serviceID,
			OwnerClass: yamlServiceClassName(
				serviceID,
				config,
			),
			Format: "yaml",
		}
		calls := yamlquery.Property(config, "calls")
		if yamlquery.IsSequence(calls) {
			for _, callItem := range yamlquery.Items(calls) {
				call := yamlquery.ItemValue(callItem)
				if !yamlquery.IsSequence(call) {
					continue
				}
				parts := yamlquery.Items(call)
				if len(parts) == 0 {
					continue
				}
				result = appendYAMLServiceMethodItemReference(
					result,
					base,
					parts[0],
					includeEmpty,
				)
			}
		}
		result = appendYAMLTagMethodReferences(
			result,
			base,
			yamlquery.Property(config, "tags"),
			includeEmpty,
		)
		result = appendYAMLFactoryMethodReferences(
			result,
			base,
			config,
			includeEmpty,
		)
	}
	return result
}

func XMLServiceMethodReferences(root *cst.Node) []ServiceMethodReference {
	return xmlServiceMethodReferences(root, false)
}

// XMLServiceMethodReferenceAt recognizes <call method>, <tag method>,
// <factory method>, and legacy factory-method attributes at a cursor position.
func XMLServiceMethodReferenceAt(
	root *cst.Node,
	offset uint32,
) (ServiceMethodReference, bool) {
	for _, reference := range xmlServiceMethodReferences(root, true) {
		if serviceMethodRangeContains(reference.Range, offset) {
			return reference, true
		}
	}
	return ServiceMethodReference{}, false
}

func xmlServiceMethodReferences(
	root *cst.Node,
	includeEmpty bool,
) []ServiceMethodReference {
	var result []ServiceMethodReference
	for _, service := range xmlquery.Elements(root, "service") {
		ownerID := xmlquery.AttributeValue(xmlquery.Attribute(service, "id"))
		ownerClass := xmlquery.AttributeValue(
			xmlquery.Attribute(service, "class"),
		)
		if !staticYAMLServiceClassName(ownerClass) &&
			staticPHPClassName(ownerID) {
			ownerClass = ownerID
		}
		base := ServiceMethodReference{
			OwnerServiceID: ownerID,
			OwnerClass: strings.TrimPrefix(
				ownerClass,
				"\\",
			),
			Format: "xml",
		}
		for _, call := range xmlquery.ChildElements(service, "call") {
			method := xmlquery.Attribute(call, "method")
			result = appendXMLServiceMethodReference(
				result,
				base,
				method,
				includeEmpty,
			)
		}
		for _, tag := range xmlquery.ChildElements(service, "tag") {
			result = appendXMLServiceMethodReference(
				result,
				base,
				xmlquery.Attribute(tag, "method"),
				includeEmpty,
			)
		}
		if factory := xmlquery.ChildElement(service, "factory"); factory != nil {
			reference := base
			reference.Factory = true
			xmlFactoryReceiver(factory, &reference)
			result = appendXMLServiceMethodReference(
				result,
				reference,
				xmlquery.Attribute(factory, "method"),
				includeEmpty,
			)
		}
		if method := xmlquery.Attribute(service, "factory-method"); method != nil {
			reference := base
			reference.Factory = true
			if value := xmlquery.AttributeValue(
				xmlquery.Attribute(service, "factory-service"),
			); value != "" {
				reference.ReceiverService = strings.TrimPrefix(value, "@")
			}
			reference.ReceiverClass = strings.TrimPrefix(
				xmlquery.AttributeValue(
					xmlquery.Attribute(service, "factory-class"),
				),
				"\\",
			)
			result = appendXMLServiceMethodReference(
				result,
				reference,
				method,
				includeEmpty,
			)
		}
	}
	return result
}

func appendYAMLServiceMethodReference(
	result []ServiceMethodReference,
	base ServiceMethodReference,
	method *cst.Node,
	includeEmpty bool,
) []ServiceMethodReference {
	if method == nil || method.Kind() != yamlsyntax.YamlScalar {
		return result
	}
	base.MethodName = yamlquery.ScalarValue(method)
	if base.MethodName == "" && !includeEmpty {
		return result
	}
	base.Range = yamlScalarContentRange(method)
	return append(result, base)
}

func appendYAMLServiceMethodItemReference(
	result []ServiceMethodReference,
	base ServiceMethodReference,
	item *cst.Node,
	includeEmpty bool,
) []ServiceMethodReference {
	if item == nil {
		return result
	}
	if value := yamlquery.ItemValue(item); value != nil {
		return appendYAMLServiceMethodReference(
			result,
			base,
			value,
			includeEmpty,
		)
	}
	if !includeEmpty {
		return result
	}
	base.Range = item.RangeTrimmedTrivia()
	return append(result, base)
}

func appendYAMLTagMethodReferences(
	result []ServiceMethodReference,
	base ServiceMethodReference,
	tags *cst.Node,
	includeEmpty bool,
) []ServiceMethodReference {
	if tags == nil {
		return result
	}
	values := []*cst.Node{tags}
	if yamlquery.IsSequence(tags) {
		values = values[:0]
		for _, item := range yamlquery.Items(tags) {
			values = append(values, yamlquery.ItemValue(item))
		}
	}
	for _, value := range values {
		if !yamlquery.IsMapping(value) {
			continue
		}
		result = appendYAMLServiceMethodReference(
			result,
			base,
			yamlquery.Property(value, "method"),
			includeEmpty,
		)
	}
	return result
}

func appendYAMLFactoryMethodReferences(
	result []ServiceMethodReference,
	base ServiceMethodReference,
	config *cst.Node,
	includeEmpty bool,
) []ServiceMethodReference {
	factory := yamlquery.Property(config, "factory")
	if yamlquery.IsSequence(factory) {
		parts := yamlquery.Items(factory)
		if len(parts) >= 2 {
			reference := base
			reference.Factory = true
			yamlFactoryReceiver(
				yamlquery.ScalarValue(yamlquery.ItemValue(parts[0])),
				&reference,
			)
			result = appendYAMLServiceMethodItemReference(
				result,
				reference,
				parts[1],
				includeEmpty,
			)
		}
	} else if factory != nil && factory.Kind() == yamlsyntax.YamlScalar {
		if reference, found := yamlScalarFactoryMethodReference(
			base,
			factory,
			includeEmpty,
		); found {
			result = append(result, reference)
		}
	}

	method := yamlquery.Property(config, "factory_method")
	if method == nil {
		return result
	}
	reference := base
	reference.Factory = true
	yamlFactoryReceiver(
		yamlquery.ScalarValue(yamlquery.Property(config, "factory_service")),
		&reference,
	)
	if className := yamlquery.ScalarValue(
		yamlquery.Property(config, "factory_class"),
	); className != "" {
		reference.ReceiverClass = strings.TrimPrefix(className, "\\")
		reference.ReceiverService = ""
	}
	return appendYAMLServiceMethodReference(
		result,
		reference,
		method,
		includeEmpty,
	)
}

func yamlScalarFactoryMethodReference(
	base ServiceMethodReference,
	factory *cst.Node,
	includeEmpty bool,
) (ServiceMethodReference, bool) {
	value := yamlquery.ScalarValue(factory)
	separator := strings.LastIndex(value, "::")
	width := 2
	if separator < 0 {
		separator = strings.LastIndex(value, ":")
		width = 1
	}
	if separator < 1 {
		return ServiceMethodReference{}, false
	}
	method := value[separator+width:]
	if method == "" && !includeEmpty {
		return ServiceMethodReference{}, false
	}
	base.Factory = true
	yamlFactoryReceiver(value[:separator], &base)
	base.MethodName = method
	base.Range = yamlScalarContentRange(factory)
	base.Range.Start += uint32(separator + width)
	if base.Range.Start > base.Range.End {
		return ServiceMethodReference{}, false
	}
	return base, true
}

func yamlFactoryReceiver(
	value string,
	reference *ServiceMethodReference,
) {
	if reference == nil {
		return
	}
	value = strings.TrimSpace(value)
	if serviceID, _, found := ParseServiceReference(value); found {
		reference.ReceiverService = serviceID
		return
	}
	value = strings.TrimPrefix(value, "@")
	if staticPHPClassName(value) {
		reference.ReceiverClass = strings.TrimPrefix(value, "\\")
		return
	}
	reference.ReceiverService = value
}

func xmlFactoryReceiver(
	factory *cst.Node,
	reference *ServiceMethodReference,
) {
	if factory == nil || reference == nil {
		return
	}
	if serviceID := strings.TrimPrefix(
		xmlquery.AttributeValue(xmlquery.Attribute(factory, "service")),
		"@",
	); serviceID != "" {
		reference.ReceiverService = serviceID
	}
	if className := strings.TrimPrefix(
		xmlquery.AttributeValue(xmlquery.Attribute(factory, "class")),
		"\\",
	); className != "" {
		reference.ReceiverClass = className
		reference.ReceiverService = ""
	}
}

func appendXMLServiceMethodReference(
	result []ServiceMethodReference,
	base ServiceMethodReference,
	method *cst.Node,
	includeEmpty bool,
) []ServiceMethodReference {
	if method == nil {
		return result
	}
	base.MethodName = xmlquery.AttributeValue(method)
	if base.MethodName == "" && !includeEmpty {
		return result
	}
	base.Range = xmlAttributeContentRange(method)
	return append(result, base)
}

func serviceMethodRangeContains(rng cst.TextRange, offset uint32) bool {
	if rng.Start == rng.End {
		return offset == rng.Start
	}
	return offset >= rng.Start && offset <= rng.End
}

func YAMLServiceArgumentReferences(
	root *cst.Node,
) []ServiceArgumentReference {
	var result []ServiceArgumentReference
	for _, servicePair := range yamlServicePairs(root) {
		serviceID := yamlquery.ScalarValue(yamlquery.PairKey(servicePair))
		if serviceID == "" || strings.HasPrefix(serviceID, "_") {
			continue
		}
		config := yamlquery.PairValue(servicePair)
		if !yamlquery.IsMapping(config) {
			continue
		}
		base := ServiceArgumentReference{
			OwnerServiceID: serviceID,
			OwnerClass:     yamlServiceClassName(serviceID, config),
			ParameterIndex: -1,
			Format:         "yaml",
		}
		if yamlquery.Property(config, "factory") == nil {
			arguments := yamlquery.Property(config, "arguments")
			switch {
			case yamlquery.IsSequence(arguments):
				for index, item := range yamlquery.Items(arguments) {
					if reference, found := yamlServiceArgumentReference(
						yamlquery.ItemValue(item),
						base,
					); found {
						reference.ParameterIndex = index
						result = append(result, reference)
					}
				}
			case yamlquery.IsMapping(arguments):
				for index, pair := range yamlquery.Pairs(arguments) {
					reference, found := yamlServiceArgumentReference(
						yamlquery.PairValue(pair),
						base,
					)
					if !found {
						continue
					}
					key := yamlquery.ScalarValue(yamlquery.PairKey(pair))
					switch {
					case strings.HasPrefix(key, "$"):
						reference.ParameterName = key
					case numericIndex(key) >= 0:
						reference.ParameterIndex = numericIndex(key)
					default:
						reference.ParameterIndex = index
					}
					result = append(result, reference)
				}
			default:
				if reference, found := yamlServiceArgumentReference(
					arguments,
					base,
				); found {
					reference.ParameterIndex = 0
					result = append(result, reference)
				}
			}
		}
		result = append(result, yamlServiceCallReferences(config, base)...)
	}
	return result
}

func yamlServiceArgumentReference(
	value *cst.Node,
	base ServiceArgumentReference,
) (ServiceArgumentReference, bool) {
	if value == nil {
		return ServiceArgumentReference{}, false
	}
	serviceID, _, found := ParseServiceReference(
		yamlquery.ScalarValue(value),
	)
	if !found {
		return ServiceArgumentReference{}, false
	}
	base.ServiceID = serviceID
	base.Range = yamlScalarContentRange(value)
	return base, true
}

func yamlServiceCallReferences(
	config *cst.Node,
	base ServiceArgumentReference,
) []ServiceArgumentReference {
	calls := yamlquery.Property(config, "calls")
	if !yamlquery.IsSequence(calls) {
		return nil
	}
	var result []ServiceArgumentReference
	for _, callItem := range yamlquery.Items(calls) {
		call := yamlquery.ItemValue(callItem)
		if !yamlquery.IsSequence(call) {
			continue
		}
		parts := yamlquery.Items(call)
		if len(parts) < 2 {
			continue
		}
		methodName := yamlquery.ScalarValue(yamlquery.ItemValue(parts[0]))
		arguments := yamlquery.ItemValue(parts[1])
		if methodName == "" || !yamlquery.IsSequence(arguments) {
			continue
		}
		for index, argument := range yamlquery.Items(arguments) {
			reference, found := yamlServiceArgumentReference(
				yamlquery.ItemValue(argument),
				base,
			)
			if !found {
				continue
			}
			reference.MethodName = methodName
			reference.ParameterIndex = index
			result = append(result, reference)
		}
	}
	return result
}

func XMLServiceArgumentReferences(
	root *cst.Node,
) []ServiceArgumentReference {
	var result []ServiceArgumentReference
	for _, service := range xmlquery.Elements(root, "service") {
		ownerID := xmlquery.AttributeValue(xmlquery.Attribute(service, "id"))
		ownerClass := xmlquery.AttributeValue(
			xmlquery.Attribute(service, "class"),
		)
		if !staticYAMLServiceClassName(ownerClass) &&
			staticPHPClassName(ownerID) {
			ownerClass = ownerID
		}
		base := ServiceArgumentReference{
			OwnerServiceID: ownerID,
			OwnerClass:     strings.TrimPrefix(ownerClass, "\\"),
			ParameterIndex: -1,
			Format:         "xml",
		}
		if xmlquery.ChildElement(service, "factory") == nil {
			result = append(result, xmlServiceArgumentReferences(
				xmlquery.ChildElements(service, "argument"),
				base,
			)...)
		}
		for _, call := range xmlquery.ChildElements(service, "call") {
			callBase := base
			callBase.MethodName = xmlquery.AttributeValue(
				xmlquery.Attribute(call, "method"),
			)
			if callBase.MethodName == "" {
				continue
			}
			result = append(result, xmlServiceArgumentReferences(
				xmlquery.ChildElements(call, "argument"),
				callBase,
			)...)
		}
	}
	return result
}

func xmlServiceArgumentReferences(
	arguments []*cst.Node,
	base ServiceArgumentReference,
) []ServiceArgumentReference {
	var result []ServiceArgumentReference
	for index, argument := range arguments {
		if !strings.EqualFold(
			xmlquery.AttributeValue(xmlquery.Attribute(argument, "type")),
			"service",
		) {
			continue
		}
		idAttribute := xmlquery.Attribute(argument, "id")
		serviceID, found := xmlStaticServiceReference(
			xmlquery.AttributeValue(idAttribute),
		)
		if !found {
			continue
		}
		reference := base
		reference.ServiceID = serviceID
		reference.ParameterIndex = index
		reference.Range = xmlAttributeContentRange(idAttribute)
		key := xmlquery.AttributeValue(xmlquery.Attribute(argument, "key"))
		switch {
		case strings.HasPrefix(key, "$"):
			reference.ParameterName = key
			reference.ParameterIndex = -1
		case numericIndex(key) >= 0:
			reference.ParameterIndex = numericIndex(key)
		}
		result = append(result, reference)
	}
	return result
}

func xmlStaticServiceReference(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if serviceID, _, found := ParseServiceReference(value); found {
		return serviceID, true
	}
	value = strings.TrimPrefix(value, "@")
	return value, value != "" && !strings.ContainsAny(value, "%${}")
}

func numericIndex(value string) int {
	if value == "" {
		return -1
	}
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 {
		return -1
	}
	return index
}
