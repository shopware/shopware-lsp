//go:build integration

package app

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	adminpkg "github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	lspdiagnostics "github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

// TestShopwareTrunkAdminMarkupAudit is a development audit for typed
// Administration markup diagnostics. It intentionally reports rather than
// fails on findings so a changing local Shopware checkout remains usable as a
// discovery fixture.
func TestShopwareTrunkAdminMarkupAudit(t *testing.T) {
	root := realWorldProjectRoot(t)
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	ctx := context.Background()
	workspace, _ := openRealWorldWorkspace(t, ctx, root)
	require.NoError(t, workspace.Scanner().IndexAll(ctx))
	adminIndex := workspaceAdminIndex(t, workspace)
	analyzer := lspdiagnostics.NewAdminAnalyzer(adminIndex)
	allowedValueProps := 0
	completeAllowedValueProps := 0
	allowedValueCandidates := 0
	effectiveComponents := 0
	indexedProps := 0
	typedProps := 0
	requiredProps := 0
	sourceOwnedProps := 0
	exactPropRanges := 0
	componentNames, err := adminIndex.GetAllComponentNames()
	require.NoError(t, err)
	for _, componentName := range componentNames {
		component, componentErr := adminIndex.GetEffectiveComponent(componentName)
		require.NoError(t, componentErr)
		if component == nil {
			continue
		}
		effectiveComponents++
		for _, prop := range component.Props {
			indexedProps++
			if strings.TrimSpace(prop.Type) != "" {
				typedProps++
			}
			if prop.Required {
				requiredProps++
			}
			if prop.FilePath != "" {
				sourceOwnedProps++
			}
			if prop.NameRange.Declaration {
				exactPropRanges++
			}
			values, complete := adminpkg.VuePropAllowedValues(prop)
			if len(values) == 0 {
				continue
			}
			allowedValueProps++
			allowedValueCandidates += len(values)
			if complete {
				completeAllowedValueProps++
			}
		}
	}
	for _, fixture := range []struct {
		component string
		prop      string
		values    []string
	}{
		{"sw-label", "variant", []string{"primary", "neutral-reversed"}},
		{"mt-button", "variant", []string{"primary", "action"}},
		{"mt-button", "size", []string{"x-small", "large"}},
	} {
		component, componentErr := adminIndex.GetEffectiveComponent(fixture.component)
		require.NoError(t, componentErr)
		require.NotNil(t, component)
		prop, found := component.ComponentProp(fixture.prop)
		require.True(t, found, "%s.%s", fixture.component, fixture.prop)
		require.NotEmpty(t, prop.FilePath, "%s.%s", fixture.component, fixture.prop)
		require.True(
			t, prop.NameRange.Declaration,
			"%s.%s must retain its exact declaration range",
			fixture.component, fixture.prop,
		)
		values, complete := adminpkg.VuePropAllowedValues(prop)
		require.True(t, complete, "%s.%s", fixture.component, fixture.prop)
		for _, value := range fixture.values {
			require.Contains(t, values, value, "%s.%s", fixture.component, fixture.prop)
		}
	}
	directives, err := adminIndex.GetAllDirectives()
	require.NoError(t, err)
	knownDirectives := make(map[string]bool, len(directives))
	for _, directive := range directives {
		knownDirectives[directive.Name] = true
	}
	for _, name := range []string{
		"tooltip", "draggable", "droppable", "autofocus",
	} {
		require.True(t, knownDirectives[name], "directive %s", name)
	}
	cmsElements, err := adminIndex.GetAllCMSRegistrationsByKind(
		adminpkg.AdminCMSElement,
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cmsElements), 20)
	cmsBlocks, err := adminIndex.GetAllCMSRegistrationsByKind(
		adminpkg.AdminCMSBlock,
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cmsBlocks), 30)
	cmsComponentLinks := 0
	for _, registration := range append(cmsElements, cmsBlocks...) {
		for _, name := range []string{
			registration.Component,
			registration.ConfigComponent,
			registration.PreviewComponent,
		} {
			if name != "" {
				cmsComponentLinks++
			}
		}
	}
	administrationRoot := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src",
	)
	findings := 0
	typedFindings := 0
	invalidPropValueFindings := 0
	missingComponents := 0
	filesWithFindings := 0
	templates := 0
	dynamicSelectors := 0
	completeSelectors := 0
	staticCandidates := 0
	resolvedCandidates := 0
	singleContractSelectors := 0
	inferredSelectors := 0
	completeInferredSelectors := 0
	inferredCandidates := 0
	inferredContracts := 0
	objectBindings := 0
	completeObjectBindings := 0
	objectFields := 0
	contractObjectFields := 0
	nativeObjectFields := 0
	unresolvedObjectContractFields := 0
	unknownObjectPropFields := 0
	partialDynamicObjectFields := 0
	unresolvedObjectFieldsByTag := make(map[string]int)
	unknownObjectProps := make(map[string]int)
	directiveUsages := 0
	resolvedDirectiveUsages := 0
	unknownDirectiveUsages := make(map[string]int)
	directiveFindings := 0
	require.NoError(t, filepath.WalkDir(
		administrationRoot,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".twig" {
				return nil
			}
			templates++
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			document := lsp.NewTextDocument(
				uriutil.FileURI(path), string(source), 1,
			)
			templateDirectives, directiveErr :=
				adminIndex.GetAllDirectivesForTemplate(path)
			if directiveErr != nil {
				return directiveErr
			}
			templateDirectiveNames := make(
				map[string]bool, len(templateDirectives),
			)
			for _, directive := range templateDirectives {
				templateDirectiveNames[directive.Name] = true
			}
			for _, directive := range adminpkg.TwigDirectiveReferences(
				document.SyntaxTree.Root,
			) {
				directiveUsages++
				if templateDirectiveNames[directive.Name] {
					resolvedDirectiveUsages++
				} else {
					unknownDirectiveUsages[directive.Name]++
				}
			}
			for _, startTag := range twigquery.Nodes(
				document.SyntaxTree.Root, twigsyntax.HtmlStartingTag,
			) {
				selector, dynamic := adminpkg.TwigDynamicComponentSelector(startTag)
				var boundFields []adminpkg.VueObjectBindingField
				if tag, ok := twigast.CastHtmlStartingTag(startTag); ok {
					for attribute := range tag.Attributes() {
						if twigquery.HTMLAttributeName(attribute.Syntax()) != "v-bind" {
							continue
						}
						value, valueOK := attribute.Value()
						if !valueOK {
							continue
						}
						inner, innerOK := value.GetInner()
						if !innerOK {
							continue
						}
						fields, complete := adminpkg.VueObjectBindingFields(
							inner.Syntax().Text(), inner.Syntax().Range().Start,
						)
						if len(fields) == 0 {
							continue
						}
						objectBindings++
						objectFields += len(fields)
						if complete {
							completeObjectBindings++
						}
						boundFields = append(boundFields, fields...)
					}
				}
				if len(boundFields) > 0 {
					var contracts []adminpkg.VueComponent
					tagName := twigquery.HTMLTagName(startTag)
					if dynamic {
						_, resolved, complete, contractErr :=
							adminIndex.ResolveDynamicComponentContracts(path, selector, startTag)
						if contractErr != nil {
							return contractErr
						}
						if complete {
							contracts = resolved
						}
					} else if name, found := adminpkg.StaticComponentNameForTag(startTag); found {
						component, componentFound, componentErr :=
							adminIndex.GetComponentForTemplateTag(path, name)
						if componentErr != nil {
							return componentErr
						}
						if componentFound && component != nil {
							contracts = []adminpkg.VueComponent{*component}
						}
					}
					if len(contracts) == 0 {
						if dynamic || adminpkg.IsComponentTag(tagName) {
							unresolvedObjectContractFields += len(boundFields)
							unresolvedObjectFieldsByTag[tagName] += len(boundFields)
							if dynamic {
								fieldNames := make([]string, 0, len(boundFields))
								for _, field := range boundFields {
									fieldNames = append(fieldNames, field.Name)
								}
								t.Logf(
									"unresolved dynamic object binding: %s selector=%q fields=%s",
									strings.TrimPrefix(path, root+string(filepath.Separator)),
									selector.Expression,
									strings.Join(fieldNames, ","),
								)
							}
						} else {
							nativeObjectFields += len(boundFields)
						}
					}
					for _, field := range boundFields {
						knownContracts := 0
						for _, contract := range contracts {
							matched := false
							for _, prop := range contract.Props {
								if adminpkg.NormalizePropName(prop.Name) ==
									adminpkg.NormalizePropName(field.Name) {
									matched = true
									break
								}
							}
							if !matched {
								continue
							}
							knownContracts++
						}
						switch {
						case len(contracts) > 0 && knownContracts == len(contracts):
							contractObjectFields++
						case knownContracts > 0:
							partialDynamicObjectFields++
						case len(contracts) > 0:
							unknownObjectPropFields++
							unknownObjectProps[tagName+"."+field.Name]++
						}
					}
				}
				if !dynamic {
					continue
				}
				dynamicSelectors++
				if selector.Complete {
					completeSelectors++
				}
				staticCandidates += len(selector.Candidates)
				resolvedInSelector := 0
				for _, candidate := range selector.Candidates {
					_, found, resolveErr := adminIndex.GetComponentForTemplateTag(
						path, candidate.Name,
					)
					if resolveErr != nil {
						return resolveErr
					}
					if found {
						resolvedCandidates++
						resolvedInSelector++
					}
				}
				if selector.Complete && len(selector.Names()) == 1 &&
					resolvedInSelector > 0 {
					singleContractSelectors++
				}
				resolvedSelector, inferred, resolveErr :=
					adminIndex.ResolveDynamicComponentSelector(path, selector, startTag)
				if resolveErr != nil {
					return resolveErr
				}
				if inferred && !selector.Complete &&
					len(resolvedSelector.Names()) > 0 {
					inferredSelectors++
					inferredCandidates += len(resolvedSelector.Names())
					if resolvedSelector.Complete {
						completeInferredSelectors++
					}
					_, components, contractComplete, contractErr :=
						adminIndex.ResolveDynamicComponentContracts(path, selector, startTag)
					if contractErr != nil {
						return contractErr
					}
					if contractComplete {
						inferredContracts += len(components)
					}
				}
			}
			problems, err := analyzer.Analyze(ctx, document)
			if err != nil {
				return err
			}
			fileHasFinding := false
			for _, problem := range problems {
				switch string(problem.ID) {
				case "admin.component.bound-prop-type",
					"admin.component.model-not-assignable",
					"admin.component.model-type",
					"admin.component.unknown-vue-member",
					"admin.component.unknown-slot-prop":
					typedFindings++
				case "admin.component.invalid-prop-value":
					typedFindings++
					invalidPropValueFindings++
				case "admin.component.not-found":
					missingComponents++
				case "admin.directive.not-found":
					directiveFindings++
				default:
					continue
				}
				findings++
				fileHasFinding = true
				if findings > 100 {
					continue
				}
				relative, _ := filepath.Rel(root, path)
				line, _ := document.LineIndex.PositionUTF16(
					problem.Range.Start,
				)
				rootType := ""
				receiverType := ""
				resolvedBinding, resolveErr := adminIndex.ResolveTwigVueMember(
					document.SyntaxTree.Root,
					document.Text,
					problem.Range.Start,
					path,
				)
				if resolveErr != nil {
					return resolveErr
				}
				if resolvedBinding != nil {
					rootType = resolvedBinding.Binding.Type
					receiverType = resolvedBinding.ReceiverType
				} else {
					resolvedInstance, resolveErr :=
						adminIndex.ResolveTwigVueInstanceMember(
							document.SyntaxTree.Root,
							document.Text,
							problem.Range.Start,
							path,
						)
					if resolveErr != nil {
						return resolveErr
					}
					if resolvedInstance != nil {
						rootType = resolvedInstance.RootMember.Type
						receiverType = resolvedInstance.ReceiverType
					}
				}
				t.Logf(
					"%s:%d: %s (%s; root=%q receiver=%q)",
					filepath.ToSlash(relative),
					line+1,
					problem.Message,
					problem.ID,
					rootType,
					receiverType,
				)
			}
			if fileHasFinding {
				filesWithFindings++
			}
			return nil
		},
	))
	t.Logf(
		"Administration CMS registries: elements=%d blocks=%d component_links=%d",
		len(cmsElements), len(cmsBlocks), cmsComponentLinks,
	)
	t.Logf(
		"Administration markup audit: templates=%d files_with_findings=%d typed_findings=%d missing_components=%d",
		templates,
		filesWithFindings,
		typedFindings,
		missingComponents,
	)
	t.Logf(
		"Administration prop index: components=%d props=%d typed=%d required=%d source_owned=%d exact_ranges=%d",
		effectiveComponents,
		indexedProps,
		typedProps,
		requiredProps,
		sourceOwnedProps,
		exactPropRanges,
	)
	t.Logf(
		"Administration prop value contracts: props=%d complete=%d candidates=%d invalid_usages=%d",
		allowedValueProps,
		completeAllowedValueProps,
		allowedValueCandidates,
		invalidPropValueFindings,
	)
	t.Logf(
		"Administration dynamic components: selectors=%d complete=%d static_candidates=%d resolved_candidates=%d single_contracts=%d",
		dynamicSelectors,
		completeSelectors,
		staticCandidates,
		resolvedCandidates,
		singleContractSelectors,
	)
	t.Logf(
		"Administration inferred dynamic components: selectors=%d complete=%d candidates=%d resolved_contracts=%d",
		inferredSelectors,
		completeInferredSelectors,
		inferredCandidates,
		inferredContracts,
	)
	t.Logf(
		"Administration object v-bind: bindings=%d complete=%d fields=%d contract_fields=%d",
		objectBindings,
		completeObjectBindings,
		objectFields,
		contractObjectFields,
	)
	t.Logf(
		"Administration object v-bind classification: native_fields=%d unresolved_contract_fields=%d unknown_prop_fields=%d partial_dynamic_fields=%d",
		nativeObjectFields,
		unresolvedObjectContractFields,
		unknownObjectPropFields,
		partialDynamicObjectFields,
	)
	t.Logf(
		"Administration unresolved object-binding tags: %v",
		topAdminAuditCounts(unresolvedObjectFieldsByTag, 20),
	)
	t.Logf(
		"Administration unknown object-binding props: %v",
		topAdminAuditCounts(unknownObjectProps, 30),
	)
	t.Logf(
		"Administration custom directives: declarations=%d usages=%d resolved=%d typo_findings=%d unknown=%v",
		len(knownDirectives),
		directiveUsages,
		resolvedDirectiveUsages,
		directiveFindings,
		topAdminAuditCounts(unknownDirectiveUsages, 30),
	)
	require.Positive(t, dynamicSelectors)
	require.Positive(t, completeSelectors)
	require.Positive(t, resolvedCandidates)
	require.Positive(t, inferredSelectors)
	require.Positive(t, completeInferredSelectors)
	require.Positive(t, inferredContracts)
	require.Positive(t, objectBindings)
	require.Positive(t, objectFields)
	require.Positive(t, allowedValueProps)
	require.Positive(t, completeAllowedValueProps)
	require.Positive(t, len(knownDirectives))
	require.Positive(t, directiveUsages)
	require.Positive(t, resolvedDirectiveUsages)
	require.Positive(t, cmsComponentLinks)
}

func topAdminAuditCounts(counts map[string]int, limit int) []string {
	type entry struct {
		name  string
		count int
	}
	entries := make([]entry, 0, len(counts))
	for name, count := range counts {
		entries = append(entries, entry{name: name, count: count})
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].count != entries[right].count {
			return entries[left].count > entries[right].count
		}
		return entries[left].name < entries[right].name
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	result := make([]string, 0, len(entries))
	for _, item := range entries {
		result = append(result, item.name+"="+strconv.Itoa(item.count))
	}
	return result
}
