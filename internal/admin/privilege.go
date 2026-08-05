package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

type AdminPrivilegeKind string

const (
	AdminPrivilegeRole       AdminPrivilegeKind = "role"
	AdminPrivilegePermission AdminPrivilegeKind = "permission"

	// AdminPrivilegeAdministrator is Shopware's built-in administrator-only ACL
	// key. It is understood without a project-owned
	// addPrivilegeMappingEntry declaration.
	AdminPrivilegeAdministrator = "admin"
)

// AdminPrivilege is either a public Administration role key (for example
// product.viewer) or one of the concrete backend permissions contained by a
// role (for example product:read).
type AdminPrivilege struct {
	Name       string
	Kind       AdminPrivilegeKind
	MappingKey string
	Role       string
	FilePath   string
	Line       int
}

// IsBuiltin reports whether the privilege is provided by Shopware itself and
// therefore has no project-owned source location.
func (privilege AdminPrivilege) IsBuiltin() bool {
	return privilege.Name == AdminPrivilegeAdministrator &&
		privilege.FilePath == ""
}

func builtinAdminPrivilege(name string) (AdminPrivilege, bool) {
	if name != AdminPrivilegeAdministrator {
		return AdminPrivilege{}, false
	}
	return AdminPrivilege{
		Name: AdminPrivilegeAdministrator,
		Kind: AdminPrivilegeRole,
	}, true
}

func parseAdminPrivileges(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminPrivilege {
	var privileges []AdminPrivilege
	for _, call := range jsquery.Calls(root) {
		if jsquery.CallMethodName(call) != "addPrivilegeMappingEntry" ||
			!strings.HasSuffix(jsquery.CallName(call), ".addPrivilegeMappingEntry") {
			continue
		}
		config := jsquery.ObjectArgument(call, 0)
		mappingKey := stringProperty(config, "key")
		roles := jsquery.PropertyValue(jsquery.Property(config, "roles"))
		if mappingKey == "" || roles == nil || roles.Kind() != jssyntax.JsObject {
			continue
		}
		for _, roleProperty := range jsquery.Properties(roles) {
			roleName := jsquery.PropertyName(roleProperty)
			roleConfig := jsquery.PropertyValue(roleProperty)
			if roleName == "" || roleConfig == nil {
				continue
			}
			line, _ := lineIndex.Position(roleProperty.RangeTrimmedTrivia().Start)
			privileges = append(privileges, AdminPrivilege{
				Name: mappingKey + "." + roleName,
				Kind: AdminPrivilegeRole, MappingKey: mappingKey, Role: roleName,
				FilePath: filePath, Line: int(line) + 1,
			})

			permissionArray := jsquery.PropertyValue(
				jsquery.Property(roleConfig, "privileges"),
			)
			for _, item := range jsquery.ArrayItems(permissionArray) {
				if item.Kind() != jssyntax.JsString {
					continue
				}
				permission := jsquery.StringValue(item)
				if permission == "" {
					continue
				}
				permissionLine, _ := lineIndex.Position(
					item.RangeTrimmedTrivia().Start,
				)
				privileges = append(privileges, AdminPrivilege{
					Name: permission, Kind: AdminPrivilegePermission,
					MappingKey: mappingKey, Role: roleName,
					FilePath: filePath, Line: int(permissionLine) + 1,
				})
			}
		}
	}
	return privileges
}

func preferredPrivileges(values []AdminPrivilege) []AdminPrivilege {
	return preferRuntimeDefinitions(values, func(value AdminPrivilege) string {
		return value.FilePath
	})
}
