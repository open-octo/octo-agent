# octo blog

The octo-agent blog, built with [Astro](https://astro.build) and deployed to `/blog` on GitHub Pages.

## Writing a post

1. Create a new Markdown file under `src/content/posts/zh/` for Chinese or `src/content/posts/en/` for English.
2. Add frontmatter:

```yaml
---
title: "Your post title"
description: "A short description"
pubDate: 2026-07-04
author: "octo-agent team"
tags: ["octo-agent", "workflow"]
locale: en   # or zh
originalSlug: your-post-slug
---
```

3. Write the post in Markdown.
4. Run `npm run build` to verify.

## Bilingual support

- English posts live in `src/content/posts/en/` and are served at `/blog/posts/<slug>/`.
- Chinese posts live in `src/content/posts/zh/` and are served at `/blog/posts/zh/<slug>/`.
- The homepage has both `/blog/` (English) and `/blog/zh/` (Chinese) versions.
- Each post and page includes a language switcher to the corresponding alternate version.
- Use `originalSlug` to link the two language versions of the same post.

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
