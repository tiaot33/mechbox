import assert from "node:assert/strict";
import { test } from "node:test";
import { plan, until, deadline } from "./runner.mjs";

const steps = [
  { index: 0, group: "fetch", name: "入库", needs: [] },
  { index: 1, group: "fetch", name: "再次预览", needs: [] },
  { index: 2, group: "settings", name: "设置", needs: ["fetch"] },
  { index: 3, group: "settings", name: "保存设置", needs: ["fetch"] },
  { index: 4, group: "refs", name: "分支列表", needs: [] },
  { index: 5, group: "network", name: "[网络] GitHub", needs: [] },
];

test("按组运行补齐前置组，保留顺序并排除无关组", () => {
  const result = plan(steps, ["--group", "settings"], false);
  assert.deepEqual(result.steps.map((s) => s.name), ["入库", "再次预览", "设置", "保存设置"]);
  assert.deepEqual([...result.selected].map((s) => s.name), ["设置", "保存设置"]);
  assert.deepEqual(result.dependencies.get(steps[3]), [steps[1], steps[2]]);
});

test("过滤单步补齐同组的前置步骤，无关组不执行", () => {
  assert.deepEqual(plan(steps, ["保存设置"], false).steps, steps.slice(0, 4));
  assert.deepEqual(plan(steps, ["分支列表"], false).steps, [steps[4]]);
});

test("零匹配、未知组、只匹配被禁用的网络步骤都报错", () => {
  for (const args of [["不存在"], ["--group", "missing"], ["GitHub"], ["--unknown"]]) {
    assert.throws(() => plan(steps, args, false));
  }
  assert.deepEqual(plan(steps, ["GitHub"], true).steps, [steps[5]]);
});

test("列表不需要运行环境，前置组写在后面时报错", () => {
  assert.equal(plan(steps, ["--list"], false).steps.length, 5);
  assert.throws(() => plan([{ index: 0, group: "a", name: "a", needs: ["b"] }], [], false));
});

test("没有请求到达时，轮询和整个步骤都在时限内失败", async () => {
  await assert.rejects(until(() => false, "请求到达", 5), /等待超时：请求到达/);
  await assert.rejects(deadline(() => new Promise(() => {}), 5, "步骤超时"), { name: "DeadlineError" });
  assert.equal(await deadline(() => 42, 100, "步骤超时"), 42);
});
