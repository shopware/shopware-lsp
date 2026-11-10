const assert = require('node:assert/strict');
const {test} = require('node:test');
const {describeClearReport, formatBytes} = require('../dist/cacheReport.js');

test('formats byte counts in binary units', () => {
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(1023), '1023 B');
  assert.equal(formatBytes(1536), '1.5 KiB');
  assert.equal(formatBytes(300 * 1024 * 1024), '300.0 MiB');
});

test('describes what clearing caches did', () => {
  assert.equal(
    describeClearReport({cleared: [{}, {}], inUse: [], freedBytes: 2048}),
    'Shopware LSP: Cleared 2 caches, 2.0 KiB freed.',
  );
  assert.equal(
    describeClearReport({cleared: [{}], inUse: [{}], freedBytes: 10}),
    'Shopware LSP: Cleared 1 cache, 10 B freed. 1 cache in use by a running server kept.',
  );
  assert.equal(
    describeClearReport({cleared: [], inUse: [], freedBytes: 0}),
    'Shopware LSP: No caches to clear.',
  );
});
