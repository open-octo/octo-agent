/*
 * octo shared chrome — one source of truth for the top nav + footer across
 * the landing page (static HTML) and the blog (Astro). Drop this one file in
 * anywhere and use the custom elements; styles are self-contained (shadow DOM)
 * so the nav looks identical regardless of the host page's CSS.
 *
 *   <script src="/octo-nav.js" defer></script>
 *   <octo-site-nav variant="landing" active="home" locale="en"></octo-site-nav>
 *   <octo-site-nav variant="site" active="blog" locale="en" alt="/blog/"></octo-site-nav>
 *   <octo-site-footer locale="en"></octo-site-footer>
 *
 * Attributes:
 *   variant     "landing" (adds in-page section anchors) | "site" (default)
 *   active      "home" | "docs" | "blog"
 *   locale      "en" | "zh"
 *   alt         URL of the alternate-language version of the current page
 *   lang-toggle present → the EN/中文 pill flips text in place (for the landing
 *               page's bilingual single-page toggle) instead of navigating.
 *               It updates every [data-en]/[data-zh] and [data-href-en]/
 *               [data-href-zh] element in the host document, so the landing
 *               page can delete its own inline nav + language script.
 */
(() => {
	// Both attrs (`alt`) and same-document data-attrs (`data-href-en/zh`) are
	// treated as untrusted: escape before splicing into innerHTML, and refuse
	// dangerous URL schemes before handing a value to setAttribute('href', …).
	const escapeHtml = (s) =>
		String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
	// Parse via the URL API and require the result to carry a same-flow http(s)
	// protocol check right at the call site — inlined, not behind a helper
	// function, so the scheme guard visibly dominates every setAttribute('href')
	// sink below (a wrapped helper's "return the input unchanged" branch still
	// reads as tainted data reaching the sink to static analysis).
	function parseUrl(s) {
		try {
			return new URL(String(s ?? ''), location.origin);
		} catch {
			return null;
		}
	}

	const BLUE = '#1677FF',
		HOVER = '#4096FF';
	const MARK = (s) =>
		`<svg width="${s}" height="${s}" viewBox="0 0 100 100" aria-hidden="true"><rect width="100" height="100" rx="22" fill="${BLUE}"/><path fill="#fff" d="M50 18 C34 18 26 30 26 44 C26 54 32 60 36 62 C34 70 28 78 20 82 C18 83 18 86 20 88 C22 89 25 89 27 87 C33 83 39 76 42 68 C44 69 47 70 50 70 L50 82 C50 85 53 87 56 86 C58 85 59 83 58 81 L56 70 C59 70 62 69 64 68 C67 76 73 83 79 87 C81 89 84 89 86 88 C88 86 88 83 86 82 C78 78 72 70 70 62 C74 60 80 54 80 44 C80 30 72 18 56 18 Z"/><circle cx="40" cy="42" r="5" fill="${BLUE}"/><circle cx="60" cy="42" r="5" fill="${BLUE}"/></svg>`;
	const GH =
		'<svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>';
	const DL =
		'<svg width="15" height="15" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M8 1.5a.75.75 0 01.75.75v6.19l1.72-1.72a.75.75 0 111.06 1.06l-3 3a.75.75 0 01-1.06 0l-3-3a.75.75 0 111.06-1.06l1.72 1.72V2.25A.75.75 0 018 1.5zM2.75 12a.75.75 0 01.75.75v.75c0 .14.11.25.25.25h8.5a.25.25 0 00.25-.25v-.75a.75.75 0 011.5 0v.75A1.75 1.75 0 0112.25 15h-8.5A1.75 1.75 0 012 13.5v-.75a.75.75 0 01.75-.75z"/></svg>';

	const T = {
		en: { docs: 'Docs', blog: 'Blog', star: 'Star', dl: 'Download', legal: 'MIT licensed',
			s: [['#arms', 'Interfaces'], ['#features', 'Features'], ['#demo', 'Demo'], ['#faq', 'FAQ']] },
		zh: { docs: '文档', blog: '博客', star: 'Star', dl: '下载', legal: 'MIT 许可',
			s: [['#arms', '界面'], ['#features', '特性'], ['#demo', '演示'], ['#faq', '常见问题']] },
	};

	const BASE = `
		:host{display:block;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,"PingFang SC","Microsoft YaHei",sans-serif;}
		a{text-decoration:none;color:inherit;}
		.nav{position:sticky;top:0;z-index:100;background:rgba(255,255,255,.92);backdrop-filter:blur(8px);border-bottom:1px solid #EEEFF1;}
		.inner{max-width:1080px;margin:0 auto;display:flex;align-items:center;gap:24px;height:60px;padding:0 24px;}
		.brand{display:flex;align-items:center;gap:10px;color:rgba(0,0,0,.88);font-weight:600;font-size:17px;}
		.brand svg{display:block;}
		.links{flex:1;display:flex;gap:24px;margin-left:16px;font-size:14px;}
		.links a{color:rgba(0,0,0,.65);}
		.links a:hover{color:${BLUE};}
		.links a.active{color:${BLUE};font-weight:600;}
		.ghost{display:inline-flex;align-items:center;gap:8px;padding:5px 14px;border:1px solid #D9D9D9;border-radius:6px;font-size:13px;font-weight:600;color:rgba(0,0,0,.88);}
		.ghost:hover{border-color:${HOVER};color:${BLUE};}
		.lang{display:inline-flex;align-items:center;border:1px solid #D9D9D9;border-radius:9999px;padding:2px;font-size:12px;font-weight:600;}
		.lang .seg{color:rgba(0,0,0,.45);padding:3px 12px;border-radius:9999px;line-height:1;}
		.lang .seg.active{background:${BLUE};color:#fff;}
		.lang a.seg:hover{color:${BLUE};}
		.lang a.seg.active:hover{color:#fff;}
		.cta{display:inline-flex;align-items:center;gap:8px;padding:6px 16px;background:${BLUE};border-radius:6px;color:#fff;font-size:14px;font-weight:600;}
		.cta:hover{background:${HOVER};}
		@media(max-width:820px){.inner{gap:12px;padding:0 16px;}.links{gap:16px;margin-left:8px;}.lang{display:none;}}
		@media(max-width:560px){.ghost span{display:none;}.links{display:none;}}
	`;
	const FOOT = `
		:host{display:block;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif;}
		a{text-decoration:none;}
		.foot{border-top:1px solid #EEEFF1;background:#fff;padding:40px 0;}
		.inner{max-width:1080px;margin:0 auto;padding:0 24px;display:flex;align-items:center;gap:12px;font-size:13px;color:rgba(0,0,0,.45);}
		.inner svg{display:block;}
		.links{display:flex;gap:20px;margin-left:auto;}
		.links a{color:rgba(0,0,0,.65);}
		.links a:hover{color:${BLUE};}
	`;

	// Flip every bilingual node in the host document (landing-page toggle).
	function applyLang(lang) {
		const isZh = lang === 'zh';
		document.documentElement.lang = isZh ? 'zh-CN' : 'en';
		document.querySelectorAll('[data-en],[data-zh]').forEach((el) => {
			const v = el.getAttribute(isZh ? 'data-zh' : 'data-en');
			if (v != null) el.textContent = v;
		});
		document.querySelectorAll('[data-href-en],[data-href-zh]').forEach((el) => {
			const raw = el.getAttribute(isZh ? 'data-href-zh' : 'data-href-en');
			if (raw == null) return;
			const u = parseUrl(raw);
			if (u && (u.protocol === 'http:' || u.protocol === 'https:')) el.setAttribute('href', u.href);
		});
		document.querySelectorAll('octo-site-nav[lang-toggle],octo-site-footer[lang-toggle]').forEach((el) => {
			el.setAttribute('locale', lang);
			el.connectedCallback && el.connectedCallback();
		});
	}

	class Nav extends HTMLElement {
		connectedCallback() {
			const loc = this.getAttribute('locale') === 'zh' ? 'zh' : 'en';
			const active = this.getAttribute('active') || '';
			const variant = this.getAttribute('variant') || 'site';
			const toggle = this.hasAttribute('lang-toggle');
			const isZh = loc === 'zh';
			const t = T[loc];
			const blogHome = isZh ? '/blog/' : '/blog/en/';
			const docsHref = isZh ? '/docs/zh/' : '/docs/';
			const altAttr = this.getAttribute('alt');
			const altUrl = altAttr == null ? null : parseUrl(altAttr);
			const altSafe = altUrl && (altUrl.protocol === 'http:' || altUrl.protocol === 'https:')
				? altUrl.href
				: isZh ? '/blog/en/' : '/blog/';
			const alt = escapeHtml(altSafe);
			const releases = 'https://github.com/open-octo/octo-agent/releases/latest';
			const repo = 'https://github.com/open-octo/octo-agent';
			const sections =
				variant === 'landing'
					? t.s.map(([h, l]) => `<a href="${h}">${l}</a>`).join('')
					: '';
			const cls = (k) => (active === k ? ' class="active"' : '');
			const langPill = toggle
				? `<div class="lang" role="group" aria-label="Language">
						<button type="button" class="seg${isZh ? ' active' : ''}" data-lang="zh">中文</button>
						<button type="button" class="seg${isZh ? '' : ' active'}" data-lang="en">EN</button>
					</div>`
				: `<div class="lang" role="group" aria-label="Language">
						<a class="seg${isZh ? ' active' : ''}"${isZh ? '' : ` href="${alt}"`}>中文</a>
						<a class="seg${isZh ? '' : ' active'}"${isZh ? ` href="${alt}"` : ''}>EN</a>
					</div>`;
			const root = this.shadowRoot || this.attachShadow({ mode: 'open' });
			root.innerHTML = `<style>${BASE}${toggle ? '.lang button{background:none;border:0;cursor:pointer;font:inherit;}' : ''}</style>
				<div class="nav"><div class="inner">
					<a class="brand" href="https://octo-agent.dev/">${MARK(28)} Octo</a>
					<nav class="links">
						${sections}
						<a href="${docsHref}"${cls('docs')}>${t.docs}</a>
						<a href="${blogHome}"${cls('blog')}>${t.blog}</a>
					</nav>
					<a class="ghost" href="${repo}">${GH}<span>${t.star}</span></a>
					${langPill}
					<a class="cta" href="${releases}">${DL}<span>${t.dl}</span></a>
				</div></div>`;
			if (toggle) {
				root.querySelectorAll('.lang button').forEach((b) =>
					b.addEventListener('click', () => applyLang(b.dataset.lang)),
				);
			}
		}
	}

	class Footer extends HTMLElement {
		connectedCallback() {
			const loc = this.getAttribute('locale') === 'zh' ? 'zh' : 'en';
			const isZh = loc === 'zh';
			const t = T[loc];
			const docsHref = isZh ? '/docs/zh/' : '/docs/';
			const blogHome = isZh ? '/blog/' : '/blog/en/';
			const year = new Date().getFullYear();
			const root = this.shadowRoot || this.attachShadow({ mode: 'open' });
			root.innerHTML = `<style>${FOOT}</style>
				<div class="foot"><div class="inner">
					${MARK(20)}
					<span>© ${year} octo-agent · ${t.legal}</span>
					<div class="links"><a href="${docsHref}">${t.docs}</a><a href="${blogHome}">${t.blog}</a><a href="https://github.com/open-octo/octo-agent">GitHub</a></div>
				</div></div>`;
		}
	}

	if (!customElements.get('octo-site-nav')) customElements.define('octo-site-nav', Nav);
	if (!customElements.get('octo-site-footer')) customElements.define('octo-site-footer', Footer);
})();
