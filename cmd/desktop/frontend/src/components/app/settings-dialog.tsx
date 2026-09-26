// 设置：只读展示技能库位置、支持的 Agent 与外部配置状态。
// 外部配置目前只能来自环境变量;在界面里修改配置另有 issue。
import type { ConfigStatus } from "@bindings/cmd/desktop";
import type { Snapshot } from "@/lib/library";
import { shortPath } from "@/lib/model";
import { Modal } from "@/lib/ui";
import { AgentMark } from "./parts";

export function SettingsDialog({ s, config, onClose }: { s: Snapshot; config: ConfigStatus; onClose: () => void }) {
  const state = (ok: boolean, yes: string, no: string) => <span className={`status ${ok ? "ok" : "warn"}`}>{ok ? yes : no}</span>;
  return (
    <Modal title="设置" onClose={onClose}>
      <section className="card">
        <div className="card-h">
          <span>技能库</span>
        </div>
        <dl className="kv">
          <dt>数据库文件</dt>
          <dd className="mono tiny" style={{ userSelect: "text", wordBreak: "break-all" }}>
            {s.info.library_path}
          </dd>
          <dt>规模</dt>
          <dd>
            {s.skills.length} 个技能 · {s.groups.length} 个组 · {s.projects.length} 个已登记项目 · {s.projections.length} 个投影
          </dd>
        </dl>
      </section>
      <section className="card">
        <div className="card-h">
          <span>Agent</span>
          <span className="muted tiny">由技能库内置，全局目录相对 {shortPath(s, s.info.home_dir)}</span>
        </div>
        {s.agents.map((a) => (
          <div key={a.code} className="scope-cell" style={{ padding: "3px 0" }}>
            <AgentMark code={a.code} name={a.name} />
            {a.name}
            <span className="mono tiny muted">{a.code}</span>
          </div>
        ))}
      </section>
      <section className="card">
        <div className="card-h">
          <span>外部配置</span>
          <span className="muted tiny">读取自环境变量</span>
        </div>
        <dl className="kv">
          <dt>S3 同步</dt>
          <dd>{state(config.sync, "已配置", "未配置")} SKILLS_S3_BUCKET、AWS_ACCESS_KEY_ID、AWS_SECRET_ACCESS_KEY</dd>
          <dt>商店来源</dt>
          <dd>{state(config.store_url, "已配置", "未配置")} SKILLS_STORE_URL</dd>
          <dt>GitHub 令牌</dt>
          <dd>{state(config.github_token, "已配置", "匿名访问")} GITHUB_TOKEN 或 GH_TOKEN</dd>
        </dl>
        <p className="hint">
          从 Finder / Dock 启动的应用读不到 shell 里设置的环境变量。需要这些配置时，可在终端里运行
          bin/skills-manager.app/Contents/MacOS/skills-manager;在界面中配置将在后续版本提供。
        </p>
      </section>
    </Modal>
  );
}
