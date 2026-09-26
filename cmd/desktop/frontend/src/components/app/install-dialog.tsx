// 安装技能：本地目录直接 Install;GitHub / URL / 商店先 Fetch 暂存，再在 FetchReview 里挑选安装。
import { useState } from "react";
import { Dialogs } from "@wailsio/runtime";
import { LibraryService, type ConfigStatus } from "@bindings/cmd/desktop";
import { SourceType, type Fetch, type SourceSpec } from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";
import { resolveLocalPath } from "@/lib/paths";
import { FetchReview } from "./fetch-review";

type Tab = "local" | "github" | "url" | "store";

const tabs: { key: Tab; label: string }[] = [
  { key: "local", label: "本地目录" },
  { key: "github", label: "GitHub" },
  { key: "url", label: "URL 压缩包" },
  { key: "store", label: "商店" },
];

function splitNames(text: string): string[] | undefined {
  const names = text.split(/[,，\s]+/).map((s) => s.trim()).filter(Boolean);
  return names.length ? names : undefined;
}

// pickDirectory 打开系统目录选择框;取消时返回空串。
export async function pickDirectory(title: string): Promise<string> {
  const picked = await Dialogs.OpenFile({
    Title: title,
    CanChooseDirectories: true,
    CanChooseFiles: false,
    CanCreateDirectories: false,
  });
  return typeof picked === "string" ? picked : (picked[0] ?? "");
}

export function InstallDialog({ config, home, onClose }: { config: ConfigStatus; home: string; onClose: () => void }) {
  const ui = useUI();
  const [tab, setTab] = useState<Tab>("local");
  const [path, setPath] = useState("");
  const [github, setGithub] = useState("");
  const [ref, setRef] = useState("");
  const [url, setURL] = useState("");
  const [packageID, setPackageID] = useState("");
  const [version, setVersion] = useState("");
  const [subpath, setSubpath] = useState("");
  const [names, setNames] = useState("");
  const [working, setWorking] = useState(false);
  const [fetched, setFetched] = useState<Fetch | null>(null);

  if (fetched) return <FetchReview fetch={fetched} onClose={onClose} onBack={() => setFetched(null)} />;

  const local = resolveLocalPath(path, home);
  const installLocal = async () => {
    if (!local?.path) return;
    const dir = local.path;
    setWorking(true);
    const skill = await ui.run(() => LibraryService.Install({ path: dir }), {
      success: (s) => `已安装「${s.name}」`,
    });
    setWorking(false);
    if (skill) onClose();
  };

  // fetchRemote 组出 SourceSpec 后 Fetch;GitHub 先经 ResolveSource 解析输入并发现默认分支。
  const fetchRemote = async () => {
    setWorking(true);
    const result = await ui.run(
      async () => {
        let spec: SourceSpec;
        if (tab === "github") {
          spec = await LibraryService.ResolveSource(github, { ref: ref || undefined, subpath: subpath || undefined });
        } else if (tab === "url") {
          spec = { type: SourceType.SourceURL, locator: { url: url.trim() }, subpath: subpath || undefined };
        } else {
          spec = {
            type: SourceType.SourceStore,
            locator: { package_id: packageID.trim(), version: version.trim() },
            subpath: subpath || undefined,
          };
        }
        const extra = splitNames(names);
        if (extra) spec = { ...spec, names: [...(spec.names ?? []), ...extra] };
        return LibraryService.Fetch(spec);
      },
      { reload: false },
    );
    setWorking(false);
    if (result) setFetched(result);
  };

  const ready =
    tab === "local"
      ? !!local?.path
      : tab === "github"
        ? !!github.trim()
        : tab === "url"
          ? !!url.trim()
          : !!packageID.trim() && !!version.trim(); // 商店来源要求包 ID 与版本都给出

  return (
    <Modal
      title="安装技能"
      onClose={onClose}
      footer={
        <>
          <span className="grow" />
          <button type="button" className="pill-btn" onClick={onClose}>
            取消
          </button>
          <button
            type="button"
            className="pill-btn accent"
            disabled={!ready || working}
            aria-busy={working}
            onClick={tab === "local" ? installLocal : fetchRemote}
          >
            {working ? <span className="spin" /> : null}
            {tab === "local" ? "安装" : "获取候选"}
          </button>
        </>
      }
    >
      <div className="seg" style={{ alignSelf: "flex-start" }}>
        {tabs.map((t) => (
          <button key={t.key} type="button" className={tab === t.key ? "on" : ""} onClick={() => setTab(t.key)}>
            {t.label}
          </button>
        ))}
      </div>

      {tab === "local" && (
        <>
          <p className="hint">选择一个包含 SKILL.md 的目录，作为自研技能安装入库(一次性复制，之后可用"更新"重新读取)。</p>
          <div className="row">
            <input
              className="input grow"
              placeholder="/path/to/skill 或 ~/path/to/skill"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && ready && installLocal()}
            />
            <button
              type="button"
              className="pill-btn"
              onClick={async () => {
                const dir = await pickDirectory("选择技能目录");
                if (dir) setPath(dir);
              }}
            >
              选择…
            </button>
          </div>
          {local?.error && <span className="field-error">{local.error}</span>}
        </>
      )}

      {tab === "github" && (
        <>
          <label className="field">
            <span>仓库(owner/repo、owner/repo@技能名，或 github.com 链接，可含 /tree/分支/路径)</span>
            <input className="input" placeholder="anthropics/skills" value={github} onChange={(e) => setGithub(e.target.value)} />
          </label>
          <div className="row">
            <label className="field grow">
              <span>分支 / tag / commit(可选，默认分支)</span>
              <input className="input" value={ref} onChange={(e) => setRef(e.target.value)} />
            </label>
            <label className="field grow">
              <span>仓库内路径(可选)</span>
              <input className="input" value={subpath} onChange={(e) => setSubpath(e.target.value)} />
            </label>
          </div>
          {!config.github_token && (
            <div className="notice warn">未设置 GITHUB_TOKEN / GH_TOKEN,匿名访问 GitHub API 容易被限流。</div>
          )}
        </>
      )}

      {tab === "url" && (
        <>
          <label className="field">
            <span>zip 压缩包地址</span>
            <input className="input" placeholder="https://example.com/skills.zip" value={url} onChange={(e) => setURL(e.target.value)} />
          </label>
          <label className="field">
            <span>包内路径(可选)</span>
            <input className="input" value={subpath} onChange={(e) => setSubpath(e.target.value)} />
          </label>
        </>
      )}

      {tab === "store" && (
        <>
          <div className="row">
            <label className="field grow">
              <span>包 ID</span>
              <input className="input" value={packageID} onChange={(e) => setPackageID(e.target.value)} />
            </label>
            <label className="field">
              <span>版本</span>
              <input className="input" value={version} onChange={(e) => setVersion(e.target.value)} />
            </label>
          </div>
          {!config.store_url && <div className="notice warn">未设置 SKILLS_STORE_URL,商店来源无法获取。</div>}
        </>
      )}

      {tab !== "local" && (
        <label className="field">
          <span>只取这些技能(可选，逗号分隔的技能名)</span>
          <input className="input" value={names} onChange={(e) => setNames(e.target.value)} />
        </label>
      )}
    </Modal>
  );
}
