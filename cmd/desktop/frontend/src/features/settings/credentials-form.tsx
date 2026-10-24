// 设置页的 GitHub 与同步分区。GitHub 令牌存在数据库里，和库里其他内容一样随推送、拉回走;
// 同步设置只属于这台机器：Secret Access Key 与 Session Token 存在系统钥匙串里，其余几项存在这台机器的应用设置里，不进技能库。
// 填完点保存才存入并交给技能库;留空时技能库照旧读环境变量。同步设置整组取用：填了任意一项就整组用这里的。
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AppService, LibraryService } from "@bindings/cmd/desktop";
import type { SyncSettings } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import { gitHubTokenQuery, invalidateConfig, invalidateGitHubToken, toLibraryError } from "@/lib/library";
import { messages, useT } from "@/lib/i18n";
import { useUI } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

// SyncSection 读取同步分区填过的内容再给出表单。钥匙串读不到(例如拒绝了授权)时不给表单，
// 免得把没读到当成没填、保存时盖掉钥匙串里的内容;GitHub 分区不受影响。读到或保存后让主界面的配置状态失效重读。
// 钥匙串的读取不放进查询缓存：写操作后的统一重读会再读钥匙串，可能再弹一次授权。
export function SyncSection() {
  const t = useT().credentials;
  const [settings, setSettings] = useState<SyncSettings>();
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    setFailed(false);
    const call = AppService.SyncSettings();
    call.then(
      (s) => {
        if (!live) return;
        setSettings(s);
        void invalidateConfig();
      },
      () => live && setFailed(true),
    );
    return () => {
      live = false;
      call.cancel();
    };
  }, [attempt]);
  if (failed)
    return (
      <div className="flex items-center gap-2">
        <Hint className="grow">{t.keychainFailed()}</Hint>
        <Button onClick={() => setAttempt((n) => n + 1)}>{t.retry}</Button>
      </div>
    );
  return (
    settings && (
      <SyncForm
        saved={settings}
        onSaved={(s) => {
          setSettings(s);
          void invalidateConfig();
        }}
      />
    )
  );
}

// GitHubSection 给出 GitHub 令牌的表单。令牌是库里的内容，拉回后换成拉回的那个，表单随之重置。
// 读不到时和同步分区一样说明原因、给出重试，不给表单。
export function GitHubSection() {
  const t = useT().credentials;
  const { data: saved, error, refetch } = useQuery(gitHubTokenQuery);
  if (error)
    return (
      <div className="flex items-center gap-2">
        <Hint className="grow">{t.tokenReadFailed(describeError(toLibraryError(error)))}</Hint>
        <Button onClick={() => void refetch()}>{t.retry}</Button>
      </div>
    );
  return saved === undefined ? null : <GitHubForm key={saved} saved={saved} />;
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

function GitHubForm({ saved }: { saved: string }) {
  const t = useT().credentials;
  const [token, setToken] = useState(saved);
  return (
    <SaveForm
      dirty={token !== saved}
      save={() => LibraryService.SetGitHubToken(token)}
      success={t.tokenSaved}
      onSaved={() => {
        setToken(token.trim());
        void invalidateGitHubToken();
        void invalidateConfig();
      }}
      hint={t.tokenHint}
    >
      <Field label={t.token}>
        <TextInput secret value={token} onChange={setToken} />
      </Field>
    </SaveForm>
  );
}

function SyncForm({ saved, onSaved }: { saved: SyncSettings; onSaved: (s: SyncSettings) => void }) {
  const t = useT().credentials;
  const fields = syncFields();
  const [draft, setDraft] = useState<SyncSettings>(saved);
  const value = (s: SyncSettings, key: SyncField) => s[key] ?? "";
  const dirty = fields.flat().some(({ key }) => value(draft, key) !== value(saved, key));
  return (
    <SaveForm
      dirty={dirty}
      save={() => AppService.SaveSyncSettings(draft)}
      success={t.syncSaved}
      onSaved={(next) => {
        setDraft(next);
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

function SaveForm<T>({
  dirty,
  save,
  success,
  onSaved,
  hint,
  children,
}: {
  dirty: boolean;
  save: () => Promise<T>;
  success: string;
  onSaved: (value: T) => void;
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
    const saved = await ui.run(async () => ({ value: await save() }), { reload: false, success });
    setSaving(false);
    if (saved) onSaved(saved.value);
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
