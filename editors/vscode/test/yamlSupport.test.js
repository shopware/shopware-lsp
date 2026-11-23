const assert = require('node:assert/strict');
const {test} = require('node:test');
const {
  hasShopwareSchema,
  YAML_EXTENSION_ID,
} = require('../dist/yamlSupportModel.js');

test('knows the files the server ships a schema for', () => {
  assert.equal(hasShopwareSchema('/project/.config/shopware-lsp.yaml'), true);
  assert.equal(
    hasShopwareSchema('/project/custom/plugins/Acme/src/Resources/shopware-lsp/admin-modules.yaml'),
    true,
  );
  assert.equal(
    hasShopwareSchema('C:\\project\\src\\Resources\\shopware-lsp\\admin-modules.yaml'),
    true,
  );
});

test('leaves every other YAML file alone', () => {
  assert.equal(hasShopwareSchema('/project/config/services.yaml'), false);
  assert.equal(hasShopwareSchema('/project/config/shopware-lsp.yaml'), false);
  assert.equal(hasShopwareSchema('/project/src/Resources/config/admin-modules.yaml'), false);
});

test('suggests the extension the schemas are contributed to', () => {
  assert.equal(YAML_EXTENSION_ID, 'redhat.vscode-yaml');
});
