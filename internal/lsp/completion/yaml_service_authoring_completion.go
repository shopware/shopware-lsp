package completion

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type yamlServiceOption struct {
	name       string
	detail     string
	deprecated bool
}

var yamlServiceOptions = []yamlServiceOption{
	{name: "abstract", detail: "bool"},
	{name: "alias", detail: "service ID"},
	{name: "arguments", detail: "constructor arguments"},
	{name: "autoconfigure", detail: "bool · Symfony 3.3+"},
	{name: "autowire", detail: "bool · Symfony 2.8+"},
	{name: "autowiring_types", detail: "Symfony 2.8–3.x", deprecated: true},
	{name: "bind", detail: "named argument defaults · Symfony 2.8+"},
	{name: "calls", detail: "post-construction calls"},
	{name: "class", detail: "PHP class"},
	{name: "configurator", detail: "callable · Symfony 2.8+"},
	{name: "decorates", detail: "decorated service ID"},
	{name: "decoration_inner_name", detail: "inner service ID"},
	{name: "decoration_on_invalid", detail: "exception | ignore | null · Symfony 4.4+"},
	{name: "decoration_priority", detail: "int · Symfony 2.8+"},
	{name: "deprecated", detail: "bool | message · Symfony 2.8+"},
	{name: "exclude", detail: "resource exclusions · Symfony 3.3+"},
	{name: "factory", detail: "factory callable · Symfony 2.6+"},
	{name: "factory_class", detail: "legacy factory class", deprecated: true},
	{name: "factory_method", detail: "legacy factory method", deprecated: true},
	{name: "factory_service", detail: "legacy factory service", deprecated: true},
	{name: "file", detail: "required file · Symfony 2.8+"},
	{name: "lazy", detail: "bool"},
	{name: "parent", detail: "parent service ID"},
	{name: "properties", detail: "property injections · Symfony 5.1+"},
	{name: "public", detail: "bool"},
	{name: "resource", detail: "PSR-4 resource · Symfony 3.3+"},
	{name: "scope", detail: "request | prototype · Symfony ≤3", deprecated: true},
	{name: "shared", detail: "bool · Symfony 3.0+"},
	{name: "synchronized", detail: "legacy synchronized service", deprecated: true},
	{name: "synthetic", detail: "bool"},
	{name: "tags", detail: "service tags"},
}

type YAMLServiceAuthoringCompletionProvider struct {
	project *project.Model
}

func NewYAMLServiceAuthoringCompletionProvider(
	model *project.Model,
) *YAMLServiceAuthoringCompletionProvider {
	return &YAMLServiceAuthoringCompletionProvider{project: model}
}

func (provider *YAMLServiceAuthoringCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if provider == nil || request == nil ||
		request.CompletionParams == nil || request.LineIndex == nil {
		return nil
	}
	extension := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	if extension != ".yaml" && extension != ".yml" {
		return nil
	}
	source := string(request.DocumentContent)
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if context, found := yamlServiceOptionContext(source, offset); found {
		return yamlServiceOptionItems(context, request.LineIndex)
	}
	value, found := yamlValueCompletionContext(source, offset)
	if !found {
		return nil
	}
	path, _ := uriutil.Path(request.TextDocument.URI)
	return provider.yamlValueItems(
		value,
		request.LineIndex,
		yamlConfigurationFile(path),
		yamlInsideServiceArguments(source, offset),
	)
}

type yamlServiceOptionCompletionContext struct {
	rng      cst.TextRange
	complete bool
	existing map[string]struct{}
	current  string
}

func yamlServiceOptionContext(
	source string,
	offset uint32,
) (yamlServiceOptionCompletionContext, bool) {
	if int(offset) > len(source) {
		return yamlServiceOptionCompletionContext{}, false
	}
	if context, found := yamlFlowServiceOptionContext(source, offset); found {
		return context, true
	}
	cursor := int(offset)
	lineStart, lineEnd := yamlLineBounds(source, cursor)
	line := strings.TrimSuffix(source[lineStart:lineEnd], "\r")
	indent := leadingYAMLIndent(line)
	keyStart := lineStart + indent
	if cursor < keyStart {
		return yamlServiceOptionCompletionContext{}, false
	}
	keyEnd := yamlIdentifierEnd(source, keyStart, lineEnd)
	if cursor > keyEnd ||
		!yamlOptionPrefix(source[keyStart:cursor]) {
		return yamlServiceOptionCompletionContext{}, false
	}
	complete := yamlKeyHasColon(source, keyEnd, lineEnd)
	parent, found := yamlParentMappingLine(source, lineStart, indent)
	if !found || !parent.emptyValue {
		return yamlServiceOptionCompletionContext{}, false
	}
	grandparent, found := yamlParentMappingLine(
		source,
		parent.start,
		parent.indent,
	)
	if !found {
		return yamlServiceOptionCompletionContext{}, false
	}
	if grandparent.key != "services" {
		if grandparent.key != "_instanceof" {
			return yamlServiceOptionCompletionContext{}, false
		}
		services, servicesFound := yamlParentMappingLine(
			source,
			grandparent.start,
			grandparent.indent,
		)
		if !servicesFound || services.key != "services" {
			return yamlServiceOptionCompletionContext{}, false
		}
	}
	return yamlServiceOptionCompletionContext{
		rng: cst.TextRange{
			Start: uint32(keyStart),
			End:   uint32(keyEnd),
		},
		complete: complete,
		existing: yamlBlockMappingKeys(
			source,
			parent.start,
			parent.indent,
			indent,
		),
		current: source[keyStart:keyEnd],
	}, true
}

func yamlFlowServiceOptionContext(
	source string,
	offset uint32,
) (yamlServiceOptionCompletionContext, bool) {
	cursor := int(offset)
	lineStart, lineEnd := yamlLineBounds(source, cursor)
	before := source[lineStart:cursor]
	open := strings.LastIndexByte(before, '{')
	if open < 0 || strings.LastIndexByte(before, '}') > open {
		return yamlServiceOptionCompletionContext{}, false
	}
	open += lineStart
	containerPrefix := source[lineStart:open]
	colon := strings.IndexByte(containerPrefix, ':')
	if colon < 0 || strings.TrimSpace(containerPrefix[colon+1:]) != "" {
		return yamlServiceOptionCompletionContext{}, false
	}
	indent := leadingYAMLIndent(source[lineStart:lineEnd])
	parent, found := yamlParentMappingLine(source, lineStart, indent)
	if !found || parent.key != "services" {
		return yamlServiceOptionCompletionContext{}, false
	}
	segmentStart := open + 1
	if comma := strings.LastIndexByte(source[open+1:cursor], ','); comma >= 0 {
		segmentStart = open + 1 + comma + 1
	}
	for segmentStart < cursor &&
		(source[segmentStart] == ' ' || source[segmentStart] == '\t') {
		segmentStart++
	}
	keyEnd := yamlIdentifierEnd(source, segmentStart, lineEnd)
	if cursor > keyEnd ||
		!yamlOptionPrefix(source[segmentStart:cursor]) {
		return yamlServiceOptionCompletionContext{}, false
	}
	close := lineEnd
	if end := strings.IndexByte(source[cursor:lineEnd], '}'); end >= 0 {
		close = cursor + end
	}
	return yamlServiceOptionCompletionContext{
		rng: cst.TextRange{
			Start: uint32(segmentStart),
			End:   uint32(keyEnd),
		},
		complete: yamlKeyHasColon(source, keyEnd, close),
		existing: yamlFlowMappingKeys(source[open+1 : close]),
		current:  source[segmentStart:keyEnd],
	}, true
}

type yamlMappingLine struct {
	start      int
	indent     int
	key        string
	emptyValue bool
}

func yamlParentMappingLine(
	source string,
	before,
	childIndent int,
) (yamlMappingLine, bool) {
	searchEnd := before
	for searchEnd > 0 {
		previousEnd := searchEnd
		if previousEnd > 0 && source[previousEnd-1] == '\n' {
			previousEnd--
		}
		if previousEnd > 0 && source[previousEnd-1] == '\r' {
			previousEnd--
		}
		previousStart := strings.LastIndexByte(
			source[:previousEnd],
			'\n',
		) + 1
		line := source[previousStart:previousEnd]
		searchEnd = previousStart
		if strings.TrimSpace(line) == "" ||
			strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := leadingYAMLIndent(line)
		if indent >= childIndent {
			continue
		}
		key, empty, found := yamlMappingKeyLine(line)
		if !found {
			return yamlMappingLine{}, false
		}
		return yamlMappingLine{
			start:      previousStart,
			indent:     indent,
			key:        key,
			emptyValue: empty,
		}, true
	}
	return yamlMappingLine{}, false
}

func yamlMappingKeyLine(line string) (key string, emptyValue, found bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "-") {
		return "", false, false
	}
	colon := strings.IndexByte(trimmed, ':')
	if colon <= 0 {
		return "", false, false
	}
	key = strings.Trim(strings.TrimSpace(trimmed[:colon]), `'"`)
	if key == "" {
		return "", false, false
	}
	rest := strings.TrimSpace(trimmed[colon+1:])
	return key, rest == "" || strings.HasPrefix(rest, "#"), true
}

func yamlBlockMappingKeys(
	source string,
	parentStart,
	parentIndent,
	childIndent int,
) map[string]struct{} {
	result := make(map[string]struct{})
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
				if key, _, found := yamlMappingKeyLine(line); found {
					result[key] = struct{}{}
				}
			}
		}
		lineStart = yamlNextLineStart(source, lineEnd)
	}
	return result
}

func yamlFlowMappingKeys(source string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, segment := range strings.Split(source, ",") {
		if key, _, found := yamlMappingKeyLine(strings.TrimSpace(segment)); found {
			result[key] = struct{}{}
		}
	}
	return result
}

func yamlServiceOptionItems(
	context yamlServiceOptionCompletionContext,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(yamlServiceOptions))
	editRange := completionProtocolRange(context.rng, lineIndex)
	for _, option := range yamlServiceOptions {
		if _, duplicate := context.existing[option.name]; duplicate &&
			option.name != context.current {
			continue
		}
		newText := option.name
		if !context.complete {
			newText += ": "
		}
		items = append(items, protocol.CompletionItem{
			Label:      option.name,
			Kind:       int(protocol.PropertyCompletion),
			Detail:     "Symfony service option · " + option.detail,
			Deprecated: option.deprecated,
			TextEdit: protocol.TextEdit{
				Range:   editRange,
				NewText: newText,
			},
		})
	}
	return items
}

type yamlValueContext struct {
	rng    cst.TextRange
	prefix string
}

func yamlValueCompletionContext(
	source string,
	offset uint32,
) (yamlValueContext, bool) {
	if int(offset) > len(source) {
		return yamlValueContext{}, false
	}
	cursor := int(offset)
	lineStart, lineEnd := yamlLineBounds(source, cursor)
	delimiter := yamlValueDelimiter(source, lineStart, cursor)
	if delimiter < 0 {
		return yamlValueContext{}, false
	}
	start := delimiter + 1
	for start < cursor &&
		(source[start] == ' ' || source[start] == '\t') {
		start++
	}
	prefix := source[start:cursor]
	if strings.ContainsAny(prefix, " \t") ||
		strings.HasPrefix(prefix, "'") ||
		strings.HasPrefix(prefix, `"`) {
		return yamlValueContext{}, false
	}
	end := cursor
	for end < lineEnd && yamlValueTokenCharacter(source[end]) {
		end++
	}
	next := end
	for next < lineEnd &&
		(source[next] == ' ' || source[next] == '\t') {
		next++
	}
	if next < lineEnd && source[next] == ':' {
		return yamlValueContext{}, false
	}
	return yamlValueContext{
		rng: cst.TextRange{
			Start: uint32(start),
			End:   uint32(end),
		},
		prefix: prefix,
	}, true
}

func yamlValueDelimiter(source string, lineStart, cursor int) int {
	inSingle := false
	inDouble := false
	delimiter := -1
	for position := lineStart; position < cursor; position++ {
		switch source[position] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ':', ',', '[':
			if !inSingle && !inDouble {
				delimiter = position
			}
		case '-':
			if !inSingle && !inDouble &&
				strings.TrimSpace(source[lineStart:position]) == "" {
				delimiter = position
			}
		}
	}
	if inSingle || inDouble {
		return -1
	}
	return delimiter
}

func yamlValueTokenCharacter(value byte) bool {
	switch {
	case value >= 'a' && value <= 'z',
		value >= 'A' && value <= 'Z',
		value >= '0' && value <= '9':
		return true
	}
	switch value {
	case '!', '/', '_', '.', '~', '-':
		return true
	default:
		return false
	}
}

func (provider *YAMLServiceAuthoringCompletionProvider) yamlValueItems(
	context yamlValueContext,
	lineIndex *cst.LineIndex,
	configurationFile,
	serviceArguments bool,
) []protocol.CompletionItem {
	editRange := completionProtocolRange(context.rng, lineIndex)
	var items []protocol.CompletionItem
	add := func(label, newText, detail string, kind protocol.CompletionItemKind) {
		items = append(items, protocol.CompletionItem{
			Label:  label,
			Kind:   int(kind),
			Detail: detail,
			TextEdit: protocol.TextEdit{
				Range:   editRange,
				NewText: newText,
			},
		})
	}
	if context.prefix == "" ||
		strings.HasPrefix(context.prefix, "!") {
		for _, tag := range provider.yamlTags(
			configurationFile,
			serviceArguments,
		) {
			add(
				tag,
				tag+" ",
				"YAML/Symfony value tag",
				protocol.KeywordCompletion,
			)
		}
	}
	if context.prefix == "" ||
		!strings.HasPrefix(context.prefix, "!") {
		for _, keyword := range []struct {
			value  string
			detail string
		}{
			{value: "~", detail: "null"},
			{value: "null", detail: "null"},
			{value: "true", detail: "bool"},
			{value: "false", detail: "bool"},
			{value: ".inf", detail: "float"},
		} {
			add(
				keyword.value,
				keyword.value,
				"YAML "+keyword.detail,
				protocol.ValueCompletion,
			)
		}
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].Label < items[right].Label
	})
	return items
}

func (provider *YAMLServiceAuthoringCompletionProvider) yamlTags(
	configurationFile,
	serviceArguments bool,
) []string {
	tags := []string{"!!binary", "!!float", "!!str"}
	if configurationFile {
		tags = append(tags, "!php/const")
	}
	if !serviceArguments {
		return tags
	}
	version, found := project.Version{}, false
	if provider != nil && provider.project != nil {
		version, found = provider.project.DependencyVersion(
			"symfony/dependency-injection",
			"symfony/framework-bundle",
			"symfony/symfony",
		)
	}
	if !found {
		version = project.Version{Major: 7}
	}
	if version.AtLeast(4, 4) {
		tags = append(tags, "!tagged_iterator")
	} else if version.AtLeast(3, 4) {
		tags = append(tags, "!tagged")
	}
	if version.AtLeast(4, 3) {
		tags = append(tags, "!tagged_locator")
	}
	if version.AtLeast(4, 2) {
		tags = append(tags, "!service_locator")
	}
	if version.AtLeast(3, 3) {
		tags = append(tags, "!iterator", "!service")
	}
	return tags
}

func yamlInsideServiceArguments(source string, offset uint32) bool {
	cursor := int(offset)
	lineStart, _ := yamlLineBounds(source, cursor)
	currentLine := source[lineStart:cursor]
	if strings.Contains(currentLine, "arguments:") {
		return true
	}
	indent := leadingYAMLIndent(source[lineStart:])
	parent, found := yamlParentMappingLine(source, lineStart, indent)
	return found && parent.key == "arguments"
}

func yamlConfigurationFile(path string) bool {
	slashPath := filepath.ToSlash(path)
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(slashPath, "/config/") ||
		strings.HasPrefix(base, "config.") ||
		strings.HasPrefix(base, "services.")
}

func yamlLineBounds(source string, cursor int) (int, int) {
	start := strings.LastIndexByte(source[:cursor], '\n') + 1
	end := len(source)
	if position := strings.IndexByte(source[cursor:], '\n'); position >= 0 {
		end = cursor + position
	}
	return start, end
}

func yamlIdentifierEnd(source string, start, limit int) int {
	end := start
	for end < limit {
		value := source[end]
		if value >= 'a' && value <= 'z' ||
			value >= 'A' && value <= 'Z' ||
			value >= '0' && value <= '9' ||
			value == '_' || value == '-' {
			end++
			continue
		}
		break
	}
	return end
}

func yamlOptionPrefix(value string) bool {
	for position := range len(value) {
		character := value[position]
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func yamlKeyHasColon(source string, keyEnd, limit int) bool {
	position := keyEnd
	for position < limit &&
		(source[position] == ' ' || source[position] == '\t') {
		position++
	}
	return position < limit && source[position] == ':'
}

func yamlNextLineStart(source string, position int) int {
	for position < len(source) {
		if source[position] == '\n' {
			return position + 1
		}
		position++
	}
	return len(source)
}

func (provider *YAMLServiceAuthoringCompletionProvider) GetTriggerCharacters() []string {
	return []string{"!", "_"}
}

var _ lsp.CompletionProvider = (*YAMLServiceAuthoringCompletionProvider)(nil)
