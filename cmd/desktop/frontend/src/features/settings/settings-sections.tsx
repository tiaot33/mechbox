// 设置页里几个分区的内容：通用、项目、检查更新、关于。改了立即经设置页的 save 排队保存。
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Appearance, CheckInterval, Language, type Settings } from "@bindings/cmd/desktop";
import { useGlassOpaque } from "@/lib/glass";
import { glassTintOf, previewGlassTint, setThemeSettings, useTheme } from "@/lib/theme";
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
import { Slider } from "@/components/ui/slider";
import { Switch } from "@/components/ui/switch";
import { Icon } from "@/components/app/icons";
import { WSLBadge } from "@/components/app/parts";
import { SetRow } from "@/components/app/set-row";
import { resumeAutoTranslate } from "@/features/skill/translate";
import { ReleaseCheck } from "@/features/release/release";

// GeneralSection 是通用分区：关闭窗口后留在菜单栏、Dock 角标、左栏折叠技能少的来源、界面语言、外观、毛玻璃浓度，
// 以及 Windows 上装了 WSL 时的全局目录。
export function GeneralSection({
  settings,
  save,
  onRetryDistro,
}: {
  settings: Settings;
  save: (change: Partial<Settings>) => void;
  onRetryDistro: () => void;
}) {
  const { settings: t, paths } = useT();
  // 跟随系统时实际用哪种语言由 Go 端按系统的首选语言定。
  const languages: { value: Language; label: string }[] = [
    { value: Language.LanguageSystem, label: t.followSystem },
    { value: Language.LanguageZh, label: t.chinese },
    { value: Language.LanguageEn, label: t.english },
  ];
  const appearances: { value: Appearance; label: string }[] = [
    { value: Appearance.AppearanceSystem, label: t.followSystem },
    { value: Appearance.AppearanceLight, label: t.light },
    { value: Appearance.AppearanceDark, label: t.dark },
  ];
  // 外观与毛玻璃浓度跟着显示的设置立即生效：改了马上变，保存失败退回原设置时也跟着退回。
  const { appearance, glass_tint, glass_tint_dark } = settings;
  useEffect(() => setThemeSettings({ appearance, glass_tint, glass_tint_dark }), [appearance, glass_tint, glass_tint_dark]);
  const theme = useTheme();
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
    <SettingRow
      label={t.foldCollections}
      desc={t.foldCollectionsDesc}
      on={settings.fold_collections}
      onChange={(on) => save({ fold_collections: on })}
    />
    <SetRow className={cn("fold-below-row", !settings.fold_collections && "text-muted-foreground")}>
      <div className="grow">{t.foldBelow}</div>
      <NativeSelect
        className="shrink-0"
        aria-label={t.foldBelow}
        value={settings.fold_below}
        disabled={!settings.fold_collections}
        onChange={(e) => save({ fold_below: Number(e.target.value) })}
      >
        {[...new Set([...foldBelowOptions, settings.fold_below])]
          .sort((a, b) => a - b)
          .map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
      </NativeSelect>
    </SetRow>
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
    <SetRow className="appearance-row">
      <div className="grow">{t.appearance}</div>
      <Segmented
        className="shrink-0"
        value={settings.appearance ?? Appearance.AppearanceSystem}
        onChange={(v) => save({ appearance: v })}
        options={appearances}
      />
    </SetRow>
    <GlassTintRow
      dark={theme === "dark"}
      value={glassTintOf(settings, theme)}
      onCommit={(v) => save(theme === "dark" ? { glass_tint_dark: v } : { glass_tint: v })}
    />
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
              {paths.wsl(d)}
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

// 左栏折叠集合时可选的技能数上限(不含),在 Go 端允许的 2–100 之内;存着别的数时也列出来。
const foldBelowOptions = [2, 3, 4, 5, 6, 8, 10];

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
          <div className="flex min-w-0 items-center gap-1.5">
            <span className="truncate">{projectName(s, p.id)}</span>
            <WSLBadge path={p.path} />
          </div>
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

// ChecksSection 是检查技能更新分区：自动检查与检查间隔。
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

// TranslateSection 是 AI 分区里的自动翻译开关：填了 AI 设置时，主界面把技能描述翻译成界面语言(见 features/skill/translate);
// 重新打开时，之前因失败停下的自动翻译再试。
export function TranslateSection({ settings, save }: { settings: Settings; save: (change: Partial<Settings>) => void }) {
  const t = useT().settings;
  return (
    <SettingRow
      label={t.autoTranslate}
      desc={t.autoTranslateDesc}
      on={settings.auto_translate}
      onChange={(on) => {
        if (on) resumeAutoTranslate();
        save({ auto_translate: on });
      }}
    />
  );
}

// AboutSection 是关于分区：应用版本(旁边检查新版本)与数据库的位置。
export function AboutSection() {
  const s = useSnapshot();
  const t = useT().settings;
  return (
  <dl className="kv grid grid-cols-[110px_1fr] gap-x-3 gap-y-1.5 text-xs [&_dt]:text-muted-foreground">
    <dt>{t.version}</dt>
    <dd className="flex flex-wrap items-start gap-x-3 gap-y-1.5">
      <span className="font-mono leading-6 select-text">{s.info.version}</span>
      <ReleaseCheck />
    </dd>
    <dt>{t.databasePath}</dt>
    <dd className="font-mono text-[11px] break-all select-text">{s.info.library_path}</dd>
  </dl>
  );
}

// GlassTintRow 是现在这个外观下的毛玻璃浓度(浅色、深色各存一个值):拖动时界面上的毛玻璃跟着变，松手才保存;
// 保存失败退回原设置时也跟着退回(GeneralSection 按显示的设置写上浓度)。
// 设置页上没有毛玻璃表面，旁边放一块预览：底下垫几行像列表的色条，上面一块玻璃写着当前浓度。
// 系统要求减少透明度或开了强制颜色时，毛玻璃是不透明的底，浓度不起作用：滑块禁用，说明里写明原因。
function GlassTintRow({ dark, value, onCommit }: { dark: boolean; value: number; onCommit: (v: number) => void }) {
  const t = useT().settings;
  const [shown, setShown] = useState(value);
  const opaque = useGlassOpaque();
  useEffect(() => setShown(value), [value]);
  return (
    <SetRow className="glass-tint-row">
      <div className="grow">
        <div>{t.glassTint}</div>
        <Hint className="mt-0.5">{opaque ? t.glassTintOpaque : t.glassTintDesc(dark)}</Hint>
      </div>
      <Slider
        disabled={opaque}
        valueText={`${Math.round(shown * 100)}%`}
        className="w-[140px] shrink-0"
        aria-label={t.glassTint}
        min={0}
        max={1.5}
        step={0.05}
        value={[shown]}
        onValueChange={([v]) => {
          setShown(v);
          previewGlassTint(v);
        }}
        onValueCommit={([v]) => onCommit(v)}
      />
      <div
        className="glass-tint-preview relative h-10 w-[120px] shrink-0 overflow-hidden rounded-lg bg-panel shadow-[inset_0_0_0_0.5px_var(--shade-preview)]"
        aria-hidden
      >
        <span className="absolute top-2 left-2 h-1.5 w-12 rounded-full bg-foreground/80" />
        <span className="absolute top-2 right-2 h-3 w-9 rounded-full bg-primary" />
        <span className="absolute top-[18px] left-2 h-1.5 w-16 rounded-full bg-muted-foreground/70" />
        <span className="absolute top-[28px] left-2 h-1.5 w-10 rounded-full bg-warn" />
        <span className="absolute top-[28px] right-3 h-1.5 w-12 rounded-full bg-foreground/60" />
        <span className="glass absolute inset-x-5 inset-y-1.5 grid place-items-center rounded-md border text-[11px] font-medium tabular-nums">
          {Math.round(shown * 100)}%
        </span>
      </div>
    </SetRow>
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
