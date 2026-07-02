---
name: web-artifacts-builder
description: Suite of tools for creating elaborate, multi-component single-file HTML web apps using modern frontend technologies (React, Tailwind CSS, shadcn/ui). Use for complex artifacts requiring state management, routing, or shadcn/ui components — not for simple single-file HTML/JSX pages you can write directly.
license: Apache-2.0 (adapted from anthropics/skills; complete terms in LICENSE.txt)
---

# Web Artifacts Builder

To build a powerful frontend artifact — a self-contained single HTML file that opens in any browser — follow these steps:
1. Initialize the frontend repo using `scripts/init-artifact.ts`
2. Develop your artifact by editing the generated code
3. Bundle all code into a single HTML file using `scripts/bundle-artifact.ts`
4. Deliver the artifact to the user
5. (Optional) Test the artifact

**Stack**: React 18 + TypeScript + Vite + Bun (bundling) + Tailwind CSS + shadcn/ui

**Requirements**: bun on PATH (get it from https://bun.sh). Verify before starting (`bun --version`). If missing, tell the user what to install and wait for their go-ahead — never install unasked:

- bun — macOS/Linux: `curl -fsSL https://bun.sh/install | bash`, or `brew install oven-sh/bun/bun` on macOS; Windows: `powershell -c "irm bun.sh/install.ps1 | iex"`.
- After any mid-session install, the new tool may be missing from this session's PATH — refresh PATH or use full paths per the environment notes; if problems persist, have the user restart octo.

Note: the `scripts/` paths are relative to this skill's directory (its absolute path is in the header above) — invoke them as `bun run <skill-dir>/scripts/init-artifact.ts`. Run the project itself in the user's working directory, not inside the skill directory.

## Design & Style Guidelines

VERY IMPORTANT: To avoid what is often referred to as "AI slop", avoid using excessive centered layouts, purple gradients, uniform rounded corners, and Inter font.

## Quick Start

### Step 1: Initialize Project

Run the initialization script to create a new React project:
```bash
bun run <skill-dir>/scripts/init-artifact.ts <project-name>
cd <project-name>
```

This creates a fully configured project with:
- ✅ React + TypeScript (via Vite)
- ✅ Tailwind CSS 3.4.1 with shadcn/ui theming system
- ✅ Path aliases (`@/`) configured
- ✅ 40+ shadcn/ui components pre-installed
- ✅ All Radix UI dependencies included

### Step 2: Develop Your Artifact

To build the artifact, edit the generated files. Remember there is no persistent working directory across terminal calls — chain `cd <project> && …` inside each command, and pass absolute paths to the file tools.

### Step 3: Bundle to Single HTML File

To bundle the React app into a single HTML artifact:
```bash
cd <project-name> && bun run <skill-dir>/scripts/bundle-artifact.ts
```

This creates `bundle.html` — a self-contained file with all JavaScript, CSS, and dependencies inlined.

**Requirements**: Your project must have an `index.html` in the root directory.

**What the script does**:
- Compiles Tailwind CSS to real utility classes (via the `tailwindcss` CLI)
- Builds with Bun's native bundler (`Bun.build`, no source maps)
- Inlines the built JavaScript and CSS into a single HTML file

### Step 4: Deliver the Artifact

Call the `show_artifact` tool with the absolute path of `bundle.html` — in the web UI this renders it in the Artifacts panel. Then report the path to the user. The file is fully self-contained — it opens directly in any browser with no server and no network.

### Step 5: Testing/Visualizing the Artifact (Optional)

Note: This is a completely optional step. Only perform if necessary or requested.

To test/visualize the artifact, use available tools (a browser-automation skill or MCP tool if one is connected). In general, avoid testing the artifact upfront as it adds latency between the request and when the finished artifact can be seen. Test later, after presenting the artifact, if requested or if issues arise.

## Reference

- **shadcn/ui components**: https://ui.shadcn.com/docs/components
