package phprewrite

import (
	"testing"

	"github.com/stretchr/testify/require"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

func TestArgumentListInsertAppendAndRemove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		source   string
		edit     func(*Editor, *phpsyntax.Node) error
		expected string
	}{
		{
			name:   "insert middle",
			source: `<?php foo($first, named: $last);`,
			edit: func(editor *Editor, call *phpsyntax.Node) error {
				return editor.InsertArgument(call, 1, "$middle")
			},
			expected: `<?php foo($first, $middle, named: $last);`,
		},
		{
			name:   "append after trailing comma",
			source: `<?php foo($first,);`,
			edit: func(editor *Editor, call *phpsyntax.Node) error {
				return editor.AppendArgument(call, "$last")
			},
			expected: `<?php foo($first, $last);`,
		},
		{
			name:   "comment comma is not a separator",
			source: `<?php foo($first /* comma, in comment */);`,
			edit: func(editor *Editor, call *phpsyntax.Node) error {
				return editor.AppendArgument(call, "$last")
			},
			expected: `<?php foo($first /* comma, in comment */, $last);`,
		},
		{
			name: "append multiline",
			source: `<?php
foo(
    $first
);`,
			edit: func(editor *Editor, call *phpsyntax.Node) error {
				return editor.AppendArgument(call, "$last")
			},
			expected: `<?php
foo(
    $first,
    $last,
);`,
		},
		{
			name: "insert multiline first",
			source: `<?php
foo(
    $last,
);`,
			edit: func(editor *Editor, call *phpsyntax.Node) error {
				return editor.InsertArgument(call, 0, "$first")
			},
			expected: `<?php
foo(
    $first,
    $last,
);`,
		},
		{
			name:   "remove middle",
			source: `<?php foo($first, $middle, $last);`,
			edit: func(editor *Editor, call *phpsyntax.Node) error {
				return editor.RemoveArgument(call, 1)
			},
			expected: `<?php foo($first, $last);`,
		},
		{
			name: "remove multiline last",
			source: `<?php
foo(
    $first,
    $last,
);`,
			edit: func(editor *Editor, call *phpsyntax.Node) error {
				return editor.RemoveArgument(call, 1)
			},
			expected: `<?php
foo(
    $first,
);`,
		},
		{
			name:   "remove only",
			source: `<?php foo($only);`,
			edit: func(editor *Editor, call *phpsyntax.Node) error {
				return editor.RemoveArgument(call, 0)
			},
			expected: `<?php foo();`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			editor, root := testEditor(t, test.source)
			calls := phpquery.Nodes(root, phpsyntax.PhpFunctionCall)
			require.Len(t, calls, 1)
			require.NoError(t, test.edit(editor, calls[0]))
			require.Equal(t, test.expected, applyTestEditor(t, test.source, editor))
		})
	}
}

func TestParameterListInsertAppendAndRemove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		edit     func(*Editor, *phpsyntax.Node) error
		expected string
	}{
		{
			name: "insert",
			edit: func(editor *Editor, method *phpsyntax.Node) error {
				return editor.InsertParameter(method, 1, "LoggerInterface $logger")
			},
			expected: `<?php
final class Handler
{
    public function __construct(
        private Repository $repository,
        LoggerInterface $logger,
        bool $strict = false,
    ) {}
}`,
		},
		{
			name: "remove",
			edit: func(editor *Editor, method *phpsyntax.Node) error {
				return editor.RemoveParameter(method, 1)
			},
			expected: `<?php
final class Handler
{
    public function __construct(
        private Repository $repository,
    ) {}
}`,
		},
		{
			name: "append",
			edit: func(editor *Editor, method *phpsyntax.Node) error {
				return editor.AppendParameter(method, "array $options = []")
			},
			expected: `<?php
final class Handler
{
    public function __construct(
        private Repository $repository,
        bool $strict = false,
        array $options = [],
    ) {}
}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := `<?php
final class Handler
{
    public function __construct(
        private Repository $repository,
        bool $strict = false,
    ) {}
}`
			editor, root := testEditor(t, source)
			method := phpquery.Methods(phpquery.Classes(root)[0])[0]
			require.NoError(t, test.edit(editor, method))
			require.Equal(t, test.expected, applyTestEditor(t, source, editor))
		})
	}
}

func TestCommaListEditsRejectInvalidInput(t *testing.T) {
	t.Parallel()
	source := `<?php foo($value);`
	editor, root := testEditor(t, source)
	call := phpquery.Nodes(root, phpsyntax.PhpFunctionCall)[0]
	require.Error(t, editor.InsertArgument(call, 2, "$other"))
	require.Error(t, editor.AppendArgument(call, " "))
	require.Error(t, editor.RemoveArgument(call, -1))

	commented := `<?php foo($first, /* belongs to last */ $last);`
	commentEditor, commentRoot := testEditor(t, commented)
	commentCall := phpquery.Nodes(commentRoot, phpsyntax.PhpFunctionCall)[0]
	require.Error(t, commentEditor.InsertArgument(commentCall, 1, "$middle"))
	require.Error(t, commentEditor.RemoveArgument(commentCall, 0))
}
