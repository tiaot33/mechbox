// 同步：推送把整库存成 S3 上这台机器的一份新备份，点了就推，不覆盖任何备份;库没变时不新建。
// 拉回先列出所有机器的备份，默认选别的机器里最新的一份(新机器开着自动推送时，本机刚推的那份几乎是空的),看它的同步计划，用户确认后用它替换本机的库;列出之后那份备份被删掉或改写返回 Conflict,重新列出。
// 拉回不动 Agent 目录：之后把不在这台机器上的项目改到本机位置，再在各作用域重新安装。
// 拉回的库带着推送那台机器的设置(界面语言等),拉回后界面语言随之切换;这台机器上拖出来的集合与组的先后清掉，旧库的 ID 对不上新库。
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AppService, LibraryService, type ConfigStatus } from "@bindings/cmd/desktop";
import { CannotPullReason, ErrorCode, type SyncBackup, type SyncBackupID, type SyncPullPlan } from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import { describeError } from "@/lib/errors";
import { invalidateSyncStatus, syncSettingsQuery, type LibraryError } from "@/lib/library";
import { messages, toLanguage, useLanguage, useT } from "@/lib/i18n";
import { Modal, useUI } from "@/lib/ui";
import { clearPicked, forgetOrder, openSettings } from "@/lib/view";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { SyncStatusLine } from "./credentials-form";

function cannotPull(reason: string): string | undefined {
  const t = messages().sync;
  const known: Record<string, string> = {
    [CannotPullReason.CannotPullNotALibrary]: t.notALibrary,
    [CannotPullReason.CannotPullNewerApp]: t.newerApp,
    [CannotPullReason.CannotPullOlderApp]: t.olderApp,
  };
  return known[reason];
}

// Pull 是拉回的那一步：列出的备份、选中的那份与它的同步计划(选中后才去下载校验)。
type Pull = { backups: SyncBackup[]; selected: SyncBackupID | null; plan: SyncPullPlan | null };

export function SyncDialog({ config, onClose }: { config: ConfigStatus; onClose: () => void }) {
  const ui = useUI();
  const t = useT().sync;
  const [pull, setPull] = useState<Pull | null>(null);
  const [working, setWorking] = useState<string | null>(null);
  const [pulled, setPulled] = useState(false);
  const { setLanguage } = useLanguage();
  const settings = useQuery({ ...syncSettingsQuery, enabled: config.sync }).data;

  const push = async () => {
    setWorking("push");
    setPulled(false);
    const result = await ui.run(() => LibraryService.SyncPush(), {
      success: (r) => (r.remove_error ? undefined : r.unchanged ? t.pushUnchanged : t.pushDone(r.backup.device)),
    });
    if (result?.remove_error) ui.toast(t.pushedButNotRemoved(describeError(result.remove_error as LibraryError)), "error");
    void invalidateSyncStatus();
    setWorking(null);
  };

  const choose = async (backups: SyncBackup[], id: SyncBackupID) => {
    setPull({ backups, selected: id, plan: null });
    setWorking("plan");
    const plan = await ui.run(() => LibraryService.PlanSyncPull(id), { reload: false, onError: onConflict });
    setWorking(null);
    if (plan) setPull((p) => (p?.selected === id ? { ...p, plan } : p));
  };

  const list = async () => {
    setWorking("list");
    setPulled(false);
    const backups = await ui.run(() => LibraryService.ListSyncBackups().then((b) => b ?? []), { reload: false });
    setWorking(null);
    if (!backups) return;
    if (backups.length === 0) {
      setPull({ backups, selected: null, plan: null });
      return;
    }
    await choose(backups, (backups.find((b) => !b.this_device) ?? backups[0]).id);
  };

  const onConflict = (err: { code: ErrorCode }) => {
    if (err.code !== ErrorCode.Conflict) return false;
    ui.toast(t.conflict, "error");
    void list();
    return true;
  };

  const execute = async () => {
    if (!pull?.selected) return;
    const id = pull.selected;
    setWorking("run");
    const done = await ui.run(() => LibraryService.SyncPull(id).then(() => true), {
      success: t.pullDone,
      onError: onConflict,
    });
    setWorking(null);
    if (done) {
      // 拉回的是另一份库：旧库的 ID 会套到新库的别的技能上，先后与勾选都作废。
      forgetOrder();
      clearPicked();
      setPull(null);
      setPulled(true);
      await AppService.Language().then((l) => setLanguage(toLanguage(l)), () => {});
    }
  };

  const plan = pull?.plan;
  const missing = (plan?.projects ?? []).filter((p) => p.missing);
  return (
    <Modal
      title={t.title}
      onClose={onClose}
      footer={
        pull ? (
          <>
            <Button onClick={() => setPull(null)}>{t.back}</Button>
            <span className="grow" />
            {pull.backups.length > 0 && (
              <Button
                variant="danger"
                disabled={!plan?.can_pull || (working !== null && working !== "run")}
                loading={working === "run"}
                onClick={execute}
              >
                {t.pullConfirm}
              </Button>
            )}
          </>
        ) : undefined
      }
    >
      <Hint>{t.intro}</Hint>
      {!config.sync && (
        <Notice tone="warn">
          {t.notConfigured}
          <div className="mt-1.5">
            <Button
              onClick={() => {
                onClose();
                openSettings("sync");
              }}
            >
              {t.openSettings}
            </Button>
          </div>
        </Notice>
      )}
      {/* 没配置时推、拉都只会报缺少配置，按钮置灰，从上面的"去设置"填。 */}
      {!pull && (
        <div className="flex items-center gap-2">
          <Button className="grow" disabled={!config.sync || (working !== null && working !== "push")} loading={working === "push"} onClick={push}>
            {t.pushTo}
          </Button>
          <Button className="grow" disabled={!config.sync || (working !== null && working !== "list")} loading={working === "list"} onClick={list}>
            {t.pullFrom}
          </Button>
        </div>
      )}
      {config.sync && !pull && <SyncStatusLine folder={settings?.folder ?? ""} />}
      {pulled && <Notice tone="ok">{t.pulled}</Notice>}
      {pull && pull.backups.length === 0 && <Notice>{t.noBackups([settings?.bucket, settings?.folder].filter(Boolean).join("/") || "S3")}</Notice>}
      {pull && pull.backups.length > 0 && (
        <>
          <Hint>{t.chooseBackup}</Hint>
          <ListBox scroll className="max-h-56">
            {pull.backups.map((b) => (
              <ListRow
                key={b.id}
                compact
                clickable
                selected={b.id === pull.selected}
                className="sync-backup"
                onClick={() => b.id !== pull.selected && working !== "run" && void choose(pull.backups, b.id)}
              >
                <span className="min-w-0 truncate font-mono text-xs">{b.device}</span>
                {b.this_device && <Badge>{t.thisDevice}</Badge>}
                <span className="grow" />
                <span className="shrink-0 text-xs text-muted-foreground">{formatDate(b.pushed_at)}</span>
              </ListRow>
            ))}
          </ListBox>
          {plan?.can_pull && (
            <dl className="kv grid grid-cols-[110px_1fr] gap-x-3 gap-y-1.5 text-xs [&_dt]:text-muted-foreground">
              <dt>{t.contents}</dt>
              <dd>{t.counts(plan.skills, plan.groups, plan.projects?.length ?? 0)}</dd>
            </dl>
          )}
          {plan && !plan.can_pull && <Notice tone="bad">{cannotPull(plan.cannot_pull ?? "") ?? t.cannotPull}</Notice>}
          {plan?.can_pull && <Notice tone="warn">{t.pullWarning}</Notice>}
          {missing.length > 0 && <Notice>{t.missingProjects(missing.map((p) => p.name))}</Notice>}
        </>
      )}
    </Modal>
  );
}
