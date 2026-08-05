package completion

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

var (
	liveEventValueCompletionPattern = regexp.MustCompile(
		`(?is)\bdata-live-event-param\s*=\s*["']([^"']*)$`,
	)
	liveEventValuePattern = regexp.MustCompile(
		`(?is)\bdata-live-event-param\s*=\s*["']([^"']*)["']`,
	)
	liveEventActionPattern = regexp.MustCompile(
		`(?is)\bdata-action\s*=\s*["'][^"']*live#emit(?:self|up)?[^"']*["']`,
	)
)

type LiveComponentEventCompletionProvider struct {
	index *twigcomponent.Index
}

func NewLiveComponentEventCompletionProvider(
	index *twigcomponent.Index,
) *LiveComponentEventCompletionProvider {
	return &LiveComponentEventCompletionProvider{index: index}
}

func (p *LiveComponentEventCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Root == nil || request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	path, _ := uriutil.Path(request.TextDocument.URI)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		if event, present, found :=
			twigcomponent.LiveEventArgumentContextPHP(request.Node); found {
			return p.phpArgumentCompletions(event, present)
		}
		if liveEventNameContextPHP(request.Node) {
			return p.eventCompletions()
		}
	case ".twig":
		if argument, found := liveEventArgumentCompletionContext(
			request.DocumentContent,
			offset,
		); found {
			return p.twigArgumentCompletions(
				argument,
				request.LineIndex,
			)
		}
		if _, found := twigcomponent.LiveEventReferenceAtTwig(
			path,
			request.Root,
			offset,
		); found || liveEventNameCompletionContext(
			request.DocumentContent,
			offset,
		) {
			return p.eventCompletions()
		}
	}
	return nil
}

func (p *LiveComponentEventCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", `"`, "-", "_"}
}

func (p *LiveComponentEventCompletionProvider) eventCompletions() []protocol.CompletionItem {
	listeners, err := p.index.LiveListeners()
	if err != nil {
		return nil
	}
	byName := make(map[string][]twigcomponent.LiveListener)
	for _, listener := range listeners {
		key := strings.ToLower(listener.Name)
		byName[key] = append(byName[key], listener)
	}
	items := make([]protocol.CompletionItem, 0, len(byName))
	for _, current := range byName {
		listener := current[0]
		detail := liveListenerSignature(listener)
		if len(current) > 1 {
			detail += " • " + liveEventListenerCount(len(current))
		}
		items = append(items, protocol.CompletionItem{
			Label:  listener.Name,
			Kind:   int(protocol.EventCompletion),
			Detail: detail,
		})
	}
	sortCompletionItems(items)
	return items
}

func (p *LiveComponentEventCompletionProvider) phpArgumentCompletions(
	event string,
	present []string,
) []protocol.CompletionItem {
	listeners, err := p.index.LiveListeners()
	if err != nil {
		return nil
	}
	configured := liveEventConfiguredArguments(present)
	seen := make(map[string]struct{})
	var items []protocol.CompletionItem
	for _, listener := range listeners {
		if !strings.EqualFold(listener.Name, event) {
			continue
		}
		for _, parameter := range listener.Parameters {
			if !parameter.LiveArg {
				continue
			}
			key := strings.ToLower(parameter.Name)
			if _, exists := configured[key]; exists {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, protocol.CompletionItem{
				Label:            parameter.Name,
				Kind:             int(protocol.PropertyCompletion),
				Detail:           liveEventParameterDetail(parameter, listener),
				InsertText:       "'" + parameter.Name + "' => $0",
				InsertTextFormat: int(protocol.SnippetTextFormat),
			})
		}
	}
	sortCompletionItems(items)
	return items
}

func (p *LiveComponentEventCompletionProvider) twigArgumentCompletions(
	context liveEventArgumentCompletion,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	listeners, err := p.index.LiveListeners()
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
	for _, listener := range listeners {
		if !strings.EqualFold(listener.Name, context.Event) {
			continue
		}
		for _, parameter := range listener.Parameters {
			if !parameter.LiveArg {
				continue
			}
			key := strings.ToLower(parameter.Name)
			if _, exists := context.Present[key]; exists {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			attribute := "data-live-" +
				liveArgumentAttributeSegment(parameter.Name) +
				"-param"
			items = append(items, protocol.CompletionItem{
				Label:  attribute,
				Kind:   int(protocol.PropertyCompletion),
				Detail: liveEventParameterDetail(parameter, listener),
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

func liveEventNameContextPHP(node *phpsyntax.Node) bool {
	literal := phpquery.StringAt(node)
	call := phpquery.CallAt(literal)
	if literal == nil || call == nil ||
		call.Kind() != phpsyntax.PhpMemberCall {
		return false
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil ||
		!strings.EqualFold(strings.TrimSpace(receiver.Text()), "$this") {
		return false
	}
	switch strings.ToLower(phpquery.CallMethodName(call)) {
	case "emit", "emitup", "emitself":
	default:
		return false
	}
	index := phpquery.ArgumentIndex(call, literal)
	if index < 0 {
		return false
	}
	name := phpquery.ArgumentName(phpquery.Argument(call, index))
	return index == 0 && name == "" ||
		strings.EqualFold(name, "event") ||
		strings.EqualFold(name, "eventName")
}

func liveEventNameCompletionContext(
	source []byte,
	offset uint32,
) bool {
	if uint64(offset) > uint64(len(source)) {
		offset = uint32(len(source))
	}
	before := string(source[:offset])
	open := strings.LastIndexByte(before, '<')
	close := strings.LastIndexByte(before, '>')
	if open < 0 || close > open {
		return false
	}
	fragment := before[open:]
	return liveEventActionPattern.MatchString(fragment) &&
		liveEventValueCompletionPattern.MatchString(fragment)
}

type liveEventArgumentCompletion struct {
	Event   string
	Present map[string]struct{}
	Start   uint32
	End     uint32
}

func liveEventArgumentCompletionContext(
	source []byte,
	offset uint32,
) (liveEventArgumentCompletion, bool) {
	if uint64(offset) > uint64(len(source)) {
		offset = uint32(len(source))
	}
	before := string(source[:offset])
	open := strings.LastIndexByte(before, '<')
	close := strings.LastIndexByte(before, '>')
	if open < 0 || close > open {
		return liveEventArgumentCompletion{}, false
	}
	fragment := before[open:]
	currentStart := strings.LastIndexAny(fragment, " \t\r\n")
	if currentStart < 0 {
		return liveEventArgumentCompletion{}, false
	}
	currentStart++
	current := fragment[currentStart:]
	if !strings.HasPrefix(
		strings.ToLower(current),
		"data-live-",
	) || strings.Contains(current, "=") ||
		!liveEventActionPattern.MatchString(fragment) {
		return liveEventArgumentCompletion{}, false
	}
	match := liveEventValuePattern.FindStringSubmatch(fragment)
	if len(match) != 2 {
		return liveEventArgumentCompletion{}, false
	}
	event := strings.TrimSpace(match[1])
	if separator := strings.LastIndexByte(event, '|'); separator >= 0 {
		event = strings.TrimSpace(event[separator+1:])
	}
	if event == "" {
		return liveEventArgumentCompletion{}, false
	}
	present := make(map[string]struct{})
	for _, match := range liveArgumentAttributePattern.FindAllStringSubmatch(
		fragment,
		-1,
	) {
		if len(match) != 2 ||
			strings.EqualFold(match[1], "action") ||
			strings.EqualFold(match[1], "event") {
			continue
		}
		present[strings.ToLower(liveArgumentName(match[1]))] = struct{}{}
	}
	return liveEventArgumentCompletion{
		Event:   event,
		Present: present,
		Start:   uint32(open + currentStart),
		End:     offset,
	}, true
}

func liveEventConfiguredArguments(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.ToLower(value)] = struct{}{}
	}
	return result
}

func liveEventParameterDetail(
	parameter twigcomponent.LiveActionParameter,
	listener twigcomponent.LiveListener,
) string {
	detail := parameter.Type + " $" + parameter.PHPName +
		" • " + listener.Class + "::" + listener.Method + "()"
	if parameter.Name != parameter.PHPName {
		detail += " • LiveArg(" + parameter.Name + ")"
	}
	return strings.TrimSpace(detail)
}

func liveListenerSignature(listener twigcomponent.LiveListener) string {
	var parameters []string
	for _, parameter := range listener.Parameters {
		name := "$" + parameter.PHPName
		if parameter.Type != "" {
			name = parameter.Type + " " + name
		}
		if parameter.Optional {
			name += "?"
		}
		parameters = append(parameters, name)
	}
	return listener.Class + "::" + listener.Method +
		"(" + strings.Join(parameters, ", ") + ")"
}

func liveEventListenerCount(count int) string {
	if count == 1 {
		return "1 listener"
	}
	return strconv.Itoa(count) + " listeners"
}
