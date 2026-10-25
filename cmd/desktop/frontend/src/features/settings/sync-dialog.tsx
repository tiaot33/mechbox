// 同步：整库与 S3 手动推 / 拉。先看同步计划认出 S3 上是哪一份库，用户确认后带着它的标识执行;期间 S3 上的库变了返回 Conflict。
// 拉回不动 Agent 目录：之后把不在这台机器上的项目改到本机位置，再在各作用域重新安装。
// 拉回的库带着推送那台机器的设置(界面语言等),拉回后界面语言随之切换。
import { useState } from "react";
import { AppService, LibraryService, type ConfigStatus } from "@bindings/cmd/desktop";
import {
  CannotPullReason,
  ErrorCode,
  type RemoteLibrary,
  type SyncPullPlan,
  type SyncPushPlan,
} from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import { messages, toLanguage, useLanguage, useT } from "@/lib/i18n";
import { Modal, useUI } from "@/lib/ui";
import { openSettings } from "@/lib/view";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";

type Plan = { direction: "push"; plan: SyncPushPlan } | { direction: "pull"; plan: SyncPullPlan };

function cannotPull(reason: string): string | undefined {
  const t = messages().sync;
  const known: Record<string, string> = {
    [CannotPullReason.CannotPullNoRemote]: t.noRemote,
    [CannotPullReason.CannotPullNotALibrary]: t.notALibrary,
    [CannotPullReason.CannotPullNewerApp]: t.newerApp,
    [CannotPullReason.CannotPullOlderApp]: t.olderApp,
  };
  return known[reason];
}

// remoteText 让用户认出 S3 上是哪一份库:"MacBook-Pro 于 2026-10-01 12:00 推送"。
function remoteText(remote: RemoteLibrary): string {
  const t = messages().sync;
  const from = remote.pushed_from || t.unknownMachine;
  return remote.pushed_at ? t.pushedAt(from, formatDate(remote.pushed_at)) : t.pushed(from);
}

export function SyncDialog({ config, onClose }: { config: ConfigStatus; onClose: () => void }) {
  const ui = useUI();
  const t = useT().sync;
  const [plan, setPlan] = useState<Plan | null>(null);
  const [working, setWorking] = useState<string | null>(null);
  const [pulled, setPulled] = useState(false);
  const { setLanguage } = useLanguage();

  const load = async (direction: Plan["direction"]) => {
    setWorking(direction);
    setPulled(false);
    const next =
      direction === "push"
        ? await ui.run(() => LibraryService.PlanSyncPush(), { reload: false }).then((p) => p && ({ direction, plan: p } as const))
        : await ui.run(() => LibraryService.PlanSyncPull(), { reload: false }).then((p) => p && ({ direction, plan: p } as const));
    setWorking(null);
    if (next) setPlan(next);
  };

  const onConflict = (err: { code: ErrorCode }) => {
    if (err.code !== ErrorCode.Conflict) return false;
    ui.toast(t.conflict, "error");
    setPlan(null);
    return true;
  };

  const execute = async () => {
    if (!plan) return;
    setWorking("run");
    const identity = plan.plan.remote?.identity ?? "";
    if (plan.direction === "push") {
      const remote = await ui.run(() => LibraryService.SyncPush(identity), {
        success: (r) => t.pushDone(remoteText(r)),
        onError: onConflict,
      });
      if (remote) setPlan(null);
    } else {
      const done = await ui.run(() => LibraryService.SyncPull(identity).then(() => true), {
        success: t.pullDone,
        onError: onConflict,
      });
      if (done) {
        setPlan(null);
        setPulled(true);
        await AppService.Language().then((l) => setLanguage(toLanguage(l)), () => {});
      }
    }
    setWorking(null);
  };

  const remote = plan?.plan.remote;
  const pull = plan?.direction === "pull" ? plan.plan : null;
  const missing = (pull?.projects ?? []).filter((p) => p.missing);
  return (
    <Modal
      title={t.title}
      onClose={onClose}
      footer={
        plan ? (
          <>
            <Button onClick={() => setPlan(null)}>{t.back}</Button>
            <span className="grow" />
            <Button
              variant={pull ? "danger" : "primary"}
              disabled={(pull ? !pull.can_pull : false) || (working !== null && working !== "run")}
              loading={working === "run"}
              onClick={execute}
            >
              {pull ? t.pullConfirm : remote ? t.pushOverwrite : t.push}
            </Button>
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
      {!plan && (
        <div className="flex items-center gap-2">
          <Button className="grow" disabled={!config.sync || (working !== null && working !== "push")} loading={working === "push"} onClick={() => load("push")}>
            {t.pushTo}
          </Button>
          <Button className="grow" disabled={!config.sync || (working !== null && working !== "pull")} loading={working === "pull"} onClick={() => load("pull")}>
            {t.pullFrom}
          </Button>
        </div>
      )}
      {pulled && (
        <Notice tone="ok">{t.pulled}</Notice>
      )}
      {plan && (
        <>
          <dl className="kv grid grid-cols-[110px_1fr] gap-x-3 gap-y-1.5 text-xs [&_dt]:text-muted-foreground">
            <dt>{t.direction}</dt>
            <dd>{pull ? t.toLocal : t.toRemote}</dd>
            <dt>{t.remote}</dt>
            <dd>{remote ? remoteText(remote) : t.none}</dd>
            {pull?.can_pull && (
              <>
                <dt>{t.contents}</dt>
                <dd>{t.counts(pull.skills, pull.groups, pull.projects?.length ?? 0)}</dd>
              </>
            )}
          </dl>
          {!pull && remote && <Notice tone="warn">{t.pushWarning}</Notice>}
          {pull && !pull.can_pull && <Notice tone="bad">{cannotPull(pull.cannot_pull ?? "") ?? t.cannotPull}</Notice>}
          {pull?.can_pull && <Notice tone="warn">{t.pullWarning}</Notice>}
          {missing.length > 0 && <Notice>{t.missingProjects(missing.map((p) => p.name))}</Notice>}
        </>
      )}
    </Modal>
  );
}
