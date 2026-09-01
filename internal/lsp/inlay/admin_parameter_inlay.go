package inlay

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/lsp/signature"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminParameterProvider renders parameter names for Administration calls
// whose callable contract is already understood by AdminSignatureProvider.
// It intentionally adds no independent type-resolution rules.
type AdminParameterProvider struct {
	signatures *signature.AdminSignatureProvider
}

func NewAdminParameterProvider(
	index *admin.AdminComponentIndexer,
) *AdminParameterProvider {
	return &AdminParameterProvider{
		signatures: signature.NewAdminSignatureProvider(index),
	}
}

func (p *AdminParameterProvider) GetInlayHints(
	ctx context.Context,
	request *lsp.InlayHintRequest,
) ([]protocol.InlayHint, error) {
	if ctx.Err() != nil || p == nil || p.signatures == nil ||
		request == nil || request.InlayHintParams == nil ||
		request.Document == nil || request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	document := request.Document
	if document.SyntaxLanguage == language.Vue {
		javascriptDocument := *document
		javascriptDocument.SyntaxLanguage = language.JavaScript
		javascriptRequest := *request
		javascriptRequest.Document = &javascriptDocument
		javascriptHints, err := p.GetInlayHints(ctx, &javascriptRequest)
		if err != nil {
			return nil, err
		}
		twigDocument := *document
		twigDocument.SyntaxLanguage = language.Twig
		twigRequest := *request
		twigRequest.Document = &twigDocument
		twigHints, err := p.GetInlayHints(ctx, &twigRequest)
		if err != nil {
			return nil, err
		}
		return deduplicateAdminParameterHints(
			append(javascriptHints, twigHints...),
		), nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(
		strings.ToLower(strings.ReplaceAll(path, "\\", "/")),
		"/resources/app/administration/",
	) {
		return nil, nil
	}
	rangeStart, rangeEnd := adminInlayRange(document, request.Range)
	var result []protocol.InlayHint
	switch document.SyntaxLanguage {
	case language.JavaScript:
		for _, call := range jsquery.Calls(document.SyntaxTree.Root) {
			if ctx.Err() != nil {
				return result, nil
			}
			arguments := jsquery.IterateArguments(call)
			argumentCount := arguments.Len()
			if argumentCount == 0 {
				continue
			}
			argumentRanges := make([]cst.TextRange, 0, argumentCount)
			for arguments.Next() {
				argument := arguments.Node()
				expression := argument
				cursor := argument.ChildNodeCursor()
				if cursor.Next() {
					expression = cursor.Node()
				}
				argumentRanges = append(
					argumentRanges, expression.RangeTrimmedTrivia(),
				)
			}
			if !adminInlayRangeContainsArgument(
				argumentRanges, rangeStart, rangeEnd,
			) {
				continue
			}
			help, resolveErr := p.signatures.GetSignatureHelpAtOffset(
				ctx, document, call, call.Range().Start,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			for index, rangeValue := range argumentRanges {
				result = appendAdminParameterHint(
					result, document, help, index, rangeValue,
					rangeStart, rangeEnd,
				)
			}
		}
	case language.Twig:
		for _, call := range admin.TwigVueCalls(
			document.SyntaxTree.Root, document.Text,
		) {
			if ctx.Err() != nil {
				return result, nil
			}
			if call.Filter || len(call.Arguments) == 0 {
				continue
			}
			if !adminInlayRangeContainsArgument(
				call.Arguments, rangeStart, rangeEnd,
			) {
				continue
			}
			help, resolveErr := p.signatures.GetSignatureHelpAtOffset(
				ctx, document, nil, call.OpenParen+1,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			for index, rangeValue := range call.Arguments {
				result = appendAdminParameterHint(
					result, document, help, index, rangeValue,
					rangeStart, rangeEnd,
				)
			}
		}
	default:
		return nil, nil
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Position.Line != result[right].Position.Line {
			return result[left].Position.Line < result[right].Position.Line
		}
		if result[left].Position.Character != result[right].Position.Character {
			return result[left].Position.Character <
				result[right].Position.Character
		}
		return inlayLabelText(result[left].Label) <
			inlayLabelText(result[right].Label)
	})
	return deduplicateAdminParameterHints(result), nil
}

func adminInlayRangeContainsArgument(
	arguments []cst.TextRange,
	start,
	end uint32,
) bool {
	for _, argument := range arguments {
		if argument.Len() > 0 && argument.Start >= start &&
			argument.Start <= end {
			return true
		}
	}
	return false
}

func adminInlayRange(
	document *lsp.TextDocument,
	rangeValue protocol.Range,
) (uint32, uint32) {
	start := document.LineIndex.OffsetUTF16(
		uint32(max(rangeValue.Start.Line, 0)),
		uint32(max(rangeValue.Start.Character, 0)),
	)
	end := document.LineIndex.OffsetUTF16(
		uint32(max(rangeValue.End.Line, 0)),
		uint32(max(rangeValue.End.Character, 0)),
	)
	if end < start {
		return end, start
	}
	return start, end
}

func appendAdminParameterHint(
	result []protocol.InlayHint,
	document *lsp.TextDocument,
	help *protocol.SignatureHelp,
	argumentIndex int,
	argumentRange cst.TextRange,
	rangeStart,
	rangeEnd uint32,
) []protocol.InlayHint {
	if argumentRange.Len() == 0 || argumentRange.End > uint32(len(document.Text)) ||
		argumentRange.Start < rangeStart || argumentRange.Start > rangeEnd {
		return result
	}
	name, tooltip, found := adminParameterHint(help, argumentIndex)
	if !found || adminArgumentHasParameterName(
		document.Text[argumentRange.Start:argumentRange.End], name,
	) {
		return result
	}
	line, character := document.LineIndex.PositionUTF16(argumentRange.Start)
	return append(result, protocol.InlayHint{
		Position: protocol.Position{
			Line: int(line), Character: int(character),
		},
		Label:        name + ":",
		Kind:         protocol.InlayHintKindParameter,
		Tooltip:      tooltip,
		PaddingRight: true,
	})
}

func adminParameterHint(
	help *protocol.SignatureHelp,
	argumentIndex int,
) (string, string, bool) {
	if help == nil || argumentIndex < 0 || len(help.Signatures) == 0 {
		return "", "", false
	}
	var name string
	var details []string
	seenDetails := make(map[string]bool)
	for _, information := range help.Signatures {
		parameter, found := adminSignatureParameterAt(
			information.Parameters, argumentIndex,
		)
		if !found {
			return "", "", false
		}
		candidate, found := adminParameterName(parameter.Label)
		if !found || name != "" && candidate != name {
			return "", "", false
		}
		name = candidate
		detail := parameter.Label
		if information.Label != "" {
			detail += " — " + information.Label
		}
		if !seenDetails[detail] {
			seenDetails[detail] = true
			details = append(details, detail)
		}
	}
	if name == "" {
		return "", "", false
	}
	return name, strings.Join(details, "\n"), true
}

func adminSignatureParameterAt(
	parameters []protocol.ParameterInformation,
	argumentIndex int,
) (protocol.ParameterInformation, bool) {
	if argumentIndex >= 0 && argumentIndex < len(parameters) {
		return parameters[argumentIndex], true
	}
	if len(parameters) > 0 && strings.HasPrefix(
		strings.TrimSpace(parameters[len(parameters)-1].Label), "...",
	) {
		return parameters[len(parameters)-1], true
	}
	return protocol.ParameterInformation{}, false
}

func adminParameterName(label string) (string, bool) {
	colon := strings.IndexByte(label, ':')
	if colon <= 0 {
		return "", false
	}
	name := strings.TrimSpace(label[:colon])
	name = strings.TrimPrefix(name, "...")
	name = strings.TrimSuffix(name, "?")
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	for index, value := range name {
		if index == 0 {
			if value != '_' && value != '$' && !unicode.IsLetter(value) {
				return "", false
			}
			continue
		}
		if value != '_' && value != '$' && !unicode.IsLetter(value) &&
			!unicode.IsDigit(value) {
			return "", false
		}
	}
	return name, true
}

func adminArgumentHasParameterName(argument []byte, name string) bool {
	value := strings.TrimSpace(string(argument))
	if value != name {
		return false
	}
	_, valid := adminParameterName(value + ": unknown")
	return valid
}

func deduplicateAdminParameterHints(
	hints []protocol.InlayHint,
) []protocol.InlayHint {
	result := hints[:0]
	seen := make(map[string]bool, len(hints))
	for _, hint := range hints {
		key := inlayLabelText(hint.Label) + "\x00" +
			strconv.Itoa(hint.Position.Line) + "\x00" +
			strconv.Itoa(hint.Position.Character)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, hint)
	}
	return result
}

func inlayLabelText(label any) string {
	if value, ok := label.(string); ok {
		return value
	}
	return ""
}

var _ lsp.InlayHintProvider = (*AdminParameterProvider)(nil)
