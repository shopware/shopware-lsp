package symfony

import (
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

func (e *phpServiceEvaluator) evaluateStatement(statement *phpsyntax.Node) {
	calls := phpquery.Calls(statement)
	if len(calls) == 0 {
		return
	}
	assignedVariable := phpquery.AssignedVariable(statement)
	if e.captureAccessorAssignment(calls, assignedVariable) ||
		e.evaluateStandaloneConfigurator(calls) {
		return
	}
	definitionID := e.resolveDefinition(calls)
	if definitionID == "" {
		return
	}
	e.configureDefinition(definitionID, assignedVariable, calls)
}

func (e *phpServiceEvaluator) captureAccessorAssignment(
	calls []*phpsyntax.Node,
	assignedVariable string,
) bool {
	if e.isAccessorAssignment(calls, "services") {
		if assignedVariable != "" {
			e.serviceVariables[assignedVariable] = struct{}{}
		}
		return true
	}
	if !e.isAccessorAssignment(calls, "parameters") {
		return false
	}
	if assignedVariable != "" {
		e.parameterVariables[assignedVariable] = struct{}{}
	}
	return true
}

func (e *phpServiceEvaluator) evaluateStandaloneConfigurator(
	calls []*phpsyntax.Node,
) bool {
	if parameterSet := e.rootedCall(
		calls,
		e.parameterVariables,
		"parameters",
		"set",
	); parameterSet != nil {
		e.setParameter(parameterSet)
		return true
	}
	if remove := e.rootedCall(
		calls,
		e.serviceVariables,
		"services",
		"remove",
	); remove != nil {
		e.removeService(remove)
		return true
	}
	if defaults := e.rootedCall(
		calls,
		e.serviceVariables,
		"services",
		"defaults",
	); defaults != nil {
		e.configureDefaults(calls)
		return true
	}
	if instanceOf := e.rootedCall(
		calls,
		e.serviceVariables,
		"services",
		"instanceof",
	); instanceOf != nil {
		e.configureInstanceof(instanceOf, calls)
		return true
	}
	if load := e.rootedCall(
		calls,
		e.serviceVariables,
		"services",
		"load",
	); load != nil {
		e.setPrototype(load, calls)
		return true
	}
	return false
}

func (e *phpServiceEvaluator) removeService(call *phpsyntax.Node) {
	id, ok := e.argumentValue(call, 0)
	if !ok {
		return
	}
	delete(e.services, id)
	for variable, definitionID := range e.definitionVars {
		if definitionID == id {
			delete(e.definitionVars, variable)
		}
	}
}

func (e *phpServiceEvaluator) configureDefaults(calls []*phpsyntax.Node) {
	for _, call := range methodCalls(calls, "autowire") {
		if e.callHasConfigurator(phpquery.CallName(call), "defaults") {
			e.defaultAutowire, e.defaultAutowireSet = e.phpAutowireCall(call)
		}
	}
	for _, call := range methodCalls(calls, "tag") {
		if !e.callHasConfigurator(phpquery.CallName(call), "defaults") {
			continue
		}
		if tag, ok := e.argumentValue(call, 0); ok {
			e.defaultTags[tag] = ""
		}
	}
}

func (e *phpServiceEvaluator) configureInstanceof(
	instanceOf *phpsyntax.Node,
	calls []*phpsyntax.Node,
) {
	typeName, ok := e.argumentValue(instanceOf, 0)
	if !ok {
		return
	}
	for _, call := range methodCalls(calls, "tag") {
		if !e.callHasConfigurator(phpquery.CallName(call), "instanceof") {
			continue
		}
		if tag, found := e.argumentValue(call, 0); found {
			e.instanceofTags[tag] = typeName
		}
	}
}

func (e *phpServiceEvaluator) resolveDefinition(calls []*phpsyntax.Node) string {
	if call := e.rootedCall(calls, e.serviceVariables, "services", "set"); call != nil {
		return e.setService(call)
	}
	if call := e.invokedServicesCall(calls); call != nil {
		return e.setService(call)
	}
	if call := e.rootedCall(calls, e.serviceVariables, "services", "alias"); call != nil {
		return e.setAlias(call)
	}
	if call := e.rootedCall(calls, e.serviceVariables, "services", "stack"); call != nil {
		return e.setStack(call)
	}
	if call := e.rootedCall(calls, e.serviceVariables, "services", "get"); call != nil {
		definitionID, _ := e.argumentValue(call, 0)
		return definitionID
	}
	return e.definitionForCalls(calls)
}

func (e *phpServiceEvaluator) configureDefinition(
	definitionID,
	assignedVariable string,
	calls []*phpsyntax.Node,
) {
	if assignedVariable != "" {
		e.definitionVars[assignedVariable] = definitionID
	}
	service, exists := e.services[definitionID]
	if !exists {
		return
	}
	e.configureServiceClass(&service, definitionID, calls)
	if service.Tags == nil {
		service.Tags = make(map[string]string)
	}
	e.configureServiceTags(&service, definitionID, calls)
	e.configureServiceAutowire(&service, definitionID, calls)
	e.configureServiceDecoration(&service, definitionID, calls)
	e.configureServiceParent(&service, definitionID, calls)
	e.configureServiceDeprecation(&service, definitionID, calls)
	if e.collectReferences {
		e.references = append(
			e.references,
			e.serviceArgumentReferences(service, calls)...,
		)
		e.methodReferences = append(
			e.methodReferences,
			e.serviceMethodReferences(service, calls)...,
		)
	}
	e.services[definitionID] = service
}

func (e *phpServiceEvaluator) configureServiceClass(
	service *Service,
	definitionID string,
	calls []*phpsyntax.Node,
) {
	for _, call := range methodCalls(calls, "class") {
		if !e.callTargetsDefinition(call, definitionID) {
			continue
		}
		if className, ok := e.argumentValue(call, 0); ok {
			service.Class = className
		}
	}
}

func (e *phpServiceEvaluator) configureServiceTags(
	service *Service,
	definitionID string,
	calls []*phpsyntax.Node,
) {
	for _, call := range methodCalls(calls, "tag") {
		if !e.callTargetsDefinition(call, definitionID) {
			continue
		}
		if tag, ok := e.argumentValue(call, 0); ok {
			service.Tags[tag] = ""
		}
	}
}

func (e *phpServiceEvaluator) configureServiceAutowire(
	service *Service,
	definitionID string,
	calls []*phpsyntax.Node,
) {
	for _, call := range methodCalls(calls, "autowire") {
		if e.callTargetsDefinition(call, definitionID) {
			service.Autowire, service.AutowireSet = e.phpAutowireCall(call)
		}
	}
}

func (e *phpServiceEvaluator) configureServiceDecoration(
	service *Service,
	definitionID string,
	calls []*phpsyntax.Node,
) {
	for _, call := range methodCalls(calls, "decorate") {
		if !e.callTargetsDefinition(call, definitionID) {
			continue
		}
		if target, ok := e.argumentValue(call, 0); ok {
			service.Decorates = target
			service.DecoratesRange = phpArgumentContentRange(call, 0)
		}
	}
}

func (e *phpServiceEvaluator) configureServiceParent(
	service *Service,
	definitionID string,
	calls []*phpsyntax.Node,
) {
	for _, call := range methodCalls(calls, "parent") {
		if !e.callTargetsDefinition(call, definitionID) {
			continue
		}
		if target, ok := e.argumentValue(call, 0); ok {
			service.Parent = target
			service.ParentRange = phpArgumentContentRange(call, 0)
		}
	}
}

func (e *phpServiceEvaluator) configureServiceDeprecation(
	service *Service,
	definitionID string,
	calls []*phpsyntax.Node,
) {
	for _, call := range methodCalls(calls, "deprecate") {
		if !e.callTargetsDefinition(call, definitionID) {
			continue
		}
		service.Deprecated = true
		service.DeprecatedRange = call.RangeTrimmedTrivia()
		if message, ok := e.argumentValue(call, 2); ok {
			service.Deprecation = message
		}
	}
}
