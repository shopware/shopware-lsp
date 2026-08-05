package translation

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

const storageSeparator = "\x00"

// Message is one translation declaration in a source or compiled catalogue.
type Message struct {
	Domain       string
	Key          string
	Text         string
	Locale       string
	File         string
	Format       string
	Line         int
	Character    int
	EndLine      int
	EndCharacter int
}

func newMessage(
	domain,
	key,
	text,
	locale,
	file,
	format string,
	node *cst.Node,
	lineIndex *cst.LineIndex,
) Message {
	message := Message{
		Domain: normalizeDomain(domain),
		Key:    key,
		Text:   text,
		Locale: locale,
		File:   file,
		Format: format,
	}
	if node == nil || lineIndex == nil {
		return message
	}
	rng := node.RangeTrimmedTrivia()
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	message.Line = int(startLine)
	message.Character = int(startCharacter)
	message.EndLine = int(endLine)
	message.EndCharacter = int(endCharacter)
	return message
}

func normalizeDomain(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "+intl-icu")
	return value
}

func storageKey(domain, key string) string {
	return strings.ToLower(normalizeDomain(domain)) + storageSeparator + key
}

func splitStorageKey(value string) (string, string, bool) {
	index := strings.Index(value, storageSeparator)
	if index < 0 {
		return "", "", false
	}
	return value[:index], value[index+len(storageSeparator):], true
}

func sortMessages(messages []Message) {
	sort.Slice(messages, func(left, right int) bool {
		if messages[left].File != messages[right].File {
			return messages[left].File < messages[right].File
		}
		if messages[left].Line != messages[right].Line {
			return messages[left].Line < messages[right].Line
		}
		return messages[left].Character < messages[right].Character
	})
}
