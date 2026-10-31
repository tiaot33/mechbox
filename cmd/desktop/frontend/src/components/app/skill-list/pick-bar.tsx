// 勾选了技能以后，列表底部的操作栏：一起安装到某处、从这里卸载(作用域页、项目页)、加入组、移出组、从库里移除;
// 各操作的弹窗与写操作在 features/ 里。按 Esc 或点"取消选择"清空勾选;换页时也清空。
import { useEffect } from "react";
import type { Scope } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { parseScope, placeOf, projectAgents, projectName, scopeKey, scopeLabel, skillScopeIn, type PageListing } from "@/lib/model";
import { clearPicked, useView } from "@/lib/view";
import { pickGroup } from "@/features/group/group";
import { uninstallSkills } from "@/features/install/install";
import { installPicked } from "@/features/install/install-to";
import { removeSkills } from "@/features/skill/remove";
import { Button } from "@/components/ui/button";
import { Icon } from "@/components/app/icons";

export function PickBar({ listing }: { listing: PageListing }) {
  const s = useSnapshot();
  const picked = useView((v) => v.picked);
  const t = useT().list;
  const ids = [...picked];

  // Esc 清空勾选;弹窗、详情抽屉开着时 Esc 先关它们，输入框里的 Esc 留给输入框。
  useEffect(() => {
    if (!picked.size) return;
    const onKey = (e: KeyboardEvent) => {
      // 弹窗、抽屉在捕获阶段就处理了 Esc(preventDefault),等到这里时它们可能已经不在页面上了。
      if (e.key !== "Escape" || e.defaultPrevented || document.querySelector('[role="dialog"]')) return;
      if (e.target instanceof HTMLInputElement && e.target.type !== "checkbox") return;
      if (e.target instanceof HTMLTextAreaElement) return;
      clearPicked();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [picked.size]);

  if (!picked.size || listing.kind === "updates") return null;

  // 从这里卸载：作用域页是这个作用域，项目页是项目里装了技能的各个 Agent。
  const here: { scopes: Scope[]; where: string } | undefined =
    listing.kind === "scope"
      ? { scopes: [parseScope(listing.scope)], where: scopeLabel(s, parseScope(listing.scope)) }
      : listing.kind === "project"
        ? { scopes: projectAgents(s, listing.project, "").map((a) => parseScope(a.scope)), where: projectName(s, listing.project) }
        : undefined;
  // 安装到…默认选这一页的地方：作用域页是这个作用域，项目页是这个项目。
  const initial =
    listing.kind === "scope"
      ? { place: placeOf(parseScope(listing.scope)), agent: parseScope(listing.scope).agent }
      : listing.kind === "project"
        ? { place: listing.project }
        : {};
  const canUninstall = !!here?.scopes.some((sc) => ids.some((id) => skillScopeIn(s, id, scopeKey(sc))?.direct));
  const inGroups = s.groups.some((g) => ids.some((id) => g.members?.includes(id)));

  return (
    <div className="pointer-events-none absolute inset-x-0 bottom-4 z-[2] flex justify-center px-4">
      <div className="pick-bar pointer-events-auto flex max-w-full min-w-0 flex-wrap items-center gap-1 rounded-xl border border-hairline bg-white/95 px-2 py-1.5 shadow-[0_8px_28px_rgba(17,24,39,0.16)] backdrop-blur-[12px]">
        <span className="px-1.5 text-[12px] font-semibold whitespace-nowrap">{t.pickedCount(picked.size)}</span>
        <Button size="sm" variant="ghost" onClick={() => void installPicked(ids, initial)}>
          <Icon name="plus" />
          {t.pickInstallTo}
        </Button>
        {canUninstall && (
          <Button size="sm" variant="ghost" onClick={() => void uninstallSkills(ids, here!.scopes, here!.where)}>
            {listing.kind === "project" ? t.pickUninstallProject : t.pickUninstallHere}
          </Button>
        )}
        <Button size="sm" variant="ghost" onClick={() => void pickGroup(ids, "add")}>
          <Icon name="layers" />
          {t.pickAddToGroup}
        </Button>
        {inGroups && (
          <Button size="sm" variant="ghost" onClick={() => void pickGroup(ids, "remove")}>
            {t.pickRemoveFromGroup}
          </Button>
        )}
        <Button size="sm" variant="ghost" className="text-bad-strong" onClick={() => void removeSkills(ids)}>
          {t.pickRemove}
        </Button>
        <Button size="icon-sm" variant="ghost" className="ml-0.5" title={t.pickClear} aria-label={t.pickClear} onClick={clearPicked}>
          <Icon name="close" />
        </Button>
      </div>
    </div>
  );
}
