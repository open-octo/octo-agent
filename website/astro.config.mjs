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
			defaultLocale: 'root',
			locales: {
				root: { label: 'English', lang: 'en' },
				zh: { label: '简体中文', lang: 'zh-CN' },
			},
			sidebar: [
				{
					label: 'Blog',
					translations: { 'zh-CN': '博客' },
					link: 'https://octo-agent.dev/blog/',
					attrs: { target: '_blank' },
				},
				{
					label: 'Getting Started',
					translations: { 'zh-CN': '快速上手' },
					items: [
						{ label: 'Install', slug: 'getting-started/install', translations: { 'zh-CN': '安装' } },
						{ label: 'Quickstart', slug: 'getting-started/quickstart', translations: { 'zh-CN': '快速开始' } },
						{ label: 'Choose a provider', slug: 'getting-started/choose-a-provider', translations: { 'zh-CN': '选择 Provider' } },
					],
				},
				{
					label: 'Guides',
					translations: { 'zh-CN': '指南' },
					items: [
						{ label: 'Use skills', slug: 'guides/use-skills', translations: { 'zh-CN': '使用 Skills' } },
						{ label: 'Connect MCP servers', slug: 'guides/connect-mcp-servers', translations: { 'zh-CN': '接入 MCP 服务' } },
						{ label: 'Sandbox the agent', slug: 'guides/sandbox-the-agent', translations: { 'zh-CN': '沙箱化运行' } },
						{ label: 'Give it memory', slug: 'guides/memory', translations: { 'zh-CN': '让它拥有记忆' } },
						{ label: 'Sub-agents', slug: 'guides/sub-agents', translations: { 'zh-CN': '子代理' } },
						{ label: 'Workflows', slug: 'guides/workflows', translations: { 'zh-CN': '工作流' } },
						{ label: 'Automate with hooks', slug: 'guides/hooks', translations: { 'zh-CN': '用 Hooks 自动化' } },
						{ label: 'Bridge to chat apps', slug: 'guides/channels', translations: { 'zh-CN': '接入聊天应用' } },
						{ label: 'Automate with browser control', slug: 'guides/browser-automation', translations: { 'zh-CN': '浏览器自动化' } },
						{ label: 'Run long-horizon goals', slug: 'guides/goals', translations: { 'zh-CN': '运行长周期目标' } },
						{ label: 'Self-host octo serve', slug: 'guides/self-host', translations: { 'zh-CN': '自托管 octo serve' } },
					],
				},
				{
					label: 'Concepts',
					translations: { 'zh-CN': '核心概念' },
					items: [
						{ label: 'The agent loop', slug: 'concepts/agent-loop', translations: { 'zh-CN': 'Agent 循环' } },
						{ label: 'History compaction', slug: 'concepts/compaction', translations: { 'zh-CN': '历史压缩' } },
						{ label: 'Configuration layers', slug: 'concepts/configuration-layers', translations: { 'zh-CN': '配置分层' } },
						{ label: 'Sessions & history', slug: 'concepts/sessions-and-history', translations: { 'zh-CN': '会话与历史' } },
					],
				},
				{
					label: 'Reference',
					translations: { 'zh-CN': '参考手册' },
					items: [
						{ label: 'CLI', slug: 'reference/cli', translations: { 'zh-CN': 'CLI 参考' } },
						{ label: 'Slash commands', slug: 'reference/slash-commands', translations: { 'zh-CN': 'Slash 命令' } },
						{ label: 'Config file', slug: 'reference/config-file', translations: { 'zh-CN': '配置文件' } },
						{ label: 'Tools', slug: 'reference/tools', translations: { 'zh-CN': '工具参考' } },
						{ label: 'Permissions', slug: 'reference/permissions', translations: { 'zh-CN': '权限系统' } },
						{ label: 'HTTP & SSE API', slug: 'reference/http-api', translations: { 'zh-CN': 'HTTP 与 SSE API' } },
						{ label: 'Compatibility & exit codes', slug: 'reference/compatibility', translations: { 'zh-CN': '兼容性与退出码' } },
						{ label: 'Security model', slug: 'reference/security', translations: { 'zh-CN': '安全模型' } },
					],
				},
				{
					label: 'Architecture',
					translations: { 'zh-CN': '架构' },
					items: [
						{ label: 'System layers', slug: 'architecture/system-layers', translations: { 'zh-CN': '系统分层' } },
						{ label: 'Provider protocols', slug: 'architecture/provider-protocols', translations: { 'zh-CN': 'Provider 协议' } },
						{ label: 'Extending octo', slug: 'architecture/extending-octo', translations: { 'zh-CN': '扩展 octo' } },
					],
				},
				{
					label: 'Community',
					translations: { 'zh-CN': '社区' },
					items: [
						{ label: 'Contributing', slug: 'community/contributing', translations: { 'zh-CN': '贡献指南' } },
						{ label: 'Changelog', slug: 'community/changelog', translations: { 'zh-CN': '更新日志' } },
						{ label: 'FAQ & troubleshooting', slug: 'community/faq', translations: { 'zh-CN': '常见问题' } },
					],
				},
			],
		}),
	],
});
