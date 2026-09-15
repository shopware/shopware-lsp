package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

func writeVendorFixture(t *testing.T, root string, manifest string, directories ...string) {
	t.Helper()
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(manifest), 0o644); err != nil {
			t.Fatalf("write composer.json: %v", err)
		}
	}
	for _, directory := range directories {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
}

func TestNestedVendorPackage(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		path     string
		expected string
	}{
		{name: "root vendor is not nested", path: "vendor/shopware/core/Foo.php", expected: ""},
		{name: "root vendor directory itself", path: "vendor", expected: ""},
		{name: "nested package", path: "custom/plugins/Swag/vendor/shopware/core/Foo.php", expected: "shopware/core"},
		{name: "nested package directory", path: "custom/plugins/Swag/vendor/shopware/core", expected: "shopware/core"},
		{name: "nested vendor namespace only", path: "custom/plugins/Swag/vendor/shopware", expected: "shopware"},
		{name: "nested vendor root", path: "custom/plugins/Swag/vendor", expected: ""},
		{name: "composer metadata directory", path: "custom/plugins/Swag/vendor/composer/installed.json", expected: "composer/installed.json"},
		{name: "vendor autoloader", path: "custom/plugins/Swag/vendor/autoload.php", expected: "autoload.php"},
		{name: "windows separators", path: `custom\plugins\Swag\vendor\shopware\core\Foo.php`, expected: "shopware/core"},
		{name: "unrelated path", path: "src/Core/Content/Product/ProductDefinition.php", expected: ""},
		{name: "directory merely named vendorish", path: "custom/plugins/Swag/vendors/acme/lib.php", expected: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := nestedVendorPackage(testCase.path); actual != testCase.expected {
				t.Fatalf("nestedVendorPackage(%q) = %q, want %q", testCase.path, actual, testCase.expected)
			}
		})
	}
}

// A Flex project keeps shopware/core in the root vendor directory, so a plugin
// that vendored it again is a duplicate.
func TestDuplicatesRootPackageFromRootVendor(t *testing.T) {
	root := t.TempDir()
	writeVendorFixture(t, root, `{"name":"acme/shop"}`, "vendor/shopware/core")

	packages := NewVendorPackages(root)

	if !packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/shopware/core/Foo.php") {
		t.Fatal("vendored copy of a root package should be reported as duplicate")
	}
	if packages.DuplicatesRootPackage("vendor/shopware/core/Foo.php") {
		t.Fatal("the root vendor directory must stay indexable")
	}
}

// The monorepo replaces shopware/core and ships it from src/Core, so the root
// vendor directory never holds it and the manifest is the only evidence.
func TestDuplicatesRootPackageFromReplace(t *testing.T) {
	root := t.TempDir()
	writeVendorFixture(t, root,
		`{"name":"shopware/platform","replace":{"shopware/core":"self.version"}}`,
		"vendor/symfony/console")

	packages := NewVendorPackages(root)

	if !packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/shopware/core/Foo.php") {
		t.Fatal("replaced package should be reported as duplicate")
	}
	if !packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/shopware/platform/Foo.php") {
		t.Fatal("the root package name should be reported as duplicate")
	}
}

// A dependency that only the extension has must keep resolving, otherwise the
// plugin's own code reports missing symbols.
func TestDuplicatesRootPackageKeepsExtensionOnlyDependency(t *testing.T) {
	root := t.TempDir()
	writeVendorFixture(t, root, `{"name":"shopware/platform"}`, "vendor/shopware/dev-tools")

	packages := NewVendorPackages(root)

	if packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/acme/pdf-lib/Writer.php") {
		t.Fatal("a package the workspace does not provide must stay indexed")
	}
	if !packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/shopware/dev-tools/Foo.php") {
		t.Fatal("a package the root vendor provides should be reported as duplicate")
	}
}

func TestDuplicatesRootPackageWithoutManifest(t *testing.T) {
	root := t.TempDir()
	writeVendorFixture(t, root, "", "vendor/shopware/core")

	packages := NewVendorPackages(root)

	if !packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/shopware/core/Foo.php") {
		t.Fatal("a missing manifest must still allow root vendor lookups")
	}
	if packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/acme/lib/Foo.php") {
		t.Fatal("unknown package must not be reported as duplicate")
	}
}

func TestDuplicatesRootPackageWithMalformedManifest(t *testing.T) {
	root := t.TempDir()
	writeVendorFixture(t, root, `{"name":`, "vendor/shopware/core")

	packages := NewVendorPackages(root)

	if !packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/shopware/core/Foo.php") {
		t.Fatal("a malformed manifest must not disable root vendor lookups")
	}
}

func TestDuplicatesRootPackageZeroValue(t *testing.T) {
	var packages *VendorPackages
	if packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/shopware/core/Foo.php") {
		t.Fatal("the zero value must report nothing as duplicated")
	}
}

func TestDuplicatesRootPackageIsCached(t *testing.T) {
	root := t.TempDir()
	writeVendorFixture(t, root, `{"name":"acme/shop"}`, "vendor/shopware/core")

	packages := NewVendorPackages(root)
	if !packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/shopware/core/Foo.php") {
		t.Fatal("expected duplicate on first lookup")
	}

	// Removing the directory must not change an answer already cached, so the
	// scanner and watcher stay consistent for the lifetime of a scan.
	if err := os.RemoveAll(filepath.Join(root, "vendor", "shopware", "core")); err != nil {
		t.Fatalf("remove package: %v", err)
	}
	if !packages.DuplicatesRootPackage("custom/plugins/Swag/vendor/shopware/core/Bar.php") {
		t.Fatal("expected the cached answer to be reused")
	}
}
