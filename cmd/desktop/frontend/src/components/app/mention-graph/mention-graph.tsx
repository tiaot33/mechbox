// 关系图页：一个集合或组里的技能之间谁提到了谁，画成知识图谱。每个技能是一个圆点，被越多技能提到越大，名字写在下方;
// 一个技能的 Markdown 文件里提到库里另一个技能时，画一条指向对方的线，互相提到只画一条两头有箭头的线。
// 力导向布局(固定种子，先算完再显示，打开时不晃动);按集合着色，本集合或组以外被提到的技能画成空心虚线圆，写出它在哪个集合。
// 技能多于 30 个时只写被提到最多的约 15 个名字，放大、悬停、选中时写出其余相关的名字。
// 悬停技能高亮它和上下游;悬停线显示提到对方的那几行原文，点线把提示固定住。点技能只留它和上下游并重新排开
// (提到它的在左、它提到的在右、互相提到的在上，三种颜色),同时打开详情抽屉;抽屉关上、点空白处或 Esc 回到全图原来的布局。
// 和谁都没有连线的技能不进图，收在图下方一行。快照变了而技能和线没变时不重新排，视野也不动;技能或线变了重新排，
// 用户没自己移动过视野时把新排的图放进框里，移动过就保留视野。
// 单个词的技能名没用点名写法时由 AI 判断(features/graph/judge.ts):先画规则确定的线，顶部写出还在判断几处，
// 判断完重读、补上线并重新排一次(视野按上面的规则);没配 AI 时提示有几处没判断，整体失败时按原因给「去设置」或「重试」。
// 还有没判断的几处时，图是空的也不说「没有互相提到」。
import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { select } from "d3-selection";
import "d3-transition";
import { zoom, zoomIdentity, type ZoomBehavior, type ZoomTransform } from "d3-zoom";
import type { CollectionID, Mention, MentionsFilter, Skill, SkillID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { mentionsQuery, toLibraryError, useSnapshot } from "@/lib/library";
import { labelled, layoutKey, mentionGraph, mentionSnippet, neighbours, searchHits, type GraphLink, type GraphNode, type MentionGraph, type Relation } from "@/lib/graph";
import { focusLayout, forceLayout, radius, type Positions, type XY } from "@/lib/graph-layout";
import { matches, type GraphSet } from "@/lib/model";
import { cn } from "@/lib/utils";
import { toggleDrawer, closeDrawer, openSettings, useView } from "@/lib/view";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Icon } from "@/components/app/icons";
import { SearchLine } from "@/components/app/skill-list/search-line";
import { useJudgeMentions, type Judging } from "@/features/graph/judge";

// MentionGraphPage 是关系图页的内容：members 是这个集合或组里的技能。
export function MentionGraphPage({ set, members }: { set: GraphSet; members: SkillID[] }) {
  const t = useT().graph;
  const filter: MentionsFilter = set.kind === "collection" ? { collection_id: set.id } : { group_id: set.id };
  const query = useQuery(mentionsQuery(filter));
  if (query.isError) {
    return (
      <Centered>
        <b className="text-sm text-foreground">{t.failed}</b>
        <span className="select-text">{toLibraryError(query.error).message}</span>
        <Button className="mt-1.5" onClick={() => void query.refetch()}>
          {t.retry}
        </Button>
      </Centered>
    );
  }
  if (!query.data) return <Centered>{t.reading}</Centered>;
  return <Loaded members={members} mentions={query.data.mentions} unjudged={query.data.unjudged} filter={filter} group={set.kind === "group"} />;
}

function Loaded({ members, mentions, unjudged, filter, group }: { members: SkillID[]; mentions: Mention[]; unjudged: number; filter: MentionsFilter; group: boolean }) {
  const s = useSnapshot();
  const tt = useT();
  const t = tt.graph;
  const search = useView((v) => v.search);
  const { judging, retry } = useJudgeMentions(filter, unjudged);
  const graph = useMemo(() => mentionGraph(s, members, mentions), [s, members, mentions]);
  const outside = graph.nodes.filter((n) => n.outside).length;
  return (
    <div className="graph-page flex min-h-0 flex-1 flex-col gap-2 px-3.5 pt-3 pb-3">
      <div className="[&>.search-line]:mb-0">
        <SearchLine results={searchHits(graph, search)} />
      </div>
      <div className="graph-summary flex min-w-0 items-center gap-2 text-[11.5px] text-muted-foreground">
        <span className="shrink-0 font-medium text-foreground-secondary">{t.summary(members.length, graph.edges.length, outside, group)}</span>
        <span className="min-w-0 truncate">{t.hint}</span>
      </div>
      <JudgeLine judging={judging} unjudged={unjudged} retry={retry} />
      {graph.nodes.length ? (
        <Canvas graph={graph} />
      ) : (
        <div className="graph-empty flex min-h-0 flex-1 flex-col items-center justify-center gap-1 rounded-[10px] border border-hairline bg-white/55 text-[12px] text-muted-foreground shadow-tile">
          {members.length ? (
            unjudged ? t.noneByRules(unjudged) : t.none
          ) : (
            <>
              <b className="font-medium text-foreground-secondary">{tt.list.emptyGroup}</b>
              <span>{tt.list.emptyGroupHint}</span>
            </>
          )}
        </div>
      )}
      {graph.loose.length > 0 && <Loose skills={graph.loose} />}
    </div>
  );
}

// JudgeLine 是单个词的技能名交给 AI 判断的状态：判断中写出还有几处;没配 AI 时提示去设置;
// 只因自己没判断成的技能写出原因;整体失败时写出原因，要改 AI 设置的给「去设置」，过一会儿可能就好的给「重试」。都没有时不占位置。
function JudgeLine({ judging, unjudged, retry }: { judging: Judging; unjudged: number; retry: () => void }) {
  const s = useSnapshot();
  const t = useT().graph;
  let text: string;
  let reason: string | undefined;
  let action: { label: string; run: () => void } | undefined;
  switch (judging.state) {
    case "judging":
      text = t.judging(judging.count);
      break;
    case "unconfigured":
      [text, action] = [t.unjudged(judging.count), { label: t.setUpAI, run: () => openSettings("ai") }];
      break;
    case "stopped":
      [text, reason] = [judging.fix === "settings" ? t.judgeNeedsSettings(unjudged) : t.judgeStopped(unjudged), judging.reason];
      action = judging.fix === "settings" ? { label: t.setUpAI, run: () => openSettings("ai") } : { label: t.retry, run: retry };
      break;
    case "done": {
      if (!judging.failed?.length) return null;
      const names = judging.failed.map((f) => s.skills.find((x) => x.id === f.skill_id)?.name ?? `#${f.skill_id}`);
      [text, reason] = [t.judgeSkipped(names), judging.reason];
      break;
    }
    default:
      return null;
  }
  const warn = judging.state === "stopped" || judging.state === "done";
  return (
    <div
      className={cn("graph-judge flex min-w-0 items-start gap-2 text-[11.5px]", warn ? "text-warn-strong" : "text-muted-foreground")}
      data-state={judging.state}
    >
      {judging.state === "judging" && <Spinner className="mt-[3px]" />}
      <div className="flex min-w-0 flex-col select-text">
        <span>
          {text}
          {action && (
            <Button variant="link" className="graph-judge-action ml-2 text-[11.5px]" onClick={action.run}>
              {action.label}
            </Button>
          )}
        </span>
        {reason && <span className="whitespace-pre-line text-muted-foreground">{reason}</span>}
      </div>
    </div>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return <div className="flex flex-col items-center justify-center gap-1.5 pt-[22vh] text-[12px] text-muted-foreground">{children}</div>;
}

// Loose 是图下方的一行：和这里的技能都没有连线的，点了同样打开详情。
function Loose({ skills }: { skills: Skill[] }) {
  const t = useT().graph;
  const search = useView((v) => v.search);
  return (
    <div className="graph-loose flex max-h-[30%] min-w-0 flex-wrap items-center gap-1.5 overflow-y-auto text-[11.5px] text-muted-foreground">
      <span className="shrink-0">{t.loose(skills.length)}</span>
      {skills.map((x) => (
        <button
          key={x.id}
          type="button"
          className={cn(
            "graph-loose-skill rounded-md border border-hairline bg-white/70 px-2 py-0.5 font-medium text-foreground-secondary hover:border-primary-border hover:text-primary-strong",
            !matches(search, x) && "opacity-35",
          )}
          onClick={() => toggleDrawer(x.id)}
          title={x.description}
        >
          {x.name}
        </button>
      ))}
    </div>
  );
}

// 名字的字号与字重：量宽和画出来用同一份，字体取界面实际用的。
const nameFont = { fontSize: 12.5, fontWeight: 500 };
const whereFont = { fontSize: 10.5, fontWeight: 400 };

let canvas: HTMLCanvasElement | undefined;
function nameWidth(text: string): number {
  canvas ??= document.createElement("canvas");
  const ctx = canvas.getContext("2d");
  if (!ctx) return text.length * 6.9;
  ctx.font = `${nameFont.fontWeight} ${nameFont.fontSize}px ${getComputedStyle(document.body).fontFamily}`;
  return ctx.measureText(text).width;
}

// 按集合着色：同一个集合在哪张图里都是同一种颜色(按左栏里集合的顺序取色);没有来源的技能是灰色。
const palette = ["#3b82f6", "#f59e0b", "#10b981", "#ec4899", "#8b5cf6", "#06b6d4", "#ef4444", "#84cc16"];
const noCollection = "#9ca3af";
// 点了一个技能时三种关系的颜色：提到它的、它提到的、互相提到的。
const relationColor: Record<Relation, string> = { in: "#ea580c", out: "#2563eb", both: "#7c3aed" };
const linkColor = "#c3cad5";
const litColor = "var(--color-primary)";

// 放大到这个倍数以上时写出全部名字。
const zoomedInAt = 1.35;
// 放进框里时最多缩到这么小，字还看得清;再放不下就拖动看。
const minFit = 0.8;

// Tip 是线的提示：跟着鼠标(悬停)或固定住(点了线)。
interface Tip {
  key: string;
  x: number;
  y: number;
  pinned: boolean;
}

// Canvas 是图本身：SVG 画圆点和线,d3-zoom 负责拖动画布与滚轮、捏合缩放;圆点可以拖动。
function Canvas({ graph }: { graph: MentionGraph }) {
  const t = useT().graph;
  const s = useSnapshot();
  const search = useView((v) => v.search);
  const drawer = useView((v) => v.drawer);
  const markers = useId().replace(/:/g, "");
  const box = useRef<HTMLDivElement>(null);
  const svgRef = useRef<SVGSVGElement>(null);
  const viewRef = useRef<SVGGElement>(null);
  const zoomRef = useRef<ZoomBehavior<SVGSVGElement, unknown> | null>(null);
  const transform = useRef<ZoomTransform>(zoomIdentity);
  const homeView = useRef<ZoomTransform | null>(null);
  // viewMoved 是用户自己移动过视野(拖动、滚轮、缩放按钮);点「整张放进框里」后清掉。
  const viewMoved = useRef(false);
  const latest = useRef(graph);
  latest.current = graph;
  const key = useMemo(() => layoutKey(graph), [graph]);

  const [zoomedIn, setZoomedIn] = useState(false);
  const [hoverNode, setHoverNode] = useState<SkillID | null>(null);
  const [tip, setTip] = useState<Tip | null>(null);
  const [rows, setRows] = useState(12);

  const byID = useMemo(() => new Map(graph.nodes.map((n) => [n.skill.id, n])), [graph]);
  const selected = drawer !== null && byID.has(drawer) ? drawer : null;

  // 全图的位置只在技能或线变了时重新算;拖动过的圆点记在 moved 里，重新排时清掉。
  const home = useMemo(() => forceLayout(latest.current, nameWidth), [key]);
  const [moved, setMoved] = useState<{ key: string; at: Positions }>({ key, at: new Map() });
  const focus = useMemo(() => (selected === null ? null : focusLayout(latest.current, selected, rows, nameWidth)), [key, selected, rows]);
  const [focusMoved, setFocusMoved] = useState<{ selected: SkillID | null; at: Positions }>({ selected, at: new Map() });

  const target = useMemo(() => {
    const at = new Map(home);
    if (moved.key === key) for (const [id, p] of moved.at) at.set(id, p);
    if (focus) {
      for (const [id, p] of focus.at) at.set(id, p);
      if (focusMoved.selected === selected) for (const [id, p] of focusMoved.at) at.set(id, p);
    }
    return at;
  }, [home, moved, key, focus, focusMoved, selected]);
  const instant = useRef(false);
  const shown = useTween(target, instant);

  // 画布：拖动平移，滚轮与捏合缩放;从圆点上按下是拖圆点，不平移。用户自己移动视野时收起没固定的提示。
  useLayoutEffect(() => {
    const svg = select(svgRef.current!);
    const z = zoom<SVGSVGElement, unknown>()
      .scaleExtent([0.25, 3])
      .filter((e: MouseEvent & WheelEvent) => !(e.target as Element).closest?.(".graph-node") && (!e.ctrlKey || e.type === "wheel") && !e.button)
      .on("zoom", (e: { transform: ZoomTransform; sourceEvent: Event | null }) => {
        transform.current = e.transform;
        viewRef.current?.setAttribute("transform", e.transform.toString());
        setZoomedIn(e.transform.k > zoomedInAt);
        if (e.sourceEvent) {
          viewMoved.current = true;
          setTip((t) => (t?.pinned ? t : null));
        }
      });
    svg.call(z).on("dblclick.zoom", null);
    zoomRef.current = z;
    svg.call(z.transform, fitTransform([...home.values()], latest.current, home, area(box.current), false));
    return () => void svg.on(".zoom", null);
    // 挂载时放进框里一次;技能或线变了重新排之后见下面。
  }, []);

  // 技能或线变了(如 AI 判断完补上线)重新排之后：用户没自己拖动、滚轮或点缩放按钮移动过视野时，把新排的图放进框里;
  // 移动过就保留用户当时的视野。点了技能时不动，回到全图时再按同样的规则(见下面)。
  const laidOut = useRef(key);
  useEffect(() => {
    if (laidOut.current === key) return;
    laidOut.current = key;
    const z = zoomRef.current;
    if (!z || viewMoved.current || selected !== null) return;
    select(svgRef.current!).transition().duration(450).call(z.transform, fitTransform([...home.values()], latest.current, home, area(box.current), false));
  }, [key, home]);

  // 框的高度决定点了技能后一列最多排几个。
  useEffect(() => {
    const el = box.current!;
    const update = () => setRows(Math.max(4, Math.floor((el.clientHeight - 170) / 46)));
    update();
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // 点了技能：记下全图时的视野，把它和上下游放进抽屉左边的那块;回到全图时恢复原来的视野。
  // 点着技能期间全图重新排过(如 AI 判断完补上线)、点之前又没自己移动过视野时，回到全图把新排的图放进框里。
  const wasSelected = useRef<SkillID | null>(null);
  const homeAt = useRef({ key, moved: false });
  useEffect(() => {
    const svg = svgRef.current;
    const z = zoomRef.current;
    if (!svg || !z || wasSelected.current === selected) return;
    if (wasSelected.current === null) {
      homeView.current = transform.current;
      homeAt.current = { key, moved: viewMoved.current };
    }
    wasSelected.current = selected;
    setTip(null);
    setHoverNode(null);
    if (selected !== null && focus) {
      const ids = [...focus.at.keys()];
      const next = fitTransform(
        ids.map((id) => focus.at.get(id)!),
        latest.current,
        focus.at,
        area(box.current, true),
        true,
      );
      select(svg).transition().duration(450).call(z.transform, next);
    } else if (homeView.current) {
      viewMoved.current = homeAt.current.moved;
      const relaid = homeAt.current.key !== key && !homeAt.current.moved;
      const next = relaid ? fitTransform([...home.values()], latest.current, home, area(box.current), false) : homeView.current;
      select(svg).transition().duration(450).call(z.transform, next);
    }
  }, [selected, focus]);

  // 固定住的提示按 Esc 关掉。
  useEffect(() => {
    if (!tip?.pinned) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setTip(null);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [tip?.pinned]);

  const colorOf = useMemo(() => {
    const index = new Map<CollectionID, number>(s.collections.map((c, i) => [c.id, i]));
    return (n: GraphNode) => (n.collection === null ? noCollection : palette[(index.get(n.collection) ?? 0) % palette.length]);
  }, [s.collections]);
  const linkByKey = useMemo(() => new Map(graph.links.map((l) => [l.key, l])), [graph]);
  const tipLink = tip ? linkByKey.get(tip.key) : undefined;
  const named = useMemo(() => labelled(graph.nodes), [graph]);

  // 现在高亮哪些：点了技能时是它和上下游;否则悬停的技能和上下游;否则悬停或固定的线两头。
  const lit = useMemo(() => {
    if (selected !== null) return null;
    if (hoverNode !== null) return neighbours(graph.links, hoverNode);
    if (tipLink) return { nodes: new Set([tipLink.from, tipLink.to]), links: new Set([tipLink.key]) };
    return null;
  }, [graph, selected, hoverNode, tipLink]);
  const searching = !!search.trim();

  const at = (e: React.MouseEvent) => {
    const r = box.current!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };
  const hoverLink = (e: React.MouseEvent, l: GraphLink) => setTip((t) => (t?.pinned ? t : { key: l.key, ...at(e), pinned: false }));

  // 拖动圆点：移动超过 3px 才算拖动，否则松开时当作点了它。
  const drag = useRef<{ id: SkillID; x: number; y: number; from: XY; moved: boolean } | null>(null);
  const onNodeDown = (e: React.PointerEvent, id: SkillID) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    (e.currentTarget as Element).setPointerCapture(e.pointerId);
    drag.current = { id, x: e.clientX, y: e.clientY, from: shown.get(id) ?? { x: 0, y: 0 }, moved: false };
  };
  const onNodeMove = (e: React.PointerEvent) => {
    const d = drag.current;
    if (!d) return;
    const [dx, dy] = [e.clientX - d.x, e.clientY - d.y];
    if (!d.moved && Math.hypot(dx, dy) < 3) return;
    d.moved = true;
    const k = transform.current.k;
    const p = { x: d.from.x + dx / k, y: d.from.y + dy / k };
    instant.current = true;
    if (selected !== null) setFocusMoved((m) => ({ selected, at: new Map(m.selected === selected ? m.at : []).set(d.id, p) }));
    else setMoved((m) => ({ key, at: new Map(m.key === key ? m.at : []).set(d.id, p) }));
  };
  const onNodeUp = (e: React.PointerEvent) => {
    const d = drag.current;
    drag.current = null;
    if (!d || d.moved) return;
    e.stopPropagation();
    setTip(null);
    toggleDrawer(d.id);
  };

  const zoomBy = (k: number) => {
    viewMoved.current = true;
    if (zoomRef.current) select(svgRef.current!).transition().duration(250).call(zoomRef.current.scaleBy, k);
  };
  const fit = () => {
    const z = zoomRef.current;
    if (!z) return;
    viewMoved.current = false;
    const ids = focus ? [...focus.at.keys()] : [...home.keys()];
    const next = fitTransform(
      ids.map((id) => shown.get(id) ?? { x: 0, y: 0 }),
      latest.current,
      shown,
      area(box.current, focus !== null),
      focus !== null,
    );
    select(svgRef.current!).transition().duration(350).call(z.transform, next);
  };

  return (
    <div ref={box} className="graph-canvas relative min-h-0 flex-1 overflow-hidden rounded-[10px] border border-hairline bg-white/55 shadow-tile">
      <svg
        ref={svgRef}
        className="graph-svg block size-full cursor-grab select-none active:cursor-grabbing"
        onClick={(e) => {
          if ((e.target as Element).closest(".graph-node, .graph-link")) return;
          setTip(null);
          if (selected !== null) closeDrawer();
        }}
        onMouseLeave={() => {
          setTip((t) => (t?.pinned ? t : null));
          setHoverNode(null);
        }}
      >
        <defs>
          {Object.entries({ base: linkColor, lit: litColor, ...relationColor }).map(([name, color]) => (
            <g key={name}>
              <marker id={`${markers}-end-${name}`} viewBox="0 -5 10 10" refX={10} markerWidth={7} markerHeight={7} orient="auto">
                <path d="M0,-4.5L10,0L0,4.5Z" fill={color} />
              </marker>
              <marker id={`${markers}-start-${name}`} viewBox="0 -5 10 10" refX={0} markerWidth={7} markerHeight={7} orient="auto">
                <path d="M10,-4.5L0,0L10,4.5Z" fill={color} />
              </marker>
            </g>
          ))}
        </defs>
        <g ref={viewRef}>
          <g className="graph-links">
            {graph.links.map((l) => {
              const a = shown.get(l.from);
              const b = shown.get(l.to);
              const na = byID.get(l.from);
              const nb = byID.get(l.to);
              if (!a || !b || !na || !nb) return null;
              const rel = focus && (l.from === selected || l.to === selected) ? focus.relation.get(l.from === selected ? l.to : l.from) : undefined;
              const hidden = focus !== null && !rel;
              const isLit = !!lit?.links.has(l.key);
              const kind = rel ?? (isLit ? "lit" : "base");
              const color = rel ? relationColor[rel] : isLit ? litColor : linkColor;
              const d = linkPath(a, b, radius(na), radius(nb), l.both, focus ? [nameWidth(na.skill.name), nameWidth(nb.skill.name)] : undefined);
              const dim = !hidden && !rel && ((lit && !isLit) || (searching && !matches(search, na.skill) && !matches(search, nb.skill)));
              return (
                <g
                  key={l.key}
                  className="graph-link"
                  data-link={`${na.skill.name}->${nb.skill.name}`}
                  data-both={l.both || undefined}
                  style={{ opacity: hidden ? 0 : dim ? 0.12 : 1, transition: "opacity .2s", pointerEvents: hidden ? "none" : undefined }}
                >
                  <path
                    d={d}
                    fill="none"
                    stroke={color}
                    strokeWidth={rel || isLit ? 2 : 1.3}
                    markerEnd={`url(#${markers}-end-${kind})`}
                    markerStart={l.both ? `url(#${markers}-start-${kind})` : undefined}
                  />
                  <path
                    className="graph-link-hit cursor-pointer"
                    d={d}
                    fill="none"
                    stroke="transparent"
                    strokeWidth={12}
                    onMouseEnter={(e) => hoverLink(e, l)}
                    onMouseMove={(e) => hoverLink(e, l)}
                    onMouseLeave={() => setTip((t) => (t?.pinned ? t : null))}
                    onClick={(e) => setTip({ key: l.key, ...at(e), pinned: true })}
                  />
                </g>
              );
            })}
          </g>
          <g className="graph-nodes">
            {graph.nodes.map((n) => {
              const p = shown.get(n.skill.id);
              if (!p) return null;
              const id = n.skill.id;
              const rel = focus?.relation.get(id);
              const isSelected = id === selected;
              const hidden = focus !== null && !rel && !isSelected;
              const isLit = !!lit?.nodes.has(id);
              const dim = !hidden && !focus && ((lit && !isLit) || (searching && !matches(search, n.skill)));
              const showName = focus ? !hidden : zoomedIn || named.has(id) || isLit || (searching && matches(search, n.skill));
              const r = radius(n);
              const color = colorOf(n);
              const beside = focus !== null;
              return (
                <g
                  key={id}
                  className="graph-node cursor-pointer"
                  data-skill={n.skill.name}
                  data-outside={n.outside || undefined}
                  data-selected={isSelected || undefined}
                  data-relation={rel}
                  transform={`translate(${p.x},${p.y})`}
                  style={{ opacity: hidden ? 0 : dim ? 0.15 : 1, transition: "opacity .2s", pointerEvents: hidden ? "none" : undefined }}
                  onPointerDown={(e) => onNodeDown(e, id)}
                  onPointerMove={onNodeMove}
                  onPointerUp={onNodeUp}
                  onMouseEnter={() => {
                    if (focus) return;
                    setTip((t) => (t?.pinned ? t : null));
                    setHoverNode(id);
                  }}
                  onMouseLeave={() => setHoverNode(null)}
                >
                  {isSelected && <circle r={r + 4} fill="none" stroke="var(--color-primary)" strokeWidth={2} />}
                  <circle
                    r={r}
                    fill={n.outside ? "#fff" : color}
                    stroke={n.outside ? color : "#fff"}
                    strokeWidth={n.outside ? 1.6 : 2}
                    strokeDasharray={n.outside ? "3 2" : undefined}
                  />
                  {showName && (
                    <g className="graph-node-label" style={{ paintOrder: "stroke" }} stroke="#fff" strokeWidth={4} strokeLinejoin="round">
                      <text
                        x={beside ? r + 7 : 0}
                        y={beside ? 4 : r + 14}
                        textAnchor={beside ? "start" : "middle"}
                        fill={n.outside ? "var(--color-foreground-secondary)" : "var(--color-foreground)"}
                        style={{ ...nameFont, fontWeight: isSelected ? 650 : nameFont.fontWeight }}
                      >
                        {n.skill.name}
                      </text>
                      {n.outside && (
                        <text
                          className="graph-node-where"
                          x={beside ? r + 7 : 0}
                          y={beside ? 18 : r + 27}
                          textAnchor={beside ? "start" : "middle"}
                          fill="var(--color-muted-foreground)"
                          style={whereFont}
                        >
                          {n.where}
                        </text>
                      )}
                    </g>
                  )}
                </g>
              );
            })}
          </g>
        </g>
      </svg>
      {focus && (
        <div className="graph-legend pointer-events-none absolute top-2.5 left-3 flex gap-3 rounded-md bg-white/75 px-2 py-1 text-[11px] text-foreground-secondary">
          {(["in", "out", "both"] as const).map((r) => (
            <span key={r} className="flex items-center gap-1.5">
              <span className="h-[3px] w-4 rounded-full" style={{ background: relationColor[r] }} />
              {t.relation[r]}
            </span>
          ))}
        </div>
      )}
      {/* 放在左下角：右侧会被详情抽屉盖住。 */}
      <div className="graph-controls absolute bottom-3 left-3 flex flex-col overflow-hidden rounded-md border border-hairline bg-white shadow-tile">
        {(
          [
            ["plus", t.zoomIn, () => zoomBy(1.25)],
            ["minus", t.zoomOut, () => zoomBy(0.8)],
            ["fit", t.fit, fit],
          ] as const
        ).map(([icon, label, act]) => (
          <button
            key={icon}
            type="button"
            className={`graph-${icon} flex size-7 items-center justify-center border-b border-hairline text-foreground-secondary last:border-b-0 hover:bg-primary-soft hover:text-primary-strong`}
            title={label}
            aria-label={label}
            onClick={act}
          >
            <Icon name={icon} className="size-3.5" />
          </button>
        ))}
      </div>
      {tip && tipLink && <LinesTip link={tipLink} tip={tip} box={box.current} onClose={() => setTip(null)} />}
    </div>
  );
}

// useTween 让位置从当前显示的平滑移到 target(点技能重排、回到全图时);拖动时 instant 置位，直接跳到 target。
function useTween(target: Positions, instant: React.RefObject<boolean>): Positions {
  const [shown, setShown] = useState(target);
  const now = useRef(shown);
  now.current = shown;
  useEffect(() => {
    if (instant.current) {
      instant.current = false;
      setShown(target);
      return;
    }
    const from = now.current;
    if (from === target) return;
    const start = performance.now();
    let frame = 0;
    const step = (time: number) => {
      const t = Math.min(1, (time - start) / 450);
      const e = 1 - Math.pow(1 - t, 3);
      const next: Positions = new Map();
      for (const [id, p] of target) {
        const f = from.get(id) ?? p;
        next.set(id, { x: f.x + (p.x - f.x) * e, y: f.y + (p.y - f.y) * e });
      }
      setShown(t < 1 ? next : target);
      if (t < 1) frame = requestAnimationFrame(step);
    };
    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
  }, [target, instant]);
  return shown;
}

// linkPath 是一条线：从一个圆点的边到另一个圆点的边;单向的略弯，互相提到的是直线。
// 点了技能时名字写在圆点右边：对方在名字右边更远处时，线从名字末尾出发，不穿过名字。
function linkPath(a: XY, b: XY, ra: number, rb: number, both: boolean, labels?: [number, number]): string {
  if (labels) {
    const [ea, eb] = [a.x + ra + 10 + labels[0], b.x + rb + 10 + labels[1]];
    if (b.x - rb > ea) [a, ra] = [{ x: ea, y: a.y }, 0];
    else if (a.x - ra > eb) [b, rb] = [{ x: eb, y: b.y }, 0];
  }
  const [dx, dy] = [b.x - a.x, b.y - a.y];
  const d = Math.hypot(dx, dy) || 1;
  const [sx, sy] = [a.x + (dx / d) * (ra + 2), a.y + (dy / d) * (ra + 2)];
  const [tx, ty] = [b.x - (dx / d) * (rb + 3), b.y - (dy / d) * (rb + 3)];
  if (both) return `M${sx},${sy}L${tx},${ty}`;
  return `M${sx},${sy}Q${(sx + tx) / 2 - dy * 0.08},${(sy + ty) / 2 + dx * 0.08} ${tx},${ty}`;
}

type Area = { left: number; top: number; width: number; height: number };

// area 是框里能看到图的那块：点了技能时右边被详情抽屉盖住，只用抽屉左边的部分。
function area(el: HTMLDivElement | null, besideDrawer = false): Area {
  if (!el) return { left: 0, top: 0, width: 800, height: 600 };
  const r = el.getBoundingClientRect();
  let right = r.width;
  const sheet = besideDrawer ? document.querySelector<HTMLElement>('[data-slot="sheet-content"]') : null;
  const parent = sheet?.offsetParent?.getBoundingClientRect();
  if (sheet && parent) right = Math.max(r.width * 0.4, Math.min(r.width, parent.right - 10 - sheet.offsetWidth - 16 - r.left));
  return { left: 0, top: 0, width: right, height: r.height };
}

// fitTransform 把这些圆点(连同名字)放进 area:最多放大到 100%,最多缩到约 80% 字还看得清，再放不下就居中、其余拖动看。
// 名字在圆点下方(全图)或右边(点了技能)。
function fitTransform(points: XY[], graph: MentionGraph, at: Positions, a: Area, beside: boolean): ZoomTransform {
  if (!points.length) return zoomIdentity;
  const byPoint = new Map<XY, GraphNode>();
  for (const n of graph.nodes) {
    const p = at.get(n.skill.id);
    if (p) byPoint.set(p, n);
  }
  let [x0, y0, x1, y1] = [Infinity, Infinity, -Infinity, -Infinity];
  for (const p of points) {
    const n = byPoint.get(p);
    const r = n ? radius(n) : 6;
    const w = n ? nameWidth(n.skill.name) : 80;
    if (beside) {
      x0 = Math.min(x0, p.x - r);
      x1 = Math.max(x1, p.x + r + 8 + w);
      y0 = Math.min(y0, p.y - r);
      y1 = Math.max(y1, p.y + Math.max(r, n?.outside ? 22 : 10));
    } else {
      x0 = Math.min(x0, p.x - Math.max(r, w / 2));
      x1 = Math.max(x1, p.x + Math.max(r, w / 2));
      y0 = Math.min(y0, p.y - r);
      y1 = Math.max(y1, p.y + r + (n?.outside ? 30 : 18));
    }
  }
  const pad = 28;
  const k = Math.max(minFit, Math.min(1, (a.width - pad * 2) / (x1 - x0), (a.height - pad * 2) / (y1 - y0)));
  const [cx, cy] = [(x0 + x1) / 2, (y0 + y1) / 2];
  return zoomIdentity.translate(a.left + a.width / 2 - cx * k, a.top + a.height / 2 - cy * k).scale(k);
}

const maxLines = 6;

// LinesTip 是线的提示：谁提到谁，以及提到对方的那几行原文，被提到的名字高亮;长行从名字前面一点开始显示。
// 互相提到的线两个方向都列出来。悬停时跟着鼠标、最多 6 行、每行最多 3 行高;点了线固定住，列出全部行和整行原文，
// 可以滚动，点空白处、× 或 Esc 关掉。
function LinesTip({ link, tip, box, onClose }: { link: GraphLink; tip: Tip; box: HTMLDivElement | null; onClose: () => void }) {
  const s = useSnapshot();
  const t = useT().graph;
  const name = (id: SkillID) => s.skills.find((x) => x.id === id)?.name ?? `#${id}`;
  // 悬停时最多 6 行，互相提到的两个方向平分。
  const each = tip.pinned ? Infinity : Math.ceil(maxLines / link.mentions.length);
  let hidden = 0;
  const sections = link.mentions.map((m) => {
    const lines = m.lines ?? [];
    const shown = lines.slice(0, each);
    hidden += lines.length - shown.length;
    return { m, shown };
  });
  const width = 400;
  const right = box ? tip.x + 16 + width > box.clientWidth : false;
  const below = box ? tip.y < box.clientHeight / 2 : true;
  return (
    <div
      className={cn(
        "graph-tip absolute z-10 flex w-[400px] flex-col gap-1.5 glass glass-clear rounded-lg border px-3 py-2.5 text-[11.5px] leading-[1.45] text-foreground [--glass-drop:var(--shadow-popover)]",
        tip.pinned ? "max-h-[60%] overflow-y-auto select-text" : "pointer-events-none",
      )}
      data-pinned={tip.pinned || undefined}
      style={{
        left: right ? undefined : tip.x + 16,
        right: right && box ? box.clientWidth - tip.x + 16 : undefined,
        top: below ? tip.y + 14 : undefined,
        bottom: below || !box ? undefined : box.clientHeight - tip.y + 14,
      }}
    >
      {sections.map(({ m, shown }, k) => {
        const target = name(m.to);
        return (
          <div key={k} className={cn("flex flex-col gap-1.5", k > 0 && "mt-1 border-t border-hairline pt-2")}>
            <div className="flex items-start gap-2">
              <b className="min-w-0 grow text-[12px] font-semibold">{t.mentions(name(m.from), target)}</b>
              {tip.pinned && k === 0 && (
                <button type="button" className="graph-tip-close shrink-0 text-muted-foreground hover:text-foreground" title={t.close} onClick={onClose}>
                  <Icon name="close" className="size-3" />
                </button>
              )}
            </div>
            {shown.map((l, i) => {
              const snip = tip.pinned ? mentionSnippet(l.text, target, 0, Infinity) : mentionSnippet(l.text, target);
              return (
                <div key={i} className="graph-tip-line min-w-0">
                  <span className="font-mono text-[10px] text-subtle">{l.path}</span>
                  <p className={cn("break-words text-foreground-secondary", !tip.pinned && "line-clamp-3")}>
                    {snip.cut && "…"}
                    {snip.parts.map((p, j) =>
                      p.hit ? (
                        <mark key={j} className="rounded-[3px] bg-primary-soft px-0.5 font-semibold text-primary-strong">
                          {p.text}
                        </mark>
                      ) : (
                        <span key={j}>{p.text}</span>
                      ),
                    )}
                  </p>
                </div>
              );
            })}
          </div>
        );
      })}
      {!tip.pinned && hidden > 0 && <span className="text-muted-foreground">{t.moreLines(hidden)}</span>}
    </div>
  );
}
