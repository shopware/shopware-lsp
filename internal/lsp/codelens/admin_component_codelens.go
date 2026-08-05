package codelens

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminComponentCodeLensProvider provides the portable equivalent of the
// PhpStorm component registration/override gutter markers.
type AdminComponentCodeLensProvider struct {
	index *admin.AdminComponentIndexer
}

func NewAdminComponentCodeLensProvider(index *admin.AdminComponentIndexer) *AdminComponentCodeLensProvider {
	return &AdminComponentCodeLensProvider{index: index}
}

func (p *AdminComponentCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.index == nil || request == nil || request.Document == nil {
		return nil, nil
	}
	ext := strings.ToLower(filepath.Ext(request.TextDocument.URI))
	if ext != ".js" && ext != ".ts" && ext != ".twig" && ext != ".vue" {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	components, err := p.index.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(components, func(left, right int) bool {
		if components[left].FilePath != components[right].FilePath {
			return components[left].FilePath < components[right].FilePath
		}
		if components[left].Line != components[right].Line {
			return components[left].Line < components[right].Line
		}
		return components[left].Name < components[right].Name
	})
	if ext == ".twig" {
		return p.templateCodeLenses(ctx, path, components)
	}
	if ext == ".vue" {
		result, err := p.scriptCodeLenses(ctx, path, components)
		if err != nil {
			return nil, err
		}
		template, err := p.templateCodeLenses(ctx, path, components)
		if err != nil {
			return nil, err
		}
		result = append(result, template...)
		sortAdminComponentCodeLenses(result)
		return result, nil
	}
	return p.scriptCodeLenses(ctx, path, components)
}

func (p *AdminComponentCodeLensProvider) scriptCodeLenses(
	ctx context.Context,
	path string,
	components []admin.VueComponent,
) ([]protocol.CodeLens, error) {
	var result []protocol.CodeLens
	for _, component := range components {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !componentSourceMatchesPath(component, path) {
			continue
		}
		line := component.Line - 1
		if line < 0 || (component.DefinitionPath != "" &&
			filepath.Clean(component.DefinitionPath) == filepath.Clean(path) &&
			filepath.Clean(component.FilePath) != filepath.Clean(path)) {
			line = 0
		}
		templatePath, err := p.componentTemplatePath(component)
		if err != nil {
			return nil, err
		}
		if templatePath != "" &&
			filepath.Clean(templatePath) != filepath.Clean(path) {
			result = append(result, relatedLens(protocol.Range{
				Start: protocol.Position{Line: line},
				End:   protocol.Position{Line: line},
			}, "Open component template", []string{
				relatedTarget(templatePath, 1),
			}))
		}
		var targets []string
		title := "Open base component"
		if component.Kind == admin.ComponentRegister || component.Kind == "" {
			title = "Open component extensions"
			for _, candidate := range components {
				if candidate.TargetComponent == component.Name &&
					candidate.FilePath != component.FilePath {
					targets = append(targets, relatedTarget(candidate.FilePath, candidate.Line))
				}
			}
		} else {
			for _, candidate := range components {
				if candidate.Name == component.TargetComponent &&
					(candidate.Kind == admin.ComponentRegister || candidate.Kind == "") {
					targets = append(targets, relatedTarget(candidate.FilePath, candidate.Line))
				}
			}
		}
		targets = uniqueRelatedTargets(targets)
		if len(targets) == 0 {
			continue
		}
		if len(targets) > 1 {
			title = fmt.Sprintf("%s (%d)", title, len(targets))
		}
		result = append(result, relatedLens(protocol.Range{
			Start: protocol.Position{Line: line},
			End:   protocol.Position{Line: line},
		}, title, targets))
	}
	sortAdminComponentCodeLenses(result)
	return result, nil
}

func (p *AdminComponentCodeLensProvider) templateCodeLenses(
	ctx context.Context,
	path string,
	components []admin.VueComponent,
) ([]protocol.CodeLens, error) {
	owner, err := p.index.GetComponentByTemplatePath(path)
	if err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, nil
	}
	definitionTargets := make([]string, 0, 1)
	componentNames := map[string]bool{owner.Name: true}
	for _, component := range components {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if component.Name != owner.Name {
			continue
		}
		templatePath, err := p.componentTemplatePath(component)
		if err != nil {
			return nil, err
		}
		if templatePath == "" ||
			filepath.Clean(templatePath) != filepath.Clean(path) {
			continue
		}
		targetPath := component.DefinitionPath
		targetLine := 1
		if targetPath == "" {
			targetPath = component.FilePath
			targetLine = component.Line
		} else if filepath.Clean(targetPath) == filepath.Clean(component.FilePath) {
			targetLine = component.Line
		}
		if filepath.Clean(targetPath) != filepath.Clean(path) {
			definitionTargets = append(
				definitionTargets,
				relatedTarget(targetPath, targetLine),
			)
		}
	}
	definitionTargets = uniqueRelatedTargets(definitionTargets)
	var result []protocol.CodeLens
	if len(definitionTargets) > 0 {
		title := "Open component definition"
		if len(componentNames) == 1 {
			for name := range componentNames {
				title = "Open " + name + " component definition"
			}
		} else if len(definitionTargets) > 1 {
			title = fmt.Sprintf("Open component definitions (%d)", len(definitionTargets))
		}
		result = append(result, relatedLens(protocol.Range{}, title, definitionTargets))
	}

	var extensionTargets []string
	for _, component := range components {
		if !componentNames[component.TargetComponent] {
			continue
		}
		extensionTargets = append(
			extensionTargets,
			relatedTarget(component.FilePath, component.Line),
		)
	}
	extensionTargets = uniqueRelatedTargets(extensionTargets)
	if len(extensionTargets) > 0 {
		title := "Open component extensions"
		if len(extensionTargets) > 1 {
			title = fmt.Sprintf("%s (%d)", title, len(extensionTargets))
		}
		result = append(result, relatedLens(
			protocol.Range{}, title, extensionTargets,
		))
	}
	return result, nil
}

func (p *AdminComponentCodeLensProvider) componentTemplatePath(
	component admin.VueComponent,
) (string, error) {
	if component.TemplatePath != "" {
		return component.TemplatePath, nil
	}
	resolved, err := p.index.GetComponentRegistrationWithDefinition(component)
	if err != nil || resolved == nil {
		return "", err
	}
	return resolved.TemplatePath, nil
}

func componentSourceMatchesPath(component admin.VueComponent, path string) bool {
	cleanPath := filepath.Clean(path)
	return filepath.Clean(component.FilePath) == cleanPath ||
		(component.DefinitionPath != "" &&
			filepath.Clean(component.DefinitionPath) == cleanPath)
}

func sortAdminComponentCodeLenses(result []protocol.CodeLens) {
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line < result[right].Range.Start.Line
		}
		return result[left].Command.Title < result[right].Command.Title
	})
}

func (p *AdminComponentCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}
