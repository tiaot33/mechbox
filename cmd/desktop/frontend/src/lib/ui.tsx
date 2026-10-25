// 全局界面服务：toast、弹窗(确认 / 输入 / 自定义)与写操作执行器 run;组件外的写操作流程也直接用 ui。
// 写操作统一经 run:显示忙碌、失败时按错误码提示、完成后让查询缓存失效重读(前端在自己的写操作之后重读，见 ADR 0008)。
import { useState, type ReactNode } from "react";
import { create } from "zustand";
import { ErrorCode } from "@bindings/internal/library";
import { Icon } from "@/components/app/icons";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogMotionContext,
  DialogTitle,
  type DialogMotion,
} from "@/components/ui/dialog";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { SwipeToast } from "@/components/ui/swipe-toast";
import { cn } from "./utils";
import { describeError } from "./errors";
import { useT } from "./i18n";
import { reloadLibrary, toLibraryError, type LibraryError } from "./library";

export interface Choice {
  value: string;
  label: string;
  desc?: string;
}

export interface ConfirmOptions {
  title: string;
  body?: ReactNode;
  confirm?: string; // 确认按钮文案
  danger?: boolean;
  choices?: Choice[]; // 给出时是单选，resolve 为选中的 value
  initial?: string;
}

export interface PromptOptions {
  title: string;
  label?: string;
  initial?: string;
  confirm?: string;
}

export interface RunOptions<T> {
  success?: string | ((value: T) => string | undefined);
  reload?: boolean; // 默认 true
  // onError 返回 true 表示已自行处理，run 不再弹 toast。
  onError?: (err: LibraryError) => boolean;
  // failed 返回 true 表示调用成功了但有逐项失败(结果里列出没完成的),顶栏的状态圈画叉而不是对勾。
  failed?: (value: T) => boolean;
  // mark 为 false 时顶栏的状态圈不跟着转，也不画对勾或叉，进度画在发起它的按钮上(如左栏的检查更新);默认 true。
  mark?: boolean;
}

// ToastAction 是 toast 上的一个按钮(如更新完成后的"回滚");带按钮的 toast 停留得久一些。
export interface ToastAction {
  label: string;
  onClick: () => void;
}

// OpenOptions.origin 是点了哪个元素打开的弹窗：弹窗从它的位置放大出来，关掉时缩回去。
export interface OpenOptions {
  origin?: Element | null;
}

interface UIService {
  toast: (message: string, tone?: "ok" | "error", action?: ToastAction) => void;
  // open 打开一个自定义弹窗;render 拿到 close,close(value) 关闭并 resolve。
  open: <T>(render: (close: (value?: T) => void) => ReactNode, opts?: OpenOptions) => Promise<T | undefined>;
  confirm: (opts: ConfirmOptions) => Promise<string | undefined>;
  prompt: (opts: PromptOptions) => Promise<string | undefined>;
  run: <T>(task: () => Promise<T>, opts?: RunOptions<T>) => Promise<T | undefined>;
  // reload 重读界面上的数据：不经 run 的只读调用也会写库(如生成更新计划时写下的检查结论),之后由调用方让界面跟上。
  reload: () => void;
}

interface ToastState {
  id: number; // 每条 toast 一个，新的一条换掉旧的、重新升起
  message: string;
  tone: "ok" | "error";
  action?: ToastAction;
}

interface DialogEntry {
  id: number;
  node: ReactNode;
  motion: DialogMotion;
}

// 弹窗栈、toast 与进行中的写操作数放在 store 里，组件外的写操作流程也能打开弹窗、弹 toast;UIHost 把它们画出来。
// marked 是其中顶栏状态圈要跟着转的写操作数(不含 mark 为 false 的)。settled 是这些写操作全部结束时的结果(有失败的就是 failed),
// 顶栏的状态圈据此画对勾或叉;这一轮只有被取消的写操作时不更新。finished、failed 记着这一轮里有没有没被取消的、有没有失败的。
const useUIStore = create<{
  dialogs: DialogEntry[];
  toast: ToastState | null;
  pending: number;
  marked: number;
  finished: boolean;
  failed: boolean;
  settled: { id: number; failed: boolean } | null;
}>()(() => ({
  dialogs: [],
  toast: null,
  pending: 0,
  marked: 0,
  finished: false,
  failed: false,
  settled: null,
}));
const set = useUIStore.setState;

let nextToastID = 1;
let nextDialogID = 1;
let nextSettledID = 1;

function toast(message: string, tone: "ok" | "error" = "ok", action?: ToastAction) {
  set({ toast: { id: nextToastID++, message, tone, action } });
}

function toastDuration(t: ToastState): number {
  return t.action ? 6000 : t.tone === "error" ? 5200 : 2400;
}

function open<T>(render: (close: (value?: T) => void) => ReactNode, opts: OpenOptions = {}): Promise<T | undefined> {
  return new Promise<T | undefined>((resolve) => {
    const id = nextDialogID++;
    const remove = () => set((s) => ({ dialogs: s.dialogs.filter((d) => d.id !== id) }));
    // 从某个元素打开的弹窗关掉时先缩回去，缩完再从栈里拿掉;结果马上交给调用方，不等动画。
    const close = (value?: T) => {
      const entry = useUIStore.getState().dialogs.find((d) => d.id === id);
      if (entry?.motion.origin && !entry.motion.closing) {
        const motion = { ...entry.motion, closing: true };
        set((s) => ({ dialogs: s.dialogs.map((d) => (d.id === id ? { ...d, motion } : d)) }));
      } else if (!entry?.motion.closing) {
        remove();
      }
      resolve(value);
    };
    const motion: DialogMotion = { origin: opts.origin ?? undefined, closing: false, exited: remove, last: { played: false } };
    set((s) => ({ dialogs: [...s.dialogs, { id, node: render(close), motion }] }));
  });
}

async function run<T>(task: () => Promise<T>, opts: RunOptions<T> = {}): Promise<T | undefined> {
  const mark = opts.mark !== false;
  set((s) => ({ pending: s.pending + 1, marked: s.marked + (mark ? 1 : 0) }));
  let outcome: "done" | "failed" | "canceled" = "done";
  try {
    const value = await task();
    if (opts.failed?.(value)) outcome = "failed";
    const message = typeof opts.success === "function" ? opts.success(value) : opts.success;
    if (message) toast(message);
    return value;
  } catch (e) {
    const err = toLibraryError(e);
    if (err.code === ErrorCode.Canceled) {
      outcome = "canceled";
      return undefined;
    }
    outcome = "failed";
    if (!opts.onError?.(err)) toast(describeError(err), "error");
    return undefined;
  } finally {
    set((s) => {
      const pending = s.pending - 1;
      if (!mark) return { pending };
      const marked = s.marked - 1;
      const finished = s.finished || outcome !== "canceled";
      const failed = s.failed || outcome === "failed";
      if (marked > 0) return { pending, marked, finished, failed };
      return { pending, marked, finished: false, failed: false, ...(finished && { settled: { id: nextSettledID++, failed } }) };
    });
    // 失败也重读：部分成功(如组安装里跳过的成员)或状态已被别处改变时，界面要跟上。
    if (opts.reload !== false) void reloadLibrary();
  }
}

// ui 是全局界面服务;组件里也可以用 useUI() 取同一个。
export const ui: UIService = {
  toast,
  open,
  confirm: (opts) => open<string>((close) => <ConfirmDialog opts={opts} close={close} />),
  prompt: (opts) => open<string>((close) => <PromptDialog opts={opts} close={close} />),
  run,
  reload: () => void reloadLibrary(),
};

export function useUI(): UIService {
  return ui;
}

// useBusy 是有没有经 run 进行中的写操作(顶栏转圈、禁用会冲突的按钮)。
export function useBusy(): boolean {
  return useUIStore((s) => s.pending > 0);
}

// useMarkBusy 是顶栏的状态圈该不该转：有 mark 不为 false 的写操作进行中。
export function useMarkBusy(): boolean {
  return useUIStore((s) => s.marked > 0);
}

// useSettled 是最近一次写操作全部结束时的结果;id 每次结束都换，用来只闪一下对勾或叉。
export function useSettled(): { id: number; failed: boolean } | null {
  return useUIStore((s) => s.settled);
}

// UIHost 画出打开着的弹窗与 toast,放在主界面最外层。
export function UIHost() {
  const dialogs = useUIStore((s) => s.dialogs);
  const toastState = useUIStore((s) => s.toast);
  return (
    <>
      {dialogs.map((d) => (
        <DialogMotionContext key={d.id} value={d.motion}>
          {d.node}
        </DialogMotionContext>
      ))}
      {toastState && (
        <SwipeToast
          key={toastState.id}
          tone={toastState.tone}
          duration={toastDuration(toastState)}
          actionLabel={toastState.action?.label}
          onAction={toastState.action?.onClick}
          onClose={() => set((s) => (s.toast?.id === toastState.id ? { toast: null } : {}))}
        >
          {toastState.message}
        </SwipeToast>
      )}
    </>
  );
}

// Modal 是弹窗外壳：标题、关闭按钮、可滚动的主体与底部按钮行;点遮罩或按 Esc 关闭，叠了几层时只关最上面一层。
export function Modal({
  title,
  onClose,
  wide,
  footer,
  children,
}: {
  title: ReactNode;
  onClose: () => void;
  wide?: boolean;
  footer?: ReactNode;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent wide={wide}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogClose asChild>
            <Button variant="ghost" size="icon" aria-label={t.common.close}>
              <Icon name="close" />
            </Button>
          </DialogClose>
        </DialogHeader>
        <DialogBody>{children}</DialogBody>
        {footer && <DialogFooter>{footer}</DialogFooter>}
      </DialogContent>
    </Dialog>
  );
}

function ConfirmDialog({ opts, close }: { opts: ConfirmOptions; close: (value?: string) => void }) {
  const t = useT();
  const [choice, setChoice] = useState(opts.initial ?? opts.choices?.[0]?.value ?? "ok");
  return (
    <Modal
      title={opts.title}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant={opts.danger ? "danger" : "primary"} onClick={() => close(choice)} autoFocus>
            {opts.confirm ?? t.common.ok}
          </Button>
        </>
      }
    >
      {opts.body}
      {opts.choices && <ChoiceList choices={opts.choices} value={choice} onChange={setChoice} />}
    </Modal>
  );
}

// ChoiceList 是弹窗里的单选项，确认框与自己画的弹窗(如卸载 Mechbox)共用。
export function ChoiceList({ choices, value, onChange }: { choices: Choice[]; value: string; onChange: (value: string) => void }) {
  return choices.map((c) => (
    <label
      key={c.value}
      data-slot="choice"
      className={cn(
        "flex cursor-pointer items-start gap-2 rounded-[9px] border border-border-subtle bg-white px-2.5 py-2",
        value === c.value && "border-primary-border bg-primary-soft",
      )}
    >
      <input type="radio" className="mt-[3px] accent-primary" checked={value === c.value} onChange={() => onChange(c.value)} />
      <div>
        <b className="block font-semibold">{c.label}</b>
        {c.desc && <span className="text-xs text-muted-foreground">{c.desc}</span>}
      </div>
    </label>
  ));
}

function PromptDialog({ opts, close }: { opts: PromptOptions; close: (value?: string) => void }) {
  const t = useT();
  const [value, setValue] = useState(opts.initial ?? "");
  const submit = () => value.trim() && close(value.trim());
  return (
    <Modal
      title={opts.title}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant="primary" onClick={submit} disabled={!value.trim()}>
            {opts.confirm ?? t.common.ok}
          </Button>
        </>
      }
    >
      <Field label={opts.label}>
        <Input
          autoFocus
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
      </Field>
    </Modal>
  );
}
