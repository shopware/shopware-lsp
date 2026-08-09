package diagnostics

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func (p *AdminAnalyzer) twigDiagnostics(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	var diagnostics []lsp.Problem
	analysis, err := p.adminTwigDiagnosticDocument(document)
	if err != nil {
		return nil, err
	}
	directiveDiagnostics, err := p.twigDirectiveDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, directiveDiagnostics...)
	privilegeDiagnostics, err := p.twigPrivilegeDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, privilegeDiagnostics...)
	routeDiagnostics, err := p.twigModuleRouteDiagnostics(document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, routeDiagnostics...)

	// Find all html_start_tag nodes
	if err := p.findHTMLStartTags(
		ctx,
		document.SyntaxTree.Root,
		document.Text,
		analysis,
		&diagnostics,
	); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	slotBindingDiagnostics, err := p.unknownScopedSlotBindingDiagnostics(
		ctx, document, analysis,
	)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, slotBindingDiagnostics...)
	slotMemberDiagnostics, err := p.unknownScopedSlotMemberDiagnostics(
		ctx, document, analysis,
	)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, slotMemberDiagnostics...)
	vueMemberDiagnostics, err := p.unknownVueBindingMemberDiagnostics(
		ctx, document, analysis,
	)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, vueMemberDiagnostics...)
	templateMemberDiagnostics, err :=
		p.unknownTwigComponentMemberDiagnostics(ctx, document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, templateMemberDiagnostics...)
	deprecatedMemberDiagnostics, err :=
		p.deprecatedTwigComponentMemberDiagnostics(ctx, document, analysis)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, deprecatedMemberDiagnostics...)

	// Check for invalid block references in component overrides
	p.checkBlockReferences(
		document.SyntaxTree.Root, document.LineIndex, analysis, &diagnostics,
	)

	return diagnostics, nil
}

func (p *AdminAnalyzer) adminTwigDiagnosticDocument(
	document *lsp.TextDocument,
) (*adminTwigDiagnosticDocument, error) {
	templatePath, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	liveOwner, err := p.adminIndexer.GetComponentForDocument(
		templatePath, document.SyntaxTree.Root,
		document.Source, document.LineIndex,
	)
	if err != nil {
		return nil, err
	}
	result := &adminTwigDiagnosticDocument{
		templatePath: templatePath,
		liveOwner:    liveOwner,
		rootIdentifiers: admin.TwigVueExpressionRootIdentifiers(
			document.SyntaxTree.Root, document.Text,
		),
		memberAccesses: admin.TwigVueExpressionMemberAccesses(
			document.SyntaxTree.Root, document.Text,
		),
		registryReferences: admin.TwigRegistryReferences(
			document.SyntaxTree.Root,
		),
		directiveReferences: admin.TwigDirectiveReferences(
			document.SyntaxTree.Root,
		),
		components: make(map[string]adminTwigComponentResolution),
	}
	if liveOwner != nil {
		result.localIdentifiers = make(map[cst.TextRange]bool)
		for _, identifier := range result.rootIdentifiers {
			if admin.TwigVueRootIdentifierIsLocal(
				document.SyntaxTree.Root, document.Text, identifier,
			) {
				result.localIdentifiers[identifier.Range] = true
			}
		}
	}
	return result, nil
}

func (p *AdminAnalyzer) diagnosticComponentForTemplateTag(
	analysis *adminTwigDiagnosticDocument,
	name string,
) (*admin.VueComponent, bool, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if resolved, ok := analysis.components[key]; ok {
		return resolved.component, resolved.found, resolved.err
	}
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		analysis.templatePath, name, analysis.liveOwner,
	)
	analysis.components[key] = adminTwigComponentResolution{
		component: component, found: found, err: err,
	}
	return component, found, err
}

func (p *AdminAnalyzer) unknownTwigComponentMemberDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil || analysis.liveOwner == nil {
		return nil, nil
	}
	component := analysis.liveOwner
	known := make(map[string]bool)
	var candidates []string
	addKnown := func(name string, suggest bool) {
		if name == "" || known[name] {
			return
		}
		known[name] = true
		if suggest {
			candidates = append(candidates, name)
		}
	}
	for _, member := range component.TemplateMembers() {
		addKnown(member.Name, !component.OpenRuntimeMembers)
	}
	for _, member := range admin.VueBuiltinMembers() {
		addKnown(member.Name, true)
	}
	for _, global := range admin.VueTemplateGlobals() {
		addKnown(global.Name, true)
	}

	var diagnostics []lsp.Problem
	seen := make(map[cst.TextRange]bool)
	for _, identifier := range analysis.rootIdentifiers {
		if ctx.Err() != nil {
			return nil, nil
		}
		if known[identifier.Name] || seen[identifier.Range] ||
			analysis.localIdentifiers[identifier.Range] {
			continue
		}
		if node := document.SyntaxTree.Root.NodeAtOffset(
			identifier.Range.Start,
		); node != nil {
			if _, _, blockLocal := admin.TwigBlockScopeMemberAt(
				*component, node, identifier.Name,
			); blockLocal {
				continue
			}
		}
		suggestions := adminNearbySuggestions(identifier.Name, candidates)
		// Runtime mixins and application plugins can add arbitrary template
		// globals. Report only a close misspelling of a statically known name.
		if len(suggestions) == 0 {
			continue
		}
		seen[identifier.Range] = true
		diagnostics = append(diagnostics, lsp.Problem{
			Range: identifier.Range,
			Message: fmt.Sprintf(
				"Unknown Administration component template member '%s'",
				identifier.Name,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.unknown-template-member",
			Payload: map[string]any{
				"memberName":  identifier.Name,
				"suggestions": suggestions,
			},
		})
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) deprecatedInstanceMemberDiagnostics(
	document *lsp.TextDocument,
	analysis *admin.JavaScriptDocumentAnalysis,
) []lsp.Problem {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil
	}
	components, err := p.adminIndexer.GetComponentsByDefinitionPath(path)
	if err != nil || len(components) == 0 {
		return nil
	}
	var diagnostics []lsp.Problem
	seen := make(map[cst.TextRange]bool)
	for _, expression := range analysis.Nodes(jssyntax.JsMemberExpression) {
		name, matched := jsquery.ThisMember(expression)
		if !matched || name == "" {
			continue
		}
		nameNode := jsquery.ThisMemberNameNode(expression)
		if nameNode == nil {
			continue
		}
		rangeValue := nameNode.RangeTrimmedTrivia()
		if seen[rangeValue] {
			continue
		}
		message := commonDeprecatedAdminMember(components, name)
		if message == "" {
			continue
		}
		seen[rangeValue] = true
		diagnostics = append(diagnostics, deprecatedAdminMemberProblem(
			name, message, rangeValue,
		))
	}
	return diagnostics
}

func (p *AdminAnalyzer) deprecatedTwigComponentMemberDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil || analysis.liveOwner == nil {
		return nil, nil
	}
	component := analysis.liveOwner
	var diagnostics []lsp.Problem
	for _, identifier := range analysis.rootIdentifiers {
		if ctx.Err() != nil {
			return nil, nil
		}
		if analysis.localIdentifiers[identifier.Range] {
			continue
		}
		member, found := component.TemplateMember(identifier.Name)
		if !found || member.Deprecated == "" {
			continue
		}
		diagnostics = append(diagnostics, deprecatedAdminMemberProblem(
			member.Name, member.Deprecated, identifier.Range,
		))
	}
	return diagnostics, nil
}

func commonDeprecatedAdminMember(
	components []admin.VueComponent,
	name string,
) string {
	var messages []string
	seen := make(map[string]bool)
	for _, component := range components {
		member, found := component.TemplateMember(name)
		if !found || member.Deprecated == "" {
			return ""
		}
		if !seen[member.Deprecated] {
			seen[member.Deprecated] = true
			messages = append(messages, member.Deprecated)
		}
	}
	return strings.Join(messages, " / ")
}

func deprecatedAdminMemberProblem(
	name,
	deprecation string,
	rangeValue cst.TextRange,
) lsp.Problem {
	return lsp.Problem{
		Range: rangeValue,
		Message: fmt.Sprintf(
			"Administration component member '%s' is deprecated: %s",
			name, deprecation,
		),
		Source:   "shopware-lsp",
		Severity: protocol.DiagnosticSeverityHint,
		ID:       "admin.component.deprecated-member",
		Tags: []protocol.DiagnosticTag{
			protocol.DiagnosticTagDeprecated,
		},
	}
}

func (p *AdminAnalyzer) unknownScopedSlotBindingDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil {
		return nil, nil
	}
	root := document.SyntaxTree.Root
	templatePath := analysis.templatePath
	liveOwner := analysis.liveOwner
	var diagnostics []lsp.Problem
	seenScopes := make(map[cst.TextRange]bool)
	for _, attributeNode := range twigquery.Nodes(
		root, twigsyntax.HtmlAttribute,
	) {
		if err := ctx.Err(); err != nil {
			return nil, nil
		}
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok || admin.NormalizeSlotName(
			twigquery.HTMLAttributeName(attributeNode),
		) == "" {
			continue
		}
		value, found := attribute.Value()
		if !found {
			continue
		}
		inner, found := value.GetInner()
		if !found {
			continue
		}
		scope, found := admin.TwigScopedSlotAtOffset(
			root, inner.Syntax().Range().Start,
		)
		if !found || seenScopes[scope.BindingRange] {
			continue
		}
		seenScopes[scope.BindingRange] = true
		resolved, resolveErr := p.adminIndexer.ResolveTwigScopedSlotForOwner(
			root, scope.BindingRange.Start, templatePath, liveOwner,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if resolved == nil || !resolved.Slot.MembersComplete {
			continue
		}
		memberNames := make([]string, 0, len(resolved.Slot.Members))
		for _, member := range resolved.Slot.Members {
			memberNames = append(memberNames, member.Name)
		}
		componentName := resolved.Scope.ComponentName
		if componentName == "" {
			componentName = resolved.Component.Name
		}
		for _, binding := range scope.Bindings {
			if binding.WholeObject || binding.MemberName == "" ||
				binding.MemberRange.Len() == 0 {
				continue
			}
			if _, memberFound := resolved.Slot.Member(
				binding.MemberName,
			); memberFound {
				continue
			}
			diagnostics = append(diagnostics, lsp.Problem{
				Range: binding.MemberRange,
				Message: fmt.Sprintf(
					"Unknown scoped-slot prop '%s' on '%s'",
					binding.MemberName, resolved.QualifiedName(),
				),
				Source:   "shopware",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       "admin.component.unknown-slot-prop",
				Payload: map[string]any{
					"componentName":  componentName,
					"componentNames": resolved.ComponentNames(),
					"slotName":       resolved.Scope.SlotName,
					"memberName":     binding.MemberName,
					"suggestions": suggestion.Similar(
						binding.MemberName, memberNames,
					),
				},
			})
		}
	}
	return diagnostics, nil
}

func (p *AdminAnalyzer) twigDirectiveDiagnostics(
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil || len(analysis.directiveReferences) == 0 {
		return nil, nil
	}
	directives, err := p.adminIndexer.GetAllDirectivesForTemplate(
		analysis.templatePath,
	)
	if err != nil || len(directives) == 0 {
		return nil, err
	}
	known := make(map[string]bool, len(directives))
	names := make([]string, 0, len(directives))
	for _, directive := range directives {
		if directive.Name == "" || known[directive.Name] {
			continue
		}
		known[directive.Name] = true
		names = append(names, directive.Name)
	}
	var result []lsp.Problem
	for _, reference := range analysis.directiveReferences {
		if known[reference.Name] {
			continue
		}
		suggestions := adminDirectiveSuggestions(reference.Name, names)
		// Custom Vue directives may be installed by third-party code outside the
		// Shopware registry. Report only likely misspellings of an indexed name.
		if len(suggestions) == 0 {
			continue
		}
		result = append(result, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Administration Vue directive 'v-%s' is not registered",
				reference.Name,
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.directive.not-found",
			Payload: map[string]any{
				"directiveName": reference.Name,
				"suggestions":   suggestions,
			},
		})
	}
	return result, nil
}

func adminDirectiveSuggestions(input string, candidates []string) []string {
	return adminNearbySuggestions(input, candidates)
}

func adminNearbySuggestions(input string, candidates []string) []string {
	var nearby []string
	for _, candidate := range candidates {
		if boundedAdminEditDistance(
			strings.ToLower(input), strings.ToLower(candidate), 2,
		) <= 2 {
			nearby = append(nearby, candidate)
		}
	}
	return suggestion.Similar(input, nearby)
}

func boundedAdminEditDistance(left, right string, limit int) int {
	leftRunes, rightRunes := []rune(left), []rune(right)
	if difference := len(leftRunes) - len(rightRunes); difference > limit ||
		difference < -limit {
		return limit + 1
	}
	previous := make([]int, len(rightRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range leftRunes {
		current := make([]int, len(rightRunes)+1)
		current[0] = leftIndex + 1
		rowMinimum := current[0]
		for rightIndex, rightRune := range rightRunes {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[rightIndex+1] = min(
				current[rightIndex]+1,
				previous[rightIndex+1]+1,
				previous[rightIndex]+cost,
			)
			rowMinimum = min(rowMinimum, current[rightIndex+1])
		}
		if rowMinimum > limit {
			return limit + 1
		}
		previous = current
	}
	return previous[len(rightRunes)]
}

func (p *AdminAnalyzer) unknownVueBindingMemberDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil {
		return nil, nil
	}
	templatePath := analysis.templatePath
	root := document.SyntaxTree.Root
	liveComponent := analysis.liveOwner
	var result []lsp.Problem
	for _, access := range analysis.memberAccesses {
		if err := ctx.Err(); err != nil {
			return nil, nil
		}
		if liveComponent == nil || analysis.localIdentifiers[access.RootRange] {
			node := root.NodeAtOffset(access.MemberRange.Start)
			resolvedSlot, err :=
				p.adminIndexer.ResolveTwigScopedSlotMemberForOwner(
					root, node, document.Text, access.MemberRange.Start,
					templatePath, liveComponent,
				)
			if err != nil {
				return nil, err
			}
			resolved, err := p.adminIndexer.ResolveTwigVueMemberForComponent(
				root, document.Text, access.MemberRange.Start,
				templatePath, liveComponent,
			)
			if err != nil {
				return nil, err
			}
			if resolved != nil {
				if resolved.MemberFound || !resolved.ReceiverFound ||
					!resolved.MembersComplete {
					continue
				}
				if resolvedSlot != nil && resolved.Binding.ScopeRange.Len() >
					resolvedSlot.Scope.TemplateRange.Len() {
					continue
				}
				result = append(result, unknownTypedVueMemberProblem(
					access, access.QualifiedName(), "typed Vue binding",
					twigVueMemberNames(resolved.ReceiverMembers),
				))
				continue
			}
			continue
		}
		resolvedInstance, instanceErr :=
			p.adminIndexer.ResolveTwigVueInstanceMemberAccessForComponent(
				root, document.Text, access,
				templatePath, liveComponent,
			)
		if instanceErr != nil {
			return nil, instanceErr
		}
		if resolvedInstance == nil || resolvedInstance.MemberFound ||
			!resolvedInstance.ReceiverFound ||
			!resolvedInstance.MembersComplete {
			continue
		}
		result = append(result, unknownTypedVueMemberProblem(
			access, resolvedInstance.QualifiedName(),
			"typed component member",
			twigVueMemberNames(resolvedInstance.ReceiverMembers),
		))
	}
	return result, nil
}

func twigVueMemberNames(members []admin.TwigVueMember) []string {
	result := make([]string, 0, len(members))
	for _, member := range members {
		result = append(result, member.Name)
	}
	return result
}

func unknownTypedVueMemberProblem(
	access admin.TwigVueMemberAccess,
	qualified,
	receiverKind string,
	members []string,
) lsp.Problem {
	return lsp.Problem{
		Range: access.MemberRange,
		Message: fmt.Sprintf(
			"Unknown property '%s' on %s '%s'",
			access.Member, receiverKind, qualified,
		),
		Source:   "shopware",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "admin.component.unknown-vue-member",
		Payload: map[string]any{
			"bindingName": access.Root,
			"memberName":  access.Member,
			"suggestions": suggestion.Similar(access.Member, members),
		},
	}
}

func (p *AdminAnalyzer) unknownScopedSlotMemberDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
	analysis *adminTwigDiagnosticDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.adminIndexer == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		analysis == nil {
		return nil, nil
	}
	root := document.SyntaxTree.Root
	templatePath := analysis.templatePath
	liveOwner := analysis.liveOwner
	var diagnostics []lsp.Problem
	for _, access := range analysis.memberAccesses {
		if err := ctx.Err(); err != nil {
			return nil, nil
		}
		node := root.NodeAtOffset(access.MemberRange.Start)
		resolved, err := p.adminIndexer.ResolveTwigScopedSlotMemberForOwner(
			root, node, document.Text, access.MemberRange.Start,
			templatePath, liveOwner,
		)
		if err != nil {
			return nil, err
		}
		if resolved == nil || resolved.MemberFound ||
			!resolved.ReceiverFound || !resolved.MembersComplete {
			continue
		}
		if vueBinding, found := admin.TwigVueBindingAtOffset(
			root, document.Text, access.RootRange.Start,
		); found && vueBinding != nil &&
			vueBinding.ScopeRange.Len() <= resolved.Scope.TemplateRange.Len() {
			continue
		}
		memberNames := make([]string, 0, len(resolved.Slot.Members))
		for _, member := range resolved.Slot.Members {
			memberNames = append(memberNames, member.Name)
		}
		componentName := resolved.Scope.ComponentName
		if componentName == "" {
			componentName = resolved.Component.Name
		}
		diagnostics = append(diagnostics, lsp.Problem{
			Range: access.MemberRange,
			Message: fmt.Sprintf(
				"Unknown scoped-slot prop '%s' on '%s'",
				access.Member,
				resolved.QualifiedName(),
			),
			Source:   "shopware",
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "admin.component.unknown-slot-prop",
			Payload: map[string]any{
				"componentName":  componentName,
				"componentNames": resolved.ComponentNames(),
				"slotName":       resolved.Scope.SlotName,
				"memberName":     access.Member,
				"suggestions": suggestion.Similar(
					access.Member, memberNames,
				),
			},
		})
	}
	return diagnostics, nil
}
