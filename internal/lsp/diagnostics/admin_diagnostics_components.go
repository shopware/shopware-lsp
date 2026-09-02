package diagnostics

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/suggestion"
)

func (p *AdminAnalyzer) checkComponentProps(
	root *twigsyntax.Node,
	content []byte,
	startTag *twigsyntax.Node,
	analysis *adminTwigDiagnosticDocument,
	diagnostics *[]lsp.Problem,
) error {
	// Get the tag name
	tagName := p.getTagName(startTag)
	if tagName == "" {
		return nil
	}
	componentRange := cst.TextRange{}
	if tagNameNode := p.getTagNameNode(startTag); tagNameNode != nil {
		componentRange = tagNameNode.Range()
	}
	selector, dynamicComponent := admin.TwigDynamicComponentSelector(startTag)
	if dynamicComponent {
		for _, candidate := range selector.Candidates {
			component, found, resolveErr := p.diagnosticComponentForTemplateTag(
				analysis, candidate.Name,
			)
			if resolveErr != nil {
				return resolveErr
			}
			if !found || component == nil {
				p.checkUnknownDynamicComponent(candidate, diagnostics)
			} else {
				p.checkDeprecatedComponent(
					*component, candidate.Name, candidate.Range, diagnostics,
				)
			}
		}
		resolvedSelector, components, complete, resolveErr :=
			p.adminIndexer.ResolveDynamicComponentContractsForOwner(
				analysis.templatePath, selector, analysis.liveOwner, startTag,
			)
		if resolveErr != nil {
			return resolveErr
		}
		if !complete {
			return nil
		}
		p.checkUnknownComponentContractAttributes(
			startTag, components, selector.AttributeName, tagName, diagnostics,
		)
		p.checkDeprecatedComponentContractAttributes(
			startTag, components, diagnostics,
		)
		for _, component := range components {
			candidateRange := componentRange
			for _, candidate := range resolvedSelector.Candidates {
				if candidate.Name == component.Name {
					if candidate.Range.Len() > 0 {
						candidateRange = candidate.Range
					} else if resolvedSelector.ExpressionRange.Len() > 0 {
						candidateRange = resolvedSelector.ExpressionRange
					}
					break
				}
			}
			if err := p.checkResolvedComponentProps(
				root, content, startTag, analysis.templatePath,
				component, component.Name, candidateRange, true,
				analysis.liveOwner, diagnostics,
			); err != nil {
				return err
			}
		}
		return nil
	}

	// Skip non-component tags (standard HTML elements and template)
	if !admin.IsComponentTag(tagName) {
		return nil
	}

	// Get the component definition
	component, found, err := p.diagnosticComponentForTemplateTag(
		analysis, tagName,
	)
	if err != nil || !found || component == nil {
		if !dynamicComponent {
			p.checkUnknownComponent(tagName, startTag, diagnostics)
		}
		return nil
	}
	p.checkUnknownComponentContractAttributes(
		startTag, []admin.VueComponent{*component}, "", tagName, diagnostics,
	)
	p.checkDeprecatedComponent(*component, tagName, componentRange, diagnostics)
	p.checkDeprecatedComponentContractAttributes(
		startTag, []admin.VueComponent{*component}, diagnostics,
	)

	return p.checkResolvedComponentProps(
		root, content, startTag, analysis.templatePath,
		*component, tagName, componentRange, false, analysis.liveOwner, diagnostics,
	)
}

func (p *AdminAnalyzer) checkDeprecatedComponent(
	component admin.VueComponent,
	displayName string,
	rangeValue cst.TextRange,
	diagnostics *[]lsp.Problem,
) {
	if rangeValue.Len() == 0 || strings.TrimSpace(component.Deprecated) == "" {
		return
	}
	if _, suppressed := p.suppressedComponentDiagnostics[displayName]; suppressed {
		return
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: rangeValue,
		Message: fmt.Sprintf(
			"Administration component '%s' is deprecated: %s",
			displayName, component.Deprecated,
		),
		Source: "shopware", Severity: protocol.DiagnosticSeverityHint,
		ID:   "admin.component.deprecated",
		Tags: []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
	})
}

func (p *AdminAnalyzer) checkDeprecatedComponentContractAttributes(
	startTag *twigsyntax.Node,
	components []admin.VueComponent,
	diagnostics *[]lsp.Problem,
) {
	if startTag == nil || len(components) == 0 {
		return
	}
	ownerNames := make([]string, 0, len(components))
	for _, component := range components {
		ownerNames = append(ownerNames, component.Name)
	}
	owner := strings.Join(ownerNames, " | ")
	tag, ok := twigast.CastHtmlStartingTag(startTag)
	if !ok {
		return
	}
	for attribute := range tag.Attributes() {
		nameToken := attribute.Name()
		if nameToken == nil {
			continue
		}
		attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
		if attributeName == "v-bind" {
			value, valueOK := attribute.Value()
			if !valueOK {
				continue
			}
			inner, innerOK := value.GetInner()
			if !innerOK {
				continue
			}
			fields, _ := admin.VueObjectBindingFields(
				inner.Syntax().Text(), inner.Syntax().Range().Start,
			)
			for _, field := range fields {
				name := admin.NormalizePropName(field.Name)
				if message := commonDeprecatedAdminProp(
					components, name,
				); message != "" {
					appendDeprecatedAdminPropProblem(
						diagnostics, owner, name, message, field.NameRange,
					)
				}
			}
			continue
		}
		if _, modelAttribute := admin.NormalizeModelArgument(attributeName); modelAttribute {
			message, propName := commonDeprecatedAdminModel(
				components, attributeName,
			)
			if message != "" {
				appendDeprecatedAdminPropProblem(
					diagnostics, owner, propName, message, nameToken.Range(),
				)
			}
			continue
		}
		reference, found := admin.VuePropReferenceForAttribute(
			attributeName, nameToken.Range(),
		)
		if !found {
			continue
		}
		if message := commonDeprecatedAdminProp(
			components, reference.Name,
		); message != "" {
			appendDeprecatedAdminPropProblem(
				diagnostics, owner, reference.Name, message, reference.Range,
			)
		}
	}
}

func commonDeprecatedAdminProp(
	components []admin.VueComponent,
	name string,
) string {
	seen := make(map[string]bool)
	var messages []string
	for _, component := range components {
		prop, found := component.ComponentProp(name)
		if !found || strings.TrimSpace(prop.Deprecated) == "" {
			return ""
		}
		if !seen[prop.Deprecated] {
			seen[prop.Deprecated] = true
			messages = append(messages, prop.Deprecated)
		}
	}
	return strings.Join(messages, " / ")
}

func commonDeprecatedAdminModel(
	components []admin.VueComponent,
	attributeName string,
) (string, string) {
	seen := make(map[string]bool)
	var messages []string
	propName := ""
	for _, component := range components {
		model, found := component.ComponentModel(attributeName)
		if !found || strings.TrimSpace(model.Prop.Deprecated) == "" {
			return "", ""
		}
		if propName == "" {
			propName = model.PropName
		}
		if !seen[model.Prop.Deprecated] {
			seen[model.Prop.Deprecated] = true
			messages = append(messages, model.Prop.Deprecated)
		}
	}
	return strings.Join(messages, " / "), propName
}

func appendDeprecatedAdminPropProblem(
	diagnostics *[]lsp.Problem,
	owner,
	propName,
	message string,
	rangeValue cst.TextRange,
) {
	if diagnostics == nil || rangeValue.Len() == 0 {
		return
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: rangeValue,
		Message: fmt.Sprintf(
			"Prop '%s' on Administration component '%s' is deprecated: %s",
			propName, owner, message,
		),
		Source: "shopware", Severity: protocol.DiagnosticSeverityHint,
		ID:   "admin.component.deprecated-prop",
		Tags: []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
	})
}

func (p *AdminAnalyzer) checkUnknownComponentContractAttributes(
	startTag *twigsyntax.Node,
	components []admin.VueComponent,
	selectorAttribute,
	tagName string,
	diagnostics *[]lsp.Problem,
) {
	if startTag == nil || len(components) == 0 {
		return
	}
	catalog := newAdminComponentContractCatalog(components)
	tag, ok := twigast.CastHtmlStartingTag(startTag)
	if !ok {
		return
	}
	for attribute := range tag.Attributes() {
		p.checkUnknownComponentContractAttribute(
			attribute,
			catalog,
			selectorAttribute,
			tagName,
			diagnostics,
		)
	}
}

type adminComponentContractCatalog struct {
	props      map[string]bool
	propNames  []string
	events     map[string]bool
	eventNames []string
	models     map[string]bool
	modelNames []string
}

func newAdminComponentContractCatalog(
	components []admin.VueComponent,
) adminComponentContractCatalog {
	result := adminComponentContractCatalog{
		props:  make(map[string]bool),
		events: make(map[string]bool),
		models: make(map[string]bool),
	}
	for _, component := range components {
		for _, prop := range component.Props {
			name := strings.TrimSpace(prop.Name)
			if name != "" && !result.props[name] {
				result.props[name] = true
				result.propNames = append(result.propNames, name)
			}
			attributeName := "v-model:" + admin.CamelToKebab(prop.Name)
			if _, found := component.ComponentModel(attributeName); found &&
				!result.models[prop.Name] {
				result.models[prop.Name] = true
				result.modelNames = append(result.modelNames, prop.Name)
			}
		}
		for _, event := range component.ComponentEvents() {
			name := admin.CanonicalEventName(event.Name)
			if name != "" && !result.events[name] {
				result.events[name] = true
				result.eventNames = append(result.eventNames, name)
			}
		}
	}
	return result
}

func (p *AdminAnalyzer) checkUnknownComponentContractAttribute(
	attribute twigast.HtmlAttribute,
	catalog adminComponentContractCatalog,
	selectorAttribute,
	tagName string,
	diagnostics *[]lsp.Problem,
) {
	nameToken := attribute.Name()
	if nameToken == nil {
		return
	}
	attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
	if attributeName == "" || attributeName == selectorAttribute {
		return
	}
	if attributeName == "v-bind" {
		p.checkUnknownComponentObjectBinding(
			attribute,
			catalog,
			tagName,
			diagnostics,
		)
		return
	}
	if reference, found := admin.VueModelReferenceForAttribute(
		attributeName,
		nameToken.Range(),
	); found {
		appendUnknownAdminContractProblem(
			diagnostics,
			reference,
			tagName,
			"model",
			catalog.models,
			catalog.modelNames,
			true,
		)
		return
	}
	if reference, found := admin.VueEventReferenceForAttribute(
		attributeName,
		nameToken.Range(),
	); found {
		if !isNativeAdministrationEvent(reference.Name) {
			appendUnknownAdminContractProblem(
				diagnostics,
				reference,
				tagName,
				"event",
				catalog.events,
				catalog.eventNames,
				false,
			)
		}
		return
	}
	reference, found := admin.VuePropReferenceForAttribute(
		attributeName,
		nameToken.Range(),
	)
	if !found || isAdministrationFallthroughAttribute(
		attributeName,
		reference.Name,
	) {
		return
	}
	appendUnknownAdminContractProblem(
		diagnostics,
		reference,
		tagName,
		"prop",
		catalog.props,
		catalog.propNames,
		true,
	)
}

func (p *AdminAnalyzer) checkUnknownComponentObjectBinding(
	attribute twigast.HtmlAttribute,
	catalog adminComponentContractCatalog,
	tagName string,
	diagnostics *[]lsp.Problem,
) {
	value, found := attribute.Value()
	if !found {
		return
	}
	inner, found := value.GetInner()
	if !found {
		return
	}
	fields, _ := admin.VueObjectBindingFields(
		inner.Syntax().Text(),
		inner.Syntax().Range().Start,
	)
	for _, field := range fields {
		p.checkUnknownComponentObjectBindingField(
			field,
			catalog.props,
			catalog.propNames,
			tagName,
			diagnostics,
		)
	}
}

func appendUnknownAdminContractProblem(
	diagnostics *[]lsp.Problem,
	reference admin.VueAttributeReference,
	tagName,
	kind string,
	known map[string]bool,
	names []string,
	kebabSuggestions bool,
) {
	if known[reference.Name] {
		return
	}
	suggestions := adminNearbySuggestions(reference.Name, names)
	if kebabSuggestions {
		for index := range suggestions {
			suggestions[index] = admin.CamelToKebab(suggestions[index])
		}
	}
	if len(suggestions) == 0 {
		return
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: reference.Range,
		Message: fmt.Sprintf(
			"Unknown %s '%s' on component '%s'",
			kind,
			reference.Name,
			tagName,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       lsp.DiagnosticID("admin.component.unknown-" + kind),
		Payload: map[string]any{
			"componentName": tagName,
			kind + "Name":   reference.Name,
			"suggestions":   suggestions,
		},
	})
}

func (p *AdminAnalyzer) checkComponentSlotNames(
	startTag *twigsyntax.Node,
	templatePath string,
	liveOwner *admin.VueComponent,
	diagnostics *[]lsp.Problem,
) error {
	if p == nil || p.adminIndexer == nil || startTag == nil {
		return nil
	}
	tag, ok := twigast.CastHtmlStartingTag(startTag)
	if !ok {
		return nil
	}
	var references []admin.VueAttributeReference
	for attribute := range tag.Attributes() {
		nameToken := attribute.Name()
		if nameToken == nil {
			continue
		}
		if reference, found := admin.VueSlotReferenceForAttribute(
			twigquery.HTMLAttributeName(attribute.Syntax()), nameToken.Range(),
		); found {
			references = append(references, reference)
		}
	}
	if len(references) == 0 {
		return nil
	}
	components, complete, err := p.adminIndexer.ResolveTwigSlotConsumerComponents(
		templatePath, startTag, liveOwner,
	)
	if err != nil || !complete || len(components) == 0 {
		return err
	}
	knownNames := make([]string, 0)
	known := make(map[string]bool)
	componentNames := make([]string, 0, len(components))
	for _, component := range components {
		componentNames = append(componentNames, component.Name)
		for _, slot := range component.Slots {
			if slot.Name == "" || known[slot.Name] {
				continue
			}
			known[slot.Name] = true
			knownNames = append(knownNames, slot.Name)
		}
	}
	owner := strings.Join(componentNames, " | ")
	for _, reference := range references {
		valid := false
		for _, component := range components {
			if _, found := component.ComponentSlot(reference.Name); found {
				valid = true
				break
			}
		}
		if valid {
			continue
		}
		suggestions := adminNearbySuggestions(reference.Name, knownNames)
		if len(suggestions) == 0 {
			continue
		}
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Unknown slot '%s' on component '%s'",
				reference.Name, owner,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.unknown-slot",
			Payload: map[string]any{
				"componentName": owner,
				"slotName":      reference.Name,
				"suggestions":   suggestions,
			},
		})
	}
	return nil
}

func (p *AdminAnalyzer) checkUnknownComponentObjectBindingField(
	field admin.VueObjectBindingField,
	knownProps map[string]bool,
	propNames []string,
	tagName string,
	diagnostics *[]lsp.Problem,
) {
	name := admin.NormalizePropName(field.Name)
	if name == "" || knownProps[name] ||
		isAdministrationFallthroughAttribute(field.Name, name) {
		return
	}
	suggestions := adminNearbySuggestions(name, propNames)
	if strings.Contains(field.Name, "-") {
		for index := range suggestions {
			suggestions[index] = admin.CamelToKebab(suggestions[index])
		}
	}
	if len(suggestions) == 0 {
		return
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: field.NameRange,
		Message: fmt.Sprintf(
			"Unknown v-bind prop '%s' on component '%s'", name, tagName,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.component.unknown-prop",
		Payload: map[string]any{
			"componentName": tagName,
			"propName":      name,
			"suggestions":   suggestions,
		},
	})
}

func isAdministrationFallthroughAttribute(raw, normalized string) bool {
	raw = strings.ToLower(strings.TrimLeft(
		strings.TrimSpace(raw), ":",
	))
	raw = strings.TrimPrefix(raw, "v-bind:")
	if strings.HasPrefix(raw, "data-") || strings.HasPrefix(raw, "aria-") ||
		strings.HasPrefix(normalized, "on") && len(normalized) > 2 &&
			normalized[2] >= 'A' && normalized[2] <= 'Z' {
		return true
	}
	switch strings.ToLower(normalized) {
	case "accept", "accesskey", "action", "alt", "autocomplete", "autofocus",
		"checked", "class", "contenteditable", "controls", "crossorigin",
		"disabled", "draggable", "enctype", "for", "form", "height", "hidden",
		"href", "id", "inert", "inputmode", "is", "key", "lang", "loop",
		"max", "maxlength", "method", "min", "minlength", "multiple", "muted",
		"name", "nonce", "novalidate", "part", "pattern", "placeholder",
		"playsinline", "poster", "preload", "readonly", "ref", "rel", "required",
		"role", "rows", "selected", "size", "slot", "spellcheck", "src", "step",
		"style", "tabindex", "target", "title", "translate", "type", "value",
		"width", "wrap":
		return true
	default:
		return false
	}
}

func isNativeAdministrationEvent(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "abort", "animationcancel", "animationend", "animationiteration",
		"animationstart", "auxclick", "beforeinput", "beforetoggle", "blur",
		"cancel", "canplay", "canplaythrough", "change", "click", "close",
		"compositionend", "compositionstart", "compositionupdate", "contextmenu",
		"copy", "cuechange", "cut", "dblclick", "drag", "dragend", "dragenter",
		"dragleave", "dragover", "dragstart", "drop", "durationchange", "emptied",
		"ended", "error", "focus", "focusin", "focusout", "formdata",
		"fullscreenchange", "fullscreenerror", "gotpointercapture", "input",
		"invalid", "keydown", "keypress", "keyup", "load", "loadeddata",
		"loadedmetadata", "loadstart", "lostpointercapture", "mousedown",
		"mouseenter", "mouseleave", "mousemove", "mouseout", "mouseover",
		"mouseup", "paste", "pause", "play", "playing", "pointercancel",
		"pointerdown", "pointerenter", "pointerleave", "pointermove", "pointerout",
		"pointerover", "pointerup", "progress", "ratechange", "reset", "resize",
		"scroll", "scrollend", "securitypolicyviolation", "seeked", "seeking",
		"select", "selectionchange", "selectstart", "slotchange", "stalled",
		"submit", "suspend", "timeupdate", "toggle", "touchcancel", "touchend",
		"touchmove", "touchstart", "transitioncancel", "transitionend",
		"transitionrun", "transitionstart", "volumechange", "waiting", "wheel":
		return true
	default:
		return false
	}
}

func (p *AdminAnalyzer) checkResolvedComponentProps(
	root *twigsyntax.Node,
	content []byte,
	startTag *twigsyntax.Node,
	templatePath string,
	comp admin.VueComponent,
	tagName string,
	componentRange cst.TextRange,
	dynamicComponent bool,
	liveOwner *admin.VueComponent,
	diagnostics *[]lsp.Problem,
) error {
	// Get the attributes present on the tag
	presentAttrs, hasUnknownObjectBinding := p.getTagAttributes(startTag)
	props := make(map[string]admin.VueComponentProp, len(comp.Props))
	for _, prop := range comp.Props {
		props[prop.Name] = prop
	}
	if dynamicComponent {
		delete(props, "is")
		delete(presentAttrs, "is")
	}
	p.checkStaticPropTypes(tagName, startTag, props, diagnostics)
	p.checkStaticPropValues(tagName, startTag, props, diagnostics)
	if err := p.checkBoundPropTypes(
		root, content, tagName, startTag, props, templatePath,
		liveOwner, diagnostics,
	); err != nil {
		return err
	}
	if err := p.checkModelBindings(
		root, content, tagName, startTag, comp, templatePath,
		liveOwner, diagnostics,
	); err != nil {
		return err
	}
	if hasUnknownObjectBinding {
		// An arbitrary v-bind expression may contain any required prop. Absence
		// cannot be proven without evaluating the component expression.
		return nil
	}

	// Check for missing required props
	for _, prop := range comp.Props {
		if !prop.Required {
			continue
		}

		// Check if prop is present (also check Vue binding variants)
		if p.isPropPresent(prop.Name, presentAttrs) ||
			componentModelPropPresent(comp, prop.Name, presentAttrs) {
			continue
		}

		// Get the tag name node for the diagnostic range
		if componentRange.Len() == 0 {
			continue
		}

		*diagnostics = append(*diagnostics, lsp.Problem{
			Range:    componentRange,
			Message:  fmt.Sprintf("Missing required prop '%s' on component '%s'", prop.Name, tagName),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.missing-required-prop",
			Payload: map[string]any{
				"componentName": tagName,
				"propName":      prop.Name,
			},
		})
	}
	return nil
}

func (p *AdminAnalyzer) checkUnknownDynamicComponent(
	candidate admin.VueDynamicComponentCandidate,
	diagnostics *[]lsp.Problem,
) {
	if p == nil || p.adminIndexer == nil ||
		!admin.IsShopwareComponentTag(candidate.Name) {
		return
	}
	components, err := p.adminIndexer.GetAllComponentsView()
	if err != nil || len(components) == 0 {
		return
	}
	names := make([]string, 0, len(components))
	seen := make(map[string]bool, len(components))
	for _, component := range components {
		if component.Name == "" || seen[component.Name] {
			continue
		}
		seen[component.Name] = true
		names = append(names, component.Name)
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: candidate.Range,
		Message: fmt.Sprintf(
			"Administration component '%s' is not registered",
			candidate.Name,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.component.not-found",
		Payload: map[string]any{
			"componentName": candidate.Name,
			"suggestions":   suggestion.Similar(candidate.Name, names),
		},
	})
}

func (p *AdminAnalyzer) checkModelBindings(
	root *twigsyntax.Node,
	content []byte,
	tagName string,
	startTag *twigsyntax.Node,
	component admin.VueComponent,
	templatePath string,
	liveOwner *admin.VueComponent,
	diagnostics *[]lsp.Problem,
) error {
	for attributeNode := range twigquery.IterateNodes(
		startTag, twigsyntax.HtmlAttribute,
	) {
		attributeName := twigquery.HTMLAttributeName(attributeNode)
		if _, modelAttribute := admin.NormalizeModelArgument(attributeName); !modelAttribute {
			continue
		}
		model, found := component.ComponentModel(attributeName)
		if !found {
			continue
		}
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok {
			continue
		}
		value, ok := attribute.Value()
		if !ok {
			continue
		}
		inner, ok := value.GetInner()
		if !ok {
			continue
		}
		expressionRange := inner.Syntax().RangeTrimmedTrivia()
		if expressionRange.Len() == 0 || expressionRange.End > uint32(len(content)) {
			continue
		}
		expression := strings.TrimSpace(inner.Syntax().Text())
		if !admin.VueModelExpressionAssignable(expression) {
			*diagnostics = append(*diagnostics, lsp.Problem{
				Range: expressionRange,
				Message: fmt.Sprintf(
					"Model binding '%s' on component '%s' requires an assignable expression",
					attributeName, tagName,
				),
				Source:   "shopware",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       "admin.component.model-not-assignable",
				Payload: map[string]any{
					"componentName": tagName,
					"modelName":     attributeName,
					"expression":    expression,
				},
			})
			continue
		}
		actualType, resolved, err :=
			p.adminIndexer.ResolveTwigVueExpressionTypeForComponent(
				root, content, expression, expressionRange.Start,
				templatePath, liveOwner,
			)
		if err != nil {
			return err
		}
		if !resolved {
			continue
		}
		expectedTypes := []string{model.Prop.Type}
		if payloadType := admin.VueEventPayloadType(model.Event.Type); payloadType != "" {
			expectedTypes = append(expectedTypes, payloadType)
		}
		expectedType := ""
		incompatible := false
		for _, candidate := range expectedTypes {
			if !admin.VueTypesProvablyIncompatible(candidate, actualType) {
				continue
			}
			incompatible = true
			expectedType = admin.VuePropValueType(candidate)
			break
		}
		if !incompatible {
			continue
		}
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: expressionRange,
			Message: fmt.Sprintf(
				"Model binding '%s' on component '%s' expects %s, but the expression has type %s",
				attributeName, tagName, expectedType, actualType,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.model-type",
			Payload: map[string]any{
				"componentName": tagName,
				"modelName":     attributeName,
				"propName":      model.PropName,
				"eventName":     model.EventName,
				"expectedType":  expectedType,
				"actualType":    actualType,
			},
		})
	}
	return nil
}

func componentModelPropPresent(
	component admin.VueComponent,
	propName string,
	attributes map[string]bool,
) bool {
	for attributeName := range attributes {
		argument, model := admin.NormalizeModelArgument(attributeName)
		if !model {
			continue
		}
		if argument != "" {
			prop, found := component.ComponentProp(argument)
			if found && prop.Name == propName {
				return true
			}
			continue
		}

		modelPropName := component.ModelProp
		if modelPropName == "" && component.ModelEvent != "" {
			modelPropName = "value"
		}
		if modelPropName == "" {
			if _, found := component.ComponentProp("modelValue"); found {
				modelPropName = "modelValue"
			} else if _, found := component.ComponentProp("value"); found {
				modelPropName = "value"
			}
		}
		prop, found := component.ComponentProp(modelPropName)
		if found && prop.Name == propName {
			return true
		}
	}
	return false
}

func (p *AdminAnalyzer) checkBoundPropTypes(
	root *twigsyntax.Node,
	content []byte,
	tagName string,
	startTag *twigsyntax.Node,
	props map[string]admin.VueComponentProp,
	templatePath string,
	liveOwner *admin.VueComponent,
	diagnostics *[]lsp.Problem,
) error {
	if p == nil || p.adminIndexer == nil || root == nil {
		return nil
	}
	for attributeNode := range twigquery.IterateNodes(
		startTag, twigsyntax.HtmlAttribute,
	) {
		attributeName := twigquery.HTMLAttributeName(attributeNode)
		if attributeName == "v-bind" {
			if err := p.checkObjectBoundPropTypes(
				root, content, tagName, attributeNode, props,
				templatePath, liveOwner, diagnostics,
			); err != nil {
				return err
			}
			continue
		}
		if !strings.HasPrefix(attributeName, ":") &&
			!strings.HasPrefix(attributeName, "v-bind:") {
			continue
		}
		propName := admin.NormalizePropName(attributeName)
		prop, exists := props[propName]
		if !exists {
			continue
		}
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok {
			continue
		}
		value, ok := attribute.Value()
		if !ok {
			continue
		}
		inner, ok := value.GetInner()
		if !ok {
			continue
		}
		expressionRange := inner.Syntax().RangeTrimmedTrivia()
		if expressionRange.Len() == 0 || expressionRange.End > uint32(len(content)) {
			continue
		}
		expression := strings.TrimSpace(inner.Syntax().Text())
		if actual, start, end, literal := admin.VueStaticStringLiteral(
			inner.Syntax().Text(),
		); literal {
			literalRange := cst.TextRange{
				Start: inner.Syntax().Range().Start + start,
				End:   inner.Syntax().Range().Start + end,
			}
			if problem, invalid := invalidAdminPropValueProblem(
				tagName, prop, actual, literalRange,
			); invalid {
				*diagnostics = append(*diagnostics, problem)
				continue
			}
		}
		if strings.TrimSpace(prop.Type) == "" {
			continue
		}
		actualType, resolved, err :=
			p.adminIndexer.ResolveTwigVueExpressionTypeForComponent(
				root, content, expression, expressionRange.Start,
				templatePath, liveOwner,
			)
		if err != nil {
			return err
		}
		if !resolved || !admin.VueTypesProvablyIncompatible(
			prop.Type, actualType,
		) {
			continue
		}
		expectedType := admin.VuePropValueType(prop.Type)
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: expressionRange,
			Message: fmt.Sprintf(
				"Prop '%s' on component '%s' expects %s, but the bound expression has type %s",
				prop.Name, tagName, expectedType, actualType,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.bound-prop-type",
			Payload: map[string]any{
				"componentName": tagName,
				"propName":      prop.Name,
				"expectedType":  expectedType,
				"actualType":    actualType,
			},
		})
	}
	return nil
}

func (p *AdminAnalyzer) checkObjectBoundPropTypes(
	root *twigsyntax.Node,
	content []byte,
	tagName string,
	attributeNode *twigsyntax.Node,
	props map[string]admin.VueComponentProp,
	templatePath string,
	liveOwner *admin.VueComponent,
	diagnostics *[]lsp.Problem,
) error {
	attribute, ok := twigast.CastHtmlAttribute(attributeNode)
	if !ok {
		return nil
	}
	value, ok := attribute.Value()
	if !ok {
		return nil
	}
	inner, ok := value.GetInner()
	if !ok {
		return nil
	}
	fields, _ := admin.VueObjectBindingFields(
		inner.Syntax().Text(), inner.Syntax().Range().Start,
	)
	for _, field := range fields {
		propName := admin.NormalizePropName(field.Name)
		prop, exists := props[propName]
		if !exists || field.Expression == "" || field.ExpressionRange.Len() == 0 {
			continue
		}
		if actual, start, end, literal := admin.VueStaticStringLiteral(
			field.Expression,
		); literal {
			literalRange := cst.TextRange{
				Start: field.ExpressionRange.Start + start,
				End:   field.ExpressionRange.Start + end,
			}
			if problem, invalid := invalidAdminPropValueProblem(
				tagName, prop, actual, literalRange,
			); invalid {
				*diagnostics = append(*diagnostics, problem)
				continue
			}
		}
		if strings.TrimSpace(prop.Type) == "" {
			continue
		}
		actualType, resolved, err :=
			p.adminIndexer.ResolveTwigVueExpressionTypeForComponent(
				root, content, field.Expression, field.ExpressionRange.Start,
				templatePath, liveOwner,
			)
		if err != nil {
			return err
		}
		if !resolved || !admin.VueTypesProvablyIncompatible(
			prop.Type, actualType,
		) {
			continue
		}
		expectedType := admin.VuePropValueType(prop.Type)
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: field.ExpressionRange,
			Message: fmt.Sprintf(
				"Prop '%s' on component '%s' expects %s, but the v-bind field has type %s",
				prop.Name, tagName, expectedType, actualType,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.bound-prop-type",
			Payload: map[string]any{
				"componentName": tagName,
				"propName":      prop.Name,
				"expectedType":  expectedType,
				"actualType":    actualType,
			},
		})
	}
	return nil
}

func (p *AdminAnalyzer) checkUnknownComponent(
	tagName string,
	startTag *twigsyntax.Node,
	diagnostics *[]lsp.Problem,
) {
	if p == nil || p.adminIndexer == nil ||
		!admin.IsShopwareComponentTag(tagName) {
		return
	}
	if _, suppressed := p.suppressedComponentDiagnostics[tagName]; suppressed {
		return
	}
	components, err := p.adminIndexer.GetAllComponentsView()
	if err != nil || len(components) == 0 {
		// During an initial or partial index there is not enough evidence to
		// claim that a registry-owned tag is missing.
		return
	}
	names := make([]string, 0, len(components))
	seen := make(map[string]struct{}, len(components))
	for _, component := range components {
		name := strings.TrimSpace(component.Name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	nameNode := p.getTagNameNode(startTag)
	if nameNode == nil {
		return
	}
	*diagnostics = append(*diagnostics, lsp.Problem{
		Range: nameNode.Range(),
		Message: fmt.Sprintf(
			"Administration component '%s' is not registered",
			tagName,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.component.not-found",
		Payload: map[string]any{
			"componentName": tagName,
			"suggestions":   suggestion.Similar(tagName, names),
		},
	})
}

func (p *AdminAnalyzer) checkStaticPropTypes(
	tagName string,
	startTag *twigsyntax.Node,
	props map[string]admin.VueComponentProp,
	diagnostics *[]lsp.Problem,
) {
	for attribute := range twigquery.IterateNodes(startTag, twigsyntax.HtmlAttribute) {
		attributeName := twigquery.HTMLAttributeName(attribute)
		if strings.HasPrefix(attributeName, ":") ||
			strings.HasPrefix(attributeName, "v-bind:") {
			continue
		}
		propName := admin.NormalizePropName(attributeName)
		prop, exists := props[propName]
		if !exists || !staticPropNeedsBinding(prop, attributeName, attribute.Text()) {
			continue
		}
		*diagnostics = append(*diagnostics, lsp.Problem{
			Range: attribute.RangeTrimmedTrivia(),
			Message: fmt.Sprintf(
				"Prop '%s' on component '%s' expects %s; bind the value with ':'",
				prop.Name,
				tagName,
				prop.Type,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.static-prop-type",
			Payload: map[string]any{
				"componentName": tagName,
				"propName":      prop.Name,
				"attributeName": attributeName,
			},
		})
	}
}

func (p *AdminAnalyzer) checkStaticPropValues(
	tagName string,
	startTag *twigsyntax.Node,
	props map[string]admin.VueComponentProp,
	diagnostics *[]lsp.Problem,
) {
	for attributeNode := range twigquery.IterateNodes(
		startTag, twigsyntax.HtmlAttribute,
	) {
		attributeName := twigquery.HTMLAttributeName(attributeNode)
		if attributeName == "" || strings.HasPrefix(attributeName, ":") ||
			strings.HasPrefix(attributeName, "v-") ||
			strings.HasPrefix(attributeName, "@") ||
			strings.HasPrefix(attributeName, "#") {
			continue
		}
		prop, exists := props[admin.NormalizePropName(attributeName)]
		if !exists {
			continue
		}
		allowed, complete := admin.VuePropAllowedValues(prop)
		if !complete || len(allowed) == 0 {
			continue
		}
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok {
			continue
		}
		value, ok := attribute.Value()
		if !ok {
			continue
		}
		actual := ""
		var valueRange cst.TextRange
		if inner, innerOK := value.GetInner(); innerOK {
			actual = inner.Syntax().Text()
			valueRange = inner.Syntax().Range()
		} else {
			opening := value.GetOpeningQuote()
			closing := value.GetClosingQuote()
			if opening == nil || closing == nil {
				continue
			}
			valueRange = cst.TextRange{
				Start: opening.Range().End, End: closing.Range().Start,
			}
		}
		if strings.Contains(actual, "{{") || strings.Contains(actual, "{%") ||
			strings.Contains(actual, "{#") {
			continue
		}
		valid := false
		for _, candidate := range allowed {
			if actual == candidate {
				valid = true
				break
			}
		}
		if valid {
			continue
		}
		problem, _ := invalidAdminPropValueProblem(
			tagName, prop, actual, valueRange,
		)
		*diagnostics = append(*diagnostics, problem)
	}
}

func invalidAdminPropValueProblem(
	tagName string,
	prop admin.VueComponentProp,
	actual string,
	rangeValue cst.TextRange,
) (lsp.Problem, bool) {
	allowed, complete := admin.VuePropAllowedValues(prop)
	if !complete || len(allowed) == 0 {
		return lsp.Problem{}, false
	}
	for _, candidate := range allowed {
		if actual == candidate {
			return lsp.Problem{}, false
		}
	}
	return lsp.Problem{
		Range: rangeValue,
		Message: fmt.Sprintf(
			"Prop '%s' on component '%s' does not accept value %q",
			prop.Name, tagName, actual,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.component.invalid-prop-value",
		Payload: map[string]any{
			"componentName": tagName,
			"propName":      prop.Name,
			"value":         actual,
			"allowedValues": allowed,
			"suggestions":   suggestion.Similar(actual, allowed),
		},
	}, true
}

func staticPropNeedsBinding(
	prop admin.VueComponentProp,
	attributeName,
	attributeText string,
) bool {
	propType := strings.ToLower(strings.TrimSpace(prop.Type))
	if propType == "" || strings.Contains(propType, "string") {
		return false
	}
	if !strings.Contains(attributeText, "=") {
		return false
	}
	switch {
	case strings.Contains(propType, "boolean"):
		value := strings.TrimSpace(attributeText[strings.IndexByte(attributeText, '=')+1:])
		value = strings.Trim(value, "'\"")
		return value != "" && value != attributeName
	case strings.Contains(propType, "number"),
		strings.Contains(propType, "array"),
		strings.Contains(propType, "object"),
		strings.Contains(propType, "function"):
		return true
	default:
		return false
	}
}

// getTagName extracts the tag name from an html_start_tag node
func (p *AdminAnalyzer) getTagName(startTag *twigsyntax.Node) string {
	return twigquery.HTMLTagName(startTag)
}

// getTagNameNode returns the html_tag_name node from an html_start_tag
func (p *AdminAnalyzer) getTagNameNode(startTag *twigsyntax.Node) *twigsyntax.Token {
	tag, ok := twigast.CastHtmlStartingTag(startTag)
	if !ok {
		return nil
	}
	return tag.Name()
}

// getTagAttributes extracts all attribute names from an html_start_tag
func (p *AdminAnalyzer) getTagAttributes(
	startTag *twigsyntax.Node,
) (map[string]bool, bool) {
	attrs := make(map[string]bool)
	hasUnknownObjectBinding := false

	for attribute := range twigquery.IterateNodes(startTag, twigsyntax.HtmlAttribute) {
		attrName := p.getAttributeName(attribute)
		if attrName != "" {
			attrs[attrName] = true
		}
		if attrName != "v-bind" {
			continue
		}
		value, ok := twigast.CastHtmlAttribute(attribute)
		if !ok {
			continue
		}
		attributeValue, ok := value.Value()
		if !ok {
			continue
		}
		inner, ok := attributeValue.GetInner()
		if !ok {
			continue
		}
		names, complete := admin.VueObjectBindingNames(
			strings.TrimSpace(inner.Syntax().Text()),
		)
		for _, name := range names {
			attrs[name] = true
		}
		if !complete {
			hasUnknownObjectBinding = true
		}
	}

	return attrs, hasUnknownObjectBinding
}

// getAttributeName extracts the attribute name from an html_attribute node
func (p *AdminAnalyzer) getAttributeName(attrNode *twigsyntax.Node) string {
	return twigquery.HTMLAttributeName(attrNode)
}

// isPropPresent checks if a prop is present in the attributes
// It checks for the prop name directly, as well as Vue binding variants (:prop, v-bind:prop)
// Also handles camelCase to kebab-case conversion (positionIdentifier -> position-identifier)
func (p *AdminAnalyzer) isPropPresent(propName string, attrs map[string]bool) bool {
	// Get both camelCase and kebab-case versions
	kebabName := camelToKebab(propName)

	// Check both variants
	namesToCheck := []string{propName, kebabName}

	for _, name := range namesToCheck {
		// Direct attribute
		if attrs[name] {
			return true
		}

		// Vue shorthand binding :propName
		if attrs[":"+name] {
			return true
		}

		// Vue v-bind:propName
		if attrs["v-bind:"+name] {
			return true
		}
	}

	return false
}

// camelToKebab converts camelCase to kebab-case (delegates to shared function)
func camelToKebab(s string) string {
	return admin.CamelToKebab(s)
}
