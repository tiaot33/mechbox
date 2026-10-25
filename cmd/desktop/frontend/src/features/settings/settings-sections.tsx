// 设置页里几个分区的内容：通用、项目、检查更新、关于。改了立即经设置页的 save 排队保存。
import { useQuery } from "@tanstack/react-query";
import { CheckInterval, Language, type Settings } from "@bindings/cmd/desktop";
import { ui } from "@/lib/ui";
import { useT } from "@/lib/i18n";
import { useSnapshot, wslDistroErrorQuery, wslDistrosQuery } from "@/lib/library";
import { projectName, shortPath } from "@/lib/model";
import { cn } from "@/lib/utils";
import { ProjectPathDialog } from "@/features/scope/project-path-dialog";
import { unregisterProject } from "@/features/scope/projects";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { NativeSelect } from "@/components/ui/native-select";
import { Notice } from "@/components/ui/notice";
import { Segmented } from "@/components/ui/segmented";
import { Switch } from "@/components/ui/switch";
import { Icon } from "@/components/app/icons";
import { SetRow } from "@/components/app/set-row";

// GeneralSection 是通用分区：关闭窗口后留在菜单栏、Dock 角标、界面语言，以及 Windows 上装了 WSL 时的全局目录。
export function GeneralSection({
  settings,
  save,
  onRetryDistro,
}: {
  settings: Settings;
  save: (change: Partial<Settings>) => void;
  onRetryDistro: () => void;
}) {
  const t = useT().settings;
  // 跟随系统时实际用哪种语言由 Go 端按系统的首选语言定。
  const languages: { value: Language; label: string }[] = [
    { value: Language.LanguageSystem, label: t.followSystem },
    { value: Language.LanguageZh, label: t.chinese },
    { value: Language.LanguageEn, label: t.english },
  ];
  // 这台机器上装的 WSL 发行版;不在 Windows 上或没装 WSL 时为空，全局目录这一项不出现。
  const distros = useQuery(wslDistrosQuery).data ?? [];

  // 选的发行版启动时没能打开、现在用的是 Windows 本机的全局目录时，这一项下面写明，并可以再试一次。
  // 再试、改选发行版都经 run,完成后随缓存失效重读。
  const distroError = useQuery(wslDistroErrorQuery).data ?? "";
  return (
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
      <Segmented
        className="shrink-0"
        value={settings.language ?? Language.LanguageSystem}
        onChange={(v) => save({ language: v })}
        options={languages}
      />
    </SetRow>
    {(distros.length > 0 || settings.wsl_distro) && (
      <SetRow>
        <div className="grow">
          <div>{t.wslDistro}</div>
          <Hint className="mt-0.5">{t.wslDistroDesc}</Hint>
        </div>
        <NativeSelect
          className="shrink-0 max-w-[180px]"
          value={settings.wsl_distro ?? ""}
          onChange={(e) => save({ wsl_distro: e.target.value })}
        >
          <option value="">{t.windowsHome}</option>
          {[...new Set([...distros, ...(settings.wsl_distro ? [settings.wsl_distro] : [])])].map((d) => (
            <option key={d} value={d}>
              WSL · {d}
            </option>
          ))}
        </NativeSelect>
      </SetRow>
    )}
    {distroError && settings.wsl_distro && (
      <SetRow>
        <Notice tone="warn" className="grow">
          {t.distroFallback(settings.wsl_distro, distroError)}
        </Notice>
        <Button onClick={onRetryDistro}>{t.retryDistro}</Button>
      </SetRow>
    )}
  </>
  );
}

// ProjectsSection 是项目分区：已登记的项目，可以改位置、注销;项目目录不在这台机器上时带标记。
export function ProjectsSection() {
  const s = useSnapshot();
  const t = useT().settings;
  return (
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
        <Button onClick={() => void unregisterProject(p)}>{t.unregister}</Button>
      </SetRow>
    ))}
    {s.projects.length === 0 && <Hint>{t.noProjects}</Hint>}
  </>
  );
}

// ChecksSection 是检查更新分区：自动检查与检查间隔。
export function ChecksSection({ settings, save }: { settings: Settings; save: (change: Partial<Settings>) => void }) {
  const t = useT().settings;
  const intervals: { value: CheckInterval; label: string }[] = [
    { value: CheckInterval.CheckOnOpen, label: t.onOpen },
    { value: CheckInterval.CheckHourly, label: t.hourly },
    { value: CheckInterval.CheckDaily, label: t.daily },
    { value: CheckInterval.CheckWeekly, label: t.weekly },
  ];
  return (
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
  );
}

// TranslateSection 是 AI 分区里的自动翻译开关：填了 AI 设置时，主界面把技能描述翻译成界面语言(见 lib/translate)。
export function TranslateSection({ settings, save }: { settings: Settings; save: (change: Partial<Settings>) => void }) {
  const t = useT().settings;
  return (
    <SettingRow
      label={t.autoTranslate}
      desc={t.autoTranslateDesc}
      on={settings.auto_translate}
      onChange={(on) => save({ auto_translate: on })}
    />
  );
}

// AboutSection 是关于分区：应用版本与数据库的位置。
export function AboutSection() {
  const s = useSnapshot();
  const t = useT().settings;
  return (
  <dl className="kv grid grid-cols-[110px_1fr] gap-x-3 gap-y-1.5 text-xs [&_dt]:text-muted-foreground">
    <dt>{t.version}</dt>
    <dd className="font-mono select-text">{s.info.version}</dd>
    <dt>{t.databasePath}</dt>
    <dd className="font-mono text-[11px] break-all select-text">{s.info.library_path}</dd>
  </dl>
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
