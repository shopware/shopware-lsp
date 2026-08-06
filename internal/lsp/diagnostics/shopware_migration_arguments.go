package diagnostics

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

const (
	removeArgumentMigrationCode lsp.DiagnosticID = "shopware.migration.arguments.remove"
	addDefaultArgumentCode      lsp.DiagnosticID = "shopware.migration.arguments.add_default"
	thumbnailGenerateCode       lsp.DiagnosticID = "shopware.migration.thumbnail.generate"

	calculatedTaxCollectionClass = "Shopware\\Core\\Checkout\\Cart\\Tax\\Struct\\CalculatedTaxCollection"
	thumbnailServiceClass        = "Shopware\\Core\\Content\\Media\\Thumbnail\\ThumbnailService"
	mediaCollectionClass         = "Shopware\\Core\\Content\\Media\\MediaCollection"
)

var removedConstructorArgumentClasses = []string{
	"Shopware\\Core\\Checkout\\Customer\\Exception\\DuplicateWishlistProductException",
	"Shopware\\Core\\Content\\Newsletter\\Exception\\LanguageOfNewsletterDeleteException",
}

func (p *ShopwareMigrationAnalyzer) argumentMigrationProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	source string,
) []lsp.Problem {
	var result []lsp.Problem
	result = append(result, p.removedCallArgumentProblems(ctx, root, document, snapshot)...)
	result = append(result, p.removedConstructorArgumentProblems(ctx, root, snapshot)...)
	result = append(result, p.thumbnailArgumentProblems(ctx, root, document, snapshot, source)...)
	return result
}

func (p *ShopwareMigrationAnalyzer) removedCallArgumentProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	document *semantic.Document,
	snapshot *semantic.Snapshot,
) []lsp.Problem {
	var result []lsp.Problem
	for _, call := range phpquery.Nodes(root, phpsyntax.PhpMemberCall) {
		if ctx.Err() != nil {
			return result
		}
		if !strings.EqualFold(phpquery.CallMethodName(call), "merge") {
			continue
		}
		receiver := phpquery.CallReceiver(call)
		arguments := phpquery.Arguments(call)
		if receiver == nil || len(arguments) <= 1 ||
			!phpTypeIsSubtype(document.TypeOf(receiver).Type, snapshot, calculatedTaxCollectionClass) {
			continue
		}
		result = append(result, argumentMigrationProblem(
			removeArgumentMigrationCode,
			call,
			"Shopware 6.5: remove the obsolete second CalculatedTaxCollection::merge() argument",
			"call-argument",
			1,
			"",
			true,
		))
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) removedConstructorArgumentProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
) []lsp.Problem {
	resolver := php.NewNameResolver(root)
	var result []lsp.Problem
	for _, creation := range phpquery.ObjectCreations(root) {
		if ctx.Err() != nil {
			return result
		}
		class := strings.Trim(resolver.Resolve(phpquery.ObjectClassName(creation)), "\\")
		if len(phpquery.Arguments(creation)) == 0 ||
			!phpClassMatchesAny(class, snapshot, removedConstructorArgumentClasses) {
			continue
		}
		result = append(result, argumentMigrationProblem(
			removeArgumentMigrationCode,
			creation,
			"Shopware 6.5: remove the obsolete exception constructor argument",
			"call-argument",
			0,
			"",
			true,
		))
	}
	for _, class := range phpquery.Classes(root) {
		if ctx.Err() != nil {
			return result
		}
		matches := false
		for _, target := range removedConstructorArgumentClasses {
			if phpClassIsSubtype(class, root, snapshot, target) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		constructor := phpOwnMethodForMigration(class, "__construct")
		parameters := phpquery.IterateParameters(constructor)
		if constructor == nil || !parameters.Next() {
			continue
		}
		result = append(result, argumentMigrationProblem(
			removeArgumentMigrationCode,
			constructor,
			"Shopware 6.5: remove the obsolete first exception constructor parameter",
			"constructor-parameter",
			0,
			"",
			true,
		))
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) thumbnailArgumentProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	source string,
) []lsp.Problem {
	var result []lsp.Problem
	for _, call := range phpquery.Nodes(root, phpsyntax.PhpMemberCall) {
		if ctx.Err() != nil {
			return result
		}
		receiver := phpquery.CallReceiver(call)
		if receiver == nil || !phpTypeIsSubtype(
			document.TypeOf(receiver).Type,
			snapshot,
			thumbnailServiceClass,
		) {
			continue
		}
		method := phpquery.CallMethodName(call)
		switch {
		case strings.EqualFold(method, "updateThumbnails"):
			arguments := phpquery.Arguments(call)
			if len(arguments) != 2 {
				continue
			}
			result = append(result, argumentMigrationProblem(
				addDefaultArgumentCode,
				call,
				"Shopware 6.5: pass the new strict flag to ThumbnailService::updateThumbnails()",
				"call-argument",
				2,
				"false",
				phpArgumentsArePositional(arguments),
			))
		case strings.EqualFold(method, "generateThumbnails"):
			arguments := phpquery.Arguments(call)
			first := phpArgumentExpression(phpquery.Argument(call, 0))
			second := phpArgumentExpression(phpquery.Argument(call, 1))
			rng := call.RangeTrimmedTrivia()
			safe := len(arguments) == 2 && first != nil && second != nil &&
				validSourceRange(rng.Start, rng.End, source) &&
				phpArgumentsArePositional(arguments)
			replacement := ""
			original := ""
			if safe {
				replacement = strings.TrimSpace(receiver.Text()) + "->generate(new \\" +
					mediaCollectionClass + "([" + strings.TrimSpace(first.Text()) + "]), " +
					strings.TrimSpace(second.Text()) + ")"
				original = source[rng.Start:rng.End]
			}
			name := callTargetName(call)
			problemRange := rng
			if name != nil {
				problemRange = name.RangeTrimmedTrivia()
			}
			result = append(result, lsp.Problem{
				ID:       thumbnailGenerateCode,
				Range:    problemRange,
				Element:  call,
				Message:  "Shopware 6.5: migrate single thumbnail generation to MediaCollection batching",
				Severity: protocol.DiagnosticSeverityWarning,
				Source:   "shopware-rector",
				Payload: ShopwareMigrationPayload{
					Rule:        "thumbnail-generate",
					Kind:        "call",
					Safe:        safe,
					Original:    original,
					Replacement: replacement,
					Start:       rng.Start,
					End:         rng.End,
				},
			})
		}
	}
	return result
}

func argumentMigrationProblem(
	code lsp.DiagnosticID,
	element *phpsyntax.Node,
	message string,
	kind string,
	index int,
	value string,
	safe bool,
) lsp.Problem {
	rng := element.RangeTrimmedTrivia()
	if name := callTargetName(element); name != nil {
		rng = name.RangeTrimmedTrivia()
	}
	return lsp.Problem{
		ID:       code,
		Range:    rng,
		Element:  element,
		Message:  message,
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "shopware-rector",
		Payload: ShopwareMigrationPayload{
			Rule:          "argument-migration",
			Kind:          kind,
			Safe:          safe,
			ArgumentIndex: index,
			Value:         value,
		},
	}
}

func phpClassMatchesAny(
	class string,
	snapshot *semantic.Snapshot,
	targets []string,
) bool {
	for _, target := range targets {
		if snapshot != nil && snapshot.IsSubtypeOf(class, target) {
			return true
		}
	}
	return false
}

func phpArgumentsArePositional(arguments []*phpsyntax.Node) bool {
	for _, argument := range arguments {
		if argument == nil || argument.Kind() == phpsyntax.PhpNamedArgument {
			return false
		}
	}
	return true
}
