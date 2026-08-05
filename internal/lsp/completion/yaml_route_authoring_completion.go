package completion

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type yamlRouteOption struct {
	name       string
	detail     string
	deprecated bool
}

var yamlRouteOptions = []yamlRouteOption{
	{name: "pattern", detail: "legacy path", deprecated: true},
	{name: "defaults", detail: "default parameter values"},
	{name: "path", detail: "URL path"},
	{name: "requirements", detail: "parameter requirements"},
	{name: "methods", detail: "HTTP methods"},
	{name: "condition", detail: "expression"},
	{name: "resource", detail: "imported routing resource"},
	{name: "prefix", detail: "imported route prefix"},
	{name: "schemes", detail: "URL schemes"},
	{name: "host", detail: "route host"},
	{name: "controller", detail: "controller callable"},
}

var yamlRoutePlaceholderPattern = regexp.MustCompile(
	`\{([A-Za-z0-9_]+)(?:<[^{}]*>)?\}`,
)

type YAMLRouteAuthoringCompletionProvider struct {
	routeIndex *symfony.RouteIndexer
}

func NewYAMLRouteAuthoringCompletionProvider(
	routeIndex *symfony.RouteIndexer,
) *YAMLRouteAuthoringCompletionProvider {
	return &YAMLRouteAuthoringCompletionProvider{routeIndex: routeIndex}
}

func (provider *YAMLRouteAuthoringCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if provider == nil || request == nil ||
		request.CompletionParams == nil || request.LineIndex == nil {
		return nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil || !yamlRoutingFile(path) {
		return nil
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".yaml" && extension != ".yml" {
		return nil
	}
	source := string(request.DocumentContent)
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if routeContext, found := yamlRouteOptionContext(source, offset); found {
		return yamlRouteOptionItems(routeContext, request.LineIndex)
	}
	if pathContext, found := yamlRoutePathContext(source, offset); found {
		return provider.yamlRoutePathItems(
			ctx,
			pathContext,
			request.LineIndex,
		)
	}
	requirementContext, found := yamlRouteRequirementContext(source, offset)
	if !found {
		return nil
	}
	return yamlRouteRequirementItems(requirementContext, request.LineIndex)
}

type yamlRoutePathCompletionContext struct {
	rng cst.TextRange
}

func yamlRoutePathContext(
	source string,
	offset uint32,
) (yamlRoutePathCompletionContext, bool) {
	if int(offset) > len(source) {
		return yamlRoutePathCompletionContext{}, false
	}
	if context, found := yamlFlowRoutePathContext(source, offset); found {
		return context, true
	}
	cursor := int(offset)
	lineStart, lineEnd := yamlLineBounds(source, cursor)
	indent := leadingYAMLIndent(source[lineStart:lineEnd])
	if indent == 0 {
		return yamlRoutePathCompletionContext{}, false
	}
	parent, found := yamlParentMappingLine(source, lineStart, indent)
	if !found || parent.indent != 0 || !parent.emptyValue {
		return yamlRoutePathCompletionContext{}, false
	}
	if _, grandparentFound := yamlParentMappingLine(
		source,
		parent.start,
		parent.indent,
	); grandparentFound {
		return yamlRoutePathCompletionContext{}, false
	}
	rng, found := yamlRoutePathValueRange(
		source,
		lineStart,
		lineEnd,
		cursor,
	)
	return yamlRoutePathCompletionContext{rng: rng}, found
}

func yamlFlowRoutePathContext(
	source string,
	offset uint32,
) (yamlRoutePathCompletionContext, bool) {
	cursor := int(offset)
	lineStart, lineEnd := yamlLineBounds(source, cursor)
	opens := yamlUnclosedFlowMappingOpens(source, lineStart, cursor)
	if len(opens) != 1 ||
		leadingYAMLIndent(source[lineStart:lineEnd]) != 0 {
		return yamlRoutePathCompletionContext{}, false
	}
	open := opens[0]
	_, empty, found := yamlMappingKeyLine(source[lineStart:open])
	if !found || !empty {
		return yamlRoutePathCompletionContext{}, false
	}
	segmentStart := yamlFlowSegmentStart(source, open+1, cursor)
	segmentEnd := yamlFlowSegmentEnd(source, cursor, lineEnd)
	rng, found := yamlRoutePathValueRange(
		source,
		segmentStart,
		segmentEnd,
		cursor,
	)
	return yamlRoutePathCompletionContext{rng: rng}, found
}

func yamlRoutePathValueRange(
	source string,
	start,
	end,
	cursor int,
) (cst.TextRange, bool) {
	if cursor < start || cursor > end {
		return cst.TextRange{}, false
	}
	line := source[start:end]
	colon := yamlUnquotedColon(line)
	if colon <= 0 ||
		strings.Trim(strings.TrimSpace(line[:colon]), `'"`) != "path" {
		return cst.TextRange{}, false
	}
	valueStart := start + colon + 1
	for valueStart < end &&
		(source[valueStart] == ' ' || source[valueStart] == '\t') {
		valueStart++
	}
	if cursor < valueStart {
		return cst.TextRange{}, false
	}
	if valueStart < end &&
		(source[valueStart] == '\'' || source[valueStart] == '"') {
		quote := source[valueStart]
		contentStart := valueStart + 1
		closePosition := strings.IndexByte(source[contentStart:end], quote)
		contentEnd := end
		if closePosition >= 0 {
			contentEnd = contentStart + closePosition
		}
		if cursor < contentStart || cursor > contentEnd {
			return cst.TextRange{}, false
		}
		return cst.TextRange{
			Start: uint32(contentStart),
			End:   uint32(contentEnd),
		}, true
	}
	valueEnd := valueStart
	for valueEnd < end &&
		source[valueEnd] != ' ' &&
		source[valueEnd] != '\t' &&
		source[valueEnd] != '#' {
		valueEnd++
	}
	if cursor > valueEnd {
		return cst.TextRange{}, false
	}
	return cst.TextRange{
		Start: uint32(valueStart),
		End:   uint32(valueEnd),
	}, true
}

func (provider *YAMLRouteAuthoringCompletionProvider) yamlRoutePathItems(
	ctx context.Context,
	completionContext yamlRoutePathCompletionContext,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	if provider.routeIndex == nil {
		return nil
	}
	routes, err := provider.routeIndex.GetRoutes()
	if err != nil {
		return nil
	}
	sort.Slice(routes, func(left, right int) bool {
		if routes[left].Path == routes[right].Path {
			return routes[left].Name < routes[right].Name
		}
		return routes[left].Path < routes[right].Path
	})
	editRange := completionProtocolRange(completionContext.rng, lineIndex)
	seen := make(map[string]struct{}, len(routes))
	items := make([]protocol.CompletionItem, 0, len(routes))
	for _, route := range routes {
		if ctx.Err() != nil {
			return nil
		}
		if route.Path == "" || route.Name == "" ||
			strings.HasPrefix(route.Name, "_") {
			continue
		}
		if _, duplicate := seen[route.Path]; duplicate {
			continue
		}
		seen[route.Path] = struct{}{}
		items = append(items, protocol.CompletionItem{
			Label:      route.Path,
			FilterText: route.Path + " " + route.Name,
			Kind:       int(protocol.ReferenceCompletion),
			Detail:     route.Name,
			TextEdit: protocol.TextEdit{
				Range:   editRange,
				NewText: route.Path,
			},
		})
	}
	return items
}

type yamlAuthoringKey struct {
	rng      cst.TextRange
	current  string
	quote    byte
	complete bool
}

func yamlAuthoringKeyAt(
	source string,
	lineStart,
	lineEnd,
	cursor int,
) (yamlAuthoringKey, bool) {
	indent := leadingYAMLIndent(source[lineStart:lineEnd])
	start := lineStart + indent
	if cursor < start || start > lineEnd ||
		(start < lineEnd && source[start] == '-') {
		return yamlAuthoringKey{}, false
	}
	if source[start] == '\'' || source[start] == '"' {
		quote := source[start]
		contentStart := start + 1
		closePosition := strings.IndexByte(source[contentStart:lineEnd], quote)
		tokenEnd := lineEnd
		contentEnd := lineEnd
		if closePosition >= 0 {
			contentEnd = contentStart + closePosition
			tokenEnd = contentEnd + 1
		}
		if cursor < contentStart || cursor > contentEnd ||
			!yamlOptionPrefix(source[contentStart:cursor]) {
			return yamlAuthoringKey{}, false
		}
		return yamlAuthoringKey{
			rng: cst.TextRange{
				Start: uint32(start),
				End:   uint32(tokenEnd),
			},
			current:  source[contentStart:contentEnd],
			quote:    quote,
			complete: yamlKeyHasColon(source, tokenEnd, lineEnd),
		}, true
	}
	end := yamlIdentifierEnd(source, start, lineEnd)
	if cursor > end || !yamlOptionPrefix(source[start:cursor]) {
		return yamlAuthoringKey{}, false
	}
	return yamlAuthoringKey{
		rng: cst.TextRange{
			Start: uint32(start),
			End:   uint32(end),
		},
		current:  source[start:end],
		complete: yamlKeyHasColon(source, end, lineEnd),
	}, true
}

type yamlRouteOptionCompletionContext struct {
	key      yamlAuthoringKey
	existing map[string]struct{}
}

func yamlRouteOptionContext(
	source string,
	offset uint32,
) (yamlRouteOptionCompletionContext, bool) {
	if int(offset) > len(source) {
		return yamlRouteOptionCompletionContext{}, false
	}
	if context, found := yamlFlowRouteOptionContext(source, offset); found {
		return context, true
	}
	cursor := int(offset)
	lineStart, lineEnd := yamlLineBounds(source, cursor)
	key, found := yamlAuthoringKeyAt(source, lineStart, lineEnd, cursor)
	if !found {
		return yamlRouteOptionCompletionContext{}, false
	}
	indent := leadingYAMLIndent(source[lineStart:lineEnd])
	parent, found := yamlParentMappingLine(source, lineStart, indent)
	if !found || !parent.emptyValue || parent.indent != 0 {
		return yamlRouteOptionCompletionContext{}, false
	}
	if _, grandparentFound := yamlParentMappingLine(
		source,
		parent.start,
		parent.indent,
	); grandparentFound {
		return yamlRouteOptionCompletionContext{}, false
	}
	return yamlRouteOptionCompletionContext{
		key: key,
		existing: yamlBlockMappingKeys(
			source,
			parent.start,
			parent.indent,
			indent,
		),
	}, true
}

func yamlFlowRouteOptionContext(
	source string,
	offset uint32,
) (yamlRouteOptionCompletionContext, bool) {
	cursor := int(offset)
	lineStart, lineEnd := yamlLineBounds(source, cursor)
	opens := yamlUnclosedFlowMappingOpens(source, lineStart, cursor)
	if len(opens) != 1 {
		return yamlRouteOptionCompletionContext{}, false
	}
	open := opens[0]
	prefix := source[lineStart:open]
	if leadingYAMLIndent(source[lineStart:lineEnd]) != 0 {
		return yamlRouteOptionCompletionContext{}, false
	}
	_, empty, found := yamlMappingKeyLine(prefix)
	if !found || !empty {
		return yamlRouteOptionCompletionContext{}, false
	}
	segmentStart := yamlFlowSegmentStart(source, open+1, cursor)
	key, found := yamlAuthoringKeyAt(
		source,
		segmentStart,
		yamlFlowSegmentEnd(source, cursor, lineEnd),
		cursor,
	)
	if !found {
		return yamlRouteOptionCompletionContext{}, false
	}
	closePosition := yamlMatchingFlowBrace(source, open, lineEnd)
	if closePosition < 0 {
		closePosition = lineEnd
	}
	return yamlRouteOptionCompletionContext{
		key:      key,
		existing: yamlFlowMappingKeys(source[open+1 : closePosition]),
	}, true
}

func yamlRouteOptionItems(
	context yamlRouteOptionCompletionContext,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(yamlRouteOptions))
	editRange := completionProtocolRange(context.key.rng, lineIndex)
	for _, option := range yamlRouteOptions {
		if _, duplicate := context.existing[option.name]; duplicate &&
			option.name != context.key.current {
			continue
		}
		newText := option.name
		if context.key.quote != 0 {
			newText = string(context.key.quote) +
				newText +
				string(context.key.quote)
		}
		if !context.key.complete {
			newText += ": "
		}
		items = append(items, protocol.CompletionItem{
			Label:      option.name,
			Kind:       int(protocol.PropertyCompletion),
			Detail:     "Symfony route option · " + option.detail,
			Deprecated: option.deprecated,
			TextEdit: protocol.TextEdit{
				Range:   editRange,
				NewText: newText,
			},
		})
	}
	return items
}

type yamlRouteRequirementCompletionContext struct {
	key          yamlAuthoringKey
	existing     map[string]struct{}
	placeholders []string
}

func yamlRouteRequirementContext(
	source string,
	offset uint32,
) (yamlRouteRequirementCompletionContext, bool) {
	if int(offset) > len(source) {
		return yamlRouteRequirementCompletionContext{}, false
	}
	if context, found := yamlFlowRouteRequirementContext(source, offset); found {
		return context, true
	}
	cursor := int(offset)
	lineStart, lineEnd := yamlLineBounds(source, cursor)
	key, found := yamlAuthoringKeyAt(source, lineStart, lineEnd, cursor)
	if !found {
		return yamlRouteRequirementCompletionContext{}, false
	}
	indent := leadingYAMLIndent(source[lineStart:lineEnd])
	requirements, found := yamlParentMappingLine(source, lineStart, indent)
	if !found || requirements.key != "requirements" ||
		!requirements.emptyValue {
		return yamlRouteRequirementCompletionContext{}, false
	}
	route, found := yamlParentMappingLine(
		source,
		requirements.start,
		requirements.indent,
	)
	if !found || route.indent != 0 || !route.emptyValue {
		return yamlRouteRequirementCompletionContext{}, false
	}
	if _, grandparentFound := yamlParentMappingLine(
		source,
		route.start,
		route.indent,
	); grandparentFound {
		return yamlRouteRequirementCompletionContext{}, false
	}
	path, found := yamlBlockMappingScalar(
		source,
		route.start,
		route.indent,
		requirements.indent,
		"path",
	)
	if !found {
		path, found = yamlBlockMappingScalar(
			source,
			route.start,
			route.indent,
			requirements.indent,
			"pattern",
		)
	}
	if !found {
		return yamlRouteRequirementCompletionContext{}, false
	}
	return yamlRouteRequirementCompletionContext{
		key: key,
		existing: yamlBlockMappingKeys(
			source,
			requirements.start,
			requirements.indent,
			indent,
		),
		placeholders: yamlRoutePlaceholders(path),
	}, true
}

func yamlFlowRouteRequirementContext(
	source string,
	offset uint32,
) (yamlRouteRequirementCompletionContext, bool) {
	cursor := int(offset)
	lineStart, lineEnd := yamlLineBounds(source, cursor)
	opens := yamlUnclosedFlowMappingOpens(source, lineStart, cursor)
	if len(opens) != 2 ||
		leadingYAMLIndent(source[lineStart:lineEnd]) != 0 {
		return yamlRouteRequirementCompletionContext{}, false
	}
	routeOpen := opens[0]
	requirementsOpen := opens[1]
	_, routeEmpty, routeFound := yamlMappingKeyLine(
		source[lineStart:routeOpen],
	)
	if !routeFound || !routeEmpty {
		return yamlRouteRequirementCompletionContext{}, false
	}
	requirementSegment := source[yamlFlowSegmentStart(
		source,
		routeOpen+1,
		requirementsOpen,
	):requirementsOpen]
	requirementKey, requirementEmpty, found := yamlMappingKeyLine(
		requirementSegment,
	)
	if !found || requirementKey != "requirements" || !requirementEmpty {
		return yamlRouteRequirementCompletionContext{}, false
	}
	segmentStart := yamlFlowSegmentStart(
		source,
		requirementsOpen+1,
		cursor,
	)
	key, found := yamlAuthoringKeyAt(
		source,
		segmentStart,
		yamlFlowSegmentEnd(source, cursor, lineEnd),
		cursor,
	)
	if !found {
		return yamlRouteRequirementCompletionContext{}, false
	}
	routeClose := yamlMatchingFlowBrace(source, routeOpen, lineEnd)
	if routeClose < 0 {
		routeClose = lineEnd
	}
	path, found := yamlFlowMappingScalar(
		source[routeOpen+1:routeClose],
		"path",
	)
	if !found {
		path, found = yamlFlowMappingScalar(
			source[routeOpen+1:routeClose],
			"pattern",
		)
	}
	if !found {
		return yamlRouteRequirementCompletionContext{}, false
	}
	requirementsClose := yamlMatchingFlowBrace(
		source,
		requirementsOpen,
		lineEnd,
	)
	if requirementsClose < 0 {
		requirementsClose = lineEnd
	}
	return yamlRouteRequirementCompletionContext{
		key: key,
		existing: yamlFlowMappingKeys(
			source[requirementsOpen+1 : requirementsClose],
		),
		placeholders: yamlRoutePlaceholders(path),
	}, true
}

func yamlRouteRequirementItems(
	context yamlRouteRequirementCompletionContext,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(context.placeholders))
	editRange := completionProtocolRange(context.key.rng, lineIndex)
	for _, placeholder := range context.placeholders {
		if _, duplicate := context.existing[placeholder]; duplicate &&
			placeholder != context.key.current {
			continue
		}
		newText := placeholder
		if context.key.quote != 0 {
			newText = string(context.key.quote) +
				newText +
				string(context.key.quote)
		}
		if !context.key.complete {
			newText += ": "
		}
		items = append(items, protocol.CompletionItem{
			Label:  placeholder,
			Kind:   int(protocol.PropertyCompletion),
			Detail: "Symfony route path parameter",
			TextEdit: protocol.TextEdit{
				Range:   editRange,
				NewText: newText,
			},
		})
	}
	return items
}

func yamlBlockMappingScalar(
	source string,
	parentStart,
	parentIndent,
	childIndent int,
	wanted string,
) (string, bool) {
	lineStart := yamlNextLineStart(source, parentStart)
	for lineStart < len(source) {
		lineEnd := lineStart
		for lineEnd < len(source) &&
			source[lineEnd] != '\n' && source[lineEnd] != '\r' {
			lineEnd++
		}
		line := source[lineStart:lineEnd]
		if strings.TrimSpace(line) != "" {
			indent := leadingYAMLIndent(line)
			if indent <= parentIndent {
				break
			}
			if indent == childIndent {
				key, value, found := yamlMappingKeyValueLine(line)
				if found && key == wanted {
					return yamlScalarText(value)
				}
			}
		}
		lineStart = yamlNextLineStart(source, lineEnd)
	}
	return "", false
}

func yamlFlowMappingScalar(source, wanted string) (string, bool) {
	for _, segment := range yamlFlowSegments(source) {
		key, value, found := yamlMappingKeyValueLine(segment)
		if found && key == wanted {
			return yamlScalarText(value)
		}
	}
	return "", false
}

func yamlMappingKeyValueLine(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	colon := yamlUnquotedColon(trimmed)
	if colon <= 0 {
		return "", "", false
	}
	key := strings.Trim(strings.TrimSpace(trimmed[:colon]), `'"`)
	value := strings.TrimSpace(trimmed[colon+1:])
	if key == "" || value == "" {
		return "", "", false
	}
	return key, value, true
}

func yamlScalarText(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "{") ||
		strings.HasPrefix(value, "[") {
		return "", false
	}
	if (value[0] == '\'' || value[0] == '"') &&
		len(value) >= 2 && value[len(value)-1] == value[0] {
		value = value[1 : len(value)-1]
	}
	return value, value != ""
}

func yamlRoutePlaceholders(path string) []string {
	matches := yamlRoutePlaceholderPattern.FindAllStringSubmatch(path, -1)
	result := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		if _, duplicate := seen[match[1]]; duplicate {
			continue
		}
		seen[match[1]] = struct{}{}
		result = append(result, match[1])
	}
	return result
}

func yamlRoutingFile(path string) bool {
	slashPath := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(base, "routing") ||
		strings.Contains(slashPath, "/routing") ||
		base == "routes.yaml" ||
		base == "routes.yml" ||
		strings.Contains(slashPath, "/config/routes/")
}

func yamlUnclosedFlowMappingOpens(
	source string,
	start,
	end int,
) []int {
	var stack []int
	inSingle := false
	inDouble := false
	for position := start; position < end; position++ {
		switch source[position] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle &&
				(position == start || source[position-1] != '\\') {
				inDouble = !inDouble
			}
		case '{':
			if !inSingle && !inDouble {
				stack = append(stack, position)
			}
		case '}':
			if !inSingle && !inDouble && len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return stack
}

func yamlMatchingFlowBrace(source string, open, limit int) int {
	depth := 0
	inSingle := false
	inDouble := false
	for position := open; position < limit; position++ {
		switch source[position] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle &&
				(position == open || source[position-1] != '\\') {
				inDouble = !inDouble
			}
		case '{':
			if !inSingle && !inDouble {
				depth++
			}
		case '}':
			if !inSingle && !inDouble {
				depth--
				if depth == 0 {
					return position
				}
			}
		}
	}
	return -1
}

func yamlFlowSegmentStart(source string, start, end int) int {
	depth := 0
	inSingle := false
	inDouble := false
	segmentStart := start
	for position := start; position < end; position++ {
		switch source[position] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle &&
				(position == start || source[position-1] != '\\') {
				inDouble = !inDouble
			}
		case '{', '[':
			if !inSingle && !inDouble {
				depth++
			}
		case '}', ']':
			if !inSingle && !inDouble && depth > 0 {
				depth--
			}
		case ',':
			if !inSingle && !inDouble && depth == 0 {
				segmentStart = position + 1
			}
		}
	}
	return segmentStart
}

func yamlFlowSegmentEnd(source string, start, limit int) int {
	depth := 0
	inSingle := false
	inDouble := false
	for position := start; position < limit; position++ {
		switch source[position] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle &&
				(position == start || source[position-1] != '\\') {
				inDouble = !inDouble
			}
		case '{', '[':
			if !inSingle && !inDouble {
				depth++
			}
		case '}', ']':
			if !inSingle && !inDouble {
				if depth == 0 {
					return position
				}
				depth--
			}
		case ',':
			if !inSingle && !inDouble && depth == 0 {
				return position
			}
		}
	}
	return limit
}

func yamlFlowSegments(source string) []string {
	var result []string
	start := 0
	for start <= len(source) {
		end := yamlFlowSegmentEnd(source, start, len(source))
		result = append(result, source[start:end])
		if end >= len(source) {
			break
		}
		start = end + 1
	}
	return result
}

func yamlUnquotedColon(value string) int {
	inSingle := false
	inDouble := false
	for position := range len(value) {
		switch value[position] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle &&
				(position == 0 || value[position-1] != '\\') {
				inDouble = !inDouble
			}
		case ':':
			if !inSingle && !inDouble {
				return position
			}
		}
	}
	return -1
}

func (provider *YAMLRouteAuthoringCompletionProvider) GetTriggerCharacters() []string {
	return []string{"_", "{"}
}

var _ lsp.CompletionProvider = (*YAMLRouteAuthoringCompletionProvider)(nil)
