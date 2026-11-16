// 技能详情里的提到关系：这个技能提到了谁、被谁提到。「提到了」的每一项写出在这个技能装着的地方找不找得到它
// (按装出去、文件夹在这台机器上的各处，一处都没有时只写库里有没有);提到它的那几行原文放在名字的悬停提示里;库里的技能点名字打开它的详情。
// 单个词的技能名照关系图的做法：打开时配了 AI 就判断还没判断的几处，判断完补上;没配时在底部提示去设置。
// 两栏都空、也没有要判断的时候不显示这张卡片。
import { useQuery } from "@tanstack/react-query";
import type { MentionLine, Skill, SkillMention } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { skillMentionsQuery, useSnapshot } from "@/lib/library";
import { folderPresent, mergeProjectPlaces, scopeLabel, skillScopesOf } from "@/lib/model";
import { toggleDrawer } from "@/lib/view";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Truncated } from "@/components/app/parts";
import { useJudgeSkillMentions } from "@/features/graph/judge";
import { JudgeLine } from "@/features/graph/judge-line";

export function MentionsCard({ skill }: { skill: Skill }) {
  const t = useT().drawer;
  const query = useQuery(skillMentionsQuery(skill.id));
  const data = query.data;
  const { judging, retry } = useJudgeSkillMentions(skill.id, data?.unjudged ?? 0);
  if (!data || (!data.mentions.length && !data.mentionedBy.length && !data.unjudged)) return null;
  return (
    <Card className="mentions-card">
      <CardHeader title={t.mentions} note={t.mentionsNote(data.mentions.length, data.mentionedBy.length)} />
      <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5">
        <Column label={t.mentionsOut} className="mentions-out">
          {data.mentions.map((m) => (
            <li key={m.name} className="flex min-w-0 items-center justify-between gap-3">
              <MentionName mention={m} />
              <Status skill={skill} mention={m} />
            </li>
          ))}
        </Column>
        <Column label={t.mentionsIn} className="mentions-in">
          {data.mentionedBy.map((m) => (
            <li key={m.skill_id} className="flex min-w-0 items-center">
              <MentionName mention={m} />
            </li>
          ))}
        </Column>
      </div>
      <JudgeLine judging={judging} unjudged={data.unjudged} retry={retry} texts={t.judge} className="mt-2" />
    </Card>
  );
}

function Column({ label, className, children }: { label: string; className: string; children: React.ReactNode[] }) {
  const t = useT().drawer;
  return (
    <>
      <span className="pt-px text-caption text-muted-foreground">{label}</span>
      {children.length ? <ul className={`${className} flex min-w-0 flex-col gap-1`}>{children}</ul> : <span className={`${className} text-caption text-muted-foreground`}>{t.mentionsNone}</span>}
    </>
  );
}

// MentionName 是一项的名字：库里的技能是能点的(打开它的详情),悬停看提到的那几行原文。
function MentionName({ mention }: { mention: SkillMention }) {
  const t = useT().drawer;
  const name = mention.skill_id ? (
    <Button variant="ghost" size="inline" className="mention-name min-w-0 truncate font-semibold" aria-label={t.openMentioned(mention.name)} onClick={() => toggleDrawer(mention.skill_id!)}>
      {mention.name}
    </Button>
  ) : (
    <span className="mention-name min-w-0 truncate font-semibold">{mention.name}</span>
  );
  return (
    <Truncated always tip={<Lines lines={mention.lines ?? []} />}>
      {name}
    </Truncated>
  );
}

function Lines({ lines }: { lines: MentionLine[] }) {
  const t = useT().drawer;
  return (
    <div className="flex max-w-96 flex-col gap-1 select-text">
      <b className="font-semibold">{t.mentionLinesTitle(lines.length)}</b>
      {lines.map((l) => (
        <div key={`${l.path}|${l.text}`}>
          <span className="opacity-70">{l.path}: </span>
          {l.text}
        </div>
      ))}
    </div>
  );
}

// Status 是「提到了」的一项在这个技能装着的地方找不找得到。只看文件夹在这台机器上的那几处(项目不在这台机器上、
// 文件夹不在了的，技能库没法核对，安装卡片里另有说明);一处都没有时只写库里有没有。
function Status({ skill, mention }: { skill: Skill; mention: SkillMention }) {
  const s = useSnapshot();
  const t = useT().drawer;
  const checked = mergeProjectPlaces(s, skillScopesOf(s, skill.id).filter(folderPresent));
  if (!checked.length) {
    return mention.skill_id ? (
      <span className="mention-status shrink-0 text-caption text-muted-foreground">{t.mentionInLibrary}</span>
    ) : (
      <span className="mention-status shrink-0 text-caption text-muted-foreground" title={t.mentionNotInLibraryTitle}>
        {t.mentionNotInLibrary}
      </span>
    );
  }
  const missing = s.missingMentions.filter((m) => m.skill_id === skill.id && m.name === mention.name);
  if (!missing.length) {
    return (
      <span className="mention-status shrink-0 text-caption text-muted-foreground" title={t.mentionAvailableTitle}>
        {t.mentionAvailable}
      </span>
    );
  }
  // 项目里共用一个目录的几个 Agent 在界面上是一处，合起来写。
  const wheres = mergeProjectPlaces(s, missing).map((m) => scopeLabel(s, m.scope));
  // 装着的地方都找不到时不再逐个写出作用域(几项都一样，太长),悬停时列出。
  const everywhere = wheres.length > 1 && wheres.length === checked.length;
  return (
    <Badge tone="info" className="mention-status min-w-0 shrink" title={t.mentionMissingTitle(wheres)}>
      <span className="truncate">{everywhere ? t.mentionMissingEverywhere : t.mentionMissing(wheres)}</span>
    </Badge>
  );
}
