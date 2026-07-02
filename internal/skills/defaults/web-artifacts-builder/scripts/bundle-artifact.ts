#!/usr/bin/env bun
// Bundles the React app in the current directory into a single self-contained
// bundle.html using Bun's native bundler, then inlines the referenced
// script/style assets in place. Run with:
//   bun run bundle-artifact.ts

import * as fs from "node:fs";
import * as path from "node:path";

class FatalError extends Error {}

function fail(message: string): never {
  throw new FatalError(message);
}

const cwd = process.cwd();

console.log("Bundling React app to single HTML artifact...");

if (!fs.existsSync(path.join(cwd, "package.json"))) {
  fail("No package.json found. Run this script from your project root.");
}

const indexHtmlPath = path.join(cwd, "index.html");
if (!fs.existsSync(indexHtmlPath)) {
  fail("No index.html found in project root. This script requires an index.html entry point.");
}

const distDir = path.join(cwd, "dist");
const bundleHtmlPath = path.join(cwd, "bundle.html");
const indexCssPath = path.join(cwd, "src", "index.css");
const tailwindConfigPath = path.join(cwd, "tailwind.config.js");
const tempEntryPath = path.join(cwd, ".bundle-artifact-entry.html");

console.log("Cleaning previous build...");
fs.rmSync(distDir, { recursive: true, force: true });
fs.rmSync(bundleHtmlPath, { force: true });

// indexCssBackup and tempEntryPath are declared outside the try below (but
// the on-disk swaps that populate them happen inside it) so the finally can
// always see them and undo any half-finished state, no matter which step
// throws.
let indexCssBackup: string | null = null;

try {
  // Bun's native CSS bundler concatenates/minifies CSS but does not run
  // PostCSS plugins — it has no idea what `@tailwind
  // base/components/utilities` means and would ship those directives
  // through to the browser verbatim (i.e. the artifact would render
  // completely unstyled). Precompile the real utility CSS with the
  // `tailwindcss` CLI (already a project devDependency) and swap it in for
  // the build. Everything from here through the end of this try block can
  // throw while src/index.css is still holding that swap (a bad
  // tailwindcss-CLI run, index.html unreadable, disk full on a temp-file
  // write, Bun.build throwing) — the try/finally spans all of it so the
  // finally's restoration of src/index.css is guaranteed on every path, not
  // just the happy one.
  if (fs.existsSync(indexCssPath) && fs.existsSync(tailwindConfigPath)) {
    const original = fs.readFileSync(indexCssPath, "utf8");
    if (/@tailwind\s/.test(original)) {
      console.log("Compiling Tailwind CSS...");
      const compiledPath = path.join(cwd, ".bundle-artifact-tailwind.css");
      const tw = Bun.spawnSync(
        [
          "bun",
          "x",
          "tailwindcss",
          "-i",
          indexCssPath,
          "-o",
          compiledPath,
          "-c",
          tailwindConfigPath,
          "--minify",
        ],
        { cwd, stdout: "inherit", stderr: "inherit" },
      );
      if (!tw.success || !fs.existsSync(compiledPath)) {
        fail("failed to compile Tailwind CSS via `bun x tailwindcss` (see output above)");
      }
      const compiled = fs.readFileSync(compiledPath, "utf8");
      fs.rmSync(compiledPath, { force: true });
      indexCssBackup = original;
      fs.writeFileSync(indexCssPath, compiled);
    }
  }

  // Bun.build's HTML entrypoint resolves root-absolute references (href="/x")
  // relative to the project root, but it has no notion of Vite's "public/"
  // convention (where "/favicon.svg" on disk actually lives at
  // "public/favicon.svg" and is only mapped to "/" by Vite's dev/prod
  // pipeline). Left as-is, Bun.build throws "Could not resolve" on a fresh
  // scaffold's own index.html (the favicon link). Build from a temporary
  // copy of index.html with such references rewritten to point at
  // "public/", so we never need to mutate the user's actual index.html on
  // disk.
  let entryHtml = fs.readFileSync(indexHtmlPath, "utf8");
  entryHtml = entryHtml.replace(
    /((?:href|src)=")\/([^"]+)(")/g,
    (full, prefix, assetPath, suffix) => {
      if (fs.existsSync(path.join(cwd, assetPath))) {
        return full; // already resolvable as-is (e.g. /src/main.tsx)
      }
      if (fs.existsSync(path.join(cwd, "public", assetPath))) {
        return `${prefix}/public/${assetPath}${suffix}`;
      }
      return full;
    },
  );
  fs.writeFileSync(tempEntryPath, entryHtml);

  console.log("Building with Bun.build...");
  let buildResult;
  try {
    buildResult = await Bun.build({
      entrypoints: [tempEntryPath],
      outdir: distDir,
      sourcemap: "none",
      minify: true,
      // Force the emitted HTML to be named index.html regardless of the
      // temp entry file's own basename.
      naming: { entry: "index.[ext]" },
    });
  } catch (err) {
    fail(`build failed: ${err}`);
  }

  if (!buildResult.success) {
    for (const message of buildResult.logs) {
      console.error(message);
    }
    fail("build failed (see logs above)");
  }

  const builtIndexHtmlPath = path.join(distDir, "index.html");
  if (!fs.existsSync(builtIndexHtmlPath)) {
    fail(`build did not produce ${builtIndexHtmlPath}`);
  }

  console.log("Inlining all assets into single HTML file...");
  let html = fs.readFileSync(builtIndexHtmlPath, "utf8");

  function resolveLocalAsset(ref: string): string | null {
    if (/^(https?:)?\/\//.test(ref) || ref.startsWith("data:")) {
      return null;
    }
    let decoded: string;
    try {
      decoded = decodeURIComponent(ref);
    } catch {
      return null;
    }
    const assetPath = path.join(distDir, decoded);
    return fs.existsSync(assetPath) ? assetPath : null;
  }

  // Inline <link rel="stylesheet" href="...."> tags first, then <script
  // src="...">. Order matters: once a <script> tag is replaced with its raw
  // JS contents inline, that JS text becomes part of `html` and a later
  // regex pass could spuriously match tag-shaped substrings inside a
  // minified dependency's own string literals. Running the link pass first
  // (against content with no inlined JS yet) avoids that; there is nothing
  // left to inline after the script pass runs last.
  html = html.replace(/<link[^>]*\srel="stylesheet"[^>]*\shref="([^"]+)"[^>]*>/g, (full, href) => {
    const assetPath = resolveLocalAsset(href);
    if (!assetPath) {
      console.warn(`Warning: could not inline stylesheet href="${href}" (file not found)`);
      return full;
    }
    const contents = fs.readFileSync(assetPath, "utf8");
    return `<style>\n${contents}\n</style>`;
  });

  // Inline <script src="...">...</script> tags that reference a local build
  // output file (relative path, not http(s):// or a data: URI).
  html = html.replace(
    /<script([^>]*)\ssrc="([^"]+)"([^>]*)><\/script>/g,
    (full, before, src, after) => {
      const assetPath = resolveLocalAsset(src);
      if (!assetPath) {
        console.warn(`Warning: could not inline script src="${src}" (file not found)`);
        return full;
      }
      let contents = fs.readFileSync(assetPath, "utf8");
      // React's own minified source contains the literal string
      // "<script></script>" (an internal DOM feature-detection snippet).
      // Left unescaped, embedding that raw inside a real <script> tag makes
      // the HTML parser treat it as the tag's actual close, truncating
      // everything after it and dumping the remaining JS as visible page
      // text. Escaping the slash keeps the JS semantically identical (a
      // string literal's "\/" is just "/") while no longer matching the
      // HTML tag-close grammar.
      contents = contents.replace(/<\/script/gi, "<\\/script");
      // Preserve any remaining attributes (e.g. type="module") but drop src=.
      const attrs = `${before}${after}`;
      return `<script${attrs}>\n${contents}\n</script>`;
    },
  );

  fs.writeFileSync(bundleHtmlPath, html);

  const fileSizeBytes = fs.statSync(bundleHtmlPath).size;
  const fileSizeKb = (fileSizeBytes / 1024).toFixed(1);

  console.log("");
  console.log("Bundle complete!");
  console.log(`Output: bundle.html (${fileSizeKb} KB)`);
  console.log("");
  console.log("You can now use this single HTML file as an artifact.");
  console.log("To test locally: open bundle.html in your browser");
} catch (err) {
  if (err instanceof FatalError) {
    console.error(`Error: ${err.message}`);
  } else {
    console.error(err);
  }
  process.exitCode = 1;
} finally {
  fs.rmSync(tempEntryPath, { force: true });
  if (indexCssBackup !== null) {
    fs.writeFileSync(indexCssPath, indexCssBackup);
  }
}
