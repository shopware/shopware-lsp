package twigcomponent

import (
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

var propDocumentationPattern = regexp.MustCompile(
	`(?m)@prop\s+([A-Za-z_][A-Za-z0-9_]*)\s+(\S+)(?:\s+([^\r\n#]+))?`,
)

func usagesInTwig(
	path string,
	root *twigsyntax.Node,
) []Usage {
	if root == nil {
		return nil
	}
	var result []Usage
	for _, call := range twigquery.Nodes(
		root,
		twigsyntax.TwigFunctionCall,
	) {
		if !strings.EqualFold(
			twigquery.FunctionName(call),
			"component",
		) {
			continue
		}
		literal := twigquery.StringArgument(call, 0)
		if literal == nil || !twigquery.StringIsStatic(literal) {
			continue
		}
		name := normalizeComponentName(twigquery.StringValue(literal))
		if name == "" {
			continue
		}
		result = append(result, Usage{
			Name:  name,
			File:  path,
			Range: twigStringValueRange(literal),
			Kind:  FunctionUsage,
		})
	}
	for _, start := range twigquery.Nodes(
		root,
		twigsyntax.TwigComponentStartingBlock,
	) {
		name, rng := blockComponentName(start)
		if name == "" {
			continue
		}
		result = append(result, Usage{
			Name:  name,
			File:  path,
			Range: rng,
			Kind:  BlockUsage,
		})
	}
	for _, start := range twigquery.Nodes(
		root,
		twigsyntax.HtmlStartingTag,
	) {
		tag, ok := twigast.CastHtmlStartingTag(start)
		if !ok || !tag.IsTwigComponent() || tag.Name() == nil {
			continue
		}
		text := tag.Name().Text()
		if !strings.HasPrefix(strings.ToLower(text), "twig:") ||
			len(text) <= len("twig:") {
			continue
		}
		componentName := normalizeComponentName(text[len("twig:"):])
		if strings.EqualFold(componentName, "block") {
			continue
		}
		rng := tag.Name().Range()
		rng.Start += uint32(len("twig:"))
		result = append(result, Usage{
			Name:  componentName,
			File:  path,
			Range: rng,
			Kind:  HTMLUsage,
		})
	}
	return uniqueUsages(result)
}

func liveActionReferencesInTwig(
	path string,
	root *twigsyntax.Node,
) []LiveActionReference {
	if root == nil {
		return nil
	}
	var result []LiveActionReference
	for _, call := range twigquery.Nodes(
		root,
		twigsyntax.TwigFunctionCall,
	) {
		if !strings.EqualFold(
			twigquery.FunctionName(call),
			"live_action",
		) {
			continue
		}
		literal := twigquery.StringArgument(call, 0)
		if literal == nil || !twigquery.StringIsStatic(literal) {
			continue
		}
		contextRange := twigStringValueRange(literal)
		result = append(result, LiveActionReference{
			Name:         strings.TrimSpace(twigquery.StringValue(literal)),
			File:         path,
			Range:        contextRange,
			ContextRange: contextRange,
			Kind:         LiveActionHelperReference,
		})
	}
	for _, node := range twigquery.Nodes(
		root,
		twigsyntax.HtmlAttribute,
	) {
		if reference, found := liveActionReferenceFromHTMLAttribute(
			path,
			node,
		); found {
			result = append(result, reference)
		}
	}
	return uniqueLiveActionReferences(result)
}

func liveActionReferenceFromHTMLAttribute(
	path string,
	node *twigsyntax.Node,
) (LiveActionReference, bool) {
	if !strings.EqualFold(
		twigquery.HTMLAttributeName(node),
		"data-live-action-param",
	) {
		return LiveActionReference{}, false
	}
	attribute, ok := twigast.CastHtmlAttribute(node)
	if !ok {
		return LiveActionReference{}, false
	}
	value, ok := attribute.Value()
	if !ok {
		return LiveActionReference{}, false
	}
	contextRange, text, static := htmlStringStaticValue(value)
	if !static {
		return LiveActionReference{}, false
	}
	name, rng := liveActionDirectiveName(text, contextRange)
	return LiveActionReference{
		Name:         name,
		File:         path,
		Range:        rng,
		ContextRange: contextRange,
		Kind:         LiveActionAttributeReference,
	}, true
}

func LiveActionReferencesInTwig(
	path string,
	root *twigsyntax.Node,
) []LiveActionReference {
	return liveActionReferencesInTwig(path, root)
}

func LiveActionReferenceAt(
	path string,
	root *twigsyntax.Node,
	offset uint32,
) (LiveActionReference, bool) {
	for _, reference := range liveActionReferencesInTwig(path, root) {
		if rangeContainsCursor(reference.Range, offset) ||
			reference.Range.Len() == 0 &&
				offset == reference.Range.Start {
			return reference, true
		}
	}
	return LiveActionReference{}, false
}

func LiveActionArgumentReferencesInTwig(
	path string,
	root *twigsyntax.Node,
) []LiveActionArgumentReference {
	if root == nil {
		return nil
	}
	var result []LiveActionArgumentReference
	for _, node := range twigquery.Nodes(
		root,
		twigsyntax.HtmlStartingTag,
	) {
		tag, ok := twigast.CastHtmlStartingTag(node)
		if !ok {
			continue
		}
		var action LiveActionReference
		var arguments []LiveActionArgumentReference
		for _, attribute := range tag.Attributes() {
			attributeNode := attribute.Syntax()
			if reference, found := liveActionReferenceFromHTMLAttribute(
				path,
				attributeNode,
			); found {
				action = reference
				continue
			}
			name, rng, found := liveActionArgumentAttribute(
				attributeNode,
			)
			if !found {
				continue
			}
			arguments = append(arguments, LiveActionArgumentReference{
				Name:  name,
				File:  path,
				Range: rng,
			})
		}
		if action.Name == "" {
			continue
		}
		for index := range arguments {
			arguments[index].Action = action.Name
			result = append(result, arguments[index])
		}
	}
	for _, call := range twigquery.Nodes(
		root,
		twigsyntax.TwigFunctionCall,
	) {
		if !strings.EqualFold(
			twigquery.FunctionName(call),
			"live_action",
		) {
			continue
		}
		action := strings.TrimSpace(
			twigquery.StringValue(twigquery.StringArgument(call, 0)),
		)
		if action == "" {
			continue
		}
		for _, key := range twigquery.Nodes(
			call,
			twigsyntax.TwigLiteralHashKey,
		) {
			hash := twigquery.HashAt(key)
			if hash == nil ||
				twigquery.FunctionCallAt(hash) != call ||
				twigquery.FunctionArgumentIndex(hash) != 1 {
				continue
			}
			name, rng := twigHashKeyNameRange(key)
			if name == "" {
				continue
			}
			result = append(result, LiveActionArgumentReference{
				Action: action,
				Name:   name,
				File:   path,
				Range:  rng,
			})
		}
	}
	return uniqueLiveActionArgumentReferences(result)
}

func LiveActionArgumentReferenceAt(
	path string,
	root *twigsyntax.Node,
	offset uint32,
) (LiveActionArgumentReference, bool) {
	for _, reference := range LiveActionArgumentReferencesInTwig(path, root) {
		if rangeContainsCursor(reference.Range, offset) {
			return reference, true
		}
	}
	return LiveActionArgumentReference{}, false
}

func LiveActionArgumentContext(
	node *twigsyntax.Node,
) (string, []string, bool) {
	hash := twigquery.HashAt(node)
	if hash == nil {
		return "", nil, false
	}
	call := twigquery.FunctionCallAt(hash)
	if call == nil ||
		!strings.EqualFold(twigquery.FunctionName(call), "live_action") ||
		twigquery.FunctionArgumentIndex(hash) != 1 {
		return "", nil, false
	}
	if pair := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigLiteralHashPair,
	); pair != nil && twigquery.HashKeyAt(node) == nil {
		return "", nil, false
	}
	action := strings.TrimSpace(
		twigquery.StringValue(twigquery.StringArgument(call, 0)),
	)
	if action == "" {
		return "", nil, false
	}
	var present []string
	for _, key := range twigquery.Nodes(
		hash,
		twigsyntax.TwigLiteralHashKey,
	) {
		name, _ := twigHashKeyNameRange(key)
		if name != "" {
			present = append(present, name)
		}
	}
	return action, present, true
}

func liveEventReferencesInTwig(
	path string,
	root *twigsyntax.Node,
) ([]LiveEventReference, []LiveEventArgumentReference) {
	if root == nil {
		return nil, nil
	}
	var references []LiveEventReference
	var arguments []LiveEventArgumentReference
	for _, node := range twigquery.Nodes(
		root,
		twigsyntax.HtmlStartingTag,
	) {
		tag, ok := twigast.CastHtmlStartingTag(node)
		if !ok {
			continue
		}
		var actionKind LiveEventReferenceKind
		actionFound := false
		var eventName string
		var eventRange cst.TextRange
		var contextRange cst.TextRange
		var currentArguments []LiveEventArgumentReference
		for _, attribute := range tag.Attributes() {
			attributeNode := attribute.Syntax()
			attributeName := strings.ToLower(
				twigquery.HTMLAttributeName(attributeNode),
			)
			switch attributeName {
			case "data-action":
				value, valueFound := attribute.Value()
				if !valueFound {
					continue
				}
				_, text, static := htmlStringStaticValue(value)
				if !static {
					continue
				}
				actionKind, actionFound =
					liveEventAttributeActionKind(text)
			case "data-live-event-param":
				value, valueFound := attribute.Value()
				if !valueFound {
					continue
				}
				contextRange, text, static :=
					htmlStringStaticValue(value)
				if !static {
					continue
				}
				eventName, eventRange = liveActionDirectiveName(
					text,
					contextRange,
				)
			default:
				name, rng, found := liveEventArgumentAttribute(
					attributeNode,
				)
				if found {
					currentArguments = append(
						currentArguments,
						LiveEventArgumentReference{
							Name:  name,
							File:  path,
							Range: rng,
						},
					)
				}
			}
		}
		if !actionFound || eventName == "" {
			continue
		}
		references = append(references, LiveEventReference{
			Name:         eventName,
			File:         path,
			Range:        eventRange,
			ContextRange: contextRange,
			Kind:         actionKind,
		})
		for index := range currentArguments {
			currentArguments[index].Event = eventName
			arguments = append(arguments, currentArguments[index])
		}
	}
	return uniqueLiveEventReferences(references),
		uniqueLiveEventArgumentReferences(arguments)
}

func liveEventAttributeActionKind(
	value string,
) (LiveEventReferenceKind, bool) {
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "live#emitself"):
		return LiveEventEmitSelfReference, true
	case strings.Contains(lower, "live#emitup"):
		return LiveEventEmitUpReference, true
	case strings.Contains(lower, "live#emit"):
		return LiveEventAttributeReference, true
	default:
		return 0, false
	}
}

func liveEventArgumentAttribute(
	node *twigsyntax.Node,
) (string, cst.TextRange, bool) {
	name, rng, found := liveActionArgumentAttribute(node)
	if !found || strings.EqualFold(name, "event") {
		return "", cst.TextRange{}, false
	}
	return name, rng, true
}

func LiveEventReferencesInTwig(
	path string,
	root *twigsyntax.Node,
) []LiveEventReference {
	references, _ := liveEventReferencesInTwig(path, root)
	return references
}

func LiveEventReferenceAtTwig(
	path string,
	root *twigsyntax.Node,
	offset uint32,
) (LiveEventReference, bool) {
	for _, reference := range LiveEventReferencesInTwig(path, root) {
		if rangeContainsCursor(reference.Range, offset) ||
			reference.Range.Len() == 0 && reference.Range.Start == offset {
			return reference, true
		}
	}
	return LiveEventReference{}, false
}

func LiveEventArgumentReferencesInTwig(
	path string,
	root *twigsyntax.Node,
) []LiveEventArgumentReference {
	_, arguments := liveEventReferencesInTwig(path, root)
	return arguments
}

func LiveEventArgumentReferenceAtTwig(
	path string,
	root *twigsyntax.Node,
	offset uint32,
) (LiveEventArgumentReference, bool) {
	for _, reference := range LiveEventArgumentReferencesInTwig(path, root) {
		if rangeContainsCursor(reference.Range, offset) {
			return reference, true
		}
	}
	return LiveEventArgumentReference{}, false
}

func twigHashKeyNameRange(
	key *twigsyntax.Node,
) (string, cst.TextRange) {
	if key == nil {
		return "", cst.TextRange{}
	}
	if literal := twigquery.LiteralStringAt(key); literal != nil {
		name := strings.TrimSpace(twigquery.StringValue(literal))
		if name != "" {
			return name, twigStringValueRange(literal)
		}
	}
	text := strings.TrimSpace(key.Text())
	name := strings.TrimSpace(strings.TrimSuffix(text, ":"))
	name = strings.Trim(name, "'\"`")
	if name == "" {
		return "", cst.TextRange{}
	}
	rng := key.RangeTrimmedTrivia()
	if start := strings.Index(text, name); start >= 0 {
		rng.Start += uint32(start)
		rng.End = rng.Start + uint32(len(name))
	}
	return name, rng
}

func liveActionArgumentAttribute(
	node *twigsyntax.Node,
) (string, cst.TextRange, bool) {
	raw := strings.ToLower(twigquery.HTMLAttributeName(node))
	const prefix = "data-live-"
	const suffix = "-param"
	if !strings.HasPrefix(raw, prefix) ||
		!strings.HasSuffix(raw, suffix) {
		return "", cst.TextRange{}, false
	}
	middle := raw[len(prefix) : len(raw)-len(suffix)]
	if middle == "" || middle == "action" || middle == "event" {
		return "", cst.TextRange{}, false
	}
	for _, char := range middle {
		if char != '-' && char != '_' &&
			(char < 'a' || char > 'z') &&
			(char < '0' || char > '9') {
			return "", cst.TextRange{}, false
		}
	}
	text := strings.TrimSpace(node.Text())
	start := strings.Index(strings.ToLower(text), raw)
	if start < 0 {
		return "", cst.TextRange{}, false
	}
	rng := node.RangeTrimmedTrivia()
	rng.Start += uint32(start + len(prefix))
	rng.End = rng.Start + uint32(len(middle))
	return liveArgumentName(middle), rng, true
}

func liveArgumentName(value string) string {
	segments := strings.FieldsFunc(value, func(char rune) bool {
		return char == '-' || char == '_'
	})
	if len(segments) == 0 {
		return ""
	}
	var result strings.Builder
	result.WriteString(strings.ToLower(segments[0]))
	for _, segment := range segments[1:] {
		if segment == "" {
			continue
		}
		result.WriteString(strings.ToUpper(segment[:1]))
		result.WriteString(strings.ToLower(segment[1:]))
	}
	return result.String()
}

func htmlStringStaticValue(
	value twigast.HtmlString,
) (cst.TextRange, string, bool) {
	node := value.Syntax()
	if node == nil {
		return cst.TextRange{}, "", false
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] {
		rng.Start++
		rng.End--
		text = text[1 : len(text)-1]
	}
	if strings.Contains(text, "{{") ||
		strings.Contains(text, "{%") ||
		strings.Contains(text, "{#") {
		return cst.TextRange{}, "", false
	}
	return rng, text, true
}

func liveActionDirectiveName(
	value string,
	valueRange cst.TextRange,
) (string, cst.TextRange) {
	start := 0
	if separator := strings.LastIndexByte(value, '|'); separator >= 0 {
		start = separator + 1
	}
	for start < len(value) &&
		(value[start] == ' ' || value[start] == '\t' ||
			value[start] == '\r' || value[start] == '\n') {
		start++
	}
	end := len(value)
	for end > start &&
		(value[end-1] == ' ' || value[end-1] == '\t' ||
			value[end-1] == '\r' || value[end-1] == '\n') {
		end--
	}
	rng := cst.TextRange{
		Start: valueRange.Start + uint32(start),
		End:   valueRange.Start + uint32(end),
	}
	name := value[start:end]
	if name == "" {
		return "", rng
	}
	for index, char := range name {
		if index == 0 {
			if char != '_' &&
				(char < 'A' || char > 'Z') &&
				(char < 'a' || char > 'z') {
				return "", rng
			}
			continue
		}
		if char != '_' &&
			(char < 'A' || char > 'Z') &&
			(char < 'a' || char > 'z') &&
			(char < '0' || char > '9') {
			return "", rng
		}
	}
	return name, rng
}

func BlockUsageAt(
	node *twigsyntax.Node,
	offset uint32,
) (ComponentBlockUsage, bool) {
	attribute := twigquery.HTMLAttributeAt(node)
	if attribute == nil ||
		!strings.EqualFold(
			strings.TrimPrefix(
				twigquery.HTMLAttributeName(attribute),
				":",
			),
			"name",
		) {
		return ComponentBlockUsage{}, false
	}
	tagNode := twigquery.StartingHTMLTagAt(attribute)
	tag, ok := twigast.CastHtmlStartingTag(tagNode)
	if !ok || tag.Name() == nil ||
		!strings.EqualFold(tag.Name().Text(), "twig:block") {
		return ComponentBlockUsage{}, false
	}
	value := firstDescendant(attribute, twigsyntax.HtmlString)
	if value == nil {
		return ComponentBlockUsage{}, false
	}
	stringValue, ok := twigast.CastHtmlString(value)
	if !ok {
		return ComponentBlockUsage{}, false
	}
	inner, ok := stringValue.GetInner()
	if !ok {
		return ComponentBlockUsage{}, false
	}
	rng := inner.Syntax().RangeTrimmedTrivia()
	if !rangeContainsCursor(rng, offset) {
		return ComponentBlockUsage{}, false
	}
	component := enclosingHTMLComponent(tagNode)
	if component == "" {
		return ComponentBlockUsage{}, false
	}
	return ComponentBlockUsage{
		Component: component,
		Name:      inner.Syntax().Text(),
		Range:     rng,
	}, true
}

func BlockUsagesInTwig(
	root *twigsyntax.Node,
) []ComponentBlockUsage {
	if root == nil {
		return nil
	}
	var result []ComponentBlockUsage
	for _, attribute := range twigquery.Nodes(
		root,
		twigsyntax.HtmlAttribute,
	) {
		value := firstDescendant(attribute, twigsyntax.HtmlString)
		if value == nil {
			continue
		}
		stringValue, ok := twigast.CastHtmlString(value)
		if !ok {
			continue
		}
		inner, ok := stringValue.GetInner()
		if !ok {
			continue
		}
		usage, found := BlockUsageAt(
			attribute,
			inner.Syntax().Range().Start,
		)
		if found {
			result = append(result, usage)
		}
	}
	return result
}

func EnclosingHTMLComponent(
	node *twigsyntax.Node,
) string {
	return enclosingHTMLComponent(node)
}

func enclosingHTMLComponent(node *twigsyntax.Node) string {
	for current := node.Parent(); current != nil; current = current.Parent() {
		tag, ok := twigast.CastHtmlTag(current)
		if !ok {
			continue
		}
		start, ok := tag.StartingTag()
		if !ok || !start.IsTwigComponent() || start.Name() == nil {
			continue
		}
		name := start.Name().Text()
		if !strings.HasPrefix(strings.ToLower(name), "twig:") {
			continue
		}
		name = normalizeComponentName(name[len("twig:"):])
		if !strings.EqualFold(name, "block") {
			return name
		}
	}
	return ""
}

func UsagesInTwig(
	path string,
	root *twigsyntax.Node,
) []Usage {
	return usagesInTwig(path, root)
}

func UsageAt(
	path string,
	root *twigsyntax.Node,
	offset uint32,
) (Usage, bool) {
	for _, usage := range usagesInTwig(path, root) {
		if rangeContainsCursor(usage.Range, offset) {
			return usage, true
		}
	}
	return Usage{}, false
}

func PropUsageAt(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	offset uint32,
) (PropUsage, bool) {
	attribute := twigquery.HTMLAttributeAt(node)
	if attribute == nil {
		return PropUsage{}, false
	}
	tagNode := twigquery.StartingHTMLTagAt(attribute)
	tag, ok := twigast.CastHtmlStartingTag(tagNode)
	if !ok || !tag.IsTwigComponent() || tag.Name() == nil {
		return PropUsage{}, false
	}
	tagName := tag.Name().Text()
	if !strings.HasPrefix(strings.ToLower(tagName), "twig:") {
		return PropUsage{}, false
	}
	rawName := twigquery.HTMLAttributeName(attribute)
	name := strings.TrimPrefix(rawName, ":")
	if name == "" {
		return PropUsage{}, false
	}
	rng := attribute.RangeTrimmedTrivia()
	text := strings.TrimSpace(attribute.Text())
	if start := strings.Index(text, rawName); start >= 0 {
		rng.Start += uint32(start)
		rng.End = rng.Start + uint32(len(rawName))
	}
	if !rangeContainsCursor(rng, offset) {
		return PropUsage{}, false
	}
	return PropUsage{
		Component: normalizeComponentName(tagName[len("twig:"):]),
		Name:      name,
		Range:     rng,
		Dynamic:   strings.HasPrefix(rawName, ":"),
	}, true
}

func VariableAt(
	node *twigsyntax.Node,
) (string, cst.TextRange, bool) {
	nameNode := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigLiteralName,
	)
	if nameNode == nil ||
		twigquery.ClosestNodeOfKind(
			nameNode,
			twigsyntax.TwigVar,
		) == nil {
		return "", cst.TextRange{}, false
	}
	if accessor := twigquery.ClosestNodeOfKind(
		nameNode,
		twigsyntax.TwigAccessor,
	); accessor != nil {
		names := twigquery.Nodes(
			accessor,
			twigsyntax.TwigLiteralName,
		)
		if len(names) == 0 || names[0] != nameNode {
			return "", cst.TextRange{}, false
		}
	}
	name := strings.TrimSpace(nameNode.Text())
	return name, nameNode.RangeTrimmedTrivia(), name != ""
}

// AccessorMemberAt returns the root and first member for a simple Twig
// accessor such as `computed.total`. Deeper members are not attributed to the
// component object because their receiver is the getter result.
func AccessorMemberAt(
	node *twigsyntax.Node,
) (string, string, cst.TextRange, bool) {
	nameNode := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigLiteralName,
	)
	accessor := twigquery.ClosestNodeOfKind(
		nameNode,
		twigsyntax.TwigAccessor,
	)
	if nameNode == nil || accessor == nil ||
		twigquery.ClosestNodeOfKind(
			accessor,
			twigsyntax.TwigVar,
		) == nil {
		return "", "", cst.TextRange{}, false
	}
	names := twigquery.Nodes(accessor, twigsyntax.TwigLiteralName)
	if len(names) < 2 || names[1] != nameNode {
		return "", "", cst.TextRange{}, false
	}
	root := strings.TrimSpace(names[0].Text())
	member := strings.TrimSpace(names[1].Text())
	return root, member, names[1].RangeTrimmedTrivia(),
		root != "" && member != ""
}

func propsInTwig(
	path string,
	root *twigsyntax.Node,
) []Prop {
	if root == nil {
		return nil
	}
	documentation := propDocumentation(root.Text())
	var result []Prop
	for _, declaration := range twigquery.Nodes(
		root,
		twigsyntax.TwigPropDeclaration,
	) {
		nameNode := firstDescendant(
			declaration,
			twigsyntax.TwigLiteralName,
		)
		if nameNode == nil {
			continue
		}
		name := strings.TrimSpace(nameNode.Text())
		if name == "" {
			continue
		}
		prop := Prop{
			Name:  name,
			File:  path,
			Range: nameNode.RangeTrimmedTrivia(),
		}
		if documented, exists := documentation[name]; exists {
			prop.Type = documented.Type
			prop.Description = documented.Description
		}
		if equal := strings.Index(declaration.Text(), "="); equal >= 0 {
			prop.DefaultValue = strings.TrimSpace(
				declaration.Text()[equal+1:],
			)
		}
		result = append(result, prop)
	}
	return uniqueProps(result)
}

func PropsInTwig(
	path string,
	root *twigsyntax.Node,
) []Prop {
	return propsInTwig(path, root)
}

func blockComponentName(
	start *twigsyntax.Node,
) (string, cst.TextRange) {
	if start == nil {
		return "", cst.TextRange{}
	}
	literal := firstDescendant(
		start,
		twigsyntax.TwigLiteralString,
	)
	name := firstDescendant(
		start,
		twigsyntax.TwigLiteralName,
	)
	if literal != nil &&
		(name == nil || literal.Range().Start < name.Range().Start) {
		return normalizeComponentName(twigquery.StringValue(literal)),
			twigStringValueRange(literal)
	}
	if name != nil {
		return normalizeComponentName(name.Text()),
			name.RangeTrimmedTrivia()
	}
	return "", cst.TextRange{}
}

func twigStringValueRange(node *twigsyntax.Node) cst.TextRange {
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

func firstDescendant(
	node *twigsyntax.Node,
	kind twigsyntax.Kind,
) *twigsyntax.Node {
	if node == nil {
		return nil
	}
	for element := range node.Descendants() {
		child, ok := element.(*twigsyntax.Node)
		if ok && child.Kind() == kind {
			return child
		}
	}
	return nil
}

type documentedProp struct {
	Type        string
	Description string
}

func propDocumentation(source string) map[string]documentedProp {
	result := make(map[string]documentedProp)
	for _, match := range propDocumentationPattern.FindAllStringSubmatch(source, -1) {
		if len(match) < 3 {
			continue
		}
		description := ""
		if len(match) > 3 {
			description = strings.TrimSpace(match[3])
		}
		result[match[1]] = documentedProp{
			Type:        match[2],
			Description: description,
		}
	}
	return result
}

func uniqueUsages(usages []Usage) []Usage {
	seen := make(map[string]struct{}, len(usages))
	result := make([]Usage, 0, len(usages))
	for _, usage := range usages {
		key := usage.File + "\x00" + usage.Range.String() +
			"\x00" + usage.Name
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, usage)
	}
	return result
}

func uniqueProps(props []Prop) []Prop {
	seen := make(map[string]struct{}, len(props))
	result := make([]Prop, 0, len(props))
	for _, prop := range props {
		key := prop.File + "\x00" + prop.Name +
			"\x00" + prop.Range.String()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, prop)
	}
	return result
}

func uniqueLiveActionReferences(
	references []LiveActionReference,
) []LiveActionReference {
	seen := make(map[string]struct{}, len(references))
	result := make([]LiveActionReference, 0, len(references))
	for _, reference := range references {
		key := reference.File + "\x00" + reference.Range.String() +
			"\x00" + reference.Name
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result
}

func uniqueLiveActionArgumentReferences(
	references []LiveActionArgumentReference,
) []LiveActionArgumentReference {
	seen := make(map[string]struct{}, len(references))
	result := make([]LiveActionArgumentReference, 0, len(references))
	for _, reference := range references {
		key := reference.File + "\x00" + reference.Range.String() +
			"\x00" + reference.Action + "\x00" + reference.Name
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result
}

func uniqueLiveEventReferences(
	references []LiveEventReference,
) []LiveEventReference {
	seen := make(map[string]struct{}, len(references))
	result := make([]LiveEventReference, 0, len(references))
	for _, reference := range references {
		key := reference.File + "\x00" + reference.Range.String() +
			"\x00" + reference.Name
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result
}

func uniqueLiveEventArgumentReferences(
	references []LiveEventArgumentReference,
) []LiveEventArgumentReference {
	seen := make(map[string]struct{}, len(references))
	result := make([]LiveEventArgumentReference, 0, len(references))
	for _, reference := range references {
		key := reference.File + "\x00" + reference.Range.String() +
			"\x00" + reference.Event + "\x00" + reference.Name
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result
}

func rangeContainsCursor(rng cst.TextRange, offset uint32) bool {
	return rng.Contains(offset) ||
		offset == rng.End && rng.End > rng.Start
}
