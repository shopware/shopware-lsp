const assert = require('node:assert/strict');
const {test} = require('node:test');
const {setNested} = require('../dist/configurationModel.js');

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
