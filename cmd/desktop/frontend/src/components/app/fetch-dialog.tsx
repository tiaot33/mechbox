// 入库技能：给出来源(本地目录 / GitHub / URL 压缩包),先做入库预览，再在 FetchReview 里挑选候选入库。
import { useState } from "react";
import { Dialogs } from "@wailsio/runtime";
import { LibraryService, type ConfigStatus } from "@bindings/cmd/desktop";
import type { FetchPreview, PreviewFetchRequest } from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";
import { resolveLocalPath } from "@/lib/paths";
import { FetchReview } from "./fetch-review";

type Tab = "local" | "github" | "url";

const tabs: { key: Tab; label: string }[] = [
  { key: "local", label: "本地目录" },
  { key: "github", label: "GitHub" },
  { key: "url", label: "URL 压缩包" },
];

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

export function FetchDialog({ config, home, onClose }: { config: ConfigStatus; home: string; onClose: () => void }) {
  const ui = useUI();
  const [tab, setTab] = useState<Tab>("local");
  const [path, setPath] = useState("");
  const [github, setGithub] = useState("");
  const [ref, setRef] = useState("");
  const [subpath, setSubpath] = useState("");
  const [url, setURL] = useState("");
  const [working, setWorking] = useState(false);
  const [preview, setPreview] = useState<FetchPreview | null>(null);

  if (preview) {
    return (
      <FetchReview preview={preview} audits={tab === "github"} onClose={onClose} onBack={() => setPreview(null)} />
    );
  }

  const local = resolveLocalPath(path, home);
  const request = (): PreviewFetchRequest => {
    switch (tab) {
      case "local":
        return { local: local?.path };
      case "github":
        return { github: { input: github.trim(), ref: ref.trim() || undefined, subpath: subpath.trim() || undefined } };
      case "url":
        return { url: url.trim() };
    }
  };

  const previewFetch = async () => {
    setWorking(true);
    const req = request();
    const result = await ui.run(() => LibraryService.PreviewFetch(req), { reload: false });
    setWorking(false);
    if (result) setPreview(result);
  };

  const ready = tab === "local" ? !!local?.path : tab === "github" ? !!github.trim() : !!url.trim();

  return (
    <Modal
      title="入库技能"
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
            onClick={previewFetch}
          >
            {working ? <span className="spin" /> : null}
            入库预览
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
          <p className="hint">选择一个目录，列出其中的技能(含 SKILL.md 的文件夹)。入库时复制的是预览这一刻的内容，之后可用"更新"重新读取。</p>
          <div className="row">
            <input
              className="input grow"
              placeholder="/path/to/skills 或 ~/path/to/skills"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && ready && previewFetch()}
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
              <span>分支 / tag(可选，默认分支)</span>
              <input className="input" value={ref} onChange={(e) => setRef(e.target.value)} />
            </label>
            <label className="field grow">
              <span>仓库内路径(可选)</span>
              <input className="input" value={subpath} onChange={(e) => setSubpath(e.target.value)} />
            </label>
          </div>
          {!config.github_token && (
            <div className="notice warn">设置页没有填 GitHub 令牌，也没有设置 GITHUB_TOKEN / GH_TOKEN,匿名访问 GitHub API 容易被限流。</div>
          )}
        </>
      )}

      {tab === "url" && (
        <label className="field">
          <span>zip 压缩包地址</span>
          <input className="input" placeholder="https://example.com/skills.zip" value={url} onChange={(e) => setURL(e.target.value)} />
        </label>
      )}
    </Modal>
  );
}
