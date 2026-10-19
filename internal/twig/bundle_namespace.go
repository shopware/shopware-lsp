package twig

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

// bundleTemplateRoot is one bundle class resolved to the directory Symfony
// derives its Twig loader root from: the directory containing the bundle
// class file (Bundle::getPath()). The Twig namespace is the bundle class
// short name with a trailing "Bundle" suffix dropped, which is how Symfony
// registers Resources/views and templates paths (@WebProfiler for
// WebProfilerBundle, @FOSUser for FOSUserBundle, @Administration for
// Shopware's Administration bundle class).
type bundleTemplateRoot struct {
	Namespace string
	ClassName string
	Root      string
}

var bundleBaseClasses = map[string]struct{}{
	`Symfony\Component\HttpKernel\Bundle\Bundle`:          {},
	`Symfony\Component\HttpKernel\Bundle\AbstractBundle`:  {},
	`Symfony\Component\HttpKernel\Bundle\BundleInterface`: {},
	`Shopware\Core\Framework\Bundle`:                      {},
	`Shopware\Core\Framework\Plugin`:                      {},
}

// BundleNamespace returns the Twig namespace Symfony derives from a bundle
// class name. It returns "" for the framework base classes themselves.
func BundleNamespace(className string) string {
	short := strings.TrimPrefix(className, `\`)
	if index := strings.LastIndex(short, `\`); index >= 0 {
		short = short[index+1:]
	}
	namespace := strings.TrimSuffix(short, "Bundle")
	if namespace == "" {
		return ""
	}
	return namespace
}

// bundleTemplateRoots resolves every indexed bundle class to its Twig root.
// Results are derived from the PHP index at query time, so a template and
// the bundle class declaring its namespace can be indexed in any order, and
// no namespace is ever invented from a Composer directory name: a class that
// is not a bundle produces no entry, and an unknown namespace resolves to
// nothing.
func (idx *TwigIndexer) bundleTemplateRoots() []bundleTemplateRoot {
	if idx == nil {
		return nil
	}
	phpIndex, _ := idx.dependencies()
	if phpIndex == nil {
		return nil
	}
	snapshot := phpIndex.SemanticSnapshot()

	idx.bundleCacheMu.Lock()
	defer idx.bundleCacheMu.Unlock()
	if idx.bundleSnapshot == snapshot {
		return idx.bundleRoots
	}
	roots := resolveBundleTemplateRoots(phpIndex)
	idx.bundleSnapshot = snapshot
	idx.bundleRoots = roots
	return roots
}

func resolveBundleTemplateRoots(phpIndex interface {
	ClassSymbols() []semantic.Symbol
	FindClass(string) (semantic.Symbol, bool)
}) []bundleTemplateRoot {
	var roots []bundleTemplateRoot
	seen := make(map[string]struct{})
	for _, class := range phpIndex.ClassSymbols() {
		if class.Kind != semantic.ClassSymbol || class.Path == "" {
			continue
		}
		fq := strings.TrimPrefix(class.FullyQualified, `\`)
		if _, isBase := bundleBaseClasses[fq]; isBase {
			continue
		}
		namespace := BundleNamespace(fq)
		if namespace == "" || !isBundleClass(phpIndex, class) {
			continue
		}
		root := filepath.Clean(filepath.Dir(class.Path))
		key := namespace + "\x00" + root
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		roots = append(roots, bundleTemplateRoot{
			Namespace: namespace,
			ClassName: fq,
			Root:      root,
		})
	}
	// Longest root first so a nested bundle wins over an enclosing one when
	// mapping a template path back to its bundle.
	sort.Slice(roots, func(left, right int) bool {
		if len(roots[left].Root) != len(roots[right].Root) {
			return len(roots[left].Root) > len(roots[right].Root)
		}
		if roots[left].Namespace != roots[right].Namespace {
			return roots[left].Namespace < roots[right].Namespace
		}
		return roots[left].Root < roots[right].Root
	})
	return roots
}

func isBundleClass(phpIndex interface {
	FindClass(string) (semantic.Symbol, bool)
}, class semantic.Symbol) bool {
	visited := map[string]struct{}{}
	queue := append(append([]string{}, class.Extends()...), class.Implements()...)
	for depth := 0; len(queue) != 0 && depth < 32; depth++ {
		name := strings.TrimPrefix(queue[0], `\`)
		queue = queue[1:]
		if name == "" {
			continue
		}
		if _, isBase := bundleBaseClasses[name]; isBase {
			return true
		}
		key := strings.ToLower(name)
		if _, seen := visited[key]; seen {
			continue
		}
		visited[key] = struct{}{}
		parent, found := phpIndex.FindClass(name)
		if !found {
			continue
		}
		queue = append(queue, parent.Extends()...)
		queue = append(queue, parent.Implements()...)
	}
	return false
}

// bundleRootsForNamespace returns the roots of bundles whose Symfony Twig
// namespace is exactly namespace.
func (idx *TwigIndexer) bundleRootsForNamespace(namespace string) []bundleTemplateRoot {
	var result []bundleTemplateRoot
	for _, root := range idx.bundleTemplateRoots() {
		if root.Namespace == namespace {
			result = append(result, root)
		}
	}
	return result
}

// bundleForTemplatePath maps an indexed template file back to the bundle
// that owns it and the name relative to that bundle's template root.
func (idx *TwigIndexer) bundleForTemplatePath(templatePath string) (bundleTemplateRoot, string, bool) {
	normalized := filepath.ToSlash(filepath.Clean(templatePath))
	for _, root := range idx.bundleTemplateRoots() {
		rootPath := filepath.ToSlash(root.Root)
		for _, marker := range []string{"/Resources/views/", "/templates/"} {
			prefix := rootPath + marker
			if strings.HasPrefix(normalized, prefix) {
				relative := strings.TrimPrefix(normalized, prefix)
				if relative != "" {
					return root, relative, true
				}
			}
		}
	}
	return bundleTemplateRoot{}, "", false
}

// resolveBundleTemplateFiles resolves @Namespace/relative through the
// bundle class map instead of through a name guessed at index time. It
// returns nil when the namespace is unknown, so genuinely missing templates
// keep being reported.
func (idx *TwigIndexer) resolveBundleTemplateFiles(namespace, relative string) ([]TwigFile, error) {
	relative = strings.TrimPrefix(filepath.ToSlash(relative), "/")
	if namespace == "" || relative == "" {
		return nil, nil
	}
	var result []TwigFile
	seen := make(map[string]struct{})
	for _, root := range idx.bundleRootsForNamespace(namespace) {
		for _, marker := range []string{"Resources/views", "templates"} {
			candidate := filepath.Join(append([]string{root.Root, marker}, strings.Split(relative, "/")...)...)
			file, found, err := idx.GetTwigFileByPath(candidate)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			if _, duplicate := seen[file.Path]; duplicate {
				continue
			}
			seen[file.Path] = struct{}{}
			result = append(result, file)
		}
	}
	return result, nil
}

// splitNamespacedTemplate splits @Namespace/relative into its parts.
func splitNamespacedTemplate(template string) (namespace, relative string, ok bool) {
	if !strings.HasPrefix(template, "@") {
		return "", "", false
	}
	namespace, relative, found := strings.Cut(strings.TrimPrefix(template, "@"), "/")
	if !found || namespace == "" || relative == "" {
		return "", "", false
	}
	return namespace, relative, true
}

// indexedAliasesForTemplate returns the index keys under which the file
// behind a (possibly bundle-namespaced) template name was stored, so
// catalogs keyed by template name (macros, template variables) resolve the
// same way template files do.
func (idx *TwigIndexer) indexedAliasesForTemplate(template string) []string {
	aliases := []string{template}
	namespace, relative, ok := splitNamespacedTemplate(template)
	if !ok {
		return aliases
	}
	files, err := idx.resolveBundleTemplateFiles(namespace, relative)
	if err != nil {
		return aliases
	}
	for _, file := range files {
		for _, name := range TemplateNames(file.Path) {
			if !containsString(aliases, name) {
				aliases = append(aliases, name)
			}
		}
	}
	return aliases
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
