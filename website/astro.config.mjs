// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
	site: 'https://octo-agent.dev',
	base: '/docs',
	integrations: [
		starlight({
			title: 'octo docs',
			description:
				'Documentation for octo-agent — the open, self-hostable, model-agnostic AI agent.',
			logo: {
				src: './src/assets/octo-mark.svg',
				replacesTitle: false,
			},
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/open-octo/octo-agent' },
			],
			editLink: {
				baseUrl: 'https://github.com/open-octo/octo-agent/edit/main/website/',
			},
			customCss: ['./src/styles/custom.css'],
			sidebar: [
				{
					label: 'Getting Started',
					items: [
						{ label: 'Install', slug: 'getting-started/install' },
						{ label: 'Quickstart', slug: 'getting-started/quickstart' },
						{ label: 'Choose a provider', slug: 'getting-started/choose-a-provider' },
					],
				},
				{
					label: 'Guides',
					items: [
						{ label: 'Use skills', slug: 'guides/use-skills' },
						{ label: 'Connect MCP servers', slug: 'guides/connect-mcp-servers' },
						{ label: 'Sandbox the agent', slug: 'guides/sandbox-the-agent' },
						{ label: 'Give it memory', slug: 'guides/memory' },
						{ label: 'Sub-agents & workflows', slug: 'guides/sub-agents-and-workflows' },
						{ label: 'Automate with hooks', slug: 'guides/hooks' },
						{ label: 'Bridge to chat apps', slug: 'guides/channels' },
						{ label: 'Automate with browser control', slug: 'guides/browser-automation' },
						{ label: 'Run long-horizon goals', slug: 'guides/goals' },
						{ label: 'Self-host octo serve', slug: 'guides/self-host' },
					],
				},
				{
					label: 'Concepts',
					items: [
						{ label: 'The agent loop', slug: 'concepts/agent-loop' },
						{ label: 'Configuration layers', slug: 'concepts/configuration-layers' },
						{ label: 'Sessions & history', slug: 'concepts/sessions-and-history' },
					],
				},
				{
					label: 'Reference',
					items: [
						{ label: 'CLI', slug: 'reference/cli' },
						{ label: 'Config file', slug: 'reference/config-file' },
						{ label: 'Tools', slug: 'reference/tools' },
						{ label: 'HTTP & SSE API', slug: 'reference/http-api' },
						{ label: 'Compatibility & exit codes', slug: 'reference/compatibility' },
						{ label: 'Security model', slug: 'reference/security' },
					],
				},
				{
					label: 'Architecture',
					items: [
						{ label: 'System layers', slug: 'architecture/system-layers' },
						{ label: 'Provider protocols', slug: 'architecture/provider-protocols' },
						{ label: 'Extending octo', slug: 'architecture/extending-octo' },
					],
				},
				{
					label: 'Community',
					items: [
						{ label: 'Contributing', slug: 'community/contributing' },
						{ label: 'Changelog', slug: 'community/changelog' },
						{ label: 'FAQ & troubleshooting', slug: 'community/faq' },
					],
				},
			],
		}),
	],
});
