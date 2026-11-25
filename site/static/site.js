// 每页都加载。语言切换：先把白色滑块滑到目标语言，再跳到另一种语言的同一页；
// 鼠标移上去就预取那一页，跳过去几乎不用等。开了「减少动态效果」时直接跳。
(() => {
  const sw = document.querySelector(".lang-switch");
  if (!sw) return;
  const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const items = [...sw.children];
  const prefetched = new Set();
  for (const a of sw.querySelectorAll("a")) {
    const prefetch = () => {
      if (prefetched.has(a.href)) return;
      prefetched.add(a.href);
      const l = document.createElement("link");
      l.rel = "prefetch";
      l.href = a.href;
      document.head.append(l);
    };
    a.addEventListener("pointerenter", prefetch);
    a.addEventListener("focus", prefetch);
    a.addEventListener("click", (e) => {
      if (reduce || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
      e.preventDefault();
      prefetch();
      sw.style.setProperty("--pos", items.indexOf(a));
      a.classList.add("to");
      sw.classList.add("going");
      setTimeout(() => (location.href = a.href), 280);
    });
  }
  // 从浏览器后退回来时（bfcache）滑块还停在目标语言上，复原
  addEventListener("pageshow", (e) => {
    if (!e.persisted) return;
    sw.classList.toggle("going", false);
    sw.querySelectorAll(".to").forEach((a) => a.classList.toggle("to", false));
    sw.style.setProperty("--pos", items.findIndex((el) => el.classList.contains("on")));
  });
})();
