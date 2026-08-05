package asset

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

const asseticCatalogRefreshInterval = 500 * time.Millisecond

type asseticCatalog struct {
	root string

	mu          sync.Mutex
	checkedAt   time.Time
	fingerprint string
	cached      []Resource
}

type asseticContainerCandidate struct {
	path     string
	dev      bool
	modified time.Time
	size     int64
}

func newAsseticCatalog(root string) *asseticCatalog {
	return &asseticCatalog{root: filepath.Clean(root)}
}

func (catalog *asseticCatalog) resources(
	force bool,
) ([]Resource, error) {
	if catalog == nil {
		return nil, nil
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if !force &&
		!catalog.checkedAt.IsZero() &&
		time.Since(catalog.checkedAt) < asseticCatalogRefreshInterval {
		return append([]Resource(nil), catalog.cached...), nil
	}
	catalog.checkedAt = time.Now()
	candidates, err := findAsseticContainerCandidates(catalog.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			catalog.fingerprint = ""
			catalog.cached = nil
			return nil, nil
		}
		return nil, err
	}
	fingerprint := asseticCandidatesFingerprint(candidates)
	if fingerprint == catalog.fingerprint {
		return append([]Resource(nil), catalog.cached...), nil
	}
	var resources []Resource
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		content, readErr := os.ReadFile(candidate.path)
		if readErr != nil {
			continue
		}
		if !bytes.Contains(content, []byte("assetic.asset_manager")) ||
			!bytes.Contains(content, []byte("ConfigurationResource")) {
			continue
		}
		for _, resource := range parseAsseticNamedAssets(
			catalog.root,
			candidate.path,
			content,
		) {
			key := strings.ToLower(resource.Name) + "\x00" +
				filepath.Clean(resource.Target) + "\x00" +
				resource.Range.String()
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			resources = append(resources, resource)
		}
	}
	sort.Slice(resources, func(left, right int) bool {
		if !strings.EqualFold(resources[left].Name, resources[right].Name) {
			return strings.ToLower(resources[left].Name) <
				strings.ToLower(resources[right].Name)
		}
		if resources[left].Target != resources[right].Target {
			return resources[left].Target < resources[right].Target
		}
		return resources[left].Range.Start < resources[right].Range.Start
	})
	catalog.fingerprint = fingerprint
	catalog.cached = resources
	return append([]Resource(nil), resources...), nil
}

func findAsseticContainerCandidates(
	root string,
) ([]asseticContainerCandidate, error) {
	var result []asseticContainerCandidate
	for _, cacheRoot := range []string{
		filepath.Join(root, "var", "cache"),
		filepath.Join(root, "app", "cache"),
	} {
		environments, err := os.ReadDir(cacheRoot)
		if err != nil {
			continue
		}
		for _, environment := range environments {
			if !environment.IsDir() {
				continue
			}
			environmentDir := filepath.Join(cacheRoot, environment.Name())
			files, readErr := os.ReadDir(environmentDir)
			if readErr != nil {
				continue
			}
			for _, file := range files {
				if file.IsDir() ||
					!strings.HasSuffix(file.Name(), "Container.xml") {
					continue
				}
				info, infoErr := file.Info()
				if infoErr != nil {
					continue
				}
				result = append(result, asseticContainerCandidate{
					path: filepath.Join(environmentDir, file.Name()),
					dev: strings.HasPrefix(
						strings.ToLower(environment.Name()),
						"dev",
					),
					modified: info.ModTime(),
					size:     info.Size(),
				})
			}
		}
	}
	if len(result) == 0 {
		return nil, os.ErrNotExist
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].dev != result[right].dev {
			return result[left].dev
		}
		if !result[left].modified.Equal(result[right].modified) {
			return result[left].modified.After(result[right].modified)
		}
		return result[left].path < result[right].path
	})
	return result, nil
}

func asseticCandidatesFingerprint(
	candidates []asseticContainerCandidate,
) string {
	var value strings.Builder
	for _, candidate := range candidates {
		value.WriteString(candidate.path)
		value.WriteByte('\x00')
		value.WriteString(strconv.FormatInt(
			candidate.modified.UnixNano(),
			10,
		))
		value.WriteByte('\x00')
		value.WriteString(strconv.FormatInt(candidate.size, 10))
		value.WriteByte('\n')
	}
	return value.String()
}

func parseAsseticNamedAssets(
	root,
	path string,
	content []byte,
) []Resource {
	tree := xmlparser.Parse(string(content)).Tree
	if tree == nil || tree.Root == nil {
		return nil
	}
	var result []Resource
	for _, manager := range xmlquery.Elements(tree.Root, "service") {
		if xmlquery.AttributeValue(
			xmlquery.Attribute(manager, "id"),
		) != "assetic.asset_manager" {
			continue
		}
		for _, call := range xmlquery.ChildElements(manager, "call") {
			if xmlquery.AttributeValue(
				xmlquery.Attribute(call, "method"),
			) != "addResource" {
				continue
			}
			for _, configuration := range xmlquery.Elements(
				call,
				"service",
			) {
				if xmlquery.AttributeValue(
					xmlquery.Attribute(configuration, "class"),
				) != "Symfony\\Bundle\\AsseticBundle\\Factory\\Resource\\ConfigurationResource" {
					continue
				}
				arguments := xmlquery.ChildElements(
					configuration,
					"argument",
				)
				if len(arguments) == 0 {
					continue
				}
				for _, formula := range xmlquery.ChildElements(
					arguments[0],
					"argument",
				) {
					nameAttribute := xmlquery.Attribute(formula, "key")
					name := strings.TrimSpace(
						xmlquery.AttributeValue(nameAttribute),
					)
					if name == "" {
						continue
					}
					nameRange := asseticXMLAttributeRange(nameAttribute)
					files := asseticFormulaFiles(formula)
					if len(files) == 0 {
						result = append(result, Resource{
							Name:  name,
							File:  path,
							Kind:  AsseticNamedAsset,
							Range: nameRange,
						})
						continue
					}
					for _, file := range files {
						result = append(result, Resource{
							Name:   name,
							File:   path,
							Target: resolveAsseticTarget(root, file),
							Kind:   AsseticNamedAsset,
							Range:  nameRange,
						})
					}
				}
			}
		}
	}
	return result
}

func asseticFormulaFiles(formula *xmlsyntax.Node) []string {
	arguments := xmlquery.ChildElements(formula, "argument")
	if len(arguments) == 0 {
		return nil
	}
	var result []string
	for _, argument := range xmlquery.Elements(arguments[0], "argument") {
		if len(xmlquery.ChildElements(argument, "argument")) != 0 {
			continue
		}
		value := strings.TrimSpace(xmlquery.TextContent(argument))
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func resolveAsseticTarget(root, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		if info, err := os.Stat(value); err == nil && !info.IsDir() {
			return filepath.Clean(value)
		}
		return ""
	}
	normalized := filepath.FromSlash(strings.ReplaceAll(value, `\`, "/"))
	candidates := []string{
		filepath.Join(root, normalized),
	}
	trimmed := normalized
	for strings.HasPrefix(trimmed, ".."+string(os.PathSeparator)) {
		trimmed = strings.TrimPrefix(
			trimmed,
			".."+string(os.PathSeparator),
		)
	}
	if trimmed != normalized {
		candidates = append(candidates, filepath.Join(root, trimmed))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return filepath.Clean(candidate)
		}
	}
	return ""
}

func asseticXMLAttributeRange(node *xmlsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if separator := strings.IndexByte(text, '='); separator >= 0 {
		value := strings.TrimSpace(text[separator+1:])
		start := strings.Index(node.Text(), value)
		if start >= 0 {
			rng.Start = node.Range().Start + uint32(start)
			rng.End = rng.Start + uint32(len(value))
		}
	}
	if rng.Len() >= 2 {
		raw := strings.TrimSpace(node.Text())
		if strings.HasSuffix(raw, `"`) || strings.HasSuffix(raw, `'`) {
			rng.Start++
			rng.End--
		}
	}
	return rng
}
