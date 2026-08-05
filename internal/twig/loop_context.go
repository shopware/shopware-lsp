package twig

import (
	"strings"

	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const TwigLoopContextClass = "Twig\\Runtime\\LoopContext"

var twig3LoopVariables = []string{
	"index",
	"index0",
	"revindex",
	"revindex0",
	"first",
	"last",
	"length",
	"parent",
}

// Twig3LoopVariables returns the fixed loop keys exposed by Twig 3. Twig 4
// replaces this array-shaped value with Twig\Runtime\LoopContext.
func Twig3LoopVariables() []string {
	return append([]string(nil), twig3LoopVariables...)
}

// LoopContextType resolves Twig 4's loop object when that runtime is installed.
func LoopContextType(index *php.PHPIndex) (types.Type, bool) {
	if index == nil {
		return types.Unknown(), false
	}
	if _, found := index.FindClass(TwigLoopContextClass); !found {
		return types.Unknown(), false
	}
	return types.Named(TwigLoopContextClass), true
}

// LoopAccessorInScope reports whether accessor is a direct loop.member access
// inside the body of a Twig for loop. A for-else body does not expose that
// loop's context, although an enclosing loop can still remain in scope.
func LoopAccessorInScope(accessor *twigsyntax.Node) bool {
	if !directLoopAccessor(accessor) {
		return false
	}
	offset := accessor.Range().Start
	for current := accessor.Parent(); current != nil; current = current.Parent() {
		if current.Kind() != twigsyntax.TwigFor {
			continue
		}
		for child := range current.ChildNodes() {
			if child.Kind() != twigsyntax.Body {
				continue
			}
			if child.Range().Contains(offset) {
				return true
			}
			break
		}
	}
	return false
}

func directLoopAccessor(accessor *twigsyntax.Node) bool {
	if accessor == nil || accessor.Kind() != twigsyntax.TwigAccessor {
		return false
	}
	operands := directTwigChildren(accessor, twigsyntax.TwigOperand)
	if len(operands) == 0 {
		return false
	}
	root := firstTwigChild(operands[0])
	return root != nil &&
		root.Kind() == twigsyntax.TwigLiteralName &&
		strings.EqualFold(strings.TrimSpace(root.Text()), "loop")
}
