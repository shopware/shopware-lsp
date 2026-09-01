// Package twigmigration contains versioned Administration Twig component
// migrations. It compiles immutable Twig CST nodes to lossless source edits;
// callers decide whether to expose those edits as an LSP quick fix.
package twigmigration

import (
	"errors"
	"fmt"
	"html"
	"strings"
	"unicode"

	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

var ErrUnsafe = errors.New("administration Twig migration is not safely automatable")

// Rule identifies one shopware-cli Administration Twig migration.
type Rule struct {
	ID        string
	SourceTag string
	TargetTag string
	Message   string
}

var rules = []Rule{
	{ID: "alert", SourceTag: "sw-alert", TargetTag: "mt-banner", Message: "sw-alert is removed; use mt-banner and review variant changes"},
	{ID: "button", SourceTag: "sw-button", TargetTag: "mt-button", Message: "sw-button is removed; use mt-button and review variant and router-link behavior"},
	{ID: "card", SourceTag: "sw-card", TargetTag: "mt-card", Message: "sw-card is removed; use mt-card and review aiBadge and contentPadding"},
	{ID: "checkbox-field", SourceTag: "sw-checkbox-field", TargetTag: "mt-checkbox", Message: "sw-checkbox-field is removed; use mt-checkbox and review props, events, and slots"},
	{ID: "colorpicker", SourceTag: "sw-colorpicker", TargetTag: "mt-colorpicker", Message: "sw-colorpicker is removed; use mt-colorpicker and review its label"},
	{ID: "datepicker", SourceTag: "sw-datepicker", TargetTag: "mt-datepicker", Message: "sw-datepicker is removed; use mt-datepicker and review its label"},
	{ID: "email-field", SourceTag: "sw-email-field", TargetTag: "mt-email-field", Message: "sw-email-field is removed; use mt-email-field and review props, events, and slots"},
	{ID: "icon", SourceTag: "sw-icon", TargetTag: "mt-icon", Message: "sw-icon is removed; use mt-icon with an explicit size"},
	{ID: "loader", SourceTag: "sw-loader", TargetTag: "mt-loader", Message: "sw-loader is removed; use mt-loader"},
	{ID: "number-field", SourceTag: "sw-number-field", TargetTag: "mt-number-field", Message: "sw-number-field is removed; use mt-number-field and review props, events, and slots"},
	{ID: "password-field", SourceTag: "sw-password-field", TargetTag: "mt-password-field", Message: "sw-password-field is removed; use mt-password-field and review label and hint values"},
	{ID: "progress-bar", SourceTag: "sw-progress-bar", TargetTag: "mt-progress-bar", Message: "sw-progress-bar is removed; use mt-progress-bar"},
	{ID: "select-field", SourceTag: "sw-select-field", TargetTag: "mt-select", Message: "sw-select-field is removed; use mt-select and review props, options, events, and slots"},
	{ID: "skeleton-bar", SourceTag: "sw-skeleton-bar", TargetTag: "mt-skeleton-bar", Message: "sw-skeleton-bar is removed; use mt-skeleton-bar"},
	{ID: "switch-field", SourceTag: "sw-switch-field", TargetTag: "mt-switch", Message: "sw-switch-field is removed; use mt-switch and review props, events, and slots"},
	{ID: "text-field", SourceTag: "sw-text-field", TargetTag: "mt-text-field", Message: "sw-text-field is removed; use mt-text-field and review props, events, and slots"},
	{ID: "textarea-field", SourceTag: "sw-textarea-field", TargetTag: "mt-textarea", Message: "sw-textarea-field is removed; use mt-textarea and review its API"},
	{ID: "url-field", SourceTag: "sw-url-field", TargetTag: "mt-url-field", Message: "sw-url-field is removed; use mt-url-field and review props, events, and slots"},
	{ID: "popover", SourceTag: "sw-popover", TargetTag: "mt-floating-ui", Message: "sw-popover is removed; use mt-floating-ui"},
}

var rulesByID, rulesByTag = indexRules(rules)

func indexRules(values []Rule) (map[string]Rule, map[string]Rule) {
	byID := make(map[string]Rule, len(values))
	byTag := make(map[string]Rule, len(values))
	for _, rule := range values {
		byID[rule.ID] = rule
		byTag[rule.SourceTag] = rule
	}
	return byID, byTag
}

func Rules() []Rule { return append([]Rule(nil), rules...) }

func RuleByID(id string) (Rule, bool) {
	rule, found := rulesByID[id]
	return rule, found
}

func RuleForTag(tag string) (Rule, bool) {
	rule, found := rulesByTag[tag]
	return rule, found
}

// Compile creates a local, lossless migration for one complete HTML tag. It
// returns ErrUnsafe when the legacy API carries behavior for which the target
// component has no deterministic equivalent.
func Compile(source string, node *twigsyntax.Node, rule Rule) ([]rewrite.Edit, error) {
	tag, ok := twigast.CastHtmlTag(node)
	if !ok {
		return nil, fmt.Errorf("compile %s: expected complete HTML tag", rule.ID)
	}
	starting, ok := tag.StartingTag()
	if !ok || starting.Name() == nil || starting.Name().Text() != rule.SourceTag {
		return nil, fmt.Errorf("compile %s: source tag changed", rule.ID)
	}
	editor, err := newTagEditor(source, tag)
	if err != nil {
		return nil, err
	}
	if err := editor.renameTag(rule.TargetTag); err != nil {
		return nil, err
	}
	if err := compileRule(editor, rule.ID); err != nil {
		return nil, err
	}
	return editor.finish()
}

func compileRule(editor *tagEditor, id string) error {
	switch id {
	case "alert":
		if editor.hasAny(":variant", "v-bind:variant") {
			return unsafe("a bound alert variant may need a value migration")
		}
		return editor.mapStaticValue("variant", map[string]string{
			"success": "positive", "error": "critical", "warning": "attention",
		})
	case "button":
		return migrateButton(editor)
	case "card":
		return migrateCard(editor)
	case "checkbox-field":
		if err := editor.renameMany(map[string]string{
			":value": ":checked", "v-model": "v-model:checked",
			"v-model:value": "v-model:checked", "@update:value": "@update:checked",
			"partlyChecked": "partial",
		}); err != nil {
			return err
		}
		if err := editor.deleteMany("id", "ghostValue", "padded"); err != nil {
			return err
		}
		return editor.migrateSlotProps(map[string]slotPolicy{
			"label": slotToProp,
			"hint":  slotMustBeEmpty,
		})
	case "colorpicker", "datepicker":
		if err := migrateModelValue(editor, false); err != nil {
			return err
		}
		return editor.migrateSlotProps(map[string]slotPolicy{"label": slotToProp})
	case "email-field", "text-field":
		if editor.hasAny("isInvalid", "aiBadge", "@base-field-mounted") {
			return unsafe("isInvalid, aiBadge, or base-field-mounted behavior needs manual migration")
		}
		if err := migrateModelValue(editor, true); err != nil {
			return err
		}
		if err := editor.mapStaticValue("size", map[string]string{"medium": "default"}); err != nil {
			return err
		}
		if id == "text-field" {
			for _, name := range []string{
				":copyable", ":copyableTooltip", ":copyable-tooltip", ":disabled",
				":required", ":isInherited", ":is-inherited",
				":disableInheritanceToggle", ":disable-inheritance-toggle",
			} {
				if err := editor.booleanBindingToShorthand(name); err != nil {
					return err
				}
			}
		}
		return editor.migrateSlotProps(map[string]slotPolicy{"label": slotToProp})
	case "icon":
		return migrateIcon(editor)
	case "loader", "skeleton-bar":
		return nil
	case "number-field":
		if err := migrateModelValue(editor, false); err != nil {
			return err
		}
		return editor.migrateSlotProps(map[string]slotPolicy{"label": slotToProp})
	case "password-field":
		if editor.hasAny("isInvalid", "@base-field-mounted") {
			return unsafe("isInvalid or base-field-mounted behavior needs manual migration")
		}
		if err := migrateModelValue(editor, true); err != nil {
			return err
		}
		if err := editor.mapStaticValue("size", map[string]string{"medium": "default"}); err != nil {
			return err
		}
		return editor.migrateSlotProps(map[string]slotPolicy{
			"label": slotToProp, "hint": slotToProp,
		})
	case "progress-bar", "url-field":
		if err := migrateModelValue(editor, true); err != nil {
			return err
		}
		if id == "url-field" {
			return editor.migrateSlotProps(map[string]slotPolicy{"label": slotToProp})
		}
		return nil
	case "select-field":
		return migrateSelect(editor)
	case "switch-field":
		return migrateSwitch(editor)
	case "textarea-field":
		if err := migrateModelValue(editor, true); err != nil {
			return err
		}
		return editor.migrateSlotProps(map[string]slotPolicy{"label": slotToProp})
	case "popover":
		return migratePopover(editor)
	default:
		return fmt.Errorf("unknown Administration Twig migration %q", id)
	}
}

func migrateModelValue(editor *tagEditor, staticValue bool) error {
	renames := map[string]string{
		":value": ":model-value", "v-model:value": "v-model",
		"@update:value": "@update:model-value",
	}
	if staticValue {
		renames["value"] = "model-value"
	}
	return editor.renameMany(renames)
}

func migrateButton(editor *tagEditor) error {
	if editor.hasAny(":variant", "v-bind:variant") {
		return unsafe("a bound button variant may need a value migration")
	}
	if variant := editor.attribute("variant"); variant != nil {
		value, ok := attributeValue(*variant)
		if !ok {
			return unsafe("button variant has no static value")
		}
		switch strings.ToLower(value) {
		case "ghost":
			if err := editor.delete("variant"); err != nil {
				return err
			}
			editor.addBoolean("ghost")
		case "danger":
			if err := editor.setAttributeValue("variant", "critical"); err != nil {
				return err
			}
		case "ghost-danger":
			if err := editor.setAttributeValue("variant", "critical"); err != nil {
				return err
			}
			editor.addBoolean("ghost")
		case "contrast", "context":
			if err := editor.delete("variant"); err != nil {
				return err
			}
		}
	}
	for _, route := range []struct {
		name  string
		bound bool
	}{{"router-link", false}, {":router-link", true}, {"v-bind:router-link", true}} {
		attribute := editor.attribute(route.name)
		if attribute == nil {
			continue
		}
		if editor.hasAny("@click", "v-on:click") {
			return unsafe("router-link cannot be merged with an existing click handler")
		}
		value, ok := attributeValue(*attribute)
		if !ok || strings.TrimSpace(value) == "" {
			return unsafe("router-link has no migratable value")
		}
		expression := strings.TrimSpace(value)
		if !route.bound {
			expression = "'" + escapeJavaScriptSingleQuoted(value) + "'"
		}
		if err := editor.renameAttribute(route.name, "@click"); err != nil {
			return err
		}
		return editor.setAttributeValue("@click", "$router.push("+expression+")")
	}
	return nil
}

func migrateCard(editor *tagEditor) error {
	if editor.hasAny("aiBadge", ":aiBadge", ":ai-badge") {
		return unsafe("aiBadge augments the old title and needs a hand-written mt-card title slot")
	}
	if attribute := editor.attribute("contentPadding"); attribute != nil {
		value, hasValue := attributeValue(*attribute)
		if hasValue && !strings.EqualFold(strings.TrimSpace(value), "true") {
			return unsafe("contentPadding=false has no lossless automatic mt-card equivalent")
		}
		if err := editor.delete("contentPadding"); err != nil {
			return err
		}
	}
	if editor.hasAny(":contentPadding", ":content-padding") {
		return unsafe("bound contentPadding behavior needs manual migration")
	}
	return nil
}

func migrateIcon(editor *tagEditor) error {
	hasSmall := editor.attribute("small") != nil
	hasLarge := editor.attribute("large") != nil
	hasSize := editor.attribute("size") != nil || editor.attribute(":size") != nil
	if (hasSmall && hasLarge) || (hasSize && (hasSmall || hasLarge)) {
		return unsafe("conflicting icon size properties need manual migration")
	}
	switch {
	case hasSmall:
		return editor.replaceAttribute("small", `size="16px"`, "size")
	case hasLarge:
		return editor.replaceAttribute("large", `size="32px"`, "size")
	case !hasSize:
		editor.add(`size="24px"`, "size")
	}
	return nil
}

func migrateSelect(editor *tagEditor) error {
	if editor.hasAny(":aside", "aside") {
		return unsafe("select aside content has no deterministic mt-select equivalent")
	}
	if err := migrateModelValue(editor, false); err != nil {
		return err
	}
	if err := editor.migrateSlotProps(map[string]slotPolicy{"label": slotToProp}); err != nil {
		return err
	}
	return editor.migrateSelectOptions()
}

func migrateSwitch(editor *tagEditor) error {
	if err := editor.renameMany(map[string]string{
		"noMarginTop": "removeTopMargin", "value": "model-value",
		":value": ":model-value", "v-model:value": "v-model",
		"@update:value": "@update:model-value",
	}); err != nil {
		return err
	}
	if err := editor.deleteMany("size", "id", "ghostValue", "padded", "partlyChecked"); err != nil {
		return err
	}
	return editor.migrateSlotProps(map[string]slotPolicy{
		"label": slotToProp,
		"hint":  slotMustBeEmpty,
	})
}

func migratePopover(editor *tagEditor) error {
	if editor.hasAny(":zIndex", ":z-index", "zIndex", "z-index") {
		return unsafe("popover z-index behavior has no deterministic mt-floating-ui equivalent")
	}
	if err := editor.renameMany(map[string]string{
		":resizeWidth":  ":match-reference-width",
		":resize-width": ":match-reference-width",
	}); err != nil {
		return err
	}
	if editor.hasAny(":isOpened", ":is-opened") {
		return editor.renameAttribute(":isOpened", ":is-opened")
	}
	// v-if remains structural. mt-floating-ui still needs to be opened while it
	// exists, so retaining v-if and adding the target prop preserves behavior.
	editor.add(`:is-opened="true"`, ":is-opened")
	return nil
}

type slotPolicy uint8

const (
	slotToProp slotPolicy = iota + 1
	slotMustBeEmpty
)

type tagEditor struct {
	source    string
	tag       twigast.HtmlTag
	starting  twigast.HtmlStartingTag
	builder   *rewrite.Builder
	attrs     map[string]twigast.HtmlAttribute
	additions []string
	added     map[string]struct{}
}

func newTagEditor(source string, tag twigast.HtmlTag) (*tagEditor, error) {
	starting, ok := tag.StartingTag()
	if !ok {
		return nil, errors.New("administration Twig migration has no starting tag")
	}
	editor := &tagEditor{
		source: source, tag: tag, starting: starting,
		builder: rewrite.NewBuilder(source), attrs: make(map[string]twigast.HtmlAttribute),
		added: make(map[string]struct{}),
	}
	for _, attribute := range starting.Attributes() {
		name := twigquery.HTMLAttributeName(attribute.Syntax())
		if name == "" {
			continue
		}
		if _, duplicate := editor.attrs[name]; duplicate {
			return nil, unsafe("duplicate attribute " + name)
		}
		editor.attrs[name] = attribute
	}
	return editor, nil
}

func (e *tagEditor) renameTag(target string) error {
	if e.starting.Name() == nil {
		return errors.New("administration Twig migration tag name is unavailable")
	}
	if err := e.builder.ReplaceRange(e.starting.Name().Range(), target); err != nil {
		return err
	}
	if ending, ok := e.tag.EndingTag(); ok && ending.Name() != nil {
		if err := e.builder.ReplaceRange(ending.Name().Range(), target); err != nil {
			return err
		}
	}
	return nil
}

func (e *tagEditor) attribute(name string) *twigast.HtmlAttribute {
	attribute, found := e.attrs[name]
	if !found {
		return nil
	}
	return &attribute
}

func (e *tagEditor) hasAny(names ...string) bool {
	for _, name := range names {
		if _, found := e.attrs[name]; found {
			return true
		}
		if _, found := e.added[name]; found {
			return true
		}
	}
	return false
}

func (e *tagEditor) renameMany(values map[string]string) error {
	for old, replacement := range values {
		if err := e.renameAttribute(old, replacement); err != nil {
			return err
		}
	}
	return nil
}

func (e *tagEditor) renameAttribute(old, replacement string) error {
	if old == replacement {
		return nil
	}
	attribute, found := e.attrs[old]
	if !found {
		return nil
	}
	if _, conflict := e.attrs[replacement]; conflict {
		return unsafe("both " + old + " and " + replacement + " are present")
	}
	if _, conflict := e.added[replacement]; conflict {
		return unsafe("attribute " + replacement + " would be duplicated")
	}
	if attribute.Name() == nil {
		return errors.New("administration Twig attribute name is unavailable")
	}
	if err := e.builder.ReplaceRange(attribute.Name().Range(), replacement); err != nil {
		return err
	}
	delete(e.attrs, old)
	e.attrs[replacement] = attribute
	return nil
}

func (e *tagEditor) replaceAttribute(old, replacement, replacementName string) error {
	attribute, found := e.attrs[old]
	if !found {
		return nil
	}
	if old != replacementName && e.hasAny(replacementName) {
		return unsafe("attribute " + replacementName + " would be duplicated")
	}
	if err := e.builder.ReplaceRange(attribute.Syntax().RangeTrimmedTrivia(), replacement); err != nil {
		return err
	}
	delete(e.attrs, old)
	e.attrs[replacementName] = attribute
	return nil
}

func (e *tagEditor) delete(name string) error {
	attribute, found := e.attrs[name]
	if !found {
		return nil
	}
	if err := e.builder.Delete(attribute.Syntax()); err != nil {
		return err
	}
	delete(e.attrs, name)
	return nil
}

func (e *tagEditor) deleteMany(names ...string) error {
	for _, name := range names {
		if err := e.delete(name); err != nil {
			return err
		}
	}
	return nil
}

func (e *tagEditor) add(text, name string) {
	if e.hasAny(name) {
		return
	}
	e.additions = append(e.additions, text)
	e.added[name] = struct{}{}
}

func (e *tagEditor) addBoolean(name string) { e.add(name, name) }

func (e *tagEditor) setAttributeValue(name, value string) error {
	attribute := e.attribute(name)
	if attribute == nil {
		return fmt.Errorf("attribute %s is unavailable", name)
	}
	stringValue, ok := attribute.Value()
	if !ok {
		return e.builder.Insert(attribute.Syntax().RangeTrimmedTrivia().End, `="`+escapeAttribute(value)+`"`)
	}
	if inner, innerOK := stringValue.GetInner(); innerOK {
		return e.builder.ReplaceRange(inner.Syntax().Range(), escapeAttribute(value))
	}
	return e.builder.ReplaceRange(stringValue.Syntax().Range(), `"`+escapeAttribute(value)+`"`)
}

func (e *tagEditor) mapStaticValue(name string, values map[string]string) error {
	attribute := e.attribute(name)
	if attribute == nil {
		return nil
	}
	value, ok := attributeValue(*attribute)
	if !ok {
		return unsafe(name + " has no static value")
	}
	replacement, found := values[strings.ToLower(strings.TrimSpace(value))]
	if !found {
		return nil
	}
	return e.setAttributeValue(name, replacement)
}

func (e *tagEditor) booleanBindingToShorthand(name string) error {
	attribute := e.attribute(name)
	if attribute == nil {
		return nil
	}
	value, ok := attributeValue(*attribute)
	if !ok || !strings.EqualFold(strings.TrimSpace(value), "true") {
		return nil
	}
	replacement := strings.TrimPrefix(name, ":")
	return e.replaceAttribute(name, replacement, replacement)
}

func (e *tagEditor) migrateSlotProps(policies map[string]slotPolicy) error {
	body, ok := e.tag.Body()
	if !ok {
		return nil
	}
	for child := range body.Syntax().ChildNodes() {
		template, cast := twigast.CastHtmlTag(child)
		if !cast || template.Name() == nil || template.Name().Text() != "template" {
			continue
		}
		name := templateSlotName(template)
		policy, handled := policies[name]
		if !handled {
			continue
		}
		value, expression, empty, valueOK := templateSlotValue(template)
		switch policy {
		case slotMustBeEmpty:
			if !empty {
				return unsafe(name + " slot content has no deterministic target equivalent")
			}
		case slotToProp:
			if !valueOK {
				return unsafe(name + " slot is too complex to convert to a prop")
			}
			if !empty {
				attributeName := name
				if expression {
					attributeName = ":" + name
				}
				if e.hasAny(name, ":"+name, "v-bind:"+name) {
					return unsafe(name + " prop and slot are both present")
				}
				e.add(attributeName+`="`+escapeAttribute(value)+`"`, attributeName)
			}
		}
		if err := e.builder.Delete(template.Syntax()); err != nil {
			return err
		}
	}
	return nil
}

func (e *tagEditor) migrateSelectOptions() error {
	body, ok := e.tag.Body()
	if !ok {
		return nil
	}
	var options []string
	var optionNodes []*twigsyntax.Node
	for child := range body.Syntax().ChildNodes() {
		option, cast := twigast.CastHtmlTag(child)
		if !cast || option.Name() == nil || option.Name().Text() != "option" {
			continue
		}
		value, valueOK := optionValue(option)
		label, expression, empty, labelOK := templateSlotValue(option)
		if !valueOK || !labelOK || empty {
			return unsafe("select option cannot be represented as a label/value object")
		}
		if !expression {
			label = "'" + escapeJavaScriptSingleQuoted(html.UnescapeString(label)) + "'"
		}
		options = append(options, "{ label: "+label+", value: "+value+" }")
		optionNodes = append(optionNodes, option.Syntax())
	}
	if len(options) == 0 {
		return nil
	}
	if e.hasAny(":options", "v-bind:options", "options") {
		return unsafe("select has both option children and an options prop")
	}
	for _, option := range optionNodes {
		if err := e.builder.Delete(option); err != nil {
			return err
		}
	}
	expression := "[" + strings.Join(options, ", ") + "]"
	e.add(`:options="`+escapeAttribute(expression)+`"`, ":options")
	return nil
}

func (e *tagEditor) finish() ([]rewrite.Edit, error) {
	if len(e.additions) != 0 {
		list := firstChild(e.starting.Syntax(), twigsyntax.HtmlAttributeList)
		if list == nil {
			return nil, errors.New("administration Twig attribute list is unavailable")
		}
		separator := " "
		if strings.Contains(e.starting.Syntax().Text(), "\n") {
			separator = "\n" + e.attributeIndent()
		}
		if err := e.builder.Insert(list.Range().End, separator+strings.Join(e.additions, separator)); err != nil {
			return nil, err
		}
	}
	return e.builder.Finish()
}

func (e *tagEditor) attributeIndent() string {
	attributes := e.starting.Attributes()
	if len(attributes) != 0 {
		attribute := attributes[len(attributes)-1].Syntax()
		prefix := e.source[attribute.Range().Start:attribute.RangeTrimmedTrivia().Start]
		if newline := strings.LastIndexByte(prefix, '\n'); newline >= 0 {
			indent := prefix[newline+1:]
			if strings.TrimSpace(indent) == "" {
				return indent
			}
		}
	}
	return "    "
}

func attributeValue(attribute twigast.HtmlAttribute) (string, bool) {
	value, ok := attribute.Value()
	if !ok {
		return "", false
	}
	inner, ok := value.GetInner()
	if !ok {
		return "", true
	}
	return inner.Syntax().Text(), true
}

func templateSlotName(tag twigast.HtmlTag) string {
	for _, attribute := range tag.Attributes() {
		name := twigquery.HTMLAttributeName(attribute.Syntax())
		switch {
		case strings.HasPrefix(name, "#"):
			return strings.TrimPrefix(name, "#")
		case strings.HasPrefix(name, "v-slot:"):
			return strings.TrimPrefix(name, "v-slot:")
		}
	}
	return ""
}

func templateSlotValue(tag twigast.HtmlTag) (value string, expression, empty, ok bool) {
	body, found := tag.Body()
	if !found {
		return "", false, true, true
	}
	text := strings.TrimSpace(body.Syntax().Text())
	if text == "" {
		return "", false, true, true
	}
	twigVars := twigquery.Nodes(body.Syntax(), twigsyntax.TwigVar)
	if len(twigVars) == 1 && strings.TrimSpace(twigVars[0].Text()) == text {
		variable, cast := twigast.CastTwigVar(twigVars[0])
		if !cast {
			return "", false, false, false
		}
		expressionNode, expressionOK := variable.GetExpression()
		if !expressionOK {
			return "", false, false, false
		}
		return strings.TrimSpace(expressionNode.Syntax().Text()), true, false, true
	}
	for descendant := range body.Syntax().Descendants() {
		node, nodeOK := descendant.(*twigsyntax.Node)
		if !nodeOK || node == body.Syntax() {
			continue
		}
		switch node.Kind() {
		case twigsyntax.HtmlText:
			continue
		default:
			return "", false, false, false
		}
	}
	plain := strings.Join(strings.Fields(html.UnescapeString(text)), " ")
	return plain, false, false, true
}

func optionValue(option twigast.HtmlTag) (string, bool) {
	for _, attribute := range option.Attributes() {
		name := twigquery.HTMLAttributeName(attribute.Syntax())
		value, ok := attributeValue(attribute)
		if !ok {
			continue
		}
		switch name {
		case "value":
			return "'" + escapeJavaScriptSingleQuoted(html.UnescapeString(value)) + "'", true
		case ":value", "v-bind:value", "v-model:value":
			return strings.TrimSpace(value), strings.TrimSpace(value) != ""
		}
	}
	return "", false
}

func firstChild(node *twigsyntax.Node, kind twigsyntax.Kind) *twigsyntax.Node {
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

func escapeAttribute(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;",
	)
	return replacer.Replace(value)
}

func escapeJavaScriptSingleQuoted(value string) string {
	var result strings.Builder
	for _, character := range value {
		switch character {
		case '\\':
			result.WriteString(`\\`)
		case '\'':
			result.WriteString(`\'`)
		case '\n':
			result.WriteString(`\n`)
		case '\r':
			result.WriteString(`\r`)
		default:
			if unicode.IsControl(character) {
				continue
			}
			result.WriteRune(character)
		}
	}
	return result.String()
}

func unsafe(reason string) error { return fmt.Errorf("%w: %s", ErrUnsafe, reason) }
