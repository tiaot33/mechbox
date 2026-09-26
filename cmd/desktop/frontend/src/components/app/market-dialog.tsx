// 技能市场：搜索 skills.sh,选中一项后 ResolveSource + Fetch(只取该技能),进入 FetchReview 安装。
import { useState } from "react";
import { Browser } from "@wailsio/runtime";
import { LibraryService } from "@bindings/cmd/desktop";
import type { Fetch, SearchResult, SearchSkill } from "@bindings/internal/library";
import { Modal, useUI } from "@/lib/ui";
import { FetchReview } from "./fetch-review";
import { Icon } from "./icons";

export function MarketDialog({ onClose }: { onClose: () => void }) {
  const ui = useUI();
  const [query, setQuery] = useState("");
  const [result, setResult] = useState<SearchResult | null>(null);
  const [searching, setSearching] = useState(false);
  const [fetching, setFetching] = useState<string | null>(null);
  const [fetched, setFetched] = useState<Fetch | null>(null);

  if (fetched) return <FetchReview fetch={fetched} onClose={onClose} onBack={() => setFetched(null)} />;

  const search = async () => {
    if (!query.trim()) return;
    setSearching(true);
    const r = await ui.run(() => LibraryService.SearchSkills(query.trim()), { reload: false });
    setSearching(false);
    if (r) setResult(r);
  };

  const pick = async (skill: SearchSkill) => {
    setFetching(skill.id);
    const f = await ui.run(
      async () => {
        const spec = await LibraryService.ResolveSource(skill.source_input, {});
        return LibraryService.Fetch(spec);
      },
      { reload: false },
    );
    setFetching(null);
    if (f) setFetched(f);
  };

  const skills = result?.skills ?? [];
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
                    <b>{s.name}</b>
                    {s.installs != null && <span className="tiny muted">{s.installs.toLocaleString()} 次安装</span>}
                  </div>
                  <div className="mono tiny muted">{s.source_input}</div>
                </div>
                <button type="button" className="link" onClick={() => void Browser.OpenURL(s.detail_url)}>
                  详情
                </button>
                <button
                  type="button"
                  className="pill-btn sm accent"
                  disabled={fetching !== null}
                  onClick={() => pick(s)}
                >
                  {fetching === s.id ? <span className="spin" /> : null}
                  安装…
                </button>
              </div>
            ))}
          </div>
        ) : (
          <div className="notice">没有搜到技能。</div>
        ))}
      {(result?.diagnostics ?? []).length > 0 && (
        <details>
          <summary className="tiny muted">{result!.diagnostics!.length} 个结果无法作为 GitHub 来源安装</summary>
          <div className="list-box" style={{ marginTop: 6 }}>
            {result!.diagnostics!.map((d) => (
              <div key={d.id} className="list-row">
                <span className="mono tiny">{d.id}</span>
                <span className="grow" />
                <span className="tiny muted">{d.reason}</span>
              </div>
            ))}
          </div>
        </details>
      )}
      <p className="hint">搜索结果来自 skills.sh,安装前会从 GitHub 获取并展示候选与审计信息。</p>
    </Modal>
  );
}
