const esbuild = require('esbuild');
const fs = require('fs');
const path = require('path');

const production = process.argv.includes('--production');
const watch = process.argv.includes('--watch');

async function main() {
  fs.mkdirSync('dist', {recursive: true});
  fs.copyFileSync(
    path.join('..', 'internal', 'projectconfig', 'schema.json'),
    path.join('dist', 'shopware-lsp.schema.json')
  );
  const extensionContext = await esbuild.context({
    entryPoints: ['src/extension.ts'],
    bundle: true,
    format: 'cjs',
    minify: production,
    sourcemap: !production,
    sourcesContent: false,
    platform: 'node',
    outfile: 'dist/extension.js',
    external: ['vscode'],
    logLevel: 'warning',
    plugins: [
      /* add to the end of plugins array */
      esbuildProblemMatcherPlugin
    ]
  });
  const webviewContext = await esbuild.context({
    entryPoints: ['src/entityDesignerWebview.ts'],
    bundle: true,
    format: 'iife',
    minify: production,
    sourcemap: !production,
    sourcesContent: false,
    platform: 'browser',
    outfile: 'dist/entityDesignerWebview.js',
    logLevel: 'warning',
    plugins: [esbuildProblemMatcherPlugin]
  });
  const configurationModelContext = await esbuild.context({
    entryPoints: ['src/configurationModel.ts'],
    bundle: true,
    format: 'cjs',
    minify: production,
    sourcemap: !production,
    sourcesContent: false,
    platform: 'node',
    outfile: 'dist/configurationModel.js',
    logLevel: 'warning',
    plugins: [esbuildProblemMatcherPlugin]
  });
  const mcpModelContext = await esbuild.context({
    entryPoints: {
      mcpServerModel: 'src/mcpServerModel.ts',
      serverExecutable: 'src/serverExecutable.ts'
    },
    bundle: true,
    format: 'cjs',
    minify: production,
    sourcemap: !production,
    sourcesContent: false,
    platform: 'node',
    outdir: 'dist',
    logLevel: 'warning',
    plugins: [esbuildProblemMatcherPlugin]
  });
  if (watch) {
    await Promise.all([
      extensionContext.watch(), webviewContext.watch(), configurationModelContext.watch(),
      mcpModelContext.watch()
    ]);
  } else {
    await Promise.all([
      extensionContext.rebuild(), webviewContext.rebuild(), configurationModelContext.rebuild(),
      mcpModelContext.rebuild()
    ]);
    await Promise.all([
      extensionContext.dispose(), webviewContext.dispose(), configurationModelContext.dispose(),
      mcpModelContext.dispose()
    ]);
  }
}

/**
 * @type {import('esbuild').Plugin}
 */
const esbuildProblemMatcherPlugin = {
  name: 'esbuild-problem-matcher',

  setup(build) {
    build.onStart(() => {
      console.log('[watch] build started');
    });
    build.onEnd(result => {
      result.errors.forEach(({ text, location }) => {
        console.error(`✘ [ERROR] ${text}`);
        if (location == null) return;
        console.error(`    ${location.file}:${location.line}:${location.column}:`);
      });
      console.log('[watch] build finished');
    });
  }
};

main().catch(e => {
  console.error(e);
  process.exit(1);
});
