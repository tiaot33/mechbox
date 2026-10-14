// 桌面端端到端验证：server 模式 + 本机 Chrome,按界面操作走遍技能库的全部功能，并核对磁盘。
// 由 run.sh 准备环境后调用;单独运行时需给出下面几个环境变量。
import assert from "node:assert";
import { chromium } from "playwright-core";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const HOME = process.env.E2E_HOME;
const URL = process.env.E2E_URL;
const ZIP_URL = process.env.E2E_ZIP_URL;
const ZIP_DIR = process.env.E2E_ZIP_DIR;
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
    if (await drawer().count()) await drawer().locator('[data-slot="sheet-close"]').click();
  }
}
const modal = () => page.locator('[data-slot="dialog-content"]').last();
const card = (name) => page.locator(".a-card", { has: page.locator(".a-card-name", { hasText: new RegExp(`^${name}$`) }) });
const drawer = () => page.locator('[data-slot="sheet-content"]');
async function toast(text, tone) {
  await page.waitForFunction(
    ([t, tone]) => {
      const el = document.querySelector('[data-slot="toast"][data-state="open"]');
      return el && el.textContent.includes(t) && (!tone || el.dataset.tone === tone);
    },
    [text, tone],
    { timeout: 15000 },
  ).catch(async () => {
    const cur = await page.locator('[data-slot="toast"]').textContent();
    throw new Error(`toast "${text}" 未出现，当前:"${cur}"`);
  });
}
async function settle() {
  await page.waitForTimeout(400);
  await page.waitForFunction(() => !document.querySelector('.tb-crumb [data-slot="spinner"]'), null, { timeout: 30000 });
}
const btn = (scope, name) => scope.getByRole("button", { name, exact: true });
async function openFetch() {
  await page.locator(".side-btn", { hasText: "入库技能" }).click();
}
// previewLocal 对本地目录做入库预览，等到候选列表出现。
async function previewLocal(dir) {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(dir);
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
}
async function fetchLocal(dir) {
  await previewLocal(dir);
  await btn(modal(), "入库所选").click();
}
async function openDrawer(name) {
  // 抽屉没有遮罩，开着时点卡片会切换;先关掉，免得点到正打开的那张反而关上。
  await closeDrawer();
  await card(name).click();
  await drawer().waitFor();
  await page.waitForTimeout(300);
}
async function closeDrawer() {
  if (await drawer().count()) await drawer().locator('[data-slot="sheet-close"]').click();
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
  const t = (await page.locator(".a-scope-sum .a-sum-row .font-mono").first().textContent()).trim();
  return t.replace(/^~/, HOME);
};

// ============ 入库 ============
await step("本地目录入库 alpha/beta/gamma(入库预览 → 候选 → 入库)", async () => {
  await previewLocal(SK);
  for (const n of ["alpha", "beta", "gamma"]) {
    assert((await modal().locator('[data-slot="list-row"]', { hasText: n }).count()) === 1, `候选里应有 ${n}`);
  }
  await btn(modal(), "入库所选").click();
  await toast("已入库 3 个技能");
  await settle();
  assert((await page.locator(".a-card").count()) === 3, "应有 3 张卡片");
  assert((await page.locator(".a-left .nav-item", { hasText: "code/my-skills" }).count()) === 1, "同一目录下的技能应归入一个集合");
});

await step("再次预览同一目录：候选标已入库、不能再选", async () => {
  await previewLocal(SK);
  assert((await modal().locator('[data-slot="list-row"] [data-slot="badge"]', { hasText: "已入库" }).count()) === 3, "三个候选都应标已入库");
  assert(await btn(modal(), "入库所选").isDisabled(), "没有可入库的候选时不能提交");
  await page.keyboard.press("Escape");
});

await step("预览不存在的目录报错", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(`${SK}/nope`);
  await btn(modal(), "入库预览").click();
  await page.waitForSelector('[data-slot="toast"][data-state="open"][data-tone="error"]');
  await modal().getByRole("button", { name: "取消" }).click();
});

await step("本地路径：相对路径被拦下，~ 展开为主目录", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill("code/my-skills");
  assert((await modal().locator('[data-slot="field-error"]').count()) === 1, "相对路径应提示");
  assert(await btn(modal(), "入库预览").isDisabled(), "相对路径时不能提交");
  await modal().locator('[data-slot="input"]').first().fill("~/code/my-skills");
  assert((await modal().locator('[data-slot="field-error"]').count()) === 0, "~ 路径不应提示");
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  // 候选都已入库：说明 ~ 被展开成了同一个目录
  assert((await modal().locator('[data-slot="list-row"] [data-slot="badge"]', { hasText: "已入库" }).count()) === 3, "~ 应展开为同一目录");
  await page.keyboard.press("Escape");
});

await step("URL 压缩包入库(入库预览 → 候选 → 入库)", async () => {
  await openFetch();
  await btn(modal(), "URL 压缩包").click();
  await modal().locator('[data-slot="input"]').first().fill(ZIP_URL);
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  assert((await modal().locator('[data-slot="list-row"]', { hasText: "delta" }).count()) === 1, "候选里应有 delta");
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
  assert((await d.locator(".file-row", { hasText: "scripts/run.sh" }).locator('[data-slot="badge"]', { hasText: "可执行" }).count()) === 1, "run.sh 应标可执行");
  await d.locator(".file-row", { hasText: "scripts/run.sh" }).click();
  await d.locator('[data-slot="code"]', { hasText: "echo hi" }).waitFor();
  await d.locator(".file-row", { hasText: "SKILL.md" }).click();
  await btn(d, "查看源码").click();
  await d.locator('[data-slot="code"]', { hasText: "name: alpha" }).waitFor();
  assert((await d.locator(".meta dt", { hasText: "检查更新" }).count()) === 0, "本地目录来源不检查更新");
  assert((await btn(d, "重新读取").count()) === 1, "本地目录来源只有重新读取");
  assert((await d.locator(".versions li").count()) === 1, "应有 1 个版本");
});

// ============ 分组 ============
await step("新建组(左栏 +)", async () => {
  await closeDrawer();
  await page.locator(".a-left .nav-sec", { hasText: "组" }).locator("button[title='新建组']").click();
  await modal().locator('[data-slot="input"]').fill("前端调试");
  await btn(modal(), "创建").click();
  await toast("已创建组「前端调试」");
  await settle();
  assert((await page.locator(".a-left .nav-item", { hasText: "前端调试" }).count()) === 1, "左栏应出现组");
});

await step("同名新建组自动加 #2", async () => {
  await page.locator(".a-left .nav-sec", { hasText: "组" }).locator("button[title='新建组']").click();
  await modal().locator('[data-slot="input"]').fill("前端调试");
  await btn(modal(), "创建").click();
  await toast("前端调试#2");
  await settle();
});

await step("详情：加入分组 / 新建组并加入 / 移出", async () => {
  await openDrawer("alpha");
  await drawer().locator("select.chip-add").selectOption({ label: "前端调试" });
  await toast("已把「alpha」加入「前端调试」");
  await settle();
  assert((await drawer().locator('[data-slot="chip"]', { hasText: "前端调试" }).count()) >= 1, "应显示组 chip");
  await drawer().locator("select.chip-add").selectOption({ label: "新建组…" });
  await modal().locator('[data-slot="input"]').fill("文档处理");
  await btn(modal(), "创建").click();
  await settle();
  assert((await drawer().locator('[data-slot="chip"]', { hasText: "文档处理" }).count()) === 1, "新建组并加入");
  await drawer().locator('[data-slot="chip"]', { hasText: "文档处理" }).locator("button").click();
  await toast("移出「文档处理」");
  await settle();
  assert((await drawer().locator('[data-slot="chip"]', { hasText: "文档处理" }).count()) === 0, "应已移出");
});

await step("重命名组与删除组(左栏悬停按钮)", async () => {
  await closeDrawer();
  const row = page.locator(".a-left .nav-item", { hasText: "前端调试#2" });
  await row.hover();
  await row.locator(".row-actions button[title='重命名']").click();
  await modal().locator('[data-slot="input"]').fill("临时组");
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
    await page.locator(".a-right .nav-sec", { hasText: "已登记项目" }).locator("button[title='登记项目']").click();
    await modal().locator('[data-slot="input"]').fill(`${HOME}/code/${p}`);
    await btn(modal(), "登记").click();
    await toast("已登记项目");
    await settle();
  }
  assert((await page.locator(".a-right .nav-item.proj").count()) === 2, "应有 2 个项目");
});

await step("重复登记同一项目报错", async () => {
  await page.locator(".a-right .nav-sec", { hasText: "已登记项目" }).locator("button[title='登记项目']").click();
  await modal().locator('[data-slot="input"]').fill(`${HOME}/code/proj-a`);
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
  // 安装表里显示的是技能文件夹，它的上一级是 Agent 目录。
  const folder = (await d.locator(".scope-table tbody tr", { hasText: "Claude Code · 全局" }).locator(".font-mono").textContent()).replace(/^~/, HOME);
  claudeGlobal = path.dirname(folder);
  assert(fs.existsSync(path.join(claudeGlobal, "alpha", "SKILL.md")), `磁盘上应有 ${claudeGlobal}/alpha`);
  assert(fs.statSync(path.join(claudeGlobal, "alpha", "scripts", "run.sh")).mode & 0o100, "run.sh 应保持可执行");
});

await step("选择作用域(胶囊弹层 → 其他 Agent 下拉)后卡片开关安装 beta", async () => {
  await closeDrawer();
  await selectScope("Codex");
  assert((await page.locator(".scope-pill", { hasText: "Codex" }).count()) === 1, "胶囊应显示 Codex");
  await card("beta").locator('[role="switch"]').click();
  await toast("已安装「beta」到 Codex · 全局");
  await settle();
  assert((await card("beta").locator('[role="switch"][aria-checked="true"]').count()) === 1, "开关应打开");
  const dir = await scopeDir();
  assert(fs.existsSync(path.join(dir, "beta", "SKILL.md")), `磁盘上应有 ${dir}/beta`);
});

await step("仅已安装筛选", async () => {
  await page.locator('.toggle-label [role="switch"]').click();
  await page.waitForTimeout(200);
  assert((await page.locator(".a-card").count()) === 1, "仅已安装时应只剩 beta");
  await page.locator('.toggle-label [role="switch"]').click();
});

await step("组安装(摘要里的组开关)→ 随组安装 → 卸载组安装", async () => {
  await page.locator(".gchip", { hasText: "前端调试" }).click();
  await toast("已把「前端调试」组安装到");
  await settle();
  assert((await card("alpha").locator(".a-card-foot", { hasText: "随「前端调试」安装" }).count()) === 1, "alpha 应随组安装");
  assert((await page.locator('.gchip[data-state="on"]', { hasText: "前端调试" }).count()) === 1, "组开关应为 on");
  await page.locator(".gchip", { hasText: "前端调试" }).click();
  await toast("已卸载「前端调试」");
  await settle();
  assert((await card("alpha").locator(".a-card-foot", { hasText: "未安装" }).count()) === 1, "卸载后 alpha 应未安装");
});

await step("卡片开关卸载直接安装", async () => {
  await card("beta").locator('[role="switch"]').click();
  await toast("已卸载「beta」");
  await settle();
  const dir = await scopeDir();
  assert(!fs.existsSync(path.join(dir, "beta")), "撤掉最后一个安装后文件夹应移除");
});

await step("安装到项目作用域(右栏项目展开 → 检测到的 Agent 直接列出 → 选中)", async () => {
  await clearScope();
  await page.locator(".a-right .nav-item.proj", { hasText: "proj-a" }).click();
  assert((await page.locator(".a-right .nav-item.more").count()) === 0, "检测到的 Agent 应直接列出，不折进其他");
  await page.locator(".a-right .nav-item.sub", { hasText: "Claude Code" }).nth(1).click();
  await settle();
  assert((await page.locator(".scope-pill", { hasText: "proj-a" }).count()) === 1, "胶囊应显示 proj-a");
  assert((await page.locator('.toggle-label [role="switch"][aria-checked="true"]').count()) === 1, "右栏点选应打开仅已安装筛选");
  assert((await page.locator(".a-card").count()) === 0, "该作用域还没有安装，列表应为空");
  await page.locator('.toggle-label [role="switch"]').click();
  await card("gamma").locator('[role="switch"]').click();
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
  await card("beta").locator('[role="switch"]').click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "目标已有同名文件夹" }).waitFor();
  await btn(modal(), "覆盖并安装").click();
  await toast("已安装「beta」");
  await settle();
  assert(fs.existsSync(path.join(dir, "beta", "SKILL.md")) && !fs.existsSync(path.join(dir, "beta", "note.txt")), "应已覆盖");
});

// ============ 重新安装 ============
await step("刷新发现被手动改过的文件夹 → 全部重新安装 → 逐处勾选覆盖", async () => {
  const dir = await scopeDir();
  fs.writeFileSync(path.join(dir, "gamma", "SKILL.md"), "edited by hand\n");
  await page.locator(".titlebar button[title^='刷新']").click();
  await settle();
  await page.locator('.a-scope-sum .stat[data-tone="warn"]', { hasText: "1" }).waitFor();
  assert((await card("gamma").locator('[data-slot="badge"]', { hasText: "被手动改过" }).count()) === 1, "gamma 应标被手动改过");
  await btn(page.locator(".a-scope-sum"), "全部重新安装").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "有技能文件夹被改过" }).waitFor();
  await modal().locator('[data-slot="list-row"]', { hasText: "gamma" }).locator("input[type=checkbox]").check();
  await btn(modal(), "重新安装并覆盖 1 处").click();
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
  assert((await row.locator('[data-slot="badge"]', { hasText: "文件夹不在了" }).count()) === 1, "应显示文件夹不在了");
  await btn(row, "重新安装").click();
  await toast("已重新安装 1 处");
  await settle();
  assert(fs.existsSync(path.join(dir, "gamma", "SKILL.md")), "应已重新创建");
  await closeDrawer();
});

// ============ 更新 ============
// URL 来源的更新：改写 zip 里的 delta(带一个新脚本),检查更新 → 更新列表(风险提示)→ 按默认勾选更新，装出去的地方一起换成新版;
// 之后在历史里回滚、从更新计划里把回滚掉的改动找回来。
const updatesNav = () => page.locator(".a-left .nav-item").filter({ has: page.locator("span", { hasText: /^更新$/ }) });
function rewriteZip(description) {
  fs.writeFileSync(`${ZIP_DIR}/delta/SKILL.md`, `---\nname: delta\ndescription: "${description}"\n---\n\n# ${description}\n`);
  fs.mkdirSync(`${ZIP_DIR}/delta/scripts`, { recursive: true });
  fs.writeFileSync(`${ZIP_DIR}/delta/scripts/setup.sh`, "echo setup\n");
  fs.rmSync(`${ZIP_DIR}/delta.zip`);
  execFileSync("zip", ["-qr", "delta.zip", "delta"], { cwd: ZIP_DIR });
}
const rezip = (name, dirs) => {
  fs.rmSync(`${ZIP_DIR}/${name}.zip`);
  execFileSync("zip", ["-qr", `../${name}.zip`, ...dirs], { cwd: `${ZIP_DIR}/${name}` });
};
const projSkill = (name) => {
  const projDir = `${HOME}/code/proj-a`;
  const found = fs.readdirSync(projDir, { recursive: true }).find((f) => String(f).endsWith(`${name}/SKILL.md`));
  return found ? fs.readFileSync(path.join(projDir, String(found)), "utf8") : "";
};
const checkUpdates = async (text) => {
  await page.locator(".a-left .nav-item", { hasText: "检查更新" }).click();
  await toast(text);
  await settle();
};
const planRow = (cls, name) => modal().locator(`.${cls}`, { has: page.locator("b", { hasText: new RegExp(`^${name}$`) }) });

await step("检查更新 → 更新列表(风险提示)→ 更新(连同装出去的地方)", async () => {
  await clearScope();
  await openDrawer("delta");
  await drawer().locator(".install-to select").nth(0).selectOption({ label: "proj-a" });
  await drawer().locator(".install-to select").nth(1).selectOption("claude-code");
  await btn(drawer(), "安装").click();
  await toast("已安装「delta」");
  await settle();
  await closeDrawer();
  rewriteZip("delta v2");
  await checkUpdates("1 个技能有新版本");
  assert((await updatesNav().locator('[data-slot="count"]').textContent()) === "1", "侧边栏计数应为 1");
  await updatesNav().click();
  assert((await card("delta").locator('[data-slot="badge"][data-tone="warn"]', { hasText: "可更新" }).count()) === 1, "delta 应标可更新");
  const row = page.locator(".update-row", { hasText: "delta" });
  assert((await row.getByText("来自 delta.zip", { exact: false }).count()) === 1, "只有一个技能的集合直接显示技能名");
  await row.getByText("⚠ 新增 1 个脚本").waitFor({ timeout: 15000 });
  await btn(row, "更新").click();
  await toast("已更新 delta · 1 个技能换了新版本 · 换到了 1 个地方");
  assert((await page.locator('[data-slot="toast"]').getByRole("button", { name: "回滚" }).count()) === 1, "完成提示应带回滚");
  await settle();
  assert(projSkill("delta").includes("delta v2"), "项目里的 delta 应换成 v2");
  assert((await updatesNav().count()) === 0, "没有更新后左栏不再显示更新");
});

await step("版本历史：两个版本、与当前比较、查看旧版文件", async () => {
  await openDrawer("delta");
  const d = drawer();
  assert((await d.locator(".versions li").count()) === 2, "应有 2 个版本");
  await btn(d, "与当前比较").click();
  await d.locator('[data-diff="changed"]', { hasText: "SKILL.md" }).waitFor();
  await btn(d.locator(".versions li").nth(1), "查看文件").click();
  await d.locator('[data-slot="code"]', { hasText: "delta skill from a zip" }).waitFor();
  assert((await d.locator(".meta", { hasText: "已是最新" }).count()) === 1, "更新后检查状态应为已是最新");
  await closeDrawer();
});

await step("历史与回滚：库和装过的地方回到旧版，回滚掉的改动不计入角标，之后能在更新计划里找回来", async () => {
  await openDrawer("delta");
  await btn(drawer(), "更新历史").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "「delta」的更新历史" }).waitFor();
  const record = modal().locator(".update-record", { has: page.locator('[data-slot="badge"]', { hasText: "更新" }) });
  await btn(record.first(), "改了什么").click();
  await record.first().locator('[data-diff="added"]', { hasText: "scripts/setup.sh" }).waitFor();
  await record.first().getByText("从 ", { exact: false }).waitFor();
  await record.first().locator(".update-places summary").click();
  await record.first().locator(".update-places", { hasText: "Claude Code · proj-a" }).locator('[data-slot="badge"]', { hasText: "换上了" }).waitFor();
  await btn(record.first(), "回滚").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "回滚这次更新" }).waitFor();
  await btn(modal(), "回滚").click();
  await toast("装回了 1 个地方");
  await settle();
  assert(projSkill("delta").includes("delta skill from a zip"), "项目里的 delta 应回到 v1");
  await page.locator(".update-record", { has: page.locator('[data-slot="badge"]', { hasText: "回滚" }) }).first().waitFor();
  assert((await page.locator(".update-record").getByText("已回滚", { exact: true }).count()) === 1, "那次更新应标已回滚");
  await page.keyboard.press("Escape");
  assert((await updatesNav().count()) === 0, "回滚掉的改动不计入角标");
  // 从技能发起时就是要更新这一个：跳过的改动照样标出来，但默认勾上。
  await btn(drawer(), "更新").click();
  const delta = planRow("update-skill", "delta");
  await delta.locator('[data-slot="badge"]', { hasText: "已跳过" }).waitFor({ timeout: 15000 });
  assert(await delta.locator("input[type=checkbox]").isChecked(), "从技能发起时默认勾上这一个");
  await btn(modal(), "更新 1 个").click();
  await toast("已更新 delta");
  await settle();
  assert(projSkill("delta").includes("delta v2"), "找回来再更新后项目里的 delta 又是 v2");
  await closeDrawer();
});

await step("详情：本地目录来源重新读取，一个操作完成，改过的地方不动", async () => {
  await openDrawer("gamma");
  assert((await btn(drawer(), "检查更新").count()) === 0, "单个技能不提供检查更新");
  await drawer().locator(".install-to select").nth(0).selectOption("0");
  await drawer().locator(".install-to select").nth(1).selectOption("codex");
  await btn(drawer(), "安装").click();
  await toast("已安装「gamma」");
  await settle();
  const codexGlobal = (await drawer().locator(".scope-table tbody tr", { hasText: "Codex · 全局" }).locator(".font-mono").textContent()).replace(/^~/, HOME);
  const edited = projSkill("gamma").replace("does gamma things", "edited by hand");
  const projGamma = fs.readdirSync(`${HOME}/code/proj-a`, { recursive: true }).find((f) => String(f).endsWith("gamma/SKILL.md"));
  fs.writeFileSync(path.join(`${HOME}/code/proj-a`, String(projGamma)), edited);
  fs.writeFileSync(`${SK}/gamma/SKILL.md`, '---\nname: gamma\ndescription: "gamma v3"\n---\n\n# gamma v3\n');
  await btn(drawer(), "重新读取").click();
  await toast("已重新读取「gamma」 · 1 个技能换了新版本 · 换到了 1 个地方 · 1 个地方你改过，没动");
  await settle();
  assert(fs.readFileSync(path.join(codexGlobal, "SKILL.md"), "utf8").includes("gamma v3"), "Codex 全局的 gamma 应为 v3");
  assert(projSkill("gamma").includes("edited by hand"), "改过的地方保持原样");
  await closeDrawer();
});

await step("从技能发起更新：只勾这一个、其余折叠;之后更新集合重新对齐", async () => {
  const pairURL = ZIP_URL.replace(/delta\.zip$/, "pair.zip");
  await openFetch();
  await btn(modal(), "URL 压缩包").click();
  await modal().locator('[data-slot="input"]').first().fill(pairURL);
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  await btn(modal(), "入库所选").click();
  await toast("已入库 2 个技能");
  await settle();
  for (const name of ["eta", "theta"]) {
    fs.writeFileSync(`${ZIP_DIR}/pair/${name}/SKILL.md`, `---\nname: ${name}\ndescription: "${name} v2"\n---\n\n# ${name} v2\n`);
  }
  rezip("pair", ["eta", "theta"]);
  await checkUpdates("2 个技能有新版本");

  await openDrawer("eta");
  await btn(drawer(), "更新").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "更新 pair.zip" }).waitFor();
  assert(await planRow("update-skill", "eta").locator("input[type=checkbox]").isChecked(), "eta 应勾上");
  await modal().locator('[data-slot="list-row"]', { hasText: "同一来源还有 1 个技能有变化(theta)" }).waitFor();
  await modal().locator('[data-slot="notice"][data-tone="warn"]', { hasText: "不在同一个来源版本上" }).waitFor();
  await btn(modal(), "更新 1 个").click();
  await toast("已更新 pair.zip · 1 个技能换了新版本");
  await settle();
  const relation = drawer().locator(".meta dd").filter({ hasText: "有变化" });
  assert((await relation.filter({ hasText: " 在 " }).count()) === 1, "eta 与 theta 应不在同一个来源版本上，且 theta 仍有变化");
  await closeDrawer();

  await updatesNav().click();
  const row = page.locator(".update-row", { hasText: "pair.zip" });
  assert((await row.getByText("theta 有变化", { exact: false }).count()) === 1, "更新列表应列出 theta");
  await btn(row, "查看").click();
  await modal().locator('[data-slot="list-row"]', { hasText: "没变化:eta" }).waitFor({ timeout: 15000 });
  await btn(modal(), "更新 1 个").click();
  await toast("已更新 pair.zip · 1 个技能换了新版本");
  await settle();
  await openDrawer("eta");
  await drawer().locator(".meta dd", { hasText: "和其他 1 个技能在同一个来源版本上" }).waitFor();
  await closeDrawer();
});

await step("更新计划：风险与差异、确认改名、继续保留已删除的、新技能不感兴趣、跳过", async () => {
  const trioURL = ZIP_URL.replace(/delta\.zip$/, "trio.zip");
  await openFetch();
  await btn(modal(), "URL 压缩包").click();
  await modal().locator('[data-slot="input"]').first().fill(trioURL);
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  await btn(modal(), "入库所选").click();
  await toast("已入库 3 个技能");
  await settle();
  await openDrawer("lambda");
  await drawer().locator(".install-to select").nth(0).selectOption("0");
  await drawer().locator(".install-to select").nth(1).selectOption("claude-code");
  await btn(drawer(), "安装").click();
  await toast("已安装「lambda」");
  await settle();
  const claudeSkills = path.dirname((await drawer().locator(".scope-table tbody tr", { hasText: "Claude Code · 全局" }).locator(".font-mono").textContent()).replace(/^~/, HOME));
  await closeDrawer();

  // kappa 改了并新增一个脚本;lambda 改名为 lambda-kit(内容不变);mu 删掉;新增 nu。
  const trio = `${ZIP_DIR}/trio`;
  fs.appendFileSync(`${trio}/kappa/SKILL.md`, "\nNow with a script.\n");
  fs.mkdirSync(`${trio}/kappa/scripts`);
  fs.writeFileSync(`${trio}/kappa/scripts/x.sh`, "echo kappa\n");
  fs.mkdirSync(`${trio}/lambda-kit`);
  fs.writeFileSync(`${trio}/lambda-kit/SKILL.md`, fs.readFileSync(`${trio}/lambda/SKILL.md`, "utf8").replace("name: lambda", "name: lambda-kit"));
  fs.rmSync(`${trio}/lambda`, { recursive: true });
  fs.rmSync(`${trio}/mu`, { recursive: true });
  fs.mkdirSync(`${trio}/nu`);
  fs.writeFileSync(`${trio}/nu/SKILL.md`, '---\nname: nu\ndescription: "nu v1"\n---\n\n# nu\n');
  rezip("trio", ["kappa", "lambda-kit", "nu"]);
  await checkUpdates("1 个技能可能改了名");
  assert((await updatesNav().locator('[data-slot="count"]').textContent()) === "3", "侧边栏计数应为 3(有新版本、可能改名、来源里已删除)");

  await updatesNav().click();
  const row = page.locator(".update-row", { hasText: "trio.zip" });
  await row.getByText("lambda 可能改名为 lambda-kit", { exact: false }).waitFor();
  await row.getByText("⚠ 新增 1 个脚本").waitFor({ timeout: 15000 });
  await btn(row, "查看").click();
  await modal().locator(".update-risk", { hasText: "scripts/x.sh" }).click();
  await modal().locator('.update-diff [data-slot="file-diff"] [data-diff="add"]', { hasText: "echo kappa" }).waitFor();
  await btn(planRow("update-skill", "kappa"), "收起差异").click();
  await btn(planRow("update-skill", "kappa"), "看差异").click();
  await modal().locator(".update-diff button", { hasText: "SKILL.md" }).click();
  await modal().locator('.update-diff [data-slot="file-diff"] [data-diff="add"]', { hasText: "Now with a script." }).waitFor();

  await btn(planRow("update-rename", "lambda"), "是").click();
  await planRow("update-rename", "lambda").locator('[data-slot="badge"]', { hasText: "是同一个" }).waitFor();
  await modal().locator(".update-place", { hasText: "Claude Code · 全局" }).getByText("lambda → lambda-kit").waitFor();
  await btn(planRow("update-deleted", "mu"), "继续保留").click();
  await toast("已继续保留「mu」");
  await planRow("update-deleted", "mu").locator('[data-slot="badge"]', { hasText: "已继续保留" }).waitFor();
  await modal().locator(".update-new", { hasText: "nu" }).waitFor();
  await btn(modal().locator(".update-new"), "不感兴趣").click();
  await toast("不再提示这几个新技能");
  assert((await modal().locator(".update-new").count()) === 0, "点了不感兴趣后不再提新技能");
  await btn(planRow("update-skill", "kappa"), "跳过").click();
  await toast("已跳过「kappa」这次的改动");
  assert(!(await planRow("update-skill", "kappa").locator("input[type=checkbox]").isChecked()), "跳过后不再勾选");
  await btn(modal(), "更新 1 个").click();
  await toast("已更新 trio.zip · 1 个技能换了新版本 · 换到了 1 个地方");
  await settle();

  assert(fs.existsSync(path.join(claudeSkills, "lambda-kit", "SKILL.md")), "确认改名后装出去的文件夹换成新名字");
  assert(!fs.existsSync(path.join(claudeSkills, "lambda")), "旧文件夹清掉");
  assert((await card("lambda-kit").count()) === 1 && (await card("lambda").count()) === 0, "技能改用新名字");
  assert((await updatesNav().count()) === 0, "跳过的、继续保留的不计入角标");
  assert((await card("mu").getByText("来源已删除").count()) === 1, "继续保留的技能留一个淡色标记");
  await page.locator(".a-left .nav-item", { hasText: "trio.zip" }).click();
  assert((await page.locator(".collection-new").count()) === 0, "不感兴趣的新技能不再在集合上提");
  await btn(page.locator(".a-scope-sum"), "看来源里的其他技能").click();
  await modal().locator('[data-slot="list-row"]', { hasText: "nu" }).waitFor({ timeout: 20000 });
  await page.keyboard.press("Escape");
  await page.locator(".a-left .nav-item", { hasText: "所有技能" }).click();
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

await step("拖放：按指针轨迹预判，朝哪个折叠的项目移就提前展开哪个，拖完收回", async () => {
  const proj = page.locator(".a-right .nav-item.proj", { hasText: "proj-b" });
  assert((await proj.getAttribute("aria-expanded")) === "false", "proj-b 应是折叠的");
  const rail = await page.locator(".a-right").boundingBox();
  const stopX = rail.x - 60;
  // 以约 1.5px/ms 的速度横向移到右栏左边 60px 处停下，指针始终没进右栏。
  const sweep = async (y) => {
    await page.mouse.move(stopX - 360, y);
    await page.waitForTimeout(150);
    for (let x = stopX - 360; x <= stopX; x += 24) {
      await page.mouse.move(x, y);
      await page.waitForTimeout(16);
    }
    await page.waitForTimeout(150);
  };
  await card("delta").hover();
  await page.mouse.down();
  await page.mouse.move(stopX - 360, rail.y + rail.height / 2, { steps: 5 });

  const global = await page.locator(".a-right .nav-item.sub", { hasText: "Claude Code" }).first().boundingBox();
  await sweep(global.y + global.height / 2);
  const notAimed = await proj.getAttribute("aria-expanded");

  const row = await proj.boundingBox();
  await sweep(row.y + row.height / 2);
  const aimed = await proj.getAttribute("aria-expanded");

  await page.mouse.move(stopX, 5, { steps: 5 });
  await page.mouse.up();
  assert(notAimed === "false", "朝全局那一行移动时 proj-b 不应展开");
  assert(aimed === "true", "朝 proj-b 移动、还没进右栏时 proj-b 就应展开");
  await page.locator('.a-right .nav-item.proj[aria-expanded="false"]', { hasText: "proj-b" }).waitFor({ timeout: 2000 });
});

await step("拖放：组 → 右栏作用域(组安装)", async () => {
  await page.locator(".a-left .nav-item", { hasText: "前端调试" }).dragTo(page.locator(".a-right .nav-item.sub", { hasText: "Claude Code" }).first());
  await toast("已把「前端调试」组安装到");
  await settle();
});

await step("组标题条：组安装于 chip、卸载", async () => {
  await page.locator(".a-left .nav-item", { hasText: "前端调试" }).click();
  const head = page.locator(".a-group-head");
  assert((await head.locator('[data-slot="chip"]', { hasText: "全局" }).count()) === 1, "应显示组安装于全局");
  await head.locator('[data-slot="chip"]', { hasText: "全局" }).locator("button").click();
  await toast("已卸载「前端调试」");
  await settle();
  await page.locator(".a-left .nav-item", { hasText: "所有技能" }).click();
});

await step("移出分组(有组安装时询问去留)", async () => {
  await selectScope("Claude Code");
  await page.locator(".gchip", { hasText: "前端调试" }).click(); // 重新组安装
  await settle();
  await openDrawer("alpha");
  await drawer().locator('[data-slot="chip"]', { hasText: "前端调试" }).locator("button").click();
  await modal().locator('[data-slot="choice"]', { hasText: "一并卸载" }).click();
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
  assert((await page.locator('.a-list[data-view="list"]').count()) === 1, "应切到列表");
  await page.locator(".icon-seg button[title='卡片']").click();
  await page.locator(".titlebar button[title='显示/隐藏侧栏']").click();
  assert((await page.locator(".a-left").count()) === 0, "左栏应隐藏");
  await page.locator(".titlebar button[title='显示/隐藏侧栏']").click();
});

await step("集合筛选 → 看来源里的其他技能", async () => {
  assert((await page.locator(".a-left .nav-item", { hasText: "delta.zip" }).count()) === 0, "只有一个技能的集合不列出");
  await page.locator(".a-left .nav-item", { hasText: "my-skills" }).click();
  assert((await page.locator(".a-card").count()) === 3, "my-skills 集合应有 3 个技能");
  await btn(page.locator(".a-scope-sum"), "看来源里的其他技能").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  assert((await modal().locator('[data-slot="list-row"] [data-slot="badge"]', { hasText: "已入库" }).count()) === 3, "已入库的候选应标出来");
  await page.keyboard.press("Escape");
  await page.locator(".a-left .nav-item", { hasText: "所有技能" }).click();
});

// ============ 同步与设置 ============
await step("左栏分节折叠与展开", async () => {
  const toggle = page.locator(".a-left .sec-toggle", { hasText: "集合" });
  await toggle.click();
  assert((await page.locator(".a-left .nav-item", { hasText: "my-skills" }).isVisible()) === false, "折叠后集合条目应隐藏");
  await toggle.click();
  assert(await page.locator(".a-left .nav-item", { hasText: "my-skills" }).isVisible(), "展开后集合条目应可见");
});

await step("同步：未配置时同步计划报错", async () => {
  await page.locator(".a-left .nav-item", { hasText: "同步" }).click();
  assert((await modal().locator('[data-slot="notice"][data-tone="warn"]', { hasText: "SKILLS_S3_BUCKET" }).count()) === 1, "应提示未配置");
  await btn(modal(), "推送到 S3…").click();
  await toast("缺少所需配置", "error");
  await btn(modal(), "从 S3 拉回…").click();
  await toast("缺少所需配置", "error");
  await page.keyboard.press("Escape");
});

await step("设置页：分区、应用设置写进这台机器的设置文件、项目、关于", async () => {
  const settingsFile = `${HOME}/Library/Application Support/skills-manager/settings.json`;
  const section = (name) => page.locator('.settings [data-slot="card"]', { has: page.locator('[data-slot="card-title"]', { hasText: new RegExp(`^${name}$`) }) });
  const sw = (label) => page.locator(".settings .set-row", { hasText: label }).locator('[role="switch"]');
  const interval = (label) => btn(section("检查更新"), label);
  const open = async () => {
    await page.locator(".a-left .nav-item", { hasText: "设置" }).click();
    await page.locator('.settings [role="switch"]').first().waitFor();
  };
  await open();
  for (const name of ["GitHub", "同步", "检查更新", "通用", "Agent", "项目", "关于"]) {
    assert((await section(name).count()) === 1, `应有分区「${name}」`);
  }
  assert((await section("关于").locator(".kv", { hasText: `${HOME}/Library/Application Support/skills-manager/library.db` }).count()) === 1, "关于应显示数据库位置");
  assert(/^\d+\.\d+\.\d+/.test((await section("关于").locator("dd").first().textContent()).trim()), "关于应显示应用版本");
  assert((await section("Agent").locator(".set-row", { hasText: "Codex" }).count()) === 1, "应列出 Codex");
  assert((await section("项目").locator(".set-row", { hasText: "proj-a" }).count()) === 1, "项目分区应列出 proj-a");

  // 默认：自动检查打开、每天;留在菜单栏、Dock 角标打开。改了立即保存到这台机器的设置文件，重新载入后仍是改过的值。
  for (const label of ["自动检查更新", "关闭窗口后留在菜单栏", "Dock 角标"]) {
    assert((await sw(label).getAttribute("aria-checked")) === "true", `「${label}」默认应打开`);
  }
  assert((await interval("每天").getAttribute("aria-pressed")) === "true", "间隔默认应为每天");
  // 连着改两项、不等第一项保存完：后一次保存不能把前一次改的值盖回去。本机保存太快，这里把保存请求拖慢，让两次一定重叠
  const slowSave = async (route) => {
    if (route.request().postData()?.includes("check_interval")) await new Promise((r) => setTimeout(r, 600));
    await route.continue();
  };
  await page.route("**/wails/runtime", slowSave);
  await sw("Dock 角标").click();
  await interval("每周").click();
  assert((await sw("Dock 角标").getAttribute("aria-checked")) === "false", "保存中界面也应显示已关掉的 Dock 角标");
  await page.waitForTimeout(1600);
  await page.unroute("**/wails/runtime", slowSave);
  await settle();
  const saved = JSON.parse(fs.readFileSync(settingsFile, "utf8"));
  assert.deepStrictEqual(saved, { auto_check: true, check_interval: "weekly", stay_in_menu_bar: true, dock_badge: false });
  await page.reload();
  await page.waitForSelector(".a-list");
  await open();
  assert((await sw("Dock 角标").getAttribute("aria-checked")) === "false", "重新载入后 Dock 角标应仍关闭");
  assert((await interval("每周").getAttribute("aria-pressed")) === "true", "重新载入后间隔应仍为每周");
  await sw("自动检查更新").click();
  await settle();
  assert(await interval("每周").isDisabled(), "关闭自动检查后间隔不可选");
  await sw("自动检查更新").click();
  await settle();
  await sw("Dock 角标").click();
  await settle();
  await interval("每天").click();
  await settle();

  // 项目分区：登记、改位置、注销走主界面同一套弹窗
  fs.mkdirSync(`${HOME}/code/proj-c`, { recursive: true });
  fs.mkdirSync(`${HOME}/code/proj-d`, { recursive: true });
  await btn(section("项目"), "登记项目").click();
  await modal().locator('[data-slot="input"]').fill(`${HOME}/code/proj-c`);
  await btn(modal(), "登记").click();
  await toast("已登记项目");
  await settle();
  assert((await page.locator(".settings .set-row", { hasText: "proj-c" }).count()) === 1, "登记后应列出 proj-c");
  await btn(page.locator(".settings .set-row", { hasText: "proj-c" }), "改位置").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "改项目「proj-c」的位置" }).waitFor();
  await modal().locator('[data-slot="input"]').fill(`${HOME}/code/proj-d`);
  await btn(modal(), "改位置").click();
  // 项目名取目录名，改位置后跟着变
  await toast("已把项目「proj-d」指到");
  await settle();
  assert((await page.locator(".settings .set-row", { hasText: "/code/proj-d" }).count()) === 1, "改位置后应显示新位置");
  await btn(page.locator(".settings .set-row", { hasText: "proj-d" }), "注销").click();
  await btn(modal(), "注销").click();
  await toast("已注销项目");
  await settle();
  assert((await page.locator(".settings .set-row", { hasText: "proj-d" }).count()) === 0, "注销后不再列出");
  await page.locator(".titlebar button[title='返回技能库']").click();
  await page.waitForSelector(".a-list");
});

await step("设置页:GitHub 令牌与同步设置存在这台机器上，全部留空时回到环境变量", async () => {
  const settingsFile = `${HOME}/Library/Application Support/skills-manager/settings.json`;
  const endpoint = ZIP_URL.replace(/\/delta\.zip$/, "");
  const section = (name) => page.locator('.settings [data-slot="card"]', { has: page.locator('[data-slot="card-title"]', { hasText: new RegExp(`^${name}$`) }) });
  const field = (name, label) => section(name).locator('[data-slot="field"]', { hasText: label }).locator("input");
  const sidebarSync = () => page.locator(".a-left .nav-item", { hasText: "同步" });
  // 回到主界面后侧栏重读配置状态，等它变成预期的文字
  const sidebarSays = async (text, message) => {
    await sidebarSync().filter({ hasText: text }).waitFor({ timeout: 5000 }).catch(() => assert.fail(message));
  };
  const open = async () => {
    await page.locator(".a-left .nav-item", { hasText: "设置" }).click();
    await field("GitHub", "个人访问令牌").waitFor();
  };
  await sidebarSays("未配置", "没填同步设置、也没有环境变量时侧栏应显示未配置");
  await open();
  assert(await btn(section("GitHub"), "保存").isDisabled(), "没改时不能保存");
  assert((await field("GitHub", "个人访问令牌").getAttribute("type")) === "password", "令牌输入框应遮住内容");
  await field("GitHub", "个人访问令牌").fill("ghp_e2e_token");
  await btn(section("GitHub"), "保存").click();
  await toast("已保存 GitHub 令牌");
  await settle();

  // 整组取用：只填存储桶时缺密钥，仍是未配置
  await field("同步", "存储桶").fill("e2e-bucket");
  await btn(section("同步"), "保存").click();
  await toast("已保存同步设置");
  await settle();
  await page.locator(".titlebar button[title='返回技能库']").click();
  await page.waitForSelector(".a-list");
  await sidebarSays("未配置", "只填了存储桶时应仍显示未配置");

  await open();
  await field("同步", "服务地址").fill(endpoint);
  await field("同步", "Access Key ID").fill("AKIDE2E");
  await field("同步", "Secret Access Key").fill("e2e-secret");
  // 保存拖慢、没等它完成就回到主界面：保存期间输入框不可改，保存完成后侧栏仍要跟上
  let sent;
  const slowSent = new Promise((r) => (sent = r));
  const slowSave = async (route) => {
    if (!route.request().postData()?.includes("AKIDE2E")) return route.continue();
    await new Promise((r) => setTimeout(r, 1000));
    await route.continue();
    sent();
  };
  await page.route("**/wails/runtime", slowSave);
  await btn(section("同步"), "保存").click();
  assert(await field("同步", "存储桶").isDisabled(), "保存期间输入框应不可改");
  await page.locator(".titlebar button[title='返回技能库']").click();
  await page.waitForSelector(".a-list");
  await slowSent;
  await page.unroute("**/wails/runtime", slowSave);
  await sidebarSays("手动", "保存晚于回到主界面完成时，侧栏也应显示手动");
  const raw = fs.readFileSync(settingsFile, "utf8");
  assert.deepStrictEqual(JSON.parse(raw).sync, { endpoint, bucket: "e2e-bucket", access_key_id: "AKIDE2E" });
  for (const secret of ["ghp_e2e_token", "e2e-secret"]) assert(!raw.includes(secret), `settings.json 里不应有 ${secret}`);

  // 重新载入后仍在;侧栏与同步框认设置页的值，同步计划发到设置页填的服务地址
  await page.reload();
  await page.waitForSelector(".a-list");
  await sidebarSays("手动", "填好同步设置后侧栏应显示手动");
  await sidebarSync().click();
  assert((await modal().locator('[data-slot="notice"][data-tone="warn"]', { hasText: "未配置" }).count()) === 0, "填好同步设置后不应提示未配置");
  await btn(modal(), "推送到 S3…").click();
  await modal().locator(".kv dd", { hasText: "还没有" }).waitFor();
  await page.keyboard.press("Escape");
  await open();
  assert((await field("GitHub", "个人访问令牌").inputValue()) === "ghp_e2e_token", "重新载入后令牌应仍在");
  assert((await field("同步", "存储桶").inputValue()) === "e2e-bucket", "重新载入后存储桶应仍在");
  assert((await field("同步", "Secret Access Key").inputValue()) === "e2e-secret", "重新载入后密钥应仍在");

  // 全部清空：回到读环境变量
  await field("GitHub", "个人访问令牌").fill("");
  await btn(section("GitHub"), "保存").click();
  await toast("已保存 GitHub 令牌");
  await settle();
  for (const label of ["服务地址", "存储桶", "Access Key ID", "Secret Access Key"]) await field("同步", label).fill("");
  await btn(section("同步"), "保存").click();
  await toast("已保存同步设置");
  await settle();
  assert(!("sync" in JSON.parse(fs.readFileSync(settingsFile, "utf8"))), "清空后 settings.json 里不应再有同步设置");
  await page.locator(".titlebar button[title='返回技能库']").click();
  await page.waitForSelector(".a-list");
  await sidebarSays("未配置", "清空后侧栏应回到未配置");
});

await step("设置页 Agent 分区：显示勾选、自定义 Agent 的添加 / 编辑迁移 / 删除、共用目录卸载", async () => {
  const settingsFile = `${HOME}/Library/Application Support/skills-manager/settings.json`;
  const agents = () => page.locator('.settings [data-slot="card"]', { has: page.locator('[data-slot="card-title"]', { hasText: /^Agent$/ }) });
  const row = (name) => agents().locator(".set-row", { hasText: name });
  const shownSwitch = (name) => row(name).locator('[role="switch"]');
  const open = async () => {
    await page.locator(".a-left .nav-item", { hasText: "设置" }).click();
    await page.locator('.settings [role="switch"]').first().waitFor();
  };
  const back = async () => {
    await page.locator(".titlebar button[title='返回技能库']").click();
    await page.waitForSelector(".a-list");
    await settle();
  };
  const fillAgent = async (name, global, project) => {
    const fields = modal().locator('[data-slot="input"]');
    await fields.nth(0).fill(name);
    await fields.nth(1).fill(global);
    await fields.nth(2).fill(project);
  };
  const installTo = async (skill, agentName) => {
    await openDrawer(skill);
    await drawer().locator(".install-to select").nth(0).selectOption("0");
    await drawer().locator(".install-to select").nth(1).selectOption({ label: agentName });
    await btn(drawer(), "安装").click();
    await toast(`已安装「${skill}」`);
    await settle();
  };
  // slow 把匹配的绑定调用拖慢，模拟慢请求;返回撤销函数。
  const slow = async (match) => {
    const handler = async (route) => {
      if (route.request().postData()?.includes(match)) await new Promise((r) => setTimeout(r, 700));
      await route.continue();
    };
    await page.route("**/wails/runtime", handler);
    return () => page.unroute("**/wails/runtime", handler);
  };
  const groupChip = (name) => page.locator(".gchip", { hasText: name });

  // 内置 Agent:检测到的默认勾上，不能编辑、删除。
  await open();
  for (const name of ["Claude Code", "Codex"]) {
    assert((await row(name).locator('[data-slot="badge"]', { hasText: "检测到" }).count()) === 1, `${name} 应标检测到`);
    assert((await shownSwitch(name).getAttribute("aria-checked")) === "true", `${name} 默认应显示`);
    assert((await row(name).getByRole("button", { name: "编辑" }).count()) === 0, `${name} 不能编辑`);
  }

  // 显示勾选：连着关掉两个、保存还没完成就返回，主界面也按最后的勾选;存进这台机器的设置文件，重新载入后仍关着。
  let unslow = await slow("agents_shown");
  await shownSwitch("Codex").click();
  await shownSwitch("Claude Code").click();
  await page.locator(".titlebar button[title='返回技能库']").click();
  await page.waitForSelector(".a-list");
  await settle();
  await unslow();
  await page.locator(".scope-pill").click();
  const projB = page.locator(".scope-menu > div", { has: page.locator(".sm-place", { hasText: "proj-b" }) });
  for (const name of ["Codex", "Claude Code"]) {
    assert((await projB.getByText(name).count()) === 0, `作用域弹层不应列出不显示的 ${name}`);
  }
  await page.keyboard.press("Escape");
  await openDrawer("alpha");
  await drawer().locator(".install-to select").nth(0).selectOption({ label: "proj-b" });
  assert((await drawer().locator(".install-to select").nth(1).locator("option", { hasText: "Codex" }).count()) === 0, "安装到…不应列出 Codex");
  await closeDrawer();
  assert.deepStrictEqual(JSON.parse(fs.readFileSync(settingsFile, "utf8")).agents_shown, { codex: false, "claude-code": false });
  await page.reload();
  await page.waitForSelector(".a-list");
  await open();
  assert((await shownSwitch("Codex").getAttribute("aria-checked")) === "false", "重新载入后 Codex 应仍不显示");
  await shownSwitch("Codex").click();
  await shownSwitch("Claude Code").click();
  await settle();

  // 添加：全局技能目录必须在用户主目录里面。
  await btn(agents(), "添加 Agent").click();
  await fillAgent("My Agent", "/opt/my-agent/skills", ".my-agent/skills");
  await btn(modal(), "添加").click();
  assert((await modal().locator('[data-slot="field-error"]', { hasText: "用户主目录里面" }).count()) === 1, "主目录外的全局目录应被拦下");
  await modal().locator('[data-slot="input"]').nth(1).fill("~/.my-agent/skills");
  await btn(modal(), "添加").click();
  await toast("已添加 Agent「My Agent」");
  await settle();
  assert((await row("My Agent").locator('[data-slot="badge"]', { hasText: "自定义" }).count()) === 1, "应标自定义");
  assert((await shownSwitch("My Agent").getAttribute("aria-checked")) === "true", "自定义 Agent 默认应显示");
  await back();

  // 装到自定义 Agent。
  await installTo("alpha", "My Agent");
  await installTo("gamma", "My Agent");
  assert(fs.existsSync(`${HOME}/.my-agent/skills/alpha/SKILL.md`), "alpha 应装进自定义 Agent 的全局目录");
  await closeDrawer();

  // 改目录并迁移：新目录已有同名文件夹的 gamma 不搬、列出来，alpha 原样搬过去。
  fs.mkdirSync(`${HOME}/.my-agent2/skills/gamma`, { recursive: true });
  fs.writeFileSync(`${HOME}/.my-agent2/skills/gamma/note.txt`, "already here");
  await open();
  await btn(row("My Agent"), "编辑").click();
  await modal().locator('[data-slot="input"]').nth(1).fill("~/.my-agent2/skills");
  await btn(modal(), "保存").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "改 Agent「My Agent」的目录" }).waitFor();
  await modal().locator('[data-slot="choice"]', { hasText: "迁移到新目录" }).click();
  await btn(modal(), "保存").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "已保存 Agent「My Agent」" }).waitFor();
  assert((await modal().locator('[data-slot="list-row"]', { hasText: "gamma" }).count()) === 1, "应列出没搬的 gamma");
  assert((await modal().locator('[data-slot="list-row"]', { hasText: "alpha" }).count()) === 0, "alpha 搬过去了，不应列出");
  await btn(modal(), "知道了").click();
  await settle();
  assert(fs.existsSync(`${HOME}/.my-agent2/skills/alpha/SKILL.md`) && !fs.existsSync(`${HOME}/.my-agent/skills/alpha`), "alpha 应原样搬到新目录");
  assert(fs.existsSync(`${HOME}/.my-agent/skills/gamma/SKILL.md`), "没搬的 gamma 留在旧目录");
  assert((await row("My Agent").locator(".font-mono", { hasText: "~/.my-agent2/skills" }).count()) === 1, "应显示新目录");
  await back();

  // 只有空组的组安装时改目录，也要问是否迁移;迁移后组安装还在。
  await page.locator(".a-left .nav-sec", { hasText: "组" }).locator("button[title='新建组']").click();
  await modal().locator('[data-slot="input"]').fill("空组");
  await btn(modal(), "创建").click();
  await toast("已创建组「空组」");
  await settle();
  await selectScope("My Agent", "proj-b");
  await groupChip("空组").click();
  await toast("已把「空组」组安装到");
  await settle();
  await clearScope();
  await open();
  await btn(row("My Agent"), "编辑").click();
  await modal().locator('[data-slot="input"]').nth(2).fill(".my-agent-p/skills");
  await btn(modal(), "保存").click();
  await modal().locator('[data-slot="hint"]', { hasText: "有 1 个组安装" }).waitFor();
  await modal().locator('[data-slot="choice"]', { hasText: "迁移到新目录" }).click();
  await btn(modal(), "保存").click();
  await toast("已保存 Agent「My Agent」");
  await settle();

  // 添加时连按两次 Enter 只添加一个。
  await btn(agents(), "添加 Agent").click();
  await fillAgent("Twin", "~/.my-agent2/skills", ".twin/skills");
  unslow = await slow('"Twin"');
  await modal().locator('[data-slot="input"]').nth(2).press("Enter");
  await modal().locator('[data-slot="input"]').nth(2).press("Enter");
  await toast("已添加 Agent「Twin」");
  await settle();
  await unslow();
  assert((await row("Twin").count()) === 1, "连按 Enter 只应添加一个 Twin");
  await back();
  await selectScope("My Agent", "proj-b");
  assert((await groupChip("空组").getAttribute("aria-pressed")) === "true", "迁移后空组的组安装应还在");
  await clearScope();

  // 共用目录：同一个目录的另一个 Agent 不用再装就算已装;从其中一个卸载时提示还被谁共用，确认后删除文件夹。
  await openDrawer("alpha");
  const tableRow = (label) => drawer().locator(".scope-table tbody tr", { hasText: label });
  assert((await tableRow("Twin · 全局").count()) === 1, "共用目录的 Twin 应算已装");
  await tableRow("Twin · 全局").locator('[role="switch"]').click();
  await modal().locator('[data-slot="notice"]', { hasText: "还被 My Agent 共用" }).waitFor();
  await btn(modal(), "卸载").click();
  await toast("已卸载「alpha」");
  await settle();
  assert(!fs.existsSync(`${HOME}/.my-agent2/skills/alpha`), "卸载后共用的文件夹应删除");
  assert((await tableRow("My Agent · 全局").count()) === 0, "共用的 My Agent 也不再算已装");
  await closeDrawer();

  // 组卸载也一样：只靠这个组安装留着的共用文件夹，卸载前提示还被谁共用。
  await selectScope("Twin");
  await groupChip("空组").click();
  await toast("已把「空组」组安装到");
  await settle();
  await openDrawer("beta");
  await drawer().locator("select.chip-add").selectOption({ label: "空组" });
  await toast("已把「beta」加入「空组」");
  await settle();
  await closeDrawer();
  assert(fs.existsSync(`${HOME}/.my-agent2/skills/beta/SKILL.md`), "beta 应随组装进共用目录");
  await groupChip("空组").click();
  await modal().locator('[data-slot="list-row"]', { hasText: "My Agent 在用" }).waitFor();
  await btn(modal(), "卸载").click();
  await toast("已卸载「空组」");
  await settle();
  assert(!fs.existsSync(`${HOME}/.my-agent2/skills/beta`), "组卸载后共用的文件夹应删除");
  await clearScope();

  // 不显示的 Agent 在已在用的地方选中后，换到别的地方不会仍装到它。
  await installTo("alpha", "Twin");
  await closeDrawer();
  await open();
  await shownSwitch("Twin").click();
  await settle();
  await back();
  await openDrawer("beta");
  await drawer().locator(".install-to select").nth(0).selectOption("0");
  await drawer().locator(".install-to select").nth(1).selectOption({ label: "Twin" });
  await drawer().locator(".install-to select").nth(0).selectOption({ label: "proj-b" });
  await btn(drawer(), "安装").click();
  await toast("已安装「beta」到 Claude Code · proj-b");
  await settle();
  await tableRow("Claude Code · proj-b").locator('[role="switch"]').click();
  await toast("已卸载「beta」");
  await settle();
  await closeDrawer();

  // 删除：还有别的 Agent 在读的文件夹即使选了一并删除也保留，确认框里写明。
  await open();
  await btn(row("My Agent"), "删除").click();
  await modal().locator('[data-slot="notice"]', { hasText: "还有别的 Agent 在用" }).waitFor();
  assert((await modal().locator('[data-slot="list-row"]', { hasText: "Twin 在用" }).count()) === 1, "确认框应写明 Twin 还在用");
  await btn(modal(), "删除").click();
  await toast("已删除 Agent「My Agent」");
  await settle();
  assert((await row("My Agent").count()) === 0, "删除后不再列出");
  assert(fs.existsSync(`${HOME}/.my-agent2/skills/alpha/SKILL.md`), "Twin 还在用的文件夹应保留");
  await btn(row("Twin"), "删除").click();
  await modal().locator('[data-slot="choice"]', { hasText: "同时删除已安装的文件夹" }).click();
  await btn(modal(), "删除").click();
  await toast("已删除 Agent「Twin」");
  await settle();
  assert(!fs.existsSync(`${HOME}/.my-agent2/skills/alpha`), "删除 Twin 时选了一并删除，文件夹应删除");
  await back();
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
  await cards.nth(0).locator('[role="switch"]').click();
  await toast("已安装「gamma」");
  await settle();
  await cards.nth(1).locator('[role="switch"]').click();
  await toast("来自 ~/code/my-skills 的同名技能已装在", "error");
  await clearScope();
});

await step("组安装遇到外来文件夹：选择跳过，结果列出被跳过的成员", async () => {
  await selectScope("Codex");
  const dir = await scopeDir();
  fs.mkdirSync(path.join(dir, "delta"), { recursive: true });
  fs.writeFileSync(path.join(dir, "delta", "x.txt"), "hand made");
  await page.locator(".gchip", { hasText: "前端调试" }).click();
  await modal().locator('[data-slot="notice"][data-tone="warn"]', { hasText: "delta" }).waitFor();
  await modal().locator('[data-slot="choice"]', { hasText: "跳过这些成员" }).click();
  await btn(modal(), "安装").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "组安装「前端调试」" }).waitFor();
  assert((await modal().locator('[data-slot="list-row"]', { hasText: "delta" }).locator('[data-slot="badge"]', { hasText: "已跳过" }).count()) === 1, "delta 应被跳过");
  await btn(modal(), "知道了").click();
  assert(fs.existsSync(path.join(dir, "delta", "x.txt")), "外来文件夹应保持原样");
  await page.locator(".gchip", { hasText: "前端调试" }).click(); // 卸载组安装，恢复
  await settle();
  await clearScope();
});

await step("检查更新失败：来源读不到，列出没读成的来源", async () => {
  fs.renameSync(`${ZIP_DIR}/delta.zip`, `${ZIP_DIR}/delta.zip.bak`);
  try {
    await page.locator(".a-left .nav-item", { hasText: "检查更新" }).click();
    await modal().locator('[data-slot="dialog-title"]', { hasText: "检查更新" }).waitFor();
    assert((await modal().locator('[data-slot="list-row"]', { hasText: "delta.zip" }).locator('[data-slot="badge"]', { hasText: "检查失败" }).count()) === 1, "delta.zip 应检查失败");
    await btn(modal(), "知道了").click();
    await settle();
  } finally {
    fs.renameSync(`${ZIP_DIR}/delta.zip.bak`, `${ZIP_DIR}/delta.zip`);
  }
});

// ============ 移除 ============
await step("移除技能(保留已安装的文件夹)", async () => {
  await openDrawer("delta");
  await btn(drawer(), "移除技能").click();
  await modal().locator('[data-slot="choice"]', { hasText: "保留已安装的文件夹" }).click();
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

await step("项目目录被移走 → 标记不在这台机器上 → 改项目位置", async () => {
  fs.renameSync(`${HOME}/code/proj-b`, `${HOME}/code/proj-b-moved`);
  await page.locator(".titlebar button[title^='刷新']").click();
  await settle();
  const row = page.locator(".a-right .nav-item.proj", { hasText: "proj-b" });
  await row.locator('[data-slot="badge"]', { hasText: "不在这台机器上" }).waitFor();
  await row.hover();
  await row.locator(".row-actions button[title='改项目位置']").click();
  await modal().locator('[data-slot="input"]').fill(`${HOME}/code/proj-b-moved`);
  await btn(modal(), "改位置").click();
  await toast("已把项目「proj-b-moved」指到");
  await settle();
  const moved = page.locator(".a-right .nav-item.proj", { hasText: "proj-b-moved" });
  assert((await moved.count()) === 1, "项目名应随新位置变化");
  assert((await moved.locator('[data-slot="badge"]', { hasText: "不在这台机器上" }).count()) === 0, "改位置后不应再标不在这台机器上");
});

await step("注销项目(删除项目里已安装的文件夹)", async () => {
  const row = page.locator(".a-right .nav-item.proj", { hasText: "proj-a" });
  await row.hover();
  await row.locator(".row-actions button[title='注销项目']").click();
  await btn(modal(), "注销").click();
  await toast("已注销项目");
  await settle();
  assert((await page.locator(".a-right .nav-item.proj", { hasText: "proj-a" }).count()) === 0, "项目应消失");
  // delta 已经"移除技能(保留已安装的文件夹)",库不再管它的文件夹，注销项目时也不删。
  const left = fs
    .readdirSync(`${HOME}/code/proj-a`, { recursive: true })
    .filter((f) => String(f).endsWith("SKILL.md") && !String(f).includes("delta/"));
  assert(left.length === 0, `项目里已安装的文件夹应被删除，剩余:${left}`);
});

// ============ 市场与 GitHub(需要网络) ============
await step("[网络] 技能市场搜索 → 入库", async () => {
  await page.locator(".side-btn", { hasText: "技能市场" }).click();
  await modal().locator("input").fill("frontend design");
  await btn(modal(), "搜索").click();
  await modal().locator('[data-slot="list-row"]').first().waitFor({ timeout: 30000 });
  await modal().locator('[data-slot="list-row"]').first().getByRole("button", { name: "入库", exact: true }).click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 90000 });
  await btn(modal(), "入库所选").click();
  await toast("已入库", undefined);
  await settle();
});

await step("[网络] GitHub 入库(owner/repo@技能名)", async () => {
  await openFetch();
  await btn(modal(), "GitHub").click();
  await modal().locator('[data-slot="input"]').first().fill("anthropics/skills@pdf");
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 90000 });
  assert((await modal().locator('[data-slot="list-row"]', { hasText: "pdf" }).count()) >= 1, "候选里应有 pdf");
  await btn(modal(), "入库所选").click();
  await toast("已入库");
  await settle();
  assert((await page.locator(".a-left .nav-item", { hasText: "anthropics/skills" }).count()) === 1, "集合名应为 owner/repo");
  await openDrawer("pdf");
  assert((await drawer().locator(".src-chip", { hasText: "anthropics/skills" }).count()) === 1, "详情里集合应为 owner/repo");
  assert((await drawer().getByRole("button", { name: "在 GitHub 查看" }).count()) === 1, "GitHub 来源应有查看链接");
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
