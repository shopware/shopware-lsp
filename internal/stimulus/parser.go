package stimulus

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	jsonquery "github.com/shopware/shopware-lsp/internal/parser/json/query"
	jsonsyntax "github.com/shopware/shopware-lsp/internal/parser/json/syntax"
)

var (
	stimulusClassPattern = regexp.MustCompile(
		`\bclass(?:\s+[A-Za-z_$][A-Za-z0-9_$]*)?\s+extends\s+Controller\b`,
	)
	stimulusAppPattern = regexp.MustCompile(
		`\b(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*startStimulusApp\s*\(`,
	)
)

func ControllersInJavaScript(
	path string,
	root *jssyntax.Node,
	source string,
) []Controller {
	if root == nil {
		return nil
	}
	var result []Controller
	seen := make(map[string]struct{})
	for _, match := range stimulusAppPattern.FindAllStringSubmatch(
		source,
		-1,
	) {
		if len(match) < 2 {
			continue
		}
		for call := range jsquery.IterateCalls(root, match[1]+".register") {
			if jsquery.Argument(call, 1) == nil {
				continue
			}
			literal := jsquery.StringArgument(call, 0)
			name := jsquery.StringValue(literal)
			addController(&result, seen, Controller{
				Name:   NormalizeName(name),
				File:   path,
				Range:  javascriptStringRange(literal),
				Source: RegisteredSource,
			})
		}
	}

	module := jsquery.ImportPath(root, "Controller")
	if module != "@hotwired/stimulus" && module != "stimulus" ||
		!hasStimulusControllerClass(root) {
		return result
	}
	name, found := controllerNameFromPath(path)
	if !found {
		return result
	}
	addController(&result, seen, Controller{
		Name:   name,
		File:   path,
		Source: JavaScriptSource,
	})
	return result
}

func ControllersInJSON(
	path string,
	root *jsonsyntax.Node,
) []Controller {
	if root == nil ||
		!strings.EqualFold(filepath.Base(path), "controllers.json") {
		return nil
	}
	top := jsonquery.RootValue(root)
	controllers := jsonquery.Property(top, "controllers")
	if controllers == nil || controllers.Kind() != jsonsyntax.JsonObject {
		return nil
	}
	var result []Controller
	seen := make(map[string]struct{})
	for _, packagePair := range jsonquery.Pairs(controllers) {
		packageName := jsonquery.StringValue(
			jsonquery.PairKey(packagePair),
		)
		packageObject := jsonquery.PairValue(packagePair)
		if packageName == "" || packageObject == nil ||
			packageObject.Kind() != jsonsyntax.JsonObject {
			continue
		}
		for _, controllerPair := range jsonquery.Pairs(packageObject) {
			controllerName := jsonquery.StringValue(
				jsonquery.PairKey(controllerPair),
			)
			config := jsonquery.PairValue(controllerPair)
			if controllerName == "" || config == nil ||
				config.Kind() != jsonsyntax.JsonObject ||
				jsonControllerDisabled(config) {
				continue
			}
			original := packageName + "/" + controllerName
			addController(&result, seen, Controller{
				Name:         NormalizeName(original),
				OriginalName: original,
				File:         path,
				Range: jsonStringRange(
					jsonquery.PairKey(controllerPair),
				),
				Source: ControllersJSONSource,
			})
		}
	}
	return result
}

func hasStimulusControllerClass(root *jssyntax.Node) bool {
	for _, function := range jsquery.Nodes(root, jssyntax.JsFunction) {
		if stimulusClassPattern.MatchString(function.Text()) {
			return true
		}
	}
	return false
}

func controllerNameFromPath(path string) (string, bool) {
	base := strings.TrimSuffix(
		filepath.Base(path),
		filepath.Ext(path),
	)
	switch {
	case strings.HasSuffix(base, "_controller"):
		base = strings.TrimSuffix(base, "_controller")
	case strings.HasSuffix(base, "-controller"):
		base = strings.TrimSuffix(base, "-controller")
	default:
		return "", false
	}
	base = strings.ReplaceAll(base, "_", "-")
	var namespace []string
	parent := filepath.Dir(path)
	for level := 0; level < 3; level++ {
		name := filepath.Base(parent)
		if name == "." || name == string(filepath.Separator) ||
			strings.EqualFold(name, "controllers") {
			break
		}
		namespace = append(
			[]string{strings.ReplaceAll(name, "_", "-")},
			namespace...,
		)
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	if len(namespace) != 0 {
		return strings.Join(namespace, "--") + "--" + base, true
	}
	return base, base != ""
}

func jsonControllerDisabled(config *jsonsyntax.Node) bool {
	enabled := jsonquery.Property(config, "enabled")
	value, found := jsonquery.BooleanValue(enabled)
	return found && !value
}

func addController(
	result *[]Controller,
	seen map[string]struct{},
	controller Controller,
) {
	if controller.Name == "" {
		return
	}
	key := strings.ToLower(controller.Name)
	if _, duplicate := seen[key]; duplicate {
		return
	}
	seen[key] = struct{}{}
	*result = append(*result, controller)
}

func javascriptStringRange(node *jssyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	return unquotedRange(node.RangeTrimmedTrivia(), node.Text())
}

func jsonStringRange(node *jsonsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	return unquotedRange(node.RangeTrimmedTrivia(), node.Text())
}

func unquotedRange(rng cst.TextRange, text string) cst.TextRange {
	text = strings.TrimSpace(text)
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"' || text[0] == '`') &&
		text[len(text)-1] == text[0] {
		rng.Start++
		rng.End--
	}
	return rng
}
