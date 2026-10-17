// 入库技能：给出一个来源输入(本地路径、GitHub、Git 仓库、下载链接或站点链接),由技能库识别来源并做入库预览，
// 再在 FetchReview 里挑选候选入库。Git 类来源在扫描前从下拉里选分支或 tag。
import { useEffect, useRef, useState } from "react";
import { Dialogs, type CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import { ErrorCode, type FetchPreview, type PreviewFetchRequest, type SourceRefs } from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";
import { describeError } from "@/lib/errors";
import { toLibraryError } from "@/lib/library";
import { resolveFetchInput } from "@/lib/paths";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { FieldError, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Notice } from "@/components/ui/notice";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Spinner } from "@/components/ui/spinner";
import { FetchReview } from "./fetch-review";
import { Icon } from "./icons";

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
  // chosen 是用户在下拉里选的分支或 tag;没选时为 null,下拉选中输入里带的分支或默认分支。
  const [chosen, setChosen] = useState<string | null>(null);
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
  const input = resolveFetchInput(text, home);
  const refs = useSourceRefs(input?.path ?? "");

  if (preview) {
    return <FetchReview preview={preview} onClose={onClose} onBack={() => setPreview(null)} />;
  }

  const ready = !!input?.path;
  const edit = (v: string) => {
    setText(v);
    setChosen(null);
    discard();
    setWorking(false);
    setFailure(null);
  };

  const previewFetch = async () => {
    if (pending.current || !input?.path) return;
    setWorking(true);
    setFailure(null);
    const req: PreviewFetchRequest = { input: input.path, ref: refToSend(refs, chosen) };
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
          onChange={(e) => edit(e.target.value)}
          onKeyDown={submitOnEnter}
        />
        <Button
          onClick={async () => {
            const dir = await pickDirectory("选择技能目录");
            if (dir) edit(dir);
          }}
        >
          选择…
        </Button>
      </div>
      {input?.error && <FieldError>{input.error}</FieldError>}
      <RefPicker
        refs={refs}
        chosen={chosen}
        onChoose={(ref) => {
          setChosen(ref);
          setFailure(null);
        }}
      />
      {failure && (
        <Notice tone="bad" className="fetch-failure whitespace-pre-line">
          {failure}
        </Notice>
      )}
    </Modal>
  );
}

// RefsState 是分支下拉的状态:hidden 不显示(本地路径、技能库认不出是 Git 类来源),loading 正在读取，
// failed 取不到列表(显示原因，入库预览照常可用)。
type RefsState =
  | { status: "hidden" }
  | { status: "loading" }
  | { status: "ready"; refs: SourceRefs }
  | { status: "failed"; reason: string };

// useSourceRefs 在输入停下约半秒后向技能库取分支和 tag 列表;输入变了，旧请求的结果不再采用。
// 本地路径不取;技能库认不出是 Git 类来源时报 InvalidArgument,下拉不显示。
// 已经读了一阵(在等远端)的旧请求取消;刚发出的不取消：认不出的输入技能库马上返回错误，
// 这时取消，错误会晚于取消到达，被 Wails 报成未处理的 rejection。
function useSourceRefs(source: string): RefsState {
  const [state, setState] = useState<RefsState>({ status: "hidden" });
  useEffect(() => {
    if (!source || source.startsWith("/")) {
      setState({ status: "hidden" });
      return;
    }
    // 下拉已经显示着(从一个 Git 类输入改成另一个)时直接换成正在读取，旧列表不留在新输入下面;
    // 没显示时等请求慢于 150ms 再显示，认不出的输入不会闪一下下拉。
    setState((cur) => (cur.status === "hidden" ? cur : { status: "loading" }));
    let live = true;
    let call: CancellablePromise<SourceRefs> | undefined;
    let started = 0;
    let slow: number | undefined;
    const timer = window.setTimeout(() => {
      started = Date.now();
      call = LibraryService.ListSourceRefs(source);
      slow = window.setTimeout(() => setState({ status: "loading" }), 150);
      call
        .then(
          (refs) => live && setState({ status: "ready", refs }),
          (err) => {
            const e = toLibraryError(err);
            if (!live || e.code === ErrorCode.Canceled) return;
            setState(e.code === ErrorCode.InvalidArgument ? { status: "hidden" } : { status: "failed", reason: describeError(e) });
          },
        )
        .finally(() => window.clearTimeout(slow));
    }, 500);
    return () => {
      live = false;
      window.clearTimeout(timer);
      window.clearTimeout(slow);
      if (call && Date.now() - started > 300) void call.cancel();
    };
  }, [source]);
  return state;
}

// refToSend 是入库预览要传给技能库的分支或 tag:下拉选中的那个;选的是默认分支(输入里没带分支)或列表还没取回时不传。
// 输入里带的分支也传：tree 链接里带斜杠的分支，技能库要显式给出才能和目录分开。
function refToSend(refs: RefsState, chosen: string | null): string | undefined {
  if (refs.status !== "ready") return undefined;
  const selected = selectedRef(refs.refs, chosen);
  return selected && (refs.refs.input_ref || selected !== refs.refs.default_branch) ? selected : undefined;
}

// selectedRef 是下拉当前选中的分支或 tag:用户选过的，否则输入里带的，否则默认分支。
function selectedRef(refs: SourceRefs, chosen: string | null): string {
  return chosen ?? (refs.input_ref || refs.default_branch);
}

// RefPicker 是分支 / tag 下拉：分支和 tag 分组列出，可输入文字筛选;默认选中输入里带的分支，没带时选中默认分支。
function RefPicker({ refs, chosen, onChoose }: { refs: RefsState; chosen: string | null; onChoose: (ref: string) => void }) {
  const [open, setOpen] = useState(false);
  if (refs.status === "hidden") return null;
  const ready = refs.status === "ready" ? refs.refs : null;
  const selected = ready ? selectedRef(ready, chosen) : "";
  const item = (kind: "b" | "t", name: string) => (
    <CommandItem
      key={kind + name}
      value={`${kind}:${name}`}
      onSelect={() => {
        onChoose(name);
        setOpen(false);
      }}
    >
      <span className={cn("w-3 shrink-0 text-primary", name !== selected && "invisible")}>
        <Icon name="check" className="size-3" />
      </span>
      <span className="grow truncate font-mono">{name}</span>
      {kind === "b" && name === ready?.default_branch && <span className="text-[11px] text-muted-foreground">默认分支</span>}
    </CommandItem>
  );
  return (
    <div className="fetch-refs flex min-w-0 flex-col gap-1">
      <span className="text-xs text-muted-foreground">分支 / tag</span>
      {refs.status === "failed" ? (
        <Notice tone="warn" className="refs-failure whitespace-pre-line">
          {`取不到分支和 tag 列表，仍可入库预览。\n${refs.reason}`}
        </Notice>
      ) : (
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild disabled={!ready}>
            <Button className="refs-trigger h-[30px] w-72 max-w-full justify-between rounded-lg px-2.5 font-normal">
              {ready ? (
                <span className="truncate font-mono">{selected || "默认分支"}</span>
              ) : (
                <span className="inline-flex items-center gap-1.5">
                  <Spinner />
                  正在读取分支和 tag…
                </span>
              )}
              <Icon name="down" className="size-3 shrink-0 text-muted-foreground" />
            </Button>
          </PopoverTrigger>
          {ready && (
            <PopoverContent align="start" className="refs-menu z-[60] w-72">
              <Command filter={(value, search) => (value.slice(2).toLowerCase().includes(search.trim().toLowerCase()) ? 1 : 0)}>
                <CommandInput placeholder="筛选分支和 tag" />
                <CommandList>
                  <CommandEmpty>没有匹配的分支或 tag</CommandEmpty>
                  {!!ready.branches?.length && <CommandGroup heading="分支">{ready.branches.map((name) => item("b", name))}</CommandGroup>}
                  {!!ready.tags?.length && <CommandGroup heading="Tag">{ready.tags.map((name) => item("t", name))}</CommandGroup>}
                </CommandList>
              </Command>
            </PopoverContent>
          )}
        </Popover>
      )}
    </div>
  );
}
