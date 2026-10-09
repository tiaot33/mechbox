// 设置页：替换主界面的三栏，左边是分区目录，右边按分区排开。
// 检查更新与提示方式是只属于这台机器的应用设置(Go 端存在用户配置目录下，不进技能库),改了立即保存、生效。
// GitHub、同步、Agent 暂时只读展示外部配置状态与内置 Agent;项目分区复用主界面右栏的登记 / 改位置 / 注销。
import { useEffect, useRef, useState } from "react";
import { AppService, CheckInterval, type ConfigStatus, type Settings } from "@bindings/cmd/desktop";
import { unregisterProject } from "@/lib/actions";
import type { Snapshot } from "@/lib/library";
import { projectName, shortPath } from "@/lib/model";
import { useUI } from "@/lib/ui";
import { Icon } from "./icons";
import { AgentMark } from "./parts";
import { ProjectPathDialog } from "./scope-panel";

const sections = ["GitHub", "同步", "检查更新", "提示方式", "Agent", "项目", "关于"] as const;
type Section = (typeof sections)[number];

const intervals: { value: CheckInterval; label: string }[] = [
  { value: CheckInterval.CheckOnOpen, label: "只在打开时" },
  { value: CheckInterval.CheckHourly, label: "每小时" },
  { value: CheckInterval.CheckDaily, label: "每天" },
  { value: CheckInterval.CheckWeekly, label: "每周" },
];

export function SettingsPage({ s, config, onClose }: { s: Snapshot; config: ConfigStatus; onClose: () => void }) {
  const ui = useUI();
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
    <section key={section} className="card" ref={(el) => void (refs.current[section] = el)}>
      <div className="card-h">
        <span>{section}</span>
        {note && <span className="muted tiny">{note}</span>}
      </div>
      {children}
    </section>
  );
  const state = (ok: boolean, yes: string, no: string) => <span className={`status ${ok ? "ok" : "warn"}`}>{ok ? yes : no}</span>;
  const envHint = (
    <p className="hint">
      从 Finder / Dock 启动的应用读不到 shell 里设置的环境变量。需要这些配置时，可在终端里运行
      bin/skills-manager.app/Contents/MacOS/skills-manager。
    </p>
  );
  const register = () => ui.open((close) => <ProjectPathDialog home={s.info.home_dir} onClose={close} />);

  return (
    <>
      <header className="titlebar" style={{ "--wails-draggable": "drag" } as React.CSSProperties}>
        <div className="tb-left">
          <div className="tl-space" />
          <button type="button" className="icon-btn" onClick={onClose} title="返回技能库">
            <Icon name="chevL" />
          </button>
          <span className="tb-crumb">
            <b>设置</b>
            {ui.busy && <span className="spin muted" title="正在处理…" />}
          </span>
        </div>
      </header>
      <div className="a-body">
        <aside className="a-left">
          <ul className="nav">
            {sections.map((section) => (
              <li key={section}>
                <button type="button" className={`nav-item ${current === section ? "active" : ""}`} onClick={() => go(section)}>
                  <span className="grow">{section}</span>
                </button>
              </li>
            ))}
          </ul>
        </aside>
        <main className="a-center tile">
          <div className="a-scroll">
            <div className="settings">
              {card(
                "GitHub",
                "读取自环境变量",
                <>
                  <dl className="kv">
                    <dt>GitHub 令牌</dt>
                    <dd>{state(config.github_token, "已配置", "匿名访问")} GITHUB_TOKEN 或 GH_TOKEN</dd>
                  </dl>
                  {envHint}
                </>,
              )}
              {card(
                "同步",
                "读取自环境变量",
                <>
                  <dl className="kv">
                    <dt>S3 同步</dt>
                    <dd>{state(config.sync, "已配置", "未配置")} SKILLS_S3_BUCKET、AWS_ACCESS_KEY_ID、AWS_SECRET_ACCESS_KEY</dd>
                  </dl>
                  {envHint}
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
                    <div className={`set-row ${settings.auto_check ? "" : "disabled"}`}>
                      <div className="grow">间隔</div>
                      <div className="seg">
                        {intervals.map((i) => (
                          <button
                            key={i.value}
                            type="button"
                            className={settings.check_interval === i.value ? "on" : ""}
                            disabled={!settings.auto_check}
                            onClick={() => save({ check_interval: i.value })}
                          >
                            {i.label}
                          </button>
                        ))}
                      </div>
                    </div>
                  </>
                ),
              )}
              {card(
                "提示方式",
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
                `由技能库内置，全局目录相对 ${shortPath(s, s.info.home_dir)}`,
                s.agents.map((a) => (
                  <div key={a.code} className="scope-cell" style={{ padding: "3px 0" }}>
                    <AgentMark code={a.code} name={a.name} />
                    {a.name}
                    <span className="mono tiny muted">{a.code}</span>
                  </div>
                )),
              )}
              {card(
                "项目",
                <button type="button" className="pill-btn" onClick={register}>
                  <Icon name="plus" />
                  登记项目
                </button>,
                <>
                  {s.projects.map((p) => (
                    <div key={p.id} className="set-row">
                      <Icon name="folder" />
                      <div className="grow">
                        <div className="ellipsis">{projectName(s, p.id)}</div>
                        <div className="mono tiny muted ellipsis" title={p.path} style={{ userSelect: "text" }}>
                          {shortPath(s, p.path)}
                        </div>
                      </div>
                      {p.missing && (
                        <span className="badge warn" title="项目目录在这台机器上不存在;可以改项目位置">
                          不在这台机器上
                        </span>
                      )}
                      <button
                        type="button"
                        className="pill-btn"
                        onClick={() => ui.open((close) => <ProjectPathDialog home={s.info.home_dir} project={p} onClose={close} />)}
                      >
                        改位置
                      </button>
                      <button type="button" className="pill-btn" onClick={() => void unregisterProject(ui, s, p)}>
                        注销
                      </button>
                    </div>
                  ))}
                  {s.projects.length === 0 && <p className="hint">还没有登记项目。登记后可以把技能安装到项目级目录。</p>}
                </>,
              )}
              {card(
                "关于",
                null,
                <dl className="kv">
                  <dt>应用版本</dt>
                  <dd className="mono" style={{ userSelect: "text" }}>
                    {s.info.version}
                  </dd>
                  <dt>数据库位置</dt>
                  <dd className="mono tiny" style={{ userSelect: "text", wordBreak: "break-all" }}>
                    {s.info.library_path}
                  </dd>
                </dl>,
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
    <div className="set-row">
      <div className="grow">
        <div>{label}</div>
        <div className="hint">{desc}</div>
      </div>
      <button type="button" role="switch" aria-checked={on} className={`switch ${on ? "on" : ""}`} onClick={() => onChange(!on)}>
        <i />
      </button>
    </div>
  );
}
