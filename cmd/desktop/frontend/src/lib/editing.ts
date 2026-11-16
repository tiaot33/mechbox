// 同一时间只有一处在编辑(抽屉里的备注)。每次开始编辑是新的一次(startEditing),结束时只放掉自己那一次(endEditing):
// 旧的异步保存晚于切换、或晚于重新开始编辑才返回时，不会关掉后来开始的编辑，哪怕编辑的是同一个技能。

// Editing 是一次编辑;按对象本身区分，先后两次编辑是两个不同的 Editing。
export interface Editing {
  readonly n: number;
}

let next = 0;

export function startEditing(): Editing {
  return { n: ++next };
}

// endEditing 结束 session 这一次编辑：现在开着的是它才关上，否则保持现在开着的那一次。
export function endEditing(current: Editing | null, session: Editing): Editing | null {
  return current === session ? null : current;
}
