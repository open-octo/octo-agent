// ── I18N — internationalisation ──────────────────────────────────────────
//
// Lightweight i18n using data-i18n attributes. Supports en and zh.
// Usage:
//   <span data-i18n="key.name">Fallback text</span>
//
// Call I18N.init() on boot. The locale is read from localStorage("octo-lang")
// or the browser's navigator.language.
// ─────────────────────────────────────────────────────────────────────────

const I18N = (() => {
  let _locale = "en";

  const _strings = {
    en: {
      "auth.subtitle": "Enter your access key to continue",
      "auth.submit": "Connect",
      "auth.required": "Access key is required",
      "sidebar.chat": "Sessions",
      "sidebar.config": "Config",
      "sidebar.dataSection": "My Data",
      "sidebar.tasks": "Scheduled Tasks",
      "sidebar.skills": "Skills",
      "sidebar.channels": "Channels",
      "sidebar.settings": "Settings",
      "sidebar.profile": "Profile & Soul",
      "sidebar.trash": "File Recall",
      "sessions.newSession": "+ New Session",
      "sessions.newSessionAdvanced": "More Options",
      "welcome.title": "Welcome to Octo",
      "welcome.body": "Create a new session or select one from the sidebar.",
      "welcome.btn": "New Session",
      "tasks.title": "Scheduled Tasks",
      "tasks.subtitle": "Manage and schedule automated tasks for your assistant",
      "tasks.btn.create": "Create Task",
      "skills.title": "Skills",
      "skills.subtitle": "Extend your assistant's capabilities with custom skills",
      "skills.btn.import": "Import",
      "skills.btn.create": "Create",
      "skills.tab.my": "My Skills",
      "skills.import.placeholder": "Paste ZIP or GitHub URL…",
      "skills.import.install": "Install",
      "channels.title": "Channels",
      "channels.subtitle": "Connect IM platforms so your users can chat with the assistant",
      "trash.title": "File Recall",
      "trash.subtitle": "Files the agent moved to trash across all projects.",
      "trash.refresh": "Refresh",
      "trash.emptyOld": "Empty >7 days",
      "trash.emptyAll": "Empty all",
      "profile.title": "Assistant Memory",
      "profile.subtitle": "A window into the assistant's inner life.",
      "profile.tab.soul": "Soul",
      "profile.tab.user": "User",
      "profile.tab.memories": "Memories",
      "settings.title": "Settings",
      "settings.models.title": "AI Models",
      "settings.models.add": "+ Add Model",
      "settings.personalize.title": "Personalize",
      "settings.personalize.desc": "Re-run the onboarding to update your assistant's personality.",
      "settings.personalize.btn": "✨ Re-run Onboard",
      "settings.lang.title": "Language",
      "settings.lang.en": "English",
      "settings.lang.zh": "中文",
      "chat.input.placeholder": "Message… (Enter to send, Shift+Enter for newline)",
      "chat.btn.send": "Send",
      "modal.ok": "OK",
      "modal.cancel": "Cancel",
    },
    zh: {
      "auth.subtitle": "请输入访问密钥以继续",
      "auth.submit": "连接",
      "auth.required": "请输入访问密钥",
      "sidebar.chat": "会话",
      "sidebar.config": "配置",
      "sidebar.dataSection": "我的数据",
      "sidebar.tasks": "定时任务",
      "sidebar.skills": "技能管理",
      "sidebar.channels": "频道管理",
      "sidebar.settings": "设置",
      "sidebar.profile": "个人档案",
      "sidebar.trash": "文件回收",
      "sessions.newSession": "+ 新会话",
      "sessions.newSessionAdvanced": "更多选项",
      "welcome.title": "欢迎使用 Octo",
      "welcome.body": "创建一个新会话，或从侧边栏中选择一个已有会话。",
      "welcome.btn": "新会话",
      "tasks.title": "定时任务",
      "tasks.subtitle": "管理和调度自动化任务",
      "tasks.btn.create": "创建任务",
      "skills.title": "技能",
      "skills.subtitle": "使用自定义技能扩展助手的能力",
      "skills.btn.import": "导入",
      "skills.btn.create": "创建",
      "skills.tab.my": "我的技能",
      "skills.import.placeholder": "粘贴 ZIP 或 GitHub 链接…",
      "skills.import.install": "安装",
      "channels.title": "频道",
      "channels.subtitle": "连接 IM 平台，让用户通过飞书/企业微信与助手对话",
      "trash.title": "文件回收",
      "trash.subtitle": "助手在所有项目中移到回收站的文件。",
      "trash.refresh": "刷新",
      "trash.emptyOld": "清空 7 天前",
      "trash.emptyAll": "全部清空",
      "profile.title": "助手记忆",
      "profile.subtitle": "了解助手的内心世界。",
      "profile.tab.soul": "灵魂",
      "profile.tab.user": "用户",
      "profile.tab.memories": "记忆",
      "settings.title": "设置",
      "settings.models.title": "AI 模型",
      "settings.models.add": "+ 添加模型",
      "settings.personalize.title": "个性化",
      "settings.personalize.desc": "重新运行引导程序以更新助手的个性。",
      "settings.personalize.btn": "✨ 重新引导",
      "settings.lang.title": "语言",
      "settings.lang.en": "English",
      "settings.lang.zh": "中文",
      "chat.input.placeholder": "输入消息… (Enter 发送，Shift+Enter 换行)",
      "chat.btn.send": "发送",
      "modal.ok": "确定",
      "modal.cancel": "取消",
    },
  };

  function init() {
    // Read locale from localStorage or browser.
    const saved = localStorage.getItem("octo-lang");
    if (saved && (saved === "en" || saved === "zh")) {
      _locale = saved;
    } else {
      const nav = navigator.language || "";
      _locale = nav.startsWith("zh") ? "zh" : "en";
    }
    apply();
  }

  function apply() {
    document.documentElement.lang = _locale;

    // Walk all elements with data-i18n attribute.
    document.querySelectorAll("[data-i18n]").forEach(el => {
      const key = el.getAttribute("data-i18n");
      if (!key) return;
      const text = t(key);
      if (text) {
        if (el.tagName === "INPUT" || el.tagName === "TEXTAREA") {
          el.placeholder = text;
        } else {
          el.textContent = text;
        }
      }
    });
  }

  function t(key) {
    const dict = _strings[_locale] || _strings.en;
    return dict[key] || key;
  }

  function setLocale(loc) {
    _locale = loc;
    localStorage.setItem("octo-lang", loc);
    apply();
  }

  function getLocale() {
    return _locale;
  }

  return { init, t, setLocale, getLocale, apply };
})();

// Auto-init on script load.
I18N.init();
