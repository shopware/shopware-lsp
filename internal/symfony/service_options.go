package symfony

import "strings"

// configuredServiceBool parses statically knowable Symfony configuration
// booleans. The second return value reports whether the value was understood.
func configuredServiceBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off", "null", "~":
		return false, true
	default:
		return false, false
	}
}
