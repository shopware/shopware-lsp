const assert = require('node:assert/strict');
const {test} = require('node:test');
const {
  pathWithinRoot,
  selectOutermostWorkspaceRoots,
  workspaceRootForPath,
} = require('../dist/workspaceRoots.js');

const root = (key, fsPath, enabled = true) => ({key, fsPath, enabled});

test('selects every disjoint enabled workspace root', () => {
  assert.deepEqual(
    selectOutermostWorkspaceRoots([
      root('storefront', '/workspace/storefront'),
      root('administration', '/workspace/administration'),
      root('disabled', '/workspace/disabled', false),
    ]).map(item => item.key),
    ['administration', 'storefront'],
  );
});

test('keeps the outermost enabled root for overlapping workspaces', () => {
  assert.deepEqual(
    selectOutermostWorkspaceRoots([
      root('plugin', '/workspace/shop/custom/plugins/Example'),
      root('shop', '/workspace/shop'),
    ]).map(item => item.key),
    ['shop'],
  );
});

test('allows an enabled child when its parent is inactive', () => {
  assert.deepEqual(
    selectOutermostWorkspaceRoots([
      root('shop', '/workspace/shop', false),
      root('plugin', '/workspace/shop/custom/plugins/Example'),
    ]).map(item => item.key),
    ['plugin'],
  );
});

test('routes nested paths to their most specific running owner', () => {
  const roots = [
    root('shop', '/workspace/shop'),
    root('other', '/workspace/other'),
  ];
  assert.equal(
    workspaceRootForPath(roots, '/workspace/shop/custom/plugins/Example/src/Test.php')?.key,
    'shop',
  );
  assert.equal(workspaceRootForPath(roots, '/workspace/unknown/Test.php'), undefined);
  assert.equal(pathWithinRoot('/workspace/shop', '/workspace/shopware/Test.php'), false);
});
