// 统一工具栏：左 = 侧栏开关 + 当前页面的标题与个数(关系图页前面多一个返回按钮，回到打开它之前的那一页);右 = 搜索、刷新与右栏开关。
// 作用域在右栏点选，打开作用域页。
// 外框(拖动区、红绿灯的位置、Windows 的窗口按钮)见 titlebar.tsx。
import { useEffect, useRef } from "react";
import { useT } from "@/lib/i18n";
import { useLoadedSnapshot, reloadLibrary } from "@/lib/library";
import { pageTitle } from "@/lib/model";
import { cn } from "@/lib/utils";
import { closeGraph, setSearch, toggleLeft, toggleRight, useCurrentPage, usePageListing, useView } from "@/lib/view";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/input";
import { Icon } from "./icons";
import { BusyMark, Count } from "./parts";
import { Titlebar } from "./titlebar";

export function Toolbar() {
  const s = useLoadedSnapshot();
  const page = useCurrentPage();
  const listing = usePageListing();
  const text = useView((v) => v.search);
  const left = useView((v) => v.left);
  const right = useView((v) => v.right);
  const search = useRef<HTMLInputElement>(null);
  const back = useView((v) => v.back);
  const tt = useT();
  const t = tt.toolbar;

  // ⌘K(Windows、Linux 上是 Ctrl+K)聚焦搜索框。
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
    <Titlebar
      left={
        <>
          <Button variant="ghost" size="icon" className={cn(left && "text-foreground")} onClick={toggleLeft} title={t.toggleLeft}>
            <Icon name="sidebarL" />
          </Button>
          {s && page.kind === "graph" && (
            <Button variant="ghost" size="sm" className="graph-back ml-1 shrink-0 gap-0.5 pl-1.5" onClick={closeGraph}>
              <Icon name="chevL" />
              {tt.graph.back(pageTitle(s, back?.page ?? { kind: "all" }))}
            </Button>
          )}
          <span className="tb-crumb ml-1 inline-flex min-w-0 items-center gap-1.5 text-[13px] font-semibold whitespace-nowrap">
            <b className="truncate">{s ? pageTitle(s, page) : "Mechbox"}</b>
            {listing && <Count value={listing.total} />}
            <BusyMark checks={!left} />
          </span>
        </>
      }
      right={
        <>
          <SearchInput
            ref={search}
            className="h-[26px] min-w-[110px] flex-[0_1_240px] px-[7px] text-[11.5px]"
            icon={<Icon name="search" className="size-[13px] text-muted-foreground" />}
            suffix={<kbd className="rounded border border-border px-1 font-sans text-[10.5px] text-subtle">{t.searchKey()}</kbd>}
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
        </>
      }
    />
  );
}
