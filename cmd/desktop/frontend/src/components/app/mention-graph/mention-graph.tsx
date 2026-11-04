// 关系图页：一个集合或组里的技能之间谁提到了谁。一个技能的 Markdown 文件里提到库里另一个技能时，画一条从它指向对方的线;
// 从左往右按提到的方向分层(ELK 的分层布局),没被提到的入口技能在最左。
// 悬停技能高亮它提到的和提到它的技能与相连的线，其他变淡;悬停线显示提到对方的那几行原文;点技能打开详情抽屉。
// 本集合或组以外被提到的技能用虚线框画出，写出它在哪个集合;和谁都没有连线的技能不进图，收在图下方一行。
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { BaseEdge, Controls, Handle, MarkerType, Position, ReactFlow, type Edge, type EdgeProps, type Node, type NodeProps } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import ELK, { type ElkNode } from "elkjs/lib/elk.bundled.js";
import type { Mention, MentionsFilter, Skill, SkillID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { mentionsQuery, toLibraryError, useSnapshot } from "@/lib/library";
import { edgeKey, mentionGraph, neighbours, type GraphNode, type MentionGraph } from "@/lib/graph";
import { matches, type GraphSet } from "@/lib/model";
import { cn } from "@/lib/utils";
import { toggleDrawer, useView } from "@/lib/view";
import { Button } from "@/components/ui/button";

const elk = new ELK();

const nodeHeight = 32;
const outsideHeight = 44;
const font = "600 12.5px -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif";
const smallFont = "400 10.5px -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif";

let canvas: HTMLCanvasElement | undefined;
function textWidth(text: string, f: string): number {
  canvas ??= document.createElement("canvas");
  const ctx = canvas.getContext("2d");
  if (!ctx) return text.length * 7.5;
  ctx.font = f;
  return ctx.measureText(text).width;
}

function nodeSize(n: GraphNode, noSource: string): { width: number; height: number } {
  const name = textWidth(n.skill.name, font);
  const where = n.outside ? textWidth(n.where ?? noSource, smallFont) : 0;
  return { width: Math.ceil(Math.min(260, Math.max(name, where)) + 28), height: n.outside ? outsideHeight : nodeHeight };
}

type XY = { x: number; y: number };
type Placed = { nodes: Record<SkillID, XY & { width: number; height: number }>; edges: Record<string, XY[]> };

// layout 用 ELK 的分层布局排出每个技能的位置和每条线的走向(样条曲线，反向的线也由 ELK 绕好)。
async function layout(graph: MentionGraph, noSource: string): Promise<Placed> {
  const root: ElkNode = {
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      "elk.edgeRouting": "SPLINES",
      "elk.layered.edgeRouting.splines.mode": "SLOPPY",
      "elk.layered.spacing.nodeNodeBetweenLayers": "44",
      "elk.layered.spacing.edgeNodeBetweenLayers": "14",
      "elk.layered.spacing.edgeEdgeBetweenLayers": "6",
      "elk.spacing.edgeEdge": "6",
      "elk.spacing.edgeNode": "12",
      "elk.spacing.nodeNode": "18",
      "elk.spacing.componentComponent": "36",
      "elk.layered.nodePlacement.strategy": "NETWORK_SIMPLEX",
      "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
    },
    children: graph.nodes.map((n) => ({ id: String(n.skill.id), ...nodeSize(n, noSource) })),
    edges: graph.edges.map((m) => ({ id: edgeKey(m), sources: [String(m.from)], targets: [String(m.to)] })),
  };
  const result = await elk.layout(root);
  const placed: Placed = { nodes: {}, edges: {} };
  for (const c of result.children ?? []) placed.nodes[Number(c.id)] = { x: c.x ?? 0, y: c.y ?? 0, width: c.width ?? 0, height: c.height ?? 0 };
  for (const e of result.edges ?? []) {
    const section = e.sections?.[0];
    if (section) placed.edges[e.id] = [section.startPoint, ...(section.bendPoints ?? []), section.endPoint];
  }
  return placed;
}

// splinePath 把 ELK 给的样条画出来:起点、终点之间每三个点是一段三次贝塞尔曲线的两个控制点和终点;对不上时退回圆角折线。
function splinePath(points: XY[]): string {
  if ((points.length - 1) % 3 !== 0) return roundedPath(points);
  let d = `M ${points[0].x} ${points[0].y}`;
  for (let i = 1; i < points.length; i += 3) {
    const [a, b, c] = [points[i], points[i + 1], points[i + 2]];
    d += ` C ${a.x} ${a.y} ${b.x} ${b.y} ${c.x} ${c.y}`;
  }
  return d;
}

// roundedPath 把折线画成拐角带圆角的路径。
function roundedPath(points: XY[], radius = 8): string {
  let d = `M ${points[0].x} ${points[0].y}`;
  for (let i = 1; i < points.length - 1; i++) {
    const [a, b, c] = [points[i - 1], points[i], points[i + 1]];
    const r = Math.min(radius, Math.hypot(b.x - a.x, b.y - a.y) / 2, Math.hypot(c.x - b.x, c.y - b.y) / 2);
    const toward = (p: XY, q: XY) => {
      const len = Math.hypot(q.x - p.x, q.y - p.y) || 1;
      return { x: p.x + ((q.x - p.x) / len) * r, y: p.y + ((q.y - p.y) / len) * r };
    };
    const before = toward(b, a);
    const after = toward(b, c);
    d += ` L ${before.x} ${before.y} Q ${b.x} ${b.y} ${after.x} ${after.y}`;
  }
  const last = points[points.length - 1];
  return `${d} L ${last.x} ${last.y}`;
}

type RoutedEdge = Edge<{ points: XY[] }, "routed">;

function Routed({ id, data, style, markerEnd, interactionWidth }: EdgeProps<RoutedEdge>) {
  if (!data?.points.length) return null;
  return <BaseEdge id={id} path={splinePath(data.points)} style={style} markerEnd={markerEnd} interactionWidth={interactionWidth} />;
}

const edgeTypes = { routed: Routed };

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
  return <Loaded members={members} mentions={query.data} group={set.kind === "group"} />;
}

function Loaded({ members, mentions, group }: { members: SkillID[]; mentions: Mention[]; group: boolean }) {
  const s = useSnapshot();
  const t = useT().graph;
  const graph = useMemo(() => mentionGraph(s, members, mentions), [s, members, mentions]);
  const outside = graph.nodes.filter((n) => n.outside).length;
  return (
    <div className="graph-page flex min-h-0 flex-1 flex-col gap-2 px-3.5 pt-3 pb-3">
      <div className="graph-summary flex min-w-0 items-center gap-2 text-[11.5px] text-muted-foreground">
        <span className="shrink-0 font-medium text-foreground-secondary">{t.summary(members.length, graph.edges.length, outside, group)}</span>
        <span className="min-w-0 truncate">{t.hint}</span>
      </div>
      {graph.nodes.length ? (
        <Canvas graph={graph} />
      ) : (
        <div className="graph-empty grid min-h-0 flex-1 place-items-center rounded-[10px] border border-hairline bg-white/55 text-[12px] text-muted-foreground shadow-tile">
          {t.none}
        </div>
      )}
      {graph.loose.length > 0 && <Loose skills={graph.loose} />}
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

type SkillNodeData = { node: GraphNode; noSource: string; dim: boolean; lit: boolean; open: boolean };
type SkillFlowNode = Node<SkillNodeData, "skill">;

const SkillNode = memo(function SkillNode({ data }: NodeProps<SkillFlowNode>) {
  const { node, dim, lit, open } = data;
  return (
    <div
      className={cn(
        "graph-node flex h-full w-full cursor-pointer flex-col justify-center rounded-lg border px-3 transition-opacity",
        node.outside ? "border-dashed border-border-strong bg-white/60" : "border-hairline bg-white shadow-tile",
        lit && "border-primary-border bg-primary-soft",
        open && "ring-2 ring-primary/40",
        dim && "opacity-25",
      )}
      data-skill={node.skill.name}
      data-outside={node.outside || undefined}
    >
      <Handle type="target" position={Position.Left} className="!size-1 !min-h-0 !min-w-0 !border-0 !bg-transparent" isConnectable={false} />
      <span className={cn("truncate text-[12.5px] font-semibold", node.outside ? "text-foreground-secondary" : "text-foreground", lit && "text-primary-strong")}>
        {node.skill.name}
      </span>
      {node.outside && <span className="graph-node-where truncate text-[10.5px] text-muted-foreground">{node.where ?? data.noSource}</span>}
      <Handle type="source" position={Position.Right} className="!size-1 !min-h-0 !min-w-0 !border-0 !bg-transparent" isConnectable={false} />
    </div>
  );
});

const nodeTypes = { skill: SkillNode };

interface EdgeHover {
  key: string;
  x: number;
  y: number;
}

// Canvas 是图本身：先按 ELK 排出位置，再交给 React Flow 画;可以拖动平移、捏合缩放。
function Canvas({ graph }: { graph: MentionGraph }) {
  const t = useT().graph;
  const search = useView((v) => v.search);
  const drawer = useView((v) => v.drawer);
  const [placed, setPlaced] = useState<{ graph: MentionGraph; at: Placed; n: number } | null>(null);
  const [hoverNode, setHoverNode] = useState<SkillID | null>(null);
  const [hoverEdge, setHoverEdge] = useState<EdgeHover | null>(null);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let live = true;
    void layout(graph, t.noSource).then((at) => live && setPlaced((p) => ({ graph, at, n: (p?.n ?? 0) + 1 })));
    return () => {
      live = false;
    };
  }, [graph, t.noSource]);

  const byKey = useMemo(() => new Map(graph.edges.map((m) => [edgeKey(m), m])), [graph]);
  const focus = useMemo(() => {
    if (hoverNode !== null) return neighbours(graph.edges, hoverNode);
    const m = hoverEdge && byKey.get(hoverEdge.key);
    if (m) return { nodes: new Set([m.from, m.to]), edges: new Set([hoverEdge.key]) };
    return null;
  }, [graph, hoverNode, hoverEdge, byKey]);

  const searching = !!search.trim();
  const nodes = useMemo<SkillFlowNode[]>(() => {
    if (!placed) return [];
    return graph.nodes.flatMap((n) => {
      const p = placed.at.nodes[n.skill.id];
      if (!p) return [];
      const lit = !!focus?.nodes.has(n.skill.id);
      const dim = focus ? !lit : searching && !matches(search, n.skill);
      return [
        {
          id: String(n.skill.id),
          type: "skill" as const,
          position: { x: p.x, y: p.y },
          width: p.width,
          height: p.height,
          data: { node: n, noSource: t.noSource, dim, lit, open: drawer === n.skill.id },
        },
      ];
    });
  }, [graph, placed, focus, search, searching, hoverNode, drawer, t.noSource]);

  const edges = useMemo<RoutedEdge[]>(
    () =>
      graph.edges.map((m) => {
        const key = edgeKey(m);
        const lit = !!focus?.edges.has(key);
        const color = lit ? "var(--color-primary)" : "var(--color-subtle)";
        return {
          id: key,
          source: String(m.from),
          target: String(m.to),
          type: "routed" as const,
          data: { points: placed?.at.edges[key] ?? [] },
          interactionWidth: 18,
          markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color },
          style: { stroke: color, strokeWidth: lit ? 1.8 : 1.2, opacity: focus && !lit ? 0.12 : 1, transition: "opacity .12s" },
          zIndex: lit ? 1 : 0,
        };
      }),
    [graph, focus, placed],
  );

  const viewport = useMemo(() => (placed ? initialViewport(placed.at, box.current) : undefined), [placed]);

  const at = (e: React.MouseEvent) => {
    const r = box.current!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };

  return (
    <div ref={box} className="graph-canvas relative min-h-0 flex-1 overflow-hidden rounded-[10px] border border-hairline bg-white/55 shadow-tile">
      {placed && placed.graph === graph && (
        <ReactFlow
          key={placed.n}
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          defaultViewport={viewport}
          minZoom={0.2}
          maxZoom={2}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
          panOnScroll
          zoomOnScroll={false}
          zoomOnPinch
          proOptions={{ hideAttribution: true }}
          onNodeMouseEnter={(_, n) => {
            setHoverEdge(null);
            setHoverNode(Number(n.id));
          }}
          onNodeMouseLeave={() => setHoverNode(null)}
          onNodeClick={(_, n) => {
            setHoverEdge(null);
            toggleDrawer(Number(n.id));
          }}
          onMoveStart={() => setHoverEdge(null)}
          onPaneMouseLeave={() => {
            setHoverEdge(null);
            setHoverNode(null);
          }}
          onEdgeClick={(e, edge) => setHoverEdge({ key: edge.id, ...at(e) })}
          onEdgeMouseEnter={(e, edge) => setHoverEdge({ key: edge.id, ...at(e) })}
          onEdgeMouseMove={(e, edge) => setHoverEdge({ key: edge.id, ...at(e) })}
          onEdgeMouseLeave={() => setHoverEdge(null)}
        >
          <Controls showInteractive={false} position="bottom-right" fitViewOptions={{ padding: 0.08, maxZoom: 1 }} />
        </ReactFlow>
      )}
      {hoverEdge && byKey.get(hoverEdge.key) && <LinesTip mention={byKey.get(hoverEdge.key)!} at={hoverEdge} box={box.current} />}
    </div>
  );
}

// initialViewport 是图刚画出来时的视野：放得下就整张缩放到框里(不放大);太大时不缩到字看不清，
// 从最左边(入口技能)开始显示，其余拖动或滚动查看。
function initialViewport(placed: Placed, box: HTMLDivElement | null): { x: number; y: number; zoom: number } {
  const all = Object.values(placed.nodes);
  if (!box || !all.length) return { x: 0, y: 0, zoom: 1 };
  const pad = 24;
  const right = Math.max(...all.map((n) => n.x + n.width));
  const bottom = Math.max(...all.map((n) => n.y + n.height));
  const [w, h] = [box.clientWidth - pad * 2, box.clientHeight - pad * 2];
  const zoom = Math.max(0.8, Math.min(1, w / right, h / bottom));
  const x = right * zoom <= w ? (box.clientWidth - right * zoom) / 2 : pad;
  const y = bottom * zoom <= h ? (box.clientHeight - bottom * zoom) / 2 : pad;
  return { x, y, zoom };
}

const maxLines = 6;

// LinesTip 是悬停一条线时跟着鼠标的说明：谁提到谁，以及提到对方的那几行原文。
function LinesTip({ mention, at, box }: { mention: Mention; at: { x: number; y: number }; box: HTMLDivElement | null }) {
  const s = useSnapshot();
  const t = useT().graph;
  const name = (id: SkillID) => s.skills.find((x) => x.id === id)?.name ?? `#${id}`;
  const lines = mention.lines ?? [];
  const width = 380;
  const right = box ? at.x + 16 + width > box.clientWidth : false;
  const below = box ? at.y < box.clientHeight / 2 : true;
  return (
    <div
      className="graph-tip pointer-events-none absolute z-10 flex w-[380px] flex-col gap-1.5 rounded-lg border border-hairline glass px-3 py-2.5 text-[11.5px] shadow-[0_8px_24px_rgba(15,23,42,0.14)]"
      style={{
        left: right ? undefined : at.x + 16,
        right: right && box ? box.clientWidth - at.x + 16 : undefined,
        top: below ? at.y + 14 : undefined,
        bottom: below || !box ? undefined : box.clientHeight - at.y + 14,
      }}
    >
      <b className="text-[12px] font-semibold text-foreground">{t.mentions(name(mention.from), name(mention.to))}</b>
      {lines.slice(0, maxLines).map((l, i) => (
        <div key={i} className="graph-tip-line min-w-0">
          <span className="font-mono text-[10px] text-subtle">{l.path}</span>
          <p className="line-clamp-3 break-words text-foreground-secondary">{l.text}</p>
        </div>
      ))}
      {lines.length > maxLines && <span className="text-muted-foreground">{t.moreLines(lines.length - maxLines)}</span>}
    </div>
  );
}
