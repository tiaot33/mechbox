// 统一工具栏：左 = 侧栏开关 + 当前页面的标题与个数;右 = 搜索、刷新与右栏开关。
// 作用域在右栏点选，打开作用域页。
// 整条是窗口拖动区，能点的元素显式 no-drag(见 layout.ts 的 titlebarClass)。
import { useEffect, useRef } from "react";
import { useT } from "@/lib/i18n";
import { useLoadedSnapshot, reloadLibrary } from "@/lib/library";
import { pageTitle } from "@/lib/model";
import { cn } from "@/lib/utils";
import { setSearch, toggleLeft, toggleRight, useCurrentPage, usePageListing, useView } from "@/lib/view";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/input";
import { Icon } from "./icons";
import { titlebarClass } from "./layout";
import { BusyMark, Count } from "./parts";

export function Toolbar() {
  const s = useLoadedSnapshot();
  const page = useCurrentPage();
  const listing = usePageListing();
  const text = useView((v) => v.search);
  const left = useView((v) => v.left);
  const right = useView((v) => v.right);
  const search = useRef<HTMLInputElement>(null);
  const t = useT().toolbar;

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
          <BusyMark />
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
