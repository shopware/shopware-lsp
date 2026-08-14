package entityschema

import "github.com/shopware/shopware-lsp/internal/php/project"

var (
	bulkEntityExtensionMinimumVersion = project.Version{Major: 6, Minor: 6, Patch: 10}
	enumFieldMinimumVersion           = project.Version{Major: 6, Minor: 6, Patch: 10}
)

// BulkEntityExtensionSupported reports whether BulkEntityExtension is
// available throughout the configured Shopware version constraint. Unknown
// versions remain permissive because the generator cannot prove that the
// framework class is unavailable.
func BulkEntityExtensionSupported(versionConstraint string) bool {
	version, known := project.ParseVersionConstraint(versionConstraint)
	return !known || version.Compare(bulkEntityExtensionMinimumVersion) >= 0
}

// EnumFieldSupported reports whether the class-based EnumField is available
// throughout the configured Shopware compatibility range.
func EnumFieldSupported(versionConstraint string) bool {
	version, known := project.ParseVersionConstraint(versionConstraint)
	return !known || version.Compare(enumFieldMinimumVersion) >= 0
}

// DefinitionKindsForVersion returns the class-based definition modes that may
// safely be generated for the configured Shopware compatibility range.
func DefinitionKindsForVersion(versionConstraint string) []DefinitionKind {
	kinds := []DefinitionKind{DefinitionEntity, DefinitionMapping, DefinitionExtension}
	if BulkEntityExtensionSupported(versionConstraint) {
		kinds = append(kinds, DefinitionBulkExtension)
	}
	return kinds
}
