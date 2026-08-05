//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/analytics"
	"github.com/shopware/shopware-lsp/internal/appscript"
	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/httpclient"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	lspcallhierarchy "github.com/shopware/shopware-lsp/internal/lsp/callhierarchy"
	"github.com/shopware/shopware-lsp/internal/lsp/codeaction"
	"github.com/shopware/shopware-lsp/internal/lsp/codelens"
	lspcolor "github.com/shopware/shopware-lsp/internal/lsp/color"
	lspcompletion "github.com/shopware/shopware-lsp/internal/lsp/completion"
	lspdefinition "github.com/shopware/shopware-lsp/internal/lsp/definition"
	lspdiagnostics "github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	lspdocumentlink "github.com/shopware/shopware-lsp/internal/lsp/documentlink"
	lspfolding "github.com/shopware/shopware-lsp/internal/lsp/folding"
	lsphighlight "github.com/shopware/shopware-lsp/internal/lsp/highlight"
	lsphover "github.com/shopware/shopware-lsp/internal/lsp/hover"
	lspinlay "github.com/shopware/shopware-lsp/internal/lsp/inlay"
	lsplinkedediting "github.com/shopware/shopware-lsp/internal/lsp/linkedediting"
	lspphpsemantic "github.com/shopware/shopware-lsp/internal/lsp/phpsemantic"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	lsprefactor "github.com/shopware/shopware-lsp/internal/lsp/refactor"
	lspreference "github.com/shopware/shopware-lsp/internal/lsp/reference"
	"github.com/shopware/shopware-lsp/internal/lsp/scaffold"
	lspselection "github.com/shopware/shopware-lsp/internal/lsp/selection"
	lspsemantic "github.com/shopware/shopware-lsp/internal/lsp/semantic"
	lspsignature "github.com/shopware/shopware-lsp/internal/lsp/signature"
	lspsymbol "github.com/shopware/shopware-lsp/internal/lsp/symbol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/pathmatch"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/security"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

type realWorldFormCandidate struct {
	Name          string `json:"name"`
	SuggestedType string `json:"suggestedType"`
}

type realWorldTranslationExtractionPreparation struct {
	Text          string         `json:"text"`
	DefaultDomain string         `json:"defaultDomain"`
	Domains       []string       `json:"domains"`
	Range         protocol.Range `json:"range"`
}

type realWorldTranslationExtractionTarget struct {
	FileURI string `json:"fileUri"`
	NewText string `json:"newText"`
}

type realWorldTranslationExtractionEdits struct {
	Replacement string                                 `json:"replacement"`
	Targets     []realWorldTranslationExtractionTarget `json:"targets"`
}

// TestShopwareTrunkIndexing exercises the production workspace composition
// against a real Shopware checkout. It is opt-in because the fixture is large
// and developer-local:
//
//	go test -tags=integration ./internal/app -run TestShopwareTrunkIndexing -v
//
// SHOPWARE_LSP_REAL_WORLD_ROOT overrides the default ~/Developer/sw-trunk.
func TestShopwareTrunkIndexing(t *testing.T) {
	root := realWorldProjectRoot(t)
	cacheRoot := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", cacheRoot)
	ctx := context.Background()

	workspace, phpIndex, realWorldServer := openRealWorldWorkspaceWithServer(
		t, ctx, root,
	)
	coldStarted := time.Now()
	require.NoError(t, workspace.Scanner().IndexAll(ctx))
	coldElapsed := time.Since(coldStarted)

	classes := phpIndex.ClassSymbols()
	require.Greater(t, len(classes), 5_000)
	classCount := len(classes)
	_, found := phpIndex.FindClass("Shopware\\Core\\Kernel")
	require.True(t, found, "the production Shopware kernel must be indexed")
	adminComponents, err := workspaceAdminIndex(t, workspace).GetAllComponents()
	require.NoError(t, err)
	require.NotEmpty(t, adminComponents)
	vueScriptSetupPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/adapter/_mocks_/example-extendable-script-setup-component.vue",
	)
	vueScriptSetup, err := workspaceAdminIndex(t, workspace).
		GetComponentDefinition(vueScriptSetupPath)
	require.NoError(t, err)
	require.NotNil(t, vueScriptSetup, "real Administration Vue SFC must be indexed")
	require.Equal(t, vueScriptSetupPath, vueScriptSetup.TemplatePath)
	require.Contains(t, adminPropNames(vueScriptSetup.Props), "multiplier")
	require.Contains(t, adminPropNames(vueScriptSetup.Props), "added")
	for _, memberName := range []string{
		"baseValue", "multipliedValue", "addedValue", "reactiveValue",
		"increment", "privateStuff", "message",
	} {
		_, found := componentDefinitionMemberNamed(vueScriptSetup, memberName)
		require.Truef(t, found, "script-setup member %s must be indexed", memberName)
	}
	adminTemplateAnalyzer := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	)
	adminTemplatePaths := make(map[string]bool)
	adminComponentNames := make(map[string]bool)
	adminTemplateProblemCounts := make(map[string]int)
	adminTemplateProblemSamples := make(map[string][]string)
	adminMissingRequiredPropCounts := make(map[string]int)
	adminMissingRequiredPropSamples := make(map[string]string)
	var unknownTemplateMemberDiagnostics []string
	adminEffectiveComponentCount := 0
	adminPropCount := 0
	adminTypedPropCount := 0
	adminRequiredPropCount := 0
	adminDocumentedPropCount := 0
	adminExactPropRangeCount := 0
	adminIdentifierPropRangeCount := 0
	adminClosedValuePropCount := 0
	adminEventCount := 0
	adminExactEventRangeCount := 0
	adminSlotCount := 0
	adminExactSlotRangeCount := 0
	adminSlotMemberCount := 0
	adminExactSlotMemberRangeCount := 0
	var adminMissingExactPropRanges []string
	var adminMissingExactEventRanges []string
	var adminMissingExactSlotRanges []string
	var adminMissingExactSlotMemberRanges []string
	for _, registration := range adminComponents {
		if adminComponentNames[registration.Name] {
			continue
		}
		adminComponentNames[registration.Name] = true
		component, componentErr := workspaceAdminIndex(t, workspace).
			GetEffectiveComponent(registration.Name)
		require.NoError(t, componentErr)
		if component != nil {
			adminEffectiveComponentCount++
			for _, prop := range component.Props {
				adminPropCount++
				if strings.TrimSpace(prop.Documentation) != "" {
					adminDocumentedPropCount++
				}
				if strings.TrimSpace(prop.Type) != "" {
					adminTypedPropCount++
				}
				if prop.Required {
					adminRequiredPropCount++
				}
				hasExactRange := prop.NameRange.Declaration &&
					(prop.NameRange.EndLine > prop.NameRange.StartLine ||
						prop.NameRange.EndLine == prop.NameRange.StartLine &&
							prop.NameRange.EndCharacter >
								prop.NameRange.StartCharacter)
				if hasExactRange {
					adminExactPropRangeCount++
				} else {
					adminMissingExactPropRanges = append(
						adminMissingExactPropRanges,
						fmt.Sprintf(
							"%s.%s (%s:%d, range=%+v)",
							component.Name, prop.Name,
							strings.TrimPrefix(
								filepath.ToSlash(prop.FilePath),
								filepath.ToSlash(root)+"/",
							),
							prop.Line, prop.NameRange,
						),
					)
				}
				if prop.NameRange.Identifier && hasExactRange {
					adminIdentifierPropRangeCount++
				}
				if _, complete := admin.VuePropAllowedValues(prop); complete {
					adminClosedValuePropCount++
				}
			}
			for _, event := range component.ComponentEvents() {
				adminEventCount++
				hasExactRange := event.NameRange.Declaration &&
					(event.NameRange.EndLine > event.NameRange.StartLine ||
						event.NameRange.EndLine == event.NameRange.StartLine &&
							event.NameRange.EndCharacter >
								event.NameRange.StartCharacter)
				if hasExactRange {
					adminExactEventRangeCount++
					continue
				}
				adminMissingExactEventRanges = append(
					adminMissingExactEventRanges,
					fmt.Sprintf(
						"%s.%s (%s:%d)", component.Name,
						admin.CanonicalEventName(event.Name),
						strings.TrimPrefix(
							filepath.ToSlash(event.FilePath),
							filepath.ToSlash(root)+"/",
						),
						event.Line,
					),
				)
			}
			for _, slot := range component.Slots {
				adminSlotCount++
				hasExactRange := slot.NameRange.Declaration &&
					(slot.NameRange.EndLine > slot.NameRange.StartLine ||
						slot.NameRange.EndLine == slot.NameRange.StartLine &&
							slot.NameRange.EndCharacter >
								slot.NameRange.StartCharacter)
				if hasExactRange {
					adminExactSlotRangeCount++
				} else {
					adminMissingExactSlotRanges = append(
						adminMissingExactSlotRanges,
						fmt.Sprintf(
							"%s.%s (%s:%d)", component.Name,
							slot.DisplayName(),
							strings.TrimPrefix(
								filepath.ToSlash(slot.FilePath),
								filepath.ToSlash(root)+"/",
							),
							slot.Line,
						),
					)
				}
				for _, member := range slot.Members {
					adminSlotMemberCount++
					memberHasExactRange := member.NameRange.Declaration &&
						(member.NameRange.EndLine > member.NameRange.StartLine ||
							member.NameRange.EndLine == member.NameRange.StartLine &&
								member.NameRange.EndCharacter >
									member.NameRange.StartCharacter)
					if memberHasExactRange {
						adminExactSlotMemberRangeCount++
						continue
					}
					adminMissingExactSlotMemberRanges = append(
						adminMissingExactSlotMemberRanges,
						fmt.Sprintf(
							"%s.%s.%s (%s:%d)", component.Name,
							slot.DisplayName(), member.Name,
							strings.TrimPrefix(
								filepath.ToSlash(member.FilePath),
								filepath.ToSlash(root)+"/",
							),
							member.Line,
						),
					)
				}
			}
		}
		if component == nil || component.TemplatePath == "" ||
			adminTemplatePaths[component.TemplatePath] {
			continue
		}
		adminTemplatePaths[component.TemplatePath] = true
		source, readErr := os.ReadFile(component.TemplatePath)
		require.NoError(t, readErr)
		document := lsp.NewTextDocument(
			uriutil.FileURI(component.TemplatePath), string(source), 1,
		)
		problems, analyzeErr := adminTemplateAnalyzer.Analyze(ctx, document)
		require.NoError(t, analyzeErr)
		relativeTemplatePath := strings.TrimPrefix(
			filepath.ToSlash(component.TemplatePath),
			filepath.ToSlash(root)+"/",
		)
		for _, problem := range problems {
			problemID := string(problem.ID)
			adminTemplateProblemCounts[problemID]++
			if problemID == "admin.component.missing-required-prop" {
				if payload, ok := problem.Payload.(map[string]any); ok {
					componentName, _ := payload["componentName"].(string)
					propName, _ := payload["propName"].(string)
					if componentName != "" && propName != "" {
						componentProp := componentName + "." + propName
						adminMissingRequiredPropCounts[componentProp]++
						if adminMissingRequiredPropSamples[componentProp] == "" {
							adminMissingRequiredPropSamples[componentProp] =
								relativeTemplatePath
						}
					}
				}
			}
			sampleLimit := 5
			if problemID == "admin.component.unknown-slot-prop" ||
				problemID == "admin.component.unknown-vue-member" {
				sampleLimit = 20
			}
			if len(adminTemplateProblemSamples[problemID]) < sampleLimit {
				sample := fmt.Sprintf(
					"%s: %s", relativeTemplatePath, problem.Message,
				)
				if problemID == "admin.component.unknown-vue-member" &&
					component != nil {
					if payload, ok := problem.Payload.(map[string]any); ok {
						bindingName, _ := payload["bindingName"].(string)
						if member, found := component.TemplateMember(bindingName); found {
							sample += fmt.Sprintf(
								" [type=%q, open=%t, source=%q, context=%s]",
								member.Type, member.OpenRuntimeShape,
								member.SourceExpression,
								strings.TrimPrefix(
									filepath.ToSlash(member.TypeContextPath),
									filepath.ToSlash(root)+"/",
								),
							)
						}
					}
				}
				adminTemplateProblemSamples[problemID] = append(
					adminTemplateProblemSamples[problemID], sample,
				)
			}
			if string(problem.ID) !=
				"admin.component.unknown-template-member" {
				continue
			}
			unknownTemplateMemberDiagnostics = append(
				unknownTemplateMemberDiagnostics,
				fmt.Sprintf("%s: %s", relativeTemplatePath, problem.Message),
			)
		}
	}
	require.GreaterOrEqual(t, adminEffectiveComponentCount, 1_000)
	require.GreaterOrEqual(t, adminPropCount, 3_000)
	require.GreaterOrEqual(t, adminTypedPropCount, 2_900)
	require.GreaterOrEqual(t, adminRequiredPropCount, 700)
	require.GreaterOrEqual(t, adminDocumentedPropCount, 10)
	require.Zero(
		t, adminTemplateProblemCounts["admin.module-route.not-found"],
		"all static module routes in the real project must be indexed",
	)
	require.Zero(
		t, adminTemplateProblemCounts["admin.privilege.not-found"],
		"Shopware's built-in admin ACL privilege must be recognized",
	)
	require.Equal(t, adminPropCount, adminExactPropRangeCount)
	require.GreaterOrEqual(t, adminClosedValuePropCount, 50)
	require.Positive(t, adminEventCount)
	sort.Strings(adminMissingExactPropRanges)
	require.Empty(t, adminMissingExactPropRanges)
	sort.Strings(adminMissingExactEventRanges)
	require.Empty(t, adminMissingExactEventRanges)
	require.Equal(t, adminEventCount, adminExactEventRangeCount)
	require.Positive(t, adminSlotCount)
	sort.Strings(adminMissingExactSlotRanges)
	require.Empty(t, adminMissingExactSlotRanges)
	require.Equal(t, adminSlotCount, adminExactSlotRangeCount)
	require.Positive(t, adminSlotMemberCount)
	sort.Strings(adminMissingExactSlotMemberRanges)
	require.Empty(t, adminMissingExactSlotMemberRanges)
	require.Equal(
		t, adminSlotMemberCount, adminExactSlotMemberRangeCount,
	)
	t.Logf(
		"Administration component contracts: effective=%d, props=%d, typed=%d, required=%d, documented=%d, exact_prop_ranges=%d, identifier_prop_ranges=%d, closed_values=%d, events=%d, exact_event_ranges=%d, slots=%d, exact_slot_ranges=%d, slot_members=%d, exact_slot_member_ranges=%d",
		adminEffectiveComponentCount,
		adminPropCount,
		adminTypedPropCount,
		adminRequiredPropCount,
		adminDocumentedPropCount,
		adminExactPropRangeCount,
		adminIdentifierPropRangeCount,
		adminClosedValuePropCount,
		adminEventCount,
		adminExactEventRangeCount,
		adminSlotCount,
		adminExactSlotRangeCount,
		adminSlotMemberCount,
		adminExactSlotMemberRangeCount,
	)
	adminTemplateProblemIDs := make([]string, 0, len(adminTemplateProblemCounts))
	for problemID := range adminTemplateProblemCounts {
		adminTemplateProblemIDs = append(adminTemplateProblemIDs, problemID)
	}
	sort.Strings(adminTemplateProblemIDs)
	for _, problemID := range adminTemplateProblemIDs {
		t.Logf(
			"Administration template diagnostics: %s=%d samples=%s",
			problemID, adminTemplateProblemCounts[problemID],
			strings.Join(adminTemplateProblemSamples[problemID], " | "),
		)
	}
	adminMissingRequiredProps := make(
		[]string, 0, len(adminMissingRequiredPropCounts),
	)
	for componentProp := range adminMissingRequiredPropCounts {
		adminMissingRequiredProps = append(
			adminMissingRequiredProps, componentProp,
		)
	}
	sort.Strings(adminMissingRequiredProps)
	for _, componentProp := range adminMissingRequiredProps {
		t.Logf(
			"Administration missing required prop: %s=%d sample=%s",
			componentProp, adminMissingRequiredPropCounts[componentProp],
			adminMissingRequiredPropSamples[componentProp],
		)
	}
	require.Empty(
		t, unknownTemplateMemberDiagnostics,
		"indexed Administration template scope should not report typo false positives",
	)
	cmsElements, err := workspaceAdminIndex(t, workspace).
		GetAllCMSRegistrationsByKind(admin.AdminCMSElement)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cmsElements), 20)
	cmsBlocks, err := workspaceAdminIndex(t, workspace).
		GetAllCMSRegistrationsByKind(admin.AdminCMSBlock)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cmsBlocks), 30)
	imageElements, err := workspaceAdminIndex(t, workspace).
		GetCMSRegistration(admin.AdminCMSElement, "image")
	require.NoError(t, err)
	require.NotEmpty(t, imageElements)
	require.Equal(t, "sw-cms-el-image", imageElements[0].Component)
	require.Equal(t, "sw-cms-el-config-image", imageElements[0].ConfigComponent)
	require.Equal(t, "sw-cms-el-preview-image", imageElements[0].PreviewComponent)
	imageComponentUsages, err := workspaceAdminIndex(t, workspace).GetSymbolUsages(
		admin.AdminSymbolTarget{
			Kind: admin.AdminSymbolComponent, Name: "sw-cms-el-image",
		},
	)
	require.NoError(t, err)
	imageCMSLinkIndexed := false
	for _, set := range imageComponentUsages {
		if filepath.Clean(set.FilePath) != filepath.Clean(imageElements[0].FilePath) {
			continue
		}
		for _, occurrence := range set.Occurrences {
			if !occurrence.Declaration {
				imageCMSLinkIndexed = true
			}
		}
	}
	require.True(t, imageCMSLinkIndexed, "CMS component link must be indexed as a component usage")
	imageBlocks, err := workspaceAdminIndex(t, workspace).
		GetCMSRegistration(admin.AdminCMSBlock, "image-two-column")
	require.NoError(t, err)
	require.NotEmpty(t, imageBlocks)
	require.Equal(t, "sw-cms-preview-image-two-column", imageBlocks[0].PreviewComponent)
	require.Len(t, imageBlocks[0].Slots, 2)
	productComponents, err := workspaceAdminIndex(t, workspace).GetComponent(
		"sw-product-list",
	)
	require.NoError(t, err)
	require.NotEmpty(t, productComponents)
	roleGeneral, err := workspaceAdminIndex(t, workspace).GetEffectiveComponent(
		"sw-users-permissions-role-view-general",
	)
	require.NoError(t, err)
	require.NotNil(t, roleGeneral)
	require.Contains(t, adminPropNames(roleGeneral.Props), "role")
	require.Contains(t, adminPropNames(roleGeneral.Props), "isLoading")
	require.Contains(t, roleGeneral.Data, "mcpIntegrations")
	require.Contains(t, roleGeneral.Data, "showMcpModal")
	require.Contains(t, roleGeneral.Injected, "repositoryFactory")
	require.Contains(t, roleGeneral.Computed, "roleId")
	require.Contains(t, roleGeneral.Methods, "onOpenMcpModal")
	require.True(t, strings.HasSuffix(
		filepath.ToSlash(roleGeneral.TemplatePath),
		"/sw-users-permissions-role-view-general.html.twig",
	))
	contractTypoDocument := lsp.NewTextDocument(
		uriutil.FileURI(roleGeneral.TemplatePath),
		`<sw-users-permissions-role-view-general :role="role" :is-laoding="isLoading" />`,
		1,
	)
	contractTypoProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, contractTypoDocument)
	require.NoError(t, err)
	require.Len(t, contractTypoProblems, 1)
	require.Equal(
		t, "admin.component.unknown-prop",
		string(contractTypoProblems[0].ID),
	)
	require.Contains(
		t,
		contractTypoProblems[0].Payload.(map[string]any)["suggestions"],
		"is-loading",
	)
	cmsSlot, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-cms-slot")
	require.NoError(t, err)
	require.NotNil(t, cmsSlot)
	elementConfig, found := cmsSlot.TemplateMember("elementConfig")
	require.True(t, found)
	require.Equal(t, admin.AdminCMSElement, elementConfig.CMSRegistryKind)
	cmsRegistry, found := cmsSlot.TemplateMember("cmsElements")
	require.True(t, found)
	require.Equal(t, admin.AdminCMSElement, cmsRegistry.CMSRegistryKind)
	validationMixins, err := workspaceAdminIndex(t, workspace).GetMixin("validation")
	require.NoError(t, err)
	require.NotEmpty(t, validationMixins)
	require.Contains(t, adminPropNames(validationMixins[0].Definition.Props), "validation")
	require.Contains(t, validationMixins[0].Definition.Injected, "validationService")
	require.Contains(t, validationMixins[0].Definition.Methods, "validate")
	require.Contains(t, validationMixins[0].Definition.Methods, "validateRule")
	loginComponent, err := workspaceAdminIndex(t, workspace).GetEffectiveComponent(
		"sw-login-login",
	)
	require.NoError(t, err)
	require.NotNil(t, loginComponent)
	require.Contains(t, loginComponent.Data, "loginConfig")
	require.Contains(t, loginComponent.Methods, "doSsoForwarding")
	require.Contains(t, loginComponent.Emits, "login-success")
	modelEditor, err := workspaceAdminIndex(t, workspace).GetEffectiveComponent(
		"sw-model-editor",
	)
	require.NoError(t, err)
	require.NotNil(t, modelEditor)
	changePosition, found := modelEditor.TemplateMember("changeModelPosition")
	require.True(t, found)
	require.Equal(
		t,
		"(position: { x: number; y: number; z: number }) => void",
		changePosition.Type,
	)
	modelSignatureSource := `<button @click="changeModelPosition({ x: 1, y: 2, z: 3 })" />`
	modelSignatureDocument := lsp.NewTextDocument(
		uriutil.FileURI(modelEditor.TemplatePath), modelSignatureSource, 1,
	)
	modelSignatureOffset := uint32(
		strings.Index(modelSignatureSource, "z: 3") + len("z: "),
	)
	modelSignature, err := lspsignature.NewAdminSignatureProvider(
		workspaceAdminIndex(t, workspace),
	).GetSignatureHelp(
		ctx, realWorldSignatureRequest(
			modelSignatureDocument, modelSignatureOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, modelSignature)
	require.Len(t, modelSignature.Signatures, 1)
	require.Equal(
		t,
		"changeModelPosition(position: { x: number; y: number; z: number }): void",
		modelSignature.Signatures[0].Label,
	)
	modelInlayParams := &protocol.InlayHintParams{}
	modelInlayParams.TextDocument.URI = modelSignatureDocument.URI
	modelInlayEndLine, modelInlayEndCharacter :=
		modelSignatureDocument.LineIndex.PositionUTF16(
			uint32(len(modelSignatureSource)),
		)
	modelInlayParams.Range.End = protocol.Position{
		Line: int(modelInlayEndLine), Character: int(modelInlayEndCharacter),
	}
	modelInlayHints, err := lspinlay.NewAdminParameterProvider(
		workspaceAdminIndex(t, workspace),
	).GetInlayHints(ctx, &lsp.InlayHintRequest{
		InlayHintParams: modelInlayParams,
		Document:        modelSignatureDocument,
	})
	require.NoError(t, err)
	require.Len(t, modelInlayHints, 1)
	require.Equal(t, "position:", modelInlayHints[0].Label)
	require.Contains(
		t, modelInlayHints[0].Tooltip,
		"changeModelPosition(position: { x: number; y: number; z: number }): void",
	)
	slotCard, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-card")
	require.NoError(t, err)
	require.NotNil(t, slotCard)
	require.Contains(t, slotCard.Deprecated, "use mt-card instead")
	_, found = slotCard.ComponentSlot("default")
	require.True(t, found)
	entityListing, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-entity-listing")
	require.NoError(t, err)
	require.NotNil(t, entityListing)
	deprecatedItems, found := entityListing.ComponentProp("items")
	require.True(t, found)
	require.Contains(t, deprecatedItems.Deprecated, "Use `dataSource` prop instead")
	deprecatedBlockComponent, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-newsletter-recipient-filter-switch")
	require.NoError(t, err)
	require.NotNil(t, deprecatedBlockComponent)
	deprecatedAdminBlock, found := deprecatedBlockComponent.ComponentBlock(
		"sw_newsletter_recipient_filter_switch_field",
	)
	require.True(t, found)
	require.Contains(
		t, deprecatedAdminBlock.Deprecated,
		"Block will be removed without replacement",
	)
	require.True(t, deprecatedAdminBlock.NameRange.Identifier)
	require.True(t, strings.HasSuffix(
		filepath.ToSlash(deprecatedAdminBlock.FilePath),
		"/sw-newsletter-recipient-filter-switch.html.twig",
	))
	deprecatedMemberComponent, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-settings-snippet-set-list")
	require.NoError(t, err)
	require.NotNil(t, deprecatedMemberComponent)
	deprecatedAdminMember, found := deprecatedMemberComponent.TemplateMember(
		"getNoPermissionsTooltip",
	)
	require.True(t, found)
	require.Equal(t, admin.ComponentMemberMethod, deprecatedAdminMember.Kind)
	require.Contains(
		t, deprecatedAdminMember.Deprecated,
		"Will be removed without replacement",
	)
	require.True(t, deprecatedAdminMember.NameRange.Identifier)
	require.Positive(t, deprecatedAdminMember.Line)
	require.True(t, strings.HasSuffix(
		filepath.ToSlash(deprecatedAdminMember.FilePath),
		"/sw-settings-snippet-set-list/index.js",
	))
	checkboxField, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-checkbox-field")
	require.NoError(t, err)
	require.NotNil(t, checkboxField)
	_, found = checkboxField.ComponentModel("v-model:model-value")
	require.True(t, found)
	contractArgumentTypoDocument := lsp.NewTextDocument(
		uriutil.FileURI(slotCard.TemplatePath),
		`<sw-card><template #defualt>Content</template></sw-card><sw-checkbox-field v-model:modle-value="value" />`,
		1,
	)
	contractArgumentProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, contractArgumentTypoDocument)
	require.NoError(t, err)
	contractArgumentSuggestions := make(map[string]any)
	for _, problem := range contractArgumentProblems {
		payload, payloadOK := problem.Payload.(map[string]any)
		if !payloadOK || payload["suggestions"] == nil {
			continue
		}
		contractArgumentSuggestions[string(problem.ID)] = payload["suggestions"]
	}
	require.Contains(
		t, contractArgumentSuggestions["admin.component.unknown-slot"],
		"default",
	)
	require.Contains(
		t, contractArgumentSuggestions["admin.component.unknown-model"],
		"model-value",
	)
	deprecationDocument := lsp.NewTextDocument(
		uriutil.FileURI(slotCard.TemplatePath),
		`<sw-card></sw-card><sw-entity-listing :items="items" :repository="repository" />`,
		2,
	)
	deprecationProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, deprecationDocument)
	require.NoError(t, err)
	deprecationIDs := make(map[string]bool)
	for _, problem := range deprecationProblems {
		if len(problem.Tags) == 1 &&
			problem.Tags[0] == protocol.DiagnosticTagDeprecated {
			deprecationIDs[string(problem.ID)] = true
			require.Equal(t, protocol.DiagnosticSeverityHint, problem.Severity)
		}
	}
	require.True(t, deprecationIDs["admin.component.deprecated"])
	require.True(t, deprecationIDs["admin.component.deprecated-prop"])
	deprecatedMemberSource := `{{ getNoPermissionsTooltip(role) }}`
	deprecatedMemberDocument := lsp.NewTextDocument(
		uriutil.FileURI(deprecatedMemberComponent.TemplatePath),
		deprecatedMemberSource,
		3,
	)
	deprecatedMemberProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, deprecatedMemberDocument)
	require.NoError(t, err)
	deprecatedMemberProblemFound := false
	for _, problem := range deprecatedMemberProblems {
		if string(problem.ID) != "admin.component.deprecated-member" {
			continue
		}
		deprecatedMemberProblemFound = true
		require.Equal(t, protocol.DiagnosticSeverityHint, problem.Severity)
		require.Equal(
			t,
			[]protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
			problem.Tags,
		)
		require.Contains(t, problem.Message, "Will be removed without replacement")
	}
	require.True(t, deprecatedMemberProblemFound)
	deprecatedMemberCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			deprecatedMemberDocument,
			uint32(strings.Index(
				deprecatedMemberSource, "getNoPermissionsTooltip",
			)+len("getNoPermissionsTool")),
		),
	)
	deprecatedMemberCompletion := realWorldCompletionByLabel(
		t, deprecatedMemberCompletions, "getNoPermissionsTooltip",
	)
	require.True(t, deprecatedMemberCompletion.Deprecated)
	require.Contains(
		t, deprecatedMemberCompletion.Documentation.Value,
		"Will be removed without replacement",
	)
	deprecatedMemberHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			deprecatedMemberDocument,
			uint32(strings.Index(
				deprecatedMemberSource, "getNoPermissionsTooltip",
			)+len("getNoPermissions")),
		),
	)
	require.NoError(t, err)
	require.NotNil(t, deprecatedMemberHover)
	require.Contains(t, deprecatedMemberHover.Contents.Value, "Deprecated")
	require.Contains(
		t, deprecatedMemberHover.Contents.Value,
		"Will be removed without replacement",
	)
	getSlotsMember, slotMemberFound := slotCard.TemplateMember("getSlots")
	require.True(t, slotMemberFound)
	require.Equal(
		t, "() => Record<string, Function | undefined>", getSlotsMember.Type,
	)
	require.NotEmpty(t, slotCard.TemplatePath)
	slotCardTemplateSource, err := os.ReadFile(slotCard.TemplatePath)
	require.NoError(t, err)
	slotCardTemplateDocument := lsp.NewTextDocument(
		uriutil.FileURI(slotCard.TemplatePath), string(slotCardTemplateSource), 1,
	)
	slotCardNamePosition := strings.Index(
		string(slotCardTemplateSource), `:name="name"`,
	)
	require.NotEqual(t, -1, slotCardNamePosition)
	slotCardNameOffset := uint32(slotCardNamePosition + len(`:name="na`))
	slotCardNameBinding, err := workspaceAdminIndex(t, workspace).
		ResolveTwigVueBinding(
			slotCardTemplateDocument.SyntaxTree.Root,
			slotCardTemplateDocument.Text,
			slotCardNameOffset,
			slotCard.TemplatePath,
		)
	require.NoError(t, err)
	require.NotNil(t, slotCardNameBinding)
	require.Equal(t, "string", slotCardNameBinding.Type)
	slotLoopSource := `<div v-for="(slot, name, index) in getSlots()"><span :title="name.length" :data-name="name."></span><span :data-index="index."></span></div>`
	slotLoopDocument := lsp.NewTextDocument(
		uriutil.FileURI(slotCard.TemplatePath), slotLoopSource, 1,
	)
	slotNameOffset := uint32(
		strings.LastIndex(slotLoopSource, "name.") + len("name."),
	)
	slotNameUsageOffset := uint32(
		strings.Index(slotLoopSource, "name.length") + 1,
	)
	slotNameBinding, err := workspaceAdminIndex(t, workspace).
		ResolveTwigVueBinding(
			slotLoopDocument.SyntaxTree.Root,
			slotLoopDocument.Text,
			slotNameUsageOffset,
			slotCard.TemplatePath,
		)
	require.NoError(t, err)
	require.NotNil(t, slotNameBinding)
	require.Equal(t, "string", slotNameBinding.Type)
	slotNameCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx, realWorldCompletionRequest(slotLoopDocument, slotNameOffset),
	)
	slotNameLabels := realWorldCompletionLabels(slotNameCompletions)
	for _, member := range []string{"length", "toLowerCase", "trim"} {
		require.Contains(t, slotNameLabels, member)
	}
	slotIndexOffset := uint32(
		strings.Index(slotLoopSource, "index.") + len("index."),
	)
	slotIndexCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx, realWorldCompletionRequest(slotLoopDocument, slotIndexOffset),
	)
	require.Contains(
		t, realWorldCompletionLabels(slotIndexCompletions), "toFixed",
	)
	slotNameLengthOffset := uint32(
		strings.Index(slotLoopSource, "name.length") + len("name.len"),
	)
	slotNameHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(slotLoopDocument, slotNameLengthOffset),
	)
	require.NoError(t, err)
	require.NotNil(t, slotNameHover)
	require.Contains(
		t, slotNameHover.Contents.Value,
		"**property** `name.length`: `number`",
	)
	objectKeysLoopSource := `<div v-for="slotName in Object.keys(getSlots())"><span :title="slotName.length" :data-name="slotName."></span></div>`
	objectKeysLoopDocument := lsp.NewTextDocument(
		uriutil.FileURI(slotCard.TemplatePath), objectKeysLoopSource, 1,
	)
	objectKeysNameOffset := uint32(
		strings.LastIndex(objectKeysLoopSource, "slotName.") + len("slotName."),
	)
	objectKeysNameCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(objectKeysLoopDocument, objectKeysNameOffset),
	)
	for _, member := range []string{"length", "toLowerCase", "trim"} {
		require.Contains(
			t, realWorldCompletionLabels(objectKeysNameCompletions), member,
		)
	}
	dashboard, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-dashboard-index")
	require.NoError(t, err)
	require.NotNil(t, dashboard)
	require.NotEmpty(t, dashboard.TemplatePath)
	dashboardTemplateSource, err := os.ReadFile(dashboard.TemplatePath)
	require.NoError(t, err)
	dashboardTemplateDocument := lsp.NewTextDocument(
		uriutil.FileURI(dashboard.TemplatePath), string(dashboardTemplateSource), 1,
	)
	dashboardKeyPosition := strings.Index(
		string(dashboardTemplateSource), "${key}Link",
	)
	require.NotEqual(t, -1, dashboardKeyPosition)
	dashboardKeyBinding, err := workspaceAdminIndex(t, workspace).
		ResolveTwigVueBinding(
			dashboardTemplateDocument.SyntaxTree.Root,
			dashboardTemplateDocument.Text,
			uint32(dashboardKeyPosition+len("${k")),
			dashboard.TemplatePath,
		)
	require.NoError(t, err)
	require.NotNil(t, dashboardKeyBinding)
	require.Equal(t, "string", dashboardKeyBinding.Type)
	topbarSidebar, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-app-topbar-sidebar")
	require.NoError(t, err)
	require.NotNil(t, topbarSidebar)
	sidebarsMember, sidebarsFound := topbarSidebar.TemplateMember("sidebars")
	require.True(t, sidebarsFound)
	require.Contains(t, sidebarsMember.Type, "SidebarItemEntry")
	require.NotEmpty(t, topbarSidebar.TemplatePath)
	topbarSidebarSource, err := os.ReadFile(topbarSidebar.TemplatePath)
	require.NoError(t, err)
	topbarSidebarDocument := lsp.NewTextDocument(
		uriutil.FileURI(topbarSidebar.TemplatePath),
		string(topbarSidebarSource),
		1,
	)
	topbarActivePosition := strings.Index(
		string(topbarSidebarSource), "sidebars[0].active",
	)
	require.NotEqual(t, -1, topbarActivePosition)
	topbarActiveOffset := uint32(
		topbarActivePosition + len("sidebars[0].ac"),
	)
	topbarActive, err := workspaceAdminIndex(t, workspace).
		ResolveTwigVueInstanceMember(
			topbarSidebarDocument.SyntaxTree.Root,
			topbarSidebarDocument.Text,
			topbarActiveOffset,
			topbarSidebar.TemplatePath,
		)
	require.NoError(t, err)
	require.NotNil(t, topbarActive)
	require.True(t, topbarActive.MemberFound)
	require.Equal(t, "boolean", topbarActive.Member.Type)
	require.Equal(t, "sidebars[0].active", topbarActive.QualifiedName())
	require.True(t, strings.HasSuffix(
		filepath.ToSlash(topbarActive.Member.DefinitionPath),
		"/app/store/sidebar.store.ts",
	))
	topbarCompletionSource := `<div :class="sidebars[0]."></div>`
	topbarCompletionDocument := lsp.NewTextDocument(
		uriutil.FileURI(topbarSidebar.TemplatePath),
		topbarCompletionSource,
		1,
	)
	topbarCompletionOffset := uint32(
		strings.Index(topbarCompletionSource, "sidebars[0].") +
			len("sidebars[0]."),
	)
	topbarCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			topbarCompletionDocument, topbarCompletionOffset,
		),
	)
	topbarLabels := realWorldCompletionLabels(topbarCompletions)
	for _, member := range []string{"active", "baseUrl"} {
		require.Contains(t, topbarLabels, member)
	}
	topbarHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			topbarSidebarDocument, topbarActiveOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, topbarHover)
	require.Contains(
		t, topbarHover.Contents.Value,
		"**property** `sidebars[0].active`: `boolean`",
	)
	topbarDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			topbarSidebarDocument,
			topbarSidebarDocument.SyntaxTree.Root.NodeAtOffset(
				topbarActiveOffset,
			),
			topbarActiveOffset,
		),
	)
	require.Len(t, topbarDefinitions, 1)
	require.True(t, strings.HasSuffix(
		filepath.ToSlash(topbarDefinitions[0].URI),
		"/app/store/sidebar.store.ts",
	))
	topbarProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, topbarSidebarDocument)
	require.NoError(t, err)
	for _, problem := range topbarProblems {
		require.NotEqual(
			t, "admin.component.unknown-vue-member", string(problem.ID),
			"unexpected indexed Administration markup problem: %s",
			problem.Message,
		)
	}
	adminMarkupSource := `<sw-users-permissions-role-view-general : />`
	adminMarkupDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/real-world.html.twig",
		)),
		adminMarkupSource,
		1,
	)
	adminMarkupOffset := uint32(strings.Index(adminMarkupSource, ":"))
	adminMarkupParams := &protocol.CompletionParams{}
	adminMarkupParams.TextDocument.URI = adminMarkupDocument.URI
	adminMarkupCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(ctx, &lsp.CompletionRequest{
		CompletionParams: adminMarkupParams,
		SyntaxContext: lsp.SyntaxContext{
			Document: adminMarkupDocument, DocumentContent: adminMarkupDocument.Text,
			DocumentTree: adminMarkupDocument.SyntaxTree,
			LineIndex:    adminMarkupDocument.LineIndex,
			Root:         adminMarkupDocument.SyntaxTree.Root,
			Node: adminMarkupDocument.SyntaxTree.Root.NodeAtOffset(
				adminMarkupOffset,
			),
			Token: adminMarkupDocument.SyntaxTree.Root.TokenAtOffset(
				adminMarkupOffset,
			),
		},
	})
	adminMarkupLabels := realWorldCompletionLabels(adminMarkupCompletions)
	for _, label := range []string{"role", ":role", "is-loading", ":is-loading"} {
		require.Contains(
			t, adminMarkupLabels, label,
			"expected effective component prop completion %s", label,
		)
	}
	dynamicSlotSource := `<component :is="legacy ? 'sw-card' : 'sw-card-deprecated'"><template #def /></component>`
	dynamicSlotDocument := lsp.NewTextDocument(
		adminMarkupDocument.URI, dynamicSlotSource, 2,
	)
	dynamicSlotOffset := uint32(
		strings.Index(dynamicSlotSource, "#def") + len("#def"),
	)
	dynamicSlotCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx, realWorldCompletionRequest(dynamicSlotDocument, dynamicSlotOffset-1),
	)
	require.Contains(
		t, realWorldCompletionLabels(dynamicSlotCompletions), "default",
		"expected the common real sw-card slot below a dynamic component",
	)
	dynamicSlotSource = `<component :is="legacy ? 'sw-card' : 'sw-card-deprecated'"><template #default /></component>`
	dynamicSlotDocument = lsp.NewTextDocument(
		adminMarkupDocument.URI, dynamicSlotSource, 3,
	)
	dynamicSlotOffset = uint32(strings.Index(dynamicSlotSource, "default") + 2)
	dynamicSlotHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx, realWorldHoverRequest(dynamicSlotDocument, dynamicSlotOffset),
	)
	require.NoError(t, err)
	require.NotNil(t, dynamicSlotHover)
	require.Contains(t, dynamicSlotHover.Contents.Value, "`sw-card`")
	require.Contains(t, dynamicSlotHover.Contents.Value, "`sw-card-deprecated`")
	dynamicSlotDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			dynamicSlotDocument,
			dynamicSlotDocument.SyntaxTree.Root.NodeAtOffset(dynamicSlotOffset),
			dynamicSlotOffset,
		),
	)
	require.GreaterOrEqual(t, len(dynamicSlotDefinitions), 2)
	dynamicSlotTokens, err := lspsemantic.NewAdminMarkupProvider(
		workspaceAdminIndex(t, workspace),
	).GetSemanticTokens(
		ctx, &lsp.SemanticTokensRequest{Document: dynamicSlotDocument},
	)
	require.NoError(t, err)
	dynamicSlotHighlighted := false
	for _, token := range dynamicSlotTokens {
		if string(dynamicSlotDocument.Text[token.Range.Start:token.Range.End]) ==
			"#default" && token.Type == protocol.SemanticTokenProperty {
			dynamicSlotHighlighted = true
			break
		}
	}
	require.True(t, dynamicSlotHighlighted)
	for _, componentName := range []string{"sw-data-grid", "sw-entity-listing"} {
		component, componentErr := workspaceAdminIndex(t, workspace).
			GetEffectiveComponent(componentName)
		require.NoError(t, componentErr)
		require.NotNil(t, component)
		slot, slotFound := component.ComponentSlot("column-name")
		require.True(t, slotFound, "expected %s dynamic column slot", componentName)
		_, memberFound := slot.Member("item")
		require.True(t, memberFound, "expected %s column item payload", componentName)
	}
	dynamicPayloadCompletionSource := `<component :is="entityMode ? 'sw-data-grid' : 'sw-entity-listing'"><template #column-name="{ it }"></template></component>`
	dynamicPayloadCompletionDocument := lsp.NewTextDocument(
		uriutil.FileURI(slotCard.TemplatePath), dynamicPayloadCompletionSource, 4,
	)
	dynamicPayloadCompletionOffset := uint32(
		strings.Index(dynamicPayloadCompletionSource, "{ it") + len("{ it"),
	)
	dynamicPayloadCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			dynamicPayloadCompletionDocument,
			dynamicPayloadCompletionOffset-1,
		),
	)
	require.Contains(
		t, realWorldCompletionLabels(dynamicPayloadCompletions), "item",
		"expected the inherited real data-grid scoped-slot payload",
	)
	dynamicPayloadSource := `<component :is="entityMode ? 'sw-data-grid' : 'sw-entity-listing'"><template #column-name="props">{{ props.item }}</template></component>`
	dynamicPayloadDocument := lsp.NewTextDocument(
		uriutil.FileURI(slotCard.TemplatePath), dynamicPayloadSource, 5,
	)
	dynamicPayloadOffset := uint32(
		strings.Index(dynamicPayloadSource, "props.item") + len("props.it"),
	)
	dynamicPayloadHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx, realWorldHoverRequest(dynamicPayloadDocument, dynamicPayloadOffset),
	)
	require.NoError(t, err)
	require.NotNil(t, dynamicPayloadHover)
	require.Contains(t, dynamicPayloadHover.Contents.Value, "sw-data-grid")
	require.Contains(t, dynamicPayloadHover.Contents.Value, "sw-entity-listing")
	dynamicPayloadDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			dynamicPayloadDocument,
			dynamicPayloadDocument.SyntaxTree.Root.NodeAtOffset(dynamicPayloadOffset),
			dynamicPayloadOffset,
		),
	)
	require.NotEmpty(t, dynamicPayloadDefinitions)
	require.True(t, strings.Contains(
		filepath.ToSlash(dynamicPayloadDefinitions[0].URI),
		"/sw-data-grid/sw-data-grid.html.twig",
	))
	dynamicPayloadReferences, err := lspreference.NewAdminReferenceProvider(
		workspaceAdminIndex(t, workspace),
	).GetReferences(
		ctx,
		realWorldReferenceRequest(
			dynamicPayloadDocument, dynamicPayloadOffset, true,
		),
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(dynamicPayloadReferences), 2)
	dynamicPayloadTokens, err := lspsemantic.NewAdminMarkupProvider(
		workspaceAdminIndex(t, workspace),
	).GetSemanticTokens(
		ctx, &lsp.SemanticTokensRequest{Document: dynamicPayloadDocument},
	)
	require.NoError(t, err)
	dynamicPayloadTokenTypes := make(map[string][]uint32)
	for _, token := range dynamicPayloadTokens {
		text := string(dynamicPayloadDocument.Text[token.Range.Start:token.Range.End])
		dynamicPayloadTokenTypes[text] = append(
			dynamicPayloadTokenTypes[text], token.Type,
		)
	}
	require.Contains(
		t, dynamicPayloadTokenTypes["props"],
		uint32(protocol.SemanticTokenVariable),
	)
	require.Contains(
		t, dynamicPayloadTokenTypes["item"],
		uint32(protocol.SemanticTokenProperty),
	)
	dynamicPayloadProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, dynamicPayloadDocument)
	require.NoError(t, err)
	for _, problem := range dynamicPayloadProblems {
		require.NotEqual(
			t, "admin.component.unknown-slot-prop", string(problem.ID),
			"unexpected real scoped-slot payload problem: %s", problem.Message,
		)
	}
	dynamicPayloadRenameSource := `<component :is="entityMode ? 'sw-data-grid' : 'sw-entity-listing'"><template #column-name="{ item: row }">{{ row.id }}</template></component>`
	dynamicPayloadRenameDocument := lsp.NewTextDocument(
		uriutil.FileURI(slotCard.TemplatePath), dynamicPayloadRenameSource, 6,
	)
	dynamicPayloadRenameOffset := uint32(
		strings.Index(dynamicPayloadRenameSource, "row.id") + 1,
	)
	dynamicPayloadRename, err := lsprefactor.NewAdminRenameProvider(
		workspaceAdminIndex(t, workspace),
	).Rename(
		ctx,
		realWorldRenameRequest(
			dynamicPayloadRenameDocument, dynamicPayloadRenameOffset, "record",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, dynamicPayloadRename)
	require.Len(
		t,
		dynamicPayloadRename.Changes[dynamicPayloadRenameDocument.URI],
		2,
	)
	profileComponent, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-profile-index")
	require.NoError(t, err)
	require.NotNil(t, profileComponent)
	profileTemplatePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/module/sw-profile/page/sw-profile-index/sw-profile-index.html.twig",
	)
	profileSource, err := os.ReadFile(profileTemplatePath)
	require.NoError(t, err)
	profileDocument := lsp.NewTextDocument(
		uriutil.FileURI(profileTemplatePath), string(profileSource), 7,
	)
	var profileDynamicTag *twigsyntax.Node
	var profileSelector admin.VueDynamicComponentSelector
	for _, startTag := range twigquery.Nodes(
		profileDocument.SyntaxTree.Root, twigsyntax.HtmlStartingTag,
	) {
		selector, dynamic := admin.TwigDynamicComponentSelector(startTag)
		if !dynamic || selector.Expression != "Component" {
			continue
		}
		profileDynamicTag = startTag
		profileSelector = selector
		break
	}
	require.NotNil(t, profileDynamicTag)
	resolvedProfileSelector, profileRouteComponents, profileRouteComplete, err :=
		workspaceAdminIndex(t, workspace).ResolveDynamicComponentContracts(
			profileTemplatePath, profileSelector, profileDynamicTag,
		)
	require.NoError(t, err)
	require.True(t, profileRouteComplete)
	require.Equal(t, []string{
		"frosh-profile-mfa",
		"sw-profile-index-general",
		"sw-profile-index-privacy-preferences",
		"sw-profile-index-search-preferences",
	}, resolvedProfileSelector.Names())
	require.Len(t, profileRouteComponents, 4)
	profileUserIndex := strings.Index(
		string(profileSource), "\n                            user,",
	)
	require.GreaterOrEqual(t, profileUserIndex, 0)
	profileUserOffset := uint32(
		profileUserIndex + len("\n                            us"),
	)
	profileUserHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(ctx, realWorldHoverRequest(profileDocument, profileUserOffset))
	require.NoError(t, err)
	require.NotNil(t, profileUserHover)
	require.Contains(t, profileUserHover.Contents.Value, "**prop** `user`")
	require.Contains(
		t, profileUserHover.Contents.Value, "Component: `sw-profile-index-general`",
	)
	profileGeneral, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-profile-index-general")
	require.NoError(t, err)
	require.NotNil(t, profileGeneral)
	profileUserSource, found := profileGeneral.SymbolSource(
		admin.AdminSymbolComponentProp, "user",
	)
	require.True(t, found)
	profileUserUsages, err := workspaceAdminIndex(t, workspace).
		GetSymbolUsages(admin.AdminSymbolTarget{
			Kind:  admin.AdminSymbolComponentProp,
			Owner: profileUserSource,
			Name:  "user",
		})
	require.NoError(t, err)
	profileDynamicUserReferences := 0
	for _, set := range profileUserUsages {
		if filepath.Clean(set.FilePath) != filepath.Clean(profileTemplatePath) {
			continue
		}
		for _, occurrence := range set.Occurrences {
			if occurrence.DynamicRouterView &&
				occurrence.DynamicComponentSelector == "Component" {
				profileDynamicUserReferences++
			}
		}
	}
	require.GreaterOrEqual(t, profileDynamicUserReferences, 1)
	adminSnippetDefinitions, err := workspaceSnippetIndex(t, workspace).GetAdminSnippet(
		"global.default.save",
	)
	require.NoError(t, err)
	require.NotEmpty(t, adminSnippetDefinitions)
	adminSnippetCompletionSource := `<mt-button :label="$t('global.default.sa')" />`
	adminSnippetCompletionDocument := lsp.NewTextDocument(
		adminMarkupDocument.URI,
		adminSnippetCompletionSource,
		2,
	)
	adminSnippetCompletionOffset := uint32(
		strings.Index(adminSnippetCompletionSource, "global.default.sa") +
			len("global.default.sa"),
	)
	adminSnippetCompletions := lspcompletion.NewSnippetCompletionProvider(
		workspaceSnippetIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminSnippetCompletionDocument,
			adminSnippetCompletionOffset,
		),
	)
	require.Contains(
		t,
		realWorldCompletionLabels(adminSnippetCompletions),
		"global.default.save",
	)
	adminSnippetDefinitionSource := `<mt-button :label="$t('global.default.save')" />`
	adminSnippetDefinitionDocument := lsp.NewTextDocument(
		adminMarkupDocument.URI,
		adminSnippetDefinitionSource,
		3,
	)
	adminSnippetDefinitionOffset := uint32(
		strings.Index(adminSnippetDefinitionSource, "global.default.save") + 3,
	)
	adminSnippetLocations := lspdefinition.NewSnippetDefinitionProvider(
		workspaceSnippetIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminSnippetDefinitionDocument,
			adminSnippetDefinitionDocument.SyntaxTree.Root.NodeAtOffset(
				adminSnippetDefinitionOffset,
			),
			adminSnippetDefinitionOffset,
		),
	)
	require.NotEmpty(t, adminSnippetLocations)
	adminEnglishSnippetFound := false
	for _, location := range adminSnippetLocations {
		if strings.Contains(
			filepath.ToSlash(location.URI),
			"/administration/src/app/snippet/en.json",
		) {
			adminEnglishSnippetFound = true
			break
		}
	}
	require.True(
		t, adminEnglishSnippetFound,
		"expected an English Administration snippet definition in %#v",
		adminSnippetLocations,
	)
	adminModuleSnippetSource := `Module.register('demo', {
        title: 'global.default.sa',
        navigation: [{ label: 'global.default.save' }],
    });`
	adminModuleSnippetDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/module-real-world.js",
		)),
		adminModuleSnippetSource,
		4,
	)
	adminModuleSnippetOffset := uint32(
		strings.Index(adminModuleSnippetSource, "global.default.sa") +
			len("global.default.sa"),
	)
	adminModuleSnippetCompletions := lspcompletion.NewSnippetCompletionProvider(
		workspaceSnippetIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminModuleSnippetDocument,
			adminModuleSnippetOffset,
		),
	)
	require.Contains(
		t,
		realWorldCompletionLabels(adminModuleSnippetCompletions),
		"global.default.save",
	)
	adminProductModulePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/module/sw-product/index.js",
	)
	adminProductModuleSource, err := os.ReadFile(adminProductModulePath)
	require.NoError(t, err)
	adminProductModuleDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminProductModulePath),
		string(adminProductModuleSource),
		1,
	)
	require.GreaterOrEqual(
		t,
		len(snippet.AdminJavaScriptStringReferences(
			adminProductModuleDocument.SyntaxTree.Root,
		)),
		3,
	)
	adminProductModuleProblems, err := lspdiagnostics.NewSnippetAnalyzer(
		workspaceSnippetIndex(t, workspace),
	).Analyze(ctx, adminProductModuleDocument)
	require.NoError(t, err)
	for _, problem := range adminProductModuleProblems {
		require.NotEqual(t, "admin.snippet.missing", string(problem.ID))
	}
	adminSnippetTemplatePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/component/data-grid",
		"sw-data-grid/sw-data-grid.html.twig",
	)
	adminSnippetTemplateSource, err := os.ReadFile(adminSnippetTemplatePath)
	require.NoError(t, err)
	adminSnippetTemplateDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminSnippetTemplatePath),
		string(adminSnippetTemplateSource),
		1,
	)
	require.Greater(
		t,
		len(snippet.AdminTwigReferences(adminSnippetTemplateDocument.SyntaxTree.Root)),
		5,
	)
	adminSnippetProblems, err := lspdiagnostics.NewSnippetAnalyzer(
		workspaceSnippetIndex(t, workspace),
	).Analyze(ctx, adminSnippetTemplateDocument)
	require.NoError(t, err)
	for _, problem := range adminSnippetProblems {
		require.NotEqual(t, "admin.snippet.missing", string(problem.ID))
	}
	meteorButton, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("mt-button")
	require.NoError(t, err)
	require.NotNil(t, meteorButton)
	for _, prop := range []string{"disabled", "variant", "isLoading"} {
		require.Contains(t, adminPropNames(meteorButton.Props), prop)
	}
	require.Contains(t, adminSlotNames(meteorButton.Slots), "default")
	helpSidebar, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-help-sidebar")
	require.NoError(t, err)
	require.NotNil(t, helpSidebar)
	helpSidebarSelector := requireAdminProp(t, helpSidebar.Props, "selector")
	require.Contains(
		t, helpSidebarSelector.Documentation,
		"selector of the element where the sidebar should be appended",
	)
	uploadListener, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-upload-listener")
	require.NoError(t, err)
	require.NotNil(t, uploadListener)
	for eventName, eventType := range map[string]string{
		"media-upload-add":    "UploadTask[]",
		"media-upload-finish": "string",
		"media-upload-fail":   "UploadTask",
		"media-upload-cancel": "UploadTask",
	} {
		event := requireAdminEvent(
			t, uploadListener.ComponentEvents(), eventName,
		)
		require.Equal(t, eventType, event.Type, eventName)
		require.True(t, event.NameRange.Declaration, eventName)
	}
	swAddress, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-address")
	require.NoError(t, err)
	require.NotNil(t, swAddress)
	addressProp := requireAdminProp(t, swAddress.Props, "address")
	require.Contains(t, addressProp.Type, "Object")
	editLinkProp := requireAdminProp(t, swAddress.Props, "editLink")
	require.Contains(t, editLinkProp.Type, "PropType<RouteLocationNamedRaw | null>")
	mediaItem, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-media-media-item")
	require.NoError(t, err)
	require.NotNil(t, mediaItem)
	require.True(t, requireAdminProp(t, mediaItem.Props, "item").Required)
	legacyArrayComponent, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-cms-el-form-template-contact")
	require.NoError(t, err)
	require.NotNil(t, legacyArrayComponent)
	legacyArrayProp := requireAdminProp(
		t, legacyArrayComponent.Props, "formSettings",
	)
	require.True(t, legacyArrayProp.NameRange.Declaration)
	require.False(t, legacyArrayProp.NameRange.Identifier)
	require.Greater(
		t, legacyArrayProp.NameRange.EndCharacter,
		legacyArrayProp.NameRange.StartCharacter,
	)
	legacyArrayMarkupSource :=
		`<sw-cms-el-form-template-contact :form-settings="settings" />`
	legacyArrayMarkupDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/legacy-array-prop-real-world.html.twig",
		)),
		legacyArrayMarkupSource,
		1,
	)
	legacyArrayPropOffset := uint32(
		strings.Index(legacyArrayMarkupSource, "form-settings") + 3,
	)
	legacyArrayPropDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			legacyArrayMarkupDocument,
			legacyArrayMarkupDocument.SyntaxTree.Root.NodeAtOffset(
				legacyArrayPropOffset,
			),
			legacyArrayPropOffset,
		),
	)
	require.Len(t, legacyArrayPropDefinitions, 1)
	require.Equal(
		t, uriutil.FileURI(legacyArrayProp.FilePath),
		legacyArrayPropDefinitions[0].URI,
	)
	require.Equal(t, protocol.Range{
		Start: protocol.Position{
			Line:      legacyArrayProp.NameRange.StartLine,
			Character: legacyArrayProp.NameRange.StartCharacter,
		},
		End: protocol.Position{
			Line:      legacyArrayProp.NameRange.EndLine,
			Character: legacyArrayProp.NameRange.EndCharacter,
		},
	}, legacyArrayPropDefinitions[0].Range)
	imageSlider, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-image-slider")
	require.NoError(t, err)
	require.NotNil(t, imageSlider)
	navigationType := requireAdminProp(
		t, imageSlider.Props, "navigationType",
	)
	navigationValues, navigationValuesComplete :=
		admin.VuePropAllowedValues(navigationType)
	require.True(t, navigationValuesComplete)
	require.ElementsMatch(
		t, []string{"arrow", "button", "all"}, navigationValues,
	)
	validatorMarkupSource := `<sw-image-slider navigation-type="a" />`
	validatorMarkupDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/validator-real-world.html.twig",
		)),
		validatorMarkupSource,
		1,
	)
	validatorMarkupCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			validatorMarkupDocument,
			uint32(strings.Index(validatorMarkupSource, `"a"`)+2),
		),
	)
	for _, value := range []string{"arrow", "button", "all"} {
		require.Contains(
			t, realWorldCompletionLabels(validatorMarkupCompletions), value,
			"expected runtime-validator prop value completion %s", value,
		)
	}
	validatorDiagnosticSource := `<sw-image-slider :images="[]" navigation-type="tabs" />`
	validatorProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(
		ctx,
		lsp.NewTextDocument(
			validatorMarkupDocument.URI, validatorDiagnosticSource, 2,
		),
	)
	require.NoError(t, err)
	require.Len(t, validatorProblems, 1)
	require.Equal(
		t, lsp.DiagnosticID("admin.component.invalid-prop-value"),
		validatorProblems[0].ID,
	)
	skeleton, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-skeleton")
	require.NoError(t, err)
	require.NotNil(t, skeleton)
	skeletonVariant := requireAdminProp(t, skeleton.Props, "variant")
	skeletonVariantValues, skeletonVariantValuesComplete :=
		admin.VuePropAllowedValues(skeletonVariant)
	require.True(t, skeletonVariantValuesComplete)
	require.ElementsMatch(t, []string{
		"gallery", "detail", "detail-bold", "category", "listing",
		"tree-item", "tree-item-nested", "media", "extension-apps",
		"extension-themes",
	}, skeletonVariantValues)
	localValidatorMarkupSource := `<sw-skeleton variant="g" />`
	localValidatorMarkupDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/validator-local-real-world.html.twig",
		)),
		localValidatorMarkupSource,
		1,
	)
	localValidatorMarkupCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			localValidatorMarkupDocument,
			uint32(strings.Index(localValidatorMarkupSource, `"g"`)+2),
		),
	)
	for _, value := range skeletonVariantValues {
		require.Contains(
			t, realWorldCompletionLabels(localValidatorMarkupCompletions), value,
			"expected validator-local prop value completion %s", value,
		)
	}
	objectBoundValidatorSource :=
		`<sw-skeleton v-bind="{ variant: 'g' }" />`
	objectBoundValidatorDocument := lsp.NewTextDocument(
		localValidatorMarkupDocument.URI,
		objectBoundValidatorSource,
		2,
	)
	objectBoundValidatorCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			objectBoundValidatorDocument,
			uint32(strings.Index(objectBoundValidatorSource, `'g'`)+2),
		),
	)
	for _, value := range skeletonVariantValues {
		require.Contains(
			t, realWorldCompletionLabels(objectBoundValidatorCompletions), value,
			"expected object-bound prop value completion %s", value,
		)
	}
	localValidatorDiagnosticSource := `<sw-skeleton variant="unknown" />`
	localValidatorProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(
		ctx,
		lsp.NewTextDocument(
			localValidatorMarkupDocument.URI,
			localValidatorDiagnosticSource,
			2,
		),
	)
	require.NoError(t, err)
	require.Len(t, localValidatorProblems, 1)
	require.Equal(
		t, lsp.DiagnosticID("admin.component.invalid-prop-value"),
		localValidatorProblems[0].ID,
	)
	objectBoundValidatorProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(
		ctx,
		lsp.NewTextDocument(
			localValidatorMarkupDocument.URI,
			`<sw-skeleton v-bind="{ variant: 'unknown' }" />`,
			3,
		),
	)
	require.NoError(t, err)
	require.Len(t, objectBoundValidatorProblems, 1)
	require.Equal(
		t, lsp.DiagnosticID("admin.component.invalid-prop-value"),
		objectBoundValidatorProblems[0].ID,
	)
	accessKeyModal, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-user-sso-access-key-create-modal")
	require.NoError(t, err)
	require.NotNil(t, accessKeyModal)
	accessKeyMode := requireAdminProp(t, accessKeyModal.Props, "mode")
	require.Equal(t, "String", accessKeyMode.Type)
	accessKeyModeValues, accessKeyModeValuesComplete :=
		admin.VuePropAllowedValues(accessKeyMode)
	require.True(t, accessKeyModeValuesComplete)
	require.ElementsMatch(
		t, []string{"view", "edit", "create"}, accessKeyModeValues,
	)
	notifications, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-notifications")
	require.NoError(t, err)
	require.NotNil(t, notifications)
	notificationPosition := requireAdminProp(
		t, notifications.Props, "position",
	)
	notificationPositionValues, notificationPositionValuesComplete :=
		admin.VuePropAllowedValues(notificationPosition)
	require.True(t, notificationPositionValuesComplete)
	require.ElementsMatch(
		t, []string{"topRight", "bottomRight", ""},
		notificationPositionValues,
	)
	require.Contains(t, adminSlotNames(meteorButton.Slots), "iconFront")
	meteorIconFrontSlot := requireAdminSlot(
		t, meteorButton.Slots, "iconFront",
	)
	require.NotEmpty(t, meteorIconFrontSlot.FilePath)
	require.Positive(t, meteorIconFrontSlot.Line)
	meteorIconSize := requireAdminSlotMember(
		t, meteorIconFrontSlot.Members, "size",
	)
	require.Equal(t, "number", meteorIconSize.Type)
	require.Equal(t, meteorIconFrontSlot.FilePath, meteorIconSize.FilePath)
	require.Positive(t, meteorIconSize.Line)
	deprecatedTabs, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-tabs-deprecated")
	require.NoError(t, err)
	require.NotNil(t, deprecatedTabs)
	deprecatedTabsDefault := requireAdminSlot(
		t, deprecatedTabs.Slots, "default",
	)
	deprecatedTabsActive := requireAdminSlotMember(
		t, deprecatedTabsDefault.Members, "active",
	)
	require.True(t, strings.HasSuffix(
		filepath.ToSlash(deprecatedTabsActive.FilePath),
		"/app/component/base/sw-tabs-deprecated/sw-tabs-deprecated.html.twig",
	))
	require.Positive(t, deprecatedTabsActive.Line)
	meteorMarkupSource := `<mt-button : />`
	meteorMarkupDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/meteor-real-world.html.twig",
		)),
		meteorMarkupSource,
		1,
	)
	meteorMarkupOffset := uint32(strings.Index(meteorMarkupSource, ":"))
	meteorMarkupParams := &protocol.CompletionParams{}
	meteorMarkupParams.TextDocument.URI = meteorMarkupDocument.URI
	meteorMarkupCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(ctx, &lsp.CompletionRequest{
		CompletionParams: meteorMarkupParams,
		SyntaxContext: lsp.SyntaxContext{
			Document: meteorMarkupDocument, DocumentContent: meteorMarkupDocument.Text,
			DocumentTree: meteorMarkupDocument.SyntaxTree,
			LineIndex:    meteorMarkupDocument.LineIndex,
			Root:         meteorMarkupDocument.SyntaxTree.Root,
			Node: meteorMarkupDocument.SyntaxTree.Root.NodeAtOffset(
				meteorMarkupOffset,
			),
			Token: meteorMarkupDocument.SyntaxTree.Root.TokenAtOffset(
				meteorMarkupOffset,
			),
		},
	})
	meteorMarkupLabels := realWorldCompletionLabels(meteorMarkupCompletions)
	for _, label := range []string{
		"disabled", ":disabled", "variant", ":is-loading",
	} {
		require.Contains(
			t, meteorMarkupLabels, label,
			"expected Meteor component prop completion %s", label,
		)
	}
	dataGrid, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-data-grid")
	require.NoError(t, err)
	require.NotNil(t, dataGrid)
	dataGridCurrentColumns, found := dataGrid.TemplateMember("currentColumns")
	require.True(t, found)
	require.Equal(t, "Array", dataGridCurrentColumns.Type)
	dataGridVisibleColumns, found := dataGrid.TemplateMember(
		"currentVisibleColumns",
	)
	require.True(t, found)
	require.Equal(t, "Array", dataGridVisibleColumns.Type)
	dataGridDefaultColumns, found := dataGrid.TemplateMember("getDefaultColumns")
	require.True(t, found)
	require.Contains(t, dataGridDefaultColumns.Type, "=> Array")
	rowClickEvent := requireAdminEvent(t, dataGrid.ComponentEvents(), "row-click")
	dataGridActions := requireAdminSlot(t, dataGrid.Slots, "actions")
	requireAdminSlotMember(t, dataGridActions.Members, "item")
	requireAdminSlotMember(t, dataGridActions.Members, "itemIndex")
	dataGridSelection := requireAdminSlot(
		t, dataGrid.Slots, "selection-content",
	)
	for _, member := range []string{
		"item", "isSelected", "isRecordSelectable", "selectItem",
		"itemIdentifierProperty",
	} {
		requireAdminSlotMember(t, dataGridSelection.Members, member)
	}
	dataGridColumnFamily, found := dataGrid.ComponentSlot("column-name")
	require.True(t, found)
	require.True(t, dataGridColumnFamily.IsDynamicName())
	require.Equal(t, "column-*", dataGridColumnFamily.DisplayName())
	require.True(t, dataGridColumnFamily.NameRange.Declaration)
	require.False(t, dataGridColumnFamily.NameRange.Identifier)
	require.Greater(
		t, dataGridColumnFamily.NameRange.EndCharacter,
		dataGridColumnFamily.NameRange.StartCharacter,
	)
	for _, member := range []string{
		"item", "itemIndex", "column", "columnIndex", "compact",
		"isInlineEdit", "selectItem",
	} {
		requireAdminSlotMember(t, dataGridColumnFamily.Members, member)
	}
	dataGridColumnLabelFamily, found := dataGrid.ComponentSlot(
		"column-label-name",
	)
	require.True(t, found)
	require.Equal(t, "column-label-*", dataGridColumnLabelFamily.DisplayName())
	for _, member := range []string{"column", "columnIndex"} {
		requireAdminSlotMember(t, dataGridColumnLabelFamily.Members, member)
	}
	dataGridRouterBlock, found := dataGrid.ComponentBlock(
		"sw_data_grid_columns_render_router_link",
	)
	require.True(t, found)
	for _, memberName := range []string{"item", "column"} {
		member, memberFound := dataGridRouterBlock.ScopeMember(memberName)
		require.True(t, memberFound, "expected inherited block input %s", memberName)
		require.True(t, strings.HasSuffix(
			filepath.ToSlash(member.FilePath),
			"/app/component/data-grid/sw-data-grid/sw-data-grid.html.twig",
		))
		require.Positive(t, member.Line)
		require.Greater(
			t, member.NameRange.EndCharacter,
			member.NameRange.StartCharacter,
		)
	}
	require.True(t, strings.HasSuffix(
		filepath.ToSlash(rowClickEvent.FilePath),
		"/app/component/data-grid/sw-data-grid/index.js",
	))
	require.Positive(t, rowClickEvent.Line)
	require.True(t, rowClickEvent.NameRange.Declaration)
	require.False(t, rowClickEvent.NameRange.Identifier)
	require.Greater(
		t, rowClickEvent.NameRange.EndCharacter,
		rowClickEvent.NameRange.StartCharacter,
	)
	rowClickDefinitionSource, err := os.ReadFile(rowClickEvent.FilePath)
	require.NoError(t, err)
	rowClickDefinitionLine := strings.Split(
		string(rowClickDefinitionSource), "\n",
	)[rowClickEvent.NameRange.StartLine]
	require.Equal(
		t, "row-click",
		rowClickDefinitionLine[rowClickEvent.NameRange.StartCharacter:rowClickEvent.NameRange.EndCharacter],
	)
	dataGridTemplateSource, err := os.ReadFile(dataGrid.TemplatePath)
	require.NoError(t, err)
	dataGridSlotDeclarationLine := strings.Split(
		string(dataGridTemplateSource), "\n",
	)[dataGridColumnFamily.NameRange.StartLine]
	dataGridSlotDeclarationRange := dataGridColumnFamily.NameRange
	dataGridSlotDeclarationName := dataGridSlotDeclarationLine[dataGridSlotDeclarationRange.StartCharacter:dataGridSlotDeclarationRange.EndCharacter]
	require.Equal(
		t, "`column-${column.property}`",
		dataGridSlotDeclarationName,
	)
	dataGridTemplateDocument := lsp.NewTextDocument(
		uriutil.FileURI(dataGrid.TemplatePath),
		string(dataGridTemplateSource),
		1,
	)
	dataGridColumnUsagePosition := strings.Index(
		string(dataGridTemplateSource), `v-show="column.visible"`,
	)
	require.NotEqual(t, -1, dataGridColumnUsagePosition)
	dataGridColumnUsageOffset := uint32(
		dataGridColumnUsagePosition + len(`v-show="col`),
	)
	dataGridLexicalCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			dataGridTemplateDocument,
			dataGridColumnUsageOffset,
		),
	)
	dataGridLexicalLabels := realWorldCompletionLabels(
		dataGridLexicalCompletions,
	)
	for _, label := range []string{"column", "columnIndex", "currentColumns"} {
		require.Contains(t, dataGridLexicalLabels, label)
	}
	dataGridColumnMemberOffset := uint32(
		dataGridColumnUsagePosition + len(`v-show="column.`),
	)
	dataGridColumnMemberCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			dataGridTemplateDocument,
			dataGridColumnMemberOffset,
		),
	)
	dataGridColumnMemberLabels := realWorldCompletionLabels(
		dataGridColumnMemberCompletions,
	)
	for _, label := range []string{
		"visible", "property", "width", "iconLabel", "sortable",
	} {
		require.Contains(
			t, dataGridColumnMemberLabels, label,
			"expected observed sw-data-grid column member %s", label,
		)
	}
	require.NotContains(t, dataGridColumnMemberLabels, "currentColumns")
	dataGridColumnHover, err := lsphover.NewAdminHoverProvider(
		root,
		workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			dataGridTemplateDocument,
			dataGridColumnUsageOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, dataGridColumnHover)
	require.Contains(
		t, dataGridColumnHover.Contents.Value,
		"**v-for local** `column`",
	)
	require.Contains(
		t, dataGridColumnHover.Contents.Value,
		"Iterates `currentColumns`",
	)
	dataGridColumnPropertyOffset := uint32(
		dataGridColumnUsagePosition + len(`v-show="column.v`),
	)
	dataGridColumnPropertyHover, err := lsphover.NewAdminHoverProvider(
		root,
		workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			dataGridTemplateDocument,
			dataGridColumnPropertyOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, dataGridColumnPropertyHover)
	require.Contains(
		t, dataGridColumnPropertyHover.Contents.Value,
		"**property** `column.visible`",
	)
	dataGridColumnDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			dataGridTemplateDocument,
			dataGridTemplateDocument.SyntaxTree.Root.NodeAtOffset(
				dataGridColumnUsageOffset,
			),
			dataGridColumnUsageOffset,
		),
	)
	require.Len(t, dataGridColumnDefinitions, 1)
	require.Equal(
		t, dataGridTemplateDocument.URI, dataGridColumnDefinitions[0].URI,
	)
	dataGridColumnDeclarationPosition := strings.Index(
		string(dataGridTemplateSource), "column, columnIndex",
	)
	require.NotEqual(t, -1, dataGridColumnDeclarationPosition)
	dataGridColumnDeclarationOffset := uint32(dataGridColumnDeclarationPosition)
	declarationLine, declarationCharacter :=
		dataGridTemplateDocument.LineIndex.PositionUTF16(
			dataGridColumnDeclarationOffset,
		)
	require.Equal(
		t, int(declarationLine),
		dataGridColumnDefinitions[0].Range.Start.Line,
	)
	require.Equal(
		t, int(declarationCharacter),
		dataGridColumnDefinitions[0].Range.Start.Character,
	)
	dataGridLexicalReferences, err := lspreference.NewAdminReferenceProvider(
		workspaceAdminIndex(t, workspace),
	).GetReferences(
		ctx,
		realWorldReferenceRequest(
			dataGridTemplateDocument,
			dataGridColumnUsageOffset,
			true,
		),
	)
	require.NoError(t, err)
	require.Greater(t, len(dataGridLexicalReferences), 10)
	for _, location := range dataGridLexicalReferences {
		require.Equal(t, dataGridTemplateDocument.URI, location.URI)
	}
	dataGridLexicalRename, err := lsprefactor.NewAdminRenameProvider(
		workspaceAdminIndex(t, workspace),
	).Rename(
		ctx,
		realWorldRenameRequest(
			dataGridTemplateDocument,
			dataGridColumnUsageOffset,
			"headerColumn",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, dataGridLexicalRename)
	require.Len(t, dataGridLexicalRename.Changes, 1)
	require.Greater(
		t,
		len(dataGridLexicalRename.Changes[dataGridTemplateDocument.URI]),
		10,
	)
	dataGridEventPosition := strings.Index(
		string(dataGridTemplateSource),
		"onClickHeaderCell($event, column)",
	)
	require.NotEqual(t, -1, dataGridEventPosition)
	dataGridEventOffset := uint32(
		dataGridEventPosition + len("onClickHeaderCell($ev"),
	)
	dataGridEventHover, err := lsphover.NewAdminHoverProvider(
		root,
		workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(dataGridTemplateDocument, dataGridEventOffset),
	)
	require.NoError(t, err)
	require.NotNil(t, dataGridEventHover)
	require.Contains(
		t, dataGridEventHover.Contents.Value,
		"**event payload** `$event`: `MouseEvent`",
	)
	dataGridSemanticTokens, err := lspsemantic.NewAdminMarkupProvider(
		workspaceAdminIndex(t, workspace),
	).GetSemanticTokens(
		ctx,
		&lsp.SemanticTokensRequest{Document: dataGridTemplateDocument},
	)
	require.NoError(t, err)
	requireSemanticTokenText(
		t,
		dataGridTemplateDocument,
		dataGridSemanticTokens,
		"$event",
		protocol.SemanticTokenVariable,
	)
	requireSemanticTokenText(
		t,
		dataGridTemplateDocument,
		dataGridSemanticTokens,
		"visible",
		protocol.SemanticTokenProperty,
	)
	addressHandling, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-settings-country-address-handling")
	require.NoError(t, err)
	require.NotNil(t, addressHandling)
	addressRows, found := addressHandling.TemplateMember("addressFormatRows")
	require.True(t, found)
	require.Equal(t, "AddressFormatRow[]", addressRows.Type)
	addressRowShape, err := workspaceAdminIndex(t, workspace).ResolveVueType(
		"AddressFormatRow", addressRows.FilePath,
	)
	require.NoError(t, err)
	require.True(
		t, addressRowShape.Complete,
		"expected AddressFormatRow from %s to resolve: %#v",
		addressRows.FilePath, addressRowShape,
	)
	require.Len(t, addressRowShape.Members, 5)
	addressTemplateSource, err := os.ReadFile(addressHandling.TemplatePath)
	require.NoError(t, err)
	addressTemplateDocument := lsp.NewTextDocument(
		uriutil.FileURI(addressHandling.TemplatePath),
		string(addressTemplateSource),
		1,
	)
	addressByTemplate, err := workspaceAdminIndex(t, workspace).
		GetComponentByTemplatePath(addressHandling.TemplatePath)
	require.NoError(t, err)
	require.NotNil(t, addressByTemplate)
	_, found = addressByTemplate.TemplateMember("addressFormatRows")
	require.True(
		t, found, "component by template: %#v", addressByTemplate,
	)
	addressRowUsagePosition := strings.Index(
		string(addressTemplateSource), `v-if="row.isPlaceholder"`,
	)
	require.NotEqual(t, -1, addressRowUsagePosition)
	addressRowMemberOffset := uint32(
		addressRowUsagePosition + len(`v-if="row.`),
	)
	addressRowMemberCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			addressTemplateDocument, addressRowMemberOffset,
		),
	)
	addressRowMemberLabels := realWorldCompletionLabels(
		addressRowMemberCompletions,
	)
	for _, member := range []string{
		"index", "isPlaceholder", "isSource", "key", "snippet",
	} {
		require.Contains(
			t, addressRowMemberLabels, member,
			"expected AddressFormatRow member %s", member,
		)
	}
	addressSnippetPosition := strings.Index(
		string(addressTemplateSource), `:value="row.snippet"`,
	)
	require.NotEqual(t, -1, addressSnippetPosition)
	addressSnippetOffset := uint32(
		addressSnippetPosition + len(`:value="row.sni`),
	)
	resolvedAddressSnippet, err := workspaceAdminIndex(t, workspace).
		ResolveTwigVueMember(
			addressTemplateDocument.SyntaxTree.Root,
			addressTemplateDocument.Text,
			addressSnippetOffset,
			addressHandling.TemplatePath,
		)
	require.NoError(t, err)
	require.NotNil(t, resolvedAddressSnippet)
	require.True(t, resolvedAddressSnippet.MemberFound)
	require.Equal(
		t, "string[]", resolvedAddressSnippet.Member.Type,
		"binding=%#v member=%#v", resolvedAddressSnippet.Binding,
		resolvedAddressSnippet.Member,
	)
	addressSnippetHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(addressTemplateDocument, addressSnippetOffset),
	)
	require.NoError(t, err)
	require.NotNil(t, addressSnippetHover)
	require.Contains(
		t, addressSnippetHover.Contents.Value,
		"**property** `row.snippet`: `string[]`",
	)
	addressSnippetDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			addressTemplateDocument,
			addressTemplateDocument.SyntaxTree.Root.NodeAtOffset(
				addressSnippetOffset,
			),
			addressSnippetOffset,
		),
	)
	require.Len(t, addressSnippetDefinitions, 1)
	require.Equal(
		t, uriutil.FileURI(addressRows.FilePath),
		addressSnippetDefinitions[0].URI,
	)
	addressProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, addressTemplateDocument)
	require.NoError(t, err)
	for _, problem := range addressProblems {
		require.NotEqual(
			t, "admin.component.unknown-vue-member", string(problem.ID),
			"unexpected typed Administration markup problem: %s", problem.Message,
		)
	}
	customerGrid, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-order-customer-grid")
	require.NoError(t, err)
	require.NotNil(t, customerGrid)
	customerMember, found := customerGrid.TemplateMember("customer")
	require.True(t, found)
	require.Equal(t, "Entity<'customer'> | null", customerMember.Type)
	customerShape, err := workspaceAdminIndex(t, workspace).ResolveVueType(
		customerMember.Type, customerMember.FilePath,
	)
	require.NoError(t, err)
	require.True(t, customerShape.Complete)
	customerShapeNames := make([]string, 0, len(customerShape.Members))
	for _, member := range customerShape.Members {
		customerShapeNames = append(customerShapeNames, member.Name)
	}
	for _, member := range []string{
		"id", "firstName", "lastName", "salesChannelId", "addresses",
		"getEntityName",
	} {
		require.Contains(t, customerShapeNames, member)
	}
	customerGridTemplateSource, err := os.ReadFile(customerGrid.TemplatePath)
	require.NoError(t, err)
	customerGridDocument := lsp.NewTextDocument(
		uriutil.FileURI(customerGrid.TemplatePath),
		string(customerGridTemplateSource),
		1,
	)
	customerSalesChannelPosition := strings.Index(
		string(customerGridTemplateSource), "customer.salesChannelId",
	)
	require.NotEqual(t, -1, customerSalesChannelPosition)
	customerSalesChannelOffset := uint32(
		customerSalesChannelPosition + len("customer.salesChannel"),
	)
	resolvedCustomerSalesChannel, err := workspaceAdminIndex(t, workspace).
		ResolveTwigVueInstanceMember(
			customerGridDocument.SyntaxTree.Root,
			customerGridDocument.Text,
			customerSalesChannelOffset,
			customerGrid.TemplatePath,
		)
	require.NoError(t, err)
	require.NotNil(t, resolvedCustomerSalesChannel)
	require.True(t, resolvedCustomerSalesChannel.MemberFound)
	require.Equal(t, "string", resolvedCustomerSalesChannel.Member.Type)
	customerMemberCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			customerGridDocument,
			uint32(customerSalesChannelPosition+len("customer.")),
		),
	)
	customerMemberLabels := realWorldCompletionLabels(
		customerMemberCompletions,
	)
	for _, member := range []string{
		"firstName", "lastName", "salesChannelId", "addresses",
	} {
		require.Contains(t, customerMemberLabels, member)
	}
	customerSalesChannelHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			customerGridDocument, customerSalesChannelOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, customerSalesChannelHover)
	require.Contains(
		t, customerSalesChannelHover.Contents.Value,
		"**property** `customer.salesChannelId`: `string`",
	)
	customerSalesChannelDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			customerGridDocument,
			customerGridDocument.SyntaxTree.Root.NodeAtOffset(
				customerSalesChannelOffset,
			),
			customerSalesChannelOffset,
		),
	)
	require.Len(t, customerSalesChannelDefinitions, 1)
	require.Equal(
		t,
		uriutil.FileURI(resolvedCustomerSalesChannel.Member.DefinitionPath),
		customerSalesChannelDefinitions[0].URI,
	)
	customerGridProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, customerGridDocument)
	require.NoError(t, err)
	for _, problem := range customerGridProblems {
		require.NotEqual(
			t, "admin.component.unknown-vue-member", string(problem.ID),
			"unexpected customer entity markup problem: %s", problem.Message,
		)
	}

	cmsPageForm, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-cms-page-form")
	require.NoError(t, err)
	require.NotNil(t, cmsPageForm)
	pageMember, found := cmsPageForm.TemplateMember("page")
	require.True(t, found)
	require.Contains(t, pageMember.Type, "Entity<'cms_page'>")
	cmsPageTemplateSource, err := os.ReadFile(cmsPageForm.TemplatePath)
	require.NoError(t, err)
	cmsPageDocument := lsp.NewTextDocument(
		uriutil.FileURI(cmsPageForm.TemplatePath),
		string(cmsPageTemplateSource),
		1,
	)
	sectionIDPosition := strings.Index(
		string(cmsPageTemplateSource), "section.id",
	)
	require.NotEqual(t, -1, sectionIDPosition)
	sectionIDOffset := uint32(sectionIDPosition + len("section.i"))
	resolvedSectionID, err := workspaceAdminIndex(t, workspace).
		ResolveTwigVueMember(
			cmsPageDocument.SyntaxTree.Root,
			cmsPageDocument.Text,
			sectionIDOffset,
			cmsPageForm.TemplatePath,
		)
	require.NoError(t, err)
	require.NotNil(t, resolvedSectionID)
	require.Equal(t, "Entity<'cms_section'>", resolvedSectionID.Binding.Type)
	require.True(t, resolvedSectionID.MemberFound)
	require.Equal(t, "string", resolvedSectionID.Member.Type)
	entityCallSource := `<div :title="page.sections?.first()?.name"></div>`
	entityCallDocument := lsp.NewTextDocument(
		uriutil.FileURI(cmsPageForm.TemplatePath), entityCallSource, 2,
	)
	entityCallCompletionOffset := uint32(
		strings.Index(entityCallSource, "first()?.") + len("first()?."),
	)
	entityCallCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			entityCallDocument, entityCallCompletionOffset,
		),
	)
	entityCallLabels := realWorldCompletionLabels(entityCallCompletions)
	for _, member := range []string{"id", "name", "position", "blocks"} {
		require.Contains(t, entityCallLabels, member)
	}
	require.NotContains(t, entityCallLabels, "total")
	entityCallNameOffset := uint32(
		strings.Index(entityCallSource, "name\"") + 1,
	)
	entityCallHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(entityCallDocument, entityCallNameOffset),
	)
	require.NoError(t, err)
	require.NotNil(t, entityCallHover)
	require.Contains(
		t, entityCallHover.Contents.Value,
		"**property** `page.sections.first().name`: `string`",
	)
	entityCallDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			entityCallDocument,
			entityCallDocument.SyntaxTree.Root.NodeAtOffset(
				entityCallNameOffset,
			),
			entityCallNameOffset,
		),
	)
	require.Len(t, entityCallDefinitions, 1)
	require.Contains(
		t, entityCallDefinitions[0].URI,
		"entity-schema-definition.d.ts",
	)
	entityCallTypoDocument := lsp.NewTextDocument(
		uriutil.FileURI(cmsPageForm.TemplatePath),
		`<div :title="page.sections?.first()?.naem"></div>`,
		3,
	)
	entityCallProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, entityCallTypoDocument)
	require.NoError(t, err)
	require.Len(t, entityCallProblems, 1)
	require.Equal(
		t, "admin.component.unknown-vue-member",
		string(entityCallProblems[0].ID),
	)
	entityCallPayload := entityCallProblems[0].Payload.(map[string]any)
	require.Contains(t, entityCallPayload["suggestions"], "name")
	cmsPageProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, cmsPageDocument)
	require.NoError(t, err)
	for _, problem := range cmsPageProblems {
		require.NotEqual(
			t, "admin.component.unknown-vue-member", string(problem.ID),
			"unexpected CMS entity markup problem: %s", problem.Message,
		)
	}

	legacyMedia, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-sidebar-media-item")
	require.NoError(t, err)
	require.NotNil(t, legacyMedia)
	legacyMediaRepository, found := legacyMedia.TemplateMember("mediaRepository")
	require.True(t, found)
	require.Equal(t, "Repository<'media'>", legacyMediaRepository.Type)
	legacyItemsLoaded, found := legacyMedia.TemplateMember("itemsLoaded")
	require.True(t, found)
	require.Equal(t, "number", legacyItemsLoaded.Type)
	legacyMediaItems, found := legacyMedia.TemplateMember("mediaItems")
	require.True(t, found)
	require.Equal(t, "EntityCollection<'media'>", legacyMediaItems.Type)
	legacyMediaItemSource := `<div v-for="mediaItem in mediaItems" :title="mediaItem.fileName" :data-value="mediaItem."></div>`
	legacyMediaItemDocument := lsp.NewTextDocument(
		uriutil.FileURI(legacyMedia.TemplatePath), legacyMediaItemSource, 4,
	)
	legacyMediaItemCompletionOffset := uint32(
		strings.LastIndex(legacyMediaItemSource, "mediaItem.") + len("mediaItem."),
	)
	legacyMediaItemCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			legacyMediaItemDocument, legacyMediaItemCompletionOffset,
		),
	)
	legacyMediaItemLabels := realWorldCompletionLabels(legacyMediaItemCompletions)
	for _, member := range []string{"id", "fileName", "mimeType", "url"} {
		require.Contains(t, legacyMediaItemLabels, member)
	}
	legacyFilteredMediaSource := `<div v-for="filteredMedia in mediaItems.filter((item) => item.fileName)" :title="filteredMedia.fileName" :data-value="filteredMedia."></div>`
	legacyFilteredMediaDocument := lsp.NewTextDocument(
		uriutil.FileURI(legacyMedia.TemplatePath), legacyFilteredMediaSource, 5,
	)
	legacyFilteredMediaCompletionOffset := uint32(
		strings.LastIndex(legacyFilteredMediaSource, "filteredMedia.") +
			len("filteredMedia."),
	)
	legacyFilteredMediaCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			legacyFilteredMediaDocument, legacyFilteredMediaCompletionOffset,
		),
	)
	legacyFilteredMediaLabels := realWorldCompletionLabels(
		legacyFilteredMediaCompletions,
	)
	for _, member := range []string{"id", "fileName", "mimeType", "url"} {
		require.Contains(t, legacyFilteredMediaLabels, member)
	}
	legacyFilteredMediaFileNameOffset := uint32(
		strings.Index(legacyFilteredMediaSource, "filteredMedia.fileName") +
			len("filteredMedia.fileNa"),
	)
	legacyFilteredMediaHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			legacyFilteredMediaDocument, legacyFilteredMediaFileNameOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, legacyFilteredMediaHover)
	require.Contains(
		t, legacyFilteredMediaHover.Contents.Value,
		"**property** `filteredMedia.fileName`: `string`",
	)
	legacyFilteredMediaTypoDocument := lsp.NewTextDocument(
		uriutil.FileURI(legacyMedia.TemplatePath),
		`<div v-for="filteredMedia in mediaItems.filter((item) => item.fileName)">{{ filteredMedia.fileNmae }}</div>`,
		6,
	)
	legacyFilteredMediaProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, legacyFilteredMediaTypoDocument)
	require.NoError(t, err)
	require.Len(t, legacyFilteredMediaProblems, 1)
	require.Equal(
		t, "admin.component.unknown-vue-member",
		string(legacyFilteredMediaProblems[0].ID),
	)
	require.Contains(
		t,
		legacyFilteredMediaProblems[0].Payload.(map[string]any)["suggestions"],
		"fileName",
	)
	legacyMappedMediaSource := `<div v-for="mediaName in mediaItems?.map((item) => item.fileName) ?? []" :title="mediaName.length" :data-value="mediaName."></div>`
	legacyMappedMediaDocument := lsp.NewTextDocument(
		uriutil.FileURI(legacyMedia.TemplatePath), legacyMappedMediaSource, 7,
	)
	legacyMappedMediaCompletionOffset := uint32(
		strings.LastIndex(legacyMappedMediaSource, "mediaName.") +
			len("mediaName."),
	)
	legacyMappedMediaCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			legacyMappedMediaDocument, legacyMappedMediaCompletionOffset,
		),
	)
	legacyMappedMediaLabels := realWorldCompletionLabels(
		legacyMappedMediaCompletions,
	)
	for _, member := range []string{"length", "toLowerCase", "trim"} {
		require.Contains(t, legacyMappedMediaLabels, member)
	}
	legacyMappedMediaLengthOffset := uint32(
		strings.Index(legacyMappedMediaSource, "mediaName.length") +
			len("mediaName.len"),
	)
	legacyMappedMediaHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			legacyMappedMediaDocument, legacyMappedMediaLengthOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, legacyMappedMediaHover)
	require.Contains(
		t, legacyMappedMediaHover.Contents.Value,
		"**property** `mediaName.length`: `number`",
	)
	legacyMappedMediaTypoDocument := lsp.NewTextDocument(
		uriutil.FileURI(legacyMedia.TemplatePath),
		`<div v-for="mediaCard in mediaItems?.map((item) => ({ label: item.fileName })) ?? []">{{ mediaCard.lable }}</div>`,
		8,
	)
	legacyMappedMediaProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, legacyMappedMediaTypoDocument)
	require.NoError(t, err)
	require.Len(t, legacyMappedMediaProblems, 1)
	require.Equal(
		t, "admin.component.unknown-vue-member",
		string(legacyMappedMediaProblems[0].ID),
	)
	require.Contains(
		t,
		legacyMappedMediaProblems[0].Payload.(map[string]any)["suggestions"],
		"label",
	)
	legacyMediaItemFileNameOffset := uint32(
		strings.Index(legacyMediaItemSource, "fileName") + 2,
	)
	legacyMediaItemHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			legacyMediaItemDocument, legacyMediaItemFileNameOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, legacyMediaItemHover)
	require.Contains(
		t, legacyMediaItemHover.Contents.Value,
		"**property** `mediaItem.fileName`: `string`",
	)
	legacyMediaItemDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			legacyMediaItemDocument,
			legacyMediaItemDocument.SyntaxTree.Root.NodeAtOffset(
				legacyMediaItemFileNameOffset,
			),
			legacyMediaItemFileNameOffset,
		),
	)
	require.Len(t, legacyMediaItemDefinitions, 1)
	require.Contains(
		t, legacyMediaItemDefinitions[0].URI,
		"entity-schema-definition.d.ts",
	)
	legacyMediaItemTypoDocument := lsp.NewTextDocument(
		uriutil.FileURI(legacyMedia.TemplatePath),
		`<div v-for="mediaItem in mediaItems">{{ mediaItem.fileNmae }}</div>`,
		5,
	)
	legacyMediaItemProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, legacyMediaItemTypoDocument)
	require.NoError(t, err)
	require.Len(t, legacyMediaItemProblems, 1)
	require.Equal(
		t, "admin.component.unknown-vue-member",
		string(legacyMediaItemProblems[0].ID),
	)
	require.Contains(
		t,
		legacyMediaItemProblems[0].Payload.(map[string]any)["suggestions"],
		"fileName",
	)
	legacyRepositorySource := `<div :title="mediaRepository.create().fileName"></div>`
	legacyRepositoryDocument := lsp.NewTextDocument(
		uriutil.FileURI(legacyMedia.TemplatePath), legacyRepositorySource, 4,
	)
	legacyRepositoryCompletionOffset := uint32(
		strings.Index(legacyRepositorySource, "create().") + len("create()."),
	)
	legacyRepositoryCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			legacyRepositoryDocument, legacyRepositoryCompletionOffset,
		),
	)
	legacyRepositoryLabels := realWorldCompletionLabels(
		legacyRepositoryCompletions,
	)
	for _, member := range []string{"id", "fileName", "mimeType", "url"} {
		require.Contains(t, legacyRepositoryLabels, member)
	}
	legacyRepositoryFileNameOffset := uint32(
		strings.Index(legacyRepositorySource, "fileName") + 2,
	)
	legacyRepositoryHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			legacyRepositoryDocument, legacyRepositoryFileNameOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, legacyRepositoryHover)
	require.Contains(
		t, legacyRepositoryHover.Contents.Value,
		"**property** `mediaRepository.create().fileName`: `string`",
	)
	legacyRepositoryDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			legacyRepositoryDocument,
			legacyRepositoryDocument.SyntaxTree.Root.NodeAtOffset(
				legacyRepositoryFileNameOffset,
			),
			legacyRepositoryFileNameOffset,
		),
	)
	require.Len(t, legacyRepositoryDefinitions, 1)
	require.Contains(
		t, legacyRepositoryDefinitions[0].URI,
		"entity-schema-definition.d.ts",
	)
	legacyRepositoryTypoDocument := lsp.NewTextDocument(
		uriutil.FileURI(legacyMedia.TemplatePath),
		`<div :title="mediaRepository.create().fileNmae"></div>`,
		5,
	)
	legacyRepositoryProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, legacyRepositoryTypoDocument)
	require.NoError(t, err)
	require.Len(t, legacyRepositoryProblems, 1)
	require.Equal(
		t, "admin.component.unknown-vue-member",
		string(legacyRepositoryProblems[0].ID),
	)
	require.Contains(
		t,
		legacyRepositoryProblems[0].Payload.(map[string]any)["suggestions"],
		"fileName",
	)

	newsletterRecipientList, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-newsletter-recipient-list")
	require.NoError(t, err)
	require.NotNil(t, newsletterRecipientList)
	newsletterItems, found := newsletterRecipientList.TemplateMember("items")
	require.True(t, found)
	require.Equal(
		t,
		"EntityCollection<'newsletter_recipient'> | null",
		newsletterItems.Type,
	)
	newsletterSource := `<div v-for="recipient in items" :title="recipient.email" :data-value="recipient."></div>`
	newsletterDocument := lsp.NewTextDocument(
		uriutil.FileURI(newsletterRecipientList.TemplatePath), newsletterSource, 6,
	)
	newsletterCompletionOffset := uint32(
		strings.LastIndex(newsletterSource, "recipient.") + len("recipient."),
	)
	newsletterCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			newsletterDocument, newsletterCompletionOffset,
		),
	)
	newsletterLabels := realWorldCompletionLabels(newsletterCompletions)
	for _, member := range []string{"id", "email", "status", "salesChannel"} {
		require.Contains(t, newsletterLabels, member)
	}
	newsletterEmailOffset := uint32(
		strings.Index(newsletterSource, "email") + 2,
	)
	newsletterHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(newsletterDocument, newsletterEmailOffset),
	)
	require.NoError(t, err)
	require.NotNil(t, newsletterHover)
	require.Contains(
		t, newsletterHover.Contents.Value,
		"**property** `recipient.email`: `string`",
	)
	newsletterDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			newsletterDocument,
			newsletterDocument.SyntaxTree.Root.NodeAtOffset(newsletterEmailOffset),
			newsletterEmailOffset,
		),
	)
	require.Len(t, newsletterDefinitions, 1)
	require.Contains(
		t, newsletterDefinitions[0].URI, "entity-schema-definition.d.ts",
	)
	newsletterTypoDocument := lsp.NewTextDocument(
		uriutil.FileURI(newsletterRecipientList.TemplatePath),
		`<div v-for="recipient in items">{{ recipient.emial }}</div>`,
		7,
	)
	newsletterProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, newsletterTypoDocument)
	require.NoError(t, err)
	require.Len(t, newsletterProblems, 1)
	require.Equal(
		t, "admin.component.unknown-vue-member",
		string(newsletterProblems[0].ID),
	)
	require.Contains(
		t,
		newsletterProblems[0].Payload.(map[string]any)["suggestions"],
		"email",
	)

	customerGroupList, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-settings-customer-group-list")
	require.NoError(t, err)
	require.NotNil(t, customerGroupList)
	customerGroups, found := customerGroupList.TemplateMember("customerGroups")
	require.True(t, found)
	require.Equal(
		t, "EntityCollection<'customer_group'> | null", customerGroups.Type,
	)
	customerGroupSource := `<div v-for="group in customerGroups" :title="group.name" :data-value="group."></div>`
	customerGroupDocument := lsp.NewTextDocument(
		uriutil.FileURI(customerGroupList.TemplatePath), customerGroupSource, 8,
	)
	customerGroupCompletionOffset := uint32(
		strings.LastIndex(customerGroupSource, "group.") + len("group."),
	)
	customerGroupCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			customerGroupDocument, customerGroupCompletionOffset,
		),
	)
	customerGroupLabels := realWorldCompletionLabels(customerGroupCompletions)
	for _, member := range []string{
		"id", "name", "displayGross", "customers", "salesChannels",
	} {
		require.Contains(t, customerGroupLabels, member)
	}
	customerGroupNameOffset := uint32(
		strings.Index(customerGroupSource, "group.name") + len("group.na"),
	)
	customerGroupHover, err := lsphover.NewAdminHoverProvider(
		root, workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(customerGroupDocument, customerGroupNameOffset),
	)
	require.NoError(t, err)
	require.NotNil(t, customerGroupHover)
	require.Contains(
		t, customerGroupHover.Contents.Value,
		"**property** `group.name`: `string`",
	)
	customerGroupTypoDocument := lsp.NewTextDocument(
		uriutil.FileURI(customerGroupList.TemplatePath),
		`<div v-for="group in customerGroups">{{ group.naem }}</div>`,
		9,
	)
	customerGroupProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, customerGroupTypoDocument)
	require.NoError(t, err)
	require.Len(t, customerGroupProblems, 1)
	require.Equal(
		t, "admin.component.unknown-vue-member",
		string(customerGroupProblems[0].ID),
	)
	require.Contains(
		t,
		customerGroupProblems[0].Payload.(map[string]any)["suggestions"],
		"name",
	)

	landingPageView, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-landing-page-view")
	require.NoError(t, err)
	require.NotNil(t, landingPageView)
	landingPageMember, found := landingPageView.TemplateMember("landingPage")
	require.True(t, found)
	require.Equal(t, "Entity<'landing_page'> | null", landingPageMember.Type)
	cmsStorePageMember, found := landingPageView.TemplateMember("cmsPage")
	require.True(t, found)
	require.Equal(t, "null | Entity<'cms_page'>", cmsStorePageMember.Type)
	inheritWrapper, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-inherit-wrapper")
	require.NoError(t, err)
	require.NotNil(t, inheritWrapper)
	inheritContentSlot := requireAdminSlot(
		t, inheritWrapper.Slots, "content",
	)
	require.True(t, inheritContentSlot.MembersComplete)
	inheritCurrentValue := requireAdminSlotMember(
		t, inheritContentSlot.Members, "currentValue",
	)
	for _, member := range []string{
		"isInherited", "isInheritField", "updateCurrentValue",
		"restoreInheritance", "removeInheritance",
	} {
		requireAdminSlotMember(t, inheritContentSlot.Members, member)
	}
	seoURLTemplatePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/module/sw-settings-seo",
		"component/sw-seo-url/sw-seo-url.html.twig",
	)
	seoURLTemplateSource, err := os.ReadFile(seoURLTemplatePath)
	require.NoError(t, err)
	seoURLTemplateDocument := lsp.NewTextDocument(
		uriutil.FileURI(seoURLTemplatePath), string(seoURLTemplateSource), 1,
	)
	seoURLPropsPosition := strings.Index(
		string(seoURLTemplateSource), "props.currentValue",
	)
	require.NotEqual(t, -1, seoURLPropsPosition)
	seoURLPropsCompletionOffset := uint32(
		seoURLPropsPosition + len("props."),
	)
	seoURLPropsCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			seoURLTemplateDocument,
			seoURLPropsCompletionOffset,
		),
	)
	seoURLPropsLabels := realWorldCompletionLabels(seoURLPropsCompletions)
	for _, label := range []string{
		"currentValue", "isInherited", "updateCurrentValue",
	} {
		require.Contains(t, seoURLPropsLabels, label)
	}
	seoURLCurrentValueOffset := uint32(
		seoURLPropsPosition + len("props.cu"),
	)
	seoURLCurrentValueHover, err := lsphover.NewAdminHoverProvider(
		root,
		workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			seoURLTemplateDocument,
			seoURLCurrentValueOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, seoURLCurrentValueHover)
	require.Contains(
		t, seoURLCurrentValueHover.Contents.Value,
		"**slot prop** `props.currentValue`",
	)
	seoURLCurrentValueDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			seoURLTemplateDocument,
			seoURLTemplateDocument.SyntaxTree.Root.NodeAtOffset(
				seoURLCurrentValueOffset,
			),
			seoURLCurrentValueOffset,
		),
	)
	require.Len(t, seoURLCurrentValueDefinitions, 1)
	require.Equal(
		t, uriutil.FileURI(inheritCurrentValue.FilePath),
		seoURLCurrentValueDefinitions[0].URI,
	)
	seoURLProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, seoURLTemplateDocument)
	require.NoError(t, err)
	for _, problem := range seoURLProblems {
		require.NotEqual(
			t, "admin.component.unknown-slot-prop", string(problem.ID),
			problem.Message,
		)
	}
	adminEventCompletionSource := `<sw-data-grid @ />`
	adminEventCompletionDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/event-real-world.html.twig",
		)),
		adminEventCompletionSource,
		1,
	)
	adminEventCompletionOffset := uint32(
		strings.Index(adminEventCompletionSource, "@") + 1,
	)
	adminEventCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminEventCompletionDocument,
			adminEventCompletionOffset,
		),
	)
	require.Contains(
		t,
		realWorldCompletionLabels(adminEventCompletions),
		"@row-click",
	)
	adminEventSource := `<sw-data-grid @row-click="onRowClick" />`
	adminEventDocument := lsp.NewTextDocument(
		adminEventCompletionDocument.URI,
		adminEventSource,
		2,
	)
	adminEventOffset := uint32(strings.Index(adminEventSource, "row-click") + 1)
	adminEventDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminEventDocument,
			adminEventDocument.SyntaxTree.Root.NodeAtOffset(adminEventOffset),
			adminEventOffset,
		),
	)
	require.Len(t, adminEventDefinitions, 1)
	require.Equal(t, uriutil.FileURI(rowClickEvent.FilePath), adminEventDefinitions[0].URI)
	require.Equal(
		t,
		rowClickEvent.NameRange.StartLine,
		adminEventDefinitions[0].Range.Start.Line,
	)
	require.Equal(
		t, rowClickEvent.NameRange.StartCharacter,
		adminEventDefinitions[0].Range.Start.Character,
	)
	require.Equal(
		t, rowClickEvent.NameRange.EndCharacter,
		adminEventDefinitions[0].Range.End.Character,
	)
	adminEventHover, err := lsphover.NewAdminHoverProvider(
		root,
		workspaceAdminIndex(t, workspace),
	).GetHover(ctx, realWorldHoverRequest(adminEventDocument, adminEventOffset))
	require.NoError(t, err)
	require.NotNil(t, adminEventHover)
	require.Contains(t, adminEventHover.Contents.Value, "**event** `row-click`")

	adminSlotSource := `<mt-button><template #iconFront></template></mt-button>`
	adminSlotDocument := lsp.NewTextDocument(
		adminEventCompletionDocument.URI,
		adminSlotSource,
		3,
	)
	adminSlotOffset := uint32(strings.Index(adminSlotSource, "iconFront") + 1)
	adminSlotDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminSlotDocument,
			adminSlotDocument.SyntaxTree.Root.NodeAtOffset(adminSlotOffset),
			adminSlotOffset,
		),
	)
	require.Len(t, adminSlotDefinitions, 1)
	require.Equal(
		t,
		uriutil.FileURI(meteorIconFrontSlot.FilePath),
		adminSlotDefinitions[0].URI,
	)
	adminSlotHover, err := lsphover.NewAdminHoverProvider(
		root,
		workspaceAdminIndex(t, workspace),
	).GetHover(ctx, realWorldHoverRequest(adminSlotDocument, adminSlotOffset))
	require.NoError(t, err)
	require.NotNil(t, adminSlotHover)
	require.Contains(t, adminSlotHover.Contents.Value, "**slot** `iconFront`")
	require.Contains(t, adminSlotHover.Contents.Value, "`size`: `number`")

	adminScopedSlotSource := `<mt-button><template #iconFront="{ size }">{{ size }}</template></mt-button>`
	adminScopedSlotDocument := lsp.NewTextDocument(
		adminEventCompletionDocument.URI,
		adminScopedSlotSource,
		4,
	)
	adminScopedSlotBindingOffset := uint32(
		strings.Index(adminScopedSlotSource, "{ size") + len("{ si"),
	)
	adminScopedSlotCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminScopedSlotDocument,
			adminScopedSlotBindingOffset,
		),
	)
	adminScopedSlotLabels := realWorldCompletionLabels(
		adminScopedSlotCompletions,
	)
	require.Contains(t, adminScopedSlotLabels, "size")
	adminScopedSlotSizeDetail := ""
	for _, item := range adminScopedSlotCompletions {
		if item.Label == "size" {
			adminScopedSlotSizeDetail = item.Detail
			break
		}
	}
	require.Contains(t, adminScopedSlotSizeDetail, "number")
	adminScopedSlotValueOffset := uint32(
		strings.LastIndex(adminScopedSlotSource, "size") + 1,
	)
	adminScopedSlotHover, err := lsphover.NewAdminHoverProvider(
		root,
		workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			adminScopedSlotDocument,
			adminScopedSlotValueOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, adminScopedSlotHover)
	require.Contains(
		t,
		adminScopedSlotHover.Contents.Value,
		"**slot prop** `size`: `number`",
	)
	adminScopedSlotDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminScopedSlotDocument,
			adminScopedSlotDocument.SyntaxTree.Root.NodeAtOffset(
				adminScopedSlotValueOffset,
			),
			adminScopedSlotValueOffset,
		),
	)
	require.Len(t, adminScopedSlotDefinitions, 1)
	require.Equal(
		t,
		uriutil.FileURI(meteorIconSize.FilePath),
		adminScopedSlotDefinitions[0].URI,
	)
	require.Equal(
		t,
		meteorIconSize.Line-1,
		adminScopedSlotDefinitions[0].Range.Start.Line,
	)

	adminGridSlotSource := `<sw-data-grid><template #column-name="{ item: row, isInlineEdit }">{{ row }}</template></sw-data-grid>`
	adminGridSlotDocument := lsp.NewTextDocument(
		adminEventCompletionDocument.URI,
		adminGridSlotSource,
		5,
	)
	adminGridSlotNameOffset := uint32(
		strings.Index(adminGridSlotSource, "column-name") + 1,
	)
	adminGridSlotNameDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminGridSlotDocument,
			adminGridSlotDocument.SyntaxTree.Root.NodeAtOffset(
				adminGridSlotNameOffset,
			),
			adminGridSlotNameOffset,
		),
	)
	require.Len(t, adminGridSlotNameDefinitions, 1)
	require.Equal(
		t, uriutil.FileURI(dataGridColumnFamily.FilePath),
		adminGridSlotNameDefinitions[0].URI,
	)
	require.Equal(
		t, dataGridColumnFamily.NameRange.StartLine,
		adminGridSlotNameDefinitions[0].Range.Start.Line,
	)
	require.Equal(
		t, dataGridColumnFamily.NameRange.StartCharacter,
		adminGridSlotNameDefinitions[0].Range.Start.Character,
	)
	require.Equal(
		t, dataGridColumnFamily.NameRange.EndLine,
		adminGridSlotNameDefinitions[0].Range.End.Line,
	)
	require.Equal(
		t, dataGridColumnFamily.NameRange.EndCharacter,
		adminGridSlotNameDefinitions[0].Range.End.Character,
	)
	adminGridSlotBindingOffset := uint32(
		strings.Index(adminGridSlotSource, "{ item") + len("{ it"),
	)
	adminGridSlotCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminGridSlotDocument,
			adminGridSlotBindingOffset,
		),
	)
	adminGridSlotLabels := realWorldCompletionLabels(
		adminGridSlotCompletions,
	)
	for _, member := range []string{
		"item", "itemIndex", "column", "columnIndex", "compact",
		"isInlineEdit", "selectItem",
	} {
		require.Contains(t, adminGridSlotLabels, member)
	}
	adminGridSlotValueOffset := uint32(
		strings.LastIndex(adminGridSlotSource, "row") + 1,
	)
	adminGridSlotHover, err := lsphover.NewAdminHoverProvider(
		root,
		workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			adminGridSlotDocument,
			adminGridSlotValueOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, adminGridSlotHover)
	require.Contains(
		t,
		adminGridSlotHover.Contents.Value,
		"Dynamic slot family: `column-*`",
	)
	dataGridItemMember := requireAdminSlotMember(
		t, dataGridColumnFamily.Members, "item",
	)
	require.True(t, dataGridItemMember.NameRange.Declaration)
	require.Greater(
		t, dataGridItemMember.NameRange.EndCharacter,
		dataGridItemMember.NameRange.StartCharacter,
	)
	adminGridSlotDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminGridSlotDocument,
			adminGridSlotDocument.SyntaxTree.Root.NodeAtOffset(
				adminGridSlotValueOffset,
			),
			adminGridSlotValueOffset,
		),
	)
	require.Len(t, adminGridSlotDefinitions, 1)
	require.Equal(
		t,
		uriutil.FileURI(dataGridItemMember.FilePath),
		adminGridSlotDefinitions[0].URI,
	)
	require.Equal(
		t,
		dataGridItemMember.NameRange.StartLine,
		adminGridSlotDefinitions[0].Range.Start.Line,
	)
	require.Equal(
		t, dataGridItemMember.NameRange.StartCharacter,
		adminGridSlotDefinitions[0].Range.Start.Character,
	)
	require.Equal(
		t, dataGridItemMember.NameRange.EndLine,
		adminGridSlotDefinitions[0].Range.End.Line,
	)
	require.Equal(
		t, dataGridItemMember.NameRange.EndCharacter,
		adminGridSlotDefinitions[0].Range.End.Character,
	)
	adminReferenceProvider := lspreference.NewAdminReferenceProvider(
		workspaceAdminIndex(t, workspace),
	)
	cmsListItem, err := workspaceAdminIndex(t, workspace).
		GetEffectiveComponent("sw-cms-list-item")
	require.NoError(t, err)
	require.NotNil(t, cmsListItem)
	itemClickEvent := requireAdminEvent(
		t, cmsListItem.ComponentEvents(), "item-click",
	)
	cmsListPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/module/sw-cms/page",
		"sw-cms-list/sw-cms-list.html.twig",
	)
	cmsListSource, err := os.ReadFile(cmsListPath)
	require.NoError(t, err)
	cmsListDocument := lsp.NewTextDocument(
		uriutil.FileURI(cmsListPath), string(cmsListSource), 1,
	)
	cmsItemClickPosition := strings.Index(string(cmsListSource), "@item-click")
	require.NotEqual(t, -1, cmsItemClickPosition)
	cmsItemClickOffset := uint32(cmsItemClickPosition + 2)
	adminEventReferences, err := adminReferenceProvider.GetReferences(
		ctx,
		realWorldReferenceRequest(
			cmsListDocument,
			cmsItemClickOffset,
			true,
		),
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(adminEventReferences), 4)
	requireLocationURI(t, adminEventReferences, itemClickEvent.FilePath)
	requireLocationURI(t, adminEventReferences, cmsListPath)
	adminRenameProvider := lsprefactor.NewAdminRenameProvider(
		workspaceAdminIndex(t, workspace),
	)
	adminEventRenameEdit, err := adminRenameProvider.Rename(
		ctx,
		realWorldRenameRequest(
			cmsListDocument,
			cmsItemClickOffset,
			"item-selected",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, adminEventRenameEdit)
	require.Contains(
		t, adminEventRenameEdit.Changes, uriutil.FileURI(itemClickEvent.FilePath),
	)
	require.Contains(
		t, adminEventRenameEdit.Changes, uriutil.FileURI(cmsListPath),
	)
	adminEventRenameCount := 0
	for uri, edits := range adminEventRenameEdit.Changes {
		adminEventRenameCount += len(edits)
		for _, edit := range edits {
			require.Equal(t, "item-selected", edit.NewText)
		}
		require.NotContains(
			t,
			filepath.ToSlash(uri),
			"/app/component/sidebar/",
		)
	}
	require.GreaterOrEqual(t, adminEventRenameCount, 4)

	meteorSlotUsagePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/module/sw-login/view",
		"sw-login-login/sw-login-login.html.twig",
	)
	meteorSlotUsageSource, err := os.ReadFile(meteorSlotUsagePath)
	require.NoError(t, err)
	meteorSlotUsageDocument := lsp.NewTextDocument(
		uriutil.FileURI(meteorSlotUsagePath), string(meteorSlotUsageSource), 1,
	)
	meteorSlotUsagePosition := strings.Index(
		string(meteorSlotUsageSource), "#iconFront",
	)
	require.NotEqual(t, -1, meteorSlotUsagePosition)
	meteorSlotUsageOffset := uint32(meteorSlotUsagePosition + 2)
	adminSlotReferences, err := adminReferenceProvider.GetReferences(
		ctx,
		realWorldReferenceRequest(
			meteorSlotUsageDocument,
			meteorSlotUsageOffset,
			true,
		),
	)
	require.NoError(t, err)
	require.Greater(t, len(adminSlotReferences), 5)
	requireLocationURI(t, adminSlotReferences, meteorIconFrontSlot.FilePath)
	requireLocationURI(t, adminSlotReferences, meteorSlotUsagePath)
	_, err = adminRenameProvider.Rename(
		ctx,
		realWorldRenameRequest(
			meteorSlotUsageDocument,
			meteorSlotUsageOffset,
			"leadingIcon",
		),
	)
	require.ErrorContains(t, err, "external Meteor")
	adminDefinitionPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/module",
		"sw-newsletter-recipient/component",
		"sw-newsletter-recipient-filter-switch/index.js",
	)
	adminDefinitionSource, err := os.ReadFile(adminDefinitionPath)
	require.NoError(t, err)
	adminDefinitionDiagnostics, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, lsp.NewTextDocument(
		uriutil.FileURI(adminDefinitionPath),
		string(adminDefinitionSource),
		1,
	))
	require.NoError(t, err)
	require.Empty(t, adminDefinitionDiagnostics)
	adminThisSource := `export default { methods: { probe() { return this.; } } };`
	adminThisDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminDefinitionPath),
		adminThisSource,
		1,
	)
	adminThisOffset := uint32(strings.Index(adminThisSource, "this.") + len("this."))
	adminThisParams := &protocol.CompletionParams{}
	adminThisParams.TextDocument.URI = adminThisDocument.URI
	adminThisCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(ctx, &lsp.CompletionRequest{
		CompletionParams: adminThisParams,
		SyntaxContext: lsp.SyntaxContext{
			Document: adminThisDocument, DocumentContent: adminThisDocument.Text,
			DocumentTree: adminThisDocument.SyntaxTree,
			LineIndex:    adminThisDocument.LineIndex,
			Root:         adminThisDocument.SyntaxTree.Root,
			Node: adminThisDocument.SyntaxTree.Root.NodeAtOffset(
				adminThisOffset - 1,
			),
			Token: adminThisDocument.SyntaxTree.Root.TokenAtOffset(
				adminThisOffset - 1,
			),
		},
	})
	adminThisLabels := realWorldCompletionLabels(adminThisCompletions)
	for _, label := range []string{"id", "group", "onChange", "$emit"} {
		require.Contains(t, adminThisLabels, label)
	}
	adminComponentCount := len(adminComponents)
	adminFilters, err := workspaceAdminIndex(t, workspace).GetAllFilters()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(adminFilters), 12)
	assetFilters, err := workspaceAdminIndex(t, workspace).GetFilter("asset")
	require.NoError(t, err)
	require.NotEmpty(t, assetFilters)
	require.Contains(
		t, filepath.ToSlash(assetFilters[0].FilePath),
		"/administration/src/app/filter/asset.filter.ts",
	)
	adminFilterCount := len(adminFilters)
	adminServices, err := workspaceAdminIndex(t, workspace).GetAllServices()
	require.NoError(t, err)
	require.Greater(t, len(adminServices), 25)
	aclServices, err := workspaceAdminIndex(t, workspace).GetService("acl")
	require.NoError(t, err)
	require.NotEmpty(t, aclServices)
	require.Contains(t, filepath.ToSlash(aclServices[0].FilePath), "/administration/src/app/main.ts")
	require.True(t, strings.HasSuffix(
		filepath.ToSlash(aclServices[0].ImplementationPath),
		"/administration/src/app/service/acl.service.ts",
	))
	adminStores, err := workspaceAdminIndex(t, workspace).GetAllStores()
	require.NoError(t, err)
	require.Greater(t, len(adminStores), 10)
	profileStores, err := workspaceAdminIndex(t, workspace).GetStore("swProfile")
	require.NoError(t, err)
	require.NotEmpty(t, profileStores)
	_, hasProfileAction := profileStores[0].Member("setMinSearchTermLength")
	require.True(t, hasProfileAction)
	sessionStores, err := workspaceAdminIndex(t, workspace).GetStore("session")
	require.NoError(t, err)
	require.NotEmpty(t, sessionStores)
	for _, member := range []string{
		"currentUser", "userPending", "setAdminLocale", "setCurrentUser",
	} {
		_, found := sessionStores[0].Member(member)
		require.True(t, found, "expected setup-store member %s", member)
	}
	contextStores, err := workspaceAdminIndex(t, workspace).GetStore("context")
	require.NoError(t, err)
	require.NotEmpty(t, contextStores)
	for _, member := range []string{
		"app", "api", "setApiLanguageId", "resetLanguageToDefault",
	} {
		_, found := contextStores[0].Member(member)
		require.True(t, found, "expected spread setup-store member %s", member)
	}
	adminPrivileges, err := workspaceAdminIndex(t, workspace).GetAllPrivileges()
	require.NoError(t, err)
	require.Greater(t, len(adminPrivileges), 100)
	productViewerPrivileges, err := workspaceAdminIndex(t, workspace).
		GetPrivilege("product.viewer")
	require.NoError(t, err)
	require.NotEmpty(t, productViewerPrivileges)
	require.Equal(t, admin.AdminPrivilegeRole, productViewerPrivileges[0].Kind)
	productReadPrivileges, err := workspaceAdminIndex(t, workspace).
		GetPrivilege("product:read")
	require.NoError(t, err)
	require.NotEmpty(t, productReadPrivileges)
	require.Equal(
		t, admin.AdminPrivilegePermission, productReadPrivileges[0].Kind,
	)
	productModule, productDetailRoute, err := workspaceAdminIndex(
		t, workspace,
	).GetModuleRoute("sw.product.detail")
	require.NoError(t, err)
	require.NotNil(t, productModule)
	require.NotNil(t, productDetailRoute)
	require.Equal(t, "sw-product", productModule.Name)
	require.Equal(t, "sw-product-detail", productDetailRoute.Component)
	orderModule, orderDetailGeneralRoute, err := workspaceAdminIndex(
		t, workspace,
	).GetModuleRoute("sw.order.detail.general")
	require.NoError(t, err)
	require.NotNil(t, orderModule)
	require.NotNil(t, orderDetailGeneralRoute)
	require.Equal(t, "sw-order", orderModule.Name)
	require.Equal(
		t,
		"sw-order-detail-general",
		orderDetailGeneralRoute.Component,
	)
	for routeName, componentName := range map[string]string{
		"sw.settings.security.index":    "sw-settings-security-view",
		"sw.profile.index.mfa":          "frosh-profile-mfa",
		"sw.sales.channel.detail.theme": "sw-sales-channel-detail-theme",
	} {
		_, route, routeErr := workspaceAdminIndex(t, workspace).
			GetModuleRoute(routeName)
		require.NoError(t, routeErr)
		require.NotNil(t, route, routeName)
		require.Equal(t, componentName, route.Component, routeName)
	}
	adminModules, err := workspaceAdminIndex(t, workspace).GetAllModules()
	require.NoError(t, err)
	require.Greater(t, len(adminModules), 40)
	adminModuleCount := len(adminModules)
	adminGlobalTypesPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/global.types.ts",
	)
	adminContainerIndex := workspaceAdminIndex(t, workspace)
	factoryContainerShape, err := adminContainerIndex.ResolveApplicationContainer(
		"factory", adminGlobalTypesPath,
	)
	require.NoError(t, err)
	factoryContainerMembers := make(map[string]admin.TwigVueMember)
	for _, member := range factoryContainerShape.Members {
		factoryContainerMembers[member.Name] = member
	}
	for _, memberName := range []string{
		"$list", "apiService", "entityDefinition", "locale", "module",
		"workerNotification",
	} {
		require.Contains(
			t, factoryContainerMembers, memberName,
			"real FactoryContainer must expose %s", memberName,
		)
	}
	require.Equal(
		t, "typeof LocaleFactory", factoryContainerMembers["locale"].Type,
	)
	serviceContainerShape, err := adminContainerIndex.ResolveApplicationContainer(
		"service", adminGlobalTypesPath,
	)
	require.NoError(t, err)
	serviceContainerMembers := make(map[string]admin.TwigVueMember)
	for _, member := range serviceContainerShape.Members {
		serviceContainerMembers[member.Name] = member
	}
	for _, memberName := range []string{
		"acl", "repositoryFactory", "languageAutoFetchingService",
		"extensionSdkService",
	} {
		require.Contains(
			t, serviceContainerMembers, memberName,
			"merged real ServiceContainer must expose %s", memberName,
		)
	}
	adminContainerDocumentPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/application-container-real-world.ts",
	)
	adminFactoryContainerSource := `Application.getContainer('factory').`
	adminFactoryContainerDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminContainerDocumentPath),
		adminFactoryContainerSource,
		1,
	)
	adminFactoryContainerCompletions := lspcompletion.NewAdminCompletionProvider(
		adminContainerIndex,
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminFactoryContainerDocument,
			uint32(len(adminFactoryContainerSource)-1),
		),
	)
	adminFactoryContainerLabels := realWorldCompletionLabels(
		adminFactoryContainerCompletions,
	)
	for _, memberName := range []string{"locale", "module", "entityDefinition"} {
		require.Contains(t, adminFactoryContainerLabels, memberName)
	}
	adminServiceContainerSource := `function resolveService() {
    const services = Shopware.Application.getContainer('service');
    return services.;
}`
	adminServiceContainerDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminContainerDocumentPath),
		adminServiceContainerSource,
		2,
	)
	adminServiceContainerOffset := uint32(
		strings.LastIndex(adminServiceContainerSource, ".;"),
	)
	adminServiceContainerCompletions := lspcompletion.NewAdminCompletionProvider(
		adminContainerIndex,
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminServiceContainerDocument, adminServiceContainerOffset,
		),
	)
	adminServiceContainerLabels := realWorldCompletionLabels(
		adminServiceContainerCompletions,
	)
	for _, memberName := range []string{
		"acl", "repositoryFactory", "languageAutoFetchingService",
	} {
		require.Contains(t, adminServiceContainerLabels, memberName)
	}
	adminContainerDefinitionSource := `Application.getContainer('factory').locale`
	adminContainerDefinitionDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminContainerDocumentPath),
		adminContainerDefinitionSource,
		3,
	)
	adminContainerDefinitionOffset := uint32(
		strings.LastIndex(adminContainerDefinitionSource, "locale") + 1,
	)
	adminContainerDefinitions := lspdefinition.NewAdminDefinitionProvider(
		adminContainerIndex,
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminContainerDefinitionDocument,
			adminContainerDefinitionDocument.SyntaxTree.Root.NodeAtOffset(
				adminContainerDefinitionOffset,
			),
			adminContainerDefinitionOffset,
		),
	)
	require.Len(t, adminContainerDefinitions, 1)
	require.Equal(
		t, uriutil.FileURI(adminGlobalTypesPath), adminContainerDefinitions[0].URI,
	)
	require.Greater(t, adminContainerDefinitions[0].Range.Start.Character, 0)
	languageServiceUsages, err := adminContainerIndex.GetUsages(
		admin.AdminSymbolService, "", "languageAutoFetchingService",
	)
	require.NoError(t, err)
	languageServiceContainerPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/init-post/language.init.ts",
	)
	var foundLanguageContainerUsage bool
	for _, usage := range languageServiceUsages {
		if filepath.Clean(usage.FilePath) == filepath.Clean(
			languageServiceContainerPath,
		) && len(usage.Occurrences) > 0 {
			foundLanguageContainerUsage = true
			break
		}
	}
	require.True(
		t, foundLanguageContainerUsage,
		"real getContainer('service') member must be indexed as a service reference",
	)
	adminContextPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/composables/use-context.ts",
	)
	apiContextShape, err := adminContainerIndex.ResolveShopwareContext(
		"api", adminContainerDocumentPath,
	)
	require.NoError(t, err)
	require.True(t, apiContextShape.Complete)
	apiContextMembers := make(map[string]admin.TwigVueMember)
	for _, member := range apiContextShape.Members {
		apiContextMembers[member.Name] = member
	}
	for _, memberName := range []string{
		"languageId", "versionId", "currencyId", "systemLanguageId",
	} {
		require.Contains(t, apiContextMembers, memberName)
	}
	require.Equal(
		t, "null | string", apiContextMembers["languageId"].Type,
	)
	adminContextCompletionSource := `Shopware.Context.api.`
	adminContextCompletionDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminContainerDocumentPath),
		adminContextCompletionSource,
		4,
	)
	adminContextCompletions := lspcompletion.NewAdminCompletionProvider(
		adminContainerIndex,
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminContextCompletionDocument,
			uint32(len(adminContextCompletionSource)-1),
		),
	)
	adminContextLabels := realWorldCompletionLabels(adminContextCompletions)
	for _, memberName := range []string{"languageId", "versionId", "currencyId"} {
		require.Contains(t, adminContextLabels, memberName)
	}
	adminContextDefinitionSource := `Shopware.Context.api.languageId`
	adminContextDefinitionDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminContainerDocumentPath),
		adminContextDefinitionSource,
		5,
	)
	adminContextDefinitionOffset := uint32(
		strings.LastIndex(adminContextDefinitionSource, "languageId") + 1,
	)
	adminContextDefinitions := lspdefinition.NewAdminDefinitionProvider(
		adminContainerIndex,
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminContextDefinitionDocument,
			adminContextDefinitionDocument.SyntaxTree.Root.NodeAtOffset(
				adminContextDefinitionOffset,
			),
			adminContextDefinitionOffset,
		),
	)
	require.Len(t, adminContextDefinitions, 1)
	require.Equal(
		t, uriutil.FileURI(adminContextPath), adminContextDefinitions[0].URI,
	)
	require.Greater(t, adminContextDefinitions[0].Range.Start.Character, 0)
	adminUtilsPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/core/service/util.service.ts",
	)
	adminUtilsShape, err := adminContainerIndex.ResolveShopwareUtils(
		"", adminContainerDocumentPath,
	)
	require.NoError(t, err)
	require.True(t, adminUtilsShape.Complete)
	adminUtilsMembers := make(map[string]admin.TwigVueMember)
	for _, member := range adminUtilsShape.Members {
		adminUtilsMembers[member.Name] = member
	}
	for _, memberName := range []string{
		"EventBus", "array", "createId", "debug", "debounce", "format",
		"object", "string", "types",
	} {
		require.Contains(t, adminUtilsMembers, memberName)
	}
	require.Equal(t, "() => string", adminUtilsMembers["createId"].Type)
	adminFormatShape, err := adminContainerIndex.ResolveShopwareUtils(
		"format", adminContainerDocumentPath,
	)
	require.NoError(t, err)
	require.True(t, adminFormatShape.Complete)
	adminFormatMembers := make(map[string]admin.TwigVueMember)
	for _, member := range adminFormatShape.Members {
		adminFormatMembers[member.Name] = member
	}
	for _, memberName := range []string{
		"currency", "date", "dateWithUserTimezone", "fileSize", "toISODate",
	} {
		require.Contains(t, adminFormatMembers, memberName)
	}
	require.Contains(t, adminFormatMembers["date"].Type, "=> string")
	adminEventBusShape, err := adminContainerIndex.ResolveShopwareUtils(
		"EventBus", adminContainerDocumentPath,
	)
	require.NoError(t, err)
	for _, memberName := range []string{"all", "emit", "off", "on"} {
		require.Contains(t, realWorldAdminMemberNames(adminEventBusShape.Members), memberName)
	}
	adminEventBusPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/core/service/utils/eventBus.utils.ts",
	)
	adminEventMap, err := adminContainerIndex.ResolveShopwareEventBusEvents(
		adminContainerDocumentPath,
	)
	require.NoError(t, err)
	require.False(t, adminEventMap.Complete)
	for _, eventName := range []string{
		"consent", "sw-media-library-item-updated",
		"sw-product-detail-save-finish", "telemetry",
	} {
		require.Contains(t, realWorldAdminMemberNames(adminEventMap.Members), eventName)
	}
	adminEventBusCompletionSource := `const { EventBus } = Shopware.Utils;
EventBus.on('sw-media', handler)`
	adminEventBusCompletionDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminContainerDocumentPath), adminEventBusCompletionSource, 6,
	)
	adminEventBusCompletions := lspcompletion.NewAdminCompletionProvider(
		adminContainerIndex,
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminEventBusCompletionDocument,
			uint32(strings.LastIndex(adminEventBusCompletionSource, "sw-media")+
				len("sw-media")),
		),
	)
	require.Contains(
		t, realWorldCompletionLabels(adminEventBusCompletions),
		"sw-media-library-item-updated",
	)
	adminEventBusDefinitionSource := `const bus = Shopware.Utils.EventBus;
bus.emit('sw-media-library-item-updated', mediaId)`
	adminEventBusDefinitionDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminContainerDocumentPath), adminEventBusDefinitionSource, 7,
	)
	adminEventBusDefinitionOffset := uint32(
		strings.LastIndex(
			adminEventBusDefinitionSource, "sw-media-library-item-updated",
		) + 1,
	)
	adminEventBusDefinitions := lspdefinition.NewAdminDefinitionProvider(
		adminContainerIndex,
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminEventBusDefinitionDocument,
			adminEventBusDefinitionDocument.SyntaxTree.Root.NodeAtOffset(
				adminEventBusDefinitionOffset,
			),
			adminEventBusDefinitionOffset,
		),
	)
	require.Len(t, adminEventBusDefinitions, 1)
	require.Equal(
		t, uriutil.FileURI(adminEventBusPath), adminEventBusDefinitions[0].URI,
	)
	require.Greater(t, adminEventBusDefinitions[0].Range.Start.Character, 0)
	adminEventBusReferences, err := lspreference.NewAdminReferenceProvider(
		adminContainerIndex,
	).GetReferences(
		ctx,
		realWorldReferenceRequest(
			adminEventBusDefinitionDocument, adminEventBusDefinitionOffset, true,
		),
	)
	require.NoError(t, err)
	require.Greater(t, len(adminEventBusReferences), 5)
	var foundAdminEventDeclaration bool
	for _, location := range adminEventBusReferences {
		if location.URI == uriutil.FileURI(adminEventBusPath) {
			foundAdminEventDeclaration = true
			break
		}
	}
	require.True(t, foundAdminEventDeclaration)
	adminEventBusRenamePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/component/media",
		"sw-model-editor/index.ts",
	)
	adminEventBusRenameSource, err := os.ReadFile(adminEventBusRenamePath)
	require.NoError(t, err)
	adminEventBusRenameDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminEventBusRenamePath),
		string(adminEventBusRenameSource),
		1,
	)
	adminEventBusRenameOffset := uint32(strings.Index(
		string(adminEventBusRenameSource), "sw-media-library-item-updated",
	) + 2)
	adminEventBusRenameEdit, err := lsprefactor.NewAdminRenameProvider(
		adminContainerIndex,
	).Rename(
		ctx,
		realWorldRenameRequest(
			adminEventBusRenameDocument,
			adminEventBusRenameOffset,
			"sw-media-library-item-refreshed",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, adminEventBusRenameEdit)
	require.Greater(t, adminWorkspaceEditCount(adminEventBusRenameEdit), 5)
	adminEventBusDeclarationSource, err := os.ReadFile(adminEventBusPath)
	require.NoError(t, err)
	renamedAdminEventDeclaration := applyRealWorldTextEdits(
		t,
		adminEventBusDeclarationSource,
		adminEventBusRenameEdit.Changes[uriutil.FileURI(adminEventBusPath)],
	)
	require.Contains(
		t, renamedAdminEventDeclaration,
		"'sw-media-library-item-refreshed': string",
	)
	renamedAdminEventConsumer := applyRealWorldTextEdits(
		t,
		adminEventBusRenameSource,
		adminEventBusRenameEdit.Changes[uriutil.FileURI(adminEventBusRenamePath)],
	)
	require.Contains(
		t, renamedAdminEventConsumer,
		"EventBus.on('sw-media-library-item-refreshed'",
	)
	adminEventBusSignature, err := lspsignature.NewAdminSignatureProvider(
		adminContainerIndex,
	).GetSignatureHelp(
		ctx,
		realWorldSignatureRequest(
			adminEventBusDefinitionDocument,
			uint32(strings.LastIndex(adminEventBusDefinitionSource, "mediaId")+1),
		),
	)
	require.NoError(t, err)
	require.NotNil(t, adminEventBusSignature)
	require.Len(t, adminEventBusSignature.Signatures, 1)
	require.Equal(
		t,
		`emit(event: "sw-media-library-item-updated", payload: string): void`,
		adminEventBusSignature.Signatures[0].Label,
	)
	require.Equal(t, 1, adminEventBusSignature.ActiveParameter)
	adminEventBusDiagnosticSource := []byte(`
Shopware.Utils.EventBus.emit('sw-media-library-item-updated', 42);
Shopware.Utils.EventBus.emit('extension-owned-event', 42);
`)
	adminEventBusProblems, err := lspdiagnostics.NewAdminAnalyzer(
		adminContainerIndex,
	).Analyze(
		ctx,
		lsp.NewTextDocument(
			uriutil.FileURI(adminContainerDocumentPath),
			string(adminEventBusDiagnosticSource), 8,
		),
	)
	require.NoError(t, err)
	require.Len(t, adminEventBusProblems, 1)
	require.Equal(
		t, lsp.DiagnosticID("admin.event-bus.payload-type"),
		adminEventBusProblems[0].ID,
	)
	adminEventBusProblemRange := adminEventBusProblems[0].Range
	require.Equal(
		t, "42", string(adminEventBusDiagnosticSource[adminEventBusProblemRange.Start:adminEventBusProblemRange.End]),
	)
	adminUtilsCompletionSource := `const format = Shopware.Utils.format;
format.`
	adminUtilsCompletionDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminContainerDocumentPath), adminUtilsCompletionSource, 6,
	)
	adminUtilsCompletions := lspcompletion.NewAdminCompletionProvider(
		adminContainerIndex,
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminUtilsCompletionDocument,
			uint32(len(adminUtilsCompletionSource)-1),
		),
	)
	adminUtilsLabels := realWorldCompletionLabels(adminUtilsCompletions)
	for _, memberName := range []string{"currency", "date", "fileSize"} {
		require.Contains(t, adminUtilsLabels, memberName)
	}
	adminUtilsDefinitionSource := `const { date: formatDate } = Shopware.Utils.format;
formatDate('2026-01-01')`
	adminUtilsDefinitionDocument := lsp.NewTextDocument(
		uriutil.FileURI(adminContainerDocumentPath), adminUtilsDefinitionSource, 7,
	)
	adminUtilsDefinitionOffset := uint32(
		strings.LastIndex(adminUtilsDefinitionSource, "formatDate") + 1,
	)
	adminUtilsDefinitions := lspdefinition.NewAdminDefinitionProvider(
		adminContainerIndex,
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminUtilsDefinitionDocument,
			adminUtilsDefinitionDocument.SyntaxTree.Root.NodeAtOffset(
				adminUtilsDefinitionOffset,
			),
			adminUtilsDefinitionOffset,
		),
	)
	require.Len(t, adminUtilsDefinitions, 1)
	require.Equal(t, uriutil.FileURI(adminUtilsPath), adminUtilsDefinitions[0].URI)
	require.Greater(t, adminUtilsDefinitions[0].Range.Start.Character, 0)
	adminUtilsSignatureOffset := uint32(
		strings.LastIndex(adminUtilsDefinitionSource, "2026") + len("2026"),
	)
	adminUtilsSignature, err := lspsignature.NewAdminSignatureProvider(
		adminContainerIndex,
	).GetSignatureHelp(
		ctx,
		realWorldSignatureRequest(
			adminUtilsDefinitionDocument, adminUtilsSignatureOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, adminUtilsSignature)
	require.Len(t, adminUtilsSignature.Signatures, 1)
	require.Contains(t, adminUtilsSignature.Signatures[0].Label, "date(val: string")
	require.Contains(t, adminUtilsSignature.Signatures[0].Label, "): string")
	adminRegistryCompletionSource := `Module.getModuleRegistry().get('sw-pro')`
	adminRegistryCompletionDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/registry-real-world.js",
		)),
		adminRegistryCompletionSource,
		1,
	)
	adminRegistryCompletionOffset := uint32(
		strings.Index(adminRegistryCompletionSource, "sw-pro") + len("sw-pro"),
	)
	adminRegistryCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			adminRegistryCompletionDocument,
			adminRegistryCompletionOffset,
		),
	)
	require.Contains(
		t,
		realWorldCompletionLabels(adminRegistryCompletions),
		"sw-product",
	)
	adminRegistryDefinitionSource := `Module.getModuleRegistry().get('sw-product')`
	adminRegistryDefinitionDocument := lsp.NewTextDocument(
		adminRegistryCompletionDocument.URI,
		adminRegistryDefinitionSource,
		2,
	)
	adminRegistryDefinitionOffset := uint32(
		strings.Index(adminRegistryDefinitionSource, "sw-product") + 2,
	)
	adminRegistryDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminRegistryDefinitionDocument,
			adminRegistryDefinitionDocument.SyntaxTree.Root.NodeAtOffset(
				adminRegistryDefinitionOffset,
			),
			adminRegistryDefinitionOffset,
		),
	)
	require.NotEmpty(t, adminRegistryDefinitions)
	require.Equal(t, uriutil.FileURI(productModule.FilePath), adminRegistryDefinitions[0].URI)
	adminRegistryHover, err := lsphover.NewAdminHoverProvider(
		root,
		workspaceAdminIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			adminRegistryDefinitionDocument,
			adminRegistryDefinitionOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, adminRegistryHover)
	require.Contains(t, adminRegistryHover.Contents.Value, "Administration module")
	require.Contains(t, adminRegistryHover.Contents.Value, "`sw-product`")
	adminComponentRegistrySource := `Shopware.Component.getComponentRegistry().has('sw-product-list')`
	adminComponentRegistryDocument := lsp.NewTextDocument(
		adminRegistryCompletionDocument.URI,
		adminComponentRegistrySource,
		3,
	)
	adminComponentRegistryOffset := uint32(
		strings.Index(adminComponentRegistrySource, "sw-product-list") + 2,
	)
	adminComponentRegistryDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminComponentRegistryDocument,
			adminComponentRegistryDocument.SyntaxTree.Root.NodeAtOffset(
				adminComponentRegistryOffset,
			),
			adminComponentRegistryOffset,
		),
	)
	require.NotEmpty(t, adminComponentRegistryDefinitions)
	registrySpecPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/module",
		"sw-settings-message-stats/index.spec.js",
	)
	registrySpecSource, err := os.ReadFile(registrySpecPath)
	require.NoError(t, err)
	registrySpecProblems, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, lsp.NewTextDocument(
		uriutil.FileURI(registrySpecPath), string(registrySpecSource), 1,
	))
	require.NoError(t, err)
	for _, problem := range registrySpecProblems {
		require.NotEqual(t, "admin.component.registry-not-found", string(problem.ID))
		require.NotEqual(t, "admin.module.not-found", string(problem.ID))
	}
	adminPrivilegeSource := `<mt-button :disabled="acl.can('product.v')" />`
	adminPrivilegeDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/privilege-real-world.html.twig",
		)),
		adminPrivilegeSource,
		1,
	)
	adminPrivilegeOffset := uint32(
		strings.Index(adminPrivilegeSource, "product.v") + len("product.v"),
	)
	adminPrivilegeCompletions := lspcompletion.NewAdminCompletionProvider(
		workspaceAdminIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(adminPrivilegeDocument, adminPrivilegeOffset),
	)
	require.Contains(
		t,
		realWorldCompletionLabels(adminPrivilegeCompletions),
		"product.viewer",
	)
	adminPrivilegeDefinitionSource := `<mt-button :disabled="acl.can('product.viewer')" />`
	adminPrivilegeDefinitionDocument := lsp.NewTextDocument(
		adminPrivilegeDocument.URI,
		adminPrivilegeDefinitionSource,
		2,
	)
	adminPrivilegeDefinitionOffset := uint32(
		strings.Index(adminPrivilegeDefinitionSource, "product.viewer") + 2,
	)
	adminPrivilegeDefinitions := lspdefinition.NewAdminDefinitionProvider(
		workspaceAdminIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			adminPrivilegeDefinitionDocument,
			adminPrivilegeDefinitionDocument.SyntaxTree.Root.NodeAtOffset(
				adminPrivilegeDefinitionOffset,
			),
			adminPrivilegeDefinitionOffset,
		),
	)
	require.NotEmpty(t, adminPrivilegeDefinitions)
	require.Equal(
		t,
		uriutil.FileURI(productViewerPrivileges[0].FilePath),
		adminPrivilegeDefinitions[0].URI,
	)
	adminPrivilegeTemplatePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/module/sw-product/component",
		"sw-product-properties/sw-product-properties.html.twig",
	)
	adminPrivilegeTemplateSource, err := os.ReadFile(adminPrivilegeTemplatePath)
	require.NoError(t, err)
	adminPrivilegeDiagnostics, err := lspdiagnostics.NewAdminAnalyzer(
		workspaceAdminIndex(t, workspace),
	).Analyze(ctx, lsp.NewTextDocument(
		uriutil.FileURI(adminPrivilegeTemplatePath),
		string(adminPrivilegeTemplateSource),
		1,
	))
	require.NoError(t, err)
	for _, problem := range adminPrivilegeDiagnostics {
		require.NotEqual(t, "admin.privilege.not-found", string(problem.ID))
	}
	adminSymbolProvider := lspsymbol.NewAdminWorkspaceSymbolProvider(
		workspaceAdminIndex(t, workspace),
	)
	for _, expected := range []struct {
		name      string
		container string
	}{
		{"acl", "Administration service"},
		{"session", "Administration store"},
		{"setCurrentUser", "store · session"},
		{"product.viewer", "ACL role"},
	} {
		symbols, symbolErr := adminSymbolProvider.WorkspaceSymbols(
			ctx,
			expected.name,
		)
		require.NoError(t, symbolErr)
		requireWorkspaceSymbol(
			t,
			symbols,
			expected.name,
			expected.container,
		)
	}
	for _, expected := range []struct {
		query     string
		name      string
		container string
	}{
		{"deprecated", "deprecated", "sw-card · component prop"},
		{"#default", "default", "sw-card · component slot"},
	} {
		symbols, symbolErr := adminSymbolProvider.WorkspaceSymbols(
			ctx,
			expected.query,
		)
		require.NoError(t, symbolErr)
		requireWorkspaceSymbol(
			t,
			symbols,
			expected.name,
			expected.container,
		)
	}
	swCardTemplatePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/component/base/sw-card/sw-card.html.twig",
	)
	swCardDefinitionPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/component/base/sw-card/index.ts",
	)
	swCardTemplateLenses := realWorldCodeLenses(
		t,
		ctx,
		codelens.NewAdminComponentCodeLensProvider(
			workspaceAdminIndex(t, workspace),
		),
		swCardTemplatePath,
	)
	requireRelatedLensFileTargets(
		t,
		swCardTemplateLenses,
		"component definition",
		uriutil.FileURI(swCardDefinitionPath),
		1,
	)
	adminDocumentSymbolProvider := lspsymbol.NewAdminDocumentSymbolProvider(
		workspaceAdminIndex(t, workspace),
	)
	swCardDefinitionSymbols := realWorldDocumentSymbols(
		t, ctx, adminDocumentSymbolProvider, swCardDefinitionPath,
	)
	require.Len(t, swCardDefinitionSymbols, 1)
	require.Equal(t, "sw-card", swCardDefinitionSymbols[0].Name)
	requireDocumentSymbolNamed(
		t, swCardDefinitionSymbols[0].Children, "deprecated",
	)
	requireDocumentSymbolNamed(
		t, swCardDefinitionSymbols[0].Children, "getSlots",
	)
	swCardTemplateSymbols := realWorldDocumentSymbols(
		t, ctx, adminDocumentSymbolProvider, swCardTemplatePath,
	)
	require.Len(t, swCardTemplateSymbols, 1)
	require.Equal(t, "sw-card", swCardTemplateSymbols[0].Name)
	requireDocumentSymbolNamed(
		t, swCardTemplateSymbols[0].Children, "sw_card",
	)
	requireDocumentSymbolNamedRecursive(
		t, swCardTemplateSymbols[0].Children, "default",
	)
	adminSelectionProvider := lspselection.NewAdminSelectionRangeProvider()
	swCardDefinitionSelections := realWorldSelectionTexts(
		t, ctx, adminSelectionProvider, swCardDefinitionPath, "getSlots",
	)
	require.GreaterOrEqual(t, len(swCardDefinitionSelections), 5)
	require.Equal(t, "getSlots", swCardDefinitionSelections[0])
	requireRealWorldSelectionContaining(
		t, swCardDefinitionSelections, "return this.$slots",
	)
	swCardTemplateSelections := realWorldSelectionTexts(
		t, ctx, adminSelectionProvider, swCardTemplatePath, "getSlots",
	)
	require.GreaterOrEqual(t, len(swCardTemplateSelections), 7)
	require.Equal(t, "getSlots", swCardTemplateSelections[0])
	requireRealWorldSelectionContaining(
		t, swCardTemplateSelections, `v-for="(index, name) in getSlots()"`,
	)
	adminFoldingProvider := lspfolding.NewAdminFoldingProvider()
	swCardDefinitionFolds := realWorldFoldingRanges(
		t, ctx, adminFoldingProvider, swCardDefinitionPath,
	)
	requireRealWorldFoldingRange(
		t, swCardDefinitionFolds, 2, 10, protocol.FoldingRangeKindComment,
	)
	requireRealWorldFoldingRange(t, swCardDefinitionFolds, 11, 26, "")
	requireRealWorldFoldingRange(t, swCardDefinitionFolds, 14, 19, "")
	requireRealWorldFoldingRange(t, swCardDefinitionFolds, 22, 25, "")
	requireRealWorldFoldingRange(t, swCardDefinitionFolds, 23, 24, "")
	swCardTemplateFolds := realWorldFoldingRanges(
		t, ctx, adminFoldingProvider, swCardTemplatePath,
	)
	requireRealWorldFoldingRange(t, swCardTemplateFolds, 1, 17, "")
	requireRealWorldFoldingRange(t, swCardTemplateFolds, 2, 16, "")
	requireRealWorldFoldingRange(t, swCardTemplateFolds, 21, 35, "")
	adminColorProvider := lspcolor.NewAdminSCSSColorProvider()
	variablesSCSSPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/assets/scss/variables.scss",
	)
	variablesColors, variablesDocument := realWorldDocumentColors(
		t, ctx, adminColorProvider, variablesSCSSPath,
	)
	var gray50 *protocol.ColorInformation
	for index := range variablesColors {
		if realWorldRangeText(variablesDocument, variablesColors[index].Range) == "#f9fafb" {
			gray50 = &variablesColors[index]
			break
		}
	}
	require.NotNil(t, gray50, "real Administration variables must expose #f9fafb")
	require.InDelta(t, 249.0/255, gray50.Color.Red, 0.00001)
	require.InDelta(t, 250.0/255, gray50.Color.Green, 0.00001)
	require.InDelta(t, 251.0/255, gray50.Color.Blue, 0.00001)
	require.Equal(t, 1.0, gray50.Color.Alpha)
	presentations, err := adminColorProvider.GetColorPresentations(
		ctx,
		&lsp.ColorPresentationRequest{
			ColorPresentationParams: &protocol.ColorPresentationParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: variablesDocument.URI},
				Color:        gray50.Color,
				Range:        gray50.Range,
			},
			Document: variablesDocument,
		},
	)
	require.NoError(t, err)
	require.Len(t, presentations, 3)
	require.Equal(t, "#f9fafb", presentations[0].Label)
	require.Equal(t, "rgb(249, 250, 251)", presentations[1].Label)

	mixinsSCSSPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/assets/scss/mixins.scss",
	)
	mixinColors, mixinsDocument := realWorldDocumentColors(
		t, ctx, adminColorProvider, mixinsSCSSPath,
	)
	var eightPercentShadow *protocol.ColorInformation
	for index := range mixinColors {
		if realWorldRangeText(mixinsDocument, mixinColors[index].Range) ==
			"rgba(0, 0, 0, 8%)" {
			eightPercentShadow = &mixinColors[index]
			break
		}
	}
	require.NotNil(
		t, eightPercentShadow,
		"real Administration mixins must expose rgba percentage alpha",
	)
	require.InDelta(t, .08, eightPercentShadow.Color.Alpha, 0.00001)
	swCardHighlights := realWorldDocumentHighlights(
		t,
		ctx,
		lsphighlight.NewAdminDocumentHighlightProvider(
			workspaceAdminIndex(t, workspace),
		),
		swCardTemplatePath,
		"getSlots",
	)
	require.Len(t, swCardHighlights, 2)
	for _, item := range swCardHighlights {
		require.Equal(t, protocol.DocumentHighlightText, item.Kind)
	}
	swCardLinkedEditing := realWorldLinkedEditingRanges(
		t,
		ctx,
		lsplinkedediting.NewAdminLinkedEditingProvider(),
		swCardTemplatePath,
		"sw-card-deprecated",
	)
	require.NotNil(t, swCardLinkedEditing)
	require.Len(t, swCardLinkedEditing.Ranges, 2)
	adminCallHierarchyProvider := lspcallhierarchy.NewAdminCallHierarchyProvider(
		workspaceAdminIndex(t, workspace),
	)
	swCardCallItems := realWorldCallHierarchyItems(
		t,
		ctx,
		adminCallHierarchyProvider,
		swCardDefinitionPath,
		"getSlots",
	)
	require.Len(t, swCardCallItems, 1)
	require.Equal(t, "getSlots", swCardCallItems[0].Name)
	swCardIncomingCalls, err := adminCallHierarchyProvider.IncomingCalls(
		ctx,
		&lsp.CallHierarchyCallsRequest{Item: swCardCallItems[0]},
	)
	require.NoError(t, err)
	var swCardTemplateCaller *protocol.CallHierarchyIncomingCall
	for index := range swCardIncomingCalls {
		if swCardIncomingCalls[index].From.URI ==
			uriutil.FileURI(swCardTemplatePath) {
			swCardTemplateCaller = &swCardIncomingCalls[index]
			break
		}
	}
	require.NotNil(
		t,
		swCardTemplateCaller,
		"real sw-card template must call its indexed getSlots method",
	)
	require.Equal(t, "sw-card template", swCardTemplateCaller.From.Name)
	require.Len(t, swCardTemplateCaller.FromRanges, 2)
	swCardTemplateOutgoingCalls, err := adminCallHierarchyProvider.OutgoingCalls(
		ctx,
		&lsp.CallHierarchyCallsRequest{Item: swCardTemplateCaller.From},
	)
	require.NoError(t, err)
	var getSlotsOutgoing *protocol.CallHierarchyOutgoingCall
	for index := range swCardTemplateOutgoingCalls {
		if swCardTemplateOutgoingCalls[index].To.Name == "getSlots" &&
			swCardTemplateOutgoingCalls[index].To.URI ==
				uriutil.FileURI(swCardDefinitionPath) {
			getSlotsOutgoing = &swCardTemplateOutgoingCalls[index]
			break
		}
	}
	require.NotNil(t, getSlotsOutgoing)
	require.Len(t, getSlotsOutgoing.FromRanges, 2)
	swPageUsages, err := workspaceAdminIndex(t, workspace).GetUsages(
		admin.AdminSymbolComponent, "", "sw-page",
	)
	require.NoError(t, err)
	swPageUsageCount := adminUsageOccurrenceCount(swPageUsages)
	require.Greater(t, swPageUsageCount, 150)
	aclUsages, err := workspaceAdminIndex(t, workspace).GetUsages(
		admin.AdminSymbolService, "", "acl",
	)
	require.NoError(t, err)
	aclUsageCount := adminUsageOccurrenceCount(aclUsages)
	require.Greater(t, aclUsageCount, 20)
	sessionUsages, err := workspaceAdminIndex(t, workspace).GetUsages(
		admin.AdminSymbolStore, "", "session",
	)
	require.NoError(t, err)
	sessionUsageCount := adminUsageOccurrenceCount(sessionUsages)
	require.Greater(t, sessionUsageCount, 15)
	productViewerUsages, err := workspaceAdminIndex(t, workspace).GetUsages(
		admin.AdminSymbolPrivilege, "", "product.viewer",
	)
	require.NoError(t, err)
	productViewerUsageCount := adminUsageOccurrenceCount(productViewerUsages)
	require.Greater(t, productViewerUsageCount, 10)
	var productViewerTwigUsage bool
	for _, usage := range productViewerUsages {
		if strings.HasSuffix(usage.FilePath, ".twig") {
			productViewerTwigUsage = true
			break
		}
	}
	require.True(t, productViewerTwigUsage)
	productDetailRouteUsages, err := workspaceAdminIndex(t, workspace).GetUsages(
		admin.AdminSymbolModuleRoute, "", "sw.product.detail",
	)
	require.NoError(t, err)
	productDetailRouteUsageCount := adminUsageOccurrenceCount(
		productDetailRouteUsages,
	)
	require.GreaterOrEqual(t, productDetailRouteUsageCount, 6)
	var productDetailRouteTwigUsage bool
	for _, usage := range productDetailRouteUsages {
		if strings.HasSuffix(usage.FilePath, ".twig") {
			productDetailRouteTwigUsage = true
			break
		}
	}
	require.True(t, productDetailRouteTwigUsage)
	settingsMessageStatsModuleUsages, err := workspaceAdminIndex(
		t, workspace,
	).GetUsages(
		admin.AdminSymbolModule, "", "sw-settings-message-stats",
	)
	require.NoError(t, err)
	settingsMessageStatsModuleUsageCount := adminUsageOccurrenceCount(
		settingsMessageStatsModuleUsages,
	)
	require.GreaterOrEqual(t, settingsMessageStatsModuleUsageCount, 3)
	t.Logf(
		"administration usages: sw-page=%d, acl=%d, session=%d, product.viewer=%d",
		swPageUsageCount,
		aclUsageCount,
		sessionUsageCount,
		productViewerUsageCount,
	)
	adminServiceCount := len(adminServices)
	adminStoreCount := len(adminStores)
	adminPrivilegeCount := len(adminPrivileges)
	dalDefinitions, err := workspaceDALIndex(t, workspace).Definitions()
	require.NoError(t, err)
	require.NotEmpty(t, dalDefinitions)
	productDefinitions, err := workspaceDALIndex(t, workspace).Definition("product")
	require.NoError(t, err)
	require.NotEmpty(t, productDefinitions)
	require.NotEmpty(t, productDefinitions[0].Fields)
	dalEntitySource := `Shopware.EntityDefinition.get('prod')`
	dalEntityDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/dal-entity-real-world.ts",
		)),
		dalEntitySource,
		1,
	)
	dalEntityOffset := uint32(strings.Index(dalEntitySource, "prod") + 2)
	dalEntityCompletions := lspcompletion.NewDALCompletionProvider(
		workspaceDALIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(dalEntityDocument, dalEntityOffset),
	)
	require.Contains(
		t, realWorldCompletionLabels(dalEntityCompletions), "product",
	)
	dalEntityDefinitionSource := `Shopware.EntityDefinition.get('product')`
	dalEntityDefinitionDocument := lsp.NewTextDocument(
		dalEntityDocument.URI, dalEntityDefinitionSource, 2,
	)
	dalEntityDefinitionOffset := uint32(
		strings.Index(dalEntityDefinitionSource, "product") + 2,
	)
	dalEntityDefinitions := lspdefinition.NewDALDefinitionProvider(
		workspaceDALIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			dalEntityDefinitionDocument,
			dalEntityDefinitionDocument.SyntaxTree.Root.NodeAtOffset(
				dalEntityDefinitionOffset,
			),
			dalEntityDefinitionOffset,
		),
	)
	require.NotEmpty(t, dalEntityDefinitions)
	require.Contains(
		t, filepath.ToSlash(dalEntityDefinitions[0].URI),
		"/ProductDefinition.php",
	)
	dalEntityHover, err := lsphover.NewDALHoverProvider(
		workspaceDALIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(
			dalEntityDefinitionDocument, dalEntityDefinitionOffset,
		),
	)
	require.NoError(t, err)
	require.NotNil(t, dalEntityHover)
	require.Contains(t, dalEntityHover.Contents.Value, "Shopware DAL entity")
	require.Contains(t, dalEntityHover.Contents.Value, "ProductDefinition")
	dalEntityTypoDocument := lsp.NewTextDocument(
		dalEntityDocument.URI,
		`Shopware.EntityDefinition.get('prodcut')`,
		3,
	)
	dalEntityProblems, err := lspdiagnostics.NewDALEntityAnalyzer(
		workspaceDALIndex(t, workspace),
	).Analyze(ctx, dalEntityTypoDocument)
	require.NoError(t, err)
	require.Len(t, dalEntityProblems, 1)
	require.Equal(
		t,
		lsp.DiagnosticID("shopware.dal.entity-not-found"),
		dalEntityProblems[0].ID,
	)
	dalEntitySymbols, err := lspsymbol.NewDALWorkspaceSymbolProvider(
		workspaceDALIndex(t, workspace),
	).WorkspaceSymbols(ctx, "ProductDefinition")
	require.NoError(t, err)
	var productEntitySymbol bool
	for _, current := range dalEntitySymbols {
		if current.Name == "product" {
			productEntitySymbol = true
			break
		}
	}
	require.True(t, productEntitySymbol)
	dalCriteriaCompletionSource := `Criteria.equals('manu', value)`
	dalCriteriaCompletionDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/dal-real-world.js",
		)),
		dalCriteriaCompletionSource,
		1,
	)
	dalCriteriaCompletionOffset := uint32(
		strings.Index(dalCriteriaCompletionSource, "manu") + len("manu"),
	)
	dalCriteriaCompletions := lspcompletion.NewDALCompletionProvider(
		workspaceDALIndex(t, workspace),
	).GetCompletions(
		ctx,
		realWorldCompletionRequest(
			dalCriteriaCompletionDocument,
			dalCriteriaCompletionOffset,
		),
	)
	require.Contains(
		t,
		realWorldCompletionLabels(dalCriteriaCompletions),
		"manufacturer",
	)
	dalCriteriaDefinitionSource := `Criteria.equals('manufacturer.name', value)`
	dalCriteriaDefinitionDocument := lsp.NewTextDocument(
		dalCriteriaCompletionDocument.URI,
		dalCriteriaDefinitionSource,
		2,
	)
	manufacturerOffset := uint32(
		strings.Index(dalCriteriaDefinitionSource, "manufacturer") + 2,
	)
	manufacturerDefinitions := lspdefinition.NewDALDefinitionProvider(
		workspaceDALIndex(t, workspace),
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			dalCriteriaDefinitionDocument,
			dalCriteriaDefinitionDocument.SyntaxTree.Root.NodeAtOffset(
				manufacturerOffset,
			),
			manufacturerOffset,
		),
	)
	require.NotEmpty(t, manufacturerDefinitions)
	var manufacturerDefinitionFound bool
	for _, location := range manufacturerDefinitions {
		if strings.Contains(
			filepath.ToSlash(location.URI),
			"/ProductDefinition.php",
		) {
			manufacturerDefinitionFound = true
			break
		}
	}
	require.True(t, manufacturerDefinitionFound)
	nameOffset := uint32(
		strings.Index(dalCriteriaDefinitionSource, ".name") + len(".n"),
	)
	dalCriteriaHover, err := lsphover.NewDALHoverProvider(
		workspaceDALIndex(t, workspace),
	).GetHover(
		ctx,
		realWorldHoverRequest(dalCriteriaDefinitionDocument, nameOffset),
	)
	require.NoError(t, err)
	require.NotNil(t, dalCriteriaHover)
	require.Contains(t, dalCriteriaHover.Contents.Value, "Shopware DAL field")
	require.Contains(t, dalCriteriaHover.Contents.Value, "StringField")
	dalDefinitionCount := len(dalDefinitions)
	appScriptHooks, err := workspaceAppScriptIndex(t, workspace).Hooks()
	require.NoError(t, err)
	require.NotEmpty(t, appScriptHooks)
	appScriptFacades, err := workspaceAppScriptIndex(t, workspace).Facades()
	require.NoError(t, err)
	require.NotEmpty(t, appScriptFacades)
	appScriptHookCount := len(appScriptHooks)
	appScriptFacadeCount := len(appScriptFacades)
	semanticDiagnostics := lspphpsemantic.New(phpIndex)
	for _, relativePath := range []string{
		"src/Core/Framework/Demodata/DemodataContext.php",
		"src/Core/Framework/Api/Controller/ApiController.php",
		"src/Core/Framework/Gateway/Context/Command/Handler/ChangeShippingLocationCommandHandler.php",
		"src/Core/DevOps/StaticAnalyze/PHPStan/Rules/NoUpdatesInExecuteQueryRule.php",
		"src/Core/Framework/Adapter/Kernel/KernelFactory.php",
		"src/Core/Content/Product/Cms/ManufacturerLogoCmsElementResolver.php",
	} {
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		source, err := os.ReadFile(path)
		require.NoError(t, err)
		diagnostics, err := semanticDiagnostics.Analyze(
			ctx,
			lsp.NewTextDocument(
				uriutil.FileURI(path),
				string(source),
				1,
			),
		)
		require.NoError(t, err)
		require.Empty(
			t,
			diagnostics,
			"%s must remain free of PHP semantic false positives",
			relativePath,
		)
	}
	productVariables, err := phpIndex.TwigTemplateVariables(
		"@Storefront/storefront/page/content/product-detail.html.twig",
	)
	require.NoError(t, err)
	requireTwigVariable(t, productVariables, "page")
	requireTwigVariable(t, productVariables, "redirectTo")
	loopCompletionSource := `{% for entry in entries %}{{ loop. }}{% endfor %}`
	loopCompletionLabels := realWorldCompletionLabels(
		realWorldTwigCompletions(
			root,
			workspaceTwigIndex(t, workspace),
			phpIndex,
			loopCompletionSource,
			"loop.",
		),
	)
	require.Contains(t, loopCompletionLabels, "index")
	_, twig4LoopContextInstalled := phpIndex.FindClass(
		twig.TwigLoopContextClass,
	)
	if twig4LoopContextInstalled {
		require.Contains(t, loopCompletionLabels, "previous")
		require.Contains(t, loopCompletionLabels, "depth0")
	} else {
		require.Contains(t, loopCompletionLabels, "revindex")
		require.NotContains(t, loopCompletionLabels, "previous")
	}
	productTemplateDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"Storefront",
			"Resources",
			"views",
			"storefront",
			"page",
			"content",
			"product-detail.html.twig",
		)),
		"",
		1,
	)
	productVariableHints, err := lspinlay.NewTwigVariableProvider(
		phpIndex,
	).GetInlayHints(
		ctx,
		&lsp.InlayHintRequest{
			InlayHintParams: &protocol.InlayHintParams{
				Range: protocol.Range{},
			},
			Document: productTemplateDocument,
		},
	)
	require.NoError(t, err)
	require.Len(t, productVariableHints, 1)
	productVariableParts, ok := productVariableHints[0].Label.([]protocol.InlayHintLabelPart)
	require.True(t, ok)
	require.Len(t, productVariableParts, 1)
	require.Equal(
		t,
		fmt.Sprintf("Variables (%d)", len(productVariables)),
		productVariableParts[0].Value,
	)
	require.NotNil(t, productVariableParts[0].Command)
	require.Equal(
		t,
		lspinlay.BrowseTwigVariablesCommand,
		productVariableParts[0].Command.Command,
	)
	administrationTemplatePath := filepath.Join(
		root,
		"src",
		"Administration",
		"Resources",
		"views",
		"administration",
		"index.html.twig",
	)
	administrationTemplateSource, err := os.ReadFile(
		administrationTemplatePath,
	)
	require.NoError(t, err)
	administrationDocument := lsp.NewTextDocument(
		uriutil.FileURI(administrationTemplatePath),
		string(administrationTemplateSource),
		1,
	)
	administrationLinkParams := &protocol.DocumentLinkParams{}
	administrationLinkParams.TextDocument.URI = administrationDocument.URI
	administrationLinks, err := lspdocumentlink.NewRelatedProvider(
		workspaceTwigIndex(t, workspace),
		workspaceSymfonyConfigIndex(t, workspace),
		phpIndex,
	).GetDocumentLinks(
		ctx,
		&lsp.DocumentLinkRequest{
			DocumentLinkParams: administrationLinkParams,
			Document:           administrationDocument,
		},
	)
	require.NoError(t, err)
	require.Contains(
		t,
		realWorldDocumentLinkTargets(administrationLinks),
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"Administration",
			"Resources",
			"views",
			"administration",
			"layout",
			"base.html.twig",
		)),
	)
	navigationInlayControllerPath := filepath.Join(
		root,
		"src",
		"Storefront",
		"Controller",
		"NavigationController.php",
	)
	navigationControllerSource, err := os.ReadFile(
		navigationInlayControllerPath,
	)
	require.NoError(t, err)
	navigationDocument := lsp.NewTextDocument(
		uriutil.FileURI(navigationInlayControllerPath),
		string(navigationControllerSource),
		1,
	)
	navigationInlayParams := &protocol.InlayHintParams{}
	navigationInlayParams.TextDocument.URI = navigationDocument.URI
	navigationEndLine, navigationEndCharacter :=
		navigationDocument.LineIndex.PositionUTF16(
			uint32(len(navigationDocument.Source)),
		)
	navigationInlayParams.Range.End = protocol.Position{
		Line:      int(navigationEndLine),
		Character: int(navigationEndCharacter),
	}
	navigationHints, err := lspinlay.NewRouteControllerProvider(
		workspaceServiceIndex(t, workspace),
		phpIndex,
	).GetInlayHints(
		ctx,
		&lsp.InlayHintRequest{
			InlayHintParams: navigationInlayParams,
			Document:        navigationDocument,
		},
	)
	require.NoError(t, err)
	var homeControllerLocation string
	for _, hint := range navigationHints {
		parts, ok := hint.Label.([]protocol.InlayHintLabelPart)
		if !ok || len(parts) != 1 ||
			parts[0].Value != "→ NavigationController::home" ||
			parts[0].Location == nil {
			continue
		}
		homeControllerLocation = parts[0].Location.URI
		break
	}
	require.Equal(
		t,
		uriutil.FileURI(navigationInlayControllerPath),
		homeControllerLocation,
	)
	templateDeclarationDocument := lsp.NewTextDocument(
		"file:///real-world-template-declaration.php",
		`<?php
namespace App\Controller;
use Symfony\Bridge\Twig\Attribute\Template;
class RealWorldController
{
    #[Template('@Storefront/storefront/page/content/product-detail.html.twig')]
    public function existing(): array { return []; }

    #[Template]
    public function missingAction(): array { return []; }
}
`,
		1,
	)
	templateDeclarationDiagnostics, err := lspdiagnostics.
		NewTemplateAnalyzer(
			workspaceTwigIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, templateDeclarationDocument)
	require.NoError(t, err)
	require.Len(t, templateDeclarationDiagnostics, 1)
	require.Equal(
		t,
		"twig.template.missing",
		fmt.Sprint(templateDeclarationDiagnostics[0].ID),
	)
	require.Equal(
		t,
		"real_world/missing.html.twig",
		templateDeclarationDiagnostics[0].Payload.(map[string]any)["templateName"],
	)
	configCommands, err := workspaceConsoleIndex(t, workspace).GetCommand(
		"system:config:get",
	)
	require.NoError(t, err)
	require.NotEmpty(t, configCommands)
	for _, command := range configCommands {
		requireConsoleInput(t, command.Arguments, "key")
		requireConsoleInput(t, command.Options, "format")
		requireConsoleInput(t, command.Options, "salesChannelId")
	}
	consoleCatalogRequest := console.CatalogRequest{
		Query:    "system:config:get",
		FileGlob: "src/**/Command/*.php",
	}
	consoleCatalog, err := console.NewCatalogProvider(
		workspaceConsoleIndex(t, workspace),
		root,
	).CatalogWithRequest(ctx, consoleCatalogRequest)
	require.NoError(t, err)
	require.Len(t, consoleCatalog, 1)
	require.Equal(t, "system:config:get", consoleCatalog[0].Name)
	require.Equal(
		t,
		"Shopware\\Core\\System\\SystemConfig\\Command\\ConfigGet",
		consoleCatalog[0].Class,
	)
	require.NotEmpty(t, consoleCatalog[0].FilePath)
	require.True(
		t,
		pathmatch.Ant(
			consoleCatalogRequest.FileGlob,
			consoleCatalog[0].FilePath,
		),
	)
	requireConsoleCatalogInput(t, consoleCatalog[0].Arguments, "key")
	requireConsoleCatalogInput(t, consoleCatalog[0].Options, "format")
	requireConsoleCatalogInput(
		t,
		consoleCatalog[0].Options,
		"salesChannelId",
	)
	commandAliases, err := workspaceConsoleIndex(t, workspace).GetCommand(
		"snippets:validate",
	)
	require.NoError(t, err)
	require.NotEmpty(t, commandAliases)
	require.Equal(t, "translation:validate", commandAliases[0].Canonical)
	invokableCommandDocument := lsp.NewTextDocument(
		"file:///real-world-invokable-command.php",
		`<?php
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;

#[AsCommand(name: 'app:real-world')]
class RealWorldCommand
{
    public function __invoke()
    {
        if (random_int(0, 1)) {
            return Command::SUCCESS;
        }
        return 'invalid';
    }
}
`,
		1,
	)
	invokableCommandDiagnostics, err := lspdiagnostics.
		NewInvokableCommandAnalyzer(phpIndex).
		Analyze(ctx, invokableCommandDocument)
	require.NoError(t, err)
	var commandReturnTypes, commandReturnValues int
	for _, diagnostic := range invokableCommandDiagnostics {
		switch fmt.Sprint(diagnostic.ID) {
		case "symfony.console.invoke.return_type":
			commandReturnTypes++
		case "symfony.console.invoke.return_value":
			commandReturnValues++
		}
	}
	require.Equal(t, 1, commandReturnTypes)
	require.Equal(t, 1, commandReturnValues)
	cartMerged, found, err := workspaceEventIndex(t, workspace).GetEvent(
		"Shopware\\Core\\Checkout\\Cart\\Event\\CartMergedEvent",
	)
	require.NoError(t, err)
	require.True(t, found)
	requireEventListener(
		t,
		cartMerged,
		"Shopware\\Storefront\\Event\\CartMergedSubscriber",
		"addCartMergedNoticeFlash",
	)
	kernelRequest, found, err := workspaceEventIndex(t, workspace).GetEvent(
		"kernel.request",
	)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, kernelRequest.Listeners())
	messengerMessages, err := workspaceMessengerIndex(
		t,
		workspace,
	).Messages()
	require.NoError(t, err)
	require.NotEmpty(t, messengerMessages)
	collectMessage, found, err := workspaceMessengerIndex(
		t,
		workspace,
	).GetMessage(
		"Shopware\\Core\\System\\UsageData\\EntitySync\\" +
			"CollectEntityDataMessage",
	)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, collectMessage.Handlers())
	require.NotEmpty(t, collectMessage.Dispatches())
	environmentVariables, err := workspaceEnvironmentIndex(
		t,
		workspace,
	).Variables()
	require.NoError(t, err)
	require.NotEmpty(t, environmentVariables)
	appEnv, found, err := workspaceEnvironmentIndex(
		t,
		workspace,
	).Variable("APP_ENV")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, appEnv.Declarations)
	require.GreaterOrEqual(t, len(appEnv.References), 2)
	serviceDefinitions, err := workspaceServiceIndex(
		t,
		workspace,
	).GetAllServiceDefinitions()
	require.NoError(t, err)
	deprecatedServiceCount := 0
	for _, service := range serviceDefinitions {
		if service.Deprecated {
			deprecatedServiceCount++
		}
	}
	require.GreaterOrEqual(t, deprecatedServiceCount, 5)
	const realParameterName = "shopware.messenger.enforce_message_size"
	realParameter, found, err := workspaceServiceIndex(
		t,
		workspace,
	).GetParameterByName(realParameterName)
	require.NoError(t, err)
	require.True(t, found)
	parameterBagSource := fmt.Sprintf(`<?php
use Symfony\Component\DependencyInjection\ParameterBag\ParameterBagInterface;
function inspect(ParameterBagInterface $bag): void {
    $bag->get('%s');
    $bag->has('real_world.missing_parameter');
}`, realParameterName)
	parameterBagDocument := lsp.NewTextDocument(
		"file:///real-world-parameter-bag.php",
		parameterBagSource,
		1,
	)
	parameterOffset := uint32(
		strings.Index(parameterBagSource, realParameterName) + 3,
	)
	parameterNode := parameterBagDocument.SyntaxTree.Root.NodeAtOffset(
		parameterOffset,
	)
	parameterContext := phpIndex.AddDocumentContext(
		ctx,
		"/real-world-parameter-bag.php",
		1,
		parameterNode,
		parameterBagDocument.SyntaxTree.Root,
	)
	parameterLine, parameterCharacter := parameterBagDocument.LineIndex.
		PositionUTF16(parameterOffset)
	parameterCompletionParams := &protocol.CompletionParams{}
	parameterCompletionParams.TextDocument.URI = parameterBagDocument.URI
	parameterCompletionParams.Position.Line = int(parameterLine)
	parameterCompletionParams.Position.Character = int(parameterCharacter)
	parameterCompletionLabels := realWorldCompletionLabels(
		lspcompletion.NewServiceCompletionProvider(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).GetCompletions(
			parameterContext,
			&lsp.CompletionRequest{
				CompletionParams: parameterCompletionParams,
				SyntaxContext: lsp.SyntaxContext{
					Document:        parameterBagDocument,
					Language:        parameterBagDocument.SyntaxLanguage,
					DocumentContent: parameterBagDocument.Text,
					DocumentTree:    parameterBagDocument.SyntaxTree,
					LineIndex:       parameterBagDocument.LineIndex,
					Root:            parameterBagDocument.SyntaxTree.Root,
					Node:            parameterNode,
				},
			},
		),
	)
	require.Contains(
		t,
		parameterCompletionLabels,
		realParameterName,
	)
	parameterDefinitions := lspdefinition.
		NewServiceXMLDefinitionProvider(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		GetDefinition(
			parameterContext,
			realWorldDefinitionRequest(
				parameterBagDocument,
				parameterNode,
				parameterOffset,
			),
		)
	require.Len(t, parameterDefinitions, 1)
	require.Equal(
		t,
		uriutil.FileURI(realParameter.Path),
		parameterDefinitions[0].URI,
	)
	parameterDiagnostics, err := lspdiagnostics.
		NewServiceAnalyzer(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, parameterBagDocument)
	require.NoError(t, err)
	require.Len(t, parameterDiagnostics, 1)
	require.Equal(
		t,
		"symfony.parameter.missing",
		fmt.Sprint(parameterDiagnostics[0].ID),
	)
	type configuredMethodFixture struct {
		path       string
		targetPath string
		method     string
	}
	configuredMethodFixtures := []configuredMethodFixture{
		{
			path: filepath.Join(root, "config", "services_test.xml"),
			targetPath: filepath.Join(
				root,
				"src",
				"Core",
				"Framework",
				"Telemetry",
				"Metrics",
				"Transport",
				"TransportCollection.php",
			),
			method: "create",
		},
		{
			path: filepath.Join(
				root,
				"tests",
				"integration",
				"Core",
				"Framework",
				"Plugin",
				"_fixtures",
				"plugins",
				"SwagTestPlugin",
				"src",
				"Resources",
				"config",
				"services.xml",
			),
			targetPath: filepath.Join(
				root,
				"tests",
				"integration",
				"Core",
				"Framework",
				"Plugin",
				"_fixtures",
				"plugins",
				"SwagTestPlugin",
				"src",
				"SwagTestPlugin.php",
			),
			method: "manualSetter",
		},
		{
			path: filepath.Join(
				root,
				"src",
				"Core",
				"System",
				"DependencyInjection",
				"snippet.xml",
			),
			targetPath: filepath.Join(
				root,
				"src",
				"Core",
				"System",
				"Snippet",
				"Service",
				"TranslationConfigLoader.php",
			),
			method: "load",
		},
	}
	var configuredMethodFixtureValue configuredMethodFixture
	for _, fixture := range configuredMethodFixtures {
		if _, err := os.Stat(fixture.path); err != nil {
			continue
		}
		if _, err := os.Stat(fixture.targetPath); err != nil {
			continue
		}
		configuredMethodFixtureValue = fixture
		break
	}
	var configuredMethodDocument *lsp.TextDocument
	var configuredMethodNode *cst.Node
	var configuredMethodCompletionParams *protocol.CompletionParams
	var configuredMethodCompletions []protocol.CompletionItem
	var configuredMethodDefinition []protocol.Location
	var configuredMethodOffset uint32
	if configuredMethodFixtureValue.path == "" {
		t.Log("configured XML service-method fixture is not present in this Shopware checkout")
	} else {
		configuredMethodPath := configuredMethodFixtureValue.path
		configuredMethodTargetPath := configuredMethodFixtureValue.targetPath
		configuredMethodName := configuredMethodFixtureValue.method
		configuredMethodSource, err := os.ReadFile(configuredMethodPath)
		require.NoError(t, err)
		configuredMethodDocument = lsp.NewTextDocument(
			uriutil.FileURI(configuredMethodPath),
			string(configuredMethodSource),
			1,
		)
		configuredMethodMarker := `method="` + configuredMethodName + `"`
		configuredMethodPrefix := `method="` +
			configuredMethodName[:max(1, len(configuredMethodName)/2)]
		configuredMethodOffset = uint32(
			strings.Index(string(configuredMethodSource), configuredMethodMarker) +
				len(configuredMethodPrefix),
		)
		require.Greater(
			t,
			configuredMethodOffset,
			uint32(len(configuredMethodPrefix)),
		)
		configuredMethodNode = configuredMethodDocument.SyntaxTree.Root.
			NodeAtOffset(configuredMethodOffset)
		configuredMethodLine, configuredMethodCharacter :=
			configuredMethodDocument.LineIndex.PositionUTF16(
				configuredMethodOffset,
			)
		configuredMethodCompletionParams = &protocol.CompletionParams{}
		configuredMethodCompletionParams.TextDocument.URI =
			configuredMethodDocument.URI
		configuredMethodCompletionParams.Position.Line =
			int(configuredMethodLine)
		configuredMethodCompletionParams.Position.Character =
			int(configuredMethodCharacter)
		configuredMethodCompletions = lspcompletion.
			NewServiceCompletionProvider(
				workspaceServiceIndex(t, workspace),
				phpIndex,
			).
			GetCompletions(
				ctx,
				&lsp.CompletionRequest{
					CompletionParams: configuredMethodCompletionParams,
					SyntaxContext: lsp.SyntaxContext{
						Document:        configuredMethodDocument,
						Language:        configuredMethodDocument.SyntaxLanguage,
						DocumentContent: configuredMethodDocument.Text,
						DocumentTree:    configuredMethodDocument.SyntaxTree,
						LineIndex:       configuredMethodDocument.LineIndex,
						Root:            configuredMethodDocument.SyntaxTree.Root,
						Node:            configuredMethodNode,
					},
				},
			)
		configuredMethodCompletion := realWorldCompletionByLabel(
			t,
			configuredMethodCompletions,
			configuredMethodName,
		)
		configuredMethodEdit, ok := configuredMethodCompletion.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		require.Equal(t, configuredMethodName, configuredMethodEdit.NewText)
		configuredMethodDefinition = lspdefinition.
			NewServiceXMLDefinitionProvider(
				workspaceServiceIndex(t, workspace),
				phpIndex,
			).
			GetDefinition(
				ctx,
				realWorldDefinitionRequest(
					configuredMethodDocument,
					configuredMethodNode,
					configuredMethodOffset,
				),
			)
		require.Len(t, configuredMethodDefinition, 1)
		require.Equal(
			t,
			uriutil.FileURI(configuredMethodTargetPath),
			configuredMethodDefinition[0].URI,
		)
	}
	serviceOptionSource := "services:\n  app.real_world:\n    aut"
	serviceOptionDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "config", "services.yaml")),
		serviceOptionSource,
		1,
	)
	serviceAuthoringProvider := lspcompletion.
		NewYAMLServiceAuthoringCompletionProvider(phpIndex.Project())
	serviceOptionCompletions := serviceAuthoringProvider.GetCompletions(
		ctx,
		realWorldCompletionRequest(
			serviceOptionDocument,
			uint32(len(serviceOptionSource)),
		),
	)
	autowireOption := realWorldCompletionByLabel(
		t,
		serviceOptionCompletions,
		"autowire",
	)
	autowireOptionEdit, ok := autowireOption.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, "autowire: ", autowireOptionEdit.NewText)
	legacyFactoryOption := realWorldCompletionByLabel(
		t,
		serviceOptionCompletions,
		"factory_class",
	)
	require.True(t, legacyFactoryOption.Deprecated)

	serviceArgumentTagSource := "services:\n" +
		"  app.real_world:\n" +
		"    arguments: [!tag"
	serviceArgumentTagDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "config", "services.yaml")),
		serviceArgumentTagSource,
		1,
	)
	serviceArgumentTagCompletions := serviceAuthoringProvider.GetCompletions(
		ctx,
		realWorldCompletionRequest(
			serviceArgumentTagDocument,
			uint32(len(serviceArgumentTagSource)),
		),
	)
	serviceArgumentTagLabels := realWorldCompletionLabels(
		serviceArgumentTagCompletions,
	)
	require.Contains(t, serviceArgumentTagLabels, "!tagged_iterator")
	require.Contains(t, serviceArgumentTagLabels, "!tagged_locator")
	require.NotContains(t, serviceArgumentTagLabels, "!tagged")
	routeOptionSource := "real_world.catalog:\n" +
		"  path: /real-world/{id}\n" +
		"  meth"
	routeOptionDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "config", "routes.yaml")),
		routeOptionSource,
		1,
	)
	routeAuthoringProvider := lspcompletion.
		NewYAMLRouteAuthoringCompletionProvider(
			workspaceRouteIndex(t, workspace),
		)
	routeOptionCompletions := routeAuthoringProvider.GetCompletions(
		ctx,
		realWorldCompletionRequest(
			routeOptionDocument,
			uint32(len(routeOptionSource)),
		),
	)
	routeMethodsOption := realWorldCompletionByLabel(
		t,
		routeOptionCompletions,
		"methods",
	)
	routeMethodsEdit, ok := routeMethodsOption.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, "methods: ", routeMethodsEdit.NewText)
	legacyPatternOption := realWorldCompletionByLabel(
		t,
		routeOptionCompletions,
		"pattern",
	)
	require.True(t, legacyPatternOption.Deprecated)

	routeRequirementSource := "real_world.catalog:\n" +
		"  path: /real-world/{id}/{slug}\n" +
		"  requirements:\n" +
		"    sl"
	routeRequirementDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "config", "routes.yaml")),
		routeRequirementSource,
		1,
	)
	routeRequirementCompletions := routeAuthoringProvider.GetCompletions(
		ctx,
		realWorldCompletionRequest(
			routeRequirementDocument,
			uint32(len(routeRequirementSource)),
		),
	)
	routeSlugRequirement := realWorldCompletionByLabel(
		t,
		routeRequirementCompletions,
		"slug",
	)
	routeSlugEdit, ok := routeSlugRequirement.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, "slug: ", routeSlugEdit.NewText)
	deprecatedNotification, found, err := workspaceServiceIndex(
		t,
		workspace,
	).GetServiceByID(
		"Shopware\\Administration\\Notification\\NotificationDefinition",
	)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, deprecatedNotification.Deprecated)
	serviceLocatorRequest := analytics.ServiceLocatorRequest{
		Identifier: deprecatedNotification.ID,
	}
	serviceLocatorStarted := time.Now()
	serviceLocatorCatalog, err := analytics.NewServiceLocatorProvider(
		workspaceServiceIndex(t, workspace),
		phpIndex,
	).Locate(ctx, serviceLocatorRequest)
	require.NoError(t, err)
	t.Logf(
		"service locator analytics: %s (%d matches)",
		time.Since(serviceLocatorStarted).Round(time.Millisecond),
		len(serviceLocatorCatalog),
	)
	locatedNotification := requireAnalyticsService(
		t,
		serviceLocatorCatalog,
		deprecatedNotification.ID,
	)
	require.Equal(
		t,
		deprecatedNotification.Class,
		locatedNotification.ResolvedClass,
	)
	require.True(t, locatedNotification.Deprecated)
	require.NotEmpty(t, locatedNotification.Definitions)
	serviceDefinitionRequest :=
		codeaction.SymfonyServiceDefinitionCollectionRequest{
			ClassNames: deprecatedNotification.Class,
			Output:     "yaml",
			ClassAsID:  true,
		}
	serviceDefinitionCollection, err := codeaction.
		NewSymfonyGeneratorProvider(
			phpIndex,
			workspaceServiceIndex(t, workspace),
		).
		CollectServiceDefinitions(ctx, serviceDefinitionRequest)
	require.NoError(t, err)
	require.Len(t, serviceDefinitionCollection.Definitions, 1)
	require.Empty(t, serviceDefinitionCollection.Definitions[0].Error)
	require.Contains(
		t,
		serviceDefinitionCollection.Definitions[0].Content,
		deprecatedNotification.Class+":",
	)
	entityForm, found, err := workspaceFormIndex(t, workspace).GetType("entity")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(
		t,
		"Symfony\\Bridge\\Doctrine\\Form\\Type\\EntityType",
		entityForm.Class,
	)
	entityOptions, err := workspaceFormIndex(t, workspace).EffectiveOptions(
		entityForm.Class,
	)
	require.NoError(t, err)
	requireFormOption(t, entityOptions, "class")
	requireFormOption(t, entityOptions, "em")
	requireFormOption(t, entityOptions, "query_builder")
	entityFormCatalogRequest := analytics.FormTypeCatalogRequest{
		Query: "entity",
	}
	entityFormCatalogProvider := analytics.NewFormCatalogProvider(
		root,
		workspaceFormIndex(t, workspace),
		phpIndex,
	)
	entityFormCatalog, err := entityFormCatalogProvider.Types(
		ctx,
		entityFormCatalogRequest,
	)
	require.NoError(t, err)
	entityFormCatalogEntry := requireAnalyticsFormType(
		t,
		entityFormCatalog,
		"entity",
	)
	require.Equal(t, entityForm.Class, entityFormCatalogEntry.ClassName)
	require.NotEmpty(t, entityFormCatalogEntry.FileURI)
	entityFormOptionRequest := analytics.FormOptionCatalogRequest{
		FormType: "entity",
	}
	entityFormOptionCatalog, err := entityFormCatalogProvider.Options(
		ctx,
		entityFormOptionRequest,
	)
	require.NoError(t, err)
	entityClassOption := requireAnalyticsFormOption(
		t,
		entityFormOptionCatalog,
		"class",
	)
	require.NotEmpty(t, entityClassOption.Kinds)
	require.NotEmpty(t, entityClassOption.SourceClass)
	realFormGeneratorSource := `<?php
namespace App\Form;

use Shopware\Core\Content\Product\ProductEntity;
use Symfony\Component\Form\AbstractType;
use Symfony\Component\Form\FormBuilderInterface;
use Symfony\Component\OptionsResolver\OptionsResolver;

class ProductType extends AbstractType
{
    public function configureOptions(OptionsResolver $resolver): void
    {
        $resolver->setDefaults(['data_class' => ProductEntity::class]);
    }

    public function buildForm(FormBuilderInterface $builder, array $options): void
    {
    }
}
`
	realFormGeneratorDocument := lsp.NewTextDocument(
		"file:///real-world-product-form.php",
		realFormGeneratorSource,
		1,
	)
	realFormGenerator := codeaction.NewFormFieldGeneratorProvider(
		workspaceFormIndex(t, workspace),
		phpIndex,
		workspaceDoctrineIndex(t, workspace),
	)
	realFormGeneratorActions := realFormGenerator.GetCodeActions(
		ctx,
		realWorldCodeActionRequest(
			t,
			realFormGeneratorDocument,
			"function buildForm",
		),
	)
	require.Len(t, realFormGeneratorActions, 1)
	require.Equal(
		t,
		"shopware.symfony.generateFormFields",
		realFormGeneratorActions[0].Command.Command,
	)
	formCandidatePayload, err := json.Marshal(map[string]any{
		"fileUri":   realFormGeneratorDocument.URI,
		"className": "App\\Form\\ProductType",
		"source":    realFormGeneratorSource,
		"version":   1,
	})
	require.NoError(t, err)
	formCandidateRaw := json.RawMessage(formCandidatePayload)
	formCandidateCommand := realFormGenerator.GetCommands(ctx)["shopware/symfony/form/fields/candidates"]
	formCandidateValue, err := formCandidateCommand(ctx, &formCandidateRaw)
	require.NoError(t, err)
	formCandidateJSON, err := json.Marshal(formCandidateValue)
	require.NoError(t, err)
	var formCandidateResponse struct {
		DataClass string                   `json:"dataClass"`
		Fields    []realWorldFormCandidate `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(
		formCandidateJSON,
		&formCandidateResponse,
	))
	require.Equal(
		t,
		"Shopware\\Core\\Content\\Product\\ProductEntity",
		formCandidateResponse.DataClass,
	)
	requireFormGeneratorCandidate(
		t,
		formCandidateResponse.Fields,
		"active",
		"CheckboxType",
	)
	legacyFormAliasDocument := lsp.NewTextDocument(
		"file:///real-world-legacy-form-alias.php",
		`<?php
use Symfony\Component\Form\FormBuilderInterface;

function build(FormBuilderInterface $builder): void
{
    $builder->add('product', 'entity');
}
`,
		1,
	)
	legacyFormAliasDiagnostics, err := lspdiagnostics.
		NewFormAnalyzer(
			workspaceFormIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, legacyFormAliasDocument)
	require.NoError(t, err)
	var deprecatedFormAliases []lsp.Problem
	for _, diagnostic := range legacyFormAliasDiagnostics {
		if fmt.Sprint(diagnostic.ID) == "symfony.form.type.legacy_alias" {
			deprecatedFormAliases = append(
				deprecatedFormAliases,
				diagnostic,
			)
		}
	}
	require.Len(t, deprecatedFormAliases, 1)
	require.Equal(
		t,
		"Symfony\\Bridge\\Doctrine\\Form\\Type\\EntityType",
		deprecatedFormAliases[0].Payload.(map[string]any)["className"],
	)
	yamlCompatibilityDocument := lsp.NewTextDocument(
		"file:///real-world-yaml-compatibility.yaml",
		"escaped: \"App\\Invalid\"\n"+
			"reference: @logger\n"+
			"label: left: right\n",
		1,
	)
	yamlCompatibilityDiagnostics, err := lspdiagnostics.
		NewYAMLCompatibilityAnalyzer(phpIndex.Project()).
		Analyze(ctx, yamlCompatibilityDocument)
	require.NoError(t, err)
	yamlCompatibilityCodes := make(map[string]int)
	for _, diagnostic := range yamlCompatibilityDiagnostics {
		yamlCompatibilityCodes[fmt.Sprint(diagnostic.ID)]++
	}
	require.Equal(t, map[string]int{
		"symfony.yaml.quoted_escape":      1,
		"symfony.yaml.unquoted_indicator": 1,
		"symfony.yaml.unquoted_colon":     1,
	}, yamlCompatibilityCodes)
	securityNames, err := workspaceSecurityIndex(t, workspace).Names()
	require.NoError(t, err)
	require.Contains(t, securityNames, "PUBLIC_ACCESS")
	require.Contains(t, securityNames, "IS_AUTHENTICATED_FULLY")
	securityProviders, err := workspaceSecurityIndex(
		t,
		workspace,
	).ConfigNames(security.ConfigProvider)
	require.NoError(t, err)
	securityFirewalls, err := workspaceSecurityIndex(
		t,
		workspace,
	).ConfigNames(security.ConfigFirewall)
	require.NoError(t, err)
	configurationRoots, err := workspaceSymfonyConfigIndex(
		t,
		workspace,
	).Names()
	require.NoError(t, err)
	require.Contains(t, configurationRoots, "shopware")
	shopwareConfigurationRoots, err := workspaceSymfonyConfigIndex(
		t,
		workspace,
	).Roots("shopware")
	require.NoError(t, err)
	require.NotEmpty(t, shopwareConfigurationRoots)
	require.Equal(
		t,
		"Shopware\\Core\\Framework\\DependencyInjection\\Configuration",
		shopwareConfigurationRoots[0].Class,
	)
	serializerClasses, err := workspaceSerializerIndex(
		t,
		workspace,
	).Classes()
	require.NoError(t, err)
	doctrineModels, err := workspaceDoctrineIndex(t, workspace).Models()
	require.NoError(t, err)
	doctrineCatalogProvider := analytics.NewDoctrineCatalogProvider(
		root,
		workspaceDoctrineIndex(t, workspace),
	)
	doctrineCatalog, err := doctrineCatalogProvider.Entities(
		ctx,
		analytics.DoctrineEntityCatalogRequest{},
	)
	require.NoError(t, err)
	require.Len(t, doctrineCatalog, len(doctrineModels))
	translationDomains, err := workspaceTranslationIndex(
		t,
		workspace,
	).GetDomains()
	require.NoError(t, err)
	var assistantTranslationDomain, assistantTranslationKey string
	for _, domain := range translationDomains {
		keys, keysErr := workspaceTranslationIndex(
			t,
			workspace,
		).GetKeys(domain)
		require.NoError(t, keysErr)
		if len(keys) == 0 {
			continue
		}
		assistantTranslationDomain = domain
		assistantTranslationKey = keys[0]
		break
	}
	require.NotEmpty(t, assistantTranslationDomain)
	require.NotEmpty(t, assistantTranslationKey)
	const extractedTranslationText = "Real world extraction"
	const extractedTranslationKey = "shopware_lsp.real_world.extraction"
	var extractionDomain string
	for _, domain := range translationDomains {
		insertions, insertionErr := workspaceTranslationIndex(
			t,
			workspace,
		).InsertionsWithValue(
			domain,
			extractedTranslationKey,
			extractedTranslationText,
		)
		require.NoError(t, insertionErr)
		if len(insertions) != 0 {
			extractionDomain = domain
			break
		}
	}
	require.NotEmpty(
		t,
		extractionDomain,
		"the real project must expose a writable YAML/XLIFF translation domain",
	)
	extractionSource := fmt.Sprintf(
		"{%% trans_default_domain '%s' %%}<p>%s</p>",
		strings.ReplaceAll(extractionDomain, "'", `\'`),
		extractedTranslationText,
	)
	extractionLineIndex := cst.NewLineIndex(extractionSource)
	extractionStart := strings.Index(
		extractionSource,
		extractedTranslationText,
	)
	extractionEnd := extractionStart + len(extractedTranslationText)
	extractionStartLine, extractionStartCharacter := extractionLineIndex.
		PositionUTF16(uint32(extractionStart))
	extractionEndLine, extractionEndCharacter := extractionLineIndex.
		PositionUTF16(uint32(extractionEnd))
	extractionRange := protocol.Range{
		Start: protocol.Position{
			Line:      int(extractionStartLine),
			Character: int(extractionStartCharacter),
		},
		End: protocol.Position{
			Line:      int(extractionEndLine),
			Character: int(extractionEndCharacter),
		},
	}
	extractionProvider := codeaction.NewTwigTranslationExtractProvider(
		workspaceTranslationIndex(t, workspace),
	)
	extractionCommands := extractionProvider.GetCommands(ctx)
	extractionRequest := map[string]any{
		"fileUri": "file:///real-world-extraction.html.twig",
		"source":  extractionSource,
		"range":   extractionRange,
	}
	extractionRaw, err := json.Marshal(extractionRequest)
	require.NoError(t, err)
	preparedValue, err := extractionCommands["shopware/symfony/translation/extract/prepare"](ctx, (*json.RawMessage)(&extractionRaw))
	require.NoError(t, err)
	var extractionPreparation realWorldTranslationExtractionPreparation
	realWorldDecodeCommandResponse(
		t,
		preparedValue,
		&extractionPreparation,
	)
	require.Equal(t, extractedTranslationText, extractionPreparation.Text)
	require.Equal(t, extractionDomain, extractionPreparation.DefaultDomain)
	require.Contains(t, extractionPreparation.Domains, extractionDomain)
	extractionRequest["key"] = extractedTranslationKey
	extractionRequest["domain"] = extractionDomain
	extractionRequest["range"] = extractionPreparation.Range
	extractionRaw, err = json.Marshal(extractionRequest)
	require.NoError(t, err)
	extractedValue, err := extractionCommands["shopware/symfony/translation/extract/generate"](ctx, (*json.RawMessage)(&extractionRaw))
	require.NoError(t, err)
	var extractionEdits realWorldTranslationExtractionEdits
	realWorldDecodeCommandResponse(t, extractedValue, &extractionEdits)
	require.Equal(
		t,
		"{{ '"+extractedTranslationKey+"'|trans }}",
		extractionEdits.Replacement,
	)
	require.NotEmpty(t, extractionEdits.Targets)
	for _, target := range extractionEdits.Targets {
		require.NotEmpty(t, target.FileURI)
		require.Contains(t, target.NewText, extractedTranslationText)
	}
	htmlRoutes, err := workspaceRouteIndex(
		t,
		workspace,
	).FindRoutesByPath("/sitemap/shop.xml.gz")
	require.NoError(t, err)
	requireRoute(t, htmlRoutes, "frontend.sitemap.proxy")
	routeCatalogRequest := analytics.RouteCatalogRequest{
		RouteName: "frontend.home.page",
		FileGlob:  "src/**/NavigationController.php",
	}
	routeCatalogProvider := analytics.NewRouteCatalogProvider(
		root,
		workspaceRouteIndex(t, workspace),
		workspaceServiceIndex(t, workspace),
		phpIndex,
		workspaceTwigIndex(t, workspace),
	)
	allRouteCatalogStarted := time.Now()
	allRouteCatalog, err := routeCatalogProvider.Catalog(
		ctx,
		analytics.RouteCatalogRequest{},
	)
	require.NoError(t, err)
	require.Greater(t, len(allRouteCatalog), 100)
	t.Logf(
		"route analytics catalog: %s (%d routes)",
		time.Since(allRouteCatalogStarted).Round(time.Millisecond),
		len(allRouteCatalog),
	)
	profilerCatalog, profilerCatalogErr := analytics.
		NewProfilerCatalogProvider(
			root,
			phpIndex,
			workspaceTwigIndex(t, workspace),
		).
		Catalog(
			ctx,
			analytics.ProfilerRequestCatalogRequest{Limit: 3},
		)
	if profilerCatalogErr != nil {
		require.True(
			t,
			strings.Contains(
				profilerCatalogErr.Error(),
				"no local Symfony profiler index",
			) || strings.Contains(
				profilerCatalogErr.Error(),
				"no profiler requests were found",
			),
			"unexpected optional profiler analytics error: %v",
			profilerCatalogErr,
		)
	} else {
		require.NotEmpty(t, profilerCatalog)
		require.LessOrEqual(t, len(profilerCatalog), 3)
		for _, request := range profilerCatalog {
			require.NotEmpty(t, request.Hash)
			require.NotEmpty(t, request.IndexFileURI)
		}
	}
	routeCatalog, err := routeCatalogProvider.Catalog(ctx, routeCatalogRequest)
	require.NoError(t, err)
	require.Len(t, routeCatalog, 1)
	navigationControllerPath := filepath.Join(
		root,
		"src",
		"Storefront",
		"Controller",
		"NavigationController.php",
	)
	require.Equal(t, "frontend.home.page", routeCatalog[0].Name)
	require.Equal(t, "/", routeCatalog[0].Path)
	require.Equal(t, []string{"GET"}, routeCatalog[0].Methods)
	require.Equal(
		t,
		"Shopware\\Storefront\\Controller\\NavigationController::home",
		routeCatalog[0].ResolvedController,
	)
	require.Equal(
		t,
		uriutil.FileURI(navigationControllerPath),
		routeCatalog[0].ControllerURI,
	)
	require.Equal(t, 56, routeCatalog[0].ControllerLine)
	require.Contains(
		t,
		routeCatalog[0].Templates,
		"@Storefront/storefront/page/content/index.html.twig",
	)
	routePathSource := "real_world.draft:\n  path: /site"
	routePathDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "config", "routes.yaml")),
		routePathSource,
		1,
	)
	routePathCompletions := routeAuthoringProvider.GetCompletions(
		ctx,
		realWorldCompletionRequest(
			routePathDocument,
			uint32(len(routePathSource)),
		),
	)
	sitemapPathCompletion := realWorldCompletionByLabel(
		t,
		routePathCompletions,
		htmlRoutes[0].Path,
	)
	require.Equal(
		t,
		int(protocol.ReferenceCompletion),
		sitemapPathCompletion.Kind,
	)
	sitemapPathEdit, ok := sitemapPathCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, htmlRoutes[0].Path, sitemapPathEdit.NewText)
	phpRoutePathSource := `<?php
use Symfony\Component\Routing\Attribute\Route;

#[Route('/site')]
function draft(): void {}
`
	phpRoutePathDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "src", "DraftController.php")),
		phpRoutePathSource,
		1,
	)
	phpRoutePathOffset := uint32(
		strings.Index(phpRoutePathSource, "/site") + len("/site"),
	)
	phpRoutePathProvider := lspcompletion.NewRouteCompletionProvider(
		workspaceRouteIndex(t, workspace),
	)
	phpRoutePathCompletions := phpRoutePathProvider.GetCompletions(
		ctx,
		realWorldCompletionRequest(
			phpRoutePathDocument,
			phpRoutePathOffset,
		),
	)
	phpSitemapPathCompletion := realWorldCompletionByLabel(
		t,
		phpRoutePathCompletions,
		htmlRoutes[0].Path,
	)
	phpSitemapPathEdit, ok := phpSitemapPathCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, htmlRoutes[0].Path, phpSitemapPathEdit.NewText)

	annotationRoutePathSource := `<?php
/** @Route(path="/site") */
function draft(): void {}
`
	annotationRoutePathDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "src", "LegacyController.php")),
		annotationRoutePathSource,
		1,
	)
	annotationRoutePathOffset := uint32(
		strings.Index(annotationRoutePathSource, "/site") + len("/site"),
	)
	annotationRoutePathCompletions := phpRoutePathProvider.GetCompletions(
		ctx,
		realWorldCompletionRequest(
			annotationRoutePathDocument,
			annotationRoutePathOffset,
		),
	)
	realWorldCompletionByLabel(
		t,
		annotationRoutePathCompletions,
		htmlRoutes[0].Path,
	)
	routeNameSource := `<?php
namespace Shopware\Core\Controller;

class CatalogController
{
    #[Route(path: '/catalog', name: 'dr')]
    public function detailAction(): void {}
}
`
	routeNameDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "src", "CatalogController.php")),
		routeNameSource,
		1,
	)
	routeNameOffset := uint32(
		strings.Index(routeNameSource, "'dr'") + len("'dr"),
	)
	routeNameCompletions := phpRoutePathProvider.GetCompletions(
		ctx,
		realWorldCompletionRequest(routeNameDocument, routeNameOffset),
	)
	generatedRouteName := realWorldCompletionByLabel(
		t,
		routeNameCompletions,
		"shopware_core_catalog_detail",
	)
	generatedRouteNameEdit, ok := generatedRouteName.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(
		t,
		"shopware_core_catalog_detail",
		generatedRouteNameEdit.NewText,
	)

	annotationRouteNameSource := `<?php
namespace Shopware\Core\Controller;

class CatalogController
{
    /** @Route(path="/catalog", name="dr") */
    public function detailAction(): void {}
}
`
	annotationRouteNameDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"LegacyCatalogController.php",
		)),
		annotationRouteNameSource,
		1,
	)
	annotationRouteNameOffset := uint32(
		strings.Index(annotationRouteNameSource, `"dr"`) + len(`"dr`),
	)
	annotationRouteNameCompletions := phpRoutePathProvider.GetCompletions(
		ctx,
		realWorldCompletionRequest(
			annotationRouteNameDocument,
			annotationRouteNameOffset,
		),
	)
	realWorldCompletionByLabel(
		t,
		annotationRouteNameCompletions,
		"shopware_core_catalog_detail",
	)
	absoluteHTMLRoutes, err := workspaceRouteIndex(
		t,
		workspace,
	).FindRoutesByPath(
		"https://shopware.test/sitemap/shop.xml.gz?preview=1#details",
	)
	require.NoError(t, err)
	require.Equal(t, htmlRoutes, absoluteHTMLRoutes)
	routePrefixMatches, err := workspaceRouteIndex(
		t,
		workspace,
	).FindRoutesByPathPrefix(
		"/store-api/checkout/cart/line-item",
		"/store-api/checkout/cart/line-item/delete",
	)
	require.NoError(t, err)
	requireRoute(t, routePrefixMatches, "store-api.checkout.cart.add")
	requireRoute(t, routePrefixMatches, "store-api.checkout.cart.update-lineitem")
	requireRoute(t, routePrefixMatches, "store-api.checkout.cart.remove-item")
	for _, route := range routePrefixMatches {
		require.NotEqual(
			t,
			"store-api.checkout.cart.remove-item-v2",
			route.Name,
		)
	}
	bundleRouteImportPath := filepath.Join(
		root,
		"src",
		"Core",
		"Framework",
		"Resources",
		"config",
		"routes_dev.php",
	)
	bundleRouteTargetPath := filepath.Join(
		root,
		"vendor",
		"symfony",
		"framework-bundle",
		"Resources",
		"config",
		"routing",
		"errors.php",
	)
	bundleRouteImportSource, err := os.ReadFile(bundleRouteImportPath)
	require.NoError(t, err)
	bundleRouteDocument := lsp.NewTextDocument(
		uriutil.FileURI(bundleRouteImportPath),
		string(bundleRouteImportSource),
		1,
	)
	bundleRouteOffset := uint32(
		strings.Index(
			string(bundleRouteImportSource),
			"@FrameworkBundle",
		) + 3,
	)
	require.Greater(t, bundleRouteOffset, uint32(2))
	bundleRouteDefinition := lspdefinition.NewRouteDefinitionProvider(
		workspaceRouteIndex(t, workspace),
		phpIndex,
	).GetDefinition(
		ctx,
		realWorldDefinitionRequest(
			bundleRouteDocument,
			bundleRouteDocument.SyntaxTree.Root.NodeAtOffset(
				bundleRouteOffset,
			),
			bundleRouteOffset,
		),
	)
	require.Len(t, bundleRouteDefinition, 1)
	require.Equal(
		t,
		uriutil.FileURI(bundleRouteTargetPath),
		bundleRouteDefinition[0].URI,
	)
	bundleCompletionLine, bundleCompletionCharacter :=
		bundleRouteDocument.LineIndex.PositionUTF16(bundleRouteOffset)
	bundleCompletionParams := &protocol.CompletionParams{}
	bundleCompletionParams.TextDocument.URI = bundleRouteDocument.URI
	bundleCompletionParams.Position.Line = int(bundleCompletionLine)
	bundleCompletionParams.Position.Character = int(
		bundleCompletionCharacter,
	)
	bundleCompletionStarted := time.Now()
	bundleResourceCompletions := lspcompletion.
		NewBundleResourceCompletionProvider(phpIndex).
		GetCompletions(
			ctx,
			&lsp.CompletionRequest{
				CompletionParams: bundleCompletionParams,
				SyntaxContext: lsp.SyntaxContext{
					Document:        bundleRouteDocument,
					Language:        bundleRouteDocument.SyntaxLanguage,
					DocumentContent: bundleRouteDocument.Text,
					DocumentTree:    bundleRouteDocument.SyntaxTree,
					LineIndex:       bundleRouteDocument.LineIndex,
					Root:            bundleRouteDocument.SyntaxTree.Root,
					Node: bundleRouteDocument.SyntaxTree.Root.
						NodeAtOffset(bundleRouteOffset),
				},
			},
		)
	t.Logf(
		"bundle resource completion: %s (%d candidates)",
		time.Since(bundleCompletionStarted).Round(time.Millisecond),
		len(bundleResourceCompletions),
	)
	frameworkBundleCompletion := realWorldCompletionByLabel(
		t,
		bundleResourceCompletions,
		"FrameworkBundle/Resources/config/routing/errors.php",
	)
	frameworkBundleEdit, ok := frameworkBundleCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(
		t,
		"@FrameworkBundle/Resources/config/routing/errors.php",
		frameworkBundleEdit.NewText,
	)
	compiledRouteFiles, globErr := filepath.Glob(filepath.Join(
		root,
		"var",
		"cache",
		"*",
		"url_generating_routes.php",
	))
	require.NoError(t, globErr)
	if len(compiledRouteFiles) != 0 {
		compiledRoutes := workspaceRouteIndex(
			t,
			workspace,
		).GetCompiledRoutes()
		require.NotEmpty(
			t,
			compiledRoutes,
			"the generated route catalog should be loaded",
		)
		require.Equal(
			t,
			"url_generating_routes.php",
			filepath.Base(compiledRoutes[0].FilePath),
		)
	}
	routeComparisonSource := `{% if app.request.attributes.get('_route') in ['frontend.sitemap.proxy'] %}{% endif %}`
	routeComparisonDocument := lsp.NewTextDocument(
		"file:///real-world-route-comparison.twig",
		routeComparisonSource,
		1,
	)
	routeComparisonDiagnostics, err := lspdiagnostics.
		NewRouteAnalyzer(
			workspaceRouteIndex(t, workspace),
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, routeComparisonDocument)
	require.NoError(t, err)
	require.Empty(t, routeComparisonDiagnostics)
	routeComparisonOffset := uint32(
		strings.Index(routeComparisonSource, "frontend.sitemap.proxy") + 3,
	)
	routeComparisonNode := routeComparisonDocument.SyntaxTree.Root.
		NodeAtOffset(routeComparisonOffset)
	routeComparisonDefinition := lspdefinition.
		NewRouteDefinitionProvider(
			workspaceRouteIndex(t, workspace),
		).
		GetDefinition(
			ctx,
			realWorldDefinitionRequest(
				routeComparisonDocument,
				routeComparisonNode,
				routeComparisonOffset,
			),
		)
	require.NotEmpty(t, routeComparisonDefinition)
	requireRouteDefinitionPath(
		t,
		routeComparisonDefinition,
		htmlRoutes[0].FilePath,
	)
	deprecatedRouteDocument := lsp.NewTextDocument(
		"file:///real-world-deprecated-route.twig",
		`{{ path('widgets.account.order.detail') }}`,
		1,
	)
	deprecatedRouteDiagnostics, err := lspdiagnostics.
		NewRouteAnalyzer(
			workspaceRouteIndex(t, workspace),
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, deprecatedRouteDocument)
	require.NoError(t, err)
	require.Len(t, deprecatedRouteDiagnostics, 1)
	require.Equal(
		t,
		"symfony.controller.deprecated",
		fmt.Sprint(deprecatedRouteDiagnostics[0].ID),
	)
	require.Equal(
		t,
		[]protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
		deprecatedRouteDiagnostics[0].Tags,
	)
	routeAttributeDocument := lsp.NewTextDocument(
		"file:///real-world-route-attribute.php",
		`<?php
namespace App\Controller\Admin;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
class ProductController extends AbstractController
{
    public function editAction(): void {}
}`,
		1,
	)
	routeAttributeActions := codeaction.
		NewRouteAttributeCodeActionProvider(phpIndex).
		GetCodeActions(
			ctx,
			realWorldCodeActionRequest(
				t,
				routeAttributeDocument,
				"editAction",
			),
		)
	require.Len(t, routeAttributeActions, 1)
	routeAttributeEdits := routeAttributeActions[0].Edit.Changes[routeAttributeDocument.URI]
	require.Len(t, routeAttributeEdits, 2)
	require.Contains(
		t,
		routeAttributeEdits[0].NewText,
		"#[Route('/admin/product/edit', "+
			"name: 'app_admin_product_edit')]",
	)
	routeParameterDocument := lsp.NewTextDocument(
		"file:///real-world-route-parameter.php",
		`<?php
namespace App\Controller;
use Symfony\Component\Routing\Attribute\Route;
class ProductController
{
    #[Route('/product/{id}')]
    public function detail(?string $format = null): void {}
}`,
		1,
	)
	routeParameterActions := codeaction.
		NewRouteActionParameterCodeActionProvider().
		GetCodeActions(
			ctx,
			realWorldCodeActionRequest(
				t,
				routeParameterDocument,
				"detail",
			),
		)
	require.Len(t, routeParameterActions, 2)
	require.Equal(
		t,
		"Symfony: Add Request parameter to route action",
		routeParameterActions[0].Title,
	)
	routeParameterEdits := routeParameterActions[0].Edit.Changes[routeParameterDocument.URI]
	require.Len(t, routeParameterEdits, 2)
	require.Equal(t, "Request $request, ", routeParameterEdits[0].NewText)
	require.Contains(
		t,
		routeParameterEdits[1].NewText,
		"use Symfony\\Component\\HttpFoundation\\Request;",
	)
	_, found = phpIndex.FindClass(
		"Symfony\\Component\\Console\\Command\\InvokableCommand",
	)
	require.True(t, found)
	commandMigrationDocument := lsp.NewTextDocument(
		"file:///real-world-command-migration.php",
		`<?php
namespace App\Command;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputArgument;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;

#[AsCommand(name: 'app:index-migrate')]
class IndexMigrateCommand extends Command
{
    protected function configure(): void
    {
        $this->addArgument(
            'project',
            InputArgument::REQUIRED,
            'Project'
        );
        $this->addOption(
            'dry-run',
            'd',
            InputOption::VALUE_NONE,
            'Dry run'
        );
    }

    protected function execute(
        InputInterface $input,
        OutputInterface $output,
    ): int {
        $project = $input->getArgument('project');
        $dryRun = $input->getOption('dry-run');
        $output->writeln($project);

        return $dryRun ? self::SUCCESS : Command::SUCCESS;
    }
}`,
		1,
	)
	commandMigrationActions := codeaction.
		NewInvokableCommandMigrationCodeActionProvider(phpIndex).
		GetCodeActions(
			ctx,
			realWorldCodeActionRequest(
				t,
				commandMigrationDocument,
				"IndexMigrateCommand",
			),
		)
	require.Len(t, commandMigrationActions, 1)
	commandMigrationEdits := commandMigrationActions[0].Edit.
		Changes[commandMigrationDocument.URI]
	require.Len(t, commandMigrationEdits, 1)
	migratedCommandSource := commandMigrationEdits[0].NewText
	require.Contains(
		t,
		migratedCommandSource,
		"public function __invoke(",
	)
	require.Contains(
		t,
		migratedCommandSource,
		"#[Argument(description: 'Project')] string $project",
	)
	require.Contains(
		t,
		migratedCommandSource,
		"#[Option(name: 'dry-run', shortcut: 'd', "+
			"description: 'Dry run')] bool $dryRun = false",
	)
	require.NotContains(t, migratedCommandSource, "extends Command")
	require.NotContains(t, migratedCommandSource, "function configure")
	require.NotContains(t, migratedCommandSource, "$input->getArgument")
	require.Empty(
		t,
		lsp.NewTextDocument(
			commandMigrationDocument.URI,
			migratedCommandSource,
			2,
		).ParseErrors,
	)
	migratedCommandIndex, err := console.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, migratedCommandIndex.Close())
	})
	require.NoError(t, migratedCommandIndex.Index(indexer.NewParsedFile(
		"/real-world-command-migration.php",
		[]byte(migratedCommandSource),
	)))
	migratedCommands, err := migratedCommandIndex.GetCommand(
		"app:index-migrate",
	)
	require.NoError(t, err)
	require.Len(t, migratedCommands, 1)
	requireConsoleInput(t, migratedCommands[0].Arguments, "project")
	requireConsoleInput(t, migratedCommands[0].Options, "dry-run")
	_, found = phpIndex.FindClass(
		"Symfony\\Component\\Console\\Style\\SymfonyStyle",
	)
	require.True(t, found)
	commandParameterDocument := lsp.NewTextDocument(
		"file:///real-world-command-parameter.php",
		`<?php
namespace App\Command;
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand(name: 'app:index-test')]
class IndexTestCommand
{
    public function __invoke(?string $format = null): int
    {
        return 0;
    }
}`,
		1,
	)
	commandParameterActions := codeaction.
		NewCommandInvokeParameterCodeActionProvider(phpIndex).
		GetCodeActions(
			ctx,
			realWorldCodeActionRequest(
				t,
				commandParameterDocument,
				"IndexTestCommand",
			),
		)
	require.Len(t, commandParameterActions, 5)
	var styleParameterAction *protocol.CodeAction
	for index := range commandParameterActions {
		if strings.Contains(
			commandParameterActions[index].Title,
			"Add SymfonyStyle parameter",
		) {
			styleParameterAction = &commandParameterActions[index]
			break
		}
	}
	require.NotNil(t, styleParameterAction)
	styleParameterEdits := styleParameterAction.Edit.Changes[commandParameterDocument.URI]
	require.Len(t, styleParameterEdits, 2)
	require.Equal(t, "SymfonyStyle $io, ", styleParameterEdits[0].NewText)
	require.Contains(
		t,
		styleParameterEdits[1].NewText,
		"use Symfony\\Component\\Console\\Style\\SymfonyStyle;",
	)
	_, found = phpIndex.FindClass("Twig\\Attribute\\AsTwigFunction")
	require.True(t, found)
	twigMigrationDocument := lsp.NewTextDocument(
		"file:///real-world-twig-migration.php",
		`<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class IndexTestExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction('index_test', [$this, 'indexTest']),
        ];
    }

    public function indexTest(string $value): string
    {
        return $value;
    }
}`,
		1,
	)
	twigMigrationActions := codeaction.
		NewTwigExtensionAttributeCodeActionProvider(phpIndex).
		GetCodeActions(
			ctx,
			realWorldCodeActionRequest(
				t,
				twigMigrationDocument,
				"IndexTestExtension",
			),
		)
	require.Len(t, twigMigrationActions, 1)
	twigMigrationEdits := twigMigrationActions[0].Edit.Changes[twigMigrationDocument.URI]
	require.Len(t, twigMigrationEdits, 1)
	require.Contains(
		t,
		twigMigrationEdits[0].NewText,
		"#[AsTwigFunction('index_test')]",
	)
	require.NotContains(
		t,
		twigMigrationEdits[0].NewText,
		"extends AbstractExtension",
	)
	migratedTwigDocument := lsp.NewTextDocument(
		twigMigrationDocument.URI,
		twigMigrationEdits[0].NewText,
		2,
	)
	require.Empty(t, migratedTwigDocument.ParseErrors)
	migratedFunctions, migratedFilters, err := twig.ParseTwigExtensionTree(
		"/real-world-twig-migration.php",
		migratedTwigDocument.SyntaxTree.Root,
		migratedTwigDocument.Text,
		migratedTwigDocument.LineIndex,
	)
	require.NoError(t, err)
	require.Len(t, migratedFunctions, 1)
	require.Equal(t, "index_test", migratedFunctions[0].Name)
	require.Equal(
		t,
		"App\\Twig\\IndexTestExtension::indexTest",
		migratedFunctions[0].Method,
	)
	require.Empty(t, migratedFilters)
	_, found = phpIndex.FindClass("Doctrine\\Persistence\\ManagerRegistry")
	require.True(t, found)
	doctrineActionIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineActionIndex.Close()) })
	require.NoError(t, doctrineActionIndex.Index(indexer.NewParsedFile(
		"/real-world-doctrine-action.orm.yaml",
		[]byte(`Shopware\Core\Kernel:
  type: entity
`),
	)))
	doctrineActionDocument := lsp.NewTextDocument(
		"file:///real-world-doctrine-action.php",
		`<?php
namespace App\Service;
use Doctrine\Persistence\ManagerRegistry;
function load(ManagerRegistry $registry): void
{
    $registry->getRepository('Kernel');
}`,
		1,
	)
	doctrineActions := codeaction.
		NewDoctrineClassConstantCodeActionProvider(
			doctrineActionIndex,
			phpIndex,
		).
		GetCodeActions(
			ctx,
			realWorldCodeActionRequest(
				t,
				doctrineActionDocument,
				"Kernel",
			),
		)
	require.Len(t, doctrineActions, 1)
	doctrineActionEdits := doctrineActions[0].Edit.Changes[doctrineActionDocument.URI]
	require.Len(t, doctrineActionEdits, 2)
	require.Equal(t, "Kernel::class", doctrineActionEdits[0].NewText)
	require.Contains(
		t,
		doctrineActionEdits[1].NewText,
		"use Shopware\\Core\\Kernel;",
	)
	deprecatedTwigMemberDocument := lsp.NewTextDocument(
		"file:///real-world-deprecated-member.twig",
		`{# @var header \Shopware\Storefront\Pagelet\Header\HeaderPagelet #}
{{ header.activeLanguage }}`,
		1,
	)
	deprecatedTwigMemberDiagnostics, err := lspdiagnostics.
		NewTwigMemberDeprecationAnalyzer(
			workspaceTwigIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, deprecatedTwigMemberDocument)
	require.NoError(t, err)
	require.Len(t, deprecatedTwigMemberDiagnostics, 1)
	require.Equal(
		t,
		"twig.member.deprecated",
		fmt.Sprint(deprecatedTwigMemberDiagnostics[0].ID),
	)
	require.Contains(
		t,
		deprecatedTwigMemberDiagnostics[0].Message,
		"HeaderPagelet::getActiveLanguage",
	)
	require.Equal(
		t,
		[]protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
		deprecatedTwigMemberDiagnostics[0].Tags,
	)
	missingTwigMemberDocument := lsp.NewTextDocument(
		"file:///real-world-missing-member.twig",
		`{# @var context \Shopware\Core\Framework\Context #}
{{ context.versionId }}
{{ context.SYSTEM_SCOPE }}
{{ context.definitelyMissing }}`,
		1,
	)
	missingTwigMemberDiagnostics, err := lspdiagnostics.
		NewTwigMemberMissingAnalyzer(
			workspaceTwigIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, missingTwigMemberDocument)
	require.NoError(t, err)
	require.Len(t, missingTwigMemberDiagnostics, 1)
	require.Equal(
		t,
		"twig.member.missing",
		fmt.Sprint(missingTwigMemberDiagnostics[0].ID),
	)
	missingMemberStart := missingTwigMemberDiagnostics[0].Range.Start
	missingMemberEnd := missingTwigMemberDiagnostics[0].Range.End
	require.Equal(
		t,
		"definitelyMissing",
		string(missingTwigMemberDocument.Text[missingMemberStart:missingMemberEnd]),
	)
	_, found = phpIndex.FindClass("Twig\\Extension\\ExtensionInterface")
	require.True(t, found)
	serviceTagDocument := lsp.NewTextDocument(
		"file:///real-world-service-tag.yaml",
		`services:
  app.cart_merged_subscriber:
    class: Shopware\Storefront\Event\CartMergedSubscriber
`,
		1,
	)
	serviceTagActions := codeaction.
		NewServiceTagCodeActionProvider(phpIndex).
		GetCodeActions(
			ctx,
			realWorldCodeActionRequest(
				t,
				serviceTagDocument,
				"CartMergedSubscriber",
			),
		)
	require.Len(t, serviceTagActions, 1)
	require.Equal(
		t,
		"Symfony: Add service tag 'kernel.event_subscriber'",
		serviceTagActions[0].Title,
	)
	serviceTagEdits := serviceTagActions[0].Edit.Changes[serviceTagDocument.URI]
	require.Len(t, serviceTagEdits, 1)
	require.Contains(
		t,
		serviceTagEdits[0].NewText,
		"kernel.event_subscriber",
	)
	xmlServiceSuggestionDocument := lsp.NewTextDocument(
		"file:///real-world-service-suggestion.xml",
		`<container>
  <services>
    <service id="app.cart_merged" class="Shopware\Storefront\Event\CartMergedSubscriber">
      <argument/>
    </service>
  </services>
</container>`,
		1,
	)
	xmlServiceSuggestionActions := codeaction.
		NewXMLServiceSuggestionCodeActionProvider(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		GetCodeActions(
			ctx,
			realWorldCodeActionRequest(
				t,
				xmlServiceSuggestionDocument,
				"<argument",
			),
		)
	require.NotEmpty(t, xmlServiceSuggestionActions)
	var translatorServiceAction *protocol.CodeAction
	for actionIndex := range xmlServiceSuggestionActions {
		if strings.HasSuffix(
			xmlServiceSuggestionActions[actionIndex].Title,
			"for $translator",
		) {
			translatorServiceAction =
				&xmlServiceSuggestionActions[actionIndex]
			break
		}
	}
	require.NotNil(t, translatorServiceAction)
	translatorServiceEdits := translatorServiceAction.Edit.
		Changes[xmlServiceSuggestionDocument.URI]
	require.Len(t, translatorServiceEdits, 1)
	require.Contains(
		t,
		translatorServiceEdits[0].NewText,
		`type="service"`,
	)
	require.Contains(
		t,
		translatorServiceEdits[0].NewText,
		`id="`,
	)
	serviceInlayDocument := lsp.NewTextDocument(
		"file:///real-world-service-inlay.yaml",
		`services:
  app.cart_merged:
    class: Shopware\Storefront\Event\CartMergedSubscriber
    arguments: ['@translator', '@request_stack']
`,
		1,
	)
	serviceInlayParams := &protocol.InlayHintParams{}
	serviceInlayParams.TextDocument.URI = serviceInlayDocument.URI
	inlayEndLine, inlayEndCharacter :=
		serviceInlayDocument.LineIndex.PositionUTF16(
			uint32(len(serviceInlayDocument.Source)),
		)
	serviceInlayParams.Range.End = protocol.Position{
		Line:      int(inlayEndLine),
		Character: int(inlayEndCharacter),
	}
	serviceInlayHints, err := lspinlay.NewServiceArgumentProvider(
		workspaceServiceIndex(t, workspace),
		phpIndex,
	).GetInlayHints(
		ctx,
		&lsp.InlayHintRequest{
			InlayHintParams: serviceInlayParams,
			Document:        serviceInlayDocument,
		},
	)
	require.NoError(t, err)
	require.Len(t, serviceInlayHints, 2)
	require.Equal(t, "TranslatorInterface", serviceInlayHints[0].Label)
	require.Equal(t, "RequestStack", serviceInlayHints[1].Label)
	propertyServiceDocument := lsp.NewTextDocument(
		"file:///real-world-property-service.php",
		`<?php
namespace Shopware\Storefront\Event;
class CartMergedSubscriber
{
    public function indexTest(): void
    {
        $this->indexTestLogger->info('indexed');
    }
}`,
		1,
	)
	propertyServiceActions := codeaction.
		NewPropertyServiceCodeActionProvider(
			phpIndex,
			workspaceServiceIndex(t, workspace),
		).
		GetCodeActions(
			ctx,
			realWorldCodeActionRequest(
				t,
				propertyServiceDocument,
				"indexTestLogger",
			),
		)
	var loggerPropertyAction *protocol.CodeAction
	for actionIndex := range propertyServiceActions {
		if propertyServiceActions[actionIndex].Title ==
			"Symfony: Inject LoggerInterface as $indexTestLogger" {
			loggerPropertyAction = &propertyServiceActions[actionIndex]
			break
		}
	}
	require.NotNil(t, loggerPropertyAction)
	loggerPropertyEdits := loggerPropertyAction.Edit.
		Changes[propertyServiceDocument.URI]
	require.Len(t, loggerPropertyEdits, 2)
	require.Contains(
		t,
		loggerPropertyEdits[0].NewText,
		"private readonly LoggerInterface $indexTestLogger",
	)
	require.Contains(
		t,
		loggerPropertyEdits[1].NewText,
		"use Psr\\Log\\LoggerInterface;",
	)
	bundleActionPath := filepath.Join(
		root,
		"src",
		"Core",
		"Framework",
		"Test",
		"TestBundle.php",
	)
	bundleActionSource, err := os.ReadFile(bundleActionPath)
	require.NoError(t, err)
	bundleActionDocument := lsp.NewTextDocument(
		uriutil.FileURI(bundleActionPath),
		string(bundleActionSource),
		1,
	)
	symfonyGeneratorActions := codeaction.
		NewSymfonyGeneratorProvider(
			phpIndex,
			workspaceServiceIndex(t, workspace),
		).
		GetCodeActions(
			ctx,
			realWorldCodeActionRequest(
				t,
				bundleActionDocument,
				"function build",
			),
		)
	require.Len(t, symfonyGeneratorActions, 2)
	require.Equal(
		t,
		"Generate Symfony service",
		symfonyGeneratorActions[0].Title,
	)
	require.Equal(
		t,
		"shopware.symfony.generateService",
		symfonyGeneratorActions[0].Command.Command,
	)
	require.Equal(
		t,
		"Symfony: Create CompilerPass",
		symfonyGeneratorActions[1].Title,
	)
	require.Equal(
		t,
		"shopware.symfony.createCompilerPass",
		symfonyGeneratorActions[1].Command.Command,
	)
	taggedServiceDocument := lsp.NewTextDocument(
		"file:///real-world-tagged-service.yaml",
		`services:
  invalid_extension:
    class: Shopware\Core\Kernel
    tags:
      - { name: twig.extension }
`,
		1,
	)
	taggedServiceDiagnostics, err := lspdiagnostics.
		NewTaggedServiceAnalyzer(phpIndex).
		Analyze(ctx, taggedServiceDocument)
	require.NoError(t, err)
	require.Len(t, taggedServiceDiagnostics, 1)
	require.Equal(
		t,
		"symfony.service.tag_type",
		fmt.Sprint(taggedServiceDiagnostics[0].ID),
	)
	require.Contains(
		t,
		taggedServiceDiagnostics[0].Message,
		"twig.extension",
	)
	phpConstantCount := len(phpIndex.ConstantSymbols())
	require.Greater(t, phpConstantCount, 500)
	httpClientOptions := httpclient.Options(phpIndex)
	require.GreaterOrEqual(t, len(httpClientOptions), 20)
	httpClientOptionCount := len(httpClientOptions)
	var timeoutOptionFound bool
	for _, option := range httpClientOptions {
		if option.Name == "timeout" {
			timeoutOptionFound = true
			require.Equal(t, "null", option.Type.String())
			require.Equal(t, "null", option.Default)
			break
		}
	}
	require.True(t, timeoutOptionFound)
	httpClientSource := `<?php
use Symfony\Contracts\HttpClient\HttpClientInterface;
function fetch(HttpClientInterface $client): void
{
    $client->request('GET', '/', [
        'headers' => [],
        'ti' => null,
    ]);
}
`
	httpClientDocument := lsp.NewTextDocument(
		"file:///real-world-http-client.php",
		httpClientSource,
		1,
	)
	httpClientOffset := uint32(strings.Index(httpClientSource, "'ti'") + 2)
	httpClientNode := httpClientDocument.SyntaxTree.Root.NodeAtOffset(
		httpClientOffset,
	)
	httpClientContext := phpIndex.AddDocumentContext(
		ctx,
		"/real-world-http-client.php",
		1,
		httpClientNode,
		httpClientDocument.SyntaxTree.Root,
	)
	httpClientLine, httpClientCharacter := httpClientDocument.LineIndex.
		PositionUTF16(httpClientOffset)
	httpClientParams := &protocol.CompletionParams{}
	httpClientParams.TextDocument.URI = httpClientDocument.URI
	httpClientParams.Position.Line = int(httpClientLine)
	httpClientParams.Position.Character = int(httpClientCharacter)
	httpClientCompletions := lspcompletion.
		NewHttpClientCompletionProvider(phpIndex).
		GetCompletions(
			httpClientContext,
			&lsp.CompletionRequest{
				CompletionParams: httpClientParams,
				SyntaxContext: lsp.SyntaxContext{
					Document:        httpClientDocument,
					Language:        httpClientDocument.SyntaxLanguage,
					DocumentContent: httpClientDocument.Text,
					DocumentTree:    httpClientDocument.SyntaxTree,
					LineIndex:       httpClientDocument.LineIndex,
					Root:            httpClientDocument.SyntaxTree.Root,
					Node:            httpClientNode,
				},
			},
		)
	var timeoutCompletionFound bool
	for _, item := range httpClientCompletions {
		require.NotEqual(t, "headers", item.Label)
		if item.Label == "timeout" {
			timeoutCompletionFound = true
			require.Contains(t, item.Detail, "null")
		}
	}
	require.True(t, timeoutCompletionFound)
	consoleHelperStarted := time.Now()
	consoleHelpers := console.Helpers(phpIndex)
	consoleHelperElapsed := time.Since(consoleHelperStarted)
	require.GreaterOrEqual(t, len(consoleHelpers), 5)
	consoleHelperCount := len(consoleHelpers)
	var questionHelperFound bool
	for _, helper := range consoleHelpers {
		if helper.Name == "question" {
			questionHelperFound = true
			require.Equal(
				t,
				"Symfony\\Component\\Console\\Helper\\QuestionHelper",
				helper.Class,
			)
			break
		}
	}
	require.True(t, questionHelperFound)
	t.Logf(
		"console helper discovery: %s (%d helpers)",
		consoleHelperElapsed.Round(time.Millisecond),
		consoleHelperCount,
	)
	consoleHelperSource := `<?php
use Symfony\Component\Console\Helper\HelperSet;
function helper(HelperSet $helpers): void
{
    $helpers->get('que');
}
`
	consoleHelperDocument := lsp.NewTextDocument(
		"file:///real-world-console-helper.php",
		consoleHelperSource,
		1,
	)
	consoleHelperOffset := uint32(
		strings.Index(consoleHelperSource, "'que'") + 2,
	)
	consoleHelperNode := consoleHelperDocument.SyntaxTree.Root.NodeAtOffset(
		consoleHelperOffset,
	)
	consoleHelperContext := phpIndex.AddDocumentContext(
		ctx,
		"/real-world-console-helper.php",
		1,
		consoleHelperNode,
		consoleHelperDocument.SyntaxTree.Root,
	)
	consoleHelperParams := &protocol.CompletionParams{}
	consoleHelperParams.TextDocument.URI = consoleHelperDocument.URI
	consoleHelperCompletions := lspcompletion.
		NewConsoleHelperCompletionProvider(phpIndex).
		GetCompletions(
			consoleHelperContext,
			&lsp.CompletionRequest{
				CompletionParams: consoleHelperParams,
				SyntaxContext: lsp.SyntaxContext{
					Document:        consoleHelperDocument,
					Language:        consoleHelperDocument.SyntaxLanguage,
					DocumentContent: consoleHelperDocument.Text,
					DocumentTree:    consoleHelperDocument.SyntaxTree,
					LineIndex:       consoleHelperDocument.LineIndex,
					Root:            consoleHelperDocument.SyntaxTree.Root,
					Node:            consoleHelperNode,
				},
			},
		)
	var questionHelperCompletionFound bool
	for _, item := range consoleHelperCompletions {
		if item.Label == "question" {
			questionHelperCompletionFound = true
			require.Equal(
				t,
				"Symfony\\Component\\Console\\Helper\\QuestionHelper",
				item.Detail,
			)
			break
		}
	}
	require.True(t, questionHelperCompletionFound)
	phpAttributeSource := `<?php
namespace App\Controller;
class RealWorldController
{
    #[Rou]
    public function index(): void {}
}
`
	phpAttributeCompletions := realWorldPHPAttributeCompletions(
		phpIndex,
		phpAttributeSource,
		"Rou",
	)
	phpAttributeLabels := realWorldCompletionLabels(
		phpAttributeCompletions,
	)
	require.Contains(t, phpAttributeLabels, "Route")
	require.Contains(t, phpAttributeLabels, "Cache")
	_, isGrantedInstalled := phpIndex.FindClass(
		"Symfony\\Component\\Security\\Http\\Attribute\\IsGranted",
	)
	if isGrantedInstalled {
		require.Contains(t, phpAttributeLabels, "IsGranted")
	} else {
		require.NotContains(t, phpAttributeLabels, "IsGranted")
	}
	routeAttributeCompletion := realWorldCompletionByLabel(
		t,
		phpAttributeCompletions,
		"Route",
	)
	require.Len(t, routeAttributeCompletion.AdditionalTextEdits, 1)
	routeAttributeEdit, ok := routeAttributeCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, "Route('${1}')$0", routeAttributeEdit.NewText)
	doctrineAttributeSource := `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class RealWorldEntity
{
    #[Col]
    private string $name;
}
`
	doctrineAttributeCompletions := realWorldPHPAttributeCompletions(
		phpIndex,
		doctrineAttributeSource,
		"Col",
	)
	doctrineAttributeLabels := realWorldCompletionLabels(
		doctrineAttributeCompletions,
	)
	_, doctrineColumnInstalled := phpIndex.FindClass(
		"Doctrine\\ORM\\Mapping\\Column",
	)
	if doctrineColumnInstalled {
		require.Contains(t, doctrineAttributeLabels, "Column")
		columnAttributeCompletion := realWorldCompletionByLabel(
			t,
			doctrineAttributeCompletions,
			"Column",
		)
		columnAttributeEdit, columnEditOK :=
			columnAttributeCompletion.TextEdit.(protocol.TextEdit)
		require.True(t, columnEditOK)
		require.Equal(t, `ORM\Column`, columnAttributeEdit.NewText)
		require.Empty(t, columnAttributeCompletion.AdditionalTextEdits)
	} else {
		require.Empty(t, doctrineAttributeLabels)
	}
	defaultLanguageConstants := symfony.ResolveContainerConstant(
		phpIndex,
		"Shopware\\Core\\Defaults::LANGUAGE_SYSTEM",
	)
	require.NotEmpty(t, defaultLanguageConstants)
	containerConstantDocument := lsp.NewTextDocument(
		"file:///real-world-container-constant.yaml",
		`parameters:
  language: !php/const Shopware\Core\Defaults::LANGUAGE_SYSTEM
  invalid: !php/const Shopware\Core\Defaults::MISSING
`,
		1,
	)
	containerConstantDiagnostics, err := lspdiagnostics.
		NewContainerConstantAnalyzer(phpIndex).
		Analyze(ctx, containerConstantDocument)
	require.NoError(t, err)
	require.Len(t, containerConstantDiagnostics, 1)
	require.Equal(
		t,
		"symfony.constant.missing",
		fmt.Sprint(containerConstantDiagnostics[0].ID),
	)
	kernelConstructors := phpIndex.FindMethods(
		"Shopware\\Core\\Kernel",
		"__construct",
	)
	require.NotEmpty(t, kernelConstructors)
	require.GreaterOrEqual(t, len(kernelConstructors[0].Parameters), 7)
	namedArgumentDocument := lsp.NewTextDocument(
		"file:///real-world-named-argument.yaml",
		`services:
  app.wrong_loader:
    class: Shopware\Core\Kernel
  app.kernel:
    class: Shopware\Core\Kernel
    arguments:
      $environment: test
      $debug: false
      $pluginLoader: '@app.wrong_loader'
      $cacheId: test
      $version: test
      $connection: '@connection'
      $projectDire: /project
`,
		1,
	)
	namedArgumentDiagnostics, err := lspdiagnostics.
		NewServiceArgumentAnalyzer(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, namedArgumentDocument)
	require.NoError(t, err)
	var unknownNamedArguments []lsp.Problem
	var invalidServiceArgumentTypes []lsp.Problem
	for _, diagnostic := range namedArgumentDiagnostics {
		switch fmt.Sprint(diagnostic.ID) {
		case "symfony.service.named_argument.unknown":
			unknownNamedArguments = append(
				unknownNamedArguments,
				diagnostic,
			)
		case "symfony.service.argument.type":
			invalidServiceArgumentTypes = append(
				invalidServiceArgumentTypes,
				diagnostic,
			)
		}
	}
	require.Len(t, unknownNamedArguments, 1)
	require.Len(t, invalidServiceArgumentTypes, 1)
	require.Contains(
		t,
		invalidServiceArgumentTypes[0].Message,
		"KernelPluginLoader",
	)
	namedArgumentStart := unknownNamedArguments[0].Range.Start
	namedArgumentEnd := unknownNamedArguments[0].Range.End
	require.Equal(
		t,
		"$projectDire",
		string(namedArgumentDocument.Text[namedArgumentStart:namedArgumentEnd]),
	)
	missingServiceMethodDocument := lsp.NewTextDocument(
		"file:///real-world-missing-service-method.yaml",
		`services:
  app.kernel:
    class: Shopware\Core\Kernel
    calls:
      - [definitelyMissingMethod, []]
`,
		1,
	)
	missingServiceMethodDiagnostics, err := lspdiagnostics.
		NewServiceArgumentAnalyzer(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, missingServiceMethodDocument)
	require.NoError(t, err)
	var missingServiceMethods []lsp.Problem
	for _, diagnostic := range missingServiceMethodDiagnostics {
		if fmt.Sprint(diagnostic.ID) ==
			"symfony.service.method.missing" {
			missingServiceMethods = append(
				missingServiceMethods,
				diagnostic,
			)
		}
	}
	require.Len(t, missingServiceMethods, 1)
	require.Equal(t, "Missing Method", missingServiceMethods[0].Message)
	missingServiceMethodData := missingServiceMethods[0].Payload.(map[string]any)
	require.Equal(
		t,
		"definitelyMissingMethod",
		missingServiceMethodData["methodName"],
	)
	require.NotContains(t, missingServiceMethodData, "classURI")
	phpServiceArgumentDocument := lsp.NewTextDocument(
		"file:///real-world-service-argument.php",
		`<?php
use Shopware\Core\Kernel;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('app.wrong_loader', Kernel::class);
    $services->set('app.kernel', Kernel::class)
        ->arg('$pluginLoader', service('app.wrong_loader'));
};
`,
		1,
	)
	phpServiceArgumentDiagnostics, err := lspdiagnostics.
		NewServiceArgumentAnalyzer(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, phpServiceArgumentDocument)
	require.NoError(t, err)
	var invalidPHPServiceArgumentTypes []lsp.Problem
	for _, diagnostic := range phpServiceArgumentDiagnostics {
		if fmt.Sprint(diagnostic.ID) == "symfony.service.argument.type" {
			invalidPHPServiceArgumentTypes = append(
				invalidPHPServiceArgumentTypes,
				diagnostic,
			)
		}
	}
	require.Len(t, invalidPHPServiceArgumentTypes, 1)
	require.Contains(
		t,
		invalidPHPServiceArgumentTypes[0].Message,
		"KernelPluginLoader",
	)
	assetNames, err := workspaceAssetIndex(t, workspace).Names()
	require.NoError(t, err)
	require.Greater(t, len(assetNames), 1_000)
	encoreEntries, err := workspaceAssetIndex(t, workspace).EntryNames()
	require.NoError(t, err)
	importmapEntries, err := workspaceAssetIndex(
		t,
		workspace,
	).ImportmapEntryNames()
	require.NoError(t, err)
	require.NotContains(t, importmapEntries, "twig.runtime.importmap")
	viteEntries, err := workspaceAssetIndex(t, workspace).ViteEntryNames()
	require.NoError(t, err)
	require.Contains(t, viteEntries, "app")
	viteUsages, err := workspaceAssetIndex(t, workspace).Usages(
		"app",
		asset.ViteEntryReference,
	)
	require.NoError(t, err)
	require.Len(t, viteUsages, 1)
	viteAdministrationUsages, err := workspaceAssetIndex(t, workspace).Usages(
		"administration",
		asset.ViteEntryReference,
	)
	require.NoError(t, err)
	require.Len(t, viteAdministrationUsages, 1)
	assetPackageNames, err := workspaceAssetIndex(
		t,
		workspace,
	).PackageNames()
	require.NoError(t, err)
	require.Contains(t, assetPackageNames, "@Administration")
	require.Contains(t, assetPackageNames, "asset")
	require.Contains(t, assetPackageNames, "theme")
	administrationAssets, err := workspaceAssetIndex(
		t,
		workspace,
	).FindAssetsForPackage(
		"administration/static/img/favicon/favicon-16x16.png",
		"@Administration",
	)
	require.NoError(t, err)
	require.NotEmpty(t, administrationAssets)
	themeAssets, err := workspaceAssetIndex(
		t,
		workspace,
	).FindAssetsForPackage(
		"assets/illustration/404_error.svg",
		"theme",
	)
	require.NoError(t, err)
	require.NotEmpty(t, themeAssets)
	administrationPackageUsages, err := workspaceAssetIndex(
		t,
		workspace,
	).Usages(
		"@Administration",
		asset.AssetPackageReference,
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(administrationPackageUsages), 5)
	htmlAssetUsages, err := workspaceAssetIndex(
		t,
		workspace,
	).Usages(
		"_webpack_hot_proxy_/storefront/hot-reloading.js",
		asset.AssetReference,
	)
	require.NoError(t, err)
	require.NotEmpty(t, htmlAssetUsages)
	twigMacros, err := workspaceTwigIndex(t, workspace).GetAllMacros()
	require.NoError(t, err)
	require.NotEmpty(t, twigMacros)
	twigFunctions, err := workspaceTwigIndex(t, workspace).
		GetAllTwigFunctions()
	require.NoError(t, err)
	twigTests, err := workspaceTwigIndex(t, workspace).
		GetAllTwigTests()
	require.NoError(t, err)
	twigOperators, err := workspaceTwigIndex(t, workspace).
		GetAllTwigOperators()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(twigOperators), 4)
	requireTwigOperator(
		t,
		twigOperators,
		"===",
		filepath.Join(
			"src",
			"Core",
			"Framework",
			"Adapter",
			"Twig",
			"Extension",
			"PhpSyntaxExtension.php",
		),
	)
	operatorCompletionSource := `{% if value  %}`
	operatorCompletionItems := realWorldTwigCompletions(
		root,
		workspaceTwigIndex(t, workspace),
		phpIndex,
		operatorCompletionSource,
		"value ",
	)
	operatorCompletionLabels := realWorldCompletionLabels(
		operatorCompletionItems,
	)
	require.Contains(t, operatorCompletionLabels, "===")
	require.Equal(
		t,
		"Custom Twig binary operator",
		realWorldCompletionByLabel(
			t,
			operatorCompletionItems,
			"===",
		).Detail,
	)
	twigFunctionNames := make(map[string]struct{}, len(twigFunctions))
	for _, function := range twigFunctions {
		twigFunctionNames[function.Name] = struct{}{}
	}
	var deprecatedTwigFunctionNames []string
	for name := range twigFunctionNames {
		deprecated, _, err := workspaceTwigIndex(t, workspace).
			TwigFunctionDeprecation(name)
		require.NoError(t, err)
		if deprecated {
			deprecatedTwigFunctionNames = append(
				deprecatedTwigFunctionNames,
				name,
			)
		}
	}
	sort.Strings(deprecatedTwigFunctionNames)
	deprecatedTwigFunctionCount := len(deprecatedTwigFunctionNames)
	require.GreaterOrEqual(t, deprecatedTwigFunctionCount, 4)
	t.Logf(
		"deprecated Twig functions: %s",
		strings.Join(deprecatedTwigFunctionNames, ", "),
	)
	categoryURLFunctions, err := workspaceTwigIndex(t, workspace).
		GetTwigFunction("category_url")
	require.NoError(t, err)
	require.NotEmpty(t, categoryURLFunctions)
	categoryURLDeprecated, categoryURLDeprecation, err := workspaceTwigIndex(
		t,
		workspace,
	).TwigFunctionDeprecation("category_url")
	require.NoError(t, err)
	require.True(t, categoryURLDeprecated)
	require.Contains(t, categoryURLDeprecation, "deprecated")
	twigTags, err := workspaceTwigIndex(t, workspace).GetAllTwigTags()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(twigTags), 10)
	requireTwigTag(t, twigTags, "sw_extends")
	requireTwigTag(t, twigTags, "sw_icon")
	requireTwigTag(t, twigTags, "macro")
	twigExtensionCatalog, err := analytics.NewTwigExtensionCatalogProvider(
		workspaceTwigIndex(t, workspace),
		phpIndex,
	).Catalog(ctx, analytics.TwigExtensionCatalogRequest{})
	require.NoError(t, err)
	require.Greater(
		t,
		len(twigExtensionCatalog),
		len(twigFunctions)+len(twigTests)+len(twigTags),
	)
	categoryURLCatalogFound := false
	for _, entry := range twigExtensionCatalog {
		if entry.Type == "function" && entry.Name == "category_url" {
			categoryURLCatalogFound = true
			require.True(t, entry.Deprecated)
			require.NotEmpty(t, entry.FileURI)
			require.Positive(t, entry.SourceLine)
			break
		}
	}
	require.True(t, categoryURLCatalogFound)
	twigGlobals, err := workspaceTwigIndex(t, workspace).GetAllGlobals()
	require.NoError(t, err)
	if _, cacheErr := os.Stat(filepath.Join(root, "var", "cache")); cacheErr == nil {
		requireTwigGlobal(
			t,
			twigGlobals,
			"app",
			"Shopware\\Storefront\\Framework\\Twig\\TwigAppVariable",
		)
	} else {
		require.ErrorIs(t, cacheErr, os.ErrNotExist)
		t.Log("Symfony cache absent; skipping compiled-container Twig global")
	}
	requireTwigGlobal(t, twigGlobals, "shopware", "")
	baseTemplateReferences, err := workspaceTwigIndex(
		t,
		workspace,
	).GetTemplateReferences("@Storefront/storefront/base.html.twig")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(baseTemplateReferences), 10)
	baseTemplateUsageRequest := analytics.TwigTemplateUsageCatalogRequest{
		Template: "@Storefront/storefront/base.html.twig",
		FileGlob: "src/Storefront/Resources/views/storefront/base.html.twig",
	}
	baseTemplateUsageCatalog, err := analytics.
		NewTwigTemplateUsageCatalogProvider(
			root,
			workspaceTwigIndex(t, workspace),
			phpIndex,
			workspaceRouteIndex(t, workspace),
			workspaceServiceIndex(t, workspace),
			workspaceTwigComponentIndex(t, workspace),
		).
		Catalog(ctx, baseTemplateUsageRequest)
	require.NoError(t, err)
	require.Len(t, baseTemplateUsageCatalog, 1)
	require.Equal(
		t,
		"@Storefront/storefront/base.html.twig",
		baseTemplateUsageCatalog[0].Template,
	)
	require.Len(t, baseTemplateUsageCatalog[0].Files, 1)
	require.GreaterOrEqual(
		t,
		len(baseTemplateUsageCatalog[0].Extends),
		10,
	)
	productTemplateReferences, err := workspaceTwigIndex(
		t,
		workspace,
	).GetTemplateReferences(
		"@Storefront/storefront/page/content/product-detail.html.twig",
	)
	require.NoError(t, err)
	requireTemplateReferencePath(
		t,
		productTemplateReferences,
		filepath.Join("src", "Storefront", "Controller", "ProductController.php"),
	)
	profilerMacros, err := workspaceTwigIndex(t, workspace).FindMacro(
		"Collector/db.html.twig",
		"render_simple_table",
	)
	require.NoError(t, err)
	require.NotEmpty(t, profilerMacros)
	profilerMacroUsages, err := workspaceTwigIndex(
		t,
		workspace,
	).GetMacroUsages("Collector/db.html.twig", "render_simple_table")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(profilerMacroUsages), 5)
	productTemplateInputs, err := workspaceTwigIndex(
		t,
		workspace,
	).GetTemplateVariables(
		"@Storefront/storefront/page/content/product-detail.html.twig",
	)
	require.NoError(t, err)
	requireTwigTemplateInput(t, productTemplateInputs, "page")
	productTemplateVariableRequest :=
		analytics.TwigTemplateVariableCatalogRequest{
			Template: "@Storefront/storefront/page/content/" +
				"product-detail.html.twig",
			FileGlob: "src/Storefront/Resources/views/storefront/page/" +
				"content/product-detail.html.twig",
		}
	productTemplateVariableCatalog, err := analytics.
		NewTwigTemplateVariableCatalogProvider(
			root,
			workspaceTwigIndex(t, workspace),
			phpIndex,
			workspaceTwigComponentIndex(t, workspace),
		).
		Catalog(ctx, productTemplateVariableRequest)
	require.NoError(t, err)
	require.NotEmpty(t, productTemplateVariableCatalog)
	pageVariableFound := false
	for _, catalog := range productTemplateVariableCatalog {
		if catalog.Template !=
			"@Storefront/storefront/page/content/"+
				"product-detail.html.twig" {
			continue
		}
		for _, variable := range catalog.Variables {
			if variable.Name != "page" {
				continue
			}
			pageVariableFound = true
			require.NotEqual(t, "unknown", variable.Type)
			require.NotEmpty(t, variable.Properties)
			break
		}
	}
	require.True(t, pageVariableFound)
	productTemplateBlocks, err := workspaceTwigIndex(
		t,
		workspace,
	).GetTemplateBlocks(
		"@Storefront/storefront/page/content/product-detail.html.twig",
	)
	require.NoError(t, err)
	requireTwigBlock(t, productTemplateBlocks, "page_content")
	requireTwigBlock(t, productTemplateBlocks, "base_body")
	requireTwigBlock(t, productTemplateBlocks, "base_main_inner")
	realTwigGeneratorSource := `{% sw_extends '@Storefront/storefront/page/content/product-detail.html.twig' %}
`
	realTwigGeneratorDocument := lsp.NewTextDocument(
		"file:///real-world-template-generator.html.twig",
		realTwigGeneratorSource,
		1,
	)
	realTwigGenerator := codeaction.NewTwigTemplateGeneratorProvider(
		workspaceTwigIndex(t, workspace),
	)
	realTwigGeneratorActions := realTwigGenerator.GetCodeActions(
		ctx,
		realWorldCodeActionRequest(
			t,
			realTwigGeneratorDocument,
			"sw_extends",
		),
	)
	require.Len(t, realTwigGeneratorActions, 1)
	require.Equal(
		t,
		"shopware.symfony.generateTwigBlocks",
		realTwigGeneratorActions[0].Command.Command,
	)
	twigBlockPayload, err := json.Marshal(map[string]any{
		"fileUri": realTwigGeneratorDocument.URI,
		"source":  realTwigGeneratorSource,
	})
	require.NoError(t, err)
	twigBlockRaw := json.RawMessage(twigBlockPayload)
	twigBlockCommand := realTwigGenerator.GetCommands(ctx)["shopware/symfony/twig/blocks/candidates"]
	twigBlockValue, err := twigBlockCommand(ctx, &twigBlockRaw)
	require.NoError(t, err)
	twigBlockJSON, err := json.Marshal(twigBlockValue)
	require.NoError(t, err)
	var twigBlockResponse struct {
		Parent string   `json:"parent"`
		Blocks []string `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(twigBlockJSON, &twigBlockResponse))
	require.Equal(
		t,
		"@Storefront/storefront/page/content/product-detail.html.twig",
		twigBlockResponse.Parent,
	)
	require.Contains(t, twigBlockResponse.Blocks, "page_content")
	realScaffolds := scaffold.NewProvider(
		root,
		phpIndex,
		workspaceConsoleIndex(t, workspace),
	)
	scaffoldCommands := realScaffolds.GetCommands(ctx)
	scaffoldCommand := scaffoldCommands[scaffold.CreateSymfonyScaffoldCommand]
	require.NotNil(t, scaffoldCommand)
	commandScaffoldDirectory := filepath.Join(
		root,
		"src",
		"Core",
		"Framework",
		"Feature",
		"Command",
	)
	commandScaffoldPayload, err := json.Marshal(scaffold.Request{
		Kind:         "command",
		DirectoryURI: uriutil.FileURI(commandScaffoldDirectory),
		Name:         "LspScaffoldProbe",
	})
	require.NoError(t, err)
	commandScaffoldRaw := json.RawMessage(commandScaffoldPayload)
	commandScaffoldValue, err := scaffoldCommand(
		ctx,
		&commandScaffoldRaw,
	)
	require.NoError(t, err)
	commandScaffoldResponse, ok := commandScaffoldValue.(scaffold.Response)
	require.True(t, ok)
	require.Equal(
		t,
		"Shopware\\Core\\Framework\\Feature\\Command",
		commandScaffoldResponse.Namespace,
	)
	require.Equal(
		t,
		"LspScaffoldProbeCommand",
		commandScaffoldResponse.ClassName,
	)
	require.Contains(t, commandScaffoldResponse.Content, "#[AsCommand(")
	require.Contains(
		t,
		commandScaffoldResponse.Content,
		"name: 'feature:lsp_scaffold_probe'",
	)
	require.Contains(
		t,
		commandScaffoldResponse.Content,
		"protected function execute(",
	)
	commandScaffoldPath := filepath.Join(
		commandScaffoldDirectory,
		"LspScaffoldProbeCommand.php",
	)
	_, err = os.Stat(commandScaffoldPath)
	require.ErrorIs(t, err, os.ErrNotExist)

	serviceScaffoldPayload, err := json.Marshal(scaffold.Request{
		Kind:         "services-yaml",
		DirectoryURI: uriutil.FileURI(filepath.Join(root, "config")),
		Name:         "lsp-scaffold-probe",
	})
	require.NoError(t, err)
	serviceScaffoldRaw := json.RawMessage(serviceScaffoldPayload)
	serviceScaffoldValue, err := scaffoldCommand(
		ctx,
		&serviceScaffoldRaw,
	)
	require.NoError(t, err)
	serviceScaffoldResponse, ok := serviceScaffoldValue.(scaffold.Response)
	require.True(t, ok)
	require.Contains(
		t,
		serviceScaffoldResponse.Content,
		"  Shopware\\:",
	)
	require.Contains(
		t,
		serviceScaffoldResponse.Content,
		"resource: '../src/'",
	)
	_, err = os.Stat(filepath.Join(
		root,
		"config",
		"lsp-scaffold-probe.yaml",
	))
	require.ErrorIs(t, err, os.ErrNotExist)
	workspaceSymbols := realWorldSymbolSnapshot(t, ctx, workspace)
	relatedNavigation := realWorldRelatedNavigationSnapshot(
		t,
		ctx,
		root,
		workspace,
		phpIndex,
	)
	routeEndpoints := realWorldRouteEndpointSnapshot(
		t,
		ctx,
		root,
		workspace,
		phpIndex,
	)
	consoleCommandLenses := realWorldConsoleCommandLensSnapshot(
		t,
		ctx,
		root,
	)
	serviceNavigation := realWorldServiceNavigationSnapshot(
		t,
		ctx,
		root,
		workspace,
		phpIndex,
	)
	controllerUsageNavigation := realWorldControllerUsageSnapshot(
		t,
		ctx,
		root,
		workspace,
		phpIndex,
	)
	componentSelfImportDocument := lsp.NewTextDocument(
		"file:///real-world-component-self-import.html.twig",
		`<twig:Alert>
  {% from _self import message %}
</twig:Alert>`,
		1,
	)
	componentSelfImportDiagnostics, err := lspdiagnostics.
		NewTwigComponentAnalyzer(
			workspaceTwigComponentIndex(t, workspace),
		).
		Analyze(ctx, componentSelfImportDocument)
	require.NoError(t, err)
	var invalidComponentSelfImports int
	for _, diagnostic := range componentSelfImportDiagnostics {
		if fmt.Sprint(diagnostic.ID) ==
			"twig.component.self_macro_import" {
			invalidComponentSelfImports++
		}
	}
	require.Equal(t, 1, invalidComponentSelfImports)
	twigUXSemanticTokens := realWorldTwigUXSemanticTokenSnapshot(t, ctx)
	twigComponentNames, err := workspaceTwigComponentIndex(
		t,
		workspace,
	).Names()
	require.NoError(t, err)
	twigComponentCatalog, err := analytics.NewTwigComponentCatalogProvider(
		workspaceTwigComponentIndex(t, workspace),
	).Catalog(ctx, analytics.TwigComponentCatalogRequest{})
	require.NoError(t, err)
	require.Len(t, twigComponentCatalog, len(twigComponentNames))
	twigConstantTarget := twig.ConstantReference{
		Class: "Shopware\\Core\\Content\\Product\\ProductDefinition",
		Name:  "TYPE_DIGITAL",
	}
	twigConstantReferences, err := workspaceTwigIndex(
		t,
		workspace,
	).GetConstantReferences(twigConstantTarget)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(twigConstantReferences), 5)
	require.NotEmpty(
		t,
		phpIndex.FindConstants(
			twigConstantTarget.Class,
			twigConstantTarget.Name,
		),
	)
	twigPHPClassTarget := twig.PHPUsageReference{
		Class: "Shopware\\Core\\Content\\Product\\SalesChannel\\" +
			"SalesChannelProductEntity",
	}
	twigPHPClassReferences, err := workspaceTwigIndex(
		t,
		workspace,
	).GetPHPUsageReferences(twigPHPClassTarget)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(twigPHPClassReferences), 3)
	twigTransFilterUsages, err := workspaceTwigIndex(
		t,
		workspace,
	).GetExtensionUsages(
		twig.ExtensionFilterUsage,
		"trans",
	)
	require.NoError(t, err)
	require.Greater(t, len(twigTransFilterUsages), 100)
	twigDefinedTestUsages, err := workspaceTwigIndex(
		t,
		workspace,
	).GetExtensionUsages(
		twig.ExtensionTestUsage,
		"defined",
	)
	require.NoError(t, err)
	require.Greater(t, len(twigDefinedTestUsages), 100)
	revision := phpIndex.Revision()
	var endMemory runtime.MemStats
	runtime.ReadMemStats(&endMemory)
	runtime.GC()
	var retainedMemory runtime.MemStats
	runtime.ReadMemStats(&retainedMemory)
	t.Logf(
		"cold index: %s, classes=%d, php_constants=%d, messenger_messages=%d, collect_message_handlers=%d, collect_message_dispatches=%d, environment_variables=%d, app_env_declarations=%d, app_env_references=%d, deprecated_services=%d, doctrine_models=%d, assets=%d, asset_packages=%d, administration_package_usages=%d, html_asset_usages=%d, encore_entries=%d, importmap_entries=%d, vite_entries=%d, vite_usages=%d, twig_macros=%d, twig_tests=%d, twig_operators=%d, deprecated_twig_functions=%d, twig_tags=%d, twig_globals=%d, base_template_references=%d, product_template_inputs=%d, product_template_blocks=%d, twig_components=%d, twig_constant_references=%d, twig_php_class_references=%d, twig_trans_filter_usages=%d, twig_defined_test_usages=%d, security_providers=%d, security_firewalls=%d, heap_end=%s, heap_retained=%s, total_alloc=%s",
		coldElapsed.Round(time.Millisecond),
		classCount,
		phpConstantCount,
		len(messengerMessages),
		len(collectMessage.Handlers()),
		len(collectMessage.Dispatches()),
		len(environmentVariables),
		len(appEnv.Declarations),
		len(appEnv.References),
		deprecatedServiceCount,
		len(doctrineModels),
		len(assetNames),
		len(assetPackageNames),
		len(administrationPackageUsages),
		len(htmlAssetUsages),
		len(encoreEntries),
		len(importmapEntries),
		len(viteEntries),
		len(viteUsages)+len(viteAdministrationUsages),
		len(twigMacros),
		len(twigTests),
		len(twigOperators),
		deprecatedTwigFunctionCount,
		len(twigTags),
		len(twigGlobals),
		len(baseTemplateReferences),
		len(productTemplateInputs),
		len(productTemplateBlocks),
		len(twigComponentNames),
		len(twigConstantReferences),
		len(twigPHPClassReferences),
		len(twigTransFilterUsages),
		len(twigDefinedTestUsages),
		len(securityProviders),
		len(securityFirewalls),
		formatBytes(endMemory.HeapAlloc),
		formatBytes(retainedMemory.HeapAlloc),
		formatBytes(retainedMemory.TotalAlloc),
	)

	warmStarted := time.Now()
	require.NoError(t, workspace.Scanner().IndexAll(ctx))
	warmElapsed := time.Since(warmStarted)
	require.Equal(t, revision, phpIndex.Revision(), "warm scan must not republish unchanged PHP documents")
	t.Logf("warm no-op scan: %s", warmElapsed.Round(time.Millisecond))

	routeAssistantDefinitionPath := filepath.Join(
		root,
		".shopware-lsp",
		"virtual_route_assistant.php",
	)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		routeAssistantDefinitionPath,
		[]byte(`<?php
/** @param string $route #Route */
function real_world_route_assistant(string $route): void {}

/** @param string $service #Service */
function real_world_service_assistant(string $service): void {}

/** @param string $name #Parameter */
function real_world_parameter_assistant(string $name): void {}

/** @param string $name #ClassInterface */
function real_world_class_assistant(string $name): void {}

/** @param string $entity #Entity */
function real_world_entity_assistant(string $entity): void {}

/** @param string $type #FormType */
function real_world_form_assistant(string $type): void {}

/** @param string $template #Template */
function real_world_template_assistant(string $template): void {}

/**
 * @param string $key #TranslationKey
 * @param string $domain #TranslationDomain
 */
function real_world_translation_assistant(string $key, string $domain): void {}
`),
	)))
	assistantDoctrineMappingPath := filepath.Join(
		root,
		".shopware-lsp",
		"virtual_assistant_entity.orm.xml",
	)
	require.NoError(t, workspaceDoctrineIndex(t, workspace).Index(
		indexer.NewParsedFile(
			assistantDoctrineMappingPath,
			[]byte(`<doctrine-mapping>
<entity name="Shopware\Core\Kernel"/>
</doctrine-mapping>`),
		),
	))
	doctrineModels, err = workspaceDoctrineIndex(t, workspace).Models()
	require.NoError(t, err)
	doctrineCatalog, err = doctrineCatalogProvider.Entities(
		ctx,
		analytics.DoctrineEntityCatalogRequest{
			Query: "Shopware\\Core\\Kernel",
		},
	)
	require.NoError(t, err)
	require.Len(t, doctrineCatalog, 1)
	require.Equal(
		t,
		"Shopware\\Core\\Kernel",
		doctrineCatalog[0].Class,
	)
	require.Equal(t, "xml", doctrineCatalog[0].Source)
	routeAssistantName := "frontend.sitemap.proxy"
	routeAssistantSource := "<?php real_world_route_assistant('" +
		routeAssistantName + "');"
	routeAssistantDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"RouteAssistantUsage.php",
		)),
		routeAssistantSource,
		1,
	)
	routeAssistantOffset := uint32(
		strings.Index(routeAssistantSource, routeAssistantName) +
			len(routeAssistantName),
	)
	routeAssistantRequest := realWorldCompletionRequest(
		routeAssistantDocument,
		routeAssistantOffset,
	)
	routeAssistantContext := phpIndex.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "RouteAssistantUsage.php"),
		routeAssistantDocument.Version,
		routeAssistantRequest.Node,
		routeAssistantRequest.Root,
	)
	routeAssistantCompletions := phpRoutePathProvider.GetCompletions(
		routeAssistantContext,
		routeAssistantRequest,
	)
	routeAssistantCompletion := realWorldCompletionByLabel(
		t,
		routeAssistantCompletions,
		routeAssistantName,
	)
	routeAssistantEdit, ok := routeAssistantCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(
		t,
		routeAssistantName,
		routeAssistantEdit.NewText,
	)
	routeAssistantDefinition := lspdefinition.NewRouteDefinitionProvider(
		workspaceRouteIndex(t, workspace),
	).GetDefinition(
		routeAssistantContext,
		realWorldDefinitionRequest(
			routeAssistantDocument,
			routeAssistantRequest.Node,
			routeAssistantOffset,
		),
	)
	require.Len(t, routeAssistantDefinition, 1)
	serviceAssistantSource := "<?php real_world_service_assistant('" +
		deprecatedNotification.ID + "');"
	serviceAssistantDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"ServiceAssistantUsage.php",
		)),
		serviceAssistantSource,
		1,
	)
	serviceAssistantOffset := uint32(
		strings.Index(serviceAssistantSource, deprecatedNotification.ID) +
			len(deprecatedNotification.ID),
	)
	serviceAssistantRequest := realWorldCompletionRequest(
		serviceAssistantDocument,
		serviceAssistantOffset,
	)
	serviceAssistantContext := phpIndex.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "ServiceAssistantUsage.php"),
		serviceAssistantDocument.Version,
		serviceAssistantRequest.Node,
		serviceAssistantRequest.Root,
	)
	serviceAssistantCompletions := lspcompletion.
		NewServiceCompletionProvider(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		GetCompletions(serviceAssistantContext, serviceAssistantRequest)
	serviceAssistantCompletion := realWorldCompletionByLabel(
		t,
		serviceAssistantCompletions,
		deprecatedNotification.ID,
	)
	serviceAssistantEdit, ok := serviceAssistantCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, deprecatedNotification.ID, serviceAssistantEdit.NewText)
	require.True(t, serviceAssistantCompletion.Deprecated)
	serviceAssistantDefinition := lspdefinition.
		NewServiceXMLDefinitionProvider(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		GetDefinition(
			serviceAssistantContext,
			realWorldDefinitionRequest(
				serviceAssistantDocument,
				serviceAssistantRequest.Node,
				serviceAssistantOffset,
			),
		)
	require.Len(t, serviceAssistantDefinition, 1)
	require.Equal(
		t,
		uriutil.FileURI(deprecatedNotification.Path),
		serviceAssistantDefinition[0].URI,
	)
	parameterAssistantSource := "<?php real_world_parameter_assistant('" +
		realParameter.Name + "');"
	parameterAssistantDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"ParameterAssistantUsage.php",
		)),
		parameterAssistantSource,
		1,
	)
	parameterAssistantOffset := uint32(
		strings.Index(parameterAssistantSource, realParameter.Name) +
			len(realParameter.Name),
	)
	parameterAssistantRequest := realWorldCompletionRequest(
		parameterAssistantDocument,
		parameterAssistantOffset,
	)
	parameterAssistantContext := phpIndex.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "ParameterAssistantUsage.php"),
		parameterAssistantDocument.Version,
		parameterAssistantRequest.Node,
		parameterAssistantRequest.Root,
	)
	parameterAssistantCompletions := lspcompletion.
		NewServiceCompletionProvider(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		GetCompletions(parameterAssistantContext, parameterAssistantRequest)
	parameterAssistantCompletion := realWorldCompletionByLabel(
		t,
		parameterAssistantCompletions,
		realParameter.Name,
	)
	parameterAssistantEdit, ok := parameterAssistantCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, realParameter.Name, parameterAssistantEdit.NewText)
	parameterAssistantDefinition := lspdefinition.
		NewServiceXMLDefinitionProvider(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		GetDefinition(
			parameterAssistantContext,
			realWorldDefinitionRequest(
				parameterAssistantDocument,
				parameterAssistantRequest.Node,
				parameterAssistantOffset,
			),
		)
	require.Len(t, parameterAssistantDefinition, 1)
	require.Equal(
		t,
		uriutil.FileURI(realParameter.Path),
		parameterAssistantDefinition[0].URI,
	)
	classAssistantName := "Shopware\\Core\\Kernel"
	interfaceAssistantName := "Shopware\\Core\\Framework\\Struct\\" +
		"ExtendableInterface"
	classAssistantSource := "<?php real_world_class_assistant('" +
		classAssistantName + "');"
	classAssistantDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"ClassAssistantUsage.php",
		)),
		classAssistantSource,
		1,
	)
	classAssistantOffset := uint32(
		strings.Index(classAssistantSource, classAssistantName) +
			len(classAssistantName),
	)
	classAssistantRequest := realWorldCompletionRequest(
		classAssistantDocument,
		classAssistantOffset,
	)
	classAssistantContext := phpIndex.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "ClassAssistantUsage.php"),
		classAssistantDocument.Version,
		classAssistantRequest.Node,
		classAssistantRequest.Root,
	)
	classAssistantProvider := lspphpsemantic.New(phpIndex)
	classAssistantCompletions := classAssistantProvider.GetCompletions(
		classAssistantContext,
		classAssistantRequest,
	)
	classAssistantCompletion := realWorldCompletionByLabel(
		t,
		classAssistantCompletions,
		classAssistantName,
	)
	classAssistantEdit, ok := classAssistantCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, classAssistantName, classAssistantEdit.NewText)
	realWorldCompletionByLabel(
		t,
		classAssistantCompletions,
		interfaceAssistantName,
	)
	classAssistantDefinition := classAssistantProvider.GetDefinition(
		classAssistantContext,
		realWorldDefinitionRequest(
			classAssistantDocument,
			classAssistantRequest.Node,
			classAssistantOffset,
		),
	)
	require.Len(t, classAssistantDefinition, 1)
	require.Equal(
		t,
		uriutil.FileURI(filepath.Join(root, "src", "Core", "Kernel.php")),
		classAssistantDefinition[0].URI,
	)
	entityAssistantSource := "<?php real_world_entity_assistant('" +
		classAssistantName + "');"
	entityAssistantDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"EntityAssistantUsage.php",
		)),
		entityAssistantSource,
		1,
	)
	entityAssistantOffset := uint32(
		strings.Index(entityAssistantSource, classAssistantName) +
			len(classAssistantName),
	)
	entityAssistantRequest := realWorldCompletionRequest(
		entityAssistantDocument,
		entityAssistantOffset,
	)
	entityAssistantContext := phpIndex.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "EntityAssistantUsage.php"),
		entityAssistantDocument.Version,
		entityAssistantRequest.Node,
		entityAssistantRequest.Root,
	)
	entityAssistantCompletionProvider := lspcompletion.
		NewDoctrineCompletionProvider(
			workspaceDoctrineIndex(t, workspace),
			phpIndex,
		)
	entityAssistantCompletions := entityAssistantCompletionProvider.
		GetCompletions(entityAssistantContext, entityAssistantRequest)
	entityAssistantCompletion := realWorldCompletionByLabel(
		t,
		entityAssistantCompletions,
		classAssistantName,
	)
	entityAssistantEdit, ok := entityAssistantCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, classAssistantName, entityAssistantEdit.NewText)
	entityAssistantDefinition := lspdefinition.
		NewDoctrineDefinitionProvider(
			workspaceDoctrineIndex(t, workspace),
			phpIndex,
		).
		GetDefinition(
			entityAssistantContext,
			realWorldDefinitionRequest(
				entityAssistantDocument,
				entityAssistantRequest.Node,
				entityAssistantOffset,
			),
		)
	require.NotEmpty(t, entityAssistantDefinition)

	formAssistantName := "entity"
	formAssistantSource := "<?php real_world_form_assistant('" +
		formAssistantName + "');"
	formAssistantDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"FormAssistantUsage.php",
		)),
		formAssistantSource,
		1,
	)
	formAssistantOffset := uint32(
		strings.Index(formAssistantSource, formAssistantName) +
			len(formAssistantName),
	)
	formAssistantRequest := realWorldCompletionRequest(
		formAssistantDocument,
		formAssistantOffset,
	)
	formAssistantContext := phpIndex.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "FormAssistantUsage.php"),
		formAssistantDocument.Version,
		formAssistantRequest.Node,
		formAssistantRequest.Root,
	)
	formAssistantCompletions := lspcompletion.NewFormCompletionProvider(
		workspaceFormIndex(t, workspace),
		phpIndex,
	).GetCompletions(formAssistantContext, formAssistantRequest)
	formAssistantCompletion := realWorldCompletionByLabel(
		t,
		formAssistantCompletions,
		formAssistantName,
	)
	formAssistantEdit, ok := formAssistantCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, formAssistantName, formAssistantEdit.NewText)
	formAssistantDefinition := lspdefinition.NewFormDefinitionProvider(
		workspaceFormIndex(t, workspace),
		phpIndex,
	).GetDefinition(
		formAssistantContext,
		realWorldDefinitionRequest(
			formAssistantDocument,
			formAssistantRequest.Node,
			formAssistantOffset,
		),
	)
	require.NotEmpty(t, formAssistantDefinition)

	templateAssistantName := "@Storefront/storefront/base.html.twig"
	templateAssistantSource := "<?php real_world_template_assistant('" +
		templateAssistantName + "');"
	templateAssistantDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"TemplateAssistantUsage.php",
		)),
		templateAssistantSource,
		1,
	)
	templateAssistantOffset := uint32(
		strings.Index(templateAssistantSource, templateAssistantName) +
			len(templateAssistantName),
	)
	templateAssistantRequest := realWorldCompletionRequest(
		templateAssistantDocument,
		templateAssistantOffset,
	)
	templateAssistantContext := phpIndex.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "TemplateAssistantUsage.php"),
		templateAssistantDocument.Version,
		templateAssistantRequest.Node,
		templateAssistantRequest.Root,
	)
	templateAssistantCompletions := lspcompletion.
		NewTwigCompletionProvider(
			root,
			workspaceTwigIndex(t, workspace),
			nil,
			phpIndex,
		).
		GetCompletions(templateAssistantContext, templateAssistantRequest)
	templateAssistantCompletion := realWorldCompletionByLabel(
		t,
		templateAssistantCompletions,
		templateAssistantName,
	)
	templateAssistantEdit, ok := templateAssistantCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, templateAssistantName, templateAssistantEdit.NewText)
	templateAssistantDefinition := lspdefinition.NewTwigDefinitionProvider(
		root,
		workspaceTwigIndex(t, workspace),
		nil,
		phpIndex,
	).GetDefinition(
		templateAssistantContext,
		realWorldDefinitionRequest(
			templateAssistantDocument,
			templateAssistantRequest.Node,
			templateAssistantOffset,
		),
	)
	require.NotEmpty(t, templateAssistantDefinition)

	translationAssistantSource := "<?php real_world_translation_assistant(" +
		"domain: '" + assistantTranslationDomain + "', key: '" +
		assistantTranslationKey + "');"
	translationAssistantDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"TranslationAssistantUsage.php",
		)),
		translationAssistantSource,
		1,
	)
	translationAssistantOffset := uint32(
		strings.LastIndex(
			translationAssistantSource,
			assistantTranslationKey,
		) + len(assistantTranslationKey),
	)
	translationAssistantRequest := realWorldCompletionRequest(
		translationAssistantDocument,
		translationAssistantOffset,
	)
	translationAssistantContext := phpIndex.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "TranslationAssistantUsage.php"),
		translationAssistantDocument.Version,
		translationAssistantRequest.Node,
		translationAssistantRequest.Root,
	)
	translationAssistantCompletions := lspcompletion.
		NewTranslationCompletionProvider(
			workspaceTranslationIndex(t, workspace),
			phpIndex,
		).
		GetCompletions(
			translationAssistantContext,
			translationAssistantRequest,
		)
	translationAssistantCompletion := realWorldCompletionByLabel(
		t,
		translationAssistantCompletions,
		assistantTranslationKey,
	)
	translationAssistantEdit, ok := translationAssistantCompletion.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(
		t,
		assistantTranslationKey,
		translationAssistantEdit.NewText,
	)
	translationAssistantDefinition := lspdefinition.
		NewTranslationDefinitionProvider(
			workspaceTranslationIndex(t, workspace),
			phpIndex,
		).
		GetDefinition(
			translationAssistantContext,
			realWorldDefinitionRequest(
				translationAssistantDocument,
				translationAssistantRequest.Node,
				translationAssistantOffset,
			),
		)
	require.NotEmpty(t, translationAssistantDefinition)

	routeAssistantTypo := routeAssistantName + "x"
	routeAssistantDiagnosticDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"RouteAssistantDiagnostic.php",
		)),
		"<?php real_world_route_assistant('"+
			routeAssistantTypo+"');",
		1,
	)
	routeAssistantDiagnostics, err := lspdiagnostics.
		NewRouteAnalyzer(
			workspaceRouteIndex(t, workspace),
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, routeAssistantDiagnosticDocument)
	require.NoError(t, err)
	requireRealWorldSuggestionDiagnostic(
		t,
		routeAssistantDiagnosticDocument,
		routeAssistantDiagnostics,
		"symfony.route.missing",
		routeAssistantTypo,
		routeAssistantName,
	)

	serviceAssistantTypo := deprecatedNotification.ID + "x"
	serviceAssistantDiagnosticDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"ServiceAssistantDiagnostic.php",
		)),
		"<?php real_world_service_assistant('"+
			serviceAssistantTypo+"');",
		1,
	)
	serviceAssistantDiagnostics, err := lspdiagnostics.
		NewServiceAnalyzer(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, serviceAssistantDiagnosticDocument)
	require.NoError(t, err)
	requireRealWorldSuggestionDiagnostic(
		t,
		serviceAssistantDiagnosticDocument,
		serviceAssistantDiagnostics,
		"symfony.service.missing",
		serviceAssistantTypo,
		deprecatedNotification.ID,
	)

	translationAssistantTypo := assistantTranslationKey + "x"
	translationAssistantDiagnosticDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"TranslationAssistantDiagnostic.php",
		)),
		"<?php real_world_translation_assistant(domain: '"+
			assistantTranslationDomain+"', key: '"+
			translationAssistantTypo+"');",
		1,
	)
	translationAssistantDiagnostics, err := lspdiagnostics.
		NewTranslationAnalyzer(
			workspaceTranslationIndex(t, workspace),
			phpIndex,
		).
		Analyze(ctx, translationAssistantDiagnosticDocument)
	require.NoError(t, err)
	requireRealWorldSuggestionDiagnostic(
		t,
		translationAssistantDiagnosticDocument,
		translationAssistantDiagnostics,
		"symfony.translation.key.missing",
		translationAssistantTypo,
		assistantTranslationKey,
	)

	measureRealWorldLSPRequestLatency(t, ctx, realWorldServer, root)

	require.NoError(t, workspace.Close())
	workspace = nil
	phpIndex = nil
	classes = nil
	runtime.GC()

	restoreStarted := time.Now()
	reopened, restoredPHP := openRealWorldWorkspace(t, ctx, root)
	restoreElapsed := time.Since(restoreStarted)
	require.Len(t, restoredPHP.ClassSymbols(), classCount)
	_, found = restoredPHP.FindClass("Shopware\\Core\\Kernel")
	require.True(t, found)
	restoredAdminComponents, err := workspaceAdminIndex(t, reopened).GetAllComponents()
	require.NoError(t, err)
	require.Len(t, restoredAdminComponents, adminComponentCount)
	restoredRoleGeneral, err := workspaceAdminIndex(t, reopened).
		GetEffectiveComponent("sw-users-permissions-role-view-general")
	require.NoError(t, err)
	require.NotNil(t, restoredRoleGeneral)
	require.Contains(t, restoredRoleGeneral.Data, "mcpIntegrations")
	require.Contains(t, restoredRoleGeneral.Injected, "repositoryFactory")
	restoredDataGrid, err := workspaceAdminIndex(t, reopened).
		GetEffectiveComponent("sw-data-grid")
	require.NoError(t, err)
	require.NotNil(t, restoredDataGrid)
	require.Equal(
		t,
		rowClickEvent,
		requireAdminEvent(t, restoredDataGrid.ComponentEvents(), "row-click"),
	)
	restoredDataGridColumnFamily, restoredFamilyFound :=
		restoredDataGrid.ComponentSlot("column-name")
	require.True(t, restoredFamilyFound)
	require.Equal(t, dataGridColumnFamily, restoredDataGridColumnFamily)
	restoredDataGridRouterBlock, restoredBlockFound :=
		restoredDataGrid.ComponentBlock(
			"sw_data_grid_columns_render_router_link",
		)
	require.True(t, restoredBlockFound)
	require.Equal(t, dataGridRouterBlock, restoredDataGridRouterBlock)
	restoredSlotCard, err := workspaceAdminIndex(t, reopened).
		GetEffectiveComponent("sw-card")
	require.NoError(t, err)
	require.NotNil(t, restoredSlotCard)
	require.Equal(t, slotCard.Deprecated, restoredSlotCard.Deprecated)
	restoredEntityListing, err := workspaceAdminIndex(t, reopened).
		GetEffectiveComponent("sw-entity-listing")
	require.NoError(t, err)
	require.NotNil(t, restoredEntityListing)
	restoredDeprecatedItems, restoredItemsFound :=
		restoredEntityListing.ComponentProp("items")
	require.True(t, restoredItemsFound)
	require.Equal(t, deprecatedItems.Deprecated, restoredDeprecatedItems.Deprecated)
	restoredDeprecatedBlockComponent, err := workspaceAdminIndex(t, reopened).
		GetEffectiveComponent("sw-newsletter-recipient-filter-switch")
	require.NoError(t, err)
	require.NotNil(t, restoredDeprecatedBlockComponent)
	restoredDeprecatedAdminBlock, restoredBlockFound :=
		restoredDeprecatedBlockComponent.ComponentBlock(
			"sw_newsletter_recipient_filter_switch_field",
		)
	require.True(t, restoredBlockFound)
	require.Equal(t, deprecatedAdminBlock, restoredDeprecatedAdminBlock)
	restoredDeprecatedMemberComponent, err := workspaceAdminIndex(t, reopened).
		GetEffectiveComponent("sw-settings-snippet-set-list")
	require.NoError(t, err)
	require.NotNil(t, restoredDeprecatedMemberComponent)
	restoredDeprecatedAdminMember, restoredMemberFound :=
		restoredDeprecatedMemberComponent.TemplateMember(
			"getNoPermissionsTooltip",
		)
	require.True(t, restoredMemberFound)
	require.Equal(t, deprecatedAdminMember, restoredDeprecatedAdminMember)
	restoredMeteorButton, err := workspaceAdminIndex(t, reopened).
		GetEffectiveComponent("mt-button")
	require.NoError(t, err)
	require.NotNil(t, restoredMeteorButton)
	require.Equal(
		t,
		meteorIconFrontSlot,
		requireAdminSlot(t, restoredMeteorButton.Slots, "iconFront"),
	)
	restoredAdminReferenceProvider := lspreference.NewAdminReferenceProvider(
		workspaceAdminIndex(t, reopened),
	)
	restoredAdminEventReferences, err := restoredAdminReferenceProvider.GetReferences(
		ctx,
		realWorldReferenceRequest(
			cmsListDocument,
			cmsItemClickOffset,
			true,
		),
	)
	require.NoError(t, err)
	require.Equal(t, adminEventReferences, restoredAdminEventReferences)
	restoredAdminSlotReferences, err := restoredAdminReferenceProvider.GetReferences(
		ctx,
		realWorldReferenceRequest(
			meteorSlotUsageDocument,
			meteorSlotUsageOffset,
			true,
		),
	)
	require.NoError(t, err)
	require.Equal(t, adminSlotReferences, restoredAdminSlotReferences)
	restoredAdminEventRenameEdit, err := lsprefactor.NewAdminRenameProvider(
		workspaceAdminIndex(t, reopened),
	).Rename(
		ctx,
		realWorldRenameRequest(
			cmsListDocument,
			cmsItemClickOffset,
			"item-selected",
		),
	)
	require.NoError(t, err)
	require.Equal(t, adminEventRenameEdit, restoredAdminEventRenameEdit)
	restoredValidationMixins, err := workspaceAdminIndex(t, reopened).
		GetMixin("validation")
	require.NoError(t, err)
	require.NotEmpty(t, restoredValidationMixins)
	require.Contains(
		t,
		restoredValidationMixins[0].Definition.Methods,
		"validateRule",
	)
	restoredAdminServices, err := workspaceAdminIndex(t, reopened).GetAllServices()
	require.NoError(t, err)
	require.Len(t, restoredAdminServices, adminServiceCount)
	restoredAdminFilters, err := workspaceAdminIndex(t, reopened).GetAllFilters()
	require.NoError(t, err)
	require.Len(t, restoredAdminFilters, adminFilterCount)
	restoredAdminStores, err := workspaceAdminIndex(t, reopened).GetAllStores()
	require.NoError(t, err)
	require.Len(t, restoredAdminStores, adminStoreCount)
	restoredAdminPrivileges, err := workspaceAdminIndex(t, reopened).
		GetAllPrivileges()
	require.NoError(t, err)
	require.Len(t, restoredAdminPrivileges, adminPrivilegeCount)
	restoredProductViewer, err := workspaceAdminIndex(t, reopened).
		GetPrivilege("product.viewer")
	require.NoError(t, err)
	require.NotEmpty(t, restoredProductViewer)
	restoredAdminModules, err := workspaceAdminIndex(t, reopened).GetAllModules()
	require.NoError(t, err)
	require.Len(t, restoredAdminModules, adminModuleCount)
	for _, expected := range []struct {
		kind  admin.AdminSymbolKind
		name  string
		count int
	}{
		{admin.AdminSymbolComponent, "sw-page", swPageUsageCount},
		{admin.AdminSymbolService, "acl", aclUsageCount},
		{admin.AdminSymbolStore, "session", sessionUsageCount},
		{admin.AdminSymbolPrivilege, "product.viewer", productViewerUsageCount},
		{admin.AdminSymbolModuleRoute, "sw.product.detail", productDetailRouteUsageCount},
		{admin.AdminSymbolModule, "sw-settings-message-stats", settingsMessageStatsModuleUsageCount},
	} {
		sets, usageErr := workspaceAdminIndex(t, reopened).GetUsages(
			expected.kind, "", expected.name,
		)
		require.NoError(t, usageErr)
		require.Equal(t, expected.count, adminUsageOccurrenceCount(sets))
	}
	restoredProfileStores, err := workspaceAdminIndex(t, reopened).GetStore("swProfile")
	require.NoError(t, err)
	require.NotEmpty(t, restoredProfileStores)
	_, restoredProfileAction := restoredProfileStores[0].Member("setMinSearchTermLength")
	require.True(t, restoredProfileAction)
	restoredSessionStores, err := workspaceAdminIndex(t, reopened).GetStore("session")
	require.NoError(t, err)
	require.NotEmpty(t, restoredSessionStores)
	_, restoredSessionAction := restoredSessionStores[0].Member("setCurrentUser")
	require.True(t, restoredSessionAction)
	restoredContextStores, err := workspaceAdminIndex(t, reopened).GetStore("context")
	require.NoError(t, err)
	require.NotEmpty(t, restoredContextStores)
	_, restoredContextState := restoredContextStores[0].Member("app")
	require.True(t, restoredContextState)
	restoredAPIContextShape, err := workspaceAdminIndex(t, reopened).
		ResolveShopwareContext("api", adminContainerDocumentPath)
	require.NoError(t, err)
	require.True(t, restoredAPIContextShape.Complete)
	restoredAPIContextMembers := make(map[string]admin.TwigVueMember)
	for _, member := range restoredAPIContextShape.Members {
		restoredAPIContextMembers[member.Name] = member
	}
	require.Contains(t, restoredAPIContextMembers, "languageId")
	require.Equal(
		t, adminContextPath,
		restoredAPIContextMembers["languageId"].DefinitionPath,
	)
	require.True(
		t, restoredAPIContextMembers["languageId"].DefinitionRange.Identifier,
	)
	restoredAdminFormatShape, err := workspaceAdminIndex(t, reopened).
		ResolveShopwareUtils("format", adminContainerDocumentPath)
	require.NoError(t, err)
	require.True(t, restoredAdminFormatShape.Complete)
	restoredAdminFormatMembers := make(map[string]admin.TwigVueMember)
	for _, member := range restoredAdminFormatShape.Members {
		restoredAdminFormatMembers[member.Name] = member
	}
	require.Contains(t, restoredAdminFormatMembers, "date")
	require.Contains(t, restoredAdminFormatMembers["date"].Type, "=> string")
	require.Equal(
		t, adminUtilsPath,
		restoredAdminFormatMembers["date"].DefinitionPath,
	)
	require.True(
		t, restoredAdminFormatMembers["date"].DefinitionRange.Identifier,
	)
	restoredDALDefinitions, err := workspaceDALIndex(t, reopened).Definitions()
	require.NoError(t, err)
	require.Len(t, restoredDALDefinitions, dalDefinitionCount)
	restoredAppScriptHooks, err := workspaceAppScriptIndex(t, reopened).Hooks()
	require.NoError(t, err)
	require.Len(t, restoredAppScriptHooks, appScriptHookCount)
	restoredAppScriptFacades, err := workspaceAppScriptIndex(t, reopened).Facades()
	require.NoError(t, err)
	require.Len(t, restoredAppScriptFacades, appScriptFacadeCount)
	require.Equal(
		t,
		phpAttributeLabels,
		realWorldCompletionLabels(realWorldPHPAttributeCompletions(
			restoredPHP,
			phpAttributeSource,
			"Rou",
		)),
	)
	require.Equal(
		t,
		doctrineAttributeLabels,
		realWorldCompletionLabels(realWorldPHPAttributeCompletions(
			restoredPHP,
			doctrineAttributeSource,
			"Col",
		)),
	)
	require.Equal(
		t,
		loopCompletionLabels,
		realWorldCompletionLabels(realWorldTwigCompletions(
			root,
			workspaceTwigIndex(t, reopened),
			restoredPHP,
			loopCompletionSource,
			"loop.",
		)),
	)
	require.Equal(
		t,
		operatorCompletionLabels,
		realWorldCompletionLabels(realWorldTwigCompletions(
			root,
			workspaceTwigIndex(t, reopened),
			restoredPHP,
			operatorCompletionSource,
			"value ",
		)),
	)
	require.NotEmpty(t, symfony.ResolveContainerConstant(
		restoredPHP,
		"Shopware\\Core\\Defaults::LANGUAGE_SYSTEM",
	))
	restoredHTTPClientOptions := httpclient.Options(restoredPHP)
	require.Len(t, restoredHTTPClientOptions, httpClientOptionCount)
	var restoredTimeoutFound bool
	for _, option := range restoredHTTPClientOptions {
		if option.Name == "timeout" {
			restoredTimeoutFound = true
			require.Equal(t, "null", option.Type.String())
			require.Equal(t, "null", option.Default)
			break
		}
	}
	require.True(t, restoredTimeoutFound)
	restoredConsoleHelpers := console.Helpers(restoredPHP)
	require.Len(t, restoredConsoleHelpers, consoleHelperCount)
	var restoredQuestionHelperFound bool
	for _, helper := range restoredConsoleHelpers {
		if helper.Name == "question" {
			restoredQuestionHelperFound = true
			require.Equal(
				t,
				"Symfony\\Component\\Console\\Helper\\QuestionHelper",
				helper.Class,
			)
			break
		}
	}
	require.True(t, restoredQuestionHelperFound)
	restoredVariables, err := restoredPHP.TwigTemplateVariables(
		"@Storefront/storefront/page/content/product-detail.html.twig",
	)
	require.NoError(t, err)
	requireTwigVariable(t, restoredVariables, "page")
	restoredMissingTwigMemberDiagnostics, err := lspdiagnostics.
		NewTwigMemberMissingAnalyzer(
			workspaceTwigIndex(t, reopened),
			restoredPHP,
		).
		Analyze(ctx, missingTwigMemberDocument)
	require.NoError(t, err)
	require.Equal(
		t,
		missingTwigMemberDiagnostics,
		restoredMissingTwigMemberDiagnostics,
	)
	restoredCommands, err := workspaceConsoleIndex(t, reopened).GetCommand(
		"system:config:get",
	)
	require.NoError(t, err)
	require.NotEmpty(t, restoredCommands)
	for _, command := range restoredCommands {
		requireConsoleInput(t, command.Options, "format")
	}
	restoredConsoleCatalog, err := console.NewCatalogProvider(
		workspaceConsoleIndex(t, reopened),
		root,
	).CatalogWithRequest(ctx, consoleCatalogRequest)
	require.NoError(t, err)
	require.Equal(t, consoleCatalog, restoredConsoleCatalog)
	restoredCommandAliases, err := workspaceConsoleIndex(
		t,
		reopened,
	).GetCommand("snippets:validate")
	require.NoError(t, err)
	require.Equal(t, commandAliases, restoredCommandAliases)
	restoredEvent, found, err := workspaceEventIndex(t, reopened).GetEvent(
		"Shopware\\Core\\Checkout\\Cart\\Event\\CartMergedEvent",
	)
	require.NoError(t, err)
	require.True(t, found)
	requireEventListener(
		t,
		restoredEvent,
		"Shopware\\Storefront\\Event\\CartMergedSubscriber",
		"addCartMergedNoticeFlash",
	)
	restoredMessengerMessages, err := workspaceMessengerIndex(
		t,
		reopened,
	).Messages()
	require.NoError(t, err)
	require.Equal(t, len(messengerMessages), len(restoredMessengerMessages))
	restoredCollectMessage, found, err := workspaceMessengerIndex(
		t,
		reopened,
	).GetMessage(collectMessage.Name)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, collectMessage, restoredCollectMessage)
	restoredEnvironmentVariables, err := workspaceEnvironmentIndex(
		t,
		reopened,
	).Variables()
	require.NoError(t, err)
	require.Equal(
		t,
		len(environmentVariables),
		len(restoredEnvironmentVariables),
	)
	restoredAppEnv, found, err := workspaceEnvironmentIndex(
		t,
		reopened,
	).Variable("APP_ENV")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, appEnv, restoredAppEnv)
	restoredServiceDefinitions, err := workspaceServiceIndex(
		t,
		reopened,
	).GetAllServiceDefinitions()
	require.NoError(t, err)
	require.Equal(t, len(serviceDefinitions), len(restoredServiceDefinitions))
	if configuredMethodFixtureValue.path != "" {
		restoredConfiguredMethodCompletions := lspcompletion.
			NewServiceCompletionProvider(
				workspaceServiceIndex(t, reopened),
				restoredPHP,
			).
			GetCompletions(
				ctx,
				&lsp.CompletionRequest{
					CompletionParams: configuredMethodCompletionParams,
					SyntaxContext: lsp.SyntaxContext{
						Document:        configuredMethodDocument,
						Language:        configuredMethodDocument.SyntaxLanguage,
						DocumentContent: configuredMethodDocument.Text,
						DocumentTree:    configuredMethodDocument.SyntaxTree,
						LineIndex:       configuredMethodDocument.LineIndex,
						Root:            configuredMethodDocument.SyntaxTree.Root,
						Node:            configuredMethodNode,
					},
				},
			)
		require.Equal(
			t,
			configuredMethodCompletions,
			restoredConfiguredMethodCompletions,
		)
		restoredConfiguredMethodDefinition := lspdefinition.
			NewServiceXMLDefinitionProvider(
				workspaceServiceIndex(t, reopened),
				restoredPHP,
			).
			GetDefinition(
				ctx,
				realWorldDefinitionRequest(
					configuredMethodDocument,
					configuredMethodNode,
					configuredMethodOffset,
				),
			)
		require.Equal(
			t,
			configuredMethodDefinition,
			restoredConfiguredMethodDefinition,
		)
	}
	restoredServiceAuthoringProvider := lspcompletion.
		NewYAMLServiceAuthoringCompletionProvider(restoredPHP.Project())
	require.Equal(
		t,
		serviceOptionCompletions,
		restoredServiceAuthoringProvider.GetCompletions(
			ctx,
			realWorldCompletionRequest(
				serviceOptionDocument,
				uint32(len(serviceOptionSource)),
			),
		),
	)
	require.Equal(
		t,
		serviceArgumentTagCompletions,
		restoredServiceAuthoringProvider.GetCompletions(
			ctx,
			realWorldCompletionRequest(
				serviceArgumentTagDocument,
				uint32(len(serviceArgumentTagSource)),
			),
		),
	)
	restoredRouteAuthoringProvider := lspcompletion.
		NewYAMLRouteAuthoringCompletionProvider(
			workspaceRouteIndex(t, reopened),
		)
	require.Equal(
		t,
		routeOptionCompletions,
		restoredRouteAuthoringProvider.GetCompletions(
			ctx,
			realWorldCompletionRequest(
				routeOptionDocument,
				uint32(len(routeOptionSource)),
			),
		),
	)
	require.Equal(
		t,
		routeRequirementCompletions,
		restoredRouteAuthoringProvider.GetCompletions(
			ctx,
			realWorldCompletionRequest(
				routeRequirementDocument,
				uint32(len(routeRequirementSource)),
			),
		),
	)
	restoredDeprecatedNotification, found, err := workspaceServiceIndex(
		t,
		reopened,
	).GetServiceByID(deprecatedNotification.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, deprecatedNotification, restoredDeprecatedNotification)
	restoredServiceLocatorCatalog, err := analytics.NewServiceLocatorProvider(
		workspaceServiceIndex(t, reopened),
		restoredPHP,
	).Locate(ctx, serviceLocatorRequest)
	require.NoError(t, err)
	require.Equal(
		t,
		serviceLocatorCatalog,
		restoredServiceLocatorCatalog,
	)
	restoredServiceDefinitionCollection, err := codeaction.
		NewSymfonyGeneratorProvider(
			restoredPHP,
			workspaceServiceIndex(t, reopened),
		).
		CollectServiceDefinitions(ctx, serviceDefinitionRequest)
	require.NoError(t, err)
	require.Equal(
		t,
		serviceDefinitionCollection,
		restoredServiceDefinitionCollection,
	)
	restoredForm, found, err := workspaceFormIndex(t, reopened).GetType(
		"entity",
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, entityForm.Class, restoredForm.Class)
	restoredFormOptions, err := workspaceFormIndex(
		t,
		reopened,
	).EffectiveOptions(restoredForm.Class)
	require.NoError(t, err)
	requireFormOption(t, restoredFormOptions, "class")
	requireFormOption(t, restoredFormOptions, "query_builder")
	restoredFormCatalogProvider := analytics.NewFormCatalogProvider(
		root,
		workspaceFormIndex(t, reopened),
		restoredPHP,
	)
	restoredEntityFormCatalog, err := restoredFormCatalogProvider.Types(
		ctx,
		entityFormCatalogRequest,
	)
	require.NoError(t, err)
	require.Equal(t, entityFormCatalog, restoredEntityFormCatalog)
	restoredEntityFormOptions, err := restoredFormCatalogProvider.Options(
		ctx,
		entityFormOptionRequest,
	)
	require.NoError(t, err)
	require.Equal(t, entityFormOptionCatalog, restoredEntityFormOptions)
	restoredSecurityNames, err := workspaceSecurityIndex(t, reopened).Names()
	require.NoError(t, err)
	require.Equal(t, securityNames, restoredSecurityNames)
	restoredSecurityProviders, err := workspaceSecurityIndex(
		t,
		reopened,
	).ConfigNames(security.ConfigProvider)
	require.NoError(t, err)
	require.Equal(t, securityProviders, restoredSecurityProviders)
	restoredSecurityFirewalls, err := workspaceSecurityIndex(
		t,
		reopened,
	).ConfigNames(security.ConfigFirewall)
	require.NoError(t, err)
	require.Equal(t, securityFirewalls, restoredSecurityFirewalls)
	restoredConfigurationRoots, err := workspaceSymfonyConfigIndex(
		t,
		reopened,
	).Names()
	require.NoError(t, err)
	require.Equal(t, configurationRoots, restoredConfigurationRoots)
	restoredShopwareRoots, err := workspaceSymfonyConfigIndex(
		t,
		reopened,
	).Roots("shopware")
	require.NoError(t, err)
	require.Equal(t, shopwareConfigurationRoots, restoredShopwareRoots)
	restoredSerializerClasses, err := workspaceSerializerIndex(
		t,
		reopened,
	).Classes()
	require.NoError(t, err)
	require.Equal(t, serializerClasses, restoredSerializerClasses)
	restoredDoctrineModels, err := workspaceDoctrineIndex(
		t,
		reopened,
	).Models()
	require.NoError(t, err)
	require.Equal(t, doctrineModels, restoredDoctrineModels)
	restoredDoctrineCatalog, err := analytics.NewDoctrineCatalogProvider(
		root,
		workspaceDoctrineIndex(t, reopened),
	).Entities(
		ctx,
		analytics.DoctrineEntityCatalogRequest{
			Query: "Shopware\\Core\\Kernel",
		},
	)
	require.NoError(t, err)
	require.Equal(t, doctrineCatalog, restoredDoctrineCatalog)
	restoredHTMLRoutes, err := workspaceRouteIndex(
		t,
		reopened,
	).FindRoutesByPath("/sitemap/shop.xml.gz")
	require.NoError(t, err)
	require.Equal(t, htmlRoutes, restoredHTMLRoutes)
	restoredRouteCatalogProvider := analytics.NewRouteCatalogProvider(
		root,
		workspaceRouteIndex(t, reopened),
		workspaceServiceIndex(t, reopened),
		restoredPHP,
		workspaceTwigIndex(t, reopened),
	)
	restoredAllRouteCatalog, err := restoredRouteCatalogProvider.Catalog(
		ctx,
		analytics.RouteCatalogRequest{},
	)
	require.NoError(t, err)
	require.Equal(t, allRouteCatalog, restoredAllRouteCatalog)
	restoredRouteCatalog, err := restoredRouteCatalogProvider.Catalog(
		ctx,
		routeCatalogRequest,
	)
	require.NoError(t, err)
	require.Equal(t, routeCatalog, restoredRouteCatalog)
	require.Equal(
		t,
		routePathCompletions,
		restoredRouteAuthoringProvider.GetCompletions(
			ctx,
			realWorldCompletionRequest(
				routePathDocument,
				uint32(len(routePathSource)),
			),
		),
	)
	restoredPHPRoutePathProvider := lspcompletion.NewRouteCompletionProvider(
		workspaceRouteIndex(t, reopened),
	)
	require.Equal(
		t,
		phpRoutePathCompletions,
		restoredPHPRoutePathProvider.GetCompletions(
			ctx,
			realWorldCompletionRequest(
				phpRoutePathDocument,
				phpRoutePathOffset,
			),
		),
	)
	require.Equal(
		t,
		annotationRoutePathCompletions,
		restoredPHPRoutePathProvider.GetCompletions(
			ctx,
			realWorldCompletionRequest(
				annotationRoutePathDocument,
				annotationRoutePathOffset,
			),
		),
	)
	require.Equal(
		t,
		routeNameCompletions,
		restoredPHPRoutePathProvider.GetCompletions(
			ctx,
			realWorldCompletionRequest(
				routeNameDocument,
				routeNameOffset,
			),
		),
	)
	require.Equal(
		t,
		annotationRouteNameCompletions,
		restoredPHPRoutePathProvider.GetCompletions(
			ctx,
			realWorldCompletionRequest(
				annotationRouteNameDocument,
				annotationRouteNameOffset,
			),
		),
	)
	restoredRouteAssistantRequest := realWorldCompletionRequest(
		routeAssistantDocument,
		routeAssistantOffset,
	)
	restoredRouteAssistantContext := restoredPHP.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "RouteAssistantUsage.php"),
		routeAssistantDocument.Version,
		restoredRouteAssistantRequest.Node,
		restoredRouteAssistantRequest.Root,
	)
	require.Equal(
		t,
		routeAssistantCompletions,
		restoredPHPRoutePathProvider.GetCompletions(
			restoredRouteAssistantContext,
			restoredRouteAssistantRequest,
		),
	)
	require.Equal(
		t,
		routeAssistantDefinition,
		lspdefinition.NewRouteDefinitionProvider(
			workspaceRouteIndex(t, reopened),
		).GetDefinition(
			restoredRouteAssistantContext,
			realWorldDefinitionRequest(
				routeAssistantDocument,
				restoredRouteAssistantRequest.Node,
				routeAssistantOffset,
			),
		),
	)
	restoredServiceAssistantRequest := realWorldCompletionRequest(
		serviceAssistantDocument,
		serviceAssistantOffset,
	)
	restoredServiceAssistantContext := restoredPHP.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "ServiceAssistantUsage.php"),
		serviceAssistantDocument.Version,
		restoredServiceAssistantRequest.Node,
		restoredServiceAssistantRequest.Root,
	)
	require.Equal(
		t,
		serviceAssistantCompletions,
		lspcompletion.NewServiceCompletionProvider(
			workspaceServiceIndex(t, reopened),
			restoredPHP,
		).GetCompletions(
			restoredServiceAssistantContext,
			restoredServiceAssistantRequest,
		),
	)
	require.Equal(
		t,
		serviceAssistantDefinition,
		lspdefinition.NewServiceXMLDefinitionProvider(
			workspaceServiceIndex(t, reopened),
			restoredPHP,
		).GetDefinition(
			restoredServiceAssistantContext,
			realWorldDefinitionRequest(
				serviceAssistantDocument,
				restoredServiceAssistantRequest.Node,
				serviceAssistantOffset,
			),
		),
	)
	restoredParameterAssistantRequest := realWorldCompletionRequest(
		parameterAssistantDocument,
		parameterAssistantOffset,
	)
	restoredParameterAssistantContext := restoredPHP.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "ParameterAssistantUsage.php"),
		parameterAssistantDocument.Version,
		restoredParameterAssistantRequest.Node,
		restoredParameterAssistantRequest.Root,
	)
	require.Equal(
		t,
		parameterAssistantCompletions,
		lspcompletion.NewServiceCompletionProvider(
			workspaceServiceIndex(t, reopened),
			restoredPHP,
		).GetCompletions(
			restoredParameterAssistantContext,
			restoredParameterAssistantRequest,
		),
	)
	require.Equal(
		t,
		parameterAssistantDefinition,
		lspdefinition.NewServiceXMLDefinitionProvider(
			workspaceServiceIndex(t, reopened),
			restoredPHP,
		).GetDefinition(
			restoredParameterAssistantContext,
			realWorldDefinitionRequest(
				parameterAssistantDocument,
				restoredParameterAssistantRequest.Node,
				parameterAssistantOffset,
			),
		),
	)
	restoredClassAssistantRequest := realWorldCompletionRequest(
		classAssistantDocument,
		classAssistantOffset,
	)
	restoredClassAssistantContext := restoredPHP.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "ClassAssistantUsage.php"),
		classAssistantDocument.Version,
		restoredClassAssistantRequest.Node,
		restoredClassAssistantRequest.Root,
	)
	restoredClassAssistantProvider := lspphpsemantic.New(restoredPHP)
	require.Equal(
		t,
		classAssistantCompletions,
		restoredClassAssistantProvider.GetCompletions(
			restoredClassAssistantContext,
			restoredClassAssistantRequest,
		),
	)
	require.Equal(
		t,
		classAssistantDefinition,
		restoredClassAssistantProvider.GetDefinition(
			restoredClassAssistantContext,
			realWorldDefinitionRequest(
				classAssistantDocument,
				restoredClassAssistantRequest.Node,
				classAssistantOffset,
			),
		),
	)
	restoredEntityAssistantRequest := realWorldCompletionRequest(
		entityAssistantDocument,
		entityAssistantOffset,
	)
	restoredEntityAssistantContext := restoredPHP.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "EntityAssistantUsage.php"),
		entityAssistantDocument.Version,
		restoredEntityAssistantRequest.Node,
		restoredEntityAssistantRequest.Root,
	)
	require.Equal(
		t,
		entityAssistantCompletions,
		lspcompletion.NewDoctrineCompletionProvider(
			workspaceDoctrineIndex(t, reopened),
			restoredPHP,
		).GetCompletions(
			restoredEntityAssistantContext,
			restoredEntityAssistantRequest,
		),
	)
	require.Equal(
		t,
		entityAssistantDefinition,
		lspdefinition.NewDoctrineDefinitionProvider(
			workspaceDoctrineIndex(t, reopened),
			restoredPHP,
		).GetDefinition(
			restoredEntityAssistantContext,
			realWorldDefinitionRequest(
				entityAssistantDocument,
				restoredEntityAssistantRequest.Node,
				entityAssistantOffset,
			),
		),
	)
	restoredFormAssistantRequest := realWorldCompletionRequest(
		formAssistantDocument,
		formAssistantOffset,
	)
	restoredFormAssistantContext := restoredPHP.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "FormAssistantUsage.php"),
		formAssistantDocument.Version,
		restoredFormAssistantRequest.Node,
		restoredFormAssistantRequest.Root,
	)
	require.Equal(
		t,
		formAssistantCompletions,
		lspcompletion.NewFormCompletionProvider(
			workspaceFormIndex(t, reopened),
			restoredPHP,
		).GetCompletions(
			restoredFormAssistantContext,
			restoredFormAssistantRequest,
		),
	)
	require.Equal(
		t,
		formAssistantDefinition,
		lspdefinition.NewFormDefinitionProvider(
			workspaceFormIndex(t, reopened),
			restoredPHP,
		).GetDefinition(
			restoredFormAssistantContext,
			realWorldDefinitionRequest(
				formAssistantDocument,
				restoredFormAssistantRequest.Node,
				formAssistantOffset,
			),
		),
	)
	restoredTemplateAssistantRequest := realWorldCompletionRequest(
		templateAssistantDocument,
		templateAssistantOffset,
	)
	restoredTemplateAssistantContext := restoredPHP.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "TemplateAssistantUsage.php"),
		templateAssistantDocument.Version,
		restoredTemplateAssistantRequest.Node,
		restoredTemplateAssistantRequest.Root,
	)
	require.Equal(
		t,
		templateAssistantCompletions,
		lspcompletion.NewTwigCompletionProvider(
			root,
			workspaceTwigIndex(t, reopened),
			nil,
			restoredPHP,
		).GetCompletions(
			restoredTemplateAssistantContext,
			restoredTemplateAssistantRequest,
		),
	)
	require.Equal(
		t,
		templateAssistantDefinition,
		lspdefinition.NewTwigDefinitionProvider(
			root,
			workspaceTwigIndex(t, reopened),
			nil,
			restoredPHP,
		).GetDefinition(
			restoredTemplateAssistantContext,
			realWorldDefinitionRequest(
				templateAssistantDocument,
				restoredTemplateAssistantRequest.Node,
				templateAssistantOffset,
			),
		),
	)
	restoredTranslationAssistantRequest := realWorldCompletionRequest(
		translationAssistantDocument,
		translationAssistantOffset,
	)
	restoredTranslationAssistantContext := restoredPHP.AddDocumentContext(
		ctx,
		filepath.Join(root, "src", "TranslationAssistantUsage.php"),
		translationAssistantDocument.Version,
		restoredTranslationAssistantRequest.Node,
		restoredTranslationAssistantRequest.Root,
	)
	require.Equal(
		t,
		translationAssistantCompletions,
		lspcompletion.NewTranslationCompletionProvider(
			workspaceTranslationIndex(t, reopened),
			restoredPHP,
		).GetCompletions(
			restoredTranslationAssistantContext,
			restoredTranslationAssistantRequest,
		),
	)
	require.Equal(
		t,
		translationAssistantDefinition,
		lspdefinition.NewTranslationDefinitionProvider(
			workspaceTranslationIndex(t, reopened),
			restoredPHP,
		).GetDefinition(
			restoredTranslationAssistantContext,
			realWorldDefinitionRequest(
				translationAssistantDocument,
				restoredTranslationAssistantRequest.Node,
				translationAssistantOffset,
			),
		),
	)
	restoredRouteAssistantDiagnostics, err := lspdiagnostics.
		NewRouteAnalyzer(
			workspaceRouteIndex(t, reopened),
			workspaceServiceIndex(t, reopened),
			restoredPHP,
		).
		Analyze(ctx, routeAssistantDiagnosticDocument)
	require.NoError(t, err)
	require.Equal(
		t,
		routeAssistantDiagnostics,
		restoredRouteAssistantDiagnostics,
	)
	restoredServiceAssistantDiagnostics, err := lspdiagnostics.
		NewServiceAnalyzer(
			workspaceServiceIndex(t, reopened),
			restoredPHP,
		).
		Analyze(ctx, serviceAssistantDiagnosticDocument)
	require.NoError(t, err)
	require.Equal(
		t,
		serviceAssistantDiagnostics,
		restoredServiceAssistantDiagnostics,
	)
	restoredTranslationAssistantDiagnostics, err := lspdiagnostics.
		NewTranslationAnalyzer(
			workspaceTranslationIndex(t, reopened),
			restoredPHP,
		).
		Analyze(ctx, translationAssistantDiagnosticDocument)
	require.NoError(t, err)
	require.Equal(
		t,
		translationAssistantDiagnostics,
		restoredTranslationAssistantDiagnostics,
	)
	restoredAbsoluteHTMLRoutes, err := workspaceRouteIndex(
		t,
		reopened,
	).FindRoutesByPath(
		"https://shopware.test/sitemap/shop.xml.gz?preview=1#details",
	)
	require.NoError(t, err)
	require.Equal(t, absoluteHTMLRoutes, restoredAbsoluteHTMLRoutes)
	restoredRoutePrefixMatches, err := workspaceRouteIndex(
		t,
		reopened,
	).FindRoutesByPathPrefix(
		"/store-api/checkout/cart/line-item",
		"/store-api/checkout/cart/line-item/delete",
	)
	require.NoError(t, err)
	require.Equal(t, routePrefixMatches, restoredRoutePrefixMatches)
	restoredBundleRouteDefinition := lspdefinition.
		NewRouteDefinitionProvider(
			workspaceRouteIndex(t, reopened),
			restoredPHP,
		).
		GetDefinition(
			ctx,
			realWorldDefinitionRequest(
				bundleRouteDocument,
				bundleRouteDocument.SyntaxTree.Root.NodeAtOffset(
					bundleRouteOffset,
				),
				bundleRouteOffset,
			),
		)
	require.Equal(
		t,
		bundleRouteDefinition,
		restoredBundleRouteDefinition,
	)
	restoredBundleResourceCompletions := lspcompletion.
		NewBundleResourceCompletionProvider(restoredPHP).
		GetCompletions(
			ctx,
			&lsp.CompletionRequest{
				CompletionParams: bundleCompletionParams,
				SyntaxContext: lsp.SyntaxContext{
					Document:        bundleRouteDocument,
					Language:        bundleRouteDocument.SyntaxLanguage,
					DocumentContent: bundleRouteDocument.Text,
					DocumentTree:    bundleRouteDocument.SyntaxTree,
					LineIndex:       bundleRouteDocument.LineIndex,
					Root:            bundleRouteDocument.SyntaxTree.Root,
					Node: bundleRouteDocument.SyntaxTree.Root.
						NodeAtOffset(bundleRouteOffset),
				},
			},
		)
	require.Equal(
		t,
		bundleResourceCompletions,
		restoredBundleResourceCompletions,
	)
	restoredRouteComparisonDiagnostics, err := lspdiagnostics.
		NewRouteAnalyzer(
			workspaceRouteIndex(t, reopened),
			workspaceServiceIndex(t, reopened),
			restoredPHP,
		).
		Analyze(ctx, routeComparisonDocument)
	require.NoError(t, err)
	require.Empty(t, restoredRouteComparisonDiagnostics)
	restoredRouteComparisonDefinition := lspdefinition.
		NewRouteDefinitionProvider(
			workspaceRouteIndex(t, reopened),
		).
		GetDefinition(
			ctx,
			realWorldDefinitionRequest(
				routeComparisonDocument,
				routeComparisonNode,
				routeComparisonOffset,
			),
		)
	require.Equal(
		t,
		routeComparisonDefinition,
		restoredRouteComparisonDefinition,
	)
	restoredAssetNames, err := workspaceAssetIndex(t, reopened).Names()
	require.NoError(t, err)
	require.Equal(t, assetNames, restoredAssetNames)
	restoredAssetPackageNames, err := workspaceAssetIndex(
		t,
		reopened,
	).PackageNames()
	require.NoError(t, err)
	require.Equal(t, assetPackageNames, restoredAssetPackageNames)
	restoredAdministrationAssets, err := workspaceAssetIndex(
		t,
		reopened,
	).FindAssetsForPackage(
		"administration/static/img/favicon/favicon-16x16.png",
		"@Administration",
	)
	require.NoError(t, err)
	require.Equal(t, administrationAssets, restoredAdministrationAssets)
	restoredThemeAssets, err := workspaceAssetIndex(
		t,
		reopened,
	).FindAssetsForPackage(
		"assets/illustration/404_error.svg",
		"theme",
	)
	require.NoError(t, err)
	require.Equal(t, themeAssets, restoredThemeAssets)
	restoredAdministrationPackageUsages, err := workspaceAssetIndex(
		t,
		reopened,
	).Usages(
		"@Administration",
		asset.AssetPackageReference,
	)
	require.NoError(t, err)
	require.Equal(
		t,
		administrationPackageUsages,
		restoredAdministrationPackageUsages,
	)
	restoredHTMLAssetUsages, err := workspaceAssetIndex(
		t,
		reopened,
	).Usages(
		"_webpack_hot_proxy_/storefront/hot-reloading.js",
		asset.AssetReference,
	)
	require.NoError(t, err)
	require.Equal(t, htmlAssetUsages, restoredHTMLAssetUsages)
	restoredEncoreEntries, err := workspaceAssetIndex(
		t,
		reopened,
	).EntryNames()
	require.NoError(t, err)
	require.Equal(t, encoreEntries, restoredEncoreEntries)
	restoredImportmapEntries, err := workspaceAssetIndex(
		t,
		reopened,
	).ImportmapEntryNames()
	require.NoError(t, err)
	require.Equal(t, importmapEntries, restoredImportmapEntries)
	restoredViteEntries, err := workspaceAssetIndex(
		t,
		reopened,
	).ViteEntryNames()
	require.NoError(t, err)
	require.Equal(t, viteEntries, restoredViteEntries)
	restoredViteUsages, err := workspaceAssetIndex(t, reopened).Usages(
		"app",
		asset.ViteEntryReference,
	)
	require.NoError(t, err)
	require.Equal(t, viteUsages, restoredViteUsages)
	restoredViteAdministrationUsages, err := workspaceAssetIndex(
		t,
		reopened,
	).Usages("administration", asset.ViteEntryReference)
	require.NoError(t, err)
	require.Equal(
		t,
		viteAdministrationUsages,
		restoredViteAdministrationUsages,
	)
	restoredTwigMacros, err := workspaceTwigIndex(t, reopened).GetAllMacros()
	require.NoError(t, err)
	require.Equal(t, twigMacros, restoredTwigMacros)
	restoredTwigFunctions, err := workspaceTwigIndex(
		t,
		reopened,
	).GetAllTwigFunctions()
	require.NoError(t, err)
	require.Equal(t, twigFunctions, restoredTwigFunctions)
	restoredTwigTests, err := workspaceTwigIndex(
		t,
		reopened,
	).GetAllTwigTests()
	require.NoError(t, err)
	require.Equal(t, twigTests, restoredTwigTests)
	restoredTwigOperators, err := workspaceTwigIndex(
		t,
		reopened,
	).GetAllTwigOperators()
	require.NoError(t, err)
	require.Equal(t, twigOperators, restoredTwigOperators)
	restoredCategoryURLFunctions, err := workspaceTwigIndex(
		t,
		reopened,
	).GetTwigFunction("category_url")
	require.NoError(t, err)
	require.Equal(t, categoryURLFunctions, restoredCategoryURLFunctions)
	restoredTwigTags, err := workspaceTwigIndex(t, reopened).GetAllTwigTags()
	require.NoError(t, err)
	require.Equal(t, twigTags, restoredTwigTags)
	restoredTwigExtensionCatalog, err := analytics.
		NewTwigExtensionCatalogProvider(
			workspaceTwigIndex(t, reopened),
			restoredPHP,
		).
		Catalog(ctx, analytics.TwigExtensionCatalogRequest{})
	require.NoError(t, err)
	require.Equal(t, twigExtensionCatalog, restoredTwigExtensionCatalog)
	restoredTwigGlobals, err := workspaceTwigIndex(t, reopened).GetAllGlobals()
	require.NoError(t, err)
	require.Equal(t, twigGlobals, restoredTwigGlobals)
	restoredBaseReferences, err := workspaceTwigIndex(
		t,
		reopened,
	).GetTemplateReferences("@Storefront/storefront/base.html.twig")
	require.NoError(t, err)
	require.Equal(t, baseTemplateReferences, restoredBaseReferences)
	restoredBaseTemplateUsageCatalog, err := analytics.
		NewTwigTemplateUsageCatalogProvider(
			root,
			workspaceTwigIndex(t, reopened),
			restoredPHP,
			workspaceRouteIndex(t, reopened),
			workspaceServiceIndex(t, reopened),
			workspaceTwigComponentIndex(t, reopened),
		).
		Catalog(ctx, baseTemplateUsageRequest)
	require.NoError(t, err)
	require.Equal(
		t,
		baseTemplateUsageCatalog,
		restoredBaseTemplateUsageCatalog,
	)
	restoredProductReferences, err := workspaceTwigIndex(
		t,
		reopened,
	).GetTemplateReferences(
		"@Storefront/storefront/page/content/product-detail.html.twig",
	)
	require.NoError(t, err)
	require.Equal(t, productTemplateReferences, restoredProductReferences)
	restoredProfilerUsages, err := workspaceTwigIndex(
		t,
		reopened,
	).GetMacroUsages("Collector/db.html.twig", "render_simple_table")
	require.NoError(t, err)
	require.Equal(t, profilerMacroUsages, restoredProfilerUsages)
	restoredProductInputs, err := workspaceTwigIndex(
		t,
		reopened,
	).GetTemplateVariables(
		"@Storefront/storefront/page/content/product-detail.html.twig",
	)
	require.NoError(t, err)
	require.Equal(t, productTemplateInputs, restoredProductInputs)
	restoredProductTemplateVariableCatalog, err := analytics.
		NewTwigTemplateVariableCatalogProvider(
			root,
			workspaceTwigIndex(t, reopened),
			restoredPHP,
			workspaceTwigComponentIndex(t, reopened),
		).
		Catalog(ctx, productTemplateVariableRequest)
	require.NoError(t, err)
	require.Equal(
		t,
		productTemplateVariableCatalog,
		restoredProductTemplateVariableCatalog,
	)
	restoredProductBlocks, err := workspaceTwigIndex(
		t,
		reopened,
	).GetTemplateBlocks(
		"@Storefront/storefront/page/content/product-detail.html.twig",
	)
	require.NoError(t, err)
	require.Equal(t, productTemplateBlocks, restoredProductBlocks)
	require.Equal(
		t,
		workspaceSymbols,
		realWorldSymbolSnapshot(t, ctx, reopened),
	)
	require.Equal(
		t,
		relatedNavigation,
		realWorldRelatedNavigationSnapshot(
			t,
			ctx,
			root,
			reopened,
			restoredPHP,
		),
	)
	require.Equal(
		t,
		routeEndpoints,
		realWorldRouteEndpointSnapshot(
			t,
			ctx,
			root,
			reopened,
			restoredPHP,
		),
	)
	require.Equal(
		t,
		consoleCommandLenses,
		realWorldConsoleCommandLensSnapshot(t, ctx, root),
	)
	require.Equal(
		t,
		serviceNavigation,
		realWorldServiceNavigationSnapshot(
			t,
			ctx,
			root,
			reopened,
			restoredPHP,
		),
	)
	require.Equal(
		t,
		controllerUsageNavigation,
		realWorldControllerUsageSnapshot(
			t,
			ctx,
			root,
			reopened,
			restoredPHP,
		),
	)
	restoredTwigComponentNames, err := workspaceTwigComponentIndex(
		t,
		reopened,
	).Names()
	require.NoError(t, err)
	require.Equal(t, twigComponentNames, restoredTwigComponentNames)
	restoredTwigComponentCatalog, err := analytics.
		NewTwigComponentCatalogProvider(
			workspaceTwigComponentIndex(t, reopened),
		).
		Catalog(ctx, analytics.TwigComponentCatalogRequest{})
	require.NoError(t, err)
	require.Equal(t, twigComponentCatalog, restoredTwigComponentCatalog)
	restoredTwigConstantReferences, err := workspaceTwigIndex(
		t,
		reopened,
	).GetConstantReferences(twigConstantTarget)
	require.NoError(t, err)
	require.Equal(
		t,
		twigConstantReferences,
		restoredTwigConstantReferences,
	)
	restoredTwigPHPClassReferences, err := workspaceTwigIndex(
		t,
		reopened,
	).GetPHPUsageReferences(twigPHPClassTarget)
	require.NoError(t, err)
	require.Equal(
		t,
		twigPHPClassReferences,
		restoredTwigPHPClassReferences,
	)
	restoredTwigTransFilterUsages, err := workspaceTwigIndex(
		t,
		reopened,
	).GetExtensionUsages(
		twig.ExtensionFilterUsage,
		"trans",
	)
	require.NoError(t, err)
	require.Equal(
		t,
		twigTransFilterUsages,
		restoredTwigTransFilterUsages,
	)
	restoredTwigDefinedTestUsages, err := workspaceTwigIndex(
		t,
		reopened,
	).GetExtensionUsages(
		twig.ExtensionTestUsage,
		"defined",
	)
	require.NoError(t, err)
	require.Equal(
		t,
		twigDefinedTestUsages,
		restoredTwigDefinedTestUsages,
	)
	require.Equal(
		t,
		twigUXSemanticTokens,
		realWorldTwigUXSemanticTokenSnapshot(t, ctx),
	)
	t.Logf("cache restore: %s", restoreElapsed.Round(time.Millisecond))
	require.NoError(t, reopened.Close())
}

func realWorldDocumentLinkTargets(
	links []protocol.DocumentLink,
) []string {
	result := make([]string, 0, len(links))
	for _, link := range links {
		result = append(result, link.Target)
	}
	return result
}

func realWorldProjectRoot(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT"); configured != "" {
		root, err := filepath.Abs(configured)
		require.NoError(t, err)
		require.DirExists(t, root)
		return root
	}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	root := filepath.Join(home, "Developer", "sw-trunk")
	if _, err := os.Stat(root); err != nil {
		t.Skipf(
			"real-world checkout not found at %s; set SHOPWARE_LSP_REAL_WORLD_ROOT",
			root,
		)
	}
	return root
}

func openRealWorldWorkspace(
	t *testing.T,
	ctx context.Context,
	root string,
) (*Workspace, *php.PHPIndex) {
	t.Helper()
	workspace, phpIndex, _ := openRealWorldWorkspaceWithServer(
		t, ctx, root,
	)
	return workspace, phpIndex
}

func openRealWorldWorkspaceWithServer(
	t *testing.T,
	ctx context.Context,
	root string,
) (*Workspace, *php.PHPIndex, *lsp.Server) {
	t.Helper()
	server := lsp.NewServer(nil, root, "integration-test")
	workspace, err := NewWorkspace(ctx, root, server)
	require.NoError(t, err)
	for _, idx := range workspace.indexers {
		if phpIndex, ok := idx.(*php.PHPIndex); ok {
			return workspace, phpIndex, server
		}
	}
	require.NoError(t, workspace.Close())
	t.Fatal("PHP index is not registered")
	return nil, nil, nil
}

func formatBytes(value uint64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case value >= gib:
		return formatFloat(float64(value)/gib) + " GiB"
	case value >= mib:
		return formatFloat(float64(value)/mib) + " MiB"
	case value >= kib:
		return formatFloat(float64(value)/kib) + " KiB"
	default:
		return formatFloat(float64(value)) + " B"
	}
}

func requireTwigGlobal(
	t *testing.T,
	globals []twig.Global,
	name,
	typeName string,
) {
	t.Helper()
	for _, global := range globals {
		if global.Name != name {
			continue
		}
		if typeName == "" || global.Type == typeName {
			return
		}
	}
	require.Failf(
		t,
		"missing Twig global",
		"name=%s type=%s globals=%v",
		name,
		typeName,
		globals,
	)
}

func requireTwigTag(
	t *testing.T,
	tags []twig.TwigTag,
	name string,
) {
	t.Helper()
	for _, tag := range tags {
		if tag.Name == name {
			return
		}
	}
	t.Fatalf("Twig tag %q not found in %#v", name, tags)
}

func requireTwigOperator(
	t *testing.T,
	operators []twig.TwigOperator,
	name,
	pathSuffix string,
) {
	t.Helper()
	for _, operator := range operators {
		if operator.Name == name &&
			strings.HasSuffix(
				filepath.ToSlash(operator.FilePath),
				filepath.ToSlash(pathSuffix),
			) {
			return
		}
	}
	t.Fatalf(
		"Twig operator %q from %q not found in %#v",
		name,
		pathSuffix,
		operators,
	)
}

func requireTemplateReferencePath(
	t *testing.T,
	references []twig.TemplateReference,
	suffix string,
) {
	t.Helper()
	suffix = filepath.Clean(suffix)
	for _, reference := range references {
		if strings.HasSuffix(filepath.Clean(reference.FilePath), suffix) {
			return
		}
	}
	t.Fatalf("template reference path ending in %q not found", suffix)
}

func formatFloat(value float64) string {
	return fmt.Sprintf("%.1f", value)
}

func requireTwigVariable(
	t *testing.T,
	variables []php.TwigTemplateVariable,
	name string,
) {
	t.Helper()
	for _, variable := range variables {
		if variable.Name == name {
			return
		}
	}
	t.Fatalf("Twig variable %q not found in %#v", name, variables)
}

func requireFormGeneratorCandidate(
	t *testing.T,
	candidates []realWorldFormCandidate,
	name,
	suggestedType string,
) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.Name == name {
			require.Equal(t, suggestedType, candidate.SuggestedType)
			return
		}
	}
	t.Fatalf("form generator candidate %q not found in %#v", name, candidates)
}

func requireTwigTemplateInput(
	t *testing.T,
	variables []twig.TemplateVariable,
	name string,
) {
	t.Helper()
	for _, variable := range variables {
		if variable.Name == name {
			return
		}
	}
	t.Fatalf("Twig template input %q not found in %#v", name, variables)
}

func requireTwigBlock(
	t *testing.T,
	blocks []twig.TemplateBlock,
	name string,
) {
	t.Helper()
	for _, block := range blocks {
		if block.Name == name {
			require.NotZero(t, block.Range.Len())
			require.FileExists(t, block.FilePath)
			return
		}
	}
	t.Fatalf("Twig block %q not found in %#v", name, blocks)
}

func requireRoute(t *testing.T, routes []symfony.Route, name string) {
	t.Helper()
	for _, route := range routes {
		if route.Name == name {
			return
		}
	}
	t.Fatalf("Symfony route %q not found in %#v", name, routes)
}

func requireRealWorldSuggestionDiagnostic(
	t *testing.T,
	document *lsp.TextDocument,
	diagnostics []lsp.Problem,
	code,
	value,
	suggestion string,
) lsp.Problem {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if fmt.Sprint(diagnostic.ID) != code {
			continue
		}
		data, ok := diagnostic.Payload.(map[string]any)
		require.True(t, ok)
		switch suggestions := data["suggestions"].(type) {
		case []string:
			require.Contains(t, suggestions, suggestion)
		case []any:
			require.Contains(t, suggestions, suggestion)
		default:
			t.Fatalf(
				"diagnostic %q has unsupported suggestions %#v",
				code,
				data["suggestions"],
			)
		}
		start := diagnostic.Range.Start
		end := diagnostic.Range.End
		require.Equal(t, value, document.Source[start:end])
		return diagnostic
	}
	t.Fatalf("diagnostic %q not found in %#v", code, diagnostics)
	return lsp.Problem{}
}

func realWorldSymbolSnapshot(
	t *testing.T,
	ctx context.Context,
	workspace *Workspace,
) map[string][]protocol.SymbolInformation {
	t.Helper()
	provider := lspsymbol.NewSymfonyWorkspaceSymbolProvider(
		nil,
		workspaceRouteIndex(t, workspace),
		workspaceConsoleIndex(t, workspace),
		workspaceTwigIndex(t, workspace),
		workspaceDoctrineIndex(t, workspace),
		workspaceTwigComponentIndex(t, workspace),
		workspaceTranslationIndex(t, workspace),
		workspacePHPIndex(t, workspace),
	)
	queries := map[string]string{
		"frontend.home.page": "Symfony route · GET / · " +
			"NavigationController:home",
		"Shopware\\Storefront\\Controller\\NavigationController::home": "Symfony controller · " +
			"Shopware\\Storefront\\Controller\\NavigationController",
		"system:config:get":                     "Symfony command",
		"@Storefront/storefront/base.html.twig": "Twig template",
		"base_body":                             "Twig block",
		"shopware.installer.header_title":       "Translation",
	}
	result := make(map[string][]protocol.SymbolInformation, len(queries))
	for query, container := range queries {
		symbols, err := provider.WorkspaceSymbols(ctx, query)
		require.NoError(t, err)
		requireWorkspaceSymbol(t, symbols, query, container)
		result[query] = symbols
	}
	return result
}

func realWorldRouteEndpointSnapshot(
	t *testing.T,
	ctx context.Context,
	root string,
	workspace *Workspace,
	phpIndex *php.PHPIndex,
) []protocol.CodeLens {
	t.Helper()
	controllerPath := filepath.Join(
		root,
		"src",
		"Storefront",
		"Controller",
		"NavigationController.php",
	)
	lenses := realWorldCodeLenses(
		t,
		ctx,
		codelens.NewRouteEndpointCodeLensProvider(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		),
		controllerPath,
	)
	requireRelatedLensTarget(
		t,
		lenses,
		"GET / · frontend.home.page",
		uriutil.FileURIWithFragment(controllerPath, "56"),
	)
	return lenses
}

func realWorldConsoleCommandLensSnapshot(
	t *testing.T,
	ctx context.Context,
	root string,
) []protocol.CodeLens {
	t.Helper()
	commandPath := filepath.Join(
		root,
		"src",
		"Core",
		"System",
		"SystemConfig",
		"Command",
		"ConfigGet.php",
	)
	lenses := realWorldCodeLenses(
		t,
		ctx,
		codelens.NewConsoleCommandCodeLensProvider(root),
		commandPath,
	)
	requireCodeLensCommand(
		t,
		lenses,
		"Run system:config:get",
		"shopware.symfony.runConsoleCommand",
		[]any{
			"system:config:get",
			uriutil.FileURI(commandPath),
		},
	)
	return lenses
}

type relatedNavigationSnapshot struct {
	Controller []protocol.CodeLens
	Template   []protocol.CodeLens
}

func realWorldRelatedNavigationSnapshot(
	t *testing.T,
	ctx context.Context,
	root string,
	workspace *Workspace,
	phpIndex *php.PHPIndex,
) relatedNavigationSnapshot {
	t.Helper()
	controllerPath := filepath.Join(
		root,
		"src",
		"Storefront",
		"Controller",
		"NavigationController.php",
	)
	templatePath := filepath.Join(
		root,
		"src",
		"Storefront",
		"Resources",
		"views",
		"storefront",
		"page",
		"content",
		"index.html.twig",
	)
	provider := codelens.NewRelatedNavigationCodeLensProvider(
		workspaceTwigIndex(t, workspace),
		phpIndex,
		workspaceRouteIndex(t, workspace),
		workspaceServiceIndex(t, workspace),
	)
	result := relatedNavigationSnapshot{
		Controller: realWorldCodeLenses(
			t,
			ctx,
			provider,
			controllerPath,
		),
		Template: realWorldCodeLenses(
			t,
			ctx,
			provider,
			templatePath,
		),
	}
	requireRelatedLensTarget(
		t,
		result.Controller,
		"related template",
		uriutil.FileURIWithFragment(templatePath, "1"),
	)
	requireRelatedLensTarget(
		t,
		result.Controller,
		"route definition",
		uriutil.FileURIWithFragment(controllerPath, "49"),
	)
	requireRelatedLensTarget(
		t,
		result.Template,
		"rendering PHP location",
		uriutil.FileURIWithFragment(controllerPath, "62"),
	)
	requireRelatedLensTarget(
		t,
		result.Template,
		"related route",
		uriutil.FileURIWithFragment(controllerPath, "49"),
	)
	return result
}

func realWorldCodeLenses(
	t *testing.T,
	ctx context.Context,
	provider lsp.CodeLensProvider,
	path string,
) []protocol.CodeLens {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path),
		string(source),
		1,
	)
	params := &protocol.CodeLensParams{}
	params.TextDocument.URI = document.URI
	lenses, err := provider.GetCodeLenses(ctx, &lsp.CodeLensRequest{
		CodeLensParams: params,
		Document:       document,
	})
	require.NoError(t, err)
	require.NotEmpty(t, lenses)
	return lenses
}

func realWorldDocumentSymbols(
	t *testing.T,
	ctx context.Context,
	provider lsp.DocumentSymbolProvider,
	path string,
) []protocol.DocumentSymbol {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(uriutil.FileURI(path), string(source), 1)
	symbols, err := provider.GetDocumentSymbols(
		ctx,
		&lsp.DocumentSymbolRequest{
			DocumentSymbolParams: &protocol.DocumentSymbolParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
			},
			Document: document,
		},
	)
	require.NoError(t, err)
	return symbols
}

func realWorldDocumentHighlights(
	t *testing.T,
	ctx context.Context,
	provider lsp.DocumentHighlightProvider,
	path,
	needle string,
) []protocol.DocumentHighlight {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 1,
	)
	offset := uint32(strings.Index(document.Source, needle) + 1)
	require.Greater(t, int(offset), 0)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DocumentHighlightParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Position: protocol.Position{
			Line: int(line), Character: int(character),
		},
	}
	highlights, err := provider.GetDocumentHighlights(
		ctx,
		&lsp.DocumentHighlightRequest{
			DocumentHighlightParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	return highlights
}

func realWorldLinkedEditingRanges(
	t *testing.T,
	ctx context.Context,
	provider lsp.LinkedEditingRangeProvider,
	path,
	needle string,
) *protocol.LinkedEditingRanges {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 1,
	)
	offset := uint32(strings.Index(document.Source, needle) + 1)
	require.Greater(t, int(offset), 0)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.LinkedEditingRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Position: protocol.Position{
			Line: int(line), Character: int(character),
		},
	}
	ranges, err := provider.GetLinkedEditingRanges(
		ctx,
		&lsp.LinkedEditingRangeRequest{
			LinkedEditingRangeParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	if ranges != nil {
		for _, rangeValue := range ranges.Ranges {
			start := document.LineIndex.OffsetUTF16(
				uint32(rangeValue.Start.Line),
				uint32(rangeValue.Start.Character),
			)
			end := document.LineIndex.OffsetUTF16(
				uint32(rangeValue.End.Line),
				uint32(rangeValue.End.Character),
			)
			require.Equal(t, needle, string(document.Text[start:end]))
		}
	}
	return ranges
}

func realWorldFoldingRanges(
	t *testing.T,
	ctx context.Context,
	provider lsp.FoldingRangeProvider,
	path string,
) []protocol.FoldingRange {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 1,
	)
	ranges, err := provider.GetFoldingRanges(
		ctx,
		&lsp.FoldingRangeRequest{
			FoldingRangeParams: &protocol.FoldingRangeParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
			},
			Document: document,
		},
	)
	require.NoError(t, err)
	return ranges
}

func realWorldSelectionTexts(
	t *testing.T,
	ctx context.Context,
	provider lsp.SelectionRangeProvider,
	path,
	needle string,
) []string {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 1,
	)
	offset := uint32(strings.Index(document.Source, needle) + 1)
	require.Greater(t, int(offset), 0)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.SelectionRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Positions: []protocol.Position{{
			Line: int(line), Character: int(character),
		}},
	}
	ranges, err := provider.GetSelectionRanges(
		ctx,
		&lsp.SelectionRangeRequest{
			SelectionRangeParams: params, Document: document,
		},
	)
	require.NoError(t, err)
	require.Len(t, ranges, 1)
	var result []string
	for current := &ranges[0]; current != nil; current = current.Parent {
		start := document.LineIndex.OffsetUTF16(
			uint32(current.Range.Start.Line),
			uint32(current.Range.Start.Character),
		)
		end := document.LineIndex.OffsetUTF16(
			uint32(current.Range.End.Line),
			uint32(current.Range.End.Character),
		)
		result = append(result, string(document.Text[start:end]))
	}
	return result
}

func realWorldDocumentColors(
	t *testing.T,
	ctx context.Context,
	provider lsp.DocumentColorProvider,
	path string,
) ([]protocol.ColorInformation, *lsp.TextDocument) {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(uriutil.FileURI(path), string(source), 1)
	colors, err := provider.GetDocumentColors(
		ctx,
		&lsp.DocumentColorRequest{
			DocumentColorParams: &protocol.DocumentColorParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
			},
			Document: document,
		},
	)
	require.NoError(t, err)
	return colors, document
}

func realWorldRangeText(document *lsp.TextDocument, value protocol.Range) string {
	start := document.LineIndex.OffsetUTF16(
		uint32(value.Start.Line), uint32(value.Start.Character),
	)
	end := document.LineIndex.OffsetUTF16(
		uint32(value.End.Line), uint32(value.End.Character),
	)
	return string(document.Text[start:end])
}

func adminWorkspaceEditCount(edit *protocol.WorkspaceEdit) int {
	if edit == nil {
		return 0
	}
	count := 0
	for _, edits := range edit.Changes {
		count += len(edits)
	}
	return count
}

func applyRealWorldTextEdits(
	t *testing.T,
	source []byte,
	edits []protocol.TextEdit,
) string {
	t.Helper()
	lineIndex := cst.NewLineIndex(string(source))
	result := string(source)
	for _, edit := range edits {
		start := lineIndex.OffsetUTF16(
			uint32(edit.Range.Start.Line), uint32(edit.Range.Start.Character),
		)
		end := lineIndex.OffsetUTF16(
			uint32(edit.Range.End.Line), uint32(edit.Range.End.Character),
		)
		require.LessOrEqual(t, start, end)
		require.LessOrEqual(t, int(end), len(result))
		result = result[:start] + edit.NewText + result[end:]
	}
	return result
}

func requireRealWorldSelectionContaining(
	t *testing.T,
	values []string,
	needle string,
) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(value, needle) {
			return
		}
	}
	require.Failf(
		t, "missing selection ancestor", "expected %q in %#v", needle, values,
	)
}

func requireRealWorldFoldingRange(
	t *testing.T,
	ranges []protocol.FoldingRange,
	startLine,
	endLine int,
	kind string,
) {
	t.Helper()
	for _, rangeValue := range ranges {
		if rangeValue.StartLine == startLine && rangeValue.EndLine == endLine &&
			rangeValue.Kind == kind {
			return
		}
	}
	require.Failf(
		t,
		"missing real-world folding range",
		"expected %d..%d kind %q in %#v",
		startLine,
		endLine,
		kind,
		ranges,
	)
}

func realWorldCallHierarchyItems(
	t *testing.T,
	ctx context.Context,
	provider lsp.CallHierarchyProvider,
	path,
	needle string,
) []protocol.CallHierarchyItem {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 1,
	)
	offset := uint32(strings.Index(document.Source, needle) + 1)
	require.Greater(t, int(offset), 0)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CallHierarchyPrepareParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Position: protocol.Position{
			Line: int(line), Character: int(character),
		},
	}
	items, err := provider.PrepareCallHierarchy(
		ctx,
		&lsp.CallHierarchyPrepareRequest{
			CallHierarchyPrepareParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	return items
}

func requireDocumentSymbolNamed(
	t *testing.T,
	symbols []protocol.DocumentSymbol,
	name string,
) protocol.DocumentSymbol {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Name == name {
			return symbol
		}
	}
	t.Fatalf("document symbol %q not found in %#v", name, symbols)
	return protocol.DocumentSymbol{}
}

func requireDocumentSymbolNamedRecursive(
	t *testing.T,
	symbols []protocol.DocumentSymbol,
	name string,
) protocol.DocumentSymbol {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Name == name {
			return symbol
		}
		if child, found := findDocumentSymbolNamed(symbol.Children, name); found {
			return child
		}
	}
	t.Fatalf("document symbol %q not found recursively in %#v", name, symbols)
	return protocol.DocumentSymbol{}
}

func findDocumentSymbolNamed(
	symbols []protocol.DocumentSymbol,
	name string,
) (protocol.DocumentSymbol, bool) {
	for _, symbol := range symbols {
		if symbol.Name == name {
			return symbol, true
		}
		if child, found := findDocumentSymbolNamed(symbol.Children, name); found {
			return child, true
		}
	}
	return protocol.DocumentSymbol{}, false
}

type controllerUsageSnapshot struct {
	Usages     []symfony.ControllerUsage
	Controller []protocol.CodeLens
	Template   []protocol.CodeLens
}

func realWorldControllerUsageSnapshot(
	t *testing.T,
	ctx context.Context,
	root string,
	workspace *Workspace,
	phpIndex *php.PHPIndex,
) controllerUsageSnapshot {
	t.Helper()
	controllerPath := filepath.Join(
		root,
		"src",
		"Core",
		"Profiling",
		"Controller",
		"ProfilerController.php",
	)
	templatePath := filepath.Join(
		root,
		"src",
		"Core",
		"Profiling",
		"Resources",
		"views",
		"Collector",
		"db.html.twig",
	)
	reference, ok := symfony.ParseControllerReference(
		"Shopware\\Core\\Profiling\\Controller\\ProfilerController::explainAction",
	)
	require.True(t, ok)
	usageIndex := workspaceRouteUsageIndex(t, workspace)
	usages, err := usageIndex.GetControllerUsages(reference)
	require.NoError(t, err)
	require.NotEmpty(t, usages)
	requireControllerUsagePath(t, usages, templatePath)
	provider := codelens.NewControllerRelatedCodeLensProvider(
		usageIndex,
		workspaceServiceIndex(t, workspace),
		phpIndex,
	)
	result := controllerUsageSnapshot{
		Usages: usages,
		Controller: realWorldCodeLenses(
			t,
			ctx,
			provider,
			controllerPath,
		),
		Template: realWorldCodeLenses(
			t,
			ctx,
			provider,
			templatePath,
		),
	}
	requireRelatedLensTarget(
		t,
		result.Controller,
		"Twig controller usage",
		uriutil.FileURIWithFragment(templatePath, "80"),
	)
	requireRelatedLensTarget(
		t,
		result.Template,
		"controller method",
		uriutil.FileURIWithFragment(controllerPath, "29"),
	)
	return result
}

func requireControllerUsagePath(
	t *testing.T,
	usages []symfony.ControllerUsage,
	path string,
) {
	t.Helper()
	for _, usage := range usages {
		if filepath.Clean(usage.File) == filepath.Clean(path) {
			return
		}
	}
	t.Fatalf("controller usage %q not found in %#v", path, usages)
}

type serviceNavigationSnapshot struct {
	Decorators []protocol.CodeLens
	Target     []protocol.CodeLens
}

func realWorldServiceNavigationSnapshot(
	t *testing.T,
	ctx context.Context,
	root string,
	workspace *Workspace,
	phpIndex *php.PHPIndex,
) serviceNavigationSnapshot {
	t.Helper()
	decoratorPath := filepath.Join(
		root,
		"src",
		"Elasticsearch",
		"DependencyInjection",
		"services.php",
	)
	targetPath := filepath.Join(
		root,
		"src",
		"Core",
		"Framework",
		"DependencyInjection",
		"data-abstraction-layer.php",
	)
	provider := codelens.NewServiceRelatedCodeLensProvider(
		workspaceServiceIndex(t, workspace),
		phpIndex,
	)
	result := serviceNavigationSnapshot{
		Decorators: realWorldCodeLenses(
			t,
			ctx,
			provider,
			decoratorPath,
		),
		Target: realWorldCodeLenses(
			t,
			ctx,
			provider,
			targetPath,
		),
	}
	requireRelatedLensFileTargets(
		t,
		result.Decorators,
		"decorated service",
		uriutil.FileURI(targetPath),
		1,
	)
	requireRelatedLensFileTargets(
		t,
		result.Target,
		"decorating services",
		uriutil.FileURI(decoratorPath),
		2,
	)
	return result
}

func requireRelatedLensFileTargets(
	t *testing.T,
	lenses []protocol.CodeLens,
	title,
	fileURI string,
	minimum int,
) {
	t.Helper()

	targets := make(map[string]struct{})
	for _, lens := range lenses {
		if lens.Command == nil ||
			!strings.Contains(lens.Command.Title, title) ||
			len(lens.Command.Arguments) != 1 {
			continue
		}
		currentTargets, ok := lens.Command.Arguments[0].([]string)
		if !ok {
			continue
		}
		for _, current := range currentTargets {
			if current == fileURI || strings.HasPrefix(current, fileURI+"#") {
				targets[current] = struct{}{}
			}
		}
	}
	require.GreaterOrEqual(
		t,
		len(targets),
		minimum,
		"related code lens %q targets in %q",
		title,
		fileURI,
	)
}

func requireRelatedLensTarget(
	t *testing.T,
	lenses []protocol.CodeLens,
	title,
	target string,
) {
	t.Helper()
	for _, lens := range lenses {
		if lens.Command == nil ||
			!strings.Contains(lens.Command.Title, title) ||
			len(lens.Command.Arguments) != 1 {
			continue
		}
		targets, ok := lens.Command.Arguments[0].([]string)
		if !ok {
			continue
		}
		for _, current := range targets {
			if current == target {
				return
			}
		}
	}
	t.Fatalf(
		"related code lens %q target %q not found in %#v",
		title,
		target,
		lenses,
	)
}

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

func realWorldPHPAttributeCompletions(
	phpIndex *php.PHPIndex,
	source,
	needle string,
) []protocol.CompletionItem {
	document := lsp.NewTextDocument(
		"file:///real-world-php-attribute.php",
		source,
		1,
	)
	offset := strings.LastIndex(source, needle) + len(needle)
	if offset < len(needle) {
		return nil
	}
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	nodeOffset := offset
	if nodeOffset > 0 {
		nodeOffset--
	}
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(nodeOffset))
	return lspcompletion.NewPHPAttributeCompletionProvider(
		phpIndex,
	).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
}

func realWorldTwigCompletions(
	root string,
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
	source,
	needle string,
) []protocol.CompletionItem {
	document := lsp.NewTextDocument(
		"file:///real-world-loop.html.twig",
		source,
		1,
	)
	offset := strings.LastIndex(source, needle) + len(needle)
	if offset < len(needle) {
		return nil
	}
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	nodeOffset := offset
	if nodeOffset > 0 {
		nodeOffset--
	}
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(nodeOffset))
	return lspcompletion.NewTwigCompletionProvider(
		root,
		twigIndex,
		nil,
		phpIndex,
	).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
}

func realWorldCompletionLabels(
	items []protocol.CompletionItem,
) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Label)
	}
	sort.Strings(result)
	return result
}

func realWorldCompletionRequest(
	document *lsp.TextDocument,
	offset uint32,
) *lsp.CompletionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	if node == nil && offset > 0 {
		node = document.SyntaxTree.Root.NodeAtOffset(offset - 1)
	}
	return &lsp.CompletionRequest{
		CompletionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}

func realWorldSignatureRequest(
	document *lsp.TextDocument,
	offset uint32,
) *lsp.SignatureHelpRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.SignatureHelpParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	if node == nil && offset > 0 {
		node = document.SyntaxTree.Root.NodeAtOffset(offset - 1)
	}
	return &lsp.SignatureHelpRequest{
		SignatureHelpParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document, DocumentContent: document.Text,
			DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
			Root: document.SyntaxTree.Root, Node: node,
		},
	}
}

func realWorldCompletionByLabel(
	t *testing.T,
	items []protocol.CompletionItem,
	label string,
) protocol.CompletionItem {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("completion %q not found in %v", label,
		realWorldCompletionLabels(items))
	return protocol.CompletionItem{}
}

func realWorldTwigUXSemanticTokenSnapshot(
	t *testing.T,
	ctx context.Context,
) []string {
	t.Helper()
	document := lsp.NewTextDocument(
		"file:///real-world-ux-annotations.html.twig",
		`{# @prop title string Visible title #}
{# @block content Main content #}`,
		1,
	)
	tokens, err := lspsemantic.NewTwigUXToolkitProvider().
		GetSemanticTokens(
			ctx,
			&lsp.SemanticTokensRequest{Document: document},
		)
	require.NoError(t, err)
	require.Len(t, tokens, 5)
	result := make([]string, 0, len(tokens))
	for _, token := range tokens {
		result = append(
			result,
			fmt.Sprintf(
				"%d:%s",
				token.Type,
				string(document.Text[token.Range.Start:token.Range.End]),
			),
		)
	}
	require.Equal(t, []string{
		fmt.Sprintf("%d:@prop", protocol.SemanticTokenKeyword),
		fmt.Sprintf("%d:title", protocol.SemanticTokenProperty),
		fmt.Sprintf("%d:string", protocol.SemanticTokenType),
		fmt.Sprintf("%d:@block", protocol.SemanticTokenKeyword),
		fmt.Sprintf("%d:content", protocol.SemanticTokenProperty),
	}, result)
	return result
}

func realWorldDefinitionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
) *lsp.DefinitionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.DefinitionRequest{
		DefinitionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}

func realWorldReferenceRequest(
	document *lsp.TextDocument,
	offset uint32,
	includeDeclaration bool,
) *lsp.ReferenceRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = includeDeclaration
	return &lsp.ReferenceRequest{
		ReferenceParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			Token:           document.SyntaxTree.Root.TokenAtOffset(offset),
		},
	}
}

func realWorldRenameRequest(
	document *lsp.TextDocument,
	offset uint32,
	newName string,
) *lsp.RenameRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.RenameParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.NewName = newName
	return &lsp.RenameRequest{
		RenameParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			Token:           document.SyntaxTree.Root.TokenAtOffset(offset),
		},
	}
}

func realWorldHoverRequest(
	document *lsp.TextDocument,
	offset uint32,
) *lsp.HoverRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			Token:           document.SyntaxTree.Root.TokenAtOffset(offset),
		},
	}
}

func requireRouteDefinitionPath(
	t *testing.T,
	locations []protocol.Location,
	path string,
) {
	t.Helper()
	requireLocationURI(t, locations, path)
}

func requireLocationURI(
	t *testing.T,
	locations []protocol.Location,
	path string,
) {
	t.Helper()
	uri := uriutil.FileURI(path)
	for _, location := range locations {
		if location.URI == uri {
			return
		}
	}
	t.Fatalf("location %q not found in %#v", uri, locations)
}

func realWorldCodeActionRequest(
	t *testing.T,
	document *lsp.TextDocument,
	needle string,
) *lsp.CodeActionRequest {
	t.Helper()
	require.NotNil(t, document)
	require.NotNil(t, document.SyntaxTree)
	require.NotNil(t, document.SyntaxTree.Root)
	offset := strings.Index(document.Source, needle)
	require.NotEqual(t, -1, offset)
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CodeActionParams{
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
			End: protocol.Position{
				Line:      int(line),
				Character: int(character + uint32(len(needle))),
			},
		},
	}
	params.TextDocument.URI = document.URI
	return &lsp.CodeActionRequest{
		CodeActionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	}
}
