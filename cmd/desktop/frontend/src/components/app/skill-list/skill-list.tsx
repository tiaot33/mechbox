// 中间：五种页面，各回答一个问题，长得也不一样。
// - 技能库(我有什么):永远是全部，按集合或按组分节;左栏点集合、组只是跳到那一节。
// - 更新(要处理的事):每个有要处理的更新的集合一行，下面是要处理的技能。
// - 作用域(Agent 在这里看得到什么):分上下两块。上面是 Agent 在这里看得到的：装在这里的库里的技能(和技能库页一样按集合或按组分节)、
//   未纳管技能，以及项目里来自全局的;下面是库里没装在这里的技能，整块默认折成一行，点开同样按集合或按组分节。
// - 项目(这个项目里各 Agent 装了什么):每个装了技能的 Agent 一节，列出装在这里的技能与未纳管技能。
// - 关系图(这些技能之间谁提到谁):一个集合或组的技能画成一张图，见 mention-graph。
// 搜索只在当前页里找，顶上写出搜的是什么、几个结果。
import { useEffect, useLayoutEffect, useRef } from "react";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { cn } from "@/lib/utils";
import {
  agentNameIn,
  parseScope,
  placeOf,
  projectFolderNote,
  scopeLabel,
  sectionKey,
  sourceLabel,
  updatableCollections,
  type PageListing,
  type ScopeKey,
  type SkillSection,
} from "@/lib/model";
import { closeDrawer, doneRestoreScroll, openScope, setArrange, toggleUnfolded, usePageListing, useScopeDirectory, useView } from "@/lib/view";
import { MentionGraphPage } from "@/components/app/mention-graph/mention-graph";
import { SearchLine } from "./search-line";
import { openFetch } from "@/features/fetch/fetch-dialog";
import { offerFetchUnmanaged } from "@/features/fetch/fetch-unmanaged-all";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/segmented";
import { Icon } from "@/components/ui/icon";
import { SourceIcon } from "@/components/app/icons";
import { centerClass } from "@/components/app/layout";
import { PlaceAgentMark } from "@/components/app/parts";
import { ProjectSummary, ScopeSummary } from "./heads";
import { PickBar } from "./pick-bar";
import { nameIndent, nameIndentVars, pickColumn, SkillRow, UnmanagedRow, UpdateSkillRow } from "./rows";
import { CollectionSection, GroupSection, PlainHead, Section, SkillRows } from "./sections";
import { UpdatesHead } from "./updates-head";
import { flash } from "./flash";
import { useSameNameCopies } from "./same-name";

// notBlank 是列表里不算空白的地方：技能行、节标题、作用域页与项目页顶部的摘要条，以及各种控件。
// 列表里新加能点的东西要用按钮、链接或带 role 的元素(或加进这里),否则点它也会关上技能详情。
const notBlank = "article, .section-head, .a-scope-sum, button, a, input, textarea, select, label, [role]";

// drawerBusy 是技能详情里正在写备注，或开着弹层(下拉框、菜单):这时点空白处只让输入框失焦、收起弹层，不关详情。
const drawerBusy = () => !!document.querySelector('[data-slot="sheet-content"] textarea, [data-radix-popper-content-wrapper]');

export function SkillList() {
  const listing = usePageListing();
  const scroller = useRef<HTMLDivElement>(null);
  const restore = useView((v) => v.restoreScroll);
  const picking = useView((v) => v.picked.size > 0);
  const busy = useRef(false);
  // 从关系图页返回：列表滚回打开关系图之前的位置。
  useLayoutEffect(() => {
    if (restore === null || !scroller.current) return;
    scroller.current.scrollTop = restore;
    doneRestoreScroll();
  }, [restore, listing?.kind]);
  const page = !listing ? "" : listing.kind === "scope" ? listing.scope : listing.kind === "project" ? `project-${listing.project}` : listing.kind;
  // 技能详情并排打开或关上时列表变窄、变宽，跨过 600px 时每行在一层和两层之间换，行高跟着变:
  // 把选中的那一行(刚点开详情的)留在视口里，不让它被挤出去。宽度按容器的内容宽度算，和 rows.tsx 的容器查询一致。
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    let narrow: boolean | null = null;
    const observer = new ResizeObserver(([entry]) => {
      const now = entry.contentRect.width < 600;
      if (narrow !== null && now !== narrow) el.querySelector(".skill-row[data-selected]")?.scrollIntoView({ block: "nearest" });
      narrow = now;
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [page]);
  if (!listing) return <main className={centerClass} />;
  if (listing.kind === "graph") {
    return (
      <main className={cn(centerClass, "relative")} data-page="graph">
        <MentionGraphPage key={`${listing.set.kind}-${listing.set.id}`} set={listing.set} members={listing.members} />
      </main>
    );
  }
  return (
    <main className={cn(centerClass, "relative")}>
      {/* 上边距放在里面一层：滚动区自己没有上内边距，吸顶的节标题才贴着底板上沿。换页时换一个滚动区，回到顶上。 */}
      <div
        ref={scroller}
        key={page}
        // 勾选了技能时底部浮着操作栏(PickBar,换行时两层):多留出它的高度，滚到底时最后一行不被盖住。
        className={cn("a-list min-h-0 flex-1 overflow-y-auto px-3.5 pb-15", picking && listing.kind !== "updates" && "pb-28", nameIndentVars)}
        data-page={listing.kind}
        // 点空白处关上技能详情，和关系图里点画布空白处一样;点技能行是切换，点控件照常用，都不关。
        // 抽屉忙不忙在按下时看：松开前弹层已经收起了。选中了文字(拖选)也不关。
        onPointerDownCapture={() => (busy.current = drawerBusy())}
        onClick={(e) => {
          if (busy.current || getSelection()?.isCollapsed === false || (e.target as Element).closest(notBlank)) return;
          closeDrawer();
        }}
      >
        <div className="pt-3">
          <SearchLine results={listing.results} />
          {listing.kind === "all" && <AllSkills sections={listing.sections} />}
          {listing.kind === "updates" && <Updates skills={listing.skills} />}
          {listing.kind === "scope" && <ScopePage listing={listing} />}
          {listing.kind === "project" && <ProjectPage listing={listing} />}
        </div>
      </div>
      <PickBar listing={listing} />
    </main>
  );
}

// EmptyLibrary 是库里还没有技能时的空状态，引导去入库。
function EmptyLibrary() {
  const s = useSnapshot();
  const t = useT().list;
  return (
    <ListEmpty>
      <b className="text-heading text-foreground">{t.emptyLibrary}</b>
      <span>{t.emptyHint}</span>
      <Button variant="primary" className="mt-1.5" onClick={(e) => void openFetch(s.info.home_dir, e.currentTarget)}>
        <Icon name="plus" />
        {t.fetchSkills}
      </Button>
    </ListEmpty>
  );
}

function AllSkills({ sections }: { sections: SkillSection[] }) {
  const s = useSnapshot();
  const jump = useView((v) => v.jump);
  const t = useT().list;

  // 左栏点了集合或组：滚到那一节，闪一下标出来;紧接着又点了别的，上一节的闪烁立刻收掉。
  useEffect(() => {
    if (!jump) return;
    const el = document.querySelector<HTMLElement>(`[data-section="${jump.section}"]`);
    if (el) return flash(el, "start");
  }, [jump]);

  if (s.skills.length === 0) return <EmptyLibrary />;
  return (
    <>
      <ArrangeBar />
      {sections.map((x) => (
        <SkillSectionView key={x.key} section={x} />
      ))}
      {sections.length === 0 && <ListEmpty>{t.noMatch}</ListEmpty>}
    </>
  );
}

// ArrangeBar 切换按集合或按组分节，技能库页和作用域页共用一个设置。
function ArrangeBar({ children }: { children?: React.ReactNode }) {
  const arrange = useView((v) => v.arrange);
  const t = useT().list;
  return (
    // 和下面各节的名字、技能名在同一条线上。
    <div className={cn("mb-1.5 flex items-center gap-2", nameIndent)}>
      <Segmented
        className="arrange-seg"
        value={arrange}
        onChange={setArrange}
        options={[
          { value: "collection", label: t.arrangeCollection, title: t.arrangeCollectionTitle },
          { value: "group", label: t.arrangeGroup, title: t.arrangeGroupTitle },
        ]}
      />
      {children}
    </div>
  );
}

// SkillSectionView 是一节。技能库页(没有 scope)的节里装了的在前、没装的折起来;作用域页(有 scope)的行带开关、不折:
// 上面那块的节只有装在这里的，节标题写装在这里几个;下面那块(notHere)的节只有没装在这里的，节标题只写名字和个数。
// 节标题上的勾选框勾这一节列出的技能。
function SkillSectionView({ section, scope, notHere = false }: { section: SkillSection; scope?: ScopeKey; notHere?: boolean }) {
  const s = useSnapshot();
  const t = useT().list;
  const pick = section.skills.map((x) => x.id);
  // 集合的节标题已经写了来源，行里不再写;其他节逐行写出(没有来源的技能行上照样写"没有来源")。
  const row = (skill: SkillSection["skills"][number]) => (
    <SkillRow
      key={skill.id}
      skill={skill}
      scope={scope}
      showSource={section.kind !== "collection"}
      hideGroup={section.kind === "group" ? section.id : undefined}
    />
  );
  const rows = scope ? section.skills.map(row) : <SkillRows id={section.key} skills={section.skills} render={row} />;
  // 下面那块的节与"没有来源" / "没有归组"用不能拖的节标题。
  if (notHere || section.kind === "rest") {
    const look = sectionLook(s, t, section, section.key === sectionKey("rest", "collection", scope, notHere));
    const count = notHere ? section.all.length : scope ? t.hereCount(section.all.length) : section.skills.length;
    return (
      <Section id={section.key} icon={look.icon} pick={pick} head={<PlainHead name={look.name} title={look.title} count={count} />}>
        {rows}
      </Section>
    );
  }
  const count = scope ? t.hereCount(section.all.length) : undefined;
  if (section.kind === "collection") {
    const c = s.collections.find((x) => x.id === section.id)!;
    return (
      <CollectionSection id={section.key} collection={c} count={count} pick={pick} showGraph={!scope}>
        {rows}
      </CollectionSection>
    );
  }
  const g = s.groups.find((x) => x.id === section.id)!;
  return (
    <GroupSection id={section.key} group={g} count={count} pick={pick} showGraph={!scope}>
      {section.skills.length ? (
        rows
      ) : (
        <EmptyRows>
          <b className="mr-1.5 font-semibold text-foreground-secondary">{t.emptyGroup}</b>
          {t.emptyGroupHint}
        </EmptyRows>
      )}
    </GroupSection>
  );
}

// sectionLook 是不能拖的节标题上的图标、名字和悬停说明：集合写来源，组写组名，最后一节写"没有来源"(rest)或"没有归组"。
function sectionLook(
  s: ReturnType<typeof useSnapshot>,
  t: ReturnType<typeof useT>["list"],
  section: SkillSection,
  rest: boolean,
): { icon: React.ReactNode; name: string; title?: string } {
  const c = section.kind === "collection" ? s.collections.find((x) => x.id === section.id) : undefined;
  if (c) return { icon: <SourceIcon type={c.source.type} className="size-3.5 text-muted-foreground" />, name: sourceLabel(s, c.source) };
  const g = section.kind === "group" ? s.groups.find((x) => x.id === section.id) : undefined;
  if (g) return { icon: <Icon name="layers" className="size-3.5 text-muted-foreground" />, name: g.name };
  return rest
    ? { icon: <Icon name="folder" className="size-3.5 text-muted-foreground" />, name: t.restCollection, title: t.restCollectionTitle }
    : { icon: <Icon name="layers" className="size-3.5 text-muted-foreground" />, name: t.restGroup };
}

function Updates({ skills }: { skills: Extract<PageListing, { kind: "updates" }>["skills"] }) {
  const s = useSnapshot();
  const t = useT().list;
  return (
    <>
      {updatableCollections(s).length > 0 && <UpdatesHead />}
      <Section id="updates" className="mt-4" gutter={false} head={<PlainHead name={t.updatesSkills} count={skills.length} />}>
        {skills.map((skill) => (
          <UpdateSkillRow key={skill.id} skill={skill} />
        ))}
        {skills.length === 0 && <EmptyRows aligned={false}>{t.noMatch}</EmptyRows>}
      </Section>
    </>
  );
}

function ScopePage({ listing: page }: { listing: Extract<PageListing, { kind: "scope" }> }) {
  const s = useSnapshot();
  const search = useView((v) => v.search.trim());
  const directory = useScopeDirectory();
  const t = useT().list;
  const { scope, listing: l } = page;
  const project = placeOf(parseScope(scope)) !== 0;
  const global = l.global.length + l.globalUnmanaged.length;
  const inUse = l.hereSections.length > 0 || l.hereUnmanaged.length > 0 || (project && global > 0);
  const empty = !inUse && !l.notHereSkills.length;
  // 这里的未纳管技能在同一层的别处有同名、内容不同的：行上和整批入库的确认里都说出来。
  // 还在比内容时，整批入库的入口先等着：确认要和行上一样写出同名不同内容的几份。
  const same = useSameNameCopies(scope);
  return (
    <>
      <ScopeSummary scope={scope} directory={directory} here={page.total - page.global} global={page.global} />
      {(l.hereSections.length > 0 || l.notHereSkills.length > 0) && (
        <div className="mt-2.5">
          <ArrangeBar>
            {!search && !l.hereCount && !l.hereUnmanaged.length && <span className="min-w-0 text-caption text-muted-foreground">{t.nothingHere}</span>}
          </ArrangeBar>
        </div>
      )}
      {/* 上面：Agent 在这里看得到的。块里第一节没有上边距，由块自己补上。 */}
      {inUse && (
        <div className="in-use mt-1">
          {l.hereSections.map((x) => (
            <SkillSectionView key={x.key} section={x} scope={scope} />
          ))}
          {l.hereUnmanaged.length > 0 && (
            <Section
              id="scope-unmanaged"
              icon={<Icon name="inbox" className="size-3.5 text-muted-foreground" />}
              head={<PlainHead name={t.notInLibrary} count={l.hereUnmanaged.length} title={t.notInLibraryTitle} />}
              actions={
                // 搜索时节里只列搜到的，整批确认却是整个作用域的：只在不搜索时放这个入口。
                !search && (
                  <Button
                    variant="ghost"
                    className="fetch-all-unmanaged shrink-0"
                    title={t.fetchAllTitle}
                    loading={same.pending}
                    disabled={same.pending}
                    onClick={() => void offerFetchUnmanaged(s.agents, [parseScope(scope)], { kind: "scope", label: scopeLabel(s, parseScope(scope)), elsewhere: same.copies }, false)}
                  >
                    {t.fetchAll}
                  </Button>
                )
              }
            >
              {l.hereUnmanaged.map((u) => (
                <UnmanagedRow key={u.folder} skill={u} headed copies={same.copies.get(u.folder)} />
              ))}
            </Section>
          )}
          {project && global > 0 && (
            <Section
              id="scope-global"
              icon={<Icon name="globe" className="size-3.5 text-muted-foreground" />}
              defaultCollapsed
              pick={l.global.map((x) => x.id)}
              head={<PlainHead name={t.sectionGlobal} count={global} title={t.sectionGlobalTitle} />}
            >
              {l.global.map((skill) => (
                <SkillRow key={skill.id} skill={skill} scope={scope} fromGlobal showSource />
              ))}
              {l.globalUnmanaged.map((u) => (
                <UnmanagedRow key={u.folder} skill={u} fromGlobal />
              ))}
            </Section>
          )}
        </div>
      )}
      {/* 下面：库里没装在这里的。 */}
      {l.notHereSkills.length > 0 && <NotHere scope={scope} listing={l} more={inUse} />}
      {empty && (search ? <ListEmpty>{t.noMatch}</ListEmpty> : !s.skills.length && <EmptyLibrary />)}
    </>
  );
}

// NotHere 是作用域页下面那块：库里没装在这里的技能。上面一条分隔，整块默认折成一行，写出个数和前几个名字;
// 点开按集合或按组分节。展开状态按作用域分开记;搜索时展开，搜到的行都看得见。
function NotHere({ scope, listing: l, more }: { scope: ScopeKey; listing: Extract<PageListing, { kind: "scope" }>["listing"]; more: boolean }) {
  const t = useT().list;
  const id = `${scope}:not-here`;
  const searching = useView((v) => !!v.search.trim());
  const unfolded = useView((v) => v.unfolded.has(id)) || searching;
  const names = l.notHereSkills.map((x) => x.name);
  return (
    <div className="not-here mt-3 border-t border-row-line pt-1">
      <button
        type="button"
        className="not-installed-fold not-here-fold flex h-8 w-full min-w-0 items-center gap-4 rounded-control px-3 text-left text-body text-muted-foreground hover:bg-fill hover:text-foreground"
        aria-expanded={unfolded}
        disabled={searching}
        onClick={() => toggleUnfolded(id)}
      >
        {/* 箭头在勾选框那一列，文字和上面各节的名字在同一条线上。 */}
        <span className={cn("flex shrink-0 justify-center", pickColumn)}>
          <Icon name="chev" className={cn("size-2.5 transition-transform", unfolded && "rotate-90")} />
        </span>
        <span className="ml-[calc(var(--name-indent)-42px)] truncate">{unfolded && !searching ? t.hideNotHere : t.notHereBlock(names, more)}</span>
      </button>
      {unfolded &&
        l.notHereSections.map((x) => (
          <SkillSectionView key={x.key} section={x} scope={scope} notHere />
        ))}
    </div>
  );
}

// ProjectPage 是项目页：顶上写项目里一共装了几个技能与项目目录，下面每个装了技能的 Agent 一节;
// 节里的行和作用域页一样带开关，点节标题上的"打开作用域"去那个 Agent 的作用域页装更多。
function ProjectPage({ listing: page }: { listing: Extract<PageListing, { kind: "project" }> }) {
  const s = useSnapshot();
  const search = useView((v) => v.search.trim());
  const t = useT().list;
  const project = s.projects.find((p) => p.id === page.project)!;
  return (
    <>
      <ProjectSummary project={project} total={page.total} unmanaged={page.unmanaged} />
      {page.agents.map((a) => (
        <Section
          key={a.scope}
          id={`project-${page.project}:${a.scope}`}
          pick={a.skills.map((x) => x.id)}
          icon={<PlaceAgentMark code={a.agent.code} place={page.project} />}
          head={
            <PlainHead
              name={agentNameIn(s, a.agent.code, page.project)}
              count={t.skillCount(a.all)}
              title={projectFolderNote(s, a.agent.code, page.project)}
            />
          }
          actions={
            <Button variant="ghost" className="shrink-0" onClick={() => openScope(a.scope)} title={t.openScopeTitle}>
              {t.openScope}
            </Button>
          }
        >
          {a.skills.map((skill) => (
            <SkillRow key={skill.id} skill={skill} scope={a.scope} showSource />
          ))}
          {a.unmanaged.map((u) => (
            <UnmanagedRow key={u.folder} skill={u} />
          ))}
        </Section>
      ))}
      {page.agents.length === 0 && <ListEmpty>{search ? t.noMatch : t.projectEmpty}</ListEmpty>}
    </>
  );
}

// EmptyRows 是一节里没有行时的说明;aligned 时(行首有勾选框那一列的节)和技能名对齐。
function EmptyRows({ aligned = true, children }: { aligned?: boolean; children: React.ReactNode }) {
  return <p className={cn("py-3 pr-3 text-caption text-muted-foreground", aligned ? nameIndent : "pl-3")}>{children}</p>;
}

// 空状态放在列表区的视觉中部，用上边距定位。
function ListEmpty({ children }: { children: React.ReactNode }) {
  return <div className="flex flex-col items-center justify-center gap-1.5 pt-[22vh] text-muted-foreground">{children}</div>;
}
