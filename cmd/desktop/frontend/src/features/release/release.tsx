// Mechbox 的新版本：更新说明与更新的对话框，以及设置页「关于」里的「检查新版本」。左栏的新版本一行在 left-sidebar。
// 检查、下载、校验与装上都在 Go 端(AppService.CheckRelease / InstallRelease);不能在 app 里直接装的(Release.manual)只打开下载页。
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Browser, Events } from "@wailsio/runtime";
import { AppService, ManualReason, type Release } from "@bindings/cmd/desktop";
import { ErrorCode } from "@bindings/internal/library";
import { formatDate, formatSize, renderMarkdown } from "@/lib/content";
import { describeError } from "@/lib/errors";
import { invalidateRelease, releaseQuery, toLibraryError, useSnapshot, type LibraryError } from "@/lib/library";
import { messages, useT } from "@/lib/i18n";
import { platform } from "@/lib/platform";
import { Modal, ui } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";

// Wails 的 updater 下载时发的进度事件。
const progressEvent = "wails:updater:download-progress";

type Progress = { written: number; total: number };

function releaseError(err: LibraryError): string {
  const t = messages().release;
  switch (err.code) {
    case ErrorCode.SourceUnreachable:
      return `${t.unreachable}\n${err.message}`;
    case ErrorCode.SourceInvalid:
      return `${t.invalid}\n${err.message}`;
  }
  return `${t.failed}\n${describeError(err)}`;
}

function manualText(reason: ManualReason): string {
  const t = messages().release;
  switch (reason) {
    case ManualReason.ManualPackage:
      return t.manualPackage;
    case ManualReason.ManualLocation:
      return t.manualLocation;
  }
  return t.manualAsset;
}

// openRelease 打开新版本的对话框：更新说明，以及「更新」或「打开下载页」。
export function openRelease(release: Release) {
  void ui.open((close) => <ReleaseDialog release={release} onClose={close} />);
}

function ReleaseDialog({ release, onClose }: { release: Release; onClose: () => void }) {
  const t = useT().release;
  const s = useSnapshot();
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

  // 成功时 Go 端随即退出 app,这次调用不一定等得到返回;失败时当前版本不变，可以再点一次。
  const install = async () => {
    setInstalling(true);
    setProgress(null);
    setError(undefined);
    try {
      await AppService.InstallRelease();
    } catch (e) {
      const err = toLibraryError(e);
      if (err.code !== ErrorCode.Canceled) setError(releaseError(err));
      setInstalling(false);
    }
  };

  const downloading = progress && (!progress.total || progress.written < progress.total);
  const status = !installing
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
      // 下载、装上的途中不能关：关了 app 也会自己重启。
      onClose={installing ? () => {} : onClose}
      footer={
        <>
          {status && <span className="release-status mr-auto text-[11px] text-muted-foreground">{status}</span>}
          <Button onClick={onClose} disabled={installing}>
            {t.later}
          </Button>
          {manual ? (
            <Button variant="primary" onClick={() => void Browser.OpenURL(release.page_url)}>
              {t.openPage}
            </Button>
          ) : (
            <Button variant="primary" loading={installing} disabled={installing} onClick={() => void install()}>
              {t.update}
            </Button>
          )}
        </>
      }
    >
      <Hint>{t.current(s.info.version, formatDate(release.published_at))}</Hint>
      {notes ? (
        <div className="md max-h-[45vh] overflow-auto select-text" dangerouslySetInnerHTML={{ __html: renderMarkdown(notes) }} />
      ) : (
        <Hint>{t.noNotes}</Hint>
      )}
      {manual ? <Notice tone="warn">{manual}</Notice> : <Hint>{platform() === "windows" ? t.uac : t.restartNote}</Hint>}
      {error && <Notice tone="bad" className="whitespace-pre-line select-text">{error}</Notice>}
    </Modal>
  );
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
      if (err.code !== ErrorCode.Canceled) setError(releaseError(err));
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
