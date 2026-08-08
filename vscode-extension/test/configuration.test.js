const assert = require('node:assert/strict');
const {test} = require('node:test');
const {
  diagnosticPattern,
  normalizeConfigurationPattern,
  setNested,
  upsertDiagnosticOverride,
} = require('../dist/configurationModel.js');

test('sets nested configuration values without replacing siblings', () => {
  const configuration = {
    version: 1,
    diagnostics: {enabled: true, rules: {'php.arguments': 'warning'}},
  };
  setNested(configuration, ['diagnostics', 'rules', 'php.returnType'], 'error');
  assert.deepEqual(configuration, {
    version: 1,
    diagnostics: {
      enabled: true,
      rules: {'php.arguments': 'warning', 'php.returnType': 'error'},
    },
  });
});

test('removes inherited overrides and prunes empty containers', () => {
  const configuration = {version: 1, features: {hover: false}};
  setNested(configuration, ['features', 'hover'], null);
  assert.deepEqual(configuration, {version: 1});
});

test('merges repeated diagnostic overrides for the same exact pattern', () => {
  const first = upsertDiagnosticOverride([], 'src\\Generated\\**', {
    rule: {id: 'php.arguments', severity: 'off'},
  });
  const second = upsertDiagnosticOverride(first, './src/Generated/**', {enabled: false});
  assert.deepEqual(second, [{
    files: ['src/Generated/**'],
    enabled: false,
    rules: {'php.arguments': 'off'},
  }]);
  assert.equal(normalizeConfigurationPattern('\\src\\Generated\\'), 'src/Generated');
});

test('appends a later override when an existing entry owns multiple patterns', () => {
  const result = upsertDiagnosticOverride([{
    files: ['src/**', 'tests/**'],
    enabled: false,
  }], 'src/**', {rule: {id: 'php.arguments', severity: 'off'}});
  assert.equal(result.length, 2);
  assert.deepEqual(result[1], {
    files: ['src/**'], rules: {'php.arguments': 'off'},
  });
});

test('creates portable file and recursive directory patterns', () => {
  assert.equal(diagnosticPattern('src/Service.php', false), 'src/Service.php');
  assert.equal(diagnosticPattern('src/Generated', true), 'src/Generated/**');
  assert.equal(diagnosticPattern('.', true), '**');
});
