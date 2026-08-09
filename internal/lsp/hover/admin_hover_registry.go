package hover

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func (p *AdminHoverProvider) serviceHover(name string) (*protocol.Hover, error) {
	services, err := p.adminIndexer.GetService(name)
	if err != nil || len(services) == 0 {
		return nil, err
	}
	var sections []string
	for _, service := range services {
		value := fmt.Sprintf(
			"**Administration service** `%s`\n\nRegistered in `%s:%d`.",
			service.Name,
			p.makeRelativePath(service.FilePath),
			service.Line,
		)
		if service.ImplementationPath != "" {
			value += "\n\nImplementation: `" +
				p.makeRelativePath(service.ImplementationPath) + "`."
		}
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) storeHover(name string) (*protocol.Hover, error) {
	stores, err := p.adminIndexer.GetStore(name)
	if err != nil || len(stores) == 0 {
		return nil, err
	}
	var sections []string
	for _, store := range stores {
		value := fmt.Sprintf(
			"**Administration store** `%s`\n\nRegistered in `%s:%d`.",
			store.Name,
			p.makeRelativePath(store.FilePath),
			store.Line,
		)
		if len(store.Members) > 0 {
			value += fmt.Sprintf("\n\nIndexed members: %d.", len(store.Members))
		}
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) privilegeHover(name string) (*protocol.Hover, error) {
	privileges, err := p.adminIndexer.GetPrivilege(name)
	if err != nil || len(privileges) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(privileges))
	for _, privilege := range privileges {
		if privilege.IsBuiltin() {
			sections = append(sections, fmt.Sprintf(
				"**Built-in Administration privilege** `%s`\n\n"+
					"Provided by Shopware for administrator-only access.",
				privilege.Name,
			))
			continue
		}
		kind := "Administration privilege role"
		owner := privilege.MappingKey
		if privilege.Kind == admin.AdminPrivilegePermission {
			kind = "Administration permission"
			if privilege.Role != "" {
				owner += "." + privilege.Role
			}
		}
		value := fmt.Sprintf("**%s** `%s`", kind, privilege.Name)
		if owner != "" {
			value += "\n\nDeclared by `" + owner + "`."
		}
		value += fmt.Sprintf(
			"\n\nDefined in `%s:%d`.",
			p.makeRelativePath(privilege.FilePath), privilege.Line,
		)
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) moduleRouteHover(
	name string,
) (*protocol.Hover, error) {
	module, route, err := p.adminIndexer.GetModuleRoute(name)
	if err != nil || module == nil || route == nil {
		return nil, err
	}
	value := fmt.Sprintf(
		"**Administration module route** `%s`\n\nModule: `%s`",
		route.Name,
		module.Name,
	)
	if route.Path != "" {
		value += "\n\nPath: `" + route.Path + "`"
	}
	if route.Component != "" {
		value += "\n\nComponent: `" + route.Component + "`"
	}
	value += fmt.Sprintf(
		"\n\nDefined in `%s:%d`.",
		p.makeRelativePath(module.FilePath),
		route.Line,
	)
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: value,
	}}, nil
}

func (p *AdminHoverProvider) mixinHover(name string) (*protocol.Hover, error) {
	mixins, err := p.adminIndexer.GetMixin(name)
	if err != nil || len(mixins) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(mixins))
	for _, mixin := range mixins {
		value := fmt.Sprintf(
			"**Administration mixin** `%s`\n\nDefined in `%s:%d`.",
			mixin.Name,
			p.makeRelativePath(mixin.FilePath),
			mixin.Line,
		)
		memberCount := len(mixin.Definition.Members)
		if memberCount == 0 {
			memberCount = len(mixin.Definition.Props) +
				len(mixin.Definition.Data) +
				len(mixin.Definition.Computed) +
				len(mixin.Definition.Methods) +
				len(mixin.Definition.Injected)
		}
		if memberCount > 0 {
			value += fmt.Sprintf("\n\nIndexed members: %d.", memberCount)
		}
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) directiveHover(
	name string,
) (*protocol.Hover, error) {
	directives, err := p.adminIndexer.GetDirective(name)
	if err != nil || len(directives) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(directives))
	for _, directive := range directives {
		sections = append(sections, fmt.Sprintf(
			"**Administration Vue directive** `v-%s`\n\nDefined in `%s:%d`.",
			directive.Name,
			p.makeRelativePath(directive.FilePath),
			directive.Line,
		))
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) filterHover(
	name string,
) (*protocol.Hover, error) {
	filters, err := p.adminIndexer.GetFilter(name)
	if err != nil || len(filters) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(filters))
	for _, filter := range filters {
		value := fmt.Sprintf(
			"**Administration filter** `%s`", filter.Name,
		)
		if filter.Signature != "" {
			value += "\n\n```typescript\n" + filter.Signature + "\n```"
		}
		value += fmt.Sprintf(
			"\n\nDefined in `%s:%d`.",
			p.makeRelativePath(filter.FilePath), filter.Line,
		)
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) cmsHover(
	kind admin.AdminCMSRegistrationKind,
	name string,
) (*protocol.Hover, error) {
	registrations, err := p.adminIndexer.GetCMSRegistration(kind, name)
	if err != nil || len(registrations) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		value := fmt.Sprintf(
			"**Shopware CMS %s** `%s`", kind, registration.Name,
		)
		if registration.Label != "" {
			value += "\n\nLabel: `" + registration.Label + "`."
		}
		if registration.Category != "" {
			value += "\n\nCategory: `" + registration.Category + "`."
		}
		for _, component := range []struct {
			label string
			name  string
		}{
			{"Component", registration.Component},
			{"Configuration component", registration.ConfigComponent},
			{"Preview component", registration.PreviewComponent},
		} {
			if component.name != "" {
				value += "\n\n" + component.label + ": `" + component.name + "`."
			}
		}
		if len(registration.Slots) > 0 {
			value += fmt.Sprintf("\n\nIndexed slots: %d.", len(registration.Slots))
		}
		value += fmt.Sprintf(
			"\n\nDefined in `%s:%d`.",
			p.makeRelativePath(registration.FilePath), registration.Line,
		)
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) directiveHoverForTemplate(
	name,
	templatePath string,
) (*protocol.Hover, error) {
	directives, err := p.adminIndexer.GetDirectiveForTemplate(templatePath, name)
	if err != nil || len(directives) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(directives))
	for _, directive := range directives {
		kind := "Administration Vue directive"
		if directive.Local {
			kind = "Component-local Administration Vue directive"
		}
		sections = append(sections, fmt.Sprintf(
			"**%s** `v-%s`\n\nDefined in `%s:%d`.",
			kind, directive.Name,
			p.makeRelativePath(directive.FilePath), directive.Line,
		))
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) directiveHoverTarget(
	target admin.AdminSymbolTarget,
) (*protocol.Hover, error) {
	if target.Owner == "" {
		return p.directiveHover(target.Name)
	}
	components, err := p.adminIndexer.GetComponentsByDefinitionPath(target.Owner)
	if err != nil {
		return nil, err
	}
	for _, component := range components {
		if local, found := component.LocalDirective(target.Name); found {
			return &protocol.Hover{Contents: protocol.MarkupContent{
				Kind: protocol.Markdown,
				Value: fmt.Sprintf(
					"**Component-local Administration Vue directive** `v-%s`\n\nDefined in `%s:%d`.",
					local.Name, p.makeRelativePath(local.FilePath), local.Line,
				),
			}}, nil
		}
	}
	return nil, nil
}

func (p *AdminHoverProvider) moduleHover(name string) (*protocol.Hover, error) {
	modules, err := p.adminIndexer.GetModule(name)
	if err != nil || len(modules) == 0 {
		return nil, err
	}
	sections := make([]string, 0, len(modules))
	for _, module := range modules {
		value := fmt.Sprintf("**Administration module** `%s`", module.Name)
		if module.Title != "" {
			value += "\n\nTitle: `" + module.Title + "`."
		}
		if module.Type != "" {
			value += "\n\nType: `" + module.Type + "`."
		}
		if module.DisplayName != "" {
			value += "\n\nName: `" + module.DisplayName + "`."
		}
		value += fmt.Sprintf("\n\nIndexed routes: %d.", len(module.Routes))
		value += fmt.Sprintf(
			"\n\nDefined in `%s:%d`.",
			p.makeRelativePath(module.FilePath), module.Line,
		)
		sections = append(sections, value)
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) storeMemberHover(
	storeName,
	memberName string,
) (*protocol.Hover, error) {
	stores, err := p.adminIndexer.GetStore(storeName)
	if err != nil {
		return nil, err
	}
	var sections []string
	for _, store := range stores {
		member, found := store.Member(memberName)
		if !found {
			continue
		}
		value := fmt.Sprintf("**%s** `%s`", member.Kind, member.Name)
		if member.Type != "" {
			value += ": `" + member.Type + "`"
		}
		value += fmt.Sprintf(
			"\n\nMember of Administration store `%s`.\n\nDefined in `%s:%d`.",
			store.Name,
			p.makeRelativePath(member.FilePath),
			member.Line,
		)
		sections = append(sections, value)
	}
	if len(sections) == 0 {
		return nil, nil
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}

func (p *AdminHoverProvider) thisMemberHover(
	uri,
	name string,
) (*protocol.Hover, error) {
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil, nil
	}
	components, err := p.adminIndexer.GetComponentsByDefinitionPath(path)
	if err != nil || len(components) == 0 {
		return nil, err
	}
	var sections []string
	seen := make(map[string]bool)
	for _, component := range components {
		member, found := component.TemplateMember(name)
		if !found {
			continue
		}
		key := component.Name + "\x00" + string(member.Kind) + "\x00" + member.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		value := fmt.Sprintf("**%s** `%s`", member.Kind, member.Name)
		if member.Type != "" {
			value += ": `" + member.Type + "`"
		}
		value += "\n\nVue instance member of `" + component.Name + "`."
		if member.Deprecated != "" {
			value += "\n\n**Deprecated:** " + member.Deprecated
		} else if member.Kind == admin.ComponentMemberProp {
			if prop, propFound := component.ComponentProp(member.Name); propFound &&
				prop.Deprecated != "" {
				value += "\n\n**Deprecated:** " + prop.Deprecated
			}
		}
		if member.FilePath != "" {
			value += "\n\nDefined in `" + p.makeRelativePath(member.FilePath) + "`."
		}
		sections = append(sections, value)
	}
	if len(sections) == 0 {
		for _, builtin := range admin.VueBuiltinMembers() {
			if builtin.Name == name {
				return &protocol.Hover{Contents: protocol.MarkupContent{
					Kind:  protocol.Markdown,
					Value: "**Vue instance member** `" + name + "`",
				}}, nil
			}
		}
		return nil, nil
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{
		Kind: protocol.Markdown, Value: strings.Join(sections, "\n\n---\n\n"),
	}}, nil
}
