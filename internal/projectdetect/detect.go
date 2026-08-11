package projectdetect

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/projectconfig"
)

type Kind string

const (
	KindUnknown    Kind = "unknown"
	KindConfigured Kind = "configured"
	KindSymfony    Kind = "symfony"
	KindShopware   Kind = "shopware"
)

type Evidence struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Result struct {
	Supported bool       `json:"supported"`
	Kind      Kind       `json:"kind"`
	Evidence  []Evidence `json:"evidence"`
}

type composerManifest struct {
	Name       string                     `json:"name"`
	Type       string                     `json:"type"`
	Require    map[string]json.RawMessage `json:"require"`
	RequireDev map[string]json.RawMessage `json:"require-dev"`
	Extra      struct {
		ShopwarePluginClass string `json:"shopware-plugin-class"`
	} `json:"extra"`
}

type composerLock struct {
	Packages    []composerPackage `json:"packages"`
	PackagesDev []composerPackage `json:"packages-dev"`
}

type composerPackage struct {
	Name string `json:"name"`
}

var shopwarePackages = []string{
	"shopware/core",
	"shopware/platform",
	"shopware/production",
	"shopware/development",
}

func Detect(root string) (Result, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Result{}, fmt.Errorf("resolve project root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return Result{}, fmt.Errorf("inspect project root: %w", err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("project root is not a directory: %s", absolute)
	}

	var configured, shopware, symfony []Evidence
	configurationPath := projectconfig.ProjectRelativePath
	if found, err := regularFile(filepath.Join(absolute, filepath.FromSlash(configurationPath))); err != nil {
		return Result{}, err
	} else if found {
		configured = append(configured, Evidence{
			Path: configurationPath, Reason: "explicit Shopware LSP configuration",
		})
	}

	manifestPath := filepath.Join(absolute, "composer.json")
	if content, found, err := readOptional(manifestPath); err != nil {
		return Result{}, err
	} else if found {
		var composer composerManifest
		if err := json.Unmarshal(content, &composer); err != nil {
			if result, supported := existingSupport(nil, nil, configured); supported {
				return result, nil
			}
			return Result{}, fmt.Errorf("parse composer.json: %w", err)
		}
		shopware = append(shopware, composerShopwareEvidence(composer)...)
		symfony = append(symfony, composerSymfonyEvidence(composer)...)
	}
	if result, supported := existingSupport(shopware, nil, nil); supported {
		return result, nil
	}

	if content, found, err := readOptional(filepath.Join(absolute, "manifest.xml")); err != nil {
		return Result{}, err
	} else if found {
		match, matchErr := shopwareManifest(content)
		if matchErr != nil {
			if result, supported := existingSupport(shopware, symfony, configured); supported {
				return result, nil
			}
			return Result{}, fmt.Errorf("parse manifest.xml: %w", matchErr)
		}
		if match {
			shopware = append(shopware, Evidence{
				Path: "manifest.xml", Reason: "Shopware app manifest schema",
			})
		}
	}
	if result, supported := existingSupport(shopware, nil, nil); supported {
		return result, nil
	}

	if content, found, err := readOptional(filepath.Join(absolute, "config", "bundles.php")); err != nil {
		return Result{}, err
	} else if found && bytesContainFrameworkBundle(content) {
		symfony = append(symfony, Evidence{
			Path: "config/bundles.php", Reason: "registers Symfony FrameworkBundle",
		})
	}

	if len(shopware) == 0 && len(symfony) == 0 {
		lockPath := filepath.Join(absolute, "composer.lock")
		if content, found, err := readOptional(lockPath); err != nil {
			return Result{}, err
		} else if found {
			var lock composerLock
			if err := json.Unmarshal(content, &lock); err != nil {
				if result, supported := existingSupport(shopware, symfony, configured); supported {
					return result, nil
				}
				return Result{}, fmt.Errorf("parse composer.lock: %w", err)
			}
			for _, current := range append(lock.Packages, lock.PackagesDev...) {
				name := normalizedPackage(current.Name)
				switch {
				case slices.Contains(shopwarePackages, name):
					shopware = append(shopware, Evidence{
						Path: "composer.lock", Reason: "contains " + name,
					})
				case name == "symfony/framework-bundle":
					symfony = append(symfony, Evidence{
						Path: "composer.lock", Reason: "contains symfony/framework-bundle",
					})
				}
			}
		}
	}

	if result, found := existingSupport(shopware, symfony, configured); found {
		return result, nil
	}
	return Result{Kind: KindUnknown, Evidence: []Evidence{}}, nil
}

func composerShopwareEvidence(composer composerManifest) []Evidence {
	var result []Evidence
	name := normalizedPackage(composer.Name)
	if slices.Contains(shopwarePackages, name) {
		result = append(result, Evidence{Path: "composer.json", Reason: "package name is " + name})
	}
	if strings.EqualFold(strings.TrimSpace(composer.Type), "shopware-platform-plugin") {
		result = append(result, Evidence{Path: "composer.json", Reason: "type is shopware-platform-plugin"})
	}
	if strings.TrimSpace(composer.Extra.ShopwarePluginClass) != "" {
		result = append(result, Evidence{Path: "composer.json", Reason: "declares extra.shopware-plugin-class"})
	}
	for _, name := range dependencyNames(composer) {
		if slices.Contains(shopwarePackages, name) {
			result = append(result, Evidence{Path: "composer.json", Reason: "requires " + name})
		}
	}
	return deduplicateEvidence(result)
}

func composerSymfonyEvidence(composer composerManifest) []Evidence {
	if slices.Contains(dependencyNames(composer), "symfony/framework-bundle") {
		return []Evidence{{Path: "composer.json", Reason: "requires symfony/framework-bundle"}}
	}
	return nil
}

func dependencyNames(composer composerManifest) []string {
	result := make([]string, 0, len(composer.Require)+len(composer.RequireDev))
	for name := range composer.Require {
		result = append(result, normalizedPackage(name))
	}
	for name := range composer.RequireDev {
		result = append(result, normalizedPackage(name))
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func shopwareManifest(content []byte) (bool, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(content)))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if !strings.EqualFold(start.Name.Local, "manifest") {
			return false, nil
		}
		for _, attribute := range start.Attr {
			value := strings.ToLower(attribute.Value)
			if strings.Contains(value, "shopware") && strings.Contains(value, "manifest") {
				return true, nil
			}
		}
		return false, nil
	}
}

func bytesContainFrameworkBundle(content []byte) bool {
	source := string(content)
	return strings.Contains(source, `Symfony\Bundle\FrameworkBundle\FrameworkBundle`) ||
		strings.Contains(source, `FrameworkBundle::class`)
}

func supported(kind Kind, evidence []Evidence) Result {
	return Result{Supported: true, Kind: kind, Evidence: deduplicateEvidence(evidence)}
}

func existingSupport(shopware, symfony, configured []Evidence) (Result, bool) {
	if len(shopware) > 0 {
		return supported(KindShopware, shopware), true
	}
	if len(symfony) > 0 {
		return supported(KindSymfony, symfony), true
	}
	if len(configured) > 0 {
		return supported(KindConfigured, configured), true
	}
	return Result{}, false
}

func deduplicateEvidence(values []Evidence) []Evidence {
	result := make([]Evidence, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := value.Path + "\x00" + value.Reason
		if _, found := seen[key]; found {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizedPackage(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func regularFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", filepath.Base(path), err)
	}
	return info.Mode().IsRegular(), nil
}

func readOptional(path string) ([]byte, bool, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return content, true, nil
}
