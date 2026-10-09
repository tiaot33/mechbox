// 设置页的 GitHub 与同步分区：令牌与密钥存在系统钥匙串里，其余几项存在这台机器的应用设置里，都不进技能库。
// 填完点保存才存入并交给技能库;留空时技能库照旧读环境变量。同步设置整组取用：填了任意一项就整组用这里的。
import { useEffect, useState, type ReactNode } from "react";
import { AppService, type Credentials } from "@bindings/cmd/desktop";
import type { SyncSettings } from "@bindings/internal/library";
import { useUI } from "@/lib/ui";

type Form = (saved: Credentials, onSaved: (c: Credentials) => void) => ReactNode;

// useCredentials 读取设置页填过的内容，返回按分区渲染表单的函数。钥匙串读不到(例如拒绝了授权)时不给表单，
// 免得把没读到当成没填、保存时盖掉钥匙串里的内容。读到或保存后调用 onChange,让主界面重读配置状态。
export function useCredentials(onChange: () => void): (form: Form) => ReactNode {
  const [credentials, setCredentials] = useState<Credentials>();
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    setFailed(false);
    const call = AppService.Credentials();
    call.then(
      (c) => {
        if (!live) return;
        setCredentials(c);
        onChange();
      },
      () => live && setFailed(true),
    );
    return () => {
      live = false;
      call.cancel();
    };
  }, [attempt]);
  const saved = (c: Credentials) => {
    setCredentials(c);
    onChange();
  };
  return (form) =>
    failed ? (
      <div className="row">
        <p className="hint grow">没能读取系统钥匙串，令牌和密钥暂时不能查看或修改。</p>
        <button type="button" className="pill-btn" onClick={() => setAttempt((n) => n + 1)}>
          重试
        </button>
      </div>
    ) : (
      credentials && form(credentials, saved)
    );
}

type SyncField = keyof SyncSettings;

const syncFields: { key: SyncField; label: string; secret?: boolean }[][] = [
  [{ key: "endpoint", label: "服务地址(可选，默认 AWS S3)" }],
  [
    { key: "bucket", label: "存储桶" },
    { key: "key", label: "对象名(可选，默认 library.db)" },
    { key: "region", label: "区域(可选，默认 us-east-1)" },
  ],
  [
    { key: "access_key_id", label: "Access Key ID" },
    { key: "secret_access_key", label: "Secret Access Key", secret: true },
  ],
  [{ key: "session_token", label: "Session Token(可选)", secret: true }],
];

export function GitHubForm({ saved, onSaved }: { saved: Credentials; onSaved: (c: Credentials) => void }) {
  const [token, setToken] = useState(saved.github_token);
  return (
    <SaveForm
      dirty={token !== saved.github_token}
      save={() => AppService.SaveGitHubToken(token)}
      success="已保存 GitHub 令牌"
      onSaved={(next) => {
        setToken(next.github_token);
        onSaved(next);
      }}
      hint="留空时从环境变量 GITHUB_TOKEN 或 GH_TOKEN 读取。"
    >
      <label className="field">
        <span>个人访问令牌</span>
        <TextInput secret value={token} onChange={setToken} />
      </label>
    </SaveForm>
  );
}

export function SyncForm({ saved, onSaved }: { saved: Credentials; onSaved: (c: Credentials) => void }) {
  const [draft, setDraft] = useState<SyncSettings>(saved.sync);
  const value = (s: SyncSettings, key: SyncField) => s[key] ?? "";
  const dirty = syncFields.flat().some(({ key }) => value(draft, key) !== value(saved.sync, key));
  return (
    <SaveForm
      dirty={dirty}
      save={() => AppService.SaveSyncSettings(draft)}
      success="已保存同步设置"
      onSaved={(next) => {
        setDraft(next.sync);
        onSaved(next);
      }}
      hint="全部留空时从环境变量 SKILLS_S3_*、AWS_* 读取;填了任意一项就整组用这里的。"
    >
      {syncFields.map((row) => (
        <div key={row[0].key} className="row">
          {row.map((f) => (
            <label key={f.key} className="field grow">
              <span>{f.label}</span>
              <TextInput secret={f.secret} value={value(draft, f.key)} onChange={(v) => setDraft((d) => ({ ...d, [f.key]: v }))} />
            </label>
          ))}
        </div>
      ))}
    </SaveForm>
  );
}

function SaveForm({
  dirty,
  save,
  success,
  onSaved,
  hint,
  children,
}: {
  dirty: boolean;
  save: () => Promise<Credentials>;
  success: string;
  onSaved: (c: Credentials) => void;
  hint: string;
  children: React.ReactNode;
}) {
  const ui = useUI();
  const [saving, setSaving] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!dirty || saving) return;
    setSaving(true);
    const next = await ui.run(save, { reload: false, success });
    setSaving(false);
    if (next) onSaved(next);
  };
  return (
    <form className="cred-form" onSubmit={submit}>
      <fieldset className="cred-form" disabled={saving}>
        {children}
      </fieldset>
      <div className="row">
        <p className="hint grow">{hint}</p>
        <button type="submit" className="pill-btn accent" disabled={!dirty || saving}>
          {saving && <span className="spin" />}
          保存
        </button>
      </div>
    </form>
  );
}

function TextInput({ value, onChange, secret }: { value: string; onChange: (v: string) => void; secret?: boolean }) {
  return (
    <input
      className="input mono"
      type={secret ? "password" : "text"}
      autoComplete="off"
      spellCheck={false}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}
