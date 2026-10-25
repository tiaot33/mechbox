// 统一工具栏：左 = 侧栏开关 + 当前页面的标题与个数;右 = 搜索、刷新与右栏开关。
// 作用域在右栏点选，打开作用域页。
// 整条是窗口拖动区，能点的元素显式 no-drag(见 layout.ts 的 titlebarClass)。
import { useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { useLoadedSnapshot, reloadLibrary } from "@/lib/library";
import { pageTitle } from "@/lib/model";
import { useBusy, useSettled } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { setSearch, toggleLeft, toggleRight, useCurrentPage, usePageListing, useView } from "@/lib/view";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/input";
import { StatusMark } from "@/components/ui/status-mark";
import { Icon } from "./icons";
import { titlebarClass } from "./layout";
import { Count } from "./parts";

export function Toolbar() {
  const s = useLoadedSnapshot();
  const busy = useBusy();
  const page = useCurrentPage();
  const listing = usePageListing();
  const text = useView((v) => v.search);
  const left = useView((v) => v.left);
  const right = useView((v) => v.right);
  const search = useRef<HTMLInputElement>(null);
  const tt = useT();
  const t = tt.toolbar;

  // ⌘K 聚焦搜索框。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        search.current?.focus();
        search.current?.select();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <header className={titlebarClass} style={{ "--wails-draggable": "drag" } as React.CSSProperties}>
      <div className="flex min-w-0 items-center gap-2">
        <div className="w-16" />
        <Button variant="ghost" size="icon" className={cn(left && "text-foreground")} onClick={toggleLeft} title={t.toggleLeft}>
          <Icon name="sidebarL" />
        </Button>
        <span className="tb-crumb ml-1 inline-flex min-w-0 items-center gap-1.5 text-[13px] font-semibold whitespace-nowrap">
          <b className="truncate">{s ? pageTitle(s, page) : "Mechbox"}</b>
          {listing && <Count value={listing.total} />}
          <BusyMark busy={busy} title={tt.common.working} />
        </span>
      </div>
      <div />
      <div className="flex min-w-0 items-center justify-end gap-2">
        <SearchInput
          ref={search}
          className="h-[26px] min-w-[110px] flex-[0_1_240px] px-[7px] text-[11.5px]"
          icon={<Icon name="search" className="size-[13px] text-muted-foreground" />}
          suffix={<kbd className="rounded border border-border px-1 font-sans text-[10.5px] text-subtle">⌘K</kbd>}
          placeholder={t.search}
          value={text}
          onChange={(e) => setSearch(e.target.value)}
          onKeyDown={(e) => e.key === "Escape" && setSearch("")}
        />
        <Button variant="ghost" size="icon" onClick={() => void reloadLibrary()} title={t.refresh}>
          <Icon name="refresh" />
        </Button>
        <Button variant="ghost" size="icon" className={cn(right && "text-foreground")} onClick={toggleRight} title={t.toggleRight}>
          <Icon name="sidebarR" />
        </Button>
      </div>
    </header>
  );
}

// BusyMark 是标题旁的状态圈：有写操作进行中时转圈，全部结束后转动的弧合成整圈、画出对勾(有失败的画叉),停一下再淡出。
// 转圈与对勾是同一个状态圈换状态，才有形变;转圈时带 data-slot="spinner",和别处的转圈一样(e2e 等它消失)。
function BusyMark({ busy, title }: { busy: boolean; title: string }) {
  const settled = useSettled();
  const [dismissed, setDismissed] = useState<number | null>(null);
  useEffect(() => {
    if (busy || !settled) return;
    const timer = window.setTimeout(() => setDismissed(settled.id), 1100);
    return () => window.clearTimeout(timer);
  }, [busy, settled]);
  const status = busy ? "running" : settled && settled.id !== dismissed ? (settled.failed ? "failed" : "done") : null;
  if (!status) return null;
  return (
    <span
      data-slot={busy ? "spinner" : undefined}
      role="status"
      aria-label={busy ? title : undefined}
      title={busy ? title : undefined}
      className={cn("inline-flex text-muted-foreground", !busy && "animate-[fade-in_0.2s_ease-out_0.85s_reverse_both]")}
    >
      <StatusMark status={status} size={11} strokeWidth={3} />
    </span>
  );
}
