package semantic

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func commonAdminAttributeSemanticType(
	name string,
	components []admin.VueComponent,
) (uint32, bool) {
	if len(components) == 0 {
		return 0, false
	}
	var common uint32
	for index := range components {
		tokenType, found := adminAttributeSemanticType(
			name, &components[index], &components[index],
		)
		if !found || index > 0 && tokenType != common {
			return 0, false
		}
		common = tokenType
	}
	return common, true
}

func adminAttributeSemanticType(
	name string,
	component *admin.VueComponent,
	slotComponent *admin.VueComponent,
) (uint32, bool) {
	if slotName := admin.NormalizeSlotName(name); slotName != "" &&
		slotComponent != nil {
		if _, found := slotComponent.ComponentSlot(slotName); found {
			return protocol.SemanticTokenProperty, true
		}
		return 0, false
	}
	if component == nil {
		return 0, false
	}
	if _, model := admin.NormalizeModelArgument(name); model {
		if _, found := component.ComponentModel(name); found {
			return protocol.SemanticTokenProperty, true
		}
		return 0, false
	}
	if event := admin.NormalizeEventName(name); event != "" {
		if _, found := component.ComponentEvent(event); found {
			return protocol.SemanticTokenFunction, true
		}
		return 0, false
	}
	if modifier := strings.IndexByte(name, '.'); modifier >= 0 {
		name = name[:modifier]
	}
	propName := admin.NormalizePropName(name)
	for _, prop := range component.Props {
		if prop.Name == propName {
			return protocol.SemanticTokenProperty, true
		}
	}
	return 0, false
}

func filepathSlash(value string) string {
	return strings.ReplaceAll(value, `\`, "/")
}

var _ lsp.SemanticTokensProvider = (*AdminMarkupProvider)(nil)
