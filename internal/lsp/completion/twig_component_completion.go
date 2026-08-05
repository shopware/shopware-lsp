package completion

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

var (
	componentFunctionCompletionPattern = regexp.MustCompile(
		`(?is)\bcomponent\s*\(\s*['"][A-Za-z0-9_:-]*$`,
	)
	componentBlockCompletionPattern = regexp.MustCompile(
		`(?is)\{%-?\s*component\s+(?:['"])?[A-Za-z0-9_:-]*$`,
	)
	componentHTMLCompletionPattern = regexp.MustCompile(
		`(?is)</?twig:[A-Za-z0-9_:-]*$`,
	)
	componentHTMLPropsPattern = regexp.MustCompile(
		`(?is)<twig:([A-Za-z_][A-Za-z0-9_:-]*)\s+([^>]*)$`,
	)
	componentHTMLAttributePattern = regexp.MustCompile(
		`(?:^|\s):?([A-Za-z_][A-Za-z0-9_-]*)\s*=`,
	)
	componentBlockNamePattern = regexp.MustCompile(
		`(?is)<twig:block\s+[^>]*\bname\s*=\s*['"][A-Za-z0-9_-]*$`,
	)
	liveActionAttributePattern = regexp.MustCompile(
		`(?is)\bdata-live-action-param\s*=\s*["']([^"']*)["']`,
	)
	liveArgumentAttributePattern = regexp.MustCompile(
		`(?i)\bdata-live-([a-z0-9_-]+)-param\s*=`,
	)
)

type TwigComponentCompletionProvider struct {
	index *twigcomponent.Index
}

func NewTwigComponentCompletionProvider(
	index *twigcomponent.Index,
) *TwigComponentCompletionProvider {
	return &TwigComponentCompletionProvider{index: index}
}

func (p *TwigComponentCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".twig",
		) {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	path, _ := uriutil.Path(request.TextDocument.URI)
	if argument, found := liveActionArgumentCompletionContext(
		request.DocumentContent,
		offset,
	); found {
		return p.liveActionArgumentCompletions(
			path,
			argument,
			request.LineIndex,
		)
	}
	if action, present, found := twigcomponent.LiveActionArgumentContext(
		request.Node,
	); found {
		return p.liveActionHashArgumentCompletions(
			path,
			action,
			present,
		)
	}
	if _, found := twigcomponent.LiveActionReferenceAt(
		path,
		request.Root,
		offset,
	); found {
		return p.liveActionCompletions(path)
	}
	before := sourceBefore(request.DocumentContent, offset)
	if componentBlockNamePattern.MatchString(before) {
		component := twigcomponent.EnclosingHTMLComponent(request.Node)
		if component != "" {
			return p.blockCompletions(component)
		}
	}
	if context, found := htmlPropCompletionContext(before); found {
		return p.propCompletions(context)
	}
	if !componentNameCompletionContext(before) {
		return nil
	}
	components, err := p.index.Components()
	if err != nil {
		return nil
	}
	byName := make(map[string]twigcomponent.Component)
	for _, component := range components {
		existing, exists := byName[component.Name]
		if !exists || existing.Class == "" && component.Class != "" {
			byName[component.Name] = component
		}
	}
	items := make([]protocol.CompletionItem, 0, len(byName))
	for name, component := range byName {
		detail := "anonymous Twig component"
		if component.Class != "" {
			detail = component.Class
		}
		if component.Live {
			detail += " • live component"
		}
		items = append(items, protocol.CompletionItem{
			Label:  name,
			Kind:   int(protocol.ClassCompletion),
			Detail: detail,
		})
	}
	sortCompletionItems(items)
	return items
}

func (p *TwigComponentCompletionProvider) liveActionCompletions(
	path string,
) []protocol.CompletionItem {
	actions, err := p.index.LiveActionsForTemplate(path)
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(actions))
	seen := make(map[string]struct{}, len(actions))
	for _, action := range actions {
		key := strings.ToLower(action.Name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		detail := action.Class + "::" + action.Method + "()"
		if len(action.Parameters) != 0 {
			names := make([]string, 0, len(action.Parameters))
			for _, parameter := range action.Parameters {
				phpName := parameter.PHPName
				if phpName == "" {
					phpName = parameter.Name
				}
				name := "$" + phpName
				if parameter.Type != "" {
					name = parameter.Type + " " + name
				}
				if parameter.Optional {
					name += "?"
				}
				names = append(names, name)
			}
			detail = action.Class + "::" + action.Method +
				"(" + strings.Join(names, ", ") + ")"
		}
		items = append(items, protocol.CompletionItem{
			Label:  action.Name,
			Kind:   int(protocol.MethodCompletion),
			Detail: detail,
		})
	}
	sortCompletionItems(items)
	return items
}

func (p *TwigComponentCompletionProvider) liveActionHashArgumentCompletions(
	path,
	actionName string,
	present []string,
) []protocol.CompletionItem {
	actions, err := p.index.LiveActionsForTemplate(path)
	if err != nil {
		return nil
	}
	configured := make(map[string]struct{}, len(present))
	for _, name := range present {
		configured[strings.ToLower(name)] = struct{}{}
	}
	seen := make(map[string]struct{})
	var items []protocol.CompletionItem
	for _, action := range actions {
		if !strings.EqualFold(action.Name, actionName) {
			continue
		}
		for _, parameter := range action.Parameters {
			key := strings.ToLower(parameter.Name)
			if _, exists := configured[key]; exists {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			detail := parameter.Type + " $" + parameter.PHPName
			if parameter.Name != parameter.PHPName {
				detail += " • LiveArg(" + parameter.Name + ")"
			}
			items = append(items, protocol.CompletionItem{
				Label:            parameter.Name,
				Kind:             int(protocol.PropertyCompletion),
				Detail:           strings.TrimSpace(detail),
				InsertText:       parameter.Name + ": $0",
				InsertTextFormat: int(protocol.SnippetTextFormat),
			})
		}
	}
	sortCompletionItems(items)
	return items
}

type liveActionArgumentContext struct {
	Action  string
	Present map[string]struct{}
	Start   uint32
	End     uint32
}

func liveActionArgumentCompletionContext(
	source []byte,
	offset uint32,
) (liveActionArgumentContext, bool) {
	if uint64(offset) > uint64(len(source)) {
		offset = uint32(len(source))
	}
	before := string(source[:offset])
	open := strings.LastIndexByte(before, '<')
	close := strings.LastIndexByte(before, '>')
	if open < 0 || close > open {
		return liveActionArgumentContext{}, false
	}
	fragment := before[open:]
	currentStart := strings.LastIndexAny(fragment, " \t\r\n")
	if currentStart < 0 {
		return liveActionArgumentContext{}, false
	}
	currentStart++
	current := fragment[currentStart:]
	if !strings.HasPrefix(
		strings.ToLower(current),
		"data-live-",
	) || strings.Contains(current, "=") {
		return liveActionArgumentContext{}, false
	}
	actionMatch := liveActionAttributePattern.FindStringSubmatch(fragment)
	if len(actionMatch) != 2 {
		return liveActionArgumentContext{}, false
	}
	action := strings.TrimSpace(actionMatch[1])
	if separator := strings.LastIndexByte(action, '|'); separator >= 0 {
		action = strings.TrimSpace(action[separator+1:])
	}
	if action == "" {
		return liveActionArgumentContext{}, false
	}
	present := make(map[string]struct{})
	for _, match := range liveArgumentAttributePattern.FindAllStringSubmatch(
		fragment,
		-1,
	) {
		if len(match) != 2 ||
			strings.EqualFold(match[1], "action") {
			continue
		}
		present[strings.ToLower(liveArgumentName(match[1]))] = struct{}{}
	}
	start := uint32(open + currentStart)
	return liveActionArgumentContext{
		Action:  action,
		Present: present,
		Start:   start,
		End:     offset,
	}, true
}

func (p *TwigComponentCompletionProvider) liveActionArgumentCompletions(
	path string,
	context liveActionArgumentContext,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	actions, err := p.index.LiveActionsForTemplate(path)
	if err != nil {
		return nil
	}
	startLine, startCharacter := lineIndex.PositionUTF16(context.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(context.End)
	editRange := protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
	seen := make(map[string]struct{})
	var items []protocol.CompletionItem
	for _, action := range actions {
		if !strings.EqualFold(action.Name, context.Action) {
			continue
		}
		for _, parameter := range action.Parameters {
			key := strings.ToLower(parameter.Name)
			if _, exists := seen[key]; exists {
				continue
			}
			if _, exists := context.Present[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			attribute := "data-live-" +
				liveArgumentAttributeSegment(parameter.Name) +
				"-param"
			detail := parameter.Type + " $" + parameter.PHPName
			if parameter.Name != parameter.PHPName {
				detail += " • LiveArg(" + parameter.Name + ")"
			}
			items = append(items, protocol.CompletionItem{
				Label:  attribute,
				Kind:   int(protocol.PropertyCompletion),
				Detail: strings.TrimSpace(detail),
				TextEdit: protocol.TextEdit{
					Range:   editRange,
					NewText: attribute + `="$0"`,
				},
				InsertTextFormat: int(protocol.SnippetTextFormat),
			})
		}
	}
	sortCompletionItems(items)
	return items
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

func liveArgumentAttributeSegment(value string) string {
	var result strings.Builder
	for index, char := range value {
		if char >= 'A' && char <= 'Z' {
			if index != 0 {
				result.WriteByte('-')
			}
			result.WriteByte(byte(char - 'A' + 'a'))
			continue
		}
		if char == '_' {
			result.WriteByte('-')
			continue
		}
		result.WriteRune(char)
	}
	return strings.Trim(result.String(), "-")
}

func (p *TwigComponentCompletionProvider) blockCompletions(
	component string,
) []protocol.CompletionItem {
	blocks, err := p.index.Blocks(component)
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(blocks))
	seen := make(map[string]struct{})
	for _, block := range blocks {
		if _, exists := seen[block.Name]; exists {
			continue
		}
		seen[block.Name] = struct{}{}
		items = append(items, protocol.CompletionItem{
			Label:  block.Name,
			Kind:   int(protocol.ReferenceCompletion),
			Detail: filepath.Base(block.File),
		})
	}
	sortCompletionItems(items)
	return items
}

type htmlPropContext struct {
	Component string
	Dynamic   bool
	Present   map[string]struct{}
}

func htmlPropCompletionContext(
	before string,
) (htmlPropContext, bool) {
	match := componentHTMLPropsPattern.FindStringSubmatch(before)
	if len(match) != 3 {
		return htmlPropContext{}, false
	}
	attributes := match[2]
	current := attributes
	if whitespace := strings.LastIndexAny(current, " \t\r\n"); whitespace >= 0 {
		current = current[whitespace+1:]
	}
	context := htmlPropContext{
		Component: match[1],
		Dynamic:   strings.HasPrefix(current, ":"),
		Present:   make(map[string]struct{}),
	}
	for _, attribute := range componentHTMLAttributePattern.FindAllStringSubmatch(
		attributes,
		-1,
	) {
		if len(attribute) == 2 {
			context.Present[strings.ToLower(attribute[1])] = struct{}{}
		}
	}
	return context, true
}

func componentNameCompletionContext(before string) bool {
	return componentFunctionCompletionPattern.MatchString(before) ||
		componentBlockCompletionPattern.MatchString(before) ||
		componentHTMLCompletionPattern.MatchString(before)
}

func (p *TwigComponentCompletionProvider) propCompletions(
	context htmlPropContext,
) []protocol.CompletionItem {
	props, err := p.index.Props(context.Component)
	if err != nil {
		return nil
	}
	var items []protocol.CompletionItem
	for _, prop := range props {
		if _, present := context.Present[strings.ToLower(prop.Name)]; present {
			continue
		}
		labels := []string{prop.Name}
		if context.Dynamic {
			labels = []string{":" + prop.Name}
		}
		for _, label := range labels {
			detail := componentPropDetail(prop)
			if detail == "" {
				detail = "Twig component prop"
			}
			items = append(items, protocol.CompletionItem{
				Label:            label,
				Kind:             int(protocol.PropertyCompletion),
				Detail:           detail,
				InsertText:       label + `="$0"`,
				InsertTextFormat: int(protocol.SnippetTextFormat),
			})
		}
	}
	sortCompletionItems(items)
	return items
}

func sourceBefore(source []byte, offset uint32) string {
	if uint64(offset) > uint64(len(source)) {
		offset = uint32(len(source))
	}
	return string(source[:offset])
}

func (p *TwigComponentCompletionProvider) GetTriggerCharacters() []string {
	return []string{":", "'", `"`, " "}
}
