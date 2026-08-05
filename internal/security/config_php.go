package security

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

const securityConfigClass = "Symfony\\Config\\SecurityConfig"

type phpSecurityConfigState uint8

const (
	phpSecurityConfigUnknown phpSecurityConfigState = iota
	phpSecurityConfigRoot
	phpSecurityConfigProvider
	phpSecurityConfigProviderChain
	phpSecurityConfigFirewall
	phpSecurityConfigSwitchUser
)

type phpSecurityConfigEvaluator struct {
	path          string
	rootVariables map[string]struct{}
	variables     map[string]phpSecurityConfigState
	occurrences   []ConfigOccurrence
}

func parsePHPConfigOccurrences(
	path string,
	root *phpsyntax.Node,
) []ConfigOccurrence {
	if root == nil ||
		!strings.Contains(root.Text(), "SecurityConfig") ||
		(!strings.Contains(root.Text(), "->provider") &&
			!strings.Contains(root.Text(), "->firewall")) {
		return nil
	}

	resolver := php.NewNameResolver(root)
	var result []ConfigOccurrence
	for _, function := range phpquery.Nodes(
		root,
		phpsyntax.PhpClosure,
		phpsyntax.PhpFunctionDeclaration,
		phpsyntax.PhpArrowFunction,
	) {
		rootVariables := securityConfigParameters(function, resolver)
		if len(rootVariables) == 0 {
			continue
		}
		evaluator := phpSecurityConfigEvaluator{
			path:          path,
			rootVariables: rootVariables,
			variables:     make(map[string]phpSecurityConfigState),
		}
		evaluator.evaluate(function)
		result = append(result, evaluator.occurrences...)
	}
	return uniqueConfigOccurrences(result)
}

func securityConfigParameters(
	function *phpsyntax.Node,
	resolver *php.NameResolver,
) map[string]struct{} {
	result := make(map[string]struct{})
	for _, parameter := range phpquery.Parameters(function) {
		if !isSecurityConfigType(phpquery.ParameterType(parameter), resolver) {
			continue
		}
		if name := phpquery.ParameterName(parameter); name != "" {
			result[name] = struct{}{}
		}
	}
	return result
}

func isSecurityConfigType(
	value string,
	resolver *php.NameResolver,
) bool {
	for _, member := range strings.FieldsFunc(value, func(value rune) bool {
		switch value {
		case '|', '&', '(', ')':
			return true
		default:
			return false
		}
	}) {
		member = strings.TrimSpace(strings.TrimPrefix(member, "?"))
		if member != "" && strings.EqualFold(
			resolver.Resolve(member),
			securityConfigClass,
		) {
			return true
		}
	}
	return false
}

func (e *phpSecurityConfigEvaluator) evaluate(
	function *phpsyntax.Node,
) {
	for _, statement := range phpquery.ExpressionStatements(function) {
		if phpquery.FunctionLikeAt(statement) != function {
			continue
		}
		e.evaluateExpression(statement)
	}
	if function.Kind() == phpsyntax.PhpArrowFunction {
		e.evaluateExpression(function)
	}
}

func (e *phpSecurityConfigEvaluator) evaluateExpression(
	root *phpsyntax.Node,
) {
	for _, call := range outermostSecurityConfigCalls(root) {
		e.resolve(call)
	}

	variable := phpquery.AssignedVariable(root)
	if variable == "" {
		return
	}
	value := securityConfigAssignmentValue(root)
	if value == nil {
		delete(e.variables, variable)
		return
	}
	if state, found := e.resolve(value); found {
		e.variables[variable] = state
	} else {
		delete(e.variables, variable)
	}
}

func outermostSecurityConfigCalls(
	root *phpsyntax.Node,
) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	for _, call := range phpquery.Calls(root) {
		nested := false
		for parent := call.Parent(); parent != nil && parent != root; parent = parent.Parent() {
			switch parent.Kind() {
			case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall,
				phpsyntax.PhpFunctionCall:
				nested = true
			}
			if nested {
				break
			}
		}
		if !nested {
			result = append(result, call)
		}
	}
	return result
}

func securityConfigAssignmentValue(
	root *phpsyntax.Node,
) *phpsyntax.Node {
	assignments := phpquery.Nodes(root, phpsyntax.PhpAssignmentExpression)
	if len(assignments) == 0 {
		return nil
	}
	var leftSeen bool
	for child := range assignments[0].ChildNodes() {
		if !leftSeen {
			leftSeen = true
			continue
		}
		return child
	}
	return nil
}

func (e *phpSecurityConfigEvaluator) resolve(
	expression *phpsyntax.Node,
) (phpSecurityConfigState, bool) {
	if expression == nil {
		return phpSecurityConfigUnknown, false
	}
	if expression.Kind() == phpsyntax.PhpVariable {
		name := "$" + phpquery.VariableName(expression)
		if state, found := e.variables[name]; found {
			return state, true
		}
		if _, found := e.rootVariables[name]; found {
			return phpSecurityConfigRoot, true
		}
		return phpSecurityConfigUnknown, false
	}
	if expression.Kind() != phpsyntax.PhpMemberCall {
		for child := range expression.ChildNodes() {
			if state, found := e.resolve(child); found {
				return state, true
			}
		}
		return phpSecurityConfigUnknown, false
	}

	state, found := e.resolve(phpquery.CallReceiver(expression))
	if !found {
		return phpSecurityConfigUnknown, false
	}
	method := strings.ToLower(phpquery.CallMethodName(expression))
	switch state {
	case phpSecurityConfigRoot:
		switch method {
		case "provider":
			e.appendLiteral(
				expression,
				0,
				ConfigProvider,
				ConfigDeclaration,
				ConfigProviderDeclaration,
			)
			return phpSecurityConfigProvider, true
		case "firewall":
			e.appendLiteral(
				expression,
				0,
				ConfigFirewall,
				ConfigDeclaration,
				ConfigFirewallDeclaration,
			)
			return phpSecurityConfigFirewall, true
		}
	case phpSecurityConfigProvider:
		if method == "chain" {
			return phpSecurityConfigProviderChain, true
		}
	case phpSecurityConfigProviderChain:
		if method == "providers" {
			e.appendLiteralValues(
				phpquery.ArgumentExpression(expression, 0),
				ConfigChainProvider,
			)
		}
	case phpSecurityConfigFirewall:
		switch method {
		case "provider":
			e.appendLiteral(
				expression,
				0,
				ConfigProvider,
				ConfigReference,
				ConfigFirewallProvider,
			)
		case "switchuser":
			return phpSecurityConfigSwitchUser, true
		}
	case phpSecurityConfigSwitchUser:
		if method == "provider" {
			e.appendLiteral(
				expression,
				0,
				ConfigProvider,
				ConfigReference,
				ConfigSwitchUserProvider,
			)
		}
	}
	return state, true
}

func (e *phpSecurityConfigEvaluator) appendLiteral(
	call *phpsyntax.Node,
	index int,
	kind ConfigKind,
	role ConfigRole,
	origin ConfigOrigin,
) {
	node := phpquery.ArgumentExpression(call, index)
	value, found := staticSecurityConfigString(node)
	if !found || role == ConfigDeclaration &&
		(value == "" || strings.HasPrefix(value, "_")) {
		return
	}
	e.occurrences = append(e.occurrences, ConfigOccurrence{
		Name:   value,
		Kind:   kind,
		Role:   role,
		Origin: origin,
		File:   e.path,
		Range:  phpquery.StringContentRange(node),
	})
}

func (e *phpSecurityConfigEvaluator) appendLiteralValues(
	expression *phpsyntax.Node,
	origin ConfigOrigin,
) {
	if expression == nil {
		return
	}
	if value, found := staticSecurityConfigString(expression); found {
		e.occurrences = append(e.occurrences, ConfigOccurrence{
			Name:   value,
			Kind:   ConfigProvider,
			Role:   ConfigReference,
			Origin: origin,
			File:   e.path,
			Range:  phpquery.StringContentRange(expression),
		})
		return
	}
	if expression.Kind() != phpsyntax.PhpArray {
		return
	}
	for _, item := range phpquery.ArrayItems(expression) {
		valueNode := phpquery.ArrayItemValue(item)
		value, found := staticSecurityConfigString(valueNode)
		if !found {
			continue
		}
		e.occurrences = append(e.occurrences, ConfigOccurrence{
			Name:   value,
			Kind:   ConfigProvider,
			Role:   ConfigReference,
			Origin: origin,
			File:   e.path,
			Range:  phpquery.StringContentRange(valueNode),
		})
	}
}

func staticSecurityConfigString(
	node *phpsyntax.Node,
) (string, bool) {
	if node == nil || node.Kind() != phpsyntax.PhpString {
		return "", false
	}
	text := strings.TrimSpace(node.Text())
	if len(text) < 2 ||
		(text[0] != '\'' && text[0] != '"') ||
		text[len(text)-1] != text[0] {
		return "", false
	}
	if text[0] == '"' && strings.Contains(text[1:len(text)-1], "$") {
		return "", false
	}
	return phpquery.StringValue(node), true
}
