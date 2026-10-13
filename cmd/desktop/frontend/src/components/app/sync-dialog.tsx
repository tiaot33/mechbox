// 同步：整库与 S3 手动推 / 拉。先看同步计划认出 S3 上是哪一份库，用户确认后带着它的标识执行;期间 S3 上的库变了返回 Conflict。
// 拉回不动 Agent 目录：之后把不在这台机器上的项目改到本机位置，再在各作用域重新安装。
import { useState } from "react";
import { LibraryService, type ConfigStatus } from "@bindings/cmd/desktop";
import {
  CannotPullReason,
  ErrorCode,
  type RemoteLibrary,
  type SyncPullPlan,
  type SyncPushPlan,
} from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import { Modal, useUI } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";

type Plan = { direction: "push"; plan: SyncPushPlan } | { direction: "pull"; plan: SyncPullPlan };

const cannotPull: Record<string, string> = {
  [CannotPullReason.CannotPullNoRemote]: "S3 上还没有库：还没有哪台机器推送过。",
  [CannotPullReason.CannotPullNotALibrary]: "S3 上的文件不是技能库，或已损坏。",
  [CannotPullReason.CannotPullNewerApp]: "S3 上的库来自较新版本的应用：先把这台机器上的应用升级。",
  [CannotPullReason.CannotPullOlderApp]: "S3 上的库来自较旧版本的应用：先在推送它的机器上升级应用，再推送一次。",
};

// remoteText 让用户认出 S3 上是哪一份库:"MacBook-Pro 于 2026-10-01 12:00 推送"。
function remoteText(remote: RemoteLibrary): string {
  const from = remote.pushed_from || "未知机器";
  return remote.pushed_at ? `${from} 于 ${formatDate(remote.pushed_at)} 推送` : `${from} 推送`;
}

export function SyncDialog({ config, onClose }: { config: ConfigStatus; onClose: () => void }) {
  const ui = useUI();
  const [plan, setPlan] = useState<Plan | null>(null);
  const [working, setWorking] = useState<string | null>(null);
  const [pulled, setPulled] = useState(false);

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
    ui.toast("S3 上的库在查看同步计划之后变了，请重新查看同步计划再确认", "error");
    setPlan(null);
    return true;
  };

  const execute = async () => {
    if (!plan) return;
    setWorking("run");
    const identity = plan.plan.remote?.identity ?? "";
    if (plan.direction === "push") {
      const remote = await ui.run(() => LibraryService.SyncPush(identity), {
        success: (r) => `已推送到 S3(${remoteText(r)})`,
        onError: onConflict,
      });
      if (remote) setPlan(null);
    } else {
      const done = await ui.run(() => LibraryService.SyncPull(identity).then(() => true), {
        success: "已从 S3 拉回并替换本机的技能库",
        onError: onConflict,
      });
      if (done) {
        setPlan(null);
        setPulled(true);
      }
    }
    setWorking(null);
  };

  const remote = plan?.plan.remote;
  const pull = plan?.direction === "pull" ? plan.plan : null;
  const missing = (pull?.projects ?? []).filter((p) => p.missing);
  return (
    <Modal
      title="同步"
      onClose={onClose}
      footer={
        plan ? (
          <>
            <Button onClick={() => setPlan(null)}>返回</Button>
            <span className="grow" />
            <Button
              variant={pull ? "danger" : "primary"}
              disabled={(pull ? !pull.can_pull : false) || (working !== null && working !== "run")}
              loading={working === "run"}
              onClick={execute}
            >
              {pull ? "拉回并替换本机的库" : remote ? "推送并覆盖 S3 上的库" : "推送"}
            </Button>
          </>
        ) : undefined
      }
    >
      <Hint>手动把整个技能库推送到 S3,或从 S3 拉回，在另一台机器上还原同样的配置。不会自动进行。</Hint>
      {!config.sync && (
        <Notice tone="warn">
          未配置 S3:在设置页的同步分区填写存储桶、Access Key ID 和 Secret Access Key,或设置环境变量
          SKILLS_S3_BUCKET、AWS_ACCESS_KEY_ID、AWS_SECRET_ACCESS_KEY(可选 SKILLS_S3_ENDPOINT、SKILLS_S3_KEY、
          AWS_REGION、AWS_SESSION_TOKEN)。
        </Notice>
      )}
      {!plan && (
        <div className="flex items-center gap-2">
          <Button className="grow" disabled={working !== null && working !== "push"} loading={working === "push"} onClick={() => load("push")}>
            推送到 S3…
          </Button>
          <Button className="grow" disabled={working !== null && working !== "pull"} loading={working === "pull"} onClick={() => load("pull")}>
            从 S3 拉回…
          </Button>
        </div>
      )}
      {pulled && (
        <Notice tone="ok">
          已换成 S3 上的库。Agent 目录里的文件夹没有动：右栏里标着"不在这台机器上"的项目，先改项目位置指到本机;
          再在各作用域查看状态，按需"全部重新安装"。
        </Notice>
      )}
      {plan && (
        <>
          <dl className="kv grid grid-cols-[110px_1fr] gap-x-3 gap-y-1.5 text-xs [&_dt]:text-muted-foreground">
            <dt>方向</dt>
            <dd>{pull ? "S3 → 本机" : "本机 → S3"}</dd>
            <dt>S3 上的库</dt>
            <dd>{remote ? remoteText(remote) : "还没有"}</dd>
            {pull?.can_pull && (
              <>
                <dt>里面有</dt>
                <dd>
                  {pull.skills} 个技能 · {pull.groups} 个组 · {pull.projects?.length ?? 0} 个已登记项目
                </dd>
              </>
            )}
          </dl>
          {!pull && remote && <Notice tone="warn">推送会用本机的库覆盖 S3 上的这一份。</Notice>}
          {pull && !pull.can_pull && <Notice tone="bad">{cannotPull[pull.cannot_pull ?? ""] ?? "S3 上的库不能拉回。"}</Notice>}
          {pull?.can_pull && (
            <Notice tone="warn">
              拉回会整体替换本机的技能库，本机有、那份库里没有的技能、组和项目都会消失。Agent 目录里的文件夹不会改动。
            </Notice>
          )}
          {missing.length > 0 && (
            <Notice>这些项目不在这台机器上，拉回后在右栏改项目位置指到本机:{missing.map((p) => p.name).join("、")}</Notice>
          )}
        </>
      )}
    </Modal>
  );
}
