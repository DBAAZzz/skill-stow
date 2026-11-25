// 首页动画。页面本身是终态（开关已关、数字已写好），这里只负责把它演出来；
// 系统开了「减少动态效果」时什么都不做。
(() => {
  const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const $ = (s, root = document) => root.querySelector(s);
  const $$ = (s, root = document) => [...root.querySelectorAll(s)];

  // 顶栏滚动后收紧；首屏窗口随滚动从倾斜放平
  const header = $(".site-header");
  const tilt = $(".tilt");
  let ticking = false;
  const onScroll = () => {
    if (ticking) return;
    ticking = true;
    requestAnimationFrame(() => {
      const y = scrollY;
      header.classList.toggle("scrolled", y > 20);
      if (!reduce && tilt) {
        const p = Math.min(1, y / 420);
        tilt.style.setProperty("--tilt", (18 * (1 - p)).toFixed(2) + "deg");
        tilt.style.setProperty("--sc", (0.92 + 0.08 * p).toFixed(3));
      }
      ticking = false;
    });
  };
  addEventListener("scroll", onScroll, { passive: true });
  onScroll();

  // 「首次打开说明」这类锚点直接展开对应的问答
  const openTarget = (hash) => {
    const el = hash && document.getElementById(hash.slice(1));
    if (el && el.tagName === "DETAILS") el.open = true;
  };
  $$('a[href^="#"]').forEach((a) => a.addEventListener("click", () => openTarget(a.hash)));
  openTarget(location.hash);

  if (reduce) return;

  // 标题逐词浮现；中文按词组拆
  const h1 = $("h1.split");
  if (h1) {
    const seg = "Segmenter" in Intl ? new Intl.Segmenter(document.documentElement.lang, { granularity: "word" }) : null;
    const words = (text) => (seg ? [...seg.segment(text)].map((s) => s.segment) : text.split(/(\s+)/));
    let i = 0;
    const word = (el) => {
      el.classList.add("w");
      el.style.setProperty("--i", i++);
      return el;
    };
    for (const n of [...h1.childNodes]) {
      if (n.nodeType !== Node.TEXT_NODE) {
        word(n);
        continue;
      }
      const frag = document.createDocumentFragment();
      // 标点粘在前一个词上，免得单独占一拍
      const parts = words(n.textContent).reduce((acc, t) => {
        if (acc.length && /^[\s\p{P}]+$/u.test(t)) acc[acc.length - 1] += t;
        else acc.push(t);
        return acc;
      }, []);
      for (const t of parts) {
        const lead = t.match(/^\s*/)[0];
        if (lead) frag.append(lead);
        const body = t.slice(lead.length);
        if (!body) continue;
        const trail = body.match(/\s*$/)[0];
        const s = document.createElement("span");
        s.textContent = body.slice(0, body.length - trail.length);
        frag.append(word(s));
        if (trail) frag.append(trail);
      }
      n.replaceWith(frag);
    }
  }

  // 光晕跟着鼠标做一点视差
  const hero = $("#hero");
  const glow = $(".glow");
  if (hero && glow) {
    hero.addEventListener("pointermove", (e) => {
      const r = hero.getBoundingClientRect();
      glow.style.setProperty("--mx", ((e.clientX - r.left) / r.width - 0.5) * 40 + "px");
      glow.style.setProperty("--my", ((e.clientY - r.top) / r.height - 0.5) * 30 + "px");
    });
  }

  // 进入视口时加 .in
  const tiles = $(".tiles");
  const demo = $(".demo");
  const io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue;
        e.target.classList.add("in");
        io.unobserve(e.target);
        if (e.target === tiles) countUp($(".agents .count"));
        if (e.target === demo) runDemo();
        $$(".feat .count", e.target).forEach(countUp);
        $$("[data-type]", e.target).forEach(typeIn);
      }
    },
    { threshold: 0.25, rootMargin: "0px 0px -8% 0px" },
  );
  $$(".reveal").forEach((el) => io.observe(el));
  if (tiles) io.observe(tiles);

  // 数字从 0 数上去
  function countUp(el) {
    if (!el) return;
    const to = +el.dataset.to;
    const t0 = performance.now();
    const step = (t) => {
      const p = Math.min(1, (t - t0) / 1200);
      el.textContent = Math.round(to * (1 - Math.pow(1 - p, 3)));
      if (p < 1) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  }

  // 搜索框里逐字打出查询词
  async function typeIn(el) {
    const text = el.textContent;
    el.textContent = "";
    el.classList.add("typing");
    await new Promise((r) => setTimeout(r, 500));
    for (const ch of text) {
      el.textContent += ch;
      await new Promise((r) => setTimeout(r, 110));
    }
    setTimeout(() => el.classList.toggle("typing", false), 1600);
  }

  // 开关演示：光标点下第一个开关，配置文件里逐字写出新的一行，循环
  async function runDemo() {
    const toggle = $('[data-demo="toggle"]', demo);
    const cfg = $('[data-demo="config"]', demo);
    const cur = $('[data-demo="cursor"]', demo);
    const line = $(".new", cfg);
    if (!toggle || !cfg || !cur || !line) return;
    const plain = line.textContent;
    const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
    const place = (x, y) => (cur.style.transform = `translate(${x}px, ${y}px)`);
    const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;");
    const colored = (typed) => {
      const cut = typed.indexOf(":");
      return cut < 0
        ? `<span class="k">${esc(typed)}</span>`
        : `<span class="k">${esc(typed.slice(0, cut))}</span>: <span class="s">${esc(typed.slice(cut + 2))}</span>`;
    };
    const caret = document.createElement("span");
    caret.className = "caret";
    for (;;) {
      toggle.classList.add("on");
      line.textContent = "";
      line.after(caret);
      const d = demo.getBoundingClientRect();
      const t = toggle.getBoundingClientRect();
      const x = t.left - d.left + 16;
      const y = t.top - d.top + 10;
      place(x + 120, y + 160);
      cur.style.opacity = 1;
      await sleep(400);
      place(x, y);
      await sleep(1100);
      cur.classList.add("click");
      toggle.classList.remove("on");
      await sleep(300);
      cur.classList.remove("click");
      for (let k = 1; k <= plain.length; k++) {
        line.innerHTML = colored(plain.slice(0, k));
        await sleep(45);
      }
      await sleep(400);
      place(x + 140, y + 200);
      cur.style.opacity = 0;
      caret.remove();
      await sleep(2600);
    }
  }

})();
