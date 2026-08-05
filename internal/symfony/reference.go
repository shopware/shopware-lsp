package symfony

import "strings"

// NormalizeServiceReference parses Symfony's @service syntax. Escaped @@
// values and expression-language references are not static service uses.
func NormalizeServiceReference(value string) (string, bool) {
	name, _, ok := ParseServiceReference(value)
	return name, ok
}

func ParseServiceReference(value string) (name string, optional bool, ok bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "@") || strings.HasPrefix(value, "@@") {
		return "", false, false
	}
	value = strings.TrimPrefix(value, "@")
	if strings.HasPrefix(value, "?") || strings.HasPrefix(value, "!") {
		optional = true
		value = value[1:]
	}
	if value == "" || strings.HasPrefix(value, "=") || strings.ContainsAny(value, "%${}") {
		return "", false, false
	}
	return value, optional, true
}

// ParameterReferences extracts %parameter% references, excluding escaped %%
// markers and env()/expression placeholders that are resolved dynamically.
func ParameterReferences(value string) []string {
	var result []string
	seen := make(map[string]struct{})
	for offset := 0; offset < len(value); {
		start := strings.IndexByte(value[offset:], '%')
		if start < 0 {
			break
		}
		start += offset
		if start+1 < len(value) && value[start+1] == '%' {
			offset = start + 2
			continue
		}
		end := strings.IndexByte(value[start+1:], '%')
		if end < 0 {
			break
		}
		end += start + 1
		name := strings.TrimSpace(value[start+1 : end])
		offset = end + 1
		if name == "" || strings.HasPrefix(name, "env(") ||
			strings.HasPrefix(name, "expr(") {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
	}
	return result
}
