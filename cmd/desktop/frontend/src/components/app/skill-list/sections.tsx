// 列表的分节：技能库页与作用域页都按集合或按组分节，作用域页另有未纳管技能与(项目里)来自全局的两节，以及下面那块没装在这里的。
// 节标题可以收起;集合与组的节标题可以拖到右栏作用域(集合 = 把它的技能逐个直接安装，组 = 组安装),
// 拖到同类的节标题上调整先后;组的节标题也接受技能与集合(加入该组)。集合的更新、历史与来源里的新技能放在集合的节上，组的重命名、删除与组安装于放在组的节标题上。
// 版式(卡片、标题行、列)见 list-layout.tsx。
import type { Collection, Group, Skill, SkillID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import {
  collectionSkills,
  hasUpdate,
  isLocal,
  pending,
  placeOf,
  projectName,
  revisionSummary,
  revisionText,
  scopeKey,
  skillScopesOf,
  sourceLabel,
  updateAvailable,
  wslTitle,
} from "@/lib/model";
import { cn } from "@/lib/utils";
import { toggleSection, toggleUnfolded, useView } from "@/lib/view";
import { notInterested, previewCollection, previewNewSkills } from "@/features/fetch/preview";
import { deleteGroup, renameGroup } from "@/features/group/group";
import { uninstallGroup } from "@/features/install/install";
import { openHistory, openUpdate } from "@/features/update/update";
import { Button } from "@/components/ui/button";
import { useDnD } from "@/components/app/dnd";
import { Icon } from "@/components/app/icons";
import { Chip, PlaceAgentMark, WSLBadge } from "@/components/app/parts";
import { CardHead, FoldRow, HeadAction, HeadActions, HeadMeta, HeadName, ListCard, Notice, NoticeAction, type CardTone } from "./list-layout";
import { SectionPick } from "./pick";

// Section 是一节：标题行与行在同一张卡片里，notice 是标题行与行之间的一条提示(集合里来源新发布的技能)。
// 标题开头一格放这一节的图标(mark);给了 pick 时鼠标移上去换成勾选框，一次勾上这一节的这些技能。
// defaultCollapsed 的节(项目里来自全局的)默认收起，点一下展开;收起状态记在界面状态里，按 id 存与默认相反的那些。搜索时都展开，搜到的行都看得见。
export function Section({
  id,
  head,
  mark,
  notice,
  tone,
  defaultCollapsed = false,
  className,
  headProps,
  pick,
  children,
}: {
  id: string;
  head: React.ReactNode;
  mark?: React.ReactNode;
  notice?: React.ReactNode;
  tone?: CardTone;
  defaultCollapsed?: boolean;
  className?: string;
  headProps?: React.HTMLAttributes<HTMLDivElement> & Record<`data-${string}`, unknown>;
  pick?: SkillID[];
  children: React.ReactNode;
}) {
  const flipped = useView((v) => v.flipped.has(id));
  const searching = useView((v) => !!v.search.trim());
  const open = searching || defaultCollapsed === flipped;
  return (
    <section className={cn("list-section mt-2 first:mt-0", className)} data-section={id}>
      <ListCard
        tone={tone}
        open={open}
        notice={notice}
        head={
          <CardHead lead={<SectionPick ids={pick ?? []} mark={mark} />} open={open} onToggle={() => toggleSection(id)} headProps={headProps}>
            {head}
          </CardHead>
        }
      >
        {children}
      </ListCard>
    </section>
  );
}

// inUse 是一节里算作在用的技能：装在至少一个地方的。技能库页节标题的个数和行的先后都按它。
const inUse = (s: ReturnType<typeof useSnapshot>, skills: Skill[]) => skills.filter((x) => skillScopesOf(s, x.id).length > 0);

// CollectionHead 是集合的节标题：来源、在用几个(作用域页给 count,只写个数：整页都是这个作用域，不再每节写"装在这里")、来源版本与有变化的个数，
// 看来源里的其他技能、历史、更新;可以整个拖到作用域。
export function CollectionHead({ collection, count }: { collection: Collection; count?: number }) {
  const s = useSnapshot();
  const dnd = useDnD();
  const t = useT();
  const skills = collectionSkills(s, collection);
  const todo = skills.filter(pending).length;
  // "有变化"只算来源里有新内容的技能(跳过了的也算);来源里已删除、推测出改名的不算。
  const changed = skills.filter(updateAvailable).length;
  const local = isLocal(collection.source);
  const label = sourceLabel(s, collection.source);
  const facts = [
    count === undefined ? t.list.inUse(inUse(s, skills).length, skills.length) : "",
    revisionText(revisionSummary(skills)),
    changed ? t.drawer.changedCount(changed) : "",
  ].filter(Boolean);
  return (
    <>
      <span
        className="collection-handle flex min-w-0 cursor-grab items-center gap-2 active:cursor-grabbing"
        draggable
        onDragStart={(e) => dnd.start(e, { kind: "collection", id: collection.id }, t.list.collectionDrag(label, skills.length))}
        onDragEnd={dnd.end}
        title={t.list.collectionDragTitle(skills.length)}
      >
        <HeadName title={wslTitle(local ? collection.source.label : undefined)}>{label}</HeadName>
        {local && <WSLBadge path={collection.source.label} />}
      </span>
      {count !== undefined && <HeadMeta>{count}</HeadMeta>}
      <HeadMeta grow title={facts.join(" · ")}>
        {facts.join(" · ")}
      </HeadMeta>
      <HeadActions>
        <HeadAction icon="search" onClick={() => previewCollection(collection.id)} title={t.list.otherSkillsHint}>
          {t.drawer.otherSkills}
        </HeadAction>
        <HeadAction icon="clock" onClick={() => openHistory({ collection_id: collection.id }, t.list.historyTitle(label))}>
          {t.list.history}
        </HeadAction>
        {!local && skills.some(hasUpdate) && (
          <Button size="sm" variant={todo ? "primary" : "default"} onClick={() => openUpdate(collection.id)}>
            <Icon name="up" />
            {todo ? t.list.updateCount(todo) : t.list.update}
          </Button>
        )}
      </HeadActions>
    </>
  );
}

// CollectionNews 是集合卡片下面的一条提示：来源里新发布的技能，去入库或不感兴趣。
export function CollectionNews({ collection }: { collection: Collection }) {
  const t = useT();
  const fresh = collection.new_skills ?? [];
  if (!fresh.length) return null;
  return (
    <Notice
      className="collection-new"
      actions={
        <>
          <NoticeAction onClick={() => previewNewSkills(collection.id)}>{t.drawer.goFetch}</NoticeAction>
          <NoticeAction onClick={() => notInterested(collection.id, fresh)}>{t.drawer.notInterested}</NoticeAction>
        </>
      }
    >
      {t.list.newPublished(fresh)}
    </Notice>
  );
}

// GroupHead 是组的节标题：可以整组拖到作用域(组安装),写出组安装于哪些作用域(可以卸载),重命名与删除。
// 作用域页给 count,只写装在这里几个(一个数);否则写组里几个技能。
export function GroupHead({ group, count }: { group: Group; count?: number }) {
  const s = useSnapshot();
  const dnd = useDnD();
  const t = useT();
  const scopes = s.groupInstalls.filter((a) => a.group_id === group.id).map((a) => a.scope);
  const size = group.members?.length ?? 0;
  return (
    <>
      <span
        className="group-handle flex min-w-0 cursor-grab items-center gap-2 active:cursor-grabbing"
        draggable
        onDragStart={(e) => dnd.start(e, { kind: "group", id: group.id }, t.list.groupDrag(group.name, size))}
        onDragEnd={dnd.end}
        title={t.list.groupDragTitle(size)}
      >
        <HeadName>{group.name}</HeadName>
      </span>
      <HeadMeta>{count ?? t.list.skillCount(size)}</HeadMeta>
      <span className="inline-flex min-w-0 grow basis-0 items-center gap-1 overflow-hidden text-[12px] whitespace-nowrap text-muted-foreground">
        <span aria-hidden>·</span>
        {scopes.length ? (
          <>
            {t.list.groupInstalledIn}
            {scopes.map((sc) => (
              <Chip key={scopeKey(sc)} small removeTitle={t.list.uninstallGroupHere} onRemove={() => uninstallGroup(group.id, sc)}>
                <PlaceAgentMark code={sc.agent} place={placeOf(sc)} />
                {sc.project_id ? projectName(s, sc.project_id) : t.model.global}
              </Chip>
            ))}
          </>
        ) : (
          <span className="text-subtle">{t.list.notGroupInstalled}</span>
        )}
      </span>
      <HeadActions>
        <HeadAction icon="edit" onClick={() => renameGroup(group.id)}>
          {t.list.rename}
        </HeadAction>
        <HeadAction icon="trash" className="text-bad-strong hover:text-bad-strong" onClick={() => deleteGroup(group.id)}>
          {t.list.deleteGroup}
        </HeadAction>
      </HeadActions>
    </>
  );
}

// PlainHead 是不能拖的节标题：没有来源、没有归组、作用域页的未纳管技能与来自全局、作用域页下面那块(没装在这里的)的各节、项目页的各个 Agent。
// 图标由 Section 的 mark 放在开头一格;actions 放在右边(项目页的"打开作用域")。
export function PlainHead({ name, count, title, actions }: { name: string; count: React.ReactNode; title?: string; actions?: React.ReactNode }) {
  return (
    <>
      <span className="flex min-w-0 items-center gap-2" title={title}>
        <HeadName>{name}</HeadName>
        <HeadMeta>{count}</HeadMeta>
      </span>
      {actions && <HeadActions>{actions}</HeadActions>}
    </>
  );
}

// SkillRows 列出技能库页一节里的技能：装在至少一个地方的在前;没装的默认折成一行，点开再列出。
// 搜索时、或这一节一个都没装时(刚入库的集合)直接全部列出。
export function SkillRows({ id, skills, render }: { id: string; skills: Skill[]; render: (skill: Skill) => React.ReactNode }) {
  const s = useSnapshot();
  const t = useT().list;
  const searching = useView((v) => !!v.search.trim());
  const unfolded = useView((v) => v.unfolded.has(id));
  const used = inUse(s, skills);
  const idle = skills.filter((x) => !used.includes(x));
  const foldable = !searching && used.length > 0 && idle.length > 0;
  const shown = foldable && !unfolded ? used : [...used, ...idle];
  return (
    <>
      {shown.map(render)}
      {foldable && (
        <FoldRow open={unfolded} label={unfolded ? t.hideNotInstalled : t.notInstalledFold(idle.map((x) => x.name))} onClick={() => toggleUnfolded(id)} />
      )}
    </>
  );
}
