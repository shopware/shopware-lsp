package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const missingTemplateCode lsp.DiagnosticID = "twig.template.missing"

type templateDiagnosticCandidate struct {
	name   string
	range_ cst.TextRange
}

type TemplateAnalyzer struct {
	twigIndex *twig.TwigIndexer
	phpIndex  *php.PHPIndex
}

func NewTemplateAnalyzer(
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
) *TemplateAnalyzer {
	return &TemplateAnalyzer{
		twigIndex: twigIndex,
		phpIndex:  phpIndex,
	}
}

func (p *TemplateAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.twigIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}

	var candidates []templateDiagnosticCandidate
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".twig":
		for _, literal := range twig.TwigTemplateStrings(
			document.SyntaxTree.Root,
		) {
			name := strings.TrimSpace(twigquery.StringValue(literal))
			if name == "" {
				continue
			}
			candidates = append(candidates, templateDiagnosticCandidate{
				name:   name,
				range_: valueNodeTextRange(literal, name),
			})
		}
	case ".php":
		var err error
		candidates, err = p.phpCandidates(ctx, document)
		if err != nil {
			return nil, err
		}
	default:
		return nil, nil
	}

	var result []lsp.Problem
	seen := make(map[string]struct{})
	var candidateNames []string
	candidatesLoaded := false
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil, nil
		}
		name := candidate.name
		files, err := p.twigIndex.GetTwigFilesByRelPath(name)
		if err != nil {
			return nil, fmt.Errorf("query Twig template %q: %w", name, err)
		}
		if len(files) != 0 {
			continue
		}
		if !candidatesLoaded {
			candidateNames, err = p.twigIndex.GetAllTemplateFiles()
			if err != nil {
				return nil, fmt.Errorf("query Twig templates: %w", err)
			}
			candidatesLoaded = true
		}
		key := fmt.Sprintf(
			"%d:%d:%s",
			candidate.range_.Start,
			candidate.range_.End,
			name,
		)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, lsp.Problem{
			Range:    candidate.range_,
			Message:  fmt.Sprintf("Template '%s' not found", name),
			Source:   "twig",
			Severity: protocol.DiagnosticSeverityError,
			ID:       missingTemplateCode,
			Payload: map[string]any{
				"templateName": name,
				"suggestions": suggestion.SimilarTemplates(
					name,
					candidateNames,
				),
			},
		})
	}
	return result, nil
}

func (p *TemplateAnalyzer) phpCandidates(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]templateDiagnosticCandidate, error) {
	path, _ := uriutil.Path(document.URI)
	references := twig.PHPTemplateReferences(
		path,
		document.SyntaxTree.Root,
	)
	var phpDocument *semantic.Document
	var phpSnapshot *semantic.Snapshot
	if len(references) != 0 && p.phpIndex != nil {
		phpDocument = p.phpIndex.AnalyzeDocument(
			path,
			document.Version,
			document.SyntaxTree.Root,
		)
		phpSnapshot = p.phpIndex.SemanticSnapshot().
			WithDocument(phpDocument)
	}
	result := make([]templateDiagnosticCandidate, 0, len(references))
	for _, reference := range references {
		node := document.SyntaxTree.Root.NodeAtOffset(reference.Range.Start)
		if reference.Kind == twig.TemplateRenderReference &&
			!p.isSupportedPHPCall(node, phpDocument, phpSnapshot) {
			continue
		}
		result = append(result, templateDiagnosticCandidate{
			name:   reference.Template,
			range_: reference.Range,
		})
	}
	if p.phpIndex != nil {
		validationContext := p.phpIndex.AddDocumentContext(
			ctx,
			path,
			document.Version,
			document.SyntaxTree.Root,
			document.SyntaxTree.Root,
		)
		for _, literal := range phpquery.Nodes(
			document.SyntaxTree.Root,
			phpsyntax.PhpString,
		) {
			if _, tags := php.AssistantArgumentTags(
				validationContext,
				literal,
				"Template",
			); len(tags) == 0 {
				continue
			}
			name := phpquery.StringValue(literal)
			if name == "" {
				continue
			}
			result = append(result, templateDiagnosticCandidate{
				name:   name,
				range_: phpquery.StringContentRange(literal),
			})
		}
	}
	result = append(
		result,
		implicitPHPTemplateCandidates(document)...,
	)
	return result, nil
}

func implicitPHPTemplateCandidates(
	document *lsp.TextDocument,
) []templateDiagnosticCandidate {
	root := document.SyntaxTree.Root
	var result []templateDiagnosticCandidate
	for _, class := range phpquery.Classes(root) {
		for _, method := range phpquery.Methods(class) {
			name := php.GuessedControllerTemplate(root, method)
			if name == "" {
				continue
			}
			for _, attribute := range phpquery.Attributes(method) {
				if !isTemplateAttribute(attribute) ||
					len(phpquery.Arguments(attribute)) != 0 {
					continue
				}
				nameNode := phpquery.DirectChild(attribute, phpsyntax.PhpName)
				if nameNode == nil {
					continue
				}
				result = append(result, templateDiagnosticCandidate{
					name:   name,
					range_: nameNode.RangeTrimmedTrivia(),
				})
			}
			for _, rng := range emptyTemplateAnnotationRanges(method) {
				result = append(result, templateDiagnosticCandidate{
					name:   name,
					range_: rng,
				})
			}
		}
	}
	return result
}

func isTemplateAttribute(attribute *phpsyntax.Node) bool {
	name := strings.TrimPrefix(phpquery.AttributeName(attribute), "\\")
	if index := strings.LastIndex(name, "\\"); index >= 0 {
		name = name[index+1:]
	}
	return strings.EqualFold(name, "Template")
}

func emptyTemplateAnnotationRanges(
	method *phpsyntax.Node,
) []cst.TextRange {
	if method == nil {
		return nil
	}
	text := method.Text()
	if body := phpquery.DirectChild(
		method,
		phpsyntax.PhpBlock,
	); body != nil && body.Range().Start >= method.Range().Start {
		prefixLength := int(body.Range().Start - method.Range().Start)
		if prefixLength >= 0 && prefixLength <= len(text) {
			text = text[:prefixLength]
		}
	}
	lower := strings.ToLower(text)
	const annotation = "@template"
	var result []cst.TextRange
	for search := 0; search < len(lower); {
		relative := strings.Index(lower[search:], annotation)
		if relative < 0 {
			break
		}
		start := search + relative
		afterName := start + len(annotation)
		search = afterName
		if afterName < len(lower) &&
			isTemplateAnnotationIdentifierByte(lower[afterName]) {
			continue
		}
		cursor := afterName
		for cursor < len(text) &&
			(text[cursor] == ' ' || text[cursor] == '\t') {
			cursor++
		}
		if cursor < len(text) && text[cursor] == '(' {
			closeOffset := strings.IndexByte(text[cursor+1:], ')')
			if closeOffset < 0 ||
				strings.TrimSpace(
					text[cursor+1:cursor+1+closeOffset],
				) != "" {
				continue
			}
		}
		result = append(result, cst.TextRange{
			Start: method.Range().Start + uint32(start+1),
			End:   method.Range().Start + uint32(afterName),
		})
	}
	return result
}

func isTemplateAnnotationIdentifierByte(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= '0' && value <= '9'
}

func (p *TemplateAnalyzer) isSupportedPHPCall(
	node *cst.Node,
	document *semantic.Document,
	snapshot *semantic.Snapshot,
) bool {
	if phpquery.AttributeAt(node) != nil {
		return true
	}
	call := phpquery.CallAt(node)
	name := phpquery.CallMethodName(call)
	if name == "renderStorefront" {
		return true
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil || document == nil || snapshot == nil {
		return false
	}
	receiverType := document.TypeOf(receiver).Type
	if receiverType.IsUnknown() {
		return false
	}
	return phpTemplateReceiverSupported(snapshot, receiverType, name)
}

func phpTemplateReceiverSupported(
	snapshot *semantic.Snapshot,
	receiverType types.Type,
	method string,
) bool {
	targets := []string{
		"Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController",
	}
	if method == "render" {
		targets = append(targets, "Twig\\Environment")
	}
	for _, target := range targets {
		if snapshot.Relations().IsSubtype(receiverType, types.Named(target)) {
			return true
		}
	}
	return false
}
