const assert = require('node:assert/strict');
const {test} = require('node:test');
const {
  createTwigWordPattern,
} = require('../dist/languageConfigurationModel.js');

test('treats hyphenated Administration component tags as one word', () => {
  const source = '<sw-button><mt-button></mt-button></sw-button>';

  assert.deepEqual(source.match(createTwigWordPattern()), [
    'sw-button',
    'mt-button',
    'mt-button',
    'sw-button',
  ]);
});

test('keeps Twig and markup punctuation outside words', () => {
  const source = '<sw-button :class="product.manufacturer.name">';

  assert.deepEqual(source.match(createTwigWordPattern()), [
    'sw-button',
    'class',
    'product',
    'manufacturer',
    'name',
  ]);
});
