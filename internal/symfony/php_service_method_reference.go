package symfony

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	php "github.com/shopware/shopware-lsp/internal/php"
)

func (e *phpServiceEvaluator) serviceMethodReferences(
	service Service,
	calls []*phpsyntax.Node,
) []ServiceMethodReference {
	base := ServiceMethodReference{
		OwnerServiceID: service.ID,
		OwnerClass:     service.Class,
		Format:         "php",
	}
	return phpServiceMethodReferencesForCalls(
		base,
		calls,
		e.resolver,
		e.path,
		func(call *phpsyntax.Node) bool {
			return e.callTargetsDefinition(call, service.ID)
		},
	)
}

func phpServiceMethodReferencesForCalls(
	base ServiceMethodReference,
	calls []*phpsyntax.Node,
	resolver *php.NameResolver,
	path string,
	accept func(*phpsyntax.Node) bool,
) []ServiceMethodReference {
	var result []ServiceMethodReference
	for _, call := range methodCalls(calls, "call") {
		if !accept(call) {
			continue
		}
		method, static := staticPHPValue(
			phpquery.ArgumentValueText(call, 0),
			resolver,
			path,
		)
		if !static || method == "" {
			continue
		}
		reference := base
		reference.MethodName = method
		reference.Range = phpArgumentContentRange(call, 0)
		result = append(result, reference)
	}
	return result
}

func directPHPServiceMethodReferences(
	path string,
	root *phpsyntax.Node,
) []ServiceMethodReference {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []ServiceMethodReference
	for _, statement := range phpquery.ExpressionStatements(root) {
		calls := phpquery.Calls(statement)
		var setCall *phpsyntax.Node
		for _, candidate := range methodCalls(calls, "set") {
			if strings.Contains(
				strings.ToLower(phpquery.CallName(candidate)),
				"->services()->set",
			) {
				setCall = candidate
				break
			}
		}
		if setCall == nil {
			continue
		}
		ownerID, static := staticPHPValue(
			phpquery.ArgumentValueText(setCall, 0),
			resolver,
			path,
		)
		if !static || ownerID == "" {
			continue
		}
		ownerClass := ownerID
		if value := phpquery.ArgumentValueText(setCall, 1); value != "" {
			className, classStatic := staticPHPValue(value, resolver, path)
			if !classStatic {
				continue
			}
			ownerClass = className
		}
		if !strings.Contains(ownerClass, "\\") {
			continue
		}
		result = append(
			result,
			phpServiceMethodReferencesForCalls(
				ServiceMethodReference{
					OwnerServiceID: ownerID,
					OwnerClass:     strings.TrimPrefix(ownerClass, "\\"),
					Format:         "php",
				},
				calls,
				resolver,
				path,
				func(*phpsyntax.Node) bool { return true },
			)...,
		)
	}
	return result
}

func phpArrayServiceMethodReferences(
	path string,
	root *phpsyntax.Node,
) []ServiceMethodReference {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []ServiceMethodReference
	for _, config := range phpArrayConfigRoots(root) {
		services := phpArrayEntryArray(config, "services", resolver, path)
		for _, item := range phpquery.ArrayItems(services) {
			ownerID := phpArrayStaticValue(
				phpquery.ArrayItemKey(item),
				resolver,
				path,
			)
			if ownerID == "" || strings.HasPrefix(ownerID, "_") {
				continue
			}
			definition := phpArrayNode(phpquery.ArrayItemValue(item))
			if definition == nil {
				continue
			}
			ownerClass := ""
			if strings.Contains(ownerID, "\\") {
				ownerClass = strings.TrimPrefix(ownerID, "\\")
			}
			if class := phpArrayOptionValue(
				definition,
				"class",
				resolver,
				path,
			); class != nil {
				value, static := phpArrayStaticValueOK(class, resolver, path)
				if !static {
					ownerClass = ""
				} else {
					ownerClass = strings.TrimPrefix(value, "\\")
				}
			}
			if ownerClass == "" {
				continue
			}
			calls := phpArrayNode(phpArrayOptionValue(
				definition,
				"calls",
				resolver,
				path,
			))
			for _, call := range phpquery.ArrayItems(calls) {
				method := phpquery.ArrayItemKey(call)
				if method == nil {
					tuple := phpArrayNode(phpquery.ArrayItemValue(call))
					parts := phpquery.ArrayItems(tuple)
					if len(parts) == 0 {
						continue
					}
					method = phpquery.ArrayItemValue(parts[0])
				}
				methodName, static := phpArrayStaticValueOK(
					method,
					resolver,
					path,
				)
				if !static || methodName == "" {
					continue
				}
				result = append(result, ServiceMethodReference{
					OwnerServiceID: ownerID,
					OwnerClass:     ownerClass,
					MethodName:     methodName,
					Range:          phpExpressionContentRange(method),
					Format:         "php",
				})
			}
		}
	}
	return result
}

func uniqueServiceMethodReferences(
	references []ServiceMethodReference,
) []ServiceMethodReference {
	type key struct {
		owner  string
		class  string
		method string
		start  uint32
		end    uint32
	}
	seen := make(map[key]struct{}, len(references))
	result := make([]ServiceMethodReference, 0, len(references))
	for _, reference := range references {
		value := key{
			owner:  reference.OwnerServiceID,
			class:  reference.OwnerClass,
			method: reference.MethodName,
			start:  reference.Range.Start,
			end:    reference.Range.End,
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, reference)
	}
	return result
}
