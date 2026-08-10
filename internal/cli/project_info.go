package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/projectdetect"
)

func (r *Runner) runProjectInfo(_ context.Context, args []string) error {
	if len(args) != 0 {
		return usageError("project-info takes no arguments")
	}
	root, err := r.workspaceRoot()
	if err != nil {
		return err
	}
	result, err := projectdetect.Detect(root)
	if err != nil {
		return err
	}
	if r.json {
		return writeJSON(r.out, result)
	}
	lines := []string{fmt.Sprintf("Project type: %s", result.Kind)}
	if len(result.Evidence) == 0 {
		lines = append(lines, "Supported: no", "Evidence: none")
	} else {
		lines = append(lines, "Supported: yes", "Evidence:")
		for _, evidence := range result.Evidence {
			lines = append(lines, fmt.Sprintf("  %s: %s", evidence.Path, evidence.Reason))
		}
	}
	return writeFormatted(r.out, "%s\n", strings.Join(lines, "\n"))
}

func (r *Runner) requireSupportedProject(root string) error {
	if r.allowUnsupportedProject {
		return nil
	}
	result, err := projectdetect.Detect(root)
	if err != nil {
		return fmt.Errorf("detect project type: %w", err)
	}
	if result.Supported {
		return nil
	}
	return fmt.Errorf(
		"unsupported project root %s: no Shopware or Symfony project markers found; add .config/shopware-lsp/config.json or pass -allow-unsupported-project",
		root,
	)
}
