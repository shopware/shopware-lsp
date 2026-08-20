package phpsemantic

import (
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/languagelevel"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/suppression"
)

type phpDiagnosticRun struct {
	provider     *Provider
	document     *lsp.TextDocument
	semantic     *semantic.Document
	snapshot     *semantic.Snapshot
	suppressions suppression.Set
	diagnostics  []lsp.Problem
}

func newPHPDiagnosticRun(
	provider *Provider,
	document *lsp.TextDocument,
	semanticDocument *semantic.Document,
	snapshot *semantic.Snapshot,
) *phpDiagnosticRun {
	if snapshot == nil {
		snapshot = provider.index.SemanticSnapshot().WithDocument(semanticDocument)
	}
	return &phpDiagnosticRun{
		provider:     provider,
		document:     document,
		semantic:     semanticDocument,
		snapshot:     snapshot,
		suppressions: suppression.Parse(document.Source),
	}
}

func (r *phpDiagnosticRun) analyze() []lsp.Problem {
	r.addParseErrors()
	r.addLanguageLevelProblems()
	r.addReferenceProblems()
	r.addSemanticIssues()
	r.addDuplicateDeclarations()
	return r.diagnostics
}

func (r *phpDiagnosticRun) addParseErrors() {
	for errorIndex := range r.document.ParseErrors {
		parseError := &r.document.ParseErrors[errorIndex]
		if r.suppressions.Suppresses(parseError.Range.Start, "php.parse") {
			continue
		}
		r.diagnostics = append(r.diagnostics, lsp.Problem{
			Range:    parseError.Range,
			Severity: protocol.DiagnosticSeverityError,
			ID:       "php.parse",
			Source:   "shopware-php",
			Message:  parseError.Message(),
		})
	}
}

func (r *phpDiagnosticRun) addLanguageLevelProblems() {
	model := r.provider.index.Project()
	if model == nil {
		return
	}
	for _, occurrence := range languagelevel.Detect(r.document.SyntaxTree.Root) {
		definition, found := languagelevel.Lookup(occurrence.Feature)
		if !found || languagelevel.Supports(model.PHPVersion, occurrence.Feature) ||
			r.suppressions.Suppresses(occurrence.Range.Start, "php.version") {
			continue
		}
		r.diagnostics = append(r.diagnostics, lsp.Problem{
			Range:    occurrence.Range,
			Severity: protocol.DiagnosticSeverityError,
			ID:       "php.version",
			Source:   "shopware-php",
			Message:  languagelevel.UnsupportedMessage(definition, model.PHPVersion),
			Payload: map[string]any{
				"feature":       occurrence.Feature,
				"minimumPHP":    fmt.Sprintf("%d.%d", definition.Major, definition.Minor),
				"configuredPHP": fmt.Sprintf("%d.%d", model.PHPVersion.Major, model.PHPVersion.Minor),
			},
		})
	}
}

func (r *phpDiagnosticRun) addReferenceProblems() {
	for _, reference := range r.semantic.References {
		candidates := referenceCandidates(r.snapshot, reference)
		if len(candidates) != 0 {
			r.addResolvedReferenceProblems(reference, candidates)
			continue
		}
		r.addUndefinedReferenceProblem(reference)
	}
}

func (r *phpDiagnosticRun) addResolvedReferenceProblems(
	reference semantic.Reference,
	candidates []semantic.Symbol,
) {
	r.addDeprecationProblem(reference, candidates)
	r.addSoftFinalExtensionProblem(reference, candidates)
	if reference.Kind != semantic.MemberName || anyMemberAccessible(
		r.semantic,
		r.snapshot,
		r.document.SyntaxTree.Root,
		reference,
		candidates,
	) || r.suppressions.Suppresses(reference.Range.Start, "php.visibility") {
		return
	}
	r.diagnostics = append(r.diagnostics, lsp.Problem{
		Range:    reference.Range,
		Severity: protocol.DiagnosticSeverityError,
		ID:       "php.visibility",
		Source:   "shopware-php",
		Message:  inaccessibleMemberMessage(reference, candidates[0]),
	})
}

func (r *phpDiagnosticRun) addSoftFinalExtensionProblem(
	reference semantic.Reference,
	candidates []semantic.Symbol,
) {
	if reference.Kind != semantic.ClassName ||
		r.suppressions.Suppresses(reference.Range.Start, "php.inheritance") {
		return
	}
	node := r.document.SyntaxTree.Root.NodeAtOffset(reference.Range.Start)
	class := phpquery.ClassAt(node)
	if class == nil || class.Kind() != phpsyntax.PhpClassDeclaration {
		return
	}
	extends := phpquery.DirectChild(class, phpsyntax.PhpExtendsClause)
	if extends == nil || !extends.Range().Contains(reference.Range.Start) {
		return
	}
	for _, symbol := range candidates {
		if symbol.Kind != semantic.ClassSymbol ||
			!symbol.Flags.Has(semantic.SoftFinalFlag) ||
			symbol.Flags.Has(semantic.FinalFlag) {
			continue
		}
		r.diagnostics = append(r.diagnostics, lsp.Problem{
			Range:    reference.Range,
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "php.inheritance",
			Source:   "shopware-php",
			Message: "Class " + symbol.FullyQualified +
				" is marked @final and should not be extended",
			Payload: map[string]any{
				"class": symbol.FullyQualified,
			},
		})
		return
	}
}

func (r *phpDiagnosticRun) addDeprecationProblem(
	reference semantic.Reference,
	candidates []semantic.Symbol,
) {
	if r.suppressions.Suppresses(reference.Range.Start, "php.deprecated") {
		return
	}
	for _, symbol := range candidates {
		if !symbol.Flags.Has(semantic.DeprecatedFlag) ||
			isDeprecationSuppressedAtReference(
				r.semantic,
				r.snapshot,
				reference,
				symbol,
			) {
			continue
		}
		r.diagnostics = append(r.diagnostics, lsp.Problem{
			Range:    reference.Range,
			Severity: protocol.DiagnosticSeverityHint,
			ID:       "php.deprecated",
			Source:   "shopware-php",
			Message:  formatDeprecationDiagnostic(symbol),
			Tags:     []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
		})
		return
	}
}

func (r *phpDiagnosticRun) addUndefinedReferenceProblem(reference semantic.Reference) {
	message, report := r.undefinedReferenceMessage(reference)
	if !report || r.suppressions.Suppresses(reference.Range.Start, "php.undefined") {
		return
	}
	r.diagnostics = append(r.diagnostics, lsp.Problem{
		Range:    reference.Range,
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "php.undefined",
		Source:   "shopware-php",
		Message:  message,
	})
}

func (r *phpDiagnosticRun) undefinedReferenceMessage(
	reference semantic.Reference,
) (string, bool) {
	switch reference.Kind {
	case semantic.ClassName:
		if classExistenceGuarded(r.document.SyntaxTree.Root, r.semantic, reference) ||
			r.addExtensionProblem(reference) {
			return "", false
		}
		return "Undefined class " + reference.Name, true
	case semantic.FunctionName, semantic.ConstantName:
		r.addExtensionProblem(reference)
		return "", false
	case semantic.MemberName:
		if !r.diagnosableUndefinedMember(reference) {
			return "", false
		}
		return undefinedMemberMessage(reference), true
	case semantic.VariableName:
		if isImplicitVariable(reference.Name) {
			return "", false
		}
		return "Undefined variable " + reference.Name, true
	default:
		return "", false
	}
}

func (r *phpDiagnosticRun) addExtensionProblem(reference semantic.Reference) bool {
	diagnostic, handled := r.provider.unavailableExtensionDiagnostic(reference)
	if handled && diagnostic != nil &&
		!r.suppressions.Suppresses(reference.Range.Start, "php.extension") {
		r.diagnostics = append(r.diagnostics, *diagnostic)
	}
	return handled
}

func (r *phpDiagnosticRun) diagnosableUndefinedMember(
	reference semantic.Reference,
) bool {
	return diagnosableMemberReference(r.snapshot, reference) &&
		!lateStaticMemberMayBeDeclaredBySubclass(
			r.document.SyntaxTree.Root,
			r.snapshot,
			reference,
		) &&
		(reference.TargetKind != semantic.MethodSymbol ||
			!methodExistsGuarded(r.document.SyntaxTree.Root, reference)) &&
		!isImplicitTraitRequirement(r.semantic, r.snapshot, reference)
}

func (r *phpDiagnosticRun) addSemanticIssues() {
	for _, issue := range r.semantic.Issues {
		r.diagnostics = append(r.diagnostics, lsp.Problem{
			Range:    issue.Range,
			Severity: protocol.DiagnosticSeverityError,
			ID:       lsp.DiagnosticID(issue.Code),
			Source:   "shopware-php",
			Message:  issue.Message,
		})
	}
}

func (r *phpDiagnosticRun) addDuplicateDeclarations() {
	for _, symbol := range r.semantic.Symbols {
		if !symbol.IsClassLike() ||
			len(r.snapshot.Classes(symbol.FullyQualified)) < 2 {
			continue
		}
		r.diagnostics = append(r.diagnostics, lsp.Problem{
			Range:    symbol.SelectionRange,
			Severity: protocol.DiagnosticSeverityError,
			ID:       "php.duplicate",
			Source:   "shopware-php",
			Message:  "Duplicate declaration of " + symbol.FullyQualified,
		})
	}
}
