package twig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// pluginBundleNames caches the bundle name declared by a plugin's
// composer.json, keyed by manifest path. Template names are computed on index
// and request paths, so the manifest is only re-read when its stat changes.
var pluginBundleNames sync.Map

type pluginBundleNameEntry struct {
	modified time.Time
	size     int64
	name     string
}

// composerPluginBundleName returns the Twig namespace a Shopware plugin
// registers: the short name of extra.shopware-plugin-class. The directory name
// is not authoritative — Composer installs store plugins below lowercase
// package directories such as vendor/store.shopware.com/swagcmsextensions.
func composerPluginBundleName(bundleRoot string) string {
	if bundleRoot == "" || bundleRoot == "." {
		return ""
	}
	manifest := filepath.Join(filepath.FromSlash(bundleRoot), "composer.json")
	info, err := os.Stat(manifest)
	if err != nil || info.IsDir() {
		pluginBundleNames.Delete(manifest)
		return ""
	}
	if cached, ok := pluginBundleNames.Load(manifest); ok {
		entry := cached.(pluginBundleNameEntry)
		if entry.modified.Equal(info.ModTime()) && entry.size == info.Size() {
			return entry.name
		}
	}
	name := readComposerPluginBundleName(manifest)
	pluginBundleNames.Store(manifest, pluginBundleNameEntry{
		modified: info.ModTime(),
		size:     info.Size(),
		name:     name,
	})
	return name
}

func readComposerPluginBundleName(manifest string) string {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return ""
	}
	var composer struct {
		Extra struct {
			ShopwarePluginClass string `json:"shopware-plugin-class"`
		} `json:"extra"`
	}
	if json.Unmarshal(data, &composer) != nil {
		return ""
	}
	class := strings.Trim(strings.TrimSpace(composer.Extra.ShopwarePluginClass), `\`)
	if class == "" {
		return ""
	}
	return class[strings.LastIndex(class, `\`)+1:]
}
