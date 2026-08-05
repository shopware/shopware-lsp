package types

import "strings"

const phpDocAliasPrefix = "__ShopwareLSPPHPDocAlias\\"

// PHPDocAlias creates a stable nominal reference to a PHPDoc type alias.
// Relations expand it through the workspace hierarchy when the declaring
// class and alias definition are available.
func PHPDocAlias(className, alias string) Type {
	className = strings.Trim(strings.TrimSpace(className), "\\")
	alias = strings.TrimSpace(alias)
	if !validQualifiedName(className) || !validIdentifier(alias) {
		return Unknown()
	}
	return Named(phpDocAliasPrefix + className + "\\" + alias)
}

// PHPDocAliasParts returns the declaring class and alias name encoded in a
// nominal PHPDoc alias reference.
func PHPDocAliasParts(value Type) (string, string, bool) {
	if value.Kind() != ObjectKind {
		return "", "", false
	}
	name := strings.TrimPrefix(value.Name(), "\\")
	if !strings.HasPrefix(name, phpDocAliasPrefix) {
		return "", "", false
	}
	payload := strings.TrimPrefix(name, phpDocAliasPrefix)
	separator := strings.LastIndexByte(payload, '\\')
	if separator <= 0 || separator+1 >= len(payload) {
		return "", "", false
	}
	return payload[:separator], payload[separator+1:], true
}
