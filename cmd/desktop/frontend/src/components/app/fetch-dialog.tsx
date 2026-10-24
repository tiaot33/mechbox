// 入库技能：给出一个来源输入(本地路径、GitHub、Git 仓库、下载链接或站点链接),由技能库识别来源并做入库预览，
// 再在 FetchReview 里挑选候选入库。Git 类来源在扫描前从下拉里选分支或 tag。
import { useEffect, useId, useRef, useState } from "react";
import { Dialogs, type CancellablePromise } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import { ErrorCode, type FetchPreview, type PreviewFetchRequest, type SourceRefs } from "@bindings/internal/library";
import { Modal, ui, useUI } from "@/lib/ui";
import { describeError } from "@/lib/errors";
import { useT } from "@/lib/i18n";
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

// openFetch 打开入库技能对话框(左栏"入库技能"、空库时的按钮);home 用来展开输入里的 ~。
export function openFetch(home: string) {
  return ui.open((close) => <FetchDialog home={home} onClose={close} />);
}

export function FetchDialog({ home, onClose }: { home: string; onClose: () => void }) {
  const ui = useUI();
  const t = useT().fetch;
  const [text, setText] = useState("");
  // chosen 是用户在下拉里选的分支或 tag;没选时为 null,下拉选中输入里带的分支，没带时选中默认分支。
  const [chosen, setChosen] = useState<string | null>(null);
  const [working, setWorking] = useState(false);
  // failure 是技能库给出的识别 / 入库预览失败原因，改了输入就清掉。
  const [failure, setFailure] = useState<string | null>(null);
  const [preview, setPreview] = useState<FetchPreview | null>(null);
  // pending 是还没完成的一次入库预览(先等分支列表，再调 PreviewFetch);改了输入、改选分支或关掉对话框就作废它，
  // 已发出的调用取消，返回的结果与失败原因也不再采用。
  const pending = useRef<{ call?: CancellablePromise<FetchPreview> } | null>(null);
  const discard = () => {
    void pending.current?.call?.cancel();
    pending.current = null;
  };
  useEffect(() => discard, []);
  const input = resolveFetchInput(text, home);
  const [refs, settleRefs] = useSourceRefs(input?.path ?? "");

  if (preview) {
    return <FetchReview preview={preview} onClose={onClose} onBack={() => setPreview(null)} />;
  }

  const ready = !!input?.path;
  const restart = () => {
    discard();
    setWorking(false);
    setFailure(null);
  };
  const edit = (v: string) => {
    setText(v);
    setChosen(null);
    restart();
  };

  const previewFetch = async () => {
    if (pending.current || !input?.path) return;
    const op: { call?: CancellablePromise<FetchPreview> } = {};
    pending.current = op;
    setWorking(true);
    setFailure(null);
    // 列表还没取回时先等它(还在防抖就马上取):tree 链接里带斜杠的分支要从列表里认出来，显式交给技能库。
    const listed = await settleRefs();
    if (pending.current !== op) return;
    const req = fetchRequest(input.path, listed, chosen);
    const call = LibraryService.PreviewFetch(req);
    op.call = call;
    const result = await ui.run(() => call, {
      reload: false,
      onError: (err) => {
        if (pending.current === op) setFailure(describeError(err));
        return true;
      },
    });
    if (pending.current !== op) return;
    pending.current = null;
    setWorking(false);
    if (result) setPreview(result);
  };
  const submitOnEnter = (e: React.KeyboardEvent) => e.key === "Enter" && ready && previewFetch();

  return (
    <Modal
      title={t.title}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t.cancel}</Button>
          <Button variant="primary" disabled={!ready} loading={working} onClick={previewFetch}>
            {t.preview}
          </Button>
        </>
      }
    >
      <Hint>{t.hint}</Hint>
      <div className="flex min-w-0 items-center gap-2">
        <Input
          className="grow"
          autoFocus
          placeholder={t.inputPlaceholder}
          value={text}
          onChange={(e) => edit(e.target.value)}
          onKeyDown={submitOnEnter}
        />
        <Button
          onClick={async () => {
            const dir = await pickDirectory(t.chooseDirectory);
            if (dir) edit(dir);
          }}
        >
          {t.choose}
        </Button>
      </div>
      {input?.error && <FieldError>{input.error}</FieldError>}
      <RefPicker
        refs={refs}
        chosen={chosen}
        locked={refs.status === "ready" && !!refs.refs.input_ref && !switchRef(input?.path ?? "", refs.refs.input_ref, refs.refs.input_ref)}
        onChoose={(ref) => {
          setChosen(ref);
          restart();
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

// useSourceRefs 在输入停下约半秒后向技能库取分支和 tag 列表;输入变了或对话框关了，旧请求取消、结果不再采用。
// 本地路径不取;技能库认不出是 Git 类来源时报 InvalidArgument,下拉不显示。
// 第二个返回值等这次输入的列表取回(还在防抖就马上取),取不到或不是 Git 类来源时为 null。
function useSourceRefs(source: string): [RefsState, () => Promise<SourceRefs | null>] {
  const [state, setState] = useState<RefsState>({ status: "hidden" });
  const request = useRef<{ start: () => void; result: Promise<SourceRefs | null> } | null>(null);
  useEffect(() => {
    if (!source || source.startsWith("/")) {
      setState({ status: "hidden" });
      request.current = null;
      return;
    }
    // 下拉已经显示着(从一个 Git 类输入改成另一个)时直接换成正在读取，旧列表不留在新输入下面;
    // 没显示时等请求慢于 150ms 再显示，认不出的输入不会闪一下下拉。
    setState((cur) => (cur.status === "hidden" ? cur : { status: "loading" }));
    let live = true;
    let call: CancellablePromise<SourceRefs> | undefined;
    let slow: number | undefined;
    let settle: (refs: SourceRefs | null) => void = () => {};
    const result = new Promise<SourceRefs | null>((resolve) => (settle = resolve));
    const start = () => {
      if (call) return;
      window.clearTimeout(timer);
      call = LibraryService.ListSourceRefs(source);
      slow = window.setTimeout(() => setState({ status: "loading" }), 150);
      call
        .then(
          (refs) => {
            settle(refs);
            if (live) setState({ status: "ready", refs });
          },
          (err) => {
            settle(null);
            const e = toLibraryError(err);
            if (!live || e.code === ErrorCode.Canceled) return;
            setState(e.code === ErrorCode.InvalidArgument ? { status: "hidden" } : { status: "failed", reason: describeError(e) });
          },
        )
        .finally(() => window.clearTimeout(slow));
    };
    const timer = window.setTimeout(start, 500);
    request.current = { start, result };
    return () => {
      live = false;
      settle(null);
      window.clearTimeout(timer);
      window.clearTimeout(slow);
      void call?.cancel();
    };
  }, [source]);
  const settleRefs = () => {
    const current = request.current;
    if (!current) return Promise.resolve(null);
    current.start();
    return current.result;
  };
  return [state, settleRefs];
}

// fetchRequest 是入库预览发给技能库的请求。输入里带了分支(tree 链接、#ref)时显式传分支:
// tree 链接里带斜杠的分支，技能库要显式给出才能和目录分开。下拉改选了别的分支时，只在请求里把输入里的分支换掉，
// 输入框里的文字不变;选回输入里的分支时输入照原样发。
// 输入里没带分支时传下拉选中的，选的是默认分支或没取到列表时不传。
function fetchRequest(input: string, refs: SourceRefs | null, chosen: string | null): PreviewFetchRequest {
  if (!refs) return { input };
  if (refs.input_ref) {
    const switched = chosen && chosen !== refs.input_ref ? switchRef(input, refs.input_ref, chosen) : null;
    if (chosen && switched) return { input: switched, ref: chosen };
    return { input, ref: refs.input_ref };
  }
  const selected = chosen ?? refs.default_branch;
  return { input, ref: selected && selected !== refs.default_branch ? selected : undefined };
}

// switchRef 把输入里带的分支 from 换成 to:去掉结尾的 #from;tree 链接(GitHub 的 /tree/、GitLab 的 /-/tree/)里
// 紧跟着的 from 整段换成 to,后面的目录保留。from 是技能库给出的分支，带斜杠的也整段替换;完整 commit 技能库会转成小写，
// 这里比较时不分大小写。输入里认不出 from 时返回 null。
function switchRef(input: string, from: string, to: string): string | null {
  const fold = /^[0-9a-f]{40}$/.test(from) ? (v: string) => v.toLowerCase() : (v: string) => v;
  let rest = input;
  let found = false;
  if (fold(rest.slice(-from.length - 1)) === "#" + from) {
    rest = rest.slice(0, -from.length - 1);
    found = true;
  }
  for (const marker of ["/-/tree/", "/tree/"]) {
    for (let at = rest.indexOf(marker); at >= 0; at = rest.indexOf(marker, at + 1)) {
      const start = at + marker.length;
      const after = rest.slice(start + from.length);
      if (fold(rest.slice(start, start + from.length)) === from && (after === "" || after.startsWith("/"))) {
        return rest.slice(0, start) + to + after;
      }
    }
  }
  return found ? rest : null;
}

// selectedRef 是下拉当前选中的分支或 tag:用户选过的，否则输入里带的，否则默认分支。
function selectedRef(refs: SourceRefs, chosen: string | null): string {
  return chosen ?? (refs.input_ref || refs.default_branch);
}

// RefPicker 是分支 / tag 下拉：分支和 tag 分组列出，可输入文字筛选，默认选中输入里带的分支，没带时选中默认分支。
// locked 时(输入里带了分支，但认不出它在输入里的位置，换不掉)只显示输入里的分支，要换分支得改输入。
function RefPicker({
  refs,
  chosen,
  locked,
  onChoose,
}: {
  refs: RefsState;
  chosen: string | null;
  locked: boolean;
  onChoose: (ref: string) => void;
}) {
  const t = useT().fetch;
  const [open, setOpen] = useState(false);
  const labelId = useId();
  const triggerId = useId();
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
      {kind === "b" && name === ready?.default_branch && <span className="text-[11px] text-muted-foreground">{t.defaultBranch}</span>}
    </CommandItem>
  );
  return (
    <div className="fetch-refs flex min-w-0 flex-col gap-1">
      <span id={labelId} className="text-xs text-muted-foreground">
        {t.refs}
      </span>
      {refs.status === "failed" ? (
        <Notice tone="warn" className="refs-failure whitespace-pre-line">
          {t.refsFailed(refs.reason)}
        </Notice>
      ) : (
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild disabled={!ready || locked}>
            <Button id={triggerId} aria-labelledby={`${labelId} ${triggerId}`} className="refs-trigger h-[30px] w-72 max-w-full justify-between rounded-lg px-2.5 font-normal">
              {ready ? (
                <span className="truncate font-mono">{selected || t.defaultBranch}</span>
              ) : (
                <span className="inline-flex items-center gap-1.5">
                  <Spinner />
                  {t.readingRefs}
                </span>
              )}
              <Icon name="down" className="size-3 shrink-0 text-muted-foreground" />
            </Button>
          </PopoverTrigger>
          {ready && (
            <PopoverContent align="start" className="refs-menu z-[60] w-72">
              <Command label={t.filterRefs} filter={(value, search) => (value.slice(2).toLowerCase().includes(search.trim().toLowerCase()) ? 1 : 0)}>
                <CommandInput placeholder={t.filterRefs} />
                <CommandList>
                  <CommandEmpty>{t.noRefs}</CommandEmpty>
                  {!!ready.branches?.length && <CommandGroup heading={t.branches}>{ready.branches.map((name) => item("b", name))}</CommandGroup>}
                  {!!ready.tags?.length && <CommandGroup heading={t.tags}>{ready.tags.map((name) => item("t", name))}</CommandGroup>}
                </CommandList>
              </Command>
            </PopoverContent>
          )}
        </Popover>
      )}
      {locked && <Hint className="refs-locked">{t.locked(ready?.input_ref)}</Hint>}
    </div>
  );
}
