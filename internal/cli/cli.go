package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/pprof"
	"runtime/trace"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
)

type Options struct {
	Version           string
	License           string
	ThirdPartyNotices string
	GCPercent         int
	GCPolicyApplied   bool
}

type Runner struct {
	options Options
	root    string
	json    bool
	verbose bool
	in      io.Reader
	out     io.Writer
	errOut  io.Writer
}

type commandDefinition struct {
	Name    string                                `json:"name"`
	Summary string                                `json:"summary"`
	Usage   string                                `json:"usage"`
	Run     func(context.Context, []string) error `json:"-"`
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func ExitCode(err error) int {
	var target *exitError
	if errors.As(err, &target) {
		return target.code
	}
	if err != nil {
		return 1
	}
	return 0
}

func New(options Options) *Runner {
	if options.Version == "" {
		options.Version = "dev"
	}
	return &Runner{options: options}
}

func (r *Runner) Run(
	ctx context.Context,
	args []string,
	in io.Reader,
	out,
	errOut io.Writer,
) (runErr error) {
	r.in, r.out, r.errOut = in, out, errOut
	log.SetOutput(errOut)
	global := flag.NewFlagSet("shopware-lsp", flag.ContinueOnError)
	global.SetOutput(errOut)
	global.StringVar(&r.root, "root", "", "workspace root (defaults to the current directory)")
	global.BoolVar(&r.json, "json", false, "emit machine-readable JSON where supported")
	global.BoolVar(&r.verbose, "v", false, "verbose progress output")
	// vscode-languageclient and several other LSP clients append these
	// conventional process flags for stdio servers. The transport is already
	// implied when no remote listener is configured, but accepting the flags
	// keeps the CLI compatible with those launchers.
	global.Bool("stdio", false, "use standard input/output transport")
	global.Int("clientProcessId", 0, "parent language-client process ID")
	var showHelp bool
	global.BoolVar(&showHelp, "h", false, "show help")
	global.BoolVar(&showHelp, "help", false, "show help")
	var cpuProfile, memoryProfile, traceProfile string
	global.StringVar(&cpuProfile, "profile.cpu", "", "write a CPU profile to this file")
	global.StringVar(&memoryProfile, "profile.mem", "", "write a heap profile to this file")
	global.StringVar(&traceProfile, "profile.trace", "", "write a runtime trace to this file")
	if err := global.Parse(args); err != nil {
		return &exitError{code: 2, err: err}
	}
	stopProfiles, err := startProfiles(cpuProfile, traceProfile, memoryProfile)
	if err != nil {
		return err
	}
	defer func() {
		if profileErr := stopProfiles(); profileErr != nil {
			_, _ = fmt.Fprintf(errOut, "profile: %v\n", profileErr)
			runErr = errors.Join(runErr, fmt.Errorf("write profile: %w", profileErr))
		}
	}()

	rest := global.Args()
	name := "serve"
	if len(rest) > 0 {
		name = rest[0]
		rest = rest[1:]
	}
	if showHelp {
		name = "help"
		rest = nil
	}
	if name == "-h" || name == "--help" {
		name = "help"
	}
	commands := r.commands()
	command, found := commands[name]
	if !found {
		return &exitError{
			code: 2,
			err:  fmt.Errorf("unknown command %q; run 'shopware-lsp help'", name),
		}
	}
	if name != "serve" && !r.verbose {
		log.SetOutput(io.Discard)
	}
	if err := command.Run(ctx, rest); err != nil {
		var target *exitError
		if errors.As(err, &target) {
			return err
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (r *Runner) commands() map[string]commandDefinition {
	definitions := []commandDefinition{
		{"serve", "run the Shopware language server", "serve [server-flags]", r.runServe},
		{"version", "print version information", "version", r.runVersion},
		{"help", "print command usage", "help [command]", r.runHelp},
		{"api-json", "print the supported CLI and language API", "api-json", r.runAPIJSON},
		{"licenses", "print license and third-party notices", "licenses", r.runLicenses},
		{"config", "validate and print the effective project configuration", "config", r.runConfig},
		{"index", "build or refresh the workspace index", "index [-force]", r.runIndex},
		{"stats", "print indexing, workspace, cache, and memory statistics", "stats", r.runStats},
		{"check", "show diagnostics for files and directories", "check [-severity level] [-fail-on level] [-workers count] <file-or-directory>...", r.runCheck},
		{"completion", "show completion items at a position", "completion <file[:line[:column]]>", r.runCompletion},
		{"definition", "show definitions at a position", "definition <file[:line[:column]]>", r.runDefinition},
		{"implementation", "show implementations at a position", "implementation <file[:line[:column]]>", r.runImplementation},
		{"references", "show references at a position", "references <file[:line[:column]]>", r.runReferences},
		{"hover", "show hover information at a position", "hover <file[:line[:column]]>", r.runHover},
		{"signature", "show signature help at a position", "signature <file[:line[:column]]>", r.runSignature},
		{"call-hierarchy", "show incoming and outgoing calls", "call-hierarchy <file[:line[:column]]>", r.runCallHierarchy},
		{"type-hierarchy", "show supertypes and subtypes", "type-hierarchy <file[:line[:column]]>", r.runTypeHierarchy},
		{"symbols", "show a document outline", "symbols <file>", r.runSymbols},
		{"workspace-symbol", "search workspace symbols", "workspace-symbol [--fresh] <query>", r.runWorkspaceSymbols},
		{"highlights", "show document highlights at a position", "highlights <file[:line[:column]]>", r.runHighlights},
		{"folding-ranges", "show folding ranges for a document", "folding-ranges <file>", r.runFoldingRanges},
		{"links", "show document links", "links <file>", r.runLinks},
		{"semtok", "show semantic tokens", "semtok <file>", r.runSemanticTokens},
		{"inlay-hints", "show inlay hints", "inlay-hints <file>", r.runInlayHints},
		{"codelens", "show code lenses", "codelens <file>", r.runCodeLens},
		{"selection-ranges", "show expandable selection ranges", "selection-ranges <file[:line[:column]]>", r.runSelectionRanges},
		{"linked-editing", "show linked editing ranges", "linked-editing <file[:line[:column]]>", r.runLinkedEditing},
		{"colors", "show document colors", "colors <file>", r.runColors},
		{"codeaction", "list or apply code actions", "codeaction [-kind kind] [-title regexp] [-exec] [-w|-d] <position>", r.runCodeAction},
		{"rename", "preview or apply a rename", "rename [-w|-d] <position> <new-name>", r.runRename},
		{"execute", "execute a Shopware custom command", "execute <method> [json-arguments]", r.runExecute},
		{"request", "send an arbitrary LSP request", "request <method> [json-params]", r.runRequest},
	}
	result := make(map[string]commandDefinition, len(definitions))
	for _, definition := range definitions {
		result[definition.Name] = definition
	}
	return result
}

func (r *Runner) runConfig(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return usageError("config takes no arguments")
	}
	session, err := r.connectWithoutIndex(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	if r.json {
		return writeJSON(r.out, session.configuration)
	}
	return writeJSON(r.out, map[string]interface{}{
		"path":      session.configuration.Path,
		"effective": session.configuration.Effective,
	})
}

func (r *Runner) runHelp(_ context.Context, args []string) error {
	commands := r.commands()
	if len(args) > 0 {
		command, found := commands[args[0]]
		if !found {
			return &exitError{code: 2, err: fmt.Errorf("unknown help topic %q", args[0])}
		}
		return writeFormatted(
			r.out, "%s\n\nUsage:\n  shopware-lsp [global-flags] %s\n",
			command.Summary, command.Usage,
		)
	}
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := []string{
		"Shopware LSP provides editor features and command-line project analysis.",
		"",
		"Usage:",
		"  shopware-lsp [global-flags] [command] [command-flags]",
		"",
		"Commands:",
	}
	for _, name := range names {
		command := commands[name]
		lines = append(lines, fmt.Sprintf("  %-18s %s", name, command.Summary))
	}
	lines = append(lines,
		"",
		"Global flags:",
		"  -root PATH          workspace root (default: current directory)",
		"  -json               machine-readable output",
		"  -v                  verbose progress output",
		"  -profile.cpu FILE   write CPU profile",
		"  -profile.mem FILE   write heap profile",
		"  -profile.trace FILE write runtime trace",
		"",
		"With no command, the stdio language server is started.",
	)
	return writeFormatted(r.out, "%s\n", strings.Join(lines, "\n"))
}

func (r *Runner) runVersion(_ context.Context, args []string) error {
	if len(args) != 0 {
		return usageError("version takes no arguments")
	}
	if r.json {
		return writeJSON(r.out, map[string]string{
			"name": "shopware-lsp", "version": r.options.Version,
		})
	}
	return writeFormatted(r.out, "shopware-lsp %s\n", r.options.Version)
}

func (r *Runner) runAPIJSON(_ context.Context, args []string) error {
	if len(args) != 0 {
		return usageError("api-json takes no arguments")
	}
	commands := r.commands()
	items := make([]commandDefinition, 0, len(commands))
	for _, command := range commands {
		items = append(items, command)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	languages := make(map[language.ID][]string)
	registry := language.DefaultRegistry()
	for _, extension := range registry.Extensions() {
		definition, _ := registry.ByExtension(extension)
		languages[definition.ID] = append(languages[definition.ID], extension)
	}
	lspMethods := []string{
		"callHierarchy/incomingCalls", "callHierarchy/outgoingCalls",
		"codeAction/resolve", "codeLens/resolve", "textDocument/codeAction",
		"textDocument/codeLens", "textDocument/colorPresentation",
		"textDocument/completion", "textDocument/definition",
		"textDocument/diagnostic", "textDocument/documentColor",
		"textDocument/documentHighlight", "textDocument/documentLink",
		"textDocument/documentSymbol", "textDocument/foldingRange",
		"textDocument/hover", "textDocument/implementation",
		"textDocument/inlayHint", "textDocument/linkedEditingRange",
		"textDocument/prepareCallHierarchy", "textDocument/prepareTypeHierarchy",
		"textDocument/references", "textDocument/rename",
		"textDocument/selectionRange", "textDocument/semanticTokens/full",
		"textDocument/signatureHelp", "typeHierarchy/subtypes",
		"typeHierarchy/supertypes", "workspace/symbol",
		"workspace/willRenameFiles",
		"shopware/configuration/catalog", "shopware/configuration/effective",
		"shopware/configuration/reload", "workspace/didChangeConfiguration",
	}
	return writeJSON(r.out, map[string]interface{}{
		"name":                   "shopware-lsp",
		"version":                r.options.Version,
		"commands":               items,
		"languages":              languages,
		"lspMethods":             lspMethods,
		"customCommandDiscovery": "shopware-lsp execute",
	})
}

func (r *Runner) runLicenses(_ context.Context, args []string) error {
	if len(args) != 0 {
		return usageError("licenses takes no arguments")
	}
	output := strings.TrimSpace(r.options.License) + "\n"
	if strings.TrimSpace(r.options.ThirdPartyNotices) != "" {
		output += "\n--- Third-party notices ---\n\n"
		output += strings.TrimSpace(r.options.ThirdPartyNotices) + "\n"
	}
	return writeFormatted(r.out, "%s", output)
}

func usageError(message string) error {
	return &exitError{code: 2, err: errors.New(message)}
}

func writeJSON(writer io.Writer, value interface{}) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeFormatted(writer io.Writer, format string, args ...interface{}) error {
	_, err := fmt.Fprintf(writer, format, args...)
	return err
}

type errorCloser interface {
	Close() error
}

func closeIgnoringError(closer errorCloser) {
	_ = closer.Close()
}

func startProfiles(cpuPath, tracePath, memoryPath string) (func() error, error) {
	var cpuFile, traceFile *os.File
	if cpuPath != "" {
		file, err := os.Create(cpuPath)
		if err != nil {
			return nil, fmt.Errorf("create CPU profile: %w", err)
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("start CPU profile: %w", err)
		}
		cpuFile = file
	}
	if tracePath != "" {
		file, err := os.Create(tracePath)
		if err != nil {
			if cpuFile != nil {
				pprof.StopCPUProfile()
				_ = cpuFile.Close()
			}
			return nil, fmt.Errorf("create trace: %w", err)
		}
		if err := trace.Start(file); err != nil {
			_ = file.Close()
			if cpuFile != nil {
				pprof.StopCPUProfile()
				_ = cpuFile.Close()
			}
			return nil, fmt.Errorf("start trace: %w", err)
		}
		traceFile = file
	}
	return func() error {
		var result error
		if traceFile != nil {
			trace.Stop()
			result = errors.Join(result, traceFile.Close())
		}
		if cpuFile != nil {
			pprof.StopCPUProfile()
			result = errors.Join(result, cpuFile.Close())
		}
		if memoryPath != "" {
			file, err := os.Create(memoryPath)
			if err != nil {
				return errors.Join(result, err)
			}
			result = errors.Join(result, pprof.WriteHeapProfile(file), file.Close())
		}
		return result
	}, nil
}
