package translation

import (
	"regexp"
	"sort"
	"strings"
)

var placeholderPatterns = []*regexp.Regexp{
	regexp.MustCompile(`%[^%\s]+%`),
	regexp.MustCompile(`\{\{\s*[^{}]+?\s*\}\}`),
	regexp.MustCompile(`[@!%][^\s][\w-]*`),
	regexp.MustCompile(`\{\s*[^{}]+?\s*\}`),
}

var icuArgumentPattern = regexp.MustCompile(
	`\{\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*,`,
)

func Placeholders(text string) []string {
	unique := make(map[string]struct{})
	for _, pattern := range placeholderPatterns {
		for _, match := range pattern.FindAllString(text, -1) {
			match = strings.TrimSpace(match)
			if match == "" {
				continue
			}
			unique[match] = struct{}{}
			if strings.HasPrefix(match, "{") &&
				strings.HasSuffix(match, "}") &&
				!strings.HasPrefix(match, "{{") {
				name := strings.TrimSpace(
					strings.TrimSuffix(
						strings.TrimPrefix(match, "{"),
						"}",
					),
				)
				if comma := strings.IndexByte(name, ','); comma >= 0 {
					name = strings.TrimSpace(name[:comma])
				}
				if name != "" {
					unique[name] = struct{}{}
				}
			}
		}
	}
	for _, match := range icuArgumentPattern.FindAllStringSubmatch(text, -1) {
		if len(match) == 2 {
			unique[match[1]] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for placeholder := range unique {
		result = append(result, placeholder)
	}
	sort.Strings(result)
	return result
}

func (idx *Index) GetPlaceholders(domain, key string) ([]string, error) {
	messages, err := idx.GetMessages(domain, key)
	if err != nil {
		return nil, err
	}
	unique := make(map[string]struct{})
	for _, message := range messages {
		for _, placeholder := range Placeholders(message.Text) {
			unique[placeholder] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for placeholder := range unique {
		result = append(result, placeholder)
	}
	sort.Strings(result)
	return result, nil
}
