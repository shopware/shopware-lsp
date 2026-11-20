const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {test} = require('node:test');

const root = path.join(__dirname, '..');
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));

function read(relative) {
  return JSON.parse(fs.readFileSync(path.join(root, relative), 'utf8'));
}

function patterns(node, found = []) {
  if (Array.isArray(node)) {
    node.forEach((child) => patterns(child, found));
  } else if (node && typeof node === 'object') {
    for (const key of ['match', 'begin', 'end']) {
      if (typeof node[key] === 'string') {
        found.push(node[key]);
      }
    }
    Object.values(node).forEach((child) => patterns(child, found));
  }
  return found;
}

test('contributes the Twig language itself, with no extension dependency', () => {
  const language = manifest.contributes.languages.find(({id}) => id === 'twig');

  assert.deepEqual(language.extensions, ['.twig']);
  assert.ok(read(language.configuration).comments.blockComment);
  assert.equal(manifest.extensionDependencies, undefined);
});

test('contributes a Twig grammar whose includes and expressions resolve', () => {
  const contribution = manifest.contributes.grammars.find(({language}) => language === 'twig');
  const grammar = read(contribution.path);

  assert.equal(grammar.scopeName, contribution.scopeName);
  for (const expression of patterns(grammar)) {
    assert.doesNotThrow(() => new RegExp(expression), expression);
  }
  const includes = JSON.stringify(grammar).match(/"include":"#[^"]+"/g) ?? [];
  for (const include of includes) {
    const name = include.slice('"include":"#'.length, -1);
    assert.ok(grammar.repository[name], `unknown rule ${name}`);
  }
});

test('the grammar tells tags, output, comments and filters apart', () => {
  const {repository} = read('syntaxes/twig.tmLanguage.json');
  const tag = new RegExp(repository.tag.begin);
  const filter = new RegExp(repository.expression.patterns.find(
    (rule) => rule.captures?.['2']?.name === 'support.function.filter.twig',
  ).match);

  assert.equal('{%- sw_extends "@Storefront/base.html.twig" %}'.match(tag)[2], 'sw_extends');
  assert.equal("{{ 'key'|trans }}".match(filter)[2], 'trans');
  assert.ok(new RegExp(repository.output.begin).test('{{ product.name }}'));
  assert.ok(new RegExp(repository.comment.begin).test('{# note #}'));
});
