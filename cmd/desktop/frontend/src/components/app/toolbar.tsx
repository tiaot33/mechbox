// 统一工具栏：左 = 侧栏开关 + 当前页面的标题与个数(只在左栏收起、别处看不到时写，见 showTitle;关系图页多一个返回按钮，回到打开它之前的那一页);
// 右 = 动作(检查更新、入库技能、技能市场)、搜索与右栏开关。左栏只放页面与集合、组，动作都在这里。
// 作用域在右栏点选，打开作用域页。
// 外框(拖动区、红绿灯的位置、Windows 的窗口按钮)见 titlebar.tsx。
import { useEffect, useRef } from "react";
import { useT } from "@/lib/i18n";
import { useLoadedSnapshot, useSnapshot } from "@/lib/library";
import { checkableCollections, pageTitle } from "@/lib/model";
import { useBusy, useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import { closeGraph, setSearch, toggleLeft, toggleRight, useCurrentPage, usePageListing, useView } from "@/lib/view";
import { checkUpdates, useCheckMark } from "@/features/update/check";
import { openFetch } from "@/features/fetch/fetch-dialog";
import { MarketDialog } from "@/features/fetch/market-dialog";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/input";
import { StatusMark } from "@/components/ui/status-mark";
import { Icon } from "@/components/ui/icon";
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
  // 作用域页、项目页的摘要标题不吸顶：右栏收起时(看不到选中的那一行)也写在顶栏。
  const showTitle =
    !s || (!left && (page.kind === "all" || page.kind === "updates" || page.kind === "graph")) || (!right && (page.kind === "scope" || page.kind === "project"));

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
            <Button variant="ghost" className="graph-back ml-1 shrink-0 gap-0.5 pl-1.5" onClick={closeGraph}>
              <Icon name="chevL" />
              {tt.graph.back(pageTitle(s, back?.page ?? { kind: "all" }))}
            </Button>
          )}
          <span className="tb-crumb ml-1 inline-flex min-w-0 items-center gap-1.5 text-body font-semibold whitespace-nowrap">
            {/* 同一个信息一屏只出现一次：左栏开着时技能库、更新页、关系图的名字和个数就在左栏选中的那一行上;作用域页、项目页的摘要标题已经写了。
                只有它们看不到时(左栏收起;作用域页、项目页的右栏收起)才写在顶栏。 */}
            {showTitle && (
              <>
                <b className="truncate">{s ? pageTitle(s, page) : "Mechbox"}</b>
                {listing && <Count value={listing.total} />}
              </>
            )}
            {/* 从左栏集合右键发起的检查画在那一行上;左栏收起时由这里补上。顶栏按钮发起的画在按钮上。 */}
            <BusyMark checks={!left && "collections"} />
          </span>
        </>
      }
      right={
        <>
          {s && <Actions />}
          <SearchInput
            ref={search}
            className="min-w-[110px] flex-[0_1_240px] text-caption"
            icon={<Icon name="search" className="size-[13px] text-muted-foreground" />}
            suffix={<kbd className="rounded-[4px] border border-border px-1 font-sans text-caption text-muted-foreground">{t.searchKey()}</kbd>}
            placeholder={t.search}
            value={text}
            onChange={(e) => setSearch(e.target.value)}
            onKeyDown={(e) => e.key === "Escape" && setSearch("")}
          />
          <Button variant="ghost" size="icon" className={cn(right && "text-foreground")} onClick={toggleRight} title={t.toggleRight}>
            <Icon name="sidebarR" />
          </Button>
        </>
      }
    />
  );
}

// Actions 是顶栏右边的动作：检查更新(检查中与检查完在按钮上标出)、入库技能、技能市场。用轻按钮，和左栏的页面分开;和同一行的搜索框、侧栏开关一样高。
function Actions() {
  const s = useSnapshot();
  const ui = useUI();
  const busy = useBusy();
  const checkStatus = useCheckMark("all");
  const checkable = checkableCollections(s);
  const t = useT().toolbar;
  return (
    <>
      <Button variant="ghost" className="tb-check shrink-0" disabled={!checkable.length || busy} onClick={() => checkUpdates(checkable)} title={t.checkTitle}>
        {checkStatus ? <StatusMark status={checkStatus} size={12} /> : <Icon name="refresh" />}
        {t.check}
      </Button>
      <Button variant="ghost" className="tb-fetch shrink-0" onClick={(e) => void openFetch(s.info.home_dir, e.currentTarget)}>
        <Icon name="plus" />
        {t.fetch}
      </Button>
      <Button
        variant="ghost"
        className="tb-market shrink-0"
        onClick={(e) => ui.open((close) => <MarketDialog onClose={close} />, { origin: e.currentTarget })}
      >
        <Icon name="tag" />
        {t.market}
      </Button>
    </>
  );
}
