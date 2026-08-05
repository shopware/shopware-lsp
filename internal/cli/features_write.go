package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (r *Runner) runCodeAction(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("codeaction", flag.ContinueOnError)
	flags.SetOutput(r.errOut)
	kind := flags.String("kind", "", "filter by hierarchical code-action kind")
	titlePattern := flags.String("title", "", "regular expression matched against action titles")
	execute := flags.Bool("exec", false, "execute the first matching action")
	write := flags.Bool("w", false, "write resulting edits")
	diff := flags.Bool("d", false, "print resulting edits as a unified diff")
	if err := flags.Parse(args); err != nil {
		return usageError(err.Error())
	}
	value, err := requireOneArgument(flags.Args(), "codeaction expects one file position")
	if err != nil {
		return err
	}
	var titleMatcher *regexp.Regexp
	if *titlePattern != "" {
		titleMatcher, err = regexp.Compile(*titlePattern)
		if err != nil {
			return fmt.Errorf("compile title expression: %w", err)
		}
	}
	target, err := parsePositionTarget(value)
	if err != nil {
		return err
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	document, err := session.openDocument(ctx, target.Path)
	if err != nil {
		return err
	}
	var diagnostics protocol.DiagnosticResult
	if err := session.call(
		ctx, "textDocument/diagnostic", textDocumentParams(document.URI), &diagnostics,
	); err != nil {
		return err
	}
	params := positionParams(document.URI, target.Position)
	params["range"] = protocol.Range{Start: target.Position, End: target.Position}
	contextParams := map[string]interface{}{"diagnostics": diagnostics.Items}
	if *kind != "" {
		contextParams["only"] = []string{*kind}
	}
	params["context"] = contextParams
	var actions []protocol.CodeAction
	if err := session.call(ctx, "textDocument/codeAction", params, &actions); err != nil {
		return err
	}
	filtered := actions[:0]
	for _, action := range actions {
		if *kind != "" && !matchesCodeActionKind(string(action.Kind), *kind) {
			continue
		}
		if titleMatcher != nil && !titleMatcher.MatchString(action.Title) {
			continue
		}
		filtered = append(filtered, action)
	}
	if !*execute {
		return writeJSON(r.out, filtered)
	}
	if len(filtered) == 0 {
		return fmt.Errorf("no matching code action")
	}
	action := filtered[0]
	if action.Disabled != nil {
		return fmt.Errorf("code action %q is disabled: %s", action.Title, action.Disabled.Reason)
	}
	if action.Edit == nil && action.Data != nil {
		var resolved protocol.CodeAction
		if err := session.call(ctx, "codeAction/resolve", action, &resolved); err != nil {
			return err
		}
		action = resolved
		if action.Disabled != nil {
			return fmt.Errorf("code action %q is disabled: %s", action.Title, action.Disabled.Reason)
		}
	}
	if action.Edit != nil {
		return applyWorkspaceEdit(r.out, action.Edit, editMode{Write: *write, Diff: *diff})
	}
	if action.Command != nil {
		return fmt.Errorf(
			"code action %q requires editor command %q; use the corresponding CLI execute command",
			action.Title, action.Command.Command,
		)
	}
	return fmt.Errorf("code action %q returned neither edits nor a command", action.Title)
}

func matchesCodeActionKind(actual, requested string) bool {
	return actual == requested || strings.HasPrefix(actual, requested+".")
}

func (r *Runner) runRename(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("rename", flag.ContinueOnError)
	flags.SetOutput(r.errOut)
	write := flags.Bool("w", false, "write resulting edits")
	diff := flags.Bool("d", false, "print resulting edits as a unified diff")
	if err := flags.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if flags.NArg() != 2 {
		return usageError("rename expects a file position and a new name")
	}
	target, err := parsePositionTarget(flags.Arg(0))
	if err != nil {
		return err
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	document, err := session.openDocument(ctx, target.Path)
	if err != nil {
		return err
	}
	params := positionParams(document.URI, target.Position)
	params["newName"] = flags.Arg(1)
	var edit *protocol.WorkspaceEdit
	if err := session.call(ctx, "textDocument/rename", params, &edit); err != nil {
		return err
	}
	if edit == nil {
		return fmt.Errorf("the selected symbol cannot be renamed")
	}
	return applyWorkspaceEdit(r.out, edit, editMode{Write: *write, Diff: *diff})
}

func (r *Runner) runExecute(ctx context.Context, args []string) error {
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	if len(args) == 0 {
		var commands []string
		if err := session.call(ctx, "shopware/commands", struct{}{}, &commands); err != nil {
			return err
		}
		return writeJSON(r.out, commands)
	}
	if len(args) > 2 {
		return usageError("execute expects a method and one optional JSON argument")
	}
	params, err := r.parseJSONArgument(args[1:])
	if err != nil {
		return err
	}
	var result interface{}
	if err := session.call(ctx, args[0], params, &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runRequest(ctx context.Context, args []string) error {
	if len(args) == 0 || len(args) > 2 {
		return usageError("request expects a method and one optional JSON parameter")
	}
	params, err := r.parseJSONArgument(args[1:])
	if err != nil {
		return err
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	var result interface{}
	if err := session.call(ctx, args[0], params, &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) parseJSONArgument(args []string) (interface{}, error) {
	if len(args) == 0 {
		return map[string]interface{}{}, nil
	}
	content := []byte(args[0])
	if args[0] == "-" {
		var err error
		content, err = io.ReadAll(r.in)
		if err != nil {
			return nil, fmt.Errorf("read JSON argument: %w", err)
		}
	} else if strings.HasPrefix(args[0], "@") {
		var err error
		content, err = os.ReadFile(strings.TrimPrefix(args[0], "@"))
		if err != nil {
			return nil, fmt.Errorf("read JSON argument file: %w", err)
		}
	}
	var result interface{}
	if err := json.Unmarshal(content, &result); err != nil {
		return nil, fmt.Errorf("parse JSON argument: %w", err)
	}
	return result, nil
}
