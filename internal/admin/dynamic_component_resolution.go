package admin

import (
	"sort"
	"strings"

	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

type dynamicComponentNames struct {
	names    []string
	found    bool
	complete bool
}

// ResolveDynamicComponentSelector augments the selector syntax with component
// names which are safely recoverable from the owning effective Vue component.
// Inline literals remain authoritative. Runtime props, services, mutable data,
// and unresolved return branches may contribute candidates but never turn the
// selector into a closed contract.
func (idx *AdminComponentIndexer) ResolveDynamicComponentSelector(
	templatePath string,
	selector VueDynamicComponentSelector,
	context ...*twigsyntax.Node,
) (VueDynamicComponentSelector, bool, error) {
	return idx.resolveDynamicComponentSelector(
		templatePath, selector, nil, context...,
	)
}

// ResolveDynamicComponentSelectorForOwner resolves an inferred selector
// against the request-local owner of an open Vue document.
func (idx *AdminComponentIndexer) ResolveDynamicComponentSelectorForOwner(
	templatePath string,
	selector VueDynamicComponentSelector,
	owner *VueComponent,
	context ...*twigsyntax.Node,
) (VueDynamicComponentSelector, bool, error) {
	return idx.resolveDynamicComponentSelector(
		templatePath, selector, owner, context...,
	)
}

func (idx *AdminComponentIndexer) resolveDynamicComponentSelector(
	templatePath string,
	selector VueDynamicComponentSelector,
	component *VueComponent,
	context ...*twigsyntax.Node,
) (VueDynamicComponentSelector, bool, error) {
	if idx == nil {
		return selector, false, nil
	}
	if selector.Complete && len(selector.Names()) > 0 {
		return selector, true, nil
	}
	if component == nil {
		var err error
		component, err = idx.GetComponentByTemplatePath(templatePath)
		if err != nil || component == nil {
			return selector, false, err
		}
	}
	resolved := idx.resolveDynamicComponentNames(
		*component, selector.Expression, make(map[string]bool),
	)
	if (!resolved.found || len(resolved.names) == 0) && len(context) > 0 {
		routeResolved, routeErr := idx.resolveRouterViewDynamicComponentNames(
			*component, selector, context[0],
		)
		if routeErr != nil {
			return selector, false, routeErr
		}
		if routeResolved.found {
			resolved = routeResolved
		}
	}
	if !resolved.found || len(resolved.names) == 0 {
		return selector, false, nil
	}
	result := selector
	result.Candidates = nil
	for _, name := range resolved.names {
		result.Candidates = append(result.Candidates, VueDynamicComponentCandidate{
			Name: name,
		})
	}
	result.Complete = resolved.complete
	return result, true, nil
}

// ResolveDynamicComponentContracts returns every concrete component behind a
// closed inline or inferred selector. The returned selector lets diagnostics
// distinguish source literals (exact candidate ranges) from inferred names.
func (idx *AdminComponentIndexer) ResolveDynamicComponentContracts(
	templatePath string,
	selector VueDynamicComponentSelector,
	context ...*twigsyntax.Node,
) (VueDynamicComponentSelector, []VueComponent, bool, error) {
	return idx.resolveDynamicComponentContracts(
		templatePath, selector, nil, context...,
	)
}

// ResolveDynamicComponentContractsForOwner is the live-document counterpart
// of ResolveDynamicComponentContracts.
func (idx *AdminComponentIndexer) ResolveDynamicComponentContractsForOwner(
	templatePath string,
	selector VueDynamicComponentSelector,
	owner *VueComponent,
	context ...*twigsyntax.Node,
) (VueDynamicComponentSelector, []VueComponent, bool, error) {
	return idx.resolveDynamicComponentContracts(
		templatePath, selector, owner, context...,
	)
}

func (idx *AdminComponentIndexer) resolveDynamicComponentContracts(
	templatePath string,
	selector VueDynamicComponentSelector,
	owner *VueComponent,
	context ...*twigsyntax.Node,
) (VueDynamicComponentSelector, []VueComponent, bool, error) {
	resolvedSelector, _, err := idx.resolveDynamicComponentSelector(
		templatePath, selector, owner, context...,
	)
	if err != nil {
		return selector, nil, false, err
	}
	components, complete, err := idx.ResolveDynamicComponents(
		templatePath, resolvedSelector, owner,
	)
	return resolvedSelector, components, complete, err
}

// resolveRouterViewDynamicComponentNames joins Vue Router's Component slot
// binding with the indexed Administration module route tree. A router-view in
// the component registered for route A can render the concrete components of
// A's direct child routes. Unlike an arbitrary runtime selector, that set is
// finite for the indexed workspace and can safely drive the normal dynamic
// component contract intersection.
func (idx *AdminComponentIndexer) resolveRouterViewDynamicComponentNames(
	owner VueComponent,
	selector VueDynamicComponentSelector,
	startTag *twigsyntax.Node,
) (dynamicComponentNames, error) {
	if !twigDynamicComponentUsesRouterView(startTag, selector) {
		return dynamicComponentNames{}, nil
	}
	return idx.resolveRouterViewRouteComponentNames(owner)
}

func (idx *AdminComponentIndexer) resolveRouterViewRouteComponentNames(
	owner VueComponent,
) (dynamicComponentNames, error) {
	routes, err := idx.GetAllModuleRoutes()
	if err != nil {
		return dynamicComponentNames{}, err
	}
	hostRoutes := make(map[string]bool)
	ownerName := CamelToKebab(owner.Name)
	for _, route := range routes {
		if CamelToKebab(route.Component) == ownerName && route.Name != "" {
			hostRoutes[route.Name] = true
		}
	}
	if len(hostRoutes) == 0 {
		return dynamicComponentNames{}, nil
	}
	result := dynamicComponentNames{found: true, complete: true}
	for _, route := range routes {
		if route.Component == "" {
			continue
		}
		for host := range hostRoutes {
			suffix := strings.TrimPrefix(route.Name, host+".")
			if suffix == route.Name || suffix == "" ||
				strings.Contains(suffix, ".") {
				continue
			}
			result.names = appendUniqueDynamicNames(
				result.names, route.Component,
			)
			break
		}
	}
	if len(result.names) == 0 {
		return dynamicComponentNames{}, nil
	}
	sort.Strings(result.names)
	return result, nil
}

func twigDynamicComponentUsesRouterView(
	startTag *twigsyntax.Node,
	selector VueDynamicComponentSelector,
) bool {
	if startTag == nil || selector.ExpressionRange.Len() == 0 {
		return false
	}
	path, matched := vueStaticTemplateExpression(selector.Expression)
	if !matched || len(path) == 0 {
		return false
	}
	for _, segment := range path {
		if segment.Indexed || segment.Called {
			return false
		}
	}
	root := startTag
	for root.Parent() != nil {
		root = root.Parent()
	}
	scope, found := TwigScopedSlotAtOffset(
		root, selector.ExpressionRange.Start,
	)
	return found && scope.ComponentName == "router-view" &&
		routerViewComponentPath(scope, path)
}

func routerViewComponentPath(
	scope TwigScopedSlot,
	path []vueStaticExpressionSegment,
) bool {
	for _, binding := range scope.Bindings {
		if binding.WholeObject {
			if len(path) == 2 && path[0].Name == binding.LocalName &&
				path[1].Name == "Component" {
				return true
			}
			continue
		}
		if len(path) == 1 && binding.MemberName == "Component" &&
			path[0].Name == binding.LocalName {
			return true
		}
	}
	return false
}

func (idx *AdminComponentIndexer) resolveDynamicComponentNames(
	component VueComponent,
	expression string,
	resolving map[string]bool,
) dynamicComponentNames {
	expression = unwrapVueExpressionParentheses(
		trimVueSourceExpression(expression),
	)
	if expression == "" {
		return dynamicComponentNames{}
	}
	if name, _, _, literal := staticDynamicComponentString(expression); literal {
		return dynamicComponentNames{
			names: []string{name}, found: true, complete: true,
		}
	}
	switch expression {
	case "null", "undefined", "false":
		return dynamicComponentNames{found: true, complete: true}
	}
	trueStart, trueEnd, falseStart, falseEnd, conditional :=
		splitDynamicComponentConditional(expression)
	if conditional {
		return mergeDynamicComponentNames(
			idx.resolveDynamicComponentNames(
				component, expression[trueStart:trueEnd], resolving,
			),
			idx.resolveDynamicComponentNames(
				component, expression[falseStart:falseEnd], resolving,
			),
		)
	}
	for _, operator := range []string{"??", "||"} {
		if left, right, split := splitVueTopLevelOperator(
			expression, operator,
		); split {
			return mergeDynamicComponentNames(
				idx.resolveDynamicComponentNames(component, left, resolving),
				idx.resolveDynamicComponentNames(component, right, resolving),
			)
		}
	}
	if cmsNames := idx.resolveCMSDynamicComponentNames(
		component, expression, resolving,
	); cmsNames.found {
		return cmsNames
	}
	path, matched := vueStaticTemplateExpression(expression)
	if !matched || len(path) != 1 || path[0].Indexed {
		return dynamicComponentNames{}
	}
	if !path[0].Called {
		for _, local := range component.LocalComponents {
			if path[0].Name != local.Symbol &&
				path[0].Name != local.Name &&
				CamelToKebab(path[0].Name) != local.Name {
				continue
			}
			return dynamicComponentNames{
				names: []string{local.Name}, found: true, complete: true,
			}
		}
	}
	member, found := component.TemplateMember(path[0].Name)
	if !found || member.Kind == ComponentMemberMethod && !path[0].Called ||
		member.Kind != ComponentMemberMethod && path[0].Called {
		return dynamicComponentNames{}
	}
	key := string(member.Kind) + "\x00" + member.Name
	if resolving[key] {
		return dynamicComponentNames{}
	}
	resolving[key] = true
	defer delete(resolving, key)

	if typeNames := dynamicComponentNamesFromType(memberValueType(member)); typeNames.found && typeNames.complete {
		return typeNames
	}
	if member.Kind == ComponentMemberData {
		result := dynamicComponentNames{}
		if member.SourceExpression != "" {
			result = idx.resolveDynamicComponentNames(
				component, member.SourceExpression, resolving,
			)
		}
		for _, assignment := range component.Assignments {
			if assignment.Target != member.Name || assignment.Expression == "" {
				continue
			}
			result = mergeDynamicComponentNames(
				result,
				idx.resolveDynamicComponentNames(
					component, assignment.Expression, resolving,
				),
			)
		}
		result.complete = false
		if result.found {
			return result
		}
	}
	if member.SourceExpression != "" {
		resolved := idx.resolveDynamicComponentNames(
			component, member.SourceExpression, resolving,
		)
		if resolved.found {
			return resolved
		}
	}
	if len(member.ReturnExpressions) > 0 {
		result := dynamicComponentNames{
			found: true, complete: member.ReturnsComplete,
		}
		for _, alternative := range member.ReturnExpressions {
			resolved := idx.resolveDynamicComponentNames(
				component, alternative, resolving,
			)
			result.names = appendUniqueDynamicNames(
				result.names, resolved.names...,
			)
			if !resolved.found || !resolved.complete {
				result.complete = false
			}
		}
		return result
	}
	if member.Kind == ComponentMemberProp {
		for _, prop := range component.Props {
			if prop.Name != member.Name || prop.Default == "" {
				continue
			}
			resolved := idx.resolveDynamicComponentNames(
				component, prop.Default, resolving,
			)
			resolved.complete = false
			return resolved
		}
	}
	return dynamicComponentNames{}
}

func (idx *AdminComponentIndexer) resolveCMSDynamicComponentNames(
	component VueComponent,
	expression string,
	resolving map[string]bool,
) dynamicComponentNames {
	field, base, found := cmsComponentFieldExpression(expression)
	if !found {
		return dynamicComponentNames{}
	}
	rootName := vueExpressionRootName(base)
	if rootName == "" {
		return dynamicComponentNames{}
	}
	member, found := component.TemplateMember(rootName)
	if !found {
		return dynamicComponentNames{}
	}
	kind, found := cmsRegistryKindFromMember(component, member, resolving)
	if !found {
		return dynamicComponentNames{}
	}
	registrations, err := idx.GetAllCMSRegistrationsByKind(kind)
	if err != nil || len(registrations) == 0 {
		return dynamicComponentNames{}
	}
	result := dynamicComponentNames{found: true, complete: true}
	for _, registration := range registrations {
		name := ""
		switch field {
		case "component":
			name = registration.Component
		case "configComponent":
			name = registration.ConfigComponent
		case "previewComponent":
			name = registration.PreviewComponent
		}
		result.names = appendUniqueDynamicNames(result.names, name)
	}
	if len(result.names) == 0 {
		return dynamicComponentNames{}
	}
	return result
}

func cmsComponentFieldExpression(expression string) (string, string, bool) {
	expression = unwrapVueExpressionParentheses(
		trimVueSourceExpression(expression),
	)
	for _, field := range []string{
		"configComponent", "previewComponent", "component",
	} {
		for _, separator := range []string{"?.", "."} {
			suffix := separator + field
			if strings.HasSuffix(expression, suffix) {
				base := strings.TrimSpace(
					strings.TrimSuffix(expression, suffix),
				)
				return field, base, base != ""
			}
		}
	}
	return "", "", false
}

func vueExpressionRootName(expression string) string {
	expression = strings.TrimSpace(expression)
	expression = strings.TrimPrefix(expression, "this.")
	end := 0
	for end < len(expression) {
		character := expression[end]
		if end == 0 && !isVueIdentifierStart(character) ||
			end > 0 && !isVueIdentifierPart(character) {
			break
		}
		end++
	}
	if end == 0 {
		return ""
	}
	return expression[:end]
}

func cmsRegistryKindFromMember(
	component VueComponent,
	member VueComponentMember,
	resolving map[string]bool,
) (AdminCMSRegistrationKind, bool) {
	if member.CMSRegistryKind != "" {
		return member.CMSRegistryKind, true
	}
	key := "cms\x00" + string(member.Kind) + "\x00" + member.Name
	if resolving[key] {
		return "", false
	}
	resolving[key] = true
	defer delete(resolving, key)
	expressions := append(
		[]string{member.SourceExpression}, member.ReturnExpressions...,
	)
	for _, expression := range expressions {
		if kind, found := cmsRegistryKindFromExpression(expression); found {
			return kind, true
		}
		rootName := vueExpressionRootName(expression)
		if rootName == "" || rootName == member.Name {
			continue
		}
		root, found := component.TemplateMember(rootName)
		if !found {
			continue
		}
		if kind, found := cmsRegistryKindFromMember(
			component, root, resolving,
		); found {
			return kind, true
		}
	}
	return "", false
}

func cmsRegistryKindFromExpression(
	expression string,
) (AdminCMSRegistrationKind, bool) {
	switch {
	case strings.Contains(expression, "getCmsElementConfigByName("),
		strings.Contains(expression, "getCmsElementRegistry("),
		strings.Contains(expression, ".elementRegistry"):
		return AdminCMSElement, true
	case strings.Contains(expression, "getCmsBlockConfigByName("),
		strings.Contains(expression, "getCmsBlockRegistry("),
		strings.Contains(expression, ".blockRegistry"):
		return AdminCMSBlock, true
	default:
		return "", false
	}
}

func memberValueType(member VueComponentMember) string {
	if member.Kind == ComponentMemberMethod {
		return VueCallableReturnType(member.Type)
	}
	return member.Type
}

func dynamicComponentNamesFromType(value string) dynamicComponentNames {
	value = strings.TrimSpace(value)
	if value == "" {
		return dynamicComponentNames{}
	}
	result := dynamicComponentNames{found: true, complete: true}
	for _, alternative := range splitAdminTypeTopLevel(value, '|') {
		alternative = strings.TrimSpace(alternative)
		if name := adminTypeStringLiteral(alternative); name != "" {
			result.names = appendUniqueDynamicNames(result.names, name)
			continue
		}
		switch alternative {
		case "null", "undefined", "false", "never":
			continue
		default:
			return dynamicComponentNames{}
		}
	}
	return result
}

func mergeDynamicComponentNames(
	left,
	right dynamicComponentNames,
) dynamicComponentNames {
	return dynamicComponentNames{
		names: appendUniqueDynamicNames(
			append([]string(nil), left.names...), right.names...,
		),
		found:    left.found || right.found,
		complete: left.found && right.found && left.complete && right.complete,
	}
}

func appendUniqueDynamicNames(values []string, additions ...string) []string {
	seen := make(map[string]bool, len(values)+len(additions))
	for _, value := range values {
		seen[value] = true
	}
	for _, addition := range additions {
		if addition == "" || seen[addition] {
			continue
		}
		seen[addition] = true
		values = append(values, addition)
	}
	return values
}
