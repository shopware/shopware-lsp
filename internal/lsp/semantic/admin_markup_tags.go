package semantic

import (
	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func (collector *adminMarkupTokenCollector) resolve(
	name string,
) (*admin.VueComponent, error) {
	if collector.resolved[name] {
		return collector.effective[name], nil
	}
	collector.resolved[name] = true
	component, found, err := collector.provider.index.GetComponentForTemplateTag(
		collector.templatePath,
		name,
		collector.owner,
	)
	if err != nil {
		return nil, err
	}
	if !found {
		component = nil
	}
	collector.effective[name] = component
	return component, nil
}

func (collector *adminMarkupTokenCollector) collectMarkup() error {
	for node := range twigquery.IterateNodes(
		collector.root,
		twigsyntax.HtmlStartingTag,
		twigsyntax.HtmlEndingTag,
	) {
		if err := collector.ctx.Err(); err != nil {
			return err
		}
		var err error
		switch node.Kind() {
		case twigsyntax.HtmlStartingTag:
			err = collector.collectStartingTag(node)
		case twigsyntax.HtmlEndingTag:
			err = collector.collectEndingTag(node)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (collector *adminMarkupTokenCollector) collectStartingTag(
	node *twigsyntax.Node,
) error {
	start, ok := twigast.CastHtmlStartingTag(node)
	if !ok || start.Name() == nil {
		return nil
	}
	componentName, dynamicContracts, selector, dynamic, err :=
		collector.dynamicComponentContracts(node, start.Name().Text())
	if err != nil {
		return err
	}
	component, err := collector.resolve(componentName)
	if err != nil {
		return err
	}
	if component != nil && !dynamic {
		collector.appendToken(start.Name().Range(), protocol.SemanticTokenClass)
	}
	slotComponents, err := collector.slotComponents(start, node)
	if err != nil {
		return err
	}
	for attribute := range start.Attributes() {
		if err := collector.collectAttribute(
			attribute,
			component,
			dynamicContracts,
			slotComponents,
			selector,
			dynamic,
		); err != nil {
			return err
		}
	}
	return nil
}

func (collector *adminMarkupTokenCollector) dynamicComponentContracts(
	node *twigsyntax.Node,
	fallbackName string,
) (
	string,
	[]admin.VueComponent,
	admin.VueDynamicComponentSelector,
	bool,
	error,
) {
	selector, dynamic := admin.TwigDynamicComponentSelector(node)
	if !dynamic {
		return fallbackName, nil, selector, false, nil
	}
	for _, candidate := range selector.Candidates {
		component, err := collector.resolve(candidate.Name)
		if err != nil {
			return "", nil, selector, true, err
		}
		if component != nil {
			collector.appendToken(candidate.Range, protocol.SemanticTokenClass)
		}
	}
	resolvedSelector, contracts, complete, err :=
		collector.provider.index.ResolveDynamicComponentContractsForOwner(
			collector.templatePath,
			selector,
			collector.owner,
			node,
		)
	if err != nil {
		return "", nil, selector, true, err
	}
	if !complete {
		contracts = nil
	}
	componentName := fallbackName
	if len(contracts) == 1 {
		componentName = contracts[0].Name
	} else if names := resolvedSelector.Names(); len(names) == 1 {
		componentName = names[0]
	}
	return componentName, contracts, selector, true, nil
}

func (collector *adminMarkupTokenCollector) slotComponents(
	start twigast.HtmlStartingTag,
	node *twigsyntax.Node,
) ([]admin.VueComponent, error) {
	hasSlotAttribute := false
	for attribute := range start.Attributes() {
		if admin.NormalizeSlotName(twigquery.HTMLAttributeName(attribute.Syntax())) != "" {
			hasSlotAttribute = true
			break
		}
	}
	if !hasSlotAttribute {
		return nil, nil
	}
	components, complete, err :=
		collector.provider.index.ResolveTwigSlotConsumerComponents(
			collector.templatePath,
			node,
			collector.owner,
		)
	if err != nil || !complete {
		return nil, err
	}
	return components, nil
}

func (collector *adminMarkupTokenCollector) collectAttribute(
	attribute twigast.HtmlAttribute,
	component *admin.VueComponent,
	dynamicContracts,
	slotComponents []admin.VueComponent,
	selector admin.VueDynamicComponentSelector,
	dynamic bool,
) error {
	nameToken := attribute.Name()
	if nameToken == nil {
		return nil
	}
	attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
	if directive, found := admin.VueDirectiveReferenceForAttribute(
		attributeName,
		nameToken.Range(),
	); found && collector.registeredDirectives[directive.Name] {
		collector.appendToken(nameToken.Range(), protocol.SemanticTokenFunction)
	}
	if dynamic && attributeName == selector.AttributeName {
		return nil
	}
	if attributeName == "v-bind" {
		collector.collectObjectBinding(attribute, component, dynamicContracts)
	}
	tokenType, matched := adminMarkupAttributeTokenType(
		attributeName,
		component,
		dynamicContracts,
		slotComponents,
	)
	if matched {
		collector.appendToken(nameToken.Range(), tokenType)
	}
	return nil
}

func (collector *adminMarkupTokenCollector) collectObjectBinding(
	attribute twigast.HtmlAttribute,
	component *admin.VueComponent,
	dynamicContracts []admin.VueComponent,
) {
	value, ok := attribute.Value()
	if !ok {
		return
	}
	inner, ok := value.GetInner()
	if !ok {
		return
	}
	fields, _ := admin.VueObjectBindingFields(
		inner.Syntax().Text(),
		inner.Syntax().Range().Start,
	)
	for _, field := range fields {
		matched := false
		if len(dynamicContracts) > 1 {
			_, matched = commonAdminAttributeSemanticType(field.Name, dynamicContracts)
		} else {
			_, matched = adminAttributeSemanticType(field.Name, component, component)
		}
		if matched {
			collector.appendToken(field.NameRange, protocol.SemanticTokenProperty)
		}
	}
}

func adminMarkupAttributeTokenType(
	name string,
	component *admin.VueComponent,
	dynamicContracts,
	slotComponents []admin.VueComponent,
) (uint32, bool) {
	if admin.NormalizeSlotName(name) != "" {
		switch len(slotComponents) {
		case 0:
		case 1:
			return adminAttributeSemanticType(name, nil, &slotComponents[0])
		default:
			return commonAdminAttributeSemanticType(name, slotComponents)
		}
	}
	if len(dynamicContracts) > 1 {
		return commonAdminAttributeSemanticType(name, dynamicContracts)
	}
	return adminAttributeSemanticType(name, component, component)
}

func (collector *adminMarkupTokenCollector) collectEndingTag(
	node *twigsyntax.Node,
) error {
	ending, ok := twigast.CastHtmlEndingTag(node)
	if !ok || ending.Name() == nil {
		return nil
	}
	component, err := collector.resolve(ending.Name().Text())
	if err != nil || component == nil {
		return err
	}
	collector.appendToken(ending.Name().Range(), protocol.SemanticTokenClass)
	return nil
}

func (collector *adminMarkupTokenCollector) appendToken(
	rangeValue cst.TextRange,
	tokenType uint32,
) {
	collector.result = append(collector.result, lsp.SemanticToken{
		Range: rangeValue,
		Type:  tokenType,
	})
}
