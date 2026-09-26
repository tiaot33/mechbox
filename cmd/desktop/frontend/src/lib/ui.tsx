// 全局界面服务：toast、弹窗(确认 / 输入 / 自定义)与写操作执行器 run。
// 写操作统一经 run:显示忙碌、失败时按错误码提示、成功后重读快照(前端在自己的写操作之后重读，见 ADR 0013)。
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ErrorCode } from "@bindings/internal/library";
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
  placeholder?: string;
  confirm?: string;
}

export interface RunOptions<T> {
  success?: string | ((value: T) => string | undefined);
  reload?: boolean; // 默认 true
  // onError 返回 true 表示已自行处理，run 不再弹 toast。
  onError?: (err: LibraryError) => boolean | Promise<boolean>;
}

interface UIService {
  toast: (message: string, tone?: "ok" | "error") => void;
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
  const [toastState, setToast] = useState<{ message: string; tone: "ok" | "error"; show: boolean }>({
    message: "",
    tone: "ok",
    show: false,
  });
  const timer = useRef<number | undefined>(undefined);
  const [dialogs, setDialogs] = useState<{ id: number; node: ReactNode }[]>([]);
  const nextID = useRef(1);
  const [pending, setPending] = useState(0);

  const toast = useCallback((message: string, tone: "ok" | "error" = "ok") => {
    window.clearTimeout(timer.current);
    setToast({ message, tone, show: true });
    timer.current = window.setTimeout(() => setToast((t) => ({ ...t, show: false })), tone === "error" ? 5200 : 2400);
  }, []);

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
        if (!(opts.onError && (await opts.onError(err)))) toast(describeError(err), "error");
        return undefined;
      } finally {
        setPending((n) => n - 1);
        // 失败也重读：部分成功(如组激活里跳过的成员)或状态已被别处改变时，界面要跟上。
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
      <div className={`toast ${toastState.show ? "show" : ""} ${toastState.tone}`} role="status">
        {toastState.message}
      </div>
    </UIContext.Provider>
  );
}

// Modal 是弹窗外壳：点遮罩或按 Esc 关闭。
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
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="modal-scrim window-portal" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`modal ${wide ? "wide" : ""}`} role="dialog" aria-modal="true">
        <div className="modal-head">
          <h3>{title}</h3>
          <button type="button" className="icon-btn" onClick={onClose} aria-label="关闭">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>
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
          <button type="button" className="pill-btn" onClick={() => close()}>
            取消
          </button>
          <button
            type="button"
            className={`pill-btn ${opts.danger ? "danger" : "accent"}`}
            onClick={() => close(choice)}
            autoFocus
          >
            {opts.confirm ?? "确定"}
          </button>
        </>
      }
    >
      {opts.body}
      {opts.choices?.map((c) => (
        <label key={c.value} className={`choice ${choice === c.value ? "on" : ""}`}>
          <input type="radio" checked={choice === c.value} onChange={() => setChoice(c.value)} />
          <div>
            <b>{c.label}</b>
            {c.desc && <span>{c.desc}</span>}
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
          <button type="button" className="pill-btn" onClick={() => close()}>
            取消
          </button>
          <button type="button" className="pill-btn accent" onClick={submit} disabled={!value.trim()}>
            {opts.confirm ?? "确定"}
          </button>
        </>
      }
    >
      <label className="field">
        {opts.label && <span>{opts.label}</span>}
        <input
          className="input"
          autoFocus
          value={value}
          placeholder={opts.placeholder}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
      </label>
    </Modal>
  );
}
