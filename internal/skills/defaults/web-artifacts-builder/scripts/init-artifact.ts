#!/usr/bin/env bun
// Scaffolds a React + TypeScript + Vite project pre-wired with Tailwind CSS
// and 40+ shadcn/ui components. Run with:
//   bun run init-artifact.ts <project-name>

import * as fs from "node:fs";
import * as path from "node:path";

function run(cmd: string[], opts: { cwd?: string } = {}): void {
  console.log(`$ ${cmd.join(" ")}`);
  const result = Bun.spawnSync(cmd, {
    cwd: opts.cwd,
    stdout: "inherit",
    stderr: "inherit",
  });
  if (!result.success) {
    console.error(`Error: command failed (exit ${result.exitCode}): ${cmd.join(" ")}`);
    process.exit(result.exitCode ?? 1);
  }
}

const projectName = process.argv[2];
if (!projectName) {
  console.error("Usage: bun run init-artifact.ts <project-name>");
  process.exit(1);
}

const scriptDir = import.meta.dir;
const componentsTarball = path.join(scriptDir, "shadcn-components.tar.gz");

if (!fs.existsSync(componentsTarball)) {
  console.error("Error: shadcn-components.tar.gz not found in script directory");
  console.error(`  Expected location: ${componentsTarball}`);
  process.exit(1);
}

console.log(`Creating new React + Vite project: ${projectName}`);

// Create new Vite project.
run(["bun", "create", "vite", projectName, "--template", "react-ts"]);

const projectDir = path.resolve(process.cwd(), projectName);

console.log("Cleaning up Vite template...");
{
  const indexHtmlPath = path.join(projectDir, "index.html");
  let html = fs.readFileSync(indexHtmlPath, "utf8");
  html = html.replace(/^\s*<link rel="icon".*vite\.svg.*\n/m, "");
  html = html.replace(/<title>.*<\/title>/, `<title>${projectName}</title>`);
  fs.writeFileSync(indexHtmlPath, html);
}

console.log("Installing base dependencies...");
run(["bun", "install"], { cwd: projectDir });

console.log("Installing Tailwind CSS and dependencies...");
run(
  [
    "bun",
    "add",
    "-D",
    "tailwindcss@3.4.1",
    "postcss",
    "autoprefixer",
    "@types/node",
    "tailwindcss-animate",
  ],
  { cwd: projectDir },
);
run(
  [
    "bun",
    "add",
    "class-variance-authority",
    "clsx",
    "tailwind-merge",
    "lucide-react",
    "next-themes",
  ],
  { cwd: projectDir },
);

console.log("Creating Tailwind and PostCSS configuration...");
fs.writeFileSync(
  path.join(projectDir, "postcss.config.js"),
  `export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
}
`,
);

console.log("Configuring Tailwind with shadcn theme...");
fs.writeFileSync(
  path.join(projectDir, "tailwind.config.js"),
  `/** @type {import('tailwindcss').Config} */
module.exports = {
  darkMode: ["class"],
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        border: "hsl(var(--border))",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
        background: "hsl(var(--background))",
        foreground: "hsl(var(--foreground))",
        primary: {
          DEFAULT: "hsl(var(--primary))",
          foreground: "hsl(var(--primary-foreground))",
        },
        secondary: {
          DEFAULT: "hsl(var(--secondary))",
          foreground: "hsl(var(--secondary-foreground))",
        },
        destructive: {
          DEFAULT: "hsl(var(--destructive))",
          foreground: "hsl(var(--destructive-foreground))",
        },
        muted: {
          DEFAULT: "hsl(var(--muted))",
          foreground: "hsl(var(--muted-foreground))",
        },
        accent: {
          DEFAULT: "hsl(var(--accent))",
          foreground: "hsl(var(--accent-foreground))",
        },
        popover: {
          DEFAULT: "hsl(var(--popover))",
          foreground: "hsl(var(--popover-foreground))",
        },
        card: {
          DEFAULT: "hsl(var(--card))",
          foreground: "hsl(var(--card-foreground))",
        },
      },
      borderRadius: {
        lg: "var(--radius)",
        md: "calc(var(--radius) - 2px)",
        sm: "calc(var(--radius) - 4px)",
      },
      keyframes: {
        "accordion-down": {
          from: { height: "0" },
          to: { height: "var(--radix-accordion-content-height)" },
        },
        "accordion-up": {
          from: { height: "var(--radix-accordion-content-height)" },
          to: { height: "0" },
        },
      },
      animation: {
        "accordion-down": "accordion-down 0.2s ease-out",
        "accordion-up": "accordion-up 0.2s ease-out",
      },
    },
  },
  plugins: [require("tailwindcss-animate")],
}
`,
);

console.log("Adding Tailwind directives and CSS variables...");
fs.writeFileSync(
  path.join(projectDir, "src", "index.css"),
  `@tailwind base;
@tailwind components;
@tailwind utilities;

@layer base {
  :root {
    --background: 0 0% 100%;
    --foreground: 0 0% 3.9%;
    --card: 0 0% 100%;
    --card-foreground: 0 0% 3.9%;
    --popover: 0 0% 100%;
    --popover-foreground: 0 0% 3.9%;
    --primary: 0 0% 9%;
    --primary-foreground: 0 0% 98%;
    --secondary: 0 0% 96.1%;
    --secondary-foreground: 0 0% 9%;
    --muted: 0 0% 96.1%;
    --muted-foreground: 0 0% 45.1%;
    --accent: 0 0% 96.1%;
    --accent-foreground: 0 0% 9%;
    --destructive: 0 84.2% 60.2%;
    --destructive-foreground: 0 0% 98%;
    --border: 0 0% 89.8%;
    --input: 0 0% 89.8%;
    --ring: 0 0% 3.9%;
    --radius: 0.5rem;
  }

  .dark {
    --background: 0 0% 3.9%;
    --foreground: 0 0% 98%;
    --card: 0 0% 3.9%;
    --card-foreground: 0 0% 98%;
    --popover: 0 0% 3.9%;
    --popover-foreground: 0 0% 98%;
    --primary: 0 0% 98%;
    --primary-foreground: 0 0% 9%;
    --secondary: 0 0% 14.9%;
    --secondary-foreground: 0 0% 98%;
    --muted: 0 0% 14.9%;
    --muted-foreground: 0 0% 63.9%;
    --accent: 0 0% 14.9%;
    --accent-foreground: 0 0% 98%;
    --destructive: 0 62.8% 30.6%;
    --destructive-foreground: 0 0% 98%;
    --border: 0 0% 14.9%;
    --input: 0 0% 14.9%;
    --ring: 0 0% 83.1%;
  }
}

@layer base {
  * {
    @apply border-border;
  }
  body {
    @apply bg-background text-foreground;
  }
}
`,
);

console.log("Adding path aliases to tsconfig.json...");
{
  const tsconfigPath = path.join(projectDir, "tsconfig.json");
  const config = JSON.parse(fs.readFileSync(tsconfigPath, "utf8"));
  config.compilerOptions = config.compilerOptions || {};
  config.compilerOptions.baseUrl = ".";
  config.compilerOptions.paths = { "@/*": ["./src/*"] };
  fs.writeFileSync(tsconfigPath, JSON.stringify(config, null, 2));
}

console.log("Adding path aliases to tsconfig.app.json...");
{
  const tsconfigAppPath = path.join(projectDir, "tsconfig.app.json");
  const content = fs.readFileSync(tsconfigAppPath, "utf8");
  // tsconfig.app.json is JSONC (comments + trailing commas) — strip both
  // before parsing since we only need to patch two fields.
  const lines = content.split("\n").filter((line) => !line.trim().startsWith("//"));
  const jsonContent = lines
    .join("\n")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/,(\s*[}\]])/g, "$1");
  const config = JSON.parse(jsonContent);
  config.compilerOptions = config.compilerOptions || {};
  config.compilerOptions.baseUrl = ".";
  config.compilerOptions.paths = { "@/*": ["./src/*"] };
  fs.writeFileSync(tsconfigAppPath, JSON.stringify(config, null, 2));
}

console.log("Updating Vite configuration...");
fs.writeFileSync(
  path.join(projectDir, "vite.config.ts"),
  `import path from "path";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
});
`,
);

console.log("Installing shadcn/ui dependencies...");
run(
  [
    "bun",
    "add",
    "@radix-ui/react-accordion",
    "@radix-ui/react-aspect-ratio",
    "@radix-ui/react-avatar",
    "@radix-ui/react-checkbox",
    "@radix-ui/react-collapsible",
    "@radix-ui/react-context-menu",
    "@radix-ui/react-dialog",
    "@radix-ui/react-dropdown-menu",
    "@radix-ui/react-hover-card",
    "@radix-ui/react-label",
    "@radix-ui/react-menubar",
    "@radix-ui/react-navigation-menu",
    "@radix-ui/react-popover",
    "@radix-ui/react-progress",
    "@radix-ui/react-radio-group",
    "@radix-ui/react-scroll-area",
    "@radix-ui/react-select",
    "@radix-ui/react-separator",
    "@radix-ui/react-slider",
    "@radix-ui/react-slot",
    "@radix-ui/react-switch",
    "@radix-ui/react-tabs",
    "@radix-ui/react-toast",
    "@radix-ui/react-toggle",
    "@radix-ui/react-toggle-group",
    "@radix-ui/react-tooltip",
  ],
  { cwd: projectDir },
);
run(
  [
    "bun",
    "add",
    "sonner",
    "cmdk",
    "vaul",
    "embla-carousel-react",
    "react-day-picker",
    "react-resizable-panels",
    "date-fns",
    "react-hook-form",
    "@hookform/resolvers",
    "zod",
  ],
  { cwd: projectDir },
);

console.log("Extracting shadcn/ui components...");
{
  const srcDir = path.join(projectDir, "src");
  const result = Bun.spawnSync(["tar", "-xzf", componentsTarball, "-C", srcDir], {
    stdout: "inherit",
    stderr: "pipe",
  });
  if (!result.success) {
    console.error(`Error: failed to extract ${componentsTarball}`);
    console.error(result.stderr.toString());
    process.exit(result.exitCode ?? 1);
  }
}

console.log("Creating components.json config...");
fs.writeFileSync(
  path.join(projectDir, "components.json"),
  JSON.stringify(
    {
      $schema: "https://ui.shadcn.com/schema.json",
      style: "default",
      rsc: false,
      tsx: true,
      tailwind: {
        config: "tailwind.config.js",
        css: "src/index.css",
        baseColor: "slate",
        cssVariables: true,
        prefix: "",
      },
      aliases: {
        components: "@/components",
        utils: "@/lib/utils",
        ui: "@/components/ui",
        lib: "@/lib",
        hooks: "@/hooks",
      },
    },
    null,
    2,
  ) + "\n",
);

console.log("");
console.log("Setup complete! You can now use Tailwind CSS and shadcn/ui in your project.");
console.log("");
console.log("Included components (40+ total):");
console.log("  - accordion, alert, aspect-ratio, avatar, badge, breadcrumb");
console.log("  - button, calendar, card, carousel, checkbox, collapsible");
console.log("  - command, context-menu, dialog, drawer, dropdown-menu");
console.log("  - form, hover-card, input, label, menubar, navigation-menu");
console.log("  - popover, progress, radio-group, resizable, scroll-area");
console.log("  - select, separator, sheet, skeleton, slider, sonner");
console.log("  - switch, table, tabs, textarea, toast, toggle, toggle-group, tooltip");
console.log("");
console.log("To start developing:");
console.log(`  cd ${projectName}`);
console.log("  bun run dev");
console.log("");
console.log("Import components like:");
console.log("  import { Button } from '@/components/ui/button'");
console.log("  import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'");
console.log("  import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'");
