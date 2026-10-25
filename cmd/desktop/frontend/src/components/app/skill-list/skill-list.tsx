// 中间：三种页面，各回答一个问题，长得也不一样。
// - 所有技能(我有什么):永远是全部，按集合或按组分节;左栏点集合、组只是跳到那一节。
// - 更新(要处理的事):每个有要处理的更新的集合一行，下面是要处理的技能。
// - 作用域(Agent 在这里看得到什么):装在这里的(库里的与未纳管的)、项目里来自全局的，以及库里其他可以装到这里的。
// 搜索只在当前页里找，顶上写出搜的是什么、几个结果。
import { useEffect } from "react";
import type { Group } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { parseScope, placeOf, sectionKey, updatableCollections, type PageListing, type SkillSection } from "@/lib/model";
import { setArrange, setSearch, usePageListing, useScopeDirectory, useView } from "@/lib/view";
import { openFetch } from "@/features/fetch/fetch-dialog";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/segmented";
import { Icon } from "@/components/app/icons";
import { centerClass } from "@/components/app/layout";
import { ScopeSummary } from "./heads";
import { SkillRow, UnmanagedRow } from "./rows";
import { CollectionHead, CollectionNews, GroupHead, PlainHead, Section, SkillRows, useGroupDrop } from "./sections";
import { UpdatesHead } from "./updates-head";

export function SkillList() {
  const listing = usePageListing();
  if (!listing) return <main className={centerClass} />;
  return (
    <main className={centerClass}>
      <div className="a-list min-h-0 flex-1 overflow-y-auto px-3.5 pt-3 pb-15" data-page={listing.kind}>
        <SearchLine results={listing.results} />
        {listing.kind === "all" && <AllSkills sections={listing.sections} />}
        {listing.kind === "updates" && <Updates skills={listing.skills} />}
        {listing.kind === "scope" && <ScopePage key={listing.scope} listing={listing} />}
      </div>
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

function AllSkills({ sections }: { sections: SkillSection[] }) {
  const s = useSnapshot();
  const arrange = useView((v) => v.arrange);
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

  if (s.skills.length === 0) {
    return (
      <ListEmpty>
        <b className="text-sm text-foreground">{t.emptyLibrary}</b>
        <span>{t.emptyHint}</span>
        <Button variant="primary" className="mt-1.5" onClick={() => void openFetch(s.info.home_dir)}>
          <Icon name="plus" />
          {t.fetchSkills}
        </Button>
      </ListEmpty>
    );
  }
  return (
    <>
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
      </div>
      {sections.map((x) => (
        <SkillSectionView key={x.key} section={x} />
      ))}
      {sections.length === 0 && <ListEmpty>{t.noMatch}</ListEmpty>}
    </>
  );
}

function SkillSectionView({ section }: { section: SkillSection }) {
  const s = useSnapshot();
  const t = useT().list;
  // 集合的节标题已经写了来源，行里不再写;其他节里的技能来自不同来源，逐行写出。
  const rows = (
    <SkillRows
      id={section.key}
      skills={section.skills}
      render={(skill) => (
        <SkillRow
          key={skill.id}
          skill={skill}
          showSource={section.kind !== "collection"}
          hideGroup={section.kind === "group" ? section.id : undefined}
        />
      )}
    />
  );
  if (section.kind === "collection") {
    const c = s.collections.find((x) => x.id === section.id)!;
    return (
      <Section id={section.key} head={<CollectionHead collection={c} />} sub={<CollectionNews collection={c} />}>
        {rows}
      </Section>
    );
  }
  if (section.kind === "group") {
    const g = s.groups.find((x) => x.id === section.id)!;
    return (
      <GroupSection group={g}>
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
  const rest = section.key === "rest-collection";
  return (
    <Section
      id={section.key}
      head={
        <PlainHead
          icon={<Icon name={rest ? "folder" : "layers"} className="size-3.5 text-muted-foreground" />}
          name={rest ? t.restCollection : t.restGroup}
          count={section.skills.length}
          title={rest ? t.restCollectionTitle : undefined}
        />
      }
    >
      {rows}
    </Section>
  );
}

function GroupSection({ group, children }: { group: Group; children: React.ReactNode }) {
  const drop = useGroupDrop(group);
  return (
    <Section id={sectionKey("group", group.id)} head={<GroupHead group={group} />} headProps={drop}>
      {children}
    </Section>
  );
}

function Updates({ skills }: { skills: Extract<PageListing, { kind: "updates" }>["skills"] }) {
  const s = useSnapshot();
  const t = useT().list;
  return (
    <>
      {updatableCollections(s).length > 0 && <UpdatesHead />}
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
  const search = useView((v) => v.search);
  const directory = useScopeDirectory();
  const t = useT().list;
  const { scope, listing: l } = page;
  const project = placeOf(parseScope(scope)) !== 0;
  const here = l.here.length + l.hereUnmanaged.length;
  const global = l.global.length + l.globalUnmanaged.length;
  return (
    <>
      <ScopeSummary scope={scope} directory={directory} sees={page.total} />
      <Section id="scope-here" className="mt-3" head={<PlainHead name={t.sectionHere} count={here} />}>
        {l.here.map((skill) => (
          <SkillRow key={skill.id} skill={skill} scope={scope} showSource />
        ))}
        {l.hereUnmanaged.map((u) => (
          <UnmanagedRow key={u.folder} skill={u} />
        ))}
        {here === 0 && <EmptyRows>{search.trim() ? t.noMatch : t.nothingHere}</EmptyRows>}
      </Section>
      {project && global > 0 && (
        <Section id="scope-global" defaultCollapsed head={<PlainHead name={t.sectionGlobal} count={global} title={t.sectionGlobalTitle} />}>
          {l.global.map((skill) => (
            <SkillRow key={skill.id} skill={skill} scope={scope} fromGlobal showSource />
          ))}
          {l.globalUnmanaged.map((u) => (
            <UnmanagedRow key={u.folder} skill={u} />
          ))}
        </Section>
      )}
      {l.others.length > 0 && (
        <Section id="scope-others" head={<PlainHead name={t.sectionOthers} count={l.others.length} />}>
          {l.others.map((skill) => (
            <SkillRow key={skill.id} skill={skill} scope={scope} showSource />
          ))}
        </Section>
      )}
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
