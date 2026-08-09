package admin

import (
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

type adminTwigUsageScanner struct {
	root                  *twigsyntax.Node
	filePath              string
	content               []byte
	collector             *adminUsageCollector
	shorthandMemberRanges map[cst.TextRange]bool
}

func parseAdminTwigUsages(
	root *twigsyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminUsageSet {
	scanner := &adminTwigUsageScanner{
		root:                  root,
		filePath:              filePath,
		content:               []byte(root.Text()),
		collector:             newAdminUsageCollector(filePath, lineIndex),
		shorthandMemberRanges: make(map[cst.TextRange]bool),
	}
	scanner.collectRegistryReferences()
	scanner.collectStartingTags()
	scanner.collectEndingTags()
	scanner.collectInstanceMembers()
	return scanner.collector.values()
}

func (scanner *adminTwigUsageScanner) collectRegistryReferences() {
	for _, reference := range TwigRegistryReferences(scanner.root) {
		scanner.collector.addRange(
			reference.Kind,
			"",
			reference.Name,
			reference.Range,
			false,
			false,
		)
	}
}

func (scanner *adminTwigUsageScanner) collectStartingTags() {
	for _, node := range twigquery.Nodes(scanner.root, twigsyntax.HtmlStartingTag) {
		tag, ok := twigast.CastHtmlStartingTag(node)
		if !ok || tag.Name() == nil {
			continue
		}
		scanner.collectStartingTag(node, tag)
	}
}

func (scanner *adminTwigUsageScanner) collectStartingTag(
	node *twigsyntax.Node,
	tag twigast.HtmlStartingTag,
) {
	name := tag.Name().Text()
	selector, dynamic := TwigDynamicComponentSelector(node)
	contractNames := staticComponentContractNames(node)
	dynamicUsage := dynamic && len(contractNames) == 0
	dynamicRouterView := dynamicUsage &&
		twigDynamicComponentUsesRouterView(node, selector)
	if dynamic {
		for _, candidate := range selector.Candidates {
			scanner.collector.addRange(
				AdminSymbolComponent,
				"",
				candidate.Name,
				candidate.Range,
				false,
				false,
			)
		}
	}
	if IsComponentTag(name) {
		scanner.collector.addRange(
			AdminSymbolComponent,
			"",
			name,
			tag.Name().Range(),
			false,
			false,
		)
	}
	for _, attribute := range tag.Attributes() {
		scanner.collectAttribute(
			node,
			attribute,
			contractNames,
			selector,
			dynamic,
			dynamicUsage,
			dynamicRouterView,
		)
	}
	if name == "slot" {
		collectSlotDeclaration(tag, scanner.filePath, scanner.collector)
	}
}

func (scanner *adminTwigUsageScanner) collectAttribute(
	node *twigsyntax.Node,
	attribute twigast.HtmlAttribute,
	contractNames []string,
	selector VueDynamicComponentSelector,
	dynamic,
	dynamicUsage,
	dynamicRouterView bool,
) {
	nameToken := attribute.Name()
	if nameToken == nil {
		return
	}
	attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
	if directive, found := VueDirectiveReferenceForAttribute(
		attributeName,
		nameToken.Range(),
	); found {
		scanner.collector.addRange(
			AdminSymbolDirective,
			"",
			directive.Name,
			directive.Range,
			false,
			false,
		)
	}
	if attributeName == "v-bind" {
		scanner.collectObjectBinding(
			attribute,
			contractNames,
			selector,
			dynamicUsage,
			dynamicRouterView,
		)
	}
	if len(contractNames) > 0 && (!dynamic || attributeName != selector.AttributeName) {
		scanner.collectContractAttribute(attributeName, nameToken.Range(), contractNames)
	}
	if dynamicUsage && attributeName != selector.AttributeName {
		scanner.collectDynamicAttribute(
			attributeName,
			nameToken.Range(),
			selector,
			dynamicRouterView,
		)
	}
	scanner.collectSlotAttribute(node, attributeName, nameToken.Range())
}

func (scanner *adminTwigUsageScanner) collectObjectBinding(
	attribute twigast.HtmlAttribute,
	contractNames []string,
	selector VueDynamicComponentSelector,
	dynamicUsage,
	dynamicRouterView bool,
) {
	value, ok := attribute.Value()
	if !ok {
		return
	}
	inner, ok := value.GetInner()
	if !ok {
		return
	}
	fields, _ := VueObjectBindingFields(
		inner.Syntax().Text(),
		inner.Syntax().Range().Start,
	)
	for _, field := range fields {
		if field.Shorthand {
			scanner.shorthandMemberRanges[field.ExpressionRange] = true
		}
		propName := NormalizePropName(field.Name)
		if propName == "" {
			continue
		}
		style, identifier := objectBindingNameStyle(field, propName)
		for _, contractName := range contractNames {
			scanner.collector.addStyledRange(
				AdminSymbolComponentProp,
				contractName,
				propName,
				field.NameRange,
				false,
				identifier,
				style,
			)
		}
		if dynamicUsage {
			scanner.collector.addDynamicStyledRange(
				AdminSymbolComponentProp,
				propName,
				field.NameRange,
				false,
				identifier,
				style,
				selector.Expression,
				dynamicRouterView,
			)
		}
	}
}

func objectBindingNameStyle(
	field VueObjectBindingField,
	propName string,
) (AdminNameStyle, bool) {
	if field.Shorthand {
		return AdminNameShorthand, true
	}
	return AdminNameExact, field.Name == propName
}

func (scanner *adminTwigUsageScanner) collectContractAttribute(
	attributeName string,
	rangeValue cst.TextRange,
	contractNames []string,
) {
	for _, contractName := range contractNames {
		if argument, model := NormalizeModelArgument(attributeName); model {
			modelName := "v-model"
			if argument != "" {
				modelName += ":" + CamelToKebab(argument)
			}
			scanner.collector.addRange(
				AdminSymbolComponentModel,
				contractName,
				modelName,
				mustVueAttributeArgumentRange(rangeValue, attributeName),
				false,
				false,
			)
		}
		if event, found := VueEventReferenceForAttribute(attributeName, rangeValue); found {
			scanner.collector.addRange(
				AdminSymbolComponentEvent,
				contractName,
				event.Name,
				event.Range,
				false,
				false,
			)
		}
		if prop, found := VuePropReferenceForAttribute(attributeName, rangeValue); found {
			scanner.collector.addRange(
				AdminSymbolComponentProp,
				contractName,
				prop.Name,
				prop.Range,
				false,
				false,
			)
		}
	}
}

func (scanner *adminTwigUsageScanner) collectDynamicAttribute(
	attributeName string,
	rangeValue cst.TextRange,
	selector VueDynamicComponentSelector,
	dynamicRouterView bool,
) {
	if argument, model := NormalizeModelArgument(attributeName); model {
		modelName := "v-model"
		if argument != "" {
			modelName += ":" + CamelToKebab(argument)
		}
		scanner.collector.addDynamicRange(
			AdminSymbolComponentModel,
			modelName,
			mustVueAttributeArgumentRange(rangeValue, attributeName),
			selector.Expression,
			dynamicRouterView,
		)
	}
	if event, found := VueEventReferenceForAttribute(attributeName, rangeValue); found {
		scanner.collector.addDynamicRange(
			AdminSymbolComponentEvent,
			event.Name,
			event.Range,
			selector.Expression,
			dynamicRouterView,
		)
	}
	if prop, found := VuePropReferenceForAttribute(attributeName, rangeValue); found {
		scanner.collector.addDynamicRange(
			AdminSymbolComponentProp,
			prop.Name,
			prop.Range,
			selector.Expression,
			dynamicRouterView,
		)
	}
}

func (scanner *adminTwigUsageScanner) collectSlotAttribute(
	node *twigsyntax.Node,
	attributeName string,
	rangeValue cst.TextRange,
) {
	slotName := NormalizeSlotName(attributeName)
	if slotName == "" || attributeName == "v-slot" {
		return
	}
	ownerTag := TwigSlotOwnerStartingTag(node)
	owners := staticComponentContractNames(ownerTag)
	if len(owners) == 0 && ownerTag == nil {
		if owner := parentComponentName(node); owner != "" {
			owners = []string{owner}
		}
	}
	argumentRange := mustVueAttributeArgumentRange(rangeValue, attributeName)
	for _, owner := range owners {
		scanner.collector.addRange(
			AdminSymbolComponentSlot,
			owner,
			slotName,
			argumentRange,
			false,
			false,
		)
	}
	if len(owners) != 0 {
		return
	}
	slotSelector, dynamic := TwigDynamicComponentSelector(ownerTag)
	if !dynamic {
		return
	}
	scanner.collector.addDynamicRange(
		AdminSymbolComponentSlot,
		slotName,
		argumentRange,
		slotSelector.Expression,
		twigDynamicComponentUsesRouterView(ownerTag, slotSelector),
	)
}

func (scanner *adminTwigUsageScanner) collectEndingTags() {
	for _, node := range twigquery.Nodes(scanner.root, twigsyntax.HtmlEndingTag) {
		tag, ok := twigast.CastHtmlEndingTag(node)
		if !ok || tag.Name() == nil || !IsComponentTag(tag.Name().Text()) {
			continue
		}
		scanner.collector.addRange(
			AdminSymbolComponent,
			"",
			tag.Name().Text(),
			tag.Name().Range(),
			false,
			false,
		)
	}
}

func (scanner *adminTwigUsageScanner) collectInstanceMembers() {
	for _, identifier := range TwigVueExpressionRootIdentifiers(
		scanner.root,
		scanner.content,
	) {
		if twigVueRootIdentifierIsLocal(scanner.root, scanner.content, identifier) {
			continue
		}
		style := AdminNameExact
		if scanner.shorthandMemberRanges[identifier.Range] {
			style = AdminNameShorthand
		}
		scanner.collector.addStyledRange(
			AdminSymbolComponentMember,
			"",
			identifier.Name,
			identifier.Range,
			false,
			true,
			style,
		)
	}
}
