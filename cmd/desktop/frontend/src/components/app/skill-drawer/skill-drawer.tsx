// 技能详情抽屉(原型变体 A 的右侧抽屉):元数据与组成员、各作用域的安装、文件、版本历史，以及更新 / 重新安装 / 移除。
import { useQuery } from "@tanstack/react-query";
import { SourceType, type FileMetadata, type Skill, type SkillVersion } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot, versionFilesQuery, versionsQuery } from "@/lib/library";
import { deletedInSource, hasUpdate, isLocal, pending, renamedTo, skillScopesOf, sourceLabel } from "@/lib/model";
import { AuditCard } from "@/features/fetch/audit";
import { reinstall } from "@/features/install/reinstall";
import { keepDeleted, removeSkill } from "@/features/skill/remove";
import { openHistory, openUpdate, rereadSkill } from "@/features/update/update";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Icon } from "@/components/app/icons";
import { ResizeHandle, useStoredWidth } from "@/components/app/resizable";
import { FilesCard } from "./files-card";
import { InstallCard } from "./install-card";
import { MetadataCard } from "./metadata-card";
import { VersionsCard } from "./versions-card";

const noFiles: FileMetadata[] = [];
const noVersions: SkillVersion[] = [];

// 抽屉宽度：拖左边缘调整，记在这台机器上。
const drawerWidth = { storageKey: "mechbox.drawer-width", defaultWidth: 540, min: 420, max: 960 };

export function SkillDrawer({ skill, onClose }: { skill: Skill; onClose: () => void }) {
  const t = useT();
  const d = t.drawer;
  const id = skill.id;

  // 文件按版本 ID 读：浏览途中技能被更新，读到的仍是同一版本;更新带来新版本时 version_id 变，换成新版本的文件。
  const files = useQuery(versionFilesQuery(skill.version_id)).data ?? noFiles;
  // 版本历史随写操作重读。
  const versions = useQuery(versionsQuery(id)).data ?? noVersions;

  const [width, setWidth] = useStoredWidth(drawerWidth);

  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent
        className="top-0 right-2.5 bottom-2.5 max-w-[calc(100%-20px)]"
        style={{ width }}
        {...(!skill.description && { "aria-describedby": undefined })}
      >
        <ResizeHandle {...drawerWidth} edge="left" width={width} onWidth={setWidth} label={d.resizeWidth} className="left-0" />
        <SheetHeader className="h-11 pr-3 pl-3.5 text-[11px] font-semibold text-muted-foreground">
          <span className="grow">{d.title}</span>
          <SheetClose asChild>
            <Button variant="ghost" size="icon" aria-label={t.common.close}>
              <Icon name="close" />
            </Button>
          </SheetClose>
        </SheetHeader>
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="flex flex-col gap-2 px-2.5 pt-3.5 pb-4">
            <div className="flex items-center gap-2.5 px-2">
              <SheetTitle className="flex-1 text-[18px] font-bold tracking-[-0.01em]">{skill.name}</SheetTitle>
              {hasUpdate(skill) && (
                <Button variant={pending(skill) ? "primary" : "default"} onClick={() => openUpdate(skill.collection_id, { focus: id })}>
                  <Icon name="up" />
                  {d.update}
                </Button>
              )}
            </div>
            {skill.description && (
              <SheetDescription className="-mt-1 px-2 text-[12.5px] leading-[1.45] text-foreground-secondary">
                {skill.description}
              </SheetDescription>
            )}
            <Notices skill={skill} onClose={onClose} />
            <Actions skill={skill} />

            <MetadataCard skill={skill} versions={versions} />

            {skill.source.type === SourceType.SourceGitHub && <AuditCard skillID={id} />}

            <InstallCard skill={skill} />

            <FilesCard versionID={skill.version_id} files={files} />
            <VersionsCard versions={versions} currentID={skill.version_id} />

            {/* 移除放在最底下，和常用的更新、重新安装分开。 */}
            <div className="flex justify-end px-2 pt-2">
              <Button size="sm" variant="danger" onClick={async () => (await removeSkill(id)) && onClose()}>
                {d.removeSkill}
              </Button>
            </div>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

// Notices 是抽屉顶部的提示：可能改名、来源里已删除(移除 / 继续保留)。
function Notices({ skill, onClose }: { skill: Skill; onClose: () => void }) {
  const s = useSnapshot();
  const d = useT().drawer;
  const id = skill.id;
  return (
    <>
    {renamedTo(skill) && (
      <Notice tone="warn" className="mb-2.5 flex items-center gap-2">
        <span className="grow">
          {d.maybeRenamed(skill.name, sourceLabel(s, skill.source), renamedTo(skill))}
        </span>
        <Button size="sm" onClick={() => openUpdate(skill.collection_id, { focus: id })}>
          {d.view}
        </Button>
      </Notice>
    )}
    {deletedInSource(skill) &&
      (skill.kept_deleted ? (
        <Hint className="px-2">{d.keptDeleted(sourceLabel(s, skill.source))}</Hint>
      ) : (
        <Notice className="mb-2.5 flex items-center gap-2">
          <span className="grow">
            {d.deletedInSource(sourceLabel(s, skill.source), skill.name)}
          </span>
          <Button size="sm" onClick={async () => (await removeSkill(id)) && onClose()}>
            {d.remove}
          </Button>
          <Button size="sm" onClick={() => keepDeleted(id)}>
            {d.keep}
          </Button>
        </Notice>
      ))}
    </>
  );
}

// Actions 是技能上的操作：重新读取(本地目录来源)、全部重新安装、更新历史。来源里的其他技能与新技能在集合的节标题上。
function Actions({ skill }: { skill: Skill }) {
  const s = useSnapshot();
  const t = useT();
  const d = t.drawer;
  const id = skill.id;
  const installed = skillScopesOf(s, id);
  return (
    <div className="flex flex-wrap gap-1.5 px-2">
      {isLocal(skill.source) && (
        <Button size="sm" onClick={() => rereadSkill(skill)} title={d.rereadTitle}>
          <Icon name="up" />
          {d.reread}
        </Button>
      )}
      {installed.length > 0 && (
        <Button size="sm" onClick={() => reinstall({ skill_id: id }, t.actions.reinstallSkill(skill.name))}>
          <Icon name="refresh" />
          {d.reinstallAll}
        </Button>
      )}
      <Button size="sm" onClick={() => openHistory({ skill_id: id }, d.historyTitle(skill.name))} title={d.historyHint}>
        {d.history}
      </Button>
    </div>
  );
}
