// 逐个入库一个未纳管技能：确认认出的来源(可改链接或清空),入库不动磁盘上的文件夹。
import { useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { FetchUnmanagedResult, UnmanagedSkill } from "@bindings/internal/library";
import { unmanagedSourceError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, foundSource, useFoundSources, type LibraryError } from "@/lib/library";
import { installedWhere, noSource, outsideLinkNote, sameNameFetchNote, scopeKey, scopeLabel, shortPath, sourceLabel } from "@/lib/model";
import { Modal, ui, useUI } from "@/lib/ui";
import { flashSkill } from "@/lib/view";
import { chooseAndInstall } from "@/features/install/install-to";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Notice } from "@/components/ui/notice";

// fetchUnmanaged 把作用域里一个不是库装的技能(未纳管技能)入库:磁盘上什么都不改，文件夹从此由 app 管。
// 完成后提示它已放进技能库、文件夹原样没动，带"装到别处…";列表里它那一行换成库里技能的一行，滚到看得见的地方闪一下。
// 确认时写清代价：之后还用命令行工具更新它，app 只能报为被手动改过;提交在项目仓库里的，之后在 app 里更新会改动仓库里的文件。
// 来源链接预填 npx skills 来源记录里认出的来源，认不出时预填在 skills.sh 上找到的来源，用户可以改或清空;清空就是没有来源的技能。
// 内容和库里某个技能的某个版本一致时(skill.skill_id),它就是那个技能：不问来源，确认里说清只补记这一处安装、不会多出一个技能。
export async function fetchUnmanaged(skill: UnmanagedSkill) {
  const s = currentSnapshot();
  const t = messages().actions;
  const place = scopeLabel(s, skill.scope);
  const library = s.skills.find((x) => x.id === skill.skill_id);
  const from = library && (noSource(library.source) ? messages().model.noSource : sourceLabel(s, library.source));
  // 和库里的旧版本一样时说清入库后是旧版本，之后更新、重新安装会换成当前版本。
  const same = library
    ? skill.version_id === library.version_id
      ? t.fetchIdentical(library.name, from!, place)
      : t.fetchIdenticalOld(library.name, from!, place)
    : undefined;
  // 库里已有同名的技能时确认里说出来：内容不同不合并，入库后会有两个同名的。
  const twins = library
    ? []
    : s.skills
        .filter((x) => x.name === skill.name)
        .map((x) => t.twin(noSource(x.source) ? messages().model.noSource : sourceLabel(s, x.source), installedWhere(s, x.id)));
  const outside = skill.link_outside && skill.link_target ? outsideLinkNote(shortPath(s, skill.link_target)) : undefined;
  const r = await ui.open<FetchUnmanagedResult>((close) => (
    <FetchUnmanagedDialog place={place} skill={skill} twins={twins} same={same} outside={outside} close={close} />
  ));
  if (!r) return;
  // 说清它已在技能库里、文件夹没动，给下一步"装到别处…";它那一行换成库里技能的一行，闪一下。
  const message = r.matched
    ? t.matchedVersion(skill.name, r.skill.name, place)
    : r.skill.collection_id
      ? t.fetchedTracking(r.skill.name, r.skill.source.label, place)
      : t.fetched(r.skill.name, place);
  ui.toast(message, "ok", { label: t.installElsewhere, onClick: () => void chooseAndInstall([r.skill.id]) });
  flashSkill(r.skill.id, scopeKey(skill.scope));
}

// FetchUnmanagedDialog 是入库确认：来源链接可改可清空;读来源失败时留在框里说明原因，用户可以重试、改链接或清空后入库。
function FetchUnmanagedDialog({
  place,
  skill,
  twins,
  same,
  outside,
  close,
}: {
  place: string;
  skill: UnmanagedSkill;
  twins: string[];
  same?: string; // 和库里某个技能一模一样时的说明：不问来源

  outside?: string; // 指向 Agent 目录以外的符号链接时的说明
  close: (value?: FetchUnmanagedResult) => void;
}) {
  const ui = useUI();
  const t = useT();
  // 确认框开着时才在 skills.sh 上找到来源的，用户还没动过链接就补上。
  const found = foundSource(useFoundSources(), skill);
  const [edited, setLink] = useState<string>();
  // 和库里某个技能一样的不问来源，也不带来源：入库只补记安装。
  const link = same ? "" : (edited ?? skill.source_input ?? found ?? "");
  const [error, setError] = useState<LibraryError>();
  const [busy, setBusy] = useState(false);
  const input = link.trim();
  // 来源记录里的分支只跟着它认出的那个来源走;用户改了链接就不再带上。
  const ref = input && input === skill.source_input ? skill.source_ref : undefined;
  const submit = async () => {
    if (busy) return;
    setBusy(true);
    setError(undefined);
    const r = await ui.run(
      () => LibraryService.FetchUnmanaged({ scope: skill.scope, name: skill.name, source_input: input || undefined, source_ref: ref }),
      { onError: (err) => (setError(err), true) },
    );
    setBusy(false);
    if (r) close(r);
  };
  return (
    <Modal
      title={t.actions.fetchTitle(skill.name)}
      onClose={() => close()}
      footer={
        <>
          <Button onClick={() => close()}>{t.common.cancel}</Button>
          <Button variant="primary" onClick={() => void submit()} disabled={busy} autoFocus>
            {t.actions.fetch}
          </Button>
        </>
      }
    >
      <Hint>{t.actions.fetchHandOver(place)}</Hint>
      {same && <Hint className="unmanaged-same-as">{same}</Hint>}
      <Hint>{t.actions.fetchCliCost}</Hint>
      {skill.in_repository && <Notice tone="warn">{t.actions.fetchInRepository}</Notice>}
      {outside && (
        <Notice tone="warn" className="unmanaged-link-outside">
          {outside}
        </Notice>
      )}
      {twins.length > 0 && (
        <Notice tone="warn" className="unmanaged-same-name">
          {t.actions.sameNameTwins(skill.name, twins, sameNameFetchNote(skill.name, !!input))}
        </Notice>
      )}
      {!same && (
        <>
          <Field label={t.actions.sourceLink}>
            <Input
              className="unmanaged-source font-mono"
              value={link}
              placeholder={t.actions.sourceLinkPlaceholder}
              onChange={(e) => setLink(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && void submit()}
            />
          </Field>
          <Hint className="unmanaged-source-note">
            {input ? t.actions.sourceNote(input === found, `${input}${ref ? ` · ${ref}` : ""}`) : t.actions.noSourceNote}
          </Hint>
        </>
      )}
      {error && (
        <Notice tone="bad" className="unmanaged-error whitespace-pre-line">
          {unmanagedSourceError(error, skill.name)}
        </Notice>
      )}
    </Modal>
  );
}
