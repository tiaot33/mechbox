// 选中作用域时列出的未纳管技能(不是库装的):可以入库，或在 Finder 中显示。
import { AppService } from "@bindings/cmd/desktop";
import type { UnmanagedSkill } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { foundSource, useFoundSources, useSnapshot } from "@/lib/library";
import { sameNameNote, scopeKey, shortPath } from "@/lib/model";
import { useUI } from "@/lib/ui";
import { cn } from "@/lib/utils";
import type { ListView } from "@/lib/view";
import { fetchUnmanaged } from "@/features/fetch/fetch-unmanaged";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { SameNameBadge, SameNameLine } from "./same-name";

// UnmanagedCard 是作用域里一个不是库装的技能文件夹(未纳管技能):虚线边框、标"未纳管",
// 提交在项目仓库里的再标"在项目仓库里"，符号链接写出指向哪里。不能拖动、不打开详情，只提供在 Finder 中显示与入库。
export function UnmanagedCard({ skill, view }: { skill: UnmanagedSkill; view: ListView }) {
  const s = useSnapshot();
  const ui = useUI();
  const t = useT().list;
  const list = view === "list";
  const same = sameNameNote(s, scopeKey(skill.scope), skill.folder);
  // 来源记录认不出来源的，在 skills.sh 上找到来源后补上。
  const found = foundSource(useFoundSources(), skill);
  const source = skill.source_input || found;
  return (
    <article
      className={cn(
        // 不挂 a-card:e2e 与拖放按 a-card 找库里的技能。
        "unmanaged-card flex gap-2 border border-dashed",
        list
          ? "min-h-10 flex-row items-center gap-5 rounded-[7px] border-transparent px-3 shadow-[inset_0_-1px_0_rgba(17,24,39,0.06)] hover:border-border-strong"
          : "min-h-28 flex-col rounded-[10px] border-border-strong bg-white/30 px-3 pt-3 pb-2.5",
      )}
      data-name={skill.name}
    >
      {/* 卡片上标签多时折到下一行，名字不被挤掉。 */}
      <div className={cn("flex min-w-0 items-center gap-1.5", list ? "w-[260px] shrink-0" : "flex-wrap gap-y-1")}>
        <span className={cn("unmanaged-name min-w-0 truncate font-semibold text-foreground-secondary", list ? "text-[13px]" : "text-[12.5px]")}>
          {skill.name}
        </span>
        <Badge title={t.unmanagedTitle}>{t.unmanaged}</Badge>
        {skill.in_repository && <Badge title={t.inRepositoryTitle}>{t.inRepository}</Badge>}
        {!skill.in_repository && skill.in_project_lock && <Badge title={t.inLockTitle}>{t.inLock}</Badge>}
        {skill.link_outside && <Badge title={t.linkOutsideTitle(skill.link_target ?? "")}>{t.linkOutside}</Badge>}
        {same && <SameNameBadge note={same} />}
      </div>
      <p
        className={cn(
          list
            ? "min-w-0 flex-1 truncate text-[11.5px] text-muted-foreground"
            : "line-clamp-2 min-h-[2.9em] text-[11px] leading-[1.45] text-muted-foreground",
        )}
      >
        {skill.description}
      </p>
      {same && <SameNameLine note={same} list={list} />}
      {/* 卡片上来源单独一行、长的折行，和底部的按钮抢不到位置;列表视图只有一行，放不下时截断、悬停看全。 */}
      {source && (
        <p
          className={cn(
            "unmanaged-source-label min-w-0 text-[10.5px] leading-[1.4] text-muted-foreground",
            list ? "w-[220px] shrink-0 truncate" : "-mt-1 break-all",
          )}
          title={found ? t.foundTitle(found) : t.recordedTitle(source)}
        >
          {t.from(source)}
        </p>
      )}
      <div
        className={cn(
          "a-card-foot flex min-h-[17px] items-center gap-[5px] [&:lang(en)]:gap-2 text-[10px] text-muted-foreground",
          list ? "w-[220px] shrink-0" : "mt-auto",
        )}
      >
        <span
          className={cn("unmanaged-status min-w-0 grow", skill.link_target ? "truncate" : "whitespace-nowrap")}
          title={skill.link_target ? t.linkTitle(skill.link_target) : skill.folder}
        >
          {skill.link_target ? t.link(shortPath(s, skill.link_target)) : t.notByLibrary}
        </span>
        <Button
          variant="link"
          className="reveal-in-finder shrink-0 font-sans text-[10.5px]"
          onClick={() => void ui.run(() => AppService.RevealInFinder(skill.folder), { reload: false })}
        >
          {t.revealInFinder}
        </Button>
        <Button
          variant="link"
          className="fetch-unmanaged shrink-0 font-sans text-[10.5px]"
          title={t.fetchTitle}
          onClick={() => void fetchUnmanaged(skill)}
        >
          {t.fetch}
        </Button>
      </div>
    </article>
  );
}
