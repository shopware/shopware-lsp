package twig

import (
	"fmt"
	"path/filepath"
	"strings"
)

const VersionCommentPrefix = "shopware-block:"

func IsStorefrontTemplate(uri string) bool {
	uri = filepath.ToSlash(uri)
	return strings.Contains(uri, "/src/Storefront/Resources/views/storefront/") ||
		strings.Contains(uri, "/vendor/shopware/storefront/Resources/views/storefront/")
}

func IsUpstreamTemplate(uri string) bool {
	uri = filepath.ToSlash(uri)
	return IsStorefrontTemplate(uri) ||
		strings.Contains(uri, "/vendor/") && strings.Contains(uri, "/Resources/views/")
}

func FormatVersionComment(hash, version string) string {
	comment := fmt.Sprintf("{# %s %s", VersionCommentPrefix, hash)
	if version = strings.TrimSpace(version); version != "" {
		comment += "@" + version
	}
	return comment + " #}\n"
}
