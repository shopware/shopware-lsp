package completion

import (
	"context"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const maxLocalResourceCandidates = 500

var (
	yamlResourcePrefix = regexp.MustCompile(
		`(?:^|[-,{])[ \t]*resource[ \t]*:[ \t]*(['"]?)`,
	)
	yamlPathPrefix = regexp.MustCompile(
		`(?:^|[-,{])[ \t]*path[ \t]*:[ \t]*(['"]?)`,
	)
	xmlResourcePrefix = regexp.MustCompile(
		`\bresource[ \t]*=[ \t]*(['"])`,
	)
)

// BundleResourceCompletionProvider ports the reference plugin's bundle and
// directory-scoped resource completion to native YAML/XML/PHP syntax.
type BundleResourceCompletionProvider struct {
	resolver *symfony.RouteResourceResolver
}

func NewBundleResourceCompletionProvider(
	phpIndex *php.PHPIndex,
) *BundleResourceCompletionProvider {
	return &BundleResourceCompletionProvider{
		resolver: symfony.NewRouteResourceResolver(phpIndex),
	}
}

func (provider *BundleResourceCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if provider == nil || provider.resolver == nil || request == nil ||
		request.CompletionParams == nil || request.LineIndex == nil {
		return nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	rng, found := bundleResourceCompletionRange(
		string(request.DocumentContent),
		offset,
		strings.ToLower(filepath.Ext(path)),
		request.Node,
	)
	if !found {
		return nil
	}

	editRange := completionProtocolRange(rng, request.LineIndex)
	items := make([]protocol.CompletionItem, 0)
	seen := make(map[string]struct{})
	for _, candidate := range provider.resolver.BundleResourceCandidates(ctx) {
		if ctx.Err() != nil {
			return nil
		}
		label := strings.TrimPrefix(candidate.Value, "@")
		items = appendResourceCompletion(
			items,
			seen,
			label,
			candidate.Value,
			"Symfony bundle resource · "+candidate.Path,
			protocol.FileCompletion,
			editRange,
		)
	}
	for _, candidate := range localResourceCandidates(ctx, path) {
		if ctx.Err() != nil {
			return nil
		}
		kind := protocol.FileCompletion
		if candidate.directory {
			kind = protocol.FolderCompletion
		}
		items = appendResourceCompletion(
			items,
			seen,
			candidate.value,
			candidate.value,
			"Resource relative to this configuration file",
			kind,
			editRange,
		)
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].Label < items[right].Label
	})
	return items
}

func appendResourceCompletion(
	items []protocol.CompletionItem,
	seen map[string]struct{},
	label,
	value,
	detail string,
	kind protocol.CompletionItemKind,
	editRange protocol.Range,
) []protocol.CompletionItem {
	key := value
	if _, duplicate := seen[key]; duplicate || value == "" {
		return items
	}
	seen[key] = struct{}{}
	return append(items, protocol.CompletionItem{
		Label:      label,
		FilterText: value + " " + label,
		Kind:       int(kind),
		Detail:     detail,
		TextEdit: protocol.TextEdit{
			Range:   editRange,
			NewText: value,
		},
	})
}

func bundleResourceCompletionRange(
	source string,
	offset uint32,
	extension string,
	node *cst.Node,
) (cst.TextRange, bool) {
	if int(offset) > len(source) {
		return cst.TextRange{}, false
	}
	switch extension {
	case ".yaml", ".yml":
		return yamlBundleResourceCompletionRange(source, offset)
	case ".xml":
		return xmlBundleResourceCompletionRange(source, offset)
	case ".php":
		return symfony.PHPRouteResourceCompletionRangeAt(node)
	default:
		return cst.TextRange{}, false
	}
}

func yamlBundleResourceCompletionRange(
	source string,
	offset uint32,
) (cst.TextRange, bool) {
	cursor := int(offset)
	lineStart := strings.LastIndexByte(source[:cursor], '\n') + 1
	lineEnd := len(source)
	if end := strings.IndexByte(source[cursor:], '\n'); end >= 0 {
		lineEnd = cursor + end
	}
	before := source[lineStart:cursor]
	matches := yamlResourcePrefix.FindAllStringSubmatchIndex(before, -1)
	if len(matches) == 0 &&
		yamlNestedResourcePathContext(source, lineStart, before) {
		matches = yamlPathPrefix.FindAllStringSubmatchIndex(before, -1)
	}
	if len(matches) == 0 {
		return cst.TextRange{}, false
	}
	match := matches[len(matches)-1]
	start := lineStart + match[1]
	quote := byte(0)
	if match[2] >= 0 && match[3] > match[2] {
		quote = before[match[2]]
	}
	end := resourceValueEnd(source, start, lineEnd, quote)
	if cursor < start || cursor > end {
		return cst.TextRange{}, false
	}
	return cst.TextRange{
		Start: uint32(start),
		End:   uint32(end),
	}, true
}

func xmlBundleResourceCompletionRange(
	source string,
	offset uint32,
) (cst.TextRange, bool) {
	cursor := int(offset)
	tagStart := strings.LastIndex(source[:cursor], "<import")
	if tagStart < 0 {
		return cst.TextRange{}, false
	}
	nameEnd := tagStart + len("<import")
	if nameEnd < len(source) &&
		source[nameEnd] != ' ' && source[nameEnd] != '\t' &&
		source[nameEnd] != '\r' && source[nameEnd] != '\n' &&
		source[nameEnd] != '>' && source[nameEnd] != '/' {
		return cst.TextRange{}, false
	}
	tagEnd := len(source)
	if end := strings.IndexByte(source[tagStart:], '>'); end >= 0 {
		tagEnd = tagStart + end
		if tagEnd < cursor {
			return cst.TextRange{}, false
		}
	}
	before := source[tagStart:cursor]
	matches := xmlResourcePrefix.FindAllStringSubmatchIndex(before, -1)
	if len(matches) == 0 {
		return cst.TextRange{}, false
	}
	match := matches[len(matches)-1]
	start := tagStart + match[1]
	quote := before[match[2]]
	end := resourceValueEnd(source, start, tagEnd, quote)
	if cursor < start || cursor > end {
		return cst.TextRange{}, false
	}
	return cst.TextRange{
		Start: uint32(start),
		End:   uint32(end),
	}, true
}

func yamlNestedResourcePathContext(
	source string,
	lineStart int,
	before string,
) bool {
	pathMatch := yamlPathPrefix.FindAllStringIndex(before, -1)
	if len(pathMatch) == 0 {
		return false
	}
	match := pathMatch[len(pathMatch)-1]
	if strings.Contains(before[:match[0]], "resource:") {
		return true
	}
	currentIndent := leadingYAMLIndent(source[lineStart:])
	searchEnd := lineStart
	for searchEnd > 0 {
		previousEnd := searchEnd - 1
		previousStart := strings.LastIndexByte(
			source[:previousEnd],
			'\n',
		) + 1
		line := source[previousStart:previousEnd]
		searchEnd = previousStart
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := leadingYAMLIndent(line)
		if indent >= currentIndent {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "resource:") {
			return false
		}
		return strings.TrimSpace(
			strings.TrimPrefix(trimmed, "resource:"),
		) == ""
	}
	return false
}

func leadingYAMLIndent(line string) int {
	indent := 0
	for indent < len(line) &&
		(line[indent] == ' ' || line[indent] == '\t') {
		indent++
	}
	return indent
}

func resourceValueEnd(
	source string,
	start,
	limit int,
	quote byte,
) int {
	if quote != 0 {
		if end := strings.IndexByte(source[start:limit], quote); end >= 0 {
			return start + end
		}
		return limit
	}
	end := start
	for end < limit {
		switch source[end] {
		case ' ', '\t', ',', '}', '#':
			return end
		default:
			end++
		}
	}
	return end
}

func completionProtocolRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}

type localResourceCandidate struct {
	value     string
	directory bool
}

func localResourceCandidates(
	ctx context.Context,
	currentPath string,
) []localResourceCandidate {
	root := filepath.Dir(currentPath)
	var result []localResourceCandidate
	_ = filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		err error,
	) error {
		if ctx.Err() != nil ||
			len(result) >= maxLocalResourceCandidates {
			return fs.SkipAll
		}
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root || filepath.Clean(path) == filepath.Clean(currentPath) {
			return nil
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return nil
		}
		result = append(result, localResourceCandidate{
			value:     filepath.ToSlash(relative),
			directory: entry.IsDir(),
		})
		return nil
	})
	sort.Slice(result, func(left, right int) bool {
		return result[left].value < result[right].value
	})
	return result
}

func (provider *BundleResourceCompletionProvider) GetTriggerCharacters() []string {
	return []string{"@", "/", "\\", "'", "\""}
}

var _ lsp.CompletionProvider = (*BundleResourceCompletionProvider)(nil)
