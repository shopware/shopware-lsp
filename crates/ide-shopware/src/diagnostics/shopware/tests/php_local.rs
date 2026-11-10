use std::sync::Arc;

use swlsp_lsp::{Problem, TextDocument};

use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

fn analyze_local(fixture: &str) -> (Arc<TextDocument>, Vec<Problem>) {
    check_problems(
        &ShopwarePhpLocalAnalyzer::new(),
        "file:///LocalRules.php",
        fixture,
    )
}

#[test]
fn shopware_php_local_analyzer_reports_forbidden_language_constructs() {
    analyze_local(
        r#"<?php
{|shopware.php.superglobal:$_GET|}['name'];
{|shopware.php.superglobal:$_POST|};
{|shopware.php.superglobal:$_FILES|};
{|shopware.php.superglobal:$_REQUEST|};
$_SERVER;

{|shopware.php.function.disallowed:var_dump|}($value);
{|shopware.php.function.disallowed:\dump|}($value);
{|shopware.php.function.disallowed:dd|}($value);
{|shopware.php.function.disallowed:exit|};
{|shopware.php.function.disallowed:die|}('failed');
\App\dump($value);
$debugger->dump($value);

{|shopware.php.session.function:session_start|}();
{|shopware.php.session.function:\session_destroy|}();
{|shopware.php.session.function:session_write_close|}();

glob('*.php', {|shopware.php.glob_brace:GLOB_BRACE|});
Foo::GLOB_BRACE;
GLOB_BRACE();
"#,
    );
}

#[test]
fn shopware_php_local_analyzer_reports_disabled_tls_verification() {
    analyze_local(
        r#"<?php
curl_setopt($handle, CURLOPT_SSL_VERIFYPEER, {|shopware.php.tls.verification_disabled:false|});
curl_setopt(handle: $handle, option: CURLOPT_SSL_VERIFYHOST, value: {|shopware.php.tls.verification_disabled:1|});
curl_setopt_array($handle, [
    CURLOPT_SSL_VERIFYPEER => {|shopware.php.tls.verification_disabled:0|},
]);
stream_context_create([
    'ssl' => [
        'verify_peer' => {|shopware.php.tls.verification_disabled:false|},
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
}

#[test]
fn shopware_php_local_analyzer_reports_crypto_and_cookie_problems() {
    analyze_local(
        r#"<?php
crypt($password, {|shopware.php.crypto.predictable_salt:'fixed-salt'|});
password_hash($password, PASSWORD_DEFAULT, ['{|shopware.php.crypto.predictable_salt:salt|}' => random_bytes(16)]);
openssl_pkey_new(['private_key_bits' => {|shopware.php.crypto.weak_key:1024|}]);

{|shopware.php.cookie.insecure:setcookie|}('first', 'value');
setrawcookie('second', 'value', {|shopware.php.cookie.insecure:['path' => '/']|});
setcookie('third', 'value', ['secure' => {|shopware.php.cookie.insecure:false|}]);
setcookie(name: 'fourth', value: 'value', secure: {|shopware.php.cookie.insecure:false|});

crypt($password, $randomSalt);
password_hash($password, PASSWORD_DEFAULT);
openssl_pkey_new(['private_key_bits' => 2048]);
setcookie('secure-options', 'value', ['secure' => true]);
setcookie('secure-legacy', 'value', 0, '/', 'example.com', true);
setcookie('dynamic-options', 'value', $options);
setcookie(name: 'dynamic-secure', value: 'value', secure: $secure);
"#,
    );
}

#[test]
fn shopware_php_local_analyzer_scopes_foreign_key_check_diagnostic() {
    analyze_local(
        r#"<?php
namespace App;

use Shopware\Core\Framework\Migration\MigrationStep as BaseMigration;

final class Migration extends BaseMigration
{
    public function update(): void
    {
        $connection->executeStatement('SET {|shopware.php.foreign_key_checks.disabled:FOREIGN_KEY_CHECKS = 0|}');
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
        $connection->executeStatement('set {|shopware.php.foreign_key_checks.disabled:foreign_key_checks=0|}');
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
}
