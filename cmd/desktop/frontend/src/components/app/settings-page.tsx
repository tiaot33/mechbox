// 设置页：替换主界面的三栏，左边是分区目录，右边按分区排开。
// 检查更新与通用是只属于这台机器的应用设置(Go 端存在用户配置目录下，不进技能库),改了立即保存、生效。
// GitHub 与同步填的令牌和密钥存在系统钥匙串里，其余几项也存在这台机器上，点保存后交给技能库(见 credentials-form.tsx)。
// Agent 分区见 agent-settings;项目分区复用主界面右栏的登记 / 改位置 / 注销;卸载 Mechbox 见 uninstall-dialog。
import { useEffect, useRef, useState } from "react";
import { AppService, CheckInterval, type Settings } from "@bindings/cmd/desktop";
import { unregisterProject } from "@/lib/actions";
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

const sections = ["通用", "Agent", "项目", "检查更新", "GitHub", "同步", "关于", "卸载 Mechbox"] as const;
type Section = (typeof sections)[number];

const intervals: { value: CheckInterval; label: string }[] = [
  { value: CheckInterval.CheckOnOpen, label: "只在打开时" },
  { value: CheckInterval.CheckHourly, label: "每小时" },
  { value: CheckInterval.CheckDaily, label: "每天" },
  { value: CheckInterval.CheckWeekly, label: "每周" },
];

export function SettingsPage({ s, onClose, onConfigChange }: { s: Snapshot; onClose: () => void; onConfigChange: () => void }) {
  const ui = useUI();
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

  const save = (change: Partial<Settings>) => {
    if (!latest.current) return;
    const next = { ...latest.current, ...change };
    show(next);
    saving.current = saving.current.then(async () => {
      const saved = await ui.run(() => AppService.SaveSettings(next), { reload: false });
      if (!saved) await AppService.Settings().then(show, () => {});
    });
  };

  const go = (section: Section) => {
    setCurrent(section);
    refs.current[section]?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const card = (section: Section, note: React.ReactNode, children: React.ReactNode) => (
    <Card key={section} className="scroll-mt-4" ref={(el) => void (refs.current[section] = el)}>
      <CardHeader title={section} note={note} />
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
          <Button variant="ghost" size="icon" onClick={() => void saving.current.then(onClose)} title="返回技能库">
            <Icon name="chevL" />
          </Button>
          <span className="tb-crumb ml-1 inline-flex items-center gap-1.5 text-[13px] font-semibold whitespace-nowrap">
            <b>设置</b>
            {ui.busy && <Spinner className="text-muted-foreground" title="正在处理…" />}
          </span>
        </div>
      </header>
      <div className={bodyClass}>
        <aside className={cn(leftClass, "w-[210px] flex-none")}>
          <ul className="flex flex-col gap-0.5">
            {sections.map((section) => (
              <li key={section}>
                <button type="button" className={navItem()} data-active={current === section} onClick={() => go(section)}>
                  <span className="grow">{section}</span>
                </button>
              </li>
            ))}
          </ul>
        </aside>
        <main className={centerClass}>
          <div className="min-h-0 flex-1 overflow-y-auto">
            <div className="settings mx-auto flex max-w-[680px] flex-col gap-3 px-5 pt-4 pb-15">
              {card(
                "通用",
                null,
                settings && (
                  <>
                    <SettingRow
                      label="关闭窗口后留在菜单栏"
                      desc="关闭窗口后应用继续运行，Dock 图标和角标仍在，菜单栏多一个入口，继续按间隔检查;关掉时关闭窗口就退出。"
                      on={settings.stay_in_menu_bar}
                      onChange={(on) => save({ stay_in_menu_bar: on })}
                    />
                    <SettingRow
                      label="Dock 角标"
                      desc="显示有新版本的技能数，本地目录来源的技能不计入。"
                      on={settings.dock_badge}
                      onChange={(on) => save({ dock_badge: on })}
                    />
                  </>
                ),
              )}
              {card(
                "Agent",
                <Button onClick={() => ui.open((close) => <AgentDialog s={s} onClose={close} />)}>
                  <Icon name="plus" />
                  添加 Agent
                </Button>,
                <AgentSection
                  s={s}
                  shown={settings && (settings.agents_shown ?? null)}
                  onShown={(a, on) => save({ agents_shown: { ...latest.current?.agents_shown, [a.code]: on } })}
                />,
              )}
              {card(
                "项目",
                <Button onClick={register}>
                  <Icon name="plus" />
                  登记项目
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
                        <Badge tone="warn" title="项目目录在这台机器上不存在;可以改项目位置">
                          不在这台机器上
                        </Badge>
                      )}
                      <Button onClick={() => ui.open((close) => <ProjectPathDialog home={s.info.home_dir} project={p} onClose={close} />)}>
                        改位置
                      </Button>
                      <Button onClick={() => void unregisterProject(ui, s, p)}>注销</Button>
                    </SetRow>
                  ))}
                  {s.projects.length === 0 && <Hint>还没有登记项目。登记后可以把技能安装到项目级目录。</Hint>}
                </>,
              )}
              {card(
                "检查更新",
                null,
                settings && (
                  <>
                    <SettingRow
                      label="自动检查更新"
                      desc="按间隔检查所有来源有没有新版本(本地目录来源不检查)。"
                      on={settings.auto_check}
                      onChange={(on) => save({ auto_check: on })}
                    />
                    <SetRow className={cn(!settings.auto_check && "text-muted-foreground")}>
                      <div className="grow">间隔</div>
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
              {card("GitHub", "令牌存在系统钥匙串里", credentials((saved, onSaved) => <GitHubForm saved={saved} onSaved={onSaved} />))}
              {card("同步", "密钥存在系统钥匙串里", credentials((saved, onSaved) => <SyncForm saved={saved} onSaved={onSaved} />))}
              {card(
                "关于",
                null,
                <dl className="kv grid grid-cols-[110px_1fr] gap-x-3 gap-y-1.5 text-xs [&_dt]:text-muted-foreground">
                  <dt>应用版本</dt>
                  <dd className="font-mono select-text">{s.info.version}</dd>
                  <dt>数据库位置</dt>
                  <dd className="font-mono text-[11px] break-all select-text">{s.info.library_path}</dd>
                </dl>,
              )}
              {card(
                "卸载 Mechbox",
                null,
                <SetRow>
                  <Hint className="grow">
                    删除技能库、设置页里的设置和系统钥匙串里的令牌与密钥，退出后把应用移到废纸篓;已安装的技能文件夹默认保留，Agent 照常使用。
                  </Hint>
                  <Button variant="danger" onClick={() => ui.open((close) => <UninstallDialog s={s} onClose={close} />)}>
                    卸载 Mechbox…
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
