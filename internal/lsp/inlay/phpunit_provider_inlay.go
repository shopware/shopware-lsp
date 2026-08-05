package inlay

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const phpUnitTestCaseClass = `PHPUnit\Framework\TestCase`

var phpUnitDataProviderPattern = regexp.MustCompile(
	`(?m)@dataProvider\s+([A-Za-z_][A-Za-z0-9_]*)`,
)

// PHPUnitProviderProvider labels values yielded by a data-provider method
// with the matching test method's parameter names.
type PHPUnitProviderProvider struct {
	phpIndex *php.PHPIndex
}

func NewPHPUnitProviderProvider(phpIndex *php.PHPIndex) *PHPUnitProviderProvider {
	return &PHPUnitProviderProvider{phpIndex: phpIndex}
}

func (p *PHPUnitProviderProvider) GetInlayHints(
	ctx context.Context,
	request *lsp.InlayHintRequest,
) ([]protocol.InlayHint, error) {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil || request == nil ||
		request.Document == nil || request.Document.SyntaxLanguage != language.PHP ||
		request.Document.SyntaxTree == nil || request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	path := request.Document.URI
	if resolved, err := uriutil.Path(path); err == nil {
		path = resolved
	}
	documentGraph := p.phpIndex.AnalyzeDocument(
		path,
		request.Document.Version,
		request.Document.SyntaxTree.Root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(documentGraph)
	rangeStart, rangeEnd := inlayHintByteRange(request)
	var result []protocol.InlayHint
	for _, class := range phpquery.Classes(request.Document.SyntaxTree.Root) {
		className := phpUnitClassName(request.Document.SyntaxTree.Root, class)
		if className == "" || !snapshot.IsSubtypeOf(className, phpUnitTestCaseClass) {
			continue
		}
		providers := phpUnitProviderContracts(class)
		if len(providers) == 0 {
			continue
		}
		for _, method := range phpquery.Methods(class) {
			contract, found := providers[strings.ToLower(phpquery.MethodName(method))]
			if !found || len(contract.parameters) == 0 {
				continue
			}
			for _, yield := range phpquery.Nodes(method, phpsyntax.PhpYieldExpression) {
				if ctx.Err() != nil {
					return result, nil
				}
				if phpquery.FunctionLikeAt(yield) != method {
					continue
				}
				arrays := phpquery.Nodes(yield, phpsyntax.PhpArray)
				if len(arrays) == 0 || phpquery.FunctionLikeAt(arrays[0]) != method {
					continue
				}
				for index, item := range phpquery.ArrayItems(arrays[0]) {
					if index >= len(contract.parameters) {
						break
					}
					value := phpquery.ArrayItemValue(item)
					if value == nil {
						value = item
					}
					position := value.RangeTrimmedTrivia().Start
					if position < rangeStart || position > rangeEnd {
						continue
					}
					line, character := request.Document.LineIndex.PositionUTF16(position)
					testLine, _ := request.Document.LineIndex.PositionUTF16(contract.position)
					parameter := contract.parameters[index]
					part := protocol.InlayHintLabelPart{
						Value:   parameter + ":",
						Tooltip: fmt.Sprintf("Parameter of %s()", contract.testMethod),
						Location: &protocol.Location{
							URI: request.Document.URI,
							Range: protocol.Range{
								Start: protocol.Position{Line: int(testLine)},
								End:   protocol.Position{Line: int(testLine)},
							},
						},
					}
					result = append(result, protocol.InlayHint{
						Position:     protocol.Position{Line: int(line), Character: int(character)},
						Label:        []protocol.InlayHintLabelPart{part},
						Kind:         protocol.InlayHintKindParameter,
						Tooltip:      fmt.Sprintf("PHPUnit data provider for %s()", contract.testMethod),
						PaddingRight: true,
					})
				}
			}
		}
	}
	return result, nil
}

type phpUnitProviderContract struct {
	testMethod string
	parameters []string
	position   uint32
}

func phpUnitProviderContracts(class *phpsyntax.Node) map[string]phpUnitProviderContract {
	result := make(map[string]phpUnitProviderContract)
	conflicts := make(map[string]struct{})
	for _, method := range phpquery.Methods(class) {
		providerNames := phpUnitMethodProviders(method)
		if len(providerNames) == 0 {
			continue
		}
		parameters := phpquery.Parameters(method)
		names := make([]string, 0, len(parameters))
		for _, parameter := range parameters {
			if name := phpquery.ParameterName(parameter); name != "" {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			continue
		}
		contract := phpUnitProviderContract{
			testMethod: phpquery.MethodName(method),
			parameters: names,
			position:   method.RangeTrimmedTrivia().Start,
		}
		for _, providerName := range providerNames {
			key := strings.ToLower(providerName)
			if existing, found := result[key]; found &&
				!equalParameterNames(existing.parameters, names) {
				delete(result, key)
				conflicts[key] = struct{}{}
				continue
			}
			if _, conflict := conflicts[key]; !conflict {
				result[key] = contract
			}
		}
	}
	return result
}

func phpUnitMethodProviders(method *phpsyntax.Node) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, match := range phpUnitDataProviderPattern.FindAllStringSubmatch(
		leadingPHPUnitDocComment(method),
		-1,
	) {
		if len(match) > 1 {
			seen[strings.ToLower(match[1])] = struct{}{}
			result = append(result, match[1])
		}
	}
	for _, attribute := range phpquery.Attributes(method) {
		name := strings.TrimPrefix(phpquery.AttributeName(attribute), `\`)
		if !strings.EqualFold(name, "DataProvider") &&
			!strings.HasSuffix(strings.ToLower(name), `\dataprovider`) {
			continue
		}
		providerName := phpquery.StringValue(phpquery.ArgumentExpression(attribute, 0))
		key := strings.ToLower(providerName)
		if providerName == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, providerName)
	}
	return result
}

func leadingPHPUnitDocComment(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	rng := node.Range()
	trimmed := node.RangeTrimmedTrivia()
	if trimmed.Start <= rng.Start {
		return ""
	}
	prefixLength := int(trimmed.Start - rng.Start)
	text := node.Text()
	if prefixLength > len(text) {
		prefixLength = len(text)
	}
	prefix := text[:prefixLength]
	start := strings.LastIndex(prefix, "/**")
	if start < 0 {
		return ""
	}
	end := strings.Index(prefix[start:], "*/")
	if end < 0 {
		return ""
	}
	end += start + 2
	if strings.TrimSpace(prefix[end:]) != "" {
		return ""
	}
	return prefix[start:end]
}

func phpUnitClassName(root, class *phpsyntax.Node) string {
	name := phpquery.ClassName(class)
	if namespace := strings.Trim(phpquery.Namespace(root), `\`); namespace != "" {
		return namespace + `\` + name
	}
	return name
}

func equalParameterNames(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

var _ lsp.InlayHintProvider = (*PHPUnitProviderProvider)(nil)
