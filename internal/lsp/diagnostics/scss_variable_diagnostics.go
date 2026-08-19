package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/style"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const SCSSVariableUnknownCode lsp.DiagnosticID = "scss.variable.unknown"

type SCSSVariableDeclarationProvider interface {
	HasVariableDeclaration(name, excludedPath string) (bool, error)
}

type SCSSVariableIndexReadiness interface {
	Ready(context.Context) (bool, error)
}

type SCSSVariableAnalyzer struct {
	variables SCSSVariableDeclarationProvider
	readiness SCSSVariableIndexReadiness
}

func NewSCSSVariableAnalyzer(
	variables SCSSVariableDeclarationProvider,
	readiness ...SCSSVariableIndexReadiness,
) *SCSSVariableAnalyzer {
	analyzer := &SCSSVariableAnalyzer{variables: variables}
	if len(readiness) > 0 {
		analyzer.readiness = readiness[0]
	}
	return analyzer
}

func (a *SCSSVariableAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if a == nil || a.variables == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		!strings.EqualFold(filepath.Ext(document.URI), ".scss") {
		return nil, nil
	}
	if a.readiness != nil {
		ready, err := a.readiness.Ready(ctx)
		if err != nil {
			return nil, fmt.Errorf("query SCSS index readiness: %w", err)
		}
		if !ready {
			return nil, nil
		}
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, fmt.Errorf("resolve SCSS document path: %w", err)
	}
	analysis := style.AnalyzeVariables(path, document.SyntaxTree.Root)
	known := make(map[string]struct{}, len(analysis.Bindings))
	for _, binding := range analysis.Bindings {
		known[style.NormalizeVariableName(binding.Name)] = struct{}{}
	}

	indexed := make(map[string]bool)
	var problems []lsp.Problem
	for _, reference := range analysis.References {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := style.NormalizeVariableName(reference.Name)
		if _, found := known[name]; found {
			continue
		}
		exists, checked := indexed[name]
		if !checked {
			exists, err = a.variables.HasVariableDeclaration(name, path)
			if err != nil {
				return nil, fmt.Errorf(
					"resolve SCSS variable $%s: %w", reference.Name, err,
				)
			}
			indexed[name] = exists
		}
		if exists {
			continue
		}
		problems = append(problems, lsp.Problem{
			ID:    SCSSVariableUnknownCode,
			Range: reference.Range,
			Message: fmt.Sprintf(
				"SCSS variable '$%s' is not defined", reference.Name,
			),
			Payload: map[string]string{"variable": reference.Name},
		})
	}
	return problems, nil
}
