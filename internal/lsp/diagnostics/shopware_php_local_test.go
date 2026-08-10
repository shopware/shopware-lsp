package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/require"
)

func TestShopwarePHPLocalAnalyzerReportsForbiddenLanguageConstructs(t *testing.T) {
	document, problems := analyzeShopwarePHPLocal(t, `<?php
$_GET['name'];
$_POST;
$_FILES;
$_REQUEST;
$_SERVER;
/** @phpstan-ignore-next-line shopware.php.superglobal */
$_GET['ignored'];

var_dump($value);
\dump($value);
dd($value);
exit;
die('failed');
\App\dump($value);
$debugger->dump($value);

session_start();
\session_destroy();
session_write_close();

glob('*.php', GLOB_BRACE);
Foo::GLOB_BRACE;
GLOB_BRACE();
`)

	require.Equal(t, map[lsp.DiagnosticID]int{
		ShopwarePHPSuperglobalCode:        4,
		ShopwarePHPDisallowedFunctionCode: 5,
		ShopwarePHPSessionFunctionCode:    3,
		ShopwarePHPGlobBraceCode:          1,
	}, problemCounts(problems))
	for _, problem := range problems {
		highlighted := document.Source[problem.Range.Start:problem.Range.End]
		require.NotEmpty(t, highlighted)
		require.NotContains(t, highlighted, ";")
	}
}

func TestShopwarePHPLocalAnalyzerReportsDisabledTLSVerification(t *testing.T) {
	document, problems := analyzeShopwarePHPLocal(t, `<?php
curl_setopt($handle, CURLOPT_SSL_VERIFYPEER, false);
curl_setopt(handle: $handle, option: CURLOPT_SSL_VERIFYHOST, value: 1);
curl_setopt_array($handle, [
    CURLOPT_SSL_VERIFYPEER => 0,
]);
stream_context_create([
    'ssl' => [
        'verify_peer' => false,
        'verify_peer_name' => 0,
    ],
]);

curl_setopt($handle, CURLOPT_SSL_VERIFYPEER, true);
curl_setopt($handle, CURLOPT_SSL_VERIFYHOST, 2);
curl_setopt($handle, CURLOPT_SSL_VERIFYPEER, $verifyPeer);
curl_setopt_array($handle, [CURLOPT_TIMEOUT => 5]);
stream_context_create(['ssl' => ['verify_peer' => true]]);
`)

	require.Equal(t, map[lsp.DiagnosticID]int{
		ShopwarePHPTLSVerificationCode: 4,
	}, problemCounts(problems), problemHighlights(document, problems))
}

func TestShopwarePHPLocalAnalyzerReportsCryptoAndCookieProblems(t *testing.T) {
	_, problems := analyzeShopwarePHPLocal(t, `<?php
crypt($password, 'fixed-salt');
password_hash($password, PASSWORD_DEFAULT, ['salt' => random_bytes(16)]);
openssl_pkey_new(['private_key_bits' => 1024]);

setcookie('first', 'value');
setrawcookie('second', 'value', ['path' => '/']);
setcookie('third', 'value', ['secure' => false]);
setcookie(name: 'fourth', value: 'value', secure: false);

crypt($password, $randomSalt);
password_hash($password, PASSWORD_DEFAULT);
openssl_pkey_new(['private_key_bits' => 2048]);
setcookie('secure-options', 'value', ['secure' => true]);
setcookie('secure-legacy', 'value', 0, '/', 'example.com', true);
setcookie('dynamic-options', 'value', $options);
setcookie(name: 'dynamic-secure', value: 'value', secure: $secure);
`)

	require.Equal(t, map[lsp.DiagnosticID]int{
		ShopwarePHPPredictableSaltCode: 2,
		ShopwarePHPWeakKeyCode:         1,
		ShopwarePHPInsecureCookieCode:  4,
	}, problemCounts(problems))
}

func TestShopwarePHPLocalAnalyzerScopesForeignKeyCheckDiagnostic(t *testing.T) {
	document, problems := analyzeShopwarePHPLocal(t, `<?php
namespace App;

use Shopware\Core\Framework\Migration\MigrationStep as BaseMigration;

final class Migration extends BaseMigration
{
    public function update(): void
    {
        $connection->executeStatement('SET FOREIGN_KEY_CHECKS = 0');
        $current = 'SELECT @@FOREIGN_KEY_CHECKS';
        $connection->executeStatement('SET FOREIGN_KEY_CHECKS = 1');
    }

    public function updateDestructive(): void
    {
        $connection->executeStatement('SET FOREIGN_KEY_CHECKS = 0');
    }
}

final class Extension extends \Shopware\Core\Framework\Plugin
{
    public function update(): void
    {
        $connection->executeStatement('set foreign_key_checks=0');
    }
}

final class Unrelated extends LocalMigration
{
    public function update(): void
    {
        $connection->executeStatement('SET FOREIGN_KEY_CHECKS = 0');
    }
}
`)

	require.Equal(t, map[lsp.DiagnosticID]int{
		ShopwarePHPForeignKeyChecksCode: 2,
	}, problemCounts(problems))
	for _, problem := range problems {
		highlighted := document.Source[problem.Range.Start:problem.Range.End]
		require.Contains(t, []string{
			"FOREIGN_KEY_CHECKS = 0",
			"foreign_key_checks=0",
		}, highlighted)
	}
}

func analyzeShopwarePHPLocal(
	t *testing.T,
	source string,
) (*lsp.TextDocument, []lsp.Problem) {
	t.Helper()
	document := lsp.NewTextDocument("file:///LocalRules.php", source, 1)
	problems, err := NewShopwarePHPLocalAnalyzer().Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	return document, problems
}

func problemCounts(problems []lsp.Problem) map[lsp.DiagnosticID]int {
	result := make(map[lsp.DiagnosticID]int)
	for _, problem := range problems {
		result[problem.ID]++
	}
	return result
}

func problemHighlights(
	document *lsp.TextDocument,
	problems []lsp.Problem,
) []string {
	result := make([]string, 0, len(problems))
	for _, problem := range problems {
		result = append(result, document.Source[problem.Range.Start:problem.Range.End])
	}
	return result
}
