// 技能市场：搜索 skills.sh,选中一项后对它做入库预览(只列该技能),进入 FetchReview 入库。
import { useState } from "react";
import { Browser } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import type { FetchPreview, SearchSkill } from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";
import { Button } from "@/components/ui/button";
import { Hint } from "@/components/ui/field";
import { SearchInput } from "@/components/ui/input";
import { ListBox, ListRow } from "@/components/ui/list";
import { Notice } from "@/components/ui/notice";
import { FetchReview } from "./fetch-review";
import { Icon } from "./icons";

export function MarketDialog({ onClose }: { onClose: () => void }) {
  const ui = useUI();
  const [query, setQuery] = useState("");
  const [result, setResult] = useState<SearchSkill[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [fetching, setFetching] = useState<string | null>(null);
  const [preview, setPreview] = useState<FetchPreview | null>(null);

  if (preview) return <FetchReview preview={preview} audits onClose={onClose} onBack={() => setPreview(null)} />;

  const search = async () => {
    if (!query.trim() || searching) return;
    setSearching(true);
    const r = await ui.run(() => LibraryService.SearchSkills(query.trim()), { reload: false });
    setSearching(false);
    if (r !== undefined) setResult(r ?? []);
  };

  const pick = async (skill: SearchSkill) => {
    setFetching(skill.id);
    const p = await ui.run(() => LibraryService.PreviewFetch(skill.preview), { reload: false });
    setFetching(null);
    if (p) setPreview(p);
  };

  const skills = result ?? [];
  return (
    <Modal title="技能市场" wide onClose={onClose}>
      <div className="flex min-w-0 items-center gap-2">
        <SearchInput
          className="grow"
          icon={<Icon name="search" className="size-[13px] text-muted-foreground" />}
          autoFocus
          placeholder="在 skills.sh 上搜索技能"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && search()}
        />
        <Button variant="primary" onClick={search} disabled={!query.trim()} loading={searching}>
          搜索
        </Button>
      </div>
      {result &&
        (skills.length ? (
          <ListBox>
            {skills.map((s) => (
              <ListRow key={s.id}>
                <div className="min-w-0 grow">
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      className="text-[13px] font-semibold text-foreground hover:text-primary"
                      title={`在 skills.sh 查看「${s.name}」`}
                      onClick={() => void Browser.OpenURL(s.detail_url)}
                    >
                      {s.name}
                    </button>
                    {s.installs != null && <span className="text-[11px] text-muted-foreground">{s.installs.toLocaleString()} 次安装</span>}
                  </div>
                  <div className="truncate font-mono text-[11px] text-muted-foreground">{s.preview.github?.input}</div>
                </div>
                <Button size="sm" variant="primary" disabled={fetching !== null && fetching !== s.id} loading={fetching === s.id} onClick={() => pick(s)}>
                  入库
                </Button>
              </ListRow>
            ))}
          </ListBox>
        ) : (
          <Notice>没有搜到技能。</Notice>
        ))}
      <Hint>搜索结果来自 skills.sh,入库前会先从 GitHub 做入库预览，可查看候选与审计信息。</Hint>
    </Modal>
  );
}
