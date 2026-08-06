package phprewrite

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

// SetParameterType adds or replaces the native type of one parameter.
func (e *Editor) SetParameterType(parameter *phpsyntax.Node, typeText string) error {
	typeText = strings.TrimSpace(typeText)
	if e == nil || e.builder == nil {
		return fmt.Errorf("set PHP parameter type: editor is nil")
	}
	if parameter == nil || parameter.Kind() != phpsyntax.PhpParameter {
		return fmt.Errorf("set PHP parameter type: parameter is unavailable")
	}
	if typeText == "" {
		return fmt.Errorf("set PHP parameter type: type is empty")
	}
	if existing := directTypeNode(parameter); existing != nil {
		return e.builder.ReplaceRange(existing.RangeTrimmedTrivia(), typeText)
	}
	variable := phpquery.DirectChild(parameter, phpsyntax.PhpVariable)
	if variable == nil {
		return fmt.Errorf("set PHP parameter type: variable is unavailable")
	}
	return e.builder.Insert(variable.RangeTrimmedTrivia().Start, typeText+" ")
}

// SetPropertyType adds or replaces the native type of a property declaration.
func (e *Editor) SetPropertyType(property *phpsyntax.Node, typeText string) error {
	typeText = strings.TrimSpace(typeText)
	if e == nil || e.builder == nil {
		return fmt.Errorf("set PHP property type: editor is nil")
	}
	if property == nil || property.Kind() != phpsyntax.PhpPropertyDeclaration {
		return fmt.Errorf("set PHP property type: property is unavailable")
	}
	if typeText == "" {
		return fmt.Errorf("set PHP property type: type is empty")
	}
	if existing := directTypeNode(property); existing != nil {
		return e.builder.ReplaceRange(existing.RangeTrimmedTrivia(), typeText)
	}
	variable := phpquery.DirectChild(property, phpsyntax.PhpVariable)
	if variable == nil {
		return fmt.Errorf("set PHP property type: variable is unavailable")
	}
	return e.builder.Insert(variable.RangeTrimmedTrivia().Start, typeText+" ")
}

// SetReturnType adds or replaces a function-like declaration return type.
func (e *Editor) SetReturnType(functionLike *phpsyntax.Node, typeText string) error {
	typeText = strings.TrimSpace(typeText)
	if e == nil || e.builder == nil {
		return fmt.Errorf("set PHP return type: editor is nil")
	}
	if functionLike == nil {
		return fmt.Errorf("set PHP return type: declaration is unavailable")
	}
	switch functionLike.Kind() {
	case phpsyntax.PhpMethodDeclaration, phpsyntax.PhpFunctionDeclaration,
		phpsyntax.PhpClosure, phpsyntax.PhpArrowFunction:
	default:
		return fmt.Errorf("set PHP return type: declaration is unavailable")
	}
	if typeText == "" {
		return fmt.Errorf("set PHP return type: type is empty")
	}
	if existing := directTypeNode(functionLike); existing != nil {
		return e.builder.ReplaceRange(existing.RangeTrimmedTrivia(), typeText)
	}
	parameters := phpquery.DirectChild(functionLike, phpsyntax.PhpParameterList)
	if parameters == nil {
		return fmt.Errorf("set PHP return type: parameter list is unavailable")
	}
	offset := parameters.RangeTrimmedTrivia().End
	return e.builder.ReplaceRange(cst.TextRange{Start: offset, End: offset}, ": "+typeText)
}

func directTypeNode(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		switch child.Kind() {
		case phpsyntax.PhpType, phpsyntax.PhpNullableType,
			phpsyntax.PhpUnionType, phpsyntax.PhpIntersectionType:
			return child
		}
	}
	return nil
}
