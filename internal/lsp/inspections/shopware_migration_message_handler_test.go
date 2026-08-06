package inspections

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/require"
)

func TestMessageHandlerSubscriberMigrationQuickFix(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Handler.php",
		`<?php
namespace App;

use Shopware\Core\Framework\MessageQueue\Handler\AbstractMessageHandler;

class Handler extends AbstractMessageHandler
{
    public static function getHandledMessages(): iterable
    {
        return [Message::class];
    }

    public function handle(Message $message): void
    {
    }
}
`,
		1,
	)
	updated := applyOnlyMigrationFix(
		t,
		NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
		document,
		messageHandlerSubscriberFixID,
	)
	require.Contains(t, updated, "use Symfony\\Component\\Messenger\\Attribute\\AsMessageHandler;")
	require.Contains(t, updated, "use Symfony\\Component\\Messenger\\Handler\\MessageSubscriberInterface;")
	require.Contains(t, updated, "#[AsMessageHandler]")
	require.Contains(t, updated, "class Handler implements MessageSubscriberInterface")
	require.Contains(t, updated, "public function __invoke(Message $message): void")
	require.NotContains(t, updated, "extends AbstractMessageHandler")
	require.NotContains(t, updated, "getHandledMessages")
	require.NotContains(t, updated, "function handle")
}

func TestMessageHandlerSubscriberMigrationQuickFixWithoutHandlerMethod(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/AbstractHandler.php",
		`<?php
namespace App;

use Shopware\Core\Framework\MessageQueue\Handler\AbstractMessageHandler;

abstract class Handler extends AbstractMessageHandler
{
}
`,
		1,
	)
	updated := applyOnlyMigrationFix(
		t,
		NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
		document,
		messageHandlerSubscriberFixID,
	)
	require.Contains(t, updated, "#[AsMessageHandler]")
	require.Contains(t, updated, "abstract class Handler implements MessageSubscriberInterface")
	require.NotContains(t, updated, "extends AbstractMessageHandler")
}
