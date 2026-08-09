package reference

import (
	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type adminDeclarationCollector struct {
	provider *AdminReferenceProvider
	result   []protocol.Location
}

func (c *adminDeclarationCollector) collect(target admin.AdminSymbolTarget) error {
	switch target.Kind {
	case admin.AdminSymbolComponent:
		return c.components(target.Name)
	case admin.AdminSymbolService:
		return c.services(target.Name)
	case admin.AdminSymbolStore:
		return c.stores(target.Name)
	case admin.AdminSymbolStoreMember:
		return c.storeMembers(target)
	case admin.AdminSymbolPrivilege:
		return c.privileges(target.Name)
	case admin.AdminSymbolMixin:
		return c.mixins(target.Name)
	case admin.AdminSymbolDirective:
		return c.directives(target)
	case admin.AdminSymbolFilter:
		return c.filters(target.Name)
	case admin.AdminSymbolCMSElement, admin.AdminSymbolCMSBlock:
		return c.cmsRegistrations(target)
	case admin.AdminSymbolModule:
		return c.modules(target.Name)
	case admin.AdminSymbolModuleRoute:
		return c.moduleRoutes(target.Name)
	case admin.AdminSymbolEventBusEvent:
		return c.eventBusEvents(target.Name)
	case admin.AdminSymbolComponentProp,
		admin.AdminSymbolComponentEvent,
		admin.AdminSymbolComponentSlot:
		return c.componentContracts(target)
	default:
		return nil
	}
}

func (c *adminDeclarationCollector) add(path string, line int) {
	if path == "" {
		return
	}
	if line < 1 {
		line = 1
	}
	c.result = append(c.result, protocol.Location{
		URI: uriutil.FileURI(path),
		Range: protocol.Range{
			Start: protocol.Position{Line: line - 1},
			End:   protocol.Position{Line: line - 1},
		},
	})
}

func (c *adminDeclarationCollector) addDeclaration(
	path string,
	line int,
	nameRange admin.AdminSourceRange,
) {
	if path != "" && (nameRange.Declaration || nameRange.Identifier) {
		c.result = append(c.result, adminUsageLocation(path, nameRange))
		return
	}
	c.add(path, line)
}

func (c *adminDeclarationCollector) components(name string) error {
	values, err := c.provider.index.GetComponent(name)
	if err != nil {
		return err
	}
	for _, value := range values {
		c.add(value.FilePath, value.Line)
	}
	return nil
}

func (c *adminDeclarationCollector) services(name string) error {
	values, err := c.provider.index.GetService(name)
	if err != nil {
		return err
	}
	for _, value := range values {
		c.add(value.FilePath, value.Line)
	}
	return nil
}

func (c *adminDeclarationCollector) stores(name string) error {
	values, err := c.provider.index.GetStore(name)
	if err != nil {
		return err
	}
	for _, value := range values {
		c.add(value.FilePath, value.Line)
	}
	return nil
}

func (c *adminDeclarationCollector) storeMembers(target admin.AdminSymbolTarget) error {
	values, err := c.provider.index.GetStore(target.Owner)
	if err != nil {
		return err
	}
	for _, value := range values {
		if member, found := value.Member(target.Name); found {
			c.add(member.FilePath, member.Line)
		}
	}
	return nil
}

func (c *adminDeclarationCollector) privileges(name string) error {
	values, err := c.provider.index.GetPrivilege(name)
	if err != nil {
		return err
	}
	for _, value := range values {
		c.add(value.FilePath, value.Line)
	}
	return nil
}

func (c *adminDeclarationCollector) mixins(name string) error {
	values, err := c.provider.index.GetMixin(name)
	if err != nil {
		return err
	}
	for _, value := range values {
		c.add(value.FilePath, value.Line)
	}
	return nil
}

func (c *adminDeclarationCollector) directives(target admin.AdminSymbolTarget) error {
	if target.Owner != "" {
		return c.localDirectives(target)
	}
	values, err := c.provider.index.GetDirective(target.Name)
	if err != nil {
		return err
	}
	for _, value := range values {
		c.add(value.FilePath, value.Line)
	}
	return nil
}

func (c *adminDeclarationCollector) localDirectives(target admin.AdminSymbolTarget) error {
	components, err := c.provider.index.GetComponentsByDefinitionPath(target.Owner)
	if err != nil {
		return err
	}
	for _, component := range components {
		if local, found := component.LocalDirective(target.Name); found {
			c.add(local.FilePath, local.Line)
		}
	}
	return nil
}

func (c *adminDeclarationCollector) filters(name string) error {
	values, err := c.provider.index.GetFilter(name)
	if err != nil {
		return err
	}
	for _, value := range values {
		c.add(value.FilePath, value.Line)
	}
	return nil
}

func (c *adminDeclarationCollector) cmsRegistrations(target admin.AdminSymbolTarget) error {
	kind := admin.AdminCMSElement
	if target.Kind == admin.AdminSymbolCMSBlock {
		kind = admin.AdminCMSBlock
	}
	values, err := c.provider.index.GetCMSRegistration(kind, target.Name)
	if err != nil {
		return err
	}
	for _, value := range values {
		c.add(value.FilePath, value.Line)
	}
	return nil
}

func (c *adminDeclarationCollector) modules(name string) error {
	values, err := c.provider.index.GetModule(name)
	if err != nil {
		return err
	}
	for _, value := range values {
		c.add(value.FilePath, value.Line)
	}
	return nil
}

func (c *adminDeclarationCollector) moduleRoutes(name string) error {
	module, route, err := c.provider.index.GetModuleRoute(name)
	if err != nil {
		return err
	}
	if module != nil && route != nil {
		c.add(module.FilePath, route.Line)
	}
	return nil
}

func (c *adminDeclarationCollector) eventBusEvents(name string) error {
	event, found, err := c.provider.index.ResolveShopwareEventBusEvent(name, "")
	if err != nil || !found || event.DefinitionPath == "" {
		return err
	}
	if event.DefinitionRange.Declaration || event.DefinitionRange.Identifier {
		c.result = append(c.result, adminUsageLocation(
			event.DefinitionPath,
			event.DefinitionRange,
		))
		return nil
	}
	c.add(event.DefinitionPath, event.DefinitionLine)
	return nil
}

func (c *adminDeclarationCollector) componentContracts(
	target admin.AdminSymbolTarget,
) error {
	components, err := c.provider.index.GetComponentsExposingSymbol(target)
	if err != nil {
		return err
	}
	for _, component := range components {
		c.addComponentContract(component, target)
	}
	return nil
}

func (c *adminDeclarationCollector) addComponentContract(
	component admin.VueComponent,
	target admin.AdminSymbolTarget,
) {
	switch target.Kind {
	case admin.AdminSymbolComponentProp:
		prop, _ := component.ComponentProp(target.Name)
		c.addDeclaration(target.Owner, prop.Line, prop.NameRange)
	case admin.AdminSymbolComponentEvent:
		event, _ := component.ComponentEvent(target.Name)
		c.addDeclaration(target.Owner, event.Line, event.NameRange)
	case admin.AdminSymbolComponentSlot:
		if slot, found := component.ComponentSlot(target.Name); found {
			c.addDeclaration(target.Owner, slot.Line, slot.NameRange)
		}
	}
}
