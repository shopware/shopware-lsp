package symfony

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type PHPTypeExtension struct {
	services *ServiceIndex
}

func NewPHPTypeExtension(services *ServiceIndex) *PHPTypeExtension {
	return &PHPTypeExtension{services: services}
}

func (e *PHPTypeExtension) InferCall(
	context inference.CallContext,
) (semantic.TypeFact, bool) {
	if fact, ok := inferHeaderBagGet(context); ok {
		return fact, true
	}
	if e == nil || e.services == nil || !strings.EqualFold(context.Name, "get") ||
		len(context.Arguments) == 0 ||
		context.Arguments[0].Type.Kind() != types.LiteralStringKind {
		return semantic.TypeFact{}, false
	}
	if !isContainerType(context) {
		return semantic.TypeFact{}, false
	}
	serviceID := context.Arguments[0].Type.Name()
	service, found, err := e.resolveService(serviceID, make(map[string]struct{}))
	if err != nil || !found {
		return semantic.TypeFact{}, false
	}
	className := service.Class
	if className == "" && strings.Contains(service.ID, "\\") {
		className = service.ID
	}
	if className == "" {
		return semantic.TypeFact{}, false
	}
	return semantic.TypeFact{
		Type:       types.Named(className),
		Confidence: semantic.DeclaredConfidence,
		Source:     semantic.FrameworkSource,
		Reason:     "Symfony service container",
	}, true
}

func inferHeaderBagGet(
	context inference.CallContext,
) (semantic.TypeFact, bool) {
	if context.Static || !strings.EqualFold(context.Name, "get") ||
		len(context.Arguments) < 2 || context.Snapshot == nil ||
		!context.Snapshot.Relations().IsSubtype(
			context.Receiver,
			types.Named("Symfony\\Component\\HttpFoundation\\HeaderBag"),
		) {
		return semantic.TypeFact{}, false
	}
	defaultType := context.Arguments[1].Type
	if types.ContainsUncertain(defaultType) ||
		!context.Snapshot.Relations().IsAssignableTo(
			defaultType,
			types.Nullable(types.String()),
		) {
		return semantic.TypeFact{}, false
	}
	return semantic.TypeFact{
		Type: context.Snapshot.Relations().Join(
			types.String(),
			defaultType,
		),
		Confidence: semantic.DeclaredConfidence,
		Source:     semantic.FrameworkSource,
		Reason:     "Symfony HeaderBag default",
	}, true
}

func (e *PHPTypeExtension) resolveService(
	id string,
	visited map[string]struct{},
) (Service, bool, error) {
	if _, exists := visited[id]; exists {
		return Service{}, false, nil
	}
	visited[id] = struct{}{}
	service, found, err := e.services.GetServiceByID(id)
	if err != nil || !found || service.AliasTarget == "" {
		return service, found, err
	}
	return e.resolveService(service.AliasTarget, visited)
}

func isContainerType(context inference.CallContext) bool {
	if context.Receiver.IsUnknown() {
		return false
	}
	if context.Snapshot == nil {
		return false
	}
	relations := context.Snapshot.Relations()
	for _, candidate := range []string{
		"Psr\\Container\\ContainerInterface",
		"Symfony\\Component\\DependencyInjection\\ContainerInterface",
		"Symfony\\Contracts\\Service\\ServiceProviderInterface",
	} {
		if relations.IsSubtype(context.Receiver, types.Named(candidate)) {
			return true
		}
	}
	return false
}
