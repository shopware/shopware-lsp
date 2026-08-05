package form

import (
	"bytes"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

func isPHPFormCandidate(content []byte) bool {
	return bytes.Contains(content, []byte("AbstractType")) ||
		bytes.Contains(content, []byte("FormTypeInterface")) ||
		bytes.Contains(content, []byte("FormTypeExtensionInterface")) ||
		bytes.Contains(content, []byte("buildForm")) ||
		bytes.Contains(content, []byte("buildView")) ||
		bytes.Contains(content, []byte("finishView")) ||
		bytes.Contains(content, []byte("configureOptions")) ||
		bytes.Contains(content, []byte("setDefaultOptions")) ||
		bytes.Contains(content, []byte("form.type"))
}

func parsePHP(file *indexer.ParsedFile) []Type {
	root := file.SyntaxTree().Root
	return parsePHPRoot(file.Path, root)
}

func parsePHPRoot(path string, root *phpsyntax.Node) []Type {
	if root == nil {
		return nil
	}
	nameResolver := php.NewNameResolver(root)
	var result []Type
	for _, class := range phpquery.Classes(root) {
		if phpquery.IsInterface(class) || phpquery.IsTrait(class) {
			continue
		}
		className := normalizePHPName(
			nameResolver.Resolve(phpquery.ClassName(class)),
		)
		if className == "" {
			continue
		}
		record := Type{
			Class:     className,
			Abstract:  phpquery.IsAbstract(class),
			File:      path,
			Range:     class.RangeTrimmedTrivia(),
			NameRange: phpClassNameRange(class),
		}
		directForm, directExtension := classFormKinds(class, nameResolver)
		record.FormType = directForm
		record.Extension = directExtension
		parseFormIdentity(&record, class, nameResolver)
		parseFormOptions(&record, class, nameResolver)
		parseFormFields(&record, class, nameResolver)
		parseFormViewVars(&record, class, nameResolver)
		if directForm || directExtension ||
			len(record.Options) != 0 || len(record.Fields) != 0 ||
			len(record.ViewVars) != 0 ||
			record.Parent != "" || record.DataClass != "" ||
			len(record.Aliases) != 0 || len(record.ExtendedTypes) != 0 {
			result = append(result, record)
		}
	}
	result = append(result, parsePHPRegistrations(
		path,
		root,
		nameResolver,
	)...)
	return result
}

// TypeInDocument extracts the current unsaved contribution for className.
// LSP providers use it to overlay editor contents on the persistent workspace
// index without publishing an incomplete document to other requests.
func TypeInDocument(
	path string,
	root *phpsyntax.Node,
	className string,
) (Type, bool) {
	className = normalizePHPName(className)
	for _, current := range parsePHPRoot(path, root) {
		if strings.EqualFold(current.Class, className) {
			return current, true
		}
	}
	return Type{}, false
}

func TypesInDocument(path string, root *phpsyntax.Node) []Type {
	return parsePHPRoot(path, root)
}

func classFormKinds(
	class *phpsyntax.Node,
	nameResolver *php.NameResolver,
) (bool, bool) {
	formType := false
	extension := false
	for _, name := range append(
		phpquery.ClassExtends(class),
		phpquery.ClassImplements(class)...,
	) {
		resolved := normalizePHPName(nameResolver.Resolve(name))
		switch {
		case strings.EqualFold(resolved, formTypeInterface),
			strings.EqualFold(resolved, abstractTypeClass):
			formType = true
		case strings.EqualFold(resolved, formTypeExtensionInterface):
			extension = true
		}
	}
	return formType, extension
}

func parseFormIdentity(
	record *Type,
	class *phpsyntax.Node,
	nameResolver *php.NameResolver,
) {
	for _, method := range phpquery.Methods(class) {
		switch strings.ToLower(phpquery.MethodName(method)) {
		case "getname", "getblockprefix":
			for _, expression := range methodReturnExpressions(method) {
				if expression.Kind() != phpsyntax.PhpString {
					continue
				}
				if alias := phpquery.StringValue(expression); alias != "" {
					record.Aliases = appendUniqueFold(record.Aliases, alias)
				}
			}
		case "getparent":
			for _, expression := range methodReturnExpressions(method) {
				if parent := formTypeExpression(
					expression,
					nameResolver,
					true,
				); parent != "" {
					record.Parent = parent
					break
				}
			}
		case "getextendedtype", "getextendedtypes":
			for _, expression := range methodReturnExpressions(method) {
				record.ExtendedTypes = appendUniqueFold(
					record.ExtendedTypes,
					formTypeExpressions(expression, nameResolver)...,
				)
			}
			for _, yield := range phpquery.Nodes(
				method,
				phpsyntax.PhpYieldExpression,
			) {
				if phpquery.FunctionLikeAt(yield) != method {
					continue
				}
				record.ExtendedTypes = appendUniqueFold(
					record.ExtendedTypes,
					formTypeExpressions(yield, nameResolver)...,
				)
			}
			if len(record.ExtendedTypes) != 0 {
				record.Extension = true
			}
		}
	}
}

func parseFormOptions(
	record *Type,
	class *phpsyntax.Node,
	nameResolver *php.NameResolver,
) {
	for _, method := range phpquery.Methods(class) {
		methodName := strings.ToLower(phpquery.MethodName(method))
		if methodName != "configureoptions" &&
			methodName != "setdefaultoptions" {
			continue
		}
		for _, call := range phpquery.Calls(method) {
			if phpquery.FunctionLikeAt(call) != method {
				continue
			}
			switch strings.ToLower(phpquery.CallMethodName(call)) {
			case "setdefaults":
				array := phpquery.ArrayAt(
					phpquery.ArgumentExpression(call, 0),
				)
				for _, item := range phpquery.ArrayItems(array) {
					key := phpquery.ArrayItemKey(item)
					name := phpStringValue(key)
					if name == "" {
						continue
					}
					value := phpquery.ArrayItemValue(item)
					option := Option{
						Name:    name,
						Kind:    DefaultOption,
						Default: expressionText(value),
						Class:   record.Class,
						File:    record.File,
						Range:   key.RangeTrimmedTrivia(),
					}
					record.Options = append(record.Options, option)
					if name == "data_class" {
						record.DataClass = classValue(
							value,
							nameResolver,
						)
						record.DataClassRange = nodeRange(value)
					}
				}
			case "setdefault":
				key := phpquery.ArgumentExpression(call, 0)
				name := phpStringValue(key)
				if name == "" {
					continue
				}
				value := phpquery.ArgumentExpression(call, 1)
				record.Options = append(record.Options, Option{
					Name:    name,
					Kind:    DefaultOption,
					Default: expressionText(value),
					Class:   record.Class,
					File:    record.File,
					Range:   key.RangeTrimmedTrivia(),
				})
				if name == "data_class" {
					record.DataClass = classValue(value, nameResolver)
					record.DataClassRange = nodeRange(value)
				}
			case "setrequired", "setoptional", "setdefined":
				kind := DefinedOption
				if strings.EqualFold(
					phpquery.CallMethodName(call),
					"setRequired",
				) {
					kind = RequiredOption
				}
				for _, value := range optionNames(
					phpquery.ArgumentExpression(call, 0),
				) {
					record.Options = append(record.Options, Option{
						Name:  value.name,
						Kind:  kind,
						Class: record.Class,
						File:  record.File,
						Range: value.node.RangeTrimmedTrivia(),
					})
				}
			case "setallowedtypes", "addallowedtypes":
				appendOptionConstraint(
					record,
					call,
					AllowedTypesOption,
					true,
				)
			case "setallowedvalues", "addallowedvalues":
				appendOptionConstraint(
					record,
					call,
					AllowedValuesOption,
					false,
				)
			}
		}
	}
}

func appendOptionConstraint(
	record *Type,
	call *phpsyntax.Node,
	kind OptionKind,
	typesOnly bool,
) {
	key := phpquery.ArgumentExpression(call, 0)
	name := phpStringValue(key)
	if name == "" {
		return
	}
	option := Option{
		Name:  name,
		Kind:  kind,
		Class: record.Class,
		File:  record.File,
		Range: key.RangeTrimmedTrivia(),
	}
	if typesOnly {
		for _, value := range optionNames(
			phpquery.ArgumentExpression(call, 1),
		) {
			option.AllowedTypes = appendUniqueFold(
				option.AllowedTypes,
				value.name,
			)
		}
	}
	record.Options = append(record.Options, option)
}

func parseFormFields(
	record *Type,
	class *phpsyntax.Node,
	nameResolver *php.NameResolver,
) {
	for _, method := range phpquery.Methods(class) {
		if !strings.EqualFold(phpquery.MethodName(method), "buildForm") {
			continue
		}
		builderVariables := make(map[string]struct{})
		for _, parameter := range phpquery.Parameters(method) {
			resolved := normalizePHPName(nameResolver.Resolve(
				phpquery.ParameterType(parameter),
			))
			if strings.EqualFold(
				resolved,
				"Symfony\\Component\\Form\\FormBuilderInterface",
			) {
				builderVariables[strings.TrimPrefix(
					phpquery.ParameterName(parameter),
					"$",
				)] = struct{}{}
			}
		}
		for _, call := range phpquery.Calls(method) {
			if phpquery.FunctionLikeAt(call) != method {
				continue
			}
			callName := strings.ToLower(phpquery.CallMethodName(call))
			if callName != "add" && callName != "create" {
				continue
			}
			receiver := phpquery.CallReceiver(call)
			if receiver == nil {
				continue
			}
			if len(builderVariables) != 0 {
				name := phpquery.VariableName(receiver)
				if _, exists := builderVariables[name]; !exists {
					receiverText := strings.TrimSpace(receiver.Text())
					matched := false
					for builder := range builderVariables {
						if strings.HasPrefix(
							receiverText,
							"$"+builder,
						) {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}
			}
			nameNode := phpquery.ArgumentExpression(call, 0)
			name := phpStringValue(nameNode)
			if name == "" {
				continue
			}
			typeNode := phpquery.ArgumentExpression(call, 1)
			options := phpquery.ArrayAt(
				phpquery.ArgumentExpression(call, 2),
			)
			field := Field{
				Name:      name,
				Type:      formTypeExpression(typeNode, nameResolver, true),
				Mapped:    true,
				Class:     record.Class,
				File:      record.File,
				Range:     nameNode.RangeTrimmedTrivia(),
				TypeRange: nodeRange(typeNode),
			}
			if mapped := phpArrayProperty(options, "mapped"); mapped != nil &&
				strings.EqualFold(strings.TrimSpace(mapped.Text()), "false") {
				field.Mapped = false
			}
			if propertyPath := phpArrayProperty(
				options,
				"property_path",
			); propertyPath != nil {
				field.PropertyPath = phpStringValue(propertyPath)
			}
			record.Fields = append(record.Fields, field)
		}
	}
}

func parseFormViewVars(
	record *Type,
	class *phpsyntax.Node,
	nameResolver *php.NameResolver,
) {
	for _, method := range phpquery.Methods(class) {
		methodName := strings.ToLower(phpquery.MethodName(method))
		if methodName != "buildview" && methodName != "finishview" {
			continue
		}
		viewVariables := formViewParameterNames(method, nameResolver)
		if len(viewVariables) == 0 {
			continue
		}
		for _, access := range phpquery.Nodes(
			method,
			phpsyntax.PhpArrayAccess,
		) {
			if phpquery.FunctionLikeAt(access) != method {
				continue
			}
			member, key := formViewArrayAccess(access, viewVariables)
			if member == nil || key == nil {
				continue
			}
			value := formViewAssignmentValue(access)
			appendFormViewVar(record, key, value, nameResolver)
		}
		for _, assignment := range phpquery.Nodes(
			method,
			phpsyntax.PhpAssignmentExpression,
		) {
			if phpquery.FunctionLikeAt(assignment) != method {
				continue
			}
			left, right := directPHPNodePair(assignment)
			if !isFormViewVarsMember(left, viewVariables) {
				continue
			}
			appendFormViewVarsFromArray(
				record,
				phpquery.ArrayAt(right),
				nameResolver,
			)
		}
		for _, call := range phpquery.Calls(method) {
			if phpquery.FunctionLikeAt(call) != method ||
				!strings.EqualFold(
					strings.TrimPrefix(phpquery.CallMethodName(call), `\`),
					"array_replace",
				) ||
				!containsFormViewVarsMember(
					phpquery.ArgumentExpression(call, 0),
					viewVariables,
				) {
				continue
			}
			for index := 1; index < len(phpquery.Arguments(call)); index++ {
				appendFormViewVarsFromArray(
					record,
					phpquery.ArrayAt(
						phpquery.ArgumentExpression(call, index),
					),
					nameResolver,
				)
			}
		}
	}
}

func formViewParameterNames(
	method *phpsyntax.Node,
	nameResolver *php.NameResolver,
) map[string]struct{} {
	result := make(map[string]struct{})
	parameters := phpquery.Parameters(method)
	for index, parameter := range parameters {
		name := strings.TrimPrefix(phpquery.ParameterName(parameter), "$")
		if name == "" {
			continue
		}
		resolved := normalizePHPName(nameResolver.Resolve(
			phpquery.ParameterType(parameter),
		))
		if index == 0 || strings.EqualFold(
			resolved,
			"Symfony\\Component\\Form\\FormView",
		) {
			result[name] = struct{}{}
		}
	}
	return result
}

func formViewArrayAccess(
	access *phpsyntax.Node,
	viewVariables map[string]struct{},
) (*phpsyntax.Node, *phpsyntax.Node) {
	children := directPHPNodes(access)
	if len(children) < 2 ||
		!isFormViewVarsMember(children[0], viewVariables) ||
		children[1].Kind() != phpsyntax.PhpString {
		return nil, nil
	}
	return children[0], children[1]
}

func isFormViewVarsMember(
	node *phpsyntax.Node,
	viewVariables map[string]struct{},
) bool {
	if node == nil || node.Kind() != phpsyntax.PhpMemberAccess {
		return false
	}
	children := directPHPNodes(node)
	if len(children) < 2 ||
		children[0].Kind() != phpsyntax.PhpVariable ||
		children[len(children)-1].Kind() != phpsyntax.PhpName ||
		!strings.EqualFold(
			phpquery.NameValue(children[len(children)-1]),
			"vars",
		) {
		return false
	}
	_, found := viewVariables[phpquery.VariableName(children[0])]
	return found
}

func containsFormViewVarsMember(
	node *phpsyntax.Node,
	viewVariables map[string]struct{},
) bool {
	if isFormViewVarsMember(node, viewVariables) {
		return true
	}
	for _, member := range phpquery.Nodes(node, phpsyntax.PhpMemberAccess) {
		if isFormViewVarsMember(member, viewVariables) {
			return true
		}
	}
	return false
}

func formViewAssignmentValue(
	node *phpsyntax.Node,
) *phpsyntax.Node {
	for current := node.Parent(); current != nil; current = current.Parent() {
		if current.Kind() == phpsyntax.PhpAssignmentExpression {
			left, right := directPHPNodePair(current)
			if left == node {
				return right
			}
			return nil
		}
		if current.Kind() == phpsyntax.PhpMethodDeclaration ||
			current.Kind() == phpsyntax.PhpFunctionDeclaration ||
			current.Kind() == phpsyntax.PhpClosure ||
			current.Kind() == phpsyntax.PhpArrowFunction {
			break
		}
	}
	return nil
}

func appendFormViewVarsFromArray(
	record *Type,
	array *phpsyntax.Node,
	nameResolver *php.NameResolver,
) {
	if array == nil {
		return
	}
	for _, item := range phpquery.ArrayItems(array) {
		key := phpquery.ArrayItemKey(item)
		if key == nil || key.Kind() != phpsyntax.PhpString {
			continue
		}
		appendFormViewVar(
			record,
			key,
			phpquery.ArrayItemValue(item),
			nameResolver,
		)
	}
}

func appendFormViewVar(
	record *Type,
	key,
	value *phpsyntax.Node,
	nameResolver *php.NameResolver,
) {
	name := phpStringValue(key)
	if name == "" {
		return
	}
	record.ViewVars = append(record.ViewVars, ViewVar{
		Name:  name,
		Type:  formViewValueType(value, nameResolver),
		Value: expressionText(value),
		Class: record.Class,
		File:  record.File,
		Range: key.RangeTrimmedTrivia(),
	})
}

func formViewValueType(
	value *phpsyntax.Node,
	nameResolver *php.NameResolver,
) string {
	if value == nil {
		return "mixed"
	}
	switch value.Kind() {
	case phpsyntax.PhpString:
		return "string"
	case phpsyntax.PhpNumber:
		if strings.ContainsAny(value.Text(), ".eE") {
			return "float"
		}
		return "int"
	case phpsyntax.PhpBoolean:
		return "bool"
	case phpsyntax.PhpNull:
		return "null"
	case phpsyntax.PhpArray:
		return "array"
	case phpsyntax.PhpObjectCreation:
		name := nameResolver.Resolve(phpquery.ObjectClassName(value))
		if name != "" {
			return normalizePHPName(name)
		}
	}
	return "mixed"
}

func directPHPNodes(node *phpsyntax.Node) []*phpsyntax.Node {
	if node == nil {
		return nil
	}
	var result []*phpsyntax.Node
	for child := range node.ChildNodes() {
		result = append(result, child)
	}
	return result
}

func directPHPNodePair(
	node *phpsyntax.Node,
) (*phpsyntax.Node, *phpsyntax.Node) {
	children := directPHPNodes(node)
	if len(children) < 2 {
		return nil, nil
	}
	return children[0], children[1]
}

func parsePHPRegistrations(
	path string,
	root *phpsyntax.Node,
	nameResolver *php.NameResolver,
) []Type {
	var result []Type
	for _, call := range phpquery.Calls(root) {
		if !strings.EqualFold(phpquery.CallMethodName(call), "tag") {
			continue
		}
		tag := phpStringValue(phpquery.ArgumentExpression(call, 0))
		if tag != "form.type" && tag != "form.type_extension" {
			continue
		}
		statement := ancestorOfKind(call, phpsyntax.PhpExpressionStatement)
		className := ""
		for _, candidate := range phpquery.Nodes(
			statement,
			phpsyntax.PhpScopedAccess,
		) {
			if raw := phpquery.ClassConstantName(candidate); raw != "" {
				className = normalizePHPName(nameResolver.Resolve(raw))
				break
			}
		}
		if className == "" {
			continue
		}
		options := phpquery.ArrayAt(phpquery.ArgumentExpression(call, 1))
		record := Type{
			Class:     className,
			Extension: tag == "form.type_extension",
			File:      path,
			Range:     call.RangeTrimmedTrivia(),
		}
		if alias := phpStringValue(
			phpArrayProperty(options, "alias"),
		); alias != "" {
			record.Aliases = []string{alias}
		}
		if target := classValue(
			phpArrayProperty(options, "extended_type"),
			nameResolver,
		); target != "" {
			record.ExtendedTypes = []string{target}
		}
		result = append(result, record)
	}
	return result
}

func parseYAML(file *indexer.ParsedFile) []Type {
	root := yamlquery.RootValue(file.SyntaxTree().Root)
	services := yamlquery.Property(root, "services")
	if !yamlquery.IsMapping(services) {
		return nil
	}
	var result []Type
	for _, servicePair := range yamlquery.Pairs(services) {
		serviceID := yamlquery.ScalarValue(yamlquery.PairKey(servicePair))
		config := yamlquery.PairValue(servicePair)
		if !yamlquery.IsMapping(config) {
			continue
		}
		className := yamlquery.ScalarValue(yamlquery.Property(config, "class"))
		if className == "" {
			className = serviceID
		}
		if className == "" || strings.HasPrefix(className, "_") {
			continue
		}
		tags := yamlquery.Property(config, "tags")
		for _, tag := range yamlTagValues(tags) {
			name := yamlquery.ScalarValue(yamlquery.Property(tag, "name"))
			if tag.Kind() == yamlsyntax.YamlScalar {
				name = yamlquery.ScalarValue(tag)
			}
			if name != "form.type" && name != "form.type_extension" {
				continue
			}
			record := Type{
				Class:     normalizePHPName(className),
				Extension: name == "form.type_extension",
				File:      file.Path,
				Range:     tag.RangeTrimmedTrivia(),
			}
			if alias := yamlquery.ScalarValue(
				yamlquery.Property(tag, "alias"),
			); alias != "" {
				record.Aliases = []string{alias}
			}
			if target := yamlquery.ScalarValue(
				yamlquery.Property(tag, "extended_type"),
			); target != "" {
				record.ExtendedTypes = []string{
					normalizePHPName(target),
				}
			}
			result = append(result, record)
		}
	}
	return result
}

func yamlTagValues(node *yamlsyntax.Node) []*yamlsyntax.Node {
	if yamlquery.IsSequence(node) {
		var result []*yamlsyntax.Node
		for _, item := range yamlquery.Items(node) {
			if value := yamlquery.ItemValue(item); value != nil {
				result = append(result, value)
			}
		}
		return result
	}
	if node == nil {
		return nil
	}
	return []*yamlsyntax.Node{node}
}

func parseXML(file *indexer.ParsedFile) []Type {
	var result []Type
	for _, service := range xmlquery.Elements(
		file.SyntaxTree().Root,
		"service",
	) {
		attributes := xmlquery.AttributeValues(service)
		className := attributes["class"]
		if className == "" {
			className = attributes["id"]
		}
		for _, tag := range xmlquery.ChildElements(service, "tag") {
			tagAttributes := xmlquery.AttributeValues(tag)
			name := tagAttributes["name"]
			if name != "form.type" && name != "form.type_extension" {
				continue
			}
			record := Type{
				Class:     normalizePHPName(className),
				Extension: name == "form.type_extension",
				File:      file.Path,
				Range:     tag.RangeTrimmedTrivia(),
			}
			if alias := tagAttributes["alias"]; alias != "" {
				record.Aliases = []string{alias}
			}
			if target := tagAttributes["extended-type"]; target != "" {
				record.ExtendedTypes = []string{
					normalizePHPName(target),
				}
			}
			result = append(result, record)
		}
	}
	return result
}

func methodReturnExpressions(method *phpsyntax.Node) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	for _, statement := range phpquery.Nodes(
		method,
		phpsyntax.PhpReturnStatement,
	) {
		if phpquery.FunctionLikeAt(statement) != method {
			continue
		}
		for child := range statement.ChildNodes() {
			result = append(result, child)
			break
		}
	}
	return result
}

func formTypeExpressions(
	node *phpsyntax.Node,
	nameResolver *php.NameResolver,
) []string {
	if node == nil {
		return nil
	}
	if node.Kind() == phpsyntax.PhpArray {
		var result []string
		for _, item := range phpquery.ArrayItems(node) {
			if value := formTypeExpression(
				phpquery.ArrayItemValue(item),
				nameResolver,
				true,
			); value != "" {
				result = append(result, value)
			}
		}
		return result
	}
	if value := formTypeExpression(node, nameResolver, true); value != "" {
		return []string{value}
	}
	for _, candidate := range phpquery.Nodes(
		node,
		phpsyntax.PhpScopedAccess,
		phpsyntax.PhpString,
	) {
		if value := formTypeExpression(
			candidate,
			nameResolver,
			true,
		); value != "" {
			return []string{value}
		}
	}
	return nil
}

func formTypeExpression(
	node *phpsyntax.Node,
	nameResolver *php.NameResolver,
	allowAlias bool,
) string {
	if node == nil {
		return ""
	}
	if node.Kind() == phpsyntax.PhpString {
		value := phpquery.StringValue(node)
		if allowAlias && !strings.Contains(value, `\`) {
			return value
		}
		return normalizePHPName(nameResolver.Resolve(value))
	}
	if raw := phpquery.ClassConstantName(node); raw != "" {
		return normalizePHPName(nameResolver.Resolve(raw))
	}
	if object := firstObject(node); object != nil {
		return normalizePHPName(nameResolver.Resolve(
			phpquery.ObjectClassName(object),
		))
	}
	if strings.EqualFold(strings.TrimSpace(node.Text()), "null") {
		return coreFormTypeClass
	}
	return ""
}

func classValue(
	node *phpsyntax.Node,
	nameResolver *php.NameResolver,
) string {
	if node == nil {
		return ""
	}
	if node.Kind() == phpsyntax.PhpString {
		return normalizePHPName(nameResolver.Resolve(
			phpquery.StringValue(node),
		))
	}
	return formTypeExpression(node, nameResolver, false)
}

func firstObject(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	if node.Kind() == phpsyntax.PhpObjectCreation {
		return node
	}
	objects := phpquery.ObjectCreations(node)
	if len(objects) == 0 {
		return nil
	}
	return objects[0]
}

type optionName struct {
	name string
	node *phpsyntax.Node
}

func optionNames(node *phpsyntax.Node) []optionName {
	if node == nil {
		return nil
	}
	if node.Kind() == phpsyntax.PhpString {
		return []optionName{{
			name: phpquery.StringValue(node),
			node: node,
		}}
	}
	if node.Kind() != phpsyntax.PhpArray {
		return nil
	}
	var result []optionName
	for _, item := range phpquery.ArrayItems(node) {
		value := phpquery.ArrayItemValue(item)
		if name := phpStringValue(value); name != "" {
			result = append(result, optionName{name: name, node: value})
		}
	}
	return result
}

func phpArrayProperty(
	array *phpsyntax.Node,
	name string,
) *phpsyntax.Node {
	if array == nil {
		return nil
	}
	for _, item := range phpquery.ArrayItems(array) {
		if phpStringValue(phpquery.ArrayItemKey(item)) == name {
			return phpquery.ArrayItemValue(item)
		}
	}
	return nil
}

func phpStringValue(node *phpsyntax.Node) string {
	if node == nil || node.Kind() != phpsyntax.PhpString {
		return ""
	}
	return phpquery.StringValue(node)
}

func expressionText(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	return strings.TrimSpace(node.Text())
}

func nodeRange(node *phpsyntax.Node) phpsyntax.TextRange {
	if node == nil {
		return phpsyntax.TextRange{}
	}
	return node.RangeTrimmedTrivia()
}

func phpClassNameRange(class *phpsyntax.Node) phpsyntax.TextRange {
	if class == nil {
		return phpsyntax.TextRange{}
	}
	name := phpquery.DirectChild(class, phpsyntax.PhpName)
	if name == nil {
		return class.RangeTrimmedTrivia()
	}
	return name.RangeTrimmedTrivia()
}

func ancestorOfKind(
	node *phpsyntax.Node,
	kind phpsyntax.Kind,
) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}
