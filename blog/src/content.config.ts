import { defineCollection, z } from 'astro:content';
import { glob } from 'astro/loaders';

const postsCollection = defineCollection({
	loader: glob({ pattern: '**/*.md', base: './src/content/posts' }),
	schema: z.object({
		title: z.string(),
		description: z.string(),
		pubDate: z.coerce.date(),
		updatedDate: z.coerce.date().optional(),
		author: z.string().default('octo-agent team'),
		tags: z.array(z.string()).default([]),
		draft: z.boolean().default(false),
		locale: z.enum(['en', 'zh']).default('zh'),
		originalSlug: z.string().optional(),
	}),
});

export const collections = {
	posts: postsCollection,
};
