const assert = require('node:assert/strict');
const {test} = require('node:test');
const {
  decideActivation,
  inactiveServerProject,
  normalizeActivationMode,
  parseProjectInfo,
  ProjectDetector,
} = require('../dist/projectDetection.js');

test('parses typed project-info output', () => {
  assert.deepEqual(parseProjectInfo(JSON.stringify({
    supported: true,
    kind: 'shopware',
    evidence: [{path: 'composer.json', reason: 'requires shopware/core'}],
  })), {
    supported: true,
    kind: 'shopware',
    evidence: [{path: 'composer.json', reason: 'requires shopware/core'}],
  });
  assert.throws(() => parseProjectInfo('{"supported":true,"kind":"unknown","evidence":[]}'));
  assert.throws(() => parseProjectInfo('{"supported":false,"kind":"other","evidence":[]}'));
});

test('recognizes capability-free inactive server initialization', () => {
  assert.deepEqual(inactiveServerProject({
    shopwareLSP: {active: false, reason: 'unsupportedProject'},
  }), {active: false, reason: 'unsupportedProject'});
  assert.equal(inactiveServerProject(undefined), undefined);
  assert.equal(inactiveServerProject({shopwareLSP: {active: true}}), undefined);
  assert.equal(inactiveServerProject({shopwareLSP: {active: false, reason: 'other'}}), undefined);
});

test('applies auto, always, and never activation modes', () => {
  assert.equal(normalizeActivationMode('always'), 'always');
  assert.equal(normalizeActivationMode('invalid'), 'auto');
  assert.deepEqual(decideActivation('auto', {
    supported: false, kind: 'unknown', evidence: [],
  }), {enabled: false, allowUnsupportedProject: false});
  assert.deepEqual(decideActivation('auto', {
    supported: true, kind: 'symfony', evidence: [],
  }), {enabled: true, allowUnsupportedProject: false});
  assert.deepEqual(decideActivation('always'), {
    enabled: true, allowUnsupportedProject: true,
  });
  assert.deepEqual(decideActivation('never'), {
    enabled: false, allowUnsupportedProject: false,
  });
});

test('caches detection per executable and root and supports invalidation', async () => {
  let calls = 0;
  const detector = new ProjectDetector(async () => {
    calls++;
    return '{"supported":true,"kind":"configured","evidence":[]}';
  });
  await detector.detect('/bin/shopware-lsp', '/workspace/project');
  await detector.detect('/bin/shopware-lsp', '/workspace/project');
  assert.equal(calls, 1);
  detector.invalidate('/workspace/project');
  await detector.detect('/bin/shopware-lsp', '/workspace/project');
  assert.equal(calls, 2);
});

test('does not retain a failed detection', async () => {
  let calls = 0;
  const detector = new ProjectDetector(async () => {
    calls++;
    if (calls === 1) throw new Error('temporary failure');
    return '{"supported":false,"kind":"unknown","evidence":[]}';
  });
  await assert.rejects(detector.detect('/bin/shopware-lsp', '/workspace/project'));
  assert.equal((await detector.detect('/bin/shopware-lsp', '/workspace/project')).kind, 'unknown');
  assert.equal(calls, 2);
});
