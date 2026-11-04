// Mechbox 的新版本：更新说明与更新的对话框，设置页「关于」里的「检查新版本」，以及 macOS 顶部菜单「检查新版本…」的结果。
// 左栏的新版本一行在 left-sidebar。
// 检查、下载、校验与装上都在 Go 端(AppService.CheckRelease / InstallRelease);不能在 app 里直接装的(Release.manual)只打开下载页。
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Browser, Events } from "@wailsio/runtime";
import { AppService, ManualReason, type Release } from "@bindings/cmd/desktop";
import { ErrorCode } from "@bindings/internal/library";
import { formatDate, formatSize, renderMarkdown } from "@/lib/content";
import { describeError } from "@/lib/errors";
import { invalidateRelease, releaseQuery, toLibraryError, useLoadedSnapshot, type LibraryError } from "@/lib/library";
import { messages, useT } from "@/lib/i18n";
import { platform } from "@/lib/platform";
import { Modal, ui, useBusy } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";

// Wails 的 updater 下载时发的进度事件。
const progressEvent = "wails:updater:download-progress";

type Progress = { written: number; total: number };

// releaseError 说明检查或更新为什么没成：during 是 check 时只说读没读到，install 时还要说当前版本不变。
function releaseError(err: LibraryError, during: "check" | "install"): string {
  const t = messages().release;
  if (err.code === ErrorCode.Busy) return t.busyInstalling;
  if (err.code === ErrorCode.SourceUnreachable) return `${t.unreachable}\n${err.message}`;
  if (during === "check") return err.code === ErrorCode.SourceInvalid ? `${t.unreadable}\n${err.message}` : describeError(err);
  if (err.code === ErrorCode.SourceInvalid) return `${t.invalid}\n${err.message}`;
  return `${t.failed}\n${describeError(err)}`;
}

function manualText(reason: ManualReason): string {
  const t = messages().release;
  switch (reason) {
    case ManualReason.ManualPackage:
      return t.manualPackage;
    case ManualReason.ManualLocation:
      return t.manualLocation();
  }
  return t.manualAsset;
}

// releaseOpen 是新版本的对话框开没开着：左栏、菜单都能打开它，开着时不再叠一个。
let releaseOpen = false;

// openRelease 打开新版本的对话框：更新说明，以及「更新」或「打开下载页」;已经开着时什么都不做。
export function openRelease(release: Release) {
  if (releaseOpen) return;
  releaseOpen = true;
  void ui.open((close) => <ReleaseDialog release={release} onClose={close} />).finally(() => {
    releaseOpen = false;
  });
}

// 对话框开着时后台又检查到更晚的版本，就换成显示它;「更新」带上显示的版本，Go 端只装这一个。
// 技能库快照可能还没读到(从 macOS 菜单打开时)，这时不写当前版本那一行。
function ReleaseDialog({ release: opened, onClose }: { release: Release; onClose: () => void }) {
  const t = useT().release;
  const s = useLoadedSnapshot();
  const busy = useBusy();
  const status = useQuery(releaseQuery).data;
  const failed = status?.failed;
  const release = status?.latest ?? opened;
  const call = useRef<ReturnType<typeof AppService.InstallRelease> | null>(null);
  const [installing, setInstalling] = useState(false);
  const [progress, setProgress] = useState<Progress | null>(null);
  const [error, setError] = useState<string>();
  useEffect(
    () =>
      Events.On(progressEvent, (e) => {
        const p = (Array.isArray(e.data) ? e.data[0] : e.data) as Progress | undefined;
        if (p) setProgress(p);
      }),
    [],
  );
  const manual = release.manual ? manualText(release.manual) : undefined;
  const notes = release.notes.replace(/\r\n/g, "\n").trim();

  // 成功时 Go 端随即退出 app,这次调用不一定等得到返回;失败或停止下载时当前版本不变，可以再点一次。
  const install = async () => {
    setInstalling(true);
    setProgress(null);
    setError(undefined);
    try {
      call.current = AppService.InstallRelease(release.version);
      await call.current;
    } catch (e) {
      const err = toLibraryError(e);
      if (err.code !== ErrorCode.Canceled) setError(releaseError(err, "install"));
      setInstalling(false);
    } finally {
      call.current = null;
    }
  };

  const downloading = progress && (!progress.total || progress.written < progress.total);
  // 下载完以后在校验、装上，停不下来，对话框锁住;之前(含还没收到进度时)关掉对话框就停止下载。
  const locked = installing && !!progress && !downloading;
  const close = () => {
    if (locked) return;
    void call.current?.cancel();
    onClose();
  };
  const progressText = !installing
    ? undefined
    : downloading
      ? t.downloading(formatSize(progress.written), progress.total ? formatSize(progress.total) : "")
      : progress
        ? t.installing
        : messages().common.working;

  return (
    <Modal
      wide
      title={t.title(release.version)}
      onClose={close}
      footer={
        <>
          {progressText && <span className="release-status mr-auto text-[11px] text-muted-foreground">{progressText}</span>}
          <Button onClick={close} disabled={locked}>
            {installing ? t.stop : t.later}
          </Button>
          {manual ? (
            <Button variant="primary" onClick={() => void Browser.OpenURL(release.page_url)}>
              {t.openPage}
            </Button>
          ) : (
            <Button variant="primary" loading={installing} disabled={installing || busy} onClick={() => void install()}>
              {t.update}
            </Button>
          )}
        </>
      }
    >
      {s && <Hint>{t.current(s.info.version, release.version, formatDate(release.published_at))}</Hint>}
      {notes ? (
        <div className="md max-h-[45vh] overflow-auto select-text" dangerouslySetInnerHTML={{ __html: renderMarkdown(notes) }} />
      ) : (
        <Hint>{t.noNotes}</Hint>
      )}
      {failed?.version === release.version && <Notice tone="warn">{t.lastFailed(failed.version, failed.reason ?? "")}</Notice>}
      {manual ? <Notice tone="warn">{manual}</Notice> : <Hint>{platform() === "windows" ? t.uac : t.restartNote}</Hint>}
      {error && <Notice tone="bad" className="whitespace-pre-line select-text">{error}</Notice>}
    </Modal>
  );
}

// checkReleaseFromMenu 是点了 macOS 顶部菜单的「检查新版本…」:检查时顶栏的状态圈转，有新版本时打开它的对话框，
// 已是最新或没检查成时用 toast 说明。新版本的对话框已经开着(含正在下载、装上)时只是把窗口带到前面，不再检查。
export async function checkReleaseFromMenu() {
  if (releaseOpen) return;
  const t = messages().release;
  const latest = await ui.run(() => AppService.CheckRelease(), {
    reload: false,
    success: (found) => (found ? undefined : t.latestAlready),
    onError: (err) => (ui.toast(releaseError(err, "check"), "error"), true),
  });
  await invalidateRelease();
  if (latest) openRelease(latest);
}

// ReleaseCheck 是「关于」里应用版本旁边的「检查新版本」与检查的结果;这个构建不检查新版本时只写一句说明。
export function ReleaseCheck() {
  const t = useT().release;
  const status = useQuery(releaseQuery).data;
  const [checking, setChecking] = useState(false);
  const [checked, setChecked] = useState(false);
  const [error, setError] = useState<string>();
  if (status && !status.checks) return <Hint>{t.notChecked}</Hint>;

  const check = async () => {
    setChecking(true);
    setError(undefined);
    try {
      await AppService.CheckRelease();
      setChecked(true);
    } catch (e) {
      const err = toLibraryError(e);
      if (err.code !== ErrorCode.Canceled) setError(releaseError(err, "check"));
    }
    await invalidateRelease();
    setChecking(false);
  };

  const latest = status?.latest;
  return (
    <div className="release-check flex flex-col items-start gap-1.5">
      <div className="flex items-center gap-2">
        <Button size="sm" loading={checking} disabled={checking} onClick={() => void check()}>
          {checking ? t.checking : t.check}
        </Button>
        {latest ? (
          <span className="release-found">
            {t.found(latest.version)}{" "}
            <Button variant="link" onClick={() => openRelease(latest)}>
              {t.view}
            </Button>
          </span>
        ) : (
          checked && !error && <span className="release-up-to-date text-muted-foreground">{t.upToDate}</span>
        )}
      </div>
      {error && <Notice tone="bad" className="whitespace-pre-line select-text">{error}</Notice>}
    </div>
  );
}

// useInstallFailureNotice 在启动后提示一次上一次「更新」没装上：新版本在 app 退出后才装，装没装上到这次启动才知道。
export function useInstallFailureNotice() {
  const failed = useQuery(releaseQuery).data?.failed;
  const shown = useRef(false);
  useEffect(() => {
    if (!failed || shown.current) return;
    shown.current = true;
    ui.toast(messages().release.lastFailed(failed.version, failed.reason ?? ""), "error");
  }, [failed]);
}
