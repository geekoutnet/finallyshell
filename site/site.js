// Finallyshell's site: the visitor's system, the latest release's files,
// the session list following the section in view, the server panels'
// tabs, and the copy buttons. Everything works without it: the links go
// to the releases page, and the panels all show.
(() => {
  const zh = document.documentElement.lang.startsWith("zh");
  const t = (en, cn) => (zh ? cn : en);
  const repo = "geekoutnet/finallyshell";

  // The visitor's system, for the download button and its column.
  const ua = navigator.userAgent;
  const os = /Windows/i.test(ua) ? "windows" : /Mac OS X|Macintosh/i.test(ua) ? "macos" : /Linux|X11/i.test(ua) && !/Android/i.test(ua) ? "linux" : "";
  const osName = { windows: "Windows", macos: "macOS", linux: "Linux" }[os];
  const get = document.querySelector("[data-get]");
  if (os) {
    if (get) get.lastChild.textContent = " " + t(`Download for ${osName}`, `下载 ${osName} 版`);
    const col = document.querySelector(`.system[data-os="${os}"]`);
    if (col) {
      col.classList.add("yours");
      const h = col.querySelector("h3");
      const tag = document.createElement("small");
      tag.textContent = t("Your system", "你的系统");
      h.append(tag);
    }
  }

  // The latest release: its version, and each file's own link.
  const patterns = {
    "mac-arm": /-arm64\.dmg$/, "mac-intel": /-amd64\.dmg$/,
    "win-setup": /^finallyshell-setup-.*\.exe$/, "win-zip": /-windows-amd64\.zip$/,
    "deb-amd64": /_amd64\.deb$/, "deb-arm64": /_arm64\.deb$/,
    "tar-amd64": /-linux-amd64\.tar\.gz$/, "tar-arm64": /-linux-arm64\.tar\.gz$/,
  };
  const size = (n) => (n > 1 << 20 ? (n / (1 << 20)).toFixed(0) + " MB" : Math.max(1, n >> 10) + " KB");
  fetch(`https://api.github.com/repos/${repo}/releases/latest`, { headers: { Accept: "application/vnd.github+json" } })
    .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
    .then((rel) => {
      const version = document.querySelector("[data-version]");
      if (version && rel.tag_name) {
        const date = rel.published_at ? new Date(rel.published_at).toLocaleDateString(zh ? "zh-CN" : "en") : "";
        version.innerHTML = "";
        const strong = document.createElement("strong");
        strong.textContent = rel.tag_name;
        version.append(t("Latest release ", "最新版本 "), strong, date ? t(`, published ${date}.`, `，发布于 ${date}。`) : "");
      }
      for (const a of rel.assets || []) {
        for (const [key, re] of Object.entries(patterns)) {
          if (!re.test(a.name)) continue;
          const link = document.querySelector(`[data-asset="${key}"]`);
          if (!link) continue;
          link.href = a.browser_download_url;
          link.querySelector("span").textContent = size(a.size);
          if (get && get.dataset.get === key) get.href = a.browser_download_url;
        }
      }
      // The button downloads the visitor's file straight away.
      if (get) {
        const pick = { windows: "win-setup", macos: "mac-arm", linux: "deb-amd64" }[os];
        const link = pick && document.querySelector(`[data-asset="${pick}"]`);
        if (link && link.href.includes("/download/")) get.href = os === "macos" ? "#download" : link.href;
      }
    })
    .catch(() => {});

  // The session list: the row of the section in view is selected.
  const rows = [...document.querySelectorAll(".sessions a")];
  const byId = new Map(rows.map((a) => [a.hash.slice(1), a]));
  const select = (id) => {
    for (const a of rows) a.setAttribute("aria-current", String(a.hash === "#" + id));
    const row = byId.get(id);
    const strip = row && row.closest("ol");
    if (strip && strip.scrollWidth > strip.clientWidth) {
      strip.scrollTo({ left: row.offsetLeft - 24, behavior: "smooth" });
    }
  };
  const seen = new Map();
  const io = new IntersectionObserver((entries) => {
    for (const e of entries) seen.set(e.target.id, e.isIntersecting ? e.intersectionRatio : 0);
    let best = "", most = 0;
    for (const [id, r] of seen) if (r > most) { best = id; most = r; }
    if (best) select(best);
  }, { rootMargin: "-20% 0px -55% 0px", threshold: [0, 0.1, 0.25, 0.5, 0.75, 1] });
  for (const id of byId.keys()) {
    const el = document.getElementById(id);
    if (el) io.observe(el);
  }

  // The top bar draws its line once the page scrolls.
  const top = document.querySelector(".top");
  const onScroll = () => top.classList.toggle("scrolled", scrollY > 8);
  addEventListener("scroll", onScroll, { passive: true });
  onScroll();

  // The server panels' tabs, with the arrow keys as tabs have them.
  for (const list of document.querySelectorAll('[role="tablist"]')) {
    const tabs = [...list.querySelectorAll('[role="tab"]')];
    const show = (tab, focus) => {
      for (const t2 of tabs) {
        const on = t2 === tab;
        t2.setAttribute("aria-selected", String(on));
        t2.tabIndex = on ? 0 : -1;
        document.getElementById(t2.getAttribute("aria-controls")).hidden = !on;
      }
      if (focus) tab.focus();
    };
    tabs.forEach((tab, i) => {
      tab.addEventListener("click", () => show(tab));
      tab.addEventListener("keydown", (e) => {
        const step = { ArrowRight: 1, ArrowLeft: -1 }[e.key];
        if (step) { e.preventDefault(); show(tabs[(i + step + tabs.length) % tabs.length], true); }
        if (e.key === "Home") { e.preventDefault(); show(tabs[0], true); }
        if (e.key === "End") { e.preventDefault(); show(tabs[tabs.length - 1], true); }
      });
    });
    show(tabs[0]);
  }

  // The copy buttons of the commands.
  for (const b of document.querySelectorAll(".cmd button")) {
    b.addEventListener("click", async () => {
      const text = b.parentElement.querySelector("code").textContent;
      try {
        await navigator.clipboard.writeText(text);
        b.textContent = t("Copied", "已复制");
      } catch {
        b.textContent = t("Select it", "请手动复制");
      }
      setTimeout(() => (b.textContent = t("Copy", "复制")), 1600);
    });
  }
})();
