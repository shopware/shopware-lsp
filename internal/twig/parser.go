package twig

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

var shopwareBlockCommentRegex = regexp.MustCompile(`\{#\s*` + VersionCommentPrefix + `\s*([a-f0-9]+)@([\w\.\-]+)\s*#\}`)
var twigTagRegex = regexp.MustCompile(`\{%\s*(?:block\s+([A-Za-z0-9_]+)|endblock)\b[^%]*%\}`)

func calculateBlockHash(content string) string {
	hash := sha256.New()
	hash.Write([]byte(content))
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func ParseVersionComment(comment string, line int) *TwigVersionComment {
	matches := shopwareBlockCommentRegex.FindStringSubmatch(comment)
	if len(matches) == 3 {
		return &TwigVersionComment{
			Hash:    matches[1],
			Version: matches[2],
			Line:    line,
		}
	}
	return nil
}

type TwigFile struct {
	// Name of the bundle
	BundleName string
	Path       string
	// Relative Path, used inside of Twig
	RelPath        string
	Blocks         map[string]TwigBlock
	ExtendsFile    string
	ExtendsTagLine int
}

type TwigVersionComment struct {
	Hash    string
	Version string
	Line    int
}

type TwigBlockHash struct {
	Name         string
	RelativePath string
	AbsolutePath string
	Hash         string
	Text         string
}

type TwigBlock struct {
	Name           string
	Line           int
	Hash           string
	Text           string
	VersionComment *TwigVersionComment
}

// findBlocks recursively traverses the tree to find all blocks.
// It only handles proper "block" nodes; blocks that tree-sitter fails to parse
// (e.g. those containing HTML tags, which produce ERROR nodes) are recovered
// separately by recoverMissingBlocksFromTags, which captures their full text.
func findBlocks(node *tree_sitter.Node, content []byte, file *TwigFile) {
	if node.Kind() == "block" {
		extractBlockFromNode(node, content, file)
	}

	// Recursively process all named children
	for i := 0; i < int(node.NamedChildCount()); i++ {
		findBlocks(node.NamedChild(uint(i)), content, file)
	}
}

// extractBlockFromNode extracts block name and metadata from a block or ERROR node.
// First definition wins: if a block name already exists in file.Blocks, it is not
// overwritten (e.g., when a nested block reuses a parent's name).
func extractBlockFromNode(node *tree_sitter.Node, content []byte, file *TwigFile) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(uint(i))
		if child.Kind() == "identifier" {
			blockName := string(child.Utf8Text(content))
			blockText := string(node.Utf8Text(content))
			blockHash := calculateBlockHash(blockText)

			var versionComment *TwigVersionComment
			if prevSibling := findPreviousComment(node, content); prevSibling != nil {
				commentText := string(prevSibling.Utf8Text(content))
				versionComment = ParseVersionComment(commentText, int(prevSibling.Range().StartPoint.Row)+1)
			}

			_, exists := file.Blocks[blockName]
			if !exists {
				file.Blocks[blockName] = TwigBlock{
					Name:           blockName,
					Line:           int(child.Range().StartPoint.Row) + 1,
					Hash:           blockHash,
					Text:           blockText,
					VersionComment: versionComment,
				}
			}
			break
		}
	}
}

func findPreviousComment(blockNode *tree_sitter.Node, content []byte) *tree_sitter.Node {
	parent := blockNode.Parent()
	if parent == nil {
		return nil
	}

	for i := 0; i < int(parent.NamedChildCount()); i++ {
		child := parent.NamedChild(uint(i))

		if child.Range().StartPoint.Row == blockNode.Range().StartPoint.Row &&
			child.Range().StartPoint.Column == blockNode.Range().StartPoint.Column {
			for j := i - 1; j >= 0; j-- {
				prevSibling := parent.NamedChild(uint(j))
				if prevSibling.Kind() == "comment" {
					commentText := string(prevSibling.Utf8Text(content))
					if strings.Contains(commentText, VersionCommentPrefix) {
						return prevSibling
					}
				}
				if prevSibling.Kind() == "block" {
					return nil
				}
			}
			break
		}
	}
	return nil
}

func ParseTwig(filePath string, node *tree_sitter.Node, content []byte) (*TwigFile, error) {
	file := &TwigFile{
		Path:       filePath,
		BundleName: getBundleNameByPath(filePath),
		RelPath:    ConvertToRelativePath(filePath),
		Blocks:     make(map[string]TwigBlock),
	}

	if !bytes.Contains(content, []byte("{%")) {
		return file, nil
	}

	// Find all blocks recursively
	if bytes.Contains(content, []byte("block")) {
		findBlocks(node, content, file)
		recoverMissingBlocksFromTags(content, file)
	}

	// Find extends tag
	if !bytes.Contains(content, []byte("extends")) && !bytes.Contains(content, []byte("sw_extends")) {
		return file, nil
	}

	var cursor = node.Walk()
	defer cursor.Close()

	if cursor.GotoFirstChild() {
		for {
			node := cursor.Node()

			if node.Kind() == "tag" {
				// Check if this is an extends tag by examining the tag text
				isExtendsTag := false
				tagName := ""
				for i := 0; i < int(node.NamedChildCount()); i++ {
					child := node.NamedChild(uint(i))
					if child.Kind() == "name" {
						tagName = string(child.Utf8Text(content))
						break
					}
				}

				// Check if the tag contains "extends" or "sw_extends"
				if tagName == "extends" || tagName == "sw_extends" {
					isExtendsTag = true
				}

				if !isExtendsTag && tagName == "" {
					tagText := string(node.Utf8Text(content))
					if strings.Contains(tagText, "extends") || strings.Contains(tagText, "sw_extends") {
						isExtendsTag = true
					}
				}

				// If it's an extends tag, look for the string parameter
				if isExtendsTag {
					for i := 0; i < int(node.NamedChildCount()); i++ {
						child := node.NamedChild(uint(i))

						if child.Kind() == "string" {
							file.ExtendsFile = strings.Trim(strings.Trim(string(child.Utf8Text(content)), "\""), "'")
							file.ExtendsTagLine = int(node.Range().StartPoint.Row) + 1
							break
						}
					}
				}
			}

			if !cursor.GotoNextSibling() {
				break
			}
		}
	}

	return file, nil
}

type blockStartInfo struct {
	name     string
	startIdx int
	line     int
}

func recoverMissingBlocksFromTags(content []byte, file *TwigFile) {
	matches := twigTagRegex.FindAllSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return
	}

	stack := make([]blockStartInfo, 0, len(matches))
	for _, m := range matches {
		tagStart := m[0]
		tagEnd := m[1]

		blockNameStart := m[2]
		blockNameEnd := m[3]
		if blockNameStart >= 0 && blockNameEnd >= 0 {
			stack = append(stack, blockStartInfo{
				name:     string(content[blockNameStart:blockNameEnd]),
				startIdx: tagStart,
				line:     lineAtOffset(content, tagStart),
			})
			continue
		}

		// endblock
		if len(stack) == 0 {
			continue
		}

		lastIdx := len(stack) - 1
		start := stack[lastIdx]
		stack = stack[:lastIdx]

		if _, exists := file.Blocks[start.name]; exists {
			continue
		}

		if start.startIdx < 0 || start.startIdx >= len(content) || tagEnd <= start.startIdx || tagEnd > len(content) {
			continue
		}

		blockText := string(content[start.startIdx:tagEnd])
		blockHash := calculateBlockHash(blockText)

		var versionComment *TwigVersionComment
		comment := findVersionCommentBeforeOffset(content, start.startIdx)
		if comment != nil {
			versionComment = comment
		}

		file.Blocks[start.name] = TwigBlock{
			Name:           start.name,
			Line:           start.line,
			Hash:           blockHash,
			Text:           blockText,
			VersionComment: versionComment,
		}

	}
}

func lineAtOffset(content []byte, offset int) int {
	if offset <= 0 {
		return 1
	}
	if offset > len(content) {
		offset = len(content)
	}
	return bytes.Count(content[:offset], []byte("\n")) + 1
}

func findVersionCommentBeforeOffset(content []byte, offset int) *TwigVersionComment {
	if offset <= 0 {
		return nil
	}
	if offset > len(content) {
		offset = len(content)
	}

	i := offset - 1
	for i >= 0 {
		c := content[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i--
			continue
		}
		break
	}
	if i < 1 || content[i] != '}' || content[i-1] != '#' {
		return nil
	}

	commentEnd := i + 1
	j := i - 2
	for j >= 1 {
		if content[j] == '#' && content[j-1] == '{' {
			commentStart := j - 1
			commentText := string(content[commentStart:commentEnd])
			if strings.Contains(commentText, VersionCommentPrefix) {
				return ParseVersionComment(commentText, lineAtOffset(content, commentStart))
			}
			return nil
		}
		j--
	}

	return nil
}
