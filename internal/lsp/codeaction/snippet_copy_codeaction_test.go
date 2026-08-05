package codeaction

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnippetCopyCodeActionBuildsNestedKey(t *testing.T) {
	request := symfonyGeneratorCodeActionRequest(
		"file:///project/src/Resources/snippet/en-GB/messages.json",
		`{"checkout":{"finish":{"button":"Complete order"}}}`,
		"Complete order",
	)
	actions := NewSnippetCopyCodeActionProvider().GetCodeActions(
		context.Background(),
		request,
	)
	require.Len(t, actions, 1)
	require.NotNil(t, actions[0].Command)
	assert.Equal(t, copySnippetUsageAction, actions[0].Command.Command)
	assert.Equal(
		t,
		"checkout.finish.button",
		actions[0].Command.Arguments[0],
	)
}
