package signature

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigMacroSignatureProvider struct {
	index *twig.TwigIndexer
}

func NewTwigMacroSignatureProvider(
	index *twig.TwigIndexer,
) *TwigMacroSignatureProvider {
	return &TwigMacroSignatureProvider{index: index}
}

func (p *TwigMacroSignatureProvider) GetSignatureHelp(
	_ context.Context,
	request *lsp.SignatureHelpRequest,
) (*protocol.SignatureHelp, error) {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.Root == nil ||
		request.LineIndex == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".twig" {
		return nil, nil
	}
	call := twigquery.FunctionCallAt(request.Node)
	if call == nil {
		return nil, nil
	}
	path, _ := uriutil.Path(request.TextDocument.URI)
	reference, found := macroCallReference(path, request.Root, call)
	if !found {
		return nil, nil
	}
	var macros []twig.Macro
	for _, template := range reference.Templates {
		current, err := p.index.FindMacro(template, reference.Name)
		if err != nil {
			return nil, err
		}
		macros = append(macros, current...)
	}
	currentTemplates := twig.TemplateNames(path)
	for _, template := range reference.Templates {
		if !containsSignatureTemplate(currentTemplates, template) {
			continue
		}
		filtered := macros[:0]
		for _, macro := range macros {
			if macro.FilePath != path {
				filtered = append(filtered, macro)
			}
		}
		macros = filtered
		for _, macro := range twig.MacrosInDocument(path, request.Root) {
			if strings.EqualFold(macro.Name, reference.Name) {
				macros = append(macros, macro)
			}
		}
		break
	}
	if len(macros) == 0 {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	active := macroActiveArgument(call, offset)
	result := &protocol.SignatureHelp{
		Signatures:      make([]protocol.SignatureInformation, 0, len(macros)),
		ActiveParameter: active,
	}
	seen := make(map[string]struct{})
	for _, macro := range macros {
		signature := macro.Signature()
		if _, duplicate := seen[signature]; duplicate {
			continue
		}
		seen[signature] = struct{}{}
		information := protocol.SignatureInformation{
			Label:           signature,
			ActiveParameter: active,
		}
		for _, parameter := range macro.Parameters {
			label := parameter.Name
			if parameter.Default != "" {
				label += " = " + parameter.Default
			}
			information.Parameters = append(
				information.Parameters,
				protocol.ParameterInformation{Label: label},
			)
		}
		result.Signatures = append(result.Signatures, information)
	}
	if len(result.Signatures) == 0 {
		return nil, nil
	}
	return result, nil
}

func macroCallReference(
	path string,
	root,
	call *cst.Node,
) (twig.MacroReference, bool) {
	for _, reference := range twig.MacroReferencesInDocument(path, root) {
		if reference.Role != twig.MacroUsageReference {
			continue
		}
		if reference.Range.Start >= call.Range().Start &&
			reference.Range.End <= call.Range().End {
			return reference, true
		}
	}
	return twig.MacroReference{}, false
}

func macroActiveArgument(call *cst.Node, offset uint32) int {
	var arguments *cst.Node
	for child := range call.ChildNodes() {
		if child.Kind() == twigsyntax.TwigArguments {
			arguments = child
			break
		}
	}
	if arguments == nil {
		return 0
	}
	active := 0
	for token := range arguments.ChildTokens() {
		if token.Range().Start >= offset {
			break
		}
		if token.Kind() == twigsyntax.TkComma {
			active++
		}
	}
	return active
}

func containsSignatureTemplate(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
