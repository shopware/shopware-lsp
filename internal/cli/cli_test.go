package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/app"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestCoreCommandsDoNotStartAWorkspace(t *testing.T) {
	runner := New(Options{
		Version: "1.2.3", License: "project license",
		ThirdPartyNotices: "dependency notice",
	})
	for _, test := range []struct {
		name     string
		args     []string
		contains string
	}{
		{"help", []string{"help"}, "workspace-symbol"},
		{"short-help", []string{"-h"}, "Usage:"},
		{"version", []string{"version"}, "shopware-lsp 1.2.3"},
		{"stdio-version", []string{"--stdio", "version"}, "shopware-lsp 1.2.3"},
		{"licenses", []string{"licenses"}, "dependency notice"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output, errors bytes.Buffer
			require.NoError(t, runner.Run(
				context.Background(), test.args, strings.NewReader(""),
				&output, &errors,
			))
			require.Contains(t, output.String(), test.contains)
		})
	}
}

func TestDefaultCommandStartsStdioServer(t *testing.T) {
	runner := New(Options{Version: "test"})
	var output, errors bytes.Buffer
	require.NoError(t, runner.Run(
		context.Background(), nil, strings.NewReader(""), &output, &errors,
	))
	require.Contains(t, errors.String(), "Shopware LSP version: test")
}

func TestExplicitServeAcceptsEditorTransportFlags(t *testing.T) {
	runner := New(Options{Version: "test"})
	var output, errors bytes.Buffer
	require.NoError(t, runner.Run(
		context.Background(),
		[]string{"serve", "--stdio", "--clientProcessId", "42"},
		strings.NewReader(""), &output, &errors,
	))
	require.NotContains(t, errors.String(), "flag provided but not defined")
}

func TestRemoteServeStopsWhileWaitingForAClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := New(Options{Version: "test"})
	err := runner.Run(
		ctx, []string{"serve", "-listen", "127.0.0.1:0"},
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestProfileWriteFailureIsReturned(t *testing.T) {
	runner := New(Options{Version: "test"})
	err := runner.Run(
		context.Background(), []string{"-profile.mem", t.TempDir(), "help"},
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
	)
	require.ErrorContains(t, err, "write profile")
}

func TestAPIJSONDescribesCommandsAndLanguages(t *testing.T) {
	runner := New(Options{Version: "test"})
	var output, errors bytes.Buffer
	require.NoError(t, runner.Run(
		context.Background(), []string{"-json", "api-json"},
		strings.NewReader(""), &output, &errors,
	))
	var result struct {
		Commands  []commandDefinition `json:"commands"`
		Languages map[string][]string `json:"languages"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.GreaterOrEqual(t, len(result.Commands), 30)
	require.Contains(t, commandNames(result.Commands), "mcp")
	require.Contains(t, commandNames(result.Commands), "project-info")
	require.Contains(t, result.Languages["php"], ".php")
}

func TestProjectInfoReportsUnknownWithoutCreatingWorkspaceCache(t *testing.T) {
	root := t.TempDir()
	cacheRoot := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", cacheRoot)
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "Example.php"), []byte("<?php\n"), 0o644,
	))
	runner := New(Options{Version: "test"})
	var output, errors bytes.Buffer
	require.NoError(t, runner.Run(
		context.Background(), []string{"-root", root, "-json", "project-info"},
		strings.NewReader(""), &output, &errors,
	), errors.String())
	var result struct {
		Supported bool   `json:"supported"`
		Kind      string `json:"kind"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.False(t, result.Supported)
	require.Equal(t, "unknown", result.Kind)
	entries, err := os.ReadDir(cacheRoot)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestWorkspaceCommandRejectsUnknownProjectUnlessExplicitlyAllowed(t *testing.T) {
	root := t.TempDir()
	cacheRoot := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", cacheRoot)
	path := filepath.Join(root, "Example.php")
	require.NoError(t, os.WriteFile(path, []byte("<?php\n"), 0o644))

	run := func(extra ...string) error {
		args := append([]string{"-root", root, "-json"}, extra...)
		return New(Options{Version: "test"}).Run(
			context.Background(), args, strings.NewReader(""),
			&bytes.Buffer{}, &bytes.Buffer{},
		)
	}
	require.ErrorContains(t, run("check", path), "unsupported project root")
	entries, err := os.ReadDir(cacheRoot)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoError(t, run("-allow-unsupported-project", "check", path))
}

func commandNames(commands []commandDefinition) []string {
	result := make([]string, 0, len(commands))
	for _, command := range commands {
		result = append(result, command.Name)
	}
	return result
}

func TestParsePositionTargetUsesOneBasedCLIPositions(t *testing.T) {
	target, err := parsePositionTarget("/project/Test.php:12:7")
	require.NoError(t, err)
	require.Equal(t, "/project/Test.php", target.Path)
	require.Equal(t, protocol.Position{Line: 11, Character: 6}, target.Position)

	target, err = parsePositionTarget("/project/Test.php")
	require.NoError(t, err)
	require.Equal(t, protocol.Position{}, target.Position)
}

func TestApplyProtocolTextEditsUsesUTF16Positions(t *testing.T) {
	source := "a😀b\n"
	result, err := applyProtocolTextEdits(source, []protocol.TextEdit{{
		Range: protocol.Range{
			Start: protocol.Position{Character: 1},
			End:   protocol.Position{Character: 3},
		},
		NewText: "X",
	}})
	require.NoError(t, err)
	require.Equal(t, "aXb\n", result)
}

func TestApplyWorkspaceEditPreviewsAndWritesChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "test.php")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644))
	edit := &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
		uriutil.FileURI(path): {{
			Range: protocol.Range{
				Start: protocol.Position{},
				End:   protocol.Position{Character: 3},
			},
			NewText: "new",
		}},
	}}
	var preview bytes.Buffer
	require.NoError(t, applyWorkspaceEdit(
		&preview, edit, editMode{Diff: true},
	))
	require.Contains(t, preview.String(), "-old")
	require.Contains(t, preview.String(), "+new")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "old\n", string(content))

	require.NoError(t, applyWorkspaceEdit(
		&bytes.Buffer{}, edit, editMode{Write: true},
	))
	content, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new\n", string(content))
}

func TestApplyWorkspaceEditRejectsCreateOverExistingEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.php")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	edit := &protocol.WorkspaceEdit{DocumentChanges: []protocol.DocumentChange{{
		Kind: protocol.CreateFileOperation,
		URI:  uriutil.FileURI(path),
	}}}

	err := applyWorkspaceEdit(&bytes.Buffer{}, edit, editMode{Write: true})
	require.ErrorContains(t, err, "create target already exists")
}

func TestResolveCheckFilesExpandsDirectoriesAndDeduplicatesTargets(t *testing.T) {
	root := t.TempDir()
	sourcePHP := filepath.Join(root, "src", "Example.php")
	sourceYAML := filepath.Join(root, "config", "services.yaml")
	testPHP := filepath.Join(root, "tests", "ExampleTest.php")
	skippedPHP := filepath.Join(root, "node_modules", "package", "Generated.php")
	temporaryJS := filepath.Join(root, "Resources", "app", "administration", ".tmp", "vite", "generated.js")
	unsupported := filepath.Join(root, "README.md")
	for _, path := range []string{
		sourcePHP, sourceYAML, testPHP, skippedPHP, temporaryJS, unsupported,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("fixture"), 0o644))
	}

	files, err := resolveCheckFiles(context.Background(), []string{
		root,
		sourcePHP,
		filepath.Join(root, "tests"),
	})
	require.NoError(t, err)
	require.Equal(t, []string{sourceYAML, sourcePHP, testPHP}, files)

	_, err = resolveCheckFiles(
		context.Background(),
		[]string{filepath.Join(root, "missing")},
	)
	require.ErrorContains(t, err, "inspect check target")
}

func TestCheckEmptyDirectoryReturnsAnEmptyJSONArray(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "README.md"),
		[]byte("unsupported"),
		0o644,
	))
	var output, errors bytes.Buffer
	runner := New(Options{Version: "test"})
	require.NoError(t, runner.Run(
		context.Background(),
		[]string{"-allow-unsupported-project", "-json", "check", root},
		strings.NewReader(""),
		&output,
		&errors,
	), errors.String())
	require.JSONEq(t, "[]", output.String())
}

func TestCheckRejectsInvalidWorkerCount(t *testing.T) {
	runner := New(Options{Version: "test"})
	err := runner.Run(
		context.Background(),
		[]string{"check", "-workers", "0", t.TempDir()},
		strings.NewReader(""),
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	require.ErrorContains(t, err, "workers must be at least 1")
}

func TestConfigCommandPrintsEffectiveProjectConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	configPath := filepath.Join(root, ".config", "shopware", "lsp.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(configPath, []byte(`{
        "version": 1,
        "features": {"hover": false},
        "check": {"severity": "information", "failOn": "error"}
    }`), 0o644))
	var output, errors bytes.Buffer
	runner := New(Options{Version: "test"})
	require.NoError(t, runner.Run(
		context.Background(), []string{"-root", root, "-json", "config"},
		strings.NewReader(""), &output, &errors,
	), errors.String())
	var result struct {
		Effective struct {
			Features map[string]bool `json:"features"`
			Check    struct {
				Severity string `json:"severity"`
				FailOn   string `json:"failOn"`
			} `json:"check"`
		} `json:"effective"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.False(t, result.Effective.Features["hover"])
	require.Equal(t, "information", result.Effective.Check.Severity)
	require.Equal(t, "error", result.Effective.Check.FailOn)
}

func TestCheckUsesProjectFailurePolicyAndExplicitFlagWins(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	phpPath := filepath.Join(root, "Broken.php")
	require.NoError(t, os.WriteFile(phpPath, []byte("<?php function (\n"), 0o644))
	configPath := filepath.Join(root, ".config", "shopware", "lsp.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(configPath, []byte(`{
        "version": 1,
        "check": {"severity": "hint", "failOn": "error"}
    }`), 0o644))
	run := func(extra ...string) error {
		args := []string{"-root", root, "check"}
		args = append(args, extra...)
		args = append(args, phpPath)
		return New(Options{Version: "test"}).Run(
			context.Background(), args, strings.NewReader(""),
			&bytes.Buffer{}, &bytes.Buffer{},
		)
	}
	require.ErrorContains(t, run(), "fail-on error")
	require.NoError(t, run("-fail-on", "off"))
}

func TestCLIRejectsInvalidProjectConfigurationBeforeChecking(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	configPath := filepath.Join(root, ".config", "shopware", "lsp.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(
		configPath,
		[]byte(`{"version":1,"diagnostics":{"rules":{"does.not.exist":"off"}}}`),
		0o644,
	))
	err := New(Options{Version: "test"}).Run(
		context.Background(), []string{"-root", root, "check", root},
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
	)
	require.ErrorContains(t, err, "unknown diagnostic rule")
}

func TestCLIRejectsInvalidNestedExtensionConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	configPath := filepath.Join(
		root, "custom", "plugins", "Example", ".config", "shopware", "lsp.json",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(
		configPath,
		[]byte(`{"version":1,"domains":{"php":false}}`),
		0o644,
	))
	err := New(Options{Version: "test"}).Run(
		context.Background(), []string{"-root", root, "-allow-unsupported-project", "check", root},
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
	)
	require.ErrorContains(t, err, configPath)
	require.ErrorContains(t, err, "nested configuration may only contain diagnostics")
}

func TestWorkspaceCLIIndexCheckAndExecuteUseProductionLSP(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	phpPath := filepath.Join(root, "src", "Controller", "FixtureController.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o755))
	require.NoError(t, os.WriteFile(phpPath, []byte(`<?php
namespace App\Controller;
final class FixtureController
{
    public function value(): string { return 'ok'; }
}
`), 0o644))
	for _, name := range []string{"A.php", "B.php", "C.php", "D.php"} {
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "src", name),
			[]byte("<?php\n"),
			0o644,
		))
	}

	run := func(args ...string) []byte {
		t.Helper()
		var output, errors bytes.Buffer
		runner := New(Options{Version: "test"})
		commandArgs := append([]string{
			"-root", root, "-allow-unsupported-project", "-json",
		}, args...)
		require.NoError(t, runner.Run(
			context.Background(), commandArgs, strings.NewReader(""),
			&output, &errors,
		), errors.String())
		return output.Bytes()
	}

	var indexed struct {
		Index struct {
			TrackedFiles int `json:"trackedFiles"`
		} `json:"index"`
	}
	require.NoError(t, json.Unmarshal(run("index"), &indexed))
	require.Equal(t, 5, indexed.Index.TrackedFiles)

	var diagnostics []diagnosticOutput
	require.NoError(t, json.Unmarshal(
		run("check", "-workers", "4", filepath.Join(root, "src")),
		&diagnostics,
	))

	var commands []string
	require.NoError(t, json.Unmarshal(run("execute"), &commands))
	require.Contains(t, commands, "shopware/symfony/analytics/routes")

	var tokens []decodedSemanticToken
	require.NoError(t, json.Unmarshal(run("semtok", phpPath), &tokens))
	require.NotNil(t, tokens)

	var symbols []protocol.SymbolInformation
	require.NoError(t, json.Unmarshal(run("workspace-symbol", "FixtureController"), &symbols))
	require.NotEmpty(t, symbols)

	position := phpPath + ":3:13"
	for _, request := range [][]string{
		{"codeaction", position},
		{"completion", position},
		{"definition", position},
		{"implementation", position},
		{"references", position},
		{"hover", position},
		{"signature", phpPath + ":5:37"},
		{"call-hierarchy", phpPath + ":5:21"},
		{"type-hierarchy", position},
		{"highlights", position},
		{"folding-ranges", phpPath},
		{"links", phpPath},
		{"inlay-hints", phpPath},
		{"codelens", phpPath},
		{"selection-ranges", position},
		{"linked-editing", position},
		{"colors", phpPath},
		{"symbols", phpPath},
	} {
		output := run(request...)
		require.True(t, json.Valid(output), "%s returned %s", request[0], output)
	}

	var stats map[string]interface{}
	require.NoError(t, json.Unmarshal(run("stats"), &stats))
	require.Equal(t, root, stats["root"])

	var scanner indexer.FileScannerStats
	require.NoError(t, json.Unmarshal(run(
		"request", "shopware/index/stats",
	), &scanner))
	require.Equal(t, 5, scanner.TrackedFiles)

	var extensions []interface{}
	require.NoError(t, json.Unmarshal(run(
		"execute", "shopware/extension/all",
	), &extensions))

	var forced struct {
		Forced bool `json:"forced"`
	}
	require.NoError(t, json.Unmarshal(run("index", "-force"), &forced))
	require.True(t, forced.Forced)

	renamePreview := run("rename", position, "RenamedController")
	require.Contains(t, string(renamePreview), "RenamedController")
	content, err := os.ReadFile(phpPath)
	require.NoError(t, err)
	require.Contains(t, string(content), "FixtureController")
	run("rename", "-w", position, "RenamedController")
	content, err = os.ReadFile(phpPath)
	require.NoError(t, err)
	require.Contains(t, string(content), "RenamedController")
}

func TestWorkspaceSymbolUsesPopulatedCatalogWithoutSession(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	cacheDir, err := app.ProjectCacheFolder(root)
	require.NoError(t, err)
	_, err = indexer.CheckAndMigrateCache(cacheDir)
	require.NoError(t, err)
	store, err := indexer.NewStore(filepath.Join(cacheDir, "indexes.db"))
	require.NoError(t, err)
	catalog, err := indexer.NewWorkspaceSymbolCatalog(store)
	require.NoError(t, err)
	require.NoError(t, catalog.ReplaceFiles(ctx, []indexer.WorkspaceSymbolDocument{{
		Path: filepath.Join(root, "Fixture.php"),
		Symbols: []indexer.WorkspaceSymbol{{
			Name:     "CachedFixture",
			Kind:     indexer.WorkspaceSymbolClass,
			Priority: indexer.WorkspaceSymbolPriorityPHPType,
		}},
	}}))
	require.NoError(t, catalog.SetReady(ctx, true))
	require.NoError(t, store.Close())

	runner := New(Options{Version: "test"})
	runner.root = root
	runner.allowUnsupportedProject = true
	result, available, err := runner.cachedWorkspaceSymbols(ctx, "CachedFixture")
	require.NoError(t, err)
	require.True(t, available)
	require.Len(t, result, 1)
	require.Equal(t, "CachedFixture", result[0].Name)
}
