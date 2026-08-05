package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

func main() {
	lang := flag.String("lang", "", "Language to parse (php, js, twig, vue, json, yaml, scss, xml). Auto-detected from extension if not specified.")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Println("Usage: go run cmd/debug_ast/main.go [-lang=php|js|twig|vue|json|yaml|scss|xml] <file_path>")
		fmt.Println("       go run cmd/debug_ast/main.go [-lang=php|js|twig|vue|json|yaml|scss|xml] - < input.txt")
		fmt.Println("")
		fmt.Println("Options:")
		fmt.Println("  -lang    Language to parse (php, js, twig, vue, json, yaml, scss, xml)")
		fmt.Println("           Auto-detected from file extension if not specified")
		fmt.Println("")
		fmt.Println("Examples:")
		fmt.Println("  go run cmd/debug_ast/main.go example.php")
		fmt.Println("  go run cmd/debug_ast/main.go -lang=js example.vue")
		fmt.Println("  echo \"this.\\$tc('key')\" | go run cmd/debug_ast/main.go -lang=js -")
		os.Exit(1)
	}

	filePath := args[0]

	var fileContent []byte
	var err error

	if filePath == "-" {
		// Read from stdin
		fileContent, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Printf("Error reading stdin: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Analyzing AST from stdin")
	} else {
		fileContent, err = os.ReadFile(filePath)
		if err != nil {
			fmt.Printf("Error reading file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Analyzing AST for file: %s\n\n", filePath)
	}

	// Determine language
	language := *lang
	if language == "" && filePath != "-" {
		language = detectLanguage(filePath)
	}

	if language == "" {
		fmt.Println("Error: Could not detect language. Please specify -lang flag.")
		os.Exit(1)
	}

	id := normalizeLanguageID(language)
	definition, ok := languageRegistry().ByID(id)
	if !ok {
		fmt.Printf("Error: Unsupported language '%s'. Supported: php, js, twig, vue, json, yaml, scss, xml\n", language)
		os.Exit(1)
	}

	result := definition.Parse(string(fileContent))
	fmt.Printf("Language: %s\n", definition.ID)
	fmt.Printf("Content:\n---\n%s\n---\n\n", string(fileContent))
	fmt.Println(cst.DebugTree(result.Tree.Root))
	for i := range result.Errors {
		fmt.Fprintln(os.Stderr, result.Errors[i].String())
	}
}

func detectLanguage(filePath string) string {
	definition, ok := languageRegistry().ForPath(filePath)
	if !ok {
		return ""
	}
	return string(definition.ID)
}

func normalizeLanguageID(value string) language.ID {
	switch strings.ToLower(value) {
	case "js":
		return language.JavaScript
	case "yml":
		return language.YAML
	default:
		return language.ID(strings.ToLower(value))
	}
}

func languageRegistry() *language.Registry {
	return language.DefaultRegistry()
}
