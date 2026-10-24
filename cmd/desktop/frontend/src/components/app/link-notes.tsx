// 写之前关于符号链接与共用文件夹的说明(要说明哪些见 lib/consent.ts):写到链接那一处会换成独立的一份，
// 删掉或覆盖被别处链接指着的文件夹前先给那一处换上独立的一份，共用一个文件夹的几个 Agent 一起受影响。
import { LibraryService } from "@bindings/cmd/desktop";
import type { Scope, SkillID, SkillScopeInspection } from "@bindings/internal/library";
import { linkWrites, placeKey, type Detached } from "@/lib/consent";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { agentName, mergeProjectPlaces, projectName, scopeLabel, shortPath, skillName } from "@/lib/model";
import { ui } from "@/lib/ui";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";

// DetachNotice 说明 detachedBy 找出的那几处会先换上独立的一份;when 是在删、换还是搬走这个文件夹之前,
// prefix 用在只和某个选项有关时(如"选「一并卸载」时:")。
export function DetachNotice({
  items,
  when,
  prefix = "",
}: {
  items: Detached[];
  when: "delete" | "replace" | "move";
  prefix?: string;
}) {
  const s = useSnapshot();
  const t = useT();
  if (!items.length) return null;
  return (
    <Notice className="detach-linked">
      {prefix}
      {items.map(({ place, by }) => (
        <span key={placeKey(place)} className="block">
          {t.actions.detach(
            mergeProjectPlaces(s, by.map((scope) => ({ scope }))).map((x) => scopeLabel(s, x.scope)),
            skillName(s, place.skill_id),
            shortPath(s, place.folder),
            when,
          )}
        </span>
      ))}
    </Notice>
  );
}

export function LinkWriteNotes({ links, kept }: ReturnType<typeof linkWrites>) {
  const t = useT();
  return (
    <>
      {links.length > 0 && (
        <>
          <Notice className="write-linked">{t.actions.writeLinked(links.length)}</Notice>
          <LinkedPlaces places={links} />
        </>
      )}
      <DetachNotice items={kept} when="replace" />
    </>
  );
}

// confirmLinkWrites 在写这几处之前说明 linkWrites 找出的情况;都没有时不打扰用户。explained 是前一个弹窗里已经逐行说过的链接。
export async function confirmLinkWrites(
  places: SkillScopeInspection[],
  written: (p: SkillScopeInspection) => boolean,
  title: string,
  confirm: string,
  explained: (p: SkillScopeInspection) => boolean = () => false,
): Promise<boolean> {
  const { links, kept } = linkWrites(places, written);
  const unexplained = links.filter((p) => !explained(p));
  if (!unexplained.length && !kept.length) return true;
  return !!(await ui.confirm({ title, body: <LinkWriteNotes links={unexplained} kept={kept} />, confirm }));
}

// confirmLinkedRequests 是 confirmLinkWrites 的现查版：只知道要写哪几处时，先巡检一次再说明。
export async function confirmLinkedRequests(requests: { skill_id: SkillID; scope: Scope }[], title: string, confirm: string) {
  const wanted = new Set(requests.map(placeKey));
  const inspected = await ui.run(() => LibraryService.InspectSkillScopes({}), { reload: false });
  if (inspected === undefined) return false;
  return confirmLinkWrites(inspected ?? [], (p) => wanted.has(placeKey(p)), title, confirm);
}

// LinkedPlaces 列出是符号链接的各处与它们指向哪里。
export function LinkedPlaces({ places }: { places: SkillScopeInspection[] }) {
  const s = useSnapshot();
  return (
    <ListBox>
      {mergeProjectPlaces(s, places).map((p) => (
        <ListRow key={placeKey(p)} compact className="linked-place">
          <div className="min-w-0 grow">
            <div>
              {skillName(s, p.skill_id)} · {scopeLabel(s, p.scope)}
            </div>
            <div className="truncate font-mono text-[11px] text-muted-foreground" title={p.link_target}>
              {shortPath(s, p.folder)} → {shortPath(s, p.link_target!)}
            </div>
          </div>
        </ListRow>
      ))}
    </ListBox>
  );
}

// SharedFolders 列出还有别的 Agent 在读、因而保留的技能文件夹。
export function SharedFolders({ places }: { places: SkillScopeInspection[] }) {
  const s = useSnapshot();
  const t = useT();
  return (
    <ListBox>
      {places.map((p) => (
        <ListRow key={placeKey(p)}>
          <div className="min-w-0 grow">
            <div>
              {skillName(s, p.skill_id)} · {p.scope.project_id ? projectName(s, p.scope.project_id) : t.model.global}
            </div>
            <div className="truncate font-mono text-[11px] text-muted-foreground">{shortPath(s, p.folder)}</div>
          </div>
          <span className="text-[11px] text-muted-foreground">{t.actions.sharedBy((p.shared_with ?? []).map((code) => agentName(s, code)))}</span>
        </ListRow>
      ))}
    </ListBox>
  );
}
