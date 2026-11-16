// 技能详情抽屉(原型变体 A 的右侧抽屉):描述(译文)、备注、元数据与组成员、各作用域的安装、文件、版本历史，以及更新 / 重新安装 / 移除。
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { SourceType, type FileMetadata, type Skill, type SkillID, type SkillVersion } from "@bindings/internal/library";
import { endEditing, startEditing, type Editing } from "@/lib/editing";
import { useT } from "@/lib/i18n";
import { aiSettingsQuery, useSnapshot, versionFilesQuery, versionsQuery } from "@/lib/library";
import { deletedInSource, hasUpdate, isLocal, renamedTo, skillScopesOf, sourceLabel, translatedDescription } from "@/lib/model";
import { AuditCard } from "@/features/fetch/audit";
import { reinstall } from "@/features/install/reinstall";
import { saveNote } from "@/features/skill/note";
import { keepDeleted, removeSkill } from "@/features/skill/remove";
import { translateDescription } from "@/features/skill/translate";
import { openHistory, openUpdate, rereadSkill } from "@/features/update/update";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Hint } from "@/components/ui/field";
import { Notice } from "@/components/ui/notice";
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Textarea } from "@/components/ui/textarea";
import { Icon } from "@/components/ui/icon";
import { ResizeHandle, useStoredWidth, useWidth } from "@/components/app/resizable";
import { FilesCard } from "./files-card";
import { InstallCard } from "./install-card";
import { MetadataCard } from "./metadata-card";
import { VersionsCard } from "./versions-card";

const noFiles: FileMetadata[] = [];
const noVersions: SkillVersion[] = [];

// 抽屉宽度：拖左边缘调整，记在这台机器上。
const drawerWidth = { storageKey: "mechbox.drawer-width", defaultWidth: 540, min: 420, max: 960 };

// 抽屉浮在列表和作用域栏上面时，列表左边至少还露出这么宽(行首的勾选框和技能名)。
const peek = 220;

// DrawerDock 是列表、技能详情抽屉与作用域栏(aside)这一段：抽屉从这一段的最右侧滑出，浮在列表和作用域栏上面，不挤占它们的位置;
// 最宽到这一段的宽度减去 peek,记下的宽度超出时按能给的显示。
export function DrawerDock({ skill, onClose, aside, children }: { skill: Skill | undefined; onClose: () => void; aside: React.ReactNode; children: React.ReactNode }) {
  const [dock, setDock] = useState<HTMLDivElement | null>(null);
  const total = useWidth(dock);
  return (
    <div ref={setDock} className="relative flex min-w-0 flex-1 gap-2">
      {children}
      <SkillDrawer skill={skill} max={Math.max(drawerWidth.min, total - peek)} onClose={onClose} />
      {aside && <div className="flex shrink-0">{aside}</div>}
    </div>
  );
}

// SkillDrawer 是技能详情抽屉:skill 为空时关上。关上时先播完退场动画再卸载，这期间仍画最后显示的那个技能。
function SkillDrawer({ skill, max, onClose }: { skill: Skill | undefined; max: number; onClose: () => void }) {
  const [shown, setShown] = useState(skill);
  if (skill && skill !== shown) setShown(skill);
  if (!shown) return null;
  return <DrawerSheet skill={shown} max={max} open={!!skill} onClose={onClose} onExited={() => setShown(undefined)} />;
}

function DrawerSheet({
  skill,
  max,
  open,
  onClose,
  onExited,
}: {
  skill: Skill;
  max: number;
  open: boolean;
  onClose: () => void;
  onExited: () => void;
}) {
  const t = useT();
  const d = t.drawer;
  const id = skill.id;

  // 文件按版本 ID 读：浏览途中技能被更新，读到的仍是同一版本;更新带来新版本时 version_id 变，换成新版本的文件。
  const files = useQuery(versionFilesQuery(skill.version_id)).data ?? noFiles;
  // 版本历史随写操作重读。
  const versions = useQuery(versionsQuery(id)).data ?? noVersions;

  const [stored, setWidth] = useStoredWidth(drawerWidth);
  const width = Math.min(stored, max);
  // 正在写的备注：换了技能就结束，切回来要重新点编辑;之前那一次的保存晚到时只结束它自己(见 lib/editing.ts)。
  const [noteEditing, setNoteEditing] = useState<Editing<SkillID> | null>(null);
  const [noteSkill, setNoteSkill] = useState(id);
  if (noteSkill !== id) {
    setNoteSkill(id);
    setNoteEditing(null);
  }
  const editingNote = noteEditing?.id === id ? noteEditing : null;

  return (
    <Sheet open={open} onOpenChange={(next) => !next && onClose()}>
      <SheetContent
        className="top-0 bottom-0 max-w-full"
        style={{ width, right: 0 }}
        onAnimationEnd={(e) => !open && e.target === e.currentTarget && onExited()}
        {...(!skill.description && { "aria-describedby": undefined })}
      >
        <ResizeHandle {...drawerWidth} max={max} edge="left" width={width} onWidth={setWidth} label={d.resizeWidth} className="left-0" />
        <SheetHeader className="h-11 pr-3 pl-[18px] text-caption font-semibold text-muted-foreground">
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
              <SheetTitle className="flex-1 text-title" title={skill.note || undefined}>
                {skill.name}
              </SheetTitle>
              {hasUpdate(skill) && (
                // 「更新」到处是同一个样子：绿色的胶囊。
                <Button variant="pill" onClick={() => openUpdate(skill.collection_id, { focus: id })}>
                  {d.update}
                </Button>
              )}
            </div>
            {skill.description && <Description key={`description-${id}`} skill={skill} />}
            <Notices skill={skill} onClose={onClose} />
            <Actions skill={skill} onAddNote={!skill.note && !editingNote ? () => setNoteEditing(startEditing(id)) : undefined} />

            {(skill.note || editingNote) && (
              <NoteCard
                key={`note-${id}`}
                skill={skill}
                editing={editingNote}
                onEdit={() => setNoteEditing(startEditing(id))}
                onDone={(session) => setNoteEditing((cur) => endEditing(cur, session))}
              />
            )}

            <MetadataCard skill={skill} versions={versions} />

            {skill.source.type === SourceType.SourceGitHub && <AuditCard skillID={id} />}

            <InstallCard skill={skill} />

            {/* 换技能或技能换了版本时重新挂载：选中的文件、显示原文还是译文都回到默认。 */}
            <FilesCard key={`files-${skill.version_id}`} versionID={skill.version_id} files={files} skillName={skill.name} />
            <VersionsCard key={`versions-${id}`} name={skill.name} versions={versions} currentID={skill.version_id} />

            {/* 移除放在最底下，和常用的更新、重新安装分开。 */}
            <div className="flex justify-end px-2 pt-2">
              <Button variant="ghost" onClick={async () => (await removeSkill(id)) && onClose()}>
                {d.removeSkill}
              </Button>
            </div>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

// Description 是技能的描述：有界面语言的译文时显示译文，可以切到原文;「翻译」每次都用 AI 重新翻译，译文存进数据库。
function Description({ skill }: { skill: Skill }) {
  const d = useT().drawer;
  const ai = !!useQuery(aiSettingsQuery).data?.model;
  const translated = translatedDescription(skill);
  const [original, setOriginal] = useState(false);
  const [translating, setTranslating] = useState(false);
  const translate = async () => {
    setTranslating(true);
    if (await translateDescription(skill)) setOriginal(false);
    setTranslating(false);
  };
  return (
    <div className="-mt-1 flex flex-col items-start gap-1 px-2">
      <SheetDescription className="text-body-long text-foreground-secondary">
        {translated && !original ? translated : skill.description}
      </SheetDescription>
      <div className="flex items-center gap-3">
        {translated && (
          <Button variant="ghost" size="inline" className="text-caption" onClick={() => setOriginal(!original)}>
            {original ? d.showTranslation : d.showOriginal}
          </Button>
        )}
        <Button
          variant="ghost" size="inline"
          className="text-caption"
          disabled={!ai}
          loading={translating}
          title={ai ? d.translateTitle : d.translateNeedsAI}
          onClick={() => void translate()}
        >
          {translated ? d.retranslate : d.translate}
        </Button>
      </div>
    </div>
  );
}

// NoteCard 是技能的备注：平常显示备注，点编辑(或操作里的「添加备注」)换成输入框;清空保存即删掉备注。
// editing 是正开着的这一次编辑;保存成功、取消时只结束这一次(onDone)。
function NoteCard({
  skill,
  editing,
  onEdit,
  onDone,
}: {
  skill: Skill;
  editing: Editing<SkillID> | null;
  onEdit: () => void;
  onDone: (session: Editing<SkillID>) => void;
}) {
  const t = useT();
  const d = t.drawer;
  const saved = skill.note ?? "";
  const [draft, setDraft] = useState(saved);
  const [saving, setSaving] = useState(false);
  const edit = () => {
    setDraft(saved);
    onEdit();
  };
  const save = async (session: Editing<SkillID>) => {
    setSaving(true);
    if (await saveNote(skill.id, draft)) onDone(session);
    setSaving(false);
  };
  return (
    <Card>
      <CardHeader
        title={d.note}
        note={
          !editing && (
            <Button variant="ghost" size="inline" className="text-caption" onClick={edit}>
              {d.editNote}
            </Button>
          )
        }
      />
      {editing ? (
        <div className="flex flex-col gap-2">
          <Textarea
            rows={3}
            autoFocus
            value={draft}
            placeholder={d.notePlaceholder}
            disabled={saving}
            onChange={(e) => setDraft(e.target.value)}
          />
          <div className="flex items-center gap-2">
            <Hint className="grow">{d.noteHint}</Hint>
            <Button variant="ghost" disabled={saving} onClick={() => onDone(editing)}>
              {t.common.cancel}
            </Button>
            <Button loading={saving} disabled={draft.trim() === saved} onClick={() => void save(editing)}>
              {d.saveNote}
            </Button>
          </div>
        </div>
      ) : (
        <p className="break-words whitespace-pre-wrap select-text">{saved}</p>
      )}
    </Card>
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
      <Notice className="mx-2 mb-2.5" action={<Button onClick={() => openUpdate(skill.collection_id, { focus: id })}>{d.view}</Button>}>
        {d.maybeRenamed(skill.name, sourceLabel(s, skill.source), renamedTo(skill))}
      </Notice>
    )}
    {deletedInSource(skill) &&
      (skill.kept_deleted ? (
        <Hint className="px-2">{d.keptDeleted(sourceLabel(s, skill.source))}</Hint>
      ) : (
        <Notice
          className="mx-2 mb-2.5"
          action={
            <>
              <Button onClick={async () => (await removeSkill(id)) && onClose()}>{d.remove}</Button>
              <Button onClick={() => keepDeleted(id)}>{d.keep}</Button>
            </>
          }
        >
          {d.deletedInSource(sourceLabel(s, skill.source), skill.name)}
        </Notice>
      ))}
    </>
  );
}

// Actions 是技能上的操作：重新读取(本地目录来源)、全部重新安装、更新历史、添加备注(还没有备注时)。
// 会改动磁盘的两个是普通按钮，只是看看或记一笔的两个是轻按钮，一排里分得出主次。来源里的其他技能与新技能在集合的节标题上。
function Actions({ skill, onAddNote }: { skill: Skill; onAddNote?: () => void }) {
  const s = useSnapshot();
  const t = useT();
  const d = t.drawer;
  const id = skill.id;
  const installed = skillScopesOf(s, id);
  return (
    <div className="flex flex-wrap items-center gap-1.5 px-2">
      {isLocal(skill.source) && (
        <Button onClick={() => rereadSkill(skill)} title={d.rereadTitle}>
          <Icon name="up" />
          {d.reread}
        </Button>
      )}
      {installed.length > 0 && (
        <Button onClick={() => reinstall({ skill_id: id }, t.actions.reinstallSkill(skill.name))}>
          <Icon name="refresh" />
          {d.reinstallAll}
        </Button>
      )}
      <Button variant="ghost" onClick={() => openHistory({ skill_id: id }, d.historyTitle(skill.name))} title={d.historyHint}>
        {d.history}
      </Button>
      {onAddNote && (
        <Button variant="ghost" onClick={onAddNote}>
          {d.addNote}
        </Button>
      )}
    </div>
  );
}
