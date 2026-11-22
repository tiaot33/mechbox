// 设置页里几个分区的内容：通用、项目、检查更新、关于。应用设置改了立即经设置页的 save 排队保存;
// 通用里的网络代理与 GitHub 镜像加速存进技能库本机配置(切换模式、开关立即生效，手动地址点保存生效)。
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Appearance, CheckInterval, Language, LibraryService, type Settings } from "@bindings/cmd/desktop";
import { ErrorCode, ProxyMode, type ProxySettings, type SystemProxy } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { setAppearance } from "@/lib/theme";
import { ui } from "@/lib/ui";
import { useT } from "@/lib/i18n";
import {
  githubMirrorQuery,
  invalidateProxySettings,
  invalidateSystemProxy,
  proxySettingsQuery,
  queryClient,
  systemProxyQuery,
  toLibraryError,
  useSnapshot,
  wslDistroErrorQuery,
  wslDistrosQuery,
  type LibraryError,
} from "@/lib/library";
import { projectName } from "@/lib/scope";
import { shortPath } from "@/lib/paths";
import { formatSystemProxy } from "@/lib/proxy";
import { cn } from "@/lib/utils";
import { ProjectPathDialog } from "@/features/scope/project-path-dialog";
import { unregisterProject } from "@/features/scope/projects";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Notice } from "@/components/ui/notice";
import { Segmented } from "@/components/ui/segmented";
import { Switch } from "@/components/ui/switch";
import { Icon } from "@/components/ui/icon";
import { PathText, Tipped, WSLBadge } from "@/components/app/parts";
import { SetRow } from "@/components/app/set-row";
import { resumeAutoTranslate } from "@/features/skill/translate";
import { ReleaseCheck } from "@/features/release/release";

// GeneralSection 是通用分区：关闭窗口后留在菜单栏、Dock 角标、左栏折叠技能少的来源、界面语言、外观，
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
  // 外观跟着显示的设置立即生效：改了马上变，保存失败退回原设置时也跟着退回。
  const { appearance } = settings;
  useEffect(() => setAppearance(appearance), [appearance]);
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
        {foldBelowOptions.map((n) => (
          <option key={n} value={n}>
            {n}
          </option>
        ))}
      </NativeSelect>
    </SetRow>
    <SetRow className="language-row">
      <Tipped className="grow" tip={t.languageDesc}>
        {t.language}
      </Tipped>
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
    <ProxySetting />
    <GitHubMirrorSetting />
    {(distros.length > 0 || settings.wsl_distro) && (
      <SetRow>
        <Tipped className="grow" tip={t.wslDistroDesc}>
          {t.wslDistro}
        </Tipped>
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
      // 打不开发行版的提示有几句话：这一行不套固定行高，随内容长高;在灰底的分组里，提示条自己不再铺底。
      <SetRow className="h-auto min-h-10 py-2">
        <Notice
          alert
          className="grow bg-transparent py-0 pr-0 pl-5 [&>[data-slot=icon]]:top-[3px] [&>[data-slot=icon]]:left-0"
          action={<Button onClick={onRetryDistro}>{t.retryDistro}</Button>}
        >
          {t.distroFallback(settings.wsl_distro, distroError)}
        </Notice>
      </SetRow>
    )}
  </>
  );
}

// 左栏折叠集合时可选的技能数上限(不含):2 到 10,和 Go 端允许的一样。
const foldBelowOptions = Array.from({ length: 9 }, (_, i) => i + 2);

function ProxySetting() {
  const { settings: t, credentials } = useT();
  const { data: saved, error, refetch } = useQuery(proxySettingsQuery);
  const { data: sysProxy } = useQuery(systemProxyQuery);
  if (error) {
    return (
      <SetRow className="h-auto min-h-10 py-2">
        <Hint className="grow">{t.proxyReadFailed(describeError(toLibraryError(error)))}</Hint>
        <Button onClick={() => void refetch()}>{credentials.retry}</Button>
      </SetRow>
    );
  }
  if (!saved) return null;
  const savedMode = saved.mode ?? ProxyMode.ProxySystem;
  const savedURL = saved.url ?? "";
  return <ProxyControl key={`${savedMode}\0${savedURL}`} saved={{ mode: savedMode, url: savedURL }} sysProxy={sysProxy} />;
}

function ProxyControl({ saved, sysProxy }: { saved: ProxySettings; sysProxy?: SystemProxy }) {
  const t = useT().settings;
  const savedMode = saved.mode ?? ProxyMode.ProxySystem;
  const savedURL = saved.url ?? "";
  const [mode, setMode] = useState<ProxyMode>(savedMode);
  const [draftURL, setDraftURL] = useState(savedURL);
  const [showURL, setShowURL] = useState(false);
  const [urlError, setURLError] = useState("");
  const [saving, setSaving] = useState(false);

  const modes: { value: ProxyMode; label: string }[] = [
    { value: ProxyMode.ProxySystem, label: t.proxySystem },
    { value: ProxyMode.ProxyManual, label: t.proxyManual },
    { value: ProxyMode.ProxyDirect, label: t.proxyDirect },
  ];

  const switchMode = async (next: ProxyMode) => {
    if (next === mode || saving) return;
    setURLError("");
    setMode(next);
    if (next === ProxyMode.ProxyManual && !savedURL) return;
    setSaving(true);
    const ok = await ui.run(() => LibraryService.SetProxySettings({ mode: next, url: savedURL }).then(() => true), {
      reload: false,
    });
    setSaving(false);
    if (!ok) {
      setMode(savedMode);
      return;
    }
    await invalidateProxySettings();
    if (next === ProxyMode.ProxySystem) void invalidateSystemProxy();
  };

  const dirty = savedMode !== ProxyMode.ProxyManual || draftURL.trim() !== savedURL;
  const canSave = draftURL.trim() !== "" && dirty && !saving;

  const saveManual = async () => {
    if (!canSave) return;
    setURLError("");
    setSaving(true);
    const ok = await ui.run(() => LibraryService.SetProxySettings({ mode: ProxyMode.ProxyManual, url: draftURL }).then(() => true), {
      reload: false,
      success: t.proxySaved,
      onError: (err: LibraryError) => {
        if (err.code === ErrorCode.InvalidArgument && err.details?.field === "url") {
          setURLError(t.proxyInvalidUrl);
          return true;
        }
        return false;
      },
    });
    setSaving(false);
    if (ok) {
      await invalidateProxySettings();
      const fresh = queryClient.getQueryData(proxySettingsQuery.queryKey);
      if (fresh) setDraftURL(fresh.url ?? "");
    }
  };

  const systemSummary = formatSystemProxy(sysProxy);

  return (
    <>
      <SetRow className="proxy-row">
        <Tipped className="grow" tip={t.proxyDesc}>
          {t.proxy}
        </Tipped>
        <Segmented className="shrink-0" disabled={saving} value={mode} onChange={(v) => void switchMode(v)} options={modes} />
      </SetRow>
      {mode === ProxyMode.ProxySystem && (
        <SetRow className="proxy-system-row h-auto min-h-10 py-2">
          <span className="shrink-0 text-caption text-muted-foreground">{t.systemProxyCurrent}</span>
          <span
            className="min-w-0 grow truncate text-right font-mono text-caption text-muted-foreground select-text"
            title={systemSummary || undefined}
          >
            {systemSummary || t.systemProxyNone}
          </span>
        </SetRow>
      )}
      {mode === ProxyMode.ProxyManual && (
        <SetRow className="proxy-manual-row h-auto min-h-10 py-2">
          <form
            className="flex min-w-0 grow flex-col gap-1.5"
            onSubmit={(e) => {
              e.preventDefault();
              void saveManual();
            }}
          >
            <div className="flex items-center gap-2">
              <div className="relative flex min-w-0 grow items-center">
                <Input
                  className="min-w-0 grow pr-7 font-mono"
                  type={showURL ? "text" : "password"}
                  disabled={saving}
                  aria-label={t.proxyAddress}
                  placeholder={t.proxyPlaceholder}
                  value={draftURL}
                  onChange={(e) => {
                    setDraftURL(e.target.value);
                    setURLError("");
                  }}
                  autoComplete="off"
                  autoCapitalize="off"
                  spellCheck={false}
                />
                <button
                  type="button"
                  className="proxy-visibility absolute right-1.5 inline-flex size-5 items-center justify-center rounded-sm text-muted-foreground hover:text-foreground disabled:opacity-40"
                  disabled={saving}
                  aria-label={showURL ? t.proxyHide : t.proxyShow}
                  title={showURL ? t.proxyHide : t.proxyShow}
                  onClick={() => setShowURL((v) => !v)}
                >
                  <Icon name={showURL ? "eyeOff" : "eye"} />
                </button>
              </div>
              <Button type="submit" disabled={!canSave} loading={saving}>
                {t.proxySave}
              </Button>
            </div>
            {urlError ? (
              <p className="text-caption text-destructive">{urlError}</p>
            ) : dirty ? (
              <Hint>{t.proxyUnsaved}</Hint>
            ) : null}
          </form>
        </SetRow>
      )}
    </>
  );
}

// GitHubMirrorSetting 是 GitHub 镜像加速的开关：一点就存，下一次联网生效;读不出设置时不显示(网络代理那一行已给出重试)。
function GitHubMirrorSetting() {
  const t = useT().settings;
  const { data: on } = useQuery(githubMirrorQuery);
  const [saving, setSaving] = useState(false);
  if (on === undefined) return null;
  const toggle = async (next: boolean) => {
    setSaving(true);
    const ok = await ui.run(() => LibraryService.SetGitHubMirror(next).then(() => true), { reload: false });
    setSaving(false);
    if (ok) queryClient.setQueryData(githubMirrorQuery.queryKey, next);
  };
  return (
    <SetRow className="github-mirror-row">
      <Tipped className="grow" tip={t.githubMirrorDesc}>
        {t.githubMirror}
      </Tipped>
      <Switch checked={on} disabled={saving} aria-label={t.githubMirror} onCheckedChange={(v) => void toggle(v)} />
    </SetRow>
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
        {/* 一行：项目名，后面是它的目录(放不下就截断，悬停看全)。 */}
        <span className="max-w-1/2 shrink-0 truncate">{projectName(s, p.id)}</span>
        <WSLBadge path={p.path} />
        <span className="min-w-0 grow truncate font-mono text-caption text-muted-foreground select-text" title={p.path}>
          {shortPath(s, p.path)}
        </span>
        {p.missing && (
          <Badge tone="critical" title={t.missingTitle}>
            {t.missing}
          </Badge>
        )}
        <Button variant="ghost" onClick={() => ui.open((close) => <ProjectPathDialog home={s.info.home_dir} project={p} onClose={close} />)}>
          {t.relocate}
        </Button>
        <Button variant="ghost" onClick={() => void unregisterProject(p)}>
          {t.unregister}
        </Button>
      </SetRow>
    ))}
    {s.projects.length === 0 && <Hint className="py-3">{t.noProjects}</Hint>}
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
  <dl className="kv grid grid-cols-[110px_1fr] items-center gap-x-3 text-body [&_dd]:min-h-10 [&_dd]:border-t [&_dd]:border-row-line [&_dd]:py-2 [&_dd:first-of-type]:border-t-0 [&_dt]:flex [&_dt]:h-full [&_dt]:items-center [&_dt]:border-t [&_dt]:border-row-line [&_dt]:text-muted-foreground [&_dt:first-of-type]:border-t-0">
    <dt>{t.version}</dt>
    <dd className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
      <span className="font-mono select-text">{s.info.version}</span>
      <ReleaseCheck />
    </dd>
    <dt>{t.databasePath}</dt>
    <dd className="flex items-center font-mono text-caption select-text">
      <PathText path={s.info.library_path} className="min-w-0" />
    </dd>
  </dl>
  );
}


function SettingRow({ label, desc, on, onChange }: { label: string; desc: string; on: boolean; onChange: (on: boolean) => void }) {
  return (
    <SetRow>
      <Tipped className="grow" tip={desc}>
        {label}
      </Tipped>
      <Switch checked={on} onCheckedChange={onChange} />
    </SetRow>
  );
}
