package php

import (
	"fmt"
	"os"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

// DebugAST parses a PHP file and prints the pure-Go CST structure.
func DebugAST(filePath string) {
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return
	}
	result := phpparser.ParseBytes(fileContent)
	fmt.Println(phpsyntax.DebugTree(result.Tree.Root))
	for _, diagnostic := range result.Errors {
		fmt.Fprintln(os.Stderr, diagnostic.String())
	}
}
