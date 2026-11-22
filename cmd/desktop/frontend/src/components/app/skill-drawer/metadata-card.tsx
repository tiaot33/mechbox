// 技能详情里的元数据卡片。
import { Browser } from "@wailsio/runtime";
import { SourceType, type Skill, type SkillVersion } from "@bindings/internal/library";
import { formatDate } from "@/lib/content";
import { messages, useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { collectionSkills, isLocal, noSource, recordedSources, revisionSummary, revisionText, sourceKindLabel, sourceLabel, sourceWithRef } from "@/lib/source";
import { placeNames } from "@/lib/places";
import { attachSource } from "@/features/skill/attach-source";
import { twinNote, sameNameInLibrary } from "@/lib/same-name";
import { sourceChanges, sourceState, type SourceState } from "@/lib/source-state";
import { wslTitle } from "@/lib/paths";
import { addSkillToGroup, createGroup, removeSkillFromGroup } from "@/features/group/group";
import { removeTwin } from "@/features/skill/remove";
import { openDrawer } from "@/lib/view";
import { Badge } from "@/components/ui/badge";
import type { Tone } from "@/lib/level";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { NativeSelect } from "@/components/ui/native-select";
import { SourceIcon } from "@/components/app/icons";
import { Chip, Truncated, WSLBadge } from "@/components/app/parts";

// MetadataCard 是技能的元数据：来源、和同一来源其他技能的关系、来源版本、库里的同名技能、入库时间、当前版本、检查更新的结论与组。
export function MetadataCard({ skill, versions }: { skill: Skill; versions: SkillVersion[] }) {
  const s = useSnapshot();
  const t = useT();
  const d = t.drawer;
  const id = skill.id;
  const groups = s.groups.filter((g) => g.members?.includes(id));
  const notIn = s.groups.filter((g) => !g.members?.includes(id));
  const c = s.collections.find((x) => x.id === skill.collection_id);
  const collection = c ? collectionSkills(s, c) : [];
  const sameName = sameNameInLibrary(s, skill);
  const source = sourceState(skill);
  const recorded = recordedSources(s, skill);
  return (
    <Card>
      <CardHeader title={d.metadata} />
      <dl className="meta grid grid-cols-[72px_1fr] [&:lang(en)]:grid-cols-[96px_1fr] items-center gap-x-3.5 gap-y-2.5 [&_dt]:text-muted-foreground">
        <dt>{d.collection}</dt>
        {noSource(skill.source) ? (
          <dd className="text-muted-foreground" title={d.noSourceHint}>
            {t.model.noSource}
          </dd>
        ) : (
          <dd>
            <span className="inline-flex items-center gap-[5px]" title={wslTitle(isLocal(skill.source) ? skill.source.label : undefined)}>
              <SourceIcon type={skill.source.type} className="size-3 text-muted-foreground" />
              {sourceLabel(s, skill.source)}
              {isLocal(skill.source) && <WSLBadge path={skill.source.label} />}
            </span>{" "}
            <span className="text-muted-foreground">{sourceKindLabel(skill.source.type)}</span>
          </dd>
        )}
        {/* 没有来源的技能：装着的地方或显示在它下面的文件夹在 npx skills 的来源记录里记着来源时，写出来，可以接上。 */}
        {recorded.length > 0 && (
          <>
            <dt className="self-start">{d.recordedSource}</dt>
            <dd className="recorded-sources flex min-w-0 flex-col gap-1">
              {recorded.map((r) => {
                const label = sourceWithRef(r.input, r.ref);
                return (
                  <span key={label} className="recorded-source flex min-w-0 items-center gap-2">
                    <Truncated always tip={d.recordedSourceTitle(placeNames(s, r.places), label)}>
                      <span className="min-w-0 truncate">{label}</span>
                    </Truncated>
                    <Button size="sm" className="attach-source shrink-0" onClick={() => void attachSource(skill, r)}>
                      {d.attachSource}
                    </Button>
                  </span>
                );
              })}
            </dd>
          </>
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
            <dt className="self-start">{d.sameName}</dt>
            <dd className="same-name-in-library min-w-0">
              <SameNameList skill={skill} twins={sameName} />
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
        {/* 本地目录来源：目录里改过时写出来(读目录时比出来的),在上面「重新读取」。 */}
        {source.kind === "localChanged" && (
          <>
            <dt>{d.updateCheck}</dt>
            <dd>
              <Badge tone="update" title={t.list.localChangedTitle}>
                {t.list.localChanged}
              </Badge>
            </dd>
          </>
        )}
        {/* 本地目录来源不检查更新，只有"重新读取";没有来源的技能只能用目录里的修改更新。 */}
        {!source.userAuthored && (
          <>
            <dt>{d.updateCheck}</dt>
            <dd>
              {source.checked !== "never" ? (
                <>
                  <Badge tone={checkStatus(source).tone}>{checkStatus(source).label}</Badge>{" "}
                  {source.skipped && <span className="text-caption text-muted-foreground">{d.skipped}</span>}
                  {source.replacesEdits && <span className="text-caption text-muted-foreground">{d.replacesEdits}</span>}
                  <span className="text-caption text-muted-foreground">{formatDate(source.checkedAt)}</span>
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
            className="chip-add h-6 rounded-control px-2 text-caption text-muted-foreground"
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

// SameNameList 是库里同名的另外几个技能，一个一行：点名字打开它的详情;来源和装在哪是分辨它们的依据;
// 「移除」把那一个从技能库移除，确认里可以在它装着的几处装上这一个(见 removeTwin)。
function SameNameList({ skill, twins }: { skill: Skill; twins: Skill[] }) {
  const s = useSnapshot();
  const d = useT().drawer;
  return (
    <ul className="flex min-w-0 flex-col gap-1">
      {twins.map((x) => (
        <li key={x.id} className="same-name-twin flex min-w-0 items-center gap-2" data-skill-id={x.id}>
          <Button variant="ghost" size="inline" className="twin-name shrink-0 font-semibold" aria-label={d.openTwin(x.name)} onClick={() => openDrawer(x.id)}>
            {x.name}
          </Button>
          <Truncated tip={twinNote(s, x)}>
            <span className="twin-note min-w-0 grow truncate text-caption text-muted-foreground">{twinNote(s, x)}</span>
          </Truncated>
          <Button size="sm" variant="ghost" danger className="remove-twin shrink-0" title={d.removeTwinTitle(x.name)} onClick={() => void removeTwin(x.id, skill.id)}>
            {d.removeTwin}
          </Button>
        </li>
      ))}
    </ul>
  );
}

// checkStatus 是上一次检查更新的结论怎么写：可能改名、有新版本、来源里已删除、没读成来源，其余是已是最新。
function checkStatus(source: SourceState): { label: string; tone: Tone } {
  const t = messages().drawer;
  if (source.kind === "renamed") return { label: t.maybeRenamedTo(source.renamedTo), tone: "rename" };
  if (source.kind === "newVersion") return { label: t.checkAvailable, tone: "update" };
  if (source.kind === "deleted") return { label: t.checkDeleted, tone: "info" };
  if (source.checked === "failed") return { label: t.checkFailed, tone: "critical" };
  return { label: t.checkCurrent, tone: "plain" };
}

// collectionRelation 说明技能和集合里其他技能的关系：是否在同一个来源版本上，以及其他技能有没有要处理的更新。
function collectionRelation(skill: Skill, collection: Skill[]): string {
  const others = collection.filter((x) => x.id !== skill.id);
  // 「有变化」只算来源里有新内容、没跳过的，来源里已删除的不算，和集合标题、更新页一致(见 lib/source-state)。
  const todo = sourceChanges(others).changed.length;
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
