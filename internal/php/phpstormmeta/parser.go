// Package phpstormmeta parses the declarative subset of .phpstorm.meta.php
// files into typed semantic contracts.
package phpstormmeta

import (
	"strconv"
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

const namespace = "PHPSTORM_META"

// Parse extracts supported metadata declarations in source order. Unknown and
// malformed directives are ignored so a newer PhpStorm metadata dialect never
// prevents the containing PHP file from being indexed.
func Parse(root *phpsyntax.Node) []semantic.CallContract {
	if root == nil || !strings.EqualFold(phpquery.Namespace(root), namespace) {
		return nil
	}
	extractor := contractExtractor{
		argumentSets: make(map[string][]semantic.CallValue),
	}
	phpquery.Visit(
		root,
		func(call *phpsyntax.Node) bool {
			extractor.visit(call)
			return true
		},
		phpsyntax.PhpFunctionCall,
	)
	return extractor.contracts
}

type contractExtractor struct {
	argumentSets map[string][]semantic.CallValue
	contracts    []semantic.CallContract
}

func (extractor *contractExtractor) visit(call *phpsyntax.Node) {
	name := strings.ToLower(strings.TrimPrefix(
		phpquery.CallMethodName(call),
		"\\",
	))
	var contract semantic.CallContract
	var ok bool
	switch name {
	case "override":
		contract, ok = extractor.parseOverride(call)
	case "expectedarguments":
		contract, ok = extractor.parseExpectedArguments(call)
	case "expectedreturnvalues":
		contract, ok = extractor.parseExpectedReturnValues(call)
	case "registerargumentsset":
		extractor.parseArgumentSet(call)
		return
	case "exitpoint":
		contract, ok = extractor.parseExitPoint(call)
	}
	if ok {
		extractor.contracts = append(extractor.contracts, contract)
	}
}

func (extractor *contractExtractor) parseOverride(
	call *phpsyntax.Node,
) (semantic.CallContract, bool) {
	target, selector, _, ok := parseTarget(
		phpquery.ArgumentExpression(call, 0),
	)
	if !ok {
		return semantic.CallContract{}, false
	}
	ruleNode := phpquery.ArgumentExpression(call, 1)
	if ruleNode == nil || ruleNode.Kind() != phpsyntax.PhpFunctionCall {
		return semantic.CallContract{}, false
	}
	var returnContract semantic.CallReturnContract
	switch strings.ToLower(strings.TrimPrefix(
		phpquery.CallMethodName(ruleNode),
		"\\",
	)) {
	case "type":
		argument, valid := parseArgumentIndex(ruleNode)
		if !valid {
			return semantic.CallContract{}, false
		}
		returnContract.Kind = semantic.CallReturnArgumentType
		returnContract.Argument = argument
	case "elementtype":
		argument, valid := parseArgumentIndex(ruleNode)
		if !valid {
			return semantic.CallContract{}, false
		}
		returnContract.Kind = semantic.CallReturnArgumentElementType
		returnContract.Argument = argument
	case "map":
		entries := parseMapEntries(ruleNode)
		if len(entries) == 0 {
			return semantic.CallContract{}, false
		}
		returnContract.Kind = semantic.CallReturnArgumentMap
		returnContract.Argument = selector
		returnContract.Map = entries
	default:
		return semantic.CallContract{}, false
	}
	return semantic.CallContract{
		Target: target,
		Return: returnContract,
	}, true
}

func (extractor *contractExtractor) parseExpectedArguments(
	call *phpsyntax.Node,
) (semantic.CallContract, bool) {
	target, _, _, ok := parseTarget(phpquery.ArgumentExpression(call, 0))
	if !ok {
		return semantic.CallContract{}, false
	}
	argument, ok := parseUint16(phpquery.ArgumentExpression(call, 1))
	if !ok {
		return semantic.CallContract{}, false
	}
	values := extractor.callValues(call, 2)
	if len(values) == 0 {
		return semantic.CallContract{}, false
	}
	return semantic.CallContract{
		Target: target,
		ExpectedArguments: []semantic.ExpectedArgumentContract{{
			Argument: argument,
			Values:   values,
		}},
	}, true
}

func (extractor *contractExtractor) parseExpectedReturnValues(
	call *phpsyntax.Node,
) (semantic.CallContract, bool) {
	target, _, _, ok := parseTarget(phpquery.ArgumentExpression(call, 0))
	if !ok {
		return semantic.CallContract{}, false
	}
	values := extractor.callValues(call, 1)
	if len(values) == 0 {
		return semantic.CallContract{}, false
	}
	return semantic.CallContract{
		Target:               target,
		ExpectedReturnValues: values,
	}, true
}

func (extractor *contractExtractor) parseArgumentSet(call *phpsyntax.Node) {
	nameNode := phpquery.ArgumentExpression(call, 0)
	if nameNode == nil || nameNode.Kind() != phpsyntax.PhpString {
		return
	}
	name := phpquery.StringValue(nameNode)
	values := extractor.callValues(call, 1)
	if name != "" && len(values) > 0 {
		extractor.argumentSets[name] = values
	}
}

func (extractor *contractExtractor) parseExitPoint(
	call *phpsyntax.Node,
) (semantic.CallContract, bool) {
	targetNode := phpquery.ArgumentExpression(call, 0)
	target, _, _, ok := parseTarget(targetNode)
	if !ok {
		return semantic.CallContract{}, false
	}
	var conditions []semantic.ExpectedArgumentContract
	for index := range phpquery.Arguments(targetNode) {
		expression := phpquery.ArgumentExpression(targetNode, index)
		if expression == nil {
			continue
		}
		if strings.HasSuffix(
			strings.ToUpper(strings.TrimSpace(expression.Text())),
			"ANY_ARGUMENT",
		) {
			continue
		}
		values := extractor.expressionValues(expression)
		if len(values) == 0 {
			// An unsupported predicate must not be widened into an
			// unconditional exit contract.
			return semantic.CallContract{}, false
		}
		conditions = append(conditions, semantic.ExpectedArgumentContract{
			Argument: uint16(index),
			Values:   values,
		})
	}
	return semantic.CallContract{
		Target:        target,
		ExitPoint:     true,
		ExitArguments: conditions,
	}, true
}

func (extractor *contractExtractor) callValues(
	call *phpsyntax.Node,
	start int,
) []semantic.CallValue {
	arguments := phpquery.Arguments(call)
	var result []semantic.CallValue
	for index := start; index < len(arguments); index++ {
		expression := phpquery.ArgumentExpression(call, index)
		if expression == nil {
			continue
		}
		result = append(result, extractor.expressionValues(expression)...)
	}
	return result
}

func (extractor *contractExtractor) expressionValues(
	expression *phpsyntax.Node,
) []semantic.CallValue {
	if expression == nil {
		return nil
	}
	if expression.Kind() == phpsyntax.PhpFunctionCall &&
		strings.EqualFold(
			strings.TrimPrefix(phpquery.CallMethodName(expression), "\\"),
			"argumentsSet",
		) {
		nameNode := phpquery.ArgumentExpression(expression, 0)
		if nameNode != nil && nameNode.Kind() == phpsyntax.PhpString {
			return append(
				[]semantic.CallValue(nil),
				extractor.argumentSets[phpquery.StringValue(nameNode)]...,
			)
		}
		return nil
	}
	value, ok := parseCallValue(expression)
	if !ok {
		return nil
	}
	return []semantic.CallValue{value}
}

func parseTarget(
	node *phpsyntax.Node,
) (semantic.CallTarget, uint16, bool, bool) {
	if node == nil {
		return semantic.CallTarget{}, 0, false, false
	}
	selector, hasSelector := parseUint16(phpquery.ArgumentExpression(node, 0))
	switch node.Kind() {
	case phpsyntax.PhpFunctionCall:
		name := phpquery.CallMethodName(node)
		if name == "" {
			return semantic.CallTarget{}, 0, false, false
		}
		return semantic.NewFunctionCallTarget(name), selector, hasSelector, true
	case phpsyntax.PhpScopedCall:
		receiver := phpquery.CallReceiver(node)
		if receiver == nil || receiver.Kind() != phpsyntax.PhpName {
			return semantic.CallTarget{}, 0, false, false
		}
		className := phpquery.NameValue(receiver)
		methodName := phpquery.CallMethodName(node)
		if className == "" || methodName == "" {
			return semantic.CallTarget{}, 0, false, false
		}
		return semantic.NewMethodCallTarget(
			className,
			methodName,
		), selector, hasSelector, true
	default:
		return semantic.CallTarget{}, 0, false, false
	}
}

func parseArgumentIndex(call *phpsyntax.Node) (uint16, bool) {
	return parseUint16(phpquery.ArgumentExpression(call, 0))
}

func parseUint16(expression *phpsyntax.Node) (uint16, bool) {
	if expression == nil || expression.Kind() != phpsyntax.PhpNumber {
		return 0, false
	}
	text := strings.ReplaceAll(strings.TrimSpace(expression.Text()), "_", "")
	value, err := strconv.ParseUint(text, 10, 16)
	if err != nil {
		return 0, false
	}
	return uint16(value), true
}

func parseMapEntries(call *phpsyntax.Node) []semantic.CallMapEntry {
	array := phpquery.ArrayAt(phpquery.ArgumentExpression(call, 0))
	if array == nil {
		return nil
	}
	var result []semantic.CallMapEntry
	for _, item := range phpquery.ArrayItems(array) {
		key, keyOK := parseCallValue(phpquery.ArrayItemKey(item))
		value, valueOK := parseCallValue(phpquery.ArrayItemValue(item))
		if keyOK && valueOK {
			result = append(result, semantic.CallMapEntry{
				Key:    key,
				Result: value,
			})
		}
	}
	return result
}

func parseCallValue(node *phpsyntax.Node) (semantic.CallValue, bool) {
	if node == nil {
		return semantic.CallValue{}, false
	}
	expression := strings.TrimSpace(node.Text())
	if expression == "" {
		return semantic.CallValue{}, false
	}
	value := semantic.CallValue{Expression: expression, Value: expression}
	switch node.Kind() {
	case phpsyntax.PhpString:
		value.Kind = semantic.CallValueString
		value.Value = phpquery.StringValue(node)
	case phpsyntax.PhpNumber:
		value.Kind = semantic.CallValueNumber
	case phpsyntax.PhpName:
		value.Kind = semantic.CallValueConstant
		value.Value = strings.TrimPrefix(phpquery.NameValue(node), "\\")
	case phpsyntax.PhpScopedAccess:
		value.Kind = semantic.CallValueClassConstant
		value.Value = strings.TrimPrefix(expression, "\\")
	default:
		if strings.Contains(expression, "::") &&
			!strings.ContainsAny(expression, "|&") {
			value.Kind = semantic.CallValueClassConstant
			value.Value = strings.TrimPrefix(expression, "\\")
		} else {
			value.Kind = semantic.CallValueExpression
		}
	}
	return value, true
}
