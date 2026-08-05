package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	missingEventCode          lsp.DiagnosticID = "symfony.event.missing"
	missingListenerMethodCode lsp.DiagnosticID = "symfony.event.listener_method.missing"
)

type EventAnalyzer struct {
	index    *event.Index
	phpIndex *php.PHPIndex
	services *symfony.ServiceIndex
}

func NewEventAnalyzer(
	index *event.Index,
	phpIndex *php.PHPIndex,
	services *symfony.ServiceIndex,
) *EventAnalyzer {
	return &EventAnalyzer{
		index:    index,
		phpIndex: phpIndex,
		services: services,
	}
}

func (p *EventAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	validationContext := ctx
	if strings.EqualFold(filepath.Ext(document.URI), ".php") {
		validationContext = p.phpIndex.AddDocumentContext(
			ctx,
			path,
			document.Version,
			document.SyntaxTree.Root,
			document.SyntaxTree.Root,
		)
	}
	references := eventDocumentReferences(validationContext, document)
	if len(references) == 0 {
		return nil, nil
	}
	events, err := p.index.GetEvents()
	if err != nil {
		return nil, err
	}
	listenerEventNames := make([]string, 0, len(events))
	for _, current := range events {
		if len(current.Listeners()) != 0 {
			listenerEventNames = append(listenerEventNames, current.Name)
		}
	}

	var result []lsp.Problem
	for _, reference := range references {
		if ctx.Err() != nil {
			return nil, nil
		}
		switch reference.Role {
		case event.ReferenceEvent:
			if reference.Origin != event.OriginDispatch ||
				reference.Name == "" ||
				len(listenerEventNames) == 0 {
				continue
			}
			current, found, getErr := p.index.GetEvent(reference.Name)
			if getErr != nil {
				return nil, getErr
			}
			if found && len(current.Listeners()) != 0 {
				continue
			}
			result = append(result, eventDiagnostic(
				document,
				reference,
				missingEventCode,
				fmt.Sprintf(
					"Symfony event '%s' has no indexed listener",
					reference.Name,
				),
				suggestion.Similar(
					reference.Name,
					listenerEventNames,
				),
			))
		case event.ReferenceListenerMethod:
			if reference.Name == "" {
				continue
			}
			listener, found, resolveErr := event.ResolveListener(
				p.index,
				path,
				reference.Node.Range().Start,
				reference,
			)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if !found {
				continue
			}
			className := p.listenerClass(listener)
			if className == "" ||
				len(p.phpIndex.FindMethods(
					className,
					reference.Name,
				)) != 0 {
				continue
			}
			methods := event.PublicMethods(p.phpIndex, className)
			names := make([]string, 0, len(methods))
			for _, method := range methods {
				names = append(names, method.Name)
			}
			diagnostic := eventDiagnostic(
				document,
				reference,
				missingListenerMethodCode,
				fmt.Sprintf(
					"Event listener method '%s::%s' not found",
					className,
					reference.Name,
				),
				suggestion.Similar(reference.Name, names),
			)
			data := diagnostic.Payload.(map[string]any)
			data["className"] = className
			data["methodName"] = reference.Name
			data["eventTypes"] = eventListenerTypes(
				events,
				path,
				reference,
				listener,
			)
			result = append(result, diagnostic)
		}
	}
	return result, nil
}

func eventListenerTypes(
	events []event.Event,
	path string,
	reference event.Reference,
	resolved event.Occurrence,
) []string {
	offset := uint32(0)
	if reference.Node != nil {
		offset = reference.Node.RangeTrimmedTrivia().Start
	}
	seen := make(map[string]struct{})
	var result []string
	for _, current := range events {
		if current.EventType == "" {
			continue
		}
		matches := false
		for _, listener := range current.Listeners() {
			switch {
			case listener.File == path &&
				(eventRangeContains(listener.Range, offset) ||
					eventRangeContains(listener.MethodRange, offset)):
				matches = true
			case resolved.Class != "" &&
				strings.EqualFold(listener.Class, resolved.Class) &&
				strings.EqualFold(listener.Method, reference.Name):
				matches = true
			case resolved.Service != "" &&
				strings.EqualFold(listener.Service, resolved.Service) &&
				strings.EqualFold(listener.Method, reference.Name):
				matches = true
			}
			if matches {
				break
			}
		}
		if !matches {
			continue
		}
		key := strings.ToLower(current.EventType)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, current.EventType)
	}
	sort.Strings(result)
	return result
}

func eventRangeContains(rng cst.TextRange, offset uint32) bool {
	return rng.Len() != 0 && offset >= rng.Start && offset <= rng.End
}

func eventDocumentReferences(
	ctx context.Context,
	document *lsp.TextDocument,
) []event.Reference {
	root := document.SyntaxTree.Root
	var nodes []*phpsyntax.Node
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".php":
		nodes = phpquery.Nodes(root, phpsyntax.PhpString)
	case ".yaml", ".yml":
		nodes = yamlNodes(root)
	case ".xml":
		nodes = xmlNodes(root)
	default:
		return nil
	}
	var result []event.Reference
	for _, node := range nodes {
		if reference, ok := event.ReferenceAt(
			ctx,
			document.URI,
			root,
			node,
		); ok {
			result = append(result, reference)
		}
	}
	return result
}

func yamlNodes(root *yamlsyntax.Node) []*phpsyntax.Node {
	result := yamlquery.Nodes(root, yamlsyntax.YamlScalar)
	nodes := make([]*phpsyntax.Node, len(result))
	copy(nodes, result)
	return nodes
}

func xmlNodes(root *xmlsyntax.Node) []*phpsyntax.Node {
	result := xmlquery.Nodes(root, xmlsyntax.XmlAttribute)
	nodes := make([]*phpsyntax.Node, len(result))
	copy(nodes, result)
	return nodes
}

func eventDiagnostic(
	_ *lsp.TextDocument,
	reference event.Reference,
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

func (p *EventAnalyzer) listenerClass(
	listener event.Occurrence,
) string {
	if listener.Class != "" || listener.Service == "" ||
		p.services == nil {
		return listener.Class
	}
	service, found, err := p.services.GetServiceByID(listener.Service)
	if err != nil || !found {
		return ""
	}
	return service.Class
}
