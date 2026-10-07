// 技能市场：搜索 skills.sh,选中一项后对它做入库预览(只列该技能),进入 FetchReview 入库。
import { useState } from "react";
import { Browser } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import type { FetchPreview, SearchSkill } from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";
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
    if (!query.trim()) return;
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
      <div className="row">
        <label className="search grow" style={{ width: "auto" }}>
          <Icon name="search" />
          <input
            autoFocus
            placeholder="在 skills.sh 上搜索技能"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && search()}
          />
        </label>
        <button type="button" className="pill-btn accent" onClick={search} disabled={!query.trim() || searching}>
          {searching ? <span className="spin" /> : null}
          搜索
        </button>
      </div>
      {result &&
        (skills.length ? (
          <div className="list-box">
            {skills.map((s) => (
              <div key={s.id} className="list-row">
                <div className="grow">
                  <div className="row">
                    <button
                      type="button"
                      className="link name-link"
                      title={`在 skills.sh 查看「${s.name}」`}
                      onClick={() => void Browser.OpenURL(s.detail_url)}
                    >
                      {s.name}
                    </button>
                    {s.installs != null && <span className="tiny muted">{s.installs.toLocaleString()} 次安装</span>}
                  </div>
                  <div className="mono tiny muted">{s.preview.github?.input}</div>
                </div>
                <button
                  type="button"
                  className="pill-btn sm accent"
                  disabled={fetching !== null}
                  onClick={() => pick(s)}
                >
                  {fetching === s.id ? <span className="spin" /> : null}
                  入库
                </button>
              </div>
            ))}
          </div>
        ) : (
          <div className="notice">没有搜到技能。</div>
        ))}
      <p className="hint">搜索结果来自 skills.sh,入库前会先从 GitHub 做入库预览，可查看候选与审计信息。</p>
    </Modal>
  );
}
