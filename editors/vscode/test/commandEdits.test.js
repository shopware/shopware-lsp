const assert = require('node:assert/strict');
const {test} = require('node:test');
const {applyCommandEdit} = require('../dist/commandEdits.js');

function fixture(convert = async edit => edit) {
  const applied = [];
  const client = {protocol2CodeConverter: {asWorkspaceEdit: convert}};
  const document = {uri: {toString: () => 'file:///snippet.json'}, version: 2};
  const workspace = {textDocuments: [document], applyEdit: async edit => {applied.push(edit); return true;}};
  return {client, workspace, document, applied};
}

test('applies edits with create operations and supports older command responses', async () => {
  const {client, workspace, applied} = fixture();
  await applyCommandEdit(client, workspace, null);
  await applyCommandEdit(client, workspace, {uri: 'legacy'});
  assert.equal(applied.length, 0);
  const edit = {documentChanges: [{kind: 'create', uri: 'file:///new.json'}, {textDocument: {uri: 'file:///new.json', version: null}, edits: []}]};
  await applyCommandEdit(client, workspace, {edit});
  assert.deepEqual(applied, [edit]);
  const changes = {changes: {'file:///snippet.json': []}};
  await applyCommandEdit(client, workspace, {edit: changes});
  assert.deepEqual(applied[1], changes);
});

test('rejects a target changed while converting the protocol edit', async () => {
  const state = fixture(async edit => {state.document.version++; return edit;});
  const edit = {documentChanges: [{textDocument: {uri: 'file:///snippet.json', version: 2}, edits: []}]};
  await assert.rejects(applyCommandEdit(state.client, state.workspace, {edit}), /document changed/);
  assert.equal(state.applied.length, 0);
});

test('reports application failure rather than claiming the command succeeded', async () => {
  const {client, workspace} = fixture();
  workspace.applyEdit = async () => false;
  await assert.rejects(applyCommandEdit(client, workspace, {edit: {changes: {}}}), /could not be applied/);
});
