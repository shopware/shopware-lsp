//go:build integration

package twigmigration

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/twig/parser"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/stretchr/testify/require"
)

// TestAdministrationTwigMigrationsOnTrunk dry-runs every currently applicable
// fix against a real Shopware checkout. It never writes to the checkout.
func TestAdministrationTwigMigrationsOnTrunk(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("real-world Shopware checkout is unavailable: %s", root)
	}

	type totals struct{ reported, fixed, manual int }
	counts := make(map[string]totals)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".twig" || !containsAdministrationResources(path) {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		source := string(content)
		parsed := parser.Parse(source)
		for _, node := range twigquery.Nodes(parsed.Tree.Root, twigsyntax.HtmlTag) {
			starts := twigquery.Nodes(node, twigsyntax.HtmlStartingTag)
			if len(starts) == 0 {
				continue
			}
			rule, found := RuleForTag(twigquery.HTMLTagName(starts[0]))
			if !found {
				continue
			}
			total := counts[rule.ID]
			total.reported++
			edits, compileErr := Compile(source, node, rule)
			if errors.Is(compileErr, ErrUnsafe) {
				total.manual++
				counts[rule.ID] = total
				continue
			}
			require.NoErrorf(t, compileErr, "%s in %s", rule.ID, path)
			updated, applyErr := rewrite.Apply(source, edits)
			require.NoErrorf(t, applyErr, "%s in %s", rule.ID, path)
			updatedParse := parser.Parse(updated)
			require.LessOrEqualf(
				t, len(updatedParse.Errors), len(parsed.Errors),
				"%s introduced a Twig parse error in %s", rule.ID, path,
			)
			require.Lessf(
				t,
				countTag(updatedParse.Tree.Root, rule.SourceTag),
				countTag(parsed.Tree.Root, rule.SourceTag),
				"%s did not remove its source tag in %s", rule.ID, path,
			)
			total.fixed++
			counts[rule.ID] = total
		}
		return nil
	})
	require.NoError(t, err)

	all := totals{}
	for _, rule := range Rules() {
		total := counts[rule.ID]
		all.reported += total.reported
		all.fixed += total.fixed
		all.manual += total.manual
		if total.reported != 0 {
			t.Logf("%s: reported=%d fixed=%d manual=%d", rule.ID, total.reported, total.fixed, total.manual)
		}
	}
	require.Greater(t, all.reported, 0, "trunk should contain at least one legacy Administration component")
	require.Equal(t, all.reported, all.fixed+all.manual)
	t.Logf("total: reported=%d fixed=%d manual=%d", all.reported, all.fixed, all.manual)
}

func containsAdministrationResources(path string) bool {
	return filepath.ToSlash(path) != "" &&
		contains(filepath.ToSlash(path), "/Resources/app/administration/")
}

func contains(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

func countTag(root *twigsyntax.Node, name string) int {
	count := 0
	for _, start := range twigquery.Nodes(root, twigsyntax.HtmlStartingTag) {
		if twigquery.HTMLTagName(start) == name {
			count++
		}
	}
	return count
}
