// 设置页：替换主界面的三栏，左边是分区目录，右边按分区排开。
// 检查更新与通用是只属于这台机器的应用设置(Go 端存在用户配置目录下，不进技能库),改了立即保存、生效;
// 通用里的全局目录(Windows 上装了 WSL 时才出现)改了以后重读快照，界面换成新的全局目录。
// GitHub 令牌存在数据库里，随推送、拉回走;同步的 Secret Access Key、Session Token 存在系统钥匙串里，其余几项存在这台机器的设置文件里，
// 点保存后交给技能库(见 credentials-form.tsx)。
// Agent 分区见 agent-settings;项目分区复用主界面右栏的登记 / 改位置 / 注销;卸载 Mechbox 见 uninstall-dialog。
import { useEffect, useRef, useState } from "react";
import { AppService, type Settings } from "@bindings/cmd/desktop";
import { ErrorCode } from "@bindings/internal/library";
import { toLanguage, useLanguage, useT } from "@/lib/i18n";
import { useSnapshot, type LibraryError } from "@/lib/library";
import { useBusy, useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { useView } from "@/lib/view";
import { AgentDialog, AgentSection } from "@/features/scope/agent-settings";
import { ProjectPathDialog } from "@/features/scope/project-path-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Hint } from "@/components/ui/field";
import { Spinner } from "@/components/ui/spinner";
import { Icon } from "@/components/app/icons";
import { bodyClass, centerClass, leftClass, titlebarClass } from "@/components/app/layout";
import { navItem } from "@/components/app/nav";
import { SetRow } from "@/components/app/set-row";
import { GitHubSection, SyncSection } from "./credentials-form";
import { AboutSection, ChecksSection, GeneralSection, ProjectsSection } from "./settings-sections";
import { UninstallDialog } from "./uninstall-dialog";

// 分区按文案里的键排，标题取当前语言的文案。
const sections = ["general", "agents", "projects", "checks", "github", "sync", "about", "uninstall"] as const;
type Section = (typeof sections)[number];

export function SettingsPage({ onClose }: { onClose: () => void }) {
  const s = useSnapshot();
  const ui = useUI();
  const busy = useBusy();
  const tt = useT();
  const t = tt.settings;
  const { setLanguage } = useLanguage();
  const [settings, setSettings] = useState<Settings>();
  const [current, setCurrent] = useState<Section>(sections[0]);
  const refs = useRef<Partial<Record<Section, HTMLElement | null>>>({});

  // latest 是界面上最新的设置，改动立即显示;保存排队依次进行，每次带上点击那一刻的完整设置，
  // 连续改几项时后一次不会用旧值盖掉前一次。保存失败时退回 Go 端现在的设置。
  const latest = useRef<Settings>(undefined);
  const saving = useRef<Promise<void>>(Promise.resolve());
  const show = (value: Settings) => {
    latest.current = value;
    setSettings(value);
  };

  useEffect(() => {
    const call = AppService.Settings();
    call.then(show, () => {});
    return () => void call.cancel();
  }, []);

  const retryDistro = () => ui.run(() => AppService.RetryWSLDistro(), { onError: (err) => distroFailed(err) });
  // 发行版打不开(FailedPrecondition)时说明是哪个发行版、全局目录没有换。
  const distroFailed = (err: LibraryError, distro = latest.current?.wsl_distro ?? "") => {
    if (err.code !== ErrorCode.FailedPrecondition) return false;
    ui.toast(t.distroSwitchFailed(distro, err.message), "error");
    return true;
  };

  // 改语言时：选了具体语言的先切过去，保存期间打开的确认框也是新语言;跟随系统的由 Go 端按系统定。
  // 保存完(包括保存失败退回原设置)都再问 Go 端实际用哪种，以它为准。
  const save = (change: Partial<Settings>) => {
    if (!latest.current) return;
    const next = { ...latest.current, ...change };
    show(next);
    if (change.language) setLanguage(toLanguage(change.language));
    saving.current = saving.current.then(async () => {
      const switching = "wsl_distro" in change;
      const saved = await ui.run(() => AppService.SaveSettings(next), {
        reload: switching,
        onError: (err) => switching && distroFailed(err, next.wsl_distro ?? ""),
      });
      if (!saved) await AppService.Settings().then(show, () => {});
      if ("language" in change) await AppService.Language().then((l) => setLanguage(toLanguage(l)), () => {});
    });
  };

  // 左栏高亮跟着滚动走：顶上已经滚过的最后一个分区，滚到底时是最后一个分区。
  // 点左栏跳转时高亮点的那个，平滑滚动途中不跟;用户自己再滚(滚轮、触控、按键、拖滚动条)后恢复跟随。
  const scroller = useRef<HTMLDivElement>(null);
  const jumping = useRef(false);
  const follow = () => {
    const el = scroller.current;
    if (!el || jumping.current) return;
    const top = el.getBoundingClientRect().top;
    let at: Section = sections[0];
    for (const section of sections) {
      const box = refs.current[section];
      if (box && box.getBoundingClientRect().top - top <= 32) at = section;
    }
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 2) at = sections[sections.length - 1];
    setCurrent(at);
  };
  const release = () => void (jumping.current = false);

  const go = (section: Section) => {
    jumping.current = true;
    setCurrent(section);
    refs.current[section]?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  // 从别处(如同步弹窗的"去设置")打开时跳到指定分区;等设置读回来、上面的分区有了高度再跳。
  const wanted = useView((v) => v.settingsSection);
  useEffect(() => {
    if (!wanted || !settings) return;
    if ((sections as readonly string[]).includes(wanted)) go(wanted as Section);
    useView.setState({ settingsSection: null });
  }, [wanted, settings]);

  const card = (section: Section, note: React.ReactNode, children: React.ReactNode) => (
    <Card key={section} className="scroll-mt-4" ref={(el) => void (refs.current[section] = el)}>
      <CardHeader title={t[section]} note={note} />
      {children}
    </Card>
  );
  const register = () => ui.open((close) => <ProjectPathDialog home={s.info.home_dir} onClose={close} />);

  return (
    <>
      <header className={titlebarClass} style={{ "--wails-draggable": "drag" } as React.CSSProperties}>
        <div className="flex min-w-0 items-center gap-2">
          <div className="w-16" />
          {/* 等排队的保存都完成再返回，主界面重读时拿到的是最后保存的设置(Agent 的显示勾选)。 */}
          <Button variant="ghost" size="icon" onClick={() => void saving.current.then(onClose)} title={t.back}>
            <Icon name="chevL" />
          </Button>
          <span className="tb-crumb ml-1 inline-flex items-center gap-1.5 text-[13px] font-semibold whitespace-nowrap">
            <b>{t.title}</b>
            {busy && <Spinner className="text-muted-foreground" title={tt.common.working} />}
          </span>
        </div>
      </header>
      <div className={bodyClass}>
        <aside className={cn(leftClass, "w-[210px] flex-none")}>
          <ul className="flex flex-col gap-0.5">
            {sections.map((section) => (
              <li key={section}>
                <button type="button" className={navItem()} data-active={current === section} onClick={() => go(section)}>
                  <span className="grow">{t[section]}</span>
                </button>
              </li>
            ))}
          </ul>
        </aside>
        <main className={centerClass}>
          <div
            ref={scroller}
            className="min-h-0 flex-1 overflow-y-auto"
            onScroll={follow}
            onWheel={release}
            onTouchStart={release}
            onKeyDown={release}
            onPointerDown={release}
          >
            <div className="settings mx-auto flex max-w-[680px] flex-col gap-3 px-5 pt-4 pb-15">
              {card(
                "general",
                null,
                settings && <GeneralSection settings={settings} save={save} onRetryDistro={() => void retryDistro()} />,
              )}
              {card(
                "agents",
                <Button onClick={() => ui.open((close) => <AgentDialog onClose={close} />)}>
                  <Icon name="plus" />
                  {t.addAgent}
                </Button>,
                <AgentSection
                  shown={settings && (settings.agents_shown ?? null)}
                  onShown={(a, on) => save({ agents_shown: { ...latest.current?.agents_shown, [a.code]: on } })}
                />,
              )}
              {card(
                "projects",
                <Button onClick={register}>
                  <Icon name="plus" />
                  {t.registerProject}
                </Button>,
                <ProjectsSection />,
              )}
              {card(
                "checks",
                null,
                settings && <ChecksSection settings={settings} save={save} />,
              )}
              {card("github", t.tokenInLibrary, <GitHubSection />)}
              {card("sync", t.syncOnThisMachine, <SyncSection />)}
              {card(
                "about",
                null,
                <AboutSection />,
              )}
              {card(
                "uninstall",
                null,
                <SetRow>
                  <Hint className="grow">{t.uninstallDesc()}</Hint>
                  <Button variant="danger" onClick={() => ui.open((close) => <UninstallDialog onClose={close} />)}>
                    {t.uninstallButton}
                  </Button>
                </SetRow>,
              )}
            </div>
          </div>
        </main>
      </div>
    </>
  );
}
