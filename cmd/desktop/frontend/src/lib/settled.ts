// 状态圈的结束结果：一次写操作或检查更新结束时记下，状态圈据此画一下对勾或叉。

// Settled 是一次结束的结果;id 每次结束都换，用来只画一次对勾或叉;at 是结束的时刻(Date.now())。
export interface Settled {
  id: number;
  failed: boolean;
  at: number;
}

// SETTLED_HOLD 是结束后对勾或叉停留的毫秒数：外圈先合拢，再画出对勾或叉，停一下再收起。
export const SETTLED_HOLD = 1300;

// settledFresh 是 now 时这次结果还该不该画：只在结束后的 SETTLED_HOLD 之内画。
export function settledFresh(settled: Settled, now: number): boolean {
  return now - settled.at < SETTLED_HOLD;
}
