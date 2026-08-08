package twig

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

var shopwareBlockCommentRegex = regexp.MustCompile(
	`\{#\s*` + VersionCommentPrefix + `\s*([a-fA-F0-9]+)(?:@([\w.\-+]+))?\s*#\}`,
)

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
	Range   cst.TextRange
}

type TwigBlockHash struct {
	Name                 string
	RelativePath         string
	AbsolutePath         string
	BundleName           string
	Hash                 string
	Text                 string
	Line                 int
	Deprecation          string
	HasVersioningComment bool
}

type TwigBlock struct {
	Name                 string
	Range                cst.TextRange
	NameRange            cst.TextRange
	Line                 int
	Hash                 string
	Text                 string
	HasVersioningComment bool
	VersionCommentRange  *cst.TextRange
	VersionComment       *TwigVersionComment
	Deprecation          string
}

func findBlocks(root *twigsyntax.Node, source string, lineIndex *twigsyntax.LineIndex, file *TwigFile) {
	for element := range root.Descendants() {
		node, ok := element.(*twigsyntax.Node)
		if !ok {
			continue
		}

		block, ok := twigast.CastTwigBlock(node)
		if !ok {
			continue
		}

		name := block.Name()
		if name == nil {
			continue
		}

		blockRange := node.RangeTrimmedTrivia()
		blockText := source[blockRange.Start:blockRange.End]
		var versionComment *TwigVersionComment
		var versionCommentRange *cst.TextRange
		hasVersioningComment := false
		deprecation := BlockDeprecation(node, source)
		if previous := findPreviousBlockComment(node); previous != nil {
			commentRange := previous.RangeTrimmedTrivia()
			line, _ := lineIndex.Position(commentRange.Start)
			comment := source[commentRange.Start:commentRange.End]
			hasVersioningComment = strings.Contains(comment, VersionCommentPrefix)
			if hasVersioningComment {
				copyRange := commentRange
				versionCommentRange = &copyRange
			}
			versionComment = ParseVersionComment(comment, int(line)+1)
			if versionComment != nil {
				versionComment.Range = commentRange
			}
		}

		line, _ := lineIndex.Position(name.Range().Start)
		file.Blocks[name.Text()] = TwigBlock{
			Name:                 name.Text(),
			Range:                blockRange,
			NameRange:            name.Range(),
			Line:                 int(line) + 1,
			Hash:                 calculateBlockHash(blockText),
			Text:                 blockText,
			HasVersioningComment: hasVersioningComment,
			VersionCommentRange:  versionCommentRange,
			VersionComment:       versionComment,
			Deprecation:          deprecation,
		}
	}
}

// BlockDeprecation returns normalized @deprecated documentation from the Twig
// comment immediately preceding a block declaration. Administration component
// templates and Storefront block versioning share this source convention.
func BlockDeprecation(blockNode *twigsyntax.Node, source string) string {
	previous := findPreviousBlockComment(blockNode)
	if previous == nil {
		return ""
	}
	commentRange := previous.RangeTrimmedTrivia()
	if commentRange.End > uint32(len(source)) {
		return ""
	}
	return parseBlockDeprecation(source[commentRange.Start:commentRange.End])
}

func findPreviousBlockComment(blockNode *twigsyntax.Node) *twigsyntax.Node {
	for sibling := blockNode.PrevSibling(); sibling != nil; {
		switch previous := sibling.(type) {
		case *twigsyntax.Token:
			sibling = previous.PrevSibling()
		case *twigsyntax.Node:
			if previous.Kind() == twigsyntax.TwigBlock {
				return nil
			}
			if previous.Kind() == twigsyntax.TwigComment {
				return previous
			}
			sibling = previous.PrevSibling()
		}
	}

	return nil
}

func parseBlockDeprecation(comment string) string {
	lower := strings.ToLower(comment)
	index := strings.Index(lower, "@deprecated")
	if index < 0 {
		return ""
	}
	message := strings.TrimSpace(comment[index+len("@deprecated"):])
	message = strings.TrimSpace(strings.TrimSuffix(message, "#}"))
	if message == "" {
		return "This Twig block is deprecated"
	}
	return message
}

func ParseTwig(filePath string, content []byte) (*TwigFile, error) {
	if !bytes.Contains(content, []byte("{%")) {
		return newTwigFile(filePath), nil
	}
	result := twigparser.Parse(string(content))
	return ParseTwigTree(filePath, result.Tree, twigsyntax.NewLineIndex(result.Tree.Source))
}

func newTwigFile(filePath string) *TwigFile {
	return &TwigFile{
		Path:       filePath,
		BundleName: getBundleNameByPath(filePath),
		RelPath:    ConvertToRelativePath(filePath),
		Blocks:     make(map[string]TwigBlock),
	}
}

func ParseTwigTree(filePath string, tree *twigsyntax.Tree, lineIndex *twigsyntax.LineIndex) (*TwigFile, error) {
	file := newTwigFile(filePath)
	if tree == nil || tree.Root == nil || !strings.Contains(tree.Source, "{%") {
		return file, nil
	}
	root := tree.Root

	// Find all blocks recursively
	if strings.Contains(tree.Source, "block") {
		findBlocks(root, tree.Source, lineIndex, file)
	}

	// Find extends tag
	if !strings.Contains(tree.Source, "extends") && !strings.Contains(tree.Source, "sw_extends") {
		return file, nil
	}

	for _, reference := range TwigTemplateReferences(filePath, root) {
		if reference.Kind != TemplateExtendsReference {
			continue
		}
		file.ExtendsFile = reference.Template
		line, _ := lineIndex.Position(reference.Range.Start)
		file.ExtendsTagLine = int(line) + 1
		return file, nil
	}

	return file, nil
}
