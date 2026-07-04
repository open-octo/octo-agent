# octo blog

The octo-agent blog, built with [Astro](https://astro.build) and deployed to `/blog` on GitHub Pages.

## Writing a post

1. Create a new Markdown file in `src/content/posts/`.
2. Add frontmatter:

```yaml
---
title: "Your post title"
description: "A short description"
pubDate: 2026-07-04
author: "octo-agent team"
tags: ["octo-agent", "workflow"]
---
```

3. Write the post in Markdown.
4. Run `npm run build` to verify.

## Development

```bash
npm install
npm run dev
```

## Build

```bash
npm run build
```

Output goes to `dist/`.

## Deployment

The blog is built and deployed by `.github/workflows/pages.yml` alongside the main docs site and landing page.
