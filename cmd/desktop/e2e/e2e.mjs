// 桌面端端到端验证：server 模式 + 本机 Chrome,按界面操作走遍技能库的全部功能，并核对磁盘。
// 由 run.sh 准备环境后调用;单独运行时需给出下面几个环境变量。
import assert from "node:assert";
import { chromium } from "playwright-core";
import fs from "node:fs";
import path from "node:path";

const HOME = process.env.E2E_HOME;
const URL = process.env.E2E_URL;
const ZIP_URL = process.env.E2E_ZIP_URL;
const SHOTS = process.env.E2E_SHOTS ?? "/tmp/skills-e2e-shots";
const NETWORK = process.env.E2E_NETWORK === "1";
if (!HOME || !URL || !ZIP_URL) throw new Error("需要 E2E_HOME、E2E_URL、E2E_ZIP_URL,请用 run.sh 运行");
fs.mkdirSync(SHOTS, { recursive: true });
const SK = `${HOME}/code/my-skills`;
const only = process.argv[2]; // 可选：只跑名字包含该串的步骤
const browser = await chromium.launch({ channel: "chrome", headless: true });
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
const problems = [];
page.on("pageerror", (e) => problems.push("pageerror: " + e.message));
page.on("console", (m) => m.type() === "error" && problems.push("console: " + m.text()));
page.on("response", async (r) => {
  if (r.status() < 400) return;
  const body = await r.text().catch(() => "");
  problems.push(`http ${r.status()} ${r.url()} ${body.slice(0, 160)}`);
});
await page.goto(URL);
await page.waitForSelector(".a-list");

const results = [];
let n = 0;
async function step(name, fn) {
  if (only && !name.includes(only)) return;
  if (name.startsWith("[网络]") && !NETWORK) {
    console.log("SKIP", name, "(E2E_NETWORK=1 时运行)");
    return;
  }
  n++;
  try {
    await fn();
    results.push(["PASS", name]);
    console.log("PASS", name);
  } catch (e) {
    results.push(["FAIL", name, e.message.split("\n")[0]]);
    console.log("FAIL", name, "—", e.message.split("\n").slice(0, 3).join(" | "));
    await page.screenshot({ path: path.join(SHOTS, `fail-${String(n).padStart(2, "0")}.png`) });
    // 关掉可能残留的弹窗 / 抽屉，免得影响后续步骤
    for (let i = 0; i < 3; i++) await page.keyboard.press("Escape");
    if (await page.locator(".scrim").count()) await page.locator(".scrim").click({ force: true });
  }
}
const modal = () => page.locator(".modal").last();
const card = (name) => page.locator(".a-card", { has: page.locator(".a-card-name", { hasText: new RegExp(`^${name}$`) }) });
const drawer = () => page.locator(".drawer");
async function toast(text, tone) {
  await page.waitForFunction(
    ([t, tone]) => {
      const el = document.querySelector(".toast.show");
      return el && el.textContent.includes(t) && (!tone || el.classList.contains(tone));
    },
    [text, tone],
    { timeout: 15000 },
  ).catch(async () => {
    const cur = await page.locator(".toast").textContent();
    throw new Error(`toast "${text}" 未出现，当前:"${cur}"`);
  });
}
async function settle() {
  await page.waitForTimeout(400);
  await page.waitForFunction(() => !document.querySelector(".tb-crumb .spin"), null, { timeout: 30000 });
}
const btn = (scope, name) => scope.getByRole("button", { name, exact: true });
async function openFetch() {
  await page.locator(".side-btn", { hasText: "入库技能" }).click();
}
// previewLocal 对本地目录做入库预览，等到候选列表出现。
async function previewLocal(dir) {
  await openFetch();
  await modal().locator("input.input").first().fill(dir);
  await btn(modal(), "入库预览").click();
  await modal().locator("h3", { hasText: "入库预览" }).waitFor({ timeout: 20000 });
}
async function fetchLocal(dir) {
  await previewLocal(dir);
  await btn(modal(), "入库所选").click();
}
async function openDrawer(name) {
  if (await drawer().count()) await page.locator(".scrim").click({ force: true });
  await card(name).click();
  await drawer().waitFor();
  await page.waitForTimeout(300);
}
async function closeDrawer() {
  if (await drawer().count()) await drawer().locator(".drawer-head .icon-btn").click();
}
async function selectScope(agentName, place = "全局") {
  await page.locator(".scope-pill").click();
  const menu = page.locator(".scope-menu");
  const placeBlock = menu.locator("div", { has: page.locator(".sm-place", { hasText: place }) }).last();
  const item = placeBlock.locator(".sm-item", { hasText: agentName });
  if (await item.count()) await item.click();
  else await placeBlock.locator("select.sm-more").selectOption({ label: agentName });
  await settle();
}
async function clearScope() {
  if (await page.locator(".scope-pill-clear").count()) await page.locator(".scope-pill-clear").click();
}
const scopeDir = async () => {
  const t = (await page.locator(".a-scope-sum .a-sum-row .mono").first().textContent()).trim();
  return t.replace(/^~/, HOME);
};

// ============ 入库 ============
await step("本地目录入库 alpha/beta/gamma(入库预览 → 候选 → 入库)", async () => {
  await previewLocal(SK);
  for (const n of ["alpha", "beta", "gamma"]) {
    assert((await modal().locator(".list-row", { hasText: n }).count()) === 1, `候选里应有 ${n}`);
  }
  await btn(modal(), "入库所选").click();
  await toast("已入库 3 个技能");
  await settle();
  assert((await page.locator(".a-card").count()) === 3, "应有 3 张卡片");
  assert((await page.locator(".a-left .nav-item", { hasText: "code/my-skills" }).count()) === 1, "同一目录下的技能应归入一个集合");
});

await step("再次预览同一目录：候选标已入库、不能再选", async () => {
  await previewLocal(SK);
  assert((await modal().locator(".list-row .badge", { hasText: "已入库" }).count()) === 3, "三个候选都应标已入库");
  assert(await btn(modal(), "入库所选").isDisabled(), "没有可入库的候选时不能提交");
  await page.keyboard.press("Escape");
});

await step("预览不存在的目录报错", async () => {
  await openFetch();
  await modal().locator("input.input").first().fill(`${SK}/nope`);
  await btn(modal(), "入库预览").click();
  await page.waitForSelector(".toast.show.error");
  await modal().getByRole("button", { name: "取消" }).click();
});

await step("本地路径：相对路径被拦下，~ 展开为主目录", async () => {
  await openFetch();
  await modal().locator("input.input").first().fill("code/my-skills");
  assert((await modal().locator(".field-error").count()) === 1, "相对路径应提示");
  assert(await btn(modal(), "入库预览").isDisabled(), "相对路径时不能提交");
  await modal().locator("input.input").first().fill("~/code/my-skills");
  assert((await modal().locator(".field-error").count()) === 0, "~ 路径不应提示");
  await btn(modal(), "入库预览").click();
  await modal().locator("h3", { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  // 候选都已入库：说明 ~ 被展开成了同一个目录
  assert((await modal().locator(".list-row .badge", { hasText: "已入库" }).count()) === 3, "~ 应展开为同一目录");
  await page.keyboard.press("Escape");
});

await step("URL 压缩包入库(入库预览 → 候选 → 入库)", async () => {
  await openFetch();
  await btn(modal(), "URL 压缩包").click();
  await modal().locator("input.input").first().fill(ZIP_URL);
  await btn(modal(), "入库预览").click();
  await modal().locator("h3", { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  assert((await modal().locator(".list-row", { hasText: "delta" }).count()) === 1, "候选里应有 delta");
  await btn(modal(), "入库所选").click();
  await toast("已入库 1 个技能");
  await settle();
  assert((await card("delta").count()) === 1, "应出现 delta 卡片");
});

// ============ 详情抽屉：文件与元数据 ============
await step("详情：文件列表、可执行位、SKILL.md 渲染与源码", async () => {
  await openDrawer("alpha");
  const d = drawer();
  await d.locator(".md h3", { hasText: "alpha" }).waitFor();
  assert((await d.locator(".file-row", { hasText: "scripts/run.sh" }).locator(".badge", { hasText: "可执行" }).count()) === 1, "run.sh 应标可执行");
  await d.locator(".file-row", { hasText: "scripts/run.sh" }).click();
  await d.locator("pre.code", { hasText: "echo hi" }).waitFor();
  await d.locator(".file-row", { hasText: "SKILL.md" }).click();
  await btn(d, "查看源码").click();
  await d.locator("pre.code", { hasText: "name: alpha" }).waitFor();
  assert((await d.locator(".meta", { hasText: "从未检查" }).count()) === 1, "更新检查应为从未检查");
  assert((await d.locator(".versions li").count()) === 1, "应有 1 个版本");
});

// ============ 分组 ============
await step("新建组(左栏 +)", async () => {
  await closeDrawer();
  await page.locator(".a-left .nav-sec", { hasText: "组" }).locator(".icon-btn").click();
  await modal().locator("input.input").fill("前端调试");
  await btn(modal(), "创建").click();
  await toast("已创建组「前端调试」");
  await settle();
  assert((await page.locator(".a-left .nav-item", { hasText: "前端调试" }).count()) === 1, "左栏应出现组");
});

await step("同名新建组自动加 #2", async () => {
  await page.locator(".a-left .nav-sec", { hasText: "组" }).locator(".icon-btn").click();
  await modal().locator("input.input").fill("前端调试");
  await btn(modal(), "创建").click();
  await toast("前端调试#2");
  await settle();
});

await step("详情：加入分组 / 新建组并加入 / 移出", async () => {
  await openDrawer("alpha");
  await drawer().locator("select.chip-add").selectOption({ label: "前端调试" });
  await toast("已把「alpha」加入「前端调试」");
  await settle();
  assert((await drawer().locator(".chip", { hasText: "前端调试" }).count()) >= 1, "应显示组 chip");
  await drawer().locator("select.chip-add").selectOption({ label: "新建组…" });
  await modal().locator("input.input").fill("文档处理");
  await btn(modal(), "创建").click();
  await settle();
  assert((await drawer().locator(".chip", { hasText: "文档处理" }).count()) === 1, "新建组并加入");
  await drawer().locator(".chip", { hasText: "文档处理" }).locator("button").click();
  await toast("移出「文档处理」");
  await settle();
  assert((await drawer().locator(".chip", { hasText: "文档处理" }).count()) === 0, "应已移出");
});

await step("重命名组与删除组(左栏悬停按钮)", async () => {
  await closeDrawer();
  const row = page.locator(".a-left .nav-item", { hasText: "前端调试#2" });
  await row.hover();
  await row.locator(".row-actions button[title='重命名']").click();
  await modal().locator("input.input").fill("临时组");
  await btn(modal(), "重命名").click();
  await toast("已重命名为「临时组」");
  await settle();
  const row2 = page.locator(".a-left .nav-item", { hasText: "临时组" });
  await row2.hover();
  await row2.locator(".row-actions button[title='删除组']").click();
  await btn(modal(), "删除组").click();
  await toast("已删除组");
  await settle();
  assert((await page.locator(".a-left .nav-item", { hasText: "临时组" }).count()) === 0, "组应已删除");
});

// ============ 项目 ============
await step("登记项目 proj-a / proj-b", async () => {
  for (const p of ["proj-a", "proj-b"]) {
    await page.locator(".a-right .nav-sec", { hasText: "已登记项目" }).locator(".icon-btn").click();
    await modal().locator("input.input").fill(`${HOME}/code/${p}`);
    await btn(modal(), "登记").click();
    await toast("已登记项目");
    await settle();
  }
  assert((await page.locator(".a-right .nav-item.proj").count()) === 2, "应有 2 个项目");
});

await step("重复登记同一项目报错", async () => {
  await page.locator(".a-right .nav-sec", { hasText: "已登记项目" }).locator(".icon-btn").click();
  await modal().locator("input.input").fill(`${HOME}/code/proj-a`);
  await btn(modal(), "登记").click();
  await toast("已经存在", "error");
  await modal().getByRole("button", { name: "取消" }).click();
});

// ============ 安装 ============
let claudeGlobal;
await step("详情：安装到 Claude Code 全局，并落盘", async () => {
  await openDrawer("alpha");
  const d = drawer();
  await d.locator(".install-to select").nth(0).selectOption("0");
  await d.locator(".install-to select").nth(1).selectOption("claude-code");
  await btn(d, "安装").click();
  await toast("已安装「alpha」");
  await settle();
  assert((await d.locator(".scope-table tbody tr", { hasText: "Claude Code · 全局" }).count()) === 1, "安装表应有该行");
  claudeGlobal = (await d.locator(".scope-table tbody tr", { hasText: "Claude Code · 全局" }).locator(".mono").textContent()).replace(/^~/, HOME);
  assert(fs.existsSync(path.join(claudeGlobal, "alpha", "SKILL.md")), `磁盘上应有 ${claudeGlobal}/alpha`);
  assert(fs.statSync(path.join(claudeGlobal, "alpha", "scripts", "run.sh")).mode & 0o100, "run.sh 应保持可执行");
});

await step("选择作用域(胶囊弹层 → 其他 Agent 下拉)后卡片开关安装 beta", async () => {
  await closeDrawer();
  await selectScope("Codex");
  assert((await page.locator(".scope-pill", { hasText: "Codex" }).count()) === 1, "胶囊应显示 Codex");
  await card("beta").locator(".switch").click();
  await toast("已安装「beta」到 Codex · 全局");
  await settle();
  assert((await card("beta").locator(".switch.on").count()) === 1, "开关应打开");
  const dir = await scopeDir();
  assert(fs.existsSync(path.join(dir, "beta", "SKILL.md")), `磁盘上应有 ${dir}/beta`);
});

await step("仅已安装筛选", async () => {
  await page.locator(".toggle-label .switch").click();
  await page.waitForTimeout(200);
  assert((await page.locator(".a-card").count()) === 1, "仅已安装时应只剩 beta");
  await page.locator(".toggle-label .switch").click();
});

await step("组安装(摘要里的组开关)→ 随组安装 → 卸载组安装", async () => {
  await page.locator(".gchip", { hasText: "前端调试" }).click();
  await toast("已把「前端调试」组安装到");
  await settle();
  assert((await card("alpha").locator(".a-card-foot", { hasText: "随「前端调试」安装" }).count()) === 1, "alpha 应随组安装");
  assert((await page.locator(".gchip.on", { hasText: "前端调试" }).count()) === 1, "组开关应为 on");
  await page.locator(".gchip", { hasText: "前端调试" }).click();
  await toast("已卸载「前端调试」");
  await settle();
  assert((await card("alpha").locator(".a-card-foot", { hasText: "未安装" }).count()) === 1, "卸载后 alpha 应未安装");
});

await step("卡片开关卸载直接安装", async () => {
  await card("beta").locator(".switch").click();
  await toast("已卸载「beta」");
  await settle();
  const dir = await scopeDir();
  assert(!fs.existsSync(path.join(dir, "beta")), "撤掉最后一个安装后文件夹应移除");
});

await step("安装到项目作用域(右栏项目展开 → 其他 Agent → 选中)", async () => {
  await clearScope();
  await page.locator(".a-right .nav-item.proj", { hasText: "proj-a" }).click();
  await page.locator(".a-right .nav-item.more").nth(1).click();
  await page.locator(".a-right .nav-item.sub", { hasText: "Claude Code" }).nth(1).click();
  await settle();
  assert((await page.locator(".scope-pill", { hasText: "proj-a" }).count()) === 1, "胶囊应显示 proj-a");
  assert((await page.locator(".toggle-label .switch.on").count()) === 1, "右栏点选应打开仅已安装筛选");
  assert((await page.locator(".a-card").count()) === 0, "该作用域还没有安装，列表应为空");
  await page.locator(".toggle-label .switch").click();
  await card("gamma").locator(".switch").click();
  await toast("已安装「gamma」");
  await settle();
  const dir = await scopeDir();
  // macOS 上 /tmp 是 /private/tmp 的软链接，登记时路径被规范化。
  assert(fs.realpathSync(dir).startsWith(fs.realpathSync(`${HOME}/code/proj-a`)), `项目目录应在 proj-a 下:${dir}`);
  assert(fs.existsSync(path.join(dir, "gamma", "SKILL.md")), "项目里应有 gamma");
});

await step("未记录的外来文件夹：安装计划发现后询问覆盖", async () => {
  const dir = await scopeDir();
  fs.mkdirSync(path.join(dir, "beta"), { recursive: true });
  fs.writeFileSync(path.join(dir, "beta", "note.txt"), "hand made");
  await card("beta").locator(".switch").click();
  await modal().locator("h3", { hasText: "目标已有同名文件夹" }).waitFor();
  await btn(modal(), "覆盖并安装").click();
  await toast("已安装「beta」");
  await settle();
  assert(fs.existsSync(path.join(dir, "beta", "SKILL.md")) && !fs.existsSync(path.join(dir, "beta", "note.txt")), "应已覆盖");
});

// ============ 重新安装 ============
await step("刷新发现被手动改过的文件夹 → 全部重新安装 → 询问覆盖", async () => {
  const dir = await scopeDir();
  fs.writeFileSync(path.join(dir, "gamma", "SKILL.md"), "edited by hand\n");
  await page.locator(".titlebar button[title^='刷新']").click();
  await settle();
  await page.locator(".a-scope-sum .stat.warn", { hasText: "1" }).waitFor();
  assert((await card("gamma").locator(".status", { hasText: "被手动改过" }).count()) === 1, "gamma 应标被手动改过");
  await btn(page.locator(".a-scope-sum"), "全部重新安装").click();
  await modal().locator("h3", { hasText: "有技能文件夹被手动改过" }).waitFor();
  await btn(modal(), "覆盖这些改动").click();
  await toast("已重新安装");
  await settle();
  assert(fs.readFileSync(path.join(dir, "gamma", "SKILL.md"), "utf8").includes("name: gamma"), "文件应恢复");
});

await step("删除已安装的文件夹 → 文件夹不在了 → 详情里单条重新安装", async () => {
  const dir = await scopeDir();
  fs.rmSync(path.join(dir, "gamma"), { recursive: true });
  await page.locator(".titlebar button[title^='刷新']").click();
  await settle();
  await openDrawer("gamma");
  const row = drawer().locator(".scope-table tbody tr", { hasText: "proj-a" });
  assert((await row.locator(".status", { hasText: "文件夹不在了" }).count()) === 1, "应显示文件夹不在了");
  await btn(row, "重新安装").click();
  await toast("已重新安装 1 处");
  await settle();
  assert(fs.existsSync(path.join(dir, "gamma", "SKILL.md")), "应已重新创建");
  await closeDrawer();
});

// ============ 更新 ============
await step("检查更新 → 有可用更新 → 全部更新", async () => {
  await clearScope();
  fs.writeFileSync(`${SK}/gamma/SKILL.md`, '---\nname: gamma\ndescription: "gamma v2"\n---\n\n# gamma v2\n');
  await page.locator(".a-left .nav-item", { hasText: "检查更新" }).click();
  await toast("1 个技能有可用更新");
  await settle();
  await page.locator(".a-left .nav-item", { hasText: "有可用更新" }).click();
  assert((await card("gamma").locator(".badge.warn", { hasText: "可更新" }).count()) === 1, "gamma 应标可更新");
  await btn(page.locator(".a-scope-sum"), "全部更新").click();
  await toast("已更新 1 个技能");
  await settle();
  await page.locator(".a-left .nav-item", { hasText: "所有技能" }).click();
});

await step("版本历史：两个版本、与当前比较、查看旧版文件", async () => {
  await openDrawer("gamma");
  const d = drawer();
  assert((await d.locator(".versions li").count()) === 2, "应有 2 个版本");
  await btn(d, "与当前比较").click();
  await d.locator(".diff-changed", { hasText: "SKILL.md" }).waitFor();
  await btn(d.locator(".versions li").nth(1), "查看文件").click();
  await d.locator("pre.code", { hasText: "does gamma things" }).waitFor();
  assert((await d.locator(".meta", { hasText: "已是最新" }).count()) === 1, "更新后检查状态应为已是最新");
});

await step("详情：重新读取来源 → 询问重新安装 → 重新安装", async () => {
  fs.writeFileSync(`${SK}/gamma/SKILL.md`, '---\nname: gamma\ndescription: "gamma v3"\n---\n\n# gamma v3\n');
  await btn(drawer(), "重新读取来源").click();
  await modal().locator("h3", { hasText: "重新安装到新版本" }).waitFor();
  await btn(modal(), "重新安装").click();
  await toast("已重新安装");
  await settle();
  const projDir = `${HOME}/code/proj-a`;
  const found = fs.readdirSync(projDir, { recursive: true }).find((f) => String(f).endsWith("gamma/SKILL.md"));
  assert(found && fs.readFileSync(path.join(projDir, String(found)), "utf8").includes("gamma v3"), "项目里的 gamma 应为 v3");
  await closeDrawer();
});

// ============ 拖放 ============
await step("拖放：卡片 → 左栏组(加入)", async () => {
  await card("delta").dragTo(page.locator(".a-left .nav-item", { hasText: "前端调试" }));
  await toast("已把「delta」加入「前端调试」");
  await settle();
});

await step("拖放：卡片 → 右栏作用域(安装)", async () => {
  await card("delta").dragTo(page.locator(".a-right .nav-item.sub", { hasText: "Claude Code" }).first());
  await toast("已安装「delta」到 Claude Code · 全局");
  await settle();
  assert(fs.existsSync(path.join(claudeGlobal, "delta", "SKILL.md")), "磁盘上应有 delta");
});

await step("拖放：组 → 右栏作用域(组安装)", async () => {
  await page.locator(".a-left .nav-item", { hasText: "前端调试" }).dragTo(page.locator(".a-right .nav-item.sub", { hasText: "Claude Code" }).first());
  await toast("已把「前端调试」组安装到");
  await settle();
});

await step("组标题条：组安装于 chip、卸载", async () => {
  await page.locator(".a-left .nav-item", { hasText: "前端调试" }).click();
  const head = page.locator(".a-group-head");
  assert((await head.locator(".chip", { hasText: "全局" }).count()) === 1, "应显示组安装于全局");
  await head.locator(".chip", { hasText: "全局" }).locator("button").click();
  await toast("已卸载「前端调试」");
  await settle();
  await page.locator(".a-left .nav-item", { hasText: "所有技能" }).click();
});

await step("移出分组(有组安装时询问去留)", async () => {
  await selectScope("Claude Code");
  await page.locator(".gchip", { hasText: "前端调试" }).click(); // 重新组安装
  await settle();
  await openDrawer("alpha");
  await drawer().locator(".chip", { hasText: "前端调试" }).locator("button").click();
  await modal().locator(".choice", { hasText: "一并卸载" }).click();
  await btn(modal(), "移出").click();
  await toast("移出「前端调试」");
  await settle();
  await closeDrawer();
  await clearScope();
});

// ============ 其他视图 ============
await step("搜索与 ⌘K、列表视图、侧栏开关", async () => {
  await page.keyboard.press("Meta+k");
  await page.keyboard.type("gam");
  await page.waitForTimeout(200);
  assert((await page.locator(".a-card").count()) === 1, "搜索 gam 应只剩 1 张");
  await page.keyboard.press("Escape");
  await page.locator(".icon-seg button[title='列表']").click();
  assert((await page.locator(".a-list.is-list").count()) === 1, "应切到列表");
  await page.locator(".icon-seg button[title='卡片']").click();
  await page.locator(".titlebar button[title='显示/隐藏侧栏']").click();
  assert((await page.locator(".a-left").count()) === 0, "左栏应隐藏");
  await page.locator(".titlebar button[title='显示/隐藏侧栏']").click();
});

await step("集合筛选", async () => {
  await page.locator(".a-left .nav-item", { hasText: "delta.zip" }).click();
  assert((await page.locator(".a-card").count()) === 1, "zip 集合应只有 delta");
  await page.locator(".a-left .nav-item", { hasText: "所有技能" }).click();
});

// ============ 同步与设置 ============
await step("左栏分节折叠与展开", async () => {
  const toggle = page.locator(".a-left .sec-toggle", { hasText: "集合" });
  await toggle.click();
  assert((await page.locator(".a-left .nav-item", { hasText: "delta.zip" }).isVisible()) === false, "折叠后集合条目应隐藏");
  await toggle.click();
  assert(await page.locator(".a-left .nav-item", { hasText: "delta.zip" }).isVisible(), "展开后集合条目应可见");
});

await step("同步：未配置时同步计划报错", async () => {
  await page.locator(".a-left .nav-item", { hasText: "同步" }).click();
  assert((await modal().locator(".notice.warn", { hasText: "SKILLS_S3_BUCKET" }).count()) === 1, "应提示未配置");
  await btn(modal(), "推送到 S3…").click();
  await toast("缺少所需配置", "error");
  await btn(modal(), "从 S3 拉取…").click();
  await toast("缺少所需配置", "error");
  await page.keyboard.press("Escape");
});

await step("设置：库路径、Agent、配置状态", async () => {
  await page.locator(".a-left .nav-item", { hasText: "设置" }).click();
  assert((await modal().locator(".kv", { hasText: `${HOME}/Library/Application Support/skills-manager/library.db` }).count()) === 1, "应显示库路径");
  assert((await modal().locator(".scope-cell", { hasText: "Codex" }).count()) === 1, "应列出 Codex");
  await page.keyboard.press("Escape");
});

// ============ 冲突与失败路径 ============
await step("占用冲突：同名技能已在该作用域，安装被拦下并说明占用者", async () => {
  const dir = `${HOME}/code/other/gamma`;
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(`${dir}/SKILL.md`, '---\nname: gamma\ndescription: "another gamma"\n---\n');
  await fetchLocal(`${HOME}/code/other`);
  await toast("已入库 1 个技能");
  await settle();
  await selectScope("Claude Code");
  // 原 gamma 先占住 Claude Code 全局目录里的 gamma 文件夹
  const cards = page.locator(".a-card", { has: page.locator(".a-card-name", { hasText: /^gamma$/ }) });
  await cards.nth(0).locator(".switch").click();
  await toast("已安装「gamma」");
  await settle();
  await cards.nth(1).locator(".switch").click();
  await toast("已被「gamma」占用", "error");
  await clearScope();
});

await step("组安装遇到外来文件夹：选择跳过，结果列出被跳过的成员", async () => {
  await selectScope("Codex");
  const dir = await scopeDir();
  fs.mkdirSync(path.join(dir, "delta"), { recursive: true });
  fs.writeFileSync(path.join(dir, "delta", "x.txt"), "hand made");
  await page.locator(".gchip", { hasText: "前端调试" }).click();
  await modal().locator(".notice.warn", { hasText: "delta" }).waitFor();
  await modal().locator(".choice", { hasText: "跳过这些成员" }).click();
  await btn(modal(), "安装").click();
  await modal().locator("h3", { hasText: "组安装「前端调试」" }).waitFor();
  assert((await modal().locator(".list-row", { hasText: "delta" }).locator(".status", { hasText: "已跳过" }).count()) === 1, "delta 应被跳过");
  await btn(modal(), "知道了").click();
  assert(fs.existsSync(path.join(dir, "delta", "x.txt")), "外来文件夹应保持原样");
  await page.locator(".gchip", { hasText: "前端调试" }).click(); // 卸载组安装，恢复
  await settle();
  await clearScope();
});

await step("检查更新失败：来源目录里的技能不见了，列出失败原因", async () => {
  // 移出来源目录：留在目录里改个名，仍会按 SKILL.md 里的名字被重新找到。
  fs.renameSync(`${SK}/beta`, `${HOME}/beta.bak`);
  try {
    await page.locator(".a-left .nav-item", { hasText: "检查更新" }).click();
    await modal().locator("h3", { hasText: "检查更新" }).waitFor();
    assert((await modal().locator(".list-row", { hasText: "beta" }).locator(".status", { hasText: "检查失败" }).count()) === 1, "beta 应检查失败");
    await btn(modal(), "知道了").click();
    await settle();
  } finally {
    fs.renameSync(`${HOME}/beta.bak`, `${SK}/beta`);
  }
});

// ============ 移除 ============
await step("移除技能(保留已安装的文件夹)", async () => {
  await openDrawer("delta");
  await btn(drawer(), "移除技能").click();
  await modal().locator(".choice", { hasText: "保留已安装的文件夹" }).click();
  await btn(modal(), "移除").click();
  await toast("已移除");
  await settle();
  assert((await card("delta").count()) === 0, "卡片应消失");
  assert((await drawer().count()) === 0, "抽屉应关闭");
  assert(fs.existsSync(path.join(claudeGlobal, "delta")), "保留时磁盘上的 delta 应还在");
});

await step("移除技能(同时删除已安装的文件夹)", async () => {
  await openDrawer("alpha");
  await btn(drawer(), "移除技能").click();
  await btn(modal(), "移除").click();
  await toast("已移除");
  await settle();
  assert(!fs.existsSync(path.join(claudeGlobal, "alpha")), "磁盘上的 alpha 应被删除");
});

await step("注销项目(删除项目里已安装的文件夹)", async () => {
  const row = page.locator(".a-right .nav-item.proj", { hasText: "proj-a" });
  await row.hover();
  await row.locator(".row-actions button").click();
  await btn(modal(), "注销").click();
  await toast("已注销项目");
  await settle();
  assert((await page.locator(".a-right .nav-item.proj", { hasText: "proj-a" }).count()) === 0, "项目应消失");
  const left = fs.readdirSync(`${HOME}/code/proj-a`, { recursive: true }).filter((f) => String(f).endsWith("SKILL.md"));
  assert(left.length === 0, `项目里已安装的文件夹应被删除，剩余:${left}`);
});

// ============ 市场与 GitHub(需要网络) ============
await step("[网络] 技能市场搜索 → 入库", async () => {
  await page.locator(".side-btn", { hasText: "技能市场" }).click();
  await modal().locator("input").fill("frontend design");
  await btn(modal(), "搜索").click();
  await modal().locator(".list-row").first().waitFor({ timeout: 30000 });
  await modal().locator(".list-row").first().getByRole("button", { name: "入库", exact: true }).click();
  await modal().locator("h3", { hasText: "入库预览" }).waitFor({ timeout: 90000 });
  await btn(modal(), "入库所选").click();
  await toast("已入库", undefined);
  await settle();
});

await step("[网络] GitHub 入库(owner/repo@技能名)", async () => {
  await openFetch();
  await btn(modal(), "GitHub").click();
  await modal().locator("input.input").first().fill("anthropics/skills@pdf");
  await btn(modal(), "入库预览").click();
  await modal().locator("h3", { hasText: "入库预览" }).waitFor({ timeout: 90000 });
  assert((await modal().locator(".list-row", { hasText: "pdf" }).count()) >= 1, "候选里应有 pdf");
  await btn(modal(), "入库所选").click();
  await toast("已入库");
  await settle();
  assert((await page.locator(".a-left .nav-item", { hasText: "anthropics/skills" }).count()) === 1, "集合名应为 owner/repo");
  await openDrawer("pdf");
  assert((await drawer().locator(".src-chip", { hasText: "anthropics/skills" }).count()) === 1, "详情里集合应为 owner/repo");
  assert((await drawer().locator("button.link", { hasText: "在 GitHub 查看" }).count()) === 1, "GitHub 来源应有查看链接");
  await btn(drawer(), "检查更新").click();
  await toast("都是最新");
  await closeDrawer();
});

await page.screenshot({ path: path.join(SHOTS, "final.png") });
await browser.close();
const failed = results.filter((r) => r[0] === "FAIL");
console.log(`\n${results.length - failed.length}/${results.length} 通过`);
for (const f of failed) console.log("  FAIL", f[1], "—", f[2]);
// 失败请求里 422 是绑定方法返回的错误(测试里有意触发的报错步骤),列出来便于核对。
console.log("页面错误 / 失败请求:");
for (const p of problems.filter((p) => !p.startsWith("console: Failed to load resource"))) console.log("  ", p);
process.exitCode = failed.length || problems.some((p) => p.startsWith("pageerror")) ? 1 : 0;
