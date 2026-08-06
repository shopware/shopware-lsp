package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageHandlerSubscriberMigrationDetectsSafeClasses(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Handlers.php",
		`<?php
use Shopware\Core\Framework\MessageQueue\Handler\AbstractMessageHandler;

class Handler extends AbstractMessageHandler
{
    public static function getHandledMessages(): iterable { return [Message::class]; }
    public function handle(Message $message): void {}
}
class ConflictingHandler extends AbstractMessageHandler
{
    public function handle(Message $message): void {}
    public function __invoke(Message $message): void {}
}
class IncompleteHandler extends AbstractMessageHandler {}
class UnrelatedHandler extends OtherHandler
{
    public function handle(Message $message): void {}
}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 5, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 3)
	assert.Equal(t, messageHandlerSubscriberCode, problems[0].ID)
	assert.True(t, problems[0].Payload.(ShopwareMigrationPayload).Safe)
	assert.False(t, problems[1].Payload.(ShopwareMigrationPayload).Safe)
	assert.True(t, problems[2].Payload.(ShopwareMigrationPayload).Safe)

	problems, err = NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 4, 20),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, problems)
}
