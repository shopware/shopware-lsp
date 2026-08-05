// Package inspections contains diagnostic inspections that bind their quick
// fixes while reporting byte-oriented problems.
package inspections

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

type bindProblems func(lsp.DiagnosticID, map[string]any) []lsp.BoundFix

// ProblemAnalyzer performs domain analysis and returns byte-oriented problems.
// It deliberately has no dependency on LSP diagnostics; protocol conversion is
// owned by the server's ProblemReporter.
type ProblemAnalyzer interface {
	Analyze(context.Context, *lsp.TextDocument) ([]lsp.Problem, error)
}

type analyzerInspection struct {
	definition lsp.InspectionDefinition
	analyzers  []ProblemAnalyzer
	fixes      []lsp.QuickFix
}

func NewAnalyzerInspection(
	id string,
	languages []language.ID,
	source string,
	codes []string,
	analyzers ...ProblemAnalyzer,
) lsp.Inspection {
	problems := make([]lsp.ProblemDefinition, 0, len(codes))
	for _, code := range codes {
		problems = append(problems, lsp.ProblemDefinition{
			ID:              lsp.DiagnosticID(code),
			Source:          source,
			DefaultSeverity: protocol.DiagnosticSeverityWarning,
		})
	}
	return &analyzerInspection{
		definition: lsp.InspectionDefinition{
			ID:        id,
			Languages: append([]language.ID(nil), languages...),
			Problems:  problems,
		},
		analyzers: append([]ProblemAnalyzer(nil), analyzers...),
		fixes:     []lsp.QuickFix{suggestionFix{}},
	}
}

func (i *analyzerInspection) Definition() lsp.InspectionDefinition {
	return i.definition
}

func (i *analyzerInspection) QuickFixes() []lsp.QuickFix {
	return append([]lsp.QuickFix(nil), i.fixes...)
}

func (i *analyzerInspection) Inspect(
	ctx context.Context,
	document *lsp.TextDocument,
	reporter lsp.ProblemReporter,
) error {
	declared := make(map[lsp.DiagnosticID]struct{}, len(i.definition.Problems))
	for _, definition := range i.definition.Problems {
		declared[definition.ID] = struct{}{}
	}
	for _, analyzer := range i.analyzers {
		if analyzer == nil {
			continue
		}
		problems, err := analyzer.Analyze(ctx, document)
		if err != nil {
			return err
		}
		for _, problem := range problems {
			if _, ok := declared[problem.ID]; !ok {
				return fmt.Errorf("inspection %q received undeclared problem %q", i.definition.ID, problem.ID)
			}
			payload := diagnosticPayload(problem.Payload)
			problem.Payload = payload
			problem.Fixes = append(problem.Fixes, suggestionBoundFixes(payload)...)
			if err := reporter.Report(problem); err != nil {
				return err
			}
		}
	}
	return nil
}

type boundInspection struct {
	definition lsp.InspectionDefinition
	analyzer   ProblemAnalyzer
	analyzers  []ProblemAnalyzer
	fixes      []lsp.QuickFix
	bind       bindProblems
}

func (i *boundInspection) Definition() lsp.InspectionDefinition {
	return i.definition
}

func (i *boundInspection) QuickFixes() []lsp.QuickFix {
	return append([]lsp.QuickFix(nil), i.fixes...)
}

func (i *boundInspection) Inspect(
	ctx context.Context,
	document *lsp.TextDocument,
	reporter lsp.ProblemReporter,
) error {
	if i.analyzer != nil {
		problems, err := i.analyzer.Analyze(ctx, document)
		if err != nil {
			return err
		}
		if err := i.reportProblems(ctx, document, reporter, problems); err != nil {
			return err
		}
	}
	for _, analyzer := range i.analyzers {
		if analyzer == nil {
			continue
		}
		problems, err := analyzer.Analyze(ctx, document)
		if err != nil {
			return err
		}
		if err := i.reportProblems(ctx, document, reporter, problems); err != nil {
			return err
		}
	}
	return nil
}

func (i *boundInspection) reportProblems(
	ctx context.Context,
	document *lsp.TextDocument,
	reporter lsp.ProblemReporter,
	problems []lsp.Problem,
) error {
	definitions := make(map[lsp.DiagnosticID]struct{}, len(i.definition.Problems))
	for _, definition := range i.definition.Problems {
		definitions[definition.ID] = struct{}{}
	}
	for _, problem := range problems {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, declared := definitions[problem.ID]; !declared {
			return fmt.Errorf("inspection %q received undeclared problem %q", i.definition.ID, problem.ID)
		}
		if problem.Element == nil && document.SyntaxTree != nil &&
			document.SyntaxTree.Root != nil {
			problem.Element = document.SyntaxTree.Root.DescendantForRange(problem.Range)
		}
		payload := diagnosticPayload(problem.Payload)
		problem.Payload = payload
		if i.bind != nil {
			problem.Fixes = append(problem.Fixes, i.bind(problem.ID, payload)...)
		}
		if err := reporter.Report(problem); err != nil {
			return err
		}
	}
	return nil
}

func diagnosticPayload(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	result := make(map[string]any)
	if err := json.Unmarshal(encoded, &result); err != nil {
		return map[string]any{"payload": value}
	}
	return result
}
