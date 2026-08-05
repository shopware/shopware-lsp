package doctrine

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

// TypeRegistration is a static DoctrineBundle custom DBAL type registration.
// NameRange and ClassRange retain both sides of the configuration mapping for
// navigation and future config diagnostics.
type TypeRegistration struct {
	Name       string
	Class      string
	File       string
	NameRange  cst.TextRange
	ClassRange cst.TextRange
}

type TypeRegistrationReferenceRole uint8

const (
	TypeRegistrationName TypeRegistrationReferenceRole = iota
	TypeRegistrationClass
)

type TypeRegistrationReference struct {
	Role                  TypeRegistrationReferenceRole
	Name                  string
	Class                 string
	Range                 cst.TextRange
	ClassConstant         bool
	ObjectCreation        bool
	ObjectCreationStarted bool
}

func TypeRegistrationsInDocument(
	path string,
	root *cst.Node,
) []TypeRegistration {
	if root == nil {
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return phpTypeRegistrations(path, root)
	case ".yaml", ".yml":
		return yamlTypeRegistrations(path, root)
	case ".xml":
		return xmlTypeRegistrations(path, root)
	default:
		return nil
	}
}

func TypeRegistrationReferenceAt(
	path string,
	root *cst.Node,
	offset uint32,
) (TypeRegistrationReference, bool) {
	if root == nil {
		return TypeRegistrationReference{}, false
	}
	for _, registration := range TypeRegistrationsInDocument(path, root) {
		if mappingRangeContainsCursor(registration.NameRange, offset) {
			return TypeRegistrationReference{
				Role:  TypeRegistrationName,
				Name:  registration.Name,
				Class: registration.Class,
				Range: registration.NameRange,
			}, true
		}
		if mappingRangeContainsCursor(registration.ClassRange, offset) {
			classConstant := false
			objectCreation := false
			if strings.EqualFold(filepath.Ext(path), ".php") {
				node := root.NodeAtOffset(offset)
				classConstant = phpquery.ClassConstantName(node) != ""
				objectCreation = phpTypeObjectCreationAt(node) != nil
			}
			return TypeRegistrationReference{
				Role:           TypeRegistrationClass,
				Name:           registration.Name,
				Class:          registration.Class,
				Range:          registration.ClassRange,
				ClassConstant:  classConstant,
				ObjectCreation: objectCreation,
			}, true
		}
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return phpTypeRegistrationReferenceAt(root, offset)
	case ".yaml", ".yml":
		return yamlTypeRegistrationReferenceAt(root, offset)
	case ".xml":
		return xmlTypeRegistrationReferenceAt(root, offset)
	default:
		return TypeRegistrationReference{}, false
	}
}

func phpTypeRegistrations(
	path string,
	root *phpsyntax.Node,
) []TypeRegistration {
	resolver := php.NewNameResolver(root)
	var result []TypeRegistration
	seen := make(map[string]struct{})
	for _, types := range phpDoctrineTypeArrays(root) {
		for _, item := range phpquery.ArrayItems(types) {
			nameNode := phpquery.ArrayItemKey(item)
			name, found := phpTypeConfigString(nameNode)
			if !found || name == "" {
				continue
			}
			class, classRange, _ := phpTypeRegistrationClass(
				phpquery.ArrayItemValue(item),
				resolver,
			)
			if class == "" {
				continue
			}
			nameRange := phpquery.StringContentRange(nameNode)
			key := strings.ToLower(name) + "|" +
				strings.ToLower(class) + "|" +
				nameRange.String()
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, TypeRegistration{
				Name:       name,
				Class:      class,
				File:       path,
				NameRange:  nameRange,
				ClassRange: classRange,
			})
		}
	}
	for _, call := range phpRuntimeTypeRegistrationCalls(root, resolver) {
		nameNode := phpquery.ArgumentExpression(call.Node, 0)
		name, found := phpTypeConfigString(nameNode)
		if !found || name == "" {
			continue
		}
		class, classRange, _, _ := phpRuntimeTypeRegistrationClass(
			phpquery.ArgumentExpression(call.Node, 1),
			resolver,
			call.ObjectCreation,
		)
		if class == "" {
			continue
		}
		nameRange := phpquery.StringContentRange(nameNode)
		key := strings.ToLower(name) + "|" +
			strings.ToLower(class) + "|" +
			nameRange.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, TypeRegistration{
			Name:       name,
			Class:      class,
			File:       path,
			NameRange:  nameRange,
			ClassRange: classRange,
		})
	}
	return result
}

type phpRuntimeTypeRegistrationCall struct {
	Node           *phpsyntax.Node
	ObjectCreation bool
}

func phpRuntimeTypeRegistrationCalls(
	root *phpsyntax.Node,
	resolver *php.NameResolver,
) []phpRuntimeTypeRegistrationCall {
	var result []phpRuntimeTypeRegistrationCall
	for _, call := range phpquery.Calls(root) {
		switch strings.ToLower(phpquery.CallMethodName(call)) {
		case "addtype", "overridetype":
			if phpDBALTypeStaticCall(call, resolver) {
				result = append(result, phpRuntimeTypeRegistrationCall{
					Node: call,
				})
			}
		case "register":
			receiver := phpquery.CallReceiver(call)
			if receiver != nil &&
				strings.EqualFold(
					phpquery.CallMethodName(receiver),
					"getTypeRegistry",
				) &&
				phpDBALTypeStaticCall(receiver, resolver) {
				result = append(result, phpRuntimeTypeRegistrationCall{
					Node:           call,
					ObjectCreation: true,
				})
			}
		}
	}
	return result
}

func phpDBALTypeStaticCall(
	call *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return false
	}
	name := phpquery.NameValue(receiver)
	return name != "" && strings.EqualFold(
		normalizeClass(resolver.Resolve(name)),
		"Doctrine\\DBAL\\Types\\Type",
	)
}

func phpDoctrineTypeArrays(
	root *phpsyntax.Node,
) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	seen := make(map[cst.TextRange]struct{})
	for _, call := range phpquery.Calls(root) {
		if !strings.EqualFold(
			phpquery.CallMethodName(call),
			"extension",
		) {
			continue
		}
		extension, found := phpTypeConfigString(
			phpquery.ArgumentExpression(call, 0),
		)
		if !found || !strings.EqualFold(extension, "doctrine") {
			continue
		}
		config := phpquery.ArrayAt(
			phpquery.ArgumentExpression(call, 1),
		)
		dbal := phpTypeConfigArrayProperty(config, "dbal")
		types := phpTypeConfigArrayProperty(
			phpquery.ArrayAt(dbal),
			"types",
		)
		array := phpquery.ArrayAt(types)
		if array == nil {
			continue
		}
		rng := array.Range()
		if _, duplicate := seen[rng]; duplicate {
			continue
		}
		seen[rng] = struct{}{}
		result = append(result, array)
	}
	return result
}

func phpTypeConfigArrayProperty(
	array *phpsyntax.Node,
	name string,
) *phpsyntax.Node {
	if array == nil {
		return nil
	}
	for _, item := range phpquery.ArrayItems(array) {
		key, found := phpTypeConfigString(
			phpquery.ArrayItemKey(item),
		)
		if found && strings.EqualFold(key, name) {
			return phpquery.ArrayItemValue(item)
		}
	}
	return nil
}

func phpTypeConfigString(
	node *phpsyntax.Node,
) (string, bool) {
	if node == nil {
		return "", false
	}
	literal := phpquery.StringAt(node)
	if literal == nil ||
		literal.RangeTrimmedTrivia() != node.RangeTrimmedTrivia() {
		return "", false
	}
	text := strings.TrimSpace(literal.Text())
	if len(text) < 2 {
		return "", false
	}
	quote := text[0]
	if (quote != '\'' && quote != '"') ||
		text[len(text)-1] != quote {
		return "", false
	}
	value := phpquery.StringValue(literal)
	if quote == '"' && strings.Contains(value, "$") {
		return "", false
	}
	value = strings.ReplaceAll(value, `\\`, `\`)
	if quote == '\'' {
		value = strings.ReplaceAll(value, `\'`, `'`)
	} else {
		value = strings.ReplaceAll(value, `\"`, `"`)
	}
	return value, true
}

func phpTypeRegistrationClass(
	value *phpsyntax.Node,
	resolver *php.NameResolver,
) (string, cst.TextRange, bool) {
	if value == nil {
		return "", cst.TextRange{}, false
	}
	if array := phpquery.ArrayAt(value); array != nil &&
		array.RangeTrimmedTrivia() == value.RangeTrimmedTrivia() {
		return phpTypeRegistrationClass(
			phpTypeConfigArrayProperty(array, "class"),
			resolver,
		)
	}
	if name := phpquery.ClassConstantName(value); name != "" {
		rng := value.RangeTrimmedTrivia()
		if nameNode := phpquery.DirectChild(
			value,
			phpsyntax.PhpName,
		); nameNode != nil {
			rng = nameNode.RangeTrimmedTrivia()
		}
		return normalizeClass(resolver.Resolve(name)), rng, true
	}
	if class, found := phpTypeConfigString(value); found {
		return normalizeClass(class),
			phpquery.StringContentRange(value),
			false
	}
	return "", value.RangeTrimmedTrivia(), false
}

func phpTypeRegistrationReferenceAt(
	root *phpsyntax.Node,
	offset uint32,
) (TypeRegistrationReference, bool) {
	resolver := php.NewNameResolver(root)
	for _, types := range phpDoctrineTypeArrays(root) {
		typesRange := types.Range()
		if offset < typesRange.Start || offset > typesRange.End {
			continue
		}
		items := phpquery.ArrayItems(types)
		for index, item := range items {
			itemRange := item.Range()
			itemEnd := typesRange.End
			if index+1 < len(items) {
				itemEnd = items[index+1].Range().Start
			}
			if offset < itemRange.Start || offset > itemEnd {
				continue
			}
			nameNode := phpquery.ArrayItemKey(item)
			name, _ := phpTypeConfigString(nameNode)
			nameRange := phpquery.StringContentRange(nameNode)
			value := phpquery.ArrayItemValue(item)
			if value != nil && nameNode != nil &&
				value.Range() == nameNode.Range() &&
				phpArrayItemHasArrow(item) {
				value = nil
			}
			class, classRange, classConstant := phpTypeRegistrationClass(
				value,
				resolver,
			)
			if mappingRangeContainsCursor(nameRange, offset) {
				return TypeRegistrationReference{
					Role:          TypeRegistrationName,
					Name:          name,
					Class:         class,
					Range:         nameRange,
					ClassConstant: classConstant,
				}, true
			}
			if array := phpquery.ArrayAt(value); array != nil &&
				array.RangeTrimmedTrivia() ==
					value.RangeTrimmedTrivia() {
				if reference, found := phpTypeClassOptionReferenceAt(
					array,
					name,
					resolver,
					offset,
				); found {
					return reference, true
				}
				continue
			}
			if mappingRangeContainsCursor(classRange, offset) ||
				(value == nil &&
					phpCursorAfterArrayArrow(item, offset)) {
				if value == nil {
					classRange = cst.TextRange{
						Start: offset,
						End:   offset,
					}
					classConstant = true
				}
				return TypeRegistrationReference{
					Role:          TypeRegistrationClass,
					Name:          name,
					Class:         class,
					Range:         classRange,
					ClassConstant: classConstant,
				}, true
			}
		}
	}
	return phpRuntimeTypeRegistrationReferenceAt(
		root,
		resolver,
		offset,
	)
}

func phpRuntimeTypeRegistrationReferenceAt(
	root *phpsyntax.Node,
	resolver *php.NameResolver,
	offset uint32,
) (TypeRegistrationReference, bool) {
	for _, runtimeCall := range phpRuntimeTypeRegistrationCalls(
		root,
		resolver,
	) {
		call := runtimeCall.Node
		nameNode := phpquery.ArgumentExpression(call, 0)
		name, _ := phpTypeConfigString(nameNode)
		nameRange := phpquery.StringContentRange(nameNode)
		value := phpquery.ArgumentExpression(call, 1)
		class, classRange, classConstant, objectCreation :=
			phpRuntimeTypeRegistrationClass(
				value,
				resolver,
				runtimeCall.ObjectCreation,
			)
		if mappingRangeContainsCursor(nameRange, offset) {
			return TypeRegistrationReference{
				Role:           TypeRegistrationName,
				Name:           name,
				Class:          class,
				Range:          nameRange,
				ClassConstant:  classConstant,
				ObjectCreation: objectCreation,
			}, true
		}
		valueRange := cst.TextRange{}
		if value != nil {
			valueRange = value.RangeTrimmedTrivia()
		}
		missingValue := value == nil &&
			phpCursorAfterCallArgumentSeparator(call, offset)
		incompleteValue := class == "" && value != nil &&
			phpCursorInIncompleteCallValue(call, value, offset)
		if mappingRangeContainsCursor(classRange, offset) ||
			mappingRangeContainsCursor(valueRange, offset) ||
			missingValue || incompleteValue {
			if !hasMappingRange(classRange) {
				classRange = cst.TextRange{
					Start: offset,
					End:   offset,
				}
			}
			if missingValue {
				classConstant = !runtimeCall.ObjectCreation
				objectCreation = runtimeCall.ObjectCreation
			}
			if incompleteValue && runtimeCall.ObjectCreation {
				objectCreation = true
			}
			return TypeRegistrationReference{
				Role:           TypeRegistrationClass,
				Name:           name,
				Class:          class,
				Range:          classRange,
				ClassConstant:  classConstant,
				ObjectCreation: objectCreation,
				ObjectCreationStarted: objectCreation &&
					value != nil,
			}, true
		}
	}
	return TypeRegistrationReference{}, false
}

func phpCursorInIncompleteCallValue(
	call,
	value *phpsyntax.Node,
	offset uint32,
) bool {
	if call == nil || value == nil || offset < value.Range().Start {
		return false
	}
	end := call.Range().End
	text := call.Text()
	if closing := strings.LastIndexByte(text, ')'); closing >= 0 {
		end = call.Range().Start + uint32(closing)
	}
	return offset <= end
}

func phpRuntimeTypeRegistrationClass(
	value *phpsyntax.Node,
	resolver *php.NameResolver,
	objectExpected bool,
) (string, cst.TextRange, bool, bool) {
	if objectExpected {
		if value == nil ||
			value.Kind() != phpsyntax.PhpObjectCreation {
			rng := cst.TextRange{}
			if value != nil {
				rng = value.RangeTrimmedTrivia()
			}
			return "", rng, false, true
		}
		object := value
		name := phpquery.ObjectClassName(object)
		rng := object.RangeTrimmedTrivia()
		if nameNode := phpquery.DirectChild(
			object,
			phpsyntax.PhpName,
		); nameNode != nil {
			rng = nameNode.RangeTrimmedTrivia()
		}
		return normalizeClass(resolver.Resolve(name)),
			rng,
			false,
			true
	}
	class, rng, classConstant := phpTypeRegistrationClass(
		value,
		resolver,
	)
	return class, rng, classConstant, false
}

func phpTypeObjectCreationAt(
	node *phpsyntax.Node,
) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == phpsyntax.PhpObjectCreation {
			return current
		}
	}
	return nil
}

func phpCursorAfterCallArgumentSeparator(
	call *phpsyntax.Node,
	offset uint32,
) bool {
	first := phpquery.Argument(call, 0)
	if call == nil || first == nil {
		return false
	}
	callRange := call.Range()
	firstRange := first.Range()
	if firstRange.End < callRange.Start ||
		firstRange.End > callRange.End {
		return false
	}
	text := call.Text()
	from := int(firstRange.End - callRange.Start)
	if from < 0 || from > len(text) {
		return false
	}
	separator := strings.IndexByte(text[from:], ',')
	if separator < 0 {
		return false
	}
	start := firstRange.End + uint32(separator+1)
	end := callRange.End
	if closing := strings.LastIndexByte(text, ')'); closing >= 0 {
		end = callRange.Start + uint32(closing)
	}
	return offset >= start && offset <= end
}

func phpTypeClassOptionReferenceAt(
	array *phpsyntax.Node,
	name string,
	resolver *php.NameResolver,
	offset uint32,
) (TypeRegistrationReference, bool) {
	items := phpquery.ArrayItems(array)
	for index, option := range items {
		key, found := phpTypeConfigString(
			phpquery.ArrayItemKey(option),
		)
		if !found || !strings.EqualFold(key, "class") {
			continue
		}
		optionRange := option.Range()
		optionEnd := array.Range().End
		if index+1 < len(items) {
			optionEnd = items[index+1].Range().Start
		}
		if offset < optionRange.Start || offset > optionEnd {
			continue
		}
		keyNode := phpquery.ArrayItemKey(option)
		value := phpquery.ArrayItemValue(option)
		if value != nil && keyNode != nil &&
			value.Range() == keyNode.Range() &&
			phpArrayItemHasArrow(option) {
			value = nil
		}
		class, rng, classConstant := phpTypeRegistrationClass(
			value,
			resolver,
		)
		if value == nil && phpCursorAfterArrayArrow(option, offset) {
			rng = cst.TextRange{Start: offset, End: offset}
			classConstant = true
		} else if !mappingRangeContainsCursor(rng, offset) {
			return TypeRegistrationReference{}, false
		}
		return TypeRegistrationReference{
			Role:          TypeRegistrationClass,
			Name:          name,
			Class:         class,
			Range:         rng,
			ClassConstant: classConstant,
		}, true
	}
	return TypeRegistrationReference{}, false
}

func phpArrayItemHasArrow(item *phpsyntax.Node) bool {
	return item != nil && strings.Contains(item.Text(), "=>")
}

func phpCursorAfterArrayArrow(
	item *phpsyntax.Node,
	offset uint32,
) bool {
	if item == nil {
		return false
	}
	index := strings.Index(item.Text(), "=>")
	if index < 0 {
		return false
	}
	arrowEnd := item.Range().Start + uint32(index+2)
	return offset >= arrowEnd
}

func yamlTypeRegistrations(
	path string,
	root *yamlsyntax.Node,
) []TypeRegistration {
	var result []TypeRegistration
	seen := make(map[string]struct{})
	for _, mapping := range yamlquery.Nodes(
		root,
		yamlsyntax.YamlMapping,
		yamlsyntax.YamlFlowMapping,
	) {
		doctrine := yamlquery.Property(mapping, "doctrine")
		dbal := yamlquery.Property(doctrine, "dbal")
		types := yamlquery.Property(dbal, "types")
		if !yamlquery.IsMapping(types) {
			continue
		}
		for _, pair := range yamlquery.Pairs(types) {
			nameNode := yamlquery.PairKey(pair)
			value := yamlquery.PairValue(pair)
			classNode := value
			if yamlquery.IsMapping(value) {
				classNode = yamlquery.Property(value, "class")
			}
			name := strings.TrimSpace(yamlquery.ScalarValue(nameNode))
			class := normalizeClass(yamlquery.ScalarValue(classNode))
			if name == "" || class == "" {
				continue
			}
			key := strings.ToLower(name) + "|" +
				strings.ToLower(class) + "|" +
				yamlScalarRange(nameNode).String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, TypeRegistration{
				Name:       name,
				Class:      class,
				File:       path,
				NameRange:  yamlScalarRange(nameNode),
				ClassRange: yamlScalarRange(classNode),
			})
		}
	}
	return result
}

func yamlTypeRegistrationReferenceAt(
	root *yamlsyntax.Node,
	offset uint32,
) (TypeRegistrationReference, bool) {
	node := root.NodeAtOffset(offset)
	pair := yamlquery.AncestorPair(node)
	if pair == nil {
		return TypeRegistrationReference{}, false
	}
	path := yamlquery.PairPath(pair)
	typesAt := typeRegistrationPathIndex(path)
	if typesAt < 0 || typesAt+1 >= len(path) {
		return TypeRegistrationReference{}, false
	}
	name := path[typesAt+1]
	role := TypeRegistrationClass
	if len(path) > typesAt+2 &&
		!strings.EqualFold(path[typesAt+2], "class") {
		return TypeRegistrationReference{}, false
	}
	key := yamlquery.PairKey(pair)
	value := yamlquery.PairValue(pair)
	if len(path) == typesAt+2 &&
		mappingRangeContainsCursor(yamlScalarRange(key), offset) {
		role = TypeRegistrationName
	}
	classNode := value
	if len(path) == typesAt+3 {
		classNode = value
	} else if yamlquery.IsMapping(value) {
		classNode = yamlquery.Property(value, "class")
	}
	class := normalizeClass(yamlquery.ScalarValue(classNode))
	rng := yamlScalarRange(classNode)
	if role == TypeRegistrationName {
		rng = yamlScalarRange(key)
	}
	if rng.Start == 0 && rng.End == 0 {
		rng = cst.TextRange{Start: offset, End: offset}
	}
	return TypeRegistrationReference{
		Role:  role,
		Name:  name,
		Class: class,
		Range: rng,
	}, true
}

func typeRegistrationPathIndex(path []string) int {
	for index := 0; index+2 < len(path); index++ {
		if strings.EqualFold(path[index], "doctrine") &&
			strings.EqualFold(path[index+1], "dbal") &&
			strings.EqualFold(path[index+2], "types") {
			return index + 2
		}
	}
	return -1
}

func xmlTypeRegistrations(
	path string,
	root *xmlsyntax.Node,
) []TypeRegistration {
	var result []TypeRegistration
	for _, element := range xmlquery.Elements(root) {
		if !strings.EqualFold(xmlLocalName(element), "type") ||
			!xmlHasAncestor(element, "dbal") ||
			!xmlHasAncestor(element, "config") {
			continue
		}
		nameAttribute := xmlquery.Attribute(element, "name")
		classAttribute := xmlquery.Attribute(element, "class")
		name := strings.TrimSpace(xmlquery.AttributeValue(nameAttribute))
		class := normalizeClass(xmlquery.AttributeValue(classAttribute))
		classRange := xmlValueRange(classAttribute)
		if class == "" {
			class, classRange = xmlRegistrationText(element)
			class = normalizeClass(class)
		}
		if name == "" || class == "" {
			continue
		}
		result = append(result, TypeRegistration{
			Name:       name,
			Class:      class,
			File:       path,
			NameRange:  xmlValueRange(nameAttribute),
			ClassRange: classRange,
		})
	}
	return result
}

func xmlTypeRegistrationReferenceAt(
	root *xmlsyntax.Node,
	offset uint32,
) (TypeRegistrationReference, bool) {
	node := root.NodeAtOffset(offset)
	element := xmlquery.ElementAt(node)
	if element == nil ||
		!strings.EqualFold(xmlLocalName(element), "type") ||
		!xmlHasAncestor(element, "dbal") ||
		!xmlHasAncestor(element, "config") {
		return TypeRegistrationReference{}, false
	}
	nameAttribute := xmlquery.Attribute(element, "name")
	classAttribute := xmlquery.Attribute(element, "class")
	name := strings.TrimSpace(xmlquery.AttributeValue(nameAttribute))
	class := normalizeClass(xmlquery.AttributeValue(classAttribute))
	if attribute := xmlquery.AttributeAt(node); attribute != nil {
		switch strings.ToLower(xmlquery.AttributeName(attribute)) {
		case "name":
			return TypeRegistrationReference{
				Role:  TypeRegistrationName,
				Name:  name,
				Class: class,
				Range: xmlValueRange(attribute),
			}, true
		case "class":
			rng := xmlValueRange(attribute)
			if rng.Start == 0 && rng.End == 0 {
				rng = cst.TextRange{Start: offset, End: offset}
			}
			return TypeRegistrationReference{
				Role:  TypeRegistrationClass,
				Name:  name,
				Class: class,
				Range: rng,
			}, true
		}
	}
	if classAttribute == nil {
		value, rng := xmlRegistrationText(element)
		if rng.Start == 0 && rng.End == 0 {
			rng = cst.TextRange{Start: offset, End: offset}
		}
		return TypeRegistrationReference{
			Role:  TypeRegistrationClass,
			Name:  name,
			Class: normalizeClass(value),
			Range: rng,
		}, true
	}
	return TypeRegistrationReference{}, false
}

func xmlLocalName(element *xmlsyntax.Node) string {
	name := xmlquery.ElementName(element)
	if separator := strings.LastIndexByte(name, ':'); separator >= 0 {
		return name[separator+1:]
	}
	return name
}

func xmlHasAncestor(element *xmlsyntax.Node, name string) bool {
	for parent := xmlquery.ParentElement(element); parent != nil; parent = xmlquery.ParentElement(parent) {
		if strings.EqualFold(xmlLocalName(parent), name) {
			return true
		}
	}
	return false
}

func xmlRegistrationText(
	element *xmlsyntax.Node,
) (string, cst.TextRange) {
	for _, text := range xmlquery.Nodes(element, xmlsyntax.XmlText) {
		value := strings.TrimSpace(text.Text())
		if value == "" {
			continue
		}
		rng := text.Range()
		raw := text.Text()
		leading := len(raw) - len(strings.TrimLeft(raw, " \t\r\n"))
		trailing := len(raw) - len(strings.TrimRight(raw, " \t\r\n"))
		rng.Start += uint32(leading)
		rng.End -= uint32(trailing)
		return value, rng
	}
	return "", cst.TextRange{}
}
