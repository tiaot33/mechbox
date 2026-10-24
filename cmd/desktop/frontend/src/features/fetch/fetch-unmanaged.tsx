// 逐个入库一个未纳管技能：确认认出的来源(可改链接或清空),入库不动磁盘上的文件夹。
import { useState } from "react";
import { LibraryService } from "@bindings/cmd/desktop";
import type { FetchUnmanagedResult, UnmanagedSkill } from "@bindings/internal/library";
import { unmanagedSourceError } from "@/lib/errors";
import { messages, useT } from "@/lib/i18n";
import { currentSnapshot, foundSource, useFoundSources, type LibraryError } from "@/lib/library";
import { installedWhere, noSource, outsideLinkNote, sameNameFetchNote, scopeLabel, shortPath, sourceLabel } from "@/lib/model";
import { Modal, ui, useUI } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Field, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Notice } from "@/components/ui/notice";

// fetchUnmanaged 把作用域里一个不是库装的技能(未纳管技能)入库:磁盘上什么都不改，文件夹从此由 app 管。
// 确认时写清代价：之后还用命令行工具更新它，app 只能报为被手动改过;提交在项目仓库里的，之后在 app 里更新会改动仓库里的文件。
// 来源链接预填 npx skills 来源记录里认出的来源，认不出时预填在 skills.sh 上找到的来源，用户可以改或清空;清空就是没有来源的技能。
// 内容和库里某个技能的某个版本一致时，技能库只给那个技能补记这一处安装。
export async function fetchUnmanaged(skill: UnmanagedSkill) {
  const s = currentSnapshot();
  const t = messages().actions;
  const place = scopeLabel(s, skill.scope);
  // 库里已有同名的技能时确认里说出来：内容不同不合并，入库后会有两个同名的。
  const twins = s.skills
    .filter((x) => x.name === skill.name)
    .map((x) => t.twin(noSource(x.source) ? messages().model.noSource : sourceLabel(s, x.source), installedWhere(s, x.id)));
  const outside = skill.link_outside && skill.link_target ? outsideLinkNote(shortPath(s, skill.link_target)) : undefined;
  const r = await ui.open<FetchUnmanagedResult>((close) => <FetchUnmanagedDialog place={place} skill={skill} twins={twins} outside={outside} close={close} />);
  if (!r) return;
  if (r.matched) ui.toast(t.matchedVersion(skill.name, r.skill.name, place));
  else if (r.skill.collection_id) ui.toast(t.fetchedTracking(r.skill.name, r.skill.source.label));
  else ui.toast(t.fetched(r.skill.name));
}

// FetchUnmanagedDialog 是入库确认：来源链接可改可清空;读来源失败时留在框里说明原因，用户可以重试、改链接或清空后入库。
function FetchUnmanagedDialog({
  place,
  skill,
  twins,
  outside,
  close,
}: {
  place: string;
  skill: UnmanagedSkill;
  twins: string[];
  outside?: string; // 指向 Agent 目录以外的符号链接时的说明
  close: (value?: FetchUnmanagedResult) => void;
}) {
  const ui = useUI();
  const t = useT();
  // 确认框开着时才在 skills.sh 上找到来源的，用户还没动过链接就补上。
  const found = foundSource(useFoundSources(), skill);
  const [edited, setLink] = useState<string>();
  const link = edited ?? skill.source_input ?? found ?? "";
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
      {error && (
        <Notice tone="bad" className="unmanaged-error whitespace-pre-line">
          {unmanagedSourceError(error, skill.name)}
        </Notice>
      )}
    </Modal>
  );
}
