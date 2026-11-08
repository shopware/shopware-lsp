use std::sync::Arc;

use swlsp_lsp::{Problem, TextDocument};

use super::{analyze, counts, highlight, highlights, problem_counts};
use crate::shopware::*;
use crate::test_support::document;

fn analyze_local(source: &str) -> (Arc<TextDocument>, Vec<Problem>) {
    let document = document("file:///LocalRules.php", source);
    let problems = analyze(&ShopwarePhpLocalAnalyzer::new(), &document);
    (document, problems)
}

#[test]
fn shopware_php_local_analyzer_reports_forbidden_language_constructs() {
    let (document, problems) = analyze_local(
        r#"<?php
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
"#,
    );
    assert_eq!(
        problem_counts(&problems),
        counts(&[
            (SHOPWARE_PHP_SUPERGLOBAL_CODE, 4),
            (SHOPWARE_PHP_DISALLOWED_FUNCTION_CODE, 5),
            (SHOPWARE_PHP_SESSION_FUNCTION_CODE, 3),
            (SHOPWARE_PHP_GLOB_BRACE_CODE, 1),
        ])
    );
    for problem in &problems {
        let highlighted = highlight(&document, problem);
        assert!(!highlighted.is_empty());
        assert!(!highlighted.contains(';'));
    }
}

#[test]
fn shopware_php_local_analyzer_reports_disabled_tls_verification() {
    let (document, problems) = analyze_local(
        r#"<?php
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
"#,
    );
    assert_eq!(
        problem_counts(&problems),
        counts(&[(SHOPWARE_PHP_TLS_VERIFICATION_CODE, 4)]),
        "{:?}",
        highlights(&document, &problems)
    );
}

#[test]
fn shopware_php_local_analyzer_reports_crypto_and_cookie_problems() {
    let (_, problems) = analyze_local(
        r#"<?php
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
"#,
    );
    assert_eq!(
        problem_counts(&problems),
        counts(&[
            (SHOPWARE_PHP_PREDICTABLE_SALT_CODE, 2),
            (SHOPWARE_PHP_WEAK_KEY_CODE, 1),
            (SHOPWARE_PHP_INSECURE_COOKIE_CODE, 4),
        ])
    );
}

#[test]
fn shopware_php_local_analyzer_scopes_foreign_key_check_diagnostic() {
    let (document, problems) = analyze_local(
        r#"<?php
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
"#,
    );
    assert_eq!(
        problem_counts(&problems),
        counts(&[(SHOPWARE_PHP_FOREIGN_KEY_CHECKS_CODE, 2)])
    );
    for problem in &problems {
        let highlighted = highlight(&document, problem);
        assert!(
            ["FOREIGN_KEY_CHECKS = 0", "foreign_key_checks=0"].contains(&highlighted),
            "{highlighted}"
        );
    }
}
