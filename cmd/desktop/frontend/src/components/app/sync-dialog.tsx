// 同步：整库与 S3 手动推 / 拉。先看同步计划拿到远端身份，用户确认后带着身份执行;远端期间变了返回 Conflict。
import { useState } from "react";
import { LibraryService, type ConfigStatus } from "@bindings/cmd/desktop";
import { ErrorCode, type SyncPlan } from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";

type Direction = "push" | "pull";

export function SyncDialog({ config, onClose }: { config: ConfigStatus; onClose: () => void }) {
  const ui = useUI();
  const [plan, setPlan] = useState<{ direction: Direction; plan: SyncPlan } | null>(null);
  const [working, setWorking] = useState<string | null>(null);
  const [warnings, setWarnings] = useState<string[]>([]);

  const load = async (direction: Direction) => {
    setWorking(direction);
    setWarnings([]);
    const p = await ui.run(
      () => (direction === "push" ? LibraryService.PlanSyncPush() : LibraryService.PlanSyncPull()),
      { reload: false },
    );
    setWorking(null);
    if (p) setPlan({ direction, plan: p });
  };

  const execute = async () => {
    if (!plan) return;
    const { direction, plan: p } = plan;
    setWorking("run");
    const result = await ui.run(
      () => (direction === "push" ? LibraryService.SyncPush(p.remote) : LibraryService.SyncPull(p.remote)),
      {
        success: direction === "push" ? "已推送到 S3" : "已从 S3 拉回并替换本地技能库",
        onError: (err) => {
          if (err.code !== ErrorCode.Conflict) return false;
          ui.toast("远端在生成同步计划之后已变化，请重新查看同步计划再确认", "error");
          setPlan(null);
          return true;
        },
      },
    );
    setWorking(null);
    if (result) {
      setPlan(null);
      setWarnings(result.warnings ?? []);
    }
  };

  const p = plan?.plan;
  const pullBlocked = plan?.direction === "pull" && (!p?.remote_exists || !p?.schema_ok);
  return (
    <Modal
      title="同步"
      onClose={onClose}
      footer={
        plan ? (
          <>
            <button type="button" className="pill-btn" onClick={() => setPlan(null)}>
              返回
            </button>
            <span className="grow" />
            <button
              type="button"
              className={`pill-btn ${plan.direction === "pull" ? "danger" : "accent"}`}
              disabled={pullBlocked || working !== null}
              onClick={execute}
            >
              {working === "run" ? <span className="spin" /> : null}
              {plan.direction === "push" ? "确认推送" : "确认拉取并替换本地库"}
            </button>
          </>
        ) : undefined
      }
    >
      <p className="hint">
        手动把整个技能库推送到 S3,或从 S3 拉回，在另一台机器上还原同样的配置。不会自动进行。
      </p>
      {!config.sync && (
        <div className="notice warn">
          未配置 S3:需要环境变量 SKILLS_S3_BUCKET、AWS_ACCESS_KEY_ID、AWS_SECRET_ACCESS_KEY(可选
          SKILLS_S3_ENDPOINT、SKILLS_S3_KEY、AWS_REGION、AWS_SESSION_TOKEN)。从 Finder 启动的应用读不到
          shell 里的环境变量。
        </div>
      )}
      {!plan && (
        <div className="row">
          <button type="button" className="pill-btn grow" disabled={working !== null} onClick={() => load("push")}>
            {working === "push" ? <span className="spin" /> : null}
            推送到 S3…
          </button>
          <button type="button" className="pill-btn grow" disabled={working !== null} onClick={() => load("pull")}>
            {working === "pull" ? <span className="spin" /> : null}
            从 S3 拉取…
          </button>
        </div>
      )}
      {plan && p && (
        <>
          <dl className="kv">
            <dt>方向</dt>
            <dd>{plan.direction === "push" ? "本地 → S3" : "S3 → 本地"}</dd>
            <dt>远端</dt>
            <dd>{p.remote_exists ? <span className="mono tiny">{p.remote}</span> : "远端还没有技能库"}</dd>
            {plan.direction === "pull" && (
              <>
                <dt>远端版本</dt>
                <dd>{p.schema_ok ? "可以拉取" : "不兼容，无法拉取"}</dd>
              </>
            )}
          </dl>
          {plan.direction === "push" && p.remote_exists && (
            <div className="notice warn">推送会覆盖远端已有的技能库。</div>
          )}
          {plan.direction === "pull" && p.remote_exists && p.schema_ok && (
            <div className="notice warn">
              拉取会整体替换本地技能库。磁盘上已安装的文件夹不会随之重建，拉取后请检查各作用域并按需"全部重新安装"。
            </div>
          )}
          {plan.direction === "pull" && !p.remote_exists && <div className="notice bad">远端没有可拉取的技能库。</div>}
          {(p.warnings ?? []).map((w) => (
            <div key={w} className="notice warn">
              {w}
            </div>
          ))}
        </>
      )}
      {warnings.map((w) => (
        <div key={w} className="notice warn">
          {w}
        </div>
      ))}
    </Modal>
  );
}
