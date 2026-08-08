#!/usr/bin/env node

import {createHash} from 'node:crypto';
import {existsSync} from 'node:fs';
import {
  chmod,
  copyFile,
  mkdir,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import {spawnSync} from 'node:child_process';

const repositoryRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '..',
);
const extensionDirectory = path.join(repositoryRoot, 'vscode-extension');
const outputDirectory = path.join(repositoryRoot, 'out');
const goreleaserDirectory = path.join(repositoryRoot, 'dist');
const crossImage = process.env.GORELEASER_CROSS_IMAGE ||
  'ghcr.io/goreleaser/goreleaser-cross:v1.26.5';

const options = new Set(process.argv.slice(2));
if (options.has('--help')) {
  console.log('Usage: build-vsix-release.mjs [--pre-release]');
  process.exit(0);
}
const preRelease = options.delete('--pre-release');
if (options.size > 0) {
  throw new Error(`Unknown release option: ${[...options].join(', ')}`);
}

const packageManifest = JSON.parse(await readFile(
  path.join(extensionDirectory, 'package.json'),
  'utf8',
));
const version = packageManifest.version;
if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) {
  throw new Error(`Invalid VSCode extension version: ${version}`);
}

const targets = [
  {vsce: 'darwin-x64', build: 'darwin-amd64', binary: 'shopware-lsp'},
  {vsce: 'darwin-arm64', build: 'darwin-arm64', binary: 'shopware-lsp'},
  {vsce: 'linux-x64', build: 'linux-amd64', binary: 'shopware-lsp'},
  {vsce: 'linux-arm64', build: 'linux-arm64', binary: 'shopware-lsp'},
  {vsce: 'linux-armhf', build: 'linux-armv7', binary: 'shopware-lsp'},
  {vsce: 'alpine-x64', build: 'linux-amd64', binary: 'shopware-lsp'},
  {vsce: 'alpine-arm64', build: 'linux-arm64', binary: 'shopware-lsp'},
  {vsce: 'win32-x64', build: 'windows-amd64', binary: 'shopware-lsp.exe'},
];

function run(command, args, options = {}) {
  console.log(`\n> ${command} ${args.join(' ')}`);
  const result = spawnSync(command, args, {
    cwd: repositoryRoot,
    env: process.env,
    stdio: 'inherit',
    ...options,
  });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(`${command} exited with status ${result.status}`);
  }
}

function output(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: repositoryRoot,
    env: process.env,
    encoding: 'utf8',
    ...options,
  });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(`${command} exited with status ${result.status}: ${result.stderr}`);
  }
  return result.stdout.trim();
}

function resolveArtifactPath(artifactPath) {
  return path.isAbsolute(artifactPath)
    ? artifactPath
    : path.resolve(repositoryRoot, artifactPath);
}

async function loadBinaries() {
  const manifestPath = path.join(goreleaserDirectory, 'artifacts.json');
  const manifest = JSON.parse(await readFile(manifestPath, 'utf8'));
  const artifacts = Array.isArray(manifest) ? manifest : manifest.artifacts;
  if (!Array.isArray(artifacts)) {
    throw new Error(`Invalid GoReleaser artifact manifest: ${manifestPath}`);
  }

  const binaries = new Map();
  for (const artifact of artifacts) {
    if (artifact.type !== 'Binary' || !artifact.extra?.ID) {
      continue;
    }
    binaries.set(artifact.extra.ID, resolveArtifactPath(artifact.path));
  }
  for (const target of targets) {
    const binary = binaries.get(target.build);
    if (!binary || !existsSync(binary)) {
      throw new Error(`GoReleaser did not produce ${target.build}`);
    }
  }
  return binaries;
}

function assertStaticLinuxBinaries(binaries) {
  for (const build of ['linux-amd64', 'linux-arm64', 'linux-armv7']) {
    const description = output('file', ['-b', binaries.get(build)]);
    if (!description.includes('statically linked')) {
      throw new Error(`${build} is not statically linked: ${description}`);
    }
  }
}

async function checksum(filePath) {
  const content = await readFile(filePath);
  return createHash('sha256').update(content).digest('hex');
}

await rm(outputDirectory, {recursive: true, force: true});
await mkdir(outputDirectory, {recursive: true});
run('docker', [
  'run',
  '--rm',
  '--volume', `${repositoryRoot}:/go/src/shopware-lsp`,
  '--workdir', '/go/src/shopware-lsp',
  crossImage,
  'build',
  '--clean',
  '--snapshot',
  '--skip=validate',
]);
run('npm', ['run', 'package'], {cwd: extensionDirectory});

const binaries = await loadBinaries();
assertStaticLinuxBinaries(binaries);
const stagedUnixBinary = path.join(extensionDirectory, 'shopware-lsp');
const stagedWindowsBinary = path.join(extensionDirectory, 'shopware-lsp.exe');
const packaged = [];

try {
  for (const target of targets) {
    const stagedBinary = path.join(extensionDirectory, target.binary);
    await rm(stagedUnixBinary, {force: true});
    await rm(stagedWindowsBinary, {force: true});
    await copyFile(binaries.get(target.build), stagedBinary);
    if (target.binary === 'shopware-lsp') {
      await chmod(stagedBinary, 0o755);
    }

    const output = path.join(
      outputDirectory,
      `shopware-lsp-${version}${preRelease ? '-pre-release' : ''}-${target.vsce}.vsix`,
    );
    const packageArguments = [
      'package',
      '--target', target.vsce,
      '--out', output,
      '--no-dependencies',
    ];
    if (preRelease) {
      packageArguments.push('--pre-release');
    }
    run('vsce', packageArguments, {cwd: extensionDirectory});
    packaged.push(output);
  }
} finally {
  await rm(stagedUnixBinary, {force: true});
  await rm(stagedWindowsBinary, {force: true});
}

const checksums = [];
for (const artifact of packaged) {
  checksums.push(`${await checksum(artifact)}  ${path.basename(artifact)}`);
}
await writeFile(
  path.join(outputDirectory, 'SHA256SUMS'),
  `${checksums.join('\n')}\n`,
);

console.log(
  `\nCreated ${packaged.length}${preRelease ? ' pre-release' : ''} ` +
  `platform packages in ${outputDirectory}`,
);
