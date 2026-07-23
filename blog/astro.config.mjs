// @ts-check
import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';

// https://astro.build/config
export default defineConfig({
	site: 'https://octo-agent.dev',
	base: '/blog',
	integrations: [
		sitemap({
			customPages: [
				'https://octo-agent.dev/blog/',
				'https://octo-agent.dev/blog/zh/',
			],
		}),
	],
});
