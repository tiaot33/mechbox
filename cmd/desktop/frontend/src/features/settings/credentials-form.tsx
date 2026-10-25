// 设置页的 GitHub、AI 与同步分区。三者都存在数据库里，只认这里填的，不读环境变量:
// GitHub 令牌、AI 设置和库里其他内容一样随推送、拉回走;同步设置只属于这台机器，推送时不带，拉回时保留。填完点保存才存入。
// AI 分区的「测试连接」用表单上的内容，不用先保存。
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { LibraryService } from "@bindings/cmd/desktop";
import { AIProvider, type AISettings, type SyncSettings } from "@bindings/internal/library";
import { describeError } from "@/lib/errors";
import {
  aiSettingsQuery,
  gitHubTokenQuery,
  invalidateAISettings,
  invalidateConfig,
  invalidateGitHubToken,
  invalidateSyncSettings,
  syncSettingsQuery,
  toLibraryError,
} from "@/lib/library";
import { messages, useT } from "@/lib/i18n";
import { useUI } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Segmented } from "@/components/ui/segmented";

// SyncSection 读出同步分区填过的内容再给出表单;读不到时说明原因、给出重试，不给表单，免得把没读到当成没填。
export function SyncSection() {
  const t = useT().credentials;
  const { data: saved, error, refetch } = useQuery(syncSettingsQuery);
  if (error) return <ReadFailed message={t.syncReadFailed(describeError(toLibraryError(error)))} retry={refetch} />;
  return saved === undefined ? null : <SyncForm key={JSON.stringify(saved)} saved={saved} />;
}

// GitHubSection 给出 GitHub 令牌的表单。令牌是库里的内容，拉回后换成拉回的那个，表单随之重置。
// 读不到时和同步分区一样说明原因、给出重试，不给表单。
export function GitHubSection() {
  const t = useT().credentials;
  const { data: saved, error, refetch } = useQuery(gitHubTokenQuery);
  if (error) return <ReadFailed message={t.tokenReadFailed(describeError(toLibraryError(error)))} retry={refetch} />;
  return saved === undefined ? null : <GitHubForm key={saved} saved={saved} />;
}

// AISection 给出 AI 设置的表单。AI 设置是库里的内容，拉回后换成拉回的那组，表单随之重置。
// 读不到时和同步分区一样说明原因、给出重试，不给表单。
export function AISection() {
  const t = useT().credentials;
  const { data: saved, error, refetch } = useQuery(aiSettingsQuery);
  if (error) return <ReadFailed message={t.aiReadFailed(describeError(toLibraryError(error)))} retry={refetch} />;
  return saved === undefined ? null : <AIForm key={JSON.stringify(saved)} saved={saved} />;
}

function ReadFailed({ message, retry }: { message: string; retry: () => unknown }) {
  const t = useT().credentials;
  return (
    <div className="flex items-center gap-2">
      <Hint className="grow">{message}</Hint>
      <Button onClick={() => void retry()}>{t.retry}</Button>
    </div>
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

function SyncForm({ saved }: { saved: SyncSettings }) {
  const t = useT().credentials;
  const fields = syncFields();
  const [draft, setDraft] = useState<SyncSettings>(saved);
  const value = (s: SyncSettings, key: SyncField) => s[key] ?? "";
  const dirty = fields.flat().some(({ key }) => value(draft, key) !== value(saved, key));
  return (
    <SaveForm
      dirty={dirty}
      save={() => LibraryService.SetSyncSettings(draft)}
      success={t.syncSaved}
      onSaved={() => {
        void invalidateSyncSettings();
        void invalidateConfig();
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

// 服务地址留空时用的官方地址，作为输入框的占位文字。
const officialBaseURL: Record<string, string> = {
  [AIProvider.AIProviderOpenAI]: "https://api.openai.com/v1",
  [AIProvider.AIProviderAnthropic]: "https://api.anthropic.com",
};

type AIField = keyof AISettings;

// 接口类型留空即 OpenAI 兼容，比较有没有改动时两者算一样。
const aiValue = (s: AISettings, key: AIField) => (key === "provider" ? s.provider || AIProvider.AIProviderOpenAI : (s[key] ?? ""));

function AIForm({ saved }: { saved: AISettings }) {
  const ui = useUI();
  const t = useT().credentials;
  const [draft, setDraft] = useState<AISettings>(saved);
  const [testing, setTesting] = useState(false);
  const set = (key: AIField) => (v: string) => setDraft((d) => ({ ...d, [key]: v }));
  const provider = aiValue(draft, "provider");
  const keys: AIField[] = ["provider", "base_url", "api_key", "model"];
  const dirty = keys.some((key) => aiValue(draft, key) !== aiValue(saved, key));
  const testable = !!draft.api_key?.trim() && !!draft.model?.trim();
  const test = async () => {
    setTesting(true);
    await ui.run(() => LibraryService.TestAISettings(draft), { reload: false, success: t.testPassed });
    setTesting(false);
  };
  return (
    <SaveForm
      dirty={dirty}
      save={() => LibraryService.SetAISettings(draft)}
      success={t.aiSaved}
      onSaved={() => void invalidateAISettings()}
      hint={t.aiHint}
      actions={
        <Button disabled={!testable} loading={testing} title={testable ? undefined : t.testNeeds} onClick={() => void test()}>
          {t.test}
        </Button>
      }
    >
      <div className="flex flex-col items-start gap-1">
        <span className="text-xs text-muted-foreground">{t.provider}</span>
        <Segmented
          value={provider}
          onChange={set("provider")}
          options={[
            { value: AIProvider.AIProviderOpenAI, label: t.providerOpenAI },
            { value: AIProvider.AIProviderAnthropic, label: t.providerAnthropic },
          ]}
        />
      </div>
      <Field label={t.baseURL}>
        <TextInput value={draft.base_url ?? ""} onChange={set("base_url")} placeholder={officialBaseURL[provider]} />
      </Field>
      <div className="flex min-w-0 items-start gap-2 [&:lang(en)]:items-end">
        <Field label={t.apiKey} className="flex-1">
          <TextInput secret value={draft.api_key ?? ""} onChange={set("api_key")} />
        </Field>
        <Field label={t.model} className="flex-1">
          <TextInput value={draft.model ?? ""} onChange={set("model")} />
        </Field>
      </div>
    </SaveForm>
  );
}

function SaveForm<T>({
  dirty,
  save,
  success,
  onSaved,
  hint,
  actions,
  children,
}: {
  dirty: boolean;
  save: () => Promise<T>;
  success: string;
  onSaved: (value: T) => void;
  hint: string;
  actions?: React.ReactNode;
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
        {actions}
        <Button type="submit" variant="primary" disabled={!dirty} loading={saving}>
          {t.save}
        </Button>
      </div>
    </form>
  );
}

function TextInput({
  value,
  onChange,
  secret,
  placeholder,
}: {
  value: string;
  onChange: (v: string) => void;
  secret?: boolean;
  placeholder?: string;
}) {
  return (
    <Input
      className="font-mono"
      type={secret ? "password" : "text"}
      autoComplete="off"
      spellCheck={false}
      placeholder={placeholder}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}
