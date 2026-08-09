package admin

import (
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

type adminUsageCollector struct {
	filePath  string
	lineIndex *cst.LineIndex
	sets      map[string]*AdminUsageSet
	seen      map[string]bool
}

func newAdminUsageCollector(
	filePath string,
	lineIndex *cst.LineIndex,
) *adminUsageCollector {
	return &adminUsageCollector{
		filePath: filePath, lineIndex: lineIndex,
		sets: make(map[string]*AdminUsageSet), seen: make(map[string]bool),
	}
}

func (collector *adminUsageCollector) addStoreDeclaration(call *jssyntax.Node) {
	if literal := jsquery.StringArgument(call, 0); literal != nil {
		collector.addJSString(AdminSymbolStore, "", literal, true)
		return
	}
	object := jsquery.ObjectArgument(call, 0)
	id := jsquery.PropertyValue(jsquery.Property(object, "id"))
	collector.addJSString(AdminSymbolStore, "", id, true)
}

func (collector *adminUsageCollector) addJSString(
	kind AdminSymbolKind,
	owner string,
	node *jssyntax.Node,
	declaration bool,
) {
	if node == nil || node.Kind() != jssyntax.JsString {
		return
	}
	name := jsquery.StringValue(node)
	if name == "" {
		return
	}
	rangeValue, ok := jsStringContentRange(node)
	if !ok {
		return
	}
	collector.addRange(kind, owner, name, rangeValue, declaration, false)
}

func (collector *adminUsageCollector) addNamedJSNode(
	kind AdminSymbolKind,
	owner,
	name string,
	node *jssyntax.Node,
	declaration bool,
) {
	if node == nil || name == "" {
		return
	}
	style := AdminNameExact
	original := strings.TrimSpace(node.Text())
	if node.Kind() == jssyntax.JsString {
		original = jsquery.StringValue(node)
	}
	if kind == AdminSymbolComponentEvent &&
		original != CanonicalEventName(original) {
		style = AdminNameCamel
	}
	if node.Kind() == jssyntax.JsString {
		if rangeValue, ok := jsStringContentRange(node); ok {
			collector.addStyledRange(
				kind, owner, name, rangeValue, declaration, false, style,
			)
		}
		return
	}
	collector.addStyledRange(
		kind,
		owner,
		name,
		node.RangeTrimmedTrivia(),
		declaration,
		true,
		style,
	)
}

func (collector *adminUsageCollector) addNode(
	kind AdminSymbolKind,
	owner,
	name string,
	node *jssyntax.Node,
	declaration bool,
) {
	if node == nil || name == "" {
		return
	}
	collector.addRange(
		kind, owner, name, node.RangeTrimmedTrivia(), declaration, true,
	)
}

func (collector *adminUsageCollector) addRange(
	kind AdminSymbolKind,
	owner,
	name string,
	rangeValue cst.TextRange,
	declaration bool,
	identifier bool,
) {
	collector.addStyledRange(
		kind,
		owner,
		name,
		rangeValue,
		declaration,
		identifier,
		AdminNameExact,
	)
}

func (collector *adminUsageCollector) addStyledRange(
	kind AdminSymbolKind,
	owner,
	name string,
	rangeValue cst.TextRange,
	declaration bool,
	identifier bool,
	nameStyle AdminNameStyle,
) {
	collector.addRangeWithDynamicSelector(
		kind, owner, name, rangeValue, declaration, identifier, nameStyle, "", false,
	)
}

func (collector *adminUsageCollector) addDynamicRange(
	kind AdminSymbolKind,
	name string,
	rangeValue cst.TextRange,
	selector string,
	routerView bool,
) {
	collector.addDynamicStyledRange(
		kind, name, rangeValue, false, false, AdminNameExact, selector, routerView,
	)
}

func (collector *adminUsageCollector) addDynamicStyledRange(
	kind AdminSymbolKind,
	name string,
	rangeValue cst.TextRange,
	declaration bool,
	identifier bool,
	nameStyle AdminNameStyle,
	selector string,
	routerView bool,
) {
	collector.addRangeWithDynamicSelector(
		kind,
		adminDynamicComponentUsageOwner,
		name,
		rangeValue,
		declaration,
		identifier,
		nameStyle,
		selector,
		routerView,
	)
}

func (collector *adminUsageCollector) addRangeWithDynamicSelector(
	kind AdminSymbolKind,
	owner,
	name string,
	rangeValue cst.TextRange,
	declaration bool,
	identifier bool,
	nameStyle AdminNameStyle,
	dynamicSelector string,
	dynamicRouterView bool,
) {
	if name == "" || rangeValue.End <= rangeValue.Start || collector.lineIndex == nil {
		return
	}
	key := AdminUsageKey(kind, owner, name)
	dedupKey := key + "\x00" + strconv.FormatUint(uint64(rangeValue.Start), 10) +
		"\x00" + strconv.FormatUint(uint64(rangeValue.End), 10)
	if collector.seen[dedupKey] {
		return
	}
	collector.seen[dedupKey] = true
	set := collector.sets[key]
	if set == nil {
		set = &AdminUsageSet{
			Kind: kind, Owner: owner, Name: name, FilePath: collector.filePath,
		}
		collector.sets[key] = set
	}
	startLine, startCharacter := collector.lineIndex.PositionUTF16(rangeValue.Start)
	endLine, endCharacter := collector.lineIndex.PositionUTF16(rangeValue.End)
	set.Occurrences = append(set.Occurrences, AdminSourceRange{
		StartLine: int(startLine), StartCharacter: int(startCharacter),
		EndLine: int(endLine), EndCharacter: int(endCharacter),
		Declaration: declaration, Identifier: identifier,
		NameStyle:                nameStyle,
		DynamicComponentSelector: dynamicSelector,
		DynamicRouterView:        dynamicRouterView,
	})
}

func (collector *adminUsageCollector) addSourceRange(
	kind AdminSymbolKind,
	owner,
	name string,
	rangeValue AdminSourceRange,
	nameStyle AdminNameStyle,
) {
	if collector == nil || name == "" ||
		rangeValue.EndLine < rangeValue.StartLine ||
		rangeValue.EndLine == rangeValue.StartLine &&
			rangeValue.EndCharacter <= rangeValue.StartCharacter {
		return
	}
	key := AdminUsageKey(kind, owner, name)
	dedupKey := key + "\x00" + strconv.Itoa(rangeValue.StartLine) + ":" +
		strconv.Itoa(rangeValue.StartCharacter) + ":" +
		strconv.Itoa(rangeValue.EndLine) + ":" +
		strconv.Itoa(rangeValue.EndCharacter)
	if collector.seen[dedupKey] {
		return
	}
	collector.seen[dedupKey] = true
	set := collector.sets[key]
	if set == nil {
		set = &AdminUsageSet{
			Kind: kind, Owner: owner, Name: name, FilePath: collector.filePath,
		}
		collector.sets[key] = set
	}
	rangeValue.Declaration = true
	rangeValue.NameStyle = nameStyle
	set.Occurrences = append(set.Occurrences, rangeValue)
}

func (collector *adminUsageCollector) values() []AdminUsageSet {
	result := make([]AdminUsageSet, 0, len(collector.sets))
	for _, set := range collector.sets {
		result = append(result, *set)
	}
	return result
}

func jsStringContentRange(node *jssyntax.Node) (cst.TextRange, bool) {
	if node == nil {
		return cst.TextRange{}, false
	}
	for element := range node.Descendants() {
		token, ok := element.(*jssyntax.Token)
		if !ok || (token.Kind() != jssyntax.TkString &&
			token.Kind() != jssyntax.TkTemplate) {
			continue
		}
		rangeValue := token.Range()
		text := token.Text()
		if len(text) >= 2 && (text[0] == '\'' || text[0] == '"' || text[0] == '`') &&
			text[len(text)-1] == text[0] {
			rangeValue.Start++
			rangeValue.End--
		}
		return rangeValue, true
	}
	return cst.TextRange{}, false
}

func lastJSIdentifier(node *jssyntax.Node) *jssyntax.Node {
	var result *jssyntax.Node
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		if child.Kind() == jssyntax.JsIdentifier {
			result = child
		}
	}
	return result
}

func rangeContains(rangeValue cst.TextRange, offset uint32) bool {
	return offset >= rangeValue.Start && offset <= rangeValue.End
}
