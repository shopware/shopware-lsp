package completion

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

type controllerCompletionCandidate struct {
	label  string
	detail string
}

// ControllerCompletionProvider completes statically resolvable controller()
// targets without relying on IDE indexes or tree-sitter queries.
type ControllerCompletionProvider struct {
	php      *php.PHPIndex
	services *symfony.ServiceIndex
	routes   *symfony.RouteIndexer

	cacheMu       sync.Mutex
	cacheRevision uint64
	classCache    []controllerCompletionCandidate
}

func NewControllerCompletionProvider(
	phpIndex *php.PHPIndex,
	services *symfony.ServiceIndex,
	routes *symfony.RouteIndexer,
) *ControllerCompletionProvider {
	return &ControllerCompletionProvider{
		php:      phpIndex,
		services: services,
		routes:   routes,
	}
}

func (p *ControllerCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.php == nil || request == nil || request.Node == nil ||
		!strings.EqualFold(filepath.Ext(request.TextDocument.URI), ".twig") {
		return nil
	}
	value, ok := symfony.TwigControllerValueAt(request.Node)
	if !ok {
		return nil
	}
	candidates := p.classCandidates(ctx)
	candidates = append(candidates, p.serviceCandidates(ctx)...)
	sort.Slice(candidates, func(left, right int) bool {
		return strings.ToLower(candidates[left].label) <
			strings.ToLower(candidates[right].label)
	})

	editRange := controllerCompletionRange(value.Range, request.LineIndex)
	result := make([]protocol.CompletionItem, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil
		}
		key := strings.ToLower(candidate.label)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, protocol.CompletionItem{
			Label:      candidate.label,
			Kind:       int(protocol.MethodCompletion),
			Detail:     candidate.detail,
			FilterText: candidate.label,
			TextEdit: protocol.TextEdit{
				Range:   editRange,
				NewText: twigControllerInsertText(candidate.label),
			},
		})
	}
	return result
}

func (p *ControllerCompletionProvider) classCandidates(
	ctx context.Context,
) []controllerCompletionCandidate {
	revision := p.php.Revision()
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if p.classCache != nil && p.cacheRevision == revision {
		return append([]controllerCompletionCandidate(nil), p.classCache...)
	}
	var result []controllerCompletionCandidate
	for _, class := range p.php.ClassSymbols() {
		if ctx.Err() != nil {
			return nil
		}
		if class.Kind != semantic.ClassSymbol ||
			!strings.HasSuffix(class.Name, "Controller") ||
			controllerTestClass(class) {
			continue
		}
		for _, method := range p.php.Methods(class.FullyQualified) {
			if !controllerActionMethod(method, true) {
				continue
			}
			label := strings.TrimLeft(class.FullyQualified, `\`)
			if method.Name != "__invoke" {
				label += "::" + method.Name
			}
			result = append(result, controllerCompletionCandidate{
				label:  label,
				detail: controllerMethodDetail(class, method),
			})
			if legacy := legacyControllerCandidate(class, method); legacy != "" {
				result = append(result, controllerCompletionCandidate{
					label:  legacy,
					detail: controllerMethodDetail(class, method),
				})
			}
		}
	}
	p.classCache = append([]controllerCompletionCandidate(nil), result...)
	p.cacheRevision = revision
	return result
}

func legacyControllerCandidate(
	class,
	method semantic.Symbol,
) string {
	if !strings.HasSuffix(method.Name, "Action") {
		return ""
	}
	name := strings.TrimLeft(class.FullyQualified, `\`)
	marker := `\Controller\`
	offset := strings.Index(name, marker)
	if offset < 0 {
		return ""
	}
	prefix := name[:offset]
	bundle := prefix
	if separator := strings.LastIndex(bundle, `\`); separator >= 0 {
		bundle = bundle[separator+1:]
	}
	if !strings.HasSuffix(bundle, "Bundle") {
		return ""
	}
	controller := strings.TrimSuffix(
		name[offset+len(marker):],
		"Controller",
	)
	if controller == "" {
		return ""
	}
	return bundle + ":" + controller + ":" +
		strings.TrimSuffix(method.Name, "Action")
}

func (p *ControllerCompletionProvider) serviceCandidates(
	ctx context.Context,
) []controllerCompletionCandidate {
	if p.routes == nil || p.services == nil {
		return nil
	}
	routes, err := p.routes.GetRoutes()
	if err != nil {
		return nil
	}
	serviceIDs := make(map[string]struct{})
	for _, route := range routes {
		reference, ok := symfony.ParseControllerReference(route.Controller)
		if !ok || strings.Contains(reference.Target, `\`) {
			continue
		}
		serviceIDs[reference.Target] = struct{}{}
	}
	var result []controllerCompletionCandidate
	for serviceID := range serviceIDs {
		if ctx.Err() != nil {
			return nil
		}
		service, found, err := p.services.GetServiceByID(serviceID)
		if err != nil || !found || service.Class == "" {
			continue
		}
		class, found := p.php.FindClass(service.Class)
		if !found {
			continue
		}
		for _, method := range p.php.Methods(class.FullyQualified) {
			if !controllerActionMethod(method, false) {
				continue
			}
			if method.Name == "__invoke" {
				result = append(result, controllerCompletionCandidate{
					label:  serviceID,
					detail: controllerMethodDetail(class, method),
				})
				continue
			}
			for _, separator := range []string{":", "::"} {
				result = append(result, controllerCompletionCandidate{
					label:  serviceID + separator + method.Name,
					detail: controllerMethodDetail(class, method),
				})
			}
		}
	}
	return result
}

func controllerActionMethod(
	method semantic.Symbol,
	classController bool,
) bool {
	if method.Kind != semantic.MethodSymbol ||
		method.Visibility != semantic.Public {
		return false
	}
	if strings.HasPrefix(method.Name, "__") && method.Name != "__invoke" {
		return false
	}
	return classController ||
		!strings.HasPrefix(strings.ToLower(method.Name), "set")
}

func controllerTestClass(class semantic.Symbol) bool {
	name := `\` + strings.TrimLeft(class.FullyQualified, `\`) + `\`
	return strings.Contains(strings.ToLower(name), `\test\`) ||
		strings.Contains(strings.ToLower(name), `\tests\`)
}

func controllerMethodDetail(
	class,
	method semantic.Symbol,
) string {
	parameters := make([]string, 0, len(method.Parameters))
	for _, parameter := range method.Parameters {
		name := parameter.Name
		if name == "" {
			name = "$value"
		}
		parameters = append(parameters, name)
	}
	return fmt.Sprintf(
		"%s::%s(%s)",
		strings.TrimLeft(class.FullyQualified, `\`),
		method.Name,
		strings.Join(parameters, ", "),
	)
}

func controllerCompletionRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	if lineIndex == nil {
		return protocol.Range{}
	}
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}

func twigControllerInsertText(value string) string {
	return strings.ReplaceAll(value, `\`, `\\`)
}

func (p *ControllerCompletionProvider) GetTriggerCharacters() []string {
	return []string{`\`, ":"}
}
