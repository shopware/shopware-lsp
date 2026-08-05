//go:build integration

package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp"
	lspphpsemantic "github.com/shopware/shopware-lsp/internal/lsp/phpsemantic"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

type diagnosticAuditGroup struct {
	code    string
	message string
	count   int
	samples []string
}

// TestShopwareTrunkPHPDiagnosticsAudit runs the production PHP diagnostics
// provider over real source files and prints the recurring findings. It is an
// investigation harness rather than a zero-diagnostics assertion because it
// is used to discover and classify gaps before adding focused regressions.
//
//	go test -tags=integration ./internal/app \
//	  -run '^TestShopwareTrunkPHPDiagnosticsAudit$' -count=1 -v
//
// Environment controls:
//   - SHOPWARE_LSP_DIAGNOSTICS_ROOT: directory to scan (default: <root>/src)
//   - SHOPWARE_LSP_DIAGNOSTICS_FILE: relative path or basename to inspect
//   - SHOPWARE_LSP_DIAGNOSTICS_CODES: comma-separated diagnostic codes to include
//   - SHOPWARE_LSP_DIAGNOSTICS_TRACE_VARIABLE: variable name to trace
//   - SHOPWARE_LSP_DIAGNOSTICS_TRACE_CALL: function/method call to trace
//   - SHOPWARE_LSP_DIAGNOSTICS_TRACE_CLASS: fully-qualified class to trace
//   - SHOPWARE_LSP_DIAGNOSTICS_MAX_FILES: deterministic file limit
//   - SHOPWARE_LSP_DIAGNOSTICS_MAX_GROUPS: ranked groups to print (default 50)
//   - SHOPWARE_LSP_DIAGNOSTICS_CACHE_DIR: reusable index cache
func TestShopwareTrunkPHPDiagnosticsAudit(t *testing.T) {
	root := realWorldProjectRoot(t)
	cacheRoot := os.Getenv("SHOPWARE_LSP_DIAGNOSTICS_CACHE_DIR")
	if cacheRoot == "" {
		cacheRoot = t.TempDir()
	}
	require.NoError(t, os.MkdirAll(cacheRoot, 0o755))
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", cacheRoot)

	auditRoot := os.Getenv("SHOPWARE_LSP_DIAGNOSTICS_ROOT")
	if auditRoot == "" {
		auditRoot = filepath.Join(root, "src")
	} else if !filepath.IsAbs(auditRoot) {
		auditRoot = filepath.Join(root, auditRoot)
	}
	maxFiles := 0
	if configured := os.Getenv("SHOPWARE_LSP_DIAGNOSTICS_MAX_FILES"); configured != "" {
		value, err := strconv.Atoi(configured)
		require.NoError(t, err)
		require.Positive(t, value)
		maxFiles = value
	}
	includedCodes := make(map[string]struct{})
	if configured := os.Getenv("SHOPWARE_LSP_DIAGNOSTICS_CODES"); configured != "" {
		for code := range strings.SplitSeq(configured, ",") {
			code = strings.TrimSpace(code)
			if code != "" {
				includedCodes[code] = struct{}{}
			}
		}
		require.NotEmpty(t, includedCodes)
	}

	ctx := context.Background()
	workspace, phpIndex := openRealWorldWorkspace(t, ctx, root)
	t.Cleanup(func() { require.NoError(t, workspace.Close()) })
	require.NoError(t, workspace.Scanner().IndexAll(ctx))

	var paths []string
	require.NoError(t, filepath.WalkDir(
		auditRoot,
		func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".php") {
				paths = append(paths, path)
			}
			return nil
		},
	))
	sort.Strings(paths)
	if configured := os.Getenv("SHOPWARE_LSP_DIAGNOSTICS_FILE"); configured != "" {
		configured = filepath.Clean(configured)
		filtered := paths[:0]
		for _, path := range paths {
			relative, err := filepath.Rel(root, path)
			require.NoError(t, err)
			if filepath.Clean(relative) == configured ||
				filepath.Base(path) == configured {
				filtered = append(filtered, path)
			}
		}
		paths = filtered
		require.NotEmpty(
			t,
			paths,
			"SHOPWARE_LSP_DIAGNOSTICS_FILE did not match a PHP file",
		)
	}
	if maxFiles > 0 && len(paths) > maxFiles {
		paths = paths[:maxFiles]
	}

	provider := lspphpsemantic.New(phpIndex)
	groups := make(map[string]*diagnosticAuditGroup)
	codeCounts := make(map[string]int)
	filesWithDiagnostics := 0
	diagnosticCount := 0
	started := time.Now()
	traceClass := os.Getenv("SHOPWARE_LSP_DIAGNOSTICS_TRACE_CLASS")
	if traceClass != "" {
		t.Logf(
			"class %s candidates=%v",
			traceClass,
			phpIndex.SemanticSnapshot().Classes(traceClass),
		)
	}
	for _, path := range paths {
		source, err := os.ReadFile(path)
		require.NoError(t, err)
		document := lsp.NewTextDocument(
			uriutil.FileURI(path),
			string(source),
			1,
		)
		diagnostics, err := provider.Analyze(ctx, document)
		require.NoError(t, err)
		variable := os.Getenv(
			"SHOPWARE_LSP_DIAGNOSTICS_TRACE_VARIABLE",
		)
		traceCall := os.Getenv("SHOPWARE_LSP_DIAGNOSTICS_TRACE_CALL")
		if variable != "" || traceCall != "" {
			semanticDocument := phpIndex.AnalyzeDocument(
				path,
				document.Version,
				document.SyntaxTree.Root,
			)
			for _, symbol := range semanticDocument.Symbols {
				if symbol.Name == variable {
					t.Logf(
						"symbol %s kind=%d scope-owner=%s range=%d..%d",
						symbol.ID,
						symbol.Kind,
						symbol.Container,
						symbol.Range.Start,
						symbol.Range.End,
					)
				}
			}
			for _, reference := range semanticDocument.References {
				if reference.Name == variable {
					referenceNode := document.SyntaxTree.Root.NodeAtOffset(
						reference.Range.Start,
					)
					t.Logf(
						"reference %s type=%s scope=%d resolved=%s candidates=%v range=%d..%d",
						reference.Name,
						semanticDocument.TypeOf(referenceNode).Type,
						reference.Scope,
						reference.Resolved,
						reference.CandidateIDs(),
						reference.Range.Start,
						reference.Range.End,
					)
				}
			}
			if traceCall != "" {
				for _, symbol := range semanticDocument.Symbols {
					if !strings.EqualFold(symbol.Name, traceCall) {
						continue
					}
					t.Logf(
						"document callable %s parameters=%v return=%s",
						symbol.FullyQualified,
						symbol.Parameters,
						symbol.ReturnType,
					)
				}
				for _, symbol := range phpIndex.
					SemanticSnapshot().
					SymbolsIn(path) {
					if !strings.EqualFold(symbol.Name, traceCall) {
						continue
					}
					t.Logf(
						"indexed callable %s parameters=%v return=%s",
						symbol.FullyQualified,
						symbol.Parameters,
						symbol.ReturnType,
					)
				}
				for _, candidate := range phpIndex.
					SemanticSnapshot().
					Functions(traceCall) {
					t.Logf(
						"function %s parameters=%v return=%s",
						candidate.FullyQualified,
						candidate.Parameters,
						candidate.ReturnType,
					)
				}
				for _, call := range phpquery.Calls(document.SyntaxTree.Root) {
					if !strings.EqualFold(
						phpquery.CallMethodName(call),
						traceCall,
					) {
						continue
					}
					fact := semanticDocument.TypeOf(call)
					t.Logf(
						"call %s expression=%q range=%v result type=%s source=%v reason=%q",
						traceCall,
						strings.TrimSpace(call.Text()),
						call.RangeTrimmedTrivia(),
						fact.Type,
						fact.Source,
						fact.Reason,
					)
					var resolvedArguments []resolver.Argument
					for index, argument := range phpquery.Arguments(call) {
						var expression *phpsyntax.Node
						for child := argument.ChildCount() - 1; child >= 0; child-- {
							expression, _ = argument.Child(child).(*phpsyntax.Node)
							if expression != nil {
								break
							}
						}
						if expression != nil {
							fact := semanticDocument.TypeOf(expression)
							resolvedArguments = append(
								resolvedArguments,
								resolver.Argument{
									Name: phpquery.ArgumentName(argument),
									Type: fact.Type,
								},
							)
							t.Logf(
								"call %s argument %d %q type=%s",
								traceCall,
								index,
								strings.TrimSpace(expression.Text()),
								fact.Type,
							)
						}
					}
					for _, symbol := range phpIndex.
						SemanticSnapshot().
						SymbolsIn(path) {
						if !strings.EqualFold(symbol.Name, traceCall) {
							continue
						}
						resolved := resolver.ResolveSignature(
							phpIndex.SemanticSnapshot().Relations(),
							symbol,
							resolvedArguments,
						)
						t.Logf(
							"signature %s compatible=%t",
							symbol.FullyQualified,
							resolved.Compatible,
						)
					}
					for _, symbol := range phpIndex.
						SemanticSnapshot().
						Functions(traceCall) {
						resolved := resolver.ResolveSignature(
							phpIndex.SemanticSnapshot().Relations(),
							symbol,
							resolvedArguments,
						)
						t.Logf(
							"function signature %s parameters=%v return=%s compatible=%t",
							symbol.FullyQualified,
							symbol.Parameters,
							resolved.ReturnType,
							resolved.Compatible,
						)
					}
					if call.Kind() == phpsyntax.PhpMemberCall ||
						call.Kind() == phpsyntax.PhpScopedCall {
						receiverNode := firstAuditNode(call)
						receiverType := semanticDocument.
							TypeOf(receiverNode).
							Type
						if receiverType.IsUnknown() &&
							call.Kind() == phpsyntax.PhpScopedCall &&
							receiverNode.Kind() == phpsyntax.PhpName {
							if scope, found := semanticDocument.ScopeAt(
								receiverNode.Range().Start,
							); found {
								receiverType = types.Named(
									(resolver.NameContext{
										Namespace: scope.Namespace,
										Imports:   scope.Imports,
									}).ResolveClass(
										phpquery.NameValue(receiverNode),
									),
								)
							}
						}
						t.Logf(
							"call %s receiver %q type=%s",
							traceCall,
							strings.TrimSpace(receiverNode.Text()),
							receiverType,
						)
						if receiverType.Kind() == types.UnionKind {
							for index := 0; index < receiverType.ArgumentCount(); index++ {
								arm := receiverType.Argument(index)
								for _, member := range (resolver.MemberResolver{
									Snapshot: phpIndex.SemanticSnapshot(),
								}).Methods(arm, traceCall) {
									t.Logf(
										"member arm %s effective-return=%s",
										arm,
										member.Symbol.ReturnType,
									)
								}
							}
						}
						for _, member := range (resolver.MemberResolver{
							Snapshot: phpIndex.SemanticSnapshot(),
						}).Methods(receiverType, traceCall) {
							resolved := resolver.ResolveSignature(
								phpIndex.SemanticSnapshot().Relations(),
								member.Symbol,
								resolvedArguments,
							)
							t.Logf(
								"member signature %s path=%s parameters=%v return=%s effective-return=%s resolved-return=%s declarations=%v inferred=%v compatible=%t",
								member.Symbol.FullyQualified,
								member.Symbol.Path,
								member.Symbol.Parameters,
								member.Symbol.ReturnType,
								member.Type,
								resolved.ReturnType,
								member.Symbol.Templates,
								resolved.Templates,
								resolved.Compatible,
							)
							for index, argument := range resolvedArguments {
								if index >= len(member.Symbol.Parameters) {
									break
								}
								expected := types.Substitute(
									member.Symbol.Parameters[index].Type,
									resolved.Templates,
								)
								t.Logf(
									"member argument %d actual=%s expected=%s assignable=%t",
									index,
									argument.Type,
									expected,
									phpIndex.SemanticSnapshot().
										Relations().
										IsAssignableTo(argument.Type, expected),
								)
							}
						}
					}
				}
				for _, creation := range phpquery.Nodes(
					document.SyntaxTree.Root,
					phpsyntax.PhpObjectCreation,
				) {
					nameNode := phpquery.DirectChild(
						creation,
						phpsyntax.PhpName,
					)
					if nameNode == nil {
						continue
					}
					rawName := phpquery.NameValue(nameNode)
					if !strings.EqualFold(rawName, traceCall) &&
						!strings.HasSuffix(
							strings.ToLower(rawName),
							"\\"+strings.ToLower(traceCall),
						) {
						continue
					}
					scope, found := semanticDocument.ScopeAt(
						nameNode.Range().Start,
					)
					if !found {
						continue
					}
					className := (resolver.NameContext{
						Namespace: scope.Namespace,
						Imports:   scope.Imports,
					}).ResolveClass(rawName)
					var resolvedArguments []resolver.Argument
					for index, argument := range phpquery.Arguments(creation) {
						expression := lastAuditArgumentNode(argument)
						if expression == nil {
							continue
						}
						fact := semanticDocument.TypeOf(expression)
						resolvedArguments = append(
							resolvedArguments,
							resolver.Argument{
								Name: phpquery.ArgumentName(argument),
								Type: fact.Type,
							},
						)
						t.Logf(
							"constructor %s argument %d name=%q %q type=%s",
							className,
							index,
							phpquery.ArgumentName(argument),
							strings.TrimSpace(expression.Text()),
							fact.Type,
						)
					}
					for _, constructor := range (resolver.MemberResolver{
						Snapshot: phpIndex.SemanticSnapshot(),
					}).Methods(
						types.Named(className),
						"__construct",
					) {
						resolved := resolver.ResolveSignature(
							phpIndex.SemanticSnapshot().Relations(),
							constructor.Symbol,
							resolvedArguments,
						)
						parameterType := types.Unknown()
						if len(constructor.Symbol.Parameters) > 0 {
							parameterType = constructor.Symbol.Parameters[0].Type
						}
						firstAssignable := false
						if len(resolvedArguments) > 0 &&
							len(constructor.Symbol.Parameters) > 0 {
							firstAssignable = phpIndex.
								SemanticSnapshot().
								Relations().
								IsAssignableTo(
									resolvedArguments[0].Type,
									constructor.Symbol.Parameters[0].Type,
								)
						}
						t.Logf(
							"constructor signature %s path=%s parameters=%v first-type=%s first-assignable=%t templates=%v compatible=%t",
							constructor.Symbol.FullyQualified,
							constructor.Symbol.Path,
							constructor.Symbol.Parameters,
							auditTypeShape(parameterType),
							firstAssignable,
							constructor.Symbol.Templates,
							resolved.Compatible,
						)
					}
				}
			}
		}
		relative, err := filepath.Rel(root, path)
		require.NoError(t, err)
		fileHasIncludedDiagnostics := false
		for _, diagnostic := range diagnostics {
			code := string(diagnostic.ID)
			if len(includedCodes) != 0 {
				if _, included := includedCodes[code]; !included {
					continue
				}
			}
			fileHasIncludedDiagnostics = true
			diagnosticCount++
			codeCounts[code]++
			if len(paths) == 1 {
				line, character := document.LineIndex.PositionUTF16(
					diagnostic.Range.Start,
				)
				t.Logf(
					"%s:%d:%d  %s  %s",
					relative,
					line+1,
					character+1,
					diagnostic.ID,
					diagnostic.Message,
				)
			}
			key := code + "\x00" + diagnostic.Message
			group := groups[key]
			if group == nil {
				group = &diagnosticAuditGroup{
					code:    code,
					message: diagnostic.Message,
				}
				groups[key] = group
			}
			group.count++
			if len(group.samples) < 3 {
				line, _ := document.LineIndex.PositionUTF16(
					diagnostic.Range.Start,
				)
				group.samples = append(
					group.samples,
					relative+":"+strconv.Itoa(int(line)+1),
				)
			}
		}
		if fileHasIncludedDiagnostics {
			filesWithDiagnostics++
		}
	}

	ranked := make([]*diagnosticAuditGroup, 0, len(groups))
	for _, group := range groups {
		ranked = append(ranked, group)
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].count != ranked[right].count {
			return ranked[left].count > ranked[right].count
		}
		if ranked[left].code != ranked[right].code {
			return ranked[left].code < ranked[right].code
		}
		return ranked[left].message < ranked[right].message
	})

	t.Logf(
		"PHP diagnostics audit: root=%s files=%d affected=%d diagnostics=%d "+
			"groups=%d elapsed=%s",
		auditRoot,
		len(paths),
		filesWithDiagnostics,
		diagnosticCount,
		len(ranked),
		time.Since(started).Round(time.Millisecond),
	)
	type codeCount struct {
		code  string
		count int
	}
	rankedCodes := make([]codeCount, 0, len(codeCounts))
	for code, count := range codeCounts {
		rankedCodes = append(rankedCodes, codeCount{code: code, count: count})
	}
	sort.Slice(rankedCodes, func(left, right int) bool {
		if rankedCodes[left].count != rankedCodes[right].count {
			return rankedCodes[left].count > rankedCodes[right].count
		}
		return rankedCodes[left].code < rankedCodes[right].code
	})
	codeSummary := make([]string, len(rankedCodes))
	for index, entry := range rankedCodes {
		codeSummary[index] = entry.code + ":" + strconv.Itoa(entry.count)
	}
	t.Logf("diagnostics by code: %s", strings.Join(codeSummary, ", "))
	maxGroups := 50
	if configured := os.Getenv("SHOPWARE_LSP_DIAGNOSTICS_MAX_GROUPS"); configured != "" {
		value, err := strconv.Atoi(configured)
		require.NoError(t, err)
		require.Positive(t, value)
		maxGroups = value
	}
	for index, group := range ranked {
		if index == maxGroups {
			break
		}
		t.Logf(
			"%5d  %-20s  %s  [%s]",
			group.count,
			group.code,
			group.message,
			strings.Join(group.samples, ", "),
		)
	}
}

func lastAuditArgumentNode(argument *phpsyntax.Node) *phpsyntax.Node {
	if argument == nil {
		return nil
	}
	for child := argument.ChildCount() - 1; child >= 0; child-- {
		expression, _ := argument.Child(child).(*phpsyntax.Node)
		if expression != nil {
			return expression
		}
	}
	return nil
}

func firstAuditNode(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for child := 0; child < node.ChildCount(); child++ {
		expression, _ := node.Child(child).(*phpsyntax.Node)
		if expression != nil {
			return expression
		}
	}
	return nil
}

func auditTypeShape(value types.Type) string {
	arguments := value.Arguments()
	if len(arguments) == 0 {
		return value.Kind().String() + "(" + value.Name() + ")"
	}
	parts := make([]string, len(arguments))
	for index, argument := range arguments {
		parts[index] = auditTypeShape(argument)
	}
	return value.Kind().String() + "[" + strings.Join(parts, ",") + "]"
}
