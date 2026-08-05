package cli

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

var (
	lineColumnSuffix = regexp.MustCompile(`^(.*):(\d+):(\d+)$`)
	lineSuffix       = regexp.MustCompile(`^(.*):(\d+)$`)
)

type positionTarget struct {
	Path     string
	Position protocol.Position
}

func parsePositionTarget(value string) (positionTarget, error) {
	if value == "" {
		return positionTarget{}, fmt.Errorf("empty file position")
	}
	target := positionTarget{Path: value}
	match := lineColumnSuffix.FindStringSubmatch(value)
	if len(match) == 0 {
		match = lineSuffix.FindStringSubmatch(value)
	}
	if len(match) == 0 {
		return target, nil
	}
	line, err := strconv.Atoi(match[2])
	if err != nil || line < 1 {
		return positionTarget{}, fmt.Errorf("line must be at least 1")
	}
	column := 1
	if len(match) > 3 && match[3] != "" {
		column, err = strconv.Atoi(match[3])
		if err != nil || column < 1 {
			return positionTarget{}, fmt.Errorf("column must be at least 1")
		}
	}
	target.Path = match[1]
	target.Position = protocol.Position{Line: line - 1, Character: column - 1}
	return target, nil
}

func requireOneArgument(args []string, usage string) (string, error) {
	if len(args) != 1 {
		return "", usageError(usage)
	}
	return args[0], nil
}
