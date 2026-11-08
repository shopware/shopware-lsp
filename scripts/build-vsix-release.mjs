#!/usr/bin/env node

import {createHash} from 'node:crypto';
import {existsSync} from 'node:fs';
import os from 'node:os';
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
const extensionDirectory = path.join(repositoryRoot, 'editors/vscode');
const outputDirectory = path.join(repositoryRoot, 'out');
// Release binaries are built natively per Rust target by
// .github/workflows/build-binaries.yml and staged by
// scripts/release/stage-binaries.sh as target/<triple>/release/<binary>.
const targetDirectory = process.env.CARGO_TARGET_DIR
  ? path.resolve(repositoryRoot, process.env.CARGO_TARGET_DIR)
  : path.join(repositoryRoot, 'target');

// The VSCode Marketplace and Open VSX only accept numeric major.minor.patch
// versions; semver pre-release identifiers are rejected at publish time.
const versionPattern = /^\d+\.\d+\.\d+$/;

const options = new Set(process.argv.slice(2));
if (options.has('--help')) {
  console.log([
    'Usage: build-vsix-release.mjs [--pre-release] [--version=<version>] [--local] [--dry-run]',
    '',
    'Packages one VSIX per VS Code platform target from prebuilt Rust release',
    'binaries at target/<rust-triple>/release/shopware-lsp[.exe].',
    '',
    '  --local    build only the host target with cargo and package its VSIX',
    '  --dry-run  check the binaries and print the plan without packaging',
  ].join('\n'));
  process.exit(0);
}
const preRelease = options.delete('--pre-release');
const local = options.delete('--local');
const dryRun = options.delete('--dry-run');
let versionOverride;
for (const option of options) {
  if (option.startsWith('--version=')) {
    versionOverride = option.slice('--version='.length);
    options.delete(option);
    break;
  }
}
if (options.size > 0) {
  throw new Error(`Unknown release option: ${[...options].join(', ')}`);
}

const packageManifest = JSON.parse(await readFile(
  path.join(extensionDirectory, 'package.json'),
  'utf8',
));
const version = versionOverride ?? packageManifest.version;
if (!versionPattern.test(version)) {
  throw new Error(`Invalid VSCode extension version: ${version}`);
}

// The Linux binaries are fully static (musl), so the Alpine
// packages reuse them, as they reused the static Go builds before.
const allTargets = [
  {vsce: 'darwin-x64', triple: 'x86_64-apple-darwin', binary: 'shopware-lsp'},
  {vsce: 'darwin-arm64', triple: 'aarch64-apple-darwin', binary: 'shopware-lsp'},
  {vsce: 'linux-x64', triple: 'x86_64-unknown-linux-musl', binary: 'shopware-lsp'},
  {vsce: 'linux-arm64', triple: 'aarch64-unknown-linux-musl', binary: 'shopware-lsp'},
  {vsce: 'linux-armhf', triple: 'armv7-unknown-linux-musleabihf', binary: 'shopware-lsp'},
  {vsce: 'alpine-x64', triple: 'x86_64-unknown-linux-musl', binary: 'shopware-lsp'},
  {vsce: 'alpine-arm64', triple: 'aarch64-unknown-linux-musl', binary: 'shopware-lsp'},
  {vsce: 'win32-x64', triple: 'x86_64-pc-windows-msvc', binary: 'shopware-lsp.exe'},
];
const staticLinuxTriples = [
  'x86_64-unknown-linux-musl',
  'aarch64-unknown-linux-musl',
  'armv7-unknown-linux-musleabihf',
];

function hostVsceTarget() {
  const platform = {darwin: 'darwin', linux: 'linux', win32: 'win32'}[os.platform()];
  const arch = {x64: 'x64', arm64: 'arm64', arm: 'armhf'}[os.arch()];
  const target = platform && arch ? `${platform}-${arch}` : undefined;
  if (!allTargets.some(candidate => candidate.vsce === target)) {
    throw new Error(`No VSIX target for host ${os.platform()}-${os.arch()}`);
  }
  return target;
}

const targets = local
  ? allTargets.filter(target => target.vsce === hostVsceTarget())
  : allTargets;

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

function binaryPath(target) {
  // A local build uses cargo's host layout, without a triple directory.
  return local
    ? path.join(targetDirectory, 'release', target.binary)
    : path.join(targetDirectory, target.triple, 'release', target.binary);
}

function loadBinaries() {
  const binaries = new Map();
  for (const target of targets) {
    const binary = binaryPath(target);
    if (!existsSync(binary)) {
      throw new Error(
        `Missing release binary for ${target.vsce}: ${binary}. ` +
        'Stage the build-binaries artifacts with scripts/release/stage-binaries.sh ' +
        'or pass --local to build the host target.',
      );
    }
    binaries.set(target.vsce, binary);
  }
  return binaries;
}

function assertStaticLinuxBinaries(binaries) {
  if (local) return;
  for (const triple of staticLinuxTriples) {
    const target = targets.find(candidate => candidate.triple === triple);
    const description = output('file', ['-b', binaries.get(target.vsce)]);
    if (!description.includes('statically linked') &&
      !description.includes('static-pie linked')) {
      throw new Error(`${triple} is not statically linked: ${description}`);
    }
  }
}

async function checksum(filePath) {
  const content = await readFile(filePath);
  return createHash('sha256').update(content).digest('hex');
}

await rm(outputDirectory, {recursive: true, force: true});
await mkdir(outputDirectory, {recursive: true});
if (local) {
  // Embed the package version, as the CI build embeds the tag.
  run(
    'cargo',
    ['build', '--locked', '--release', '-p', 'swlsp-cli', '--bin', 'shopware-lsp'],
    {env: {...process.env, SHOPWARE_LSP_VERSION: version}},
  );
}
const binaries = loadBinaries();
assertStaticLinuxBinaries(binaries);
if (dryRun) {
  for (const target of targets) {
    console.log(
      `${target.vsce}: ${binaries.get(target.vsce)} -> ` +
      `shopware-lsp-${version}${preRelease ? '-pre-release' : ''}-${target.vsce}.vsix`,
    );
  }
  console.log('\nDry run: no VSIX packages were created.');
  process.exit(0);
}
if (versionOverride) {
  run(
    'npm',
    ['version', version, '--no-git-tag-version', '--allow-same-version'],
    {cwd: extensionDirectory},
  );
}
run('npm', ['run', 'package'], {cwd: extensionDirectory});

const stagedUnixBinary = path.join(extensionDirectory, 'shopware-lsp');
const stagedWindowsBinary = path.join(extensionDirectory, 'shopware-lsp.exe');
const packaged = [];

try {
  for (const target of targets) {
    const stagedBinary = path.join(extensionDirectory, target.binary);
    await rm(stagedUnixBinary, {force: true});
    await rm(stagedWindowsBinary, {force: true});
    await copyFile(binaries.get(target.vsce), stagedBinary);
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
