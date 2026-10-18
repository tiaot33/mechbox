// 桌面端端到端验证：server 模式 + 本机 Chrome,按界面操作走遍技能库的全部功能，并核对磁盘。
// 由 run.sh 准备环境后调用;单独运行时需给出下面几个环境变量。
import assert from "node:assert";
import { chromium } from "playwright-core";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
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
// previewCalls / refsCalls 数前端发出的 PreviewFetch / ListSourceRefs 调用(Wails 按绑定里的 methodID 发 POST)。
const bindings = fs.readFileSync(
  new globalThis.URL("../frontend/bindings/github.com/tiaot33/skills-tui/cmd/desktop/libraryservice.ts", import.meta.url),
  "utf8",
);
const methodID = (name) => bindings.match(new RegExp(`function ${name}\\([^]*?ByID\\((\\d+)`))[1];
const previewMethodID = methodID("PreviewFetch");
const refsMethodID = methodID("ListSourceRefs");
const appBindings = fs.readFileSync(
  new globalThis.URL("../frontend/bindings/github.com/tiaot33/skills-tui/cmd/desktop/appservice.ts", import.meta.url),
  "utf8",
);
const appMethodID = (name) => appBindings.match(new RegExp(`function ${name}\\([^]*?ByID\\((\\d+)`))[1];
let previewCalls = 0;
let refsCalls = 0;
// previewRequests 记下每次 PreviewFetch 的请求参数;refsCallIDs 是 ListSourceRefs 调用的 call-id,cancelled 是前端取消过的 call-id。
const previewRequests = [];
const refsCallIDs = [];
const cancelled = new Set();
const isCall = (r, id) => r.method() === "POST" && r.url().includes("/wails/runtime") && (r.postData() ?? "").includes(`"methodID":${id}`);
page.on("request", (r) => {
  if (r.method() !== "POST" || !r.url().includes("/wails/runtime")) return;
  const body = JSON.parse(r.postData() ?? "{}");
  // Wails 的取消请求:object 10(CancelCall),args 里是被取消调用的 call-id
  if (body.object === 10) cancelled.add(body.args?.["call-id"]);
  if (body.args?.methodID === Number(previewMethodID)) {
    previewCalls++;
    previewRequests.push(body.args.args[0]);
  }
  if (body.args?.methodID === Number(refsMethodID)) {
    refsCalls++;
    refsCallIDs.push(body.args["call-id"]);
  }
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
const candidateNames = async () => (await modal().locator('[data-slot="list-row"] b').allTextContents()).sort();
const scopeDir = async () => {
  const t = (await page.locator(".a-scope-sum .a-sum-row .font-mono").first().textContent()).trim();
  return t.replace(/^~/, HOME);
};

// ============ 第一次打开 ============
const FIRST_OPEN = "第一次打开：各 Agent 全局里不是库装的技能一次确认入库，磁盘不动;同名不一样的只入库较新的一份;读不成来源的清空后入库";
if (only && !FIRST_OPEN.includes(only)) {
  // 只跑部分步骤时，先关掉第一次打开的确认。
  await modal().waitFor({ timeout: 5000 }).catch(() => {});
  if (await modal().count()) await btn(modal(), "先不入库").click();
}
await step(FIRST_OPEN, async () => {
  const claude = path.join(HOME, ".claude", "skills");
  const agents = path.join(HOME, ".agents", "skills");
  const link = path.join(claude, "first-pdf");
  const disk = () => [
    fs.readlinkSync(link),
    ...["first-notes", "first-dup"].map((n) => fs.readFileSync(path.join(claude, n, "SKILL.md"), "utf8")),
    ...["first-pdf", "first-dup"].map((n) => fs.readFileSync(path.join(agents, n, "SKILL.md"), "utf8")),
  ];
  const before = disk();
  const names = ["first-notes", "first-pdf", "first-dup"];
  try {
    await modal().locator('[data-slot="dialog-title"]', { hasText: "把你在用的技能交给 app 管" }).waitFor({ timeout: 15000 });
    const lead = await modal().locator(".fetch-unmanaged-lead").textContent();
    assert(lead.includes("在各 Agent 的全局目录里发现 4 个不是库装的技能") && lead.includes("磁盘上什么都不改，Agent 用的和现在一样"), `开头应先说磁盘不动:${lead}`);
    const text = await modal().textContent();
    assert(text.includes("之后如果还用命令行工具更新这些技能，app 只能把它们报为被手动改过"), `确认前写清代价:${text}`);
    assert(text.includes("项目里的技能，登记项目时再接"), "第一次打开时说明项目里的之后再接");
    assert((await modal().locator(".fetch-unmanaged-team").count()) === 0, "全局里没有项目仓库的提醒");
    // 符号链接和它指向的真实文件夹内容一样，合成一条，两个 Agent 都列出。
    const group = modal().locator(".fetch-unmanaged-group", { hasText: "没有来源" });
    assert.deepStrictEqual((await group.locator(".fetch-unmanaged-name").allTextContents()).sort(), ["first-notes", "first-pdf"], "没有来源的一组");
    assert((await group.locator('[data-slot="list-row"]', { hasText: "first-pdf" }).textContent()).includes("Claude Code、Codex"), "内容一样的几处合成一条");
    // 同名但内容不一样的单列出来，默认选最近修改的那份。
    const conflict = modal().locator(".fetch-unmanaged-conflict", { hasText: "first-dup" });
    assert.equal(await conflict.locator('input[type="radio"]').count(), 3, "两份加上都先不入库");
    const chosen = conflict.locator('[data-slot="list-row"]', { has: page.locator('input[type="radio"]:checked') });
    assert((await chosen.textContent()).startsWith("Codex"), `默认选较新的 Codex 那份:${await chosen.textContent()}`);
    await page.screenshot({ path: path.join(SHOTS, "first-open.png") });
    // 确认前可以给认不出来源的补一个来源链接;读不成时只有它没入库，清空后不带来源入库。
    const notes = group.locator('[data-slot="list-row"]', { hasText: "first-notes" });
    await notes.locator(".fetch-unmanaged-edit").click();
    const missing = path.join(HOME, "sources", "nowhere");
    await modal().locator(".fetch-unmanaged-source").fill(missing);
    assert.equal(await notes.locator(".fetch-unmanaged-changed").textContent(), `改为跟着 ${missing}`, "改过的来源写在这一条上");
    await btn(modal(), "入库 3 个").click();
    await modal().locator('[data-slot="dialog-title"]', { hasText: "还有几个没入库" }).waitFor({ timeout: 15000 });
    assert((await modal().textContent()).includes("已入库 2 个技能"), "其余的照常入库");
    const failed = modal().locator(".fetch-unmanaged-failed [data-slot='list-row']");
    assert.equal(await failed.count(), 1, "只有 first-notes 没入库");
    assert((await failed.locator(".fetch-unmanaged-error").textContent()).includes("清空来源链接"), "告诉用户能清空来源");
    assert.equal(await failed.locator(".fetch-unmanaged-link").inputValue(), missing, "来源链接是改过的那个");
    await failed.locator(".fetch-unmanaged-link").fill("");
    await btn(failed, "不带来源入库").click();
    await failed.first().waitFor({ state: "detached" });
    assert((await modal().textContent()).includes("已入库 3 个技能"), "重试成功后数上它");
    await btn(modal(), "完成").click();
    await settle();
    for (const n of names) await card(n).waitFor();
    assert.deepStrictEqual(disk(), before, "入库时磁盘上什么都没动");
    assert(fs.lstatSync(link).isSymbolicLink(), "符号链接还是符号链接");

    // 没选的那份仍是未纳管技能：作用域里淡淡提一句，从这里可以再全部入库。
    await selectScope("Claude Code");
    const hint = page.locator(".unmanaged-hint");
    assert((await hint.textContent()).includes("这里还有 1 个技能不是库装的"), `作用域里提一句:${await hint.textContent()}`);
    await btn(hint, "全部入库…").click();
    await modal().locator(".fetch-unmanaged-lead", { hasText: "在 Claude Code · 全局 里发现 1 个不是库装的技能" }).waitFor();
    await btn(modal(), "先不入库").click();
  } finally {
    // 后面的步骤不认这几个技能：连同文件夹移除。
    for (let i = 0; i < 3; i++) await page.keyboard.press("Escape");
    await clearScope();
    await settle();
    for (const n of names) {
      if ((await card(n).count()) === 0) continue;
      await openDrawer(n);
      await btn(drawer(), "移除技能").click();
      await btn(modal(), "移除").click();
      await toast("已移除");
      await settle();
    }
    for (const n of names) {
      fs.rmSync(path.join(claude, n), { recursive: true, force: true });
      fs.rmSync(path.join(agents, n), { recursive: true, force: true });
    }
    await page.locator(".titlebar button[title^='刷新']").click();
    await settle();
  }
});

// ============ 入库 ============
await step("入库对话框只有一个输入框，没有来源标签", async () => {
  await openFetch();
  for (const tab of ["本地目录", "GitHub", "URL 压缩包"]) {
    assert((await btn(modal(), tab).count()) === 0, `不应有「${tab}」标签`);
  }
  assert((await modal().locator('[data-slot="segmented"], [role="radiogroup"]').count()) === 0, "不应有来源切换");
  assert((await btn(modal(), "选择…").count()) === 1, "应保留「选择…」按钮");
  assert((await modal().getByText("仓库内目录").count()) === 0, "不应有仓库内目录输入框");
  assert((await modal().locator(".fetch-refs").count()) === 0, "没有输入时不应显示分支下拉");
  await modal().getByRole("button", { name: "取消" }).click();
});

await step("本地目录入库 alpha/beta/gamma(入库预览 → 候选 → 入库)", async () => {
  await previewLocal(SK);
  assert((await modal().locator(".fetch-source", { hasText: "本地目录" }).count()) === 1, "应识别为本地目录");
  assert((await modal().locator(".fetch-source", { hasText: SK }).count()) === 1, "展示名称应为目录路径");
  assert((await modal().getByText("匿名访问 GitHub API").count()) === 0, "本地目录不应提醒 GitHub 限流");
  assert((await modal().getByRole("button", { name: "查看审计" }).count()) === 0, "本地目录不应有审计");
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

await step("预览不存在的目录：在对话框里显示原因", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(`${SK}/nope`);
  await btn(modal(), "入库预览").click();
  await modal().locator(".fetch-failure").waitFor({ timeout: 20000 });
  const reason = await modal().locator(".fetch-failure").textContent();
  assert(reason.includes("no such file or directory"), `应写出技能库给出的原因，实际：${reason}`);
  assert((await page.locator('[data-slot="toast"][data-state="open"][data-tone="error"]').count()) === 0, "原因不应只弹 toast");
  await modal().locator('[data-slot="input"]').first().fill(`${SK}/nope2`);
  assert((await modal().locator(".fetch-failure").count()) === 0, "改了输入应清掉原因");
  await modal().getByRole("button", { name: "取消" }).click();
});

await step("无效链接：在对话框里显示技能库给出的原因", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(ZIP_URL.replace(/delta\.zip$/, "nothing-here"));
  await btn(modal(), "入库预览").click();
  await modal().locator(".fetch-failure").waitFor({ timeout: 30000 });
  const reason = await modal().locator(".fetch-failure").textContent();
  assert(reason.includes("输入无效"), `应按错误码写出主文案，实际：${reason}`);
  assert(reason.includes("neither a skill file or archive, nor a site with a well-known skills index, nor a Git repository"), `应写出技能库给出的原因，实际：${reason}`);
  assert((await modal().locator('[data-slot="dialog-title"]', { hasText: "入库技能" }).count()) === 1, "应留在入库对话框");
  await modal().getByRole("button", { name: "取消" }).click();
});

// ============ 分支下拉 ============
const GIT_REPO = "https://git.e2e.test/org/repo.git";
const refsTrigger = () => modal().locator(".refs-trigger");
const refsMenu = () => page.locator(".refs-menu");
// refItems 列出下拉里某一组(分支 / Tag)的选项名。
const refItems = (group) =>
  refsMenu()
    .locator('[data-slot="command-group"]', { has: page.locator("[cmdk-group-heading]", { hasText: new RegExp(`^${group}$`) }) })
    .locator('[data-slot="command-item"] .font-mono')
    .allTextContents();
const exactly = (text) => new RegExp(`^${text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);
// waitRefs 等下拉读完，触发按钮显示选中的分支或 tag。
async function waitRefs(selected) {
  await refsTrigger().filter({ hasText: exactly(selected) }).waitFor({ timeout: 20000 });
}
// chooseRef 在下拉里点选一个分支或 tag,等触发按钮显示它。
async function chooseRef(name) {
  await refsTrigger().click();
  await refsMenu().locator('[data-slot="command-item"]', { has: page.locator(".font-mono", { hasText: exactly(name) }) }).click();
  await waitRefs(name);
}
// previewRefs 点入库预览，等到结果页出现。
async function previewRefs() {
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 30000 });
}
const GIT_TREE = "https://git.e2e.test/org/repo/-/tree";
// denyGit 起一个 HTTP 服务，等 delay 毫秒后对一切请求回 403,像一个读不了的私有 Git 仓库。
async function denyGit(delay = 0) {
  const server = http.createServer((_, res) => setTimeout(() => res.writeHead(403).end(), delay));
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  return {
    url: (name) => `http://127.0.0.1:${server.address().port}/org/${name}.git`,
    close: () => {
      server.closeAllConnections();
      server.close();
    },
  };
}

await step("本地路径与 zip 链接不显示分支下拉，不取列表", async () => {
  await openFetch();
  const input = modal().locator('[data-slot="input"]').first();
  const before = refsCalls;
  await input.fill(SK);
  await page.waitForTimeout(1000);
  assert.equal(await modal().locator(".fetch-refs").count(), 0, "本地路径不应显示分支下拉");
  assert.equal(refsCalls - before, 0, "本地路径不应取分支列表");
  await input.fill(ZIP_URL);
  await page.waitForTimeout(1200);
  assert.equal(await modal().locator(".fetch-refs").count(), 0, "zip 链接不应显示分支下拉");
  await modal().getByRole("button", { name: "取消" }).click();
});

await step("Git 仓库：下拉分组列出分支和 tag,默认分支已选中，可筛选", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(GIT_REPO);
  await waitRefs("main");
  await refsTrigger().click();
  assert.deepEqual(await refItems("分支"), ["main", "release/v2"], "分支组应列出全部分支");
  assert.deepEqual(await refItems("Tag"), ["v1.0"], "Tag 组应列出全部 tag");
  assert.equal(await refsMenu().locator('[data-slot="command-item"]', { hasText: "默认分支" }).count(), 1, "默认分支应有标记");
  await refsMenu().locator('[data-slot="command-input"]').fill("rel");
  assert.deepEqual(await refItems("分支"), ["release/v2"], "筛选后只剩匹配的分支");
  assert.equal(await refsMenu().locator("[cmdk-group-heading]:visible", { hasText: "Tag" }).count(), 0, "没有匹配的 tag 时不显示 Tag 组");
  await page.keyboard.press("Escape");
  assert.equal(await modal().locator('[data-slot="dialog-title"]', { hasText: "入库技能" }).count(), 1, "Esc 只关下拉，不关对话框");
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 30000 });
  assert.equal(await modal().locator(".fetch-source").getByText("分支 main").count(), 1, "按默认分支扫描");
  assert.equal(previewRequests.at(-1).ref, undefined, "选的是默认分支时不另外传 ref");
  assert.deepEqual(await candidateNames(), ["git-main", "git-other", "git-tagged"], "应列出整个仓库的候选");
  await page.keyboard.press("Escape");
});

await step("选一个 tag 后入库预览：结果页的分支是这个 tag(键盘筛选、回车选中)", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(GIT_REPO);
  await waitRefs("main");
  assert.equal(await refsTrigger().getAttribute("disabled"), null, "输入里没带分支时下拉可选");
  await refsTrigger().click();
  const filter = refsMenu().getByRole("combobox", { name: "筛选分支和 tag" });
  assert.equal(await filter.count(), 1, "筛选框应有可访问名称");
  await filter.fill("v1");
  await page.keyboard.press("Enter");
  await waitRefs("v1.0");
  await refsMenu().waitFor({ state: "detached", timeout: 2000 });
  assert(await refsTrigger().evaluate((el) => el === document.activeElement), "选中后焦点应回到下拉按钮");
  assert.equal(await modal().getByRole("button", { name: "分支 / tag v1.0" }).count(), 1, "下拉按钮的可访问名称应带字段名和选中的值");
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 30000 });
  assert.equal(await modal().locator(".fetch-source").getByText("分支 v1.0").count(), 1, "结果页应显示 tag v1.0");
  assert.equal(previewRequests.at(-1).ref, "v1.0", "应把选中的 tag 传给技能库");
  assert.deepEqual(await candidateNames(), ["git-tagged"], "应按 tag 的内容列候选");
  await page.keyboard.press("Escape");
});

await step("tree 链接：列表没取回就入库预览，按链接里带斜杠的分支扫描;改选 main 后按 main 扫描链接里的目录", async () => {
  await openFetch();
  const input = modal().locator('[data-slot="input"]').first();
  const link = `${GIT_TREE}/release/v2/skills`;
  await input.fill(link);
  await previewRefs();
  assert.equal(await modal().locator(".fetch-source").getByText("分支 release/v2").count(), 1, "结果页应显示分支 release/v2");
  assert.deepEqual(await candidateNames(), ["git-main", "git-tagged", "git-v2"], "只应列 skills 目录下的技能");
  assert.deepEqual(previewRequests.at(-1), { input: link, ref: "release/v2" }, "应把列表里认出的分支显式传给技能库，输入照原样发");
  await btn(modal(), "返回").click();
  await waitRefs("release/v2");
  assert.equal(await refsTrigger().getAttribute("disabled"), null, "输入里带了分支时下拉也可选");
  assert.equal(await modal().locator(".refs-locked").count(), 0, "能换分支时不应说明分支由输入决定");
  await chooseRef("main");
  await previewRefs();
  assert.equal(await modal().locator(".fetch-source").getByText("分支 main").count(), 1, "结果页应显示分支 main");
  assert.deepEqual(await candidateNames(), ["git-main", "git-tagged"], "应按 main 扫描 skills 目录");
  assert.deepEqual(previewRequests.at(-1), { input: `${GIT_TREE}/main/skills`, ref: "main" }, "带斜杠的分支应整段换成 main");
  await btn(modal(), "返回").click();
  await waitRefs("main");
  assert.equal(await input.inputValue(), link, "输入框里的文字不应改变");
  await chooseRef("release/v2");
  await previewRefs();
  assert.deepEqual(previewRequests.at(-1), { input: link, ref: "release/v2" }, "选回输入里的分支时输入照原样发");
  await page.keyboard.press("Escape");
});

await step("GitLab tree 链接：初始选中链接里的分支，改选后只在请求里换掉分支，只列链接里目录下的技能", async () => {
  await openFetch();
  const input = modal().locator('[data-slot="input"]').first();
  const link = `${GIT_TREE}/main/skills`;
  await input.fill(link);
  await waitRefs("main");
  assert.equal(await refsTrigger().getAttribute("disabled"), null, "输入里带了分支时下拉也可选");
  await chooseRef("release/v2");
  assert.equal(await input.inputValue(), link, "改选后输入框里的文字不应改变");
  await previewRefs();
  assert.equal(await modal().locator(".fetch-source").getByText("分支 release/v2").count(), 1, "结果页应显示分支 release/v2");
  assert.deepEqual(await candidateNames(), ["git-main", "git-tagged", "git-v2"], "只应列 skills 目录下的技能");
  assert.deepEqual(previewRequests.at(-1), { input: `${GIT_TREE}/release/v2/skills`, ref: "release/v2" }, "应把 /-/tree/main/ 换成 /-/tree/release/v2/");
  await btn(modal(), "返回").click();
  await waitRefs("release/v2");
  assert.equal(await input.inputValue(), link, "返回后输入框里仍是原来的链接");
  await page.keyboard.press("Escape");
});

await step("tree 链接在分支处结束：改选 tag 后按 tag 扫描整个仓库", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(`${GIT_TREE}/main`);
  await waitRefs("main");
  await chooseRef("v1.0");
  await previewRefs();
  assert.equal(await modal().locator(".fetch-source").getByText("分支 v1.0").count(), 1, "结果页应显示 tag v1.0");
  assert.deepEqual(await candidateNames(), ["git-tagged"], "应按 tag 的内容列候选");
  assert.deepEqual(previewRequests.at(-1), { input: `${GIT_TREE}/v1.0`, ref: "v1.0" }, "应把 /-/tree/main 换成 /-/tree/v1.0");
  await page.keyboard.press("Escape");
});

await step("tree 链接改选的分支里没有这个目录：显示技能库给出的错误", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(`${GIT_TREE}/main/.claude/skills`);
  await waitRefs("main");
  await chooseRef("v1.0");
  await btn(modal(), "入库预览").click();
  await modal().locator(".fetch-failure").waitFor({ timeout: 30000 });
  assert.deepEqual(previewRequests.at(-1), { input: `${GIT_TREE}/v1.0/.claude/skills`, ref: "v1.0" }, "应按改选的 tag 发请求");
  const reason = await modal().locator(".fetch-failure").textContent();
  assert(reason.includes("目标不存在") && reason.includes("source subpath not found"), `应写出技能库给出的错误，实际：${reason}`);
  assert((await modal().locator('[data-slot="dialog-title"]', { hasText: "入库技能" }).count()) === 1, "应留在入库对话框");
  await modal().getByRole("button", { name: "取消" }).click();
});

await step("#ref 输入：下拉选中输入里的 tag;改选默认分支后按默认分支扫描", async () => {
  await openFetch();
  const input = modal().locator('[data-slot="input"]').first();
  await input.fill(`${GIT_REPO}#v1.0`);
  await waitRefs("v1.0");
  assert.equal(await refsTrigger().getAttribute("disabled"), null, "输入里带了分支时下拉也可选");
  await previewRefs();
  assert.equal(await modal().locator(".fetch-source").getByText("分支 v1.0").count(), 1, "结果页应显示 tag v1.0");
  assert.deepEqual(await candidateNames(), ["git-tagged"], "应按 tag 的内容列候选");
  assert.deepEqual(previewRequests.at(-1), { input: `${GIT_REPO}#v1.0`, ref: "v1.0" }, "没改选时输入照原样发");
  await btn(modal(), "返回").click();
  await chooseRef("main");
  await previewRefs();
  assert.equal(await modal().locator(".fetch-source").getByText("分支 main").count(), 1, "结果页应显示默认分支 main");
  assert.deepEqual(await candidateNames(), ["git-main", "git-other", "git-tagged"], "应按默认分支扫描整个仓库");
  assert.deepEqual(previewRequests.at(-1), { input: GIT_REPO, ref: "main" }, "应去掉 #v1.0 并带上默认分支");
  await btn(modal(), "返回").click();
  await waitRefs("main");
  assert.equal(await input.inputValue(), `${GIT_REPO}#v1.0`, "输入框里的文字不应改变");
  await page.keyboard.press("Escape");
});

await step("预览还没返回时改选分支：旧分支的结果不采用", async () => {
  // PreviewFetch 的响应压后 2 秒，好在它返回前改选
  const hold = async (route) => {
    if (!isCall(route.request(), previewMethodID)) return route.continue();
    const response = await route.fetch();
    await page.waitForTimeout(2000);
    await route.fulfill({ response }).catch(() => {});
  };
  await page.route("**/wails/runtime*", hold);
  try {
    await openFetch();
    await modal().locator('[data-slot="input"]').first().fill(GIT_REPO);
    await waitRefs("main");
    await refsTrigger().click();
    await refsMenu().locator('[data-slot="command-item"]', { hasText: "v1.0" }).click();
    await btn(modal(), "入库预览").click();
    const busy = modal().locator('[data-slot="button"][aria-busy="true"]');
    await busy.waitFor({ timeout: 2000 });
    await refsTrigger().click();
    await refsMenu().locator('[data-slot="command-item"]', { hasText: "main" }).click();
    await waitRefs("main");
    assert.equal(await busy.count(), 0, "改选后按钮不应再忙");
    await page.waitForTimeout(3000);
    assert.equal(await modal().locator('[data-slot="dialog-title"]', { hasText: "入库技能" }).count(), 1, "旧分支的预览结果不应采用");
  } finally {
    await page.unroute("**/wails/runtime*", hold);
  }
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 30000 });
  assert.equal(await modal().locator(".fetch-source").getByText("分支 main").count(), 1, "结果页应是改选后的分支");
  assert.equal(previewRequests.at(-1).ref, undefined, "改选回默认分支时不另外传 ref");
  await page.keyboard.press("Escape");
});

await step("列表请求刚发出就关掉对话框：请求被取消，晚到的错误不报成未处理", async () => {
  // ListSourceRefs 的响应压后 1 秒;zip 链接不是 Git 类来源，技能库马上返回错误，压住的错误会在取消之后才到
  const hold = async (route) => {
    if (!isCall(route.request(), refsMethodID)) return route.continue();
    const response = await route.fetch();
    await page.waitForTimeout(1000);
    await route.fulfill({ response }).catch(() => {});
  };
  await page.route("**/wails/runtime*", hold);
  const errors = problems.filter((p) => p.startsWith("pageerror")).length;
  try {
    await openFetch();
    const before = refsCalls;
    await modal().locator('[data-slot="input"]').first().fill(ZIP_URL);
    while (refsCalls === before) await page.waitForTimeout(20);
    await modal().getByRole("button", { name: "取消" }).click();
    await page.waitForTimeout(1500);
    assert(cancelled.has(refsCallIDs.at(-1)), "关掉对话框应取消还没返回的列表请求");
    assert.equal(problems.filter((p) => p.startsWith("pageerror")).length, errors, "取消后晚到的错误不应报成未处理");
  } finally {
    await page.unroute("**/wails/runtime*", hold);
  }
});

await step("快速改输入：分支下拉只显示最后一次输入的列表", async () => {
  const slow = await denyGit(3000);
  try {
    await openFetch();
    const input = modal().locator('[data-slot="input"]').first();
    // 停下输入前不取列表：连着改几次只按最后一次取
    let before = refsCalls;
    await input.fill(slow.url("a"));
    await input.fill(slow.url("b"));
    await input.fill(GIT_REPO);
    await waitRefs("main");
    assert.equal(refsCalls - before, 1, "连着改输入只应取一次列表");
    // 旧请求还没返回时改输入：读取中照常可以入库预览，旧请求的结果不采用
    before = refsCalls;
    await input.fill(slow.url("slow"));
    await refsTrigger().filter({ hasText: "正在读取" }).waitFor({ timeout: 2000 });
    await page.waitForTimeout(800);
    assert.equal(refsCalls - before, 1, "停下输入后应取列表");
    assert.equal(await refsTrigger().filter({ hasText: "正在读取" }).count(), 1, "列表还没取回时应显示正在读取");
    assert(!(await btn(modal(), "入库预览").isDisabled()), "读取中入库预览应照常可点");
    await input.fill(GIT_REPO);
    await waitRefs("main");
    await page.waitForTimeout(4000);
    assert.equal(await modal().locator(".refs-failure").count(), 0, "旧请求的失败原因不应显示在新输入下面");
    await waitRefs("main");
    await refsTrigger().click();
    assert.deepEqual(await refItems("分支"), ["main", "release/v2"], "下拉应是最后一次输入的列表");
    await page.keyboard.press("Escape");
    await modal().getByRole("button", { name: "取消" }).click();
  } finally {
    slow.close();
  }
});

await step("取不到分支列表时显示原因，仍能入库预览", async () => {
  const denied = await denyGit();
  try {
    await openFetch();
    await modal().locator('[data-slot="input"]').first().fill(denied.url("private"));
    const failure = modal().locator(".refs-failure");
    await failure.waitFor({ timeout: 20000 });
    const reason = await failure.textContent();
    assert(reason.includes("权限不足"), `应写出取不到的原因，实际：${reason}`);
    assert.equal(await refsTrigger().count(), 0, "取不到列表时不显示下拉");
    await btn(modal(), "入库预览").click();
    await modal().locator(".fetch-failure").waitFor({ timeout: 30000 });
    assert((await modal().locator(".fetch-failure").textContent()).includes("权限不足"), "入库预览应报出实际的错误");
    await modal().getByRole("button", { name: "取消" }).click();
  } finally {
    denied.close();
  }
});

await step("预览还没返回时改了输入：旧请求的结果和原因都不采用", async () => {
  // 每个请求都等 1 秒再回 404:链接本身、四个 well-known 索引与 git ls-remote 走完，旧请求大约 6 秒后才失败。
  const slow = http.createServer((_, res) => setTimeout(() => res.writeHead(404).end(), 1000));
  await new Promise((resolve) => slow.listen(0, "127.0.0.1", resolve));
  try {
    await openFetch();
    const input = modal().locator('[data-slot="input"]').first();
    await input.fill(`http://127.0.0.1:${slow.address().port}/slow`);
    await btn(modal(), "入库预览").click();
    // 忙碌时按钮文字不可见，按钮不再以「入库预览」为名，按 aria-busy 找。
    const busy = modal().locator('[data-slot="button"][aria-busy="true"]');
    await busy.waitFor({ timeout: 2000 });
    await input.fill(SK);
    assert.equal(await busy.count(), 0, "改了输入后按钮不应再忙");
    assert(!(await btn(modal(), "入库预览").isDisabled()), "改了输入后应能马上再预览");
    await page.waitForTimeout(9000);
    assert((await modal().locator(".fetch-failure").count()) === 0, "旧请求的失败原因不应显示在新输入下面");
    assert((await modal().locator('[data-slot="dialog-title"]', { hasText: "入库技能" }).count()) === 1, "应留在入库对话框");
    await btn(modal(), "入库预览").click();
    await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
    assert((await modal().locator(".fetch-source", { hasText: SK }).count()) === 1, "结果页应是新输入的入库预览");
    await page.keyboard.press("Escape");
  } finally {
    slow.closeAllConnections();
    slow.close();
  }
});

await step("空白输入回车不发请求，连按两次只发一次，返回后改输入再预览", async () => {
  await openFetch();
  const input = modal().locator('[data-slot="input"]').first();
  const before = previewCalls;
  await input.fill("   ");
  await input.press("Enter");
  await page.waitForTimeout(500);
  assert.equal(previewCalls, before, "空白输入不应发请求");
  assert(await btn(modal(), "入库预览").isDisabled(), "空白输入时不能提交");
  assert((await modal().locator(".fetch-failure, [data-slot=\"field-error\"]").count()) === 0, "空白输入不提示错误");

  await input.fill(ZIP_URL);
  // 两次回车在同一轮事件里连着到达(界面还没来得及重新渲染),只应发一次请求。
  await input.evaluate((el) => {
    for (let i = 0; i < 2; i++) el.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
  });
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  assert.equal(previewCalls, before + 1, "连按两次回车只应发一次请求");
  assert((await modal().locator(".fetch-source", { hasText: "直接下载" }).count()) === 1, "应识别为直接下载");

  await btn(modal(), "返回").click();
  assert.equal(await modal().locator('[data-slot="input"]').first().inputValue(), ZIP_URL, "返回后输入应保留");
  await modal().locator('[data-slot="input"]').first().fill(SK);
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  assert.equal(previewCalls, before + 2, "返回后再预览应发新请求");
  assert((await modal().locator(".fetch-source", { hasText: "本地目录" }).count()) === 1, "结果页应是新输入的识别结果");
  assert((await modal().locator('[data-slot="list-row"]', { hasText: "delta" }).count()) === 0, "不应留着上一次的候选");
  await page.keyboard.press("Escape");
});

await step("well-known 站点链接：识别为 well-known 索引，只列链接路径下的技能", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(ZIP_URL.replace(/delta\.zip$/, "site/"));
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  assert((await modal().locator(".fetch-source", { hasText: "well-known 索引" }).count()) === 1, "应识别为 well-known 索引");
  assert((await modal().locator(".fetch-source", { hasText: "/site" }).count()) === 1, "展示名称应带站点路径");
  assert((await modal().locator('[data-slot="list-row"]', { hasText: "omega" }).count()) === 1, "候选里应有 omega");
  assert((await modal().getByText("匿名访问 GitHub API").count()) === 0, "well-known 不应提醒 GitHub 限流");
  assert((await modal().getByRole("button", { name: "查看审计" }).count()) === 0, "well-known 不应有审计");
  await page.keyboard.press("Escape");
});

await step("本地路径：./ 相对路径被拦下，~ 展开为主目录", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill("./code/my-skills");
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

await step("SKILL.md 链接识别为直接下载(只预览，不入库)", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(ZIP_URL.replace(/delta\.zip$/, "delta/SKILL.md"));
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  assert((await modal().locator(".fetch-source", { hasText: "直接下载" }).count()) === 1, "应识别为直接下载");
  assert((await modal().locator(".fetch-source", { hasText: "delta/SKILL.md" }).count()) === 1, "展示名称应为文件地址");
  assert((await modal().locator('[data-slot="list-row"]', { hasText: "delta" }).count()) === 1, "候选里应有 delta");
  assert((await modal().getByRole("button", { name: "查看审计" }).count()) === 0, "直接下载不应有审计");
  await page.keyboard.press("Escape");
});

await step("zip 链接入库(入库预览 → 候选 → 入库)", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(ZIP_URL);
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 20000 });
  assert((await modal().locator(".fetch-source", { hasText: "直接下载" }).count()) === 1, "应识别为直接下载");
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
  assert((await d.locator(".file-row", { hasText: "run.sh" }).locator('[data-slot="badge"]', { hasText: "可执行" }).count()) === 1, "run.sh 应标可执行");
  await d.locator(".file-row", { hasText: "run.sh" }).click();
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

await step("登记项目后：一次确认入库项目里不是库装的技能，项目仓库里的默认不勾;确认前清空认出的来源", async () => {
  const projC = path.join(HOME, "code", "team-proj");
  const dir = path.join(projC, ".claude", "skills");
  const docs = { "team-rules": "committed by the team", "proj-notes": "my project notes", "locked-tool": "installed by the lock file", "lint-rules": "also in the lock file" };
  for (const [name, description] of Object.entries(docs)) {
    fs.mkdirSync(path.join(dir, name), { recursive: true });
    fs.writeFileSync(path.join(dir, name, "SKILL.md"), `---\nname: ${name}\ndescription: "${description}"\n---\n`);
  }
  // skills-lock.json 提交在仓库里，技能文件夹本身没提交;记下的来源已经不在了。
  const locked = { source: "./vendor/missing", sourceType: "local", computedHash: "abc" };
  fs.writeFileSync(path.join(projC, "skills-lock.json"), JSON.stringify({ version: 1, skills: { "locked-tool": locked, "lint-rules": locked } }));
  const git = (...args) => execFileSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@example.com", "-c", "commit.gpgsign=false", "-C", projC, ...args]);
  git("init", "-q", "-b", "main");
  git("add", "-f", ".claude/skills/team-rules", "skills-lock.json");
  git("commit", "-qm", "team");
  const disk = () => Object.keys(docs).map((n) => fs.readFileSync(path.join(dir, n, "SKILL.md"), "utf8"));
  const before = disk();
  const row = (name) => modal().locator('[data-slot="list-row"]', { has: page.locator(".fetch-unmanaged-name", { hasText: new RegExp(`^${name}$`) }) });
  const checked = (name) => row(name).locator('input[type="checkbox"]').isChecked();
  try {
    await page.locator(".a-right .nav-sec", { hasText: "已登记项目" }).locator("button[title='登记项目']").click();
    await modal().locator('[data-slot="input"]').fill(projC);
    await btn(modal(), "登记").click();
    await toast("已登记项目");
    await modal().locator(".fetch-unmanaged-lead", { hasText: "在项目「team-proj」里发现 4 个不是库装的技能" }).waitFor({ timeout: 15000 });
    assert(!(await modal().textContent()).includes("登记项目时再接"), "登记项目时不再说之后再接");
    assert(!(await checked("team-rules")) && (await row("team-rules").textContent()).includes("在项目仓库里"), "提交在项目仓库里的默认不勾");
    assert(!(await checked("locked-tool")) && (await row("locked-tool").textContent()).includes("记在 skills-lock.json 里"), "skills-lock.json 记的默认不勾");
    assert(await checked("proj-notes"), "自己的默认勾上");
    assert((await modal().locator(".fetch-unmanaged-team").count()) === 0, "没勾项目仓库里的就不提醒");
    // 勾上归项目仓库管的，才写清之后在 app 里更新会改动项目仓库里的文件。
    await row("locked-tool").click();
    assert((await modal().locator(".fetch-unmanaged-team").textContent()).includes("「locked-tool」归项目仓库管。之后在 app 里更新它，会改动项目仓库里的文件"), "勾上一项时提醒里说「它」");
    await row("lint-rules").click();
    assert((await modal().locator(".fetch-unmanaged-team").textContent()).includes("「lint-rules」「locked-tool」归项目仓库管。之后在 app 里更新它们"), "勾上几项时说「它们」");
    await row("lint-rules").click();
    await page.screenshot({ path: path.join(SHOTS, "register-project-fetch.png") });
    // 记下的来源已经不在了：确认前清空来源，作为没有来源的技能入库。
    await row("locked-tool").locator(".fetch-unmanaged-edit").click();
    assert.equal(await modal().locator(".fetch-unmanaged-source").inputValue(), path.join(fs.realpathSync(projC), "vendor", "missing"), "来源链接预填记下的来源");
    await modal().locator(".fetch-unmanaged-source").fill("");
    assert.equal(await row("locked-tool").locator(".fetch-unmanaged-changed").textContent(), "改为没有来源", "清空后写明没有来源");
    await btn(modal(), "入库 2 个").click();
    await toast("已入库 2 个技能");
    await settle();
    await card("proj-notes").waitFor();
    await openDrawer("locked-tool");
    assert((await drawer().locator(".meta").textContent()).includes("没有来源"), "清空来源后是没有来源的技能");
    await closeDrawer();
    assert((await card("team-rules").count()) === 0, "没勾的不入库");
    assert.deepStrictEqual(disk(), before, "入库时磁盘上什么都没动");
    // 没入库的 lint-rules 记在 skills-lock.json 里:卡片上和确认列表一样标出来，所以作用域里不提「还有几个没入库」。
    await selectScope("Claude Code", "team-proj");
    const lint = page.locator(".unmanaged-card", { has: page.locator(".unmanaged-name", { hasText: /^lint-rules$/ }) });
    await lint.waitFor();
    assert((await lint.locator('[data-slot="badge"]', { hasText: "记在 skills-lock.json 里" }).count()) === 1, "卡片上标出记在 skills-lock.json 里");
    assert((await page.locator(".unmanaged-hint").count()) === 0, "归项目仓库管的不算进还有几个没入库");
    await clearScope();
  } finally {
    for (let i = 0; i < 3; i++) await page.keyboard.press("Escape");
    for (const n of ["proj-notes", "locked-tool"]) {
      if ((await card(n).count()) === 0) continue;
      await openDrawer(n);
      await btn(drawer(), "移除技能").click();
      await btn(modal(), "移除").click();
      await toast("已移除");
      await settle();
    }
    const projRow = page.locator(".a-right .nav-item.proj", { hasText: "team-proj" });
    if (await projRow.count()) {
      await projRow.hover();
      await projRow.locator(".row-actions button[title='注销项目']").click();
      await btn(modal(), "注销").click();
      await toast("已注销项目");
      await settle();
    }
    fs.rmSync(projC, { recursive: true, force: true });
  }
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

// ============ 未纳管技能 ============
await step("未纳管技能：作用域里列出不是库装的技能(符号链接、项目仓库里的),只能在 Finder 中显示", async () => {
  // npx skills 的布局：真实文件夹在 ~/.agents/skills(Codex 全局),Claude Code 全局里是指过去的符号链接。
  const real = path.join(HOME, ".agents", "skills", "npx-tool");
  fs.mkdirSync(real, { recursive: true });
  fs.writeFileSync(path.join(real, "SKILL.md"), '---\nname: npx-tool\ndescription: "installed by npx skills"\n---\n');
  const link = path.join(claudeGlobal, "npx-tool");
  fs.symlinkSync(path.join("..", "..", ".agents", "skills", "npx-tool"), link);
  // proj-b 是 git 仓库：team-skill 已提交，my-draft 只在工作区。
  const projB = path.join(HOME, "code", "proj-b");
  const git = (...args) => execFileSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@example.com", "-c", "commit.gpgsign=false", "-C", projB, ...args]);
  for (const [name, description] of [["team-skill", "committed by the team"], ["my-draft", "not committed"]]) {
    fs.mkdirSync(path.join(projB, ".claude", "skills", name), { recursive: true });
    fs.writeFileSync(path.join(projB, ".claude", "skills", name, "SKILL.md"), `---\nname: ${name}\ndescription: "${description}"\n---\n`);
  }
  git("init", "-q", "-b", "main");
  git("add", ".claude/skills/team-skill");
  git("commit", "-qm", "team");
  await page.locator(".titlebar button[title^='刷新']").click();
  await settle();

  const unmanaged = (name) => page.locator(".unmanaged-card", { has: page.locator(".unmanaged-name", { hasText: new RegExp(`^${name}$`) }) });
  const revealArgs = [];
  const revealID = Number(appMethodID("RevealInFinder"));
  const onRequest = (r) => {
    if (r.method() !== "POST" || !r.url().includes("/wails/runtime")) return;
    const body = JSON.parse(r.postData() ?? "{}");
    if (body.args?.methodID === revealID) revealArgs.push(body.args.args[0]);
  };
  page.on("request", onRequest);
  try {
    await selectScope("Claude Code");
    const npx = unmanaged("npx-tool");
    await npx.waitFor();
    assert((await card("alpha").count()) === 1, "库装的 alpha 照常列出");
    assert((await npx.locator('[data-slot="badge"]', { hasText: "未纳管" }).count()) === 1, "应标未纳管");
    // 指向的是解析后的真实路径;临时 HOME 在 /tmp 下(macOS 上是 /private/tmp 的符号链接),所以不按 ~ 缩写比较。
    const foot = await npx.locator(".a-card-foot").textContent();
    assert(foot.startsWith("符号链接 → ") && foot.includes(`${fs.realpathSync(real)}`), `应写出符号链接指向哪里:${foot}`);
    assert((await npx.getByRole("button").allTextContents()).join() === "在 Finder 中显示,入库", "只有在 Finder 中显示与入库，没有删除");
    assert((await page.locator(".a-scope-sum .stat", { hasText: "未纳管" }).textContent()).includes("1"), "摘要应数出 1 个未纳管");
    await page.screenshot({ path: path.join(SHOTS, "unmanaged-claude-global.png") });
    await btn(npx, "在 Finder 中显示").click();
    await page.waitForTimeout(300);
    assert.deepStrictEqual(revealArgs, [link], "在 Finder 中显示的是符号链接本身");

    await selectScope("Codex");
    await unmanaged("npx-tool").waitFor();
    assert((await unmanaged("npx-tool").locator(".a-card-foot", { hasText: "符号链接" }).count()) === 0, "真实文件夹不标符号链接");

    await selectScope("Claude Code", "proj-b");
    await unmanaged("team-skill").waitFor();
    assert((await unmanaged("team-skill").locator('[data-slot="badge"]', { hasText: "在项目仓库里" }).count()) === 1, "已提交的应标在项目仓库里");
    assert((await unmanaged("my-draft").locator('[data-slot="badge"]', { hasText: "在项目仓库里" }).count()) === 0, "没提交的不标");
    await page.screenshot({ path: path.join(SHOTS, "unmanaged-project.png") });

    // 只看所有技能时列出;按集合、组筛选时不列。搜索同样作用于未纳管技能。
    await page.getByPlaceholder("搜索技能").fill("draft");
    await page.waitForTimeout(200);
    assert((await page.locator(".unmanaged-card").count()) === 1, "搜索应只剩 my-draft");
    await page.getByPlaceholder("搜索技能").fill("");
  } finally {
    page.off("request", onRequest);
    await selectScope("Claude Code", "proj-a");
    // 选中 proj-b 的作用域时右栏展开了它，收回去，后面的拖放步骤要从折叠的 proj-b 开始。
    const projRow = page.locator(".a-right .nav-item.proj", { hasText: "proj-b" });
    if ((await projRow.getAttribute("aria-expanded")) === "true") await projRow.click();
  }
  assert(fs.lstatSync(link).isSymbolicLink() && fs.existsSync(path.join(projB, ".claude", "skills", "my-draft", "SKILL.md")), "磁盘上什么都没动");
});

await step("未纳管技能入库：磁盘不动，成为没有来源的技能;内容一致的只补记安装;项目仓库里的也能入库", async () => {
  const real = path.join(HOME, ".agents", "skills", "npx-tool");
  const link = path.join(claudeGlobal, "npx-tool");
  const team = path.join(HOME, "code", "proj-b", ".claude", "skills", "team-skill", "SKILL.md");
  const before = [fs.readlinkSync(link), fs.readFileSync(path.join(real, "SKILL.md"), "utf8"), fs.readFileSync(team, "utf8")];
  const unmanaged = (name) => page.locator(".unmanaged-card", { has: page.locator(".unmanaged-name", { hasText: new RegExp(`^${name}$`) }) });
  try {
    await selectScope("Claude Code");
    await btn(unmanaged("npx-tool"), "入库").click();
    await modal().locator('[data-slot="dialog-title"]', { hasText: "入库「npx-tool」" }).waitFor();
    const text = await modal().textContent();
    assert(text.includes("入库就是把它交给 app 管") && text.includes("之后如果还用命令行工具更新它，app 只能把它报为被手动改过"), `确认时应写清由谁管:${text}`);
    assert(!text.includes("项目仓库"), "不在项目仓库里的不提仓库");
    await btn(modal(), "入库").click();
    await toast("已入库「npx-tool」");
    await settle();
    await card("npx-tool").waitFor();
    assert((await unmanaged("npx-tool").count()) === 0, "入库后不再是未纳管技能");
    assert((await card("npx-tool").textContent()).includes("没有来源"), "卡片上应写没有来源");
    await openDrawer("npx-tool");
    const meta = await drawer().locator(".meta").textContent();
    assert(meta.includes("没有来源") && !meta.includes("检查更新") && !meta.includes("来源版本"), `没有来源的技能不检查更新:${meta}`);
    assert((await btn(drawer(), "更新").count()) === 0 && (await btn(drawer(), "重新读取").count()) === 0, "没有更新与重新读取");
    const row = drawer().locator(".scope-table tbody tr", { hasText: "Claude Code" });
    assert((await row.locator('[data-slot="badge"]', { hasText: "最新" }).count()) === 1, "入库的这一处巡检为最新");
    await closeDrawer();

    // Codex 全局里是链接指向的真实文件夹，内容一样:只补记安装，不新建技能。
    await selectScope("Codex");
    await btn(unmanaged("npx-tool"), "入库").click();
    await btn(modal(), "入库").click();
    await toast("「npx-tool」和库里「npx-tool」的一个版本一致，已记为装在 Codex · 全局");
    await settle();
    assert((await card("npx-tool").count()) === 1 && (await unmanaged("npx-tool").count()) === 0, "Codex 里应是同一个技能");

    await selectScope("Claude Code", "proj-b");
    await btn(unmanaged("team-skill"), "入库").click();
    await modal().locator('[data-slot="dialog-title"]', { hasText: "入库「team-skill」" }).waitFor();
    assert((await modal().textContent()).includes("之后在 app 里更新它，会改动项目仓库里的文件"), "项目仓库里的应写清会改动仓库");
    await btn(modal(), "入库").click();
    await toast("已入库「team-skill」");
    await settle();
    await card("team-skill").waitFor();
    // 移出库(保留文件夹)后又是未纳管技能;后面的步骤要求 proj-b 里没有 Claude Code 装的技能。
    await openDrawer("team-skill");
    await btn(drawer(), "移除技能").click();
    await modal().locator('[data-slot="choice"]', { hasText: "保留已安装的文件夹" }).click();
    await btn(modal(), "移除").click();
    await toast("已移除");
    await settle();
    await unmanaged("team-skill").waitFor();
  } finally {
    await selectScope("Claude Code", "proj-a");
    const projRow = page.locator(".a-right .nav-item.proj", { hasText: "proj-b" });
    if ((await projRow.getAttribute("aria-expanded")) === "true") await projRow.click();
  }
  const after = [fs.readlinkSync(link), fs.readFileSync(path.join(real, "SKILL.md"), "utf8"), fs.readFileSync(team, "utf8")];
  assert.deepStrictEqual(after, before, "入库时磁盘上什么都没动");
  assert(fs.lstatSync(link).isSymbolicLink(), "符号链接还是符号链接");
});

await step("未纳管技能认出来源：npx skills 来源记录里的来源预填可改，入库进集合;和来源不一样的标出可能有你的修改;读不成时留在框里", async () => {
  // npx skills 从本地目录 team-kit 装了 kit-a(和来源一样)与 kit-b(装完被改过);kit-c 没有记录。
  const kit = path.join(HOME, "sources", "team-kit");
  const doc = (name, body) => `---\nname: ${name}\ndescription: "${name} from team-kit"\n---\n${body}\n`;
  for (const name of ["kit-a", "kit-b"]) {
    fs.mkdirSync(path.join(kit, name), { recursive: true });
    fs.writeFileSync(path.join(kit, name, "SKILL.md"), doc(name, "v1"));
  }
  const folders = { "kit-a": doc("kit-a", "v1"), "kit-b": doc("kit-b", "edited"), "kit-c": doc("kit-c", "mine") };
  for (const [name, content] of Object.entries(folders)) {
    fs.mkdirSync(path.join(claudeGlobal, name), { recursive: true });
    fs.writeFileSync(path.join(claudeGlobal, name, "SKILL.md"), content);
  }
  const records = { version: 3, skills: Object.fromEntries(["kit-a", "kit-b"].map((name) => [name, { source: kit, sourceType: "local", sourceUrl: kit, skillFolderHash: "" }])) };
  fs.writeFileSync(path.join(HOME, ".agents", ".skill-lock.json"), JSON.stringify(records));
  await page.locator(".titlebar button[title^='刷新']").click();
  await settle();
  const unmanaged = (name) => page.locator(".unmanaged-card", { has: page.locator(".unmanaged-name", { hasText: new RegExp(`^${name}$`) }) });
  try {
    await selectScope("Claude Code");
    await unmanaged("kit-a").waitFor();
    assert.equal(await unmanaged("kit-a").locator(".unmanaged-source-label").textContent(), `来自 ${kit}`, "卡片上写出认出的来源");
    // 卡片底部放不下时换行，「不是库装的」和来源都要能看全。
    const clipped = (el) => el.evaluate((e) => e.scrollWidth > e.clientWidth + 1);
    assert(!(await clipped(unmanaged("kit-a").locator(".unmanaged-status"))), "「不是库装的」没被截断");
    assert(!(await clipped(unmanaged("kit-a").locator(".unmanaged-source-label"))), "来源没被截断");
    await page.screenshot({ path: path.join(SHOTS, "unmanaged-source-label.png") });
    assert((await unmanaged("kit-c").locator(".unmanaged-source-label").count()) === 0, "认不出的不写来源");

    await btn(unmanaged("kit-a"), "入库").click();
    await modal().locator('[data-slot="dialog-title"]', { hasText: "入库「kit-a」" }).waitFor();
    assert.equal(await modal().locator(".unmanaged-source").inputValue(), kit, "来源链接预填来源记录里的来源");
    assert((await modal().locator(".unmanaged-source-note").textContent()).startsWith(`以后跟着 ${kit} 检查更新`), "确认时写清以后跟着谁");
    await btn(modal(), "入库").click();
    await toast(`已入库「kit-a」，以后跟着 ${kit} 检查更新`);
    await settle();
    await card("kit-a").waitFor();
    assert(!(await card("kit-a").textContent()).includes("没有来源"), "进了来源的集合");

    // 和来源里的不一样，又说不清是不是改过:照样进集合，版本历史里标出可能有你的修改。
    await btn(unmanaged("kit-b"), "入库").click();
    await btn(modal(), "入库").click();
    await toast(`已入库「kit-b」，以后跟着 ${kit} 检查更新`);
    await settle();
    await openDrawer("kit-b");
    assert((await drawer().locator(".versions").textContent()).includes("入库时和来源里的不一样，可能有你的修改"), "版本历史标出可能有你的修改");
    await closeDrawer();

    // 用户给的来源读不成：留在框里说明原因;清空来源链接后作为没有来源的技能入库。
    await btn(unmanaged("kit-c"), "入库").click();
    await modal().locator(".unmanaged-source").fill(path.join(HOME, "sources", "missing"));
    await btn(modal(), "入库").click();
    await modal().locator(".unmanaged-error").waitFor();
    assert((await modal().locator(".unmanaged-error").textContent()).includes("清空来源链接"), "读不成时告诉用户能清空来源");
    await modal().locator(".unmanaged-source").fill("");
    assert((await modal().locator(".unmanaged-source-note").textContent()).startsWith("没有来源"), "清空后写明没有来源");
    await btn(modal(), "入库").click();
    await toast("已入库「kit-c」");
    await settle();
    assert((await card("kit-c").textContent()).includes("没有来源"), "清空来源后是没有来源的技能");
    assert.equal(fs.readFileSync(path.join(claudeGlobal, "kit-b", "SKILL.md"), "utf8"), folders["kit-b"], "入库时磁盘上什么都没动");
  } finally {
    // 后面的步骤不认这几个技能：连同文件夹移除，来源记录也删掉。
    for (const name of ["kit-a", "kit-b", "kit-c"]) {
      if ((await card(name).count()) === 0) continue;
      await openDrawer(name);
      await btn(drawer(), "移除技能").click();
      await btn(modal(), "移除").click();
      await toast("已移除");
      await settle();
    }
    for (const name of ["kit-a", "kit-b", "kit-c"]) fs.rmSync(path.join(claudeGlobal, name), { recursive: true, force: true });
    fs.rmSync(path.join(HOME, ".agents", ".skill-lock.json"), { force: true });
    await page.locator(".titlebar button[title^='刷新']").click();
    await settle();
    await selectScope("Claude Code", "proj-a");
  }
});

// ============ 同名技能 ============
await step("同名技能：全局和项目里同名的两处都标出来，并说明在这个 Agent 里会怎样", async () => {
  // 全局手动拷了一个 gamma(库把 gamma 装在了 proj-a);proj-a 里有一个自己写的 alpha(库把 alpha 装在了全局);
  // Codex 全局里有上一步入库的 npx-tool,proj-a 里有一个文件夹名不同、SKILL.md 里 name 也是 npx-tool 的(Agent 按 name 认同名)。
  const projA = fs.realpathSync(path.join(HOME, "code", "proj-a"));
  const made = [
    [path.join(claudeGlobal, "gamma"), "gamma", "hand copied"],
    [path.join(projA, ".claude", "skills", "alpha"), "alpha", "my alpha"],
    [path.join(projA, ".agents", "skills", "vendored-npx"), "npx-tool", "vendored"],
  ];
  for (const [folder, name, description] of made) {
    assert(!fs.existsSync(folder), `${folder} 不应已存在`);
    fs.mkdirSync(folder, { recursive: true });
    fs.writeFileSync(path.join(folder, "SKILL.md"), `---\nname: ${name}\ndescription: "${description}"\n---\n`);
  }
  const unmanaged = (name) => page.locator(".unmanaged-card", { has: page.locator(".unmanaged-name", { hasText: new RegExp(`^${name}$`) }) });
  const note = async (item) => {
    assert((await item.locator(".same-name-badge").count()) === 1, "应标同名");
    return (await item.locator(".same-name-note").textContent()).trim();
  };
  try {
    await page.locator(".titlebar button[title^='刷新']").click();
    await settle();

    // 排查「gamma 在这个项目里表现得不像我装的版本」:在项目的作用域里直接看到全局那个生效。
    await selectScope("Claude Code", "proj-a");
    await card("gamma").locator(".same-name-note").waitFor();
    assert.equal(await note(card("gamma")), "全局也有 gamma(未纳管)，Claude Code 用全局的那个，这里的不会生效");
    assert.equal(await note(unmanaged("alpha")), "全局也有 alpha(库装的)，Claude Code 用全局的那个，这里的不会生效");
    assert((await card("beta").locator(".same-name-badge").count()) === 0, "只在一处的不标");
    await page.screenshot({ path: path.join(SHOTS, "same-name-claude-project.png") });

    await selectScope("Claude Code");
    await card("alpha").locator(".same-name-note").waitFor();
    assert.equal(await note(card("alpha")), "项目「proj-a」里也有 alpha，Claude Code 在那里用全局的这个");
    assert.equal(await note(unmanaged("gamma")), "项目「proj-a」里也有 gamma，Claude Code 在那里用全局的这个");
    assert((await card("npx-tool").locator(".same-name-badge").count()) === 0, "Claude Code 的项目里没有 npx-tool,不标");
    await page.screenshot({ path: path.join(SHOTS, "same-name-claude-global.png") });

    await selectScope("Codex", "proj-a");
    await unmanaged("vendored-npx").locator(".same-name-note").waitFor();
    assert.equal(await note(unmanaged("vendored-npx")), "全局也有 npx-tool(库装的)，Codex 里两份都会出现");
    await selectScope("Codex");
    assert.equal(await note(card("npx-tool")), "项目「proj-a」里也有 npx-tool，Codex 在那里两份都会出现");
  } finally {
    for (const [folder] of made) fs.rmSync(folder, { recursive: true, force: true });
    await page.locator(".titlebar button[title^='刷新']").click();
    await settle();
    await selectScope("Claude Code", "proj-a");
  }
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
  assert((await card("delta").locator('[data-slot="badge"][data-tone="warn"]', { hasText: "有新版本" }).count()) === 1, "delta 应标有新版本");
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
  await record.first().getByText(/^从 [0-9a-f]{7} 换到 [0-9a-f]{7}$/).waitFor();
  await btn(record.first(), "改了什么").click();
  await record.first().locator('[data-diff="added"]', { hasText: "scripts/setup.sh" }).waitFor();
  await record.first().getByText(/^从 [0-9a-f]{7} · .+ 换到 /).waitFor();
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
  // 目录没变、装出去的地方却没换上(这里是文件夹不在了)时，再点一次重新读取把它补上，改过的地方照样不动。
  fs.rmSync(codexGlobal, { recursive: true });
  await btn(drawer(), "重新读取").click();
  await toast("已重新读取「gamma」 · 内容没有变化 · 换到了 1 个地方 · 1 个地方你改过，没动");
  await settle();
  assert(fs.readFileSync(path.join(codexGlobal, "SKILL.md"), "utf8").includes("gamma v3"), "Codex 全局的 gamma 应重新装回 v3");
  assert(projSkill("gamma").includes("edited by hand"), "改过的地方仍保持原样");
  // 上次重新读取没换上的地方，补装算进那次更新：回滚那次更新时它也一起装回旧版。
  fs.writeFileSync(`${SK}/gamma/SKILL.md`, '---\nname: gamma\ndescription: "gamma v4"\n---\n\n# gamma v4\n');
  const codexSkills = path.dirname(codexGlobal);
  fs.chmodSync(codexSkills, 0o555);
  try {
    await btn(drawer(), "重新读取").click();
    await modal().locator('[data-slot="dialog-title"]', { hasText: "有地方没换上" }).waitFor();
  } finally {
    fs.chmodSync(codexSkills, 0o755);
  }
  await btn(modal(), "知道了").click();
  await settle();
  assert(fs.readFileSync(path.join(codexGlobal, "SKILL.md"), "utf8").includes("gamma v3"), "没换上的地方保持 v3");
  await btn(drawer(), "重新读取").click();
  await toast("已重新读取「gamma」 · 内容没有变化 · 换到了 1 个地方 · 1 个地方你改过，没动");
  await settle();
  assert(fs.readFileSync(path.join(codexGlobal, "SKILL.md"), "utf8").includes("gamma v4"), "补装后 Codex 全局的 gamma 应为 v4");
  await btn(drawer(), "更新历史").click();
  const latest = modal().locator(".update-record", { has: page.locator('[data-slot="badge"]', { hasText: "更新" }) }).first();
  await btn(latest, "回滚").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "回滚这次更新" }).waitFor();
  await btn(modal(), "回滚").click();
  await toast("装回了 1 个地方");
  await settle();
  await page.keyboard.press("Escape");
  assert(fs.readFileSync(path.join(codexGlobal, "SKILL.md"), "utf8").includes("gamma v3"), "回滚后补装过的地方也回到 v3");
  await closeDrawer();
});

// 用目录里的修改更新：上一步在 proj-a 里改过 gamma,用它更新库里的 gamma,连带换到 Codex 全局;之后回滚，库和 Codex 全局回到 v3,
// 取内容的 proj-a 不动。
await step("详情：用这里的修改更新，连带换到其他地方，能回滚", async () => {
  await openDrawer("gamma");
  const codexGlobal = (await drawer().locator(".scope-table tbody tr", { hasText: "Codex · 全局" }).locator(".font-mono").textContent()).replace(/^~/, HOME);
  const versions = await drawer().locator(".versions li").count();
  const row = drawer().locator(".scope-table tbody tr", { hasText: "proj-a" });
  assert((await row.locator('[data-slot="badge"]', { hasText: "被手动改过" }).count()) === 1, "proj-a 里的 gamma 应标被手动改过");
  assert((await btn(drawer().locator(".scope-table tbody tr", { hasText: "Codex · 全局" }), "用这里的修改更新").count()) === 0, "没改过的地方不提供");
  await btn(row, "用这里的修改更新").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "用 Claude Code · proj-a 里的修改更新「gamma」" }).waitFor();
  await modal().locator('[data-diff="changed"]', { hasText: "SKILL.md" }).waitFor({ timeout: 15000 });
  await modal().locator('[data-slot="file-diff"] [data-diff="add"]', { hasText: "edited by hand" }).waitFor();
  const place = modal().locator(".update-place", { hasText: "Codex · 全局" });
  assert(await place.locator("input[type=checkbox]").isChecked(), "其他地方默认勾上");
  assert((await modal().locator(".update-place", { hasText: "proj-a" }).count()) === 0, "取内容的那一处不列");
  await btn(modal(), "更新").click();
  await toast("已用 Claude Code · proj-a 里的修改更新「gamma」 · 换到了 1 个地方");
  assert((await page.locator('[data-slot="toast"]').getByRole("button", { name: "回滚" }).count()) === 1, "完成提示应带回滚");
  await settle();
  assert(fs.readFileSync(path.join(codexGlobal, "SKILL.md"), "utf8").includes("edited by hand"), "Codex 全局的 gamma 应换成这份修改");
  assert(projSkill("gamma").includes("edited by hand"), "取内容的地方不动");
  await row.locator('[data-slot="badge"]', { hasText: "最新" }).waitFor();
  assert((await drawer().locator(".versions li").count()) === versions + 1, "应多一个版本");
  await drawer().locator(".versions li").first().getByText(/^基于版本 \d+ 的修改$/).waitFor();
  await btn(drawer(), "更新历史").click();
  const record = modal().locator(".update-record", { hasText: "用 Claude Code · proj-a 里的修改更新" }).first();
  await record.waitFor();
  await btn(record, "回滚").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "回滚这次更新" }).waitFor();
  await btn(modal(), "回滚").click();
  await toast("装回了 1 个地方");
  await settle();
  await page.keyboard.press("Escape");
  assert(fs.readFileSync(path.join(codexGlobal, "SKILL.md"), "utf8").includes("gamma v3"), "回滚后 Codex 全局回到 v3");
  assert(projSkill("gamma").includes("edited by hand"), "回滚不动取内容的地方");
  await closeDrawer();
});

// 改了作者的技能，照样跟着作者走：用 proj-a 里的修改更新库里的 delta 后，作者没再改时检查更新不报有更新;作者发了新版时照常计入角标，
// 更新计划里标明会换掉修改、默认不勾;勾上更新后能回滚回这份修改，也可以跳过，直到作者再改。
await step("改了作者的技能：只看作者那边有没有新版，更新会换掉修改、默认不勾，能回滚、能跳过", async () => {
  const projDelta = path.join(`${HOME}/code/proj-a`, String(fs.readdirSync(`${HOME}/code/proj-a`, { recursive: true }).find((f) => String(f).endsWith("delta/SKILL.md"))));
  fs.writeFileSync(projDelta, projSkill("delta").replace("# delta v2", "# delta edited by hand"));
  await page.locator(".titlebar button[title^='刷新']").click();
  await settle();
  await openDrawer("delta");
  await btn(drawer().locator(".scope-table tbody tr", { hasText: "proj-a" }), "用这里的修改更新").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "用 Claude Code · proj-a 里的修改更新「delta」" }).waitFor();
  await modal().locator('[data-diff="changed"]', { hasText: "SKILL.md" }).waitFor({ timeout: 15000 });
  await btn(modal(), "更新").click();
  await toast("已用 Claude Code · proj-a 里的修改更新「delta」");
  await settle();
  await closeDrawer();

  await checkUpdates("没有要处理的更新");
  assert((await updatesNav().count()) === 0, "作者没再改时不显示有更新");

  rewriteZip("delta v3");
  await checkUpdates("1 个技能有新版本");
  assert((await updatesNav().locator('[data-slot="count"]').textContent()) === "1", "作者发了新版时计入角标");
  await updatesNav().click();
  const row = page.locator(".update-row", { hasText: "delta" });
  // 要处理的改动都会换掉你的修改：这一行和卡片直接写出来，「更新」不直接执行，打开更新计划。
  assert((await row.textContent()).includes("有新版本，更新会换掉你的修改"), `列表这一行写出会换掉你的修改:${await row.textContent()}`);
  assert((await row.locator('[data-slot="button"][data-variant="primary"]').count()) === 0, "没有直接执行的主按钮");
  assert((await card("delta").locator(".update-edited-note").textContent()).includes("更新会换掉你的修改"), "卡片上写出会换掉你的修改");
  await page.screenshot({ path: path.join(SHOTS, "update-row-edited.png") });
  await btn(row, "更新…").click();
  const delta = planRow("update-skill", "delta");
  await delta.locator(".update-edited", { hasText: "更新会换掉你的修改" }).waitFor({ timeout: 15000 });
  assert(!(await delta.locator("input[type=checkbox]").isChecked()), "会换掉修改的技能默认不勾");
  assert(await btn(modal(), "更新 0 个").isDisabled(), "没勾时不能更新");
  await delta.locator("input[type=checkbox]").check();
  await btn(modal(), "更新 1 个").click();
  await toast("已更新 delta · 1 个技能换了新版本 · 换到了 1 个地方");
  await settle();
  assert(projSkill("delta").includes("delta v3"), "勾上更新后项目里的 delta 换成来源的新版本");

  await openDrawer("delta");
  await btn(drawer(), "更新历史").click();
  const record = modal().locator(".update-record", { has: page.locator('[data-slot="badge"]', { hasText: "更新" }) }).first();
  await btn(record, "回滚").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "回滚这次更新" }).waitFor();
  await btn(modal(), "回滚").click();
  await toast("装回了 1 个地方");
  await settle();
  await page.keyboard.press("Escape");
  assert(projSkill("delta").includes("delta edited by hand"), "回滚回这份修改");
  assert((await updatesNav().count()) === 0, "回滚掉的改动不计入角标");
  await closeDrawer();

  rewriteZip("delta v4");
  await checkUpdates("1 个技能有新版本");
  await updatesNav().click();
  await btn(page.locator(".update-row", { hasText: "delta" }), "更新…").click();
  await planRow("update-skill", "delta").locator(".update-edited").waitFor({ timeout: 15000 });
  await btn(planRow("update-skill", "delta"), "跳过").click();
  await toast("已跳过「delta」这次的改动");
  await page.keyboard.press("Escape");
  await settle();
  assert((await updatesNav().count()) === 0, "跳过后角标消失");
  await checkUpdates("没有要处理的更新");
  assert(projSkill("delta").includes("delta edited by hand"), "跳过后修改还在");
  rewriteZip("delta v5");
  await checkUpdates("1 个技能有新版本");
  // 后面的步骤按角标计数，这次的改动也跳过。
  await updatesNav().click();
  await btn(page.locator(".update-row", { hasText: "delta" }), "更新…").click();
  await btn(planRow("update-skill", "delta"), "跳过").click();
  await toast("已跳过「delta」这次的改动");
  await page.keyboard.press("Escape");
  await settle();
});

await step("从技能发起更新：只勾这一个、其余折叠;之后更新集合重新对齐", async () => {
  const pairURL = ZIP_URL.replace(/delta\.zip$/, "pair.zip");
  await openFetch();
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
  await modal().locator('[data-slot="notice"][data-tone="warn"]', { hasText: "只更新 eta 后,pair.zip 里的技能会不在同一个来源版本上" }).waitFor();
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
  // 有变化只算来源里有新内容的(跳过了的 kappa 也算),来源里已删除的 mu 不算。
  await page.locator(".a-scope-sum").getByText("· 1 个有变化", { exact: false }).waitFor();
  await btn(page.locator(".a-scope-sum"), "看来源里的其他技能").click();
  await modal().locator('[data-slot="list-row"]', { hasText: "nu" }).waitFor({ timeout: 20000 });
  await page.keyboard.press("Escape");
  await page.locator(".a-left .nav-item", { hasText: "所有技能" }).click();
});

// ============ 拖放 ============
// npx skills 从一个 Git 仓库装了三个技能:lint 和来源一样;tdd 装的是来源现在这一版、之后被改过(记录里的哈希等于来源的);
// grill-me 被改过、记录里没有哈希(说不清)。Git 来源的名字与来源版本照常显示;来源版本未知的如实写出;
// tdd 第一次检查更新没有更新，作者再改才报，和 grill-me 一样写明更新会换掉你的修改。
await step("Git 来源与 npx skills 记录：来源名字与来源版本、来源版本未知的如实写出、装的是来源现在这一版又改过的不报有更新", async () => {
  const work = path.join(HOME, "sources", "matt-skills");
  const bare = path.join(HOME, "sources", "matt-skills.git");
  const url = "https://git.pm.test/matt/skills.git";
  const names = ["grill-me", "tdd", "lint"];
  const doc = (name, body) => `---\nname: ${name}\ndescription: "${name} from matt"\n---\n\n${body}\n`;
  const gitIn = (dir, ...args) => execFileSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@example.com", "-c", "commit.gpgsign=false", "-C", dir, ...args]).toString().trim();
  for (const name of names) {
    fs.mkdirSync(path.join(work, "skills", name), { recursive: true });
    fs.writeFileSync(path.join(work, "skills", name, "SKILL.md"), doc(name, "v1"));
  }
  gitIn(work, "init", "-q", "-b", "main");
  gitIn(work, "add", "-A");
  gitIn(work, "commit", "-qm", "v1");
  execFileSync("git", ["clone", "-q", "--bare", work, bare]);
  execFileSync("git", ["config", "--file", path.join(HOME, ".gitconfig"), `url.file://${bare}.insteadOf`, url]);
  const head = () => gitIn(work, "rev-parse", "HEAD");
  const tree = (name) => gitIn(work, "rev-parse", `HEAD:skills/${name}`);
  const codex = path.join(HOME, ".agents", "skills");
  const disk = { "grill-me": doc("grill-me", "edited"), tdd: doc("tdd", "edited"), lint: doc("lint", "v1") };
  for (const [name, content] of Object.entries(disk)) {
    fs.mkdirSync(path.join(codex, name), { recursive: true });
    fs.writeFileSync(path.join(codex, name, "SKILL.md"), content);
  }
  const record = (hash) => ({ source: url, sourceType: "git", sourceUrl: url, skillFolderHash: hash });
  fs.writeFileSync(
    path.join(HOME, ".agents", ".skill-lock.json"),
    JSON.stringify({ version: 3, skills: { "grill-me": record(""), tdd: record(tree("tdd")), lint: record(tree("lint")) } }),
  );
  const unmanaged = (name) => page.locator(".unmanaged-card", { has: page.locator(".unmanaged-name", { hasText: new RegExp(`^${name}$`) }) });
  const label = "git.pm.test/matt/skills";
  try {
    await page.locator(".titlebar button[title^='刷新']").click();
    await settle();
    await selectScope("Codex");
    for (const name of names) {
      await btn(unmanaged(name), "入库").click();
      await btn(modal(), "入库").click();
      await toast(`已入库「${name}」`);
      await settle();
    }
    await clearScope();
    await settle();

    // Git 来源：左栏集合、卡片和详情都写出 host/仓库路径，详情里的来源版本和 GitHub 一样写分支与 commit。
    const nav = page.locator(".a-left .nav-item", { hasText: label });
    assert.equal(await nav.count(), 1, "左栏集合写出 Git 仓库");
    assert.equal(await page.locator(".a-left .nav-item", { hasText: "未知来源" }).count(), 0, "没有未知来源");
    await openDrawer("lint");
    const meta = drawer().locator(".meta");
    assert((await meta.textContent()).includes(label), `详情的集合写出 Git 仓库:${await meta.textContent()}`);
    assert((await meta.textContent()).includes(`main · ${head().slice(0, 12)}`), `来源版本写分支与 commit:${await meta.textContent()}`);
    await openDrawer("grill-me");
    assert((await drawer().locator(".meta").textContent()).includes("main · 来源版本未知"), "来源版本未知时如实写出");
    await closeDrawer();

    // tdd 装的是来源现在这一版又被改过：第一次检查更新只有 grill-me 有新版本。
    await checkUpdates("1 个技能有新版本");
    await updatesNav().click();
    const row = page.locator(".update-row", { hasText: label });
    const facts = await row.textContent();
    assert(facts.includes("grill-me 的来源版本未知") && facts.includes(`其余 2 个在 ${head().slice(0, 7)}`), `列表写出哪些技能的来源版本未知:${facts}`);
    assert(!facts.includes(`${head().slice(0, 7)} → ${head().slice(0, 7)}`), `不写同一个来源版本到它自己:${facts}`);
    assert(facts.includes("grill-me 有变化，更新会换掉你的修改") && !facts.includes("tdd"), `只有 grill-me 有变化，写明会换掉你的修改:${facts}`);
    assert((await card("tdd").locator('[data-slot="badge"]', { hasText: "有新版本" }).count()) === 0, "tdd 没有更新");
    await page.screenshot({ path: path.join(SHOTS, "git-npx-updates.png") });
    await btn(row, "更新…").click();
    await planRow("update-skill", "grill-me").locator(".update-edited").waitFor({ timeout: 15000 });
    const hint = await modal().locator('[data-slot="hint"]').first().textContent();
    assert(hint.startsWith(label) && hint.includes("grill-me 的来源版本未知") && !hint.includes("技能都在"), `计划顶部如实写出:${hint}`);
    assert.equal(await modal().locator('[data-slot="notice"]', { hasText: "不在同一个来源版本上" }).count(), 0, "可能有你的修改的默认没勾，不警告不在同一个来源版本上");
    await page.screenshot({ path: path.join(SHOTS, "git-npx-plan.png") });
    await page.keyboard.press("Escape");
    await settle();

    // 作者改了 tdd:照常报有新版本，写明会换掉你的修改。
    fs.writeFileSync(path.join(work, "skills", "tdd", "SKILL.md"), doc("tdd", "v2"));
    gitIn(work, "commit", "-qam", "tdd v2");
    gitIn(work, "push", "-q", bare, "main");
    await checkUpdates("2 个技能有新版本");
    // 更新列表还开着：再点一次"更新"会收起，先回到所有技能。
    await page.locator(".a-left .nav-item", { hasText: "所有技能" }).click();
    await updatesNav().click();
    assert((await page.locator(".update-row", { hasText: label }).textContent()).includes("grill-me、tdd 有变化，更新会换掉你的修改"), "作者再改时 tdd 也报");
    assert((await card("tdd").locator(".update-edited-note").textContent()).includes("更新会换掉你的修改"), "卡片上写明");
  } finally {
    for (let i = 0; i < 3; i++) await page.keyboard.press("Escape");
    await clearScope();
    for (const name of names) {
      if ((await card(name).count()) === 0) continue;
      await openDrawer(name);
      await btn(drawer(), "移除技能").click();
      await btn(modal(), "移除").click();
      await toast("已移除");
      await settle();
    }
    for (const name of names) fs.rmSync(path.join(codex, name), { recursive: true, force: true });
    fs.rmSync(path.join(HOME, ".agents", ".skill-lock.json"), { force: true });
    await page.locator(".titlebar button[title^='刷新']").click();
    await settle();
  }
});

// 库里已有一个没有来源的 review,在另一个作用域入库同名、内容不一样的 review:确认时说出来，入库后两个 review 写出各装在哪里。
await step("库里同名的技能：入库确认时说出来，卡片和详情写出各装在哪里", async () => {
  const claude = path.join(HOME, ".claude", "skills", "review");
  const codex = path.join(HOME, ".agents", "skills", "review");
  for (const [dir, body] of [[claude, "review in claude"], [codex, "review in codex"]]) {
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, "SKILL.md"), `---\nname: review\ndescription: "${body}"\n---\n`);
  }
  const unmanaged = page.locator(".unmanaged-card", { has: page.locator(".unmanaged-name", { hasText: /^review$/ }) });
  try {
    await page.locator(".titlebar button[title^='刷新']").click();
    await settle();
    await selectScope("Claude Code");
    await btn(unmanaged, "入库").click();
    assert.equal(await modal().locator(".unmanaged-same-name").count(), 0, "库里还没有同名的");
    await btn(modal(), "入库").click();
    await toast("已入库「review」");
    await settle();

    await selectScope("Codex");
    await btn(unmanaged, "入库").click();
    const note = await modal().locator(".unmanaged-same-name").textContent();
    assert(note.includes("库里已经有叫「review」的技能") && note.includes("Claude Code · 全局"), `确认时说出库里已有同名的、装在哪里:${note}`);
    await page.screenshot({ path: path.join(SHOTS, "same-name-in-library-confirm.png") });
    await btn(modal(), "入库").click();
    await toast("已入库「review」");
    await settle();
    await clearScope();
    await settle();

    const cards = card("review");
    assert.equal(await cards.count(), 2, "同名但内容不同不合并");
    const notes = (await cards.locator(".dup-name-note").allTextContents()).sort();
    assert.deepStrictEqual(notes, ["库里有 2 个「review」;这个装在 Claude Code · 全局", "库里有 2 个「review」;这个装在 Codex · 全局"], `卡片写出各装在哪里:${notes}`);
    await page.screenshot({ path: path.join(SHOTS, "same-name-in-library-cards.png") });
    await cards.first().click();
    await drawer().waitFor();
    const meta = await drawer().locator(".meta").textContent();
    assert(meta.includes("库里还有 1 个「review」") && /装在 (Claude Code|Codex) · 全局/.test(meta), `详情写出另一个装在哪里:${meta}`);
    await closeDrawer();
  } finally {
    for (let i = 0; i < 3; i++) await page.keyboard.press("Escape");
    await clearScope();
    while ((await card("review").count()) > 0) {
      await closeDrawer();
      await card("review").first().click();
      await drawer().waitFor();
      await btn(drawer(), "移除技能").click();
      await btn(modal(), "移除").click();
      await toast("已移除");
      await settle();
    }
    for (const dir of [claude, codex]) fs.rmSync(dir, { recursive: true, force: true });
    await page.locator(".titlebar button[title^='刷新']").click();
    await settle();
  }
});

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
  assert.deepStrictEqual(saved, { auto_check: true, check_interval: "weekly", stay_in_menu_bar: true, dock_badge: false, first_open_done: true });
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

// previewSource 在入库对话框里填来源做入库预览，等到结果页出现;给了 ref 时先等下拉选中它。
async function previewSource(input, { ref = "" } = {}) {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill(input);
  if (ref) await waitRefs(ref);
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 90000 });
}
// expectSource 核对结果页的识别结果，以及限流提醒与 skills.sh 审计只在 GitHub 仓库出现。
async function expectSource(kind, label, ref) {
  const source = modal().locator(".fetch-source");
  assert((await source.locator('[data-slot="badge"]', { hasText: new RegExp(`^${kind}$`) }).count()) === 1, `应识别为${kind}`);
  assert((await source.getByText(label, { exact: true }).count()) === 1, `展示名称应为 ${label}`);
  if (ref) assert((await source.getByText(`分支 ${ref}`).count()) === 1, `应显示分支 ${ref}`);
  const github = kind === "GitHub 仓库";
  const tokenless = !process.env.GITHUB_TOKEN && !process.env.GH_TOKEN;
  assert.equal((await modal().getByText("匿名访问 GitHub API").count()) === 1, github && tokenless, "限流提醒只在没有令牌的 GitHub 仓库显示");
  assert.equal((await modal().getByRole("button", { name: "查看审计" }).count()) > 0, github, "只有 GitHub 仓库能查看审计");
}
await step("[网络] GitHub 仓库：下拉列出分支和 tag,默认分支已选中", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill("mattpocock/skills");
  await waitRefs("main");
  await refsTrigger().click();
  const branches = await refItems("分支");
  assert(branches.length > 1 && branches.includes("main"), `分支组应列出全部分支，实际 ${branches.length} 个`);
  assert((await refItems("Tag")).length > 0, "Tag 组应列出 tag");
  assert.equal(
    await refsMenu().locator('[data-slot="command-item"]', { hasText: "默认分支" }).locator(".font-mono").textContent(),
    "main",
    "默认分支应是 main",
  );
  await page.keyboard.press("Escape");
  await modal().getByRole("button", { name: "取消" }).click();
});

await step("[网络] GitHub tree 链接：下拉选中链接里的分支，目录由技能库解析", async () => {
  await previewSource("github.com/anthropics/skills/tree/main/skills/pdf", { ref: "main" });
  await expectSource("GitHub 仓库", "anthropics/skills", "main");
  assert.deepEqual(await candidateNames(), ["pdf"], "只应列仓库内目录下的技能");
  await page.keyboard.press("Escape");
});

// switchTreeRef 在 tree 链接上改选一个带斜杠的其他分支(分支随仓库变化，运行时从列表里挑),核对请求里只换掉了分支、
// 输入框里的文字没变;那个分支里可能没有链接里的目录，所以结果页与技能库给出的错误都算入库预览走通。
async function switchTreeRef(link, from) {
  await openFetch();
  const input = modal().locator('[data-slot="input"]').first();
  await input.fill(link);
  await waitRefs(from);
  assert.equal(await refsTrigger().getAttribute("disabled"), null, "输入里带了分支时下拉也可选");
  await refsTrigger().click();
  const branches = await refItems("分支");
  await page.keyboard.press("Escape");
  const to = branches.find((b) => b !== from && b.includes("/")) ?? branches.find((b) => b !== from);
  assert(to, "仓库里应有别的分支");
  await chooseRef(to);
  await btn(modal(), "入库预览").click();
  const review = modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" });
  const failure = modal().locator(".fetch-failure");
  await review.or(failure).waitFor({ timeout: 90000 });
  assert.deepEqual(previewRequests.at(-1), { input: link.replace(`/tree/${from}/`, `/tree/${to}/`), ref: to }, `应把链接里的 ${from} 换成 ${to}`);
  if (await failure.count()) {
    console.log(`    ${to} 里没有链接里的目录：`, await failure.textContent());
  } else {
    assert.equal(await modal().locator(".fetch-source").getByText(`分支 ${to}`).count(), 1, `结果页应显示分支 ${to}`);
    await btn(modal(), "返回").click();
  }
  await waitRefs(to);
  assert.equal(await input.inputValue(), link, "输入框里的文字不应改变");
  await page.keyboard.press("Escape");
}
await step("[网络] GitHub tree 链接：改选别的分支，只在请求里换掉链接里的分支", async () => {
  await switchTreeRef("github.com/anthropics/skills/tree/main/skills/pdf", "main");
});
await step("[网络] GitLab tree 链接：改选别的分支，只在请求里换掉链接里的分支", async () => {
  await switchTreeRef("https://gitlab.com/gitlab-org/ai/skills/-/tree/main/skills/adr-create", "main");
});

await step("[网络] GitHub raw SKILL.md 链接识别为直接下载", async () => {
  const link = "https://raw.githubusercontent.com/anthropics/skills/main/skills/pdf/SKILL.md";
  await previewSource(link);
  await expectSource("直接下载", link);
  assert.deepEqual(await candidateNames(), ["pdf"], "候选应为 pdf");
  await page.keyboard.press("Escape");
});

await step("[网络] GitLab tree 链接：识别为 Git 仓库，下拉选中链接里的分支，从链接里拆出仓库内目录", async () => {
  await previewSource("https://gitlab.com/gitlab-org/ai/skills/-/tree/main/skills/adr-create", { ref: "main" });
  await expectSource("Git 仓库", "gitlab.com/gitlab-org/ai/skills", "main");
  assert.deepEqual(await candidateNames(), ["adr-create"], "只应列链接里目录下的技能");
  await page.keyboard.press("Escape");
});

await step("[网络] GitLab raw SKILL.md 链接识别为直接下载", async () => {
  const link = "https://gitlab.com/gitlab-org/ai/skills/-/raw/main/skills/adr-create/SKILL.md";
  await previewSource(link);
  await expectSource("直接下载", link);
  assert.deepEqual(await candidateNames(), ["adr-create"], "候选应为 adr-create");
  await page.keyboard.press("Escape");
});

await step("[网络] GitHub 入库(owner/repo@技能名)", async () => {
  await openFetch();
  await modal().locator('[data-slot="input"]').first().fill("anthropics/skills@pdf");
  await btn(modal(), "入库预览").click();
  await modal().locator('[data-slot="dialog-title"]', { hasText: "入库预览" }).waitFor({ timeout: 90000 });
  assert((await modal().locator(".fetch-source", { hasText: "GitHub 仓库" }).count()) === 1, "应识别为 GitHub 仓库");
  assert((await modal().locator(".fetch-source", { hasText: "anthropics/skills" }).count()) === 1, "展示名称应为 owner/repo");
  assert((await modal().getByRole("button", { name: "查看审计" }).count()) >= 1, "GitHub 仓库应能查看审计");
  if (!process.env.GITHUB_TOKEN && !process.env.GH_TOKEN) {
    await modal().getByText("匿名访问 GitHub API").waitFor({ timeout: 5000 });
  }
  assert((await modal().locator('[data-slot="list-row"]', { hasText: "pdf" }).count()) >= 1, "候选里应有 pdf");
  await btn(modal(), "入库所选").click();
  await toast("已入库");
  await settle();
  assert((await page.locator(".a-left .nav-item", { hasText: "anthropics/skills" }).count()) === 1, "集合名应为 owner/repo");
  await openDrawer("pdf");
  assert((await drawer().locator(".meta dd", { hasText: "anthropics/skills" }).count()) === 1, "详情里集合应为 owner/repo");
  assert((await drawer().getByRole("button", { name: "在 GitHub 查看" }).count()) === 1, "GitHub 来源应有查看链接");
  await closeDrawer();
  await page.locator(".a-left .nav-item", { hasText: "检查更新" }).click();
  await toast("没有要处理的更新");
  await settle();
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
