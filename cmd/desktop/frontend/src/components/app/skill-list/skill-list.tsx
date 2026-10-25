// 中间：作用域摘要(选中作用域时)、组 / 集合标题条(筛选组 / 集合时)、更新列表(筛选"更新"时)与技能卡片 / 列表。
// 卡片点开详情抽屉、可拖到左栏组或右栏作用域;选中作用域时卡片上的开关控制直接安装。
// 选中作用域时，库里的技能之后接着列出这个作用域里的未纳管技能(虚线卡片，可以在 Finder 中显示或入库)。
// 和同一个 Agent 另一层级(全局 / 项目)同名的技能，卡片上标"同名"并写出后果。
import { useShallow } from "zustand/react/shallow";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { updatableCollections } from "@/lib/model";
import { cn } from "@/lib/utils";
import { toggleDrawer, useListing, useScopeDirectory, useView } from "@/lib/view";
import { openFetch } from "@/features/fetch/fetch-dialog";
import { Button } from "@/components/ui/button";
import { Icon } from "@/components/app/icons";
import { centerClass } from "@/components/app/layout";
import { CollectionHead, GroupHead, ScopeSummary } from "./heads";
import { SkillCard } from "./skill-card";
import { UnmanagedCard } from "./unmanaged-card";
import { UpdatesHead } from "./updates-head";

export function SkillList() {
  const s = useSnapshot();
  const { skills, unmanaged, filter } = useListing();
  const { scope, view, selected } = useView(useShallow((v) => ({ scope: v.scope, view: v.view, selected: v.drawer })));
  const scopeDirectory = useScopeDirectory();
  const group = filter.kind === "group" ? s.groups.find((g) => g.id === filter.groupID) : undefined;
  const collection = filter.kind === "collection" ? s.collections.find((c) => c.id === filter.collectionID) : undefined;
  // 列出的技能都来自同一个来源时，卡片上不再逐张写来源。
  const showSource = new Set(skills.map((x) => x.collection_id)).size > 1;
  const t = useT().list;
  return (
    <main className={centerClass}>
      {scope && <ScopeSummary scope={scope} directory={scopeDirectory} />}
      {group && <GroupHead group={group} scope={scope} />}
      {collection && <CollectionHead collection={collection} />}
      {filter.kind === "updates" && updatableCollections(s).length > 0 && <UpdatesHead />}
      {/* 网格放在单独的滚动容器里：网格高度随内容,auto-rows-fr 才等于最高那一行，而不是平分可视高度 */}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div
          className={cn(
            "a-list grid content-start",
            view === "card"
              ? "auto-rows-fr grid-cols-[repeat(auto-fill,minmax(236px,1fr))] gap-2.5 px-3.5 pt-3 pb-15"
              : "grid-cols-1 px-2.5 pt-1.5 pb-3.5",
          )}
          data-view={view}
        >
          {skills.map((skill) => (
            <SkillCard
              key={skill.id}
              skill={skill}
              scope={scope}
              view={view}
              showSource={showSource}
              selected={selected === skill.id}
              hideGroup={group?.id}
              onOpen={() => toggleDrawer(skill.id)}
            />
          ))}
          {unmanaged.map((u) => (
            <UnmanagedCard key={u.folder} skill={u} view={view} />
          ))}
          {skills.length === 0 &&
            unmanaged.length === 0 &&
            (s.skills.length === 0 ? (
              <ListEmpty>
                <b className="text-sm text-foreground">{t.emptyLibrary}</b>
                <span>{t.emptyHint}</span>
                <Button variant="primary" className="mt-1.5" onClick={() => void openFetch(s.info.home_dir)}>
                  <Icon name="plus" />
                  {t.fetchSkills}
                </Button>
              </ListEmpty>
            ) : group && !group.members?.length ? (
              <ListEmpty>
                <b className="text-sm text-foreground">{t.emptyGroup}</b>
                <span>{t.emptyGroupHint}</span>
              </ListEmpty>
            ) : (
              <ListEmpty>{t.noMatch}</ListEmpty>
            ))}
        </div>
      </div>
    </main>
  );
}

// 空状态放在列表区的视觉中部;列表是 content-start 的网格,h-full 不生效，用上边距定位。
function ListEmpty({ children }: { children: React.ReactNode }) {
  return <div className="col-span-full flex flex-col items-center justify-center gap-1.5 pt-[22vh] text-muted-foreground">{children}</div>;
}
