package admin

import (
	"strings"

	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

// IsServiceReference reports string positions which refer to an
// Administration service rather than register one.
func IsServiceReference(node *jssyntax.Node) bool {
	if jsquery.StringInCall(
		node,
		0,
		"Shopware.Service",
		"Service",
		"Application.addServiceProviderMiddleware",
		"Shopware.Application.addServiceProviderMiddleware",
		"Application.addServiceProviderDecorator",
		"Shopware.Application.addServiceProviderDecorator",
	) {
		return true
	}
	if jsquery.StringAt(node) == nil {
		return false
	}
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == jssyntax.JsProperty &&
			jsquery.PropertyName(current) == "inject" {
			return true
		}
	}
	return false
}

func IsStoreReference(node *jssyntax.Node) bool {
	return jsquery.StringInCall(
		node,
		0,
		"Shopware.Store.get",
		"Store.get",
		"Shopware.Store.unregister",
		"Store.unregister",
	)
}

func IsPrivilegeReference(node *jssyntax.Node) bool {
	if jsquery.StringAt(node) == nil {
		return false
	}
	if jsquery.StringArgumentIndex(node) == 0 {
		callName := jsquery.CallName(node)
		if callName == "acl.can" ||
			strings.HasSuffix(callName, ".acl.can") ||
			strings.HasSuffix(callName, "Service('acl').can") ||
			strings.HasSuffix(callName, `Service("acl").can`) ||
			strings.HasSuffix(callName, "Service('privileges').getPrivileges") ||
			strings.HasSuffix(callName, `Service("privileges").getPrivileges`) {
			return true
		}
	}
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != jssyntax.JsProperty {
			continue
		}
		switch jsquery.PropertyName(current) {
		case "privilege":
			return true
		case "dependencies":
			call := jsquery.CallAt(current)
			return call != nil &&
				jsquery.CallMethodName(call) == "addPrivilegeMappingEntry" &&
				strings.HasSuffix(
					jsquery.CallName(call), ".addPrivilegeMappingEntry",
				)
		}
	}
	return false
}
