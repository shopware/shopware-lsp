package admin

// enrichScriptSetupTypeContracts materializes compiler-macro contracts from
// the current type index. ComponentDefinition retains the original type
// expressions, so this operation is intentionally repeatable and reflects
// edits to imported declarations without reindexing the Vue component.
func (idx *AdminComponentIndexer) enrichScriptSetupTypeContracts(
	definition *ComponentDefinition,
	liveFiles ...AdminTypeFile,
) error {
	if idx == nil || definition == nil || definition.FilePath == "" {
		return nil
	}
	defaults := scriptSetupPropDefaults(definition.ScriptSetupPropDefaults)
	resolvedProps, err := idx.resolveScriptSetupProps(
		definition,
		defaults,
		liveFiles,
	)
	if err != nil {
		return err
	}
	definition.Props = overlayScriptSetupProps(definition.Props, resolvedProps)
	applyResolvedScriptSetupDefaults(definition.Props, defaults)
	definition.Members = overlayResolvedScriptSetupPropMembers(
		definition.Members,
		resolvedProps,
	)
	definition.Members = applyScriptSetupPropBindingMembers(
		definition.Members,
		definition.Props,
		definition.ScriptSetupPropBindings,
		definition.FilePath,
	)
	if err := idx.enrichScriptSetupEvents(definition, liveFiles); err != nil {
		return err
	}
	return idx.enrichScriptSetupSlots(definition, liveFiles)
}

func scriptSetupPropDefaults(values []ScriptSetupPropDefault) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		if value.Name != "" && value.Value != "" {
			result[value.Name] = value.Value
		}
	}
	return result
}

func (idx *AdminComponentIndexer) resolveScriptSetupProps(
	definition *ComponentDefinition,
	defaults map[string]string,
	liveFiles []AdminTypeFile,
) ([]VueComponentProp, error) {
	var result []VueComponentProp
	for _, typeExpression := range definition.ScriptSetupPropTypes {
		shape, err := idx.ResolveVueType(
			typeExpression, definition.FilePath, liveFiles...,
		)
		if err != nil {
			return nil, err
		}
		for _, member := range shape.Members {
			if member.Name == "" {
				continue
			}
			filePath := member.DefinitionPath
			if filePath == "" {
				filePath = definition.FilePath
			}
			prop := VueComponentProp{
				Name: member.Name, Type: member.Type,
				Documentation: member.Documentation,
				Required:      !member.Optional,
				FilePath:      filePath, Line: member.DefinitionLine,
				NameRange: member.DefinitionRange,
			}
			if value := defaults[prop.Name]; value != "" {
				prop.Default = value
				prop.Required = false
			}
			result = overlayScriptSetupProps(
				result, []VueComponentProp{prop},
			)
		}
	}
	return result, nil
}

func applyResolvedScriptSetupDefaults(
	props []VueComponentProp,
	defaults map[string]string,
) {
	for propIndex := range props {
		prop := &props[propIndex]
		if value := defaults[prop.Name]; value != "" {
			prop.Default = value
			prop.Required = false
		}
	}
}

func (idx *AdminComponentIndexer) enrichScriptSetupEvents(
	definition *ComponentDefinition,
	liveFiles []AdminTypeFile,
) error {
	for _, typeExpression := range definition.ScriptSetupEventTypes {
		shape, err := idx.ResolveVueType(
			typeExpression, definition.FilePath, liveFiles...,
		)
		if err != nil {
			return err
		}
		for _, member := range shape.Members {
			if member.Name == "" {
				continue
			}
			filePath := member.DefinitionPath
			if filePath == "" {
				filePath = definition.FilePath
			}
			event := VueComponentEvent{
				Name: member.Name, Type: member.Type,
				Documentation: member.Documentation,
				FilePath:      filePath, Line: member.DefinitionLine,
				NameRange: member.DefinitionRange,
			}
			definition.Events = appendComponentEvent(
				definition.Events, event,
			)
			definition.Emits = appendUnique(
				definition.Emits, event.Name,
			)
		}
		callEvents, err := idx.ResolveVueEvents(
			typeExpression, definition.FilePath, liveFiles...,
		)
		if err != nil {
			return err
		}
		for _, event := range callEvents {
			definition.Events = appendComponentEvent(
				definition.Events, event,
			)
			definition.Emits = appendUnique(
				definition.Emits, event.Name,
			)
		}
	}
	return nil
}

func (idx *AdminComponentIndexer) enrichScriptSetupSlots(
	definition *ComponentDefinition,
	liveFiles []AdminTypeFile,
) error {
	for _, typeExpression := range definition.ScriptSetupSlotTypes {
		shape, err := idx.ResolveVueType(
			typeExpression, definition.FilePath, liveFiles...,
		)
		if err != nil {
			return err
		}
		var slots []VueComponentSlot
		for _, member := range shape.Members {
			slot, found, resolveErr := idx.resolveScriptSetupSlot(
				member,
				definition.FilePath,
				liveFiles,
			)
			if resolveErr != nil {
				return resolveErr
			}
			if found {
				slots = append(slots, slot)
			}
		}
		definition.Slots = overlayScriptSetupSlots(definition.Slots, slots)
	}
	return nil
}

func (idx *AdminComponentIndexer) resolveScriptSetupSlot(
	member TwigVueMember,
	definitionPath string,
	liveFiles []AdminTypeFile,
) (VueComponentSlot, bool, error) {
	if member.Name == "" {
		return VueComponentSlot{}, false, nil
	}
	filePath := member.DefinitionPath
	if filePath == "" {
		filePath = definitionPath
	}
	slot := VueComponentSlot{
		Name:        member.Name,
		FilePath:    filePath,
		Line:        member.DefinitionLine,
		NameRange:   member.DefinitionRange,
		PayloadType: meteorSlotPayloadType(member.Type),
	}
	if slot.PayloadType == "" {
		slot.MembersComplete = true
		return slot, true, nil
	}
	payload, err := idx.ResolveVueType(slot.PayloadType, filePath, liveFiles...)
	if err != nil {
		return VueComponentSlot{}, false, err
	}
	slot.MembersComplete = payload.Complete
	for _, payloadMember := range payload.Members {
		memberPath := payloadMember.DefinitionPath
		if memberPath == "" {
			memberPath = filePath
		}
		memberLine := payloadMember.DefinitionLine
		if memberLine == 0 {
			memberLine = slot.Line
		}
		slot.Members = appendSlotMember(slot.Members, VueComponentSlotMember{
			Name:      payloadMember.Name,
			Type:      payloadMember.Type,
			FilePath:  memberPath,
			Line:      memberLine,
			NameRange: payloadMember.DefinitionRange,
		})
	}
	return slot, true, nil
}

func applyScriptSetupPropBindingMembers(
	base []VueComponentMember,
	props []VueComponentProp,
	bindings []ScriptSetupPropBinding,
	definitionPath string,
) []VueComponentMember {
	result := append([]VueComponentMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index := range result {
		positions[result[index].Name] = index
	}
	propsByName := make(map[string]VueComponentProp, len(props))
	for _, prop := range props {
		propsByName[prop.Name] = prop
	}
	for _, binding := range bindings {
		prop, found := propsByName[binding.PropName]
		if !found || binding.BindingName == "" {
			continue
		}
		kind := ComponentMemberData
		if binding.BindingName == binding.PropName {
			kind = ComponentMemberProp
		}
		member := VueComponentMember{
			Name: binding.BindingName, BindingName: binding.BindingName,
			Kind: kind, Type: prop.Type, FilePath: definitionPath,
			Line: binding.Line, NameRange: binding.NameRange,
			TypeContextPath: prop.FilePath,
		}
		if index, exists := positions[binding.BindingName]; exists {
			current := result[index]
			member.SourceExpression = current.SourceExpression
			member.OpenRuntimeShape = current.OpenRuntimeShape
			result[index] = member
			continue
		}
		positions[binding.BindingName] = len(result)
		result = append(result, member)
	}
	return result
}

func overlayScriptSetupSlots(
	base,
	typed []VueComponentSlot,
) []VueComponentSlot {
	result := append([]VueComponentSlot(nil), base...)
	positions := make(map[string]int, len(result))
	for index := range result {
		positions[result[index].identityKey()] = index
	}
	for _, slot := range typed {
		key := slot.identityKey()
		if index, found := positions[key]; found {
			current := &result[index]
			current.PayloadType = slot.PayloadType
			current.Members = overlayTypedScriptSetupSlotMembers(
				current.Members, slot.Members,
			)
			current.MembersComplete = slot.MembersComplete
			if current.FilePath == "" {
				current.FilePath = slot.FilePath
				current.Line = slot.Line
			}
			if current.NameRange == (AdminSourceRange{}) &&
				current.FilePath == slot.FilePath {
				current.NameRange = slot.NameRange
			}
			continue
		}
		positions[key] = len(result)
		result = append(result, slot)
	}
	return result
}

func overlayTypedScriptSetupSlotMembers(
	base,
	typed []VueComponentSlotMember,
) []VueComponentSlotMember {
	result := append([]VueComponentSlotMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index := range result {
		positions[result[index].Name] = index
	}
	for _, member := range typed {
		if member.Name == "" {
			continue
		}
		if index, found := positions[member.Name]; found {
			current := result[index]
			if member.Type == "" {
				member.Type = current.Type
			}
			if member.FilePath == "" {
				member.FilePath = current.FilePath
				member.Line = current.Line
			}
			if member.NameRange == (AdminSourceRange{}) &&
				member.FilePath == current.FilePath {
				member.NameRange = current.NameRange
			}
			result[index] = member
			continue
		}
		positions[member.Name] = len(result)
		result = append(result, member)
	}
	return result
}

func overlayResolvedScriptSetupPropMembers(
	base []VueComponentMember,
	props []VueComponentProp,
) []VueComponentMember {
	result := append([]VueComponentMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index := range result {
		positions[result[index].Name] = index
	}
	for _, prop := range props {
		if prop.Name == "" {
			continue
		}
		if index, found := positions[prop.Name]; found {
			if result[index].Type == "" {
				result[index].Type = prop.Type
			}
			continue
		}
		positions[prop.Name] = len(result)
		result = append(result, VueComponentMember{
			Name: prop.Name, Kind: ComponentMemberProp, Type: prop.Type,
			FilePath: prop.FilePath, Line: prop.Line,
		})
	}
	return result
}
