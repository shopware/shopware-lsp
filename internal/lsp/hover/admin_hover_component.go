package hover

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func (p *AdminHoverProvider) isInComponentCall(node *jssyntax.Node) bool {
	target, found := admin.JavaScriptSymbolAt(node)
	return found && target.Kind == admin.AdminSymbolComponent
}

func (p *AdminHoverProvider) extractComponentName(node *jssyntax.Node) string {
	target, found := admin.JavaScriptSymbolAt(node)
	if !found || target.Kind != admin.AdminSymbolComponent {
		return ""
	}
	return target.Name
}

// buildHoverContent creates the markdown content for the hover popup
func (p *AdminHoverProvider) buildHoverContent(components []admin.VueComponent) string {
	var sb strings.Builder

	for i, comp := range components {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}

		// Component name header
		fmt.Fprintf(&sb, "## `%s`\n\n", comp.Name)
		if comp.Deprecated != "" {
			fmt.Fprintf(&sb, "**Deprecated:** %s\n\n", comp.Deprecated)
		}

		// Show if it extends another component
		if comp.ExtendsComponent != "" {
			fmt.Fprintf(&sb, "**Extends**: `%s`\n\n", comp.ExtendsComponent)
		}

		// Props section
		if len(comp.Props) > 0 {
			sb.WriteString("### Props\n\n")
			for _, prop := range comp.Props {
				propLine := fmt.Sprintf("- `%s`", prop.Name)
				if prop.Type != "" {
					propLine += fmt.Sprintf(": **%s**", prop.Type)
				}
				if prop.Required {
					propLine += " *(required)*"
				}
				if prop.Deprecated != "" {
					propLine += " *(deprecated)*"
				}
				if prop.Default != "" {
					propLine += fmt.Sprintf(" = `%s`", prop.Default)
				}
				sb.WriteString(propLine + "\n")
			}
			sb.WriteString("\n")
		}

		// Emits section
		if events := comp.ComponentEvents(); len(events) > 0 {
			sb.WriteString("### Events\n\n")
			for _, event := range events {
				fmt.Fprintf(&sb, "- `%s`", admin.CanonicalEventName(event.Name))
				if event.Type != "" {
					fmt.Fprintf(&sb, ": `%s`", event.Type)
				}
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}

		// Methods section
		if len(comp.Methods) > 0 {
			sb.WriteString("### Methods\n\n")
			for _, method := range comp.Methods {
				fmt.Fprintf(&sb, "- `%s()`\n", method)
			}
			sb.WriteString("\n")
		}

		// Computed section
		if len(comp.Computed) > 0 {
			sb.WriteString("### Computed\n\n")
			for _, computed := range comp.Computed {
				fmt.Fprintf(&sb, "- `%s`\n", computed)
			}
			sb.WriteString("\n")
		}

		if len(comp.Data) > 0 {
			sb.WriteString("### Data\n\n")
			for _, data := range comp.Data {
				fmt.Fprintf(&sb, "- `%s`\n", data)
			}
			sb.WriteString("\n")
		}

		if len(comp.Injected) > 0 {
			sb.WriteString("### Injected services\n\n")
			for _, service := range comp.Injected {
				fmt.Fprintf(&sb, "- `%s`\n", service)
			}
			sb.WriteString("\n")
		}

		// Slots section
		if len(comp.Slots) > 0 {
			sb.WriteString("### Slots\n\n")
			for _, slot := range comp.Slots {
				fmt.Fprintf(&sb, "- `%s`\n", slot.DisplayName())
			}
			sb.WriteString("\n")
		}

		// File path (relative to project root)
		if comp.DefinitionPath != "" {
			displayPath := p.makeRelativePath(comp.DefinitionPath)
			fmt.Fprintf(&sb, "*Defined in*: `%s`\n", displayPath)
		} else if comp.FilePath != "" {
			displayPath := p.makeRelativePath(comp.FilePath)
			fmt.Fprintf(&sb, "*Registered in*: `%s`\n", displayPath)
		}
	}

	return sb.String()
}

func firstHoverOwner(values []*admin.VueComponent) *admin.VueComponent {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

// makeRelativePath converts an absolute path to a path relative to the project root
func (p *AdminHoverProvider) makeRelativePath(absPath string) string {
	if p.projectRoot == "" {
		return absPath
	}
	relPath, err := filepath.Rel(p.projectRoot, absPath)
	if err != nil {
		return absPath
	}
	return relPath
}
