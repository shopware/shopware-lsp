package twigcomponent

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

const (
	asTwigComponent  = "Symfony\\UX\\TwigComponent\\Attribute\\AsTwigComponent"
	asLiveComponent  = "Symfony\\UX\\LiveComponent\\Attribute\\AsLiveComponent"
	exposeInTemplate = "Symfony\\UX\\TwigComponent\\Attribute\\ExposeInTemplate"
	liveProp         = "Symfony\\UX\\LiveComponent\\Attribute\\LiveProp"
	liveAction       = "Symfony\\UX\\LiveComponent\\Attribute\\LiveAction"
	liveArg          = "Symfony\\UX\\LiveComponent\\Attribute\\LiveArg"
	liveListener     = "Symfony\\UX\\LiveComponent\\Attribute\\LiveListener"
)

func declarationsInPHP(
	path string,
	root *phpsyntax.Node,
) []Declaration {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []Declaration
	for _, class := range phpquery.Classes(root) {
		if phpquery.IsInterface(class) || phpquery.IsTrait(class) {
			continue
		}
		className := normalizeClass(
			resolver.Resolve(phpquery.ClassName(class)),
		)
		if className == "" {
			continue
		}
		for _, attribute := range phpquery.Attributes(class) {
			resolved := normalizeClass(
				resolver.Resolve(phpquery.AttributeName(attribute)),
			)
			if !strings.EqualFold(resolved, asTwigComponent) &&
				!strings.EqualFold(resolved, asLiveComponent) {
				continue
			}
			live := strings.EqualFold(resolved, asLiveComponent)
			name, nameRange := phpAttributeStringValue(
				attribute,
				"name",
				0,
			)
			template, templateRange := phpAttributeStringValue(
				attribute,
				"template",
				1,
			)
			exposePublicProps := true
			if value, found := phpAttributeArgument(
				attribute,
				"exposePublicProps",
				2,
			); found {
				exposePublicProps = !strings.EqualFold(
					strings.TrimSpace(value.Text()),
					"false",
				)
			}
			result = append(result, Declaration{
				Name:              normalizeComponentName(name),
				Class:             className,
				Template:          normalizeTemplate(template),
				File:              path,
				NameRange:         nameRange,
				ClassRange:        phpClassNameRange(class),
				TemplateRange:     templateRange,
				Source:            AttributeSource,
				ExposePublicProps: exposePublicProps,
				Live:              live,
			})
		}
	}
	return result
}

func propsInPHP(
	path string,
	root *phpsyntax.Node,
) []Prop {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []Prop
	for _, class := range phpquery.Classes(root) {
		if phpquery.IsInterface(class) || phpquery.IsTrait(class) {
			continue
		}
		className := normalizeClass(
			resolver.Resolve(phpquery.ClassName(class)),
		)
		if className == "" {
			continue
		}
		component, _ := componentClassKinds(class, resolver)
		if !component {
			continue
		}
		for _, property := range phpquery.Properties(class) {
			expose := resolvedAttribute(
				property,
				resolver,
				exposeInTemplate,
			)
			liveAttribute := resolvedAttribute(
				property,
				resolver,
				liveProp,
			)
			if expose == nil && liveAttribute == nil {
				continue
			}
			for _, variable := range phpquery.PropertyVariables(property) {
				name := phpquery.VariableName(variable)
				if name == "" {
					continue
				}
				member := name
				propType := phpquery.PropertyType(property)
				if expose != nil {
					if custom, _ := phpAttributeStringValue(
						expose,
						"name",
						0,
					); custom != "" {
						name = custom
					}
					if getter, _ := phpAttributeStringValue(
						expose,
						"getter",
						1,
					); getter != "" {
						for _, method := range phpquery.Methods(class) {
							if strings.EqualFold(
								phpquery.MethodName(method),
								getter,
							) {
								propType = phpquery.MethodReturnType(
									method,
								)
								break
							}
						}
					}
				}
				result = append(result, Prop{
					Name:     name,
					Type:     propType,
					File:     path,
					Class:    className,
					Member:   member,
					Range:    variable.RangeTrimmedTrivia(),
					Live:     liveAttribute != nil,
					Writable: livePropWritable(liveAttribute),
				})
			}
		}
		for _, method := range phpquery.Methods(class) {
			expose := resolvedAttribute(
				method,
				resolver,
				exposeInTemplate,
			)
			if expose == nil {
				continue
			}
			name := exposedMethodName(phpquery.MethodName(method))
			if custom, _ := phpAttributeStringValue(
				expose,
				"name",
				0,
			); custom != "" {
				name = custom
			}
			if name == "" {
				continue
			}
			result = append(result, Prop{
				Name:   name,
				Type:   phpquery.MethodReturnType(method),
				File:   path,
				Class:  className,
				Member: phpquery.MethodName(method),
				Range:  phpMethodNameRange(method),
			})
		}
	}
	return uniqueProps(result)
}

func liveActionsInPHP(
	path string,
	root *phpsyntax.Node,
) []LiveAction {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []LiveAction
	for _, class := range phpquery.Classes(root) {
		className := normalizeClass(
			resolver.Resolve(phpquery.ClassName(class)),
		)
		if className == "" {
			continue
		}
		for _, method := range phpquery.Methods(class) {
			if resolvedAttribute(method, resolver, liveAction) == nil {
				continue
			}
			action := LiveAction{
				Name:   phpquery.MethodName(method),
				Class:  className,
				Method: phpquery.MethodName(method),
				File:   path,
				Range:  phpMethodNameRange(method),
			}
			for _, parameter := range phpquery.Parameters(method) {
				phpName := strings.TrimPrefix(
					phpquery.ParameterName(parameter),
					"$",
				)
				if phpName == "" {
					continue
				}
				name := phpName
				attribute := resolvedAttribute(
					parameter,
					resolver,
					liveArg,
				)
				if attribute != nil {
					if alias, _ := phpAttributeStringValue(
						attribute,
						"name",
						0,
					); alias != "" {
						name = alias
					}
				}
				action.Parameters = append(
					action.Parameters,
					LiveActionParameter{
						Name:    name,
						PHPName: phpName,
						Type:    phpquery.ParameterType(parameter),
						Optional: phpquery.ParameterOptional(parameter) ||
							phpquery.ParameterVariadic(parameter),
						LiveArg: attribute != nil,
						Range:   parameter.RangeTrimmedTrivia(),
					},
				)
			}
			if action.Name != "" {
				result = append(result, action)
			}
		}
	}
	return result
}

func liveListenersInPHP(
	path string,
	root *phpsyntax.Node,
) []LiveListener {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []LiveListener
	for _, class := range phpquery.Classes(root) {
		className := normalizeClass(
			resolver.Resolve(phpquery.ClassName(class)),
		)
		if className == "" {
			continue
		}
		for _, method := range phpquery.Methods(class) {
			var names []struct {
				name string
				rng  cst.TextRange
			}
			for _, attribute := range phpquery.Attributes(method) {
				resolved := normalizeClass(
					resolver.Resolve(phpquery.AttributeName(attribute)),
				)
				if !strings.EqualFold(resolved, liveListener) {
					continue
				}
				name, rng := phpAttributeStringValue(
					attribute,
					"event",
					0,
				)
				name = strings.TrimSpace(name)
				if name != "" {
					names = append(names, struct {
						name string
						rng  cst.TextRange
					}{name: name, rng: rng})
				}
			}
			if len(names) == 0 {
				continue
			}
			parameters := liveParametersInPHP(method, resolver)
			methodName := phpquery.MethodName(method)
			methodRange := phpMethodNameRange(method)
			for _, event := range names {
				result = append(result, LiveListener{
					Name:        event.name,
					Class:       className,
					Method:      methodName,
					File:        path,
					Range:       event.rng,
					MethodRange: methodRange,
					Parameters:  append([]LiveActionParameter(nil), parameters...),
				})
			}
		}
	}
	return result
}

func liveParametersInPHP(
	method *phpsyntax.Node,
	resolver *php.NameResolver,
) []LiveActionParameter {
	var result []LiveActionParameter
	for _, parameter := range phpquery.Parameters(method) {
		phpName := strings.TrimPrefix(
			phpquery.ParameterName(parameter),
			"$",
		)
		if phpName == "" {
			continue
		}
		name := phpName
		attribute := resolvedAttribute(
			parameter,
			resolver,
			liveArg,
		)
		if attribute != nil {
			if alias, _ := phpAttributeStringValue(
				attribute,
				"name",
				0,
			); alias != "" {
				name = alias
			}
		}
		result = append(result, LiveActionParameter{
			Name:    name,
			PHPName: phpName,
			Type:    phpquery.ParameterType(parameter),
			Optional: phpquery.ParameterOptional(parameter) ||
				phpquery.ParameterVariadic(parameter),
			LiveArg: attribute != nil,
			Range:   parameter.RangeTrimmedTrivia(),
		})
	}
	return result
}

func liveEventReferencesInPHP(
	path string,
	root *phpsyntax.Node,
) ([]LiveEventReference, []LiveEventArgumentReference) {
	if root == nil {
		return nil, nil
	}
	resolver := php.NewNameResolver(root)
	var references []LiveEventReference
	var arguments []LiveEventArgumentReference
	for _, call := range phpquery.Calls(root) {
		kind, found := liveEventCallKind(call)
		if !found {
			continue
		}
		eventExpression := phpCallArgumentExpression(
			call,
			[]string{"event", "eventName"},
			0,
		)
		literal := phpquery.StringAt(eventExpression)
		if literal == nil || !phpStaticString(literal) {
			continue
		}
		name := strings.TrimSpace(phpquery.StringValue(literal))
		if name == "" {
			continue
		}
		rng := stringValueRange(literal)
		className := ""
		if class := phpquery.ClassAt(call); class != nil {
			className = normalizeClass(
				resolver.Resolve(phpquery.ClassName(class)),
			)
		}
		references = append(references, LiveEventReference{
			Name:         name,
			File:         path,
			Class:        className,
			Range:        rng,
			ContextRange: rng,
			Kind:         kind,
		})

		data := phpquery.ArrayAt(phpCallArgumentExpression(
			call,
			[]string{"data"},
			1,
		))
		for _, item := range phpquery.ArrayItems(data) {
			key := phpquery.StringAt(phpquery.ArrayItemKey(item))
			if key == nil || !phpStaticString(key) {
				continue
			}
			argumentName := strings.TrimSpace(phpquery.StringValue(key))
			if argumentName == "" {
				continue
			}
			arguments = append(arguments, LiveEventArgumentReference{
				Event: name,
				Name:  argumentName,
				File:  path,
				Range: stringValueRange(key),
			})
		}
	}
	return uniqueLiveEventReferences(references),
		uniqueLiveEventArgumentReferences(arguments)
}

func liveEventCallKind(
	call *phpsyntax.Node,
) (LiveEventReferenceKind, bool) {
	if call == nil || call.Kind() != phpsyntax.PhpMemberCall {
		return 0, false
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil ||
		!strings.EqualFold(strings.TrimSpace(receiver.Text()), "$this") {
		return 0, false
	}
	switch strings.ToLower(phpquery.CallMethodName(call)) {
	case "emit":
		return LiveEventEmitReference, true
	case "emitup":
		return LiveEventEmitUpReference, true
	case "emitself":
		return LiveEventEmitSelfReference, true
	default:
		return 0, false
	}
}

func phpCallArgumentExpression(
	call *phpsyntax.Node,
	names []string,
	position int,
) *phpsyntax.Node {
	positional := 0
	for index, argument := range phpquery.Arguments(call) {
		name := phpquery.ArgumentName(argument)
		if name != "" {
			for _, candidate := range names {
				if strings.EqualFold(name, candidate) {
					return phpquery.ArgumentExpression(call, index)
				}
			}
			continue
		}
		if positional == position {
			return phpquery.ArgumentExpression(call, index)
		}
		positional++
	}
	return nil
}

func phpStaticString(node *phpsyntax.Node) bool {
	if node == nil {
		return false
	}
	text := strings.TrimSpace(node.Text())
	if len(text) < 2 {
		return false
	}
	quote := text[0]
	if (quote != '\'' && quote != '"' && quote != '`') ||
		text[len(text)-1] != quote {
		return false
	}
	return quote == '\'' || !strings.Contains(text[1:len(text)-1], "$")
}

func LiveListenersInPHP(
	path string,
	root *phpsyntax.Node,
) []LiveListener {
	return liveListenersInPHP(path, root)
}

func LiveListenerAtPHP(
	path string,
	root *phpsyntax.Node,
	offset uint32,
) (LiveListener, bool) {
	for _, listener := range liveListenersInPHP(path, root) {
		if rangeContainsCursor(listener.Range, offset) ||
			rangeContainsCursor(listener.MethodRange, offset) {
			return listener, true
		}
	}
	return LiveListener{}, false
}

func LiveEventReferencesInPHP(
	path string,
	root *phpsyntax.Node,
) []LiveEventReference {
	references, _ := liveEventReferencesInPHP(path, root)
	return references
}

func LiveEventReferenceAtPHP(
	path string,
	root *phpsyntax.Node,
	offset uint32,
) (LiveEventReference, bool) {
	for _, reference := range LiveEventReferencesInPHP(path, root) {
		if rangeContainsCursor(reference.Range, offset) ||
			reference.Range.Len() == 0 && reference.Range.Start == offset {
			return reference, true
		}
	}
	return LiveEventReference{}, false
}

func LiveEventArgumentReferencesInPHP(
	path string,
	root *phpsyntax.Node,
) []LiveEventArgumentReference {
	_, arguments := liveEventReferencesInPHP(path, root)
	return arguments
}

func LiveEventArgumentReferenceAtPHP(
	path string,
	root *phpsyntax.Node,
	offset uint32,
) (LiveEventArgumentReference, bool) {
	for _, reference := range LiveEventArgumentReferencesInPHP(path, root) {
		if rangeContainsCursor(reference.Range, offset) {
			return reference, true
		}
	}
	return LiveEventArgumentReference{}, false
}

func LiveEventArgumentContextPHP(
	node *phpsyntax.Node,
) (string, []string, bool) {
	array := phpquery.ArrayAt(node)
	if array == nil {
		return "", nil, false
	}
	call := phpquery.CallAt(array)
	if _, found := liveEventCallKind(call); !found ||
		phpquery.ArrayAt(phpCallArgumentExpression(
			call,
			[]string{"data"},
			1,
		)) != array {
		return "", nil, false
	}
	event := phpquery.StringAt(phpCallArgumentExpression(
		call,
		[]string{"event", "eventName"},
		0,
	))
	name := strings.TrimSpace(phpquery.StringValue(event))
	if name == "" {
		return "", nil, false
	}
	var present []string
	for _, item := range phpquery.ArrayItems(array) {
		key := phpquery.StringAt(phpquery.ArrayItemKey(item))
		if key != nil {
			present = append(present, phpquery.StringValue(key))
		}
	}
	return name, present, true
}

func componentClassKinds(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) (component, live bool) {
	for _, attribute := range phpquery.Attributes(class) {
		resolved := normalizeClass(
			resolver.Resolve(phpquery.AttributeName(attribute)),
		)
		switch {
		case strings.EqualFold(resolved, asTwigComponent):
			component = true
		case strings.EqualFold(resolved, asLiveComponent):
			component = true
			live = true
		}
	}
	return component, live
}

func resolvedAttribute(
	node *phpsyntax.Node,
	resolver *php.NameResolver,
	name string,
) *phpsyntax.Node {
	for _, attribute := range phpquery.Attributes(node) {
		if strings.EqualFold(
			normalizeClass(
				resolver.Resolve(phpquery.AttributeName(attribute)),
			),
			name,
		) {
			return attribute
		}
	}
	return nil
}

func livePropWritable(attribute *phpsyntax.Node) bool {
	if attribute == nil {
		return false
	}
	value, found := phpAttributeArgument(attribute, "writable", 0)
	if !found || value == nil {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(value.Text()), "false")
}

func phpMethodNameRange(method *phpsyntax.Node) cst.TextRange {
	name := phpquery.MethodName(method)
	for child := range method.ChildNodes() {
		if child.Kind() == phpsyntax.PhpName &&
			phpquery.NameValue(child) == name {
			return child.RangeTrimmedTrivia()
		}
	}
	return method.RangeTrimmedTrivia()
}

func DeclarationsInPHP(
	path string,
	root *phpsyntax.Node,
) []Declaration {
	return declarationsInPHP(path, root)
}

func DeclarationAtPHP(
	path string,
	root *phpsyntax.Node,
	offset uint32,
) (Declaration, bool) {
	for _, declaration := range declarationsInPHP(path, root) {
		for _, rng := range []cst.TextRange{
			declaration.NameRange,
			declaration.ClassRange,
		} {
			if rangeContainsCursor(rng, offset) {
				return declaration, true
			}
		}
	}
	return Declaration{}, false
}

func phpAttributeArgument(
	attribute *phpsyntax.Node,
	name string,
	position int,
) (*phpsyntax.Node, bool) {
	positional := 0
	for _, argument := range phpquery.Arguments(attribute) {
		argumentName := phpquery.ArgumentName(argument)
		expression := phpArgumentExpression(argument)
		if strings.EqualFold(argumentName, name) {
			return expression, expression != nil
		}
		if argumentName == "" {
			if positional == position {
				return expression, expression != nil
			}
			positional++
		}
	}
	return nil, false
}

func phpArgumentExpression(
	argument *phpsyntax.Node,
) *phpsyntax.Node {
	if argument == nil {
		return nil
	}
	for child := range argument.ChildNodes() {
		if argument.Kind() == phpsyntax.PhpNamedArgument &&
			child.Kind() == phpsyntax.PhpName {
			continue
		}
		return child
	}
	return nil
}

func phpAttributeStringValue(
	attribute *phpsyntax.Node,
	name string,
	position int,
) (string, cst.TextRange) {
	value, found := phpAttributeArgument(attribute, name, position)
	if !found {
		return "", cst.TextRange{}
	}
	literal := phpquery.StringAt(value)
	if literal == nil {
		return "", cst.TextRange{}
	}
	return phpquery.StringValue(literal), stringValueRange(literal)
}

func phpClassNameRange(class *phpsyntax.Node) cst.TextRange {
	if class == nil {
		return cst.TextRange{}
	}
	name := phpquery.ClassName(class)
	for child := range class.ChildNodes() {
		if child.Kind() == phpsyntax.PhpName &&
			phpquery.NameValue(child) == name {
			return child.RangeTrimmedTrivia()
		}
	}
	return class.RangeTrimmedTrivia()
}

func stringValueRange(node *phpsyntax.Node) cst.TextRange {
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
