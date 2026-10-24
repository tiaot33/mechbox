// 设置页的 GitHub 与同步分区：令牌与密钥存在系统钥匙串里，其余几项存在这台机器的应用设置里，都不进技能库。
// 填完点保存才存入并交给技能库;留空时技能库照旧读环境变量。同步设置整组取用：填了任意一项就整组用这里的。
import { useEffect, useState, type ReactNode } from "react";
import { AppService, type Credentials } from "@bindings/cmd/desktop";
import type { SyncSettings } from "@bindings/internal/library";
import { invalidateConfig } from "@/lib/library";
import { messages, useT } from "@/lib/i18n";
import { useUI } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

type Form = (saved: Credentials, onSaved: (c: Credentials) => void) => ReactNode;

// useCredentials 读取设置页填过的内容，返回按分区渲染表单的函数。钥匙串读不到(例如拒绝了授权)时不给表单，
// 免得把没读到当成没填、保存时盖掉钥匙串里的内容。读到或保存后让主界面的配置状态失效重读。
// 钥匙串的读取不放进查询缓存：写操作后的统一重读会再读钥匙串，可能再弹一次授权。
export function useCredentials(): (form: Form) => ReactNode {
  const t = useT().credentials;
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
        void invalidateConfig();
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
    void invalidateConfig();
  };
  return (form) =>
    failed ? (
      <div className="flex items-center gap-2">
        <Hint className="grow">{t.keychainFailed()}</Hint>
        <Button onClick={() => setAttempt((n) => n + 1)}>{t.retry}</Button>
      </div>
    ) : (
      credentials && form(credentials, saved)
    );
}

type SyncField = keyof SyncSettings;

const syncFields = (): { key: SyncField; label: string; secret?: boolean }[][] => {
  const t = messages().credentials;
  return [
    [{ key: "endpoint", label: t.endpoint }],
    [
      { key: "bucket", label: t.bucket },
      { key: "key", label: t.key },
      { key: "region", label: t.region },
    ],
    [
      { key: "access_key_id", label: t.accessKeyId },
      { key: "secret_access_key", label: t.secretAccessKey, secret: true },
    ],
    [{ key: "session_token", label: t.sessionToken, secret: true }],
  ];
};

export function GitHubForm({ saved, onSaved }: { saved: Credentials; onSaved: (c: Credentials) => void }) {
  const t = useT().credentials;
  const [token, setToken] = useState(saved.github_token);
  return (
    <SaveForm
      dirty={token !== saved.github_token}
      save={() => AppService.SaveGitHubToken(token)}
      success={t.tokenSaved}
      onSaved={(next) => {
        setToken(next.github_token);
        onSaved(next);
      }}
      hint={t.tokenHint}
    >
      <Field label={t.token}>
        <TextInput secret value={token} onChange={setToken} />
      </Field>
    </SaveForm>
  );
}

export function SyncForm({ saved, onSaved }: { saved: Credentials; onSaved: (c: Credentials) => void }) {
  const t = useT().credentials;
  const fields = syncFields();
  const [draft, setDraft] = useState<SyncSettings>(saved.sync);
  const value = (s: SyncSettings, key: SyncField) => s[key] ?? "";
  const dirty = fields.flat().some(({ key }) => value(draft, key) !== value(saved.sync, key));
  return (
    <SaveForm
      dirty={dirty}
      save={() => AppService.SaveSyncSettings(draft)}
      success={t.syncSaved}
      onSaved={(next) => {
        setDraft(next.sync);
        onSaved(next);
      }}
      hint={t.syncHint}
    >
      {fields.map((row) => (
        <div key={row[0].key} className="flex min-w-0 items-start gap-2 [&:lang(en)]:items-end">
          {row.map((f) => (
            <Field key={f.key} label={f.label} className="flex-1">
              <TextInput secret={f.secret} value={value(draft, f.key)} onChange={(v) => setDraft((d) => ({ ...d, [f.key]: v }))} />
            </Field>
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
  const t = useT().credentials;
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
    <form className="flex flex-col gap-2.5" onSubmit={submit}>
      <fieldset className="flex min-w-0 flex-col gap-2.5" disabled={saving}>
        {children}
      </fieldset>
      <div className="flex items-center gap-2">
        <Hint className="grow">{hint}</Hint>
        <Button type="submit" variant="primary" disabled={!dirty} loading={saving}>
          {t.save}
        </Button>
      </div>
    </form>
  );
}

function TextInput({ value, onChange, secret }: { value: string; onChange: (v: string) => void; secret?: boolean }) {
  return (
    <Input
      className="font-mono"
      type={secret ? "password" : "text"}
      autoComplete="off"
      spellCheck={false}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}
