// 中间：四种页面，各回答一个问题，长得也不一样。
// - 所有技能(我有什么):永远是全部，按集合或按组分节;左栏点集合、组只是跳到那一节。
// - 更新(要处理的事):每个有要处理的更新的集合一行，下面是要处理的技能。
// - 作用域(Agent 在这里看得到什么):库里的技能和所有技能页一样按集合或按组分节，节里装在这里的在前、没装在这里的折起来;
//   有技能装在这里的节排在前面;另有装在这里的未纳管技能，以及项目里来自全局的。
// - 项目(这个项目里各 Agent 装了什么):每个装了技能的 Agent 一节，列出装在这里的技能与未纳管技能。
// 搜索只在当前页里找，顶上写出搜的是什么、几个结果。
import { useEffect } from "react";
import type { Collection, Group, Skill, SkillID } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { cn } from "@/lib/utils";
import {
  agentNameIn,
  anyUpdatable,
  parseScope,
  placeOf,
  projectFolderNote,
  sectionKey,
  type PageListing,
  type ScopeKey,
  type SkillSection,
} from "@/lib/model";
import { openScope, setArrange, setSearch, usePageListing, useScopeDirectory, useView } from "@/lib/view";
import { openFetch } from "@/features/fetch/fetch-dialog";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/segmented";
import { Icon } from "@/components/app/icons";
import { centerClass } from "@/components/app/layout";
import { PlaceAgentMark } from "@/components/app/parts";
import { ProjectSummary, ScopeSummary } from "./heads";
import { PickBar } from "./pick-bar";
import { SkillRow, UnmanagedRow } from "./rows";
import { useCollectionDrop, useGroupDrop } from "@/components/app/dnd";
import { CollectionHead, CollectionNews, GroupHead, PlainHead, Section, SkillRows, UseCounts } from "./sections";
import { UpdatesHead } from "./updates-head";

export function SkillList() {
  const listing = usePageListing();
  if (!listing) return <main className={centerClass} />;
  return (
    <main className={cn(centerClass, "relative")}>
      {/* 上边距放在里面一层：滚动区自己没有上内边距，吸顶的节标题才贴着底板上沿。换页时换一个滚动区，回到顶上。 */}
      <div
        key={listing.kind === "scope" ? listing.scope : listing.kind === "project" ? `project-${listing.project}` : listing.kind}
        className="a-list min-h-0 flex-1 overflow-y-auto px-3.5 pb-15"
        data-page={listing.kind}
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

// SearchLine 写出正在搜什么、当前页里有几个结果，点"清除"回到不搜索。
function SearchLine({ results }: { results: number }) {
  const search = useView((v) => v.search.trim());
  const t = useT().list;
  if (!search) return null;
  return (
    <div className="search-line mb-2.5 flex items-center gap-2 text-[12px] text-muted-foreground">
      <span>{t.searchResults(search, results)}</span>
      <Button variant="link" className="text-[12px]" onClick={() => setSearch("")}>
        {t.clearSearch}
      </Button>
    </div>
  );
}

// EmptyLibrary 是库里还没有技能时的空状态，引导去入库。
function EmptyLibrary() {
  const s = useSnapshot();
  const t = useT().list;
  return (
    <ListEmpty>
      <b className="text-sm text-foreground">{t.emptyLibrary}</b>
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
    if (!el) return;
    el.scrollIntoView({ block: "start", behavior: "smooth" });
    el.dataset.flash = "";
    const timer = window.setTimeout(() => delete el.dataset.flash, 1200);
    return () => {
      window.clearTimeout(timer);
      delete el.dataset.flash;
    };
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

// ArrangeBar 切换按集合或按组分节，所有技能页和作用域页共用一个设置。
function ArrangeBar({ children }: { children?: React.ReactNode }) {
  const arrange = useView((v) => v.arrange);
  const t = useT().list;
  return (
    <div className="mb-2.5 flex items-center gap-2">
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

// SkillSectionView 是一节;有 scope 时是作用域页的样子：行带开关，按装没装在这里折叠。节标题上的勾选框勾这一节列出的技能。
function SkillSectionView({ section, scope }: { section: SkillSection; scope?: ScopeKey }) {
  const s = useSnapshot();
  const t = useT().list;
  const pick = section.skills.map((x) => x.id);
  // 集合的节标题已经写了来源，行里不再写;其他节逐行写出(没有来源的技能行上照样写"没有来源")。
  const rows = (
    <SkillRows
      id={section.key}
      skills={section.skills}
      scope={scope}
      render={(skill) => (
        <SkillRow
          key={skill.id}
          skill={skill}
          scope={scope}
          pickable
          showSource={section.kind !== "collection"}
          hideGroup={section.kind === "group" ? section.id : undefined}
        />
      )}
    />
  );
  if (section.kind === "collection") {
    const c = s.collections.find((x) => x.id === section.id)!;
    return (
      <CollectionSection id={section.key} collection={c} scope={scope} listed={section.all} pick={pick}>
        {rows}
      </CollectionSection>
    );
  }
  if (section.kind === "group") {
    const g = s.groups.find((x) => x.id === section.id)!;
    return (
      <GroupSection id={section.key} group={g} scope={scope} listed={section.all} pick={pick}>
        {section.skills.length ? (
          rows
        ) : (
          <EmptyRows>
            <b className="mr-1.5 font-medium text-foreground-secondary">{t.emptyGroup}</b>
            {t.emptyGroupHint}
          </EmptyRows>
        )}
      </GroupSection>
    );
  }
  const rest = section.key === sectionKey("rest", "collection", scope);
  return (
    <Section
      id={section.key}
      pick={pick}
      head={
        <PlainHead
          icon={<Icon name={rest ? "folder" : "layers"} className="size-3.5 text-muted-foreground" />}
          name={rest ? t.restCollection : t.restGroup}
          count={scope ? <UseCounts skills={section.all} scope={scope} /> : section.skills.length}
          title={rest ? t.restCollectionTitle : undefined}
        />
      }
    >
      {rows}
    </Section>
  );
}

function CollectionSection({
  id,
  collection,
  scope,
  listed,
  pick,
  children,
}: {
  id: string;
  collection: Collection;
  scope?: ScopeKey;
  listed: Skill[];
  pick: SkillID[];
  children: React.ReactNode;
}) {
  const drop = useCollectionDrop(collection.id);
  return (
    <Section
      id={id}
      pick={pick}
      head={<CollectionHead collection={collection} scope={scope} listed={listed} />}
      sub={<CollectionNews collection={collection} />}
      headProps={drop}
    >
      {children}
    </Section>
  );
}

function GroupSection({
  id,
  group,
  scope,
  listed,
  pick,
  children,
}: {
  id: string;
  group: Group;
  scope?: ScopeKey;
  listed: Skill[];
  pick: SkillID[];
  children: React.ReactNode;
}) {
  const drop = useGroupDrop(group.id);
  return (
    <Section id={id} pick={pick} head={<GroupHead group={group} scope={scope} listed={listed} />} headProps={drop}>
      {children}
    </Section>
  );
}

function Updates({ skills }: { skills: Extract<PageListing, { kind: "updates" }>["skills"] }) {
  const s = useSnapshot();
  const t = useT().list;
  return (
    <>
      {anyUpdatable(s) && <UpdatesHead />}
      <Section id="updates" className="mt-3" head={<PlainHead name={t.updatesSkills} count={skills.length} />}>
        {skills.map((skill) => (
          <SkillRow key={skill.id} skill={skill} showSource />
        ))}
        {skills.length === 0 && <EmptyRows>{t.noMatch}</EmptyRows>}
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
  const empty = !l.sections.length && !l.hereUnmanaged.length && !(project && global);
  return (
    <>
      <ScopeSummary scope={scope} directory={directory} sees={page.total} />
      {l.sections.length > 0 && (
        <div className="mt-3">
          <ArrangeBar>
            {!search && !l.here && !l.hereUnmanaged.length && <span className="min-w-0 text-[11.5px] text-muted-foreground">{t.nothingHere}</span>}
          </ArrangeBar>
        </div>
      )}
      {/* 装在这里的未纳管技能也算装在这里：放在有技能装在这里的节后面、一个都没装的节前面。 */}
      {l.sections.slice(0, l.used).map((x) => (
        <SkillSectionView key={x.key} section={x} scope={scope} />
      ))}
      {l.hereUnmanaged.length > 0 && (
        <Section id="scope-unmanaged" head={<PlainHead name={t.sectionUnmanaged} count={l.hereUnmanaged.length} title={t.unmanagedTitle} />}>
          {l.hereUnmanaged.map((u) => (
            <UnmanagedRow key={u.folder} skill={u} />
          ))}
        </Section>
      )}
      {l.sections.slice(l.used).map((x) => (
        <SkillSectionView key={x.key} section={x} scope={scope} />
      ))}
      {project && global > 0 && (
        <Section id="scope-global" defaultCollapsed pick={l.global.map((x) => x.id)} head={<PlainHead name={t.sectionGlobal} count={global} title={t.sectionGlobalTitle} />}>
          {l.global.map((skill) => (
            <SkillRow key={skill.id} skill={skill} scope={scope} fromGlobal pickable showSource />
          ))}
          {l.globalUnmanaged.map((u) => (
            <UnmanagedRow key={u.folder} skill={u} />
          ))}
        </Section>
      )}
      {empty && (search ? <ListEmpty>{t.noMatch}</ListEmpty> : !s.skills.length && <EmptyLibrary />)}
    </>
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
      <ProjectSummary project={project} total={page.total} />
      {page.agents.map((a) => (
        <Section
          key={a.scope}
          id={`project-${page.project}:${a.scope}`}
          pick={a.skills.map((x) => x.id)}
          head={
            <>
              <PlainHead
                icon={<PlaceAgentMark code={a.agent.code} place={page.project} />}
                name={agentNameIn(s, a.agent.code, page.project)}
                count={t.skillCount(a.all)}
                title={projectFolderNote(s, a.agent.code, page.project)}
              />
              <Button size="sm" variant="ghost" className="shrink-0" onClick={() => openScope(a.scope)} title={t.openScopeTitle}>
                {t.openScope}
              </Button>
            </>
          }
        >
          {a.skills.map((skill) => (
            <SkillRow key={skill.id} skill={skill} scope={a.scope} pickable showSource />
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

function EmptyRows({ children }: { children: React.ReactNode }) {
  return <p className="px-3 py-3 text-[11.5px] text-muted-foreground">{children}</p>;
}

// 空状态放在列表区的视觉中部，用上边距定位。
function ListEmpty({ children }: { children: React.ReactNode }) {
  return <div className="flex flex-col items-center justify-center gap-1.5 pt-[22vh] text-muted-foreground">{children}</div>;
}
