// 设置页：替换主界面的三栏，左边是分区目录，右边按分区排开。
// 检查更新与通用是只属于这台机器的应用设置(Go 端存在用户配置目录下，不进技能库),改了立即保存、生效。
// GitHub 与同步填的令牌和密钥存在系统钥匙串里，其余几项也存在这台机器上，点保存后交给技能库(见 credentials-form.tsx)。
// Agent 分区见 agent-settings;项目分区复用主界面右栏的登记 / 改位置 / 注销;卸载 Mechbox 见 uninstall-dialog。
import { useEffect, useRef, useState } from "react";
import { AppService, CheckInterval, Language, type Settings } from "@bindings/cmd/desktop";
import { unregisterProject } from "@/lib/actions";
import { useLanguage, useT } from "@/lib/i18n";
import type { Snapshot } from "@/lib/library";
import { projectName, shortPath } from "@/lib/model";
import { useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Hint } from "@/components/ui/field";
import { Segmented } from "@/components/ui/segmented";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { AgentDialog, AgentSection } from "./agent-settings";
import { GitHubForm, SyncForm, useCredentials } from "./credentials-form";
import { Icon } from "./icons";
import { bodyClass, centerClass, leftClass, titlebarClass } from "./layout";
import { navItem } from "./nav";
import { ProjectPathDialog } from "./scope-panel";
import { SetRow } from "./set-row";
import { UninstallDialog } from "./uninstall-dialog";

// 分区按文案里的键排，标题取当前语言的文案。
const sections = ["general", "agents", "projects", "checks", "github", "sync", "about", "uninstall"] as const;
type Section = (typeof sections)[number];

export function SettingsPage({ s, onClose, onConfigChange }: { s: Snapshot; onClose: () => void; onConfigChange: () => void }) {
  const ui = useUI();
  const t = useT().settings;
  const { setLanguage } = useLanguage();
  const intervals: { value: CheckInterval; label: string }[] = [
    { value: CheckInterval.CheckOnOpen, label: t.onOpen },
    { value: CheckInterval.CheckHourly, label: t.hourly },
    { value: CheckInterval.CheckDaily, label: t.daily },
    { value: CheckInterval.CheckWeekly, label: t.weekly },
  ];
  // 跟随系统时实际用哪种语言由 Go 端按系统的首选语言定。
  const languages: { value: Language; label: string }[] = [
    { value: Language.LanguageSystem, label: t.followSystem },
    { value: Language.LanguageZh, label: t.chinese },
    { value: Language.LanguageEn, label: t.english },
  ];
  const [settings, setSettings] = useState<Settings>();
  const credentials = useCredentials(onConfigChange);
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

  // 改语言时保存后再问 Go 端实际用哪种(跟随系统时由系统定),整个界面随之切换。
  const save = (change: Partial<Settings>) => {
    if (!latest.current) return;
    const next = { ...latest.current, ...change };
    show(next);
    saving.current = saving.current.then(async () => {
      const saved = await ui.run(() => AppService.SaveSettings(next), { reload: false });
      if (!saved) await AppService.Settings().then(show, () => {});
      if (saved && "language" in change) await AppService.Language().then((l) => setLanguage(l === "zh" ? "zh" : "en"), () => {});
    });
  };

  const go = (section: Section) => {
    setCurrent(section);
    refs.current[section]?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

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
            {ui.busy && <Spinner className="text-muted-foreground" title={t.working} />}
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
          <div className="min-h-0 flex-1 overflow-y-auto">
            <div className="settings mx-auto flex max-w-[680px] flex-col gap-3 px-5 pt-4 pb-15">
              {card(
                "general",
                null,
                settings && (
                  <>
                    <SettingRow
                      label={t.stayInMenuBar}
                      desc={t.stayInMenuBarDesc}
                      on={settings.stay_in_menu_bar}
                      onChange={(on) => save({ stay_in_menu_bar: on })}
                    />
                    <SettingRow
                      label={t.dockBadge}
                      desc={t.dockBadgeDesc}
                      on={settings.dock_badge}
                      onChange={(on) => save({ dock_badge: on })}
                    />
                    <SetRow className="language-row">
                      <div className="grow">
                        <div>{t.language}</div>
                        <Hint className="mt-0.5">{t.languageDesc}</Hint>
                      </div>
                      <Segmented value={settings.language ?? Language.LanguageSystem} onChange={(v) => save({ language: v })} options={languages} />
                    </SetRow>
                  </>
                ),
              )}
              {card(
                "agents",
                <Button onClick={() => ui.open((close) => <AgentDialog s={s} onClose={close} />)}>
                  <Icon name="plus" />
                  {t.addAgent}
                </Button>,
                <AgentSection
                  s={s}
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
                <>
                  {s.projects.map((p) => (
                    <SetRow key={p.id}>
                      <Icon name="folder" />
                      <div className="min-w-0 grow">
                        <div className="truncate">{projectName(s, p.id)}</div>
                        <div className="truncate font-mono text-[11px] text-muted-foreground select-text" title={p.path}>
                          {shortPath(s, p.path)}
                        </div>
                      </div>
                      {p.missing && (
                        <Badge tone="warn" title={t.missingTitle}>
                          {t.missing}
                        </Badge>
                      )}
                      <Button onClick={() => ui.open((close) => <ProjectPathDialog home={s.info.home_dir} project={p} onClose={close} />)}>
                        {t.relocate}
                      </Button>
                      <Button onClick={() => void unregisterProject(ui, s, p)}>{t.unregister}</Button>
                    </SetRow>
                  ))}
                  {s.projects.length === 0 && <Hint>{t.noProjects}</Hint>}
                </>,
              )}
              {card(
                "checks",
                null,
                settings && (
                  <>
                    <SettingRow
                      label={t.autoCheck}
                      desc={t.autoCheckDesc}
                      on={settings.auto_check}
                      onChange={(on) => save({ auto_check: on })}
                    />
                    <SetRow className={cn(!settings.auto_check && "text-muted-foreground")}>
                      <div className="grow">{t.interval}</div>
                      <Segmented
                        value={settings.check_interval}
                        disabled={!settings.auto_check}
                        onChange={(v) => save({ check_interval: v })}
                        options={intervals}
                      />
                    </SetRow>
                  </>
                ),
              )}
              {card("github", t.tokenInKeychain, credentials((saved, onSaved) => <GitHubForm saved={saved} onSaved={onSaved} />))}
              {card("sync", t.secretsInKeychain, credentials((saved, onSaved) => <SyncForm saved={saved} onSaved={onSaved} />))}
              {card(
                "about",
                null,
                <dl className="kv grid grid-cols-[110px_1fr] gap-x-3 gap-y-1.5 text-xs [&_dt]:text-muted-foreground">
                  <dt>{t.version}</dt>
                  <dd className="font-mono select-text">{s.info.version}</dd>
                  <dt>{t.databasePath}</dt>
                  <dd className="font-mono text-[11px] break-all select-text">{s.info.library_path}</dd>
                </dl>,
              )}
              {card(
                "uninstall",
                null,
                <SetRow>
                  <Hint className="grow">{t.uninstallDesc}</Hint>
                  <Button variant="danger" onClick={() => ui.open((close) => <UninstallDialog s={s} onClose={close} />)}>
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

function SettingRow({ label, desc, on, onChange }: { label: string; desc: string; on: boolean; onChange: (on: boolean) => void }) {
  return (
    <SetRow>
      <div className="grow">
        <div>{label}</div>
        <Hint className="mt-0.5">{desc}</Hint>
      </div>
      <Switch checked={on} onCheckedChange={onChange} />
    </SetRow>
  );
}
