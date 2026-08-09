package admin

import (
	"sort"
	"strings"

	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func (idx *AdminComponentIndexer) ResolveTwigScopedSlot(
	root *twigsyntax.Node,
	offset uint32,
	templatePath ...string,
) (*ResolvedTwigScopedSlot, error) {
	resolved, err := idx.resolveTwigScopedSlots(
		root, offset, firstOptionalString(templatePath), nil,
	)
	if err != nil || len(resolved) == 0 {
		return nil, err
	}
	return &resolved[len(resolved)-1], nil
}

func (idx *AdminComponentIndexer) ResolveTwigScopedSlotForOwner(
	root *twigsyntax.Node,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigScopedSlot, error) {
	resolved, err := idx.resolveTwigScopedSlots(
		root, offset, templatePath, owner,
	)
	if err != nil || len(resolved) == 0 {
		return nil, err
	}
	return &resolved[len(resolved)-1], nil
}

// ResolveTwigScopedSlots resolves every lexical v-slot scope visible at the
// offset, ordered from the outermost scope to the innermost one. Consumers
// that build a lexical environment need all of them; member access keeps using
// the innermost matching binding through ResolveTwigScopedSlotBinding.
func (idx *AdminComponentIndexer) ResolveTwigScopedSlots(
	root *twigsyntax.Node,
	offset uint32,
	templatePath ...string,
) ([]ResolvedTwigScopedSlot, error) {
	return idx.resolveTwigScopedSlots(
		root, offset, firstOptionalString(templatePath), nil,
	)
}

func (idx *AdminComponentIndexer) ResolveTwigScopedSlotsForOwner(
	root *twigsyntax.Node,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) ([]ResolvedTwigScopedSlot, error) {
	return idx.resolveTwigScopedSlots(root, offset, templatePath, owner)
}

func (idx *AdminComponentIndexer) resolveTwigScopedSlots(
	root *twigsyntax.Node,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) ([]ResolvedTwigScopedSlot, error) {
	if idx == nil || root == nil {
		return nil, nil
	}
	scopes := TwigScopedSlotsAtOffset(root, offset)
	resolved := make([]ResolvedTwigScopedSlot, 0, len(scopes))
	for _, scope := range scopes {
		current, err := idx.resolveTwigScopedSlot(
			root, scope, templatePath, owner,
		)
		if err != nil {
			return nil, err
		}
		if current != nil {
			resolved = append(resolved, *current)
		}
	}
	return resolved, nil
}

func (idx *AdminComponentIndexer) resolveTwigScopedSlot(
	root *twigsyntax.Node,
	scope TwigScopedSlot,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigScopedSlot, error) {
	if startTag := TwigScopedSlotStartingTag(root, scope); startTag != nil {
		components, complete, resolveErr :=
			idx.ResolveTwigSlotConsumerComponents(
				templatePath, startTag, owner,
			)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if complete && len(components) > 0 {
			return resolveTwigScopedSlotContracts(scope, components, complete), nil
		}
		if _, dynamic := TwigDynamicComponentSelector(
			TwigSlotOwnerStartingTag(startTag),
		); dynamic {
			// Runtime-open selectors retain lexical locals, but never borrow a
			// payload shape from only the statically visible branches.
			return &ResolvedTwigScopedSlot{
				Component: VueComponent{Name: scope.ComponentName},
				Slot: VueComponentSlot{
					Name: scope.SlotName, MembersComplete: false,
				},
				Scope: scope,
			}, nil
		}
	}
	component, err := idx.GetEffectiveComponent(scope.ComponentName)
	if err != nil {
		return nil, err
	}
	if component == nil {
		return &ResolvedTwigScopedSlot{
			Component: VueComponent{Name: scope.ComponentName},
			Slot: VueComponentSlot{
				Name: scope.SlotName, MembersComplete: false,
			},
			Scope: scope,
		}, nil
	}
	if slot, exists := component.ComponentSlot(scope.SlotName); exists {
		return &ResolvedTwigScopedSlot{
			Component: *component, Slot: slot, Scope: scope,
			Contracts: []ResolvedTwigSlotContract{{
				Component: *component, Slot: slot,
			}},
			ContractsComplete: true,
		}, nil
	}
	return &ResolvedTwigScopedSlot{
		Component: *component,
		Slot:      VueComponentSlot{Name: scope.SlotName}, Scope: scope,
	}, nil
}

func resolveTwigScopedSlotContracts(
	scope TwigScopedSlot,
	components []VueComponent,
	selectorComplete bool,
) *ResolvedTwigScopedSlot {
	contracts := make([]ResolvedTwigSlotContract, 0, len(components))
	allFound := selectorComplete
	for _, component := range components {
		slot, found := component.ComponentSlot(scope.SlotName)
		if !found {
			allFound = false
			continue
		}
		contracts = append(contracts, ResolvedTwigSlotContract{
			Component: component, Slot: slot,
		})
	}
	component := VueComponent{}
	if len(components) == 1 {
		component = components[0]
	} else {
		names := make([]string, 0, len(components))
		for _, candidate := range components {
			names = append(names, candidate.Name)
		}
		component.Name = strings.Join(names, " | ")
	}
	result := &ResolvedTwigScopedSlot{
		Component: component,
		Slot: VueComponentSlot{
			Name: scope.SlotName, MembersComplete: false,
		},
		Scope: scope, Contracts: contracts,
		ContractsComplete: allFound && len(contracts) == len(components),
	}
	if !result.ContractsComplete {
		return result
	}
	if len(contracts) == 1 {
		result.Slot = contracts[0].Slot
		return result
	}
	result.Slot = commonTwigScopedSlotContract(scope.SlotName, contracts)
	return result
}

func commonTwigScopedSlotContract(
	name string,
	contracts []ResolvedTwigSlotContract,
) VueComponentSlot {
	result := VueComponentSlot{Name: name, MembersComplete: len(contracts) > 0}
	if len(contracts) == 0 {
		return result
	}
	result.FilePath = contracts[0].Slot.FilePath
	result.Line = contracts[0].Slot.Line
	payloadTypes := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		result.MembersComplete = result.MembersComplete &&
			contract.Slot.MembersComplete
		if contract.Slot.FilePath != result.FilePath ||
			contract.Slot.Line != result.Line {
			result.FilePath = ""
			result.Line = 0
		}
		if contract.Slot.PayloadType == "" {
			payloadTypes = nil
		} else if payloadTypes != nil {
			payloadTypes = append(payloadTypes, contract.Slot.PayloadType)
		}
	}
	if len(payloadTypes) == len(contracts) {
		result.PayloadType = mergeVueTypes(payloadTypes...)
	}
	for _, candidate := range contracts[0].Slot.Members {
		members := []VueComponentSlotMember{candidate}
		common := true
		for _, contract := range contracts[1:] {
			member, found := contract.Slot.Member(candidate.Name)
			if !found {
				common = false
				break
			}
			members = append(members, member)
		}
		if !common {
			continue
		}
		member := candidate
		types := make([]string, 0, len(members))
		for _, current := range members {
			if current.Type != "" {
				types = append(types, current.Type)
			}
			if current.FilePath != member.FilePath || current.Line != member.Line {
				member.FilePath = ""
				member.Line = 0
			}
		}
		if len(types) == len(members) {
			member.Type = mergeVueTypes(types...)
		} else {
			member.Type = ""
		}
		result.Members = append(result.Members, member)
	}
	return result
}

// ResolveTwigScopedSlotBinding resolves an identifier in either the binding
// declaration or the evaluated body of a scoped slot.
func (idx *AdminComponentIndexer) ResolveTwigScopedSlotBinding(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath ...string,
) (*ResolvedTwigSlotBinding, error) {
	return idx.resolveTwigScopedSlotBinding(
		root, node, content, offset, firstOptionalString(templatePath), nil,
	)
}

func (idx *AdminComponentIndexer) ResolveTwigScopedSlotBindingForOwner(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigSlotBinding, error) {
	return idx.resolveTwigScopedSlotBinding(
		root, node, content, offset, templatePath, owner,
	)
}

func (idx *AdminComponentIndexer) resolveTwigScopedSlotBinding(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigSlotBinding, error) {
	identifier, rangeValue, found := IdentifierAtOffset(content, offset)
	if !found {
		return nil, nil
	}
	scopes := TwigScopedSlotsAtOffset(root, offset)
	for scopeIndex := len(scopes) - 1; scopeIndex >= 0; scopeIndex-- {
		scope := scopes[scopeIndex]
		inBinding := scope.IsBindingOffset(offset)
		currentIdentifier := identifier
		currentRange := rangeValue
		if !inBinding {
			if !IsTwigVueExpressionAt(node, offset) {
				continue
			}
			currentIdentifier, currentRange, found =
				ExpressionRootIdentifierAtOffset(content, offset)
			if !found {
				continue
			}
		}
		for _, binding := range scope.Bindings {
			matched := currentIdentifier == binding.LocalName
			if inBinding && currentIdentifier == binding.MemberName {
				matched = true
			}
			if !matched {
				continue
			}
			resolved, err := idx.resolveTwigScopedSlot(
				root, scope, templatePath, owner,
			)
			if err != nil || resolved == nil {
				return nil, err
			}
			member, memberFound := resolved.Slot.Member(binding.MemberName)
			members := resolvedTwigSlotContractMembers(
				resolved.Contracts, binding.MemberName,
			)
			return &ResolvedTwigSlotBinding{
				ResolvedTwigScopedSlot: *resolved,
				Binding:                binding, Member: member, Members: members,
				MemberFound: memberFound,
				Identifier:  currentIdentifier, Range: currentRange,
			}, nil
		}
	}
	return nil, nil
}

// ResolveTwigScopedSlotMember resolves a direct property accessed through a
// whole-object scoped-slot local. It returns a result even when MemberFound is
// false so callers can suppress unrelated component-scope fallbacks.
func (idx *AdminComponentIndexer) ResolveTwigScopedSlotMember(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath ...string,
) (*ResolvedTwigSlotMember, error) {
	return idx.resolveTwigScopedSlotMember(
		root, node, content, offset, firstOptionalString(templatePath), nil,
	)
}

func (idx *AdminComponentIndexer) ResolveTwigScopedSlotMemberForOwner(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigSlotMember, error) {
	return idx.resolveTwigScopedSlotMember(
		root, node, content, offset, templatePath, owner,
	)
}

func (idx *AdminComponentIndexer) resolveTwigScopedSlotMember(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigSlotMember, error) {
	if !IsTwigVueExpressionAt(node, offset) {
		return nil, nil
	}
	access, found := TwigVueExpressionMemberAtOffset(root, content, offset)
	if !found {
		return nil, nil
	}
	scopes := TwigScopedSlotsAtOffset(root, offset)
	for scopeIndex := len(scopes) - 1; scopeIndex >= 0; scopeIndex-- {
		scope := scopes[scopeIndex]
		for _, binding := range scope.Bindings {
			if !binding.WholeObject || binding.LocalName != access.Root {
				continue
			}
			resolved, err := idx.resolveTwigScopedSlot(
				root, scope, templatePath, owner,
			)
			if err != nil || resolved == nil {
				return nil, err
			}
			result := &ResolvedTwigSlotMember{
				ResolvedTwigScopedSlot: *resolved,
				Binding:                binding,
				Access:                 access,
				ReceiverFound:          true,
				ReceiverMembers:        slotTwigVueMembers(resolved.Slot.Members),
				MembersComplete:        resolved.Slot.MembersComplete,
				ReceiverType:           resolved.Slot.PayloadType,
			}
			contextPath := resolved.Slot.FilePath
			for _, segment := range access.Receiver {
				indexType, indexErr := idx.resolveTwigVueIndexExpressionType(
					root, content, segment, "", nil,
				)
				if indexErr != nil {
					return nil, indexErr
				}
				receiverType, receiverMembers, membersComplete,
					resolvedContext, receiverFound, resolveErr :=
					idx.resolveTwigVueReceiverSegment(
						result.ReceiverType,
						result.ReceiverMembers,
						contextPath,
						segment,
						indexType,
					)
				if resolveErr != nil {
					return nil, resolveErr
				}
				if !receiverFound {
					result.ReceiverFound = false
					result.ReceiverMembers = nil
					result.MembersComplete = false
					return result, nil
				}
				result.ReceiverType = receiverType
				result.ReceiverMembers = receiverMembers
				result.MembersComplete = membersComplete
				contextPath = resolvedContext
			}
			member, memberFound := twigVueMemberNamed(
				result.ReceiverMembers, access.Member,
			)
			result.Member = slotMemberFromTwigVue(member)
			result.MemberFound = memberFound
			if len(access.Receiver) == 0 {
				result.Members = resolvedTwigSlotContractMembers(
					resolved.Contracts, access.Member,
				)
			}
			return result, nil
		}
	}
	return nil, nil
}

func resolvedTwigSlotContractMembers(
	contracts []ResolvedTwigSlotContract,
	name string,
) []VueComponentSlotMember {
	result := make([]VueComponentSlotMember, 0, len(contracts))
	seen := make(map[VueComponentSlotMember]bool)
	for _, contract := range contracts {
		member, found := contract.Slot.Member(name)
		if !found {
			continue
		}
		if seen[member] {
			continue
		}
		seen[member] = true
		result = append(result, member)
	}
	return result
}

func slotTwigVueMembers(members []VueComponentSlotMember) []TwigVueMember {
	result := make([]TwigVueMember, 0, len(members))
	for _, member := range members {
		result = append(result, TwigVueMember{
			Name: member.Name, Type: member.Type,
			DefinitionPath: member.FilePath, DefinitionLine: member.Line,
			DefinitionRange: member.NameRange,
		})
	}
	return result
}

func slotMemberFromTwigVue(member TwigVueMember) VueComponentSlotMember {
	return VueComponentSlotMember{
		Name: member.Name, Type: member.Type,
		FilePath: member.DefinitionPath, Line: member.DefinitionLine,
		NameRange: member.DefinitionRange,
	}
}

// ResolveTwigVueBindings resolves document-local Vue template variables and
// enriches an implicit $event with the indexed component event payload type
// and declaration. v-for bindings never enter the persistent workspace index.
func (idx *AdminComponentIndexer) ResolveTwigVueBindings(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath ...string,
) ([]TwigVueBinding, error) {
	return idx.resolveTwigVueBindings(
		root, content, offset, firstOptionalString(templatePath), nil,
	)
}

// ResolveTwigVueBindingsForComponent resolves lexical bindings against a
// request-local owner component. Open Vue buffers use this path so v-for
// element inference sees unsaved props, computed values, and type imports.
func (idx *AdminComponentIndexer) ResolveTwigVueBindingsForComponent(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) ([]TwigVueBinding, error) {
	return idx.resolveTwigVueBindings(
		root, content, offset, templatePath, component,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueBindings(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) ([]TwigVueBinding, error) {
	bindings := TwigVueBindingsAtOffset(root, content, offset)
	for index := range bindings {
		bindings[index].Members = TwigVueBindingMembers(
			root, content, bindings[index],
		)
		if err := idx.enrichTwigVueBindingWithVisible(
			&bindings[index], templatePath, bindings[:index], component,
		); err != nil {
			return nil, err
		}
	}
	return bindings, nil
}

// ResolveTwigVueBinding resolves the lexical Vue variable under the cursor.
func (idx *AdminComponentIndexer) ResolveTwigVueBinding(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath ...string,
) (*TwigVueBinding, error) {
	return idx.resolveTwigVueBinding(
		root, content, offset, firstOptionalString(templatePath), nil,
	)
}

func (idx *AdminComponentIndexer) ResolveTwigVueBindingForComponent(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*TwigVueBinding, error) {
	return idx.resolveTwigVueBinding(
		root, content, offset, templatePath, component,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueBinding(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*TwigVueBinding, error) {
	target, found := TwigVueBindingAtOffset(root, content, offset)
	if !found {
		return nil, nil
	}
	bindings, err := idx.resolveTwigVueBindings(
		root, content, offset, templatePath, component,
	)
	if err != nil {
		return nil, err
	}
	for bindingIndex := range bindings {
		if bindings[bindingIndex].sameIdentity(*target) {
			return &bindings[bindingIndex], nil
		}
	}
	return nil, nil
}

// ResolveTwigVueExpressionType resolves one complete Administration template
// expression against its lexical v-for/event/slot scope and effective
// component instance. It is intentionally limited to statically inspectable
// expressions so callers can use the result for correctness diagnostics.
func (idx *AdminComponentIndexer) ResolveTwigVueExpressionType(
	root *twigsyntax.Node,
	content []byte,
	expression string,
	offset uint32,
	templatePath string,
) (string, bool, error) {
	return idx.resolveTwigVueExpressionType(
		root, content, expression, offset, templatePath, nil,
	)
}

// ResolveTwigVueExpressionTypeForComponent is the live-document counterpart
// of ResolveTwigVueExpressionType.
func (idx *AdminComponentIndexer) ResolveTwigVueExpressionTypeForComponent(
	root *twigsyntax.Node,
	content []byte,
	expression string,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (string, bool, error) {
	return idx.resolveTwigVueExpressionType(
		root, content, expression, offset, templatePath, component,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueExpressionType(
	root *twigsyntax.Node,
	content []byte,
	expression string,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (string, bool, error) {
	if idx == nil || root == nil || strings.TrimSpace(expression) == "" {
		return "", false, nil
	}
	visible, err := idx.resolveTwigVueBindings(
		root, content, offset, templatePath, component,
	)
	if err != nil {
		return "", false, err
	}
	if component == nil && templatePath != "" {
		component, err = idx.GetComponentByTemplatePath(templatePath)
		if err != nil {
			return "", false, err
		}
	}
	if mutableVueDataExpression(expression, visible, component) {
		// An untyped Options API data seed is not an authoritative use-site
		// type once methods or watchers assign another value to it. Completion
		// can still use the indexed seed, but correctness diagnostics must stay
		// conservative without flow-sensitive narrowing.
		return "", false, nil
	}
	resolved, found, err := idx.resolveTwigVueIterableExpressionType(
		expression, visible, component,
	)
	if err != nil || !found || resolved.Type == "" {
		return "", false, err
	}
	return resolved.Type, true, nil
}

func mutableVueDataExpression(
	expression string,
	visible []TwigVueBinding,
	component *VueComponent,
) bool {
	if component == nil {
		return false
	}
	path, matched := vueStaticTemplateExpression(expression)
	if !matched || len(path) == 0 {
		return false
	}
	root := path[0].Name
	for visibleIndex := len(visible) - 1; visibleIndex >= 0; visibleIndex-- {
		if visible[visibleIndex].Name == root {
			return false
		}
	}
	member, found := component.TemplateMember(root)
	if !found || member.Kind != ComponentMemberData {
		return false
	}
	for _, assignment := range component.Assignments {
		if assignment.Target == root {
			return true
		}
	}
	return false
}

// ResolveTwigVueMember resolves the property under the cursor through the
// structural type of a lexical Vue binding. Every intermediate receiver must
// be a statically named field; unresolved chains return a handled result with
// ReceiverFound false so LSP callers do not fall back to component members.
func (idx *AdminComponentIndexer) ResolveTwigVueMember(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath ...string,
) (*ResolvedTwigVueMember, error) {
	return idx.resolveTwigVueMember(
		root, content, offset, firstOptionalString(templatePath), nil,
	)
}

// ResolveTwigVueMemberForComponent resolves a lexical member against the
// request-local owner contract of an open Vue document.
func (idx *AdminComponentIndexer) ResolveTwigVueMemberForComponent(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*ResolvedTwigVueMember, error) {
	return idx.resolveTwigVueMember(
		root, content, offset, templatePath, component,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueMember(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*ResolvedTwigVueMember, error) {
	access, found := TwigVueExpressionMemberAtOffset(root, content, offset)
	if !found {
		return nil, nil
	}
	binding, err := idx.resolveTwigVueBinding(
		root, content, access.RootRange.Start, templatePath, component,
	)
	if err != nil || binding == nil {
		return nil, err
	}
	resolved := &ResolvedTwigVueMember{
		Binding: *binding, Access: access, ReceiverFound: true,
		ReceiverType:    binding.Type,
		ReceiverMembers: append([]TwigVueMember(nil), binding.Members...),
		MembersComplete: binding.MembersComplete,
	}
	contextPath := binding.TypeContextPath
	if access.RootCalled {
		receiverType := VueCallableReturnType(binding.Type)
		if receiverType == "" {
			resolved.ReceiverFound = false
			resolved.ReceiverMembers = nil
			resolved.MembersComplete = false
			return resolved, nil
		}
		shape, resolveErr := idx.ResolveVueType(
			receiverType, contextPath, componentLiveTypeFiles(component)...,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		resolved.ReceiverType = receiverType
		resolved.ReceiverMembers = shape.Members
		resolved.MembersComplete = shape.Complete
	}
	for _, segment := range access.Receiver {
		indexType, indexErr := idx.resolveTwigVueIndexExpressionType(
			root, content, segment, templatePath, component,
		)
		if indexErr != nil {
			return nil, indexErr
		}
		receiverType, receiverMembers, membersComplete,
			resolvedContext, receiverFound, resolveErr :=
			idx.resolveTwigVueReceiverSegment(
				resolved.ReceiverType,
				resolved.ReceiverMembers,
				contextPath,
				segment,
				indexType,
				componentLiveTypeFiles(component)...,
			)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !receiverFound {
			resolved.ReceiverFound = false
			resolved.ReceiverMembers = nil
			resolved.MembersComplete = false
			return resolved, nil
		}
		resolved.ReceiverType = receiverType
		resolved.ReceiverMembers = receiverMembers
		resolved.MembersComplete = membersComplete
		contextPath = resolvedContext
	}
	resolved.Member, resolved.MemberFound = twigVueMemberNamed(
		resolved.ReceiverMembers, access.Member,
	)
	return resolved, nil
}

// ResolveTwigVueInstanceMember resolves a property chain rooted in the
// effective component instance for a template. Lexical Vue and scoped-slot
// bindings win before component members, matching Vue's runtime scoping.
func (idx *AdminComponentIndexer) ResolveTwigVueInstanceMember(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
) (*ResolvedTwigVueInstanceMember, error) {
	return idx.resolveTwigVueInstanceMember(
		root, content, offset, templatePath, nil,
	)
}

// ResolveTwigVueInstanceMemberForComponent resolves an instance member using
// the request-local component assembled from an open Vue SFC.
func (idx *AdminComponentIndexer) ResolveTwigVueInstanceMemberForComponent(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*ResolvedTwigVueInstanceMember, error) {
	return idx.resolveTwigVueInstanceMember(
		root, content, offset, templatePath, component,
	)
}

// ResolveTwigVueInstanceMemberAccessForComponent resolves a pre-parsed member
// access after the caller has established that its root is not a lexical Vue
// or scoped-slot binding. Batch diagnostics use this to avoid repeating the
// same syntax lookup for every component-instance access in a document.
func (idx *AdminComponentIndexer) ResolveTwigVueInstanceMemberAccessForComponent(
	root *twigsyntax.Node,
	content []byte,
	access TwigVueMemberAccess,
	templatePath string,
	component *VueComponent,
) (*ResolvedTwigVueInstanceMember, error) {
	return idx.resolveTwigVueInstanceMemberAccess(
		root, content, access, templatePath, component, false,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueInstanceMember(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*ResolvedTwigVueInstanceMember, error) {
	if idx == nil || root == nil || templatePath == "" {
		return nil, nil
	}
	access, found := TwigVueExpressionMemberAtOffset(root, content, offset)
	if !found {
		return nil, nil
	}
	return idx.resolveTwigVueInstanceMemberAccess(
		root, content, access, templatePath, component, true,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueInstanceMemberAccess(
	root *twigsyntax.Node,
	content []byte,
	access TwigVueMemberAccess,
	templatePath string,
	component *VueComponent,
	checkLexicalScope bool,
) (*ResolvedTwigVueInstanceMember, error) {
	if checkLexicalScope {
		if binding, bindingFound := TwigVueBindingAtOffset(
			root, content, access.RootRange.Start,
		); bindingFound && binding != nil {
			return nil, nil
		}
		if scope, scopeFound := TwigScopedSlotAtOffset(
			root, access.RootRange.Start,
		); scopeFound {
			for _, binding := range scope.Bindings {
				if binding.LocalName == access.Root {
					return nil, nil
				}
			}
		}
	}
	if component == nil {
		var err error
		component, err = idx.GetComponentByTemplatePath(templatePath)
		if err != nil || component == nil {
			return nil, err
		}
	}
	rootMember, rootFound := component.TemplateMember(access.Root)
	if !rootFound {
		rootMember, rootFound = VueBuiltinMember(access.Root)
	}
	if !rootFound {
		return nil, nil
	}
	resolved := &ResolvedTwigVueInstanceMember{
		Component: *component, RootMember: rootMember, Access: access,
		ReceiverFound: true, ReceiverType: rootMember.Type,
	}
	contextPath := rootMember.TypeContextPath
	if contextPath == "" {
		contextPath = rootMember.FilePath
	}
	if contextPath == "" {
		contextPath = component.DefinitionPath
	}
	if contextPath == "" {
		contextPath = component.FilePath
	}
	rootType, callable := twigVueReceiverType(
		rootMember.Type, access.RootCalled,
	)
	if !callable {
		resolved.ReceiverFound = false
		return resolved, nil
	}
	shape, resolveErr := idx.ResolveVueType(
		rootType, contextPath, componentLiveTypeFiles(component)...,
	)
	if resolveErr != nil {
		return nil, resolveErr
	}
	resolved.ReceiverType = rootType
	resolved.ReceiverMembers = shape.Members
	resolved.MembersComplete = shape.Complete && !rootMember.OpenRuntimeShape
	openRuntimeShape := rootMember.OpenRuntimeShape
	indexedRoot := len(access.Receiver) > 0 && access.Receiver[0].Indexed
	if rootMember.Type == "" ||
		len(shape.Members) == 0 && !shape.Complete && !indexedRoot {
		resolved.ReceiverFound = false
		return resolved, nil
	}
	for _, segment := range access.Receiver {
		indexType, indexErr := idx.resolveTwigVueIndexExpressionType(
			root, content, segment, templatePath, component,
		)
		if indexErr != nil {
			return nil, indexErr
		}
		receiverType, receiverMembers, membersComplete,
			resolvedContext, receiverFound, resolveErr :=
			idx.resolveTwigVueReceiverSegment(
				resolved.ReceiverType,
				resolved.ReceiverMembers,
				contextPath,
				segment,
				indexType,
				componentLiveTypeFiles(component)...,
			)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !receiverFound {
			resolved.ReceiverFound = false
			resolved.ReceiverMembers = nil
			resolved.MembersComplete = false
			return resolved, nil
		}
		resolved.ReceiverType = receiverType
		resolved.ReceiverMembers = receiverMembers
		resolved.MembersComplete = membersComplete && !openRuntimeShape
		contextPath = resolvedContext
	}
	resolved.Member, resolved.MemberFound = twigVueMemberNamed(
		resolved.ReceiverMembers, access.Member,
	)
	return resolved, nil
}

func twigVueReceiverType(memberType string, called bool) (string, bool) {
	if !called {
		return memberType, true
	}
	returnType := VueCallableReturnType(memberType)
	return returnType, returnType != ""
}

func (idx *AdminComponentIndexer) resolveTwigVueReceiverSegment(
	receiverType string,
	receiverMembers []TwigVueMember,
	contextPath string,
	segment TwigVueMemberSegment,
	indexType string,
	liveFiles ...AdminTypeFile,
) (string, []TwigVueMember, bool, string, bool, error) {
	if segment.Indexed {
		indexedReceiverType := receiverType
		if segment.Optional {
			indexedReceiverType = withoutVueNullishType(indexedReceiverType)
		}
		indexedType, indexedContext, found, err :=
			idx.resolveVueIndexedAccessTypeWithIndexType(
				indexedReceiverType,
				segment.IndexExpression,
				indexType,
				contextPath,
				liveFiles...,
			)
		if err != nil || !found {
			return "", nil, false, contextPath, false, err
		}
		if indexedContext != "" {
			contextPath = indexedContext
		}
		shape, err := idx.ResolveVueType(
			indexedType, contextPath, liveFiles...,
		)
		if err != nil {
			return "", nil, false, contextPath, false, err
		}
		return indexedType, shape.Members, shape.Complete,
			contextPath, true, nil
	}
	member, found := twigVueMemberNamed(receiverMembers, segment.Name)
	if !found {
		return "", nil, false, contextPath, false, nil
	}
	if member.DefinitionPath != "" {
		contextPath = member.DefinitionPath
	}
	nextType, callable := twigVueReceiverType(member.Type, segment.Called)
	if !callable {
		return "", nil, false, contextPath, false, nil
	}
	shape, err := idx.ResolveVueType(nextType, contextPath, liveFiles...)
	if err != nil {
		return "", nil, false, contextPath, false, err
	}
	return nextType, shape.Members, shape.Complete, contextPath, true, nil
}

func (idx *AdminComponentIndexer) resolveTwigVueIndexExpressionType(
	root *twigsyntax.Node,
	content []byte,
	segment TwigVueMemberSegment,
	templatePath string,
	component *VueComponent,
) (string, error) {
	if !segment.Indexed {
		return "", nil
	}
	expression := unwrapVueExpressionParentheses(
		strings.TrimSpace(segment.IndexExpression),
	)
	if expression == "" {
		return "", nil
	}
	if staticType := vueStaticIndexExpressionType(expression); staticType != "" {
		return staticType, nil
	}
	if component == nil && templatePath != "" {
		resolvedComponent, err := idx.GetComponentByTemplatePath(templatePath)
		if err != nil {
			return "", err
		}
		component = resolvedComponent
	}
	bindings, err := idx.resolveTwigVueBindings(
		root, content, segment.IndexRange.Start, templatePath, component,
	)
	if err != nil {
		return "", err
	}
	known := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		if binding.Name != "" && binding.Type != "" {
			known[binding.Name] = binding.Type
		}
	}
	if component != nil {
		for _, member := range component.TemplateMembers() {
			if member.Name != "" && member.Type != "" {
				known[member.Name] = member.Type
			}
		}
	}
	var resolve func(string) string
	resolve = func(value string) string {
		value = unwrapVueExpressionParentheses(strings.TrimSpace(value))
		if value == "" {
			return ""
		}
		if staticType := vueStaticIndexExpressionType(value); staticType != "" {
			return staticType
		}
		if isSlotIdentifier(value) {
			return known[value]
		}
		if left, right, split := splitVueTopLevelOperator(value, "+"); split {
			leftType := withoutVueNullishType(resolve(left))
			rightType := withoutVueNullishType(resolve(right))
			if leftType == "number" && rightType == "number" {
				return "number"
			}
			if leftType == "string" || rightType == "string" {
				return "string"
			}
		}
		return ""
	}
	if resolved := resolve(expression); resolved != "" {
		return resolved, nil
	}
	if segment.IndexRange.Len() == 0 {
		return "", nil
	}
	memberOffset := segment.IndexRange.End
	if memberOffset > segment.IndexRange.Start {
		memberOffset--
	}
	resolvedBinding, err := idx.resolveTwigVueMember(
		root, content, memberOffset, templatePath, component,
	)
	if err != nil {
		return "", err
	}
	if resolvedBinding != nil && resolvedBinding.MemberFound {
		return resolvedBinding.Member.Type, nil
	}
	if component == nil || templatePath == "" {
		return "", nil
	}
	resolvedInstance, err := idx.resolveTwigVueInstanceMember(
		root, content, memberOffset, templatePath, component,
	)
	if err != nil {
		return "", err
	}
	if resolvedInstance != nil && resolvedInstance.MemberFound {
		return resolvedInstance.Member.Type, nil
	}
	return "", nil
}

func twigVueMemberNamed(
	members []TwigVueMember,
	name string,
) (TwigVueMember, bool) {
	for _, member := range members {
		if member.Name == name {
			return member, true
		}
	}
	return TwigVueMember{}, false
}

func (idx *AdminComponentIndexer) enrichTwigVueBindingWithVisible(
	binding *TwigVueBinding,
	templatePath string,
	visible []TwigVueBinding,
	component *VueComponent,
) error {
	if binding == nil {
		return nil
	}
	if binding.Kind == TwigVueBindingFor {
		return idx.enrichTwigVueForBinding(
			binding, templatePath, visible, component,
		)
	}
	if binding.Kind != TwigVueBindingEvent {
		return nil
	}
	if idx == nil || binding.ComponentName == "" {
		binding.Type = nativeEventPayloadType(binding.EventName)
		return nil
	}
	component, err := idx.GetEffectiveComponent(binding.ComponentName)
	if err != nil {
		return err
	}
	if component == nil {
		binding.Type = nativeEventPayloadType(binding.EventName)
		return nil
	}
	event, found := component.ComponentEvent(binding.EventName)
	if !found {
		binding.Type = nativeEventPayloadType(binding.EventName)
		return nil
	}
	binding.Type = eventPayloadType(event.Type)
	binding.DefinitionPath = event.FilePath
	if binding.DefinitionPath == "" {
		binding.DefinitionPath = component.DefinitionPath
	}
	if binding.DefinitionPath == "" {
		binding.DefinitionPath = component.FilePath
	}
	binding.DefinitionLine = event.Line
	return nil
}

func (idx *AdminComponentIndexer) enrichTwigVueForBinding(
	binding *TwigVueBinding,
	templatePath string,
	visible []TwigVueBinding,
	component *VueComponent,
) error {
	if binding == nil || templatePath == "" {
		return nil
	}
	if component == nil {
		var err error
		component, err = idx.GetComponentByTemplatePath(templatePath)
		if err != nil {
			return err
		}
	}
	resolved, found, err := idx.resolveTwigVueIterableExpressionType(
		binding.Iterable, visible, component,
	)
	if err != nil || !found || resolved.Type == "" {
		return err
	}
	iterableType := resolved.Type
	typeContextPath := resolved.ContextPath
	bindingType := VueIterableBindingType(iterableType, binding.Ordinal)
	shape, resolveErr := idx.ResolveVueType(
		bindingType, typeContextPath, componentLiveTypeFiles(component)...,
	)
	if resolveErr != nil {
		return resolveErr
	}
	membersComplete := shape.Complete &&
		strings.TrimSpace(bindingType) != "unknown" &&
		strings.TrimSpace(bindingType) != "any" &&
		!resolved.OpenRuntimeShape
	if binding.Ordinal == 0 {
		shape.Members = mergeTwigVueMembers(
			shape.Members,
			componentIterableElementMembers(binding.Iterable, component),
		)
	}
	if membersComplete {
		binding.Members = mergeKnownTwigVueMembers(
			shape.Members, binding.Members,
		)
	} else {
		binding.Members = mergeTwigVueMembers(
			shape.Members, binding.Members,
		)
	}
	binding.MembersComplete = membersComplete
	binding.TypeContextPath = typeContextPath
	binding.Type = bindingType
	return nil
}

func componentIterableElementMembers(
	expression string,
	component *VueComponent,
) []TwigVueMember {
	if component == nil {
		return nil
	}
	path, matched := vueStaticTemplateExpression(expression)
	if !matched || len(path) != 1 || path[0].Called || path[0].Optional {
		return nil
	}
	member, found := component.TemplateMember(path[0].Name)
	if !found || len(member.ElementMembers) == 0 {
		return nil
	}
	result := make([]TwigVueMember, 0, len(member.ElementMembers))
	for _, elementMember := range member.ElementMembers {
		result = append(result, TwigVueMember{
			Name: elementMember.Name, Type: elementMember.Type,
			DefinitionPath: elementMember.FilePath,
			DefinitionLine: elementMember.Line,
		})
	}
	return result
}

func (idx *AdminComponentIndexer) resolveTwigVueIterableExpressionType(
	expression string,
	visible []TwigVueBinding,
	component *VueComponent,
) (resolvedVueExpressionType, bool, error) {
	expression = trimVueSourceExpression(expression)
	if expression == "" {
		return resolvedVueExpressionType{}, false, nil
	}
	if left, right, split := splitVueTopLevelOperator(expression, "??"); split {
		leftResolved, leftFound, err := idx.resolveTwigVueIterableExpressionType(
			left, visible, component,
		)
		if err != nil {
			return resolvedVueExpressionType{}, false, err
		}
		rightResolved, rightFound, err := idx.resolveTwigVueIterableExpressionType(
			right, visible, component,
		)
		if err != nil {
			return resolvedVueExpressionType{}, false, err
		}
		result := leftResolved
		result.Type = mergeVueNullishTypes(
			leftResolved.Type, rightResolved.Type,
		)
		if result.ContextPath == "" {
			result.ContextPath = rightResolved.ContextPath
		}
		result.OpenRuntimeShape = leftResolved.OpenRuntimeShape ||
			rightResolved.OpenRuntimeShape
		return result, (leftFound || rightFound) && result.Type != "", nil
	}
	contextPath := ""
	if component != nil {
		contextPath = component.DefinitionPath
		if contextPath == "" {
			contextPath = component.FilePath
		}
	}
	if resolved, found, err := idx.resolveVueObjectTransformExpressionType(
		expression,
		contextPath,
		func(argument string) (resolvedVueExpressionType, bool, error) {
			return idx.resolveTwigVueIterableExpressionType(
				argument, visible, component,
			)
		},
	); err != nil || found {
		return resolved, found, err
	}
	if literalType := vueStaticLiteralType(expression); literalType != "" {
		return resolvedVueExpressionType{
			Type: literalType, ContextPath: contextPath,
		}, true, nil
	}
	if literalType := vueExpressionTextType(expression, nil); literalType != "" {
		return resolvedVueExpressionType{
			Type: literalType, ContextPath: contextPath,
		}, true, nil
	}
	iterablePath, matched := vueStaticTemplateExpression(expression)
	if !matched || len(iterablePath) == 0 {
		return resolvedVueExpressionType{}, false, nil
	}
	var rootType, rootContext string
	rootOpenRuntimeShape := false
	for visibleIndex := len(visible) - 1; visibleIndex >= 0; visibleIndex-- {
		candidate := visible[visibleIndex]
		if candidate.Name != iterablePath[0].Name {
			continue
		}
		rootType = candidate.Type
		rootContext = candidate.TypeContextPath
		break
	}
	if rootType == "" && component != nil {
		member, found := component.TemplateMember(iterablePath[0].Name)
		if !found {
			member, found = VueBuiltinMember(iterablePath[0].Name)
		}
		if !found {
			return resolvedVueExpressionType{}, false, nil
		}
		rootType = member.Type
		rootContext = componentMemberTypeContext(member, *component)
		rootOpenRuntimeShape = member.OpenRuntimeShape
	}
	if rootType == "" {
		return resolvedVueExpressionType{}, false, nil
	}
	if iterablePath[0].Called {
		rootType = VueCallableReturnType(rootType)
		if rootType == "" {
			return resolvedVueExpressionType{}, false, nil
		}
	}
	resolved, found, err := idx.resolveVueStaticTypeChainWithOptional(
		rootType, rootContext, iterablePath[1:], iterablePath[0].Optional,
		componentLiveTypeFiles(component)...,
	)
	resolved.OpenRuntimeShape = resolved.OpenRuntimeShape || rootOpenRuntimeShape
	return resolved, found, err
}

func vueStaticTemplateExpression(
	expression string,
) ([]vueStaticExpressionSegment, bool) {
	expression = strings.TrimSpace(expression)
	if left, right, split := splitVueTopLevelOperator(expression, "??"); split &&
		unwrapVueExpressionParentheses(right) == "[]" {
		expression = left
	}
	for len(expression) >= 2 && expression[0] == '(' &&
		matchingSlotDelimiter(expression, 0, '(', ')') == len(expression)-1 {
		expression = strings.TrimSpace(expression[1 : len(expression)-1])
	}
	if expression == "" {
		return nil, false
	}
	if strings.HasPrefix(expression, "this.") {
		return vueStaticThisExpression(expression)
	}
	if !isVueIdentifierStart(expression[0]) {
		return nil, false
	}
	return vueStaticThisExpression("this." + expression)
}

func mergeTwigVueMembers(
	base,
	overlay []TwigVueMember,
) []TwigVueMember {
	result := append([]TwigVueMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index, member := range result {
		positions[member.Name] = index
	}
	for _, member := range overlay {
		if index, found := positions[member.Name]; found {
			if member.Documentation == "" {
				member.Documentation = result[index].Documentation
			}
			if member.Type == "" {
				member.Type = result[index].Type
			}
			if member.DefinitionPath == "" {
				member.DefinitionPath = result[index].DefinitionPath
				member.DefinitionLine = result[index].DefinitionLine
			}
			if member.DefinitionRange == (AdminSourceRange{}) {
				member.DefinitionRange = result[index].DefinitionRange
			}
			if len(member.NestedMembers) == 0 &&
				!member.NestedComplete {
				member.NestedMembers = result[index].NestedMembers
				member.NestedComplete = result[index].NestedComplete
			}
			result[index] = member
			continue
		}
		positions[member.Name] = len(result)
		result = append(result, member)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

func mergeKnownTwigVueMembers(
	base,
	overlay []TwigVueMember,
) []TwigVueMember {
	known := make(map[string]bool, len(base))
	for _, member := range base {
		known[member.Name] = true
	}
	filtered := make([]TwigVueMember, 0, len(overlay))
	for _, member := range overlay {
		if known[member.Name] {
			filtered = append(filtered, member)
		}
	}
	return mergeTwigVueMembers(base, filtered)
}

func firstOptionalString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// GetAllComponentNames returns all registered component names
