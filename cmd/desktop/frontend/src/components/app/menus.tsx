// 集合与组的菜单：左栏集合行、组行的右键菜单，与列表节标题的「⋯」和右键菜单是同一份，菜单项只在这里列一次。
// 菜单项是数据(MenuEntry),由 ContextMenuEntries / DropdownMenuEntries 画成右键菜单或下拉菜单。
import type { Collection, Group } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { collectionSkills, isLocal, sourceLabel } from "@/lib/source";
import { sourceState } from "@/lib/source-state";
import { missingFromGroup } from "@/features/group/group-rules";
import { useBusy } from "@/lib/ui";
import { openGraph } from "@/lib/view";
import { checkUpdates } from "@/features/update/check";
import { openHistory, openUpdate, rereadDirectory } from "@/features/update/update";
import { previewCollection } from "@/features/fetch/preview";
import { addCollectionToGroup, createGroup, deleteGroup, renameGroup } from "@/features/group/group";
import {
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
} from "@/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import { Icon } from "@/components/ui/icon";
import { cn } from "@/lib/utils";

// MenuEntry 是菜单里的一项:"separator" 是分隔线;有 sub 的是子菜单;note 是写在选项右边的淡色说明(如「已在组里」),
// title 是悬停提示，className 是测试钩子。
export type MenuEntry =
  | "separator"
  | { label: string; sub: MenuEntry[] }
  | { label: string; onSelect: () => void; disabled?: boolean; note?: string; title?: string; className?: string };

// useCollectionMenu 是一个集合的菜单：关系图(graph 为 true 时)、检查更新与更新(本地目录来源换成重新读取整个目录)、看来源里的其他技能、更新历史、加入组。
export function useCollectionMenu(c: Collection, { graph }: { graph: boolean }): MenuEntry[] {
  const s = useSnapshot();
  const busy = useBusy();
  const tt = useT();
  const t = tt.sidebar;
  const local = isLocal(c.source);
  const label = sourceLabel(s, c.source);
  const entries: MenuEntry[] = [];
  if (graph) entries.push({ label: tt.graph.open, onSelect: () => openGraph({ kind: "collection", id: c.id }) }, "separator");
  if (!local) {
    entries.push({ label: tt.toolbar.check, disabled: busy, onSelect: () => checkUpdates([c.id], c.id) });
    if (collectionSkills(s, c).some((x) => sourceState(x).canPlan)) entries.push({ label: t.update, onSelect: () => openUpdate(c.id) });
  } else {
    // 本地目录来源：一次把整个目录里改过的技能读进来，不用逐个点开详情。
    entries.push({ label: t.rereadDirectory, disabled: busy, onSelect: () => void rereadDirectory(c.id) });
  }
  entries.push(
    { label: t.otherSkills, onSelect: () => previewCollection(c.id) },
    { label: t.history, onSelect: () => openHistory({ collection_id: c.id }, tt.list.historyTitle(label)) },
    "separator",
    {
      label: t.addToGroup,
      sub: [
        ...s.groups.map((g): MenuEntry => {
          const already = missingFromGroup(s, c.id, g.id).length === 0;
          return { label: g.name, disabled: already || busy, note: already ? tt.layout.alreadyThere : undefined, onSelect: () => addCollectionToGroup(g.id, c.id) };
        }),
        ...(s.groups.length ? ["separator" as const] : []),
        { label: t.newGroupWith, disabled: busy, onSelect: () => createGroup(c.skills ?? []) },
      ],
    },
  );
  return entries;
}

// useGroupMenu 是一个组的菜单：关系图(graph 为 true 时)、重命名、删除组;newGroup 为 true 时(左栏)最后还有新建组。
export function useGroupMenu(g: Group, { graph, newGroup = false }: { graph: boolean; newGroup?: boolean }): MenuEntry[] {
  const tt = useT();
  const t = tt.sidebar;
  return [
    ...(graph ? [{ label: tt.graph.open, onSelect: () => openGraph({ kind: "group", id: g.id }) }, "separator" as const] : []),
    { label: t.renameWith, onSelect: () => renameGroup(g.id) },
    { label: t.deleteGroupWith, className: "text-destructive", onSelect: () => deleteGroup(g.id) },
    ...(newGroup ? ["separator" as const, { label: t.newGroupWith, onSelect: () => createGroup() }] : []),
  ];
}

const kits = {
  context: { Item: ContextMenuItem, Separator: ContextMenuSeparator, Sub: ContextMenuSub, SubTrigger: ContextMenuSubTrigger, SubContent: ContextMenuSubContent },
  dropdown: { Item: DropdownMenuItem, Separator: DropdownMenuSeparator, Sub: DropdownMenuSub, SubTrigger: DropdownMenuSubTrigger, SubContent: DropdownMenuSubContent },
};

function Entries({ entries, kit }: { entries: MenuEntry[]; kit: (typeof kits)[keyof typeof kits] }) {
  const { Item, Separator, Sub, SubTrigger, SubContent } = kit;
  return entries.map((m, i) =>
    m === "separator" ? (
      <Separator key={i} />
    ) : "sub" in m ? (
      <Sub key={i}>
        <SubTrigger>{m.label}</SubTrigger>
        <SubContent>
          <Entries entries={m.sub} kit={kit} />
        </SubContent>
      </Sub>
    ) : (
      <Item key={i} disabled={m.disabled} onSelect={m.onSelect} title={m.title} className={m.className}>
        {m.note ? <span className="min-w-0 grow truncate">{m.label}</span> : m.label}
        {m.note && <span className="shrink-0 text-caption text-muted-foreground">{m.note}</span>}
      </Item>
    ),
  );
}

// ContextMenuEntries 把菜单项画进右键菜单(ContextMenuContent 里)。
export function ContextMenuEntries({ entries }: { entries: MenuEntry[] }) {
  return <Entries entries={entries} kit={kits.context} />;
}

// DropdownMenuEntries 把菜单项画进下拉菜单(DropdownMenuContent 里)。
export function DropdownMenuEntries({ entries }: { entries: MenuEntry[] }) {
  return <Entries entries={entries} kit={kits.dropdown} />;
}

// MoreMenu 是「⋯」按钮加下拉菜单(节标题、技能详情安装表的一处)。
export function MoreMenu({ entries, label, className, align }: { entries: MenuEntry[]; label: string; className?: string; align?: "start" | "end" }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" className={cn("shrink-0", className)} title={label} aria-label={label}>
          <Icon name="more" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align={align}>
        <DropdownMenuEntries entries={entries} />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
