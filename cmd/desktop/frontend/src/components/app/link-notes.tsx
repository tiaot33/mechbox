// 写之前关于符号链接与共用文件夹的说明(要说明哪些见 lib/consent.ts):写到链接那一处会换成独立的一份，
// 删掉或覆盖被别处链接指着的文件夹前先给那一处换上独立的一份，共用一个文件夹的几个 Agent 一起受影响;
// 删掉被手动改过的文件夹前说出是哪几处，修改会跟着丢掉;确认之后才被改过的见 delete-check.tsx。
import { LibraryService } from "@bindings/cmd/desktop";
import type { Scope, SkillID, SkillScopeInspection } from "@bindings/internal/library";
import { copiesLeft, detachedBy, linkWrites, lostEdits, staleLinksOf, type Detached } from "@/lib/consent";
import { placeKey } from "@/lib/installs";
import { useT } from "@/lib/i18n";
import { useSnapshot } from "@/lib/library";
import { agentName, projectName, skillName } from "@/lib/scope";
import { eachPlace, placeNames, scopeLabel } from "@/lib/places";
import { shortPath, wslTitle } from "@/lib/paths";
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
            placeNames(s, by),
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
  const inspected = await ui.read(() => LibraryService.InspectSkillScopes({}));
  if (inspected === undefined) return false;
  return confirmLinkWrites(inspected ?? [], (p) => wanted.has(placeKey(p)), title, confirm);
}

// LinkedPlaces 列出是符号链接的各处与它们指向哪里。
export function LinkedPlaces({ places }: { places: SkillScopeInspection[] }) {
  const s = useSnapshot();
  return (
    <ListBox>
      {eachPlace(s, places).map((p) => (
        <ListRow key={placeKey(p)} compact className="linked-place">
          <div className="min-w-0 grow">
            <div>
              {skillName(s, p.skill_id)} · {scopeLabel(s, p.scope)}
            </div>
            <div className="truncate font-mono text-caption text-muted-foreground" title={p.link_target}>
              {shortPath(s, p.folder)} → {shortPath(s, p.link_target!)}
            </div>
          </div>
        </ListRow>
      ))}
    </ListBox>
  );
}

// DeleteNotes 是删掉 deleted 这几处的文件夹之前的说明：被手动改过的几处修改会丢掉，别处链接指着的先换上独立的一份，
// 以及会一并删掉的旧链接、删掉后 Agent 照样读得到的一样的那几份(见 StaleNotes)。
// prefix 用在只和某个选项有关时(如"选「一并卸载」时:")。
export function DeleteNotes({ deleted, prefix = "" }: { deleted: SkillScopeInspection[]; prefix?: string }) {
  return (
    <>
      <LostEdits places={lostEdits(deleted)} prefix={prefix} />
      <DetachNotice items={detachedBy(deleted)} when="delete" prefix={prefix} />
      <StaleNotes deleted={deleted} prefix={prefix} />
    </>
  );
}

// StaleNotes 是删掉 deleted 这几处的文件夹时另外要说的两件事：技能库一并删掉的旧链接(读这一处的 Agent 在别的技能目录里指向它的符号链接),
// 以及读这一处的 Agent 在别的技能目录里还有一份一样的，删掉后照样读得到它(stillInstalled 时还会照样显示已装)。都没有时不显示。
export function StaleNotes({
  deleted,
  prefix = "",
  stillInstalled = true,
  copies: withCopies = true,
}: {
  deleted: SkillScopeInspection[];
  prefix?: string;
  stillInstalled?: boolean;
  copies?: boolean; // 移除 Agent 时那个 Agent 不在了，不说它还读得到哪份
}) {
  const s = useSnapshot();
  const t = useT().actions;
  const links = staleLinksOf(deleted);
  const copies = withCopies ? copiesLeft(deleted) : [];
  return (
    <>
      {links.length > 0 && (
        <Notice className="stale-links">
          {prefix}
          <span className="block">{t.staleLinks(links.length)}</span>
          {links.map((link) => (
            <span key={link} className="block truncate font-mono text-caption" title={wslTitle(link)}>
              {shortPath(s, link)}
            </span>
          ))}
        </Notice>
      )}
      {copies.length > 0 && (
        <Notice className="copies-left">
          {prefix}
          {copies.map(({ place, copies }) => (
            <span key={placeKey(place)} className="block">
              {t.copiesLeft(skillName(s, place.skill_id), copies.map((c) => shortPath(s, c)), shortPath(s, place.folder), stillInstalled)}
            </span>
          ))}
        </Notice>
      )}
    </>
  );
}

// LostEdits 列出删掉文件夹时修改会跟着丢掉的几处(lostEdits 找出的);没有时不显示。
function LostEdits({ places, prefix }: { places: SkillScopeInspection[]; prefix: string }) {
  const s = useSnapshot();
  const t = useT();
  if (!places.length) return null;
  return (
    <>
      <Notice alert>
        {prefix}
        <span className="block">{t.actions.losesEdits(places.length)}</span>
      </Notice>
      <ListBox>
        {places.map((p) => (
          <ListRow key={placeKey(p)} compact className="lost-edits">
            <div className="min-w-0 grow">
              <div>
                {skillName(s, p.skill_id)} · {scopeLabel(s, p.scope)}
              </div>
              <div className="truncate font-mono text-caption text-muted-foreground" title={wslTitle(p.folder)}>
                {shortPath(s, p.folder)}
              </div>
            </div>
          </ListRow>
        ))}
      </ListBox>
    </>
  );
}

// SharedFolders 逐个列出几处技能文件夹(技能 · 作用域、文件夹路径);还有别的 Agent 在读的写出是哪几个在用。
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
            <div className="truncate font-mono text-caption text-muted-foreground" title={wslTitle(p.folder)}>
              {shortPath(s, p.folder)}
            </div>
          </div>
          {!!p.shared_with?.length && (
            <span className="text-caption text-muted-foreground">{t.actions.sharedBy(p.shared_with.map((code) => agentName(s, code)))}</span>
          )}
        </ListRow>
      ))}
    </ListBox>
  );
}
