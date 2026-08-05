package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	missingLiveEventCode         lsp.DiagnosticID = "twig.component.live_event.missing"
	missingLiveEventArgumentCode lsp.DiagnosticID = "twig.component.live_event_argument.missing"
)

type LiveComponentEventAnalyzer struct {
	index *twigcomponent.Index
}

func NewLiveComponentEventAnalyzer(
	index *twigcomponent.Index,
) *LiveComponentEventAnalyzer {
	return &LiveComponentEventAnalyzer{index: index}
}

func (p *LiveComponentEventAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".php" && extension != ".twig" {
		return nil, nil
	}
	listeners, err := p.index.LiveListeners()
	if err != nil {
		return nil, err
	}
	if extension == ".php" {
		listeners = mergeLiveEventListeners(
			listeners,
			twigcomponent.LiveListenersInPHP(
				path,
				document.SyntaxTree.Root,
			),
		)
	}
	var references []twigcomponent.LiveEventReference
	var arguments []twigcomponent.LiveEventArgumentReference
	if extension == ".php" {
		references = twigcomponent.LiveEventReferencesInPHP(
			path,
			document.SyntaxTree.Root,
		)
		arguments = twigcomponent.LiveEventArgumentReferencesInPHP(
			path,
			document.SyntaxTree.Root,
		)
	} else {
		references = twigcomponent.LiveEventReferencesInTwig(
			path,
			document.SyntaxTree.Root,
		)
		arguments = twigcomponent.LiveEventArgumentReferencesInTwig(
			path,
			document.SyntaxTree.Root,
		)
	}
	eventNames := uniqueLiveEventListenerNames(listeners)
	var result []lsp.Problem
	for _, reference := range references {
		if ctx.Err() != nil {
			return nil, nil
		}
		if containsFold(eventNames, reference.Name) {
			continue
		}
		result = append(result, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Live event '%s' has no #[LiveListener]",
				reference.Name,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   strings.TrimPrefix(extension, "."),
			ID:       missingLiveEventCode,
			Payload: map[string]any{
				"suggestions": suggestion.Similar(
					reference.Name,
					eventNames,
				),
			},
		})
	}
	for _, reference := range arguments {
		var parameterNames []string
		for _, listener := range listeners {
			if !strings.EqualFold(listener.Name, reference.Event) {
				continue
			}
			for _, parameter := range listener.Parameters {
				if parameter.LiveArg {
					parameterNames = append(
						parameterNames,
						parameter.Name,
					)
				}
			}
		}
		parameterNames = uniqueLiveEventArgumentNames(parameterNames)
		if len(parameterNames) == 0 ||
			containsFold(parameterNames, reference.Name) {
			continue
		}
		suggestions := suggestion.Similar(
			reference.Name,
			parameterNames,
		)
		if extension == ".twig" {
			for index := range suggestions {
				suggestions[index] = liveArgumentAttributeSegment(
					suggestions[index],
				)
			}
		}
		result = append(result, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Live event '%s' has no argument named '%s'",
				reference.Event,
				reference.Name,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   strings.TrimPrefix(extension, "."),
			ID:       missingLiveEventArgumentCode,
			Payload: map[string]any{
				"suggestions": suggestions,
			},
		})
	}
	return result, nil
}

func mergeLiveEventListeners(
	indexed,
	current []twigcomponent.LiveListener,
) []twigcomponent.LiveListener {
	result := append([]twigcomponent.LiveListener(nil), indexed...)
	for _, listener := range current {
		replaced := false
		for index := range result {
			if result[index].File == listener.File &&
				result[index].Range == listener.Range {
				result[index] = listener
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, listener)
		}
	}
	return result
}

func uniqueLiveEventListenerNames(
	listeners []twigcomponent.LiveListener,
) []string {
	var result []string
	seen := make(map[string]struct{}, len(listeners))
	for _, listener := range listeners {
		key := strings.ToLower(listener.Name)
		if listener.Name == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, listener.Name)
	}
	return result
}

func uniqueLiveEventArgumentNames(values []string) []string {
	var result []string
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := strings.ToLower(value)
		if value == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
