// 主界面：三栏布局——左栏页面与集合、组，中间是当前页面(所有技能 / 更新 / 作用域),右栏作用域，顶部统一工具栏;
// 点技能行打开右侧详情抽屉;左栏"设置"把三栏换成设置页。界面状态在 lib/view,各栏自己取;
// 写操作经 features/ 里的流程与 lib/ui 的 run 执行，完成后让查询缓存失效重读。
import { useEffect, useRef } from "react";
import { Events } from "@wailsio/runtime";
import { AppService } from "@bindings/cmd/desktop";
import { LeftSidebar } from "@/components/app/left-sidebar";
import { ScopePanel } from "@/components/app/scope-panel";
import { SettingsPage } from "@/features/settings/settings-page";
import { SkillDrawer } from "@/components/app/skill-drawer/skill-drawer";
import { SkillList } from "@/components/app/skill-list/skill-list";
import { Toolbar } from "@/components/app/toolbar";
import { offerFirstOpen } from "@/features/fetch/fetch-unmanaged-all";
import { useAutoTranslate } from "@/features/skill/translate";
import { setDragGhost } from "@/components/app/dnd";
import { invalidateSyncStatus, reloadLibrary, toLibraryError, useLoadedSnapshot, useSnapshotQuery, type LibraryError } from "@/lib/library";
import { messages, useT } from "@/lib/i18n";
import { ui, UIHost } from "@/lib/ui";
import { closeDrawer, closeSettings, reconcile, useView } from "@/lib/view";
import { Notice } from "@/components/ui/notice";
import { bodyClass, centerClass } from "@/components/app/layout";
import { ResizablePane } from "@/components/app/resizable";
import { Button } from "@/components/ui/button";
import "./index.css";

export default function App() {
  // 读一次当前语言的文案：切换语言时 App 随之重渲染，整个界面(包括组件外按模块级语言取文案的地方)跟着换。
  useT();
  return (
    <>
      <Main />
      <UIHost />
    </>
  );
}

function Main() {
  const state = useSnapshotQuery();
  // 重读失败时和第一次读取失败一样显示原因与重试。
  const s = useLoadedSnapshot();
  const left = useView((v) => v.left);
  const right = useView((v) => v.right);
  const drawer = useView((v) => v.drawer);
  const drag = useView((v) => v.drag);
  const settings = useView((v) => v.settings);

  // 开着自动翻译时，把还没有界面语言译文的技能描述翻译过来。
  useAutoTranslate();

  // Go 端自动检查更新后通知重读，窗口关在菜单栏时也一样。
  useEffect(() => Events.On("updates-checked", () => void reloadLibrary()), []);
  // 自动推送之后(成功或失败)重读同步状态，设置页上的上次推送时间与失败原因随之更新。
  useEffect(() => Events.On("sync-pushed", () => void invalidateSyncStatus()), []);

  // 在这台机器上第一次打开 app 时，把用户带到各 Agent 全局目录里不是库装的技能，一次确认全部入库。
  const firstOpen = useRef(false);
  useEffect(() => {
    if (!s || firstOpen.current) return;
    firstOpen.current = true;
    void offerFirstOpen(s.agents);
  }, [s]);

  // 设置里选的 WSL 发行版启动时没能打开：说明界面上暂时是 Windows 本机的全局目录，让用户再试或改回 Windows 本机。
  const distroChecked = useRef(false);
  useEffect(() => {
    if (!s || distroChecked.current) return;
    distroChecked.current = true;
    void offerDistroRetry();
  }, [s]);

  // 每次读到快照(写操作、手动检查更新之后),Dock 角标跟着重算。按读到的时间触发：重读的内容没变时快照还是同一个对象。
  const loadedAt = state.dataUpdatedAt;
  useEffect(() => {
    if (loadedAt) void AppService.RefreshBadge();
  }, [loadedAt]);

  // 快照变了以后校正界面上的选择(筛选指向的集合、组没了，作用域的项目或 Agent 被移除)。
  useEffect(() => {
    if (s) reconcile(s);
  }, [s]);

  const drawerSkill = s && drawer !== null ? s.skills.find((x) => x.id === drawer) : undefined;

  if (s && settings) {
    return (
      <div className="window-backdrop flex h-screen flex-col overflow-hidden">
        <SettingsPage
          onClose={() => {
            // 设置页改的 Agent 显示勾选只存进应用设置，回到主界面时重读一次。
            closeSettings();
            void reloadLibrary();
          }}
        />
      </div>
    );
  }

  return (
    <div className="window-backdrop relative flex h-screen flex-col overflow-hidden">
      <Toolbar />
      <div className={bodyClass} data-dragging={drag?.kind}>
        {state.isPending && <main className={centerClass} />}
        {state.isError && <LoadError error={toLibraryError(state.error)} onRetry={() => void reloadLibrary()} />}
        {s && (
          <>
            {left && (
              <ResizablePane side="left" storageKey="mechbox.left-width" defaultWidth={210} min={180} max={360}>
                <LeftSidebar />
              </ResizablePane>
            )}
            <SkillList />
            {right && (
              <ResizablePane side="right" storageKey="mechbox.right-width" defaultWidth={270} min={220} max={440}>
                <ScopePanel />
              </ResizablePane>
            )}
            {drawerSkill && <SkillDrawer skill={drawerSkill} onClose={closeDrawer} />}
          </>
        )}
      </div>
      <div
        ref={setDragGhost}
        className="fixed -top-[200px] left-0 rounded-full bg-primary px-3 py-[5px] text-xs font-semibold whitespace-nowrap text-white shadow-[0_4px_12px_rgba(0,113,227,0.35)]"
      />
    </div>
  );
}

async function offerDistroRetry() {
  const t = messages().app;
  for (;;) {
    const reason = await AppService.WSLDistroError().catch(() => "");
    if (!reason) return;
    const settings = await AppService.Settings();
    const distro = settings.wsl_distro ?? "";
    const choice = await ui.confirm({
      title: t.distroUnavailable(distro),
      body: <Notice tone="warn">{t.distroUnavailableBody(reason)}</Notice>,
      choices: [
        { value: "retry", label: t.retryDistro, desc: t.retryDistroDesc },
        { value: "windows", label: t.useWindowsHome, desc: t.useWindowsHomeDesc },
      ],
      confirm: t.continue,
    });
    if (!choice) {
      ui.toast(t.distroFallback(distro));
      return;
    }
    if (choice === "windows") {
      await ui.run(() => AppService.SaveSettings({ ...settings, wsl_distro: "" }));
      return;
    }
    try {
      await AppService.RetryWSLDistro();
      ui.toast(t.distroOpened(distro));
      ui.reload();
      return;
    } catch {
      // 仍打不开：再给一次选择，原因换成这次的。
    }
  }
}

function LoadError({ error, onRetry }: { error: LibraryError; onRetry: () => void }) {
  const t = useT().app;
  return (
    <main className={centerClass}>
      <div className="flex flex-col items-center gap-1.5 pt-[22vh] text-muted-foreground">
        <b className="text-sm text-foreground">{t.loadFailed}</b>
        <span className="select-text">{error.message}</span>
        <Button className="mt-1.5" onClick={onRetry}>
          {t.retry}
        </Button>
      </div>
    </main>
  );
}
