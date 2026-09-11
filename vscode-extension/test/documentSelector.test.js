const assert = require('node:assert/strict');
const {test} = require('node:test');
const {
  documentSelectorFilePatterns,
  documentSelectorLanguages,
} = require('../dist/documentSelectorModel.js');

test('matches the twig language ID', () => {
  assert.ok(documentSelectorLanguages.includes('twig'));
});

test('matches twig files by pattern regardless of language ID', () => {
  // VS Code has no built-in Twig language and users often reassociate *.twig
  // with HTML via files.associations. Without a pattern-only filter the
  // client never attaches to those documents, so go-to-definition on
  // sw_extends template paths silently falls through to word selection.
  assert.ok(documentSelectorFilePatterns.includes('**/*.twig'));
});

test('contributes the twig language so twig files do not open as plaintext', () => {
  const manifest = require('../package.json');
  const twig = (manifest.contributes.languages ?? []).find(
    language => language.id === 'twig',
  );
  assert.ok(twig, 'package.json should contribute the twig language');
  assert.ok(twig.extensions.includes('.twig'));
});
