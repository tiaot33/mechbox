// 全局界面服务：toast、弹窗(确认 / 输入 / 自定义)与写操作执行器 run;组件外的写操作流程也直接用 ui。
// 写操作统一经 run:显示忙碌、失败时按错误码提示、完成后让查询缓存失效重读(前端在自己的写操作之后重读，见 ADR 0008)。
import { useState, type ReactNode } from "react";
import { create } from "zustand";
import { ErrorCode } from "@bindings/internal/library";
import { Icon } from "@/components/app/icons";
import { Button } from "@/components/ui/button";
import { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
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
}

// ToastAction 是 toast 上的一个按钮(如更新完成后的"回滚");带按钮的 toast 停留得久一些。
export interface ToastAction {
  label: string;
  onClick: () => void;
}

interface UIService {
  toast: (message: string, tone?: "ok" | "error", action?: ToastAction) => void;
  // open 打开一个自定义弹窗;render 拿到 close,close(value) 关闭并 resolve。
  open: <T>(render: (close: (value?: T) => void) => ReactNode) => Promise<T | undefined>;
  confirm: (opts: ConfirmOptions) => Promise<string | undefined>;
  prompt: (opts: PromptOptions) => Promise<string | undefined>;
  run: <T>(task: () => Promise<T>, opts?: RunOptions<T>) => Promise<T | undefined>;
  // reload 重读界面上的数据：不经 run 的只读调用也会写库(如生成更新计划时写下的检查结论),之后由调用方让界面跟上。
  reload: () => void;
}

interface ToastState {
  message: string;
  tone: "ok" | "error";
  show: boolean;
  action?: ToastAction;
}

// 弹窗栈、toast 与进行中的写操作数放在 store 里，组件外的写操作流程也能打开弹窗、弹 toast;UIHost 把它们画出来。
const useUIStore = create<{ dialogs: { id: number; node: ReactNode }[]; toast: ToastState; pending: number }>()(() => ({
  dialogs: [],
  toast: { message: "", tone: "ok", show: false },
  pending: 0,
}));
const set = useUIStore.setState;

let toastTimer: number | undefined;
let nextDialogID = 1;

function toast(message: string, tone: "ok" | "error" = "ok", action?: ToastAction) {
  window.clearTimeout(toastTimer);
  set({ toast: { message, tone, show: true, action } });
  toastTimer = window.setTimeout(() => set((s) => ({ toast: { ...s.toast, show: false } })), action ? 6000 : tone === "error" ? 5200 : 2400);
}

function hideToast() {
  window.clearTimeout(toastTimer);
  set((s) => ({ toast: { ...s.toast, show: false } }));
}

function open<T>(render: (close: (value?: T) => void) => ReactNode): Promise<T | undefined> {
  return new Promise<T | undefined>((resolve) => {
    const id = nextDialogID++;
    const close = (value?: T) => {
      set((s) => ({ dialogs: s.dialogs.filter((d) => d.id !== id) }));
      resolve(value);
    };
    set((s) => ({ dialogs: [...s.dialogs, { id, node: render(close) }] }));
  });
}

async function run<T>(task: () => Promise<T>, opts: RunOptions<T> = {}): Promise<T | undefined> {
  set((s) => ({ pending: s.pending + 1 }));
  try {
    const value = await task();
    const message = typeof opts.success === "function" ? opts.success(value) : opts.success;
    if (message) toast(message);
    return value;
  } catch (e) {
    const err = toLibraryError(e);
    if (err.code === ErrorCode.Canceled) return undefined;
    if (!opts.onError?.(err)) toast(describeError(err), "error");
    return undefined;
  } finally {
    set((s) => ({ pending: s.pending - 1 }));
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

// UIHost 画出打开着的弹窗与 toast,放在主界面最外层。
export function UIHost() {
  const dialogs = useUIStore((s) => s.dialogs);
  const toastState = useUIStore((s) => s.toast);
  return (
    <>
      {dialogs.map((d) => (
        <div key={d.id}>{d.node}</div>
      ))}
      <div
        data-slot="toast"
        data-state={toastState.show ? "open" : "closed"}
        data-tone={toastState.tone}
        role="status"
        className={cn(
          "pointer-events-none fixed bottom-[66px] left-1/2 z-[101] max-w-[640px] -translate-x-1/2 translate-y-2.5 rounded-[14px] border border-border bg-white px-4 py-2 text-center text-[12.5px] break-words whitespace-pre-line opacity-0 shadow-toast transition-all duration-200",
          "data-[state=open]:translate-y-0 data-[state=open]:opacity-100 data-[tone=error]:border-bad-border data-[tone=error]:text-bad-strong",
        )}
      >
        {toastState.message}
        {toastState.action && (
          <Button
            variant="link"
            className={cn("ml-2", toastState.show && "pointer-events-auto")}
            onClick={() => {
              hideToast();
              toastState.action?.onClick();
            }}
          >
            {toastState.action.label}
          </Button>
        )}
      </div>
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
