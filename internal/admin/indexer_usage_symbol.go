package admin

import (
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func (idx *AdminComponentIndexer) JavaScriptSymbolAt(
	filePath string,
	node *jssyntax.Node,
) (AdminSymbolTarget, bool, error) {
	lookups := []func() (AdminSymbolTarget, bool, error){
		func() (AdminSymbolTarget, bool, error) {
			return idx.javaScriptComponentPropAt(filePath, node)
		},
		func() (AdminSymbolTarget, bool, error) {
			return idx.javaScriptComponentEventAt(filePath, node)
		},
		func() (AdminSymbolTarget, bool, error) {
			return idx.javaScriptDefinitionSymbolAt(filePath, node)
		},
	}
	for _, lookup := range lookups {
		if target, found, err := lookup(); err != nil || found {
			return target, found, err
		}
	}
	if target, found := JavaScriptSymbolAt(node); found {
		return target, true, nil
	}
	return idx.javaScriptThisMemberAt(filePath, node)
}

func (idx *AdminComponentIndexer) javaScriptComponentPropAt(
	filePath string,
	node *jssyntax.Node,
) (AdminSymbolTarget, bool, error) {
	name, found := JavaScriptComponentPropAt(node)
	if !found {
		return AdminSymbolTarget{}, false, nil
	}
	components, err := idx.GetComponentsByDefinitionPath(filePath)
	if err != nil {
		return AdminSymbolTarget{}, false, err
	}
	for _, component := range components {
		if prop, exists := component.ComponentProp(name); exists {
			return AdminSymbolTarget{
				Kind:  AdminSymbolComponentProp,
				Owner: adminComponentSymbolOwner(prop.FilePath, component.DefinitionPath, filePath),
				Name:  prop.Name,
			}, true, nil
		}
	}
	return AdminSymbolTarget{}, false, nil
}

func (idx *AdminComponentIndexer) javaScriptComponentEventAt(
	filePath string,
	node *jssyntax.Node,
) (AdminSymbolTarget, bool, error) {
	name, found := JavaScriptComponentEventAt(node)
	if !found {
		return AdminSymbolTarget{}, false, nil
	}
	components, err := idx.GetComponentsByDefinitionPath(filePath)
	if err != nil {
		return AdminSymbolTarget{}, false, err
	}
	for _, component := range components {
		if event, exists := component.ComponentEvent(name); exists {
			return AdminSymbolTarget{
				Kind:  AdminSymbolComponentEvent,
				Owner: adminComponentSymbolOwner(event.FilePath, component.DefinitionPath, filePath),
				Name:  CanonicalEventName(event.Name),
			}, true, nil
		}
	}
	return AdminSymbolTarget{}, false, nil
}

func adminComponentSymbolOwner(paths ...string) string {
	for _, path := range paths {
		if path != "" {
			return path
		}
	}
	return ""
}

func (idx *AdminComponentIndexer) javaScriptDefinitionSymbolAt(
	filePath string,
	node *jssyntax.Node,
) (AdminSymbolTarget, bool, error) {
	if node == nil {
		return AdminSymbolTarget{}, false, nil
	}
	root := node
	for root.Parent() != nil {
		root = root.Parent()
	}
	lineIndex := jssyntax.NewLineIndex(root.Text())
	event, found, err := idx.shopwareEventBusEventAtDefinitionRange(
		filePath,
		node.RangeTrimmedTrivia(),
		lineIndex,
	)
	if err != nil {
		return AdminSymbolTarget{}, false, err
	}
	if found {
		return AdminSymbolTarget{
			Kind: AdminSymbolEventBusEvent,
			Name: event.Name,
		}, true, nil
	}
	line, character := lineIndex.PositionUTF16(node.RangeTrimmedTrivia().Start)
	_, directive, found, err := idx.GetLocalDirectiveAtDefinitionPosition(
		filePath,
		int(line),
		int(character),
	)
	if err != nil {
		return AdminSymbolTarget{}, false, err
	}
	if found {
		return AdminSymbolTarget{
			Kind:  AdminSymbolDirective,
			Owner: directive.FilePath,
			Name:  directive.Name,
		}, true, nil
	}
	member, found, err := idx.GetComponentMemberAtDefinitionPosition(
		filePath,
		int(line),
		int(character),
	)
	if err != nil || !found {
		return AdminSymbolTarget{}, false, err
	}
	return componentMemberTarget(member), true, nil
}

func (idx *AdminComponentIndexer) javaScriptThisMemberAt(
	filePath string,
	node *jssyntax.Node,
) (AdminSymbolTarget, bool, error) {
	name, matched := jsquery.ThisMember(node)
	if !matched || name == "" {
		return AdminSymbolTarget{}, false, nil
	}
	components, err := idx.GetComponentsByDefinitionPath(filePath)
	if err != nil {
		return AdminSymbolTarget{}, false, err
	}
	for _, component := range components {
		if target, found := thisComponentTarget(component, name, filePath); found {
			return target, true, nil
		}
	}
	return idx.declaredThisMemberTarget(filePath, name)
}

func thisComponentTarget(
	component VueComponent,
	name,
	filePath string,
) (AdminSymbolTarget, bool) {
	if prop, found := component.ComponentProp(name); found {
		return AdminSymbolTarget{
			Kind:  AdminSymbolComponentProp,
			Owner: adminComponentSymbolOwner(prop.FilePath, component.DefinitionPath, filePath),
			Name:  prop.Name,
		}, true
	}
	for _, injected := range component.Injected {
		if injected == name {
			return AdminSymbolTarget{Kind: AdminSymbolService, Name: name}, true
		}
	}
	if member, found := component.TemplateMember(name); found && member.Renameable() {
		return componentMemberTarget(member), true
	}
	return AdminSymbolTarget{}, false
}

func (idx *AdminComponentIndexer) declaredThisMemberTarget(
	filePath,
	name string,
) (AdminSymbolTarget, bool, error) {
	declared, err := idx.componentMembersDeclaredIn(filePath)
	if err != nil {
		return AdminSymbolTarget{}, false, err
	}
	var target *VueComponentMember
	for index := range declared {
		if declared[index].Name != name || !declared[index].Renameable() {
			continue
		}
		if target != nil && target.SourceIdentity() != declared[index].SourceIdentity() {
			return AdminSymbolTarget{}, false, nil
		}
		candidate := declared[index]
		target = &candidate
	}
	if target == nil {
		return AdminSymbolTarget{}, false, nil
	}
	return componentMemberTarget(*target), true, nil
}
