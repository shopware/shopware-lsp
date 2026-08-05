package translation

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
)

type Insertion struct {
	File      string
	Format    string
	Locale    string
	Line      int
	Character int
	NewText   string
}

// Insertions returns safe edits for existing writable YAML and XLIFF domain
// resources. One insertion is returned per source file so the client can
// present the locale/file choice as separate quick fixes.
func (idx *Index) Insertions(domain, key string) ([]Insertion, error) {
	return idx.InsertionsWithValue(domain, key, key)
}

// InsertionsWithValue returns the same safe resource edits as Insertions, but
// writes the supplied source text as the translation value. It is used by
// extract-translation refactorings while missing-key quick fixes retain the
// key-as-placeholder behavior through Insertions.
func (idx *Index) InsertionsWithValue(
	domain, key, value string,
) ([]Insertion, error) {
	if idx == nil || domain == "" || key == "" {
		return nil, nil
	}
	messages, err := idx.GetDomainMessages(domain)
	if err != nil {
		return nil, err
	}
	files := make(map[string]Message)
	for _, message := range messages {
		extension := strings.ToLower(filepath.Ext(message.File))
		switch extension {
		case ".yaml", ".yml", ".xlf", ".xliff", ".xml":
		default:
			continue
		}
		if _, exists := files[message.File]; !exists {
			files[message.File] = message
		}
	}
	ordered := make([]Message, 0, len(files))
	for _, message := range files {
		ordered = append(ordered, message)
	}
	sort.Slice(ordered, func(left, right int) bool {
		leftPriority := insertionFilePriority(ordered[left])
		rightPriority := insertionFilePriority(ordered[right])
		if leftPriority != rightPriority {
			return leftPriority > rightPriority
		}
		return ordered[left].File < ordered[right].File
	})

	var result []Insertion
	for _, message := range ordered {
		content, readErr := os.ReadFile(message.File)
		if readErr != nil {
			continue
		}
		var insertion Insertion
		var ok bool
		switch strings.ToLower(filepath.Ext(message.File)) {
		case ".yaml", ".yml":
			insertion, ok = yamlInsertion(
				message.File,
				content,
				key,
				value,
			)
		case ".xlf", ".xliff", ".xml":
			insertion, ok = xliffInsertion(
				message.File,
				content,
				key,
				value,
			)
		}
		if ok {
			insertion.Locale = message.Locale
			result = append(result, insertion)
		}
		if len(result) == 8 {
			break
		}
	}
	return result, nil
}

func insertionFilePriority(message Message) int {
	priority := 0
	normalized := strings.ToLower(filepath.ToSlash(message.File))
	if !strings.Contains(normalized, "/vendor/") &&
		!strings.Contains(normalized, "/var/cache/") &&
		!strings.Contains(normalized, "/app/cache/") {
		priority += 4
	}
	if strings.Contains(normalized, "/src/") ||
		strings.Contains(normalized, "/app/") {
		priority += 2
	}
	locale := strings.ToLower(strings.ReplaceAll(message.Locale, "_", "-"))
	if locale == "en" || strings.HasPrefix(locale, "en-") {
		priority++
	}
	return priority
}

func yamlInsertion(
	path string,
	content []byte,
	key, value string,
) (Insertion, bool) {
	eol := lineSeparator(content)
	offset := len(content)
	prefix := ""
	if offset != 0 && content[offset-1] != '\n' && content[offset-1] != '\r' {
		prefix = eol
	}
	text := fmt.Sprintf(
		"%s'%s': '%s'%s",
		prefix,
		yamlQuote(key),
		yamlQuote(value),
		eol,
	)
	line, character := bytePosition(content, offset)
	return Insertion{
		File:      path,
		Format:    strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."),
		Line:      line,
		Character: character,
		NewText:   text,
	}, true
}

func xliffInsertion(
	path string,
	content []byte,
	key, value string,
) (Insertion, bool) {
	parsed := xmlparser.Parse(string(content))
	if parsed.Tree == nil || parsed.Tree.Root == nil {
		return Insertion{}, false
	}
	roots := xmlquery.Elements(parsed.Tree.Root, "xliff")
	if len(roots) == 0 {
		return Insertion{}, false
	}
	root := roots[0]
	version := xmlquery.AttributeValue(xmlquery.Attribute(root, "version"))
	file := xmlquery.ChildElement(root, "file")
	if file == nil {
		return Insertion{}, false
	}

	container := file
	elementName := "unit"
	unit := ""
	switch {
	case strings.HasPrefix(version, "1."):
		container = xmlquery.ChildElement(file, "body")
		elementName = "trans-unit"
		if container == nil {
			return Insertion{}, false
		}
	case strings.HasPrefix(version, "2."):
		if group := xmlquery.ChildElement(file, "group"); group != nil {
			container = group
		}
	default:
		return Insertion{}, false
	}
	nextID := nextXLIFFID(container, elementName)
	escapedKey := html.EscapeString(key)
	escapedValue := html.EscapeString(value)
	if elementName == "trans-unit" {
		unit = fmt.Sprintf(
			`<trans-unit id="%d" resname="%s"><source>%s</source><target>%s</target></trans-unit>`,
			nextID,
			escapedKey,
			escapedKey,
			escapedValue,
		)
	} else {
		unit = fmt.Sprintf(
			`<unit id="%d"><segment><source>%s</source><target>%s</target></segment></unit>`,
			nextID,
			escapedKey,
			escapedValue,
		)
	}

	containerText := container.Text()
	closing := strings.LastIndex(containerText, "</")
	if closing < 0 {
		return Insertion{}, false
	}
	offset := int(container.Range().Start) + closing
	containerIndent := indentationAtOffset(content, offset)
	childIndent := containerIndent + "  "
	if children := xmlquery.ChildElements(container); len(children) != 0 {
		childIndent = indentationAtOffset(content, int(children[0].Range().Start))
	}
	eol := lineSeparator(content)
	lineStart := lineStartAt(content, offset)
	var newText string
	if strings.TrimSpace(string(content[lineStart:offset])) == "" {
		offset = lineStart
		newText = childIndent + unit + eol
	} else {
		newText = eol + childIndent + unit + eol + containerIndent
	}
	line, character := bytePosition(content, offset)
	return Insertion{
		File:      path,
		Format:    strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."),
		Line:      line,
		Character: character,
		NewText:   newText,
	}, true
}

func nextXLIFFID(container *cst.Node, elementName string) int {
	next := 0
	for _, element := range xmlquery.Elements(container, elementName) {
		value := xmlquery.AttributeValue(xmlquery.Attribute(element, "id"))
		id, err := strconv.Atoi(value)
		if err == nil && id >= next {
			next = id + 1
		}
	}
	return next
}

func yamlQuote(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func lineSeparator(content []byte) string {
	if strings.Contains(string(content), "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func bytePosition(content []byte, offset int) (int, int) {
	if offset > len(content) {
		offset = len(content)
	}
	line, character := cst.NewLineIndex(
		string(content),
	).PositionUTF16(uint32(offset))
	return int(line), int(character)
}

func lineStartAt(content []byte, offset int) int {
	if offset > len(content) {
		offset = len(content)
	}
	for offset > 0 && content[offset-1] != '\n' {
		offset--
	}
	return offset
}

func indentationAtOffset(content []byte, offset int) string {
	start := lineStartAt(content, offset)
	end := start
	for end < len(content) {
		if content[end] != ' ' && content[end] != '\t' {
			break
		}
		end++
	}
	return string(content[start:end])
}
