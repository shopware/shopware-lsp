//go:build integration

package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/analytics"
	"github.com/shopware/shopware-lsp/internal/appscript"
	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/require"
)

func requireCodeLensCommand(
	t *testing.T,
	lenses []protocol.CodeLens,
	title,
	command string,
	arguments []any,
) {
	t.Helper()
	for _, lens := range lenses {
		if lens.Command == nil || lens.Command.Title != title {
			continue
		}
		require.Equal(t, command, lens.Command.Command)
		require.Equal(t, arguments, lens.Command.Arguments)
		return
	}
	t.Fatalf("code lens command %q not found in %#v", title, lenses)
}

func requireConsoleCatalogInput(
	t *testing.T,
	inputs []console.CatalogInput,
	name string,
) {
	t.Helper()
	for _, input := range inputs {
		if input.Name == name {
			return
		}
	}
	t.Fatalf("console catalog input %q not found in %#v", name, inputs)
}

func workspaceTranslationIndex(
	t *testing.T,
	workspace *Workspace,
) *translation.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*translation.Index); ok {
			return candidate
		}
	}
	t.Fatal("Translation index is not registered")
	return nil
}

func workspaceSnippetIndex(
	t *testing.T,
	workspace *Workspace,
) *snippet.SnippetIndexer {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*snippet.SnippetIndexer); ok {
			return candidate
		}
	}
	t.Fatal("Snippet index is not registered")
	return nil
}

func workspaceAdminIndex(
	t *testing.T,
	workspace *Workspace,
) *admin.AdminComponentIndexer {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*admin.AdminComponentIndexer); ok {
			return candidate
		}
	}
	t.Fatal("Administration index is not registered")
	return nil
}

func adminPropNames(props []admin.VueComponentProp) []string {
	names := make([]string, 0, len(props))
	for _, prop := range props {
		names = append(names, prop.Name)
	}
	return names
}

func componentDefinitionMemberNamed(
	definition *admin.ComponentDefinition,
	name string,
) (admin.VueComponentMember, bool) {
	if definition == nil {
		return admin.VueComponentMember{}, false
	}
	for _, member := range definition.Members {
		if member.Name == name {
			return member, true
		}
	}
	return admin.VueComponentMember{}, false
}

func requireAdminProp(
	t *testing.T,
	props []admin.VueComponentProp,
	name string,
) admin.VueComponentProp {
	t.Helper()
	for _, prop := range props {
		if prop.Name == name {
			return prop
		}
	}
	t.Fatalf("Administration prop %q not found in %#v", name, props)
	return admin.VueComponentProp{}
}

func adminSlotNames(slots []admin.VueComponentSlot) []string {
	result := make([]string, 0, len(slots))
	for _, slot := range slots {
		result = append(result, slot.Name)
	}
	return result
}

func requireAdminSlot(
	t *testing.T,
	slots []admin.VueComponentSlot,
	name string,
) admin.VueComponentSlot {
	t.Helper()
	for _, slot := range slots {
		if slot.Name == name {
			return slot
		}
	}
	t.Fatalf("Administration slot %q not found in %#v", name, slots)
	return admin.VueComponentSlot{}
}

func requireAdminSlotMember(
	t *testing.T,
	members []admin.VueComponentSlotMember,
	name string,
) admin.VueComponentSlotMember {
	t.Helper()
	for _, member := range members {
		if member.Name == name {
			return member
		}
	}
	t.Fatalf("Administration slot member %q not found in %#v", name, members)
	return admin.VueComponentSlotMember{}
}

func requireAdminEvent(
	t *testing.T,
	events []admin.VueComponentEvent,
	name string,
) admin.VueComponentEvent {
	t.Helper()
	canonical := admin.CanonicalEventName(name)
	for _, event := range events {
		if admin.CanonicalEventName(event.Name) == canonical {
			return event
		}
	}
	t.Fatalf("Administration event %q not found in %#v", name, events)
	return admin.VueComponentEvent{}
}

func adminUsageOccurrenceCount(sets []admin.AdminUsageSet) int {
	count := 0
	for _, set := range sets {
		count += len(set.Occurrences)
	}
	return count
}

func realWorldAdminMemberNames(members []admin.TwigVueMember) []string {
	result := make([]string, 0, len(members))
	for _, member := range members {
		result = append(result, member.Name)
	}
	return result
}

func workspaceDALIndex(
	t *testing.T,
	workspace *Workspace,
) *shopwaredal.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*shopwaredal.Index); ok {
			return candidate
		}
	}
	t.Fatal("Shopware DAL index is not registered")
	return nil
}

func workspaceAppScriptIndex(
	t *testing.T,
	workspace *Workspace,
) *appscript.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*appscript.Index); ok {
			return candidate
		}
	}
	t.Fatal("App script index is not registered")
	return nil
}

func workspaceSymfonyConfigIndex(
	t *testing.T,
	workspace *Workspace,
) *symfonyconfig.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*symfonyconfig.Index); ok {
			return candidate
		}
	}
	t.Fatal("Symfony configuration index is not registered")
	return nil
}

func workspaceServiceIndex(
	t *testing.T,
	workspace *Workspace,
) *symfony.ServiceIndex {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*symfony.ServiceIndex); ok {
			return candidate
		}
	}
	t.Fatal("Service index is not registered")
	return nil
}

func requireWorkspaceSymbol(
	t *testing.T,
	symbols []protocol.SymbolInformation,
	name,
	container string,
) {
	t.Helper()
	for _, current := range symbols {
		if current.Name == name &&
			strings.Contains(current.ContainerName, container) {
			require.NotEmpty(t, current.Location.URI)
			return
		}
	}
	t.Fatalf(
		"workspace symbol %q in %q not found in %#v",
		name,
		container,
		symbols,
	)
}

func workspaceRouteIndex(
	t *testing.T,
	workspace *Workspace,
) *symfony.RouteIndexer {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*symfony.RouteIndexer); ok {
			return candidate
		}
	}
	t.Fatal("Route index is not registered")
	return nil
}

func workspaceRouteUsageIndex(
	t *testing.T,
	workspace *Workspace,
) *symfony.RouteUsageIndexer {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*symfony.RouteUsageIndexer); ok {
			return candidate
		}
	}
	t.Fatal("Route usage index is not registered")
	return nil
}

func workspaceConsoleIndex(t *testing.T, workspace *Workspace) *console.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*console.Index); ok {
			return candidate
		}
	}
	t.Fatal("Console index is not registered")
	return nil
}

func workspaceTwigIndex(
	t *testing.T,
	workspace *Workspace,
) *twig.TwigIndexer {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*twig.TwigIndexer); ok {
			return candidate
		}
	}
	t.Fatal("Twig index is not registered")
	return nil
}

func requireEventListener(
	t *testing.T,
	current event.Event,
	class,
	method string,
) {
	t.Helper()
	for _, listener := range current.Listeners() {
		if listener.Class == class && listener.Method == method {
			return
		}
	}
	t.Fatalf(
		"event listener %s::%s not found in %#v",
		class,
		method,
		current.Listeners(),
	)
}

func requireFormOption(
	t *testing.T,
	options []form.Option,
	name string,
) {
	t.Helper()
	for _, option := range options {
		if option.Name == name {
			return
		}
	}
	t.Fatalf("form option %q not found in %#v", name, options)
}

func requireSemanticTokenText(
	t *testing.T,
	document *lsp.TextDocument,
	tokens []lsp.SemanticToken,
	text string,
	tokenType uint32,
) {
	t.Helper()
	for _, token := range tokens {
		if token.Type != tokenType || token.Range.End > uint32(len(document.Text)) {
			continue
		}
		if string(document.Text[token.Range.Start:token.Range.End]) == text {
			return
		}
	}
	t.Fatalf("semantic token %q with type %d not found", text, tokenType)
}

func requireAnalyticsFormType(
	t *testing.T,
	types []analytics.FormTypeCatalogEntry,
	name string,
) analytics.FormTypeCatalogEntry {
	t.Helper()
	for _, current := range types {
		if current.Name == name {
			return current
		}
	}
	t.Fatalf("form type catalog entry %q not found in %#v", name, types)
	return analytics.FormTypeCatalogEntry{}
}

func requireAnalyticsFormOption(
	t *testing.T,
	options []analytics.FormOptionCatalogEntry,
	name string,
) analytics.FormOptionCatalogEntry {
	t.Helper()
	for _, current := range options {
		if current.Name == name {
			return current
		}
	}
	t.Fatalf("form option catalog entry %q not found in %#v", name, options)
	return analytics.FormOptionCatalogEntry{}
}

func realWorldDecodeCommandResponse(
	t *testing.T,
	value any,
	target any,
) {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, target))
}

func requireAnalyticsService(
	t *testing.T,
	services []analytics.ServiceLocatorEntry,
	id string,
) analytics.ServiceLocatorEntry {
	t.Helper()
	for _, current := range services {
		if current.ID == id {
			return current
		}
	}
	t.Fatalf("service locator entry %q not found in %#v", id, services)
	return analytics.ServiceLocatorEntry{}
}

func requireConsoleInput(
	t *testing.T,
	inputs []console.Input,
	name string,
) {
	t.Helper()
	for _, input := range inputs {
		if input.Name == name {
			return
		}
	}
	t.Fatalf("Console input %q not found in %#v", name, inputs)
}
