// flash 把列表里的一个元素滚到看得见的地方，带上 data-flash 闪一下(样式由各自的元素定),过一会儿收掉;
// 左栏点了集合或组时的那一节、刚入库的未纳管技能换成的那一行、技能库页上刚入库的几行共用。返回的函数立刻收掉闪烁，给 effect 的清理用。
const flashMs = 1200;

// block 为 null 时不滚动，只闪(几行一起闪时，只滚到第一行)。
export function flash(el: HTMLElement, block: ScrollLogicalPosition | null, done?: () => void): () => void {
  if (block) el.scrollIntoView({ block, behavior: "smooth" });
  el.dataset.flash = "";
  const timer = window.setTimeout(() => {
    delete el.dataset.flash;
    done?.();
  }, flashMs);
  return () => {
    window.clearTimeout(timer);
    delete el.dataset.flash;
  };
}
