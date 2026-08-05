package semantic

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminMarkupProvider gives registry-backed Vue component markup semantic
// meaning in Twig. It deliberately stays out of JavaScript and TypeScript,
// where the client's native language service remains authoritative.
type AdminMarkupProvider struct {
	index *admin.AdminComponentIndexer
}

func NewAdminMarkupProvider(
	index *admin.AdminComponentIndexer,
) *AdminMarkupProvider {
	return &AdminMarkupProvider{index: index}
}

func (p *AdminMarkupProvider) GetSemanticTokens(
	ctx context.Context,
	request *lsp.SemanticTokensRequest,
) ([]lsp.SemanticToken, error) {
	if p == nil || p.index == nil || request == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil || request.Document.SyntaxTree.Root == nil ||
		(!strings.HasSuffix(strings.ToLower(request.Document.URI), ".twig") &&
			!strings.HasSuffix(strings.ToLower(request.Document.URI), ".vue")) ||
		!strings.Contains(
			filepathSlash(request.Document.URI),
			"/Resources/app/administration/",
		) {
		return nil, nil
	}
	templatePath, err := uriutil.Path(request.Document.URI)
	if err != nil {
		return nil, err
	}
	root := request.Document.SyntaxTree.Root
	owner, err := p.index.GetComponentForDocument(
		templatePath, root, string(request.Document.Text), request.Document.LineIndex,
	)
	if err != nil {
		return nil, err
	}
	registeredDirectives := make(map[string]bool)
	directives, err := p.index.GetAllDirectivesForTemplate(templatePath)
	if err != nil {
		return nil, err
	}
	for _, directive := range directives {
		registeredDirectives[directive.Name] = true
	}
	effective := make(map[string]*admin.VueComponent)
	resolved := make(map[string]bool)
	resolve := func(name string) (*admin.VueComponent, error) {
		if resolved[name] {
			return effective[name], nil
		}
		resolved[name] = true
		component, found, resolveErr := p.index.GetComponentForTemplateTag(
			templatePath, name, owner,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !found {
			component = nil
		}
		effective[name] = component
		return component, nil
	}

	var result []lsp.SemanticToken
	for _, node := range twigquery.Nodes(
		root,
		twigsyntax.HtmlStartingTag,
		twigsyntax.HtmlEndingTag,
	) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch node.Kind() {
		case twigsyntax.HtmlStartingTag:
			start, ok := twigast.CastHtmlStartingTag(node)
			if !ok || start.Name() == nil {
				continue
			}
			name := start.Name().Text()
			componentName := name
			var dynamicContracts []admin.VueComponent
			selector, dynamicComponent := admin.TwigDynamicComponentSelector(node)
			if dynamicComponent {
				for _, candidate := range selector.Candidates {
					candidateComponent, candidateErr := resolve(candidate.Name)
					if candidateErr != nil {
						return nil, candidateErr
					}
					if candidateComponent != nil {
						result = append(result, lsp.SemanticToken{
							Range: candidate.Range,
							Type:  protocol.SemanticTokenClass,
						})
					}
				}
				resolvedSelector, contracts, complete, contractErr :=
					p.index.ResolveDynamicComponentContractsForOwner(
						templatePath, selector, owner, node,
					)
				if contractErr != nil {
					return nil, contractErr
				}
				if complete {
					dynamicContracts = contracts
				}
				if len(dynamicContracts) == 1 {
					componentName = dynamicContracts[0].Name
				} else if names := resolvedSelector.Names(); len(names) == 1 {
					componentName = names[0]
				}
			}
			component, resolveErr := resolve(componentName)
			if resolveErr != nil {
				return nil, resolveErr
			}
			isComponent := component != nil
			if isComponent && !dynamicComponent {
				result = append(result, lsp.SemanticToken{
					Range: start.Name().Range(), Type: protocol.SemanticTokenClass,
				})
			}
			var slotComponents []admin.VueComponent
			hasSlotAttribute := false
			for _, attribute := range start.Attributes() {
				if admin.NormalizeSlotName(
					twigquery.HTMLAttributeName(attribute.Syntax()),
				) != "" {
					hasSlotAttribute = true
					break
				}
			}
			if hasSlotAttribute {
				resolvedSlotComponents, slotComplete, slotErr :=
					p.index.ResolveTwigSlotConsumerComponents(
						templatePath, node, owner,
					)
				if slotErr != nil {
					return nil, slotErr
				}
				if slotComplete {
					slotComponents = resolvedSlotComponents
				}
			}
			for _, attribute := range start.Attributes() {
				nameToken := attribute.Name()
				if nameToken == nil {
					continue
				}
				attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
				if directive, found := admin.VueDirectiveReferenceForAttribute(
					attributeName, nameToken.Range(),
				); found && registeredDirectives[directive.Name] {
					result = append(result, lsp.SemanticToken{
						Range: nameToken.Range(), Type: protocol.SemanticTokenFunction,
					})
				}
				if dynamicComponent && attributeName == selector.AttributeName {
					continue
				}
				if attributeName == "v-bind" {
					if value, valueOK := attribute.Value(); valueOK {
						if inner, innerOK := value.GetInner(); innerOK {
							fields, _ := admin.VueObjectBindingFields(
								inner.Syntax().Text(), inner.Syntax().Range().Start,
							)
							for _, field := range fields {
								_, fieldMatched := uint32(0), false
								if len(dynamicContracts) > 1 {
									_, fieldMatched = commonAdminAttributeSemanticType(
										field.Name, dynamicContracts,
									)
								} else {
									_, fieldMatched = adminAttributeSemanticType(
										field.Name, component, component,
									)
								}
								if fieldMatched {
									result = append(result, lsp.SemanticToken{
										Range: field.NameRange,
										Type:  protocol.SemanticTokenProperty,
									})
								}
							}
						}
					}
				}
				tokenType, matched := uint32(0), false
				if admin.NormalizeSlotName(attributeName) != "" &&
					len(slotComponents) > 1 {
					tokenType, matched = commonAdminAttributeSemanticType(
						attributeName, slotComponents,
					)
				} else if admin.NormalizeSlotName(attributeName) != "" &&
					len(slotComponents) == 1 {
					tokenType, matched = adminAttributeSemanticType(
						attributeName, nil, &slotComponents[0],
					)
				} else if len(dynamicContracts) > 1 {
					tokenType, matched = commonAdminAttributeSemanticType(
						attributeName, dynamicContracts,
					)
				} else {
					tokenType, matched = adminAttributeSemanticType(
						attributeName,
						component,
						component,
					)
				}
				if matched {
					result = append(result, lsp.SemanticToken{
						Range: nameToken.Range(), Type: tokenType,
					})
				}
			}
		case twigsyntax.HtmlEndingTag:
			ending, ok := twigast.CastHtmlEndingTag(node)
			if !ok || ending.Name() == nil {
				continue
			}
			component, resolveErr := resolve(ending.Name().Text())
			if resolveErr != nil {
				return nil, resolveErr
			}
			if component == nil {
				continue
			}
			result = append(result, lsp.SemanticToken{
				Range: ending.Name().Range(), Type: protocol.SemanticTokenClass,
			})
		}
	}
	seenLexical := make(map[cst.TextRange]bool)
	for _, token := range result {
		seenLexical[token.Range] = true
	}
	for _, binding := range admin.TwigVueBindings(
		root, request.Document.Text,
	) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, rangeValue := range admin.TwigVueBindingReferences(
			root, request.Document.Text, binding,
		) {
			if seenLexical[rangeValue] {
				continue
			}
			seenLexical[rangeValue] = true
			result = append(result, lsp.SemanticToken{
				Range: rangeValue, Type: protocol.SemanticTokenVariable,
			})
		}
	}
	for _, access := range admin.TwigVueExpressionMemberAccesses(
		root, request.Document.Text,
	) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		node := root.NodeAtOffset(access.MemberRange.Start)
		resolvedSlot, resolveErr := p.index.ResolveTwigScopedSlotMemberForOwner(
			root, node, request.Document.Text, access.MemberRange.Start,
			templatePath, owner,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		resolvedVue, vueFound := admin.TwigVueBindingAtOffset(
			root, request.Document.Text, access.RootRange.Start,
		)
		lexicalMember := vueFound && resolvedVue != nil &&
			(resolvedSlot == nil || resolvedVue.ScopeRange.Len() <=
				resolvedSlot.Scope.TemplateRange.Len())
		contractMember := !lexicalMember && resolvedSlot != nil &&
			resolvedSlot.MemberFound
		instanceMember := false
		if !lexicalMember && !contractMember {
			resolvedInstance, instanceErr :=
				p.index.ResolveTwigVueInstanceMemberForComponent(
					root, request.Document.Text, access.MemberRange.Start,
					templatePath, owner,
				)
			if instanceErr != nil {
				return nil, instanceErr
			}
			instanceMember = resolvedInstance != nil && resolvedInstance.MemberFound
		}
		if !lexicalMember && !contractMember && !instanceMember ||
			seenLexical[access.MemberRange] {
			continue
		}
		seenLexical[access.MemberRange] = true
		result = append(result, lsp.SemanticToken{
			Range: access.MemberRange, Type: protocol.SemanticTokenProperty,
		})
	}
	if owner != nil {
		for _, identifier := range admin.TwigVueExpressionRootIdentifiers(
			root, request.Document.Text,
		) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if seenLexical[identifier.Range] {
				continue
			}
			if binding, found := admin.TwigVueBindingAtOffset(
				root, request.Document.Text, identifier.Range.Start,
			); found && binding != nil {
				continue
			}
			node := root.NodeAtOffset(identifier.Range.Start)
			slotBinding, resolveErr := p.index.ResolveTwigScopedSlotBindingForOwner(
				root, node, request.Document.Text, identifier.Range.Start,
				templatePath, owner,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if slotBinding != nil {
				seenLexical[identifier.Range] = true
				result = append(result, lsp.SemanticToken{
					Range: identifier.Range,
					Type:  protocol.SemanticTokenVariable,
				})
				continue
			}
			member, found := owner.TemplateMember(identifier.Name)
			if !found {
				member, found = admin.VueBuiltinMember(identifier.Name)
			}
			if !found {
				continue
			}
			tokenType := uint32(protocol.SemanticTokenVariable)
			if member.Kind == admin.ComponentMemberMethod {
				tokenType = protocol.SemanticTokenFunction
			}
			seenLexical[identifier.Range] = true
			result = append(result, lsp.SemanticToken{
				Range: identifier.Range, Type: tokenType,
			})
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start != result[right].Range.Start {
			return result[left].Range.Start < result[right].Range.Start
		}
		return result[left].Range.End < result[right].Range.End
	})
	return result, nil
}

func commonAdminAttributeSemanticType(
	name string,
	components []admin.VueComponent,
) (uint32, bool) {
	if len(components) == 0 {
		return 0, false
	}
	var common uint32
	for index := range components {
		tokenType, found := adminAttributeSemanticType(
			name, &components[index], &components[index],
		)
		if !found || index > 0 && tokenType != common {
			return 0, false
		}
		common = tokenType
	}
	return common, true
}

func adminAttributeSemanticType(
	name string,
	component *admin.VueComponent,
	slotComponent *admin.VueComponent,
) (uint32, bool) {
	if slotName := admin.NormalizeSlotName(name); slotName != "" &&
		slotComponent != nil {
		if _, found := slotComponent.ComponentSlot(slotName); found {
			return protocol.SemanticTokenProperty, true
		}
		return 0, false
	}
	if component == nil {
		return 0, false
	}
	if _, model := admin.NormalizeModelArgument(name); model {
		if _, found := component.ComponentModel(name); found {
			return protocol.SemanticTokenProperty, true
		}
		return 0, false
	}
	if event := admin.NormalizeEventName(name); event != "" {
		if _, found := component.ComponentEvent(event); found {
			return protocol.SemanticTokenFunction, true
		}
		return 0, false
	}
	if modifier := strings.IndexByte(name, '.'); modifier >= 0 {
		name = name[:modifier]
	}
	propName := admin.NormalizePropName(name)
	for _, prop := range component.Props {
		if prop.Name == propName {
			return protocol.SemanticTokenProperty, true
		}
	}
	return 0, false
}

func filepathSlash(value string) string {
	return strings.ReplaceAll(value, `\`, "/")
}

var _ lsp.SemanticTokensProvider = (*AdminMarkupProvider)(nil)
