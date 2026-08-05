package analytics

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
)

const maxProfilerSerializedDepth = 64

type profilerSerializedValue struct {
	raw        []byte
	text       string
	hasText    bool
	integer    int
	hasInteger bool
	bodyStart  int
	bodyEnd    int
}

type profilerSerializedPair struct {
	key   profilerSerializedValue
	value profilerSerializedValue
}

func readProfilerSerializedValue(
	content []byte,
	start,
	depth int,
) (profilerSerializedValue, bool) {
	if start < 0 || start >= len(content) ||
		depth > maxProfilerSerializedDepth {
		return profilerSerializedValue{}, false
	}
	switch content[start] {
	case 'N':
		if start+2 <= len(content) &&
			bytes.Equal(content[start:start+2], []byte("N;")) {
			return profilerSerializedValue{raw: content[start : start+2]}, true
		}
	case 's':
		if start+2 > len(content) || content[start+1] != ':' {
			break
		}
		length, cursor, ok := profilerSerializedUnsigned(
			content,
			start+2,
			':',
		)
		if !ok || cursor+1 >= len(content) ||
			content[cursor+1] != '"' {
			break
		}
		valueStart := cursor + 2
		valueEnd := valueStart + length
		if valueEnd+2 > len(content) ||
			content[valueEnd] != '"' ||
			content[valueEnd+1] != ';' {
			break
		}
		return profilerSerializedValue{
			raw:     content[start : valueEnd+2],
			text:    string(content[valueStart:valueEnd]),
			hasText: true,
		}, true
	case 'i', 'b', 'd', 'r', 'R':
		if start+2 > len(content) || content[start+1] != ':' {
			break
		}
		end := bytes.IndexByte(content[start+2:], ';')
		if end < 0 {
			break
		}
		end += start + 2
		value := profilerSerializedValue{
			raw: content[start : end+1],
		}
		if content[start] == 'i' {
			integer, err := strconv.Atoi(string(content[start+2 : end]))
			if err == nil {
				value.integer = integer
				value.hasInteger = true
			}
		}
		return value, true
	case 'a':
		return readProfilerSerializedArray(content, start, depth)
	case 'O':
		return readProfilerSerializedObject(content, start, depth)
	}
	return profilerSerializedValue{}, false
}

func readProfilerSerializedArray(
	content []byte,
	start,
	depth int,
) (profilerSerializedValue, bool) {
	if start+2 > len(content) || content[start+1] != ':' {
		return profilerSerializedValue{}, false
	}
	count, cursor, ok := profilerSerializedUnsigned(
		content,
		start+2,
		':',
	)
	if !ok || cursor+1 >= len(content) || content[cursor+1] != '{' {
		return profilerSerializedValue{}, false
	}
	if count > len(content)/2 {
		return profilerSerializedValue{}, false
	}
	bodyStart := cursor + 2
	cursor = bodyStart
	for index := 0; index < count*2; index++ {
		value, found := readProfilerSerializedValue(
			content,
			cursor,
			depth+1,
		)
		if !found {
			return profilerSerializedValue{}, false
		}
		cursor += len(value.raw)
	}
	if cursor >= len(content) || content[cursor] != '}' {
		return profilerSerializedValue{}, false
	}
	end := cursor + 1
	return profilerSerializedValue{
		raw:       content[start:end],
		bodyStart: bodyStart - start,
		bodyEnd:   cursor - start,
	}, true
}

func readProfilerSerializedObject(
	content []byte,
	start,
	depth int,
) (profilerSerializedValue, bool) {
	if start+2 > len(content) || content[start+1] != ':' {
		return profilerSerializedValue{}, false
	}
	classLength, cursor, ok := profilerSerializedUnsigned(
		content,
		start+2,
		':',
	)
	if !ok || cursor+1 >= len(content) || content[cursor+1] != '"' {
		return profilerSerializedValue{}, false
	}
	classEnd := cursor + 2 + classLength
	if classEnd >= len(content) || content[classEnd] != '"' ||
		classEnd+1 >= len(content) || content[classEnd+1] != ':' {
		return profilerSerializedValue{}, false
	}
	count, cursor, ok := profilerSerializedUnsigned(
		content,
		classEnd+2,
		':',
	)
	if !ok || cursor+1 >= len(content) || content[cursor+1] != '{' {
		return profilerSerializedValue{}, false
	}
	if count > len(content)/2 {
		return profilerSerializedValue{}, false
	}
	bodyStart := cursor + 2
	cursor = bodyStart
	for index := 0; index < count*2; index++ {
		value, found := readProfilerSerializedValue(
			content,
			cursor,
			depth+1,
		)
		if !found {
			return profilerSerializedValue{}, false
		}
		cursor += len(value.raw)
	}
	if cursor >= len(content) || content[cursor] != '}' {
		return profilerSerializedValue{}, false
	}
	end := cursor + 1
	return profilerSerializedValue{
		raw:       content[start:end],
		bodyStart: bodyStart - start,
		bodyEnd:   cursor - start,
	}, true
}

func profilerSerializedUnsigned(
	content []byte,
	start int,
	terminator byte,
) (int, int, bool) {
	cursor := start
	for cursor < len(content) &&
		content[cursor] >= '0' &&
		content[cursor] <= '9' {
		cursor++
	}
	if cursor == start || cursor >= len(content) ||
		content[cursor] != terminator {
		return 0, start, false
	}
	value, err := strconv.Atoi(string(content[start:cursor]))
	if err != nil || value < 0 {
		return 0, start, false
	}
	return value, cursor, true
}

func profilerSerializedDirectPairs(
	content []byte,
) []profilerSerializedPair {
	root, found := readProfilerSerializedValue(content, 0, 0)
	if !found || root.bodyEnd <= root.bodyStart {
		return nil
	}
	cursor := root.bodyStart
	result := make([]profilerSerializedPair, 0)
	for cursor < root.bodyEnd {
		key, keyFound := readProfilerSerializedValue(
			root.raw,
			cursor,
			1,
		)
		if !keyFound {
			break
		}
		cursor += len(key.raw)
		value, valueFound := readProfilerSerializedValue(
			root.raw,
			cursor,
			1,
		)
		if !valueFound {
			break
		}
		cursor += len(value.raw)
		result = append(result, profilerSerializedPair{
			key:   key,
			value: value,
		})
	}
	return result
}

func profilerSerializedProperty(
	content []byte,
	name string,
) (profilerSerializedValue, bool) {
	for _, pair := range profilerSerializedDirectPairs(content) {
		if !pair.key.hasText ||
			profilerSerializedPropertyName(pair.key.text) != name {
			continue
		}
		return pair.value, true
	}
	return profilerSerializedValue{}, false
}

func profilerSerializedIntegerKey(
	content []byte,
	key int,
) (profilerSerializedValue, bool) {
	for _, pair := range profilerSerializedDirectPairs(content) {
		if pair.key.hasInteger && pair.key.integer == key {
			return pair.value, true
		}
	}
	return profilerSerializedValue{}, false
}

func profilerSerializedPropertyName(name string) string {
	if offset := strings.LastIndexByte(name, 0); offset >= 0 {
		return name[offset+1:]
	}
	return name
}

func resolveProfilerSerializedReference(
	value profilerSerializedValue,
	data []byte,
) profilerSerializedValue {
	reference, found := profilerSerializedIntegerKey(value.raw, 1)
	if !found || !reference.hasInteger {
		return value
	}
	resolved, found := profilerSerializedIntegerKey(
		data,
		reference.integer,
	)
	if !found {
		return value
	}
	return resolved
}

func profilerTwigComponents(
	content []byte,
) []ProfilerRuntimeTwigComponent {
	marker := []byte(`s:14:"twig_component";`)
	offset := bytes.Index(content, marker)
	if offset < 0 {
		return nil
	}
	collector, found := readProfilerSerializedValue(
		content,
		offset+len(marker),
		0,
	)
	if !found {
		return nil
	}
	dataObject, found := profilerSerializedProperty(collector.raw, "data")
	if !found {
		return nil
	}
	data, found := profilerSerializedProperty(dataObject.raw, "data")
	if !found {
		return nil
	}

	var componentMap profilerSerializedValue
	for _, pair := range profilerSerializedDirectPairs(data.raw) {
		components, exists := profilerSerializedProperty(
			pair.value.raw,
			"components",
		)
		if !exists {
			continue
		}
		componentMap = resolveProfilerSerializedReference(components, data.raw)
		break
	}
	if len(componentMap.raw) == 0 {
		return nil
	}

	grouped := make(map[string]ProfilerRuntimeTwigComponent)
	for _, pair := range profilerSerializedDirectPairs(componentMap.raw) {
		component := resolveProfilerSerializedReference(pair.value, data.raw)
		current, valid := profilerRuntimeTwigComponent(component.raw)
		if !valid {
			continue
		}
		key := strings.ToLower(current.Name)
		if existing, duplicate := grouped[key]; duplicate {
			if existing.Class == "" {
				existing.Class = current.Class
			}
			if existing.Template == "" {
				existing.Template = current.Template
			}
			existing.RenderCount += current.RenderCount
			grouped[key] = existing
			continue
		}
		grouped[key] = current
	}
	result := make(
		[]ProfilerRuntimeTwigComponent,
		0,
		len(grouped),
	)
	for _, component := range grouped {
		result = append(result, component)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].RenderCount != result[right].RenderCount {
			return result[left].RenderCount > result[right].RenderCount
		}
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result
}

func profilerRuntimeTwigComponent(
	content []byte,
) (ProfilerRuntimeTwigComponent, bool) {
	var result ProfilerRuntimeTwigComponent
	for _, pair := range profilerSerializedDirectPairs(content) {
		if !pair.key.hasText {
			continue
		}
		switch profilerSerializedPropertyName(pair.key.text) {
		case "name":
			if pair.value.hasText {
				result.Name = strings.TrimSpace(pair.value.text)
			}
		case "class":
			if pair.value.hasText {
				result.Class = strings.Trim(
					strings.TrimSpace(pair.value.text),
					`\`,
				)
			}
		case "template":
			if pair.value.hasText {
				result.Template = strings.TrimSpace(pair.value.text)
			}
		case "render_count":
			if pair.value.hasInteger {
				result.RenderCount = pair.value.integer
			}
		}
	}
	return result, result.Name != "" &&
		(result.Class != "" || result.Template != "" ||
			result.RenderCount != 0)
}
