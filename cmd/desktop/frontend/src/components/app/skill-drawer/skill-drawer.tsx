// 技能详情抽屉(原型变体 A 的右侧抽屉):描述(译文)、备注、元数据与组成员、各作用域的安装、文件、版本历史，以及更新 / 重新安装 / 移除。
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { SourceType, type FileMetadata, type Skill, type SkillID, type SkillVersion } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { aiSettingsQuery, useSnapshot, versionFilesQuery, versionsQuery } from "@/lib/library";
import { deletedInSource, hasUpdate, isLocal, pending, renamedTo, skillScopesOf, sourceLabel, translatedDescription } from "@/lib/model";
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
  // 正在写备注的技能：换了技能就不在写了。
  const [noteEditing, setNoteEditing] = useState<SkillID | null>(null);
  const editingNote = noteEditing === id;

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
              <SheetTitle className="flex-1 text-[18px] font-bold tracking-[-0.01em]" title={skill.note || undefined}>
                {skill.name}
              </SheetTitle>
              {hasUpdate(skill) && (
                <Button variant={pending(skill) ? "primary" : "default"} onClick={() => openUpdate(skill.collection_id, { focus: id })}>
                  <Icon name="up" />
                  {d.update}
                </Button>
              )}
            </div>
            {skill.description && <Description key={id} skill={skill} />}
            <Notices skill={skill} onClose={onClose} />
            <Actions skill={skill} onAddNote={!skill.note && !editingNote ? () => setNoteEditing(id) : undefined} />

            {(skill.note || editingNote) && (
              <NoteCard key={id} skill={skill} editing={editingNote} onEditing={(on) => setNoteEditing(on ? id : null)} />
            )}

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
      <SheetDescription className="text-[12.5px] leading-[1.45] text-foreground-secondary">
        {translated && !original ? translated : skill.description}
      </SheetDescription>
      <div className="flex items-center gap-3">
        {translated && (
          <Button variant="link" className="text-[11px]" onClick={() => setOriginal(!original)}>
            {original ? d.showTranslation : d.showOriginal}
          </Button>
        )}
        <Button
          variant="link"
          className="text-[11px]"
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
function NoteCard({ skill, editing, onEditing }: { skill: Skill; editing: boolean; onEditing: (on: boolean) => void }) {
  const t = useT();
  const d = t.drawer;
  const saved = skill.note ?? "";
  const [draft, setDraft] = useState(saved);
  const [saving, setSaving] = useState(false);
  const edit = () => {
    setDraft(saved);
    onEditing(true);
  };
  const save = async () => {
    setSaving(true);
    if (await saveNote(skill.id, draft)) onEditing(false);
    setSaving(false);
  };
  return (
    <Card>
      <CardHeader
        title={d.note}
        note={
          !editing && (
            <Button variant="link" className="text-[11.5px]" onClick={edit}>
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
            <Button size="sm" disabled={saving} onClick={() => onEditing(false)}>
              {t.common.cancel}
            </Button>
            <Button size="sm" variant="primary" loading={saving} disabled={draft.trim() === saved} onClick={() => void save()}>
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

// Actions 是技能上的操作：重新读取(本地目录来源)、全部重新安装、更新历史、添加备注(还没有备注时)。
// 来源里的其他技能与新技能在集合的节标题上。
function Actions({ skill, onAddNote }: { skill: Skill; onAddNote?: () => void }) {
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
      {onAddNote && (
        <Button size="sm" onClick={onAddNote}>
          {d.addNote}
        </Button>
      )}
    </div>
  );
}
