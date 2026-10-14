// 全局界面服务：toast、弹窗(确认 / 输入 / 自定义)与写操作执行器 run。
// 写操作统一经 run:显示忙碌、失败时按错误码提示、成功后重读快照(前端在自己的写操作之后重读，见 ADR 0008)。
import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import { ErrorCode } from "@bindings/internal/library";
import { Icon } from "@/components/app/icons";
import { Button } from "@/components/ui/button";
import { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { cn } from "./utils";
import { describeError } from "./errors";
import { toLibraryError, type LibraryError } from "./library";

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
  busy: boolean;
}

const UIContext = createContext<UIService | null>(null);

export function useUI(): UIService {
  const ui = useContext(UIContext);
  if (!ui) throw new Error("useUI outside UIProvider");
  return ui;
}

export function UIProvider({ reload, children }: { reload: () => void; children: ReactNode }) {
  const [toastState, setToast] = useState<{ message: string; tone: "ok" | "error"; show: boolean; action?: ToastAction }>({
    message: "",
    tone: "ok",
    show: false,
  });
  const timer = useRef<number | undefined>(undefined);
  const [dialogs, setDialogs] = useState<{ id: number; node: ReactNode }[]>([]);
  const nextID = useRef(1);
  const [pending, setPending] = useState(0);

  const toast = useCallback((message: string, tone: "ok" | "error" = "ok", action?: ToastAction) => {
    window.clearTimeout(timer.current);
    setToast({ message, tone, show: true, action });
    timer.current = window.setTimeout(() => setToast((t) => ({ ...t, show: false })), action ? 6000 : tone === "error" ? 5200 : 2400);
  }, []);
  const hideToast = () => {
    window.clearTimeout(timer.current);
    setToast((t) => ({ ...t, show: false }));
  };

  const open = useCallback(<T,>(render: (close: (value?: T) => void) => ReactNode) => {
    return new Promise<T | undefined>((resolve) => {
      const id = nextID.current++;
      const close = (value?: T) => {
        setDialogs((list) => list.filter((d) => d.id !== id));
        resolve(value);
      };
      setDialogs((list) => [...list, { id, node: render(close) }]);
    });
  }, []);

  const confirm = useCallback(
    (opts: ConfirmOptions) => open<string>((close) => <ConfirmDialog opts={opts} close={close} />),
    [open],
  );

  const prompt = useCallback(
    (opts: PromptOptions) => open<string>((close) => <PromptDialog opts={opts} close={close} />),
    [open],
  );

  const run = useCallback(
    async <T,>(task: () => Promise<T>, opts: RunOptions<T> = {}) => {
      setPending((n) => n + 1);
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
        setPending((n) => n - 1);
        // 失败也重读：部分成功(如组安装里跳过的成员)或状态已被别处改变时，界面要跟上。
        if (opts.reload !== false) reload();
      }
    },
    [reload, toast],
  );

  const service = useMemo(
    () => ({ toast, open, confirm, prompt, run, busy: pending > 0 }),
    [toast, open, confirm, prompt, run, pending],
  );

  return (
    <UIContext.Provider value={service}>
      {children}
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
    </UIContext.Provider>
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
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent wide={wide}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogClose asChild>
            <Button variant="ghost" size="icon" aria-label="关闭">
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
  const [choice, setChoice] = useState(opts.initial ?? opts.choices?.[0]?.value ?? "ok");
  return (
    <Modal
      title={opts.title}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>取消</Button>
          <Button variant={opts.danger ? "danger" : "primary"} onClick={() => close(choice)} autoFocus>
            {opts.confirm ?? "确定"}
          </Button>
        </>
      }
    >
      {opts.body}
      {opts.choices?.map((c) => (
        <label
          key={c.value}
          data-slot="choice"
          className={cn(
            "flex cursor-pointer items-start gap-2 rounded-[9px] border border-border-subtle bg-white px-2.5 py-2",
            choice === c.value && "border-primary-border bg-primary-soft",
          )}
        >
          <input type="radio" className="mt-[3px] accent-primary" checked={choice === c.value} onChange={() => setChoice(c.value)} />
          <div>
            <b className="block font-semibold">{c.label}</b>
            {c.desc && <span className="text-xs text-muted-foreground">{c.desc}</span>}
          </div>
        </label>
      ))}
    </Modal>
  );
}

function PromptDialog({ opts, close }: { opts: PromptOptions; close: (value?: string) => void }) {
  const [value, setValue] = useState(opts.initial ?? "");
  const submit = () => value.trim() && close(value.trim());
  return (
    <Modal
      title={opts.title}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>取消</Button>
          <Button variant="primary" onClick={submit} disabled={!value.trim()}>
            {opts.confirm ?? "确定"}
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
