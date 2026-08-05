package admin

import (
	"strings"

	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

const shopwareContextType = "ContextState"

// JavaScriptShopwareContextMember reports a statically named member access
// rooted at Shopware.Context. receiver contains the path between Context and
// the selected member, so Shopware.Context.api.languageId yields
// receiver=["api"] and member="languageId".
func JavaScriptShopwareContextMember(
	node *jssyntax.Node,
) (receiver []string, memberName string, matched bool) {
	return javaScriptStaticMember(node, "Shopware.Context")
}

// JavaScriptShopwareContextMemberNameNode returns the exact identifier owned
// by a recognized Shopware.Context member access.
func JavaScriptShopwareContextMemberNameNode(
	node *jssyntax.Node,
) *jssyntax.Node {
	return javaScriptStaticMemberNameNode(node, "Shopware.Context")
}

// ResolveShopwareContext resolves the receiver contract for a static
// Shopware.Context access from the indexed ContextState declaration. The root
// stays open because useContext() also exposes runtime helper functions which
// are not part of ContextState; app/api descendants remain closed structural
// contracts and can safely power typo diagnostics.
func (idx *AdminComponentIndexer) ResolveShopwareContext(
	receiver,
	contextPath string,
) (VueTypeShape, error) {
	segments, matched := staticExpressionPath(receiver)
	if !matched {
		return VueTypeShape{}, nil
	}
	shape, err := idx.ResolveVueType(shopwareContextType, contextPath)
	if err != nil {
		return shape, err
	}
	if len(segments) == 0 {
		shape.Complete = false
		return shape, nil
	}
	currentContext := contextPath
	for _, segment := range segments {
		member, found := twigVueMemberNamed(shape.Members, segment.Name)
		if !found || member.Type == "" {
			return VueTypeShape{Type: member.Type}, nil
		}
		if member.DefinitionPath != "" {
			currentContext = member.DefinitionPath
		}
		if len(member.NestedMembers) > 0 || member.NestedComplete {
			shape = VueTypeShape{
				Type: member.Type, Members: member.NestedMembers,
				Complete: member.NestedComplete,
			}
		} else {
			shape, err = idx.ResolveVueType(member.Type, currentContext)
			if err != nil {
				return shape, err
			}
		}
	}
	return shape, nil
}

func (idx *AdminComponentIndexer) resolveShopwareContextExpressionType(
	expression,
	contextPath string,
) (resolvedVueExpressionType, bool, error) {
	segments, incomplete, matched := javaScriptStaticExpression(
		expression, "Shopware.Context",
	)
	if !matched || incomplete {
		return resolvedVueExpressionType{}, false, nil
	}
	names := staticExpressionSegmentNames(segments)
	if len(names) != len(segments) {
		return resolvedVueExpressionType{}, false, nil
	}
	if len(names) == 0 {
		return resolvedVueExpressionType{
			Type: shopwareContextType, ContextPath: contextPath,
		}, true, nil
	}
	shape, err := idx.ResolveShopwareContext(
		strings.Join(names[:len(names)-1], "."), contextPath,
	)
	if err != nil {
		return resolvedVueExpressionType{}, false, err
	}
	member, found := twigVueMemberNamed(shape.Members, names[len(names)-1])
	if !found || member.Type == "" {
		return resolvedVueExpressionType{}, false, nil
	}
	resolvedContext := contextPath
	if member.DefinitionPath != "" {
		resolvedContext = member.DefinitionPath
	}
	return resolvedVueExpressionType{
		Type: member.Type, ContextPath: resolvedContext,
	}, true, nil
}
