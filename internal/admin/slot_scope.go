package admin

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// TwigScopedSlot is one lexical Vue slot scope in Administration markup.
// Bindings are visible only inside TemplateRange; sibling templates and the
// surrounding component template deliberately do not inherit them.
type TwigScopedSlot struct {
	ComponentName string
	SlotName      string
	Bindings      []TwigSlotBinding
	BindingRange  cst.TextRange
	TemplateRange cst.TextRange
	// StartingTagRange identifies the tag carrying v-slot. It lets the index
	// resolve a static, template-local, or dynamic owner without retaining CST
	// pointers beyond the parse request.
	StartingTagRange cst.TextRange
}

// TwigSlotBinding maps a slot contract member to the name chosen by the
// consumer. WholeObject is used by v-slot="props" and conservative rest
// destructuring, where the local represents the complete payload object.
type TwigSlotBinding struct {
	MemberName  string
	LocalName   string
	WholeObject bool
	MemberRange cst.TextRange
	LocalRange  cst.TextRange
}

// ResolvedTwigScopedSlot joins a lexical consumer scope with the effective
// component contract (including inherited and overridden slots).
type ResolvedTwigScopedSlot struct {
	Component VueComponent
	Slot      VueComponentSlot
	Scope     TwigScopedSlot
	// Contracts retains each concrete candidate behind a closed dynamic
	// component selector. Slot is their conservative common payload contract.
	Contracts         []ResolvedTwigSlotContract
	ContractsComplete bool
}

// ResolvedTwigSlotContract is one concrete component/slot pair contributing
// to a scoped-slot payload contract.
type ResolvedTwigSlotContract struct {
	Component VueComponent
	Slot      VueComponentSlot
}

// ResolvedTwigSlotBinding identifies one local or destructured contract field
// under the cursor. MemberFound is false when the source declares a local but
// the indexed component contract is incomplete; lexical support still works.
type ResolvedTwigSlotBinding struct {
	ResolvedTwigScopedSlot
	Binding     TwigSlotBinding
	Member      VueComponentSlotMember
	Members     []VueComponentSlotMember
	MemberFound bool
	Identifier  string
	Range       cst.TextRange
}

// ResolvedTwigSlotMember identifies a direct member accessed through a
// whole-object slot binding such as #content="props" and props.currentValue.
type ResolvedTwigSlotMember struct {
	ResolvedTwigScopedSlot
	Binding         TwigSlotBinding
	Member          VueComponentSlotMember
	Members         []VueComponentSlotMember
	MemberFound     bool
	Access          TwigVueMemberAccess
	ReceiverFound   bool
	ReceiverType    string
	ReceiverMembers []TwigVueMember
	MembersComplete bool
}

// TwigSlotOwnerStartingTag returns the component start tag whose slot contract
// owns a slot directive. Vue permits v-slot directly on a component or on a
// direct child <template>; dynamic <component> owners are retained so callers
// can resolve their complete candidate set through the index.
func TwigSlotOwnerStartingTag(
	slotStartTag *twigsyntax.Node,
) *twigsyntax.Node {
	if slotStartTag == nil {
		return nil
	}
	if _, dynamic := TwigDynamicComponentSelector(slotStartTag); dynamic {
		return slotStartTag
	}
	if _, static := StaticComponentNameForTag(slotStartTag); static {
		return slotStartTag
	}
	if twigquery.HTMLTagName(slotStartTag) != "template" {
		return nil
	}
	templateTag := twigquery.ClosestNodeOfKind(
		slotStartTag, twigsyntax.HtmlTag,
	)
	if templateTag == nil {
		return nil
	}
	parentTag := twigquery.ClosestNodeOfKind(
		templateTag.Parent(), twigsyntax.HtmlTag,
	)
	parent, ok := twigast.CastHtmlTag(parentTag)
	if !ok {
		return nil
	}
	starting, ok := parent.StartingTag()
	if !ok {
		return nil
	}
	return starting.Syntax()
}

// ResolveTwigSlotConsumerComponents resolves every component contract that may
// own a slot directive. The boolean is true only for a closed static or
// inferred dynamic owner, which lets diagnostics remain conservative.
func (idx *AdminComponentIndexer) ResolveTwigSlotConsumerComponents(
	templatePath string,
	slotStartTag *twigsyntax.Node,
	owners ...*VueComponent,
) ([]VueComponent, bool, error) {
	if idx == nil {
		return nil, false, nil
	}
	owner := TwigSlotOwnerStartingTag(slotStartTag)
	if owner == nil {
		return nil, false, nil
	}
	if selector, dynamic := TwigDynamicComponentSelector(owner); dynamic {
		_, components, complete, err := idx.ResolveDynamicComponentContractsForOwner(
			templatePath, selector, firstVueComponent(owners), owner,
		)
		return components, complete, err
	}
	name, found := StaticComponentNameForTag(owner)
	if !found {
		return nil, false, nil
	}
	component, found, err := idx.GetComponentForTemplateTag(
		templatePath, name, owners...,
	)
	if err != nil || !found || component == nil {
		return nil, false, err
	}
	return []VueComponent{*component}, true, nil
}

func firstVueComponent(values []*VueComponent) *VueComponent {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func (resolved ResolvedTwigScopedSlot) QualifiedName() string {
	componentName := resolved.Scope.ComponentName
	if componentName == "" {
		names := resolved.ComponentNames()
		switch len(names) {
		case 0:
			componentName = "<dynamic>"
		case 1:
			componentName = names[0]
		default:
			componentName = "(" + strings.Join(names, " | ") + ")"
		}
	}
	if resolved.Scope.SlotName == "" {
		return componentName + ".<dynamic>"
	}
	return componentName + "." + resolved.Scope.SlotName
}

func (resolved ResolvedTwigScopedSlot) ComponentNames() []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(resolved.Contracts))
	for _, contract := range resolved.Contracts {
		if contract.Component.Name == "" || seen[contract.Component.Name] {
			continue
		}
		seen[contract.Component.Name] = true
		result = append(result, contract.Component.Name)
	}
	if len(result) == 0 && resolved.Component.Name != "" {
		result = append(result, resolved.Component.Name)
	}
	return result
}

// TwigScopedSlotStartingTag returns the tag carrying the slot directive for a
// lexical scope returned by TwigScopedSlotAtOffset.
func TwigScopedSlotStartingTag(
	root *twigsyntax.Node,
	scope TwigScopedSlot,
) *twigsyntax.Node {
	if root == nil || scope.StartingTagRange.Len() == 0 {
		return nil
	}
	node := root.NodeAtOffset(scope.StartingTagRange.Start)
	if startTag := twigquery.StartingHTMLTagAt(node); startTag != nil &&
		startTag.RangeTrimmedTrivia() == scope.StartingTagRange {
		return startTag
	}
	for _, startTag := range twigquery.Nodes(
		root, twigsyntax.HtmlStartingTag,
	) {
		if startTag.RangeTrimmedTrivia() == scope.StartingTagRange {
			return startTag
		}
	}
	return nil
}

func (scope TwigScopedSlot) IsBindingOffset(offset uint32) bool {
	return offset >= scope.BindingRange.Start && offset <= scope.BindingRange.End
}

// TwigScopedSlotMemberReferences returns direct member usages that resolve to
// the same whole-object scoped-slot binding. Nested slot and v-for scopes with
// the same local name are excluded.
func TwigScopedSlotMemberReferences(
	root *twigsyntax.Node,
	content []byte,
	target ResolvedTwigSlotMember,
) []cst.TextRange {
	if root == nil || target.Access.Member == "" ||
		target.Binding.LocalName == "" {
		return nil
	}
	var result []cst.TextRange
	for _, access := range TwigVueExpressionMemberAccesses(root, content) {
		if access.Root != target.Binding.LocalName ||
			!access.SamePath(target.Access) {
			continue
		}
		scope, found := TwigScopedSlotAtOffset(root, access.RootRange.Start)
		if !found || scope.TemplateRange != target.Scope.TemplateRange ||
			scope.BindingRange != target.Scope.BindingRange {
			continue
		}
		ownsRoot := false
		for _, binding := range scope.Bindings {
			if binding.WholeObject && binding.LocalName == access.Root {
				ownsRoot = true
				break
			}
		}
		if !ownsRoot {
			continue
		}
		if vueBinding, vueFound := TwigVueBindingAtOffset(
			root, content, access.RootRange.Start,
		); vueFound && vueBinding != nil &&
			vueBinding.ScopeRange.Len() <= scope.TemplateRange.Len() {
			continue
		}
		result = append(result, access.MemberRange)
	}
	return result
}

// TwigScopedSlotBindingReferences returns the declaration and every lexical
// root reference of one scoped-slot local. Nested v-for or slot scopes that
// shadow the same spelling are excluded.
func TwigScopedSlotBindingReferences(
	root *twigsyntax.Node,
	content []byte,
	target ResolvedTwigSlotBinding,
) []cst.TextRange {
	if root == nil || target.Binding.LocalName == "" {
		return nil
	}
	result := make([]cst.TextRange, 0)
	seen := make(map[cst.TextRange]bool)
	add := func(rangeValue cst.TextRange) {
		if rangeValue.Len() == 0 || seen[rangeValue] {
			return
		}
		seen[rangeValue] = true
		result = append(result, rangeValue)
	}
	add(target.Binding.LocalRange)
	for _, identifier := range TwigVueExpressionRootIdentifiers(root, content) {
		if identifier.Name != target.Binding.LocalName ||
			identifier.Range.Start < target.Scope.TemplateRange.Start ||
			identifier.Range.End > target.Scope.TemplateRange.End ||
			identifier.Range.Start >= target.Scope.BindingRange.Start &&
				identifier.Range.End <= target.Scope.BindingRange.End {
			continue
		}
		scope, found := TwigScopedSlotAtOffset(root, identifier.Range.Start)
		if !found || scope.TemplateRange != target.Scope.TemplateRange ||
			scope.BindingRange != target.Scope.BindingRange {
			continue
		}
		if vueBinding, vueFound := TwigVueBindingAtOffset(
			root, content, identifier.Range.Start,
		); vueFound && vueBinding != nil &&
			vueBinding.ScopeRange.Len() <= scope.TemplateRange.Len() {
			continue
		}
		add(identifier.Range)
	}
	return result
}

// IsTwigVueExpressionAt reports whether offset is in a context evaluated by
// Vue rather than literal HTML: Twig interpolation or a bound/directive value.
func IsTwigVueExpressionAt(node *twigsyntax.Node, offset uint32) bool {
	if node == nil {
		return false
	}
	if twigquery.ClosestNodeOfKind(node, twigsyntax.TwigVar) != nil {
		return true
	}
	attributeNode := twigquery.HTMLAttributeAt(node)
	attribute, ok := twigast.CastHtmlAttribute(attributeNode)
	if !ok {
		return false
	}
	name := twigquery.HTMLAttributeName(attributeNode)
	if !strings.HasPrefix(name, ":") && !strings.HasPrefix(name, "@") &&
		!strings.HasPrefix(name, "v-") {
		return false
	}
	value, ok := attribute.Value()
	if !ok {
		return false
	}
	inner, ok := value.GetInner()
	if !ok {
		return false
	}
	rangeValue := inner.Syntax().RangeTrimmedTrivia()
	return offset >= rangeValue.Start && offset <= rangeValue.End
}

// TwigScopedSlotAtOffset returns the innermost scoped-slot template containing
// offset. Vue permits nested templates, so choosing the smallest range is what
// gives local bindings their normal lexical shadowing behavior.
func TwigScopedSlotAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (TwigScopedSlot, bool) {
	scopes := TwigScopedSlotsAtOffset(root, offset)
	if len(scopes) == 0 {
		return TwigScopedSlot{}, false
	}
	return scopes[len(scopes)-1], true
}

// TwigScopedSlotsAtOffset returns every visible scoped-slot binding from the
// outermost template to the innermost. Vue keeps outer slot locals visible in
// nested slot templates unless an inner binding shadows the same name.
func TwigScopedSlotsAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) []TwigScopedSlot {
	var result []TwigScopedSlot
	seen := make(map[string]bool)
	add := func(scope TwigScopedSlot) {
		key := scope.TemplateRange.String() + "\x00" + scope.BindingRange.String()
		if seen[key] {
			return
		}
		seen[key] = true
		result = append(result, scope)
	}
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlTag) {
		tag, ok := twigast.CastHtmlTag(node)
		if !ok || tag.Name() == nil {
			continue
		}
		templateRange := node.RangeTrimmedTrivia()
		if offset < templateRange.Start || offset > templateRange.End {
			continue
		}
		startingTag, ok := tag.StartingTag()
		if !ok {
			continue
		}
		slotStartTag := startingTag.Syntax()
		ownerStartTag := slotStartTag
		componentName, _ := StaticComponentNameForTag(ownerStartTag)
		if tag.Name().Text() == "template" {
			ownerStartTag = TwigSlotOwnerStartingTag(slotStartTag)
			componentName, _ = StaticComponentNameForTag(ownerStartTag)
			if componentName == "" {
				if _, dynamic := TwigDynamicComponentSelector(ownerStartTag); !dynamic {
					ownerStartTag = nil
				}
			}
		} else if componentName == "" {
			if _, dynamic := TwigDynamicComponentSelector(ownerStartTag); !dynamic {
				// Vue built-ins such as router-view can declare v-slot directly
				// even though they do not belong to Shopware's component prefix.
				componentName = tag.Name().Text()
			}
		}
		for _, attribute := range startingTag.Attributes() {
			attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
			slotName := NormalizeSlotName(attributeName)
			if slotName == "" && !isDynamicVueSlotDirective(attributeName) {
				continue
			}
			value, hasValue := attribute.Value()
			if !hasValue {
				continue
			}
			inner, hasInner := value.GetInner()
			if !hasInner {
				continue
			}
			bindingRange := inner.Syntax().RangeTrimmedTrivia()
			rawBindings := inner.Syntax().Text()
			scope := TwigScopedSlot{
				ComponentName: componentName,
				SlotName:      slotName,
				Bindings: parseScopedSlotBindingsAt(
					rawBindings, inner.Syntax().Range().Start,
				),
				BindingRange:  bindingRange,
				TemplateRange: templateRange,
			}
			if ownerStartTag != nil {
				scope.StartingTagRange = slotStartTag.RangeTrimmedTrivia()
			}
			if scope.ComponentName == "" && scope.StartingTagRange.Len() == 0 {
				continue
			}
			add(scope)
		}
		if scope, ok := dynamicScopedSlotFromMalformedTag(
			node, componentName, slotStartTag.RangeTrimmedTrivia(),
		); ok {
			add(scope)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].TemplateRange.Len() >
			result[right].TemplateRange.Len()
	})
	return result
}

func isDynamicVueSlotDirective(attributeName string) bool {
	name := strings.TrimSpace(attributeName)
	return strings.HasPrefix(name, "#[") ||
		strings.HasPrefix(name, "v-slot:[")
}

// Dynamic argument syntax (#[name]) currently lands in a recoverable ERROR
// node in the native Twig HTML grammar. Anchor a small lossless scan to the
// already-identified tag so explicit locals still receive lexical support;
// the dynamic name is never treated as a static component contract.
func dynamicScopedSlotFromMalformedTag(
	node *twigsyntax.Node,
	componentName string,
	startingTagRange cst.TextRange,
) (TwigScopedSlot, bool) {
	if node == nil || componentName == "" && startingTagRange.Len() == 0 {
		return TwigScopedSlot{}, false
	}
	text := node.Text()
	openingEnd := slotOpeningTagEnd(text)
	if openingEnd < 0 {
		return TwigScopedSlot{}, false
	}
	opening := text[:openingEnd]
	directive := strings.Index(opening, "#[")
	prefixLength := 2
	if long := strings.Index(opening, "v-slot:["); long >= 0 &&
		(directive < 0 || long < directive) {
		directive = long
		prefixLength = len("v-slot:[")
	}
	if directive < 0 {
		return TwigScopedSlot{}, false
	}
	argumentEnd := strings.IndexByte(opening[directive+prefixLength:], ']')
	if argumentEnd < 0 {
		return TwigScopedSlot{}, false
	}
	cursor := directive + prefixLength + argumentEnd + 1
	for cursor < len(opening) && isSlotSpace(opening[cursor]) {
		cursor++
	}
	if cursor >= len(opening) || opening[cursor] != '=' {
		return TwigScopedSlot{}, false
	}
	cursor++
	for cursor < len(opening) && isSlotSpace(opening[cursor]) {
		cursor++
	}
	if cursor >= len(opening) ||
		(opening[cursor] != '\'' && opening[cursor] != '"') {
		return TwigScopedSlot{}, false
	}
	quote := opening[cursor]
	valueStart := cursor + 1
	valueEnd := valueStart
	escaped := false
	for valueEnd < len(opening) {
		current := opening[valueEnd]
		if escaped {
			escaped = false
			valueEnd++
			continue
		}
		if current == '\\' {
			escaped = true
			valueEnd++
			continue
		}
		if current == quote {
			break
		}
		valueEnd++
	}
	if valueEnd >= len(opening) {
		return TwigScopedSlot{}, false
	}
	base := node.RangeTrimmedTrivia().Start
	return TwigScopedSlot{
		ComponentName: componentName,
		Bindings: parseScopedSlotBindingsAt(
			opening[valueStart:valueEnd], base+uint32(valueStart),
		),
		BindingRange: cst.TextRange{
			Start: base + uint32(valueStart), End: base + uint32(valueEnd),
		},
		TemplateRange:    node.RangeTrimmedTrivia(),
		StartingTagRange: startingTagRange,
	}, true
}

func slotOpeningTagEnd(value string) int {
	quote := byte(0)
	escaped := false
	for index := 0; index < len(value); index++ {
		current := value[index]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		if current == '>' {
			return index + 1
		}
	}
	return -1
}

func isSlotSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

// IdentifierAtOffset returns the JavaScript/Vue identifier touching a byte
// offset. HTML attribute values are intentionally kept as lossless strings by
// the Twig frontend, so scoped-slot features need this tiny lexical bridge.
func IdentifierAtOffset(
	content []byte,
	offset uint32,
) (string, cst.TextRange, bool) {
	if len(content) == 0 || offset > uint32(len(content)) {
		return "", cst.TextRange{}, false
	}
	position := int(offset)
	if position == len(content) ||
		(position < len(content) && !isSlotIdentifierContinue(content[position])) {
		if position == 0 || !isSlotIdentifierContinue(content[position-1]) {
			return "", cst.TextRange{}, false
		}
		position--
	}
	start := position
	for start > 0 && isSlotIdentifierContinue(content[start-1]) {
		start--
	}
	end := position + 1
	for end < len(content) && isSlotIdentifierContinue(content[end]) {
		end++
	}
	if start >= end || !isSlotIdentifierStart(content[start]) {
		return "", cst.TextRange{}, false
	}
	return string(content[start:end]), cst.TextRange{
		Start: uint32(start), End: uint32(end),
	}, true
}

// ExpressionRootIdentifierAtOffset is IdentifierAtOffset constrained to a
// variable/root expression. It excludes member-property names and explicit
// object-literal keys, preventing a local named "name" from capturing the
// property in item.name or { name: item }.
func ExpressionRootIdentifierAtOffset(
	content []byte,
	offset uint32,
) (string, cst.TextRange, bool) {
	name, rangeValue, found := IdentifierAtOffset(content, offset)
	if !found {
		return "", cst.TextRange{}, false
	}
	previous := int(rangeValue.Start) - 1
	for previous >= 0 && isSlotSpace(content[previous]) {
		previous--
	}
	if previous >= 0 && content[previous] == '.' {
		return "", cst.TextRange{}, false
	}
	next := int(rangeValue.End)
	for next < len(content) && isSlotSpace(content[next]) {
		next++
	}
	if next < len(content) && content[next] == ':' {
		return "", cst.TextRange{}, false
	}
	return name, rangeValue, true
}

func objectBindingNames(value string) []string {
	names, _ := VueObjectBindingNames(value)
	return names
}

// VueObjectBindingNames extracts statically named keys from an object passed
// to v-bind or a scoped slot. Complete is false when a spread, computed key or
// unsupported entry means additional runtime keys may be present.
func VueObjectBindingNames(value string) ([]string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") {
		return nil, false
	}
	close := matchingSlotDelimiter(value, 0, '{', '}')
	if close < 0 {
		return nil, false
	}
	if strings.TrimSpace(value[close+1:]) != "" {
		return nil, false
	}
	result := make([]string, 0)
	seen := make(map[string]bool)
	complete := true
	for _, raw := range splitSlotTopLevel(value[1:close], ',') {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "...") {
			complete = false
			continue
		}
		name := strings.TrimSpace(trimTopLevelDefault(part))
		if colon := indexSlotTopLevel(part, ':'); colon >= 0 {
			name = strings.TrimSpace(part[:colon])
		}
		name = strings.Trim(name, "'\"")
		if !isSlotPropertyName(name) {
			complete = false
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	return result, complete
}

func parseScopedSlotBindings(value string) []TwigSlotBinding {
	result := parseScopedSlotBindingsAt(value, 0)
	for index := range result {
		result[index].MemberRange = cst.TextRange{}
		result[index].LocalRange = cst.TextRange{}
	}
	return result
}

func parseScopedSlotBindingsAt(
	value string,
	base uint32,
) []TwigSlotBinding {
	leading := len(value) - len(strings.TrimLeft(value, " \t\r\n"))
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if !strings.HasPrefix(value, "{") {
		name := strings.TrimSpace(trimTopLevelDefault(value))
		if isSlotIdentifier(name) {
			start := strings.Index(value, name)
			rangeValue := scopedSlotSourceRange(
				base, leading+start, len(name),
			)
			return []TwigSlotBinding{{
				LocalName: name, WholeObject: true, LocalRange: rangeValue,
			}}
		}
		return nil
	}
	close := matchingSlotDelimiter(value, 0, '{', '}')
	if close < 0 {
		close = len(value)
	} else if strings.TrimSpace(value[close+1:]) != "" {
		return nil
	}
	var result []TwigSlotBinding
	body := value[1:close]
	for _, sourcePart := range splitScopedSlotSourceParts(body, ',') {
		raw := sourcePart.Text
		partLeading := len(raw) - len(strings.TrimLeft(raw, " \t\r\n"))
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "...") {
			local := strings.TrimSpace(trimTopLevelDefault(part[3:]))
			if isSlotIdentifier(local) {
				localStart := strings.Index(part, local)
				result = append(result, TwigSlotBinding{
					LocalName: local, WholeObject: true,
					LocalRange: scopedSlotSourceRange(
						base,
						leading+1+sourcePart.Start+partLeading+localStart,
						len(local),
					),
				})
			}
			continue
		}
		part = strings.TrimSpace(trimTopLevelDefault(part))
		colon := indexSlotTopLevel(part, ':')
		memberName := part
		localName := part
		if colon >= 0 {
			memberName = strings.Trim(strings.TrimSpace(part[:colon]), "'\"")
			localName = strings.TrimSpace(trimTopLevelDefault(part[colon+1:]))
		}
		if !isSlotPropertyName(memberName) || !isSlotIdentifier(localName) {
			continue
		}
		memberSource := part
		if colon >= 0 {
			memberSource = part[:colon]
		}
		memberStart := strings.Index(memberSource, memberName)
		localSearchStart := 0
		if colon >= 0 {
			localSearchStart = colon + 1
		}
		localStart := strings.Index(part[localSearchStart:], localName)
		if localStart >= 0 {
			localStart += localSearchStart
		}
		partBase := leading + 1 + sourcePart.Start + partLeading
		memberRange := scopedSlotSourceRange(
			base, partBase+memberStart, len(memberName),
		)
		localRange := scopedSlotSourceRange(
			base, partBase+localStart, len(localName),
		)
		result = append(result, TwigSlotBinding{
			MemberName: memberName, LocalName: localName,
			MemberRange: memberRange, LocalRange: localRange,
		})
	}
	return result
}

type scopedSlotSourcePart struct {
	Text  string
	Start int
}

func splitScopedSlotSourceParts(
	value string,
	separator byte,
) []scopedSlotSourcePart {
	var result []scopedSlotSourcePart
	start := 0
	state := slotScanState{}
	for index := 0; index < len(value); index++ {
		state.consume(value[index])
		if value[index] == separator && state.topLevel() {
			result = append(result, scopedSlotSourcePart{
				Text: value[start:index], Start: start,
			})
			start = index + 1
		}
	}
	return append(result, scopedSlotSourcePart{
		Text: value[start:], Start: start,
	})
}

func scopedSlotSourceRange(
	base uint32,
	start int,
	length int,
) cst.TextRange {
	if start < 0 || length <= 0 {
		return cst.TextRange{}
	}
	return cst.TextRange{
		Start: base + uint32(start),
		End:   base + uint32(start+length),
	}
}

func trimTopLevelDefault(value string) string {
	if index := indexSlotTopLevel(value, '='); index >= 0 {
		return value[:index]
	}
	return value
}

func splitSlotTopLevel(value string, separator byte) []string {
	var result []string
	start := 0
	state := slotScanState{}
	for index := 0; index < len(value); index++ {
		state.consume(value[index])
		if value[index] == separator && state.topLevel() {
			result = append(result, value[start:index])
			start = index + 1
		}
	}
	result = append(result, value[start:])
	return result
}

func indexSlotTopLevel(value string, needle byte) int {
	state := slotScanState{}
	for index := 0; index < len(value); index++ {
		if value[index] == needle && state.topLevel() {
			return index
		}
		state.consume(value[index])
	}
	return -1
}

type slotScanState struct {
	quote                    byte
	escaped                  bool
	braces, brackets, parens int
}

func (state *slotScanState) consume(value byte) {
	if state.quote != 0 {
		if state.escaped {
			state.escaped = false
			return
		}
		if value == '\\' {
			state.escaped = true
			return
		}
		if value == state.quote {
			state.quote = 0
		}
		return
	}
	switch value {
	case '\'', '"', '`':
		state.quote = value
	case '{':
		state.braces++
	case '}':
		state.braces--
	case '[':
		state.brackets++
	case ']':
		state.brackets--
	case '(':
		state.parens++
	case ')':
		state.parens--
	}
}

func (state slotScanState) topLevel() bool {
	return state.quote == 0 && state.braces == 0 &&
		state.brackets == 0 && state.parens == 0
}

func matchingSlotDelimiter(value string, start int, open, close byte) int {
	depth := 0
	state := slotScanState{}
	for index := start; index < len(value); index++ {
		current := value[index]
		if state.quote != 0 {
			state.consume(current)
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			state.consume(current)
			continue
		}
		switch current {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func isSlotIdentifier(value string) bool {
	if value == "" || !isSlotIdentifierStart(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isSlotIdentifierContinue(value[index]) {
			return false
		}
	}
	return true
}

func isSlotPropertyName(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if !isSlotIdentifierContinue(value[index]) && value[index] != '-' {
			return false
		}
	}
	return true
}

func isSlotIdentifierStart(value byte) bool {
	return value == '_' || value == '$' ||
		value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isSlotIdentifierContinue(value byte) bool {
	return isSlotIdentifierStart(value) || value >= '0' && value <= '9'
}
