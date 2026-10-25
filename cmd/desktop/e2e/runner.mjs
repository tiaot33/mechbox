import { setTimeout as delay } from "node:timers/promises";

export function plan(steps, args, network) {
  let filter = "";
  let group;
  let list = false;
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === "--list") list = true;
    else if (arg === "--group" && args[i + 1]) group = args[++i];
    else if (arg.startsWith("--")) throw new Error(`未知参数：${arg}`);
    else if (filter) throw new Error("只能给一个步骤名过滤条件");
    else filter = arg;
  }
  const enabled = steps.filter((s) => !(s.name.startsWith("[网络]") && !network) && !(s.name.startsWith("[离线]") && network));
  const groups = [...new Set(steps.map((s) => s.group))];
  if (group && !groups.includes(group)) throw new Error(`没有场景组「${group}」，可选：${groups.join("、")}`);
  if (list) return { list: true, steps: enabled };
  const selected = enabled.filter((s) => (!group || s.group === group) && s.name.includes(filter));
  if (!selected.length) throw new Error("没有匹配的可运行步骤；用 pnpm test --list 查看步骤和场景组");
  const dependencies = new Map();
  for (const step of enabled) {
    const before = enabled.filter((s) => s.index < step.index);
    const previous = before.findLast((s) => s.group === step.group);
    const needed = step.needs.map((name) => {
      const last = enabled.findLast((s) => s.group === name);
      if (!last || last.index >= step.index) throw new Error(`「${step.group}」的前置场景组无效：${name}`);
      return last;
    });
    dependencies.set(step, [...needed, ...(previous ? [previous] : [])]);
  }
  const included = new Set();
  function include(step) {
    if (included.has(step)) return;
    for (const needed of dependencies.get(step)) include(needed);
    included.add(step);
  }
  for (const step of selected) include(step);
  return { list: false, steps: enabled.filter((s) => included.has(s)), dependencies, selected: new Set(selected) };
}

export async function until(check, message, timeout = 15000) {
  const end = performance.now() + timeout;
  while (!(await check())) {
    if (performance.now() >= end) throw new Error(`等待超时：${message}`);
    await delay(20);
  }
}

export async function deadline(fn, ms, message) {
  let timer;
  try {
    return await Promise.race([
      Promise.resolve().then(fn),
      new Promise((_, reject) => {
        timer = setTimeout(() => {
          const err = new Error(message);
          err.name = "DeadlineError";
          reject(err);
        }, ms);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}
