// 设置页的 GitHub、AI 与同步分区。三者都存在数据库里，只认这里填的，不读环境变量:
// GitHub 令牌、AI 设置和库里其他内容一样随推送、拉回走;同步设置只属于这台机器，推送时不带，拉回时保留。填完点保存才存入。
// 同步分区还有保留份数与自动推送的间隔(定时推送由 Go 端做),下面写出这台机器的备份放在哪、上次推送的时间与自动推送失败的原因。
// AI 分区的「测试连接」用表单上的内容，不用先保存;请求头、请求体是用户填的原文，库解析，格式不对时保存与测试都报出来。
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { LibraryService } from "@bindings/cmd/desktop";
import { AIProvider, SyncInterval, type AISettings, type SyncSettings } from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import { aiError, describeError } from "@/lib/errors";
import {
  aiSettingsQuery,
  gitHubTokenQuery,
  invalidateAISettings,
  invalidateConfig,
  invalidateGitHubToken,
  invalidateSyncSettings,
  invalidateSyncStatus,
  syncSettingsQuery,
  syncStatusQuery,
  toLibraryError,
  type LibraryError,
} from "@/lib/library";
import { messages, useT } from "@/lib/i18n";
import { useUI } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Notice } from "@/components/ui/notice";
import { Segmented } from "@/components/ui/segmented";
import { Textarea } from "@/components/ui/textarea";

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

// defaultKeep 与 library.DefaultSyncKeep 相同(常量不进绑定):没填保留份数时 S3 上保留这台机器的几份备份。
const defaultKeep = 10;

type SyncField = Exclude<keyof SyncSettings, "keep" | "auto_push">;

const syncFields = (): { key: SyncField; label: string; secret?: boolean }[][] => {
  const t = messages().credentials;
  return [
    [{ key: "endpoint", label: t.endpoint }],
    [
      { key: "bucket", label: t.bucket },
      { key: "folder", label: t.folder },
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
  const dirty =
    fields.flat().some(({ key }) => value(draft, key) !== value(saved, key)) ||
    (draft.keep ?? 0) !== (saved.keep ?? 0) ||
    (draft.auto_push ?? "") !== (saved.auto_push ?? "");
  const intervals = [
    { value: "", label: t.autoPushOff },
    { value: SyncInterval.SyncHourly, label: t.autoPushHourly },
    { value: SyncInterval.SyncDaily, label: t.autoPushDaily },
    { value: SyncInterval.SyncWeekly, label: t.autoPushWeekly },
  ];
  return (
    <SaveForm
      dirty={dirty}
      save={() => LibraryService.SetSyncSettings(draft)}
      success={t.syncSaved}
      onSaved={() => {
        void invalidateSyncSettings();
        void invalidateSyncStatus();
        void invalidateConfig();
      }}
      hint={t.syncHint}
      footer={<SyncStatusLine folder={saved.folder ?? ""} />}
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
      <div className="flex min-w-0 items-start gap-2">
        <Field label={t.keep} className="flex-1">
          <Input
            className="font-mono"
            type="number"
            min={1}
            placeholder={String(defaultKeep)}
            value={draft.keep ? String(draft.keep) : ""}
            onChange={(e) => setDraft((d) => ({ ...d, keep: Math.max(0, Math.floor(Number(e.target.value) || 0)) }))}
          />
          <Hint>{t.keepHint(defaultKeep)}</Hint>
        </Field>
        <Field label={t.autoPush} className="flex-1">
          <NativeSelect
            className="h-8 max-w-none"
            value={draft.auto_push ?? ""}
            onChange={(e) => setDraft((d) => ({ ...d, auto_push: e.target.value as SyncInterval }))}
          >
            {intervals.map((i) => (
              <option key={i.value} value={i.value}>
                {i.label}
              </option>
            ))}
          </NativeSelect>
          <Hint>{t.autoPushHint}</Hint>
        </Field>
      </div>
    </SaveForm>
  );
}

// SyncStatusLine 写出这台机器的备份放在 S3 的哪个目录(按已保存的文件夹)、上次推送的时间，以及上一次推送出的问题;
// 设置页同步分区与同步弹窗共用。
export function SyncStatusLine({ folder }: { folder: string }) {
  const t = useT().credentials;
  const { data: status } = useQuery(syncStatusQuery);
  if (!status) return null;
  const path = `${folder ? `${folder}/` : ""}${status.device}/`;
  const problem = status.problem;
  return (
    <>
      <Hint>
        {t.deviceFolder(path)} · {status.last_pushed_at ? t.lastPushed(formatDate(status.last_pushed_at)) : t.neverPushed}
      </Hint>
      {problem?.error && (
        <Notice tone="warn">
          {(problem.uploaded ? t.removeFailed : t.pushFailed)(formatDate(problem.at), describeError(problem.error as LibraryError))}
        </Notice>
      )}
    </>
  );
}

type AIField = keyof AISettings;

const aiFields: AIField[] = ["provider", "base_url", "api_key", "model", "headers", "body"];

// 请求体的示例按接口类型给：思考深度在两种接口里是不同的字段。
const bodyExamples: Record<string, string> = {
  [AIProvider.AIProviderOpenAI]: `{"reasoning_effort": "high"}`,
  [AIProvider.AIProviderAnthropic]: `{"thinking": {"type": "enabled", "budget_tokens": 8192}, "max_tokens": 16000}`,
};

const trimAI = (s: AISettings): AISettings =>
  Object.fromEntries(aiFields.map((key) => [key, (s[key] ?? "").trim()])) as AISettings;

function AIForm({ saved }: { saved: AISettings }) {
  const ui = useUI();
  const t = useT().credentials;
  const [draft, setDraft] = useState<AISettings>(saved);
  const [testing, setTesting] = useState(false);
  const set = (key: AIField) => (v: string) => setDraft((d) => ({ ...d, [key]: v }));
  const dirty = aiFields.some((key) => (draft[key] ?? "") !== (saved[key] ?? ""));
  const testable = !!draft.model?.trim();
  // 失败时按错误码说明该查哪一项，后面附上服务给出的原因。
  const failed = (err: LibraryError) => {
    const text = aiError(err);
    if (!text) return false;
    ui.toast(text, "error");
    return true;
  };
  const provider = draft.provider ?? AIProvider.AIProviderOpenAI;
  const test = async () => {
    setTesting(true);
    await ui.run(() => LibraryService.TestAISettings(draft), { reload: false, success: t.testPassed, onError: failed });
    setTesting(false);
  };
  return (
    <SaveForm
      dirty={dirty}
      save={() => LibraryService.SetAISettings(draft)}
      success={t.aiSaved}
      onError={failed}
      onSaved={() => {
        setDraft(trimAI(draft));
        void invalidateAISettings();
      }}
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
        <TextInput value={draft.base_url ?? ""} onChange={set("base_url")} />
      </Field>
      <div className="flex min-w-0 items-start gap-2 [&:lang(en)]:items-end">
        <Field label={t.apiKey} className="flex-1">
          <TextInput secret value={draft.api_key ?? ""} onChange={set("api_key")} />
        </Field>
        <Field label={t.model} className="flex-1">
          <TextInput value={draft.model ?? ""} onChange={set("model")} />
        </Field>
      </div>
      <Field label={t.headers}>
        <TextArea rows={2} value={draft.headers ?? ""} onChange={set("headers")} placeholder={t.headersPlaceholder} />
      </Field>
      <Field label={t.body}>
        <TextArea rows={3} value={draft.body ?? ""} onChange={set("body")} placeholder={bodyExamples[provider]} />
        <Hint>{t.bodyHint}</Hint>
      </Field>
    </SaveForm>
  );
}

function SaveForm<T>({
  dirty,
  save,
  success,
  onError,
  onSaved,
  hint,
  actions,
  footer,
  children,
}: {
  dirty: boolean;
  save: () => Promise<T>;
  success: string;
  onError?: (err: LibraryError) => boolean;
  onSaved: (value: T) => void;
  hint: string;
  actions?: React.ReactNode;
  footer?: React.ReactNode;
  children: React.ReactNode;
}) {
  const ui = useUI();
  const t = useT().credentials;
  const [saving, setSaving] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!dirty || saving) return;
    setSaving(true);
    const saved = await ui.run(async () => ({ value: await save() }), { reload: false, success, onError });
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
      {footer}
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

function TextArea({ onChange, ...props }: { value: string; onChange: (v: string) => void; rows: number; placeholder?: string }) {
  return <Textarea className="font-mono" autoComplete="off" spellCheck={false} onChange={(e) => onChange(e.target.value)} {...props} />;
}
