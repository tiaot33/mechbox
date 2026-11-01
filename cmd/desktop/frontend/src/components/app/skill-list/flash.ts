// flash 把列表里的一个元素滚到看得见的地方，带上 data-flash 闪一下(样式由各自的元素定),过一会儿收掉;
// 左栏点了集合或组时的那一节、刚入库的未纳管技能换成的那一行共用。返回的函数立刻收掉闪烁，给 effect 的清理用。
const flashMs = 1200;

export function flash(el: HTMLElement, block: ScrollLogicalPosition, done?: () => void): () => void {
  el.scrollIntoView({ block, behavior: "smooth" });
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
