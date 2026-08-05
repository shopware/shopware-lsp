package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	missingConsoleCommandCode  lsp.DiagnosticID = "symfony.console.command.missing"
	missingConsoleArgumentCode lsp.DiagnosticID = "symfony.console.argument.missing"
	missingConsoleOptionCode   lsp.DiagnosticID = "symfony.console.option.missing"
)

type ConsoleAnalyzer struct {
	index    *console.Index
	phpIndex *php.PHPIndex
}

func NewConsoleAnalyzer(
	index *console.Index,
	phpIndex *php.PHPIndex,
) *ConsoleAnalyzer {
	return &ConsoleAnalyzer{index: index, phpIndex: phpIndex}
}

func (p *ConsoleAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil ||
		strings.ToLower(filepath.Ext(document.URI)) != ".php" {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	validationContext := p.phpIndex.AddDocumentContext(
		ctx,
		path,
		document.Version,
		document.SyntaxTree.Root,
		document.SyntaxTree.Root,
	)
	var result []lsp.Problem
	for _, literal := range phpquery.Nodes(
		document.SyntaxTree.Root,
		phpsyntax.PhpString,
	) {
		reference, ok := console.ReferenceAt(literal)
		if !ok || reference.Name == "" ||
			!console.ValidateReference(validationContext, reference) {
			continue
		}
		switch reference.Role {
		case console.ReferenceCommand:
			commands, err := p.index.GetCommand(reference.Name)
			if err != nil {
				return nil, err
			}
			if len(commands) != 0 {
				continue
			}
			all, err := p.index.GetCommands()
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(all))
			for _, command := range all {
				names = append(names, command.Name)
			}
			result = append(result, consoleDiagnostic(
				document,
				reference,
				missingConsoleCommandCode,
				fmt.Sprintf(
					"Console command '%s' not found",
					reference.Name,
				),
				suggestion.Similar(reference.Name, names),
			))
		case console.ReferenceArgument, console.ReferenceOption:
			inputs, err := console.InputsForReference(
				validationContext,
				p.index,
				reference,
			)
			if err != nil {
				return nil, err
			}
			found := false
			names := make([]string, 0, len(inputs))
			for _, input := range inputs {
				names = append(names, input.Name)
				if input.Name == reference.Name ||
					input.Kind == console.Option &&
						input.Shortcut == reference.Name {
					found = true
				}
			}
			if found || len(inputs) == 0 {
				continue
			}
			kind := "argument"
			code := missingConsoleArgumentCode
			if reference.Role == console.ReferenceOption {
				kind = "option"
				code = missingConsoleOptionCode
			}
			result = append(result, consoleDiagnostic(
				document,
				reference,
				code,
				fmt.Sprintf(
					"Console %s '%s' is not defined for this command",
					kind,
					reference.Name,
				),
				suggestion.Similar(reference.Name, names),
			))
		}
	}
	return result, nil
}

func consoleDiagnostic(
	_ *lsp.TextDocument,
	reference console.Reference,
	code lsp.DiagnosticID,
	message string,
	suggestions []string,
) lsp.Problem {
	return lsp.Problem{
		Range:    valueNodeTextRange(reference.Node, reference.Name),
		Message:  message,
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "symfony",
		ID:       code,
		Payload: map[string]any{
			"suggestions": suggestions,
		},
	}
}
