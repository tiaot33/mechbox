// 入库技能：给出一个来源输入(本地路径、GitHub、Git 仓库、下载链接或站点链接),由技能库识别来源并做入库预览，
// 再在 FetchReview 里挑选候选入库。
import { useEffect, useRef, useState } from "react";
import { Dialogs, type CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import type { FetchPreview, PreviewFetchRequest } from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";
import { describeError } from "@/lib/errors";
import { resolveFetchInput } from "@/lib/paths";
import { Button } from "@/components/ui/button";
import { Field, FieldError, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Notice } from "@/components/ui/notice";
import { FetchReview } from "./fetch-review";

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

export function FetchDialog({ home, onClose }: { home: string; onClose: () => void }) {
  const ui = useUI();
  const [text, setText] = useState("");
  const [ref, setRef] = useState("");
  const [subpath, setSubpath] = useState("");
  const [working, setWorking] = useState(false);
  // failure 是技能库给出的识别 / 入库预览失败原因，改了输入就清掉。
  const [failure, setFailure] = useState<string | null>(null);
  const [preview, setPreview] = useState<FetchPreview | null>(null);
  // pending 是还没返回的入库预览;改了输入或关掉对话框就取消它，返回的结果与失败原因也不再采用。
  const pending = useRef<CancellablePromise<FetchPreview> | null>(null);
  const discard = () => {
    pending.current?.cancel();
    pending.current = null;
  };
  useEffect(() => discard, []);

  if (preview) {
    return <FetchReview preview={preview} onClose={onClose} onBack={() => setPreview(null)} />;
  }

  const input = resolveFetchInput(text, home);
  const ready = !!input?.path;
  const edit = (set: (v: string) => void) => (v: string) => {
    set(v);
    discard();
    setWorking(false);
    setFailure(null);
  };

  const previewFetch = async () => {
    if (pending.current || !input?.path) return;
    setWorking(true);
    setFailure(null);
    const req: PreviewFetchRequest = { input: input.path, ref: ref.trim() || undefined, subpath: subpath.trim() || undefined };
    const call = LibraryService.PreviewFetch(req);
    pending.current = call;
    const result = await ui.run(() => call, {
      reload: false,
      onError: (err) => {
        if (pending.current === call) setFailure(describeError(err));
        return true;
      },
    });
    if (pending.current !== call) return;
    pending.current = null;
    setWorking(false);
    if (result) setPreview(result);
  };
  const submitOnEnter = (e: React.KeyboardEvent) => e.key === "Enter" && ready && previewFetch();

  return (
    <Modal
      title="入库技能"
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" disabled={!ready} loading={working} onClick={previewFetch}>
            入库预览
          </Button>
        </>
      }
    >
      <Hint>
        粘贴技能的来源，技能库会识别它是哪一种：本地目录、GitHub 仓库(owner/repo、owner/repo@技能名或链接)、GitLab 等 Git
        仓库地址(含 SSH)、SKILL.md / zip / tar 下载链接，或提供 well-known 索引的站点。入库时复制的是预览这一刻的内容。
      </Hint>
      <div className="flex min-w-0 items-center gap-2">
        <Input
          className="grow"
          autoFocus
          placeholder="anthropics/skills、https://gitlab.com/team/skills、~/skills"
          value={text}
          onChange={(e) => edit(setText)(e.target.value)}
          onKeyDown={submitOnEnter}
        />
        <Button
          onClick={async () => {
            const dir = await pickDirectory("选择技能目录");
            if (dir) edit(setText)(dir);
          }}
        >
          选择…
        </Button>
      </div>
      {input?.error && <FieldError>{input.error}</FieldError>}
      <div className="flex min-w-0 items-start gap-2">
        <Field label="分支 / tag(可选，只对 Git 仓库有效)" className="grow">
          <Input placeholder="默认分支" value={ref} onChange={(e) => edit(setRef)(e.target.value)} onKeyDown={submitOnEnter} />
        </Field>
        <Field label="仓库内目录(可选，只对 Git 仓库有效)" className="grow">
          <Input placeholder="整个仓库" value={subpath} onChange={(e) => edit(setSubpath)(e.target.value)} onKeyDown={submitOnEnter} />
        </Field>
      </div>
      {failure && (
        <Notice tone="bad" className="fetch-failure whitespace-pre-line">
          {failure}
        </Notice>
      )}
    </Modal>
  );
}
