package extension

import (
	"path/filepath"
)

type ShopwareExtensionType int

const (
	ShopwareExtensionTypeBundle ShopwareExtensionType = iota
	ShopwareExtensionTypeApp
)

type ShopwareExtension struct {
	Name        string
	Type        ShopwareExtensionType
	Path        string
	Permissions []AppPermission
}

type AppPermission struct {
	Operation string
	Entity    string
	Line      int
}

func (e ShopwareExtension) GetStorefrontViewsPath() string {
	return filepath.Join(e.GetRootPath(), "Resources", "views")
}

// GetRootPath returns the source root that owns an extension's Resources
// directory. Bundle paths point at the plugin class while App paths already
// point at their root directory.
func (e ShopwareExtension) GetRootPath() string {
	if e.Type == ShopwareExtensionTypeBundle {
		return filepath.Dir(e.Path)
	}
	return filepath.Clean(e.Path)
}

func (e ShopwareExtension) GetAdministrationSourcePath() string {
	return filepath.Join(
		e.GetRootPath(),
		"Resources",
		"app",
		"administration",
		"src",
	)
}
