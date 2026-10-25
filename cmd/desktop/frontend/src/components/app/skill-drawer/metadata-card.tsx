// 技能详情里的元数据卡片。
import { Browser } from "@wailsio/runtime";
import { SourceType, UpdateCheckStatus, type Skill, type SkillVersion } from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import { messages, useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import {
  collectionSkills,
  installedWhere,
  isLocal,
  noSource,
  pending,
  revisionSummary,
  revisionText,
  sameNameInLibrary,
  sourceKindLabel,
  sourceLabel,
  updateAvailable,
} from "@/lib/model";
import { useSortedGroups } from "@/lib/view";
import { addSkillToGroup, createGroup, removeSkillFromGroup } from "@/features/group/group";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { NativeSelect } from "@/components/ui/native-select";
import { SourceIcon } from "@/components/app/icons";
import { Chip } from "@/components/app/parts";

// MetadataCard 是技能的元数据：来源、和同一来源其他技能的关系、来源版本、库里的同名技能、入库时间、当前版本、检查更新的结论与组。
export function MetadataCard({ skill, versions }: { skill: Skill; versions: SkillVersion[] }) {
  const s = useSnapshot();
  const t = useT();
  const d = t.drawer;
  const id = skill.id;
  const sorted = useSortedGroups(s);
  const groups = sorted.filter((g) => g.members?.includes(id));
  const notIn = sorted.filter((g) => !g.members?.includes(id));
  const check = skill.last_update_check;
  const c = s.collections.find((x) => x.id === skill.collection_id);
  const collection = c ? collectionSkills(s, c) : [];
  const sameName = sameNameInLibrary(s, skill);
  return (
    <Card>
      <CardHeader title={d.metadata} />
      <dl className="meta grid grid-cols-[72px_1fr] [&:lang(en)]:grid-cols-[96px_1fr] items-center gap-x-3.5 gap-y-2.5 [&_dt]:font-medium [&_dt]:text-muted-foreground">
        <dt>{d.collection}</dt>
        {noSource(skill.source) ? (
          <dd className="text-muted-foreground" title={d.noSourceHint}>
            {t.model.noSource}
          </dd>
        ) : (
          <dd>
            <span className="inline-flex items-center gap-[5px]">
              <SourceIcon type={skill.source.type} className="size-3 text-muted-foreground" />
              {sourceLabel(s, skill.source)}
            </span>{" "}
            <span className="text-muted-foreground">{sourceKindLabel(skill.source.type)}</span>
          </dd>
        )}
        {collection.length > 1 && (
          <>
            <dt>{d.sameSource}</dt>
            <dd>{collectionRelation(skill, collection)}</dd>
          </>
        )}
        {!noSource(skill.source) && (
          <>
            <dt>{d.revision}</dt>
            <dd>
              <span className="font-mono">{sourceRef(skill)}</span>
              {skill.source.view_url && (
                <>
                  {" "}
                  <Button variant="link" onClick={() => void Browser.OpenURL(skill.source.view_url!)}>
                    {viewLabel(skill.source.type)}
                  </Button>
                </>
              )}
            </dd>
          </>
        )}
        {sameName.length > 0 && (
          <>
            <dt>{d.sameName}</dt>
            <dd className="same-name-in-library">
              {d.sameNameList(
                sameName.length,
                skill.name,
                sameName.map((x) => t.actions.twin(noSource(x.source) ? t.model.noSource : sourceLabel(s, x.source), installedWhere(s, x.id))),
              )}
            </dd>
          </>
        )}
        <dt>{d.fetchedAt}</dt>
        <dd>{formatDate(skill.fetched_at)}</dd>
        <dt>{d.currentVersion}</dt>
        <dd>
          {d.versionCount(formatDate(versions.find((v) => v.id === skill.version_id)?.created_at), versions.length)}
          {skill.edited && (
            <span className="has-edits text-muted-foreground" title={d.hasEditsTitle}>
              {d.hasEdits}
            </span>
          )}
        </dd>
        {/* 本地目录来源不检查更新，只有"重新读取";没有来源的技能只能用目录里的修改更新。 */}
        {!isLocal(skill.source) && !noSource(skill.source) && (
          <>
            <dt>{d.updateCheck}</dt>
            <dd>
              {check ? (
                <>
                  <Badge tone={checkStatus(check.status).tone}>
                    {check.renamed_to ? d.maybeRenamedTo(check.renamed_to) : checkStatus(check.status).label}
                  </Badge>{" "}
                  {check.skipped && <span className="text-[11px] text-muted-foreground">{d.skipped}</span>}
                  {updateAvailable(skill) && skill.edited && <span className="text-[11px] text-muted-foreground">{d.replacesEdits}</span>}
                  <span className="text-[11px] text-muted-foreground">{formatDate(check.checked_at)}</span>
                </>
              ) : (
                <span className="text-muted-foreground">{d.neverChecked}</span>
              )}
            </dd>
          </>
        )}
        <dt>{d.groups}</dt>
        <dd className="flex flex-wrap items-center gap-1.5">
          {groups.map((g) => (
            <Chip key={g.id} removeTitle={d.removeFromGroup} onRemove={() => removeSkillFromGroup(g.id, id)}>
              {g.name}
            </Chip>
          ))}
          <NativeSelect
            className="chip-add h-6 rounded-full px-2 text-[11.5px] text-muted-foreground"
            value=""
            onChange={(e) => {
              const v = e.target.value;
              if (v === "new") void createGroup([id]);
              else if (v) void addSkillToGroup(Number(v), id);
            }}
          >
            <option value="">{d.addToGroup}</option>
            {notIn.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
            <option value="new">{d.newGroup}</option>
          </NativeSelect>
        </dd>
      </dl>
    </Card>
  );
}

function checkStatus(status: UpdateCheckStatus): { label: string; tone: Tone } {
  const t = messages().drawer;
  const known: Record<UpdateCheckStatus, { label: string; tone: Tone }> = {
    [UpdateCheckStatus.UpdateAvailable]: { label: t.checkAvailable, tone: "warn" },
    [UpdateCheckStatus.UpdateCurrent]: { label: t.checkCurrent, tone: "ok" },
    [UpdateCheckStatus.UpdateDeleted]: { label: t.checkDeleted, tone: "neutral" },
    [UpdateCheckStatus.UpdateFailed]: { label: t.checkFailed, tone: "bad" },
    [UpdateCheckStatus.$zero]: { label: t.checkCurrent, tone: "ok" },
  };
  return known[status];
}

// collectionRelation 说明技能和集合里其他技能的关系：是否在同一个来源版本上，以及其他技能有没有要处理的更新。
function collectionRelation(skill: Skill, collection: Skill[]): string {
  const others = collection.filter((x) => x.id !== skill.id);
  const todo = others.filter(pending).length;
  const summary = revisionSummary(collection);
  const t = messages().drawer;
  return [
    !summary || summary.kind === "same" ? t.sameRevisionAsOthers(others.length) : revisionText(summary),
    todo ? t.changedCount(todo) : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

// sourceRef 是详情里的来源版本:Git 类来源写分支与 commit,来源版本未知(入库时和来源里的不一样)时如实写出。
function sourceRef(skill: Skill): string {
  const source = skill.source;
  switch (source.type) {
    case SourceType.SourceGitHub:
    case SourceType.SourceGit:
      return [source.ref || messages().drawer.defaultBranch, skill.revision ? skill.revision.slice(0, 12) : messages().model.revisionUnknown].join(" · ");
    case SourceType.SourceURL:
      try {
        return new URL(source.label).host;
      } catch {
        return source.label || "—";
      }
    case SourceType.SourceWellKnown:
      return source.label.split("/")[0];
    default:
      return messages().drawer.localDirectory;
  }
}

// viewLabel 是"在来源上查看"按钮的文字：只有 GitHub、GitLab 仓库与 well-known 站点有网页。
function viewLabel(type: SourceType): string {
  const t = messages().drawer;
  const known: Partial<Record<SourceType, string>> = {
    [SourceType.SourceGitHub]: t.viewOnGitHub,
    [SourceType.SourceGit]: t.viewOnGitLab,
    [SourceType.SourceWellKnown]: t.openSite,
  };
  return known[type] ?? t.openPage;
}
